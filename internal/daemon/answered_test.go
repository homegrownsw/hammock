package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Moving through a waiting agent's menu and leaving it isn't answering it:
// the agent stays waiting, in Needs You. Answering it takes it back to work.
func TestKeysIntoMenuAreNotAnAnswer(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Hammock"}}}))
	// A permission prompt, until a line (Enter) answers it.
	menu := `stty -echo; printf ' Do you want to proceed?\n \342\235\257 1. Yes\n   2. No\n\n Esc to cancel\n'; read x; printf '\033[2J\033[Hdone\n'; exec cat`
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", menu}},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	waitOutput(t, c, paneID, "Esc to cancel")

	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(
		`{"hook_event_name":"Notification","session_id":"abc-123","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`)})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, a := range ev.GetState().GetAttention() {
			if a.PaneId == paneID && a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT {
				return true
			}
		}
		return false
	})

	state := func() (pb.AgentState, bool) {
		d.mu.Lock()
		defer d.mu.Unlock()
		_, waiting := d.attention["at_"+paneID]
		return d.panes[paneID].snapshotInfo().GetAgent().GetState(), waiting
	}
	for _, k := range []string{"\x1b[B", "\x1b[A", "\x1b[<0;3;2M"} { // ↓, ↑, a click
		call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte(k)}}})
	}
	time.Sleep(answerSettle + 400*time.Millisecond)
	if s, waiting := state(); s != pb.AgentState_AGENT_STATE_NEEDS_INPUT || !waiting {
		t.Fatalf("after ↑↓ at its menu: %v, in Needs You %v; want still waiting", s, waiting)
	}

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("\r")}}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, waiting := state()
		if s == pb.AgentState_AGENT_STATE_WORKING && !waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after answering: %v, in Needs You %v; want working", s, waiting)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Clicking into a waiting agent's pane, scrolling it or moving the pointer
// over it isn't answering it: only keys take it back to working.
func TestPointerOnly(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"\x1b[<0;12;5M", true},                  // SGR press
		{"\x1b[<0;12;5m", true},                  // SGR release
		{"\x1b[<35;40;20M\x1b[<35;41;20M", true}, // motion, two reports at once
		{"\x1b[<64;3;3M", true},                  // wheel
		{"\x1b[32;12;5M", true},                  // urxvt
		{"\x1b[M !!", true},                      // X10
		{"\x1b[I", true},                         // focus in
		{"\x1b[O\x1b[<0;1;1M", true},
		{"", false},
		{"1", false},
		{"\r", false},
		{"\x1b", false},                   // Escape
		{"\x1b[A", false},                 // up arrow
		{"\x1b[1;2A", false},              // shift-up
		{"\x1b[13;2u", false},             // kitty shift-enter
		{"\x1b[<0;12;5Mx", false},         // a click, then a key
		{"\x1b[<0;12M", false},            // cut short
		{"\x1b[200~text\x1b[201~", false}, // paste
	} {
		if got := pointerOnly([]byte(c.in)); got != c.want {
			t.Errorf("pointerOnly(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
