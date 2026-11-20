package tui

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/daemon"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// What closing would stop, as the dialog says it: an agent at work, by
// name; several, counted; nothing for one at rest, exited, or with
// warnings turned off.
func TestBusyWarning(t *testing.T) {
	a, tab := paletteApp()
	working := &pb.Pane{Agent: &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_WORKING}}
	tab("one", working)
	tab("asking", &pb.Pane{Agent: &pb.Agent{Harness: "codex", State: pb.AgentState_AGENT_STATE_NEEDS_INPUT}})
	tab("command", &pb.Pane{Agent: &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_IDLE, RunningCommand: true}})
	tab("subagents", &pb.Pane{Agent: &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_IDLE, Subagents: 2}})
	tab("idle", &pb.Pane{Agent: &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_IDLE}})
	tab("exited", &pb.Pane{Exited: true, Agent: &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_WORKING}})
	tab("shell", &pb.Pane{Title: "zsh"})
	tab("two", &pb.Pane{Agent: &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_WORKING}},
		&pb.Pane{Agent: &pb.Agent{Harness: "codex", State: pb.AgentState_AGENT_STATE_STARTING}}, &pb.Pane{})

	for _, c := range []struct{ tab, title, line string }{
		{"one", "Claude is still working", "Closing stops it."},
		{"asking", "Codex is waiting for you", "Closing stops it."},
		{"command", "Claude is running a command", "Closing stops it."},
		{"subagents", "Claude has 2 subagents still running", "Closing stops it."},
		{"idle", "", ""},
		{"exited", "", ""},
		{"shell", "", ""},
		{"two", "2 agents are still at work", "Closing stops them."},
	} {
		title, line := a.busyWarning(tabIDs(a.tabs[c.tab]))
		if title != c.title || line != c.line {
			t.Errorf("%s: %q, %q; want %q, %q", c.tab, title, line, c.title, c.line)
		}
	}

	warnBusy = false
	defer func() { warnBusy = true }()
	if title, _ := a.busyWarning([]string{working.Id}); title != "" {
		t.Errorf("warnings off: %q", title)
	}
}

// Closing a tab, a pane or a Group with an agent at work asks first, and
// Cancel leaves it be.
func TestCloseAsksBeforeStoppingAnAgent(t *testing.T) {
	a, tab := paletteApp()
	tab("t", &pb.Pane{Agent: &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_WORKING}}, &pb.Pane{Title: "zsh"})
	a.tabs["t"].GroupId = "g"
	a.groups = []*pb.Group{{Id: "g", Name: "Work", TabIds: []string{"t"}}}
	agent := tabIDs(a.tabs["t"])[0]

	for _, c := range []struct {
		name  string
		close func()
		ok    string
	}{
		{"tab", func() { a.closeTab("t") }, "Close Tab"},
		{"pane", func() { a.closePane(agent) }, "Close Pane"},
		{"group", func() { a.closeGroup("g") }, "Close Group"},
	} {
		a.overlay = nil
		c.close()
		d, ok := a.overlay.(*dialog)
		if !ok {
			t.Fatalf("%s: no dialog (%T)", c.name, a.overlay)
		}
		if d.title != "Claude is still working" || d.okLabel != c.ok || !d.confirm {
			t.Errorf("%s: %q %q", c.name, d.title, d.okLabel)
		}
		d.finish(a, false) // Cancel: nothing's sent (there's no daemon to send it to)
		if a.overlay != nil {
			t.Errorf("%s: still open", c.name)
		}
	}

	// A Group of several tabs always asks; it says which agent too.
	tab("u", &pb.Pane{Title: "zsh"})
	a.groups[0].TabIds = []string{"t", "u"}
	a.closeGroup("g")
	d := a.overlay.(*dialog)
	if len(d.lines) != 2 || d.lines[1] != "Claude is still working." {
		t.Errorf("Group lines: %q", d.lines)
	}
}

// Against a real daemon: an agent at work asks first, and once closed, the
// tab (or pane) is named in a flash that says how to reopen it.
func TestCloseFlashesHowToReopen(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "hmk-tui") // short: it holds a socket
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	folder := t.TempDir()
	for _, f := range []string{"notes.txt", "todo.txt"} {
		os.WriteFile(filepath.Join(folder, f), []byte("one\n"), 0o644)
	}
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
	a := &App{c: c, anim: newSprings(), th: newTheme(RGB{0, 0, 0}, RGB{255, 255, 255}), scr: NewScreen(80, 24),
		tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{}, terms: map[string]*paneTerm{}, sizes: map[string][2]int{},
		editors: map[string]*editorView{}, paneRects: map[string]rect{}, actions: make(chan func(), 64)}
	a.apply(call(&pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState())
	gid := client.CreatedID(call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: gid, Folder: folder}}})
	newTab := func(file string) string {
		return client.CreatedID(call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
			GroupId: gid, Name: file + " tab", Pane: &pb.PaneSpec{Editor: &pb.EditorOpen{Path: file}},
		}}}))
	}
	one, two := newTab("notes.txt"), newTab("todo.txt")
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
	pump("the tabs", func() bool { return a.tabs[one] != nil && a.tabs[two] != nil })

	// Nothing at work: it closes at once, and says so.
	a.closeTab(one)
	if a.overlay != nil {
		t.Fatalf("asked: %T", a.overlay)
	}
	pump("the flash", func() bool { return a.tabs[one] == nil && a.toast != "" })
	if want := "Closed “notes.txt tab” · ⌃B T reopens"; a.toast != want {
		t.Errorf("toast %q, want %q", a.toast, want)
	}

	// An agent at work (as the daemon would say): asked, then closed.
	a.toast = ""
	pid := a.tabs[two].FocusedPaneId
	a.panes[pid].Agent = &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_WORKING}
	a.closePane(pid)
	dl, ok := a.overlay.(*dialog)
	if !ok || dl.title != "Claude is still working" {
		t.Fatalf("not asked: %#v", a.overlay)
	}
	dl.finish(a, true)
	pump("the pane's flash", func() bool { return a.toast != "" })
	if want := "Closed “todo.txt” · ⌃B T reopens"; a.toast != want {
		t.Errorf("toast %q, want %q", a.toast, want)
	}
}
