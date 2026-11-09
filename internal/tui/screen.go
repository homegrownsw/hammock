// Package tui is hammock's terminal UI: a sidebar of Groups and tabs plus the
// selected tab's panes, each rendered from its own libghostty-vt terminal and
// composited into the host terminal.
package tui

import (
	"bytes"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/clipperhouse/uax29/v2/graphemes"
)

// RGB is a 24-bit colour.
type RGB struct{ R, G, B uint8 }

// Cell is one character cell of the composed screen.
type Cell struct {
	Ch                       string // "" renders as a space; wide chars occupy two cells, the second has Cont=true
	Cont                     bool
	FG, BG                   RGB
	HasFG, HasBG             bool
	Bold, Faint, Italic      bool
	Underline, Inverse, Strk bool
}

func (c Cell) sameStyle(o Cell) bool {
	return c.FG == o.FG && c.BG == o.BG && c.HasFG == o.HasFG && c.HasBG == o.HasBG && c.Bold == o.Bold &&
		c.Faint == o.Faint && c.Italic == o.Italic && c.Underline == o.Underline && c.Inverse == o.Inverse && c.Strk == o.Strk
}

// Screen is a grid of cells that renders itself as a minimal diff against
// what it last drew.
type Screen struct {
	W, H    int
	cells   []Cell
	prev    []Cell
	cursorX int
	cursorY int
	showCur bool
	full    bool
}

// NewScreen returns a w×h screen that will fully redraw on first Flush.
func NewScreen(w, h int) *Screen {
	return &Screen{W: w, H: h, cells: make([]Cell, w*h), full: true}
}

// Clear blanks every cell.
func (s *Screen) Clear() {
	for i := range s.cells {
		s.cells[i] = Cell{}
	}
	s.showCur = false
}

// Set places c at (x, y) if in bounds.
func (s *Screen) Set(x, y int, c Cell) {
	if x >= 0 && y >= 0 && x < s.W && y < s.H {
		s.cells[y*s.W+x] = c
	}
}

// Text writes a string starting at (x, y), clipped to maxW cells, a
// grapheme at a time (emoji and CJK take two cells). Returns the number of
// cells written.
func (s *Screen) Text(x, y, maxW int, str string, style Cell) int {
	n := 0
	g := graphemes.FromString(str)
	for g.Next() {
		ch := g.Value()
		w := ansi.StringWidth(ch)
		if w == 0 {
			continue
		}
		if n+w > maxW {
			break
		}
		c := style
		c.Ch = ch
		s.Set(x+n, y, c)
		if w == 2 {
			c.Ch, c.Cont = "", true
			s.Set(x+n+1, y, c)
		}
		n += w
	}
	return n
}

// TextRight writes str right-aligned to end at x+w.
func (s *Screen) TextRight(x, y, w int, str string, style Cell) int {
	sw := min(textWidth(str), w)
	return s.Text(x+w-sw, y, sw, str, style)
}

// Get returns the cell at (x, y).
func (s *Screen) Get(x, y int) Cell {
	if x >= 0 && y >= 0 && x < s.W && y < s.H {
		return s.cells[y*s.W+x]
	}
	return Cell{}
}

// Paint sets the background of a rectangle, keeping what's drawn there.
func (s *Screen) Paint(x, y, w, h int, bg RGB) {
	for j := max(y, 0); j < min(y+h, s.H); j++ {
		for i := max(x, 0); i < min(x+w, s.W); i++ {
			c := &s.cells[j*s.W+i]
			c.BG, c.HasBG = bg, true
		}
	}
}

func textWidth(s string) int { return ansi.StringWidth(s) }

// truncate shortens s to w cells with an ellipsis.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if textWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// Fill fills a rectangle with c.
func (s *Screen) Fill(x, y, w, h int, c Cell) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			s.Set(i, j, c)
		}
	}
}

// Cursor positions the hardware cursor (hidden when show is false).
func (s *Screen) Cursor(x, y int, show bool) { s.cursorX, s.cursorY, s.showCur = x, y, show }

// Flush writes the changes since the last Flush to w.
func (s *Screen) Flush(w io.Writer) error {
	var b bytes.Buffer
	b.WriteString("\x1b[?25l\x1b[?2026h") // hide cursor, begin synchronized update
	if s.full || len(s.prev) != len(s.cells) {
		b.WriteString("\x1b[0m\x1b[2J")
		s.prev = make([]Cell, len(s.cells))
		for i := range s.prev {
			s.prev[i] = Cell{Ch: "\x00"} // force every cell to draw
		}
		s.full = false
	}
	var cur Cell
	curValid := false
	lastX, lastY := -1, -1
	for y := 0; y < s.H; y++ {
		for x := 0; x < s.W; x++ {
			i := y*s.W + x
			c := s.cells[i]
			if c == s.prev[i] || c.Cont {
				continue
			}
			if x != lastX || y != lastY {
				fmt.Fprintf(&b, "\x1b[%d;%dH", y+1, x+1)
			}
			if !curValid || !c.sameStyle(cur) {
				writeSGR(&b, c)
				cur, curValid = c, true
			}
			ch := c.Ch
			if ch == "" || !utf8.ValidString(ch) {
				ch = " "
			}
			b.WriteString(ch)
			lastX, lastY = x+1, y
			if x+1 < s.W && s.cells[i+1].Cont {
				lastX++ // wide character advanced two columns
			}
		}
	}
	copy(s.prev, s.cells)
	b.WriteString("\x1b[0m\x1b[?2026l")
	if s.showCur {
		fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[?25h", s.cursorY+1, s.cursorX+1)
	}
	_, err := w.Write(b.Bytes())
	return err
}

// Invalidate forces a full redraw on the next Flush.
func (s *Screen) Invalidate() { s.full = true }

func writeSGR(b *bytes.Buffer, c Cell) {
	b.WriteString("\x1b[0")
	if c.Bold {
		b.WriteString(";1")
	}
	if c.Faint {
		b.WriteString(";2")
	}
	if c.Italic {
		b.WriteString(";3")
	}
	if c.Underline {
		b.WriteString(";4")
	}
	if c.Inverse {
		b.WriteString(";7")
	}
	if c.Strk {
		b.WriteString(";9")
	}
	if c.HasFG {
		fmt.Fprintf(b, ";38;2;%d;%d;%d", c.FG.R, c.FG.G, c.FG.B)
	}
	if c.HasBG {
		fmt.Fprintf(b, ";48;2;%d;%d;%d", c.BG.R, c.BG.G, c.BG.B)
	}
	b.WriteString("m")
}
