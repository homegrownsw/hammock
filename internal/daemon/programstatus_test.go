package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/vt"
)

// A program that isn't an agent reports its status (OSC 7501): its pane's
// agent follows, with no harness, Needs You says it's a program, and what it
// finished outlives it. It's told the protocol is supported when it asks.
func TestProgramStatusReports(t *testing.T) {
	if !vt.Emulated {
		t.Skip("needs libghostty-vt (-tags ghostty)")
	}
	gap := statusGap
	statusGap = 0
	defer func() { statusGap = gap }()

	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Hammock"}}}))

	// Each step waits for a line (Enter) before the next report.
	script := strings.Join([]string{
		`stty -echo`,
		`printf '\033]7501;?\007'`,
		`IFS= read -r -d $'\a' reply; printf 'reply=%s\n' "${reply#*7501;}"`,
		`printf '\033]7501;state=working:app=brew:progress=40:msg=SW5zdGFsbGluZyB1cGRhdGVz\033\\'; read x`,
		`printf '\033]7501;state=blocked:kind=question:app=brew:msg=V2hpY2ggdmVyc2lvbj8=\007'; read x`,
		`printf '\033]7501;state=working:app=brew\007\033]7501;state=blocked:id=eu:title=RVU=:kind=permission\007'; read x`,
		`printf '\033]7501;state=clear:id=eu\007\033]7501;state=done:app=brew:msg=VXBncmFkZWQgMTIgcGFja2FnZXM=\007'; read x`,
		`exit 3`, // (a clean exit closes the pane)
	}, "\n")
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Pane: &pb.PaneSpec{Command: []string{"/bin/bash", "-c", script}},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	waitOutput(t, c, paneID, "reply=?:states=idle,working,done,blocked,error")

	agent := func() (*pb.Agent, *pb.AttentionItem) {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.panes[paneID].snapshotInfo().GetAgent(), d.attention["at_"+paneID]
	}
	waitFor := func(what string, ok func(*pb.Agent, *pb.AttentionItem) bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			a, at := agent()
			if ok(a, at) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: agent %v, Needs You %v", what, a, at)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	enter := func() {
		call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("\r")}}})
	}

	waitFor("working", func(a *pb.Agent, at *pb.AttentionItem) bool {
		return a.GetState() == pb.AgentState_AGENT_STATE_WORKING && a.GetReported() && a.GetHarness() == "" &&
			a.GetApp() == "brew" && a.GetProgress() == 40 && a.GetSummary() == "Installing updates" && at == nil
	})
	enter()
	waitFor("asking", func(a *pb.Agent, at *pb.AttentionItem) bool {
		return a.GetState() == pb.AgentState_AGENT_STATE_NEEDS_INPUT && a.Progress == nil &&
			at != nil && at.Program && at.Question && at.Summary == "Which version?"
	})
	enter()
	waitFor("a child blocked while the root works", func(a *pb.Agent, at *pb.AttentionItem) bool {
		return a.GetState() == pb.AgentState_AGENT_STATE_NEEDS_INPUT && a.GetSummary() == "EU: Waiting for your approval" &&
			len(a.GetRecords()) == 2 && a.GetRecords()[1].App == "brew" && at != nil && !at.Question
	})
	enter()
	waitFor("done", func(a *pb.Agent, at *pb.AttentionItem) bool {
		return a.GetState() == pb.AgentState_AGENT_STATE_IDLE && !a.GetSeen() && len(a.GetRecords()) == 1 &&
			at != nil && at.Program && at.State == pb.AgentState_AGENT_STATE_IDLE && at.Summary == "Upgraded 12 packages"
	})
	enter() // it exits, failing: what it finished stays, unseen, and says so
	waitFor("exited", func(a *pb.Agent, at *pb.AttentionItem) bool {
		d.mu.Lock()
		exited := d.panes[paneID].snapshotInfo().Exited
		d.mu.Unlock()
		// (Typing in it, the Enter that ended it, took its Needs You item.)
		return exited && a.GetState() == pb.AgentState_AGENT_STATE_IDLE && !a.GetSeen() && len(a.GetRecords()) == 1
	})
	// Looking at it acknowledges it.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Focus{Focus: &pb.Focus{PaneId: paneID}}})
	waitFor("seen", func(a *pb.Agent, at *pb.AttentionItem) bool {
		return a.GetSeen() && len(a.GetRecords()) == 1 && a.GetRecords()[0].Seen
	})
}

// An agent whose hooks reach Hammock is told by them: its reports are kept,
// not followed.
func TestProgramStatusHooksWin(t *testing.T) {
	a := &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_WORKING}
	if reportsOwn(a) {
		t.Fatal("a hooked agent's reports don't set its state")
	}
	for _, a := range []*pb.Agent{nil, {Heuristic: true}, {Reported: true}, {State: pb.AgentState_AGENT_STATE_EXITED}} {
		if !reportsOwn(a) {
			t.Fatalf("reports set %v", a)
		}
	}
	if h := reportedHarness("claude-code"); h != "claude" {
		t.Fatalf("claude-code is %q", h)
	}
	if h := reportedHarness("brew"); h != "" {
		t.Fatalf("brew is %q", h)
	}
}
