package tui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/clipperhouse/uax29/v2/graphemes"

	"github.com/homegrownsw/hammock/internal/agents"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// ---------- session finder ----------
//
// Every Claude Code and Codex session on this Mac, from the daemon's index:
// type to search, Enter resumes (or goes to the pane it's already running
// in), Alt-Enter or ⌃O continues it in the other agent.

const (
	sessionDebounce = 120 * time.Millisecond
	sessionRequery  = 500 * time.Millisecond // while the first index is built
)

type sessionFinder struct {
	group    string // where a resumed session opens, unless its folder has a Group
	computer string // a linked computer whose sessions these are ("" here)
	inGroup  bool   // only the sessions in group's folders (backspace when empty clears it)
	query    string
	sel      int
	open     *spring
	list     []*pb.SessionInfo
	indexing bool
	loaded   bool
	err      string
	gen      int // bumped on every query change; stale replies are dropped
}

func newSessionFinder(group string) *sessionFinder {
	f := &sessionFinder{group: group, open: newSpring(gentle)}
	f.open.from(0)
	f.open.to(1)
	return f
}

// openSessionFinder shows the finder; sessions resume into group (the
// current one if empty).
func (a *App) openSessionFinder(group string) { a.showSessionFinder(group, false) }

// openSessionFinderIn shows only the sessions in the folders open in group
// (its "Resume a Session…"), resumed there unless their folder has a Group.
func (a *App) openSessionFinderIn(group string) { a.showSessionFinder(group, true) }

func (a *App) showSessionFinder(group string, inGroup bool) {
	if group == "" {
		group = a.currentGroup()
	}
	f := newSessionFinder(group)
	// A Group on another of your computers: that computer's sessions.
	if g := a.group(group); g != nil {
		c := a.computerOf(g)
		if c != nil {
			f.computer = c.UserId
		}
		// Yours, here or on another computer: a Group shared with you
		// isn't, and its folders are someone else's.
		f.inGroup = inGroup && (g.HostUserId == "" || c != nil)
	}
	a.overlay = f
	f.fetch(a)
}

// openSessionFinderOn shows linked computer's sessions (resumed there, in
// their folders' Groups).
func (a *App) openSessionFinderOn(computer string) {
	f := newSessionFinder("")
	f.computer = computer
	a.overlay = f
	f.fetch(a)
}

// fetch asks the daemon for the sessions matching the query now.
func (f *sessionFinder) fetch(a *App) {
	f.gen++
	gen, q := f.gen, strings.TrimSpace(f.query)
	req := &pb.ListSessions{Query: q, ComputerId: f.computer}
	if f.inGroup {
		req.GroupId = f.group
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: req}})
		a.actions <- func() {
			if a.overlay != f || gen != f.gen {
				return
			}
			if err != nil {
				f.err = err.Error()
				return
			}
			f.setList(r.GetSessionList())
			if f.indexing {
				time.AfterFunc(sessionRequery, func() {
					a.actions <- func() {
						if a.overlay == f && gen == f.gen {
							f.fetch(a)
						}
					}
				})
			}
		}
	}()
}

// setList takes a reply, keeping the selected session selected.
func (f *sessionFinder) setList(l *pb.SessionList) {
	var selKey string
	if f.sel >= 0 && f.sel < len(f.list) {
		selKey = f.list[f.sel].Harness + ":" + f.list[f.sel].Id
	}
	f.list, f.indexing, f.loaded, f.err = l.GetSessions(), l.GetIndexing(), true, ""
	f.sel = 0
	for i, s := range f.list {
		if s.Harness+":"+s.Id == selKey {
			f.sel = i
		}
	}
}

// queryChanged searches again once typing pauses.
func (f *sessionFinder) queryChanged(a *App) {
	f.gen++
	gen := f.gen
	f.sel = 0
	time.AfterFunc(sessionDebounce, func() {
		a.actions <- func() {
			if a.overlay == f && gen == f.gen {
				f.fetch(a)
			}
		}
	})
}

func (f *sessionFinder) selected() *pb.SessionInfo {
	if f.sel >= 0 && f.sel < len(f.list) {
		return f.list[f.sel]
	}
	return nil
}

func otherHarness(h string) string { return agents.Other(h) }

func harnessName(h string) string {
	if a := agents.Get(h); a != nil {
		return a.Short
	}
	return h
}

func (a *App) harnessGlyph(h string) glyph {
	switch h {
	case "claude":
		return glyph{"✻", a.th.orange, true}
	case "codex":
		return glyph{"◇", a.th.accent, true}
	}
	if ag := agents.Get(h); ag != nil && ag.Short != "" {
		return glyph{strings.ToUpper(string([]rune(ag.Short)[:1])), a.th.accent, true} // another agent Hammock knows: its initial
	}
	return glyph{"›", a.th.dim, false}
}

// resume opens s (in target, when that's the other agent), then shows it.
func (f *sessionFinder) resume(a *App, s *pb.SessionInfo, target string) {
	a.closeOverlay()
	if s == nil {
		return
	}
	group := f.group
	if g := a.group(group); f.computer == "" && g != nil && a.computerOf(g) != nil {
		group = "" // a session here doesn't go into a Group on another computer
	}
	a.resumeSession(s, target, group, f.computer)
}

func (a *App) resumeSession(s *pb.SessionInfo, target, group, computer string) {
	if target == s.Harness {
		target = ""
	}
	if s.OpenPaneId != "" && target == "" && a.panes[s.OpenPaneId] != nil {
		a.reveal(s.OpenPaneId) // already running: no need to ask
		return
	}
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ResumeSession{ResumeSession: &pb.ResumeSession{
		Harness: s.Harness, Id: s.Id, TargetHarness: target, GroupId: group, ComputerId: computer,
	}}}, func(r *pb.ServerMessage) {
		res := r.GetSessionResumed()
		if res == nil {
			return
		}
		if res.AlreadyOpen && a.panes[res.PaneId] != nil {
			a.reveal(res.PaneId)
		} else if res.TabId != "" {
			a.selectTab(res.TabId) // a new tab: its only pane gets the focus
		}
		switch {
		case res.Report != "":
			a.flash(res.Report)
		case res.AlreadyOpen:
			a.flash("Already open")
		}
	})
}

// The finder's chrome around the list: its border, the query line and
// separator, and the footer (separator, two lines of detail and the keys).
const finderChrome = 2 + 2 + 4

// frame grows with the list (from a few rows, so it doesn't jump about as
// results come in) up to 24 lines.
func (f *sessionFinder) frame(a *App) rect {
	w := min(100, a.w-6)
	top := max(1, a.h/8)
	maxH := min(24, a.h-top-2)
	return rect{(a.w - w) / 2, top, w, max(finderChrome+1, min(maxH, finderChrome+max(len(f.list), 6)))}
}

// listRows is how many sessions fit.
func (f *sessionFinder) listRows(a *App) int { return max(1, f.frame(a).h-finderChrome) }

func (f *sessionFinder) top(a *App) int { return max(0, f.sel-f.listRows(a)+1) }

func (f *sessionFinder) draw(a *App) {
	if f.open.step() {
		a.dirty = true
	}
	t := clamp01(f.open.pos)
	a.dimBehind(t)
	r := f.frame(a)
	r.y += int(math.Round((1 - f.open.pos) * -2)) // drops in
	if f.sel >= len(f.list) {
		f.sel = max(0, len(f.list)-1)
	}
	full := r.h
	r.h = max(3, int(math.Round(float64(full)*math.Max(0.4, t))))
	bg := mix(a.th.bg, a.th.menu, t)
	inner := a.box(r, bg, mix(bg, a.th.border, t))
	bottom := inner.y + inner.h
	s := a.scr
	plain := Cell{FG: mix(bg, a.th.fg, t), HasFG: true, BG: bg, HasBG: true}
	dimC := Cell{FG: a.th.dim, HasFG: true, BG: bg, HasBG: true}

	// The query line, with the index's state on the right.
	status := ""
	switch {
	case f.err != "":
		status = "⚠ " + f.err
	case f.indexing:
		status = "Indexing…"
	case f.loaded:
		status = plural(len(f.list), "session")
	}
	sw := 0
	if status != "" {
		sw = min(textWidth(status), inner.w/2)
		sc := dimC
		if f.err != "" {
			sc.FG = a.th.red
		}
		s.TextRight(inner.x+1, inner.y, inner.w-2, truncate(status, sw), sc)
		sw += 2
	}
	s.Text(inner.x+1, inner.y, 2, "›", Cell{FG: a.th.accent, HasFG: true, BG: bg, HasBG: true, Bold: true})
	qw := inner.w - 4 - sw
	if f.query == "" {
		prompt := "Find a Claude or Codex session…"
		if c := a.computer(f.computer); c != nil {
			prompt = "Find a Claude or Codex session on " + c.Name + "…"
		}
		if g := a.group(f.group); f.inGroup && g != nil {
			prompt = "Find a session in " + g.Name + "’s folders… (⌫ for every session)"
		}
		s.Text(inner.x+3, inner.y, qw, prompt, dimC)
		s.Cursor(inner.x+3, inner.y, true)
	} else {
		n := s.Text(inner.x+3, inner.y, qw, f.query, plain)
		s.Cursor(inner.x+3+n, inner.y, true)
	}
	if r.h < full {
		return // still opening
	}
	rule := func(y int) {
		for x := inner.x; x < inner.x+inner.w; x++ {
			s.Set(x, y, Cell{Ch: "─", FG: a.th.border, HasFG: true, BG: bg, HasBG: true})
		}
	}
	rule(inner.y + 1)

	// The list.
	rows := f.listRows(a)
	top := f.top(a)
	now := time.Now()
	if len(f.list) == 0 {
		msg := "Looking…"
		switch {
		case f.loaded && f.indexing:
			msg = "Indexing your sessions…"
		case f.loaded && f.inGroup && f.query != "":
			msg = "No sessions in this Group’s folders match “" + strings.TrimSpace(f.query) + "”"
		case f.loaded && f.inGroup:
			msg = "No sessions in this Group’s folders (⌫ for every session)"
		case f.loaded && f.query != "":
			msg = "No sessions match “" + strings.TrimSpace(f.query) + "”"
		case f.loaded:
			msg = "No Claude Code or Codex sessions yet"
		}
		s.Text(inner.x+3, inner.y+2, inner.w-4, truncate(msg, inner.w-4), dimC)
	}
	// Columns, right to left: messages, when, folder, open.
	const msgW, timeW, openW = 9, 13, 7
	cwdW := min(30, max(12, inner.w/4))
	if inner.w < 70 {
		cwdW = 0
	}
	for i := 0; i < rows && top+i < len(f.list); i++ {
		it := f.list[top+i]
		y := inner.y + 2 + i
		if y >= bottom {
			break
		}
		sel := top+i == f.sel
		rowBG := bg
		if sel {
			rowBG = mix(bg, a.th.menuSel, 0.85)
		}
		s.Paint(inner.x, y, inner.w, 1, rowBG)
		fg, dim := plain.FG, a.th.dim
		if sel {
			fg, dim = RGB{255, 255, 255}, mix(RGB{255, 255, 255}, rowBG, 0.35)
		}
		g := a.harnessGlyph(it.Harness)
		if sel {
			g.col = mix(g.col, RGB{255, 255, 255}, 0.4)
		}
		s.Text(inner.x+1, y, 1, g.ch, Cell{FG: g.col, HasFG: true, BG: rowBG, HasBG: true, Bold: g.bold})
		dc := Cell{FG: dim, HasFG: true, BG: rowBG, HasBG: true}
		x := inner.x + inner.w - 1
		col := func(w int, text string, c Cell) {
			x -= w
			s.TextRight(x, y, w-1, truncate(text, w-1), c)
		}
		col(msgW, plural(int(it.Messages), "msg"), dc)
		col(timeW, relTime(it.UpdatedUnixMs, now), dc)
		if cwdW > 0 {
			x -= cwdW
			s.Text(x+1, y, cwdW-1, truncLeft(shortPath(it.Cwd), cwdW-1), dc)
		}
		if it.OpenPaneId != "" {
			oc := dc
			oc.FG = a.th.green
			if sel {
				oc.FG = RGB{255, 255, 255}
			}
			col(openW, "● open", oc)
		} else {
			x -= openW
		}
		room := x - (inner.x + 3)
		title := oneLine(it.Title)
		if title == "" {
			title = "Untitled"
		}
		s.Text(inner.x+3, y, room, truncate(title, room), Cell{FG: fg, HasFG: true, BG: rowBG, HasBG: true, Bold: sel})
	}

	// The footer: the selected session's match, or its last prompt, and the keys.
	fy := bottom - 4
	if fy <= inner.y+1 {
		return
	}
	rule(fy)
	if it := f.selected(); it != nil {
		text, hi := it.Snippet, true
		if text == "" {
			text, hi = it.LastPrompt, false
			if text == "" {
				text = it.FirstPrompt
			}
		}
		a.drawSnippet(rect{inner.x + 1, fy + 1, inner.w - 2, 2}, oneLine(text), hi, bg)
	}
	hints := "↵ Resume   esc Close"
	if it := f.selected(); it != nil {
		cont := ""
		if o := otherHarness(it.Harness); o != "" {
			cont = "   ⌥↵ / ⌃O Continue in " + harnessName(o)
		}
		hints = "↵ Resume in " + harnessName(it.Harness) + cont + "   esc Close"
		if it.OpenPaneId != "" {
			hints = "↵ Go to Pane" + cont + "   esc Close"
		}
	}
	s.Text(inner.x+1, fy+3, inner.w-2, truncate(hints, inner.w-2), Cell{FG: a.th.faint, HasFG: true, BG: bg, HasBG: true})
}

// drawSnippet writes text into r, wrapping, with the parts in «» (a
// search match) highlighted when hi is set; ends with … if it doesn't fit.
func (a *App) drawSnippet(r rect, text string, hi bool, bg RGB) {
	normal := Cell{FG: a.th.dim, HasFG: true, BG: bg, HasBG: true}
	mark := Cell{FG: a.th.fg, HasFG: true, BG: mix(bg, a.th.amber, 0.3), HasBG: true, Bold: true}
	x, y := r.x, r.y
	on := false
	g := graphemes.FromString(text)
	for g.Next() {
		ch := g.Value()
		if hi && (ch == "«" || ch == "»") {
			on = ch == "«"
			continue
		}
		w := textWidth(ch)
		if w == 0 {
			continue
		}
		if x+w > r.x+r.w {
			x, y = r.x, y+1
		}
		if y >= r.y+r.h {
			// Out of room: an ellipsis over the last cell.
			a.scr.Set(r.x+r.w-1, r.y+r.h-1, withCh(normal, "…"))
			return
		}
		c := normal
		if on {
			c = mark
		}
		a.scr.Text(x, y, w, ch, c)
		x += w
	}
}

func (f *sessionFinder) key(a *App, ks []key) {
	for _, k := range ks {
		switch k.name {
		case "esc":
			a.closeOverlay()
			return
		case "up", "shift-tab", "ctrl-p":
			f.sel = max(0, f.sel-1)
		case "down", "tab", "ctrl-n":
			f.sel = max(0, min(len(f.list)-1, f.sel+1))
		case "enter":
			if it := f.selected(); it != nil {
				f.resume(a, it, it.Harness)
			}
			return
		case "alt-enter", "ctrl-o":
			if it := f.selected(); it != nil && otherHarness(it.Harness) != "" {
				f.resume(a, it, otherHarness(it.Harness))
			}
			return
		case "backspace":
			if r := []rune(f.query); len(r) > 0 {
				f.query = string(r[:len(r)-1])
				f.queryChanged(a)
			} else if f.inGroup {
				f.inGroup = false // every session
				f.queryChanged(a)
			}
		case "ctrl-u":
			if f.query != "" {
				f.query = ""
				f.queryChanged(a)
			}
		case "":
			f.query += string(k.r)
			f.queryChanged(a)
		}
	}
}

func (f *sessionFinder) mouse(a *App, m mouseEv) {
	r := f.frame(a)
	inner := rect{r.x + 1, r.y + 1, r.w - 2, r.h - 2}
	if m.wheel != 0 {
		f.sel = max(0, min(len(f.list)-1, f.sel+m.wheel))
		return
	}
	if inner.contains(m.x, m.y) && m.y >= inner.y+2 && m.y < inner.y+2+f.listRows(a) {
		i := f.top(a) + m.y - inner.y - 2
		if i < len(f.list) {
			if m.motion || m.press {
				f.sel = i
			}
			if m.press && m.button == 0 {
				target := f.list[i].Harness
				if m.alt {
					target = otherHarness(target)
				}
				if target != "" {
					f.resume(a, f.list[i], target)
				}
			}
		}
		return
	}
	if m.press && !r.contains(m.x, m.y) {
		a.closeOverlay()
	}
}

// relTime is how long ago ms was, as people say it: "just now",
// "12 min ago", "3 hours ago", "yesterday", "4 days ago", "12 Mar".
func relTime(ms int64, now time.Time) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(ms).In(now.Location())
	d := now.Sub(t)
	day := func(t time.Time) time.Time {
		y, m, dd := t.Date()
		return time.Date(y, m, dd, 0, 0, 0, 0, t.Location())
	}
	days := int(math.Round(day(now).Sub(day(t)).Hours() / 24))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case days == 0:
		if h := int(d / time.Hour); h > 1 {
			return fmt.Sprintf("%d hours ago", h)
		}
		return "1 hour ago"
	case days == 1:
		return "yesterday"
	case days < 7:
		return fmt.Sprintf("%d days ago", days)
	case t.Year() == now.Year():
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

// truncLeft keeps the end of s (a path's most telling part) within w cells.
func truncLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if textWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && textWidth(string(r))+1 > w {
		r = r[1:]
	}
	return "…" + string(r)
}

// oneLine folds runs of whitespace (newlines too) into single spaces.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
