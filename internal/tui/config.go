package tui

import (
	"bufio"
	"os"
	"strings"
	"time"

	"github.com/alexjsp/hammock/internal/hammockconfig"
)

// The TUI reads Hammock's config file (~/.config/hammock/config, or a β's: hammockconfig.Dir;
// Ghostty syntax, shared with the Mac app):
//
//	tui-animations = false   # snap instead of animating (springs, spinners, the working arc)
//	tui-nerd-font = true     # program icons as Nerd Font glyphs (needs a Nerd Font)
//	pane-icon = btop=monitor # a program's icon (also the Mac's; here, icons with a glyph)
//	editor-wrap, editor-line-numbers, editor-tab-width, editor-indent
//	editor-tui-external = true # open editor panes in $VISUAL/$EDITOR (external.go)
//
// Edits apply within a couple of seconds.

// animations is whether the TUI animates (tui-animations, or the TUI's
// Settings, which win).
var animations = true

// animationsPref is the Settings choice, if made.
var animationsPref *bool

type config struct {
	path    string
	modTime time.Time
	checked time.Time
}

func newConfig() *config {
	c := &config{path: hammockconfig.Path()}
	c.load()
	return c
}

// poll reloads the file if it changed; reports whether it did.
func (c *config) poll() bool {
	if time.Since(c.checked) < 2*time.Second {
		return false
	}
	c.checked = time.Now()
	st, err := os.Stat(c.path)
	var mt time.Time
	if err == nil {
		mt = st.ModTime()
	}
	if mt.Equal(c.modTime) {
		return false
	}
	c.load()
	return true
}

func (c *config) load() {
	c.checked = time.Now()
	animations, nerdFont = true, false
	overrides := map[string]string{}
	values := map[string]string{}
	// The TUI's Settings win over the file, whether or not there is one.
	defer func() {
		iconOverrides = overrides
		editorPrefs = hammockconfig.EditorSettings(values)
		if animationsPref != nil {
			animations = *animationsPref
		}
		if nerdFontPref != nil {
			nerdFont = *nerdFontPref
		}
	}()
	f, err := os.Open(c.path)
	if err != nil {
		c.modTime = time.Time{}
		return
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil {
		c.modTime = st.ModTime()
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		if _, seen := values[strings.TrimSpace(k)]; !seen {
			values[strings.TrimSpace(k)] = v // the first one counts, as the Mac reads it
		}
		switch strings.TrimSpace(k) {
		case "tui-animations":
			animations = v != "false"
		case "tui-nerd-font":
			nerdFont = v == "true"
		case "pane-icon":
			parsePaneIcon(v, overrides)
		}
	}
}
