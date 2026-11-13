package tui

import (
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

type dragKind int

const (
	dragTab dragKind = iota
	dragDivider
	dragSidebar
	dragSelect
	dragFloat       // a floating pane by its header or top border
	dragFloatResize // a floating pane by another border (idx: which)
	dragEditor      // selecting text in an editor pane
)

type drag struct {
	kind      dragKind
	id        string
	idx       int
	startX    int
	startY    int
	moved     bool
	dropGroup string
	dropIndex int  // -1: the end of dropGroup
	from      rect // a float's card when the drag began
}

// selection is a run of text selected with the mouse in a pane, in the
// pane's cell coordinates (linear, like a terminal's).
type selection struct {
	pane           string
	ax, ay, bx, by int
}

func (sel *selection) ordered() (x0, y0, x1, y1 int) {
	if sel.ay < sel.by || (sel.ay == sel.by && sel.ax <= sel.bx) {
		return sel.ax, sel.ay, sel.bx, sel.by
	}
	return sel.bx, sel.by, sel.ax, sel.ay
}

func (sel *selection) contains(x, y int) bool {
	x0, y0, x1, y1 := sel.ordered()
	if y < y0 || y > y1 {
		return false
	}
	if y == y0 && x < x0 {
		return false
	}
	if y == y1 && x > x1 {
		return false
	}
	return true
}

func (a *App) paintSelection(body rect) {
	bg := mix(a.th.bg, a.th.accent, 0.45)
	for y := 0; y < body.h; y++ {
		for x := 0; x < body.w; x++ {
			if a.sel.contains(x, y) {
				c := a.scr.Get(body.x+x, body.y+y)
				c.BG, c.HasBG, c.Inverse = bg, true, false
				if !c.HasFG {
					c.FG, c.HasFG = a.th.fg, true
				}
				a.scr.Set(body.x+x, body.y+y, c)
			}
		}
	}
}

// selectionText reads the selected cells off the screen.
func (a *App) selectionText() string {
	r, ok := a.paneRects[a.sel.pane]
	if !ok {
		return ""
	}
	body := rect{r.x, r.y + 1, r.w, r.h - 1}
	x0, y0, x1, y1 := a.sel.ordered()
	var lines []string
	for y := y0; y <= min(y1, body.h-1); y++ {
		from, to := 0, body.w-1
		if y == y0 {
			from = x0
		}
		if y == y1 {
			to = min(x1, body.w-1)
		}
		var b strings.Builder
		for x := from; x <= to; x++ {
			c := a.scr.Get(body.x+x, body.y+y)
			if c.Cont {
				continue
			}
			if c.Ch == "" {
				b.WriteByte(' ')
			} else {
				b.WriteString(c.Ch)
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(lines, "\n")
}

// wordAt selects the run of non-space cells around (x, y) in a pane body.
func (a *App) wordAt(body rect, x, y int) (int, int) {
	isWord := func(x int) bool {
		c := a.scr.Get(body.x+x, body.y+y)
		return c.Ch != "" && c.Ch != " " && !strings.ContainsAny(c.Ch, "│\"'()[]{}<>,;")
	}
	l, r := x, x
	for l > 0 && isWord(l-1) {
		l--
	}
	for r < body.w-1 && isWord(r+1) {
		r++
	}
	return l, r
}

func (a *App) mouse(m mouseEv) {
	a.mouseX, a.mouseY = m.x, m.y
	a.dirty = true
	if a.overlay != nil {
		a.overlay.mouse(a, m)
		return
	}
	if a.drag != nil {
		a.dragMouse(m)
		return
	}
	if z, _ := a.zoneAt(m.x, m.y); a.liveMouse(z, m) || a.editorMouse(z, m) {
		return
	}
	z, ok := a.zoneAt(m.x, m.y)
	if !ok {
		return
	}

	// The pane's own program gets the mouse if it asked for it (shift
	// bypasses that, as in most terminals).
	if z.kind == zPane && !m.shift {
		if pt := a.terms[z.id]; pt != nil {
			r := a.paneRects[z.id]
			col, row := m.x-r.x, m.y-r.y-1
			if m.press && z.id != a.focus {
				a.setFocus(z.id)
			}
			if m.press || m.wheel != 0 || (m.motion && (a.mousePane == z.id || z.id == a.focus)) || m.release {
				if b := pt.encodeMouse(m, col, row); b != nil {
					if m.press {
						a.mousePane = z.id
					}
					a.sendTo(z.id, b)
					return
				}
			}
		}
	}
	if m.release && a.mousePane != "" {
		if pt := a.terms[a.mousePane]; pt != nil {
			r := a.paneRects[a.mousePane]
			if b := pt.encodeMouse(m, m.x-r.x, m.y-r.y-1); b != nil {
				a.sendTo(a.mousePane, b)
			}
		}
		a.mousePane = ""
		return
	}

	switch {
	case m.wheel != 0:
		a.wheel(z, m)
	case m.press && m.button == 0:
		a.leftPress(z, m)
	case m.press && m.button == 2:
		a.rightPress(z, m)
	}
}

func (a *App) sendTo(pane string, b []byte) {
	_ = a.c.Send(bgctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: pane, Data: b}}})
}

func (a *App) wheel(z zone, m mouseEv) {
	switch z.kind {
	case zSidebar, zTab, zGroup, zNewGroup:
		a.sideScroll = max(0, a.sideScroll+m.wheel*2)
	case zPane:
		pt := a.terms[z.id]
		if pt == nil {
			return
		}
		if pt.wheelAsArrows() {
			seq := "\x1b[B"
			if m.wheel < 0 {
				seq = "\x1b[A"
			}
			a.sendTo(z.id, []byte(strings.Repeat(seq, 3)))
			return
		}
		pt.scroll(m.wheel * 3)
	}
}

func (a *App) leftPress(z zone, m mouseEv) {
	now := time.Now()
	if now.Sub(a.lastClick) < 400*time.Millisecond {
		a.clickCount++
	} else {
		a.clickCount = 1
	}
	a.lastClick = now

	switch z.kind {
	case zTab, zBarTab:
		// Selected on release, unless it's dragged to another place.
		a.drag = &drag{kind: dragTab, id: z.id, startX: m.x, startY: m.y}
	case zGroup:
		if g := a.group(z.id); a.horizontal() || g != nil && len(g.TabIds) == 0 {
			a.selectGroup(z.id) // a Group without tabs: its own page
		} else {
			a.toggleGroup(z.id)
		}
	case zBarClose:
		a.closeTab(z.id)
	case zNewGroup:
		a.newGroup()
	case zComputer:
		// Again shows them all.
		if a.prefs.Computer == z.id {
			a.setComputerFilter("")
		} else {
			a.setComputerFilter(z.id)
		}
	case zNeeds:
		for _, at := range a.attention {
			if at.Id == z.id {
				a.reveal(at.PaneId)
			}
		}
	case zGrant:
		a.grantMenu(z.id, m.x, m.y)
	case zButton:
		a.button(z.id)
	case zPaneHeader:
		a.setFocus(z.id)
		if a.isFloating(z.id) {
			// A float's header moves it; double-click zooms it.
			if a.clickCount == 2 {
				a.arrange(z.id, pb.FloatPane_ACTION_ZOOM)
				return
			}
			a.startFloatDrag(z.id, edgeMove, m.x, m.y)
			return
		}
		if a.clickCount == 2 {
			// Double-click a header: zoom isn't a thing yet; rename the tab.
			if t := a.tabs[a.selTab]; t != nil {
				a.openPrompt("Rename Tab", "Name", t.Name, func(v string) { a.renameTab(t.Id, v) })
			}
		}
	case zPaneMenu:
		a.setFocus(z.id)
		a.paneMenu(z.id, m.x, m.y+1)
	case zPaneClose:
		a.closePane(z.id)
	case zWake:
		a.setFocus(z.id)
		a.sleepPane(z.id, true)
	case zFloatEdge:
		a.setFocus(z.id)
		a.startFloatDrag(z.id, z.idx, m.x, m.y)
	case zFloatMin:
		a.arrange(z.id, pb.FloatPane_ACTION_MINIMISE)
	case zFloatDock:
		a.arrange(z.id, pb.FloatPane_ACTION_DOCK)
	case zShelf:
		a.arrange(z.id, pb.FloatPane_ACTION_RESTORE)
		a.focus = z.id
	case zPane:
		a.setFocus(z.id)
		r := a.paneRects[z.id]
		body := rect{r.x, r.y + 1, r.w, r.h - 1}
		x, y := m.x-body.x, m.y-body.y
		switch a.clickCount {
		case 2:
			l, rr := a.wordAt(body, x, y)
			a.sel = &selection{pane: z.id, ax: l, ay: y, bx: rr, by: y}
			a.copyText(a.selectionText())
		case 3:
			a.sel = &selection{pane: z.id, ax: 0, ay: y, bx: body.w - 1, by: y}
			a.copyText(a.selectionText())
		default:
			a.sel = nil
			a.drag = &drag{kind: dragSelect, id: z.id, startX: x, startY: y}
		}
	case zDivider:
		a.drag = &drag{kind: dragDivider, idx: z.idx, startX: m.x, startY: m.y}
	case zSidebarEdge:
		a.drag = &drag{kind: dragSidebar, startX: m.x}
	case zEditorBar:
		a.editorBarClick(z)
	case zBrowserOpen:
		if p := a.panes[z.id]; p != nil {
			a.openLink(p.Url)
		}
	case zEmpty:
		a.newTab("", "")
	case zEmptyAgent:
		a.openStartAgent(a.currentGroup())
	}
}

func (a *App) rightPress(z zone, m mouseEv) {
	switch z.kind {
	case zTab, zBarTab:
		a.tabMenu(z.id, m.x, m.y)
	case zGroup:
		a.groupMenu(z.id, m.x, m.y)
	case zPane, zPaneHeader, zPaneMenu, zLive, zFloatEdge, zShelf:
		a.setFocus(z.id)
		a.paneMenu(z.id, m.x, m.y)
	case zSidebar, zNewGroup:
		a.openMenu(m.x, m.y, []menuItem{
			{label: "New Group", run: a.newGroup},
			{label: "New Group in Directory…", run: a.openNewGroupInFolder},
			{label: "New Tab", hint: "⌃B c", run: func() { a.newTab("", "") }},
		})
	case zGrant:
		a.grantMenu(z.id, m.x, m.y)
	case zNeeds:
		a.needsMenu(z.id, m.x, m.y)
	}
}

func (a *App) dragMouse(m mouseEv) {
	d := a.drag
	if m.release {
		a.endDrag(m)
		return
	}
	if !m.motion {
		return
	}
	switch d.kind {
	case dragFloat, dragFloatResize:
		a.dragFloatTo(m)
	case dragEditor:
		a.editorDrag(m)
	case dragTab:
		if abs(m.y-d.startY) >= 1 || abs(m.x-d.startX) >= 3 {
			d.moved = true
		}
		d.dropGroup, d.dropIndex = "", 0
		if z, ok := a.zoneAt(m.x, m.y); ok {
			switch z.kind {
			case zTab, zBarTab:
				d.dropGroup, d.dropIndex = z.group, z.idx
			case zGroup:
				d.dropGroup, d.dropIndex = z.id, -1
			}
		}
	case dragDivider:
		if d.idx < len(a.dividers) {
			dv := a.dividers[d.idx]
			p := m.x
			if !dv.horizontal {
				p = m.y
			}
			a.dragDivider(dv, p)
			if t := a.tabs[a.selTab]; t != nil {
				a.sendLayout(t, false)
			}
		}
	case dragSidebar:
		a.prefs.Sidebar = max(18, min(m.x+1, a.w/2))
		a.syncPanes()
	case dragSelect:
		r, ok := a.paneRects[d.id]
		if !ok {
			return
		}
		body := rect{r.x, r.y + 1, r.w, r.h - 1}
		x := max(0, min(m.x-body.x, body.w-1))
		y := max(0, min(m.y-body.y, body.h-1))
		if !d.moved && (x != d.startX || y != d.startY) {
			d.moved = true
		}
		if d.moved {
			a.sel = &selection{pane: d.id, ax: d.startX, ay: d.startY, bx: x, by: y}
		}
		// Dragging past the top or bottom scrolls back through history.
		if pt := a.terms[d.id]; pt != nil {
			if m.y < body.y {
				pt.scroll(-1)
			} else if m.y >= body.y+body.h {
				pt.scroll(1)
			}
		}
	}
}

func (a *App) endDrag(m mouseEv) {
	d := a.drag
	a.drag = nil
	switch d.kind {
	case dragFloat, dragFloatResize:
		a.drag = d // endFloatDrag reads it
		a.endFloatDrag()
		a.drag = nil
	case dragTab:
		if !d.moved {
			a.selectTab(d.id)
			return
		}
		if d.dropGroup == "" {
			return
		}
		idx := d.dropIndex
		if sp := a.group(d.dropGroup); sp != nil && idx < 0 {
			idx = len(sp.TabIds)
		}
		// Optimistically, so the row lands without a flicker.
		if t := a.tabs[d.id]; t != nil {
			if src := a.group(t.GroupId); src != nil {
				src.TabIds = removeString(src.TabIds, d.id)
			}
			if dst := a.group(d.dropGroup); dst != nil {
				idx = max(0, min(idx, len(dst.TabIds)))
				dst.TabIds = append(dst.TabIds[:idx], append([]string{d.id}, dst.TabIds[idx:]...)...)
			}
			t.GroupId = d.dropGroup
		}
		a.moveTab(d.id, d.dropGroup, idx)
	case dragDivider:
		if t := a.tabs[a.selTab]; t != nil {
			a.sendLayout(t, true)
		}
	case dragSidebar:
		a.prefs.save()
	case dragSelect:
		if d.moved && a.sel != nil {
			a.copyText(a.selectionText())
		}
	}
}

func (a *App) button(id string) {
	switch id {
	case "split-right":
		a.split(a.focus, pb.Split_DIRECTION_HORIZONTAL, "")
	case "split-down":
		a.split(a.focus, pb.Split_DIRECTION_VERTICAL, "")
	case "new-tab", "new-tab-bar":
		a.newTab("", "")
	case "palette":
		a.openPalette()
	}
}

func removeString(s []string, v string) []string {
	out := s[:0:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
