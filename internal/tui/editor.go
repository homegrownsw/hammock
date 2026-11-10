package tui

// Editor panes (PANE_KIND_EDITOR): a text file whose buffer is the
// daemon's, shared with every other client showing it. The TUI attaches
// the panes on screen, keeps each one's text in an editDoc (editbuf.go),
// pushes its edits and rebases them over everyone else's, and draws the
// text itself: line numbers, git marks, the cursor and selection, no
// syntax colours.

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/textbuf"
)

// editorPrefs are the editor-* keys from the config file (config.go).
var editorPrefs = hammockconfig.EditorSettings(nil)

// editorView is an editor pane on screen.
type editorView struct {
	id     string
	doc    *editDoc
	loaded bool // the snapshot has arrived
	top    int  // the first line shown
	topSub int  // and its first row shown (wrapped)
	left   int  // cells scrolled sideways (without wrap)
	marks  []*pb.GitMark
	find   *editorFind
	reveal int // scroll the cursor into view at the next draw: 1, or 2 to centre it

	selAt   time.Time // when the selection last changed, to report it once it settles
	selSent [3]int    // what was last reported: version, anchor, head
	save    *pendingSave
	onText  func(applied []textbuf.ChangeSet) // someone else changed the text (external.go)

	// Where it was last drawn, for the mouse: the screen row of each line
	// and wrapped row shown, and where the text starts.
	shown []shownRow
	textX int
	textY int
	textW int
}

type shownRow struct{ line, sub int }

// pendingSave is a save waiting for this pane's edits to reach the daemon.
type pendingSave struct {
	path  string
	force bool
}

// editorFind is the find bar: what's looked for, and where it started.
type editorFind struct {
	query  string
	origin int
	miss   bool
}

func isEditor(p *pb.Pane) bool { return p != nil && p.Kind == pb.PaneKind_PANE_KIND_EDITOR }

// isLive reports whether a pane is drawn from screenshots (live.go).
func isLive(p *pb.Pane) bool {
	return p != nil && (p.Kind == pb.PaneKind_PANE_KIND_BROWSER || p.Kind == pb.PaneKind_PANE_KIND_SIMULATOR)
}

// canDrive reports whether you may change what's in group: your own, or
// shared with you to drive (a viewer only watches).
func canDrive(g *pb.Group) bool {
	return g == nil || g.HostUserId == "" || g.MyRole == pb.Role_ROLE_DRIVER || g.MyRole == pb.Role_ROLE_OWNER || g.MyRole == pb.Role_ROLE_HOST
}

func (a *App) paneGroup(id string) *pb.Group {
	if p := a.panes[id]; p != nil {
		if t := a.tabs[p.TabId]; t != nil {
			return a.group(t.GroupId)
		}
	}
	return nil
}

// focusedEditor is the focused pane's editor, if it's one.
func (a *App) focusedEditor() *editorView {
	if p := a.panes[a.focus]; isEditor(p) && a.editors != nil {
		return a.editors[a.focus]
	}
	return nil
}

// editorIndent is how a pane indents: its file's way (.editorconfig, or
// what it already uses), else the settings.
func (a *App) editorIndent(id string) (width int, tabs bool) {
	width, tabs = editorPrefs.TabWidth, editorPrefs.Indent == "tabs"
	if ed := a.panes[id].GetEditor(); ed != nil {
		if ed.TabWidth > 0 {
			width = int(ed.TabWidth)
		}
		if ed.Indent != "" {
			tabs = ed.Indent == "tabs"
		}
	}
	return max(width, 1), tabs
}

// ---- attaching ----

// syncEditors attaches the editor panes on screen and lets go of the rest.
func (a *App) syncEditors(rects map[string]rect) {
	if a.editors == nil {
		a.editors = map[string]*editorView{}
	}
	ctx := context.Background()
	for id := range a.editors {
		if _, on := rects[id]; !on || !isEditor(a.panes[id]) {
			delete(a.editors, id)
			_ = a.c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_DetachPane{DetachPane: &pb.DetachPane{PaneId: id}}})
		}
	}
	for id := range rects {
		if !isEditor(a.panes[id]) || a.editors[id] != nil {
			continue
		}
		if t := a.terms[id]; t != nil {
			// Shown before its info arrived, as a terminal.
			t.close()
			delete(a.terms, id)
			delete(a.sizes, id)
		}
		a.editors[id] = &editorView{id: id, doc: newEditDoc()}
		_ = a.c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: id}}})
	}
}

// editorUpdate takes an EditorUpdate for a pane on screen.
func (a *App) editorUpdate(u *pb.EditorUpdate) {
	v := a.editors[u.PaneId]
	if v == nil {
		return
	}
	d := v.doc
	switch {
	case u.Snapshot:
		d.reset(u.Text, u.Version)
		v.loaded = true
		v.marks = u.Git
		if v.onText != nil {
			v.onText(nil)
		}
	case len(u.Updates) > 0:
		if !v.loaded {
			return
		}
		applied, err := d.receiveAt(u.Version, u.Updates)
		if err != nil {
			a.flash("⚠ " + err.Error())
			a.editorResync(v)
			return
		}
		if len(applied) > 0 && v.onText != nil {
			v.onText(applied)
		}
	case u.Selection == nil:
		v.marks = u.Git // the git marks changed (all of them come)
	}
	if s := u.Selection; s != nil && v.loaded {
		d.anchor, d.head = d.fromServer(int(s.Anchor)), d.fromServer(int(s.Head))
		v.reveal = 2
	}
	a.editorPush(v)
	a.editorSaveIfDue(v)
	a.dirty = true
}

// editorResync starts a pane again from the daemon's text, when its own
// went astray.
func (a *App) editorResync(v *editorView) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ReadEditor{ReadEditor: &pb.ReadEditor{PaneId: v.id}}}, func(r *pb.ServerMessage) {
		if u := r.GetEditorUpdate(); u != nil && a.editors[v.id] == v {
			v.doc.reset(u.Text, u.Version)
			v.loaded = true
		}
	})
}

// editorPush sends the pane's unconfirmed edits, if it may now.
func (a *App) editorPush(v *editorView) {
	m := v.doc.toPush()
	if m == nil {
		return
	}
	m.PaneId = v.id
	at := m.Version
	a.callErr(&pb.ClientMessage{Msg: &pb.ClientMessage_EditorPush{EditorPush: m}}, func(r *pb.ServerMessage, err error) {
		if err != nil {
			v.doc.failed()
			a.flash("⚠ " + err.Error())
			a.editorResync(v)
			return
		}
		v.doc.pushed(r.GetEditorPushReply().GetAccepted(), at)
		a.editorPush(v) // refused: again, if what beat it has already arrived
	})
}

// callErr is call, with what went wrong given to then rather than shown.
func (a *App) callErr(m *pb.ClientMessage, then func(*pb.ServerMessage, error)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := a.c.Call(ctx, m)
		a.actions <- func() { then(r, err) }
	}()
}

// editorTick reports settled selections (agents read them; the pane opens
// there next time), once the text they're in has reached the daemon.
func (a *App) editorTick() {
	for _, v := range a.editors {
		d := v.doc
		if v.selAt.IsZero() || time.Since(v.selAt) < 300*time.Millisecond || !d.confirmed() {
			continue
		}
		v.selAt = time.Time{}
		cur := [3]int{int(d.version), d.anchor, d.head}
		if cur == v.selSent {
			continue
		}
		v.selSent = cur
		_ = a.c.Send(bgctx, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorSelect{EditorSelect: &pb.EditorSelect{
			PaneId: v.id, Version: d.version, Anchor: uint32(d.anchor), Head: uint32(d.head),
		}}})
	}
}

// ---- keys ----

// edKey is a key as the editor reads it: modifiers apart.
type edKey struct {
	name             string // "up", "enter", "a" for ⌃A…; "" for a character
	r                rune
	shift, alt, ctrl bool
}

// editorKeysOf decodes typed bytes, with the modifiers terminals send on
// arrows and the like (xterm's CSI 1;m X, CSI-u and modifyOtherKeys), and
// Alt as ESC before a key.
func editorKeysOf(b []byte) []edKey {
	var out []edKey
	for len(b) > 0 {
		c := b[0]
		if c == 0x1b {
			if len(b) == 1 {
				out = append(out, edKey{name: "esc"})
				b = b[1:]
				continue
			}
			if b[1] == '[' || b[1] == 'O' {
				k, n := parseCSIKey(b)
				if k.name != "" || k.r != 0 {
					out = append(out, k)
				}
				b = b[n:]
				continue
			}
			// ESC then a key: that key with Alt (⌥ as Meta).
			ks := editorKeysOf(b[1:2])
			n := 2
			if b[1] >= 0x80 {
				_, size := utf8.DecodeRune(b[1:])
				ks, n = editorKeysOf(b[1:1+size]), 1+size
			}
			for _, k := range ks {
				k.alt = true
				switch {
				case k.r == 'b':
					k = edKey{name: "left", alt: true}
				case k.r == 'f':
					k = edKey{name: "right", alt: true}
				}
				out = append(out, k)
			}
			b = b[n:]
			continue
		}
		switch {
		case c == '\r' || c == '\n':
			out = append(out, edKey{name: "enter"})
		case c == '\t':
			out = append(out, edKey{name: "tab"})
		case c == 0x7f || c == 0x08:
			out = append(out, edKey{name: "backspace"})
		case c == 0:
			out = append(out, edKey{name: "space", ctrl: true})
		case c < 0x20:
			out = append(out, edKey{name: string(rune('a' + c - 1)), ctrl: true})
		default:
			r, n := utf8.DecodeRune(b)
			out = append(out, edKey{r: r})
			b = b[n:]
			continue
		}
		b = b[1:]
	}
	return out
}

// parseCSIKey reads one CSI or SS3 key sequence at the start of b, and how
// many bytes it took.
func parseCSIKey(b []byte) (edKey, int) {
	i := 2
	for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
		i++
	}
	if i >= len(b) {
		return edKey{name: "esc"}, 1 // cut off: let the rest come as typed
	}
	params, final := string(b[2:i]), b[i]
	n := i + 1
	var nums []int
	for _, p := range strings.Split(params, ";") {
		v, _ := strconv.Atoi(strings.SplitN(p, ":", 2)[0])
		nums = append(nums, v)
	}
	mod := 0
	if len(nums) >= 2 && nums[1] > 0 {
		mod = nums[1] - 1
	}
	k := edKey{shift: mod&1 != 0, alt: mod&2 != 0, ctrl: mod&4 != 0}
	switch final {
	case 'A':
		k.name = "up"
	case 'B':
		k.name = "down"
	case 'C':
		k.name = "right"
	case 'D':
		k.name = "left"
	case 'H':
		k.name = "home"
	case 'F':
		k.name = "end"
	case 'Z':
		k.name, k.shift = "tab", true
	case '~':
		switch nums[0] {
		case 1, 7:
			k.name = "home"
		case 4, 8:
			k.name = "end"
		case 3:
			k.name = "delete"
		case 5:
			k.name = "pgup"
		case 6:
			k.name = "pgdown"
		case 27: // modifyOtherKeys: 27;mod;code~
			if len(nums) >= 3 {
				return codeKey(rune(nums[2]), k), n
			}
		}
	case 'u': // CSI-u (kitty's keyboard protocol): code;mod u
		return codeKey(rune(nums[0]), k), n
	}
	return k, n
}

// codeKey is the key a code point names, with mods.
func codeKey(r rune, k edKey) edKey {
	switch {
	case r == 13:
		k.name = "enter"
	case r == 9:
		k.name = "tab"
	case r == 127 || r == 8:
		k.name = "backspace"
	case r == 27:
		k.name = "esc"
	case k.ctrl && r >= 'a' && r <= 'z':
		k.name = string(r)
	case k.ctrl && r >= 'A' && r <= 'Z':
		k.name, k.shift = string(r+'a'-'A'), true
	default:
		k.r = r
	}
	return k
}

// editorInput takes typed bytes for the focused editor pane.
func (a *App) editorInput(v *editorView, b []byte) {
	for _, k := range editorKeysOf(b) {
		a.editorKey(v, k)
	}
}

// editorPaste types a paste into the pane.
func (a *App) editorPaste(v *editorView, data []byte) {
	if a.editable(v, true) && v.doc.insert(string(data)) {
		a.editorChanged(v)
	}
}

// editable reports whether you may change the pane (say so if not, and
// tell).
func (a *App) editable(v *editorView, tell bool) bool {
	if !v.loaded {
		return false
	}
	if !canDrive(a.paneGroup(v.id)) {
		if tell {
			a.flash("View only: you can't change this Group's files")
		}
		return false
	}
	if p := a.panes[v.id]; p != nil && p.GetEditor().GetReadOnly() {
		if tell {
			a.flash("Read-only: you don't have permission to change this file (Save As makes a copy)")
		}
		return false
	}
	return true
}

// editorChanged follows an edit: pushed, and the cursor kept in view.
func (a *App) editorChanged(v *editorView) {
	a.editorMoved(v)
	a.editorPush(v)
}

// editorMoved follows the cursor moving: in view, and reported soon.
func (a *App) editorMoved(v *editorView) {
	v.reveal = max(v.reveal, 1)
	v.selAt = time.Now()
	a.dirty = true
}

func (a *App) editorKey(v *editorView, k edKey) {
	if !v.loaded {
		return
	}
	d := v.doc
	if v.find != nil && a.findKey(v, k) {
		return
	}
	width, tabs := a.editorIndent(v.id)
	rows := max(a.paneRects[v.id].h-2, 1)
	edit := func(f func() bool) {
		if a.editable(v, true) && f() {
			a.editorChanged(v)
		}
	}
	move := func(f func()) {
		f()
		a.editorMoved(v)
	}
	wrap := editorPrefs.Wrap
	tw := v.textW
	if k.name == "" {
		if k.alt && !k.ctrl {
			switch k.r {
			case 'r', 'R':
				a.editorReload(v.id, true)
				return
			case 'k', 'K':
				a.editorCommand(v.id, pb.EditorCommand_ACTION_KEEP_MINE)
				return
			case 'd', 'D':
				a.editorDiff(v.id)
				return
			}
		}
		if k.ctrl || k.alt {
			return
		}
		edit(func() bool { return d.insert(string(k.r)) })
		return
	}
	if k.ctrl && !k.alt {
		switch k.name {
		case "a":
			move(func() { d.lineStart(false, false) })
		case "e":
			move(func() { d.lineEnd(false) })
		case "z":
			if k.shift {
				edit(d.redoLast)
			} else {
				edit(d.undoLast)
			}
		case "y":
			edit(d.redoLast)
		case "s":
			a.editorSave(v.id, "", false)
		case "f":
			v.find = &editorFind{origin: d.head}
			if d.hasSel() && !strings.Contains(d.selText(), "\n") {
				v.find.query = d.selText()
				f, _ := d.selRange()
				v.find.origin = f
			}
		case "g":
			a.editorGoToLine(v.id)
		case "c":
			if d.hasSel() {
				a.copyText(d.selText())
			}
		case "x":
			if d.hasSel() {
				a.copyText(d.selText())
				edit(d.backspace)
			}
		case "d":
			edit(d.deleteForward)
		case "k":
			// To the end of the line, or the line break at its end.
			edit(func() bool {
				_, le := d.lineRange(d.lineOf(d.head))
				if le == d.head {
					return d.deleteForward()
				}
				d.moveTo(le, true)
				return d.backspace()
			})
		case "home":
			move(func() { d.moveTo(0, k.shift) })
		case "end":
			move(func() { d.moveTo(len(d.text), k.shift) })
		case "left":
			move(func() { d.wordLeft(k.shift) })
		case "right":
			move(func() { d.wordRight(k.shift) })
		case "up", "down":
			n := 1
			if k.name == "up" {
				n = -1
			}
			a.editorScroll(v, n)
		}
		return
	}
	switch k.name {
	case "enter":
		edit(d.newline)
	case "tab":
		if k.shift {
			edit(func() bool { return d.outdent(width) })
		} else {
			edit(func() bool { return d.tab(tabs, width) })
		}
	case "backspace":
		if k.alt {
			edit(d.deleteWordBack)
		} else {
			edit(d.backspace)
		}
	case "delete":
		edit(d.deleteForward)
	case "left":
		if k.alt {
			move(func() { d.wordLeft(k.shift) })
		} else {
			move(func() { d.left(k.shift) })
		}
	case "right":
		if k.alt {
			move(func() { d.wordRight(k.shift) })
		} else {
			move(func() { d.right(k.shift) })
		}
	case "up":
		move(func() { d.vertical(-1, tw, width, wrap, k.shift) })
	case "down":
		move(func() { d.vertical(1, tw, width, wrap, k.shift) })
	case "pgup":
		move(func() { d.vertical(-rows, tw, width, wrap, k.shift) })
	case "pgdown":
		move(func() { d.vertical(rows, tw, width, wrap, k.shift) })
	case "home":
		move(func() { d.lineStart(k.shift, true) })
	case "end":
		move(func() { d.lineEnd(k.shift) })
	case "esc":
		if d.hasSel() {
			move(func() { d.moveTo(d.head, false) })
		}
	}
}

// ---- find ----

// findKey takes a key while the find bar is open; false: it closed the
// bar and the key is the editor's.
func (a *App) findKey(v *editorView, k edKey) bool {
	f := v.find
	switch {
	case k.name == "" && !k.ctrl && !k.alt:
		f.query += string(k.r)
		a.findNext(v, f.origin, 1)
	case k.name == "backspace":
		if r := []rune(f.query); len(r) > 0 {
			f.query = string(r[:len(r)-1])
		}
		a.findNext(v, f.origin, 1)
	case k.name == "enter" && !k.shift, k.name == "down", k.ctrl && k.name == "f", k.ctrl && k.name == "g":
		_, to := v.doc.selRange()
		a.findNext(v, to, 1)
	case k.name == "up", k.name == "enter" && k.shift:
		from, _ := v.doc.selRange()
		a.findNext(v, from, -1)
	case k.name == "esc":
		v.find = nil
	default:
		v.find = nil
		return false
	}
	a.dirty = true
	return true
}

// findNext selects the query's next match from pos (or the previous one,
// before pos), going round the end. Smart case: only a query with capitals
// in it minds case.
func (a *App) findNext(v *editorView, pos, dir int) {
	f, d := v.find, v.doc
	if f.query == "" {
		f.miss = false
		d.moveTo(f.origin, false)
		a.editorMoved(v)
		return
	}
	fold := strings.ToLower(f.query) == f.query
	q := textbuf.FromString(f.query)
	n := len(d.text)
	at := func(i int) bool {
		if i+len(q) > n {
			return false
		}
		for j, c := range q {
			t := d.text[i+j]
			if fold && t < 0x80 && t >= 'A' && t <= 'Z' {
				t += 'a' - 'A'
			} else if fold && t >= 0x80 {
				t = uint16(lowerUnit(t))
			}
			if t != c {
				return false
			}
		}
		return true
	}
	for k := 0; k <= n; k++ {
		var i int
		if dir > 0 {
			i = (pos + k) % (n + 1)
		} else {
			i = ((pos-1-k)%(n+1) + n + 1) % (n + 1)
		}
		if at(i) {
			d.moveTo(i, false)
			d.moveTo(i+len(q), true)
			f.miss = false
			a.editorMoved(v)
			v.reveal = 2
			return
		}
	}
	f.miss = true
}

// lowerUnit lower-cases a UTF-16 unit outside ASCII (surrogates as they are).
func lowerUnit(c uint16) uint16 {
	if c >= 0xd800 && c < 0xe000 {
		return c
	}
	r := []rune(strings.ToLower(string(rune(c))))
	if len(r) == 1 && r[0] < 0x10000 {
		return uint16(r[0])
	}
	return c
}

// ---- commands ----

// editorCommand sends a command for a pane, saying what went wrong.
func (a *App) editorCommand(id string, action pb.EditorCommand_Action) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{PaneId: id, Action: action}}}, nil)
}

// editorSave saves a pane (as path, when given). An untitled one asks where
// first; replacing another file, or saving over a change on disk, asks.
// It waits for the pane's edits to reach the daemon, so what's saved is
// what you see.
func (a *App) editorSave(id, path string, force bool) {
	v := a.editors[id]
	if v == nil {
		return
	}
	if !a.editable(v, true) {
		return
	}
	ed := a.panes[id].GetEditor()
	if path == "" && ed.GetPath() == "" {
		a.editorSaveAs(id)
		return
	}
	v.save = &pendingSave{path: path, force: force}
	a.editorSaveIfDue(v)
}

// editorSaveAs asks for a path (in the Group's folder) and saves there.
func (a *App) editorSaveAs(id string) {
	ed := a.panes[id].GetEditor()
	value := ""
	if ed.GetPath() != "" {
		value = ed.GetPath()
		if ed.GetFolder() != "" {
			if rel, err := filepath.Rel(ed.GetFolder(), ed.GetPath()); err == nil && !strings.HasPrefix(rel, "..") {
				value = rel
			}
		}
	}
	var lines []string
	if ed.GetFolder() != "" {
		lines = []string{truncate("In "+shortPath(ed.GetFolder()), max(a.w-10, 20))}
	}
	a.openDialog(&dialog{title: "Save As", okLabel: "Save", lines: lines, fields: []*field{{label: "Path", value: value}},
		submit: func(v []string) {
			if p := strings.TrimSpace(v[0]); p != "" {
				a.editorSave(id, expandTilde(p), false)
			}
		}})
}

func (a *App) editorSaveIfDue(v *editorView) {
	if v.save == nil || !v.doc.confirmed() {
		return
	}
	s := v.save
	v.save = nil
	id := v.id
	a.callErr(&pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{
		PaneId: id, Action: pb.EditorCommand_ACTION_SAVE, Path: s.path, Force: s.force,
	}}}, func(_ *pb.ServerMessage, err error) {
		if err == nil {
			a.flash("✓ Saved")
			return
		}
		msg := err.Error()
		switch {
		case strings.HasSuffix(msg, "replace it?"):
			a.openDialog(&dialog{title: "Replace File?", okLabel: "Replace", confirm: true, lines: []string{msg},
				submit: func([]string) { a.editorSave(id, s.path, true) }})
		case strings.Contains(msg, "changed on disk"):
			a.openDialog(&dialog{title: "Changed on Disk", okLabel: "Save Anyway", confirm: true,
				lines:  []string{"The file changed on disk since you opened it.", "Save yours over it?"},
				submit: func([]string) { a.editorSave(id, s.path, true) }})
		case strings.HasPrefix(msg, "save it as what"):
			a.editorSaveAs(id)
		default:
			a.flash("⚠ " + msg)
		}
	})
}

// editorReload takes the file as it is on disk. Asked from a menu with
// unsaved edits (no conflict to choose over), it checks first.
func (a *App) editorReload(id string, chosen bool) {
	ed := a.panes[id].GetEditor()
	if ed == nil {
		return
	}
	if ed.Dirty && !ed.Conflict && !chosen {
		a.openDialog(&dialog{title: "Reload from Disk?", okLabel: "Reload", confirm: true,
			lines:  []string{"Your unsaved changes to " + ed.Name + " will be lost."},
			submit: func([]string) { a.editorCommand(id, pb.EditorCommand_ACTION_RELOAD) }})
		return
	}
	a.editorCommand(id, pb.EditorCommand_ACTION_RELOAD)
}

// editorGoToLine asks for a line (or line:column) and goes there.
func (a *App) editorGoToLine(id string) {
	a.openPrompt("Go to Line", "Line or line:column", "", func(s string) {
		v := a.editors[id]
		if v == nil || s == "" {
			return
		}
		ls, cs, _ := strings.Cut(s, ":")
		line, err := strconv.Atoi(strings.TrimSpace(ls))
		if err != nil {
			a.flash("⚠ Not a line number: " + s)
			return
		}
		col, _ := strconv.Atoi(strings.TrimSpace(cs))
		d := v.doc
		if line < 0 {
			line = d.lineCount() + 1 + line // from the end
		}
		p := d.text.Offset(line, 0)
		if col > 1 {
			// Columns count characters, not UTF-16 units.
			_, le := d.lineRange(d.lineOf(p))
			bs := graphemeOffsets(d.text, p, le)
			p = bs[min(col-1, len(bs)-1)]
		}
		d.moveTo(p, false)
		a.editorMoved(v)
		v.reveal = 2
	})
}

// editorItems: what you can do to an editor pane, for its menu and the
// palette.
func (a *App) editorItems(id string) []menuItem {
	ed := a.panes[id].GetEditor()
	v := a.editors[id]
	var items []menuItem
	if v != nil && v.doc.hasSel() {
		items = append(items,
			menuItem{label: "Copy", hint: "⌃C", run: func() { a.copyText(v.doc.selText()) }},
			menuItem{label: "Cut", hint: "⌃X", run: func() {
				a.copyText(v.doc.selText())
				if a.editable(v, true) && v.doc.backspace() {
					a.editorChanged(v)
				}
			}},
			menuItem{sep: true})
	}
	items = append(items,
		menuItem{label: "Save", hint: "⌃S", run: func() { a.editorSave(id, "", false) }},
		menuItem{label: "Save As…", run: func() { a.editorSaveAs(id) }},
		menuItem{label: "Find…", hint: "⌃F", run: func() {
			if v := a.editors[id]; v != nil {
				a.setFocus(id)
				v.find = &editorFind{origin: v.doc.head}
			}
		}},
		menuItem{label: "Go to Line…", hint: "⌃G", run: func() { a.editorGoToLine(id) }},
		menuItem{label: "Reload from Disk", disabled: ed.GetPath() == "", run: func() { a.editorReload(id, false) }})
	if ed.GetConflict() {
		items = append(items,
			menuItem{label: "Keep Mine", hint: "⌥K", run: func() { a.editorCommand(id, pb.EditorCommand_ACTION_KEEP_MINE) }},
			menuItem{label: "Show Changes on Disk", hint: "⌥D", run: func() { a.editorDiff(id) }})
	}
	return items
}

// ---- opening ----

// openEditor opens path (relative to the Group's folder; "" untitled) in a
// new tab in group. A file over 5 MB asks first.
func (a *App) openEditor(group, path string, large bool) {
	if group == "" {
		group = a.currentGroup()
	}
	if group == "" {
		a.flash("Open a Group first")
		return
	}
	spec := &pb.PaneSpec{Editor: &pb.EditorOpen{Path: path, Large: large}}
	a.callErr(&pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: group, Pane: spec}}}, func(r *pb.ServerMessage, err error) {
		if err != nil {
			a.editorOpenFailed(err, func() { a.openEditor(group, path, true) })
			return
		}
		a.selectTab(client.CreatedID(r))
	})
}

// splitEditor opens an untitled editor beside paneID.
func (a *App) splitEditor(paneID string, dir pb.Split_Direction) {
	if paneID == "" {
		a.openEditor("", "", false)
		return
	}
	spec := &pb.PaneSpec{Editor: &pb.EditorOpen{}}
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: paneID, Direction: dir, Spec: spec}}}, func(r *pb.ServerMessage) {
		a.setFocus(client.CreatedID(r))
	})
}

// editorOpenFailed says why a file didn't open; a large one can be opened
// anyway (retry).
func (a *App) editorOpenFailed(err error, retry func()) {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "large file: "); ok {
		a.openDialog(&dialog{title: "Open Large File?", okLabel: "Open Anyway", confirm: true,
			lines:  []string{rest, "Large files can be slow to edit."},
			submit: func([]string) { retry() }})
		return
	}
	a.flash("⚠ " + msg)
}

// ---- closing ----

// unsavedEditors are the editor panes among ids whose buffer would go
// with them: dirty, and no other pane left showing the file.
func (a *App) unsavedEditors(ids []string) []*pb.Pane {
	closing := map[string]bool{}
	for _, id := range ids {
		closing[id] = true
	}
	var out []*pb.Pane
	for _, id := range ids {
		p := a.panes[id]
		ed := p.GetEditor()
		if !isEditor(p) || ed == nil || !ed.Dirty {
			continue
		}
		shared := false
		if ed.Path != "" {
			for oid, o := range a.panes {
				if !closing[oid] && isEditor(o) && o.GetEditor().GetPath() == ed.Path {
					shared = true
					break
				}
			}
		}
		if !shared {
			out = append(out, p)
		}
	}
	return out
}

// confirmUnsaved runs close, first asking if it would lose unsaved edits.
func (a *App) confirmUnsaved(ids []string, close func()) {
	lost := a.unsavedEditors(ids)
	if len(lost) == 0 {
		close()
		return
	}
	var names []string
	for _, p := range lost {
		names = append(names, p.GetEditor().GetName())
	}
	what := names[0] + " has unsaved changes."
	if len(names) > 1 {
		what = strings.Join(names, ", ") + " have unsaved changes."
	}
	a.openDialog(&dialog{title: "Close Without Saving?", okLabel: "Close", confirm: true,
		lines: []string{what, "Close anyway, losing them?"}, submit: func([]string) { close() }})
}

// ---- the mouse ----

// editorMouse handles the mouse in an editor pane's text; false: not one.
func (a *App) editorMouse(z zone, m mouseEv) bool {
	if z.kind != zPane || !isEditor(a.panes[z.id]) {
		return false
	}
	v := a.editors[z.id]
	if v == nil {
		return true
	}
	switch {
	case m.wheel != 0:
		a.editorScroll(v, 3*m.wheel)
	case m.press && m.button == 0:
		a.setFocus(z.id)
		pos, ok := a.editorPosAt(v, m.x, m.y)
		if !ok {
			return true
		}
		now := time.Now()
		if now.Sub(a.lastClick) < 400*time.Millisecond {
			a.clickCount++
		} else {
			a.clickCount = 1
		}
		a.lastClick = now
		d := v.doc
		switch a.clickCount {
		case 2:
			d.moveTo(d.wordLeftOf(d.nextBoundary(pos)), false)
			d.moveTo(d.wordRightOf(d.head), true)
		case 3:
			ls, le := d.lineRange(d.lineOf(pos))
			d.moveTo(ls, false)
			d.moveTo(min(le+1, len(d.text)), true)
		default:
			d.moveTo(pos, m.shift)
			a.drag = &drag{kind: dragEditor, id: z.id}
		}
		v.find = nil
		a.editorMoved(v)
	case m.press && m.button == 2:
		a.setFocus(z.id)
		a.paneMenu(z.id, m.x, m.y)
	}
	return true
}

// editorDrag extends the selection as the mouse drags.
func (a *App) editorDrag(m mouseEv) {
	v := a.editors[a.drag.id]
	if v == nil {
		return
	}
	if m.y < v.textY {
		a.editorScroll(v, -1)
	} else if len(v.shown) > 0 && m.y >= v.textY+len(v.shown) {
		a.editorScroll(v, 1)
	}
	if pos, ok := a.editorPosAt(v, m.x, min(max(m.y, v.textY), v.textY+max(len(v.shown)-1, 0))); ok {
		v.doc.moveTo(pos, true)
		a.drag.moved = true
		a.editorMoved(v)
		v.reveal = 0
	}
}

// editorPosAt is the text position under screen cell x, y.
func (a *App) editorPosAt(v *editorView, x, y int) (int, bool) {
	i := y - v.textY
	if !v.loaded || i < 0 {
		return 0, false
	}
	d := v.doc
	if i >= len(v.shown) {
		return len(d.text), true
	}
	sr := v.shown[i]
	width, _ := a.editorIndent(v.id)
	rows := d.rows(sr.line, v.textW, width, editorPrefs.Wrap)
	if sr.sub >= len(rows) {
		return len(d.text), true
	}
	return posAt(rows[sr.sub], max(x-v.textX, 0)+v.left, true), true
}

// editorScroll scrolls the pane n rows, without moving the cursor.
func (a *App) editorScroll(v *editorView, n int) {
	if !v.loaded {
		return
	}
	width, _ := a.editorIndent(v.id)
	v.top, v.topSub = v.walk(v.top, v.topSub, n, v.textW, width, editorPrefs.Wrap)
	v.reveal = 0
	a.dirty = true
}

// walk moves (line, sub) n rows on (back, when negative), within the text.
func (v *editorView) walk(line, sub, n, width, tabw int, wrap bool) (int, int) {
	d := v.doc
	count := func(l int) int {
		if !wrap {
			return 1
		}
		return len(d.rows(l, width, tabw, true))
	}
	for ; n > 0; n-- {
		if sub+1 < count(line) {
			sub++
		} else if line+1 < d.lineCount() {
			line, sub = line+1, 0
		} else {
			break
		}
	}
	for ; n < 0; n++ {
		if sub > 0 {
			sub--
		} else if line > 0 {
			line--
			sub = count(line) - 1
		} else {
			break
		}
	}
	return line, sub
}

// ---- drawing ----

// editorRevealCursor scrolls so the cursor shows in h rows of textW cells
// (centred when asked, as for an agent's selection).
func (v *editorView) revealCursor(h, textW, tabw int, wrap bool) {
	d := v.doc
	line := d.lineOf(d.head)
	rows := d.rows(line, textW, tabw, wrap)
	sub, x := rowOf(rows, d.head)
	if wrap {
		v.left = 0
	} else if x < v.left {
		v.left = max(x-textW/4, 0)
	} else if x >= v.left+textW {
		v.left = x - textW + textW/4 + 1
	}
	if v.reveal == 2 {
		v.top, v.topSub = v.walk(line, sub, -h/3, textW, tabw, wrap)
		return
	}
	if line < v.top || line == v.top && sub < v.topSub {
		v.top, v.topSub = line, sub
		return
	}
	if line-v.top >= h {
		v.top, v.topSub = v.walk(line, sub, -(h - 1), textW, tabw, wrap)
		return
	}
	// Count the rows from the top to the cursor.
	n, l, s := 0, v.top, v.topSub
	for (l < line || l == line && s < sub) && n < h {
		l, s = v.walk(l, s, 1, textW, tabw, wrap)
		n++
	}
	if n >= h {
		v.top, v.topSub = v.walk(line, sub, -(h - 1), textW, tabw, wrap)
	}
}

func digits(n int) int { return len(strconv.Itoa(n)) }

// drawEditor draws an editor pane's body; it gives where the cursor is.
func (a *App) drawEditor(p *pb.Pane, body rect, focused bool) (cx, cy int, show bool) {
	th, s := a.th, a.scr
	v := a.editors[p.Id]
	if v == nil || !v.loaded {
		msg := "Opening " + p.GetEditor().GetName() + "…"
		s.Text(body.x+(body.w-textWidth(msg))/2, body.y+body.h/2, body.w, truncate(msg, body.w), Cell{FG: th.dim, HasFG: true})
		return 0, 0, false
	}
	ed := p.GetEditor()
	if ed.GetConflict() && body.h > 2 {
		a.drawConflictBar(p.Id, rect{body.x, body.y, body.w, 1})
		body.y++
		body.h--
	}
	findX, findY := -1, 0
	if v.find != nil && body.h > 2 {
		findX, findY = a.drawFindBar(v, rect{body.x, body.y + body.h - 1, body.w, 1})
		body.h--
	}
	if findX >= 0 {
		defer func() { cx, cy, show = findX, findY, focused }() // typing goes to the find bar
	}
	d := v.doc
	tabw, _ := a.editorIndent(p.Id)
	wrap := editorPrefs.Wrap
	numW := 0
	if editorPrefs.LineNumbers {
		numW = max(digits(d.lineCount()), 2)
	}
	gutter := 1 + numW + 1 // the git bar, the number, a space
	textW := max(body.w-gutter, 1)
	v.textX, v.textY, v.textW = body.x+gutter, body.y, textW
	if v.reveal > 0 {
		v.revealCursor(body.h, textW, tabw, wrap)
		v.reveal = 0
	}
	v.top = min(v.top, d.lineCount()-1)
	selFrom, selTo := d.selRange()
	headLine := d.lineOf(d.head)
	marks := gitMarkColours(v.marks, th)
	curLineBG := mix(th.bg, th.fg, 0.035)
	selBG := mix(th.bg, th.accent, 0.38)
	v.shown = v.shown[:0]
	y, line, sub := 0, v.top, v.topSub
	for y < body.h && line < d.lineCount() {
		rows := d.rows(line, textW, tabw, wrap)
		if sub >= len(rows) {
			sub = len(rows) - 1
		}
		for ri := sub; ri < len(rows) && y < body.h; ri++ {
			row := rows[ri]
			sy := body.y + y
			v.shown = append(v.shown, shownRow{line, ri})
			bg := th.bg
			if focused && line == headLine && !d.hasSel() {
				bg = curLineBG
				s.Fill(body.x+gutter, sy, textW, 1, Cell{BG: bg, HasBG: true})
			}
			// The gutter: the git mark, then the number on a line's first row.
			if m, ok := marks[line+1]; ok {
				s.Set(body.x, sy, Cell{Ch: m.ch, FG: m.col, HasFG: true})
			}
			if numW > 0 && ri == 0 {
				col := th.faint
				if line == headLine {
					col = th.dim
				}
				s.TextRight(body.x+1, sy, numW, strconv.Itoa(line+1), Cell{FG: col, HasFG: true})
			}
			for _, g := range row.glyphs {
				x := g.x - v.left
				if x+g.w <= 0 || x >= textW {
					continue
				}
				c := Cell{FG: th.fg, HasFG: true, BG: bg, HasBG: true}
				if g.off >= selFrom && g.off < selTo {
					c.BG = selBG
				}
				if g.s == "" || g.s == "·" || (len(g.s) == 3 && []rune(g.s)[0] >= 0x2400 && []rune(g.s)[0] <= 0x2421) {
					c.FG = th.faint // tabs, controls and invisibles
				}
				if g.w == 2 && g.s != "" && x >= 0 && x+1 < textW {
					s.Text(body.x+gutter+x, sy, 2, g.s, c)
					continue
				}
				// Tabs, and a wide character cut by an edge, as spaces.
				for i := 0; i < g.w; i++ {
					if xx := x + i; xx >= 0 && xx < textW {
						cc := c
						cc.Ch = " "
						if g.w == 1 && g.s != "" {
							cc.Ch = g.s
						}
						s.Set(body.x+gutter+xx, sy, cc)
					}
				}
			}
			// A selection running over the line break: a cell after the text.
			if row.last && selFrom <= row.to && selTo > row.to {
				if x := row.width - v.left; x >= 0 && x < textW {
					s.Set(body.x+gutter+x, sy, Cell{Ch: " ", BG: selBG, HasBG: true})
				}
			}
			if line == headLine {
				if ri2, x := rowOf(rows, d.head); ri2 == ri {
					if x -= v.left; x >= 0 && x < textW {
						cx, cy, show = body.x+gutter+x, sy, focused
					}
				}
			}
			y++
		}
		line, sub = line+1, 0
	}
	if ed.GetMissing() && len(d.text) == 0 && body.h > 2 && ed.GetPath() != "" {
		s.Text(body.x+gutter, body.y+1, textW, truncate("New file: saving creates it", textW), Cell{FG: th.faint, HasFG: true})
	}
	return cx, cy, show
}

type markGlyph struct {
	ch  string
	col RGB
}

// gitMarkColours is where the gutter shows lines changed since git's HEAD:
// a bar for added (green) and modified (blue) lines, a low mark where
// lines were removed (red).
func gitMarkColours(marks []*pb.GitMark, th theme) map[int]markGlyph {
	out := map[int]markGlyph{}
	for _, m := range marks {
		switch m.Kind {
		case pb.GitMark_KIND_ADDED, pb.GitMark_KIND_MODIFIED:
			col := th.green
			if m.Kind == pb.GitMark_KIND_MODIFIED {
				col = th.accent
			}
			for l := m.From; l <= m.To && l-m.From < 100000; l++ {
				out[int(l)] = markGlyph{"▎", col}
			}
		case pb.GitMark_KIND_DELETED:
			l := max(int(m.From), 1)
			if _, ok := out[l]; !ok {
				out[l] = markGlyph{"▁", th.red}
			}
		}
	}
	return out
}

// drawConflictBar offers what to do about the file changing on disk under
// unsaved edits.
func (a *App) drawConflictBar(id string, r rect) {
	th, s := a.th, a.scr
	bg := mix(th.bg, th.amber, 0.22)
	s.Fill(r.x, r.y, r.w, 1, Cell{BG: bg, HasBG: true})
	x := r.x + 1
	x += s.Text(x, r.y, r.w-2, "Changed on disk:", Cell{FG: th.fg, HasFG: true, BG: bg, HasBG: true, Bold: true}) + 1
	for i, label := range []string{"Reload ⌥R", "Keep Mine ⌥K", "Diff ⌥D"} {
		w := textWidth(label) + 2
		if x+w > r.x+r.w {
			break
		}
		on := a.hovered(zEditorBar, id) && a.mouseY == r.y && a.mouseX >= x && a.mouseX < x+w
		bbg := mix(bg, th.fg, 0.08+0.12*b2f(on))
		s.Fill(x, r.y, w, 1, Cell{BG: bbg, HasBG: true})
		s.Text(x+1, r.y, w-2, label, Cell{FG: th.fg, HasFG: true, BG: bbg, HasBG: true})
		a.addZone(zone{r: rect{x, r.y, w, 1}, kind: zEditorBar, id: id, idx: i})
		x += w + 1
	}
}

// editorBarClick acts on a conflict bar's button.
func (a *App) editorBarClick(z zone) {
	switch z.idx {
	case 0:
		a.editorReload(z.id, true)
	case 1:
		a.editorCommand(z.id, pb.EditorCommand_ACTION_KEEP_MINE)
	case 2:
		a.editorDiff(z.id)
	}
}

// drawFindBar draws the find bar along the pane's bottom; it gives where
// its cursor is.
func (a *App) drawFindBar(v *editorView, r rect) (int, int) {
	th, s := a.th, a.scr
	bg := mix(th.bg, th.fg, 0.08)
	s.Fill(r.x, r.y, r.w, 1, Cell{BG: bg, HasBG: true})
	n := s.Text(r.x+1, r.y, r.w-2, "Find: ", Cell{FG: th.dim, HasFG: true, BG: bg, HasBG: true})
	qc := Cell{FG: th.fg, HasFG: true, BG: bg, HasBG: true}
	if v.find.miss {
		qc.FG = th.red
	}
	q := s.Text(r.x+1+n, r.y, r.w-2-n, v.find.query, qc)
	hint := "↵ next  ↑ previous  esc done"
	if v.find.miss {
		hint = "not found  " + hint
	}
	if r.w-n-q > textWidth(hint)+6 {
		s.TextRight(r.x+1, r.y, r.w-2, hint, Cell{FG: th.faint, HasFG: true, BG: bg, HasBG: true})
	}
	return min(r.x+1+n+q, r.x+r.w-1), r.y
}

// editorHeader is what an editor pane's header says: its name (a dot while
// unsaved), where it is, and the cursor's line and column.
func (a *App) editorHeader(p *pb.Pane) (name, detail, status string) {
	ed := p.GetEditor()
	name = ed.GetName()
	if name == "" {
		name = p.Title
	}
	if ed.GetDirty() {
		name += " ●"
	}
	switch {
	case ed.GetPath() != "":
		detail = shortPath(ed.GetPath())
	case ed.GetFolder() != "":
		detail = "untitled, in " + shortPath(ed.GetFolder())
	}
	var bits []string
	if !canDrive(a.paneGroup(p.Id)) {
		bits = append(bits, "View only")
	} else if ed.GetReadOnly() {
		bits = append(bits, "Read-only")
	}
	if v := a.editors[p.Id]; v != nil && v.loaded {
		d := v.doc
		l := d.lineOf(d.head)
		ls, _ := d.lineRange(l)
		col := len(graphemeOffsets(d.text, ls, d.head))
		bits = append(bits, fmt.Sprintf("Ln %d, Col %d", l+1, col))
	}
	return name, detail, strings.Join(bits, "  ")
}
