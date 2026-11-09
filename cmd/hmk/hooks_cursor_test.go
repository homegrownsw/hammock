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

func TestCursorHooksInstall(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".cursor", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// herdr's hook (as installed on this Mac) stays.
	existing := `{"hooks":{"sessionStart":[{"command":"bash '/Users/a/.cursor/herdr-agent-state.sh' session"}]},"version":1}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	target := hookTargets["cursor"]
	edits, _, err := target.edits(home, "/Applications/Hammock β.app/Contents/Helpers/hmk", false)
	if err != nil || len(edits) != 1 || !edits[0].changed {
		t.Fatal(edits, err)
	}
	var doc struct {
		Version int
		Hooks   map[string][]struct {
			Command string
			Timeout int
		}
	}
	if err := json.Unmarshal(edits[0].new, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 {
		t.Errorf("version %d", doc.Version)
	}
	want := "'/Applications/Hammock β.app/Contents/Helpers/hmk' hook cursor"
	for _, ev := range []string{"sessionStart", "sessionEnd", "beforeSubmitPrompt", "beforeShellExecution", "afterShellExecution", "beforeMCPExecution", "afterMCPExecution", "afterFileEdit", "stop"} {
		n := 0
		for _, h := range doc.Hooks[ev] {
			if h.Command == want {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: %d Hammock hooks", ev, n)
		}
	}
	if len(doc.Hooks["sessionStart"]) != 2 || !strings.Contains(string(edits[0].new), "herdr-agent-state.sh") {
		t.Fatalf("herdr's hook lost: %s", edits[0].new)
	}

	// Installing again changes nothing; uninstalling keeps herdr's only.
	if err := os.WriteFile(path, edits[0].new, 0o644); err != nil {
		t.Fatal(err)
	}
	again, _, _ := target.edits(home, "/Applications/Hammock β.app/Contents/Helpers/hmk", false)
	if again[0].changed {
		t.Fatal("second install changed the file")
	}
	un, _, err := target.edits(home, "", true)
	if err != nil || !un[0].changed || strings.Contains(string(un[0].new), "hook cursor") || !strings.Contains(string(un[0].new), "herdr-agent-state.sh") {
		t.Fatalf("uninstall: %s %v", un[0].new, err)
	}
}

// A Cursor hook always answers {}, which leaves Cursor's decision as it
// was, inside Hammock or not; Hammock's Claude hook, which Cursor also runs,
// doesn't pass Cursor's events on as Claude's.
func TestCursorHookAnswersNeutrally(t *testing.T) {
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
	payload := `{"conversation_id":"c1","generation_id":"g1","hook_event_name":"beforeShellExecution","command":"ls","cwd":"/w","cursor_version":"2026.11.05","workspace_roots":["/w"]}`

	t.Setenv("HMK_PANE_ID", "")
	t.Setenv("PERCOLATOR_PANE_ID", "")
	var out bytes.Buffer
	_ = forwardHook([]string{"cursor"}, strings.NewReader(payload), &out)
	if out.String() != "{}\n" {
		t.Fatalf("outside Hammock: %q", out.String())
	}

	t.Setenv("HMK_PANE_ID", "p1")
	t.Setenv("HMK_HOOK_SOCKET", sock)
	out.Reset()
	_ = forwardHook([]string{"cursor"}, strings.NewReader(payload), &out)
	if out.String() != "{}\n" {
		t.Fatalf("in Hammock: %q", out.String())
	}
	select {
	case ev := <-got:
		if ev.Harness != "cursor" || ev.PaneID != "p1" || !strings.Contains(string(ev.Payload), "beforeShellExecution") {
			t.Fatalf("forwarded %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("not forwarded")
	}

	out.Reset()
	_ = forwardHook([]string{"claude"}, strings.NewReader(payload), &out)
	if out.String() != "{}\n" {
		t.Fatalf("claude hook in Cursor: %q", out.String())
	}
	out.Reset()
	_ = forwardHook([]string{"claude"}, strings.NewReader(`{"session_id":"s","hook_event_name":"Stop"}`), &out)
	if out.Len() != 0 {
		t.Fatalf("claude hook printed %q", out.String())
	}
	for _, want := range []string{"claude"} {
		select {
		case ev := <-got:
			if ev.Harness != want || !strings.Contains(string(ev.Payload), "Stop") {
				t.Fatalf("forwarded %+v", ev)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Claude's own hook not forwarded")
		}
	}
	select {
	case ev := <-got:
		t.Fatalf("also forwarded %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}
