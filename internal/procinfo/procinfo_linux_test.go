package procinfo

import (
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

// Kernels without CONFIG_PROC_CHILDREN (Unraid's) have no
// /proc/<pid>/task/<pid>/children: the scan finds them all the same.
func TestChildrenByScan(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	scan.Lock()
	scan.children = nil // a scan from before the child started
	scan.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for !slices.Contains(childrenByScan(os.Getpid()), cmd.Process.Pid) {
		if time.Now().After(deadline) {
			t.Fatalf("child %d not found by scan: %v", cmd.Process.Pid, childrenByScan(os.Getpid()))
		}
		time.Sleep(scanEvery)
	}
}
