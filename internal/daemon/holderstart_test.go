package daemon

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
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

// A command on the person's PATH but not the daemon's (a login item's is
// the system's, /usr/bin:/bin:…) runs (#43): the login shell finds it.
func TestPaneCommandOnLoginPath(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "hammock-test-tool"), []byte("#!/bin/sh\nexit 9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("PATH=\""+bin+":$PATH\"\nexport PATH\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/sh") // reads ~/.profile as a login shell
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	dir := shortDir(t)
	d := startDaemon(t, dir)
	t.Cleanup(d.Shutdown)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "s"}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"hammock-test-tool", "an argument"}, Cwd: t.TempDir()},
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
	if exited.ExitStatus != 9 || exited.StartError != "" {
		t.Errorf("exit status %d, start_error %q; want the tool's own 9", exited.ExitStatus, exited.StartError)
	}
	if want := []string{"hammock-test-tool", "an argument"}; !slices.Equal(exited.Command, want) {
		t.Errorf("command %q, want it as given %q", exited.Command, want)
	}
}

func TestViaLoginShell(t *testing.T) {
	for _, tc := range []struct {
		cmd   []string
		shell string
		want  []string
	}{
		{[]string{"revdiff", "a b"}, "/bin/zsh", []string{"/bin/zsh", "-l", "-c", `exec "$@"`, "revdiff", "revdiff", "a b"}},
		{[]string{"revdiff", "a b"}, "/opt/homebrew/bin/fish", []string{"/opt/homebrew/bin/fish", "-l", "-c", "exec $argv", "revdiff", "a b"}},
		{[]string{"revdiff"}, "/bin/tcsh", []string{"revdiff"}},              // can't pass argv on
		{[]string{"/bin/zsh", "-l"}, "/bin/zsh", []string{"/bin/zsh", "-l"}}, // a shell already
		{[]string{"/bin/zsh", "-l", "-c", "claude"}, "/bin/zsh", []string{"/bin/zsh", "-l", "-c", "claude"}},
		{nil, "/bin/zsh", nil},
	} {
		if got := viaLoginShell(tc.cmd, tc.shell); !slices.Equal(got, tc.want) {
			t.Errorf("viaLoginShell(%q, %q) = %q, want %q", tc.cmd, tc.shell, got, tc.want)
		}
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
