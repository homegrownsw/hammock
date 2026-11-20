package hammockconfig

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Writers setting different keys at once each keep their change: in one
// process (each write opens the lock afresh, so goroutines contend like
// processes do) and across processes.
func TestConcurrentWritersKeepEveryChange(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	const writers, rounds = 4, 40
	var wg sync.WaitGroup
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range rounds {
				if err := Set(fmt.Sprintf("key-%d", w), strconv.Itoa(i)); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	values := Values()
	for w := range writers {
		if got := values[fmt.Sprintf("key-%d", w)]; got != strconv.Itoa(rounds-1) {
			t.Errorf("key-%d = %q, want %d", w, got, rounds-1)
		}
	}
}

func TestConcurrentProcessesKeepEveryChange(t *testing.T) {
	if key := os.Getenv("HMK_TEST_CONFIG_WRITER"); key != "" {
		for i := range 40 {
			if err := Set(key, strconv.Itoa(i)); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		return
	}
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	var cmds []*exec.Cmd
	for w := range 3 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestConcurrentProcessesKeepEveryChange$")
		cmd.Env = append(os.Environ(), fmt.Sprintf("HMK_TEST_CONFIG_WRITER=proc-%d", w))
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	values := Values()
	for w := range 3 {
		if got := values[fmt.Sprintf("proc-%d", w)]; got != "39" {
			t.Errorf("proc-%d = %q, want 39", w, got)
		}
	}
}

// A lock that's never let go only delays a write, by lockWait.
func TestWriteGoesAheadWhenLockIsHeld(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", dir)
	old, oldWarn := lockWait, Warn
	lockWait = 150 * time.Millisecond
	warned := false
	Warn = func(string, ...any) { warned = true }
	t.Cleanup(func() { lockWait, Warn = old, oldWarn })

	f, err := os.OpenFile(LockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := Set("app-theme", "Nord"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < lockWait {
		t.Fatalf("wrote after %v, before the lock's wait (%v)", d, lockWait)
	}
	if !warned {
		t.Fatal("no warning")
	}
	if v, _ := Value("app-theme"); v != "Nord" {
		t.Fatalf("app-theme = %q", v)
	}
	if st, err := os.Stat(LockPath()); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("lock file: %v %v", st, err)
	}
}
