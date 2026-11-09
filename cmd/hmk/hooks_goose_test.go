package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGooseHookTarget(t *testing.T) {
	home := t.TempDir()
	const exe = "/Applications/Hammock.app/Contents/Helpers/hmk"
	target := hookTargets["goose"]
	edits, notes, err := target.edits(home, exe, false)
	if err != nil || len(notes) != 0 || len(edits) != 3 {
		t.Fatalf("edits %d notes %v err %v", len(edits), notes, err)
	}
	dir := filepath.Join(home, goosePluginDir)
	for i, name := range []string{"plugin.json", filepath.Join("hooks", "hooks.json"), ".mcp.json"} {
		if e := edits[i]; e.path != filepath.Join(dir, name) || !e.changed || !strings.Contains(string(e.new), pluginMarker) {
			t.Fatalf("%s: %s changed %v\n%s", name, e.path, e.changed, e.new)
		}
	}

	// hooks.json, as goose's HooksFile reads it.
	var hooks struct {
		Hooks map[string][]struct {
			Matcher *string `json:"matcher"`
			Hooks   []struct {
				Type, Command string
				Timeout       int
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(edits[1].new, &hooks); err != nil {
		t.Fatal(err)
	}
	if len(hooks.Hooks) != len(gooseHookEvents) {
		t.Errorf("events %v", hooks.Hooks)
	}
	for _, ev := range gooseHookEvents {
		rules := hooks.Hooks[ev]
		if len(rules) != 1 || rules[0].Matcher != nil || len(rules[0].Hooks) != 1 {
			t.Fatalf("%s: %+v", ev, rules)
		}
		h := rules[0].Hooks[0]
		if h.Type != "command" || h.Command != hookCommand(exe, "goose", ev) || h.Timeout != 5 {
			t.Errorf("%s: %+v", ev, h)
		}
	}
	var mcp struct {
		McpServers map[string]struct {
			Command string
			Args    []string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(edits[2].new, &mcp); err != nil || mcp.McpServers["hammock"].Command != exe || strings.Join(mcp.McpServers["hammock"].Args, " ") != "mcp" {
		t.Errorf("mcp %s", edits[2].new)
	}

	// Installed, a second install changes nothing; moved, it repoints.
	for _, e := range edits {
		_ = os.MkdirAll(filepath.Dir(e.path), 0o755)
		_ = os.WriteFile(e.path, e.new, 0o644)
	}
	again, _, _ := target.edits(home, exe, false)
	for _, e := range again {
		if e.changed {
			t.Errorf("%s changed on reinstall", e.path)
		}
	}
	moved, _, _ := target.edits(home, "/usr/local/bin/hmk", false)
	if !moved[1].changed || !strings.Contains(string(moved[1].new), "/usr/local/bin/hmk hook goose Stop") {
		t.Errorf("not repointed: %s", moved[1].new)
	}

	// Uninstalling removes Hammock's files.
	gone, _, _ := target.edits(home, "", true)
	if len(gone) != 3 {
		t.Fatalf("uninstall: %+v", gone)
	}
	for _, e := range gone {
		if !e.remove {
			t.Errorf("%s not removed", e.path)
		}
	}

	// A plugin of the same name that isn't Hammock's is left alone.
	_ = os.WriteFile(filepath.Join(dir, "hooks", "hooks.json"), []byte(`{"hooks":{}}`), 0o644)
	theirs, notes, _ := target.edits(home, exe, false)
	if len(notes) != 1 || len(theirs) != 2 {
		t.Errorf("theirs: %d edits, notes %v", len(theirs), notes)
	}
}
