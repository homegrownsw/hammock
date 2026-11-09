package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopilotHooksInstall(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".copilot", "hooks", "hammock.json")
	// Someone's own hook files and settings stay as they are.
	mine := filepath.Join(home, ".copilot", "hooks", "mine.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte(`{"version":1,"hooks":{"sessionStart":[{"type":"command","bash":"echo hi"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	edits, notes, err := copilotHookTarget.edits(home, "/Applications/Hammock β.app/Contents/Helpers/hmk", false)
	if err != nil || len(notes) != 0 || len(edits) != 1 || edits[0].path != path || !edits[0].changed {
		t.Fatalf("install: %+v %v %v", edits, notes, err)
	}
	var doc struct {
		Version int
		Hooks   map[string][]struct {
			Type       string
			Bash       string
			TimeoutSec int
		}
	}
	if err := json.Unmarshal(edits[0].new, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 || len(doc.Hooks) != len(copilotHookEvents) {
		t.Fatalf("file: %s", edits[0].new)
	}
	for _, ev := range copilotHookEvents {
		h := doc.Hooks[ev]
		want := "'/Applications/Hammock β.app/Contents/Helpers/hmk' hook copilot " + ev + " || true"
		if len(h) != 1 || h[0].Type != "command" || h[0].Bash != want || h[0].TimeoutSec != 5 {
			t.Errorf("%s: %+v", ev, h)
		}
	}
	if err := os.WriteFile(path, edits[0].new, 0o644); err != nil {
		t.Fatal(err)
	}

	// Installed: nothing to do; after the app moves, repointed.
	edits, _, _ = copilotHookTarget.edits(home, "/Applications/Hammock β.app/Contents/Helpers/hmk", false)
	if edits[0].changed {
		t.Error("second install changes the file")
	}
	edits, _, _ = copilotHookTarget.edits(home, "/usr/local/bin/hmk", false)
	if !edits[0].changed || !strings.Contains(string(edits[0].new), "/usr/local/bin/hmk hook copilot Stop || true") || strings.Contains(string(edits[0].new), "Hammock β") {
		t.Errorf("repoint: %s", edits[0].new)
	}

	// Uninstalling removes Hammock's file only.
	edits, _, _ = copilotHookTarget.edits(home, "", true)
	if len(edits) != 1 || !edits[0].remove || edits[0].path != path {
		t.Errorf("uninstall: %+v", edits)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	edits, notes, _ = copilotHookTarget.edits(home, "/usr/local/bin/hmk", false)
	if len(edits) != 0 || len(notes) != 1 {
		t.Errorf("someone else's hammock.json: %+v %v", edits, notes)
	}
	edits, _, _ = copilotHookTarget.edits(home, "", true)
	if len(edits) != 0 {
		t.Errorf("uninstall removes someone else's hammock.json: %+v", edits)
	}
}
