package daemon

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/alexjsp/hammock/internal/client"
	"github.com/alexjsp/hammock/internal/holder"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/state"
	"golang.org/x/sys/unix"
)

// otherHmkd is this test binary run as another hmkd (the same program as
// the daemon its holders know), as an agent would start one:
//   - dial: dials the holder at HMK_TEST_SOCKET with HMK_TEST_TOKEN, as one
//     that read them from state.db would;
//   - open-dial: the same, with the real state dir HMK_TEST_REAL's lock and
//     the dir itself open (not locked), as a daemon reading a file an agent
//     pointed at them (a .git linked to the lock) has them;
//   - lockexec: with no daemon running, flocks HMK_TEST_REAL's locks and
//     execs itself as `inherited`, the descriptors left open across exec;
//   - inherited: closes what it inherited first if HMK_TEST_CLOSE is set, as
//     hmkd does, then dials;
//   - daemon: starts a whole daemon on the state dir HMK_TEST_DIR (a copy of
//     the real one's), and stops it;
//   - reattach: starts a daemon on HMK_TEST_DIR, and says whether pane
//     HMK_TEST_PANE is attached to the process HMK_TEST_PID.
//
// Exit 0: attached (or started), 3: refused.
func otherHmkd() int {
	dialIt := func() int {
		hc, err := holder.Dial(os.Getenv("HMK_TEST_SOCKET"), 0, []byte(os.Getenv("HMK_TEST_TOKEN")))
		if err != nil {
			fmt.Fprintln(os.Stderr, "refused:", err)
			return 3
		}
		hc.Close()
		return 0
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	switch os.Getenv("HMK_TEST_OTHER_HMKD") {
	case "dial":
		return dialIt()
	case "open-dial":
		real := os.Getenv("HMK_TEST_REAL")
		l, err1 := os.Open(holder.LockPath(real))
		d, err2 := os.Open(real)
		if err1 != nil || err2 != nil {
			return 1
		}
		defer l.Close()
		defer d.Close()
		return dialIt()
	case "lockexec":
		real := os.Getenv("HMK_TEST_REAL")
		for _, p := range []string{holder.LockPath(real), real} {
			fd, err := unix.Open(p, unix.O_RDONLY, 0) // (no O_CLOEXEC: kept across exec)
			if err != nil {
				return 1
			}
			if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
				fmt.Fprintln(os.Stderr, "lock:", p, err)
				return 1
			}
		}
		env := []string{"HMK_TEST_OTHER_HMKD=inherited"}
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "HMK_TEST_OTHER_HMKD=") {
				env = append(env, kv)
			}
		}
		err := syscall.Exec(os.Args[0], []string{os.Args[0]}, env)
		fmt.Fprintln(os.Stderr, "exec:", err)
		return 1
	case "inherited":
		if os.Getenv("HMK_TEST_CLOSE") != "" {
			holder.CloseInherited()
		}
		return dialIt()
	case "daemon":
		holderBin = os.Getenv("HMK_TEST_HOLDER")
		d, err := New(Config{Dir: os.Getenv("HMK_TEST_DIR"), HolderPath: holderBin, Log: quiet})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		d.Shutdown()
		return 0
	case "reattach":
		holderBin = os.Getenv("HMK_TEST_HOLDER")
		d, err := New(Config{Dir: os.Getenv("HMK_TEST_DIR"), HolderPath: holderBin, Log: quiet})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		defer d.Shutdown()
		d.mu.Lock()
		p := d.panes[os.Getenv("HMK_TEST_PANE")]
		d.mu.Unlock()
		if p == nil {
			return 1
		}
		p.mu.Lock()
		pid, linked := p.pid, p.hc != nil
		p.mu.Unlock()
		if want, _ := strconv.Atoi(os.Getenv("HMK_TEST_PID")); !linked || pid != want {
			fmt.Fprintf(os.Stderr, "pane on pid %d (linked %v), want %d\n", pid, linked, want)
			return 3
		}
		return 0
	}
	return 2
}

// runOther runs otherHmkd with env, returning its exit code and output.
func runOther(t *testing.T, env ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

// stateTestPane starts a daemon on dir with one cat pane, attached by c.
func stateTestPane(t *testing.T, dir string) (*Daemon, *client.Client, *pane) {
	t.Helper()
	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "stty -echo; exec cat"}}}}}))
	d.mu.Lock()
	p := d.panes[d.tabs[tab].FocusedPaneId]
	d.mu.Unlock()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: p.id}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: p.id, Data: []byte("up\n")}}})
	waitOutput(t, c, p.id, "up")
	return d, c, p
}

// copyState copies dir's state (not run/) to a new dir, as an agent would.
func copyState(t *testing.T, dir string) string {
	t.Helper()
	cp := shortDir(t)
	os.MkdirAll(filepath.Join(cp, "run", "panes"), 0o700)
	for _, f := range []string{"state.db", "state.db-wal", "state.db-shm", "identity.key"} {
		if b, err := os.ReadFile(filepath.Join(dir, f)); err == nil {
			os.WriteFile(filepath.Join(cp, f), b, 0o600)
		}
	}
	return cp
}

func holderLog(dir, id string) string {
	b, _ := os.ReadFile(filepath.Join(dir, "logs", "holder-"+id+".log"))
	return string(b)
}

// A pane's holder takes only the daemon of its own state dir: not the same
// hmkd run again by something that read its socket and token from
// state.db, even with the state dir's lock open, nor a whole daemon started
// on a copy of the state, which mustn't end or replace the real one's
// holders either. The daemon itself still is, after reading its own lock
// through a link an agent made (a .git), which once let a POSIX lock go.
func TestHolderTakesOnlyItsStateDirsDaemon(t *testing.T) {
	dir := shortDir(t)
	d, c, p := stateTestPane(t, dir)
	defer d.Shutdown()
	p.mu.Lock()
	socket, token := p.socket, string(p.token)
	p.mu.Unlock()

	if code, out := runOther(t, "HMK_TEST_OTHER_HMKD=dial", "HMK_TEST_SOCKET="+socket, "HMK_TEST_TOKEN="+token); code != 3 {
		t.Fatalf("another hmkd with the socket and token: exit %d, %s", code, out)
	}
	if code, out := runOther(t, "HMK_TEST_OTHER_HMKD=open-dial", "HMK_TEST_REAL="+dir, "HMK_TEST_SOCKET="+socket, "HMK_TEST_TOKEN="+token); code != 3 {
		t.Fatalf("another hmkd with the state dir's lock open: exit %d, %s", code, out)
	}
	if !strings.Contains(holderLog(dir, p.id), "not the daemon of this pane's state") {
		t.Errorf("holder log: %s", holderLog(dir, p.id))
	}

	// The daemon reads its own lock through a link (a tab in a folder
	// whose .git an agent linked to it), closing what it opened.
	repo := t.TempDir()
	if err := os.Symlink(holder.LockPath(dir), filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	gitBranch(repo)
	if _, err := os.ReadFile(filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	if hc, err := holder.Dial(socket, 0, []byte(token)); err != nil {
		t.Fatalf("the daemon itself refused: %v\n%s", err, holderLog(dir, p.id))
	} else {
		hc.Close()
	}

	// A whole daemon on a copy of the state: it starts, but leaves the real
	// holders be.
	cp := copyState(t, dir)
	if code, out := runOther(t, "HMK_TEST_OTHER_HMKD=daemon", "HMK_TEST_DIR="+cp, "HMK_TEST_HOLDER="+holderBin); code != 0 {
		t.Fatalf("the copy's daemon: exit %d, %s", code, out)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: p.id, Data: []byte("still-here\n")}}})
	waitOutput(t, c, p.id, "still-here")
	if info := p.snapshotInfo(); info.Exited {
		t.Fatal("the real pane's program ended")
	}
	if st, err := os.Lstat(socket); err != nil || st.Mode()&os.ModeSocket == 0 {
		t.Fatalf("the real holder's socket: %v", err)
	}
}

// With no daemon running, a program that takes the state dir's locks and
// then execs the real hmkd (on a copy of the state) hands it descriptors
// that hold them: hmkd closes them first thing, so the holders refuse it.
func TestHolderRefusesLocksCarriedAcrossExec(t *testing.T) {
	dir := shortDir(t)
	d, _, p := stateTestPane(t, dir)
	p.mu.Lock()
	socket, token := p.socket, string(p.token)
	p.mu.Unlock()
	d.Shutdown() // (as kill -9 would: the holders run on, the locks go)
	waitUnlocked(t, dir)

	env := []string{"HMK_TEST_OTHER_HMKD=lockexec", "HMK_TEST_REAL=" + dir, "HMK_TEST_SOCKET=" + socket, "HMK_TEST_TOKEN=" + token}
	// Without the closing, the trick works: the check is only as good as it.
	if code, out := runOther(t, env...); code != 0 {
		t.Fatalf("carried locks, kept: exit %d, %s", code, out)
	}
	if code, out := runOther(t, append(env, "HMK_TEST_CLOSE=1")...); code != 3 {
		t.Fatalf("carried locks, closed as hmkd does: exit %d, %s", code, out)
	}

	// The real daemon, started again, still has them.
	d = startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: p.id}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: p.id, Data: []byte("back\n")}}})
	waitOutput(t, c, p.id, "back")
}

// The next daemon of the state dir, another process (a restart, a crash,
// an update that moved hmkd), reattaches to the holders the last one
// started.
func TestHolderTakesTheNextDaemonOfItsDir(t *testing.T) {
	dir := shortDir(t)
	d, _, p := stateTestPane(t, dir)
	pid := p.snapshotPID()
	d.Shutdown()
	waitUnlocked(t, dir)
	if code, out := runOther(t, "HMK_TEST_OTHER_HMKD=reattach", "HMK_TEST_DIR="+dir, "HMK_TEST_HOLDER="+holderBin,
		"HMK_TEST_PANE="+p.id, "HMK_TEST_PID="+strconv.Itoa(pid)); code != 0 {
		t.Fatalf("the next daemon: exit %d, %s\n%s", code, out, holderLog(dir, p.id))
	}
}

func (p *pane) snapshotPID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pid
}

// A copy of the state whose holder sockets are links to the real ones (in
// the copy's own run/panes, so they pass as its own by folder): its daemon
// reconnects to none, and ends or replaces none of the real ones.
func TestDaemonRefusesLinkedHolderSockets(t *testing.T) {
	dir := shortDir(t)
	d, c, p := stateTestPane(t, dir)
	defer d.Shutdown()
	p.mu.Lock()
	socket, token := p.socket, string(p.token)
	p.mu.Unlock()
	realPID := holderPID(t, socket)

	if !d.ownSocket(socket, p.id) {
		t.Fatal("its own holder's socket refused")
	}
	if d.ownSocket(socket, "pa_other") {
		t.Fatal("another pane's socket taken as this one's")
	}
	link := filepath.Join(dir, "run", "panes", "pa_link.sock")
	if err := os.Symlink(socket, link); err != nil {
		t.Fatal(err)
	}
	if d.ownSocket(link, "pa_link") {
		t.Fatal("a link to a holder's socket taken as one")
	}
	hard := filepath.Join(dir, "run", "panes", "pa_hard.sock")
	if os.Link(socket, hard) == nil && d.ownSocket(hard, "pa_hard") {
		t.Fatal("a second name for a holder's socket taken as one")
	}
	os.Remove(link)
	os.Remove(hard)

	cp := copyState(t, dir)
	st, err := state.Open(filepath.Join(cp, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(cp, "run", "panes", p.id+".sock")
	if err := st.PutRuntime(state.PaneRuntime{PaneID: p.id, Socket: mine, Token: token}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := os.Symlink(socket, mine); err != nil {
		t.Fatal(err)
	}
	if code, out := runOther(t, "HMK_TEST_OTHER_HMKD=daemon", "HMK_TEST_DIR="+cp, "HMK_TEST_HOLDER="+holderBin); code != 0 {
		t.Fatalf("the copy's daemon: exit %d, %s", code, out)
	}
	if got := holderPID(t, socket); got != realPID {
		t.Fatalf("the real holder is pid %d now, was %d", got, realPID)
	}
	if strings.Contains(holderLog(dir, p.id), "refused") {
		t.Errorf("the copy dialed the real holder: %s", holderLog(dir, p.id))
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: p.id, Data: []byte("still-here\n")}}})
	waitOutput(t, c, p.id, "still-here")
}

// A daemon refuses a lock file with a second name (a hard link from or to
// another state dir's, which would lock that one's), and starts without
// taking anything if something else holds its state dir's own lock.
func TestDaemonRefusesLinkedOrHeldLocks(t *testing.T) {
	dir := shortDir(t)
	os.MkdirAll(filepath.Join(dir, "run"), 0o700)
	os.WriteFile(holder.LockPath(dir), nil, 0o600)
	other := filepath.Join(t.TempDir(), "x")
	if err := os.Link(holder.LockPath(dir), other); err != nil {
		t.Fatal(err)
	}
	if l, err := lockDir(dir); err == nil {
		l.Close()
		t.Fatal("locked a lock file with two names")
	}
	os.Remove(other)
	l, err := lockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()

	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if l, err := lockDir(dir); err == nil {
		l.Close()
		t.Fatal("started with the state dir's lock held by something else")
	}
	// daemon.lock was let go again: hmk's check sees no daemon.
	lf, _ := os.OpenFile(holder.LockPath(dir), os.O_RDWR, 0)
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("daemon.lock kept: %v", err)
	}
}
