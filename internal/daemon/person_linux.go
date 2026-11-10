package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/alexjsp/hammock/internal/codesign"
	"github.com/alexjsp/hammock/internal/procinfo"
)

// isHammockApp: there's no Hammock app on Linux.
func isHammockApp(int, codesign.Token) bool { return false }

// responsibleFor: Linux keeps no such record.
func responsibleFor(int) int { return 0 }

// loginPrograms start login sessions: a terminal app, and the web
// terminal of Unraid's own UI (ttyd). sshd, console logins and Unraid's
// pages (php-fpm) are checked on their own.
var loginPrograms = map[string]bool{
	"ttyd":                  true,
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
func loginAnchor(pid, child, caller int) bool {
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
		// Unraid's web UI, running the Hammock page's action.php: only
		// with the key it read (any PHP of root's can reach php-fpm).
		return hasPersonKey(caller)
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

// Unraid's Hammock page (action.php) runs hmk for you from php-fpm. Any
// program of root's can send php-fpm a FastCGI request naming a script of
// its own, so php-fpm's children aren't you by being its children: the
// page reads a key hmkd leaves in its state folder (root's only) and
// passes it to hmk in its environment (HMK_PERSON_KEY), which hmkd reads
// from the process, as it reads SSH_CONNECTION.
//
// On Unraid everything runs as root, agents included, and root reads any
// file: there the person boundary is nominal, a guardrail against an agent
// wandering, not against one set on getting through.

const personKeyFile = "person.key"

var personKey atomic.Value // string

// writePersonKey makes a new key for this run of the daemon, in dir.
func writePersonKey(dir string, log *slog.Logger) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return
	}
	k := hex.EncodeToString(b)
	p := filepath.Join(dir, personKeyFile)
	tmp := p + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(k+"\n"), 0o600); err != nil || os.Rename(tmp, p) != nil {
		log.Warn("can't write the web page's key: Unraid's page can't do what only you may", "err", err)
		_ = os.Remove(tmp)
		return
	}
	personKey.Store(k)
}

// hasPersonKey reports whether pid was started with this run's key.
func hasPersonKey(pid int) bool {
	k, _ := personKey.Load().(string)
	if k == "" {
		return false
	}
	for _, kv := range procinfo.Env(pid) {
		if v, ok := strings.CutPrefix(kv, "HMK_PERSON_KEY="); ok {
			return subtle.ConstantTimeCompare([]byte(v), []byte(k)) == 1
		}
	}
	return false
}
