package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Hammock's Amp plugin goes into ~/.config/amp/plugins beside yours, carries
// this hmk's path, is rewritten when that changes, and is the only one
// uninstalling removes. A file of that name that isn't Hammock's is left be.
func TestAmpHookTarget(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "amp", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(dir, "mine.ts")
	_ = os.WriteFile(mine, []byte("export default function (amp) {}\n"), 0o644)
	hmk := filepath.Join(dir, "hammock.ts")
	target := hookTargets["amp"]
	apply := func(exe string, uninstall bool) configEdit {
		t.Helper()
		edits, notes, err := target.edits(home, exe, uninstall)
		if err != nil || len(edits) != 1 || len(notes) != 0 {
			t.Fatalf("edits %v, notes %v, err %v", edits, notes, err)
		}
		e := edits[0]
		if e.path != hmk {
			t.Fatalf("path %s", e.path)
		}
		switch {
		case e.remove:
			_ = os.Remove(e.path)
		case e.changed:
			_ = os.WriteFile(e.path, e.new, 0o644)
		}
		return e
	}
	exe := "/Applications/Hammock β.app/Contents/Helpers/hmk"
	if e := apply(exe, false); !e.changed || len(e.changes) != 1 {
		t.Fatalf("install: %+v", e)
	}
	src, _ := os.ReadFile(hmk)
	for _, want := range []string{pluginMarker, `const HMK = "` + exe + `"`, `spawn(HMK, ["hook", "amp", event]`, "process.env.HMK_PANE_ID",
		`amp.on("tool.call"`, `return { action: "allow" }`, `.state.subscribe(`, "export default function (amp)"} {
		if !strings.Contains(string(src), want) {
			t.Errorf("plugin lacks %q", want)
		}
	}
	if strings.Contains(string(src), "__") {
		t.Errorf("placeholder left in:\n%s", src)
	}
	if e := apply(exe, false); e.changed {
		t.Error("reinstalling changed it")
	}
	if e := apply("/usr/local/bin/hmk", false); !e.changed {
		t.Error("another hmk didn't rewrite it")
	}
	if e := apply("", true); !e.remove {
		t.Errorf("uninstall: %+v", e)
	}
	if _, err := os.Stat(hmk); !os.IsNotExist(err) {
		t.Error("plugin left behind")
	}
	if _, err := os.Stat(mine); err != nil {
		t.Error("your plugin went")
	}

	// Someone else's hammock.ts.
	_ = os.WriteFile(hmk, []byte("export default function (amp) {}\n"), 0o644)
	edits, notes, err := target.edits(home, exe, false)
	if err != nil || len(edits) != 0 || len(notes) != 1 {
		t.Fatalf("not Hammock's: %v %v %v", edits, notes, err)
	}
}

// The plugin is valid JavaScript (Amp runs it under Bun, as TypeScript,
// which plain JavaScript is).
func TestAmpPluginParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	path := filepath.Join(t.TempDir(), "hammock.mjs")
	if err := os.WriteFile(path, ampPlugin("/x/it's hmk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}
