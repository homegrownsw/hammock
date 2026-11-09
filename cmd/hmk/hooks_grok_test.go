package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/daemon"
)

func TestGrokHooksInstall(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".grok", "hooks", "hammock.json")
	// Someone's own hook files stay as they are.
	mine := filepath.Join(home, ".grok", "hooks", "mine.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	edits, notes, err := grokHookTarget.edits(home, "/Applications/Hammock β.app/Contents/Helpers/hmk", false)
	if err != nil || len(notes) != 0 || len(edits) != 1 || edits[0].path != path || !edits[0].changed {
		t.Fatalf("install: %+v %v %v", edits, notes, err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Matcher *string
			Hooks   []struct {
				Type    string
				Command string
				Timeout int
			}
		}
	}
	if err := json.Unmarshal(edits[0].new, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Hooks) != len(grokHookEvents) {
		t.Fatalf("file: %s", edits[0].new)
	}
	for _, ev := range grokHookEvents {
		g := doc.Hooks[ev]
		want := "'/Applications/Hammock β.app/Contents/Helpers/hmk' hook grok " + ev
		if len(g) != 1 || g[0].Matcher != nil || len(g[0].Hooks) != 1 || g[0].Hooks[0].Type != "command" || g[0].Hooks[0].Command != want || g[0].Hooks[0].Timeout != 5 {
			t.Errorf("%s: %+v", ev, g)
		}
	}
	if err := os.WriteFile(path, edits[0].new, 0o644); err != nil {
		t.Fatal(err)
	}

	// Installed: nothing to do; after the app moves, repointed.
	edits, _, _ = grokHookTarget.edits(home, "/Applications/Hammock β.app/Contents/Helpers/hmk", false)
	if edits[0].changed {
		t.Error("second install changes the file")
	}
	edits, _, _ = grokHookTarget.edits(home, "/usr/local/bin/hmk", false)
	if !edits[0].changed || !strings.Contains(string(edits[0].new), "/usr/local/bin/hmk hook grok StopCancelled") || strings.Contains(string(edits[0].new), "Hammock β") {
		t.Errorf("repoint: %s", edits[0].new)
	}

	// Uninstalling removes Hammock's file only.
	edits, _, _ = grokHookTarget.edits(home, "", true)
	if len(edits) != 1 || !edits[0].remove || edits[0].path != path {
		t.Errorf("uninstall: %+v", edits)
	}
	if err := os.WriteFile(path, []byte(`{"hooks":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	edits, notes, _ = grokHookTarget.edits(home, "/usr/local/bin/hmk", false)
	if len(edits) != 0 || len(notes) != 1 {
		t.Errorf("someone else's hammock.json: %+v %v", edits, notes)
	}
	edits, _, _ = grokHookTarget.edits(home, "", true)
	if len(edits) != 0 {
		t.Errorf("uninstall removes someone else's hammock.json: %+v", edits)
	}
}

// Grok runs Claude Code's hooks from ~/.claude/settings.json as well as
// its own: Hammock's Claude hook keeps out of it, so a Grok pane isn't taken
// for Claude.
func TestClaudeHookInGrok(t *testing.T) {
	dir, err := os.MkdirTemp("", "fh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan daemon.HookEvent, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var ev daemon.HookEvent
			_ = json.NewDecoder(c).Decode(&ev)
			got <- ev
			c.Close()
		}
	}()
	t.Setenv("HMK_PANE_ID", "p1")
	t.Setenv("HMK_HOOK_SOCKET", sock)
	t.Setenv("GROK_HOOK_EVENT", "stop")
	payload := `{"hookEventName":"stop","hook_event_name":"Stop","sessionId":"s1","session_id":"s1","reason":"end_turn"}`
	var out bytes.Buffer
	_ = forwardHook([]string{"claude"}, strings.NewReader(payload), &out)
	_ = forwardHook([]string{"grok", "Stop"}, strings.NewReader(payload), &out)
	if out.Len() != 0 {
		t.Errorf("printed %q", out.String())
	}
	select {
	case ev := <-got:
		if ev.Harness != "grok" || ev.Event != "Stop" {
			t.Fatalf("forwarded %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Grok's own hook not forwarded")
	}
	select {
	case ev := <-got:
		t.Fatalf("also forwarded %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}
