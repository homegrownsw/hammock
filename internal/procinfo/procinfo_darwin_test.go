package procinfo

import (
	"os/exec"
	"testing"
	"time"
)

// A setuid process (top runs as root) refuses proc_name; its name still
// comes back (program icons).
func TestNameOfSetuidProcess(t *testing.T) {
	cmd := exec.Command("/usr/bin/top", "-l", "2", "-s", "5")
	if err := cmd.Start(); err != nil {
		t.Skip("no top:", err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	time.Sleep(200 * time.Millisecond)
	name, err := Name(cmd.Process.Pid)
	if err != nil || name != "top" {
		t.Fatalf("Name = %q, %v; want top", name, err)
	}
}
