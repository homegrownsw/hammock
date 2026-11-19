package regfile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// within fails the test if f takes more than a few seconds (it blocked).
func within(t *testing.T, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("blocked")
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small")
	if err := os.WriteFile(small, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(small, link); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		path  string
		limit int64
		want  error
	}{
		{small, 5, nil},
		{link, 5, nil},
		{small, 4, ErrTooLarge},
		{dir, 1 << 20, ErrNotRegular},
		{fifo, 1 << 20, ErrNotRegular},
		{"/dev/zero", 1 << 20, ErrNotRegular},
		{"/dev/null", 1 << 20, ErrNotRegular},
	} {
		within(t, func() {
			data, err := ReadFile(tc.path, tc.limit)
			if !errors.Is(err, tc.want) || (tc.want == nil && string(data) != "hello") {
				t.Errorf("%s (limit %d): %q, %v; want %v", tc.path, tc.limit, data, err, tc.want)
			}
		})
	}
}

// Read stops at the limit, however much more there is.
func TestReadStopsAtLimit(t *testing.T) {
	f, err := os.Open("/dev/zero")
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	within(t, func() {
		if _, err := Read(f, 1<<20); !errors.Is(err, ErrTooLarge) {
			t.Errorf("got %v", err)
		}
	})
}
