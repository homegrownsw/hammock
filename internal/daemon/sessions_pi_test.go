package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// An agent with no codec (Pi) still resumes its own sessions, with its own
// command; continuing one in another agent isn't offered.
func TestResumeSessionOfAgentWithoutCodec(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "pi", "sessions")
	setAgentRoots(t, map[string][]string{"pi": {root}})
	work := t.TempDir()
	dir := filepath.Join(root, "--work--")
	os.MkdirAll(dir, 0o755)
	id := "01a1b577-f833-7289-8cf5-ab39191246a3"
	os.WriteFile(filepath.Join(dir, "2026-11-07T10-05-00-853Z_"+id+".jsonl"), []byte(
		`{"type":"session","version":3,"id":"`+id+`","timestamp":"2026-11-07T10:05:00.853Z","cwd":"`+work+`"}`+"\n"+
			`{"type":"message","id":"2e712929","parentId":null,"timestamp":"2026-11-07T10:05:00.894Z","message":{"role":"user","content":[{"type":"text","text":"polish the brass dirigible"}],"timestamp":1794045900892}}`+"\n"), 0o600)
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "pi"), []byte("#!/bin/sh\necho RESUMED \"$@\"\n"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")

	sock := shortDir(t)
	d := startDaemon(t, sock)
	defer d.Shutdown()
	c := dial(t, sock)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}})

	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		list := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{Query: "dirigible"}}}).GetSessionList()
		if !list.Indexing {
			if len(list.Sessions) != 1 || list.Sessions[0].Harness != "pi" || list.Sessions[0].Id != id {
				t.Fatalf("list: %v", list)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("still indexing")
		}
	}
	if r, err := c.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_ResumeSession{ResumeSession: &pb.ResumeSession{Id: id, TargetHarness: "claude"}}}); err == nil && r.GetError() == nil {
		t.Fatal("continued a Pi session in Claude Code")
	}
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ResumeSession{ResumeSession: &pb.ResumeSession{Id: id}}}).GetSessionResumed()
	if r == nil || r.PaneId == "" {
		t.Fatalf("resumed: %v", r)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: r.PaneId}}})
	waitOutput(t, c, r.PaneId, "RESUMED --session "+id)
}
