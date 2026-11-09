package main

import (
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/textbuf"
)

func TestSplitPathPos(t *testing.T) {
	none := func(string) bool { return false }
	for _, c := range []struct {
		arg  string
		path string
		r    textRange
	}{
		{"main.go", "main.go", textRange{}},
		{"main.go:12", "main.go", textRange{Line: 12}},
		{"main.go:12:5", "main.go", textRange{Line: 12, Col: 5}},
		{"main.go:12-14", "main.go", textRange{Line: 12, EndLine: 14, EndCol: wholeLine}},
		{"main.go:12:5-14:2", "main.go", textRange{Line: 12, Col: 5, EndLine: 14, EndCol: 2}},
		{"/a:b/c.go:3", "/a:b/c.go", textRange{Line: 3}},
		{"notes:txt", "notes:txt", textRange{}},
	} {
		path, r, err := splitPathPos(c.arg, none)
		if err != nil || path != c.path || r != c.r {
			t.Errorf("%q: %q %+v %v, want %q %+v", c.arg, path, r, err, c.path, c.r)
		}
	}
	// A file really named so is opened as it is.
	path, r, _ := splitPathPos("odd:12", func(p string) bool { return p == "odd:12" })
	if path != "odd:12" || r != (textRange{}) {
		t.Errorf("existing odd:12: %q %+v", path, r)
	}
	if _, _, err := splitPathPos("a.go:0", none); err == nil {
		t.Error("line 0 accepted")
	}
	if _, _, err := splitPathPos("a.go:9-3", none); err == nil {
		t.Error("backwards range accepted")
	}
}

func TestParsePos(t *testing.T) {
	if l, c, err := parsePos("7:3"); err != nil || l != 7 || c != 3 {
		t.Errorf("7:3: %d %d %v", l, c, err)
	}
	for _, bad := range []string{"", "x", "3:", "3:0", "-1"} {
		if _, _, err := parsePos(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseLines(t *testing.T) {
	for _, c := range []struct {
		s        string
		from, to int
	}{{"5", 5, 5}, {"5-9", 5, 9}, {"5-", 5, 0}} {
		if f, to, err := parseLines(c.s); err != nil || f != c.from || to != c.to {
			t.Errorf("%q: %d %d %v", c.s, f, to, err)
		}
	}
	if _, _, err := parseLines("9-5"); err == nil {
		t.Error("9-5 accepted")
	}
}

func TestMCPRange(t *testing.T) {
	for _, c := range []struct {
		l, c, el, ec int
		want         textRange
	}{
		{0, 0, 0, 0, textRange{}},
		{4, 0, 0, 0, textRange{Line: 4, EndLine: 4, EndCol: wholeLine}}, // a line alone: all of it
		{4, 2, 0, 0, textRange{Line: 4, Col: 2}},                        // a column: the cursor there
		{4, 0, 6, 0, textRange{Line: 4, EndLine: 6, EndCol: wholeLine}},
		{4, 2, 6, 3, textRange{Line: 4, Col: 2, EndLine: 6, EndCol: 3}},
	} {
		if r, err := mcpRange(c.l, c.c, c.el, c.ec); err != nil || r != c.want {
			t.Errorf("%v: %+v %v, want %+v", c, r, err, c.want)
		}
	}
	if _, err := mcpRange(5, 0, 2, 0); err == nil {
		t.Error("backwards range accepted")
	}
	if _, err := mcpRange(0, 3, 0, 0); err == nil {
		t.Error("column without a line accepted")
	}
}

func TestDescribeSelection(t *testing.T) {
	// "😀" is two UTF-16 units: offsets count units, columns too.
	text := textbuf.FromString("one\nt😀o\nthree\n")
	s := describeSelection(text, &pb.EditorSelection{Anchor: 11, Head: 5}) // backwards: head first
	if s.From != "2:2" || s.To != "3:3" || s.Text != "😀o\nth" || s.Empty {
		t.Errorf("selection %+v", s)
	}
	if c := describeSelection(text, &pb.EditorSelection{Anchor: 4, Head: 4}); c.From != "2:1" || !c.Empty || c.Text != "" {
		t.Errorf("cursor %+v", c)
	}
	if describeSelection(text, nil) != nil {
		t.Error("no selection described")
	}
	// Offsets past the end (a stale selection) are clamped, not a panic.
	if s := describeSelection(text, &pb.EditorSelection{Anchor: 0, Head: 999}); s.To != "4:1" {
		t.Errorf("clamped %+v", s)
	}
	// What the daemon would select for 2:2-3:3 comes back as that.
	from, to := text.Offset(2, 2), text.Offset(3, 3)
	if s := describeSelection(text, &pb.EditorSelection{Anchor: uint32(from), Head: uint32(to)}); s.From != "2:2" || s.To != "3:3" {
		t.Errorf("round trip %+v", s)
	}
}

func TestLineSlice(t *testing.T) {
	text := "a\nb\nc\n"
	if s, n, sh := lineSlice(text, 0, 0, false); s != text || n != 3 || sh != "" {
		t.Errorf("all: %q %d %q", s, n, sh)
	}
	if s, _, sh := lineSlice(text, 2, 0, true); s != "     2\tb\n     3\tc\n" || sh != "2-3" {
		t.Errorf("numbered from 2: %q %q", s, sh)
	}
	if s, _, sh := lineSlice(text, 0, 10, false); s != text || sh != "" {
		t.Errorf("past the end: %q %q", s, sh)
	}
	if s, _, sh := lineSlice("x\ny", 1, 1, false); s != "x\n" || sh != "1-1" {
		t.Errorf("first line: %q %q", s, sh)
	}
	if s, _, _ := lineSlice(text, 9, 12, false); s != "" {
		t.Errorf("beyond: %q", s)
	}
}
