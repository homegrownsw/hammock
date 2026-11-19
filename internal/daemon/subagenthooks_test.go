package daemon

import (
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A subagent's hooks (Claude Code's agent_id) don't make its agent working:
// a background one runs on after the agent's turn. Its question, answered,
// returns the agent to what it was doing.
func TestSubagentHooksLeaveTheAgentsState(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	pane := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	agent := func() *pb.Agent {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.panes[pane].snapshotInfo().GetAgent()
	}
	check := func(when string, want pb.AgentState, summary string) {
		t.Helper()
		if a := agent(); a.GetState() != want || a.GetSummary() != summary {
			t.Fatalf("%s: %v %q, want %v %q", when, a.GetState(), a.GetSummary(), want, summary)
		}
	}
	bash := func(cmd string) map[string]any {
		return map[string]any{"session_id": "s1", "tool_name": "Bash", "tool_input": map[string]any{"command": cmd}}
	}
	subBash := func(cmd string) map[string]any {
		m := bash(cmd)
		m["agent_id"], m["agent_type"] = "a1b2c3", "general-purpose"
		return m
	}

	hook(d, pane, "UserPromptSubmit", map[string]any{"session_id": "s1", "prompt": "start a subagent"})
	hook(d, pane, "Stop", map[string]any{"session_id": "s1", "last_assistant_message": "Started it."})
	check("after its turn", pb.AgentState_AGENT_STATE_IDLE, "Started it.")

	hook(d, pane, "PreToolUse", subBash("ping -c 300 localhost"))
	hook(d, pane, "PostToolUse", subBash("ping -c 300 localhost"))
	hook(d, pane, "UserPromptSubmit", map[string]any{"session_id": "s1", "prompt": "hurry up", "agent_id": "a1b2c3"})
	check("its subagent working", pb.AgentState_AGENT_STATE_IDLE, "Started it.")

	// The subagent asks; the agent waits for you until it goes ahead.
	hook(d, pane, "PreToolUse", subBash("rm -rf build"))
	hook(d, pane, "Notification", map[string]any{"session_id": "s1", "notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})
	check("its subagent asking", pb.AgentState_AGENT_STATE_NEEDS_INPUT, "Claude needs your permission to use Bash")
	hook(d, pane, "PostToolUse", subBash("rm -rf build"))
	check("answered", pb.AgentState_AGENT_STATE_IDLE, "Started it.")

	// While the agent works, its subagents' tools don't change what it says.
	hook(d, pane, "UserPromptSubmit", map[string]any{"session_id": "s1", "prompt": "now this"})
	hook(d, pane, "PreToolUse", bash("go test ./..."))
	hook(d, pane, "PreToolUse", subBash("ls"))
	check("both working", pb.AgentState_AGENT_STATE_WORKING, "Bash: go test ./...")
}
