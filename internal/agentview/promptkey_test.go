package agentview

import (
	"os"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// bashPrompt is Claude Code's permission prompt for a command, as the
// screen shows it, with the conversation above it.
func bashPrompt(command []string, selected int, indent string) string {
	var b strings.Builder
	b.WriteString("⏺ Cleaning up the build.\n  ⎿  $ ls build\n\n")
	b.WriteString(strings.Repeat("─", 60) + "\n")
	b.WriteString(indent + "Bash command\n\n")
	for _, l := range command {
		b.WriteString(indent + "  " + l + "\n")
	}
	b.WriteString(indent + "  Clean up\n\n")
	b.WriteString(indent + "Do you want to proceed?\n")
	for i, o := range []string{"Yes", "Yes, and don't ask again for rm commands in /work", "No"} {
		mark := "  "
		if i+1 == selected {
			mark = "❯ "
		}
		b.WriteString(indent + mark + string(rune('1'+i)) + ". " + o + "\n")
	}
	b.WriteString("\n" + indent + "Esc to cancel · Tab to amend\n")
	return b.String()
}

// A prompt's text is all of it above the menu, and nothing of the
// conversation above it; every line of a command.
func TestPromptBody(t *testing.T) {
	sc := ReadScreen(bashPrompt([]string{"cd /work", "rm -rf build", "rm -rf ~"}, 1, " "))
	want := "Bash command\n\n  cd /work\n  rm -rf build\n  rm -rf ~\n  Clean up\n\nDo you want to proceed?"
	if sc.Body != want {
		t.Errorf("body %q, want %q", sc.Body, want)
	}
	if sc.Key == "" || len(sc.Options) != 3 {
		t.Fatalf("screen %+v", sc)
	}
	for _, c := range []struct{ file, has, not string }{
		{"permission.txt", "touch /tmp/agentview-spike/hello.txt", "Creating empty"},
		{"codex-permission.txt", "$ touch hello.txt", "I'll create"},
		{"codex-0.166-approval.txt", "$ printf 'hi' > approval-test.txt", "Running"},
	} {
		b, _ := os.ReadFile("testdata/" + c.file)
		sc := ReadScreen(string(b))
		if !strings.Contains(sc.Body, c.has) || strings.Contains(sc.Body, c.not) || strings.Contains(sc.Body, "╌") || sc.Key == "" {
			t.Errorf("%s: body %q, key %q", c.file, sc.Body, sc.Key)
		}
	}
	if sc := ReadScreen("$ ls\nfoo bar\n$ "); sc.Key != "" || sc.Body != "" {
		t.Errorf("no menu: %+v", sc)
	}
	// A command taller than the screen: its first lines are gone, and it says so.
	full := bashPrompt([]string{"cd /work", "rm -rf build"}, 1, " ")
	cut := full[strings.Index(full, "  cd /work"):]
	if sc := ReadScreen(cut); !strings.HasPrefix(sc.Body, "…\n") || !strings.Contains(sc.Body, "cd /work\n  rm -rf build") {
		t.Errorf("run off the top: %q", sc.Body)
	}
	// Codex's, under its header box: from the conversation down.
	codex := "╭──────────────╮\n│ >_ Codex     │\n╰──────────────╯\n\n› Make it\n\n  Would you like to run the following command?\n\n  $ make\n\n› 1. Yes, proceed (y)\n  2. No, and tell Codex what to do differently (esc)\n\n  Press enter to confirm or esc to cancel\n"
	if sc := ReadScreen(codex); !strings.HasPrefix(sc.Body, "› Make it") || !strings.HasSuffix(sc.Body, "$ make") {
		t.Errorf("codex under its header: %q", sc.Body)
	}
}

// Which prompt it is doesn't change as the selection moves or the text is
// laid out again, and does with another command behind the same options.
func TestPromptKey(t *testing.T) {
	cmd := []string{"cd /work", "rm -rf build"}
	key := ReadScreen(bashPrompt(cmd, 1, " ")).Key
	if k := ReadScreen(bashPrompt(cmd, 2, " ")).Key; k != key {
		t.Error("key changed with the selection")
	}
	if k := ReadScreen(bashPrompt(cmd, 1, "   ")).Key; k != key {
		t.Error("key changed with the indentation")
	}
	if k := ReadScreen(bashPrompt([]string{"cd /work", "rm", "-rf build"}, 1, " ")).Key; k != key {
		t.Error("key changed with the wrapping")
	}
	other := ReadScreen(bashPrompt([]string{"cd /work", "rm -rf ~"}, 1, " "))
	if other.Key == key || !sameOptions(other.Options, ReadScreen(bashPrompt(cmd, 1, " ")).Options) {
		t.Error("another command, same options: same key")
	}
	if p := Prompt(other, nil); p.PromptId != other.Key {
		t.Errorf("prompt id %q, want %q", p.PromptId, other.Key)
	}
}

// Needs You's Allow is only for the prompt its card showed; Deny, and an
// older client's Allow (no prompt id), for whichever is showing.
func TestDecisionStepsBound(t *testing.T) {
	seen := ReadScreen(bashPrompt([]string{"ls build"}, 1, " "))
	now := ReadScreen(bashPrompt([]string{"rm -rf ~"}, 1, " "))
	if _, err := DecisionSteps(now, seen.Key, true, false); err != ErrChanged {
		t.Errorf("stale card: want ErrChanged, got %v", err)
	}
	steps, err := DecisionSteps(now, now.Key, true, false)
	if err != nil || len(steps) != 1 || string(steps[0].Keys) != "\r" || !steps[0].Ready(now) {
		t.Fatalf("matching: %v, %v", steps, err)
	}
	// Typed only while that prompt shows, though another has the same options.
	if steps[0].Ready(seen) {
		t.Error("allow ready for another prompt with the same options")
	}
	if deny, err := DecisionSteps(now, seen.Key, false, false); err != nil || string(deny[0].Keys) != "\x1b" || !deny[0].Ready(now) {
		t.Errorf("deny from a stale card: %v, %v", deny, err)
	}
	if legacy, err := DecisionSteps(now, "", true, false); err != nil || len(legacy) != 1 || !legacy[0].Ready(now) {
		t.Errorf("no prompt id: %v, %v", legacy, err)
	}
}

// The agent view's answers are bound the same way, when the client says
// which prompt it answered.
func TestAnswerStepsBound(t *testing.T) {
	seen := Prompt(ReadScreen(bashPrompt([]string{"ls build"}, 1, " ")), nil)
	nowSc := ReadScreen(bashPrompt([]string{"rm -rf ~"}, 1, " "))
	now := Prompt(nowSc, nil)
	if _, err := AnswerSteps(now, &pb.AgentAnswer{OptionsSeen: seen.Options, Option: 1, PromptId: seen.PromptId}, false, false); err != ErrChanged {
		t.Errorf("stale: want ErrChanged, got %v", err)
	}
	steps, err := AnswerSteps(now, &pb.AgentAnswer{OptionsSeen: now.Options, Option: 1, PromptId: now.PromptId}, false, false)
	if err != nil || !steps[0].Ready(nowSc) {
		t.Fatalf("matching: %v, %v", steps, err)
	}
	if steps[0].Ready(ReadScreen(bashPrompt([]string{"ls build"}, 1, " "))) {
		t.Error("ready for another prompt with the same options")
	}
	if _, err := AnswerSteps(now, &pb.AgentAnswer{OptionsSeen: seen.Options, Option: 1}, false, false); err != nil {
		t.Errorf("no prompt id: %v", err)
	}
}
