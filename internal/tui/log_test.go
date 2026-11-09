package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogSource(t *testing.T) {
	home, _ := os.UserHomeDir()
	for in, want := range map[string]string{
		"file ~/x.log":  "file " + filepath.Join(home, "x.log"),
		"macos Safari":  "macos Safari",
		"sim":           "simulator ",
		"logcat emu-1":  "logcat emu-1",
		"pnpm dev":      "command pnpm dev",
		"tail -f a.log": "command tail -f a.log",
	} {
		s := logSource(in)
		got := s.Kind + " "
		switch s.Kind {
		case "file":
			got += s.Path
		case "macos":
			got += s.Process
		case "simulator", "logcat":
			got += s.Device
		default:
			got += s.Command[0]
		}
		if got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if logSource("  ") != nil || logSource("file") != nil {
		t.Error("empty sources opened")
	}
}
