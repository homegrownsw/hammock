package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKiroHooksInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIRO_HOME", "")
	target := hookTargets["kiro"]
	path := filepath.Join(home, ".kiro", "hooks", "hammock.json")
	// The person's own hook files and agents stay as they are.
	mine := filepath.Join(home, ".kiro", "hooks", "lint.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte(`{"version":"v1","hooks":[{"name":"lint","trigger":"Stop","action":{"type":"command","command":"make lint"}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	const exe = "/Applications/Hammock β.app/Contents/Helpers/hmk"
	edits, notes, err := target.edits(home, exe, false)
	if err != nil || len(notes) != 0 || len(edits) != 1 || edits[0].path != path || !edits[0].changed {
		t.Fatalf("install: %+v %v %v", edits, notes, err)
	}
	var doc struct {
		Version string
		Hooks   []struct {
			Name, Description, Trigger string
			Matcher                    *string
			Action                     struct{ Type, Command string }
			Timeout                    int
		}
	}
	if err := json.Unmarshal(edits[0].new, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != "v1" || len(doc.Hooks) != len(kiroHookEvents) {
		t.Fatalf("file: %s", edits[0].new)
	}
	for i, ev := range kiroHookEvents {
		h := doc.Hooks[i]
		want := "'" + exe + "' hook kiro " + ev + " || true"
		if h.Trigger != ev || h.Name != "hammock-"+ev || h.Action.Type != "command" || h.Action.Command != want || h.Timeout != 5 ||
			h.Matcher != nil || !strings.Contains(h.Description, pluginMarker) {
			t.Errorf("%s: %+v", ev, h)
		}
	}
	if err := os.WriteFile(path, edits[0].new, 0o644); err != nil {
		t.Fatal(err)
	}

	// Installed: nothing to do; after the app moves, repointed.
	if edits, _, _ = target.edits(home, exe, false); edits[0].changed {
		t.Error("second install changes the file")
	}
	edits, _, _ = target.edits(home, "/usr/local/bin/hmk", false)
	if !edits[0].changed || !strings.Contains(string(edits[0].new), "/usr/local/bin/hmk hook kiro Stop || true") || strings.Contains(string(edits[0].new), "Hammock β") {
		t.Errorf("repoint: %s", edits[0].new)
	}

	// Uninstalling removes Hammock's file only.
	edits, _, _ = target.edits(home, "", true)
	if len(edits) != 1 || !edits[0].remove || edits[0].path != path {
		t.Errorf("uninstall: %+v", edits)
	}
	if err := os.WriteFile(path, []byte(`{"version":"v1","hooks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if edits, notes, _ = target.edits(home, "/usr/local/bin/hmk", false); len(edits) != 0 || len(notes) != 1 {
		t.Errorf("someone else's hammock.json: %+v %v", edits, notes)
	}
	if edits, _, _ = target.edits(home, "", true); len(edits) != 0 {
		t.Errorf("uninstall removes someone else's hammock.json: %+v", edits)
	}

	// $KIRO_HOME moves it.
	kh := filepath.Join(t.TempDir(), "kiro")
	t.Setenv("KIRO_HOME", kh)
	if edits, _, _ = target.edits(home, exe, false); len(edits) != 1 || edits[0].path != filepath.Join(kh, "hooks", "hammock.json") {
		t.Errorf("KIRO_HOME: %+v", edits)
	}
}
