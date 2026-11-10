package codesign

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// An Apple program passes "anchor apple"; it isn't signed as the test is.
func TestCheckAppleProgram(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	time.Sleep(100 * time.Millisecond)
	s, ok := Check(cmd.Process.Pid, "anchor apple")
	if !ok || s.Identifier != "com.apple.sleep" {
		t.Fatalf("Check(sleep) = %+v, %v", s, ok)
	}
	if _, ok := Check(cmd.Process.Pid, `anchor apple generic and identifier "hmkd"`); ok {
		t.Fatal("sleep passed for hmkd")
	}
	if _, ok := Check(0, "anchor apple"); ok {
		t.Fatal("no process passed")
	}
	Self() // (a test binary is signed ad hoc, or not at all)
}

// A socket's peer, checked by its audit token, is the program that
// connected: one that execs another (signed) program in its place after
// connecting no longer passes, though its pid does.
func TestTokenNamesWhatConnected(t *testing.T) {
	if os.Getenv("HMK_CODESIGN_HELPER") != "" {
		c, err := net.Dial("unix", os.Getenv("HMK_CODESIGN_HELPER"))
		if err != nil {
			os.Exit(2)
		}
		b := make([]byte, 1)
		c.Read(b)
		syscall.Exec("/bin/sleep", []string{"sleep", "5"}, nil) // (the socket stays open across exec)
		os.Exit(3)
	}
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTokenNamesWhatConnected$")
	cmd.Env = append(os.Environ(), "HMK_CODESIGN_HELPER="+sock)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var tok Token
	rc, _ := c.(*net.UnixConn).SyscallConn()
	rc.Control(func(fd uintptr) { tok = PeerToken(int(fd)) })
	if !tok.Valid() || tok.PID() != cmd.Process.Pid {
		t.Fatalf("token pid %d, want %d", tok.PID(), cmd.Process.Pid)
	}
	if p, err := tok.Path(); err != nil || !tok.Live() {
		t.Fatalf("path %q, %v", p, err)
	}
	c.Write([]byte{1})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := Check(cmd.Process.Pid, "anchor apple"); ok {
			break // sleep runs in its pid
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper never exec'd")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tok.Live() {
		t.Error("the token still names the process after it exec'd")
	}
	if _, ok := CheckToken(tok, "anchor apple"); ok {
		t.Error("the program exec'd in the connected process's place passed by token")
	}
}

// A program on disk is checked against a requirement as it is there.
func TestCheckFile(t *testing.T) {
	if !CheckFile("/bin/sh", "anchor apple") {
		t.Error("/bin/sh isn't Apple's")
	}
	if CheckFile("/bin/sh", `anchor apple generic and certificate leaf[subject.OU] = "TESTTEAM00"`) {
		t.Error("/bin/sh taken as another team's")
	}
	if CheckFile("/nonexistent/x", "anchor apple") {
		t.Error("a missing file passed")
	}
}
