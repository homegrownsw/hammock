package daemon

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/state"
)

func waitGone(t *testing.T, what string, gone func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !gone() {
		if time.Now().After(deadline) {
			t.Fatalf("%s still there", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestShutdownCanEndPanes(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "echo ready; exec cat"}},
	}}}))
	d.mu.Lock()
	p := d.panes[d.tabs[tabID].FocusedPaneId]
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: p.id}}})
	waitOutput(t, c, p.id, "ready")
	p.mu.Lock()
	child := p.pid
	p.mu.Unlock()

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ShutdownDaemon{ShutdownDaemon: &pb.ShutdownDaemon{EndPanes: true}}})
	select {
	case <-d.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon didn't stop")
	}
	waitGone(t, "the pane's program", func() bool { return syscall.Kill(child, 0) != nil })
	waitGone(t, "the holder's socket", func() bool { _, err := os.Stat(p.socket); return os.IsNotExist(err) })

	// The pane is still there for the next daemon, with a fresh program.
	d2 := startDaemon(t, dir)
	defer d2.Shutdown()
	c2 := dial(t, dir)
	full := call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState()
	if len(full.Panes) != 1 || full.Panes[0].Exited {
		t.Fatalf("panes after ending them: %+v", full.Panes)
	}
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: p.id}}})
	waitOutput(t, c2, p.id, "ready")
}

func TestStartEndsHoldersWithoutAPane(t *testing.T) {
	dir := shortDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "run", "panes"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A holder for a pane this daemon has never heard of.
	sock := filepath.Join(dir, "run", "panes", "pn_stray.sock")
	h := exec.Command(holderBin, "--socket", sock, "--", "/bin/cat")
	h.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { h.Wait(); close(exited) }()
	t.Cleanup(func() { h.Process.Kill() })
	waitGone(t, "the holder's missing socket", func() bool { _, err := os.Stat(sock); return err == nil })

	// Something else listening in there isn't a holder: left alone.
	other := filepath.Join(dir, "run", "panes", "pn_other.sock")
	ln, err := net.Listen("unix", other)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	d := startDaemon(t, dir)
	defer d.Shutdown()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the stray holder is still running")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("a socket that isn't a holder's was touched: %v", err)
	}
}

// holderPID is the process listening on a pane's socket.
func holderPID(t *testing.T, socket string) int {
	t.Helper()
	c, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return sockPeerPID(c.(*net.UnixConn))
}

func TestUnreachableHolderIsEndedBeforeItsReplacement(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "echo ready; exec cat"}},
	}}}))
	d.mu.Lock()
	p := d.panes[d.tabs[tabID].FocusedPaneId]
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: p.id}}})
	waitOutput(t, c, p.id, "ready")
	old := holderPID(t, p.socket)
	c.Close()
	d.Shutdown()

	// The holder runs on, but the next daemon won't get in: its secret is lost.
	st, err := state.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutRuntime(state.PaneRuntime{PaneID: p.id, Socket: p.socket, Token: "not-its-token"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	d2 := startDaemon(t, dir)
	defer d2.Shutdown()
	waitGone(t, "the unreachable holder", func() bool { return !holderOf(old, p.socket) })
	// Its going didn't take the new holder's socket with it.
	time.Sleep(500 * time.Millisecond)
	if pid := holderPID(t, p.socket); pid == old || pid <= 0 {
		t.Fatalf("socket held by %d (old %d)", pid, old)
	}
	c2 := dial(t, dir)
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	call(t, c2, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: p.id}}})
	waitOutput(t, c2, p.id, "ready")
}
