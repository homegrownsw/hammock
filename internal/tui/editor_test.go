package tui

import (
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/textbuf"
)

func TestEditorKeysOf(t *testing.T) {
	cases := map[string]edKey{
		"\x1b[1;2D":     {name: "left", shift: true},
		"\x1b[1;3C":     {name: "right", alt: true},
		"\x1b[1;4A":     {name: "up", shift: true, alt: true},
		"\x1b[1;5H":     {name: "home", ctrl: true},
		"\x1b[3~":       {name: "delete"},
		"\x1b[5;2~":     {name: "pgup", shift: true},
		"\x1bb":         {name: "left", alt: true},
		"\x1bf":         {name: "right", alt: true},
		"\x1b\x7f":      {name: "backspace", alt: true},
		"\x1a":          {name: "z", ctrl: true},
		"\x1b[122;6u":   {name: "z", ctrl: true, shift: true},
		"\x1b[27;6;90~": {name: "z", ctrl: true, shift: true},
		"\x1b[Z":        {name: "tab", shift: true},
		"\r":            {name: "enter"},
		"é":             {r: 'é'},
		"\x1br":         {r: 'r', alt: true},
		"\x1b":          {name: "esc"},
	}
	for in, want := range cases {
		got := editorKeysOf([]byte(in))
		if len(got) != 1 || got[0] != want {
			t.Errorf("%q: %+v, want %+v", in, got, want)
		}
	}
	if got := editorKeysOf([]byte("ab\x1b[D")); len(got) != 3 || got[2].name != "left" {
		t.Errorf("a run of keys: %+v", got)
	}
}

// editorApp is an App drawing one editor pane, without a daemon.
func editorApp(text string) (*App, *editorView) {
	a := &App{
		anim: newSprings(), th: newTheme(RGB{0, 0, 0}, RGB{255, 255, 255}), scr: NewScreen(40, 10),
		tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{}, editors: map[string]*editorView{}, paneRects: map[string]rect{},
	}
	a.panes["p"] = &pb.Pane{Id: "p", Kind: pb.PaneKind_PANE_KIND_EDITOR, Editor: &pb.EditorInfo{Name: "a.txt", Path: "/x/a.txt"}}
	v := &editorView{id: "p", doc: newEditDoc()}
	v.doc.reset(text, 0)
	v.loaded = true
	a.editors["p"] = v
	a.focus = "p"
	return a, v
}

func screenRow(s *Screen, y, x, w int) string {
	var b strings.Builder
	for i := x; i < x+w; i++ {
		c := s.Get(i, y)
		if c.Cont {
			continue
		}
		if c.Ch == "" {
			b.WriteByte(' ')
		} else {
			b.WriteString(c.Ch)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func TestDrawEditor(t *testing.T) {
	old := editorPrefs
	defer func() { editorPrefs = old }()
	editorPrefs.LineNumbers, editorPrefs.Wrap, editorPrefs.TabWidth = true, false, 4

	a, v := editorApp("one\n\ttwo 漢字\nthree")
	v.doc.moveTo(5, false) // after the tab
	cx, cy, show := a.drawEditor(a.panes["p"], rect{0, 0, 20, 5}, true)
	if got := screenRow(a.scr, 0, 0, 20); got != "  1 one" {
		t.Fatalf("row 0: %q", got)
	}
	if got := screenRow(a.scr, 1, 0, 20); got != "  2     two 漢字" {
		t.Fatalf("row 1: %q", got)
	}
	// The gutter is the git bar, two digits and a space, then a space: 4+4.
	if !show || cx != 8 || cy != 1 {
		t.Fatalf("cursor %d,%d %v; want 8,1", cx, cy, show)
	}
	if got := len(v.shown); got != 3 {
		t.Fatalf("%d rows shown", got)
	}
	if pos, _ := a.editorPosAt(v, 8, 1); pos != 5 {
		t.Fatalf("a click after the tab lands at %d", pos)
	}

	// Wrapped at the pane's width; a long line scrolls into view.
	editorPrefs.Wrap = true
	a, v = editorApp(strings.Repeat("abcdefghij", 5))
	v.doc.moveTo(48, false)
	v.reveal = 1
	cx, cy, _ = a.drawEditor(a.panes["p"], rect{0, 0, 14, 3}, true) // 10 cells of text a row
	if v.top != 0 || v.topSub != 2 {
		t.Fatalf("scrolled to %d/%d, want 0/2", v.top, v.topSub)
	}
	if cx != 4+8 || cy != 2 {
		t.Fatalf("cursor at %d,%d", cx, cy)
	}

	// The git marks and a selection.
	editorPrefs.Wrap = false
	a, v = editorApp("a\nb\nc")
	v.marks = []*pb.GitMark{{Kind: pb.GitMark_KIND_ADDED, From: 2, To: 2}}
	v.doc.moveTo(0, false)
	v.doc.moveTo(3, true)
	a.drawEditor(a.panes["p"], rect{0, 0, 20, 5}, true)
	if c := a.scr.Get(0, 1); c.Ch != "▎" || c.FG != a.th.green {
		t.Fatalf("added line mark: %+v", c)
	}
	if c := a.scr.Get(4, 1); c.BG != mix(a.th.bg, a.th.accent, 0.38) {
		t.Fatalf("selected b isn't highlighted")
	}
}

// The external editor writes the file while someone else has changed the
// buffer: their change stays, the file's is pushed over it.
func TestExternalApply(t *testing.T) {
	ext, other := newEditDoc(), newEditDoc()
	s := newFakeServer("hello world\n", ext, other)
	sess := &extSession{base: textbuf.FromString("hello world\n")}
	other.moveTo(0, false)
	other.insert("Hi! ")
	s.flush(t, other)
	s.deliver(t, ext)
	// Noted as extRemote does.
	sess.since = append(sess.since, textbuf.Diff(textbuf.FromString("hello world\n"), ext.text))

	if !extApply(sess, ext, "hello there world\r\n") {
		t.Fatal("the written file changed nothing")
	}
	if got := ext.text.String(); got != "Hi! hello there world\n" {
		t.Fatalf("buffer: %q", got)
	}
	s.settle(t, ext, other)
	if got := s.text.String(); got != "Hi! hello there world\n" {
		t.Fatalf("server: %q", got)
	}
	// Written again unchanged: nothing to push.
	if extApply(sess, ext, "hello there world\n") {
		t.Fatal("an unchanged file pushed something")
	}
	// The file is now in step with the buffer bar the remote change.
	if len(sess.since) != 1 {
		t.Fatalf("since: %d", len(sess.since))
	}
}

func TestUnifiedDiff(t *testing.T) {
	disk := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj"
	mine := "a\nb\nC\nd\ne\nf\ng\nh\ni\nj\nk"
	got := unifiedDiff(disk, mine, 1)
	var b strings.Builder
	for _, l := range got {
		b.WriteString(string(l.kind) + l.text + "\n")
	}
	want := "@Line 2 (disk) / 2 (yours)\n b\n-c\n+C\n d\n@Line 10 (disk) / 10 (yours)\n j\n+k\n"
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}
}

// Closing the last pane on a file with unsaved edits asks; another pane on
// it, or a clean buffer, doesn't.
func TestUnsavedEditors(t *testing.T) {
	a := &App{panes: map[string]*pb.Pane{}}
	ed := func(id, path string, dirty bool) {
		a.panes[id] = &pb.Pane{Id: id, Kind: pb.PaneKind_PANE_KIND_EDITOR, Editor: &pb.EditorInfo{Path: path, Dirty: dirty, Name: id}}
	}
	ed("a1", "/a", true)
	ed("a2", "/a", true)
	ed("b", "/b", false)
	ed("u", "", true)
	if got := a.unsavedEditors([]string{"a1"}); len(got) != 0 {
		t.Fatalf("a1 shares its file: %v", got)
	}
	if got := a.unsavedEditors([]string{"a1", "a2"}); len(got) != 2 {
		t.Fatalf("both panes on /a closing: %v", got)
	}
	if got := a.unsavedEditors([]string{"b", "u"}); len(got) != 1 || got[0].Id != "u" {
		t.Fatalf("clean b, dirty untitled u: %v", got)
	}
}

func TestQuickOpenRows(t *testing.T) {
	q := &quickOpen{query: "new.go", paths: []string{"cmd/main.go", "new.go"}}
	if rows := q.rows(); len(rows) != 2 {
		t.Fatalf("a typed path in the list isn't offered twice: %+v", rows)
	}
	q.query = "docs/new.md"
	if rows := q.rows(); len(rows) != 3 || !rows[2].typed || rows[2].path != "docs/new.md" {
		t.Fatalf("a typed path opens as typed: %+v", rows)
	}
}
