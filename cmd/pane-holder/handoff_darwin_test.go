package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/holder"
	"github.com/homegrownsw/hammock/internal/procinfo"
)

// paneHolder is a pane-holder built into a folder of its own and started
// as the daemon starts one: with a token, its state dir (this test holds
// its locks) and its daemon (this test's executable), so a client proves
// itself over Mach as the daemon does.
type paneHolder struct {
	dir, exe, sock string
	token          []byte
	cmd            *exec.Cmd
}

func startPaneHolder(t *testing.T, script string) *paneHolder {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the pane-holder")
	}
	dir, err := os.MkdirTemp("", "ph") // short: a socket's path has a length limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	h := &paneHolder{dir: dir, exe: filepath.Join(dir, "pane-holder"), token: []byte("s3cret")}
	build(t, h.exe)

	// The state dir, its locks held by this test as the daemon holds them.
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(filepath.Join(state, "run", "panes"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.sock = filepath.Join(state, "run", "panes", "pn_test.sock")
	lock, err := os.OpenFile(holder.LockPath(state), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := os.Open(state)
	if err != nil {
		t.Fatal(err)
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil || syscall.Flock(int(sd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		t.Fatal("can't lock the state dir")
	}
	t.Cleanup(func() { lock.Close(); sd.Close() })
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	log, err := os.Create(filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	tr, tw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	h.cmd = exec.Command(h.exe, "--socket", h.sock, "--token-fd", "3", "--state", state, "--daemon", self, "--", "/bin/sh", "-c", script)
	h.cmd.Stderr = log
	h.cmd.ExtraFiles = []*os.File{tr}
	h.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tr.Close()
	tw.Write(h.token)
	tw.Close()
	t.Cleanup(func() {
		h.cmd.Process.Kill()
		h.cmd.Wait()
		log.Close()
		if t.Failed() {
			b, _ := os.ReadFile(log.Name())
			t.Logf("the holder's log:\n%s", b)
		}
	})
	return h
}

func (h *paneHolder) log() string {
	b, _ := os.ReadFile(filepath.Join(h.dir, "log"))
	return string(b)
}

// update puts another build where the holder's executable was, as an app
// update does.
func (h *paneHolder) update(t *testing.T) {
	t.Helper()
	build(t, h.exe+".new", "-ldflags=-s")
	if err := os.Rename(h.exe+".new", h.exe); err != nil {
		t.Fatal(err)
	}
}

// handedOver waits for the holder to say it has taken the pane over.
func (h *paneHolder) handedOver(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); !strings.Contains(h.log(), "took the pane over after an update"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the holder didn't hand over")
		}
	}
}

func build(t *testing.T, out string, flags ...string) {
	t.Helper()
	cmd := exec.Command("go", append(append([]string{"build", "-o", out}, flags...), ".")...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
}

// dial attaches as the daemon does (the proof, the token), retrying while
// the holder hands over.
func (h *paneHolder) dial(t *testing.T, from uint64) *holder.Client {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := holder.Dial(h.sock, from, h.token)
		if err == nil {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s: %v", h.sock, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readUntil reads c's output until it has each of want, failing after a
// while or if the connection ends first.
func readUntil(t *testing.T, c *holder.Client, want ...string) {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		var out strings.Builder
		for {
			ev, err := c.Next()
			if err != nil {
				got <- out.String() + "\n(connection: " + err.Error() + ")"
				return
			}
			out.Write(ev.Output)
			all := true
			for _, w := range want {
				all = all && strings.Contains(out.String(), w)
			}
			if all {
				got <- ""
				return
			}
		}
	}()
	select {
	case s := <-got:
		if s != "" {
			t.Fatalf("waiting for %q, got %q", want, s)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no %q within 10s", want)
	}
}

// An update puts another executable where the holder's was: the holder
// execs it in its own place and hands the pane over. The same holder process
// (still known by its --socket) and child, the output kept, input still
// reaching the program, and the daemon proving itself to the new image.
func TestHandsOverOnUpdate(t *testing.T) {
	h := startPaneHolder(t, "echo ready; exec cat")
	c := h.dial(t, 0)
	child, pid := c.Info.PID, h.cmd.Process.Pid
	if c.HolderPID() != pid {
		t.Errorf("HolderPID %d, the holder is %d", c.HolderPID(), pid)
	}
	readUntil(t, c, "ready")
	if err := c.Input([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "before")

	h.update(t)
	h.handedOver(t)
	c.Close()

	c2 := h.dial(t, 0)
	defer c2.Close()
	if c2.Info.PID != child {
		t.Fatalf("child %d after the handoff, was %d", c2.Info.PID, child)
	}
	if c2.HolderPID() != pid {
		t.Fatalf("holder %d after the handoff, was %d", c2.HolderPID(), pid)
	}
	if args := procinfo.Args(pid); !slices.Contains(args, "--socket") || !slices.Contains(args, h.sock) {
		t.Fatalf("the holder's arguments after the handoff don't name its socket: %q", args)
	}
	readUntil(t, c2, "ready", "before") // kept across the exec
	if err := c2.Input([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c2, "after")
}

// A holder whose program has exited hands over too (what the program left
// running is still its), and the exit carries over.
func TestHandsOverAfterTheChildExited(t *testing.T) {
	h := startPaneHolder(t, "echo bye; exit 3")
	c := h.dial(t, 0)
	for {
		ev, err := c.Next()
		if err != nil {
			t.Fatal(err)
		}
		if ev.Exited {
			break
		}
	}
	h.update(t)
	h.handedOver(t)
	c.Close()
	c2 := h.dial(t, 0)
	defer c2.Close()
	if !c2.Info.Exited || c2.Info.ExitStatus != 3 {
		t.Fatalf("after the handoff: exited %v, status %d", c2.Info.Exited, c2.Info.ExitStatus)
	}
	readUntil(t, c2, "bye")
}

// What's put in the holder's place but doesn't take its handoff (a
// downgrade, something broken) is left alone, and the pane runs on.
func TestNoHandoffToAnExecutableThatDoesntTakeIt(t *testing.T) {
	h := startPaneHolder(t, "echo ready; exec cat")
	c := h.dial(t, 0)
	defer c.Close()
	readUntil(t, c, "ready")
	if err := os.WriteFile(h.exe+".new", []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(h.exe+".new", h.exe); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(h.log(), "doesn't take this holder's handoff"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no word of giving up on it")
		}
	}
	if err := c.Input([]byte("still\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "still")
}
