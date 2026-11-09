package codesign

import (
	"os/exec"
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
