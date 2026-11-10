// Command pane-holder owns one PTY and its child process and serves it over a
// Unix socket. It is spawned by hmkd in its own session so it survives
// daemon restarts. See internal/holder.
//
//	pane-holder --socket /path/p.sock [--dir DIR] [--cols N --rows N] [--token-fd N] [--daemon PATH] -- cmd args...
//
// With --token-fd, the holder reads a secret from that file descriptor (to
// EOF) and requires it of every client; the daemon passes it on a pipe so it
// is in neither the arguments nor the environment the pane's program sees.
// With --daemon, only that program (or another hmkd of the same install)
// may attach, token or not. With --state, only the process holding that
// state directory's locks (its daemon: holder.OpenStateLock) may attach,
// and the daemon starting the holder must hold them.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/homegrownsw/hammock/internal/holder"
)

func main() {
	socket := flag.String("socket", "", "Unix socket path to serve on")
	dir := flag.String("dir", "", "working directory for the child")
	cols := flag.Uint("cols", 80, "initial columns")
	rows := flag.Uint("rows", 24, "initial rows")
	ring := flag.Int("ring", 8<<20, "bytes of output retained for replay")
	tokenFD := flag.Int("token-fd", -1, "read a client secret from this file descriptor")
	idle := flag.Duration("idle-exit", 24*time.Hour, "exit this long after the child exits with no client attached (0 = never)")
	orphan := flag.Duration("orphan-exit", time.Hour, "once the app this runs from (or this executable) has been deleted, end the pane after this long with no client attached (0 = never)")
	daemon := flag.String("daemon", "", "the daemon starting this holder (its executable): only it, or another hmkd of this install, may attach")
	state := flag.String("state", "", "the daemon's state directory: only the process holding its locks, which must be the one starting this holder, may attach")
	// From a development build's daemon that held an fcntl lock on
	// run/daemon.owner instead: taken, and like a released daemon's holder
	// (no --state), checked by program only. Such a daemon is a build that
	// never shipped.
	flag.String("owner", "", "(ignored: from earlier development builds)")
	flag.Parse()
	if *socket == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: pane-holder --socket PATH [flags] -- command [args...]")
		os.Exit(2)
	}

	// The daemon may die at any time; never let its signals take us down.
	// Notify (not Ignore) so the child still gets default dispositions:
	// SIG_IGN would be inherited across exec.
	discard := make(chan os.Signal, 8)
	signal.Notify(discard, syscall.SIGHUP, syscall.SIGINT, syscall.SIGPIPE)
	go func() {
		for range discard {
		}
	}()

	var token []byte
	if *tokenFD >= 0 {
		// Read and closed before the child starts, so it doesn't inherit it.
		f := os.NewFile(uintptr(*tokenFD), "token")
		b, err := io.ReadAll(io.LimitReader(f, 4096))
		f.Close()
		if err != nil || len(b) == 0 {
			fmt.Fprintln(os.Stderr, "pane-holder: no token on fd", *tokenFD)
			os.Exit(2)
		}
		token = b
	}

	exe, _ := os.Executable() // "" (no orphan check) if it can't be told
	var peer func(holder.PeerProc) bool
	if *daemon != "" {
		peer = holder.DaemonPeer(*daemon, installOf(exe))
	}
	var lock *holder.StateLock
	if *state != "" {
		// As it starts, while the daemon that started it holds the locks:
		// which files they are is recorded now, whatever is put at their
		// paths later.
		var err error
		if lock, err = holder.OpenStateLock(*state, os.Getppid()); err != nil {
			fmt.Fprintln(os.Stderr, "pane-holder:", err)
			os.Exit(1)
		}
		// Its socket is where that daemon keeps them, by the rule the
		// daemon reconnects by (no other folder, no link in the way).
		if !inPanes(*socket, *state) {
			fmt.Fprintln(os.Stderr, "pane-holder: the socket isn't in", filepath.Join(*state, "run", "panes"))
			os.Exit(2)
		}
	}
	s, err := holder.Start(holder.Config{
		Socket: *socket, Command: flag.Args(), Dir: *dir, Token: token,
		Cols: uint16(*cols), Rows: uint16(*rows), RingSize: *ring, IdleExit: *idle,
		Install: installOf(exe), OrphanExit: *orphan, Peer: peer, State: lock, AdoptOrphans: true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	select {
	case sig := <-term:
		fmt.Fprintf(os.Stderr, "%s pane-holder: got %v\n", time.Now().Format(time.RFC3339), sig)
		s.Shutdown()
	case <-s.Done():
	}
}

// inPanes reports whether socket is a file directly in state's run/panes,
// both made absolute as written (links unresolved), and not a link itself
// (if it's there already).
func inPanes(socket, state string) bool {
	abs := func(p string) string {
		if a, err := filepath.Abs(p); err == nil {
			return a
		}
		return filepath.Clean(p)
	}
	if abs(filepath.Dir(socket)) != abs(filepath.Join(state, "run", "panes")) {
		return false
	}
	st, err := os.Lstat(socket)
	return err != nil || st.Mode()&os.ModeSymlink == 0
}

// installOf is the outermost app bundle exe is in (Hammock.app, not the helper
// inside it), or exe itself outside one. The app, not the executable: an
// update that moves or renames the helper leaves the app where it was, and
// holders started from the old helper still belong to it.
func installOf(exe string) string {
	if i := strings.Index(exe, ".app/"); i >= 0 {
		return exe[:i+len(".app")]
	}
	return exe
}
