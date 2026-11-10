package holder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// The state lock ties a holder to the daemon of one state directory, not
// just to a program that is hmkd. The token and the holder's socket are in
// the daemon's state, which anything of yours can copy, and the real hmkd
// will start on any directory (`hmkd --dir copy`): without this, that one
// would attach to every live holder of the real one, and type into its
// panes.
//
// The daemon of a state directory is the process that flocked it: hmkd
// takes an exclusive flock on run/daemon.lock (one daemon per directory)
// and then on the directory itself, and keeps both for as long as it runs.
// A holder records both files (their device and inode) as it starts,
// checks its parent, the daemon starting it, holds both, and takes only a
// client that does: one with a descriptor on each that a flock was taken
// through (macOS: the descriptor's FHASLOCK; Linux: the lock line in its
// fdinfo). Merely having the file open isn't enough (a daemon reads files
// whose paths agents choose, and a symlink could point one at the lock),
// and nothing else can hold the locks while the daemon does. hmkd closes
// every descriptor it inherits as it starts (CloseInherited), so a locked
// descriptor can't be carried into it across exec by a program that took
// the lock while no daemon ran. Unlike a POSIX record lock, a flock isn't
// dropped when the process closes another descriptor of the same file.
//
// The directory's lock, and not only run/daemon.lock: a copy of the state
// whose run/daemon.lock was the real one's (renamed into the copy while no
// daemon ran, and back once a daemon on the copy had locked it) would
// otherwise pass. The directory is the state; a daemon that flocked the
// real one through some other name is the real one's daemon.

// LockFile is the daemon's lock in a state directory's run folder.
const LockFile = "daemon.lock"

// ErrLockFile says a lock file isn't a plain file of this user's with only
// the one name.
var ErrLockFile = errors.New("not a plain file of yours with one name")

// LockPath is the daemon's lock file in state directory dir.
func LockPath(dir string) string { return filepath.Join(dir, "run", LockFile) }

// fileID is a file's identity: its device and inode.
type fileID struct{ dev, ino uint64 }

func idOf(st *unix.Stat_t) fileID { return fileID{uint64(st.Dev), uint64(st.Ino)} }

// plainFile checks st (a lock file's) is a regular file of this user's
// with one link: a hard link to another state dir's lock would lock that
// one's.
func plainFile(path string, st *unix.Stat_t) error {
	if st.Mode&unix.S_IFMT != unix.S_IFREG || int(st.Uid) != os.Getuid() || st.Nlink != 1 {
		return fmt.Errorf("%s: %w (mode %o, uid %d, %d links)", path, ErrLockFile, st.Mode, st.Uid, st.Nlink)
	}
	return nil
}

// CheckLockFile checks f, opened at path, is a plain file (plainFile) that
// path still names. The daemon checks its run/daemon.lock this way once it
// has locked it.
func CheckLockFile(path string, f *os.File) error {
	var st, at unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return err
	}
	if err := plainFile(path, &st); err != nil {
		return err
	}
	if err := unix.Lstat(path, &at); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if idOf(&at) != idOf(&st) {
		return fmt.Errorf("%s: no longer the file that was opened", path)
	}
	return nil
}

// StateLock is a holder's record of its state directory's locks.
type StateLock struct {
	dir, lock     string
	dirID, lockID fileID
}

// OpenStateLock records state directory dir's locks, which the process
// with pid parent (the daemon starting this holder) must hold.
func OpenStateLock(dir string, parent int) (*StateLock, error) {
	var dst, rst, lst unix.Stat_t
	if err := unix.Stat(dir, &dst); err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	if dst.Mode&unix.S_IFMT != unix.S_IFDIR || int(dst.Uid) != os.Getuid() {
		return nil, fmt.Errorf("%s isn't a folder of yours", dir)
	}
	run := filepath.Join(dir, "run")
	if err := unix.Lstat(run, &rst); err != nil || rst.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, fmt.Errorf("%s isn't a folder (%v)", run, err)
	}
	lock := LockPath(dir)
	if err := unix.Lstat(lock, &lst); err != nil {
		return nil, fmt.Errorf("%s: %w", lock, err)
	}
	if err := plainFile(lock, &lst); err != nil {
		return nil, err
	}
	s := &StateLock{dir: dir, lock: lock, dirID: idOf(&dst), lockID: idOf(&lst)}
	if err := s.HeldBy(parent); err != nil {
		return nil, fmt.Errorf("the daemon starting this holder (pid %d): %w", parent, err)
	}
	return s, nil
}

// HeldBy says (nil) whether the process pid holds this state directory's
// locks, as they were when the holder started: flocks taken through
// descriptors of its own on both, while the lock's path still names it
// with its one link.
func (s *StateLock) HeldBy(pid int) error {
	if s == nil {
		return errors.New("no state lock")
	}
	var st unix.Stat_t
	if err := unix.Lstat(s.lock, &st); err != nil {
		return fmt.Errorf("%s: %w", s.lock, err)
	}
	if err := plainFile(s.lock, &st); err != nil {
		return err
	}
	if idOf(&st) != s.lockID {
		return fmt.Errorf("%s isn't the file it was when this holder started", s.lock)
	}
	if pid <= 0 {
		return errors.New("no process")
	}
	locked, err := lockedFiles(pid)
	if err != nil {
		return fmt.Errorf("can't see what pid %d has locked: %w", pid, err)
	}
	if !locked[s.lockID] {
		return fmt.Errorf("pid %d doesn't hold %s's lock", pid, s.lock)
	}
	if !locked[s.dirID] {
		return fmt.Errorf("pid %d doesn't hold %s's lock", pid, s.dir)
	}
	return nil
}

// Dir is the state directory.
func (s *StateLock) Dir() string { return s.dir }
