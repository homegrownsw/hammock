package tui

import (
	"strings"
	"testing"
)

// Each key the sidebar's hint names after ⌃B opens what it says it does.
func TestSidebarHintKeysWork(t *testing.T) {
	want := map[string]func(o any) bool{
		"Commands": func(o any) bool { _, ok := o.(*palette); return ok },
		"Keys":     func(o any) bool { d, ok := o.(*dialog); return ok && d.title == "Keyboard Shortcuts" },
	}
	parts := strings.Split(sidebarHint, "⌃B ")[1:]
	if len(parts) != len(want) {
		t.Fatalf("hint %q: %d keys, want %d", sidebarHint, len(parts), len(want))
	}
	for _, p := range parts {
		key, label, _ := strings.Cut(strings.TrimSpace(p), "  ")
		check := want[strings.TrimSpace(label)]
		if check == nil || len(key) != 1 {
			t.Fatalf("hint part %q", p)
		}
		a, _ := paletteApp()
		a.command([]byte(key))
		if !check(a.overlay) {
			t.Errorf("⌃B %s (%s) opened %T", key, label, a.overlay)
		}
	}
}

// A key ⌃B doesn't know says so, and where to find the ones it does;
// Escape, to change your mind, says nothing.
func TestUnknownPrefixKeyFlashes(t *testing.T) {
	for key, want := range map[string]string{
		"g":      "g isn't a shortcut · ⌃B ? lists them",
		"é":      "é isn't a shortcut · ⌃B ? lists them",
		"\x07":   "⌃G isn't a shortcut · ⌃B ? lists them",
		"\r":     "Return isn't a shortcut · ⌃B ? lists them",
		"\x1b":   "",
		"\x1b[A": "",
	} {
		a, _ := paletteApp()
		if n := a.command([]byte(key)); n != len(key) {
			t.Errorf("%q: consumed %d bytes, want %d", key, n, len(key))
		}
		if a.toast != want {
			t.Errorf("%q: flashed %q, want %q", key, a.toast, want)
		}
	}
}
