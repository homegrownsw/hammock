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
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/homegrownsw/hammock/internal/codesign"
)

// hammockAppIDs are the Hammock app's bundle ids (and its menu bar item's).
var hammockAppIDs = []string{
	"com.homegrownsw.hammock", "com.homegrownsw.hammock.beta",
	"com.homegrownsw.hammock.menu", "com.homegrownsw.hammock.beta.menu",
}

// isHammockApp reports whether tok's process, as it was when the token was
// taken, is the Hammock app (or its menu bar item): signed by this daemon's
// own team with the hardened runtime, as helpers/common/peer.h checks.
// Signed ad hoc (a development build), its program must be one of the app
// this daemon is in, with the hardened runtime if this daemon has it (no
// DYLD_INSERT_LIBRARIES). By token, never pid; and the token must be one
// fixed when it was sent, the app's proof's (appproof.go): a socket's peer
// token names whatever runs in the pid that last used it when it's asked,
// so a program that connects and execs a suspended copy of the app in its
// pid passes for it there.
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
	// Each bundle's own program, as its Info.plist names it: not just any
	// program in the bundle. Weaker than a signature still: an ad-hoc
	// build's bundle is yours to write, so a program of yours can put
	// itself there (§5.6); release and TestFlight builds go by team above.
	menu := filepath.Join(app, "Contents", "Library", "LoginItems", "Hammock Menu.app")
	return exe == bundleExecutable(app, "Hammock") || exe == bundleExecutable(menu, "Hammock Menu")
}

// bundleExecutable is the program of the app bundle at app, as its
// Info.plist's CFBundleExecutable names it (def if it can't be read: a
// binary plist, say).
func bundleExecutable(app, def string) string {
	name := def
	if b, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist")); err == nil {
		if m := bundleExecutableRE.FindSubmatch(b); m != nil && !strings.Contains(string(m[1]), "/") {
			name = string(m[1])
		}
	}
	return filepath.Join(app, "Contents", "MacOS", name)
}

var bundleExecutableRE = regexp.MustCompile(`<key>CFBundleExecutable</key>\s*<string>([^<]+)</string>`)

// personPage: there's no Unraid plugin page on macOS.
func personPage(int) bool { return false }

// responsibleFor is the process macOS holds responsible for pid (asks
// about its privacy requests): the app it was started from, or a pane's
// holder, however many times it was forked away from them. 0 if unknown.
func responsibleFor(pid int) int {
	return int(C.hmk_responsible(C.int(pid)))
}

// writePersonKey: only Linux's Unraid page needs one (person_linux.go).
func writePersonKey(string, *slog.Logger) {}
