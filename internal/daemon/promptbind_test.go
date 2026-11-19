package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/agentview"
	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// permissionScreen is Claude Code's permission prompt for command.
func permissionScreen(command ...string) string {
	s := strings.Repeat("─", 40) + "\n Bash command\n\n"
	for _, l := range command {
		s += "   " + l + "\n"
	}
	return s + "\n Do you want to proceed?\n ❯ 1. Yes\n   2. Yes, and don't ask again for this command\n   3. No\n\n Esc to cancel · Tab to amend\n"
}

// Needs You's card carries the prompt its pane shows (every line of the
// command), and its Allow answers only that prompt: a card from an earlier
// prompt with the same options is refused, typing nothing, and the card
// comes back as the pane is now.
func TestAllowBoundToItsPrompt(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))

	earlier := agentview.ReadScreen(permissionScreen("ls build"))
	shown := permissionScreen("cd /work", "rm -rf build", "rm -rf ~/Documents")
	now := agentview.ReadScreen(shown)
	if earlier.Key == now.Key || len(earlier.Options) != 3 {
		t.Fatalf("screens: %+v, %+v", earlier, now)
	}
	script := filepath.Join(t.TempDir(), "prompt.txt")
	os.WriteFile(script, []byte(shown), 0o600)
	// Shows the prompt, reports the first key it gets, then anything after.
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", `stty -icanon -icrnl -echo min 1; cat "$0"; k=$(dd bs=1 count=1 2>/dev/null | od -An -c | tr -d ' '); printf '\033[2J\033[Hgot[%s]\n' "$k"; exec cat -v`, script}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	waitOutput(t, c, paneID, "Esc to cancel")

	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(
		`{"hook_event_name":"Notification","session_id":"s","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`)})
	carries := func(ev *pb.ServerMessage) bool {
		for _, a := range ev.GetState().GetAttention() {
			if a.PaneId == paneID && a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT && a.PromptId == now.Key {
				return true
			}
		}
		return false
	}
	ev := waitEvent(t, c, carries)
	for _, a := range ev.GetState().GetAttention() {
		if a.PaneId == paneID && !strings.Contains(a.Detail, "cd /work\n  rm -rf build\n  rm -rf ~/Documents") {
			t.Errorf("card's text %q: not the whole command", a.Detail)
		}
	}

	// A stale card: refused, and the card's back as the pane is now.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClearAttention{ClearAttention: &pb.ClearAttention{Id: "at_" + paneID}}})
	allow := func(id string) *pb.ClientMessage {
		return &pb.ClientMessage{Msg: &pb.ClientMessage_AgentAnswer{AgentAnswer: &pb.AgentAnswer{PaneId: paneID, Decision: pb.AgentAnswer_DECISION_ALLOW, PromptId: id, Bound: true}}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, allow(earlier.Key)); err == nil || !strings.Contains(err.Error(), "request changed") {
		t.Fatalf("allow from a stale card: %v", err)
	}
	waitEvent(t, c, carries)
	// A card that hadn't the prompt yet, from a client that knows prompts.
	if _, err := c.Call(ctx, allow("")); err == nil || !strings.Contains(err.Error(), "request changed") {
		t.Fatalf("allow from a card without its prompt: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	d.mu.Lock()
	p := d.panes[paneID]
	d.mu.Unlock()
	typed := p.text(false)
	if strings.Contains(typed, "got[") {
		t.Fatalf("a stale card typed into the pane: %q", typed)
	}

	// Its own prompt: allowed.
	call(t, c, allow(now.Key))
	if out := waitOutput(t, c, paneID, "got["); !strings.Contains(out, `got[\r]`) {
		t.Fatalf("allow typed: %q", out)
	}
}

// A card's summary says how much of a command it leaves out.
func TestSummaryCountsHiddenLines(t *testing.T) {
	for in, want := range map[string]string{
		"ls":                         "Allow Bash: ls?",
		"cd /work\nrm -rf build":     "Allow Bash: cd /work (+1 more line)?",
		"cd /work\nmake\nrm -rf ~\n": "Allow Bash: cd /work (+2 more lines)?",
	} {
		input, _ := json.Marshal(map[string]string{"command": in})
		if got := needsSummary(agents.Hook{ToolName: "Bash", ToolInput: input}); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
