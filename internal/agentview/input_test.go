package agentview

import (
	"bytes"
	"testing"
)

func TestInputLines(t *testing.T) {
	rule := "────────────────────────────────────────"
	for name, tc := range map[string]struct {
		screen string
		want   int
	}{
		"claude empty":     {"✻ Cooked for 2s\n" + rule + "\n❯ \n" + rule + "\n   ~/claude-sandbox\n  ◆ Opus 5.5", 1},
		"claude two lines": {"✻ Cooked\n" + rule + "\n❯ first line\n  second line\n" + rule + "\n   ~/claude-sandbox\n", 2},
		"codex empty":      {"\n\n› Ask Codex to do anything\n\n  GPT-6-Luna medium · ~/claude-sandbox\n", 1},
		"codex two lines":  {"\n\n› first line\n  second line\n\n  GPT-6-Luna medium · ~/claude-sandbox\n", 2},
		"no box":           {"$ ls\nfoo\n", 1},
	} {
		if got := InputLines(tc.screen); got != tc.want {
			t.Errorf("%s: %d lines, want %d", name, got, tc.want)
		}
	}
	if !bytes.Equal(ClearInput(2), []byte{5, 0x15, 0x7F, 5, 0x15, 0x7F}) {
		t.Error("ClearInput")
	}
}

func TestInputDraft(t *testing.T) {
	rule := "────────────────────────────────────────"
	for name, tc := range map[string]struct{ screen, want string }{
		// Faint placeholders arrive as spaces (vt.Mirror.BottomText).
		"claude placeholder": {rule + "\n❯                                    \n" + rule + "\n   ~/x\n", ""},
		"claude typed":       {rule + "\n❯ typed one\n  typed two\n" + rule + "\n   ~/x\n", "typed one\ntyped two"},
		"codex placeholder":  {"\n›\n\n  GPT-6-Luna medium · ~/x\n", ""},
		"codex typed":        {"\n› typed one\n  typed two\n\n  GPT-6-Luna medium\n", "typed one\ntyped two"},
		"no box":             {"$ ls\n", ""},
	} {
		if got := InputDraft(tc.screen); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
