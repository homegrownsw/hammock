// Package textbuf is editor panes' text: CodeMirror ChangeSets (their JSON,
// applying them, mapping one over another, as @codemirror/collab rebases),
// positions and lines, all in UTF-16 code units as CodeMirror counts them.
// The daemon applies the updates clients push; the TUI's editor also
// rebases its own over those it receives.
package textbuf

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Text is a document as UTF-16 code units.
type Text []uint16

// FromString is s as Text.
func FromString(s string) Text { return Text(utf16.Encode([]rune(s))) }

func (t Text) String() string { return string(utf16.Decode(t)) }

// section is a run of the old document: kept (ins < 0), or replaced by
// text (len characters deleted, text inserted).
type section struct {
	len  int
	ins  int // -1: kept
	text Text
}

// ChangeSet is a set of changes to a document of a known length.
type ChangeSet struct {
	secs []section
}

// Len is the length of the document the changes apply to.
func (c ChangeSet) Len() int {
	n := 0
	for _, s := range c.secs {
		n += s.len
	}
	return n
}

// NewLen is the length after they're applied.
func (c ChangeSet) NewLen() int {
	n := 0
	for _, s := range c.secs {
		if s.ins < 0 {
			n += s.len
		} else {
			n += s.ins
		}
	}
	return n
}

// Empty reports whether it changes nothing.
func (c ChangeSet) Empty() bool {
	for _, s := range c.secs {
		if s.ins >= 0 && (s.len > 0 || s.ins > 0) {
			return false
		}
	}
	return true
}

// Replace is one change, in the old document's positions.
type Replace struct {
	From, To int
	Text     Text
}

// Build makes a change set for a document of length n from replacements
// sorted by position and not overlapping.
func Build(n int, rs []Replace) (ChangeSet, error) {
	var c ChangeSet
	pos := 0
	for _, r := range rs {
		if r.From < pos || r.To < r.From || r.To > n {
			return ChangeSet{}, fmt.Errorf("replacement %d–%d out of order or past the end (%d)", r.From, r.To, n)
		}
		c.keep(r.From - pos)
		c.replace(r.To-r.From, r.Text)
		pos = r.To
	}
	c.keep(n - pos)
	return c, nil
}

func (c *ChangeSet) keep(n int) {
	if n <= 0 {
		return
	}
	if k := len(c.secs); k > 0 && c.secs[k-1].ins < 0 {
		c.secs[k-1].len += n
		return
	}
	c.secs = append(c.secs, section{len: n, ins: -1})
}

func (c *ChangeSet) replace(del int, t Text) {
	if del == 0 && len(t) == 0 {
		return
	}
	if k := len(c.secs); k > 0 && c.secs[k-1].ins >= 0 {
		last := &c.secs[k-1]
		last.len += del
		last.text = append(append(Text(nil), last.text...), t...)
		last.ins = len(last.text)
		return
	}
	c.secs = append(c.secs, section{len: del, ins: len(t), text: t})
}

// Replacements are its changes, in the old document's positions.
func (c ChangeSet) Replacements() []Replace {
	var out []Replace
	pos := 0
	for _, s := range c.secs {
		if s.ins >= 0 {
			out = append(out, Replace{From: pos, To: pos + s.len, Text: s.text})
		}
		pos += s.len
	}
	return out
}

// Apply makes the changed document.
func (c ChangeSet) Apply(t Text) (Text, error) {
	if c.Len() != len(t) {
		return nil, fmt.Errorf("changes for a document of %d characters, not %d", c.Len(), len(t))
	}
	out := make(Text, 0, c.NewLen())
	pos := 0
	for _, s := range c.secs {
		if s.ins < 0 {
			out = append(out, t[pos:pos+s.len]...)
		} else {
			out = append(out, s.text...)
		}
		pos += s.len
	}
	return out, nil
}

// MapPos is where pos (in the old document) is in the new one. assoc < 0
// keeps it before text inserted at it, > 0 after; a position inside
// deleted text goes after what replaced it.
func (c ChangeSet) MapPos(pos, assoc int) int {
	old, nw := 0, 0
	for _, s := range c.secs {
		end := old + s.len
		if s.ins < 0 {
			if pos < end {
				return nw + max(pos-old, 0)
			}
			nw += s.len
		} else {
			switch {
			case s.len == 0 && pos == old:
				if assoc < 0 {
					return nw
				}
			case s.len > 0 && pos == old && assoc < 0:
				return nw
			case pos > old && pos < end:
				return nw + s.ins
			}
			nw += s.ins
		}
		old = end
	}
	return nw
}

// Map is c moved over other (both made to the same document), to apply
// after it. before: c's insertions at the same place as other's go first.
func (c ChangeSet) Map(other ChangeSet, before bool) ChangeSet {
	tie := 1
	if before {
		tie = -1
	}
	var rs []Replace
	for _, r := range c.Replacements() {
		if r.From == r.To {
			p := other.MapPos(r.From, tie)
			rs = append(rs, Replace{From: p, To: p, Text: r.Text})
			continue
		}
		// c's text goes where its range starts (after other's text inserted
		// there); it deletes only what other kept of the range: text other
		// inserted inside it stays.
		start := other.MapPos(r.From, tie)
		pieces := other.kept(r.From, r.To)
		if len(pieces) > 0 && pieces[0].From == start {
			pieces[0].Text = r.Text
		} else {
			pieces = append([]Replace{{From: start, To: start, Text: r.Text}}, pieces...)
		}
		rs = append(rs, pieces...)
	}
	out, err := Build(other.NewLen(), rs)
	if err != nil {
		return ChangeSet{secs: []section{{len: other.NewLen(), ins: -1}}}
	}
	return out
}

// kept is what of the old range [from, to) c keeps, as ranges of the new
// document.
func (c ChangeSet) kept(from, to int) []Replace {
	var out []Replace
	old, nw := 0, 0
	for _, s := range c.secs {
		end := old + s.len
		if s.ins < 0 {
			lo, hi := max(from, old), min(to, end)
			if lo < hi {
				out = append(out, Replace{From: nw + lo - old, To: nw + hi - old})
			}
			nw += s.len
		} else {
			nw += s.ins
		}
		old = end
		if old >= to {
			break
		}
	}
	return out
}

// Diff is the change set from a to b: one replacement between what they
// start and end with in common.
func Diff(a, b Text) ChangeSet {
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	if p == len(a) && p == len(b) {
		c, _ := Build(len(a), nil)
		return c
	}
	c, _ := Build(len(a), []Replace{{From: p, To: len(a) - s, Text: append(Text(nil), b[p:len(b)-s]...)}})
	return c
}

// ParseJSON reads ChangeSet.toJSON's form.
func ParseJSON(s string) (ChangeSet, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return ChangeSet{}, fmt.Errorf("changes: %w", err)
	}
	var c ChangeSet
	for _, r := range raw {
		var n int
		if json.Unmarshal(r, &n) == nil {
			if n < 0 {
				return ChangeSet{}, errors.New("changes: negative length")
			}
			c.keep(n)
			continue
		}
		var parts []json.RawMessage
		if err := json.Unmarshal(r, &parts); err != nil || len(parts) == 0 {
			return ChangeSet{}, errors.New("changes: an element is neither a number nor [deleted, ...lines]")
		}
		var del int
		if err := json.Unmarshal(parts[0], &del); err != nil || del < 0 {
			return ChangeSet{}, errors.New("changes: bad deletion length")
		}
		lines := make([]string, 0, len(parts)-1)
		for _, p := range parts[1:] {
			var l string
			if err := json.Unmarshal(p, &l); err != nil {
				return ChangeSet{}, errors.New("changes: inserted lines must be strings")
			}
			lines = append(lines, l)
		}
		c.replace(del, FromString(strings.Join(lines, "\n")))
	}
	return c, nil
}

// JSON is its ChangeSet.toJSON form.
func (c ChangeSet) JSON() string {
	out := make([]any, 0, len(c.secs))
	for _, s := range c.secs {
		if s.ins < 0 {
			out = append(out, s.len)
			continue
		}
		e := []any{s.len}
		if len(s.text) > 0 {
			for _, l := range strings.Split(s.text.String(), "\n") {
				e = append(e, l)
			}
		}
		out = append(out, e)
	}
	b, _ := json.Marshal(out)
	return string(b)
}
