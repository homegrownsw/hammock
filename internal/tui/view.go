package tui

import (
	"fmt"
	"io"
	"math"
	"slices"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// The screen is drawn immediate-mode each frame; everything clickable
// registers a zone as it's drawn, and mouse events hit-test the zones from
// the last frame (topmost last).

type zoneKind int

const (
	zSidebar zoneKind = iota
	zSidebarEdge
	zGroup
	zTab
	zNewGroup
	zNeeds
	zGrant
	zButton
	zPaneHeader
	zPaneMenu
	zPaneClose
	zPane
	zDivider
	zBrowserOpen
	zEmpty
	zEmptyAgent // an empty Group's Start an Agent… button
	zBarTab     // a tab in the Horizontal Tabs bar
	zBarClose   // its close button
	zLive       // a browser or simulator pane's picture
	zComputer   // a computer in the footer's row (linked computers)
	zFloatEdge  // a floating pane's border: the top moves it, the others resize it (idx: floatEdge)
	zFloatMin   // a floating pane's minimise button
	zFloatDock  // its dock button
	zShelf      // a minimised floating pane on the tab's shelf
	zEditorBar  // a button on an editor pane's changed-on-disk bar (idx: which)
)

type zone struct {
	r     rect
	kind  zoneKind
	id    string // tab, group, pane, button, attention or grant id
	group string // for tabs: their Group
	idx   int    // for tabs: index in their Group; for dividers: index in a.dividers
}

func (z zone) key() string { return fmt.Sprintf("%d:%s:%d", z.kind, z.id, z.idx) }

func (a *App) addZone(z zone) { a.zones = append(a.zones, z) }

// zoneAt is the topmost zone under (x, y).
func (a *App) zoneAt(x, y int) (zone, bool) {
	for i := len(a.zones) - 1; i >= 0; i-- {
		if a.zones[i].r.contains(x, y) {
			return a.zones[i], true
		}
	}
	return zone{}, false
}

// hovered asks the last complete frame what's under the mouse (this frame's
// zones are still being drawn).
func (a *App) hovered(k zoneKind, id string) bool {
	z, ok := a.prevZoneAt(a.mouseX, a.mouseY)
	return ok && a.overlay == nil && a.drag == nil && z.kind == k && z.id == id
}

// hoverAmount fades a hover highlight in and out.
func (a *App) prevZoneAt(x, y int) (zone, bool) {
	for i := len(a.prevZones) - 1; i >= 0; i-- {
		if a.prevZones[i].r.contains(x, y) {
			return a.prevZones[i], true
		}
	}
	return zone{}, false
}

func (a *App) hoverAmount(key string, on bool) float64 {
	sp := a.anim.get("hover:"+key, quick)
	if on {
		sp.to(1)
	} else {
		sp.to(0)
	}
	return clamp01(sp.pos)
}

func (a *App) render() {
	if a.suspended {
		return // an external editor has the terminal
	}
	s := a.scr
	s.Clear()
	a.prevZones = append(a.prevZones[:0], a.zones...)
	a.zones = a.zones[:0]
	a.syncPanes()

	a.drawSidebar()
	a.drawToolbar()
	a.drawTabBar()
	a.drawPanes()
	a.drawDragGhost()
	a.drawToast()
	if a.overlay != nil {
		a.overlay.draw(a)
	}
	if a.pics.pending.Len() > 0 {
		// Pictures go to the terminal before the cells that show them.
		_, _ = a.out.Write(a.pics.pending.Bytes())
		a.pics.pending.Reset()
	}
	s.Flush(a.out)
	a.updatePointer()
}

// updatePointer sets the mouse pointer for what's under it (OSC 22): an
// I-beam over terminal text, resize arrows over dividers and the sidebar
// edge, the normal arrow over everything else. Terminals without OSC 22
// ignore it.
func (a *App) updatePointer() {
	shape := "default"
	if z, ok := a.zoneAt(a.mouseX, a.mouseY); ok && a.overlay == nil {
		switch z.kind {
		case zPane:
			if p := a.panes[z.id]; p != nil && (isTerm(p) || isEditor(p)) {
				shape = "text"
			}
		case zDivider:
			shape = "ns-resize"
			if z.idx < len(a.dividers) && a.dividers[z.idx].horizontal {
				shape = "ew-resize"
			}
		case zSidebarEdge:
			shape = "ew-resize"
		}
	}
	if a.drag != nil {
		switch a.drag.kind {
		case dragSelect:
			shape = "text"
		case dragSidebar:
			shape = "ew-resize"
		case dragDivider:
			if a.drag.idx < len(a.dividers) && !a.dividers[a.drag.idx].horizontal {
				shape = "ns-resize"
			} else {
				shape = "ew-resize"
			}
		}
	}
	if shape != a.pointer {
		a.pointer = shape
		io.WriteString(a.out, "\x1b]22;"+shape+"\x1b\\")
	}
}

// ---------- glyphs ----------

type glyph struct {
	ch   string
	col  RGB
	bold bool
}

// agentGlyph is a pane's agent state as the sidebar shows it, as the Mac's
// "Orbit" icons do: an indigo arc going round while working (dimmer and
// slower while starting), "!" waiting for you, a check when finished (until
// looked at), a cross on error. Only work moves; the rest sit still.
func (a *App) agentGlyph(p *pb.Pane) (glyph, bool) {
	if p == nil || p.Agent == nil || (shownState(p.Agent) == pb.AgentState_AGENT_STATE_IDLE && p.Agent.Seen) {
		return glyph{}, false
	}
	switch shownState(p.Agent) {
	case pb.AgentState_AGENT_STATE_NEEDS_INPUT:
		return glyph{"!", a.th.orange, true}, true
	case pb.AgentState_AGENT_STATE_WORKING:
		return glyph{a.orbit(1), a.th.indigo, false}, true
	case pb.AgentState_AGENT_STATE_STARTING:
		return glyph{a.orbit(2), mix(a.th.indigo, a.th.sidebar, 0.45), false}, true
	case pb.AgentState_AGENT_STATE_IDLE:
		return glyph{"✓", a.th.green, true}, true
	case pb.AgentState_AGENT_STATE_ERRORED:
		return glyph{"✗", a.th.red, true}, true
	}
	return glyph{}, false
}

// shownState is the state to show: a command you ran with ! (after the
// agent went idle) shows as work.
func shownState(ag *pb.Agent) pb.AgentState {
	if ag.RunningCommand && ag.State == pb.AgentState_AGENT_STATE_IDLE {
		return pb.AgentState_AGENT_STATE_WORKING
	}
	return ag.State
}

var stateRank = map[pb.AgentState]int{
	pb.AgentState_AGENT_STATE_NEEDS_INPUT: 0, pb.AgentState_AGENT_STATE_ERRORED: 1,
	pb.AgentState_AGENT_STATE_WORKING: 2, pb.AgentState_AGENT_STATE_STARTING: 2, pb.AgentState_AGENT_STATE_IDLE: 3,
}

// tabGlyph is the most urgent agent glyph among a tab's panes, else the
// tab's kind (a terminal prompt, a globe for a page, an empty circle for an
// agent at rest).
func (a *App) tabGlyph(t *pb.Tab) (glyph, bool) {
	best, bestRank, found := glyph{}, 99, false
	for _, id := range tabIDs(t) {
		p := a.panes[id]
		if g, ok := a.agentGlyph(p); ok {
			if r := stateRank[shownState(p.Agent)]; r < bestRank {
				best, bestRank, found = g, r, true
			}
		}
	}
	if found {
		return best, true
	}
	if p := a.panes[t.FocusedPaneId]; p != nil && p.Kind == pb.PaneKind_PANE_KIND_BROWSER {
		return glyph{"◎", a.th.dim, false}, false
	}
	if p := a.panes[t.FocusedPaneId]; p != nil && p.Kind == pb.PaneKind_PANE_KIND_SIMULATOR {
		return glyph{"▯", a.th.dim, false}, false
	}
	if p := a.panes[t.FocusedPaneId]; isEditor(p) {
		if nerdFont {
			return glyph{string(nerdGlyphs["document"]), a.th.dim, false}, false
		}
		return glyph{"≡", a.th.dim, false}, false
	}
	// An agent at rest (finished and looked at): an empty circle, with a
	// dot in it while its shells (background tasks) or subagents still run.
	for _, id := range tabIDs(t) {
		if p := a.panes[id]; p != nil && p.Agent != nil && !p.Exited && p.Agent.State == pb.AgentState_AGENT_STATE_IDLE {
			if p.Agent.Shells > 0 || p.Agent.Subagents > 0 {
				return glyph{"⊙", a.th.dim, false}, false
			}
			return glyph{"○", a.th.dim, false}, false
		}
	}
	// The focused pane's program, with program icons on.
	if p := a.panes[t.FocusedPaneId]; nerdFont && p != nil && isTerminalKind(p.Kind) && !p.Exited {
		if r, ok := programGlyph(p.Program); ok {
			return glyph{string(r), a.th.dim, false}, false
		}
	}
	return glyph{"❯", a.th.faint, false}, false
}

// spinner is the working glyph: turning, or still with animations off.
func (a *App) spinner() string {
	if !animations {
		return "◌"
	}
	return spinnerFrames[(a.frame/5)%len(spinnerFrames)]
}

// hasLiveGlyphs: something on screen animates on its own (an agent's arc).
func (a *App) hasLiveGlyphs() bool {
	if !animations {
		return false
	}
	for _, p := range a.panes {
		if p.Agent == nil {
			continue
		}
		if s := shownState(p.Agent); s == pb.AgentState_AGENT_STATE_WORKING || s == pb.AgentState_AGENT_STATE_STARTING {
			return true
		}
	}
	return false
}

// orbitFrames turn a quarter arc round a circle, as the Mac's working icon
// does: once every 2.4 s (slow = 2: half the speed).
var orbitFrames = []string{"◜", "◝", "◞", "◟"}

func (a *App) orbit(slow int) string {
	if !animations {
		return "◌"
	}
	step := fps * 6 / 10 * slow
	return orbitFrames[(a.frame/step)%len(orbitFrames)]
}

func elapsed(ms int64) string {
	if ms <= 0 {
		return ""
	}
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

// tabDetail is the right-hand text of a tab row: how long its agent has been
// at it (nothing while idle), or the pane count.
func (a *App) tabDetail(t *pb.Tab) string {
	d := a.tabDetailOnly(t)
	if a.isRunTab(t) {
		// A scheduled task's run: a clock after the rest.
		if d == "" {
			return "◷"
		}
		return d + " ◷"
	}
	return d
}

func (a *App) tabDetailOnly(t *pb.Tab) string {
	ids := tabIDs(t)
	for _, id := range ids {
		if p := a.panes[id]; p != nil && p.Agent != nil && p.Agent.State != pb.AgentState_AGENT_STATE_EXITED && p.Agent.Harness != "" {
			if p.Agent.State == pb.AgentState_AGENT_STATE_IDLE {
				return ""
			}
			return elapsed(p.Agent.StateSinceUnixMs)
		}
	}
	if len(ids) > 1 {
		return fmt.Sprint(len(ids))
	}
	return ""
}

// ---------- sidebar ----------

type sideRow struct {
	kind  zoneKind
	id    string
	group string
	idx   int
	y     int
}

// visibleTabs is which of a Group's tabs show, and how many rows the group
// takes this frame. A collapsed Group still shows its selected tab; opening
// and closing animate, the rows narrowing toward the selected tab so it
// stays put. With the collapsed-agents setting, it also shows the tabs
// whose agent is active (agentActive).
func (a *App) visibleTabs(sp *pb.Group) []int {
	if a.horizontal() {
		return nil // they're in the tab bar
	}
	n := len(sp.TabIds)
	sel := -1
	var kept []int // tabs shown collapsed
	var keptIDs []string
	for i, id := range sp.TabIds {
		if id == a.selTab {
			sel = i
		}
		if id == a.selTab || a.prefs.CollapsedAgents && a.agentActive(a.tabs[id]) {
			kept = append(kept, i)
			keptIDs = append(keptIDs, id)
		}
	}
	target := float64(n)
	collapsed := a.prefs.Collapsed[sp.Id]
	if collapsed {
		target = float64(len(kept))
	}
	h := a.anim.get("group:"+sp.Id, gentle)
	h.to(target)
	// While the rows move, a tab that's leaving (an agent come to rest, the
	// selection gone elsewhere) is narrowed toward too, so it's the one that
	// slides away.
	if a.shownKept == nil {
		a.shownKept = map[string][]string{}
	}
	switch {
	case !collapsed:
		delete(a.shownKept, sp.Id)
	case h.settled():
		a.shownKept[sp.Id] = keptIDs
	default:
		for _, id := range a.shownKept[sp.Id] {
			if i := slices.Index(sp.TabIds, id); i >= 0 && !slices.Contains(kept, i) {
				kept = append(kept, i)
			}
		}
		slices.Sort(kept)
	}
	rows := int(math.Ceil(math.Max(0, math.Min(float64(n), h.pos)) - 0.001))
	if rows >= n {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}
	if rows <= 0 {
		return nil
	}
	if len(kept) > 1 || len(kept) == 1 && sel < 0 {
		return narrowTo(n, rows, kept, sel)
	}
	if sel < 0 {
		out := make([]int, rows)
		for i := range out {
			out[i] = i
		}
		return out
	}
	before, after := sel, n-1-sel
	extra := rows - 1
	b := 0
	if before+after > 0 {
		b = int(math.Round(float64(extra) * float64(before) / float64(before+after)))
	}
	b = min(b, before)
	e := min(extra-b, after)
	b = min(extra-e, before)
	var out []int
	for i := sel - b; i <= sel+e; i++ {
		out = append(out, i)
	}
	return out
}

// narrowTo picks rows of a Group's n tabs when a collapsed Group keeps
// several (kept, in order): the kept ones first (the selected one before
// the rest, should there be fewer rows than them, as an agent's tab slides
// in), then the others nearest a kept one, so a Group closing narrows
// toward the tabs it keeps.
func narrowTo(n, rows int, kept []int, sel int) []int {
	dist := func(i int) int {
		d := n
		for _, k := range kept {
			d = min(d, max(i-k, k-i))
		}
		return d
	}
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	rank := func(i int) (int, int) {
		if i == sel {
			return 0, 0
		}
		return 1 + dist(i), i
	}
	slices.SortStableFunc(order, func(x, y int) int {
		rx, ix := rank(x)
		ry, iy := rank(y)
		if rx != ry {
			return rx - ry
		}
		return ix - iy
	})
	out := order[:min(rows, n)]
	slices.Sort(out)
	return out
}

// agentActive is whether a tab has an agent that isn't at rest: working,
// starting, waiting on you, failed, or finished and not yet seen. The
// collapsed-agents setting keeps these tabs in a collapsed Group.
func (a *App) agentActive(t *pb.Tab) bool {
	if t == nil {
		return false
	}
	_, ok := a.tabGlyph(t) // true only for an agent's glyph
	return ok
}

func (a *App) drawSidebar() {
	sw := a.sidebarWidth()
	if sw == 0 {
		return
	}
	th, s := a.th, a.scr
	s.Fill(0, 0, sw, a.h, Cell{BG: th.sidebar, HasBG: true})
	a.addZone(zone{r: rect{0, 0, sw, a.h}, kind: zSidebar})

	// Bottom: Needs You, then the hint line.
	needs := len(a.attention) + len(a.grants)
	needRows := 0
	if needs > 0 {
		needRows = 2 + min(len(a.grants), 2) + min(len(a.attention), 4-min(len(a.grants), 2))
	}
	listBottom := a.h - 1 - needRows
	if a.linked() {
		listBottom-- // the row of computers
	}

	// Lay the rows out.
	var rows []sideRow
	type group struct {
		id       string
		top, end int
	}
	var groups []group
	y := 1 - a.sideScroll
	for _, sp := range a.shownGroups() {
		g := group{id: sp.Id, top: y}
		rows = append(rows, sideRow{kind: zGroup, id: sp.Id, group: sp.Id, y: y})
		y++
		for _, i := range a.visibleTabs(sp) {
			rows = append(rows, sideRow{kind: zTab, id: sp.TabIds[i], group: sp.Id, idx: i, y: y})
			y++
		}
		g.end = y
		groups = append(groups, g)
		y++ // a gap between Groups
	}
	rows = append(rows, sideRow{kind: zNewGroup, y: y})

	// The selection pill glides to the selected tab.
	pill := a.anim.get("pill", snappy)
	pillShown := false
	for _, r := range rows {
		if r.kind == zTab && r.id == a.selTab {
			pill.to(float64(r.y))
			pillShown = true
		}
	}

	hz, hok := a.prevZoneAt(a.mouseX, a.mouseY)
	hoverGroup := ""
	if hok && a.overlay == nil && (hz.kind == zTab || hz.kind == zGroup) {
		hoverGroup = hz.group
	}
	if a.drag != nil && a.drag.kind == dragTab {
		hoverGroup = ""
	}

	inList := func(y int) bool { return y >= 1 && y < listBottom }
	rowBG := map[int]RGB{}
	for _, g := range groups {
		amt := a.hoverAmount("group:"+g.id, g.id == hoverGroup)
		for y := g.top; y < g.end; y++ {
			bg := mix(th.sidebar, th.group, amt)
			rowBG[y] = bg
			if inList(y) {
				s.Paint(1, y, sw-2, 1, bg)
			}
		}
	}

	for _, r := range rows {
		if !inList(r.y) {
			continue
		}
		base := th.sidebar
		if bg, ok := rowBG[r.y]; ok {
			base = bg
		}
		switch r.kind {
		case zGroup:
			sp := a.group(r.id)
			a.addZone(zone{r: rect{1, r.y, sw - 2, 1}, kind: zGroup, id: r.id, group: r.id})
			collapsed := a.prefs.Collapsed[sp.Id] || a.horizontal()
			empty := len(sp.TabIds) == 0 // selected as itself, and no chevron
			if a.horizontal() || empty {
				// The selected Group wears the pill its tab would.
				bg := mix(base, th.hover, a.hoverAmount("tab:"+r.id, a.hovered(zGroup, r.id)))
				if slices.Contains(sp.TabIds, a.selTab) || a.selTab == "" && a.selGroup == sp.Id {
					bg = th.pill
				}
				if bg != base {
					s.Paint(2, r.y, sw-4, 1, bg)
					s.Set(1, r.y, Cell{Ch: "▐", FG: bg, HasFG: true, BG: base, HasBG: true})
					s.Set(sw-2, r.y, Cell{Ch: "▌", FG: bg, HasFG: true, BG: base, HasBG: true})
					base = bg
				}
			}
			x := 2
			if sp.Icon != "" {
				x += s.Text(x, r.y, 3, sp.Icon, Cell{BG: base, HasBG: true}) + 1
			} else if id, ok := a.pictureID(sp); ok {
				s.Set(x, r.y, placeholder(id, 0, base))
				s.Set(x+1, r.y, placeholder(id, 1, base))
				x += 3
			}
			// On another of your computers: its name, in its colour, at the end.
			badge, bw := "", 0
			if c := a.computerOf(sp); c != nil && !a.horizontal() {
				badge = c.Name
				if sp.Offline {
					badge += " · asleep"
				}
				badge = truncate(badge, max(sw/3, 6))
				bw = textWidth(badge) + 1
				fg := a.tint(c)
				if sp.Offline {
					fg = th.faint
				}
				s.TextRight(x, r.y, sw-x-3, badge, Cell{FG: fg, HasFG: true, BG: base, HasBG: true})
			}
			title := truncate(a.groupTitle(sp), sw-x-4-bw)
			fg := th.fg
			if sp.Offline {
				fg = th.dim
			}
			x += s.Text(x, r.y, sw-x-3-bw, title, Cell{FG: fg, HasFG: true, BG: base, HasBG: true, Bold: true})
			chev := "⌄"
			if collapsed {
				chev = "›"
			}
			if !a.horizontal() && !empty {
				s.Text(x+1, r.y, 1, chev, Cell{FG: th.faint, HasFG: true, BG: base, HasBG: true})
			}
			// Collapsed: hidden tabs still say if they need you.
			if collapsed {
				best, rank := glyph{}, 99
				for _, tid := range sp.TabIds {
					t := a.tabs[tid]
					shown := !a.horizontal() && (tid == a.selTab || a.prefs.CollapsedAgents && a.agentActive(t))
					if t != nil && !shown {
						for _, pid := range tabIDs(t) {
							if g, ok := a.agentGlyph(a.panes[pid]); ok && stateRank[shownState(a.panes[pid].Agent)] < rank {
								best, rank = g, stateRank[shownState(a.panes[pid].Agent)]
							}
						}
					}
				}
				if rank < 99 {
					s.Text(sw-4, r.y, 1, best.ch, Cell{FG: best.col, HasFG: true, BG: base, HasBG: true, Bold: best.bold})
				}
			}
		case zTab:
			t := a.tabs[r.id]
			if t == nil {
				continue
			}
			a.addZone(zone{r: rect{1, r.y, sw - 2, 1}, kind: zTab, id: r.id, group: r.group, idx: r.idx})
			bg := mix(base, th.hover, a.hoverAmount("tab:"+r.id, a.hovered(zTab, r.id)))
			if pillShown {
				bg = mix(bg, th.pill, clamp01(1-math.Abs(pill.pos-float64(r.y))))
			}
			dragging := a.drag != nil && a.drag.kind == dragTab && a.drag.id == r.id && a.drag.moved
			s.Paint(2, r.y, sw-4, 1, bg)
			// Rounded-ish ends: half blocks in the row colour.
			if bg != base {
				s.Set(1, r.y, Cell{Ch: "▐", FG: bg, HasFG: true, BG: base, HasBG: true})
				s.Set(sw-2, r.y, Cell{Ch: "▌", FG: bg, HasFG: true, BG: base, HasBG: true})
			}
			fg := th.fg
			if dragging {
				fg = th.faint
			}
			g, _ := a.tabGlyph(t)
			s.Text(3, r.y, 1, g.ch, Cell{FG: g.col, HasFG: true, BG: bg, HasBG: true, Bold: g.bold})
			detail := a.tabDetail(t)
			dw := 0
			if detail != "" && sw > 24 {
				dw = textWidth(detail) + 1
				s.TextRight(5, r.y, sw-8, detail, Cell{FG: th.dim, HasFG: true, BG: bg, HasBG: true})
			}
			title := truncate(a.tabTitle(t), sw-8-dw)
			s.Text(5, r.y, sw-8-dw, title, Cell{FG: fg, HasFG: true, BG: bg, HasBG: true, Bold: r.id == a.selTab})
		case zNewGroup:
			a.addZone(zone{r: rect{1, r.y, sw - 2, 1}, kind: zNewGroup, id: "new-group"})
			bg := mix(base, th.hover, a.hoverAmount("newspace", a.hovered(zNewGroup, "new-group")))
			s.Paint(2, r.y, sw-4, 1, bg)
			if bg != base {
				s.Set(1, r.y, Cell{Ch: "▐", FG: bg, HasFG: true, BG: base, HasBG: true})
				s.Set(sw-2, r.y, Cell{Ch: "▌", FG: bg, HasFG: true, BG: base, HasBG: true})
			}
			s.Text(3, r.y, sw-5, "+  New Group", Cell{FG: th.dim, HasFG: true, BG: bg, HasBG: true})
		}
	}
	a.drawDropMarker(rows, listBottom)

	// Needs You.
	if needs > 0 {
		y := listBottom
		s.Text(2, y, sw-3, fmt.Sprintf("Needs You  %d", needs), Cell{FG: th.orange, HasFG: true, BG: th.sidebar, HasBG: true, Bold: true})
		y++
		gn := min(len(a.grants), 2)
		for _, g := range a.grants[:gn] {
			bg := mix(th.sidebar, th.hover, a.hoverAmount("grant:"+g.Id, a.hovered(zGrant, g.Id)))
			s.Paint(1, y, sw-2, 1, bg)
			s.Text(2, y, sw-3, truncate("? "+grantText(g), sw-3), Cell{FG: th.orange, HasFG: true, BG: bg, HasBG: true})
			a.addZone(zone{r: rect{1, y, sw - 2, 1}, kind: zGrant, id: g.Id})
			y++
		}
		for _, at := range a.attention[:min(len(a.attention), 4-gn)] {
			name := "pane"
			if p := a.panes[at.PaneId]; p != nil {
				if t := a.tabs[p.TabId]; t != nil {
					name = a.tabTitle(t)
				}
			}
			bg := mix(th.sidebar, th.hover, a.hoverAmount("needs:"+at.Id, a.hovered(zNeeds, at.Id)))
			s.Paint(1, y, sw-2, 1, bg)
			sym := "!"
			if at.State == pb.AgentState_AGENT_STATE_IDLE {
				sym = "✓"
			}
			s.Text(2, y, sw-3, truncate(sym+" "+name+" · "+at.Summary, sw-3), Cell{FG: th.orange, HasFG: true, BG: bg, HasBG: true})
			a.addZone(zone{r: rect{1, y, sw - 2, 1}, kind: zNeeds, id: at.Id})
			y++
		}
	}

	if a.linked() {
		a.drawComputers(a.h-2, sw)
	}

	// Hint line.
	hint, col := "⌃B K  Commands   ⌃B ?  Keys", th.faint
	if a.prefix {
		hint, col = "⌃B …  waiting for a key", th.accent
	}
	s.Text(2, a.h-1, sw-3, truncate(hint, sw-3), Cell{FG: col, HasFG: true, BG: th.sidebar, HasBG: true, Bold: a.prefix})

	// The resize edge.
	edge := a.drag != nil && a.drag.kind == dragSidebar || a.hovered(zSidebarEdge, "")
	if edge {
		for y := 0; y < a.h; y++ {
			s.Set(sw-1, y, Cell{Ch: "▕", FG: th.accent, HasFG: true, BG: th.sidebar, HasBG: true})
		}
	}
	a.addZone(zone{r: rect{sw - 1, 0, 1, a.h}, kind: zSidebarEdge})
}

// drawDropMarker shows where a dragged tab will land: an accent bar at
// the row it will take.
func (a *App) drawDropMarker(rows []sideRow, bottom int) {
	d := a.drag
	if d == nil || d.kind != dragTab || !d.moved || d.dropGroup == "" {
		return
	}
	sw := a.sidebarWidth()
	for _, r := range rows {
		hit := (r.kind == zTab && r.group == d.dropGroup && r.idx == d.dropIndex) || (r.kind == zGroup && r.id == d.dropGroup && d.dropIndex < 0)
		if !hit || r.y < 1 || r.y >= bottom {
			continue
		}
		for x := 1; x < sw-1; x++ {
			c := a.scr.Get(x, r.y)
			c.BG, c.HasBG = mix(bgOf(c, a.th.sidebar), a.th.accent, 0.18), true
			a.scr.Set(x, r.y, c)
		}
		a.scr.Set(1, r.y, Cell{Ch: "▎", FG: a.th.accent, HasFG: true, BG: a.th.sidebar, HasBG: true})
	}
}

// drawDragGhost draws the tab being dragged under the mouse.
func (a *App) drawDragGhost() {
	d := a.drag
	if d == nil || d.kind != dragTab || !d.moved {
		return
	}
	t := a.tabs[d.id]
	if t == nil {
		return
	}
	sw := a.sidebarWidth()
	w := max(sw-4, 12)
	y := a.mouseY
	bg := mix(a.th.pill, a.th.accent, 0.25)
	a.scr.Fill(2, y, w, 1, Cell{BG: bg, HasBG: true})
	g, _ := a.tabGlyph(t)
	a.scr.Text(3, y, 1, g.ch, Cell{FG: g.col, HasFG: true, BG: bg, HasBG: true})
	a.scr.Text(5, y, w-4, truncate(a.tabTitle(t), w-4), Cell{FG: a.th.fg, HasFG: true, BG: bg, HasBG: true, Bold: true})
}

// ---------- toolbar ----------

type button struct{ id, label, short string }

func (a *App) drawToolbar() {
	m := a.mainRect()
	th, s := a.th, a.scr
	s.Fill(m.x, m.y, m.w, 1, Cell{BG: th.sidebar, HasBG: true})
	t := a.tabs[a.selTab]
	buttons := []button{{"split-right", "◫ Split Right", "◫"}, {"split-down", "⊟ Split Down", "⊟"}, {"new-tab", "+ New Tab", "+"}, {"palette", "⌘ Commands", "⌘"}}
	if t == nil {
		buttons = buttons[2:]
	}
	full := 0
	for _, b := range buttons {
		full += textWidth(b.label) + 3
	}
	title, sub := "", ""
	if t != nil {
		title = a.tabTitle(t)
		if sp := a.group(t.GroupId); sp != nil {
			sub = a.groupTitle(sp)
		}
		if p := a.panes[a.focus]; p != nil {
			if p.Kind == pb.PaneKind_PANE_KIND_BROWSER {
				sub += " — " + p.Url
			} else if p.Cwd != "" {
				sub += " — " + shortPath(p.Cwd)
			}
		}
	}
	short := m.w-full < textWidth(title)+12
	x := m.x + m.w - 1
	for i := len(buttons) - 1; i >= 0; i-- {
		b := buttons[i]
		label := b.label
		if short {
			label = b.short
		}
		w := textWidth(label) + 2
		x -= w
		on := a.hovered(zButton, b.id)
		bg := mix(th.sidebar, th.hover, a.hoverAmount("btn:"+b.id, on))
		s.Fill(x, m.y, w, 1, Cell{BG: bg, HasBG: true})
		s.Text(x+1, m.y, w-2, label, Cell{FG: th.dim, HasFG: true, BG: bg, HasBG: true})
		a.addZone(zone{r: rect{x, m.y, w, 1}, kind: zButton, id: b.id})
		x--
	}
	lx := m.x + 2
	room := x - lx - 1
	n := s.Text(lx, m.y, room, truncate(title, room), Cell{FG: th.fg, HasFG: true, BG: th.sidebar, HasBG: true, Bold: true})
	if sub != "" && room-n > 4 {
		s.Text(lx+n+2, m.y, room-n-2, truncate(sub, room-n-2), Cell{FG: th.dim, HasFG: true, BG: th.sidebar, HasBG: true})
	}
}

// ---------- panes ----------

func (a *App) drawPanes() {
	th, s := a.th, a.scr
	area := a.paneArea()
	t := a.tabs[a.selTab]
	if t == nil && a.group(a.selGroup) != nil {
		a.drawEmptyGroup(area, a.group(a.selGroup))
		return
	}
	if t == nil {
		msg := "No tabs yet"
		s.Text(area.x+(area.w-textWidth(msg))/2, area.y+area.h/2-1, area.w, msg, Cell{FG: th.dim, HasFG: true, Bold: true})
		btn := "  + New Tab  "
		bx := area.x + (area.w-textWidth(btn))/2
		bg := mix(th.menu, th.accent, 0.3+0.4*a.hoverAmount("empty", a.hovered(zEmpty, "")))
		s.Text(bx, area.y+area.h/2+1, textWidth(btn), btn, Cell{FG: th.fg, HasFG: true, BG: bg, HasBG: true})
		a.addZone(zone{r: rect{bx, area.y + area.h/2 + 1, textWidth(btn), 1}, kind: zEmpty})
		return
	}
	// Dividers.
	for i, d := range a.dividers {
		col := th.border
		if (a.drag != nil && a.drag.kind == dragDivider && a.drag.idx == i) || a.hovered(zDivider, fmt.Sprint(i)) {
			col = th.accent
		}
		ch := "│"
		if !d.horizontal {
			ch = "─"
		}
		for y := d.r.y; y < d.r.y+d.r.h; y++ {
			for x := d.r.x; x < d.r.x+d.r.w; x++ {
				s.Set(x, y, Cell{Ch: ch, FG: col, HasFG: true})
			}
		}
		a.addZone(zone{r: d.r, kind: zDivider, id: fmt.Sprint(i), idx: i})
	}
	curX, curY, curShow := 0, 0, false
	// The splits, then the floats over them, back to front.
	docking := false
	for _, id := range a.paneOrder {
		r, ok := a.paneRects[id]
		if !ok {
			continue
		}
		p := a.panes[id]
		focused := id == a.focus
		if card, ok := a.floatCards[id]; ok {
			if !docking {
				a.drawDockZone()
				docking = true
			}
			if focused {
				curShow = false // a split's cursor under the card
			}
			a.drawFloatCard(id, card, focused)
		}
		a.drawPaneHeader(id, p, r, focused)
		body := rect{r.x, r.y + 1, r.w, r.h - 1}
		a.addZone(zone{r: body, kind: zPane, id: id})
		if p != nil && p.Kind == pb.PaneKind_PANE_KIND_BROWSER {
			if !a.drawLive(p, body) {
				a.drawBrowserPlaceholder(p, body)
			}
			continue
		}
		if p != nil && p.Kind == pb.PaneKind_PANE_KIND_SIMULATOR {
			if !a.drawLive(p, body) {
				a.drawSimulatorPlaceholder(p, body)
			}
			continue
		}
		if isEditor(p) {
			if cx, cy, vis := a.drawEditor(p, body, focused); vis {
				curX, curY, curShow = cx, cy, true
			} else if focused {
				curShow = false
			}
			continue
		}
		pt := a.terms[id]
		if pt == nil {
			continue
		}
		cx, cy, vis := pt.draw(s, body.x, body.y, body.w, body.h)
		if focused && vis {
			curX, curY, curShow = cx, cy, true
		}
		if a.sel != nil && a.sel.pane == id {
			a.paintSelection(body)
		}
		if back, _, _ := pt.scrollback(); back > 0 {
			curShow = false
			badge := fmt.Sprintf(" ↑ %d lines · type or scroll to return ", back)
			bw := min(textWidth(badge), body.w)
			s.Text(body.x+body.w-bw, body.y, bw, badge, Cell{FG: th.fg, HasFG: true, BG: mix(th.menu, th.accent, 0.25), HasBG: true})
		}
	}
	if !docking {
		a.drawDockZone()
	}
	a.drawShelf(t)
	if a.overlay == nil {
		s.Cursor(curX, curY, curShow)
	}
}

func (a *App) drawPaneHeader(id string, p *pb.Pane, r rect, focused bool) {
	th, s := a.th, a.scr
	base := mix(th.bg, th.fg, 0.04)
	f := a.anim.get("focus:"+id, snappy)
	if focused {
		f.to(1)
	} else {
		f.to(0)
	}
	bg := mix(base, mix(th.bg, th.accent, 0.16), clamp01(f.pos))
	s.Fill(r.x, r.y, r.w, 1, Cell{BG: bg, HasBG: true})
	a.addZone(zone{r: rect{r.x, r.y, r.w, 1}, kind: zPaneHeader, id: id})
	if p == nil {
		return
	}
	name, detail := p.Title, shortPath(p.Cwd)
	editorStatus := ""
	if isEditor(p) {
		name, detail, editorStatus = a.editorHeader(p)
	}
	if p.Kind == pb.PaneKind_PANE_KIND_BROWSER {
		detail = p.Url
		if p.Loading {
			detail = a.spinner() + " " + detail
		}
	}
	x := r.x + 1
	if g, ok := a.paneIcon(p); ok {
		s.Text(x, r.y, 1, g.ch, Cell{FG: g.col, HasFG: true, BG: bg, HasBG: true, Bold: g.bold})
		x += 2
	}
	if p.Agent != nil && p.Agent.Harness != "" {
		label := p.Agent.Harness + " ▾"
		on := a.hovered(zPaneMenu, id)
		lbg := mix(bg, th.hover, a.hoverAmount("agent:"+id, on))
		n := s.Text(x, r.y, r.w-2, label, Cell{FG: th.fg, HasFG: true, BG: lbg, HasBG: true, Bold: true})
		a.addZone(zone{r: rect{x, r.y, n, 1}, kind: zPaneMenu, id: id})
		x += n + 2
	} else {
		x += s.Text(x, r.y, r.w-2, truncate(name, r.w/2), Cell{FG: th.fg, HasFG: true, BG: bg, HasBG: true, Bold: focused}) + 2
	}
	// Right: status, and a close button while hovered.
	right := r.x + r.w - 1
	hover := a.hoverAmount("hdr:"+id, a.hovered(zPaneHeader, id) || a.hovered(zPaneClose, id) || a.hovered(zPaneMenu, id) ||
		a.hovered(zFloatMin, id) || a.hovered(zFloatDock, id))
	if hover > 0.5 {
		on := a.hovered(zPaneClose, id)
		col := th.dim
		if on {
			col = th.red
		}
		s.Text(right-1, r.y, 1, "×", Cell{FG: col, HasFG: true, BG: bg, HasBG: true, Bold: on})
		a.addZone(zone{r: rect{right - 2, r.y, 3, 1}, kind: zPaneClose, id: id})
		right -= 3
		right = a.drawFloatButtons(id, r.y, right, bg)
	}
	status := ""
	var scol RGB
	if p.Exited {
		status, scol = fmt.Sprintf("Exited %d", p.ExitStatus), th.dim
	} else if g, ok := a.agentGlyph(p); ok {
		labels := map[pb.AgentState]string{
			pb.AgentState_AGENT_STATE_NEEDS_INPUT: "Waiting for you", pb.AgentState_AGENT_STATE_WORKING: "Working",
			pb.AgentState_AGENT_STATE_STARTING: "Starting", pb.AgentState_AGENT_STATE_IDLE: "Finished", pb.AgentState_AGENT_STATE_ERRORED: "Errored",
		}
		status, scol = g.ch+" "+labels[p.Agent.State], g.col
	} else if editorStatus != "" {
		status, scol = editorStatus, th.dim
	}
	if status != "" {
		w := textWidth(status)
		if right-w > x+4 {
			s.Text(right-w, r.y, w, status, Cell{FG: scol, HasFG: true, BG: bg, HasBG: true})
			right -= w + 2
		}
	}
	if right-x > 4 {
		s.Text(x, r.y, right-x, truncate(detail, right-x), Cell{FG: th.dim, HasFG: true, BG: bg, HasBG: true})
	}
}

func (a *App) drawBrowserPlaceholder(p *pb.Pane, r rect) {
	th, s := a.th, a.scr
	cy := r.y + r.h/2 - 2
	title := p.Title
	if title == "" {
		title = "Browser"
	}
	line := func(y int, str string, c Cell) {
		str = truncate(str, r.w-4)
		s.Text(r.x+(r.w-textWidth(str))/2, y, r.w-4, str, c)
	}
	line(cy, "◎ "+title, Cell{FG: th.fg, HasFG: true, Bold: true})
	line(cy+1, p.Url, Cell{FG: th.dim, HasFG: true})
	line(cy+2, "Pages show in the Hammock app.", Cell{FG: th.faint, HasFG: true})
	btn := "  Open in Browser  "
	bx := r.x + (r.w-textWidth(btn))/2
	bg := mix(th.menu, th.accent, 0.25+0.45*a.hoverAmount("open:"+p.Id, a.hovered(zBrowserOpen, p.Id)))
	s.Text(bx, cy+4, textWidth(btn), btn, Cell{FG: th.fg, HasFG: true, BG: bg, HasBG: true})
	a.addZone(zone{r: rect{bx, cy + 4, textWidth(btn), 1}, kind: zBrowserOpen, id: p.Id})
}

func (a *App) drawSimulatorPlaceholder(p *pb.Pane, r rect) {
	th, s := a.th, a.scr
	cy := r.y + r.h/2 - 1
	line := func(y int, str string, c Cell) {
		str = truncate(str, r.w-4)
		s.Text(r.x+(r.w-textWidth(str))/2, y, r.w-4, str, c)
	}
	state := "Booting…"
	if p.DeviceBooted {
		state = "Running"
	}
	line(cy, "▯ "+p.Title, Cell{FG: th.fg, HasFG: true, Bold: true})
	line(cy+1, state, Cell{FG: th.dim, HasFG: true})
	line(cy+2, "Simulators show in the Hammock app.", Cell{FG: th.faint, HasFG: true})
}

// isTerm reports whether a pane is a terminal (not a browser or simulator).
func isTerm(p *pb.Pane) bool {
	return p.Kind == pb.PaneKind_PANE_KIND_TERMINAL || p.Kind == pb.PaneKind_PANE_KIND_UNSPECIFIED
}

// ---------- toast ----------

func (a *App) drawToast() {
	if !a.toastVisible() {
		return
	}
	sp := a.anim.get("toast", gentle)
	age := time.Since(a.toastAt)
	if age > 2400*time.Millisecond {
		sp.to(0)
	} else {
		sp.to(1)
	}
	msg := "  " + a.toast + "  "
	w := textWidth(msg)
	m := a.mainRect()
	x := m.x + m.w - w - 2 + int(math.Round((1-sp.pos)*float64(w+3)))
	y := a.h - 2
	bg := mix(a.th.bg, a.th.menu, clamp01(sp.pos))
	fg := mix(a.th.bg, a.th.fg, clamp01(sp.pos))
	for i := 0; i < w && x+i < a.w; i++ {
		a.scr.Set(x+i, y, Cell{Ch: " ", BG: bg, HasBG: true})
	}
	a.scr.Text(x, y, max(0, min(w, a.w-x)), msg, Cell{FG: fg, HasFG: true, BG: bg, HasBG: true, Bold: true})
}
