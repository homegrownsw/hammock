package daemon

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestInferState(t *testing.T) {
	cases := []struct {
		screen string
		want   pb.AgentState
	}{
		{"⏺ Reading files…\n✻ Percolating… (12s · esc to interrupt)\n> ", pb.AgentState_AGENT_STATE_WORKING},
		{"╭─ Bash command ─╮\n│ go test ./... │\n│ Do you want to proceed?\n│ ❯ 1. Yes\n│   2. No", pb.AgentState_AGENT_STATE_NEEDS_INPUT},
		{"Would you like to run the following command?\n  $ rm -rf build\n  ▌ Yes (y)  No (n)", pb.AgentState_AGENT_STATE_NEEDS_INPUT},
		{"⏺ Done. All tests pass.\n\n> \n  ? for shortcuts", pb.AgentState_AGENT_STATE_IDLE},
	}
	for _, c := range cases {
		if got, _ := inferState("", c.screen); got != c.want {
			t.Errorf("inferState(%q) = %v, want %v", c.screen, got, c.want)
		}
	}
}
