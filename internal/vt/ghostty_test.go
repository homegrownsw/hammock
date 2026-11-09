//go:build ghostty

package vt

import (
	"strings"
	"testing"
)

func TestGhosttyAnswersQueriesAndKeepsStyle(t *testing.T) {
	m := New(20, 5)
	defer m.Close()
	var reply []byte
	m.SetResponder(func(b []byte) { reply = append(reply, b...) })
	m.Write([]byte("\x1b[6n")) // cursor position report
	if !strings.HasPrefix(string(reply), "\x1b[1;1R") {
		t.Fatalf("reply %q", reply)
	}
	m.Write([]byte("\x1b[31mred\x1b[0m"))
	// Colours stay palette references (38;5;N), so each client applies its theme.
	if snap := string(m.Snapshot()); !strings.Contains(snap, "red") || !strings.Contains(snap, "38;5;1m") {
		t.Fatalf("snapshot lost style: %q", snap)
	}
}

// Colour queries and the light/dark scheme reflect the client's colours.
func TestGhosttyReportsClientColors(t *testing.T) {
	m := New(20, 5)
	defer m.Close()
	var reply []byte
	m.SetResponder(func(b []byte) { reply = append(reply, b...) })
	m.SetColors(Colors{Foreground: 0x1f1c1a, Background: 0xf8f9fb, Cursor: 0x1f1c1a, Palette: []uint32{0, 0xff2b13}})
	m.Write([]byte("\x1b]11;?\x1b\\\x1b]4;1;?\x1b\\\x1b[?996n"))
	got := string(reply)
	for _, want := range []string{"rgb:f8f8/f9f9/fbfb", "4;1;rgb:ffff/2b2b/1313", "\x1b[?997;2n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reply %q lacks %q", got, want)
		}
	}
	// A program that enabled mode 2031 hears about the switch to dark.
	reply = nil
	m.Write([]byte("\x1b[?2031h"))
	m.SetColors(Colors{Foreground: 0xfffaf5, Background: 0x06070c, Dark: true})
	if !strings.Contains(string(reply), "\x1b[?997;1n") {
		t.Fatalf("no dark report: %q", reply)
	}
}
