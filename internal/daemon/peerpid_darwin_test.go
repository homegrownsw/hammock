package daemon

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// macOS gives a socket's peer as the last process to use it: a client
// whose connection is spoken on by another process (a child it was handed
// to, or a program exec'd in its place) is hung up on, not taken for that
// process; one that connects and speaks itself is taken, as itself.
func TestSniffListenerPeerIsWhoConnected(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan localPeer, 2)
	sl := newSniffListener(ln, func(c net.Conn, _ *bufio.Reader, lp localPeer) { got <- lp; c.Close() })
	defer sl.Close()

	// Handed over: a shell speaks on this process's connection.
	nc, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	f, err := nc.(*net.UnixConn).File()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // (accepted, before the shell speaks)
	sh := exec.Command("/bin/sh", "-c", "printf PRC1 >&3")
	sh.ExtraFiles = []*os.File{f}
	if out, err := sh.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	f.Close()
	select {
	case lp := <-got:
		t.Errorf("took a connection another process spoke on, as pid %d", lp.pid)
	case <-time.After(500 * time.Millisecond):
	}

	// Itself.
	nc2, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer nc2.Close()
	if _, err := nc2.Write([]byte(frameMagic)); err != nil {
		t.Fatal(err)
	}
	select {
	case lp := <-got:
		if lp.pid != os.Getpid() || lp.tok.PID() != os.Getpid() {
			t.Errorf("a client that spoke itself taken as pid %d (token %d), want %d", lp.pid, lp.tok.PID(), os.Getpid())
		}
	case <-time.After(5 * time.Second):
		t.Error("a client that spoke itself wasn't taken")
	}
}
