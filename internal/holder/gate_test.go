package holder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain lets this test binary be a holder's gate (Config.Gate), as the
// pane-holder executable is.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == GateArg {
		RunGate(os.Args[2:])
	}
	os.Exit(m.Run())
}

func startGated(t *testing.T, await time.Duration, cmd ...string) (*Server, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Short, whatever the test's name: a Unix socket's path has a low limit.
	dir, err := os.MkdirTemp("", "gate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "p.sock")
	s, err := Start(Config{Socket: sock, Command: cmd, Cols: 100, Rows: 30, AwaitSize: await, Gate: exe})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	return s, sock
}

// The command waits for the first resize and starts at that size, in the
// gate's place: the PID the holder gave out is the command's.
func TestAwaitSizeStartsAtFirstResize(t *testing.T) {
	_, sock := startGated(t, 10*time.Second, "/bin/sh", "-c", `echo "pid:$$"; stty size; sleep 5`)
	c, err := Dial(sock, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := newReader(c)
	select {
	case ev := <-r.events:
		t.Fatalf("output before any resize: %q", ev.Output)
	case <-time.After(300 * time.Millisecond):
	}
	if err := c.Resize(90, 20); err != nil {
		t.Fatal(err)
	}
	out := r.until(t, "20 90")
	if want := fmt.Sprintf("pid:%d", c.Info.PID); !strings.Contains(out, want) {
		t.Fatalf("want %s (the holder's PID for its child) in %q", want, out)
	}
}

// Without a resize, it starts anyway once AwaitSize has passed, at the
// size the holder was started at.
func TestAwaitSizeStartsAfterTimeout(t *testing.T) {
	_, sock := startGated(t, 100*time.Millisecond, "/bin/sh", "-c", `stty size; sleep 5`)
	c, err := Dial(sock, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	newReader(c).until(t, "30 100")
}

// A holder shut down while its command waits never runs it, and its gate
// goes.
func TestAwaitSizeShutdown(t *testing.T) {
	ran := filepath.Join(t.TempDir(), "ran")
	s, _ := startGated(t, 10*time.Second, "/usr/bin/touch", ran)
	pid := s.PID()
	time.Sleep(100 * time.Millisecond)
	s.Shutdown()
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the gate is still running after the holder shut down")
		}
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the command ran after the holder shut down")
	}
}

// While its command waits, input is kept for it (not echoed, nor read by
// the gate), and an interrupt, with nothing yet to interrupt, doesn't end
// the gate.
func TestAwaitSizeKeepsInputAndSurvivesInterrupt(t *testing.T) {
	_, sock := startGated(t, 10*time.Second, "/bin/sh", "-c", `echo started; read x; echo "got:$x"; sleep 5`)
	c, err := Dial(sock, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r := newReader(c)
	if err := c.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := c.Input([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	for len(r.events) > 0 {
		if ev := <-r.events; len(ev.Output) > 0 {
			t.Fatalf("output before the command started (input echoed?): %q", ev.Output)
		}
	}
	if err := c.Resize(90, 20); err != nil {
		t.Fatal(err)
	}
	r.until(t, "got:hello")
}
