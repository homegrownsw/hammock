package tui

// An editor pane's text as this TUI has it: the daemon's buffer at the
// version it last sent, plus this pane's own edits it hasn't confirmed yet
// (CodeMirror's collab model; see EditorPush in the proto). Everything here
// counts UTF-16 code units, as textbuf and the daemon do; the screen counts
// cells, so the layout helpers convert between the two a grapheme at a time.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/charmbracelet/x/ansi"
	"github.com/clipperhouse/uax29/v2/graphemes"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/textbuf"
)

// editGroup is one undo (or redo) step: the changes that take it back, in
// the order they apply, and where the cursor was.
type editGroup struct {
	steps []textbuf.ChangeSet
}

type editDoc struct {
	text     textbuf.Text
	version  uint64 // updates received from the daemon
	clientID string

	// unconfirmed are edits made here the daemon hasn't sent back yet, in
	// order; the first inFlight of them were pushed at pushedAt.
	unconfirmed []textbuf.ChangeSet
	inFlight    int
	pushedAt    uint64
	// retryAfter: a push made at this version was refused; push again once
	// the updates that beat it arrive (the version moves past it).
	retryAfter uint64
	waiting    bool

	anchor, head int
	goal         int // the column vertical moves aim for (-1: the cursor's own)

	undo, redo []editGroup
	lastKind   string // what the last edit was ("type", "del"), to group typing
	lastAt     time.Time
	lastEnd    int // where the cursor was after it

	starts []int // where each line starts (nil: work it out again)
}

func newEditDoc() *editDoc {
	b := make([]byte, 6)
	rand.Read(b)
	return &editDoc{clientID: "tui-" + hex.EncodeToString(b), goal: -1}
}

// reset starts again from a snapshot: unconfirmed edits are dropped (the
// daemon sends one only on attaching, or when asked).
func (d *editDoc) reset(text string, version uint64) {
	d.text = textbuf.FromString(text)
	d.version = version
	d.unconfirmed, d.inFlight, d.waiting = nil, 0, false
	d.undo, d.redo, d.lastKind = nil, nil, ""
	d.starts = nil
	d.anchor, d.head = d.clamp(d.anchor), d.clamp(d.head)
	d.goal = -1
}

func (d *editDoc) clamp(p int) int { return min(max(p, 0), len(d.text)) }

// ---- lines ----

func (d *editDoc) lineStarts() []int {
	if d.starts == nil {
		d.starts = append(make([]int, 0, 64), 0)
		for i, c := range d.text {
			if c == '\n' {
				d.starts = append(d.starts, i+1)
			}
		}
	}
	return d.starts
}

func (d *editDoc) lineCount() int { return len(d.lineStarts()) }

// lineOf is pos's line, from 0.
func (d *editDoc) lineOf(pos int) int {
	s := d.lineStarts()
	return sort.Search(len(s), func(i int) bool { return s[i] > pos }) - 1
}

// lineRange is line l's text, without its "\n".
func (d *editDoc) lineRange(l int) (from, to int) {
	s := d.lineStarts()
	l = min(max(l, 0), len(s)-1)
	from, to = s[l], len(d.text)
	if l+1 < len(s) {
		to = s[l+1] - 1
	}
	return from, to
}

// ---- local edits ----

// invert is the change that undoes cs, made to before.
func invert(cs textbuf.ChangeSet, before textbuf.Text) textbuf.ChangeSet {
	var rs []textbuf.Replace
	shift := 0
	for _, r := range cs.Replacements() {
		from := r.From + shift
		rs = append(rs, textbuf.Replace{From: from, To: from + len(r.Text), Text: append(textbuf.Text(nil), before[r.From:r.To]...)})
		shift += len(r.Text) - (r.To - r.From)
	}
	out, _ := textbuf.Build(cs.NewLen(), rs)
	return out
}

// change makes an edit here: it's applied, queued to push, and (with a
// kind) undoable, typing and deleting in a run grouping into one step.
func (d *editDoc) change(cs textbuf.ChangeSet, kind string) bool {
	if cs.Empty() || cs.Len() != len(d.text) {
		return false
	}
	inv := invert(cs, d.text)
	t, err := cs.Apply(d.text)
	if err != nil {
		return false
	}
	d.text, d.starts = t, nil
	d.unconfirmed = append(d.unconfirmed, cs)
	d.goal = -1
	if kind == "" {
		return true
	}
	now := time.Now()
	run := false
	if rs := cs.Replacements(); len(rs) == 1 && kind == d.lastKind && now.Sub(d.lastAt) < time.Second && len(d.undo) > 0 {
		run = kind == "type" && rs[0].From == d.lastEnd || kind == "del" && rs[0].To == d.lastEnd
	}
	if run {
		top := &d.undo[len(d.undo)-1]
		top.steps = append([]textbuf.ChangeSet{inv}, top.steps...)
	} else {
		d.undo = append(d.undo, editGroup{steps: []textbuf.ChangeSet{inv}})
	}
	d.redo = nil
	d.lastKind, d.lastAt = kind, now
	return true
}

// replace replaces [from, to) with s, the cursor going after it.
func (d *editDoc) replace(from, to int, s, kind string) bool {
	cs, err := textbuf.Build(len(d.text), []textbuf.Replace{{From: from, To: to, Text: textbuf.FromString(s)}})
	if err != nil || !d.change(cs, kind) {
		return false
	}
	p := from + len(textbuf.FromString(s))
	d.anchor, d.head, d.lastEnd = p, p, p
	return true
}

func (d *editDoc) hasSel() bool { return d.anchor != d.head }

func (d *editDoc) selRange() (int, int) { return min(d.anchor, d.head), max(d.anchor, d.head) }

func (d *editDoc) selText() string {
	f, t := d.selRange()
	return d.text[f:t].String()
}

// insert types s over the selection (pastes come with "\r\n" and "\r").
func (d *editDoc) insert(s string) bool {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	if s == "" {
		return false
	}
	kind := "type"
	if strings.ContainsAny(s, "\n\t ") || len([]rune(s)) > 1 {
		kind = "edit" // a word, a line or a paste is its own undo step
	}
	f, t := d.selRange()
	return d.replace(f, t, s, kind)
}

// newline breaks the line, keeping its indentation.
func (d *editDoc) newline() bool {
	f, t := d.selRange()
	ls, _ := d.lineRange(d.lineOf(f))
	i := ls
	for i < f && (d.text[i] == ' ' || d.text[i] == '\t') {
		i++
	}
	return d.replace(f, t, "\n"+d.text[ls:i].String(), "edit")
}

// indentUnit is what Tab inserts at column col (in cells).
func indentUnit(tabs bool, width, col int) string {
	if tabs {
		return "\t"
	}
	return strings.Repeat(" ", width-col%max(width, 1))
}

// tab indents: the selected lines when the selection spans lines, else
// at the cursor, to the next tab stop.
func (d *editDoc) tab(tabs bool, width int) bool {
	f, t := d.selRange()
	l0, l1 := d.lineOf(f), d.lineOf(t)
	if l0 == l1 {
		ls, _ := d.lineRange(l0)
		col := 0
		for _, g := range layoutLine(d.text, ls, f, width) {
			col = g.x + g.w
		}
		return d.replace(f, t, indentUnit(tabs, width, col), "edit")
	}
	unit := indentUnit(tabs, width, 0)
	var rs []textbuf.Replace
	for l := l0; l <= l1; l++ {
		ls, le := d.lineRange(l)
		if ls == le || (l == l1 && ls == t) {
			continue // empty lines, and the line the selection ends at the start of
		}
		rs = append(rs, textbuf.Replace{From: ls, To: ls, Text: textbuf.FromString(unit)})
	}
	return d.changeKeepingSel(rs)
}

// outdent takes one level of indentation off the selected lines.
func (d *editDoc) outdent(width int) bool {
	f, t := d.selRange()
	l0, l1 := d.lineOf(f), d.lineOf(t)
	var rs []textbuf.Replace
	for l := l0; l <= l1; l++ {
		ls, le := d.lineRange(l)
		n := 0
		if ls < le && d.text[ls] == '\t' {
			n = 1
		} else {
			for n < width && ls+n < le && d.text[ls+n] == ' ' {
				n++
			}
		}
		if n > 0 {
			rs = append(rs, textbuf.Replace{From: ls, To: ls + n})
		}
	}
	return d.changeKeepingSel(rs)
}

// changeKeepingSel makes rs, the selection moving with the text.
func (d *editDoc) changeKeepingSel(rs []textbuf.Replace) bool {
	if len(rs) == 0 {
		return false
	}
	cs, err := textbuf.Build(len(d.text), rs)
	if err != nil {
		return false
	}
	a, h := d.anchor, d.head
	if !d.change(cs, "edit") {
		return false
	}
	d.anchor, d.head = cs.MapPos(a, 1), cs.MapPos(h, 1)
	if a == h {
		d.anchor = d.head
	}
	return true
}

// backspace deletes the selection, else the grapheme before the cursor.
func (d *editDoc) backspace() bool {
	if d.hasSel() {
		f, t := d.selRange()
		return d.replace(f, t, "", "edit")
	}
	if d.head == 0 {
		return false
	}
	return d.replace(d.prevBoundary(d.head), d.head, "", "del")
}

// deleteForward deletes the selection, else the grapheme after the cursor.
func (d *editDoc) deleteForward() bool {
	if d.hasSel() {
		f, t := d.selRange()
		return d.replace(f, t, "", "edit")
	}
	if d.head >= len(d.text) {
		return false
	}
	return d.replace(d.head, d.nextBoundary(d.head), "", "edit")
}

// deleteWordBack deletes back to the start of the word (⌥⌫).
func (d *editDoc) deleteWordBack() bool {
	if d.hasSel() {
		return d.backspace()
	}
	p := d.wordLeftOf(d.head)
	if p == d.head {
		return false
	}
	return d.replace(p, d.head, "", "edit")
}

// ---- undo ----

// undoLast takes back the last step; redoLast makes it again.
func (d *editDoc) undoLast() bool { return d.history(&d.undo, &d.redo) }
func (d *editDoc) redoLast() bool { return d.history(&d.redo, &d.undo) }

func (d *editDoc) history(from, to *[]editGroup) bool {
	if len(*from) == 0 {
		return false
	}
	g := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	var back []textbuf.ChangeSet
	cursor := -1
	for _, s := range g.steps {
		if s.Len() != len(d.text) {
			continue // shouldn't happen: the steps are kept in step with the text
		}
		inv := invert(s, d.text)
		if !d.change(s, "") {
			continue
		}
		back = append([]textbuf.ChangeSet{inv}, back...)
		if rs := s.Replacements(); len(rs) > 0 {
			cursor = rs[0].From + len(rs[0].Text) // after what it put back
		}
	}
	if len(back) > 0 {
		*to = append(*to, editGroup{steps: back})
	}
	if cursor >= 0 {
		d.anchor, d.head = d.clamp(cursor), d.clamp(cursor)
	}
	d.lastKind = ""
	return len(back) > 0
}

// ---- the daemon ----

// rebase moves u (made to the document local was made to) over local's
// changes, in order: it gives local mapped to apply after u, and u mapped
// to apply after them. The daemon's u goes first where both insert.
func rebase(local []textbuf.ChangeSet, u textbuf.ChangeSet) ([]textbuf.ChangeSet, textbuf.ChangeSet) {
	out := make([]textbuf.ChangeSet, len(local))
	for i, l := range local {
		out[i] = l.Map(u, false)
		u = u.Map(l, true)
	}
	return out, u
}

// mapHistory moves the undo and redo steps over u, a change just made to
// the text by someone else (each step applies after the ones above it).
func (d *editDoc) mapHistory(u textbuf.ChangeSet) {
	for _, stack := range []*[]editGroup{&d.undo, &d.redo} {
		r := u
		for i := len(*stack) - 1; i >= 0; i-- {
			(*stack)[i].steps, r = rebase((*stack)[i].steps, r)
		}
	}
}

// receive takes an EditorUpdate's updates (not a snapshot): this pane's
// own that lead it confirm its oldest unconfirmed edits; the rest are
// rebased over those still unconfirmed and applied. It gives the changes
// made to the text here, for whoever watches it.
func (d *editDoc) receive(updates []*pb.EditorChange) ([]textbuf.ChangeSet, error) {
	return d.receiveAt(d.version+uint64(len(updates)), updates)
}

// errOutOfStep: updates that don't follow on from this pane's version (some
// were missed): it needs the daemon's text again.
var errOutOfStep = errors.New("the editor fell out of step with Hammock: reloading its text")

// receiveAt is receive for updates that take the daemon to version: ones
// already had (sent before a snapshot taken since) are skipped, and a gap
// is errOutOfStep, nothing applied.
func (d *editDoc) receiveAt(version uint64, updates []*pb.EditorChange) ([]textbuf.ChangeSet, error) {
	if version <= d.version {
		return nil, nil // all had already
	}
	if base := version - uint64(len(updates)); version < uint64(len(updates)) || base != d.version {
		return nil, errOutOfStep
	}
	n := 0
	for n < len(updates) && updates[n].ClientId == d.clientID && n < len(d.unconfirmed) {
		n++
	}
	d.unconfirmed = d.unconfirmed[n:]
	d.inFlight = max(d.inFlight-n, 0)
	var applied []textbuf.ChangeSet
	for _, u := range updates[n:] {
		cs, err := textbuf.ParseJSON(u.Changes)
		if err != nil {
			d.version += uint64(len(updates)) // keep counting; the caller asks for a snapshot
			return applied, err
		}
		d.unconfirmed, cs = rebase(d.unconfirmed, cs)
		t, err := cs.Apply(d.text)
		if err != nil {
			d.version += uint64(len(updates))
			return applied, err
		}
		d.text, d.starts = t, nil
		d.anchor, d.head = cs.MapPos(d.anchor, -1), cs.MapPos(d.head, -1)
		d.mapHistory(cs)
		d.lastKind = ""
		applied = append(applied, cs)
	}
	d.version += uint64(len(updates))
	return applied, nil
}

// toPush is the next push to send: everything unconfirmed, at the current
// version, when nothing is in flight (nil: nothing to send now).
func (d *editDoc) toPush() *pb.EditorPush {
	if d.inFlight > 0 || len(d.unconfirmed) == 0 || (d.waiting && d.version <= d.retryAfter) {
		return nil
	}
	d.waiting = false
	d.inFlight, d.pushedAt = len(d.unconfirmed), d.version
	m := &pb.EditorPush{Version: d.version}
	for _, cs := range d.unconfirmed {
		m.Updates = append(m.Updates, &pb.EditorChange{Changes: cs.JSON(), ClientId: d.clientID})
	}
	return m
}

// pushed takes the daemon's answer to a push: refused, it's sent again
// once the updates that beat it have arrived.
func (d *editDoc) pushed(accepted bool, version uint64) {
	if accepted || version != d.pushedAt || d.inFlight == 0 {
		return
	}
	d.inFlight = 0
	d.waiting, d.retryAfter = true, version
}

// failed forgets a push the daemon couldn't take (an error, not a refusal).
func (d *editDoc) failed() { d.inFlight, d.waiting = 0, false }

// confirmed reports whether the text here is the daemon's at version (its
// offsets mean the same here only then).
func (d *editDoc) confirmed() bool { return len(d.unconfirmed) == 0 }

// fromServer maps a position at the daemon's version (an agent's selection)
// to the text here, over the unconfirmed edits.
func (d *editDoc) fromServer(pos int) int {
	for _, cs := range d.unconfirmed {
		pos = cs.MapPos(pos, -1)
	}
	return d.clamp(pos)
}

// ---- characters and words ----

// graphemeOffsets are the boundaries of the graphemes in t[from:to], as
// offsets into t.
func graphemeOffsets(t textbuf.Text, from, to int) []int {
	out := []int{from}
	off := from
	g := graphemes.FromString(t[from:to].String())
	for g.Next() {
		off += utf16Len(g.Value())
		out = append(out, off)
	}
	return out
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += max(utf16.RuneLen(r), 1) // a lone surrogate comes out of Text.String as U+FFFD: one unit too
	}
	return n
}

// prevBoundary is the grapheme boundary before pos (a line break counts
// as one).
func (d *editDoc) prevBoundary(pos int) int {
	if pos <= 0 {
		return 0
	}
	if d.text[pos-1] == '\n' {
		return pos - 1
	}
	ls, _ := d.lineRange(d.lineOf(pos))
	bs := graphemeOffsets(d.text, ls, pos)
	if len(bs) >= 2 {
		return bs[len(bs)-2]
	}
	return pos - 1
}

// nextBoundary is the grapheme boundary after pos.
func (d *editDoc) nextBoundary(pos int) int {
	if pos >= len(d.text) {
		return len(d.text)
	}
	if d.text[pos] == '\n' {
		return pos + 1
	}
	_, le := d.lineRange(d.lineOf(pos))
	bs := graphemeOffsets(d.text, pos, le)
	if len(bs) >= 2 {
		return bs[1]
	}
	return pos + 1
}

// runeAt decodes the character at pos (a surrogate pair is one).
func runeAt(t textbuf.Text, pos int) (rune, int) {
	c := t[pos]
	if utf16.IsSurrogate(rune(c)) && pos+1 < len(t) {
		if r := utf16.DecodeRune(rune(c), rune(t[pos+1])); r != unicode.ReplacementChar {
			return r, 2
		}
	}
	return rune(c), 1
}

// runeBefore decodes the character ending at pos.
func runeBefore(t textbuf.Text, pos int) (rune, int) {
	c := t[pos-1]
	if utf16.IsSurrogate(rune(c)) && pos >= 2 {
		if r := utf16.DecodeRune(rune(t[pos-2]), rune(c)); r != unicode.ReplacementChar {
			return r, 2
		}
	}
	return rune(c), 1
}

// charClass sorts characters for moving by word: spaces, word characters
// and everything else.
func charClass(r rune) int {
	switch {
	case r == ' ' || r == '\t':
		return 0
	case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r):
		return 1
	}
	return 2
}

// wordRightOf is where moving a word right from pos lands: past any
// spaces, then a run of one class; a line break is a step of its own.
func (d *editDoc) wordRightOf(pos int) int {
	t := d.text
	if pos >= len(t) {
		return len(t)
	}
	if t[pos] == '\n' {
		return pos + 1
	}
	for pos < len(t) && t[pos] != '\n' {
		r, n := runeAt(t, pos)
		if charClass(r) != 0 {
			break
		}
		pos += n
	}
	if pos >= len(t) || t[pos] == '\n' {
		return pos
	}
	r, _ := runeAt(t, pos)
	cls := charClass(r)
	for pos < len(t) && t[pos] != '\n' {
		r, n := runeAt(t, pos)
		if charClass(r) != cls {
			break
		}
		pos += n
	}
	return pos
}

// wordLeftOf is wordRightOf, going left.
func (d *editDoc) wordLeftOf(pos int) int {
	t := d.text
	if pos <= 0 {
		return 0
	}
	if t[pos-1] == '\n' {
		return pos - 1
	}
	for pos > 0 && t[pos-1] != '\n' {
		r, n := runeBefore(t, pos)
		if charClass(r) != 0 {
			break
		}
		pos -= n
	}
	if pos <= 0 || t[pos-1] == '\n' {
		return pos
	}
	r, _ := runeBefore(t, pos)
	cls := charClass(r)
	for pos > 0 && t[pos-1] != '\n' {
		r, n := runeBefore(t, pos)
		if charClass(r) != cls {
			break
		}
		pos -= n
	}
	return pos
}

// ---- moving the cursor ----

// moveTo puts the cursor at pos, selecting from the anchor if extend.
func (d *editDoc) moveTo(pos int, extend bool) {
	d.head = d.clamp(pos)
	if !extend {
		d.anchor = d.head
	}
	d.lastKind = ""
}

func (d *editDoc) left(extend bool) {
	d.goal = -1
	if d.hasSel() && !extend {
		f, _ := d.selRange()
		d.moveTo(f, false)
		return
	}
	d.moveTo(d.prevBoundary(d.head), extend)
}

func (d *editDoc) right(extend bool) {
	d.goal = -1
	if d.hasSel() && !extend {
		_, t := d.selRange()
		d.moveTo(t, false)
		return
	}
	d.moveTo(d.nextBoundary(d.head), extend)
}

func (d *editDoc) wordLeft(extend bool) {
	d.goal = -1
	d.moveTo(d.wordLeftOf(d.head), extend)
}

func (d *editDoc) wordRight(extend bool) {
	d.goal = -1
	d.moveTo(d.wordRightOf(d.head), extend)
}

// lineStart goes to the start of the line (⌃A); home, smart, to its first
// non-space first, then the start.
func (d *editDoc) lineStart(extend, smart bool) {
	d.goal = -1
	ls, le := d.lineRange(d.lineOf(d.head))
	p := ls
	if smart {
		i := ls
		for i < le && (d.text[i] == ' ' || d.text[i] == '\t') {
			i++
		}
		if d.head != i {
			p = i
		}
	}
	d.moveTo(p, extend)
}

func (d *editDoc) lineEnd(extend bool) {
	d.goal = -1
	_, le := d.lineRange(d.lineOf(d.head))
	d.moveTo(le, extend)
}

// ---- layout ----

// cellGlyph is a grapheme laid out in cells: where it is in the text (off,
// n units long) and on the row (x, w cells wide), and what to draw.
type cellGlyph struct {
	off, n int
	x, w   int
	s      string
}

// layoutLine lays out t[from:to] (one line, no "\n"): tabs to the next
// multiple of tabw, control characters as their pictures.
func layoutLine(t textbuf.Text, from, to, tabw int) []cellGlyph {
	if from >= to {
		return nil
	}
	tabw = max(tabw, 1)
	out := make([]cellGlyph, 0, to-from)
	off, x := from, 0
	g := graphemes.FromString(t[from:to].String())
	for g.Next() {
		s := g.Value()
		n := utf16Len(s)
		var w int
		switch {
		case s == "\t":
			w, s = tabw-x%tabw, ""
		case len(s) == 1 && s[0] < 0x20:
			w, s = 1, string(rune(0x2400+int(s[0])))
		case s == "\x7f":
			w, s = 1, "␡"
		default:
			w = ansi.StringWidth(s)
			if w == 0 {
				w, s = 1, "·" // nothing to see (a lone joiner): still somewhere to put the cursor
			}
		}
		out = append(out, cellGlyph{off: off, n: n, x: x, w: w, s: s})
		off += n
		x += w
	}
	return out
}

// vrow is one row on screen: part of a line (all of it, unless wrapped),
// its glyphs' x counted from the row's start.
type vrow struct {
	line     int
	from, to int
	glyphs   []cellGlyph
	last     bool // the line's last row (the cursor can sit at its end)
	width    int  // cells its glyphs take
}

// rows lays line out in rows width cells wide (one row without wrap).
func (d *editDoc) rows(line, width, tabw int, wrap bool) []vrow {
	from, to := d.lineRange(line)
	gs := layoutLine(d.text, from, to, tabw)
	if !wrap || width <= 0 {
		w := 0
		if len(gs) > 0 {
			w = gs[len(gs)-1].x + gs[len(gs)-1].w
		}
		return []vrow{{line: line, from: from, to: to, glyphs: gs, last: true, width: w}}
	}
	var out []vrow
	start, x0 := 0, 0
	for i, g := range gs {
		if i > start && g.x+g.w-x0 > width {
			out = append(out, d.makeRow(line, gs[start:i], x0, false))
			start, x0 = i, g.x
		}
	}
	r := d.makeRow(line, gs[start:], x0, true)
	if len(gs) == 0 {
		r.from = from
	}
	r.to = to
	return append(out, r)
}

func (d *editDoc) makeRow(line int, gs []cellGlyph, x0 int, last bool) vrow {
	row := vrow{line: line, last: last, glyphs: make([]cellGlyph, len(gs))}
	for i, g := range gs {
		g.x -= x0
		row.glyphs[i] = g
		row.width = g.x + g.w
	}
	if len(gs) > 0 {
		row.from = gs[0].off
		row.to = gs[len(gs)-1].off + gs[len(gs)-1].n
	}
	return row
}

// rowOf is which of rows pos is on, and its x there.
func rowOf(rows []vrow, pos int) (int, int) {
	for i, r := range rows {
		if pos < r.to || (pos == r.to && r.last) || i == len(rows)-1 {
			for _, g := range r.glyphs {
				if pos < g.off+g.n {
					return i, g.x
				}
			}
			return i, r.width
		}
	}
	return 0, 0
}

// posAt is the position at cell x of row r: the glyph covering it (or, with
// round, the nearer of its edges, for a click).
func posAt(r vrow, x int, round bool) int {
	for i, g := range r.glyphs {
		if x < g.x+g.w {
			if round && x-g.x >= (g.w+1)/2 {
				return g.off + g.n
			}
			return g.off
		}
		if i == len(r.glyphs)-1 && !r.last {
			return g.off // a wrapped row's end is the next row's start
		}
	}
	if r.last || len(r.glyphs) == 0 {
		return r.to
	}
	return r.glyphs[len(r.glyphs)-1].off
}

// vertical moves the cursor n rows (up when negative), aiming for the
// column it started from.
func (d *editDoc) vertical(n, width, tabw int, wrap, extend bool) {
	line := d.lineOf(d.head)
	rows := d.rows(line, width, tabw, wrap)
	ri, x := rowOf(rows, d.head)
	if d.goal < 0 {
		d.goal = x
	}
	goal := d.goal
	for ; n > 0; n-- {
		if ri++; ri >= len(rows) {
			if line+1 >= d.lineCount() {
				d.moveTo(len(d.text), extend)
				d.goal = goal
				return
			}
			line++
			rows, ri = d.rows(line, width, tabw, wrap), 0
		}
	}
	for ; n < 0; n++ {
		if ri--; ri < 0 {
			if line == 0 {
				d.moveTo(0, extend)
				d.goal = goal
				return
			}
			line--
			rows = d.rows(line, width, tabw, wrap)
			ri = len(rows) - 1
		}
	}
	d.moveTo(posAt(rows[ri], goal, false), extend)
	d.goal = goal
}
