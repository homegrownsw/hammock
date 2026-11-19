package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// dialSandboxed connects a client the daemon takes to be cl, in a sandbox.
func dialSandboxed(t *testing.T, dir string, cl *caller) *client.Client {
	t.Helper()
	sandboxedHook = func(localPeer) bool { return true }
	defer func() { sandboxedHook = nil }()
	return dialAs(t, dir, cl, "cli")
}

// What a sandboxed caller's message needs your grant for: what runs or
// types something, and nothing else.
func TestSandboxGrant(t *testing.T) {
	msgs := []struct {
		name   string
		msg    *pb.ClientMessage
		action string
		target string
	}{
		{"input", &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: "p"}}}, grantSandboxSend, "p"},
		{"prompt an agent", &pb.ClientMessage{Msg: &pb.ClientMessage_AgentSend{AgentSend: &pb.AgentSend{PaneId: "p"}}}, grantSandboxSend, "p"},
		{"answer an agent", &pb.ClientMessage{Msg: &pb.ClientMessage_AgentAnswer{AgentAnswer: &pb.AgentAnswer{PaneId: "p"}}}, grantSandboxSend, "p"},
		{"switch an agent", &pb.ClientMessage{Msg: &pb.ClientMessage_SwitchAgent{SwitchAgent: &pb.SwitchAgent{PaneId: "p"}}}, grantSandboxSend, "p"},
		{"split a command", &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: "p", Spec: &pb.PaneSpec{Command: []string{"sh"}}}}}, grantSandboxRun, "p"},
		{"split a shell", &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: "p"}}}, grantSandboxRun, "p"},
		{"new tab", &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: "g", Pane: &pb.PaneSpec{Cwd: "/tmp"}}}}, grantSandboxRun, ""},
		{"replace", &pb.ClientMessage{Msg: &pb.ClientMessage_ReplacePane{ReplacePane: &pb.ReplacePane{PaneId: "p", Spec: &pb.PaneSpec{Command: []string{"sh"}}}}}, grantSandboxRun, "p"},
		{"agent", &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{GroupId: "g"}}}, grantSandboxRun, ""},
		{"agent beside", &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{SplitPaneId: "p"}}}, grantSandboxRun, "p"},
		{"log command", &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{Pane: &pb.PaneSpec{Log: &pb.LogSource{Kind: "command", Command: []string{"sh"}}}}}}, grantSandboxRun, ""},
		{"sim install", &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: "s", Op: "install"}}}, grantSandboxRun, "s"},
		{"sim launch", &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: "s", Op: "launch"}}}, grantSandboxRun, "s"},

		{"read", &pb.ClientMessage{Msg: &pb.ClientMessage_ReadPane{ReadPane: &pb.ReadPane{PaneId: "p"}}}, "", ""},
		{"browser pane", &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: "p", Spec: &pb.PaneSpec{Url: "http://localhost"}}}}, "", ""},
		{"editor", &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{Pane: &pb.PaneSpec{Editor: &pb.EditorOpen{}}}}}, "", ""},
		{"diff", &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{Pane: &pb.PaneSpec{Diff: &pb.DiffOpen{}}}}}, "", ""},
		{"log file", &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{Pane: &pb.PaneSpec{Log: &pb.LogSource{Kind: "file", Path: "/tmp/x"}}}}}, "", ""},
		{"simulator pane", &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{Pane: &pb.PaneSpec{Simulator: "u"}}}}, "", ""},
		{"sim tap", &pb.ClientMessage{Msg: &pb.ClientMessage_SimulatorCall{SimulatorCall: &pb.SimulatorCall{PaneId: "s", Op: "touch"}}}, "", ""},
		{"interrupt", &pb.ClientMessage{Msg: &pb.ClientMessage_SignalPane{SignalPane: &pb.SignalPane{PaneId: "p"}}}, "", ""},
		{"close", &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: "p"}}}, "", ""},
		{"browse", &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserNavigate{BrowserNavigate: &pb.BrowserNavigate{PaneId: "p"}}}, "", ""},
		{"organise", &pb.ClientMessage{Msg: &pb.ClientMessage_RenameGroup{RenameGroup: &pb.RenameGroup{GroupId: "g"}}}, "", ""},
	}
	for _, m := range msgs {
		action, target, ok := SandboxGrant(m.msg)
		if ok != (m.action != "") || action != m.action || target != m.target {
			t.Errorf("%s: %q %q %v, want %q %q", m.name, action, target, ok, m.action, m.target)
		}
	}
}

// A sandboxed agent (or anything sandboxed in a pane) asks before running
// or typing anything, in its own Groups and panes too; reading and
// organising stay free, and an agent that isn't sandboxed is as it was.
func TestSandboxedCallerAsks(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: cat}}}))
	d.mu.Lock()
	agentPane := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	neighbour := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: agentPane, Spec: cat}}}))

	plain := dialAs(t, dir, &caller{pane: agentPane, agent: true}, "cli")
	sb := dialSandboxed(t, dir, &caller{pane: agentPane, agent: true})
	refused := func(err error) bool { return err != nil && strings.HasPrefix(err.Error(), SandboxRefusal) }
	split := func(c *client.Client, beside string) (string, error) {
		r, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: beside, Spec: cat}}})
		return client.CreatedID(r), err
	}
	newTab := func(c *client.Client) error {
		_, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: cat}}})
		return err
	}
	input := func(c *client.Client, pane string) error {
		_, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: []byte("x")}}})
		return err
	}

	// Not sandboxed: as it was.
	mine, err := split(plain, agentPane)
	if err != nil {
		t.Fatalf("an agent splitting its pane: %v", err)
	}
	if err := input(plain, mine); err != nil {
		t.Fatalf("an agent typing into its own pane: %v", err)
	}

	// Sandboxed: running or typing anything asks, its own panes included.
	if _, err := split(sb, agentPane); !refused(err) {
		t.Fatalf("a sandboxed agent split a command beside it: %v", err)
	}
	if err := newTab(sb); !refused(err) {
		t.Fatalf("a sandboxed agent opened a tab: %v", err)
	}
	if _, err := try(sb, &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{GroupId: g, Harness: "claude", Cwd: t.TempDir()}}}); !refused(err) {
		t.Fatalf("a sandboxed agent started an agent: %v", err)
	}
	if err := input(sb, mine); !refused(err) {
		t.Fatalf("a sandboxed agent typed into its own pane: %v", err)
	}
	if err := input(sb, neighbour); !refused(err) {
		t.Fatalf("a sandboxed agent typed into your pane: %v", err)
	}
	// Reading, organising, interrupting and closing its own: free.
	if _, err := try(sb, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadPane{ReadPane: &pb.ReadPane{PaneId: neighbour}}}); err != nil {
		t.Fatalf("a sandboxed agent reading: %v", err)
	}
	if _, err := try(sb, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupIcon{SetGroupIcon: &pb.SetGroupIcon{GroupId: g, Icon: "x"}}}); err != nil {
		t.Fatalf("a sandboxed agent organising: %v", err)
	}
	if _, err := try(sb, &pb.ClientMessage{Msg: &pb.ClientMessage_SignalPane{SignalPane: &pb.SignalPane{PaneId: mine, Signal: 2}}}); err != nil {
		t.Fatalf("a sandboxed agent interrupting its own pane: %v", err)
	}

	// It asks (as hmk does: SandboxGrant), Hammock says it's from its
	// sandbox, and once you allow it, it goes ahead.
	ask := func(action, target, want string) {
		t.Helper()
		done := make(chan *pb.GrantDecision, 1)
		go func() {
			r, _ := try(sb, &pb.ClientMessage{Msg: &pb.ClientMessage_RequestGrant{RequestGrant: &pb.RequestGrant{
				TargetPaneId: target, Action: action, Description: "hmk split",
			}}})
			done <- r.GetGrantDecision()
		}()
		var gr *pb.GrantRequest
		waitEvent(t, ui, func(ev *pb.ServerMessage) bool {
			if gs := ev.GetState().GetGrants(); len(gs) > 0 {
				gr = gs[0]
			}
			return gr != nil
		})
		if !strings.HasPrefix(gr.Headline, want) || !strings.Contains(gr.Headline, "outside its sandbox") {
			t.Fatalf("headline %q, want %q…", gr.Headline, want)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := ui.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: gr.Id, Decision: pb.RespondGrant_DECISION_ALLOW_ONCE}}}); err != nil {
			t.Fatal(err)
		}
		if dec := <-done; dec == nil || !dec.Allowed {
			t.Fatalf("decision %v", dec)
		}
	}
	ask(grantSandboxSend, mine, "An agent in a ▸ Tab 1 wants to type into a ▸ Tab 1")
	if err := input(sb, mine); err != nil {
		t.Fatalf("allowed, typing into its own pane: %v", err)
	}
	// One grant for a pane it didn't open covers both: its scope and its sandbox.
	ask(grantSandboxSend, neighbour, "An agent in a ▸ Tab 1 wants to type into a ▸ Tab 1")
	if err := input(sb, neighbour); err != nil {
		t.Fatalf("allowed, typing into your pane: %v", err)
	}
	// A new tab has no pane to name: it's asked about its own.
	ask(grantSandboxRun, "", "An agent in a ▸ Tab 1 wants to run a command outside its sandbox")
	if err := newTab(sb); err != nil {
		t.Fatalf("allowed, opening a tab: %v", err)
	}
	if _, err := split(sb, neighbour); !refused(err) {
		t.Fatalf("a grant for a new tab let it run a command beside another pane: %v", err)
	}

	// Anything sandboxed in a pane is scoped as an agent, even one that
	// isn't (a pane's own shell).
	shell := dialSandboxed(t, dir, &caller{pane: neighbour, direct: true})
	if err := input(shell, agentPane); !refused(err) {
		t.Fatalf("a sandboxed shell typed into a pane: %v", err)
	}
	if err := newTab(shell); !refused(err) {
		t.Fatalf("a sandboxed shell opened a tab: %v", err)
	}
}

// Something sandboxed outside Hammock's panes (an agent's sandboxed
// command in another terminal) is scoped as an agent, not taken as yours,
// and asks as one in a sandbox does.
func TestSandboxedOutsidePanesAsks(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "a"}}}))
	asPlain(t, func(*caller, int) bool { return true }) // a terminal of yours, but for the sandbox
	personHook = func(*caller, int) bool { return false }
	out := dialSandboxed(t, dir, nil)
	personHook = testPerson
	_, err := try(out, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}})
	if err == nil || !strings.HasPrefix(err.Error(), SandboxRefusal) {
		t.Fatalf("a sandboxed program outside the panes opened a tab: %v", err)
	}
}
