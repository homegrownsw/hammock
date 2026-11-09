package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/thread"
)

// fakeCodec reads a tiny line format and "resumes" by echoing.
type fakeCodec struct{ name, dir string }

func (f fakeCodec) Harness() string { return f.name }
func (f fakeCodec) Decode(r io.Reader) (thread.Thread, []thread.Record, error) {
	b, _ := io.ReadAll(r)
	return thread.Thread{Harness: f.name, NativeID: "src-1"}, []thread.Record{{Kind: thread.KindUser, Text: string(b)}}, nil
}
func (f fakeCodec) Encode(t thread.Thread, recs []thread.Record) (string, string, []byte, error) {
	b, _ := json.Marshal(recs)
	return "converted-1", filepath.Join(f.dir, "out.jsonl"), b, nil
}
func (f fakeCodec) ResumeCommand(id string) []string {
	return []string{"echo", "resumed-" + f.name, id}
}

func TestSwitchAgentConvertsAndResumes(t *testing.T) {
	dir := shortDir(t)
	bin := filepath.Join(dir, "bin")
	os.MkdirAll(bin, 0o755)
	// A fake "claude": a copy of sleep, so the daemon sees a process named claude.
	if out, err := exec.Command("cp", "/bin/sleep", filepath.Join(bin, "claude")).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	roots := allowTranscripts(t)
	transcript := filepath.Join(roots, "t.jsonl")
	os.WriteFile(transcript, []byte("fix the bug"), 0o600)
	thread.Register(fakeCodec{name: "claude", dir: roots})
	thread.Register(fakeCodec{name: "codex", dir: roots})

	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	sid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: sid, Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-i"}, Env: map[string]string{"PS1": "$ ", "ENV": ""}},
	}}}))
	d.mu.Lock()
	pid := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	waitPaneStarted(t, d, pid)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: pid}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pid, Data: []byte(fmt.Sprintf("%s/claude 300\r", bin))}}})
	sendHook(t, dir, HookEvent{PaneID: pid, Harness: "claude", Payload: json.RawMessage(
		fmt.Sprintf(`{"hook_event_name":"SessionStart","session_id":"src-1","transcript_path":%q}`, transcript))})

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SwitchAgent{SwitchAgent: &pb.SwitchAgent{PaneId: pid, TargetHarness: "codex"}}})
	waitOutput(t, c, pid, "resumed-codex converted-1")
	if b, err := os.ReadFile(filepath.Join(roots, "out.jsonl")); err != nil || len(b) == 0 {
		t.Fatalf("converted session not written: %v", err)
	}
	d.mu.Lock()
	h := d.panes[pid].snapshotInfo().GetAgent().GetHarness()
	d.mu.Unlock()
	if h != "codex" {
		t.Fatalf("pane agent is %q", h)
	}
	// The converted session is recorded, so switching back works with no
	// hook from the new agent (Codex without Hammock's hooks).
	d.mu.Lock()
	a := d.panes[pid].snapshotInfo().GetAgent()
	d.mu.Unlock()
	if a.SessionId != "converted-1" || a.TranscriptPath != filepath.Join(roots, "out.jsonl") || !a.Heuristic {
		t.Fatalf("agent after switching: %v", a)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SwitchAgent{SwitchAgent: &pb.SwitchAgent{PaneId: pid, TargetHarness: "claude"}}})
	waitOutput(t, c, pid, "resumed-claude converted-1")
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tid}}})
}
