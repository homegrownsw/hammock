package holder

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// TestHelperStateLock isn't a test: run as a child with HMK_TEST_STATE set,
// it opens that state dir's locks as HMK_TEST_HOW says ("lock": flocks both,
// as a daemon does; "open": only opens them, as a daemon reading a file an
// agent pointed at them does), says so, and keeps them until stdin closes.
func TestHelperStateLock(t *testing.T) {
	dir := os.Getenv("HMK_TEST_STATE")
	if dir == "" {
		t.Skip("a helper")
	}
	lock, err := os.OpenFile(LockPath(dir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		os.Exit(1)
	}
	d, err := os.Open(dir)
	if err != nil {
		os.Exit(1)
	}
	if os.Getenv("HMK_TEST_HOW") == "lock" {
		if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil || syscall.Flock(int(d.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			os.Exit(1)
		}
	}
	os.Stdout.WriteString("ready\n")
	io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

// stateHelper runs TestHelperStateLock on dir, until release.
func stateHelper(t *testing.T, dir, how string) (pid int, release func()) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperStateLock$")
	cmd.Env = append(os.Environ(), "HMK_TEST_STATE="+dir, "HMK_TEST_HOW="+how)
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if l, err := bufio.NewReader(out).ReadString('\n'); err != nil || l != "ready\n" {
		t.Fatalf("the helper didn't %s the state dir: %q %v", how, l, err)
	}
	done := false
	return cmd.Process.Pid, func() {
		if !done {
			done = true
			in.Close()
			cmd.Wait()
		}
	}
}

func stateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "run", "panes"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A holder takes the process that flocked its state dir and its lock, as
// they were when it started: not one that only has them open, nor anyone
// once the lock file has a second name or was replaced.
func TestStateLock(t *testing.T) {
	dir := stateDir(t)
	pid, release := stateHelper(t, dir, "lock")
	defer release()
	if _, err := OpenStateLock(dir, os.Getpid()); err == nil {
		t.Error("recorded for a parent that doesn't hold the locks")
	}
	s, err := OpenStateLock(dir, pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HeldBy(pid); err != nil {
		t.Fatal(err)
	}
	if s.HeldBy(os.Getpid()) == nil {
		t.Error("taken a process without the locks")
	}

	// Open, not locked: as the daemon of a copy of the state would have
	// them, reading a file whose path an agent chose (a .git linked to the
	// lock).
	opener, closeOpener := stateHelper(t, dir, "open")
	defer closeOpener()
	if err := s.HeldBy(opener); err == nil {
		t.Error("taken a process that only has the locks open")
	}

	// A second name for the lock (a hard link from a copy).
	link := filepath.Join(t.TempDir(), "x")
	if err := os.Link(LockPath(dir), link); err != nil {
		t.Fatal(err)
	}
	if s.HeldBy(pid) == nil {
		t.Error("asked a lock whose file has two names")
	}
	os.Remove(link)
	if err := s.HeldBy(pid); err != nil {
		t.Fatalf("once the link went: %v", err)
	}

	// The locks let go: nobody.
	release()
	if s.HeldBy(pid) == nil {
		t.Error("taken a process that has gone")
	}

	// Another file put in the lock's place, locked: not the one recorded.
	os.Remove(LockPath(dir))
	pid2, release2 := stateHelper(t, dir, "lock")
	defer release2()
	if s.HeldBy(pid2) == nil {
		t.Error("taken the holder of a new lock file at the path")
	}
	if _, err := OpenStateLock(dir, pid2); err != nil {
		t.Fatalf("a holder started now: %v", err)
	}
}

// The lock must be a plain file with one name, and run/ a real folder.
func TestStateLockRefusesLinks(t *testing.T) {
	dir := stateDir(t)
	pid, release := stateHelper(t, dir, "lock")
	defer release()
	other := stateDir(t)
	os.RemoveAll(filepath.Join(other, "run"))
	if err := os.Symlink(filepath.Join(dir, "run"), filepath.Join(other, "run")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStateLock(other, pid); err == nil {
		t.Error("recorded through a run folder that's a link")
	}
	os.Remove(filepath.Join(other, "run"))
	os.MkdirAll(filepath.Join(other, "run"), 0o700)
	if err := os.Link(LockPath(dir), LockPath(other)); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStateLock(other, pid); err == nil {
		t.Error("recorded a lock with two names")
	}
	f, _ := os.Open(LockPath(other))
	defer f.Close()
	if CheckLockFile(LockPath(other), f) == nil {
		t.Error("CheckLockFile took a lock with two names")
	}
}

// TestHelperInherited isn't a test: run with HMK_TEST_INHERITED=<fd>, it
// closes what it inherited (as hmkd does first) when HMK_TEST_CLOSE is set,
// and says whether fd is still open.
func TestHelperInherited(t *testing.T) {
	v := os.Getenv("HMK_TEST_INHERITED")
	if v == "" {
		t.Skip("a helper")
	}
	if os.Getenv("HMK_TEST_CLOSE") != "" {
		CloseInherited()
	}
	fd, _ := strconv.Atoi(v)
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		os.Stdout.WriteString("closed\n")
	} else {
		os.Stdout.WriteString("open\n")
	}
	os.Exit(0)
}

// CloseInherited closes a descriptor the process was started with.
func TestCloseInherited(t *testing.T) {
	f, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, close := range []bool{false, true} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperInherited$")
		cmd.ExtraFiles = []*os.File{f} // fd 3
		cmd.Env = append(os.Environ(), "HMK_TEST_INHERITED=3")
		if close {
			cmd.Env = append(cmd.Env, "HMK_TEST_CLOSE=1")
		}
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		want := map[bool]string{false: "open\n", true: "closed\n"}[close]
		if string(out) != want {
			t.Errorf("closing %v: %q, want %q", close, out, want)
		}
	}
}
