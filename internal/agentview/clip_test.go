package agentview

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClipKeepsUTF8Valid(t *testing.T) {
	s := strings.Repeat("a", 9) + "─────" // 3-byte runes from byte 9
	for n := 9; n < 14; n++ {
		if got := clip(s, n); !utf8.ValidString(got) {
			t.Fatalf("clip(%d) = %q: invalid UTF-8", n, got)
		}
	}
}

// Tool output loses its terminal escapes, hyperlinks' text kept.
func TestStripANSI(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[1;31merror\x1b[0m: no":                                   "error: no",
		"see \x1b]8;;https://example.com\x1b\\the docs\x1b]8;;\x1b\\.": "see the docs.",
		"\x1b]8;id=1;file:///w/a.go\x07a.go\x1b]8;;\x07:3":             "a.go:3",
		"\x1b]0;title\x07done":                                         "done",
		"\x1b(B\x1b)0plain\x1b=\x1b>\x1b7x\x1b8":                       "plainx",
		"\x1b[?25l\x1b[2K\x1b[>4;1mok\x1b[?25h":                        "ok",
		"\x1bP1$r0m\x1b\\after":                                        "after",
		"cut \x1b]8;;https://exam":                                     "cut ",
	} {
		if got := stripANSI(in); got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}
