package tui

import (
	"context"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/homegrownsw/hammock/internal/acknowledgements"
	"github.com/homegrownsw/hammock/internal/agents"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

var bgctx = context.Background()

// overlay is something drawn over everything else that takes the keyboard
// and mouse while it's open: a menu, the palette or a dialog.
type overlay interface {
	draw(a *App)
	key(a *App, ks []key)
	mouse(a *App, m mouseEv)
}

func (a *App) closeOverlay() {
	a.overlay = nil
	a.dirty = true
}

// dimBehind fades everything already drawn toward the background, as a
// sheet's scrim does.
func (a *App) dimBehind(amount float64) {
	if amount <= 0 {
		return
	}
	s := a.scr
	for i := range s.cells {
		c := &s.cells[i]
		fg, bg := a.th.fg, a.th.bg
		if c.HasFG {
			fg = c.FG
		}
		if c.HasBG {
			bg = c.BG
		}
		target := mix(a.th.bg, RGB{0, 0, 0}, 0.25)
		if !a.th.dark {
			target = mix(a.th.bg, RGB{0, 0, 0}, 0.12)
		}
		c.FG, c.HasFG = mix(fg, target, 0.55*amount), true
		c.BG, c.HasBG = mix(bg, target, 0.6*amount), true
	}
}

// box draws a rounded panel and returns its inner rect.
func (a *App) box(r rect, bg, border RGB) rect {
	s := a.scr
	s.Fill(r.x, r.y, r.w, r.h, Cell{BG: bg, HasBG: true})
	bc := Cell{FG: border, HasFG: true, BG: bg, HasBG: true}
	for x := r.x + 1; x < r.x+r.w-1; x++ {
		s.Set(x, r.y, withCh(bc, "─"))
		s.Set(x, r.y+r.h-1, withCh(bc, "─"))
	}
	for y := r.y + 1; y < r.y+r.h-1; y++ {
		s.Set(r.x, y, withCh(bc, "│"))
		s.Set(r.x+r.w-1, y, withCh(bc, "│"))
	}
	s.Set(r.x, r.y, withCh(bc, "╭"))
	s.Set(r.x+r.w-1, r.y, withCh(bc, "╮"))
	s.Set(r.x, r.y+r.h-1, withCh(bc, "╰"))
	s.Set(r.x+r.w-1, r.y+r.h-1, withCh(bc, "╯"))
	// A soft shadow to the right and below.
	sh := mix(a.th.bg, RGB{0, 0, 0}, 0.35)
	for y := r.y + 1; y <= r.y+r.h; y++ {
		if c := s.Get(r.x+r.w, y); true {
			c.BG, c.HasBG = mix(bgOf(c, a.th.bg), sh, 0.5), true
			s.Set(r.x+r.w, y, c)
		}
	}
	for x := r.x + 1; x <= r.x+r.w; x++ {
		c := s.Get(x, r.y+r.h)
		c.BG, c.HasBG = mix(bgOf(c, a.th.bg), sh, 0.5), true
		s.Set(x, r.y+r.h, c)
	}
	return rect{r.x + 1, r.y + 1, r.w - 2, r.h - 2}
}

func withCh(c Cell, ch string) Cell { c.Ch = ch; return c }

func bgOf(c Cell, def RGB) RGB {
	if c.HasBG {
		return c.BG
	}
	return def
}

// ---------- menus ----------

type menuItem struct {
	label, hint string
	sep         bool
	disabled    bool
	danger      bool
	run         func()
}

type menu struct {
	x, y  int
	items []menuItem
	sel   int
	top   int // the first item shown, when there are more than fit (scrolled)
	open  *spring
}

func (a *App) openMenu(x, y int, items []menuItem) {
	items = withoutToolsOff(items)
	m := &menu{x: x, y: y, items: items, sel: -1, open: newSpring(gentle)}
	m.open.from(0)
	m.open.to(1)
	a.overlay = m
}

func (m *menu) size() (int, int) {
	w := 0
	for _, it := range m.items {
		w = max(w, textWidth(it.label)+textWidth(it.hint)+6)
	}
	return max(w, 18), len(m.items) + 2
}

func (m *menu) frame(a *App) rect {
	w, h := m.size()
	h = min(h, max(3, a.h)) // taller than the screen: it scrolls
	x := min(m.x, a.w-w-1)
	y := m.y
	if y+h > a.h {
		y = max(0, m.y-h+1)
	}
	return rect{max(0, x), y, w, h}
}

// rows is how many items show at once.
func (m *menu) rows(a *App) int { return max(1, m.frame(a).h-2) }

// scroll moves the items shown by d, within the list.
func (m *menu) scroll(a *App, d int) {
	m.top = max(0, min(m.top+d, len(m.items)-m.rows(a)))
}

// reveal scrolls so the selected item shows.
func (m *menu) reveal(a *App) {
	if m.sel < 0 {
		return
	}
	if n := m.rows(a); m.sel >= m.top+n {
		m.top = m.sel - n + 1
	} else if m.sel < m.top {
		m.top = m.sel
	}
}

func (m *menu) draw(a *App) {
	moving := m.open.step()
	if moving {
		a.dirty = true
	}
	t := clamp01(m.open.pos)
	r := m.frame(a)
	// Opens downward from the click: the panel unrolls.
	shown := max(2, int(math.Round(float64(r.h)*t)))
	r.h = min(r.h, shown)
	bg := mix(a.th.bg, a.th.menu, t)
	inner := a.box(r, bg, mix(bg, a.th.border, t))
	m.top = max(0, min(m.top, len(m.items)-m.rows(a)))
	for i, it := range m.items {
		if i < m.top {
			continue
		}
		y := inner.y + i - m.top
		if y >= inner.y+inner.h {
			break
		}
		if it.sep {
			for x := inner.x; x < inner.x+inner.w; x++ {
				a.scr.Set(x, y, Cell{Ch: "─", FG: a.th.border, HasFG: true, BG: bg, HasBG: true})
			}
			continue
		}
		rowBG := bg
		if i == m.sel {
			rowBG = a.th.menuSel // highlights follow the pointer at once, as in macOS menus
		}
		a.scr.Paint(inner.x, y, inner.w, 1, rowBG)
		fg := mix(bg, a.th.fg, t)
		switch {
		case it.disabled:
			fg = a.th.faint
		case i == m.sel:
			fg = RGB{255, 255, 255}
		case it.danger:
			fg = a.th.red
		}
		a.scr.Text(inner.x+1, y, inner.w-2, it.label, Cell{FG: fg, HasFG: true, BG: rowBG, HasBG: true})
		if it.hint != "" {
			hc := a.th.dim
			if i == m.sel {
				hc = mix(RGB{255, 255, 255}, rowBG, 0.3)
			}
			a.scr.TextRight(inner.x+1, y, inner.w-2, it.hint, Cell{FG: hc, HasFG: true, BG: rowBG, HasBG: true})
		}
	}
}

func (m *menu) move(d int) {
	n := len(m.items)
	for range n {
		m.sel = (m.sel + d + n) % n
		if !m.items[m.sel].sep && !m.items[m.sel].disabled {
			return
		}
	}
}

func (m *menu) choose(a *App) {
	if m.sel < 0 || m.sel >= len(m.items) || m.items[m.sel].sep || m.items[m.sel].disabled {
		return
	}
	run := m.items[m.sel].run
	a.closeOverlay()
	if run != nil {
		run()
	}
}

func (m *menu) key(a *App, ks []key) {
	for _, k := range ks {
		switch k.name {
		case "esc":
			a.closeOverlay()
			return
		case "up", "shift-tab":
			m.move(-1)
			m.reveal(a)
		case "down", "tab":
			m.move(1)
			m.reveal(a)
		case "pgup":
			m.scroll(a, -m.rows(a))
		case "pgdown":
			m.scroll(a, m.rows(a))
		case "enter":
			m.choose(a)
			return
		}
	}
}

func (m *menu) mouse(a *App, ev mouseEv) {
	r := m.frame(a)
	inner := rect{r.x + 1, r.y + 1, r.w - 2, r.h - 2}
	if ev.wheel != 0 && r.contains(ev.x, ev.y) {
		m.scroll(a, ev.wheel)
		return
	}
	if inner.contains(ev.x, ev.y) {
		i := ev.y - inner.y + m.top
		if i >= len(m.items) {
			return
		}
		if !m.items[i].sep && !m.items[i].disabled {
			m.sel = i
		}
		if ev.release && ev.button == 0 || ev.press && ev.button == 2 {
			m.choose(a)
		} else if ev.release && ev.button == 2 && m.sel == i {
			m.choose(a)
		}
		return
	}
	if !r.contains(ev.x, ev.y) {
		m.sel = -1
		if ev.press {
			a.closeOverlay()
		}
	}
}

// The menus themselves.

func (a *App) tabMenu(id string, x, y int) {
	t := a.tabs[id]
	if t == nil {
		return
	}
	items := []menuItem{
		{label: "Rename Tab…", hint: "⌃B ,", run: func() {
			a.openPrompt("Rename Tab", "Name", t.Name, func(v string) { a.renameTab(t.Id, v) })
		}},
		{label: "New Tab", hint: "⌃B c", run: func() { a.newTab(t.GroupId, "") }},
		{label: "New Browser Tab…", hint: "⌃B b", run: func() {
			a.openPrompt("New Browser Tab", "Address", "", func(v string) {
				if v != "" {
					a.newTab(t.GroupId, v)
				}
			})
		}},
		{label: "New Agent Tab…", run: func() { a.openStartAgent(t.GroupId) }},
		{label: "New Log Tab…", run: func() {
			a.openPrompt("New Log Tab", logPromptHint, "", func(v string) { a.openLog(t.GroupId, "", v) })
		}},
		{label: "New Editor Tab", hint: "⌃B E", run: func() { a.openEditor(t.GroupId, "", false) }},
		{label: "Quick Open…", hint: "⌃B e", run: a.openQuickOpen},
	}
	if canSchedule(a.group(t.GroupId)) {
		items = append(items, menuItem{label: "Schedule This…", run: func() { a.scheduleThis(t) }})
	}
	var moves []menuItem
	for _, sp := range a.groups {
		if sp.Id != t.GroupId {
			sp := sp
			moves = append(moves, menuItem{label: "Move to " + iconTitle(sp, a.groupTitle(sp)), run: func() { a.moveTab(t.Id, sp.Id, len(sp.TabIds)) }})
		}
	}
	if len(moves) > 0 {
		items = append(items, menuItem{sep: true})
		items = append(items, moves...)
	}
	var merges []menuItem
	if sp := a.group(t.GroupId); sp != nil {
		for _, oid := range sp.TabIds {
			if o := a.tabs[oid]; o != nil && oid != t.Id {
				merges = append(merges, menuItem{label: "Merge into " + a.tabTitle(o), run: func() { a.mergeTab(t.Id, o) }})
			}
		}
	}
	if len(merges) > 0 {
		items = append(items, menuItem{sep: true})
		items = append(items, merges...)
	}
	items = append(items, menuItem{sep: true}, menuItem{label: "Close Tab", danger: true, run: func() { a.closeTab(t.Id) }})
	a.openMenu(x, y, items)
}

func iconTitle(sp *pb.Group, title string) string {
	if sp.Icon != "" {
		return sp.Icon + " " + title
	}
	return title
}

func (a *App) groupMenu(id string, x, y int) {
	sp := a.group(id)
	if sp == nil {
		return
	}
	collapse := "Collapse"
	if a.prefs.Collapsed[id] {
		collapse = "Expand"
	}
	items := []menuItem{
		{label: "Configure Group…", hint: "⌃B .", run: func() { a.openConfigureGroup(sp) }},
		{label: "New Tab", run: func() { a.newTab(id, "") }},
		{label: "New Editor Tab", run: func() { a.openEditor(id, "", false) }},
		{label: "Resume a Session…", run: func() { a.openSessionFinderIn(id) }},
		{label: "New Agent Tab…", run: func() { a.openStartAgent(id) }},
	}
	if canSchedule(sp) {
		items = append(items, menuItem{label: "New Scheduled Task…", run: func() { a.openNewSchedule(id, "", "claude") }})
	}
	a.openMenu(x, y, append(items, []menuItem{
		{label: collapse, run: func() { a.toggleGroup(id) }},
		{sep: true},
		{label: "New Group", hint: "⌃B S", run: a.newGroup},
		{label: "New Group in Directory…", run: a.openNewGroupInFolder},
		{sep: true},
		{label: "Close Group", danger: true, run: func() { a.closeGroup(id) }},
	}...))
}

func (a *App) paneMenu(id string, x, y int) {
	p := a.panes[id]
	if p == nil {
		return
	}
	var items []menuItem
	if a.sel != nil && a.sel.pane == id {
		items = append(items, menuItem{label: "Copy", run: func() { a.copyText(a.selectionText()) }}, menuItem{sep: true})
	}
	if p.Agent != nil && p.Agent.Harness != "" {
		if cur := agents.Get(p.Agent.Harness); cur != nil && cur.CanSwitch() {
			// Claude last, as it always was here and is in the apps.
			order := slices.Clone(agents.All)
			slices.SortStableFunc(order, func(x, y *agents.Agent) int {
				switch {
				case x.ID == y.ID || (x.ID != "claude" && y.ID != "claude"):
					return 0
				case x.ID == "claude":
					return 1
				}
				return -1
			})
			for _, ag := range order {
				if ag.CanSwitch() {
					target := ag.ID
					items = append(items, menuItem{label: "Continue in " + ag.Short, disabled: p.Agent.Harness == target, run: func() { a.switchAgent(id, target) }})
				}
			}
		}
		items = append(items,
			menuItem{label: "Interrupt (Esc)", run: func() { a.sendTo(id, []byte{0x1b}) }},
			menuItem{sep: true})
	}
	if p.Kind == pb.PaneKind_PANE_KIND_BROWSER {
		items = append(items, menuItem{label: "Open in Browser", run: func() { a.openLink(p.Url) }}, menuItem{sep: true})
	}
	if p.Kind == pb.PaneKind_PANE_KIND_SIMULATOR {
		items = append(append(items, a.simItems(id)...), menuItem{sep: true})
	}
	if isEditor(p) {
		items = append(append(items, a.editorItems(id)...), menuItem{sep: true})
	}
	items = append(items,
		menuItem{label: "Split Right", hint: "⌃B %", run: func() { a.split(id, pb.Split_DIRECTION_HORIZONTAL, "") }},
		menuItem{label: "Split Down", hint: "⌃B \"", run: func() { a.split(id, pb.Split_DIRECTION_VERTICAL, "") }},
		menuItem{label: "Split Left", run: func() { a.splitAt(id, pb.Split_DIRECTION_HORIZONTAL, "", true) }},
		menuItem{label: "Split Up", run: func() { a.splitAt(id, pb.Split_DIRECTION_VERTICAL, "", true) }},
		menuItem{label: "Split Right with Browser…", run: func() {
			a.openPrompt("Split Right with Browser", "Address", "", func(v string) {
				if v != "" {
					a.split(id, pb.Split_DIRECTION_HORIZONTAL, v)
				}
			})
		}},
		menuItem{label: "Split Right with Log…", run: func() {
			a.openPrompt("Split Right with Log", logPromptHint, "", func(v string) { a.openLog("", id, v) })
		}},
		menuItem{label: "Split Right with Editor", run: func() { a.splitEditor(id, pb.Split_DIRECTION_HORIZONTAL) }},
		menuItem{label: "Split Down with Editor", run: func() { a.splitEditor(id, pb.Split_DIRECTION_VERTICAL) }},
		menuItem{sep: true})
	items = append(items, a.floatMenuItems(id)...)
	items = append(items,
		menuItem{sep: true},
		menuItem{label: "Move to New Tab", disabled: a.paneCount(p.TabId) < 2, run: func() { a.breakOut(id) }},
		menuItem{label: "Close Pane", hint: "⌃B x", danger: true, run: func() { a.closePane(id) }},
	)
	a.openMenu(x, y, items)
}

func (a *App) grantMenu(id string, x, y int) {
	for _, g := range a.grants {
		if g.Id == id {
			g := g
			// What's asked, in Hammock's words, then the agent's own.
			var about []menuItem
			if g.Action == "schedule" || g.Person {
				// A schedule: all of what you'd allow, wrapped.
				for _, line := range wrapWords(grantText(g), 60, 1<<20) { // all of it: the menu scrolls
					about = append(about, menuItem{label: line, disabled: true})
				}
			} else {
				about = append(about, menuItem{label: truncate(grantText(g), 60), disabled: true})
			}
			if g.Headline != "" && g.Description != "" {
				about = append(about, menuItem{label: truncate("The agent says: “"+g.Description+"”", 60), disabled: true})
			}
			if g.Person {
				// What only you may do: answered in the app or on your
				// phone, never from a terminal (the daemon refuses it).
				about = append(about, menuItem{label: truncate(g.Origin, 60), disabled: true}, menuItem{sep: true},
					menuItem{label: "Allow or deny it in the Hammock app or on your phone", disabled: true})
				a.openMenu(x, y, about)
				return
			}
			a.openMenu(x, y, append(append(about, menuItem{sep: true}), []menuItem{
				{label: "Allow Once", hint: "⌃B y", run: func() { a.respondGrant(g, pb.RespondGrant_DECISION_ALLOW_ONCE) }},
				{label: "Always Allow in This Group", hint: "⌃B Y", run: func() { a.respondGrant(g, pb.RespondGrant_DECISION_ALLOW_GROUP) }},
				{sep: true},
				{label: "Deny", hint: "⌃B N", danger: true, run: func() { a.respondGrant(g, pb.RespondGrant_DECISION_DENY) }},
			}...))
		}
	}
}

// ---------- palette ----------

type paletteItem struct {
	id, title, sub, hint string
	glyph                glyph
	keywords             string
	run                  func()
}

type palette struct {
	query string
	sel   int
	open  *spring
	items []paletteItem
}

func (a *App) openPalette() {
	p := &palette{open: newSpring(gentle)}
	p.open.from(0)
	p.open.to(1)
	a.overlay = p
}

func (a *App) paletteItems() []paletteItem {
	var out []paletteItem
	tabs := a.paletteOrder(a.orderedTabs())
	for _, id := range tabs {
		t := a.tabs[id]
		if t == nil {
			continue
		}
		g, _ := a.tabGlyph(t)
		sub := ""
		if sp := a.group(t.GroupId); sp != nil {
			sub = iconTitle(sp, a.groupTitle(sp))
		}
		if p := a.panes[t.FocusedPaneId]; p != nil {
			if p.Kind == pb.PaneKind_PANE_KIND_BROWSER {
				sub += " · " + p.Url
			} else if p.Cwd != "" {
				sub += " · " + shortPath(p.Cwd)
			}
		}
		out = append(out, paletteItem{id: "tab:" + id, title: a.tabTitle(t), sub: sub, glyph: g, hint: a.tabDetail(t), run: func() { a.selectTab(id) }})
	}
	for _, sp := range a.groups {
		sp := sp
		out = append(out, paletteItem{id: "group:" + sp.Id, title: iconTitle(sp, a.groupTitle(sp)), sub: "Group", glyph: glyph{"◆", a.th.accent, false}, run: func() {
			a.selectGroup(sp.Id) // its tab you were last on, or the empty Group itself
		}})
	}
	act := func(id, title, hint, kw string, run func()) {
		if k := paletteTool(id); k != "" && toolOff(k) {
			return // turned off in Settings
		}
		out = append(out, paletteItem{id: "action:" + id, title: title, sub: "Command", hint: hint, keywords: kw, glyph: glyph{"›", a.th.dim, false}, run: run})
	}
	act("new-tab", "New Tab", "⌃B c", "create", func() { a.newTab("", "") })
	act("new-browser", "New Browser Tab…", "⌃B b", "web url", func() {
		a.openPrompt("New Browser Tab", "Address", "", func(v string) {
			if v != "" {
				a.newTab("", v)
			}
		})
	})
	act("new-agent", "New Agent Tab…", "", "start claude codex agent session launch", func() { a.openStartAgent("") })
	act("new-log", "New Log Tab…", "", "console logs logcat tail file stream", func() {
		a.openPrompt("New Log Tab", logPromptHint, "", func(v string) { a.openLog("", "", v) })
	})
	act("quick-open", "Quick Open…", "⌃B e", "open file find edit editor text code", a.openQuickOpen)
	act("new-editor", "New Editor Tab", "⌃B E", "edit editor text file untitled create code", func() { a.openEditor("", "", false) })
	act("review-changes", "Review Changes", "", "diff git review pull request branch commit uncommitted", a.reviewChanges)
	act("new-group", "New Group", "⌃B S", "create", a.newGroup)
	act("new-group-in-directory", "New Group in Directory…", "", "create folder project open path", a.openNewGroupInFolder)
	a.computerItems(act)
	act("reopen-closed", "Reopen Closed Tab or Pane", "⌃B T", "undo close restore", a.reopenClosed)
	act("find-session", "Find Session…", "⌃B O", "resume claude codex agent conversation history search", func() { a.openSessionFinder("") })
	act("schedules", "Scheduled Tasks", "⌃B @", "schedule cron routine recurring timer automation runs", func() { a.openSchedules("") })
	act("new-schedule", "New Scheduled Task…", "", "schedule cron routine recurring timer automation claude codex command every day weekdays", func() {
		a.openNewSchedule(a.currentGroup(), "", "claude")
	})
	// Only the sessions in the folders open in the Group you're in (yours,
	// here or on a linked computer; not one shared with you).
	if g := a.group(a.currentGroup()); g != nil && (g.HostUserId == "" || a.computerOf(g) != nil) {
		id := g.Id
		act("find-session-in-group", "Resume a Session from This Group…", "", "find resume claude codex agent conversation history group folder "+g.Name, func() { a.openSessionFinderIn(id) })
	}
	if a.focus != "" {
		f := a.focus
		act("split-right", "Split Right", "⌃B %", "pane", func() { a.split(f, pb.Split_DIRECTION_HORIZONTAL, "") })
		act("split-down", "Split Down", "⌃B \"", "pane", func() { a.split(f, pb.Split_DIRECTION_VERTICAL, "") })
		act("split-left", "Split Left", "", "pane", func() { a.splitAt(f, pb.Split_DIRECTION_HORIZONTAL, "", true) })
		act("split-up", "Split Up", "", "pane above", func() { a.splitAt(f, pb.Split_DIRECTION_VERTICAL, "", true) })
		act("split-right-editor", "Split Right with Editor", "", "pane edit editor text file untitled", func() { a.splitEditor(f, pb.Split_DIRECTION_HORIZONTAL) })
		act("split-down-editor", "Split Down with Editor", "", "pane edit editor text file untitled below", func() { a.splitEditor(f, pb.Split_DIRECTION_VERTICAL) })
		if p := a.panes[f]; isEditor(p) {
			for _, it := range a.editorItems(f) {
				if !it.sep && !it.disabled {
					act("editor:"+it.label, it.label, it.hint, "editor file text write disk", it.run)
				}
			}
		}
		act("close-pane", "Close Pane", "⌃B x", "", func() { a.closePane(f) })
		act("new-floating-pane", "New Floating Pane", "⌃B f", "float floating popup overlay scratch pane", func() { a.newFloating(f) })
		if a.isFloating(f) {
			act("dock-pane", "Dock Pane", "⌃B F", "float floating dock split", func() { a.arrange(f, pb.FloatPane_ACTION_DOCK) })
			act("minimise-pane", "Minimise Floating Pane", "⌃B m", "float floating minimize hide shelf", func() { a.arrange(f, pb.FloatPane_ACTION_MINIMISE) })
			act("zoom-float", "Zoom Floating Pane", "⌃B z", "float floating zoom fill maximize", func() { a.arrange(f, pb.FloatPane_ACTION_ZOOM) })
		} else if a.canFloat(f) {
			act("float-pane", "Float Pane", "⌃B F", "float floating popup overlay", func() { a.arrange(f, pb.FloatPane_ACTION_FLOAT) })
		}
		if t := a.tabs[a.selTab]; t != nil && len(t.Floating) > 0 {
			tid := t.Id
			act("toggle-floats", "Show/Hide Floating Panes", "⌃B j", "float floating minimize restore all shelf", func() { a.toggleFloats(tid) })
		}
		if p := a.panes[f]; p != nil && a.paneCount(p.TabId) > 1 {
			act("break-out", "Move Pane to New Tab", "", "pane tab break out", func() { a.breakOut(f) })
		}
		if p := a.panes[f]; p != nil && p.Kind == pb.PaneKind_PANE_KIND_SIMULATOR {
			for _, it := range a.simItems(f) {
				act("sim:"+it.label, it.label, "", "simulator button orientation landscape portrait", it.run)
			}
		}
	}
	if t := a.tabs[a.selTab]; t != nil {
		act("rename-tab", "Rename Tab…", "⌃B ,", "", func() {
			a.openPrompt("Rename Tab", "Name", t.Name, func(v string) { a.renameTab(t.Id, v) })
		})
		act("close-tab", "Close Tab", "", "", func() { a.closeTab(t.Id) })
		if canSchedule(a.group(t.GroupId)) {
			act("schedule-tab", "Schedule This Tab…", "", "scheduled task cron routine recurring every day weekdays", func() { a.scheduleThis(t) })
		}
		if sp := a.group(t.GroupId); sp != nil {
			for _, oid := range sp.TabIds {
				if o := a.tabs[oid]; o != nil && oid != t.Id {
					act("merge:"+oid, "Merge Tab into “"+a.tabTitle(o)+"”", "", "combine join split pane", func() { a.mergeTab(t.Id, o) })
				}
			}
		}
	}
	// The Group you're in, with tabs or without.
	if sp := a.group(a.currentGroup()); sp != nil && (a.tabs[a.selTab] != nil || a.selGroup == sp.Id) {
		act("configure-group", "Configure Group…", "⌃B .", "rename icon emoji folder", func() { a.openConfigureGroup(sp) })
		if len(sp.TabIds) == 0 {
			act("start-agent-here", "Start an Agent…", "", "claude codex new agent tab", func() { a.openStartAgent(sp.Id) })
			act("close-group", "Close Group", "", "delete remove", func() { a.closeGroup(sp.Id) })
		}
	}
	act("pair-device", "Pair a Device…", "", "iphone phone ios remote qr pairing code", a.showPairing)
	act("remote-networks", "Remote Access Networks…", "", "remote access network wifi lan local vpn wireguard tailscale subnet address", a.openRemoteNetworks)
	act("hmk-key", "Hammock Key…", "", "ssh key public secure enclave github authorized_keys git signing", a.showHammockKey)
	act("settings", "Settings…", "⌃B s", "preferences layout horizontal vertical tabs animations sidebar", a.openSettings)
	agents := "Show Active Agents in Collapsed Groups"
	if a.prefs.CollapsedAgents {
		agents = "Hide Active Agents in Collapsed Groups"
	}
	if !a.horizontal() { // no tab rows in the sidebar to keep
		act("collapsed-agents", agents, "", "sidebar collapse working agent tabs", func() {
			a.prefs.CollapsedAgents = !a.prefs.CollapsedAgents
			a.prefs.save()
		})
	}
	act("keys", "Keyboard Shortcuts", "⌃B ?", "help", a.openHelp)
	act("acknowledgements", "Acknowledgements", "", "licences licenses third party open source credits notices copyright", a.openAcknowledgements)
	act("quit", "Quit", "⌃B q", "exit detach", func() { a.quit = true })
	return out
}

// paletteOrder puts tabs in the order the palette offers them:
// agents waiting on you, then ones that finished (or failed) since you last
// looked, the newest first in each; then the rest, the ones you opened most
// recently first, and the one you're on last (the top row takes you back to
// the previous tab); then sidebar order. Not stateRank: finished and failed
// rank together, and only until you've looked.
func (a *App) paletteOrder(ids []string) []string {
	type key struct {
		rank int
		when int64
	}
	keys := make(map[string]key, len(ids))
	for _, id := range ids {
		k := key{rank: 2}
		if t := a.tabs[id]; t != nil {
			for _, pid := range tabIDs(t) {
				p := a.panes[pid]
				if p == nil || p.Agent == nil || p.Exited {
					continue
				}
				r := 2
				switch shownState(p.Agent) {
				case pb.AgentState_AGENT_STATE_NEEDS_INPUT:
					r = 0
				case pb.AgentState_AGENT_STATE_IDLE:
					if !p.Agent.Seen {
						r = 1
					}
				case pb.AgentState_AGENT_STATE_ERRORED:
					// The daemon doesn't mark a failure seen: it is, once
					// you've opened its tab since.
					if id != a.selTab && a.opened[id] < p.Agent.StateSinceUnixMs {
						r = 1
					}
				}
				if r < k.rank || r == k.rank && r < 2 && p.Agent.StateSinceUnixMs > k.when {
					k = key{r, p.Agent.StateSinceUnixMs}
				}
			}
		}
		if k.rank == 2 {
			k.when = a.opened[id]
			if id == a.selTab {
				k.when = -1
			}
		}
		keys[id] = k
	}
	out := slices.Clone(ids)
	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := keys[out[i]], keys[out[j]]
		if ki.rank != kj.rank {
			return ki.rank < kj.rank
		}
		return ki.when > kj.when
	})
	return out
}

// score is a fuzzy match: every query character in order; prefix and
// word-start hits score highest, title matches beat the rest.
func score(q, title, extra string) (int, bool) {
	sub := func(text string) (int, bool) {
		sc, idx, last := 0, 0, -1
		for _, ch := range q {
			j := strings.IndexRune(text[idx:], ch)
			if j < 0 {
				return 0, false
			}
			pos := idx + j
			switch {
			case pos == 0 || text[pos-1] == ' ':
				sc += 8
			case pos == last:
				sc += 5
			default:
				sc++
			}
			idx = pos + len(string(ch))
			last = idx
		}
		return sc, true
	}
	if strings.HasPrefix(title, q) {
		return 1000 - len(title), true
	}
	if s, ok := sub(title); ok {
		return 200 + s, true
	}
	return sub(extra)
}

func (p *palette) filtered(a *App) []paletteItem {
	all := a.paletteItems()
	q := strings.ToLower(strings.TrimSpace(p.query))
	if q == "" {
		return all
	}
	type scored struct {
		it paletteItem
		s  int
	}
	var out []scored
	for _, it := range all {
		if s, ok := score(q, strings.ToLower(it.title), strings.ToLower(it.keywords+" "+it.sub)); ok {
			out = append(out, scored{it, s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].s > out[j].s })
	res := make([]paletteItem, len(out))
	for i, s := range out {
		res[i] = s.it
	}
	// Anything typed can be opened as a page.
	typed := strings.TrimSpace(p.query)
	looksLikeURL := !strings.Contains(typed, " ") && (strings.Contains(typed, ".") || strings.Contains(typed, "://") || strings.HasPrefix(typed, "localhost"))
	open := paletteItem{id: "open", title: "Search the web for “" + typed + "”", sub: "New browser tab", glyph: glyph{"⌕", a.th.accent, false}, run: func() { a.newTab("", typed) }}
	switch {
	case toolOff("browser"):
	case looksLikeURL:
		open.title, open.glyph = "Open "+typed, glyph{"◎", a.th.accent, false}
		res = append([]paletteItem{open}, res...)
	default:
		res = append(res, open)
	}
	return res
}

func (p *palette) frame(a *App) rect {
	w := min(76, a.w-6)
	return rect{(a.w - w) / 2, max(1, a.h/6), w, min(16, a.h-a.h/6-2)}
}

func (p *palette) draw(a *App) {
	moving := p.open.step()
	if moving {
		a.dirty = true
	}
	t := clamp01(p.open.pos)
	a.dimBehind(t)
	r := p.frame(a)
	r.y += int(math.Round((1 - p.open.pos) * -2)) // drops in
	p.items = p.filtered(a)
	visible := r.h - 4
	if p.sel >= len(p.items) {
		p.sel = max(0, len(p.items)-1)
	}
	h := 3
	if len(p.items) > 0 {
		h = 4 + min(visible, len(p.items))
	}
	r.h = max(3, int(math.Round(float64(h)*math.Max(0.4, t))))
	bg := mix(a.th.bg, a.th.menu, t)
	inner := a.box(r, bg, mix(bg, a.th.border, t))
	// The query line.
	prompt := Cell{FG: a.th.accent, HasFG: true, BG: bg, HasBG: true, Bold: true}
	a.scr.Text(inner.x+1, inner.y, 2, "›", prompt)
	if p.query == "" {
		a.scr.Text(inner.x+3, inner.y, inner.w-4, "Jump to a tab, or run a command…", Cell{FG: a.th.dim, HasFG: true, BG: bg, HasBG: true})
		a.scr.Cursor(inner.x+3, inner.y, true)
	} else {
		n := a.scr.Text(inner.x+3, inner.y, inner.w-4, p.query, Cell{FG: a.th.fg, HasFG: true, BG: bg, HasBG: true})
		a.scr.Cursor(inner.x+3+n, inner.y, true)
	}
	if inner.h < 3 || len(p.items) == 0 {
		return
	}
	for x := inner.x; x < inner.x+inner.w; x++ {
		a.scr.Set(x, inner.y+1, Cell{Ch: "─", FG: a.th.border, HasFG: true, BG: bg, HasBG: true})
	}
	top := max(0, p.sel-visible+1)
	for i := 0; i < visible && top+i < len(p.items) && inner.y+2+i < inner.y+inner.h; i++ {
		it := p.items[top+i]
		y := inner.y + 2 + i
		rowBG := bg
		if top+i == p.sel {
			rowBG = mix(bg, a.th.menuSel, 0.85)
		}
		a.scr.Paint(inner.x, y, inner.w, 1, rowBG)
		sel := top+i == p.sel
		fg := mix(bg, a.th.fg, t)
		dim := a.th.dim
		if sel {
			fg, dim = RGB{255, 255, 255}, mix(RGB{255, 255, 255}, rowBG, 0.35)
		}
		a.scr.Text(inner.x+1, y, 1, it.glyph.ch, Cell{FG: it.glyph.col, HasFG: true, BG: rowBG, HasBG: true, Bold: it.glyph.bold})
		hint := it.hint
		if sel {
			hint = "↵"
		}
		hw := 0
		if hint != "" {
			hw = textWidth(hint) + 2
			a.scr.TextRight(inner.x+1, y, inner.w-2, hint, Cell{FG: dim, HasFG: true, BG: rowBG, HasBG: true})
		}
		room := inner.w - 4 - hw
		n := a.scr.Text(inner.x+3, y, room, truncate(it.title, room), Cell{FG: fg, HasFG: true, BG: rowBG, HasBG: true, Bold: sel})
		if it.sub != "" && room-n > 6 {
			a.scr.Text(inner.x+3+n+2, y, room-n-2, truncate(it.sub, room-n-2), Cell{FG: dim, HasFG: true, BG: rowBG, HasBG: true})
		}
	}
}

func (p *palette) key(a *App, ks []key) {
	for _, k := range ks {
		switch k.name {
		case "esc":
			a.closeOverlay()
			return
		case "up", "shift-tab", "ctrl-p":
			p.sel = max(0, p.sel-1)
		case "down", "tab", "ctrl-n":
			p.sel = min(len(p.items)-1, p.sel+1)
		case "enter":
			p.choose(a)
			return
		case "backspace":
			if r := []rune(p.query); len(r) > 0 {
				p.query = string(r[:len(r)-1])
				p.sel = 0
			}
		case "ctrl-u":
			p.query, p.sel = "", 0
		case "":
			p.query += string(k.r)
			p.sel = 0
		}
	}
}

func (p *palette) choose(a *App) {
	if p.sel < 0 || p.sel >= len(p.items) {
		return
	}
	run := p.items[p.sel].run
	a.closeOverlay()
	run()
}

func (p *palette) mouse(a *App, m mouseEv) {
	r := p.frame(a)
	inner := rect{r.x + 1, r.y + 1, r.w - 2, r.h - 2}
	visible := r.h - 4
	top := max(0, p.sel-visible+1)
	if m.wheel != 0 {
		p.sel = max(0, min(len(p.items)-1, p.sel+m.wheel))
		return
	}
	if inner.contains(m.x, m.y) && m.y >= inner.y+2 {
		i := top + m.y - inner.y - 2
		if i < len(p.items) {
			if m.motion || m.press {
				p.sel = i
			}
			if m.press && m.button == 0 {
				p.choose(a)
			}
		}
		return
	}
	if m.press && !r.contains(m.x, m.y) {
		a.closeOverlay()
	}
}

// ---------- dialogs ----------

type field struct {
	label, value string
	cursor       int  // in runes
	fresh        bool // prefilled and untouched: shown selected, replaced by typing
}

type dialog struct {
	title   string
	fields  []*field
	focus   int // field index; len(fields) = Cancel, +1 = OK
	okLabel string
	confirm bool     // no fields, but a Cancel button (focused first)
	lines   []string // read-only text (help)
	errText string   // what went wrong last time, shown above the lines
	submit  func(values []string)
	cancel  func() // on Cancel or Esc
	open    *spring
}

func (a *App) openDialog(d *dialog) {
	d.open = newSpring(gentle)
	d.open.from(0)
	d.open.to(1)
	for _, f := range d.fields {
		f.cursor = len([]rune(f.value))
		f.fresh = f.value != ""
	}
	a.overlay = d
}

func (a *App) openPrompt(title, label, value string, submit func(string)) {
	a.openDialog(&dialog{title: title, okLabel: "OK", fields: []*field{{label: label, value: value}},
		submit: func(v []string) { submit(strings.TrimSpace(v[0])) }})
}

func (a *App) openConfigureGroup(sp *pb.Group) {
	fields := []*field{{label: "Name", value: sp.Name}, {label: "Icon", value: sp.Icon}}
	// Where its new tabs open: your own Groups', on this computer.
	own := sp.HostUserId == ""
	if own {
		fields = append(fields, &field{label: "Folder", value: shortPath(sp.Folder)})
	}
	a.openDialog(&dialog{title: "Configure Group", okLabel: "Save", fields: fields,
		submit: func(v []string) {
			a.configureGroup(sp.Id, strings.TrimSpace(v[0]), strings.TrimSpace(v[1]))
			if own {
				if f := expandTilde(strings.TrimSpace(v[2])); f != "" && f != sp.Folder {
					a.setGroupFolder(sp.Id, f)
				}
			}
		}})
}

func (a *App) openHelp() {
	a.openDialog(&dialog{title: "Keyboard Shortcuts", okLabel: "Done", lines: []string{
		"⌃B k   Command palette            ⌃B ?   This list",
		"⌃B c   New tab                    ⌃B b   New browser tab",
		"⌃B S   New Group                  ⌃B .   Configure Group",
		"⌃B s   Settings (layout, …)       ⌃B O   Find a session",
		"⌃B ,   Rename tab                 ⌃B x   Close pane",
		"⌃B T   Reopen closed tab or pane  ⌃B M   Next computer (linked)",
		"⌃B @   Scheduled tasks",
		"⌃B e   Quick Open (a file)        ⌃B E   New editor tab",
		"⌃B %   Split right                ⌃B \"   Split down",
		"⌃B f   New floating pane          ⌃B F   Float / dock pane",
		"⌃B m   Minimise floating pane     ⌃B j   Hide / show floats",
		"⌃B z   Zoom floating pane",
		"⌃B n/p Next / previous tab        ⌃B 1-9 Tab in this Group",
		"⌃B o   Next pane                  ⌃B ←→  Move between panes",
		"⌃B [ ] Scroll back / forward      ⌃B y/Y/N  Answer a request",
		"⌃B ⌃B  Send ⌃B to the pane        ⌃B q   Quit",
		"",
		"Mouse: click tabs and Groups, drag tabs to reorder, drag",
		"dividers and the sidebar edge, right-click for menus. In a pane,",
		"drag to select (copied), double-click a word; hold Shift to",
		"select in programs that use the mouse. Right-click a Needs You",
		"row to Allow or Deny. Drag a floating pane by its header or top",
		"border, resize it from its others; hold Alt while dragging to",
		"dock it into the split under the pointer.",
		"",
		"In an editor: ⌃S save, ⌃F find, ⌃G go to line, ⌃Z/⌃Y undo/redo,",
		"⌃C/⌃X copy/cut (paste with your terminal), ⌃A/⌃E line start/end,",
		"⌥←→ by word, Shift to select, Tab/⇧Tab indent.",
	}})
}

// labelWidth is the widest field label, so fields line up after it.
func (d *dialog) labelWidth() int {
	w := 0
	for _, f := range d.fields {
		w = max(w, textWidth(f.label))
	}
	return w
}

// textLines is how many rows the read-only text and the error take.
func (d *dialog) textLines() int {
	if d.errText != "" {
		return len(d.lines) + 1
	}
	return len(d.lines)
}

func (d *dialog) frame(a *App) rect {
	w := 46
	for _, l := range d.lines {
		w = max(w, textWidth(l)+6)
	}
	w = min(w, a.w-4)
	h := 4 + d.textLines() + 2*len(d.fields) + 1
	return rect{(a.w - w) / 2, max(1, (a.h-h)/3), w, h}
}

func (d *dialog) buttons(a *App) (cancel, ok rect) {
	r := d.frame(a)
	y := r.y + r.h - 2
	okw := textWidth(d.okLabel) + 4
	ok = rect{r.x + r.w - 2 - okw, y, okw, 1}
	cancel = rect{ok.x - 11, y, 10, 1}
	return
}

func (d *dialog) draw(a *App) {
	if d.open.step() {
		a.dirty = true
	}
	t := clamp01(d.open.pos)
	a.dimBehind(t)
	r := d.frame(a)
	r.y += int(math.Round((1 - d.open.pos) * -2))
	full := r.h
	r.h = max(3, int(math.Round(float64(full)*math.Max(0.3, t))))
	bg := mix(a.th.bg, a.th.menu, t)
	inner := a.box(r, bg, mix(bg, a.th.border, t))
	if r.h < full {
		return
	}
	s := a.scr
	s.Text(inner.x+1, inner.y, inner.w-2, d.title, Cell{FG: a.th.fg, HasFG: true, BG: bg, HasBG: true, Bold: true})
	y := inner.y + 2
	if d.errText != "" {
		s.Text(inner.x+1, y, inner.w-2, truncate("⚠ "+d.errText, inner.w-2), Cell{FG: a.th.red, HasFG: true, BG: bg, HasBG: true, Bold: true})
		y++
	}
	for _, l := range d.lines {
		s.Text(inner.x+1, y, inner.w-2, l, Cell{FG: a.th.fg, HasFG: true, BG: bg, HasBG: true})
		y++
	}
	for i, f := range d.fields {
		lw := d.labelWidth()
		s.Text(inner.x+1, y, lw, f.label, Cell{FG: a.th.dim, HasFG: true, BG: bg, HasBG: true})
		fx, fw := inner.x+lw+3, inner.w-lw-4
		fbg := mix(bg, a.th.bg, 0.6)
		if i == d.focus {
			fbg = mix(a.th.bg, a.th.accent, 0.12)
		}
		s.Fill(fx, y, fw, 1, Cell{BG: fbg, HasBG: true})
		vc := Cell{FG: a.th.fg, HasFG: true, BG: fbg, HasBG: true}
		if f.fresh && i == d.focus {
			vc.BG = mix(a.th.bg, a.th.accent, 0.45)
		}
		s.Text(fx+1, y, fw-2, f.value, vc)
		if i == d.focus {
			s.Cursor(fx+1+textWidth(string([]rune(f.value)[:f.cursor])), y, true)
		}
		y += 2
	}
	cancel, ok := d.buttons(a)
	if len(d.fields) > 0 || d.confirm {
		cb := mix(bg, a.th.hover, 0.5+0.5*b2f(cancel.contains(a.mouseX, a.mouseY) || d.focus == len(d.fields)))
		s.Fill(cancel.x, cancel.y, cancel.w, 1, Cell{BG: cb, HasBG: true})
		s.Text(cancel.x+2, cancel.y, cancel.w-2, "Cancel", Cell{FG: a.th.fg, HasFG: true, BG: cb, HasBG: true})
	}
	ob := mix(a.th.accent, RGB{255, 255, 255}, 0.15*b2f(ok.contains(a.mouseX, a.mouseY)))
	s.Fill(ok.x, ok.y, ok.w, 1, Cell{BG: ob, HasBG: true})
	s.Text(ok.x+2, ok.y, ok.w-2, d.okLabel, Cell{FG: RGB{255, 255, 255}, HasFG: true, BG: ob, HasBG: true, Bold: true})
}

func (d *dialog) finish(a *App, ok bool) {
	a.closeOverlay()
	if !ok && d.cancel != nil {
		d.cancel()
	}
	if ok && d.submit != nil {
		vals := make([]string, len(d.fields))
		for i, f := range d.fields {
			vals[i] = f.value
		}
		d.submit(vals)
	}
}

func (d *dialog) key(a *App, ks []key) {
	for _, k := range ks {
		var f *field
		if d.focus < len(d.fields) {
			f = d.fields[d.focus]
			if f.fresh {
				switch k.name {
				case "", "backspace", "delete":
					f.value, f.cursor = "", 0 // typing replaces the selected text
				}
				f.fresh = false
			}
		}
		switch k.name {
		case "esc":
			d.finish(a, false)
			return
		case "enter":
			d.finish(a, d.focus != len(d.fields) || (len(d.fields) == 0 && !d.confirm))
			return
		case "tab", "down":
			d.focus = (d.focus + 1) % (len(d.fields) + 2)
		case "shift-tab", "up":
			d.focus = (d.focus + len(d.fields) + 1) % (len(d.fields) + 2)
		case "left":
			if f != nil && f.cursor > 0 {
				f.cursor--
			}
		case "right":
			if f != nil && f.cursor < len([]rune(f.value)) {
				f.cursor++
			}
		case "home", "ctrl-a":
			if f != nil {
				f.cursor = 0
			}
		case "end", "ctrl-e":
			if f != nil {
				f.cursor = len([]rune(f.value))
			}
		case "ctrl-u":
			if f != nil {
				f.value, f.cursor = "", 0
			}
		case "backspace":
			if f != nil && f.cursor > 0 {
				r := []rune(f.value)
				f.value = string(append(r[:f.cursor-1], r[f.cursor:]...))
				f.cursor--
			}
		case "delete":
			if f != nil {
				if r := []rune(f.value); f.cursor < len(r) {
					f.value = string(append(r[:f.cursor], r[f.cursor+1:]...))
				}
			}
		case "":
			if f != nil {
				r := []rune(f.value)
				f.value = string(append(r[:f.cursor], append([]rune{k.r}, r[f.cursor:]...)...))
				f.cursor++
			}
		}
	}
}

func (d *dialog) mouse(a *App, m mouseEv) {
	if !m.press || m.button != 0 {
		return
	}
	r := d.frame(a)
	cancel, ok := d.buttons(a)
	switch {
	case ok.contains(m.x, m.y):
		d.finish(a, true)
	case cancel.contains(m.x, m.y) && (len(d.fields) > 0 || d.confirm):
		d.finish(a, false)
	case !r.contains(m.x, m.y):
		d.finish(a, false)
	default:
		y := r.y + 3 + d.textLines()
		for i := range d.fields {
			if m.y == y+2*i {
				d.focus = i
				f := d.fields[i]
				f.cursor = max(0, min(len([]rune(f.value)), m.x-(r.x+d.labelWidth()+5)))
			}
		}
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// openAcknowledgements shows the third-party software in Hammock, by
// licence; `hmk acknowledgements NAME` prints one's licence text.
func (a *App) openAcknowledgements() {
	byLicense := map[string][]string{}
	var licenses []string
	for _, c := range acknowledgements.For(acknowledgements.ThisApp()) {
		if byLicense[c.License] == nil {
			licenses = append(licenses, c.License)
		}
		byLicense[c.License] = append(byLicense[c.License], c.Name)
	}
	sort.Slice(licenses, func(i, j int) bool { return len(byLicense[licenses[i]]) > len(byLicense[licenses[j]]) })
	lines := []string{"Hammock includes this software, under these licences:", ""}
	for _, l := range licenses {
		lines = append(lines, l+":")
		for _, w := range wrapWords(strings.Join(byLicense[l], ", "), 62, 4) {
			lines = append(lines, "  "+w)
		}
	}
	lines = append(lines, "", "Each one's licence: hmk acknowledgements NAME (in a shell).")
	a.openDialog(&dialog{title: "Acknowledgements", okLabel: "Done", lines: lines})
}
