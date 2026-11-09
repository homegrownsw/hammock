package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRealSessions indexes this Mac's own sessions (SESSIONS_REAL=1), for
// timing and a look at the results; never in a normal run.
func TestRealSessions(t *testing.T) {
	if os.Getenv("SESSIONS_REAL") == "" {
		t.Skip()
	}
	home, _ := os.UserHomeDir()
	x, err := Open(filepath.Join(t.TempDir(), "s.db"), claudeAndCodex(filepath.Join(home, ".claude"), filepath.Join(home, ".codex")))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	n, err := x.Refresh()
	t.Logf("first refresh: %d sessions in %v (%v)", n, time.Since(start), err)
	start = time.Now()
	n, _ = x.Refresh()
	t.Logf("second refresh: %d in %v", n, time.Since(start))
	for _, q := range []string{"", "tally top flag", "PERSIMMON", "zstd compression"} {
		start = time.Now()
		ss, err := x.Search(q, 4)
		t.Logf("search %q: %d in %v %v", q, len(ss), time.Since(start), err)
		for _, s := range ss {
			t.Logf("  %s %s · %s · %s · %d msgs · %q", s.Harness, s.ID[:8], s.Title, s.Cwd, s.Messages, s.Snippet)
		}
	}
}
