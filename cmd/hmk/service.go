package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// hmk service install|uninstall|status: run hmkd as a systemd user service
// on Linux (a VPS, a home server), started at boot whether or not you're
// logged in (lingering), as the Mac's login item does there.
//
// KillMode=process: stopping or restarting the service ends hmkd only, not
// the pane holders it started (they're in its cgroup), so panes keep
// running across daemon restarts and upgrades, as on a Mac. hmk stop asks
// hmkd to exit (status 0), which Restart=on-failure leaves stopped.

const serviceName = "hammock.service"

const serviceUsage = "usage: hmk service install|uninstall|status"

func serviceFile() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "systemd", "user", serviceName), nil
}

// serviceUnit is the unit file for hmkd at path, with its state in dir.
func serviceUnit(path, dir string) string {
	return fmt.Sprintf(`# Written by hmk service install: hmk service uninstall removes it.
[Unit]
Description=Hammock daemon (hmkd)
Documentation=https://hammock.jsp.scot
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s --dir %s
Restart=on-failure
RestartSec=2
# Pane holders outlive hmkd: stopping the service ends only the daemon.
KillMode=process
# Panes get a login shell of their own; this is the daemon's PATH.
Environment=PATH=%s

[Install]
WantedBy=default.target
`, systemdQuote(path), systemdQuote(dir), servicePath())
}

// servicePath is the PATH the daemon gets: this one, so it finds tailscale,
// avahi and the agents' CLIs as you do.
func servicePath() string {
	p := os.Getenv("PATH")
	if p == "" {
		p = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	return p
}

// systemdQuote quotes s for an ExecStart line when it needs it.
func systemdQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func systemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// systemdUnit is the service when it's installed for dir and systemd can
// be asked, so hmk start and restart go through it (and it keeps
// supervising hmkd); else "".
func systemdUnit(dir string) string {
	if runtime.GOOS != "linux" {
		return ""
	}
	f, err := serviceFile()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(f)
	if err != nil || !strings.Contains(string(b), "--dir "+systemdQuote(dir)+"\n") {
		return ""
	}
	if exec.Command("systemctl", "--user", "show-environment").Run() != nil {
		return ""
	}
	return serviceName
}

func runService(args []string) error {
	if runtime.GOOS != "linux" {
		return errors.New("hmk service is for Linux: on a Mac, Hammock's app runs the daemon")
	}
	if len(args) == 0 {
		return errors.New(serviceUsage)
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("no systemd here: start hmkd from your init system (hmkd --dir DIR), or with hmk start")
	}
	f, err := serviceFile()
	if err != nil {
		return err
	}
	dir := daemonDir()
	switch args[0] {
	case "install":
		path, err := hammockdPath()
		if err != nil {
			return err
		}
		if path, err = filepath.Abs(path); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(f, []byte(serviceUnit(path, dir)), 0o644); err != nil {
			return err
		}
		if err := systemctl("daemon-reload"); err != nil {
			return err
		}
		// One started by hand (hmk start) gives way to the service's.
		if running, pid := daemonRunning(dir); running {
			if err := stopDaemon(dir, pid, false); err != nil {
				return err
			}
		}
		if err := systemctl("enable", "--now", serviceName); err != nil {
			return err
		}
		fmt.Printf("Installed %s: hmkd runs as a service (%s).\n", serviceName, f)
		// Without lingering, systemd ends your services (and panes) when
		// you log out, and starts them only when you log in.
		if u, err := user.Current(); err == nil {
			if out, err := exec.Command("loginctl", "enable-linger", u.Username).CombinedOutput(); err != nil {
				fmt.Printf("note: couldn't turn on lingering (%v: %s): hmkd stops when you log out until you run\n  sudo loginctl enable-linger %s\n", err, strings.TrimSpace(string(out)), u.Username)
			} else {
				fmt.Println("Lingering is on: it starts at boot and keeps running when you log out.")
			}
		}
		return nil
	case "uninstall", "remove":
		if _, err := os.Stat(f); err != nil {
			fmt.Println("The service isn't installed.")
			return nil
		}
		_ = systemctl("disable", serviceName)
		if err := os.Remove(f); err != nil {
			return err
		}
		_ = systemctl("daemon-reload")
		fmt.Println("Removed the service. hmkd keeps running until you stop it (hmk stop); panes keep running either way.")
		return nil
	case "status":
		if _, err := os.Stat(f); err != nil {
			fmt.Println("The service isn't installed (hmk service install).")
			return nil
		}
		out, _ := exec.Command("systemctl", "--user", "status", "--no-pager", serviceName).CombinedOutput()
		fmt.Print(string(out))
		return nil
	}
	return errors.New(serviceUsage)
}
