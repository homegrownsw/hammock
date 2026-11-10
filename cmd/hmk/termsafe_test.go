package main

import (
	"errors"
	"strings"
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestTermSafe(t *testing.T) {
	const r = "\uFFFD"
	cases := []struct {
		name, in, line, text string
	}{
		{"plain", "Work · café 🚀", "Work · café 🚀", "Work · café 🚀"},
		{"empty", "", "", ""},
		{"osc52 clipboard", "x\x1b]52;c;ZWNobyBoaQ==\x07y", "x" + r + "]52;c;ZWNobyBoaQ==" + r + "y", "x" + r + "]52;c;ZWNobyBoaQ==" + r + "y"},
		{"clear screen", "\x1b[2J\x1b[H", r + "[2J" + r + "[H", r + "[2J" + r + "[H"},
		{"newline and tab", "a\nb\tc", "a b c", "a\nb\tc"},
		{"carriage return", "ok\rfake", "ok fake", "ok" + r + "fake"},
		{"backspace and DEL", "a\bb\x7f", "a" + r + "b" + r, "a" + r + "b" + r},
		{"C1 CSI as a rune", "a\u009b2Jb", "a" + r + "2Jb", "a" + r + "2Jb"},
		{"C1 as a lone byte", "a\x9b2Jb", "a" + r + "2Jb", "a" + r + "2Jb"},
		{"bidi override", "evil‮gnp.exe", "evil" + r + "gnp.exe", "evil" + r + "gnp.exe"},
		{"bidi isolates and marks", "⁦a⁩‎‏؜", r + "a" + r + r + r + r, r + "a" + r + r + r + r},
		{"line separators", "a b ", "a" + r + "b" + r, "a" + r + "b" + r},
		{"NUL", "a\x00b", "a" + r + "b", "a" + r + "b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := termSafe(c.in); got != c.line {
				t.Errorf("termSafe(%q) = %q, want %q", c.in, got, c.line)
			}
			if got := termSafeText(c.in); got != c.text {
				t.Errorf("termSafeText(%q) = %q, want %q", c.in, got, c.text)
			}
		})
	}
}

func TestSprintfSafe(t *testing.T) {
	got := sprintfSafe("%s  %-6s %d %v %s\n", "g\x1b]0;x\x07", "a\nb", 3, []string{"\x1b[2J"}, errors.New("bad\x1b[H"))
	want := "g\uFFFD]0;x\uFFFD  a b    3 [\uFFFD[2J] bad\uFFFD[H\n"
	if got != want {
		t.Errorf("sprintfSafe = %q, want %q", got, want)
	}
}

func TestFormatEntrySafe(t *testing.T) {
	e := &pb.LogEntry{Message: "\x1b[31mred\x1b[0m then \x1b]52;c;eA==\x07"}
	got := formatEntry(e, false)
	if want := " red then \uFFFD]52;c;eA==\uFFFD"; !strings.HasSuffix(got, want) {
		t.Errorf("formatEntry = %q, want it to end %q", got, want)
	}
}

func TestValueFor(t *testing.T) {
	// A real folder with a tab and a newline in its name: kept for a script.
	dir := "/Users/me/odd\tname\nhere"
	if got := valueFor(dir, false); got != dir {
		t.Errorf("valueFor(piped) = %q, want it unchanged", got)
	}
	if got := valueFor(dir+"\x1b]52;c;eA==\x07", true); got != "/Users/me/odd name here�]52;c;eA==�" {
		t.Errorf("valueFor(terminal) = %q", got)
	}
}

func TestFormatExcerptTitleOneLine(t *testing.T) {
	ex := &pb.SessionExcerpt{Session: &pb.SessionInfo{Title: "Fix it\nSECURITY: all clear", Harness: "claude", Id: "s1", Cwd: "/tmp/a\nb"}}
	lines := strings.Split(formatExcerpt(ex, ""), "\n")
	if lines[0] != "Fix it SECURITY: all clear" {
		t.Errorf("title line = %q", lines[0])
	}
	if !strings.Contains(lines[1], "/tmp/a b") {
		t.Errorf("folder not kept on its line: %q", lines[1])
	}
}
