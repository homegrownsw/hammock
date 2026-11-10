package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/alexjsp/hammock/internal/codesign"
	"github.com/alexjsp/hammock/internal/procinfo"
)

// isHammockApp: there's no Hammock app on Linux.
func isHammockApp(int, codesign.Token) bool { return false }

// responsibleFor: Linux keeps no such record.
func responsibleFor(int) int { return 0 }

// loginPrograms start login sessions: a terminal app,
// and the web terminal and pages of Unraid's own UI (ttyd, php-fpm), whose
// Hammock page runs hmk. sshd is checked on its own.
var loginPrograms = map[string]bool{
	"ttyd": true, "php-fpm": true,
	"gnome-terminal-server": true, "kgx": true, "ptyxis": true, "ptyxis-agent": true, "konsole": true,
	"xterm": true, "uxterm": true, "urxvt": true, "rxvt": true, "kitty": true, "alacritty": true,
	"wezterm-gui": true, "foot": true, "tilix": true, "terminator": true, "xfce4-terminal": true,
	"mate-terminal": true, "lxterminal": true, "qterminal": true, "terminology": true, "st": true,
	"sakura": true, "guake": true, "yakuake": true, "ghostty": true, "io.elementary.terminal": true,
	"cosmic-term": true, "warp": true, "tabby": true,
}

// systemDirs hold programs installed for the computer, not by a user: a
// login program elsewhere could be anything of yours under that name.
var systemDirs = []string{"/usr/", "/bin/", "/sbin/", "/lib/", "/lib64/", "/opt/", "/snap/", "/nix/store/"}

// loginAnchor reports whether pid starts a login session: sshd from
// another computer, or one of loginPrograms, installed for the computer.
// login only as a console's: run as root (it stays root while the session
// runs) by init, as getty leaves it; one started from anywhere else (`login
// -f` under a program of root's, which needs no password) isn't one.
//
// Residual: a program of yours can open a terminal on a command of its
// choosing (`setsid -f gnome-terminal -- hmk …`): a window you'd see.
func loginAnchor(pid, child, _ int) bool {
	exe, err := procinfo.Path(pid)
	if err != nil {
		return false
	}
	sys := false
	for _, d := range systemDirs {
		sys = sys || strings.HasPrefix(exe, d)
	}
	if !sys {
		return false
	}
	name := filepath.Base(exe)
	switch {
	case name == "sshd" || name == "sshd-session":
		return sshFromElsewhere(child)
	case strings.HasPrefix(name, "php-fpm"): // php-fpm8.3
		return true
	case name == "login":
		pp, err := procinfo.Parent(pid)
		return err == nil && pp == 1 && ownerUID(pid) == 0
	}
	return loginPrograms[name]
}

// ownerUID is the user pid runs as (-1 if it can't be told).
func ownerUID(pid int) int {
	fi, err := os.Stat("/proc/" + strconv.Itoa(pid))
	if err != nil {
		return -1
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}
