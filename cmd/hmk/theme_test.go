package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheme(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	var out strings.Builder
	if err := theme(nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "* Hammock\n") || !strings.Contains(out.String(), "  Tokyo Night\n") ||
		!strings.Contains(out.String(), "  custom ") {
		t.Fatalf("list:\n%s", out.String())
	}

	os.WriteFile(filepath.Join(dir, "config"), []byte("font-size = 14\n"), 0o644)
	if err := theme([]string{"tokyo", "NIGHT"}, &out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config"))
	if string(b) != "font-size = 14\napp-theme = Tokyo Night\n" {
		t.Fatalf("file: %q", b)
	}
	out.Reset()
	theme(nil, &out)
	if !strings.Contains(out.String(), "* Tokyo Night\n") {
		t.Fatalf("list after:\n%s", out.String())
	}

	// Custom, from Tokyo Night: seeded from it.
	if err := theme([]string{"Custom"}, &out); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "config"))
	if want := "font-size = 14\napp-theme = custom\nwindow-theme = Match Terminal\ntheme = dark:TokyoNight,light:TokyoNight Day\n" +
		"agent-color = Tokyo Night\nactive-pane-border-color = light:#2E7DE9,dark:#7AA2F7\n"; string(b) != want {
		t.Fatalf("custom: %q\nwant %q", b, want)
	}

	// Back to a theme and to Custom again: the Custom settings are kept.
	theme([]string{"nord"}, &out)
	os.WriteFile(filepath.Join(dir, "config"), []byte("app-theme = Nord\nagent-color = Rose\n"), 0o644)
	if err := theme([]string{"custom"}, &out); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(dir, "config"))
	if string(b) != "app-theme = custom\nagent-color = Rose\n" {
		t.Fatalf("custom again: %q", b)
	}
	if err := theme([]string{"Dracula"}, &out); err == nil || !strings.Contains(err.Error(), "no theme") {
		t.Fatalf("unknown: %v", err)
	}
}
