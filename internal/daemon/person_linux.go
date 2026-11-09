package daemon

import (
	"path/filepath"
	"strings"

	"github.com/alexjsp/hammock/internal/procinfo"
)

// isHammockApp: there's no Hammock app on Linux.
func isHammockApp(int) bool { return false }

// responsibleFor: Linux keeps no such record.
func responsibleFor(int) int { return 0 }

// loginPrograms start login sessions: a console's login, a terminal app,
// and the web terminal and pages of Unraid's own UI (ttyd, php-fpm), whose
// Hammock page runs hmk. sshd is checked on its own.
var loginPrograms = map[string]bool{
	"login": true, "ttyd": true, "php-fpm": true,
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
	}
	return loginPrograms[name]
}
