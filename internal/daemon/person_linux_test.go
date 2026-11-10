package daemon

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Unraid's page is you only with the key hmkd left for it, in the
// environment hmk was started with.
func TestPersonKey(t *testing.T) {
	dir := t.TempDir()
	writePersonKey(dir, slog.Default())
	b, err := os.ReadFile(filepath.Join(dir, personKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, personKeyFile)); fi.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %v", fi.Mode())
	}
	for _, tc := range []struct {
		key string
		ok  bool
	}{{strings.TrimSpace(string(b)), true}, {"nope", false}, {"", false}} {
		cmd := exec.Command("/bin/sleep", "5")
		cmd.Env = []string{"HMK_PERSON_KEY=" + tc.key}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		if got := hasPersonKey(cmd.Process.Pid); got != tc.ok {
			t.Errorf("key %q: %v, want %v", tc.key, got, tc.ok)
		}
		cmd.Process.Kill()
		cmd.Wait()
	}
}
