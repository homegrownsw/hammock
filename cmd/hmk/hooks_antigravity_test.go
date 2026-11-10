package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAntigravityHooksInstall(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".gemini", "config", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// herdr's hook (as it installs it) and one of the person's stay.
	existing := `{"herdr":{"PreInvocation":[{"command":"bash '/Users/a/.gemini/config/hooks/herdr-agent-state.sh' session","timeout":10,"type":"command"}]},
"lint-checker":{"PostToolUse":[{"matcher":"run_command","hooks":[{"type":"command","command":"./scripts/lint.sh"}]}]}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	const exe = "/Applications/Hammock β.app/Contents/Helpers/hmk"
	edits, notes, err := antigravityHookTarget.edits(home, exe, false)
	if err != nil || len(edits) != 1 || !edits[0].changed || len(notes) != 0 {
		t.Fatal(edits, notes, err)
	}
	type handler struct {
		Type, Command string
		Timeout       int
	}
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal(edits[0].new, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["herdr"] == nil || doc["lint-checker"] == nil {
		t.Fatalf("others' hooks lost: %s", edits[0].new)
	}
	for _, ev := range antigravityHookEvents {
		want := "'" + exe + "' hook antigravity " + ev + " || echo '{}'"
		var hs []handler
		if ev == "PostToolUse" {
			var groups []struct {
				Matcher string
				Hooks   []handler
			}
			if err := json.Unmarshal(doc["hammock"][ev], &groups); err != nil || len(groups) != 1 || groups[0].Matcher != "*" {
				t.Fatalf("%s: %s", ev, doc["hammock"][ev])
			}
			hs = groups[0].Hooks
		} else if err := json.Unmarshal(doc["hammock"][ev], &hs); err != nil {
			t.Fatal(err)
		}
		if len(hs) != 1 || hs[0].Command != want || hs[0].Type != "command" || hs[0].Timeout != 5 {
			t.Errorf("%s: %+v", ev, hs)
		}
	}
	if _, ok := doc["hammock"]["PreToolUse"]; ok {
		t.Error("a PreToolUse hook: its answer is a permission decision")
	}

	// Again: no change. Moved: rewritten. Uninstalled: only Hammock's go.
	if err := os.WriteFile(path, edits[0].new, 0o644); err != nil {
		t.Fatal(err)
	}
	again, _, _ := antigravityHookTarget.edits(home, exe, false)
	if again[0].changed {
		t.Fatal("second install changed the file")
	}
	moved, _, _ := antigravityHookTarget.edits(home, "/usr/local/bin/hmk", false)
	if !moved[0].changed || strings.Contains(string(moved[0].new), "Hammock β") || !strings.Contains(string(moved[0].new), "/usr/local/bin/hmk hook antigravity Stop") {
		t.Fatalf("moved: %s", moved[0].new)
	}
	un, _, err := antigravityHookTarget.edits(home, "", true)
	if err != nil || !un[0].changed || bytes.Contains(un[0].new, []byte("hook antigravity")) || !bytes.Contains(un[0].new, []byte("herdr-agent-state.sh")) || !bytes.Contains(un[0].new, []byte("lint.sh")) {
		t.Fatalf("uninstall: %s %v", un[0].new, err)
	}

	// A "hammock" hook that isn't Hammock's is left as it is.
	if err := os.WriteFile(path, []byte(`{"hammock":{"Stop":[{"command":"say done"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, uninstall := range []bool{false, true} {
		edits, notes, err := antigravityHookTarget.edits(home, exe, uninstall)
		if err != nil || len(edits) != 0 || len(notes) != 1 {
			t.Errorf("uninstall %v: %v %v %v", uninstall, edits, notes, err)
		}
	}

	// No file yet: one is written.
	os.Remove(path)
	fresh, _, err := antigravityHookTarget.edits(home, exe, false)
	if err != nil || !fresh[0].changed || !bytes.Contains(fresh[0].new, []byte(`"hammock"`)) {
		t.Fatalf("fresh: %s %v", fresh[0].new, err)
	}
}

// Its hooks print {} whether in a Hammock pane or not.
func TestAntigravityHookAnswers(t *testing.T) {
	t.Setenv("HMK_PANE_ID", "")
	var out bytes.Buffer
	_ = forwardHook([]string{"antigravity", "PreInvocation"}, strings.NewReader(`{"conversationId":"c"}`), &out)
	if out.String() != "{}\n" {
		t.Fatalf("outside Hammock: %q", out.String())
	}
	t.Setenv("HMK_PANE_ID", "p1")
	t.Setenv("HMK_HOOK_SOCKET", filepath.Join(t.TempDir(), "none.sock"))
	out.Reset()
	_ = forwardHook([]string{"antigravity", "Stop"}, strings.NewReader(`{"conversationId":"c"}`), &out)
	if out.String() != "{}\n" {
		t.Fatalf("in Hammock, daemon away: %q", out.String())
	}
}
