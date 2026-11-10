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
	was := onUnraid
	defer func() { onUnraid = was }()

	// Not on Unraid: no key is written, and an old one goes.
	onUnraid = func() bool { return false }
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, personKeyFile), []byte("old\n"), 0o600)
	writePersonKey(dir, slog.Default())
	if _, err := os.Stat(filepath.Join(dir, personKeyFile)); err == nil {
		t.Fatal("a key left for a page that isn't there")
	}

	onUnraid = func() bool { return true }
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
		if got := personPage(cmd.Process.Pid); got != tc.ok {
			t.Errorf("key %q: %v, want %v", tc.key, got, tc.ok)
		}
		onUnraid = func() bool { return false }
		if personPage(cmd.Process.Pid) {
			t.Errorf("key %q taken off Unraid", tc.key)
		}
		onUnraid = func() bool { return true }
		cmd.Process.Kill()
		cmd.Wait()
	}
}
