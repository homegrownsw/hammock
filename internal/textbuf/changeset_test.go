package textbuf

import (
	"math/rand"
	"strings"
	"testing"
)

func mustParse(t *testing.T, s string) ChangeSet {
	t.Helper()
	c, err := ParseJSON(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestJSONRoundTrip(t *testing.T) {
	for _, s := range []string{`[5]`, `[2,[0,"hi"],3]`, `[[3],2]`, `[1,[2,"a","b"],2]`, `[[0,"x"]]`, `[2,[0,"é😀"],1]`} {
		c := mustParse(t, s)
		if got := c.JSON(); got != s {
			t.Errorf("%s → %s", s, got)
		}
	}
}

func TestApply(t *testing.T) {
	doc := FromString("hello world")
	c := mustParse(t, `[5,[0,","],1,[5,"there"]]`)
	out, err := c.Apply(doc)
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello, there" {
		t.Fatalf("got %q", out.String())
	}
	if _, err := mustParse(t, `[3]`).Apply(doc); err == nil {
		t.Fatal("applied changes for the wrong length")
	}
	// UTF-16: the emoji is two units.
	e := FromString("a😀b")
	out, _ = mustParse(t, `[3,[1,"c"]]`).Apply(e)
	if out.String() != "a😀c" {
		t.Fatalf("utf16: %q", out.String())
	}
}

func TestMapTies(t *testing.T) {
	// Both insert at 2: the remote one (other) goes first when mapping the
	// local over it (before=false), and the two orders agree.
	doc := FromString("abcd")
	local, _ := Build(4, []Replace{{2, 2, FromString("L")}})
	remote, _ := Build(4, []Replace{{2, 2, FromString("R")}})
	viaRemote, _ := remote.Apply(doc)
	viaRemote, _ = local.Map(remote, false).Apply(viaRemote)
	viaLocal, _ := local.Apply(doc)
	viaLocal, _ = remote.Map(local, true).Apply(viaLocal)
	if viaRemote.String() != "abRLcd" || viaLocal.String() != viaRemote.String() {
		t.Fatalf("remote-first %q, local-first %q", viaRemote, viaLocal)
	}
}

func randChanges(r *rand.Rand, n int) ChangeSet {
	var rs []Replace
	pos := 0
	for pos <= n && r.Intn(3) > 0 {
		from := pos + r.Intn(n-pos+1)
		to := from + r.Intn(n-from+1)/2
		var ins Text
		if r.Intn(3) > 0 {
			ins = FromString(strings.Repeat(string(rune('A'+r.Intn(26))), 1+r.Intn(3)))
		}
		rs = append(rs, Replace{from, to, ins})
		pos = to + 1
		if pos > n {
			break
		}
	}
	c, err := Build(n, rs)
	if err != nil {
		panic(err)
	}
	return c
}

// The property collab rests on: applying a then b-mapped-over-a gives what
// b then a-mapped-over-b does.
func TestMapConverges(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		src := strings.Repeat("0123456789", 3); doc := FromString(src[:1+r.Intn(25)])
		a, b := randChanges(r, len(doc)), randChanges(r, len(doc))
		da, _ := a.Apply(doc)
		x, err := b.Map(a, true).Apply(da)
		if err != nil {
			t.Fatal(err)
		}
		db, _ := b.Apply(doc)
		y, err := a.Map(b, false).Apply(db)
		if err != nil {
			t.Fatal(err)
		}
		if x.String() != y.String() {
			t.Fatalf("doc %q a %s b %s: %q vs %q", doc, a.JSON(), b.JSON(), x, y)
		}
	}
}

func TestDiff(t *testing.T) {
	a, b := FromString("one two three"), FromString("one 2 three")
	c := Diff(a, b)
	out, _ := c.Apply(a)
	if out.String() != "one 2 three" || c.JSON() != `[4,[3,"2"],6]` {
		t.Fatalf("%s → %q", c.JSON(), out)
	}
	if !Diff(a, a).Empty() {
		t.Fatal("same text isn't empty")
	}
}

func TestLines(t *testing.T) {
	doc := FromString("ab\ncde\n\nf")
	if got := doc.Offset(2, 2); got != 4 {
		t.Errorf("2:2 = %d", got)
	}
	if got := doc.Offset(2, 99); got != 6 {
		t.Errorf("2:99 = %d", got)
	}
	if got := doc.Offset(99, 1); got != len(doc) {
		t.Errorf("99:1 = %d", got)
	}
	if l, c := doc.LineCol(7); l != 3 || c != 1 {
		t.Errorf("LineCol(7) = %d:%d", l, c)
	}
}

func TestDiffLines(t *testing.T) {
	a := []string{"a", "b", "c", "d", "e"}
	b := []string{"a", "B", "c", "e", "f"}
	got := DiffLines(a, b)
	want := []LineChange{{1, 2, 1, 2}, {3, 4, 3, 3}, {5, 5, 4, 5}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v, want %v", got, want)
		}
	}
}

// DiffLines' runs, applied to a, make b, and keep what's common (random
// files); a long file with changes at both ends stays quick and small.
func TestDiffLinesRebuilds(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	words := []string{"a", "b", "c", "d"}
	for i := 0; i < 300; i++ {
		a := make([]string, r.Intn(30))
		for j := range a {
			a[j] = words[r.Intn(len(words))]
		}
		b := make([]string, r.Intn(30))
		for j := range b {
			b[j] = words[r.Intn(len(words))]
		}
		var out []string
		at := 0
		for _, c := range DiffLines(a, b) {
			if c.OldFrom < at || c.OldTo < c.OldFrom {
				t.Fatalf("%v → %v: runs out of order: %v", a, b, DiffLines(a, b))
			}
			out = append(out, a[at:c.OldFrom]...)
			out = append(out, b[c.NewFrom:c.NewTo]...)
			at = c.OldTo
		}
		out = append(out, a[at:]...)
		if strings.Join(out, ",") != strings.Join(b, ",") {
			t.Fatalf("%v → %v: rebuilt %v", a, b, out)
		}
	}
	long := make([]string, 200000)
	for i := range long {
		long[i] = strings.Repeat("x", i%7)
	}
	edited := append([]string{"top"}, long...)
	edited[len(edited)/2] = "middle"
	edited = append(edited, "bottom")
	if got := DiffLines(long, edited); len(got) != 3 {
		t.Fatalf("long file: %d runs", len(got))
	}
}
