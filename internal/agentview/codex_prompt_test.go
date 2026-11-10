package agentview

import (
	"os"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Codex 0.166's approval prompt, as it was on screen: read like Claude's
// menus, and answered with each option's own key.
func TestCodexApprovalPrompt(t *testing.T) {
	b, err := os.ReadFile("testdata/codex-0.166-approval.txt")
	if err != nil {
		t.Fatal(err)
	}
	sc := ReadScreen(string(b))
	if len(sc.Options) != 3 || sc.Selected != 1 || sc.Options[0] != "Yes, proceed (y)" || sc.Options[2] != "No, and tell Codex what to do differently (esc)" {
		t.Fatalf("screen: %+v", sc)
	}
	if sc.Title == "" || !contains(sc.Title, "Would you like to run the following command?") || !contains(sc.Title, "$ printf 'hi' > approval-test.txt") {
		t.Fatalf("title %q", sc.Title)
	}
	p := Prompt(sc, &pb.AgentBlock{Id: "fc1", Kind: pb.AgentBlock_KIND_TOOL, Pending: true})
	if p.Kind != pb.AgentPrompt_KIND_PERMISSION {
		t.Fatalf("kind %v", p.Kind)
	}
	// Allow: Enter on the selected Yes. Deny: Esc.
	if s, _ := DecisionSteps(sc, true, false); len(s) != 1 || string(s[0].Keys) != "\r" {
		t.Fatalf("allow: %v", s)
	}
	if s, _ := DecisionSteps(sc, false, false); len(s) != 1 || string(s[0].Keys) != "\x1b" {
		t.Fatalf("deny: %v", s)
	}
	// Choosing an option presses its key: a number only moves Codex's
	// selection.
	for o, want := range map[int32]string{1: "y", 2: "p", 3: "\x1b"} {
		s, err := AnswerSteps(p, &pb.AgentAnswer{Option: o, OptionsSeen: p.Options}, false, false)
		if err != nil || len(s) != 1 || string(s[0].Keys) != want {
			t.Errorf("option %d: %v %v, want %q", o, s, err, want)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Codex's /model picker: its options with their descriptions, picked by
// number (which in a picker chooses at once).
func TestCodexModelPicker(t *testing.T) {
	screen := `• Created approval-test.txt (approval-test.txt) containing hi.
  Worked for 1m 16s • 09:57

  Select Model and Effort
› 1. GPT-6-Luna (current)  Fast and affordable model for easier tasks.
  2. GPT-5.6-Terra         Older balanced model for straightforward work.
  3. GPT-5.6-Luna          Older fast and efficient model.
  enter select · esc back`
	sc := ReadScreen(screen)
	if len(sc.Options) != 3 || sc.Options[1] != "GPT-5.6-Terra" || sc.Descriptions[1] != "Older balanced model for straightforward work." || sc.Selected != 1 || sc.Title != "Select Model and Effort" {
		t.Fatalf("%+v", sc)
	}
	p := Prompt(sc, nil)
	if s, err := AnswerSteps(p, &pb.AgentAnswer{Option: 2, OptionsSeen: p.Options}, false, false); err != nil || string(s[0].Keys) != "2" {
		t.Fatalf("pick: %v %v", s, err)
	}
	effort := ReadScreen(`  1. Low                         Fast responses with lighter reasoning
› 2. Medium (default) (current)  Balances speed and reasoning depth for everyday tasks
  3. High                        Greater reasoning depth for complex problems
  enter default · s session · esc back`)
	if len(effort.Options) != 3 || effort.Options[1] != "Medium (default) (current)" || effort.Selected != 2 {
		t.Fatalf("effort: %+v", effort)
	}
}
