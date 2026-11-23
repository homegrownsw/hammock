// Command pane-holder owns one PTY and its child process and serves it over a
// Unix socket. It is spawned by hmkd in its own session so it survives
// daemon restarts. See internal/holder.
//
//	pane-holder --socket /path/p.sock [--dir DIR] [--cols N --rows N] [--await-size D] [--token-fd N] [--daemon PATH] -- cmd args...
//
// With --await-size, the command starts at the first resize or after D,
// whichever is sooner, so a new pane's program starts at the size it's
// shown at (internal/holder Config.AwaitSize).
//
// With --token-fd, the holder reads a secret from that file descriptor (to
// EOF) and requires it of every client; the daemon passes it on a pipe so it
// is in neither the arguments nor the environment the pane's program sees.
// With --daemon, only that program (or another hmkd of the same install)
// may attach, token or not. With --state, only the process holding that
// state directory's locks (its daemon: holder.OpenStateLock) may attach,
// and the daemon starting the holder must hold them. On macOS, with either,
// a client proves over Mach that it's the process the two are checked of
// before anything it sends counts (internal/holder/proof.go).
//
// On macOS, when an update replaces this executable, the holder execs the
// new one in its own place and hands the pane over (internal/holder
// handoff.go): the new image starts with holder.ResumeArg.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/homegrownsw/hammock/internal/holder"
)

// options are the holder's arguments.
type options struct {
	socket, dir, daemon, state string
	cols, rows                 uint
	ring, tokenFD              int
	idle, orphan, awaitSize    time.Duration
	command                    []string
}

// parseArgs parses the holder's arguments. A holder hands its pane over to
// an update's executable only if that parses its arguments too
// (holder.CheckArg), so a flag can go only once no holder can have it.
func parseArgs(args []string) (*options, error) {
	fs := flag.NewFlagSet("pane-holder", flag.ContinueOnError)
	o := &options{}
	fs.StringVar(&o.socket, "socket", "", "Unix socket path to serve on")
	fs.StringVar(&o.dir, "dir", "", "working directory for the child")
	fs.UintVar(&o.cols, "cols", 80, "initial columns")
	fs.UintVar(&o.rows, "rows", 24, "initial rows")
	fs.IntVar(&o.ring, "ring", 8<<20, "bytes of output retained for replay")
	fs.IntVar(&o.tokenFD, "token-fd", -1, "read a client secret from this file descriptor")
	fs.DurationVar(&o.idle, "idle-exit", 24*time.Hour, "exit this long after the child exits with no client attached (0 = never)")
	fs.DurationVar(&o.orphan, "orphan-exit", time.Hour, "once the app this runs from (or this executable) has been deleted, end the pane after this long with no client attached (0 = never)")
	fs.DurationVar(&o.awaitSize, "await-size", 0, "start the command at the first resize, or after this long without one (0: at once)")
	fs.StringVar(&o.daemon, "daemon", "", "the daemon starting this holder (its executable): only it, or another hmkd of this install, may attach")
	fs.StringVar(&o.state, "state", "", "the daemon's state directory: only the process holding its locks, which must be the one starting this holder, may attach")
	// From a development build's daemon that held an fcntl lock on
	// run/daemon.owner instead: taken, and like a released daemon's holder
	// (no --state), checked by program only. Such a daemon is a build that
	// never shipped.
	fs.String("owner", "", "(ignored: from earlier development builds)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if o.socket == "" || fs.NArg() == 0 {
		return nil, fmt.Errorf("usage: pane-holder --socket PATH [flags] -- command [args...]")
	}
	o.command = fs.Args()
	return o, nil
}

// config is the holder's Config for o, run from exe with args.
func (o *options) config(exe string, args []string, token []byte, lock *holder.StateLock) holder.Config {
	var peer func(holder.PeerProc) bool
	if o.daemon != "" {
		peer = holder.DaemonPeer(o.daemon, installOf(exe))
	}
	cfg := holder.Config{
		Socket: o.socket, Command: o.command, Dir: o.dir, Token: token,
		Cols: uint16(o.cols), Rows: uint16(o.rows), RingSize: o.ring, IdleExit: o.idle,
		Install: installOf(exe), OrphanExit: o.orphan, Peer: peer, State: lock, AdoptOrphans: true,
		AwaitSize: o.awaitSize, Gate: exe,
	}
	if runtime.GOOS == "darwin" && exe != "" {
		cfg.Exe, cfg.Args = exe, args
	}
	return cfg
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case holder.GateArg: // a new pane's program, waiting for its size (holder.Config.AwaitSize)
			holder.RunGate(os.Args[2:])
		case holder.CheckArg: // an update's holder, asked if it takes a holder's pane with these arguments
			if _, err := parseArgs(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			holder.RunCheck()
		case holder.ResumeArg:
			resume(os.Args[2:])
			return
		}
	}
	args := os.Args[1:]
	o, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ignoreSignals()

	var token []byte
	if o.tokenFD >= 0 {
		// Read and closed before the child starts, so it doesn't inherit it.
		f := os.NewFile(uintptr(o.tokenFD), "token")
		b, err := io.ReadAll(io.LimitReader(f, 4096))
		f.Close()
		if err != nil || len(b) == 0 {
			fmt.Fprintln(os.Stderr, "pane-holder: no token on fd", o.tokenFD)
			os.Exit(2)
		}
		token = b
	}

	exe, _ := os.Executable() // "" (no orphan check) if it can't be told
	var lock *holder.StateLock
	if o.state != "" {
		// As it starts, while the daemon that started it holds the locks:
		// which files they are is recorded now, whatever is put at their
		// paths later.
		if lock, err = holder.OpenStateLock(o.state, os.Getppid()); err != nil {
			fmt.Fprintln(os.Stderr, "pane-holder:", err)
			os.Exit(1)
		}
		// Its socket is where that daemon keeps them, by the rule the
		// daemon reconnects by (no other folder, no link in the way).
		if !inPanes(o.socket, o.state) {
			fmt.Fprintln(os.Stderr, "pane-holder: the socket isn't in", filepath.Join(o.state, "run", "panes"))
			os.Exit(2)
		}
	}
	s, err := holder.Start(o.config(exe, args, token, lock))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	serve(s)
}

// resume takes over the pane the holder this process was handed over
// before it exec'd this executable (holder.ResumeArg). args are the
// handoff's descriptor, then the holder's own arguments (also in the
// handoff, which is what counts).
func resume(args []string) {
	ignoreSignals()
	fail := func(err error) {
		// The pane ends with this process: its PTY closes, hanging up the
		// child.
		fmt.Fprintf(os.Stderr, "%s pane-holder: couldn't take the pane over after an update: %v\n", time.Now().Format(time.RFC3339), err)
		os.Exit(1)
	}
	if len(args) == 0 {
		fail(errors.New("no handoff"))
	}
	fd, err := strconv.Atoi(args[0])
	if err != nil {
		fail(err)
	}
	h, err := holder.ReadHandoff(fd)
	if err != nil {
		fail(err)
	}
	o, err := parseArgs(h.Args())
	if err != nil {
		fail(err)
	}
	exe, _ := os.Executable()
	s, err := holder.Resume(o.config(exe, h.Args(), nil, nil), h)
	if err != nil {
		fail(err)
	}
	fmt.Fprintf(os.Stderr, "%s pane-holder: took the pane over after an update (child pid %d)\n", time.Now().Format(time.RFC3339), s.PID())
	serve(s)
}

// ignoreSignals: the daemon may die at any time; never let its signals take
// us down. Notify (not Ignore) so the child still gets default
// dispositions: SIG_IGN would be inherited across exec.
func ignoreSignals() {
	discard := make(chan os.Signal, 8)
	signal.Notify(discard, syscall.SIGHUP, syscall.SIGINT, syscall.SIGPIPE)
	go func() {
		for range discard {
		}
	}()
}

// serve runs until the holder ends, or it's told to (SIGTERM).
func serve(s *holder.Server) {
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
