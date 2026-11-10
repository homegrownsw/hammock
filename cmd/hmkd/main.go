// Command hmkd is the Hammock daemon.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alexjsp/hammock/internal/daemon"
	"github.com/alexjsp/hammock/internal/hammockconfig"
	"github.com/alexjsp/hammock/internal/holder"
	"github.com/alexjsp/hammock/internal/vt"
)

func main() {
	// Before anything else opens a file: a descriptor it was started with
	// would be open in this signed hmkd as if it were its own, and pane
	// holders take the daemon of their state dir by the lock descriptors it
	// has (holder/statelock.go). Nothing is passed on purpose: the app,
	// launchd, systemd and hmk start it with stdin, stdout and stderr only,
	// and those are kept only if they're what stdio is (no folder, nothing
	// flocked).
	holder.CloseInherited()
	dir := flag.String("dir", daemon.DefaultDir(), "state directory")
	holderPath := flag.String("holder", "", "pane-holder binary (default: next to hmkd)")
	verbose := flag.Bool("v", false, "debug logging")
	remote := flag.Bool("remote", false, "enable remote access on Tailscale addresses (also: hmk remote on)")
	remoteListen := flag.String("remote-listen", "", "comma-separated addresses for remote access instead of the Tailscale ones (development)")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *version {
		fmt.Println(daemon.Version)
		return
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(logOutput(*dir), &slog.HandlerOptions{Level: level}))

	var listen []string
	if *remoteListen != "" {
		listen = strings.Split(*remoteListen, ",")
	}
	// Kitty images sent as a file path (t=f): image-file-medium in Hammock's
	// config, read as each pane's terminal is made.
	vt.ImageFileMedium = func() bool { return hammockconfig.Bool("image-file-medium", true) }
	d, err := daemon.New(daemon.Config{Dir: *dir, HolderPath: *holderPath, Log: log, Remote: *remote, RemoteListen: listen})
	if err != nil {
		if errors.Is(err, daemon.ErrAlreadyRunning) {
			// Not a failure: launchd (KeepAlive) mustn't keep trying.
			fmt.Fprintln(os.Stderr, "hmkd:", err)
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "hmkd:", err)
		os.Exit(1)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Info("shutting down (pane holders keep running)")
		d.Shutdown()
	}()
	err = d.Serve()
	if err == nil {
		// Shutdown (a signal, or a client asking) closes the listener, so Serve
		// returns, before it saves each pane's screen: exit once that's done.
		select {
		case <-d.Done():
		case <-time.After(30 * time.Second):
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "hmkd:", err)
		os.Exit(1)
	}
}

// logOutput is stderr, unless that goes nowhere (launchd starts the login
// item with it on /dev/null): then the state dir's logs/hmkd.log, where the
// app and hmk start already send it.
func logOutput(dir string) *os.File {
	st, err := os.Stderr.Stat()
	null, nerr := os.Stat(os.DevNull)
	if err == nil && nerr == nil && !os.SameFile(st, null) {
		return os.Stderr
	}
	if err := os.MkdirAll(dir+"/logs", 0o700); err != nil {
		return os.Stderr
	}
	f, err := os.OpenFile(dir+"/logs/hmkd.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return os.Stderr
	}
	return f
}
