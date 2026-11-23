package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/holder"
)

// startPaneHolder builds this command into a folder of its own and starts
// it serving cat, returning the folder, its executable and the holder.
func startPaneHolder(t *testing.T) (dir, exe string, h *exec.Cmd) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the pane-holder")
	}
	dir, err := os.MkdirTemp("", "ph") // short: a socket's path has a length limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	exe = filepath.Join(dir, "pane-holder")
	build(t, exe)
	log, err := os.Create(filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	h = exec.Command(exe, "--socket", filepath.Join(dir, "p.sock"), "--", "/bin/sh", "-c", "echo ready; exec cat")
	h.Stderr = log
	h.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		h.Process.Kill()
		h.Wait()
		log.Close()
		if t.Failed() {
			b, _ := os.ReadFile(log.Name())
			t.Logf("the holder's log:\n%s", b)
		}
	})
	return dir, exe, h
}

func build(t *testing.T, out string, flags ...string) {
	t.Helper()
	cmd := exec.Command("go", append(append([]string{"build", "-o", out}, flags...), ".")...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
}

func dialWait(t *testing.T, sock string, from uint64) *holder.Client {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := holder.Dial(sock, from, nil)
		if err == nil {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s: %v", sock, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readUntil reads c's output until it has each of want, failing after a
// while; it returns the error that ended the connection, if one did first.
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
// and child, the output kept, and input still reaching the program.
func TestHandsOverOnUpdate(t *testing.T) {
	dir, exe, h := startPaneHolder(t)
	sock := filepath.Join(dir, "p.sock")
	c := dialWait(t, sock, 0)
	child := c.Info.PID
	readUntil(t, c, "ready")
	if err := c.Input([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "before")

	// Another build, as an app update puts one in place.
	build(t, exe+".new", "-ldflags=-s")
	if err := os.Rename(exe+".new", exe); err != nil {
		t.Fatal(err)
	}
	// The connection drops as the holder execs.
	dropped := make(chan struct{})
	go func() {
		for {
			if _, err := c.Next(); err != nil {
				close(dropped)
				return
			}
		}
	}()
	select {
	case <-dropped:
	case <-time.After(10 * time.Second):
		t.Fatal("the holder didn't hand over")
	}

	c2 := dialWait(t, sock, 0)
	defer c2.Close()
	if c2.Info.PID != child {
		t.Fatalf("child %d after the handoff, was %d", c2.Info.PID, child)
	}
	if err := syscall.Kill(h.Process.Pid, 0); err != nil {
		t.Fatalf("the holder's process went: %v", err)
	}
	readUntil(t, c2, "ready", "before") // kept across the exec
	if err := c2.Input([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c2, "after")
	b, _ := os.ReadFile(filepath.Join(dir, "log"))
	if !strings.Contains(string(b), "took the pane over after an update") {
		t.Fatalf("the new executable didn't take over; log:\n%s", b)
	}
}

// What's put in the holder's place but doesn't take its handoff (a
// downgrade, something broken) is left alone, and the pane runs on.
func TestNoHandoffToAnExecutableThatDoesntTakeIt(t *testing.T) {
	dir, exe, _ := startPaneHolder(t)
	c := dialWait(t, filepath.Join(dir, "p.sock"), 0)
	defer c.Close()
	readUntil(t, c, "ready")
	if err := os.WriteFile(exe+".new", []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(exe+".new", exe); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second) // a few looks
	if err := c.Input([]byte("still\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "still")
	b, _ := os.ReadFile(filepath.Join(dir, "log"))
	if !strings.Contains(string(b), "doesn't take this holder's handoff") {
		t.Fatalf("no word of the refusal; log:\n%s", b)
	}
}
