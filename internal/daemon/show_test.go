package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// browsersIn is the browser panes in tab, by id.
func browsersIn(d *Daemon, tab string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ids []string
	for id, p := range d.panes {
		if p.isBrowser() && p.tabID() == tab {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func show(t *testing.T, c *client.Client, m *pb.ShowOutput) string {
	t.Helper()
	return client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ShowOutput{ShowOutput: m}}))
}

// hmk show / show_output from a pane reuses the browser pane it showed in
// before: across its agent restarting (a new process, the same pane) and
// the daemon restarting; a different pane's show of the same page finds the
// pane already on it; a different page doesn't take over another pane's;
// a closed one is replaced; --pane shows it in the pane named.
func TestShowReuse(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	ui := dial(t, dir)
	work := filepath.Join(t.TempDir(), "Automated Briefings") // a space: file URLs say %20
	os.MkdirAll(work, 0o700)
	brief, other := filepath.Join(work, "latest.html"), filepath.Join(work, "other.html")
	os.WriteFile(brief, []byte("<p>brief"), 0o600)
	os.WriteFile(other, []byte("<p>other"), 0o600)

	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Assistant"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	run := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()

	// The run's agent shows the briefing (show_output: no pane named).
	agent := dialAs(t, dir, &caller{pane: run, agent: true}, "cli")
	first := show(t, agent, &pb.ShowOutput{Target: brief})
	if got := browsersIn(d, tab); !slices.Equal(got, []string{first}) {
		t.Fatalf("first show: %v", got)
	}
	agent.Close()

	// Resumed: a new process in the same pane. Same page, same pane.
	agent = dialAs(t, dir, &caller{pane: run, agent: true}, "cli")
	if id := show(t, agent, &pb.ShowOutput{Target: brief}); id != first {
		t.Fatalf("after the agent restarted: %s, want %s", id, first)
	}
	agent.Close()

	// The daemon restarts too (it had lost what it showed where).
	ui.Close()
	d.Shutdown()
	d = startDaemon(t, dir)
	defer func() { d.Shutdown() }()
	ui = dial(t, dir)
	agent = dialAs(t, dir, &caller{pane: run, agent: true}, "cli")
	if id := show(t, agent, &pb.ShowOutput{Target: brief}); id != first {
		t.Fatalf("after the daemon restarted: %s, want %s", id, first)
	}
	// The CLI from that pane (hmk show: its pane named): the same.
	if id := show(t, ui, &pb.ShowOutput{Target: brief, PaneId: run}); id != first {
		t.Fatalf("hmk show: %s, want %s", id, first)
	}
	// Another page from the same pane replaces what it showed.
	if id := show(t, agent, &pb.ShowOutput{Target: other}); id != first {
		t.Fatalf("another page from the same pane: %s, want %s", id, first)
	}
	if got := browsersIn(d, tab); len(got) != 1 {
		t.Fatalf("browser panes: %v", got)
	}

	// Another pane in the tab: a page no pane's on opens a new pane (it
	// doesn't take over the run's, on another page).
	second := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: run, Spec: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	fresh := show(t, ui, &pb.ShowOutput{Target: "https://example.com/x", PaneId: second})
	if fresh == first || len(browsersIn(d, tab)) != 2 {
		t.Fatalf("a page no pane's on went to %s", fresh)
	}
	// A third pane's show of a page a pane's already on goes there (an
	// address with a space, the show's with %20).
	third := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: run, Spec: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	page := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: run, Spec: &pb.PaneSpec{Url: "file://" + brief}}}}))
	if id := show(t, ui, &pb.ShowOutput{Target: brief, PaneId: third}); id != page {
		t.Fatalf("a pane already on it (space vs %%20): %s, want %s", id, page)
	}
	// second's own pane now; the run's is still the run's.
	if id := show(t, ui, &pb.ShowOutput{Target: "https://example.com/y", PaneId: second}); id != fresh {
		t.Fatalf("second's repeat: %s, want %s", id, fresh)
	}
	if id := show(t, agent, &pb.ShowOutput{Target: brief}); id != first {
		t.Fatalf("the run's after second showed: %s, want %s", id, first)
	}

	// --pane: in the browser pane named, nothing new; not a terminal pane.
	n := len(browsersIn(d, tab))
	if id := show(t, ui, &pb.ShowOutput{Target: brief, PaneId: run, IntoPaneId: fresh}); id != fresh {
		t.Fatalf("--pane %s: %s", fresh, id)
	}
	if id := show(t, ui, &pb.ShowOutput{Target: brief, IntoPaneId: page}); id != page {
		t.Fatalf("--pane with no pane of its own: %s", id)
	}
	if got := len(browsersIn(d, tab)); got != n {
		t.Fatalf("--pane opened a pane: %d, then %d", n, got)
	}
	if _, err := try(ui, &pb.ClientMessage{Msg: &pb.ClientMessage_ShowOutput{ShowOutput: &pb.ShowOutput{Target: brief, IntoPaneId: second}}}); err == nil {
		t.Fatal("--pane of a terminal pane")
	}
	// It didn't change where the run's own shows go.
	if id := show(t, agent, &pb.ShowOutput{Target: other}); id != first {
		t.Fatalf("the run's after --pane: %s, want %s", id, first)
	}
	// An agent may show in a pane it opened.
	if id := show(t, agent, &pb.ShowOutput{Target: brief, IntoPaneId: first}); id != first {
		t.Fatalf("agent --pane: %s", id)
	}

	// The run's pane is closed: the next show (of a page no other pane's
	// on) opens a new one, which the one after reuses.
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: first}}})
	again := show(t, agent, &pb.ShowOutput{Target: other})
	if again == first || again == page || again == fresh {
		t.Fatalf("after its pane closed: %s", again)
	}
	if id := show(t, agent, &pb.ShowOutput{Target: other}); id != again {
		t.Fatalf("then again: %s, want %s", id, again)
	}
}

func TestPageKey(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"file:///Users/a/Automated%20Briefings/latest.html", "file:///Users/a/Automated Briefings/latest.html", true},
		{"file:///Users/a/x.html", "file://localhost/Users/a/x.html", true},
		{"file:///Users/a/x.html#top", "file:///Users/a/x.html", true},
		{"HTTPS://Example.com", "https://example.com/", true},
		{"https://example.com/a?b=1", "https://example.com/a?b=2", false},
		{"file:///Users/a/x.html", "file:///Users/a/y.html", false},
	} {
		if got := pageKey(c.a) == pageKey(c.b); got != c.same {
			t.Errorf("%s vs %s: same %v (%q, %q)", c.a, c.b, got, pageKey(c.a), pageKey(c.b))
		}
	}
}
