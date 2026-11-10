package tui

import (
	"fmt"
	"strconv"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
)

// The TUI's Settings screen (⌃B s, or Settings… in the palette): its own
// preferences, kept in ~/.config/hammock/tui.json with the rest of its
// state, and editor panes', which are the config file's (shared with the
// Mac and iPhone).

type setting struct {
	label  string
	value  func(a *App) string
	change func(a *App, d int) // d: +1/-1 from arrows; 0 from Enter or a click
	note   string              // said under the list while it's chosen
}

func onOff(b bool) string {
	if b {
		return "On"
	}
	return "Off"
}

// setEditorKey writes an editor-* key to the config file, and takes it at once.
func (a *App) setEditorKey(key, value string) {
	if err := hammockconfig.Set(key, value); err != nil {
		a.flash("⚠ " + err.Error())
		return
	}
	editorPrefs = hammockconfig.EditorSettings(hammockconfig.Values())
	for _, v := range a.editors {
		v.reveal = 1
	}
}

func editorSettings() []setting {
	toggle := func(label, key string, get func() bool, note string) setting {
		return setting{label, func(*App) string { return onOff(get()) }, func(a *App, _ int) {
			a.setEditorKey(key, strconv.FormatBool(!get()))
		}, note}
	}
	return []setting{
		toggle("Editor: wrap lines", "editor-wrap", func() bool { return editorPrefs.Wrap }, ""),
		toggle("Editor: line numbers", "editor-line-numbers", func() bool { return editorPrefs.LineNumbers }, ""),
		{"Editor: tab width", func(*App) string { return strconv.Itoa(editorPrefs.TabWidth) }, func(a *App, d int) {
			w := editorPrefs.TabWidth + d
			if d == 0 {
				w = map[int]int{2: 4, 4: 8}[editorPrefs.TabWidth]
				if w == 0 {
					w = 2
				}
			}
			a.setEditorKey("editor-tab-width", strconv.Itoa(min(max(w, 1), 16)))
		}, "A file's .editorconfig, or the way it already indents, comes first."},
		{"Editor: indent with", func(*App) string {
			if editorPrefs.Indent == "tabs" {
				return "Tabs"
			}
			return "Spaces"
		}, func(a *App, _ int) {
			if editorPrefs.Indent == "tabs" {
				a.setEditorKey("editor-indent", "spaces")
			} else {
				a.setEditorKey("editor-indent", "tabs")
			}
		}, "A file's .editorconfig, or the way it already indents, comes first."},
		toggle("Editor: open in $EDITOR", "editor-tui-external", func() bool { return editorPrefs.TUIExternal },
			"Edits from your other devices show in $EDITOR only when it reloads the file."),
	}
}

type settingsView struct {
	sel   int
	open  *spring
	items []setting
}

func (a *App) openSettings() {
	v := &settingsView{open: newSpring(gentle), items: []setting{
		{"Tabs", func(a *App) string {
			if a.horizontal() {
				return "Horizontal"
			}
			return "Vertical"
		}, func(a *App, _ int) {
			if a.horizontal() {
				a.prefs.Layout = ""
			} else {
				a.prefs.Layout = "horizontal"
			}
			a.sizes = map[string][2]int{} // panes get a row more or less
		}, ""},
		{"Animations", func(a *App) string {
			if animations {
				return "On"
			}
			return "Off"
		}, func(a *App, _ int) {
			on := !animations
			animations, animationsPref, a.prefs.Animations = on, &on, &on
		}, ""},
		{"Agents in collapsed Groups", func(a *App) string {
			if a.prefs.CollapsedAgents {
				return "Shown"
			}
			return "Hidden"
		}, func(a *App, _ int) {
			a.prefs.CollapsedAgents = !a.prefs.CollapsedAgents
		}, ""},
		{"Program icons", func(a *App) string {
			if nerdFont {
				return "Nerd Font"
			}
			return "Off"
		}, func(a *App, _ int) {
			on := !nerdFont
			nerdFont, nerdFontPref, a.prefs.NerdFont = on, &on, &on
		}, ""},
		{"Sidebar width", func(a *App) string {
			if a.prefs.Sidebar == 0 {
				return fmt.Sprintf("Automatic (%d)", a.sidebarWidth())
			}
			return fmt.Sprint(a.sidebarWidth())
		}, func(a *App, d int) {
			if d == 0 {
				a.prefs.Sidebar = 0 // back to automatic
				return
			}
			a.prefs.Sidebar = max(18, min(a.sidebarWidth()+2*d, a.w/2))
			a.sizes = map[string][2]int{}
		}, ""},
	}}
	v.items = append(v.items, editorSettings()...)
	v.open.from(0)
	v.open.to(1)
	a.overlay = v
}

func (v *settingsView) frame(a *App) rect {
	w, h := min(64, a.w-4), len(v.items)+8
	return rect{(a.w - w) / 2, max(1, (a.h-h)/3), w, h}
}

func (v *settingsView) draw(a *App) {
	if v.open.step() {
		a.dirty = true
	}
	t := clamp01(v.open.pos)
	a.dimBehind(t)
	th, s := a.th, a.scr
	r := v.frame(a)
	inner := a.box(r, th.menu, th.border)
	s.Text(inner.x+2, inner.y, inner.w-4, "Settings", Cell{FG: th.fg, HasFG: true, BG: th.menu, HasBG: true, Bold: true})
	for i, it := range v.items {
		y := inner.y + 2 + i
		bg := th.menu
		fg := th.fg
		if i == v.sel {
			bg, fg = th.menuSel, RGB{255, 255, 255}
		}
		s.Paint(inner.x+1, y, inner.w-2, 1, bg)
		s.Text(inner.x+2, y, inner.w-4, it.label, Cell{FG: fg, HasFG: true, BG: bg, HasBG: true})
		val := "‹ " + it.value(a) + " ›"
		s.TextRight(inner.x+2, y, inner.w-4, val, Cell{FG: fg, HasFG: true, BG: bg, HasBG: true})
	}
	if note := v.items[v.sel].note; note != "" {
		for i, l := range wrapWords(note, inner.w-4, 2) {
			s.Text(inner.x+2, inner.y+inner.h-3+i, inner.w-4, l, Cell{FG: th.dim, HasFG: true, BG: th.menu, HasBG: true, Italic: true})
		}
	}
	hint := "↑↓ choose   ←→ or Enter change   Esc done"
	s.Text(inner.x+2, inner.y+inner.h-1, inner.w-4, truncate(hint, inner.w-4), Cell{FG: th.dim, HasFG: true, BG: th.menu, HasBG: true})
}

func (v *settingsView) change(a *App, d int) {
	v.items[v.sel].change(a, d)
	a.prefs.save()
	a.dirty = true
}

func (v *settingsView) key(a *App, ks []key) {
	for _, k := range ks {
		switch k.name {
		case "esc", "ctrl-c":
			a.closeOverlay()
			return
		case "up", "shift-tab":
			v.sel = (v.sel + len(v.items) - 1) % len(v.items)
		case "down", "tab":
			v.sel = (v.sel + 1) % len(v.items)
		case "left":
			v.change(a, -1)
		case "right":
			v.change(a, 1)
		case "enter":
			v.change(a, 0)
		case "":
			if k.r == ' ' {
				v.change(a, 0)
			} else if k.r == 'q' {
				a.closeOverlay()
				return
			}
		}
	}
	a.dirty = true
}

func (v *settingsView) mouse(a *App, m mouseEv) {
	r := v.frame(a)
	inner := rect{r.x + 1, r.y + 1, r.w - 2, r.h - 2}
	if i := m.y - inner.y - 2; inner.contains(m.x, m.y) && i >= 0 && i < len(v.items) {
		v.sel = i
		if m.press && m.button == 0 {
			v.change(a, 0)
		}
		a.dirty = true
		return
	}
	if m.press && !r.contains(m.x, m.y) {
		a.closeOverlay()
	}
}
