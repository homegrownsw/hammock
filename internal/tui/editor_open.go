package tui

// Quick Open (⌃B e): find a file in the Group's folder by typing part of
// its name (the daemon ranks them, ListFiles) and open it in a new tab;
// and the diff of an editor's buffer against the file on disk.

import (
	"fmt"
	"math"
	"slices"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/textbuf"
)

type quickOpen struct {
	group   string
	query   string
	folder  string
	paths   []string
	err     string
	sel     int
	asked   int // the newest ListFiles sent; older answers are dropped
	shown   int // the one paths came from
	open    *spring
	more    bool // the folder has more files than were looked at
	results []quickRow
}

// quickRow is a row: a file, or the path as typed.
type quickRow struct {
	path  string
	typed bool
}

func (a *App) openQuickOpen() {
	g := a.currentGroup()
	if g == "" {
		a.flash("Open a Group first")
		return
	}
	q := &quickOpen{group: g, open: newSpring(gentle)}
	q.open.from(0)
	q.open.to(1)
	a.overlay = q
	q.ask(a)
}

// ask lists the files matching the query, off the main loop.
func (q *quickOpen) ask(a *App) {
	q.asked++
	n, query := q.asked, q.query
	a.callErr(&pb.ClientMessage{Msg: &pb.ClientMessage_ListFiles{ListFiles: &pb.ListFiles{GroupId: q.group, Query: query, Limit: 100}}}, func(r *pb.ServerMessage, err error) {
		if n < q.shown {
			return // a newer answer is already showing
		}
		q.shown = n
		if err != nil {
			q.err, q.paths = err.Error(), nil
			return
		}
		fl := r.GetFileList()
		q.err, q.folder, q.paths, q.more = "", fl.GetFolder(), fl.GetPaths(), fl.GetTruncated()
	})
}

// rows are the files, then (when it isn't one of them) the path as typed,
// which opens even if it doesn't exist yet: saving creates it.
func (q *quickOpen) rows() []quickRow {
	out := make([]quickRow, 0, len(q.paths)+1)
	for _, p := range q.paths {
		out = append(out, quickRow{path: p})
	}
	typed := strings.TrimSpace(q.query)
	if typed != "" && !slices.Contains(q.paths, typed) {
		out = append(out, quickRow{path: typed, typed: true})
	}
	return out
}

func (q *quickOpen) frame(a *App) rect {
	w := min(76, a.w-6)
	return rect{(a.w - w) / 2, max(1, a.h/6), w, min(18, a.h-a.h/6-2)}
}

func (q *quickOpen) draw(a *App) {
	if q.open.step() {
		a.dirty = true
	}
	t := clamp01(q.open.pos)
	a.dimBehind(t)
	r := q.frame(a)
	r.y += int(math.Round((1 - q.open.pos) * -2))
	q.results = q.rows()
	visible := r.h - 5
	q.sel = min(q.sel, max(len(q.results)-1, 0))
	bg := mix(a.th.bg, a.th.menu, t)
	inner := a.box(r, bg, mix(bg, a.th.border, t))
	s := a.scr
	s.Text(inner.x+1, inner.y, 2, "›", Cell{FG: a.th.accent, HasFG: true, BG: bg, HasBG: true, Bold: true})
	if q.query == "" {
		s.Text(inner.x+3, inner.y, inner.w-4, "Open a file in this Group's folder…", Cell{FG: a.th.dim, HasFG: true, BG: bg, HasBG: true})
		s.Cursor(inner.x+3, inner.y, true)
	} else {
		n := s.Text(inner.x+3, inner.y, inner.w-4, q.query, Cell{FG: a.th.fg, HasFG: true, BG: bg, HasBG: true})
		s.Cursor(inner.x+3+n, inner.y, true)
	}
	for x := inner.x; x < inner.x+inner.w; x++ {
		s.Set(x, inner.y+1, Cell{Ch: "─", FG: a.th.border, HasFG: true, BG: bg, HasBG: true})
	}
	foot := shortPath(q.folder)
	if q.more {
		foot += " · not every file was looked at"
	}
	if q.err != "" {
		foot = "⚠ " + q.err
	}
	s.Text(inner.x+1, inner.y+inner.h-1, inner.w-2, truncate(foot, inner.w-2), Cell{FG: a.th.dim, HasFG: true, BG: bg, HasBG: true})
	top := max(0, q.sel-visible+1)
	for i := 0; i < visible && top+i < len(q.results); i++ {
		row := q.results[top+i]
		y := inner.y + 2 + i
		sel := top+i == q.sel
		rowBG, fg, dim := bg, a.th.fg, a.th.dim
		if sel {
			rowBG = mix(bg, a.th.menuSel, 0.85)
			fg, dim = RGB{255, 255, 255}, mix(RGB{255, 255, 255}, rowBG, 0.35)
		}
		s.Paint(inner.x, y, inner.w, 1, rowBG)
		if row.typed {
			s.Text(inner.x+1, y, inner.w-2, truncate("Open “"+row.path+"”", inner.w-2), Cell{FG: fg, HasFG: true, BG: rowBG, HasBG: true, Italic: true})
			continue
		}
		// The name, then the folder it's in.
		dir, name := "", row.path
		if i := strings.LastIndex(row.path, "/"); i >= 0 {
			dir, name = row.path[:i], row.path[i+1:]
		}
		n := s.Text(inner.x+1, y, inner.w-2, truncate(name, inner.w-2), Cell{FG: fg, HasFG: true, BG: rowBG, HasBG: true, Bold: sel})
		if dir != "" && inner.w-2-n > 6 {
			s.Text(inner.x+1+n+2, y, inner.w-4-n, truncate(dir, inner.w-4-n), Cell{FG: dim, HasFG: true, BG: rowBG, HasBG: true})
		}
	}
}

func (q *quickOpen) key(a *App, ks []key) {
	changed := false
	for _, k := range ks {
		switch k.name {
		case "esc":
			a.closeOverlay()
			return
		case "up", "shift-tab", "ctrl-p":
			q.sel = max(0, q.sel-1)
		case "down", "tab", "ctrl-n":
			q.sel = min(len(q.results)-1, q.sel+1)
		case "enter":
			q.choose(a)
			return
		case "backspace":
			if r := []rune(q.query); len(r) > 0 {
				q.query, q.sel, changed = string(r[:len(r)-1]), 0, true
			}
		case "ctrl-u":
			q.query, q.sel, changed = "", 0, true
		case "":
			q.query, q.sel, changed = q.query+string(k.r), 0, true
		}
	}
	if changed {
		q.ask(a)
	}
}

func (q *quickOpen) choose(a *App) {
	rows := q.rows()
	if q.sel < 0 || q.sel >= len(rows) {
		return
	}
	a.closeOverlay()
	a.openEditor(q.group, expandTilde(rows[q.sel].path), false)
}

func (q *quickOpen) mouse(a *App, m mouseEv) {
	r := q.frame(a)
	inner := rect{r.x + 1, r.y + 1, r.w - 2, r.h - 2}
	visible := r.h - 5
	top := max(0, q.sel-visible+1)
	if m.wheel != 0 {
		q.sel = max(0, min(len(q.results)-1, q.sel+m.wheel))
		return
	}
	if inner.contains(m.x, m.y) && m.y >= inner.y+2 && m.y < inner.y+2+visible {
		if i := top + m.y - inner.y - 2; i < len(q.results) {
			if m.motion || m.press {
				q.sel = i
			}
			if m.press && m.button == 0 {
				q.choose(a)
			}
		}
		return
	}
	if m.press && !r.contains(m.x, m.y) {
		a.closeOverlay()
	}
}

// ---- the diff against the disk ----

// diffLine is a line of a unified diff: ' ' the same, '-' only on disk,
// '+' only in the buffer, '@' a hunk's heading.
type diffLine struct {
	kind byte
	text string
}

// unifiedDiff compares disk with the buffer's text, with ctx lines of
// context round each change.
func unifiedDiff(disk, mine string, ctx int) []diffLine {
	a, b := textbuf.Lines(textbuf.FromString(disk)), textbuf.Lines(textbuf.FromString(mine))
	changes := textbuf.DiffLines(a, b)
	var out []diffLine
	for i := 0; i < len(changes); {
		// Changes closer than twice the context share a hunk.
		j := i
		for j+1 < len(changes) && changes[j+1].OldFrom-changes[j].OldTo <= 2*ctx {
			j++
		}
		from, to := max(changes[i].OldFrom-ctx, 0), min(changes[j].OldTo+ctx, len(a))
		nfrom := changes[i].NewFrom - (changes[i].OldFrom - from)
		out = append(out, diffLine{'@', fmt.Sprintf("Line %d (disk) / %d (yours)", from+1, nfrom+1)})
		old := from
		for k := i; k <= j; k++ {
			c := changes[k]
			for ; old < c.OldFrom; old++ {
				out = append(out, diffLine{' ', a[old]})
			}
			for _, l := range a[c.OldFrom:c.OldTo] {
				out = append(out, diffLine{'-', l})
			}
			for _, l := range b[c.NewFrom:c.NewTo] {
				out = append(out, diffLine{'+', l})
			}
			old = c.OldTo
		}
		for ; old < to; old++ {
			out = append(out, diffLine{' ', a[old]})
		}
		i = j + 1
	}
	return out
}

type diffView struct {
	pane  string
	name  string
	lines []diffLine
	top   int
}

// editorDiff shows how the buffer differs from the file on disk.
func (a *App) editorDiff(id string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReadEditor{ReadEditor: &pb.ReadEditor{PaneId: id, Disk: true}}}, func(r *pb.ServerMessage) {
		u := r.GetEditorUpdate()
		if u == nil {
			return
		}
		lines := unifiedDiff(u.DiskText, u.Text, 3)
		if u.DiskMissing {
			lines = []diffLine{{'@', "The file isn't on disk any more: saving puts it back."}}
		} else if len(lines) == 0 {
			lines = []diffLine{{'@', "No differences."}}
		}
		a.overlay = &diffView{pane: id, name: a.panes[id].GetEditor().GetName(), lines: lines}
	})
}

func (v *diffView) frame(a *App) rect {
	w, h := min(110, a.w-4), max(a.h-4, 6)
	return rect{(a.w - w) / 2, 2, w, h}
}

func (v *diffView) draw(a *App) {
	a.dimBehind(1)
	th, s := a.th, a.scr
	r := v.frame(a)
	inner := a.box(r, th.menu, th.border)
	s.Text(inner.x+1, inner.y, inner.w-2, truncate(v.name+": on disk (−) and yours (+)", inner.w-2), Cell{FG: th.fg, HasFG: true, BG: th.menu, HasBG: true, Bold: true})
	rows := inner.h - 3
	v.top = max(0, min(v.top, len(v.lines)-rows))
	for i := 0; i < rows && v.top+i < len(v.lines); i++ {
		l := v.lines[v.top+i]
		y := inner.y + 2 + i
		c := Cell{FG: th.fg, HasFG: true, BG: th.menu, HasBG: true}
		switch l.kind {
		case '-':
			c.BG = mix(th.menu, th.red, 0.18)
		case '+':
			c.BG = mix(th.menu, th.green, 0.18)
		case '@':
			c.FG = th.dim
		}
		s.Fill(inner.x, y, inner.w, 1, Cell{BG: c.BG, HasBG: true})
		text := string(l.kind) + " " + strings.ReplaceAll(l.text, "\t", "    ")
		if l.kind == '@' {
			text = l.text
		}
		s.Text(inner.x+1, y, inner.w-2, truncate(text, inner.w-2), c)
	}
	hint := "↑↓ scroll   r reload from disk   k keep mine   esc close"
	s.Text(inner.x+1, inner.y+inner.h-1, inner.w-2, truncate(hint, inner.w-2), Cell{FG: th.dim, HasFG: true, BG: th.menu, HasBG: true})
}

func (v *diffView) key(a *App, ks []key) {
	rows := v.frame(a).h - 5
	for _, k := range ks {
		switch {
		case k.name == "esc" || k.r == 'q':
			a.closeOverlay()
			return
		case k.name == "up":
			v.top--
		case k.name == "down":
			v.top++
		case k.name == "pgup":
			v.top -= rows
		case k.name == "pgdown" || k.r == ' ':
			v.top += rows
		case k.r == 'r':
			a.closeOverlay()
			a.editorReload(v.pane, true)
			return
		case k.r == 'k':
			a.closeOverlay()
			a.editorCommand(v.pane, pb.EditorCommand_ACTION_KEEP_MINE)
			return
		}
	}
	v.top = max(v.top, 0)
}

func (v *diffView) mouse(a *App, m mouseEv) {
	if m.wheel != 0 {
		v.top = max(v.top+3*m.wheel, 0)
		return
	}
	if m.press && !v.frame(a).contains(m.x, m.y) {
		a.closeOverlay()
	}
}
