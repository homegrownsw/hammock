package hammockconfig

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Writers of the config file (rewrite here, for the daemon, hmk and the
// TUI; the Mac app's AppConfig.set) each read it, edit it and rename a new
// one over it, so two at once would lose one's change. They take an
// exclusive flock on LockPath for all three steps. Readers don't: the
// rename is atomic.
//
// The lock is beside the config's own entry, in Dir, never beside a
// symlink's target (a dotfiles checkout), and is its own file because the
// config is replaced on every write. The Mac app uses the same rule.

// LockPath is the writers' lock file: Dir/.config.lock.
func LockPath() string { return filepath.Join(Dir(), ".config.lock") }

// lockWait is how long a writer waits for the lock before writing without
// it: never long enough to hang the daemon or a UI on a stuck holder.
var lockWait = 2 * time.Second

// Warn reports a write made without the lock. The TUI quiets it (its
// stderr is the screen).
var Warn = func(msg string, args ...any) { slog.Warn(msg, args...) }

// lock takes the writers' lock, and returns its release. When it can't be
// had within lockWait (or the file can't be made), the write goes ahead
// without it: a release that does nothing.
func lock() func() {
	path := LockPath()
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		Warn("config written without its lock", "lock", path, "err", err)
		return func() {}
	}
	deadline := time.Now().Add(lockWait)
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EINTR) || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.Close()
	Warn("config written without its lock", "lock", path, "err", err)
	return func() {}
}
