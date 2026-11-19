package daemon

import (
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// macOS tells a process in a sandbox from one that isn't, by the audit
// token the listener takes: a child of sandbox-exec (as an agent harness
// runs its commands under Seatbelt) is sandboxed, the same program run
// plainly isn't.
func TestPeerSandboxed(t *testing.T) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("no sandbox-exec")
	}
	sock := filepath.Join(shortDir(t), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	peer := func(argv ...string) bool {
		t.Helper()
		cmd := exec.Command(argv[0], argv[1:]...)
		in, err := cmd.StdinPipe() // held open: nc stays connected until it's closed
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { in.Close(); cmd.Process.Kill(); cmd.Wait() }()
		ln.(*net.UnixListener).SetDeadline(time.Now().Add(10 * time.Second))
		c, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		lp := localPeerOf(c)
		if !lp.tok.Valid() || lp.pid != cmd.Process.Pid {
			t.Fatalf("peer %+v, want pid %d", lp, cmd.Process.Pid)
		}
		return peerSandboxed(lp)
	}
	if peer("/usr/bin/nc", "-U", sock) {
		t.Fatal("a plain process taken as sandboxed")
	}
	// (sandbox-exec execs nc in its own pid.)
	if !peer("/usr/bin/sandbox-exec", "-p", "(version 1)(allow default)", "/usr/bin/nc", "-U", sock) {
		t.Fatal("a process in a sandbox not taken as sandboxed")
	}
}
