package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// TestSessionLink: a Claude session's Remote Control link is found as a turn
// starts, from the file Claude Code keeps for its process, and goes when
// Remote Control does.
func TestSessionLink(t *testing.T) {
	conf := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", conf)
	sessions := filepath.Join(conf, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sessions, "4242.json")
	write := func(body string) {
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()

	link := func() string {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.panes[paneID].snapshotInfo().Agent.GetSessionUrl()
	}
	waitLink := func(want string) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if link() == want {
				return
			}
		}
		t.Fatalf("session link = %q, want %q", link(), want)
	}
	hook := func(event string) {
		sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"` + event + `","session_id":"s1"}`)})
	}

	write(`{"pid":4242,"sessionId":"s1","updatedAt":1,"bridgeSessionId":"session_01Abc"}`)
	hook("UserPromptSubmit")
	waitLink("https://claude.ai/code/session_01Abc")

	write(`{"pid":4242,"sessionId":"s1","updatedAt":2}`) // Remote Control turned off
	hook("Stop")
	waitLink("")
}
