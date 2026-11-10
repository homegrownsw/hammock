package daemon

/*
#include <sys/types.h>

// Private but stable (Activity Monitor, TCC and the like use it): see
// spawn_darwin.go for its other half.
extern pid_t responsibility_get_pid_responsible_for_pid(pid_t) __attribute__((weak_import));

static int hmk_responsible(int pid) {
	return responsibility_get_pid_responsible_for_pid ? responsibility_get_pid_responsible_for_pid(pid) : 0;
}
*/
import "C"

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alexjsp/hammock/internal/codesign"
	"github.com/alexjsp/hammock/internal/procinfo"
)

// hammockAppIDs are the Hammock app's bundle ids (and its menu bar item's).
var hammockAppIDs = []string{
	"com.homegrownsw.hammock", "com.homegrownsw.hammock.beta",
	"com.homegrownsw.hammock.menu", "com.homegrownsw.hammock.beta.menu",
}

// isHammockApp reports whether the process that connected (tok, its audit
// token then) is the Hammock app (or its menu bar item): signed by this
// daemon's own team with the hardened runtime, as helpers/common/peer.h
// checks. Signed ad hoc (a development build), its program must be one of
// the app this daemon is in, with the hardened runtime if this daemon has
// it (no DYLD_INSERT_LIBRARIES). By token, never pid: a program that
// connects and then execs the app in its pid (handing the connection to a
// child) has a new pid version, and doesn't pass.
func isHammockApp(_ int, tok codesign.Token) bool {
	self, _ := codesign.Self()
	if self.Team != "" {
		var ids []string
		for _, id := range hammockAppIDs {
			ids = append(ids, fmt.Sprintf("identifier %q", id))
		}
		req := fmt.Sprintf("anchor apple generic and certificate leaf[subject.OU] = %q and (%s)", self.Team, strings.Join(ids, " or "))
		s, ok := codesign.CheckToken(tok, req)
		return ok && s.Runtime
	}
	app := daemonInstall()
	exe, err := tok.Path()
	if app == "" || err != nil {
		return false
	}
	if self.Runtime {
		if s, ok := codesign.CheckToken(tok, ""); !ok || !s.Runtime {
			return false
		}
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	if r, err := filepath.EvalSymlinks(app); err == nil {
		app = r
	}
	dir := filepath.Dir(exe)
	return dir == filepath.Join(app, "Contents", "MacOS") && filepath.Base(exe) != "hmkd" ||
		dir == filepath.Join(app, "Contents", "Library", "LoginItems", "Hammock Menu.app", "Contents", "MacOS")
}

// loginAnchor reports whether pid starts a login session on this Mac for
// caller: sshd from another computer, or a terminal app (Terminal, iTerm,
// Ghostty, kitty, WezTerm, VS Code, another copy of Hammock): one launched
// as an app (launchd's child, its bundle's program, or iTerm's session
// server), signed by Apple or a developer Apple knows, with caller in a
// terminal.
//
// Not /usr/bin/login on its own: `login -pf $USER` needs no password, so a
// program of yours can start one from launchd (`launchctl submit`) with any
// command, and it would look like a terminal's. The terminal app above it
// is what counts (the walk passes login on its way there).
//
// Residual: a program of yours can open a terminal app on a command of its
// choosing (`open -a Terminal x.command`, `open -na Ghostty --args -e …`):
// that's a window you'd see, launched as an app like any. And an app whose
// program runs scripts when told to (an Electron app's run-as-node) passes
// for one launched as an app: those told to by their environment are
// turned away.
func loginAnchor(pid, child, caller int) bool {
	exe, err := procinfo.Path(pid)
	if err != nil {
		return false
	}
	switch exe {
	case "/usr/sbin/sshd", "/usr/libexec/sshd-session":
		return sshFromElsewhere(child)
	}
	if procinfo.TTY(caller) < 0 {
		return false
	}
	if pp, err := procinfo.Parent(pid); err != nil || pp != 1 {
		return false
	}
	for _, kv := range procinfo.Env(pid) {
		if strings.HasPrefix(kv, "ELECTRON_RUN_AS_NODE=") || strings.HasPrefix(kv, "DYLD_INSERT_LIBRARIES=") {
			return false
		}
	}
	if !strings.Contains(exe, ".app/Contents/MacOS/") {
		// iTerm's session server, which may run from a copy outside
		// the app: by its signature, not its name or place.
		_, ok := codesign.Check(pid, `anchor apple generic and identifier "iTermServer" and certificate leaf[subject.OU] = "H7V7XYVQ7D"`)
		return ok
	}
	_, ok := codesign.Check(pid, "anchor apple generic")
	return ok
}

// responsibleFor is the process macOS holds responsible for pid (asks
// about its privacy requests): the app it was started from, or a pane's
// holder, however many times it was forked away from them. 0 if unknown.
func responsibleFor(pid int) int {
	return int(C.hmk_responsible(C.int(pid)))
}
