package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestClineHookTarget(t *testing.T) {
	home := t.TempDir()
	const exe = "/Applications/Hammock.app/Contents/Helpers/hmk"
	target := hookTargets["cline"]
	edits, notes, err := target.edits(home, exe, false)
	if err != nil || len(notes) != 0 || len(edits) != len(clineHookEvents) {
		t.Fatalf("edits %d notes %v err %v", len(edits), notes, err)
	}
	for i, ev := range clineHookEvents {
		e := edits[i]
		if e.path != filepath.Join(home, ".cline", "hooks", ev+".bash") || !e.changed {
			t.Fatalf("%s: %s changed %v", ev, e.path, e.changed)
		}
		s := string(e.new)
		if !strings.HasPrefix(s, "#!/bin/sh\n") || !strings.Contains(s, pluginMarker) || !strings.Contains(s, hookCommand(exe, "cline", ev)+" >/dev/null 2>&1 || true") ||
			!strings.HasSuffix(s, "echo '{}'\n") {
			t.Errorf("%s:\n%s", ev, s)
		}
		if (i == 0) != (len(e.changes) == 1) {
			t.Errorf("%s: changes %v", ev, e.changes)
		}
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
	if !moved[3].changed || !strings.Contains(string(moved[3].new), "/usr/local/bin/hmk hook cline TaskComplete") {
		t.Errorf("not repointed: %s", moved[3].new)
	}

	// The person's own hook for an event, by the same name, is left alone;
	// theirs by Cline's usual names aren't Hammock's business.
	mine := filepath.Join(home, ".cline", "hooks", "PreToolUse.sh")
	_ = os.WriteFile(mine, []byte("#!/bin/sh\necho '{}'\n"), 0o644)
	_ = os.WriteFile(filepath.Join(home, ".cline", "hooks", "PostToolUse.bash"), []byte("#!/bin/sh\necho '{}'\n"), 0o644)
	theirs, notes, _ := target.edits(home, exe, false)
	if len(notes) != 1 || len(theirs) != len(clineHookEvents)-1 {
		t.Errorf("theirs: %d edits, notes %v", len(theirs), notes)
	}

	// Uninstalling removes only Hammock's.
	gone, _, _ := target.edits(home, "", true)
	if len(gone) != len(clineHookEvents)-1 {
		t.Fatalf("uninstall: %+v", gone)
	}
	for _, e := range gone {
		if !e.remove || strings.HasSuffix(e.path, "PostToolUse.bash") || e.path == mine {
			t.Errorf("%s: remove %v", e.path, e.remove)
		}
	}
}

// The script, run as Cline runs it (sh, the event on stdin), forwards the
// event in a pane, not in Cline's hub, and always answers {}.
func TestClineHookScript(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	fake := filepath.Join(dir, "hmk")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\" >> '"+log+"'\ncat >> '"+log+"'\necho noise\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "PreToolUse.bash")
	_ = os.WriteFile(script, clineHookScript("PreToolUse")(fake), 0o644)
	run := func(env ...string) string {
		cmd := exec.Command("sh", script)
		cmd.Env = append([]string{"PATH=/usr/bin:/bin"}, env...)
		cmd.Stdin = strings.NewReader(`{"hookName":"tool_call"}`)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if out := run(); out != "{}\n" {
		t.Errorf("outside Hammock: %q", out)
	}
	if out := run("HMK_PANE_ID=p1", "CLINE_NO_INTERACTIVE=1"); out != "{}\n" {
		t.Errorf("in the hub: %q", out)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("forwarded outside a pane")
	}
	if out := run("HMK_PANE_ID=p1"); out != "{}\n" {
		t.Errorf("in a pane: %q", out)
	}
	if b, _ := os.ReadFile(log); string(b) != "hook cline PreToolUse\n{\"hookName\":\"tool_call\"}" {
		t.Errorf("forwarded %q", b)
	}
}
