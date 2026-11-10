package agents

import (
	"os/exec"
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Quote is one word for the shell whatever it holds: quotes, backslashes,
// newlines, $(...), backticks, globs and controls come out as they went in.
func TestQuoteIsOneWord(t *testing.T) {
	shells := []string{"/bin/sh", "/bin/bash", "/bin/zsh"}
	if f, err := exec.LookPath("fish"); err == nil {
		shells = append(shells, f)
	}
	for _, s := range []string{
		"plain", "it's", `\`, `\'`, `'\''`, "a\\'; touch x; #", "two\nlines", "$(touch x) `touch x` $HOME *",
		"\x15touch x\r", "\"double\" !! !$", "tab\there", "",
	} {
		for _, sh := range shells {
			if _, err := exec.LookPath(sh); err != nil {
				continue
			}
			out, err := exec.Command(sh, "-c", "printf %s "+Quote(s)).Output()
			if err != nil || string(out) != s {
				t.Errorf("%s: %q came out %q (%v)", sh, s, out, err)
			}
		}
	}
}

// A prompt keeps its text, tabs and newlines; controls, ESC (a paste's
// end, a terminal sequence), DEL, C1 and broken UTF-8 go.
func TestCleanPrompt(t *testing.T) {
	for in, want := range map[string]string{
		"fix the build":              "fix the build",
		"two\r\nlines\rand\tcolumns": "two\nlines\nand\tcolumns",
		"\x15hmk pair\r":             "hmk pair\n",
		"end\x1b[201~\x03rm -rf ~\r": "end[201~rm -rf ~\n",
		"del\x7f c1\u009b0m nul\x00": "del c10m nul",
		"bad\x9bbyte � kept é":       "badbyte � kept é",
	} {
		if got := CleanPrompt(in); got != want {
			t.Errorf("CleanPrompt(%q) = %q, want %q", in, got, want)
		}
	}
	// Scheduled runs and plain starts both clean it; the schedule itself
	// isn't changed.
	s := &pb.Schedule{Prompt: "go\x1b[201~\x15x"}
	if got := Get("claude").ScheduleCommand(s); got != "claude 'go[201~x'" || s.Prompt != "go\x1b[201~\x15x" {
		t.Errorf("scheduled: %q (prompt now %q)", got, s.Prompt)
	}
	if got := Get("codex").StartCommand("a\x15b"); got != "codex 'ab'" {
		t.Errorf("started: %q", got)
	}
}
