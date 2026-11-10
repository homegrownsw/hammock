package holder

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestHelperSpeaks isn't a test: run with HMK_TEST_SPEAK set, it proves
// itself and sends an attach (with that token) on the connection it was
// handed as fd 3 (with HMK_TEST_SPEAK_RAW, the attach only, as a daemon from
// before the proof would), and says whether the holder answered; with
// HMK_TEST_SPEAK_DIAL, on one it makes itself to that socket.
func TestHelperSpeaks(t *testing.T) {
	tok := os.Getenv("HMK_TEST_SPEAK")
	if tok == "" {
		t.Skip("a helper")
	}
	var c net.Conn
	if sock := os.Getenv("HMK_TEST_SPEAK_DIAL"); sock != "" {
		var err error
		if c, err = net.Dial("unix", sock); err != nil {
			os.Exit(2)
		}
	} else {
		var err error
		if c, err = net.FileConn(os.NewFile(3, "conn")); err != nil {
			os.Exit(2)
		}
	}
	if hc, err := attach(c, 0, []byte(tok), os.Getenv("HMK_TEST_SPEAK_RAW") == ""); err == nil {
		hc.Close()
		os.Stdout.WriteString("attached\n")
	} else {
		os.Stdout.WriteString("refused\n")
	}
	os.Exit(0)
}

// macOS gives a socket's peer as the last process to use it: one that
// connects and has another program (here the daemon's own executable,
// which the holder would take) speak for it isn't taken.
func TestPeerIsWhoConnected(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "p.sock")
	s, err := Start(Config{Socket: sock, Command: []string{"/bin/cat"}, Token: []byte("s3cret"),
		Peer: func(p PeerProc) bool { return p.PID != os.Getpid() && DaemonPeer(exe, "")(p) }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	nc, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	f, err := nc.(*net.UnixConn).File()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cmd := exec.Command(exe, "-test.run=^TestHelperSpeaks$")
	cmd.Env = append(os.Environ(), "HMK_TEST_SPEAK=s3cret")
	cmd.ExtraFiles = []*os.File{f}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "attached\n" && string(out) != "refused\n" {
		t.Fatalf("the helper: %q", out)
	}
	if string(out) == "attached\n" {
		t.Error("taken a program that spoke on a connection another process made")
	}

	// The helper connecting itself is taken (the check above refused the
	// handover, not the helper).
	cmd = exec.Command(exe, "-test.run=^TestHelperSpeaks$")
	cmd.Env = append(os.Environ(), "HMK_TEST_SPEAK=s3cret", "HMK_TEST_SPEAK_DIAL="+sock)
	if out, err := cmd.Output(); err != nil || string(out) != "attached\n" {
		t.Errorf("the helper connecting itself: %q %v", out, err)
	}
}
