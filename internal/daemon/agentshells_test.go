package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// TestBackgroundShells: the main view lists the session's background
// shells, and watching one shows its output as it's written, escape
// sequences dropped.
func TestBackgroundShells(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()

	sess := allowTranscripts(t)
	tasks := filepath.Join(t.TempDir(), "tasks")
	if err := os.MkdirAll(tasks, 0o700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tasks, "b12ab.output")
	if err := os.WriteFile(out, []byte("\x1b[32mready\x1b[0m on :3000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(sess, "s.jsonl")
	lines := `{"type":"user","uuid":"u0","timestamp":"2026-11-03T10:00:00Z","message":{"role":"user","content":"start it"}}` + "\n" +
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-03T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"npm run dev","description":"Start the dev server","run_in_background":true}}]}}` + "\n" +
		`{"type":"user","uuid":"u1","timestamp":"2026-11-03T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"Command running in background with ID: b12ab. Output is being written to: ` + out + `. You will be notified."}]},"toolUseResult":{"stdout":"","backgroundTaskId":"b12ab"}}` + "\n"
	if err := os.WriteFile(main, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","session_id":"s","prompt":"go","transcript_path":"` + main + `"}`)})

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: paneID, Watch: true}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		v := ev.GetAgentView()
		// /bin/cat isn't an agent running it, so it's listed as ended.
		return v != nil && v.ShellId == "" && len(v.Shells) == 1 && v.Shells[0].Id == "b12ab" &&
			v.Shells[0].Command == "npm run dev" && v.Shells[0].Description == "Start the dev server"
	})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: paneID, Watch: true, ShellId: "b12ab"}}})
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		v := ev.GetAgentView()
		return v != nil && v.ShellId == "b12ab" && v.Replace && v.ShellOutput == "ready on :3000\n"
	})
	f, err := os.OpenFile(out, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("GET / 200\n\x1b[3")
	f.WriteString("1merror\x1b[0m\n")
	f.Close()
	got := ""
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		v := ev.GetAgentView()
		if v == nil || v.ShellId != "b12ab" || v.Replace {
			return false
		}
		got += v.ShellOutput
		return strings.HasSuffix(got, "error\n")
	})
	if got != "GET / 200\nerror\n" {
		t.Fatalf("appended %q", got)
	}
	// Only a shell's own file, by an id that stays in its folder.
	if r, err := c.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: paneID, Watch: true, ShellId: "../x"}}}); err == nil && r.GetError() == nil {
		t.Fatal("../x accepted as a shell id")
	}
}

func TestCleanOutput(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[1;32mok\x1b[0m\r\n":               "ok\n",
		"\x1b]8;;https://x\x07link\x1b]8;;\x07": "link",
		"50%\r100%\n":                           "50%\r100%\n",
		"a\x08b\tc":                             "ab\tc",
	} {
		if got := cleanOutput([]byte(in)); got != want {
			t.Errorf("cleanOutput(%q) = %q, want %q", in, got, want)
		}
	}
	ready, held := splitHeld([]byte("ok\x1b[3"))
	if string(ready) != "ok" || string(held) != "\x1b[3" {
		t.Errorf("split escape: %q %q", ready, held)
	}
	ready, held = splitHeld([]byte("caf\xc3"))
	if string(ready) != "caf" || string(held) != "\xc3" {
		t.Errorf("split rune: %q %q", ready, held)
	}
}
