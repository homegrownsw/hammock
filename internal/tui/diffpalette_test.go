package tui

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/daemon"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// pick chooses the row titled title in the palette that's open, as Enter
// on it would.
func pick(t *testing.T, a *App, title string) {
	t.Helper()
	p, ok := a.overlay.(*palette)
	if !ok {
		t.Fatalf("no palette open: %T", a.overlay)
	}
	p.items = p.filtered(a)
	for i, it := range p.items {
		if it.title == title {
			p.sel = i
			p.choose(a)
			return
		}
	}
	t.Fatalf("no %q in %v", title, paletteTitles(p.items))
}

func paletteTitles(items []paletteItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.title)
	}
	return out
}

// New Diff Tab… and Split Right with Diff… go on to a second list, of what
// to compare; Esc goes back to the palette, and again closes it.
func TestDiffCommandsListWhatToCompare(t *testing.T) {
	a, add := paletteApp()
	a.groups = []*pb.Group{{Id: "g", TabIds: []string{"t"}}}
	add("t")
	a.tabs["t"].GroupId = "g"
	a.selTab = "t"

	ids := func() map[string]bool {
		out := map[string]bool{}
		for _, it := range a.paletteItems() {
			out[strings.TrimPrefix(it.id, "action:")] = true
		}
		return out
	}
	if have := ids(); !have["new-diff-tab"] || have["split-diff"] {
		t.Errorf("no pane focused: new-diff-tab %v, split-diff %v", have["new-diff-tab"], have["split-diff"])
	}
	a.focus = "t-p"
	if !ids()["split-diff"] {
		t.Error("no split-diff with a pane focused")
	}

	for _, cmd := range []string{"New Diff Tab…", "Split Right with Diff…"} {
		a.openPalette()
		pick(t, a, cmd)
		p, ok := a.overlay.(*palette)
		if !ok || p.list == nil {
			t.Fatalf("%s: no list (%T)", cmd, a.overlay)
		}
		want := "Uncommitted,This Branch,Last Commit,Choose Commits…"
		if got := strings.Join(paletteTitles(p.filtered(a)), ","); got != want {
			t.Errorf("%s: %s, want %s", cmd, got, want)
		}
		// Typing filters it, and offers no web search.
		p.key(a, keysOf([]byte("comm")))
		got := paletteTitles(p.filtered(a))
		slices.Sort(got)
		if strings.Join(got, ",") != "Choose Commits…,Last Commit,Uncommitted" {
			t.Errorf("%s, filtered: %s", cmd, got)
		}
		p.key(a, keysOf([]byte("\x1b")))
		if a.overlay != p || p.list != nil || p.query != "" {
			t.Fatalf("%s: esc didn't go back to the palette (%T, list %v, query %q)", cmd, a.overlay, p.list, p.query)
		}
		p.key(a, keysOf([]byte("\x1b")))
		if a.overlay != nil {
			t.Fatalf("%s: esc in the palette didn't close it", cmd)
		}
	}
}

// Choose Commits… picks the oldest commit to show, then the newest: the
// same one is that commit alone; a later one, the run of them.
func TestChooseCommitsAgainstDaemon(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Sam", "-c", "user.email=you@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	var shas []string // oldest first
	for _, s := range []string{"one", "two", "three"} {
		os.WriteFile(filepath.Join(repo, "notes.txt"), []byte(s+"\n"), 0o644)
		git("add", ".")
		git("commit", "-q", "-m", "Write "+s)
		shas = append(shas, git("rev-parse", "HEAD"))
	}

	dir, err := os.MkdirTemp("/tmp", "hmk-tui") // short: it holds a socket
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d, err := daemon.New(daemon.Config{Dir: dir, HolderPath: "/nonexistent", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(d.Shutdown)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if c, err := net.Dial("unix", daemon.SocketPath(dir)); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no daemon")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, daemon.SocketPath(dir), "tui", t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	call := func(m *pb.ClientMessage) *pb.ServerMessage {
		t.Helper()
		r, err := c.Call(ctx, m)
		if err != nil {
			t.Fatalf("%T: %v", m.Msg, err)
		}
		return r
	}
	a := &App{c: newLink(c), anim: newSprings(), th: newTheme(RGB{0, 0, 0}, RGB{255, 255, 255}), scr: NewScreen(80, 24),
		tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{}, terms: map[string]*paneTerm{}, sizes: map[string][2]int{},
		editors: map[string]*editorView{}, paneRects: map[string]rect{}, actions: make(chan func(), 64)}
	a.apply(call(&pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState())
	gid := client.CreatedID(call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: gid, Folder: repo}}})
	tid := client.CreatedID(call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: gid, Pane: &pb.PaneSpec{Editor: &pb.EditorOpen{Path: "notes.txt"}},
	}}}))
	pump := func(what string, done func() bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !done() {
			select {
			case ev := <-c.Events:
				a.handle(ev)
			case f := <-a.actions:
				f()
			case <-deadline:
				t.Fatalf("timed out waiting for %s (toast %q)", what, a.toast)
			}
		}
	}
	pump("the tab", func() bool { return a.tabs[tid] != nil && a.panes[a.tabs[tid].FocusedPaneId] != nil })
	a.selTab, a.focus = tid, a.tabs[tid].FocusedPaneId
	diffIn := func(tab string) *pb.DiffInfo {
		if tb := a.tabs[tab]; tb != nil {
			for _, id := range tabIDs(tb) {
				if p := a.panes[id]; p != nil && p.Kind == pb.PaneKind_PANE_KIND_DIFF {
					return p.GetDiff()
				}
			}
		}
		return nil
	}
	listed := func() bool {
		p, _ := a.overlay.(*palette)
		return p != nil && p.list != nil && len(p.list.items) > 0
	}

	// Split Right with Diff… ▸ Choose Commits…: the oldest of the three,
	// then the newest: all three, beside the editor.
	a.openPalette()
	pick(t, a, "Split Right with Diff…")
	pick(t, a, "Choose Commits…")
	pump("the history", listed)
	if got := strings.Join(paletteTitles(a.overlay.(*palette).list.items), ","); got != "Write three,Write two,Write one" {
		t.Fatalf("commits %s", got)
	}
	pick(t, a, "Write one")
	p := a.overlay.(*palette)
	if got := strings.Join(paletteTitles(p.list.items), ","); got != "Write one,Write two,Write three" {
		t.Fatalf("through: %s", got)
	}
	// Esc goes back to the history, and on to what to compare.
	p.key(a, keysOf([]byte("\x1b")))
	if got := strings.Join(paletteTitles(p.list.items), ","); got != "Write three,Write two,Write one" {
		t.Fatalf("back: %s", got)
	}
	pick(t, a, "Write one")
	pick(t, a, "Write three")
	pump("the split", func() bool { return diffIn(tid) != nil })
	if src := diffIn(tid).GetSource(); src.GetKind() != pb.DiffSource_KIND_RANGE || !strings.HasPrefix(src.GetBase(), shas[0]) || src.GetRev() != shas[2] {
		t.Errorf("split: %v, want %s^..%s", src, shas[0][:7], shas[2][:7])
	}

	// New Diff Tab… ▸ Choose Commits…: one commit, chosen twice, is that
	// commit alone, in a tab of its own.
	a.selTab, a.focus = tid, a.tabs[tid].FocusedPaneId
	a.openPalette()
	pick(t, a, "New Diff Tab…")
	pick(t, a, "Choose Commits…")
	pump("the history", listed)
	pick(t, a, "Write two")
	pick(t, a, "Write two")
	pump("the tab", func() bool { return a.selTab != tid && diffIn(a.selTab) != nil })
	if src := diffIn(a.selTab).GetSource(); src.GetKind() != pb.DiffSource_KIND_COMMIT || src.GetRev() != shas[1] {
		t.Errorf("tab: %v, want %s", src, shas[1][:7])
	}

	// And what's uncommitted.
	os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("four\n"), 0o644)
	before := a.selTab
	a.openPalette()
	pick(t, a, "New Diff Tab…")
	pick(t, a, "Uncommitted")
	pump("the tab", func() bool { return a.selTab != before && diffIn(a.selTab) != nil })
	if k := diffIn(a.selTab).GetSource().GetKind(); k != pb.DiffSource_KIND_UNCOMMITTED {
		t.Errorf("uncommitted: %v", k)
	}
}
