package holder

import (
	"path/filepath"
	"testing"
	"time"
)

// A program the command leaves behind is taken in by the holder and reaped
// when it exits; the command's own exit status is still the pane's.
func TestAdoptedOrphansReaped(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "p.sock")
	s, err := Start(Config{Socket: sock, AdoptOrphans: true,
		Command: []string{"/bin/sh", "-c", "(/bin/sh -c 'sleep 0.3' &); sleep 1; exit 3"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		exited, status := s.exited, s.exitStatus
		s.mu.Unlock()
		if exited {
			if status != 3 {
				t.Fatalf("exit status %d, want 3", status)
			}
			if z := exitedChildren(s.PID()); len(z) > 0 {
				t.Fatalf("zombies left: %v", z)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the command never exited")
}
