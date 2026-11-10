package daemon

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// A pane whose holder ends before it listens fails at once, not after
// holderConnectTimeout, and says why in start_error (#43): its exit status
// alone (-1) reads like a program killed by a signal, so hmk --wait couldn't
// tell a caller its command never ran.
func TestPaneHolderEndsBeforeListening(t *testing.T) {
	dir := shortDir(t)
	holderPath := filepath.Join(dir, "broken-holder")
	if err := os.WriteFile(holderPath, []byte("#!/bin/sh\necho 'pane-holder: something broke' >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := New(Config{Dir: dir, HolderPath: holderPath, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(d.Shutdown)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if nc, err := net.Dial("unix", SocketPath(dir)); err == nil {
			nc.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
	}
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	start := time.Now()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"true"}, Cwd: t.TempDir()},
	}}})
	var failed *pb.Pane
	waitEvent(t, c, func(m *pb.ServerMessage) bool {
		for _, p := range m.GetState().GetPanes() {
			if p.Exited {
				failed = p
				return true
			}
		}
		return false
	})
	if took := time.Since(start); took >= holderConnectTimeout {
		t.Errorf("took %v to notice the holder had ended", took)
	}
	if failed.ExitStatus != -1 {
		t.Errorf("exit status %d, want -1", failed.ExitStatus)
	}
	if !strings.Contains(failed.StartError, "something broke") || !strings.Contains(failed.StartError, "exit status 2") {
		t.Errorf("start_error %q doesn't say what the holder logged and how it ended", failed.StartError)
	}
}

// A program that runs and fails has no start_error: only a terminal that
// never started does.
func TestPaneProgramFailureHasNoStartError(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	t.Cleanup(d.Shutdown)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "exit 7"}, Cwd: t.TempDir()},
	}}})
	var exited *pb.Pane
	waitEvent(t, c, func(m *pb.ServerMessage) bool {
		for _, p := range m.GetState().GetPanes() {
			if p.Exited {
				exited = p
				return true
			}
		}
		return false
	})
	if exited.ExitStatus != 7 || exited.StartError != "" {
		t.Errorf("exit status %d, start_error %q; want 7 and none", exited.ExitStatus, exited.StartError)
	}
}

// The real pane-holder, asked to run a command that isn't there, listens
// and then ends: the pane fails at once, quoting the holder's own reason.
func TestPaneCommandNotFoundStartError(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	t.Cleanup(d.Shutdown)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	start := time.Now()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"hammock-no-such-command"}, Cwd: t.TempDir()},
	}}})
	var failed *pb.Pane
	waitEvent(t, c, func(m *pb.ServerMessage) bool {
		for _, p := range m.GetState().GetPanes() {
			if p.Exited {
				failed = p
				return true
			}
		}
		return false
	})
	if took := time.Since(start); took >= holderConnectTimeout {
		t.Errorf("took %v to notice the holder had ended", took)
	}
	if !strings.Contains(failed.StartError, "hammock-no-such-command") {
		t.Errorf("start_error %q doesn't name the missing command", failed.StartError)
	}
}

// A holder's log is appended to across restarts of its pane: only what this
// holder wrote counts as its reason.
func TestLastLogLineFromOffset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "holder.log")
	old := "holder: got terminated\n"
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if l := lastLogLine(path, int64(len(old))); l != "" {
		t.Errorf("quoted an earlier holder's line: %q", l)
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("holder: start: boom\n\n")
	f.Close()
	if l := lastLogLine(path, int64(len(old))); l != "holder: start: boom" {
		t.Errorf("last line %q, want this holder's", l)
	}
}
