package tui

import (
	"math"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Floating panes: a tab's panes floating over its splits (Tab.floating, back
// to front, frames in fractions of the tab), drawn as boxes with a shadow,
// and the shelf of minimised ones along the bottom. A float moves by its
// header or top border and resizes from its other borders; dragged with Alt
// held, it docks into the split under the pointer, on the nearest side.

// The smallest float, in cells (border included).
const minFloatW, minFloatH = 24, 8

// floatEdge says which borders a drag on a float's border moves.
const (
	edgeMove = iota // the top border: moves the float
	edgeLeft
	edgeRight
	edgeBottom
	edgeBottomLeft
	edgeBottomRight
)

type dockTarget struct {
	pane string
	edge int // edgeLeft, edgeRight, edgeMove (above), edgeBottom
}

// tabIDs lists a tab's panes: its splits', then its floats'.
func tabIDs(t *pb.Tab) []string {
	ids := layoutIDs(t.GetLayout())
	for _, f := range t.GetFloating() {
		ids = append(ids, f.PaneId)
	}
	return ids
}

// shownIDs lists the panes a tab shows: its splits', then the floats not on
// its shelf (back to front).
func shownIDs(t *pb.Tab) []string {
	ids := layoutIDs(t.GetLayout())
	for _, f := range t.GetFloating() {
		if !f.Minimised {
			ids = append(ids, f.PaneId)
		}
	}
	return ids
}

func floatOf(t *pb.Tab, id string) *pb.FloatingPane {
	for _, f := range t.GetFloating() {
		if f.PaneId == id {
			return f
		}
	}
	return nil
}

func hasShelf(t *pb.Tab) bool {
	for _, f := range t.GetFloating() {
		if f.Minimised {
			return true
		}
	}
	return false
}

// splitArea is where the tab's splits (and floats) go: the pane area, less
// the shelf's row when it shows.
func (a *App) splitArea(t *pb.Tab) rect {
	r := a.paneArea()
	if hasShelf(t) {
		r.h = max(r.h-1, 1)
	}
	return r
}

// floatBounds is where floats may go: area less a margin, a zoomed float's size.
func floatBounds(area rect) rect {
	if area.w < minFloatW+4 || area.h < minFloatH+2 {
		return area
	}
	return rect{area.x + 2, area.y + 1, area.w - 4, area.h - 2}
}

// floatRect is a float's card in area, in cells.
func floatRect(f *pb.FloatingPane, area rect) rect {
	if f.Zoomed {
		return floatBounds(area)
	}
	fr := f.GetFrame()
	r := rect{
		area.x + int(math.Round(float64(fr.GetX())*float64(area.w))),
		area.y + int(math.Round(float64(fr.GetY())*float64(area.h))),
		int(math.Round(float64(fr.GetW()) * float64(area.w))),
		int(math.Round(float64(fr.GetH()) * float64(area.h))),
	}
	return clampCard(r, area)
}

// clampCard keeps a card at least the smallest float, at most the zoomed
// size, and inside it.
func clampCard(r, area rect) rect {
	b := floatBounds(area)
	r.w = min(max(r.w, minFloatW), b.w)
	r.h = min(max(r.h, minFloatH), b.h)
	r.x = min(max(r.x, b.x), b.x+b.w-r.w)
	r.y = min(max(r.y, b.y), b.y+b.h-r.h)
	return r
}

// frameOf is a card's frame in fractions of area.
func frameOf(r, area rect) *pb.Frame {
	w, h := float32(max(area.w, 1)), float32(max(area.h, 1))
	return &pb.Frame{X: float32(r.x-area.x) / w, Y: float32(r.y-area.y) / h, W: float32(r.w) / w, H: float32(r.h) / h}
}

// placeFloats puts the tab's floats showing over area, the pane inside each
// card's border.
func (a *App) placeFloats(t *pb.Tab, area rect, rects map[string]rect) {
	for _, f := range t.Floating {
		if f.Minimised {
			continue
		}
		card := floatRect(f, area)
		if live, ok := a.floatLive[f.PaneId]; ok {
			card = live
		}
		a.floatCards[f.PaneId] = card
		rects[f.PaneId] = rect{card.x + 1, card.y + 1, max(card.w-2, 1), max(card.h-2, 1)}
		a.paneOrder = append(a.paneOrder, f.PaneId)
	}
}

// drawFloatCard draws a float's shadow over what's beneath, clears its card
// and draws its border (the pane draws itself inside).
func (a *App) drawFloatCard(id string, c rect, focused bool) {
	th, s := a.th, a.scr
	// A shadow a cell below and two to the right (cells are tall): what's
	// there darkened.
	shade := func(x, y int) {
		cell := s.Get(x, y)
		bg := th.bg
		if cell.HasBG {
			bg = cell.BG
		}
		if cell.HasFG {
			cell.FG = mix(cell.FG, bg, 0.6)
		}
		cell.BG, cell.HasBG = mix(bg, RGB{}, 0.5), true
		s.Set(x, y, cell)
	}
	for y := c.y + 1; y <= c.y+c.h; y++ {
		shade(c.x+c.w, y)
		shade(c.x+c.w+1, y)
	}
	for x := c.x + 2; x < c.x+c.w; x++ {
		shade(x, c.y+c.h)
	}
	s.Fill(c.x, c.y, c.w, c.h, Cell{Ch: " ", BG: th.bg, HasBG: true})
	col := mix(th.fg, th.bg, 0.55)
	if focused {
		col = th.accent
	}
	if a.drag != nil && a.drag.id == id && (a.drag.kind == dragFloat || a.drag.kind == dragFloatResize) {
		col = th.accent
	}
	st := Cell{FG: col, HasFG: true, BG: th.bg, HasBG: true}
	set := func(x, y int, ch string) { st.Ch = ch; s.Set(x, y, st) }
	for x := c.x + 1; x < c.x+c.w-1; x++ {
		set(x, c.y, "─")
		set(x, c.y+c.h-1, "─")
	}
	for y := c.y + 1; y < c.y+c.h-1; y++ {
		set(c.x, y, "│")
		set(c.x+c.w-1, y, "│")
	}
	set(c.x, c.y, "╭")
	set(c.x+c.w-1, c.y, "╮")
	set(c.x, c.y+c.h-1, "╰")
	set(c.x+c.w-1, c.y+c.h-1, "╯")
	// Its borders, for moving and resizing (the pane's own zones go on top).
	a.addZone(zone{r: rect{c.x, c.y, c.w, 1}, kind: zFloatEdge, id: id, idx: edgeMove})
	a.addZone(zone{r: rect{c.x, c.y + 1, 1, c.h - 1}, kind: zFloatEdge, id: id, idx: edgeLeft})
	a.addZone(zone{r: rect{c.x + c.w - 1, c.y + 1, 1, c.h - 1}, kind: zFloatEdge, id: id, idx: edgeRight})
	a.addZone(zone{r: rect{c.x, c.y + c.h - 1, c.w, 1}, kind: zFloatEdge, id: id, idx: edgeBottom})
	a.addZone(zone{r: rect{c.x, c.y + c.h - 1, 2, 1}, kind: zFloatEdge, id: id, idx: edgeBottomLeft})
	a.addZone(zone{r: rect{c.x + c.w - 2, c.y + c.h - 1, 2, 1}, kind: zFloatEdge, id: id, idx: edgeBottomRight})
}

// drawFloatButtons adds a float's minimise and dock buttons to its hovered
// header, left of x; returns where the header's text must stop.
func (a *App) drawFloatButtons(id string, y, right int, bg RGB) int {
	t := a.tabs[a.selTab]
	if t == nil || floatOf(t, id) == nil {
		return right
	}
	th, s := a.th, a.scr
	for _, b := range []struct {
		kind zoneKind
		ch   string
	}{{zFloatDock, "⊞"}, {zFloatMin, "–"}} {
		col := th.dim
		if a.hovered(b.kind, id) {
			col = th.accent
		}
		s.Text(right-1, y, 1, b.ch, Cell{FG: col, HasFG: true, BG: bg, HasBG: true})
		a.addZone(zone{r: rect{right - 2, y, 3, 1}, kind: b.kind, id: id})
		right -= 3
	}
	return right
}

// drawDockZone marks where a float dragged with Alt held would dock.
func (a *App) drawDockZone() {
	d := a.dock
	if d == nil {
		return
	}
	r, ok := a.paneRects[d.pane]
	if !ok {
		return
	}
	switch d.edge {
	case edgeLeft:
		r.w /= 2
	case edgeRight:
		r.x, r.w = r.x+r.w/2, r.w-r.w/2
	case edgeMove:
		r.h /= 2
	case edgeBottom:
		r.y, r.h = r.y+r.h/2, r.h-r.h/2
	}
	a.scr.Paint(r.x, r.y, r.w, r.h, mix(a.th.bg, a.th.accent, 0.3))
	label := " Dock Here "
	a.scr.Text(r.x+(r.w-textWidth(label))/2, r.y+r.h/2, r.w, label, Cell{FG: a.th.fg, HasFG: true, BG: mix(a.th.menu, a.th.accent, 0.4), HasBG: true, Bold: true})
}

// drawShelf draws the tab's minimised floats along the bottom of its area.
func (a *App) drawShelf(t *pb.Tab) {
	if t == nil || !hasShelf(t) {
		return
	}
	th, s := a.th, a.scr
	pa := a.paneArea()
	y := pa.y + pa.h - 1
	s.Fill(pa.x, y, pa.w, 1, Cell{Ch: " ", BG: mix(th.bg, th.fg, 0.04), HasBG: true})
	x := pa.x + 1
	for _, f := range t.Floating {
		if !f.Minimised {
			continue
		}
		p := a.panes[f.PaneId]
		name := "pane"
		if p != nil {
			name = p.Title
			if p.Agent != nil && p.Agent.Harness != "" {
				name = p.Agent.Harness
			}
		}
		icon := "▭"
		var icol RGB = th.dim
		if g, ok := a.agentGlyph(p); ok {
			icon, icol = g.ch, g.col
		}
		label := " " + truncate(name, 20) + " "
		w := 2 + textWidth(label)
		if x+w > pa.x+pa.w {
			break
		}
		bg := mix(th.menu, th.accent, 0.12+0.3*a.hoverAmount("shelf:"+f.PaneId, a.hovered(zShelf, f.PaneId)))
		s.Text(x, y, 2, " "+icon, Cell{FG: icol, HasFG: true, BG: bg, HasBG: true})
		s.Text(x+2, y, w-2, label, Cell{FG: th.fg, HasFG: true, BG: bg, HasBG: true})
		a.addZone(zone{r: rect{x, y, w, 1}, kind: zShelf, id: f.PaneId})
		x += w + 1
	}
}

// ---------- mouse ----------

// startFloatDrag begins moving (edgeMove) or resizing a float from (x, y).
func (a *App) startFloatDrag(id string, edge, x, y int) {
	card, ok := a.floatCards[id]
	if !ok {
		return
	}
	kind := dragFloatResize
	if edge == edgeMove {
		kind = dragFloat
	}
	a.drag = &drag{kind: kind, id: id, idx: edge, startX: x, startY: y, from: card}
}

func (a *App) dragFloatTo(m mouseEv) {
	d := a.drag
	t := a.tabs[a.selTab]
	if t == nil {
		return
	}
	area := a.splitArea(t)
	dx, dy := m.x-d.startX, m.y-d.startY
	if dx != 0 || dy != 0 {
		d.moved = true
	}
	r := d.from
	switch d.idx {
	case edgeMove:
		r.x, r.y = r.x+dx, r.y+dy
	case edgeLeft:
		r.x, r.w = r.x+dx, r.w-dx
	case edgeRight:
		r.w += dx
	case edgeBottom:
		r.h += dy
	case edgeBottomLeft:
		r.x, r.w, r.h = r.x+dx, r.w-dx, r.h+dy
	case edgeBottomRight:
		r.w, r.h = r.w+dx, r.h+dy
	}
	if d.idx == edgeLeft || d.idx == edgeBottomLeft {
		// The right side stays put.
		right := d.from.x + d.from.w
		r.w = max(r.w, minFloatW)
		r.x = right - r.w
	}
	a.floatLive[d.id] = clampCard(r, area)
	a.dock = nil
	if d.kind == dragFloat && m.alt {
		a.dock = a.dockAt(t, m.x, m.y)
	}
	a.syncPanes()
	a.dirty = true
}

// dockAt is the split pane under (x, y) and its side nearest it.
func (a *App) dockAt(t *pb.Tab, x, y int) *dockTarget {
	for _, id := range layoutIDs(t.Layout) {
		r, ok := a.paneRects[id]
		if !ok || !r.contains(x, y) {
			continue
		}
		fx := (float64(x-r.x) + 0.5) / float64(max(r.w, 1))
		fy := (float64(y-r.y) + 0.5) / float64(max(r.h, 1))
		best, edge := fx, edgeLeft
		for _, c := range []struct {
			d float64
			e int
		}{{1 - fx, edgeRight}, {fy, edgeMove}, {1 - fy, edgeBottom}} {
			if c.d < best {
				best, edge = c.d, c.e
			}
		}
		return &dockTarget{pane: id, edge: edge}
	}
	return nil
}

func (a *App) endFloatDrag() {
	d := a.drag
	live, ok := a.floatLive[d.id]
	dock := a.dock
	delete(a.floatLive, d.id)
	a.dock = nil
	t := a.tabs[a.selTab]
	if !ok || !d.moved || t == nil {
		a.syncPanes()
		return
	}
	if dock != nil {
		m := &pb.FloatPane{Action: pb.FloatPane_ACTION_DOCK, PaneId: d.id, TargetPaneId: dock.pane,
			Direction: pb.Split_DIRECTION_HORIZONTAL, Before: dock.edge == edgeLeft || dock.edge == edgeMove}
		if dock.edge == edgeMove || dock.edge == edgeBottom {
			m.Direction = pb.Split_DIRECTION_VERTICAL
		}
		a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_FloatPane{FloatPane: m}}, nil)
		a.syncPanes()
		return
	}
	fr := frameOf(live, a.splitArea(t))
	if f := floatOf(t, d.id); f != nil {
		f.Frame, f.Zoomed = fr, false // as the daemon will have it: no jump back
	}
	a.syncPanes()
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_FloatPane{FloatPane: &pb.FloatPane{Action: pb.FloatPane_ACTION_FRAME, PaneId: d.id, Frame: fr}}}, nil)
}

// ---------- actions ----------

// newFloating opens a terminal floating over paneID's tab, focused.
func (a *App) newFloating(paneID string) {
	if paneID == "" {
		return
	}
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: paneID, Floating: true}}}, func(r *pb.ServerMessage) {
		a.setFocus(client.CreatedID(r))
	})
}

// toggleFloating floats a split pane, or docks a floating one where it came from.
func (a *App) toggleFloating(paneID string) {
	p := a.panes[paneID]
	if p == nil {
		return
	}
	if t := a.tabs[p.TabId]; t != nil && floatOf(t, paneID) != nil {
		a.arrange(paneID, pb.FloatPane_ACTION_DOCK)
		return
	}
	if !a.canFloat(paneID) {
		a.flash("A tab's only pane in its splits can't float")
		return
	}
	a.arrange(paneID, pb.FloatPane_ACTION_FLOAT)
}

func (a *App) canFloat(paneID string) bool {
	p := a.panes[paneID]
	if p == nil {
		return false
	}
	t := a.tabs[p.TabId]
	return t != nil && floatOf(t, paneID) == nil && len(layoutIDs(t.Layout)) > 1
}

func (a *App) isFloating(paneID string) bool {
	p := a.panes[paneID]
	return p != nil && a.tabs[p.TabId] != nil && floatOf(a.tabs[p.TabId], paneID) != nil
}

func (a *App) arrange(paneID string, action pb.FloatPane_Action) {
	if paneID == "" {
		return
	}
	if action != pb.FloatPane_ACTION_FLOAT && !a.isFloating(paneID) {
		return
	}
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_FloatPane{FloatPane: &pb.FloatPane{Action: action, PaneId: paneID}}}, nil)
}

func (a *App) toggleFloats(tabID string) {
	if t := a.tabs[tabID]; t == nil || len(t.Floating) == 0 {
		return
	}
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_FloatPane{FloatPane: &pb.FloatPane{Action: pb.FloatPane_ACTION_TOGGLE_ALL, TabId: tabID}}}, nil)
}

// floatMenuItems are a pane's menu items for floating.
func (a *App) floatMenuItems(id string) []menuItem {
	items := []menuItem{{label: "New Floating Pane", hint: "⌃B f", run: func() { a.newFloating(id) }}}
	if a.isFloating(id) {
		return append(items,
			menuItem{label: "Dock Pane", hint: "⌃B F", run: func() { a.arrange(id, pb.FloatPane_ACTION_DOCK) }},
			menuItem{label: "Minimise Pane", hint: "⌃B m", run: func() { a.arrange(id, pb.FloatPane_ACTION_MINIMISE) }},
			menuItem{label: "Zoom Pane", hint: "⌃B z", run: func() { a.arrange(id, pb.FloatPane_ACTION_ZOOM) }})
	}
	return append(items, menuItem{label: "Float Pane", hint: "⌃B F", disabled: !a.canFloat(id), run: func() { a.arrange(id, pb.FloatPane_ACTION_FLOAT) }})
}
