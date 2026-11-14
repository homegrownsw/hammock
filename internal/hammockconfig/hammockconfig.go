// Package hammockconfig reads Hammock's config file, ~/.config/hammock/config (or
// $HMK_CONFIG_DIR/config; a β's is in its state dir: Dir), in Ghostty's
// syntax: `key = value` lines, # comments. The Mac app writes it (Settings)
// and documents its keys (apple/Sources/HammockMac/AppConfig.swift); the
// daemon and the TUI read the keys that concern them.
package hammockconfig

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/homegrownsw/hammock/internal/appbundle"
)

// DefaultDir is where Hammock's config lives unless told otherwise:
// ~/.config/hammock.
func DefaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "hammock")
}

// Dir is the config's directory: $HMK_CONFIG_DIR; else, in a β (an app
// bundle whose HammockStateDir isn't Hammock's), its state dir, so testing
// never changes your real settings (the Mac app's AppConfig does the same);
// else DefaultDir.
func Dir() string {
	if d := os.Getenv("HMK_CONFIG_DIR"); d != "" {
		return d
	}
	if d := betaDir(); d != "" {
		seedOnce.Do(func() { seed(d) })
		return d
	}
	return DefaultDir()
}

// Path is the config file's path.
func Path() string { return filepath.Join(Dir(), "config") }

func betaDir() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	name := appbundle.Info("HammockStateDir")
	if name == "" || name == "Hammock" {
		return ""
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", name)
}

var seedOnce sync.Once

// seed starts a β's config as a copy of your real one, the first time, so
// it looks like yours until you change it (AppConfig.seed does the same).
func seed(dir string) {
	dst := filepath.Join(dir, "config")
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		return
	}
	data, err := os.ReadFile(filepath.Join(DefaultDir(), "config"))
	if err != nil {
		return
	}
	if os.MkdirAll(dir, 0o755) == nil {
		os.WriteFile(dst, data, 0o644)
	}
}

// Value is key's value (the first one set, as the Mac app reads it), and
// whether it's set.
func Value(key string) (string, bool) {
	f, err := os.Open(Path())
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.Trim(strings.TrimSpace(v), `"`), true
		}
	}
	return "", false
}

// Bool is a true/false key, def when unset or neither.
func Bool(key string, def bool) bool {
	switch v, _ := Value(key); v {
	case "true":
		return true
	case "false":
		return false
	}
	return def
}

// Values is every key's first value, as Value reads them, in one read of
// the file (nil when there's none).
func Values() map[string]string {
	data, err := os.ReadFile(Path())
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if k = strings.TrimSpace(k); ok {
			if _, seen := out[k]; !seen {
				out[k] = strings.Trim(strings.TrimSpace(v), `"`)
			}
		}
	}
	return out
}

// Editor is editor panes' settings, the editor-* keys. A file's
// .editorconfig (EditorInfo.tab_width and indent, from the daemon) beats
// TabWidth and Indent.
type Editor struct {
	Wrap        bool    // editor-wrap: wrap long lines
	LineNumbers bool    // editor-line-numbers
	TabWidth    int     // editor-tab-width (1…16)
	Indent      string  // editor-indent: "spaces" or "tabs"
	FontFamily  string  // editor-font-family (editor and diff panes on the Mac and iPhone; "": the terminal's)
	FontSize    float64 // editor-font-size (the Mac and iPhone; 0: follow the terminal's)
	Vim         bool    // editor-vim (the Mac and iPhone)
	TUIExternal bool    // editor-tui-external: hmk tui opens editor panes in $VISUAL/$EDITOR
}

// EditorSettings reads the editor-* keys from values (Values), with their
// defaults for what's unset or can't be read.
func EditorSettings(values map[string]string) Editor {
	e := Editor{LineNumbers: true, TabWidth: 4, Indent: "spaces"}
	b := func(key string, def bool) bool {
		switch values[key] {
		case "true":
			return true
		case "false":
			return false
		}
		return def
	}
	e.Wrap = b("editor-wrap", e.Wrap)
	e.LineNumbers = b("editor-line-numbers", e.LineNumbers)
	e.Vim = b("editor-vim", false)
	e.TUIExternal = b("editor-tui-external", false)
	if n, err := strconv.Atoi(strings.TrimSpace(values["editor-tab-width"])); err == nil && n >= 1 && n <= 16 {
		e.TabWidth = n
	}
	if v := strings.TrimSpace(values["editor-indent"]); v == "tabs" || v == "spaces" {
		e.Indent = v
	}
	e.FontFamily = strings.TrimSpace(values["editor-font-family"])
	if f, err := strconv.ParseFloat(strings.TrimSpace(values["editor-font-size"]), 64); err == nil && f > 0 {
		e.FontSize = f
	}
	return e
}

// PaneKinds are the tools Hammock shows in panes besides terminals, which
// can be turned off (Settings ▸ Tools): each has a <kind>-enabled key,
// true by default.
// Off, Hammock doesn't offer that kind in its menus, and agents lose its
// tools (hmk mcp) and are refused it by the daemon, so it never gets in the
// way of how someone already works (their own diff reviewer, say).
var PaneKinds = []string{"browser", "simulator", "editor", "diff", "log"}

// PaneKindKey is kind's key: "diff-enabled".
func PaneKindKey(kind string) string { return kind + "-enabled" }

// PaneKindOn reports whether kind is turned on in values (Values): any
// value but "false" is.
func PaneKindOn(values map[string]string, kind string) bool {
	return strings.TrimSpace(values[PaneKindKey(kind)]) != "false"
}

// PaneKindsOff is the kinds turned off in values, in PaneKinds' order.
func PaneKindsOff(values map[string]string) []string {
	var off []string
	for _, k := range PaneKinds {
		if !PaneKindOn(values, k) {
			off = append(off, k)
		}
	}
	return off
}

// Change is one key's new value ("": removed), for SetAll.
type Change struct{ Key, Value string }

// Set sets key (or with value "", removes it), as SetAll.
func Set(key, value string) error { return SetAll(Change{key, value}) }

// SetAll sets keys (or with value "", removes them), keeping the rest of
// the file as it is, as the Mac app's Settings write it (AppConfig.set): a
// key's line goes where its first one was, else at the end. A new file
// starts with the Mac's commented header. A symlinked config (dotfiles)
// stays a symlink: what it points to is written. The Mac app and the daemon
// notice within a few seconds.
func SetAll(changes ...Change) error {
	return rewrite(func(lines []string) []string {
		for _, c := range changes {
			lines = replace(lines, c.Key, c.Value)
		}
		return lines
	})
}

// Repeated is a repeatable key's values, in order, and whether it's set at
// all (a key set to nothing, `key =`, is set, with no values).
func Repeated(key string) ([]string, bool) {
	data, err := os.ReadFile(Path())
	if err != nil {
		return nil, false
	}
	var out []string
	set := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") || strings.TrimSpace(k) != key {
			continue
		}
		set = true
		if v = strings.Trim(strings.TrimSpace(v), `"`); v != "" {
			out = append(out, v)
		}
	}
	return out, set
}

// SetRepeated sets a repeatable key's lines (all of them) where its first
// one was, as SetAll does. No values writes `key =` (set, to nothing), so a
// default doesn't come back; Unset removes the key.
func SetRepeated(key string, values []string) error {
	return rewrite(func(lines []string) []string {
		repl := make([]string, 0, max(1, len(values)))
		for _, v := range values {
			repl = append(repl, key+" = "+v)
		}
		if len(values) == 0 {
			repl = append(repl, key+" =")
		}
		var out []string
		placed := false
		for _, l := range lines {
			t := strings.TrimSpace(l)
			k, _, ok := strings.Cut(t, "=")
			if ok && !strings.HasPrefix(t, "#") && strings.TrimSpace(k) == key {
				if !placed {
					out = append(out, repl...)
					placed = true
				}
				continue
			}
			out = append(out, l)
		}
		if !placed {
			if n := len(out); n > 0 && out[n-1] == "" {
				out = append(out[:n-1], append(repl, "")...)
			} else {
				out = append(out, repl...)
			}
		}
		return out
	})
}

// Unset removes a key's lines.
func Unset(key string) error { return Set(key, "") }

// rewrite applies edit to the file's lines and writes it back.
func rewrite(edit func([]string) []string) error {
	path := Path()
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(string(data), "\n")
	} else if !os.IsNotExist(err) {
		return err
	} else {
		lines = append(strings.Split(header, "\n"), "")
	}
	lines = edit(lines)
	// Written beside it and renamed over it, so a reader never sees half.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(strings.Join(lines, "\n")); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp.Chmod(mode)
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// replace puts key's line where its first one was (dropping repeats), or
// before the file's final newline; value "" removes it.
func replace(lines []string, key, value string) []string {
	isKey := func(line string) bool {
		t := strings.TrimSpace(line)
		k, _, ok := strings.Cut(t, "=")
		return ok && !strings.HasPrefix(t, "#") && strings.TrimSpace(k) == key
	}
	var out []string
	placed := false
	for _, l := range lines {
		if !isKey(l) {
			out = append(out, l)
			continue
		}
		if !placed && value != "" {
			out = append(out, key+" = "+value)
		}
		placed = true
	}
	if !placed && value != "" {
		if n := len(out); n > 0 && out[n-1] == "" {
			out = append(out[:n-1], key+" = "+value, "")
		} else {
			out = append(out, key+" = "+value)
		}
	}
	return out
}

// header starts a config file Hammock creates, listing every key (Keys);
// the Mac app's is the same, generated from it, so the file says what it
// can hold whichever wrote it first.
var header = Header()
