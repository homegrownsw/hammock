package tui

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Opening links, as the Mac app does: web and mail links open; a file
// opens unless it's a program or an app (shown in Finder instead); any
// other scheme (another app's, which could do anything) asks first.

type linkAction int

const (
	linkOpen   linkAction = iota // hand it to `open`
	linkReveal                   // show the file in Finder
	linkAsk                      // ask before opening
)

// launchable are kinds of file that run something when opened.
var launchable = map[string]bool{
	".app": true, ".command": true, ".tool": true, ".terminal": true, ".workflow": true, ".action": true,
	".pkg": true, ".mpkg": true, ".scpt": true, ".scptd": true, ".applescript": true, ".jar": true,
	".fileloc": true, ".inetloc": true, ".webloc": true,
}

// classifyLink says what opening u does, and for a file its path.
func classifyLink(u string) (linkAction, string) {
	pu, err := url.Parse(u)
	if err != nil {
		return linkAsk, ""
	}
	switch strings.ToLower(pu.Scheme) {
	case "http", "https", "mailto":
		return linkOpen, ""
	case "file":
		path := filepath.Clean(pu.Path)
		if pu.Path == "" || (pu.Host != "" && pu.Host != "localhost") {
			return linkAsk, ""
		}
		if launchable[strings.ToLower(filepath.Ext(path))] {
			return linkReveal, path
		}
		if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return linkReveal, path
		}
		return linkOpen, path
	}
	return linkAsk, ""
}

// openLink opens u by the rules above. Where nothing can open it (a
// server reached over SSH, with no desktop), it's copied instead: your own
// terminal's clipboard is where it's useful.
func (a *App) openLink(u string) {
	action, path := classifyLink(u)
	opener := linkOpener()
	if opener == "" {
		if path != "" && action != linkAsk {
			u = path
		}
		a.copyText(u)
		a.flash("Copied: " + u)
		return
	}
	switch action {
	case linkOpen:
		if path != "" {
			_ = exec.Command(opener, path).Start()
		} else {
			_ = exec.Command(opener, u).Start()
		}
	case linkReveal:
		if runtime.GOOS == "darwin" {
			_ = exec.Command(opener, "-R", path).Start()
			a.flash("Shown in Finder: " + filepath.Base(path))
		} else {
			_ = exec.Command(opener, filepath.Dir(path)).Start()
			a.flash("Opened: " + filepath.Dir(path))
		}
	case linkAsk:
		lines := []string{"This link opens another app:", ""}
		for r := []rune(u); len(r) > 0; {
			n := min(len(r), 60)
			lines = append(lines, string(r[:n]))
			r = r[n:]
		}
		a.openDialog(&dialog{title: "Open Link?", okLabel: "Open", confirm: true, lines: lines,
			submit: func([]string) { _ = exec.Command(opener, u).Start() }})
	}
}

// linkOpener is what opens links here: open on a Mac; xdg-open on a Linux
// desktop; nothing over SSH or without a display.
func linkOpener() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return ""
	}
	if p, err := exec.LookPath("xdg-open"); err == nil {
		return p
	}
	return ""
}
