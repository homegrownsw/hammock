package daemon

import (
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Aider's screens, as aider-chat 0.86.2 drew them in a 100×30 terminal
// (rendered with pyte, trailing spaces trimmed as the mirror does).
func TestInferStateAider(t *testing.T) {
	top := "Analytics have been permanently disabled.\n" + aiderRule + "\nYou can skip this check with --no-gitignore\n"
	asked := top + "Add .aider* to .gitignore (recommended)? (Y)es/(N)o [Yes]: y\nAdded .aider* to .gitignore\nAider v0.86.2\n" +
		"Model: openai/fake with diff edit format\nGit repo: .git with 1 files\nRepo-map: disabled\nAdded hello.py to the chat.\n" + aiderRule + "\nhello.py\n"
	replied := "And add a caller:\n\nhello.py\n\n\n<<<<<<< SEARCH\n=======\n\nif __name__ == \"__main__\":\n    hello(\"world\")\n>>>>>>> REPLACE\n\n\n\n" +
		"Tokens: 2.4k sent, 77 received.\nApplied edit to hello.py\n"
	idle, working, asking := pb.AgentState_AGENT_STATE_IDLE, pb.AgentState_AGENT_STATE_WORKING, pb.AgentState_AGENT_STATE_NEEDS_INPUT
	for _, c := range []struct {
		name, screen string
		want         pb.AgentState
	}{
		{"its first question", top + "Add .aider* to .gitignore (recommended)? (Y)es/(N)o [Yes]:", asking},
		{"a question with more options", "hello.py\nAdd file to the chat? (Y)es/(N)o/(D)on't ask again [Yes]: ", asking},
		{"at its prompt", asked + "diff>", idle},
		{"at its prompt, the default mode", asked + "> ", idle},
		{"typing", asked + "diff> make it greet", idle},
		{"its completions' menu", asked + "diff> /ex\n          /exit\n          /exit-now", idle},
		{"other modes", asked + "architect multi> ", idle},
		{"waiting for the model", asked + "diff> make it greet by name\n\n ░█        Waiting for openai/fake", working},
		{"spinner further along", asked + "diff> make it greet by name\n\n     ░█   Waiting for openai/fake", working},
		{"streaming its reply", asked + "diff> make it greet by name\n\nI'll make it greet by name.\n\nhello.py\n\n\n<<<<<<< SEARCH\ndef hello():\n    print(\"hello\")\n=====", working},
		{"applying and committing", replied, working},
		{"done", replied + "Commit 721340d feat: Greet by name\nYou can use /undo to undo and discard each aider commit.\n" + aiderRule + "\nhello.py\ndiff>", idle},
		{"a question answered, then working", top + "Add .aider* to .gitignore (recommended)? (Y)es/(N)o [Yes]: y\nAdded .aider* to .gitignore\nAider v0.86.2", working},
		// Rich draws a reply's quotes with a bar; a line of a code block
		// that starts "> " would read as its prompt, for as long as it's
		// the last line.
		{"a quote in its reply", "Here's the output:\n\n▌ it works", working},
	} {
		if got, _ := inferState("aider", c.screen); got != c.want {
			t.Errorf("%s: %v, want %v\n%s", c.name, got, c.want, c.screen)
		}
	}
}

var aiderRule = strings.Repeat("─", 100)

func TestIsInterpreterVersionedPython(t *testing.T) {
	for name, want := range map[string]bool{
		"python": true, "python3": true, "Python": true, "python3.12": true, "python3.10": true, "node": true,
		"python-config": false, "pythonw": false, "pythonista": false, "aider": false, "py": false,
	} {
		if isInterpreter(name) != want {
			t.Errorf("isInterpreter(%q) = %v", name, !want)
		}
	}
}
