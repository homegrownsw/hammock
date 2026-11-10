package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/homegrownsw/hammock/internal/daemon"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// hmk stop|start|restart: manage the daemon for this state dir. Pane
// holders outlive the daemon, so panes keep running across all three.

// daemonDir is the state dir of the daemon this CLI talks to.
func daemonDir() string {
	// SocketPath is <dir>/run/hmkd.sock.
	return filepath.Dir(filepath.Dir(socketPath()))
}

// daemonRunning reports whether a daemon holds this dir's lock (one daemon
// per dir), and its pid if it wrote one.
func daemonRunning(dir string) (bool, int) {
	path := filepath.Join(dir, "run", "daemon.lock")
	f, err := os.Open(path)
	if err != nil {
		return false, 0
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false, 0
	}
	b, _ := os.ReadFile(path)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return true, pid
}

// launchdTarget is the login item's launchd service (gui/UID/label) when
// it's loaded and runs the daemon for dir, else "".
func launchdTarget(dir string) string {
	label := daemon.BundleAgentLabel()
	if label == "" || daemon.BundleDefaultDir() != dir {
		return ""
	}
	t := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	if exec.Command("launchctl", "print", t).Run() != nil {
		return ""
	}
	return t
}

func waitFor(d time.Duration, cond func() bool) bool {
	for end := time.Now().Add(d); ; {
		if cond() {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runStop(args []string) error {
	endPanes := false
	for _, a := range args {
		switch a {
		case "--panes":
			endPanes = true
		default:
			return fmt.Errorf("stop: unknown argument %q (usage: hmk stop [--panes])", a)
		}
	}
	if endPanes {
		// Run in a pane, ending it hangs up this too: finish the job first.
		signal.Ignore(syscall.SIGHUP)
	}
	dir := daemonDir()
	running, pid := daemonRunning(dir)
	if !running {
		if endPanes {
			// Holders left behind by a daemon that's gone: end them here.
			n, err := daemon.EndPaneHolders(dir, paneEnv("PANE_ID"))
			if err != nil {
				return fmt.Errorf("ending panes: %w", err)
			}
			fmt.Printf("hmkd isn't running; ended %d pane holder(s)\n", n)
			return nil
		}
		fmt.Println("hmkd isn't running")
		return nil
	}
	if err := stopDaemon(dir, pid, endPanes); err != nil {
		return err
	}
	// The login item from older builds (KeepAlive) and an open Hammock app
	// both start a daemon as soon as it's gone.
	if waitFor(2*time.Second, func() bool { r, p := daemonRunning(dir); return r && p != pid }) {
		return errors.New("hmkd stopped, but something started it again: an open Hammock app does, and so does the login item in builds before this command")
	}
	if endPanes {
		// Any the daemon couldn't reach (or didn't know about); none if a
		// new daemon has started meanwhile.
		_, _ = daemon.EndPaneHolders(dir, paneEnv("PANE_ID"))
		fmt.Println("hmkd stopped, and its panes ended")
		return nil
	}
	fmt.Println("hmkd stopped (panes keep running)")
	return nil
}

// stopDaemon asks the daemon to exit (SIGTERM if it can't be asked) and
// waits until it has saved every pane's screen and let go of the dir. With
// endPanes, the daemon ends every pane's holder (and program) as it goes.
func stopDaemon(dir string, pid int, endPanes bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	asked := false
	if c, err := connect(ctx); err == nil {
		_, err = c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ShutdownDaemon{ShutdownDaemon: &pb.ShutdownDaemon{EndPanes: endPanes}}})
		c.Close()
		asked = err == nil
	}
	if !asked {
		if pid <= 0 {
			return errors.New("can't reach hmkd to stop it, and it hasn't recorded its pid")
		}
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("stopping hmkd (pid %d): %w", pid, err)
		}
	}
	// Wait for that process to go: the app (or launchd) may start another
	// before this sees the dir unlocked.
	gone := func() bool {
		r, p := daemonRunning(dir)
		return !r || (pid > 0 && p != pid && !processAlive(pid))
	}
	if !waitFor(35*time.Second, gone) {
		return errors.New("hmkd hasn't exited yet")
	}
	// It lets go of the dir a moment before it exits; launchd ignores a
	// kickstart while the old one is still running, so wait it out.
	if pid > 0 {
		waitFor(10*time.Second, func() bool { return !processAlive(pid) })
	}
	return nil
}

func runStart() error {
	dir := daemonDir()
	if running, pid := daemonRunning(dir); running {
		fmt.Printf("hmkd is already running (pid %d)\n", pid)
		return nil
	}
	if err := startDaemon(dir); err != nil {
		return err
	}
	fmt.Println("hmkd started")
	return nil
}

// startDaemon starts the login item through launchd if it's loaded (so
// launchd keeps supervising it), or the systemd service (hmk service), else
// spawns hmkd in its own session, and
// waits until it accepts connections.
func startDaemon(dir string) error {
	if t := launchdTarget(dir); t != "" {
		if out, err := exec.Command("launchctl", "kickstart", t).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl kickstart %s: %v: %s", t, err, strings.TrimSpace(string(out)))
		}
	} else if u := systemdUnit(dir); u != "" {
		if err := systemctl("start", u); err != nil {
			return err
		}
	} else {
		path, err := hammockdPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o700); err != nil {
			return err
		}
		logf, err := os.OpenFile(filepath.Join(dir, "logs", "hmkd.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		defer logf.Close()
		cmd := exec.Command(path, "--dir", dir)
		cmd.Stdout, cmd.Stderr = logf, logf
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting %s: %w", path, err)
		}
		_ = cmd.Process.Release()
	}
	ok := waitFor(10*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		c, err := connect(ctx)
		if err != nil {
			return false
		}
		c.Close()
		return true
	})
	if !ok {
		return fmt.Errorf("hmkd didn't start: see %s", filepath.Join(dir, "logs", "hmkd.log"))
	}
	return nil
}

// hammockdPath finds the daemon that goes with this CLI: in the app bundle
// (Contents/Helpers/hmk → Contents/MacOS/hmkd), beside it (bin/), or on PATH.
func hammockdPath() (string, error) {
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		d := filepath.Dir(exe)
		for _, p := range []string{filepath.Join(d, "..", "MacOS", "hmkd"), filepath.Join(d, "hmkd")} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return filepath.Clean(p), nil
			}
		}
	}
	if p, err := exec.LookPath("hmkd"); err == nil {
		return p, nil
	}
	return "", errors.New("can't find hmkd")
}

func runRestart() error {
	dir := daemonDir()
	if running, pid := daemonRunning(dir); running {
		if err := stopDaemon(dir, pid, false); err != nil {
			return err
		}
	}
	// Something else (the app, an old login item) may already have started
	// one; that'll do.
	if running, _ := daemonRunning(dir); !running {
		if err := startDaemon(dir); err != nil {
			return err
		}
	}
	fmt.Println("hmkd restarted")
	return nil
}
