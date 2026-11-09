package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiHookTarget(t *testing.T) {
	home := t.TempDir()
	mcp := filepath.Join(home, ".pi", "agent", "mcp.json")
	_ = os.MkdirAll(filepath.Dir(mcp), 0o755)
	if err := os.WriteFile(mcp, []byte(`{"mcpServers":{"docs":{"url":"https://example.com/mcp"}},"other":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	target := hookTargets["pi"]
	edits, notes, err := target.edits(home, "/Applications/Hammock.app/Contents/Helpers/hmk", false)
	if err != nil || len(notes) != 0 || len(edits) != 2 {
		t.Fatalf("edits %d notes %v err %v", len(edits), notes, err)
	}
	ext, cfg := edits[0], edits[1]
	if ext.path != filepath.Join(home, piExtensionFile) || !ext.changed {
		t.Fatalf("extension edit: %s %v", ext.path, ext.changed)
	}
	src := string(ext.new)
	for _, want := range []string{pluginMarker, `const HMK = "/Applications/Hammock.app/Contents/Helpers/hmk";`, `spawn(HMK, ["hook", "pi", event]`,
		"process.env.HMK_PANE_ID", `ctx.mode === "tui"`, `pi.on("agent_settled"`, `pi.on("before_agent_start"`} {
		if !strings.Contains(src, want) {
			t.Errorf("extension lacks %q", want)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(cfg.new, &doc); err != nil {
		t.Fatal(err)
	}
	servers := doc["mcpServers"].(map[string]any)
	if servers["docs"] == nil || doc["other"] == nil {
		t.Fatalf("lost the person's config: %s", cfg.new)
	}
	if f := servers["hammock"].(map[string]any); f["command"] != "/Applications/Hammock.app/Contents/Helpers/hmk" {
		t.Fatalf("hmk server: %v", f)
	}

	// Installed, a second install changes nothing.
	_ = os.MkdirAll(filepath.Dir(ext.path), 0o755)
	_ = os.WriteFile(ext.path, ext.new, 0o644)
	_ = os.WriteFile(mcp, cfg.new, 0o644)
	again, _, _ := target.edits(home, "/Applications/Hammock.app/Contents/Helpers/hmk", false)
	for _, e := range again {
		if e.changed {
			t.Errorf("%s changed on reinstall", e.path)
		}
	}
	// Moved, it points both at the new copy.
	moved, _, _ := target.edits(home, "/usr/local/bin/hmk", false)
	if !moved[0].changed || !bytes.Contains(moved[0].new, []byte(`"/usr/local/bin/hmk"`)) || !moved[1].changed {
		t.Errorf("not repointed")
	}

	// Uninstalling removes the extension and Hammock's server only.
	gone, _, _ := target.edits(home, "", true)
	if len(gone) != 2 || !gone[0].remove || !gone[1].changed {
		t.Fatalf("uninstall: %+v", gone)
	}
	if bytes.Contains(gone[1].new, []byte(`"hammock"`)) || !bytes.Contains(gone[1].new, []byte(`"docs"`)) {
		t.Errorf("uninstall mcp.json: %s", gone[1].new)
	}

	// Someone else's hammock.ts, or their own "hammock" server, is left alone.
	_ = os.WriteFile(ext.path, []byte("export default function () {}\n"), 0o644)
	_ = os.WriteFile(mcp, []byte(`{"mcpServers":{"hammock":{"command":"npx","args":["hmk-mcp"]}}}`), 0o644)
	theirs, notes, _ := target.edits(home, "/usr/local/bin/hmk", false)
	if len(notes) != 1 || len(theirs) != 1 || theirs[0].changed {
		t.Errorf("theirs: %+v notes %v", theirs, notes)
	}
}
