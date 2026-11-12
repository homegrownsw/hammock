package daemon

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/homegrownsw/hammock/internal/procinfo"
)

// Pane holders outlive the daemon by design (ADR-0003), so nothing else ends
// one whose pane is gone: a pane closed while its holder couldn't be told, a
// holder that couldn't be reached and was replaced. These find them by their
// sockets in <dir>/run/panes and end them, which hangs up their programs.

// paneSockets lists the holder sockets in dir's run/panes, by pane id.
func paneSockets(dir string) map[string]string {
	ents, _ := os.ReadDir(filepath.Join(dir, "run", "panes"))
	out := map[string]string{}
	for _, e := range ents {
		if id, ok := strings.CutSuffix(e.Name(), ".sock"); ok {
			out[id] = filepath.Join(dir, "run", "panes", e.Name())
		}
	}
	return out
}

// endHolderAt ends the pane holder listening on socket, returning its pid.
// It needs no token: the socket says which process is listening, and that
// process is only signalled if its own arguments name this socket, so a
// reused pid or anything else listening there is left alone. A socket no
// one listens on any more is removed.
func endHolderAt(socket string) (int, error) {
	c, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			_ = os.Remove(socket)
		}
		return 0, err
	}
	pid := sockPeerPID(c.(*net.UnixConn))
	c.Close()
	if pid <= 0 {
		return 0, errors.New("can't tell which process holds the socket")
	}
	if !holderOf(pid, socket) {
		return 0, errors.New("the socket's process isn't its pane holder")
	}
	// The holder hangs up its program and exits on SIGTERM. Wait for it: it
	// unlinks its socket's path as it goes, which mustn't take away the
	// socket of a new holder started in its place.
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return 0, err
	}
	// Asking again each time: once it's gone (or only a zombie, whose
	// arguments can't be read), its pid may be another process's.
	for deadline := time.Now().Add(3 * time.Second); holderOf(pid, socket); {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return pid, nil
}

// removeStaleSocket removes socket if nothing listens on it: not one a
// holder other than the one that failed to start has there (a holder
// listening on a pane's socket can't be reached without its token, so
// it's ended with endHolderAt, not cut off from it).
func removeStaleSocket(socket string) {
	c, err := net.DialTimeout("unix", socket, time.Second)
	if err == nil {
		c.Close()
		return
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		_ = os.Remove(socket)
	}
}

// missing reports an error meaning nothing was there to end.
func missing(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist)
}

// holderOf reports whether pid is a pane holder serving socket.
func holderOf(pid int, socket string) bool {
	args := procinfo.Args(pid)
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--" {
			return false // the program's own arguments from here
		}
		if args[i] == "--socket" {
			return args[i+1] == socket
		}
	}
	return false
}

// EndPaneHolders ends every pane holder in dir, for `hmk stop --panes` when
// no daemon is running to do it, and reports how many it ended. It holds the
// daemon's lock throughout, so a daemon starting meanwhile (an open Hammock app
// starts one at once) can't have its holders ended under it: then it ends
// none and returns ErrAlreadyRunning. lastPane's holder, if any, goes last:
// it's the one the caller runs in, and its programs end with it.
func EndPaneHolders(dir, lastPane string) (int, error) {
	f, err := os.OpenFile(filepath.Join(dir, "run", "daemon.lock"), os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil // never had a daemon
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	// Not lockDir: that writes this pid in as the daemon's.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return 0, ErrAlreadyRunning
	}
	socks := paneSockets(dir)
	last, hasLast := socks[lastPane]
	delete(socks, lastPane)
	n := 0
	for _, socket := range socks {
		if _, err := endHolderAt(socket); err == nil {
			n++
		}
	}
	if hasLast {
		if _, err := endHolderAt(last); err == nil {
			n++
		}
	}
	return n, nil
}

// endStrayHolders ends the holders in this daemon's dir whose pane it doesn't
// have, all at once (each may take a few seconds). Callers hold d.mu, after
// restore and before serving.
func (d *Daemon) endStrayHolders() {
	var wg sync.WaitGroup
	for id, socket := range paneSockets(d.cfg.Dir) {
		if _, ok := d.panes[id]; ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if pid, err := endHolderAt(socket); err == nil {
				d.log.Info("ended a pane holder whose pane is gone", "pane", id, "pid", pid)
			} else if !missing(err) {
				d.log.Warn("couldn't end a stray pane holder", "pane", id, "socket", socket, "err", err)
			}
		}()
	}
	wg.Wait()
}
