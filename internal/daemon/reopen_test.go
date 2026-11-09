package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestReopenTab(t *testing.T) {
	typed := map[string]string{}
	var tmu sync.Mutex
	oldType := typeCommand
	typeCommand = func(p *pane, cmd string) { tmu.Lock(); typed[p.id] = cmd; tmu.Unlock() }
	t.Cleanup(func() { typeCommand = oldType })
	// A Claude session with a transcript (one without is started afresh).
	roots := t.TempDir()
	addAgentRoots(t, "claude", roots)
	file := filepath.Join(roots, "abc-123.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	sid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	cwd := t.TempDir()
	first := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Name: "one", Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}, Cwd: cwd}}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}})
	d.mu.Lock()
	a := d.tabs[first].FocusedPaneId
	d.mu.Unlock()
	b := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: a, Direction: pb.Split_DIRECTION_VERTICAL, Spec: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	// Focus back on the first pane: it should have focus when reopened.
	d.mu.Lock()
	d.tabs[first].FocusedPaneId = a
	// An agent's session in the second.
	d.panes[b].mu.Lock()
	d.panes[b].info.Agent = &pb.Agent{Harness: "claude", SessionId: "abc-123", TranscriptPath: file}
	d.panes[b].mu.Unlock()
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}}); err == nil {
		t.Fatal("reopened with nothing closed")
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: first}}})
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}}))

	d.mu.Lock()
	tab := d.tabs[tid]
	g := d.groups[sid]
	ids := layoutPanes(tab.Layout)
	if tab.Name != "one" || slices.Index(g.TabIds, tid) != 0 {
		t.Errorf("reopened as %q at %d", tab.Name, slices.Index(g.TabIds, tid))
	}
	if len(ids) != 2 || tab.Layout.GetSplit().GetDirection() != pb.Split_DIRECTION_VERTICAL || tab.FocusedPaneId != ids[0] {
		t.Errorf("layout %v, focus %s", tab.Layout, tab.FocusedPaneId)
	}
	p0, p1 := d.panes[ids[0]].snapshotInfo(), d.panes[ids[1]].snapshotInfo()
	d.mu.Unlock()
	if p0.Cwd != cwd || !slices.Equal(p0.Command, []string{"/bin/cat"}) {
		t.Errorf("first pane %q %v", p0.Cwd, p0.Command)
	}
	if p1.Agent.GetSessionId() != "abc-123" || p1.Agent.GetHarness() != "claude" || p1.Command[0] != loginShell() {
		t.Errorf("agent pane %v %v", p1.Agent, p1.Command)
	}
	tmu.Lock()
	if got := typed[ids[1]]; got != "claude --resume abc-123" {
		t.Errorf("typed %q", got)
	}
	tmu.Unlock()
	if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}}); err == nil {
		t.Fatal("reopened the same tab twice")
	}

	// Closing a tab's last pane closes the tab: that one comes back too.
	d.mu.Lock()
	last := layoutPanes(d.tabs[tid].Layout)
	d.mu.Unlock()
	for _, id := range last {
		call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: id}}})
	}
	again := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}}))
	d.mu.Lock()
	n := len(layoutPanes(d.tabs[again].Layout))
	d.mu.Unlock()
	if n != 1 {
		t.Errorf("reopened after closing its panes: %d panes", n)
	}
}

func TestReopenPane(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	reopen := func() string {
		return client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}}))
	}
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	sid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: sid, Pane: cat}}}))
	d.mu.Lock()
	a := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	b := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: a, Direction: pb.Split_DIRECTION_HORIZONTAL, Spec: cat}}}))
	cc := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: b, Direction: pb.Split_DIRECTION_VERTICAL, Spec: cat}}}))
	var shape func(*pb.Layout) string
	shape = func(l *pb.Layout) string {
		if l.GetPaneId() != "" {
			return "p"
		}
		s := "(" + l.GetSplit().GetDirection().String()[10:11]
		for _, ch := range l.GetSplit().GetChildren() {
			s += " " + shape(ch)
		}
		return s + ")"
	}
	d.mu.Lock()
	before := shape(d.tabs[tid].Layout)
	d.mu.Unlock()

	// Back exactly where it was: the middle of the tab.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: b}}})
	if got := reopen(); got != tid {
		t.Fatalf("reopened in %s, not its tab %s", got, tid)
	}
	d.mu.Lock()
	ids := layoutPanes(d.tabs[tid].Layout)
	after, focus := shape(d.tabs[tid].Layout), d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	if after != before || len(ids) != 3 || ids[0] != a || ids[2] != cc || focus != ids[1] {
		t.Errorf("layout %s (was %s), panes %v, focus %s", after, before, ids, focus)
	}

	// Its tab gone since: a tab of its own.
	nb := ids[1]
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: nb}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tid}}})
	whole := reopen() // the tab (closed last) first
	d.mu.Lock()
	n := len(layoutPanes(d.tabs[whole].Layout))
	d.mu.Unlock()
	if n != 2 {
		t.Errorf("reopened tab has %d panes", n)
	}
	own := reopen()
	d.mu.Lock()
	n = len(layoutPanes(d.tabs[own].Layout))
	d.mu.Unlock()
	if own == whole || n != 1 {
		t.Errorf("pane reopened in %s (tab %s) with %d panes", own, whole, n)
	}
}

// A session nothing was sent in has no file: the agent starts afresh.
func TestReopenUnwrittenSession(t *testing.T) {
	var typed string
	oldType := typeCommand
	typeCommand = func(p *pane, cmd string) { typed = cmd }
	t.Cleanup(func() { typeCommand = oldType })
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	spec, resume := reopenSpec(&pb.Pane{Kind: pb.PaneKind_PANE_KIND_TERMINAL, Cwd: t.TempDir(),
		Agent: &pb.Agent{Harness: "claude", SessionId: "never-written", State: pb.AgentState_AGENT_STATE_IDLE}})
	if spec == nil || resume == nil {
		t.Fatal("no agent to start")
	}
	d := &Daemon{panes: map[string]*pane{}}
	p := newPane(d, &pb.Pane{Id: "pn_test"})
	resume(d, p)
	if typed != "claude" || p.info.Agent.SessionId != "" {
		t.Errorf("typed %q, session %q", typed, p.info.Agent.SessionId)
	}
	// An agent that had quit leaves a plain shell.
	if _, r := reopenSpec(&pb.Pane{Kind: pb.PaneKind_PANE_KIND_TERMINAL, Agent: &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_EXITED}}); r != nil {
		t.Error("restarted an agent that had quit")
	}
}

// A session not written yet shows empty in the agent view, not the newest
// other session in its folder.
func TestAgentViewUnwrittenSession(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	cwd := t.TempDir()
	other := filepath.Join(cfg, "projects", strings.NewReplacer("/", "-", ".", "-").Replace(cwd), "other.jsonl")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{panes: map[string]*pane{}}
	for _, a := range []*pb.Agent{
		{Harness: "claude", SessionId: "new-one", State: pb.AgentState_AGENT_STATE_IDLE},
		{Harness: "claude", State: pb.AgentState_AGENT_STATE_STARTING, Heuristic: true},
	} {
		w := &agentWatch{p: newPane(d, &pb.Pane{Id: "pn_test", Cwd: cwd, Agent: a})}
		if p, err := w.mainPath(); p != "" || err != nil {
			t.Errorf("%v: %q, %v", a, p, err)
		}
	}
	// Found by folder still, for an agent Hammock didn't start and hasn't heard from.
	w := &agentWatch{p: newPane(d, &pb.Pane{Id: "pn_test", Cwd: cwd, Agent: &pb.Agent{Harness: "claude", Heuristic: true, State: pb.AgentState_AGENT_STATE_IDLE}})}
	if p, _ := w.mainPath(); p == "" {
		t.Error("the folder lookup didn't find its session")
	}
	// But not a session another pane's agent has: a new agent, idle before
	// its first hook, showed that one's conversation.
	d.panes["pn_other"] = newPane(d, &pb.Pane{Id: "pn_other", Cwd: cwd, Agent: &pb.Agent{Harness: "claude", SessionId: "other"}})
	if p, err := w.mainPath(); p != "" || err != nil {
		t.Errorf("took another pane's session: %q, %v", p, err)
	}
}

// Watching a session with no file yet shows it empty (it crashed the
// daemon: no parser for an unchanged empty path).
func TestWatchUnwrittenSession(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	d := &Daemon{panes: map[string]*pane{}}
	w := &agentWatch{p: newPane(d, &pb.Pane{Id: "pn_test", Cwd: t.TempDir(), Agent: &pb.Agent{Harness: "claude", SessionId: "new-one"}})}
	u := w.refresh()
	if u == nil || u.Error != "" || len(u.Blocks) != 0 {
		t.Fatalf("update %v", u)
	}
}
