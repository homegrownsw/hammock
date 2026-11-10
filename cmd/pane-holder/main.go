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
// may attach, token or not.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alexjsp/hammock/internal/holder"
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
	s, err := holder.Start(holder.Config{
		Socket: *socket, Command: flag.Args(), Dir: *dir, Token: token,
		Cols: uint16(*cols), Rows: uint16(*rows), RingSize: *ring, IdleExit: *idle,
		Install: installOf(exe), OrphanExit: *orphan, Peer: peer, AdoptOrphans: true,
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
