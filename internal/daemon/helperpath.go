package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/alexjsp/hammock/internal/codesign"
)

// A daemon started by a program of yours takes the programs it runs for
// panes and helpers from its flags and environment: --holder (each pane's
// holder, which sees all its input and output) and HMK_BROWSER_HOST,
// HMK_SCREEN_HOST, HMK_SIMULATOR_HOST and HMK_KEY_HELPER. In a build signed
// by a team (a release: the daemon is then the boundary, holder/peer.go),
// one given that way is taken only if it's inside the daemon's own app, or
// signed by the same team; otherwise it's ignored, with a line in the log,
// and the app's own is used. Development builds (ad hoc, or none) take any.

// helperLog is the daemon's log, for helperAllowed (set by New).
var helperLog atomic.Pointer[slog.Logger]

// helperRefused is each override refused, so it's logged once.
var helperRefused sync.Map

// signedTeam is the team this daemon is signed by ("" for a development
// build). Replaceable in tests.
var signedTeam = func() string {
	s, _ := codesign.Self()
	return s.Team
}

// helperAllowed reports whether path, given by what (a flag or variable),
// may be run as one of Hammock's own programs.
func helperAllowed(what, path string) bool {
	team := signedTeam()
	if team == "" {
		return true
	}
	p, err := filepath.Abs(path)
	if err == nil {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		if app := ownApp(); app != "" && strings.HasPrefix(p, app+"/") {
			return true
		}
		if codesign.CheckFile(p, `anchor apple generic and certificate leaf[subject.OU] = "`+team+`"`) {
			return true
		}
	}
	if _, seen := helperRefused.LoadOrStore(what+"\x00"+path, true); !seen {
		log := helperLog.Load()
		if log == nil {
			log = slog.Default()
		}
		log.Warn("ignoring a program given from outside: not in this app nor signed by its team; using the app's own", "given", what, "path", path)
	}
	return false
}

// ownApp is the app this daemon is in (Hammock.app, from
// Hammock.app/Contents/MacOS/hmkd), or "".
func ownApp() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	for d := filepath.Dir(exe); d != "/" && d != "."; d = filepath.Dir(d) {
		if strings.HasSuffix(d, ".app") {
			return d
		}
	}
	return ""
}

// helperEnv is the program named by environment variable name, if it's set
// and may be run (helperAllowed).
func helperEnv(name string) string {
	if p := os.Getenv(name); p != "" && helperAllowed(name, p) {
		return p
	}
	return ""
}
