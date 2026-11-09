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

// isHammockApp reports whether pid is the Hammock app (or its menu bar
// item): signed by this daemon's own team with the hardened runtime, as
// helpers/common/peer.h checks. Signed ad hoc (a development build), its
// program must be one of the app this daemon is in.
func isHammockApp(pid int) bool {
	self, _ := codesign.Self()
	if self.Team != "" {
		var ids []string
		for _, id := range hammockAppIDs {
			ids = append(ids, fmt.Sprintf("identifier %q", id))
		}
		req := fmt.Sprintf("anchor apple generic and certificate leaf[subject.OU] = %q and (%s)", self.Team, strings.Join(ids, " or "))
		s, ok := codesign.Check(pid, req)
		return ok && s.Runtime
	}
	app := daemonInstall()
	exe, err := procinfo.Path(pid)
	if app == "" || err != nil {
		return false
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
// caller: login (Terminal, iTerm, Ghostty and others start shells through
// it), sshd from another computer, or an app that starts shells itself
// (kitty, WezTerm, VS Code, another copy of Hammock): one launched as an
// app (launchd's child, its bundle's program), signed by Apple or a
// developer Apple knows, with caller in a terminal.
func loginAnchor(pid, child, caller int) bool {
	exe, err := procinfo.Path(pid)
	if err != nil {
		return false
	}
	switch exe {
	case "/usr/bin/login":
		return true
	case "/usr/sbin/sshd", "/usr/libexec/sshd-session":
		return sshFromElsewhere(child)
	}
	if !strings.Contains(exe, ".app/Contents/MacOS/") || procinfo.TTY(caller) < 0 {
		return false
	}
	if pp, err := procinfo.Parent(pid); err != nil || pp != 1 {
		return false
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
