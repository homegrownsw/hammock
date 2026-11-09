package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// openCodeApplyEdits does what `hmk hooks install` does with a target's edits.
func openCodeApplyEdits(t *testing.T, target hookTarget, home, exe string, uninstall bool) []configEdit {
	t.Helper()
	edits, _, err := target.edits(home, exe, uninstall)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edits {
		if e.remove {
			if err := os.Remove(e.path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if e.changed {
			if err := os.MkdirAll(filepath.Dir(e.path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(e.path, e.new, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if target.apply != nil {
		if err := target.apply(exe, uninstall, false); err != nil {
			t.Fatal(err)
		}
	}
	return edits
}

func TestOpenCodePluginInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	plugins := filepath.Join(home, ".config", "opencode", "plugins")
	// Someone's own plugin beside Hammock's is left alone.
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugins, "mine.js"), []byte("export default {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := hookTargets["opencode"]
	exe := `/Applications/Hammock "β".app/Contents/Helpers/hmk`
	edits := openCodeApplyEdits(t, target, home, exe, false)
	var said []string
	for _, e := range edits {
		said = append(said, e.changes...)
	}
	if len(said) != 1 {
		t.Errorf("changes = %q, want one line for the three files", said)
	}
	for _, f := range []string{"hammock/tui.js", "hammock/index.js", "hammock.js"} {
		b, err := os.ReadFile(filepath.Join(plugins, f))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), pluginMarker) {
			t.Errorf("%s has no marker", f)
		}
		if f != "hammock/index.js" && !strings.Contains(string(b), `const HMK = "/Applications/Hammock \"β\".app/Contents/Helpers/hmk"`) {
			t.Errorf("%s doesn't run this hmk:\n%s", f, b)
		}
		if f != "hammock/index.js" && !strings.Contains(string(b), `process.env.HMK_PANE_ID`) {
			t.Errorf("%s doesn't check it's in a Hammock pane", f)
		}
	}
	tui, _ := os.ReadFile(filepath.Join(plugins, "hammock", "tui.js"))
	if !strings.Contains(string(tui), `const HARNESS = "opencode"`) || !strings.Contains(string(tui), "session.execution.succeeded") {
		t.Error("TUI plugin")
	}
	if again, _, _ := target.edits(home, exe, false); openCodeAnyChanged(again) {
		t.Error("a second install would change something")
	}
	if moved, _, _ := target.edits(home, "/usr/local/bin/hmk", false); !openCodeAnyChanged(moved) {
		t.Error("a moved hmk doesn't rewrite the plugin")
	}

	openCodeApplyEdits(t, target, home, exe, true)
	if _, err := os.Stat(filepath.Join(plugins, "hammock")); !os.IsNotExist(err) {
		t.Error("plugin folder left behind")
	}
	if _, err := os.Stat(filepath.Join(plugins, "hammock.js")); !os.IsNotExist(err) {
		t.Error("1.x plugin left behind")
	}
	if _, err := os.Stat(filepath.Join(plugins, "mine.js")); err != nil {
		t.Error("removed someone else's plugin")
	}
}

func TestKiloPluginInstallLeavesOthersFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".config", "kilo", "plugin", "hammock.js")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file of the same name that isn't Hammock's isn't touched.
	if err := os.WriteFile(path, []byte("// mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := hookTargets["kilo"]
	edits, notes, _ := target.edits(home, "/x/hmk", false)
	if len(edits) != 0 || len(notes) != 1 {
		t.Fatalf("edits %v, notes %q: want it left as it is", edits, notes)
	}
	os.Remove(path)
	openCodeApplyEdits(t, target, home, "/x/hmk", false)
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `const HARNESS = "kilo"`) || !strings.Contains(string(b), `server: HammockPlugin`) {
		t.Errorf("Kilo plugin:\n%s", b)
	}
	openCodeApplyEdits(t, target, home, "/x/hmk", true)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("not removed")
	}
}

// The plugins are JavaScript modules node (and so Bun) can parse.
func TestPluginsParse(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	dir := t.TempDir()
	for name, src := range map[string][]byte{
		"tui.mjs":    openCodeTUIPlugin("/x/hmk"),
		"index.mjs":  openCodeServerStub("/x/hmk"),
		"server.mjs": openCodeServerPlugin("/x/hmk", "kilo", "Kilo CLI"),
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, src, 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(node, "--check", p).CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", name, err, out)
		}
	}
}

func openCodeAnyChanged(edits []configEdit) bool {
	for _, e := range edits {
		if e.changed {
			return true
		}
	}
	return false
}
