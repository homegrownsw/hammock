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
// client that does: one with a descriptor on each, other than its stdin,
// stdout or stderr, that a flock was taken through (macOS: the open file's
// FHASLOCK, which stays once the flock is let go; Linux: the lock line in
// its fdinfo, there while it's held), while both files are flocked now (a
// flock of the holder's own is refused). Merely having the file open isn't
// enough (a daemon reads files whose paths agents choose, and a symlink
// could point one at the lock), and nothing else can hold the locks while
// the daemon does. hmkd closes every descriptor it inherits above stderr as
// it starts, and replaces a stdin, stdout or stderr that's a folder or a
// file a flock was taken through (CloseInherited): a descriptor of the
// lock, flocked and let go by a program while no daemon ran, can't be
// carried into the real hmkd across exec and pass for its own once the
// real daemon has the lock again. That holds only of an hmkd that has run:
// one exec'd suspended (POSIX_SPAWN_START_SUSPENDED) closes nothing, and on
// macOS a socket's peer can be just that, so there the check is of a
// proof's sender, which was running when it sent it (proof.go). Unlike a
// POSIX record lock, a flock isn't dropped when the process closes another
// descriptor of the same file.
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
// locks, as they were when the holder started: it has descriptors of its
// own on both (not its stdin, stdout or stderr) through which a flock was
// taken, both are flocked now, and the lock's path still names it with its
// one link. Neither the kernel on macOS nor procfs says which process holds
// a flock now: the two together, with hmkd taking none it didn't take itself
// (CloseInherited) and nothing else able to take them while the daemon
// has them, are how the holder knows.
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
	// Having taken a flock through them isn't holding one now (macOS keeps
	// the mark once it's unlocked): something must hold both locks still.
	if err := heldNow(s.lock, s.lockID, unix.O_RDONLY|unix.O_NOFOLLOW); err != nil {
		return err
	}
	return heldNow(s.dir, s.dirID, unix.O_RDONLY|unix.O_DIRECTORY)
}

// heldNow says (nil) whether the file at path, recorded as id, is flocked
// now by someone: a flock of this holder's own on it would be refused. One
// that isn't refused is let go at once (a daemon starting just then could
// find it taken, and try again).
func heldNow(path string, id fileID, flags int) error {
	fd, err := unix.Open(path, flags|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if idOf(&st) != id {
		return fmt.Errorf("%s isn't the file it was when this holder started", path)
	}
	switch err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); {
	case err == nil:
		_ = unix.Flock(fd, unix.LOCK_UN)
		return fmt.Errorf("nothing holds %s's lock", path)
	case errors.Is(err, unix.EWOULDBLOCK):
		return nil
	default:
		return fmt.Errorf("%s: %w", path, err)
	}
}

// Dir is the state directory.
func (s *StateLock) Dir() string { return s.dir }
