package tui

import (
	"math/rand"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/textbuf"
)

// fakeEditorServer is the daemon's side of the collab protocol (editor.go's
// push and broadcast), with each client's messages queued so a test decides
// when they arrive.
type fakeEditorServer struct {
	text    textbuf.Text
	version uint64
	inbox   map[*editDoc][]fakeMsg
}

// fakeMsg is a broadcast (updates) or a push's reply.
type fakeMsg struct {
	updates  []*pb.EditorChange
	reply    bool
	accepted bool
	at       uint64
}

func newFakeServer(text string, docs ...*editDoc) *fakeEditorServer {
	s := &fakeEditorServer{text: textbuf.FromString(text), inbox: map[*editDoc][]fakeMsg{}}
	for _, d := range docs {
		d.reset(text, 0)
		s.inbox[d] = nil
	}
	return s
}

// push is EditorPush from d: taken at the current version only, and then
// sent to everyone, d included, ahead of d's reply.
func (s *fakeEditorServer) push(t *testing.T, d *editDoc, m *pb.EditorPush) {
	t.Helper()
	if m.Version != s.version {
		s.inbox[d] = append(s.inbox[d], fakeMsg{reply: true, at: m.Version})
		return
	}
	for _, u := range m.Updates {
		cs, err := textbuf.ParseJSON(u.Changes)
		if err != nil {
			t.Fatal(err)
		}
		if s.text, err = cs.Apply(s.text); err != nil {
			t.Fatalf("server can't apply %s to %q: %v", u.Changes, s.text, err)
		}
	}
	s.version += uint64(len(m.Updates))
	for c := range s.inbox {
		s.inbox[c] = append(s.inbox[c], fakeMsg{updates: m.Updates})
	}
	s.inbox[d] = append(s.inbox[d], fakeMsg{reply: true, accepted: true, at: m.Version})
}

// deliver hands d its next message, and pushes whatever d has to push.
func (s *fakeEditorServer) deliver(t *testing.T, d *editDoc) bool {
	t.Helper()
	if len(s.inbox[d]) == 0 {
		return false
	}
	m := s.inbox[d][0]
	s.inbox[d] = s.inbox[d][1:]
	if m.reply {
		d.pushed(m.accepted, m.at)
	} else if _, err := d.receive(m.updates); err != nil {
		t.Fatal(err)
	}
	s.flush(t, d)
	return true
}

func (s *fakeEditorServer) flush(t *testing.T, d *editDoc) bool {
	if p := d.toPush(); p != nil {
		s.push(t, d, p)
		return true
	}
	return false
}

func (s *fakeEditorServer) settle(t *testing.T, docs ...*editDoc) {
	t.Helper()
	for more := true; more; {
		more = false
		for _, d := range docs {
			for s.deliver(t, d) {
				more = true
			}
			if s.flush(t, d) {
				more = true
			}
		}
	}
	for i, d := range docs {
		if d.text.String() != s.text.String() {
			t.Fatalf("client %d has %q, the server %q", i, d.text, s.text)
		}
		if len(d.unconfirmed) != 0 || d.version != s.version {
			t.Fatalf("client %d: %d unconfirmed at v%d (server v%d)", i, len(d.unconfirmed), d.version, s.version)
		}
	}
}

// Two clients typing at once: the second's push is refused, it rebases
// over the first's and its edit lands after it; both end with the same text.
func TestEditorRebase(t *testing.T) {
	a, b := newEditDoc(), newEditDoc()
	s := newFakeServer("hello world", a, b)
	a.moveTo(5, false)
	a.insert(",")
	b.moveTo(11, false)
	b.insert("!")
	s.push(t, a, a.toPush())
	s.push(t, b, b.toPush()) // made at version 0, but the server's at 1: refused
	if b.inFlight != 1 {
		t.Fatalf("b's push should be in flight")
	}
	// b hears a's edit, then the refusal, then pushes again rebased.
	s.deliver(t, b)
	if got := b.text.String(); got != "hello, world!" {
		t.Fatalf("b after a's edit: %q", got)
	}
	if b.head != 13 {
		t.Fatalf("b's cursor at %d, want 13 (it moved with a's comma)", b.head)
	}
	s.settle(t, a, b)
	if got := s.text.String(); got != "hello, world!" {
		t.Fatalf("server: %q", got)
	}
}

// The same place: the daemon's (earlier) insertion goes first.
func TestEditorRebaseSamePlace(t *testing.T) {
	a, b := newEditDoc(), newEditDoc()
	s := newFakeServer("ab", a, b)
	a.moveTo(1, false)
	b.moveTo(1, false)
	a.insert("X")
	b.insert("Y")
	s.flush(t, a)
	s.flush(t, b)
	s.settle(t, a, b)
	if got := s.text.String(); got != "aXYb" {
		t.Fatalf("got %q", got)
	}
}

// Edits made while a push is in flight wait for it, then go together.
func TestEditorOneInFlight(t *testing.T) {
	a := newEditDoc()
	s := newFakeServer("", a)
	a.insert("a")
	p := a.toPush()
	a.insert("b")
	if a.toPush() != nil {
		t.Fatal("a second push while one is in flight")
	}
	s.push(t, a, p)
	s.settle(t, a)
	if s.text.String() != "ab" {
		t.Fatalf("got %q", s.text)
	}
}

// Random edits from three clients, delivered in random order: they all
// end up with the server's text.
func TestEditorRebaseRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	words := []string{"a", "bc", "😀", "é", "\n", "xyz", "  "}
	for round := 0; round < 200; round++ {
		docs := []*editDoc{newEditDoc(), newEditDoc(), newEditDoc()}
		s := newFakeServer("The quick brown fox\njumps over\nthe lazy dog", docs...)
		for step := 0; step < 30; step++ {
			d := docs[rng.Intn(len(docs))]
			switch rng.Intn(4) {
			case 0, 1:
				d.moveTo(rng.Intn(len(d.text)+1), false)
				d.moveTo(d.prevBoundary(d.nextBoundary(d.head)), false) // not inside a surrogate pair
				d.insert(words[rng.Intn(len(words))])
			case 2:
				d.moveTo(rng.Intn(len(d.text)+1), false)
				d.moveTo(d.prevBoundary(d.nextBoundary(d.head)), false)
				d.backspace()
			case 3:
				if rng.Intn(2) == 0 {
					d.undoLast()
				} else {
					d.redoLast()
				}
			}
			if rng.Intn(2) == 0 {
				s.flush(t, d)
			}
			for range rng.Intn(3) {
				s.deliver(t, docs[rng.Intn(len(docs))])
			}
		}
		s.settle(t, docs...)
	}
}

func TestEditorEditing(t *testing.T) {
	d := newEditDoc()
	d.reset("    foo", 0)
	d.lineEnd(false)
	d.newline()
	if got := d.text.String(); got != "    foo\n    " || d.head != 12 {
		t.Fatalf("newline keeps indentation: %q at %d", got, d.head)
	}
	d.insert("b")
	d.insert("a")
	d.insert("r")
	if got := d.text.String(); got != "    foo\n    bar" {
		t.Fatalf("typing: %q", got)
	}
	d.backspace()
	d.backspace()
	if got := d.text.String(); got != "    foo\n    b" {
		t.Fatalf("backspace: %q", got)
	}
	// Two backspaces are one step; typing "bar" was one; the newline one.
	d.undoLast()
	if got := d.text.String(); got != "    foo\n    bar" {
		t.Fatalf("undo deleting: %q", got)
	}
	d.undoLast()
	if got := d.text.String(); got != "    foo\n    " {
		t.Fatalf("undo typing: %q", got)
	}
	d.undoLast()
	if got := d.text.String(); got != "    foo" {
		t.Fatalf("undo newline: %q", got)
	}
	d.redoLast()
	d.redoLast()
	if got := d.text.String(); got != "    foo\n    bar" {
		t.Fatalf("redo: %q", got)
	}
	// Undo pushes inverse changes like any edit.
	// Undo and redo push inverse changes like any edit: 6 edits, then a
	// change for each one undone (6) and redone (4).
	if n := len(d.unconfirmed); n != 16 {
		t.Fatalf("%d unconfirmed changes, want 16", n)
	}

	// Delete a selection, forward.
	d.moveTo(4, false)
	d.moveTo(7, true)
	if d.selText() != "foo" {
		t.Fatalf("selected %q", d.selText())
	}
	d.deleteForward()
	if got := d.text.String(); got != "    \n    bar" {
		t.Fatalf("delete selection: %q", got)
	}
}

// Undo after someone else's edit takes back only this pane's own.
func TestEditorUndoOverRemote(t *testing.T) {
	a, b := newEditDoc(), newEditDoc()
	s := newFakeServer("one two", a, b)
	a.moveTo(7, false)
	a.insert("3")
	s.settle(t, a, b)
	b.moveTo(0, false)
	b.insert("zero ")
	s.settle(t, a, b)
	a.undoLast()
	s.settle(t, a, b)
	if got := s.text.String(); got != "zero one two" {
		t.Fatalf("got %q", got)
	}
}

// Moving by word and by character counts UTF-16 units: an emoji is two.
func TestEditorWordMovement(t *testing.T) {
	d := newEditDoc()
	d.reset("let 😀x = foo_bar(1);\nnext", 0)
	var stops []int
	for i := 0; i < 9; i++ {
		d.wordRight(false)
		stops = append(stops, d.head)
	}
	// "let" 3, "😀" is a symbol (2 units): 6, "x" 7, " =" 9, " foo_bar" 17, "(" 18, "1" 19, ");" 21, newline 22
	want := []int{3, 6, 7, 9, 17, 18, 19, 21, 22}
	for i := range want {
		if stops[i] != want[i] {
			t.Fatalf("word right stops %v, want %v", stops, want)
		}
	}
	d.moveTo(6, false)
	d.left(false)
	if d.head != 4 {
		t.Fatalf("left over the emoji went to %d, want 4", d.head)
	}
	d.right(false)
	if d.head != 6 {
		t.Fatalf("right over the emoji went to %d, want 6", d.head)
	}
	d.moveTo(17, false)
	d.wordLeft(false)
	if d.head != 10 {
		t.Fatalf("word left to %d, want 10", d.head)
	}
	d.wordLeft(true)
	if d.head != 8 || d.anchor != 10 {
		t.Fatalf("shift-word-left: %d..%d", d.anchor, d.head)
	}
	d.moveTo(6, false)
	d.backspace()
	if got := d.text.String(); got != "let x = foo_bar(1);\nnext" {
		t.Fatalf("backspace over the emoji: %q", got)
	}
}

// Up and down keep the column, over tabs and wide characters, and wrapped
// rows.
func TestEditorVertical(t *testing.T) {
	d := newEditDoc()
	d.reset("abcdef\n\tx\n漢字かな\nab", 0)
	d.moveTo(4, false) // abcd|ef: column 4
	d.vertical(1, 80, 4, false, false)
	if d.head != 8 { // \t is columns 0-3, x is 4: before x
		t.Fatalf("down over a tab: %d", d.head)
	}
	d.vertical(1, 80, 4, false, false)
	if d.head != 12 { // 漢字 is 4 columns: before か
		t.Fatalf("down over wide characters: %d", d.head)
	}
	d.vertical(1, 80, 4, false, false)
	if d.head != len(d.text) {
		t.Fatalf("down to a shorter line: %d", d.head)
	}
	d.vertical(-3, 80, 4, false, false)
	if d.head != 4 {
		t.Fatalf("back up to the goal column: %d", d.head)
	}
	// Wrapped at 4 cells: "abcd" / "ef".
	d.reset("abcdef\nxy", 0)
	d.moveTo(1, false)
	d.vertical(1, 4, 4, true, false)
	if d.head != 5 {
		t.Fatalf("down a wrapped row: %d", d.head)
	}
	d.vertical(1, 4, 4, true, false)
	if d.head != 8 {
		t.Fatalf("down to the next line: %d", d.head)
	}
	rows := d.rows(0, 4, 4, true)
	if len(rows) != 2 || rows[0].to != 4 || rows[1].from != 4 || !rows[1].last {
		t.Fatalf("rows: %+v", rows)
	}
	if ri, x := rowOf(rows, 4); ri != 1 || x != 0 {
		t.Fatalf("the wrap point shows at the next row's start: %d,%d", ri, x)
	}
}

func TestEditorIndent(t *testing.T) {
	d := newEditDoc()
	d.reset("a\nb\n", 0)
	d.moveTo(0, false)
	d.moveTo(3, true)
	d.tab(false, 2)
	if got := d.text.String(); got != "  a\n  b\n" {
		t.Fatalf("indent lines: %q", got)
	}
	d.outdent(2)
	if got := d.text.String(); got != "a\nb\n" {
		t.Fatalf("outdent: %q", got)
	}
	d.moveTo(1, false)
	d.tab(false, 4)
	if got := d.text.String(); got != "a   \nb\n" {
		t.Fatalf("tab to the next stop: %q", got)
	}
	d.tab(true, 4)
	if got := d.text.String(); got != "a   \t\nb\n" {
		t.Fatalf("tab character: %q", got)
	}
}

// Updates that skip a version are refused whole (the pane asks for the text
// again); ones a snapshot already had are passed over.
func TestEditorReceiveOutOfStep(t *testing.T) {
	d := newEditDoc()
	d.reset("abc", 3)
	ins := func(at int, s string) *pb.EditorChange {
		cs, _ := textbuf.Build(len(d.text), []textbuf.Replace{{From: at, To: at, Text: textbuf.FromString(s)}})
		return &pb.EditorChange{Changes: cs.JSON(), ClientId: "other"}
	}
	if _, err := d.receiveAt(5, []*pb.EditorChange{ins(0, "x")}); err != errOutOfStep {
		t.Fatalf("a gap: %v", err)
	}
	if d.text.String() != "abc" || d.version != 3 {
		t.Fatalf("after a gap: %q v%d", d.text.String(), d.version)
	}
	if _, err := d.receiveAt(3, []*pb.EditorChange{ins(0, "x")}); err != nil || d.text.String() != "abc" {
		t.Fatalf("already had: %v %q", err, d.text.String())
	}
	if _, err := d.receiveAt(4, []*pb.EditorChange{ins(3, "d")}); err != nil || d.text.String() != "abcd" || d.version != 4 {
		t.Fatalf("in step: %v %q v%d", err, d.text.String(), d.version)
	}
}
