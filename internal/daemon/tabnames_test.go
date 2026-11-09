package daemon

import (
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestBranchName(t *testing.T) {
	for in, want := range map[string]string{
		"me/eng-89-notify-retry-attempts": "Notify Retry Attempts [ENG-89]",
		"alex/1180-smooth-timeline":       "Smooth Timeline [#1180]",
		"feature/brand-styling":           "Brand Styling",
		"mpg-372":                         "[MPG-372]",
		"main":                            "",
	} {
		if got := branchName(in); got != want {
			t.Errorf("branchName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgentTitle(t *testing.T) {
	for in, want := range map[string]string{
		"✳ Pricing page rewrite": "Pricing page rewrite",
		"⠂ Brand styling skill":  "Brand styling skill",
		"✳ Claude Code":          "",
		"zsh in work":            "",
		"claude":                 "",
	} {
		if got := agentTitle(in); got != want {
			t.Errorf("agentTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSpinnerFrame(t *testing.T) {
	for _, c := range []struct {
		old, title string
		want       bool
	}{
		{"⠂ Fix the bug", "⠐ Fix the bug", true},
		{"✳ Fix the bug", "⠐ Fix the bug", false}, // started working
		{"⠐ Fix the bug", "✳ Fix the bug", false}, // finished
		{"⠐ Fix the bug", "⠂ Another task", false},
		{"", "⠂ Fix the bug", false},
		{"vim a.go", "vim b.go", false},
	} {
		if got := spinnerFrame(c.old, c.title); got != c.want {
			t.Errorf("spinnerFrame(%q, %q) = %v, want %v", c.old, c.title, got, c.want)
		}
	}
}

func TestTabAutoName(t *testing.T) {
	agent := &pb.Pane{Title: "✳ Fix the timeline", Agent: &pb.Agent{Harness: "claude", SessionId: "s1", State: pb.AgentState_AGENT_STATE_IDLE}}
	sim := &pb.Pane{Kind: pb.PaneKind_PANE_KIND_SIMULATOR, Title: "iPhone 18 Pro"}
	browser := &pb.Pane{Kind: pb.PaneKind_PANE_KIND_BROWSER, Title: "Example"}
	for _, c := range []struct {
		name  string
		panes []*pb.Pane
		want  string
	}{
		{"focused agent", []*pb.Pane{agent, sim}, "Fix the timeline"},
		{"focused simulator, agent beside it", []*pb.Pane{sim, agent}, "Fix the timeline"},
		{"lone simulator", []*pb.Pane{sim}, ""},
		{"browser and simulator", []*pb.Pane{browser, sim}, ""},
	} {
		if got, _ := tabAutoName(c.panes); got != c.want {
			t.Errorf("%s: tabAutoName = %q, want %q", c.name, got, c.want)
		}
	}
}
