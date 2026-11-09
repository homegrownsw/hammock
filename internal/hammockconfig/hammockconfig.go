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

	"github.com/alexjsp/hammock/internal/appbundle"
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
	if f, err := strconv.ParseFloat(strings.TrimSpace(values["editor-font-size"]), 64); err == nil && f > 0 {
		e.FontSize = f
	}
	return e
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

// header starts a config file Hammock creates: a copy of the Mac app's
// (AppConfig.header in apple/Sources/HammockMac/AppConfig.swift; keep them
// the same), so the file says what it can hold whichever wrote it first.
const header = `# Hammock config (Ghostty syntax). Changes apply automatically.
#   appearance = auto | light | dark
#   app-theme = Hammock            (Hammock, Native, Graphite, Ocean, Forest, Rose, Lavender, Sand, Catppuccin,
#                                   Tokyo Night, Gruvbox, Nord, Rosé Pine, Solarized; or custom: the theme, window-theme,
#                                   agent-color, active-pane-border-color, sidebar-color and toolbar-color keys apply)
#   theme = dark:NAME,light:NAME   (Ghostty theme files; see Settings ▸ Appearance)
#   font-family = NAME, font-size = 15, font-thicken = false
#   adjust-cell-height = 20%       (terminal line spacing: a percentage of the line height, or points; negative: tighter)
#   cursor-style = block | bar | bar_thin | underline | block_hollow, cursor-style-blink = true
#   copy-on-select = false
#   clipboard-paste-protection = true   (ask before a paste that could run commands)
#   clipboard-paste-bracketed-safe = true   (no need to ask when the program takes bracketed pastes)
#   window-theme = Native | Graphite | Hammock | Ocean | Forest | Rose | Lavender | Sand | Match Terminal
#   agent-color = Ocean            (the agent view's colour: a colour or theme name, or #RRGGBB, light:#…,dark:#…)
#   sidebar-color = #RRGGBB, toolbar-color = #RRGGBB   (or light:#…,dark:#…; override the theme)
#   sidebar-opacity = 0 … 1        (0 glassy, 1 opaque)
#   tab-status = icon | trailing   (agent status in a sidebar row's icon, or at its end with the agent's logo as the icon)
#   pane-spacing = compact | spacious   (spacious: split panes spaced apart, with rounded corners)
#   dim-inactive-panes = false     (true: dim the panes you're not typing in, a little)
#   active-pane-border = false     (true: a border around the focused pane)
#   active-pane-border-color = accent   (or #RRGGBB, or light:#…,dark:#…)
#   image-file-medium = true       (Kitty images sent as a file path; false: inline ones only)
#   simulator-shutdown = true      (simulators Hammock booted shut down a minute after their last pane closes)
#   ssh-agent-forwarding = true    (panes on your linked computers use the SSH keys of the one you're at)
#   ssh-upstream-agent = PATH      (the SSH agent Hammock's passes requests on to; default: ~/.ssh/config's IdentityAgent for every host, else SSH_AUTH_SOCK)
#   screen-sharing = false         (your iPhone and other Macs may see and control this Mac's screen)
#   claude-usage-live = false      (Claude's usage limits asked of Anthropic every 5 minutes, with Claude Code's sign-in)
#   menu-bar = always | active     (the menu bar item; active: only while a screen's shared or the server's down)
#   keep-empty-groups = true       (⌘W on a Group's last tab keeps the Group; false: closes it too)
#   notify-needs-input = true      (notify me when an agent waits for my input; and notify-permission, notify-finished, notify-error)
#   notify-style = auto | system | in-app   (auto: in the window while it's in front, else as macOS notifications)
#   notify-sound = true, notify-current-tab = false, dock-badge = true   (current tab: also for the tab I'm looking at)
#   sidebar-layout = vertical | compact | horizontal   (horizontal: the Group's tabs in a tab bar along the top)
#   sidebar-finished = true        (finished agents get their own list at the bottom of the sidebar)
#   collapsed-group-agents = false (true: a collapsed Group still shows its tabs with an active agent)
#   confirm-close-busy-agent = true   (ask before closing a pane or tab whose agent is at work)
#   simulator-frame = true         (simulator panes draw the device around the screen)
#   agent-default-view = terminal | agent   (which view agent panes open in)
#   agent-font-family = NAME, agent-font-size = 14, agent-line-spacing = 3   (the agent view's text; unset: the system font)
#   agent-code-font-family = NAME, agent-code-font-size = 13   (its code; unset: the terminal's font)
#   usage-limits-claude = false, usage-limits-codex = false   (an agent's usage limits in the sidebar)
#   browser-open-schemes = obsidian,zoommtg   (links a browser pane opens in their app without asking)
#   browser-new-tab = external | hammock   (links a page opens in a new tab: your default browser, or a Hammock browser tab)
#   keybind = cmd+shift+b=new_browser_tab   (one line per shortcut; Settings ▸ Keyboard lists every action's name)
#   keybind = cmd+j=unbind, keybind = none=new_tab, keybind = ctrl+cmd+digits=select_tab (⌃⌘1…9), keybind = clear
#   tui-animations = true          (hmk tui: false snaps instead of animating)
#   tui-nerd-font = false          (hmk tui: true shows program icons as Nerd Font glyphs)
#   tab-close-button = true        (false: no close button on a tab you hover over, in the sidebar or the tab bar)
#   program-icons = plain | colorful | off   (a terminal's program: its icon, on a squircle in its colours, or none; agents keep their logo)
#   pane-icon = btop=monitor       (a program's icon: a built-in one, like vim, git, python, monitor, or an SF Symbol; several with commas, or repeat the key)
#   editor-wrap = false            (editor panes: wrap long lines instead of scrolling sideways)
#   editor-line-numbers = true     (editor panes: line numbers in the gutter)
#   editor-tab-width = 4, editor-indent = spaces | tabs   (what Tab inserts; a file's .editorconfig beats them)
#   editor-font-size = 13          (editor panes on the Mac and iPhone; unset: the terminal's font size)
#   editor-vim = false             (editor panes on the Mac and iPhone: Vim keys)
#   editor-tui-external = false    (hmk tui: open editor panes in $VISUAL or $EDITOR; edits from your other devices show there only when it reloads the file)`
