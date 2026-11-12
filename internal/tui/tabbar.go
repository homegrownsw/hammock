package tui

import (
	"slices"
)

// drawTabBar draws Horizontal Tabs' bar under the toolbar: the selected
// Group's tabs, equal widths, the selected one in the panes' colour, a ×
// on the hovered one and a + at the end.
func (a *App) drawTabBar() {
	if !a.horizontal() {
		return
	}
	m := a.mainRect()
	th, s := a.th, a.scr
	y := m.y + 1
	barBG := mix(th.sidebar, th.fg, 0.04)
	s.Fill(m.x, y, m.w, 1, Cell{BG: barBG, HasBG: true})
	t := a.tabs[a.selTab]
	if t == nil {
		return
	}
	sp := a.group(t.GroupId)
	if sp == nil {
		return
	}
	plus := 3
	ids := sp.TabIds
	room := m.w - plus
	w := max(12, min(28, room/max(len(ids), 1)))
	// Too many to fit: a window of tabs around the selected one.
	fit := max(1, room/w)
	first := 0
	if len(ids) > fit {
		sel := slices.Index(ids, a.selTab)
		first = max(0, min(sel-fit/2, len(ids)-fit))
		ids = ids[first : first+fit]
	}
	x := m.x
	for i, id := range ids {
		tab := a.tabs[id]
		if tab == nil {
			continue
		}
		selected := id == a.selTab
		hover := a.hovered(zBarTab, id) || a.hovered(zBarClose, id)
		bg := barBG
		switch {
		case selected:
			bg = th.bg
		case hover:
			bg = mix(barBG, th.hover, a.hoverAmount("bar:"+id, true))
		}
		s.Fill(x, y, w, 1, Cell{BG: bg, HasBG: true})
		a.addZone(zone{r: rect{x, y, w, 1}, kind: zBarTab, id: id, group: sp.Id, idx: first + i})
		fg := th.dim
		if selected {
			fg = th.fg
		}
		g, _ := a.tabGlyph(tab)
		s.Text(x+2, y, 1, g.ch, Cell{FG: g.col, HasFG: true, BG: bg, HasBG: true, Bold: g.bold})
		s.Text(x+4, y, w-7, truncate(a.tabTitle(tab), w-7), Cell{FG: fg, HasFG: true, BG: bg, HasBG: true, Bold: selected})
		if hover {
			s.Text(x+w-2, y, 1, "×", Cell{FG: th.dim, HasFG: true, BG: bg, HasBG: true})
			a.addZone(zone{r: rect{x + w - 2, y, 1, 1}, kind: zBarClose, id: id})
		}
		// Separators between unselected tabs.
		if !selected && i < len(ids)-1 && ids[i+1] != a.selTab {
			s.Set(x+w-1, y, Cell{Ch: "│", FG: th.faint, HasFG: true, BG: bg, HasBG: true})
		}
		// Where a dragged tab will land: an accent bar at the tab it will take.
		if d := a.drag; d != nil && d.kind == dragTab && d.moved && d.dropGroup == sp.Id && d.dropIndex == first+i {
			s.Set(x, y, Cell{Ch: "▎", FG: th.accent, HasFG: true, BG: mix(bg, th.accent, 0.18), HasBG: true})
		}
		x += w
	}
	pbg := mix(barBG, th.hover, a.hoverAmount("btn:bar-new", a.hovered(zButton, "new-tab-bar")))
	px := m.x + m.w - plus
	s.Fill(px, y, plus, 1, Cell{BG: pbg, HasBG: true})
	s.Text(px+1, y, 1, "+", Cell{FG: th.dim, HasFG: true, BG: pbg, HasBG: true})
	a.addZone(zone{r: rect{px, y, plus, 1}, kind: zButton, id: "new-tab-bar"})
}

// openGroup shows a Group's current tab (Horizontal Tabs): the one with
// its last-focused pane, else its first.
func (a *App) openGroup(id string) {
	sp := a.group(id)
	if sp == nil || len(sp.TabIds) == 0 {
		return
	}
	for _, tid := range sp.TabIds {
		if t := a.tabs[tid]; t != nil && slices.Contains(tabIDs(t), sp.LastFocusedPaneId) {
			a.selectTab(tid)
			return
		}
	}
	a.selectTab(sp.TabIds[0])
}
