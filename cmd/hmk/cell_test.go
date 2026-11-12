package main

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestCell(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"zsh", "zsh       "},
		{"日本語", "日本語    "},
		{"🚀 deploy", "🚀 deploy "},
		{"a long pane title", "a long pa…"},
		{"日本語のタイトル", "日本語の… "},
		{"tab\there", "tab here  "},
	} {
		got := cell(c.in, 10)
		if got != c.want {
			t.Errorf("cell(%q, 10) = %q, want %q", c.in, got, c.want)
		}
		if w := ansi.StringWidth(got); w != 10 {
			t.Errorf("cell(%q, 10) is %d cells wide", c.in, w)
		}
	}
}
