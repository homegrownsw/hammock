package holder

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRingWrapAndReadFrom(t *testing.T) {
	r := NewRing(8)
	r.Write([]byte("abcde"))
	if got, at := r.ReadFrom(0); string(got) != "abcde" || at != 0 {
		t.Fatalf("got %q at %d", got, at)
	}
	r.Write([]byte("fghij")) // total 10, ring keeps last 8: "cdefghij"
	if r.Start() != 2 || r.End() != 10 {
		t.Fatalf("start/end = %d/%d", r.Start(), r.End())
	}
	if got, at := r.ReadFrom(0); string(got) != "cdefghij" || at != 2 {
		t.Fatalf("got %q at %d", got, at)
	}
	if got, at := r.ReadFrom(7); string(got) != "hij" || at != 7 {
		t.Fatalf("got %q at %d", got, at)
	}
	r.Write([]byte("0123456789ABC")) // larger than capacity
	if r.End() != 23 || r.Start() != 15 {
		t.Fatalf("start/end = %d/%d", r.Start(), r.End())
	}
	if got, _ := r.ReadFrom(0); string(got) != "56789ABC" {
		t.Fatalf("got %q", got)
	}
}

func startHolderB(t testing.TB, cmd ...string) (*Server, string) { return startHolderTB(t, cmd...) }

func startHolder(t *testing.T, cmd ...string) (*Server, string) { return startHolderTB(t, cmd...) }

func startHolderTB(t testing.TB, cmd ...string) (*Server, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "p.sock")
	s, err := Start(Config{Socket: sock, Command: cmd, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	return s, sock
}

// reader pumps a client's events into channels so tests can wait with timeouts.
type reader struct {
	events chan Event
	errs   chan error
}

func newReader(c *Client) *reader {
	r := &reader{events: make(chan Event, 1024), errs: make(chan error, 1)}
	go func() {
		for {
			ev, err := c.Next()
			if err != nil {
				r.errs <- err
				return
			}
			r.events <- ev
		}
	}()
	return r
}

func (r *reader) next(t *testing.T) Event {
	t.Helper()
	select {
	case ev := <-r.events:
		return ev
	case err := <-r.errs:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
	}
	return Event{}
}

func (r *reader) until(t *testing.T, want string) string {
	t.Helper()
	var buf bytes.Buffer
	for !strings.Contains(buf.String(), want) {
		buf.Write(r.next(t).Output)
	}
	return buf.String()
}

func TestEchoInputResizeAndExit(t *testing.T) {
	_, sock := startHolder(t, "/bin/sh", "-c", `stty -echo; echo ready; read line; echo "got:$line"; stty size; exit 3`)
	c, err := Dial(sock, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := newReader(c)
	if c.Info.Cols != 100 || c.Info.Rows != 30 {
		t.Fatalf("info size %dx%d", c.Info.Cols, c.Info.Rows)
	}
	r.until(t, "ready")
	if err := c.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := c.Input([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	out := r.until(t, "40 120")
	if !strings.Contains(out, "got:hello") {
		t.Fatalf("missing echo in %q", out)
	}
	for ev := r.next(t); !ev.Exited; ev = r.next(t) {
	}

	// A late attacher still learns the exit status.
	c2, err := Dial(sock, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if !c2.Info.Exited || c2.Info.ExitStatus != 3 {
		t.Fatalf("late info %+v", c2.Info)
	}
}

// TestReattachIsContiguous repeatedly disconnects a client abruptly while the
// child floods the PTY, reattaching from the last offset seen, and checks the
// reassembled stream is exactly the child's output.
func TestReattachIsContiguous(t *testing.T) {
	const n = 200000
	// -onlcr: under load macOS PTYs occasionally repeat the \r of a
	// translated newline; compare raw bytes instead.
	_, sock := startHolder(t, "/bin/sh", "-c", fmt.Sprintf("stty -onlcr; exec /usr/bin/seq 1 %d", n))

	var stream bytes.Buffer
	var next uint64
	exited := false
	for attempt := 0; !exited; attempt++ {
		if attempt > 10000 {
			t.Fatal("too many attempts")
		}
		c, err := Dial(sock, next, nil)
		if err != nil {
			t.Fatal(err)
		}
		if c.Info.RingStart > next {
			t.Fatalf("output lost: ring start %d > next %d", c.Info.RingStart, next)
		}
		budget := 1 + rand.IntN(20) // frames to read before an abrupt disconnect
		for i := 0; i < budget; i++ {
			ev, err := c.Next()
			if err != nil {
				break // dropped for falling behind; reattach from next
			}
			if ev.Exited {
				exited = true
				break
			}
			if ev.Offset > next {
				t.Fatalf("gap: got offset %d, want %d", ev.Offset, next)
			}
			skip := next - ev.Offset // overlap is allowed, gaps are not
			if skip < uint64(len(ev.Output)) {
				stream.Write(ev.Output[skip:])
				next = ev.Offset + uint64(len(ev.Output))
			}
		}
		c.Close()
	}

	var want strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&want, "%d\n", i)
	}
	if got, w := stream.String(), want.String(); got != w {
		i := 0
		for i < len(got) && i < len(w) && got[i] == w[i] {
			i++
		}
		t.Fatalf("stream mismatch at byte %d: got %q, want %q (lens %d vs %d)", i, got[max(0, i-20):min(len(got), i+20)], w[max(0, i-20):min(len(w), i+20)], len(got), len(w))
	}
}

// TestSignalReachesForegroundJob checks Ctrl-C-style signals hit the running
// job even when the shell put it in its own process group, and that input
// queued behind a program that isn't reading doesn't block signals.
func TestSignalReachesForegroundJob(t *testing.T) {
	// An interactive shell, as in a pane, running a job in the foreground:
	// the interrupt ends the job, not the shell. (Not sh -c 'set -m; …': a
	// shell that isn't interactive gives up when its job is interrupted on
	// Linux, and Debian's sh has no job control there at all.)
	_, sock := startHolder(t, "/bin/bash", "--norc", "--noprofile", "-i")
	c, err := Dial(sock, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := newReader(c)
	time.Sleep(300 * time.Millisecond)
	_ = c.Input([]byte("sleep 30; echo after-sleep\r"))
	time.Sleep(300 * time.Millisecond)
	// Flood input sleep never reads, then interrupt it.
	for i := 0; i < 50; i++ {
		_ = c.Input(bytes.Repeat([]byte("x"), 4096))
	}
	if err := c.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	_ = c.Input([]byte("\recho still-$((40+2))\r"))
	r.until(t, "still-42")
}

func TestTokenRequired(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "p.sock")
	s, err := Start(Config{Socket: sock, Command: []string{"/bin/cat"}, Token: []byte("s3cret")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	for _, bad := range [][]byte{nil, []byte("wrong!"), []byte("s3cre")} {
		if c, err := Dial(sock, 0, bad); err == nil {
			c.Close()
			t.Fatalf("attached with token %q", bad)
		}
	}
	c, err := Dial(sock, 0, []byte("s3cret"))
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	// A holder without a token (started by an older daemon) takes any client.
	_, sock2 := startHolder(t, "/bin/cat")
	c2, err := Dial(sock2, 0, []byte("whatever"))
	if err != nil {
		t.Fatal(err)
	}
	c2.Close()
}

func TestOrphanExitOnlyOnceDeletedAndAlone(t *testing.T) {
	dir, err := os.MkdirTemp("", "h") // short: a socket's path has a length limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	install := filepath.Join(dir, "Hammock Pane")
	if err := os.WriteFile(install, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "p.sock")
	s, err := Start(Config{Socket: sock, Command: []string{"/bin/cat"}, Install: install, OrphanExit: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	ended := func(within time.Duration) bool {
		select {
		case <-s.Done():
			return true
		case <-time.After(within):
			return false
		}
	}
	// Still installed, no one attached: it keeps running.
	if ended(1500 * time.Millisecond) {
		t.Fatal("ended while its executable was still there")
	}
	// Deleted, but a daemon is attached: it keeps running.
	c, err := Dial(sock, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(install); err != nil {
		t.Fatal(err)
	}
	if ended(1500 * time.Millisecond) {
		t.Fatal("ended with a client attached")
	}
	// Deleted and alone: it ends, hanging up the child.
	c.Close()
	if !ended(3 * time.Second) {
		t.Fatal("an orphaned holder kept running")
	}
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(s.PID(), 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("the child outlived its holder")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The token alone isn't enough: the client must also be the daemon (any
// program that read the token from the daemon's state otherwise attaches).
func TestPeerRequired(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		daemon string
		ok     bool
	}{
		{"the daemon", exe, true},
		{"another program", "/usr/bin/true", false},
	} {
		sock := filepath.Join(t.TempDir(), "p.sock")
		s, err := Start(Config{Socket: sock, Command: []string{"/bin/cat"}, Token: []byte("s3cret"),
			Peer: DaemonPeer(tc.daemon, "/nonexistent/pane-holder")})
		if err != nil {
			t.Fatal(err)
		}
		c, err := Dial(sock, 0, []byte("s3cret"))
		if (err == nil) != tc.ok {
			t.Errorf("%s: attached %v (%v)", tc.name, err == nil, err)
		}
		if err == nil {
			c.Close()
		}
		s.Shutdown()
	}
	// An hmkd beside the holder (a restarted daemon) is one too; one
	// elsewhere isn't.
	dir := t.TempDir()
	hmkd := filepath.Join(dir, "hmkd")
	if out, err := exec.Command("cp", exe, hmkd).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	sock := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cmd := exec.Command(hmkd, "-test.run=^TestSleepHelper$")
	cmd.Env = append(os.Environ(), "HMK_HOLDER_TEST_SLEEP="+sock)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	nc, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	peer := sockPeer(nc.(*net.UnixConn))
	if !DaemonPeer("/elsewhere/hmkd", filepath.Join(dir, "pane-holder"))(peer) {
		t.Error("an hmkd beside the holder refused")
	}
	if DaemonPeer("/elsewhere/hmkd", "/elsewhere/pane-holder")(peer) {
		t.Error("an hmkd elsewhere taken")
	}
}

// TestSleepHelper is a program for TestPeerRequired to run under another name.
func TestSleepHelper(t *testing.T) {
	sock := os.Getenv("HMK_HOLDER_TEST_SLEEP")
	if sock == "" {
		t.Skip("run by TestPeerRequired")
	}
	c, err := net.Dial("unix", sock) // a peer, to be checked as it connected
	if err == nil {
		defer c.Close()
	}
	time.Sleep(10 * time.Second)
}

// Input more than a frame can carry (a big paste) goes in several, in
// order, none too big for the holder to read.
func TestBigInputIsSplit(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	c := &Client{conn: a}
	in := make([]byte, 2*MaxFramePayload+10)
	for i := range in {
		in[i] = byte(i % 251)
	}
	errc := make(chan error, 1)
	go func() { errc <- c.Input(in) }()
	var got []byte
	for frames := 0; len(got) < len(in); frames++ {
		f, err := ReadFrame(b)
		if err != nil {
			t.Fatalf("frame %d: %v", frames, err)
		}
		if f.Type != FrameInput {
			t.Fatalf("frame %d is type %d", frames, f.Type)
		}
		got = append(got, f.Payload...)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, in) {
		t.Fatal("input arrived changed")
	}
}
