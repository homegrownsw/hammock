//go:build ghostty

package tui

import (
	"go.mitchellh.com/libghostty"
)

// Available reports whether the TUI can run in this build.
const Available = true

// paneTerm is the TUI's local terminal for one pane.
type paneTerm struct {
	t     *libghostty.Terminal
	rs    *libghostty.RenderState
	ri    *libghostty.RenderStateRowIterator
	rc    *libghostty.RenderStateRowCells
	cols  int
	rows  int
	style libghostty.RenderCellStyle
	cps   []uint32
	enc   *libghostty.MouseEncoder
	mev   *libghostty.MouseEvent
	down  bool
}

func newPaneTerm(cols, rows int) (*paneTerm, error) {
	t, err := libghostty.NewTerminal(libghostty.WithSize(uint16(max(cols, 1)), uint16(max(rows, 1))), libghostty.WithMaxScrollbackLines(2000),
		libghostty.WithModeDefault(libghostty.ModeGraphemeCluster, true)) // matches the daemon mirror
	if err != nil {
		return nil, err
	}
	rs, err := libghostty.NewRenderState()
	if err != nil {
		t.Close()
		return nil, err
	}
	ri, err := libghostty.NewRenderStateRowIterator()
	if err != nil {
		return nil, err
	}
	rc, err := libghostty.NewRenderStateRowCells()
	if err != nil {
		return nil, err
	}
	return &paneTerm{t: t, rs: rs, ri: ri, rc: rc, cols: cols, rows: rows}, nil
}

func (p *paneTerm) write(b []byte) { _, _ = p.t.Write(b) }

func (p *paneTerm) reset(cols, rows int) {
	p.t.Reset()
	p.resize(cols, rows)
}

func (p *paneTerm) resize(cols, rows int) {
	if cols <= 0 || rows <= 0 || (cols == p.cols && rows == p.rows) {
		return
	}
	p.cols, p.rows = cols, rows
	_ = p.t.Resize(uint16(cols), uint16(rows), 0, 0)
}

// draw renders the terminal into s at (x, y), clipped to w×h, returning the
// cursor position (screen coordinates) and whether it is visible.
func (p *paneTerm) draw(s *Screen, x, y, w, h int) (cx, cy int, visible bool) {
	if err := p.rs.Update(p.t); err != nil {
		return 0, 0, false
	}
	if err := p.rs.RowIterator(p.ri); err != nil {
		return 0, 0, false
	}
	row := 0
	for p.ri.Next() && row < h {
		if err := p.ri.Cells(p.rc); err != nil {
			break
		}
		col := 0
		for p.rc.Next() && col < w {
			var c Cell
			if raw, err := p.rc.Raw(); err == nil {
				if wide, err := raw.Wide(); err == nil && (wide == libghostty.CellWideSpacerTail || wide == libghostty.CellWideSpacerHead) {
					c.Cont = wide == libghostty.CellWideSpacerTail
				}
			}
			if !c.Cont {
				// Kitty image placeholders (U+10EEEE) stand in for an image
				// the TUI can't draw; leave those cells blank.
				if cps, err := p.rc.GraphemesInto(p.cps[:0]); err == nil && len(cps) > 0 && cps[0] != 0x10EEEE {
					p.cps = cps
					rs := make([]rune, len(cps))
					for i, cp := range cps {
						rs[i] = rune(cp)
					}
					c.Ch = string(rs)
				}
			}
			if err := p.rc.StyleInto(&p.style); err == nil && p.style.HasStyling {
				st := p.style
				c.Bold, c.Faint, c.Italic, c.Underline, c.Strk, c.Inverse = st.Bold, st.Faint, st.Italic, st.Underline, st.Strikethrough, st.Inverse
				if st.HasForeground {
					c.FG, c.HasFG = RGB{st.Foreground.R, st.Foreground.G, st.Foreground.B}, true
				}
				if st.HasBackground {
					c.BG, c.HasBG = RGB{st.Background.R, st.Background.G, st.Background.B}, true
				}
			}
			s.Set(x+col, y+row, c)
			col++
		}
		row++
	}
	vis, _ := p.rs.CursorVisible()
	has, _ := p.rs.CursorViewportHasValue()
	if !vis || !has {
		return 0, 0, false
	}
	vx, _ := p.rs.CursorViewportX()
	vy, _ := p.rs.CursorViewportY()
	if int(vx) >= w || int(vy) >= h {
		return 0, 0, false
	}
	return x + int(vx), y + int(vy), true
}

// Cell size handed to the mouse encoder: positions are whole cells here, so
// any size works; pixel mouse mode then reports multiples of it.
const cellPx = 10

// encodeMouse encodes a mouse event at pane cell (col, row) for a program
// that turned on mouse reporting; nil when it isn't listening for it.
// button: 0 left, 1 middle, 2 right, 3 none; wheel -1/+1.
func (p *paneTerm) encodeMouse(m mouseEv, col, row int) []byte {
	if on, _ := p.t.MouseTracking(); !on {
		return nil
	}
	if p.enc == nil {
		var err error
		if p.enc, err = libghostty.NewMouseEncoder(); err != nil {
			return nil
		}
		if p.mev, err = libghostty.NewMouseEvent(); err != nil {
			return nil
		}
	}
	p.enc.SetOptFromTerminal(p.t)
	p.enc.SetOptSize(libghostty.MouseEncoderSize{ScreenWidth: uint32(p.cols * cellPx), ScreenHeight: uint32(p.rows * cellPx), CellWidth: cellPx, CellHeight: cellPx})
	buttons := []libghostty.MouseButton{libghostty.MouseButtonLeft, libghostty.MouseButtonMiddle, libghostty.MouseButtonRight}
	switch {
	case m.wheel != 0:
		p.mev.SetAction(libghostty.MouseActionPress)
		if m.wheel < 0 {
			p.mev.SetButton(libghostty.MouseButtonFour)
		} else {
			p.mev.SetButton(libghostty.MouseButtonFive)
		}
	case m.press || m.release:
		if m.press {
			p.mev.SetAction(libghostty.MouseActionPress)
		} else {
			p.mev.SetAction(libghostty.MouseActionRelease)
		}
		if m.button < 3 {
			p.mev.SetButton(buttons[m.button])
			p.down = m.press
		} else {
			p.mev.ClearButton()
		}
	default:
		p.mev.SetAction(libghostty.MouseActionMotion)
		if m.button < 3 {
			p.mev.SetButton(buttons[m.button])
		} else {
			p.mev.ClearButton()
		}
	}
	p.enc.SetOptAnyButtonPressed(p.down)
	var mods libghostty.Mods
	if m.shift {
		mods |= libghostty.ModShift
	}
	if m.alt {
		mods |= libghostty.ModAlt
	}
	if m.ctrl {
		mods |= libghostty.ModCtrl
	}
	p.mev.SetMods(mods)
	p.mev.SetPosition(libghostty.MousePosition{X: float32(col*cellPx + cellPx/2), Y: float32(row*cellPx + cellPx/2)})
	b, err := p.enc.Encode(p.mev)
	if err != nil || len(b) == 0 {
		return nil
	}
	return b
}

func (p *paneTerm) mode(m libghostty.Mode) bool {
	on, _ := p.t.Mode(m)
	return on
}

func (p *paneTerm) altScreen() bool {
	s, err := p.t.ActiveScreen()
	return err == nil && s == libghostty.ScreenAlternate
}

// wheelAsArrows: a full-screen program without mouse reporting that asked
// for wheel-as-arrows (mode 1007), or any full-screen program (as most
// terminals do).
func (p *paneTerm) wheelAsArrows() bool { return p.altScreen() }

func (p *paneTerm) bracketedPaste() bool { return p.mode(libghostty.ModeBracketedPaste) }

// scroll moves the scrollback viewport (negative: back in history).
func (p *paneTerm) scroll(delta int) { p.t.ScrollViewportDelta(delta) }

func (p *paneTerm) scrollToBottom() { p.t.ScrollViewportBottom() }

// scrollback reports how far the viewport is scrolled back (rows above the
// bottom) and the total rows, for the scroll indicator.
func (p *paneTerm) scrollback() (back, total, visible int) {
	sb, err := p.t.Scrollbar()
	if err != nil {
		return 0, 0, 0
	}
	return int(sb.Total - sb.Offset - sb.Len), int(sb.Total), int(sb.Len)
}

func (p *paneTerm) close() {
	if p.enc != nil {
		p.enc.Close()
		p.mev.Close()
	}
	p.rc.Close()
	p.ri.Close()
	p.rs.Close()
	p.t.Close()
}
