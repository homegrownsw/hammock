//go:build ghostty

package vt

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"strings"

	"go.mitchellh.com/libghostty"
)

// Emulated reports a full terminal emulator behind the mirror (libghostty-vt).
const Emulated = true

// GhosttyCommit is the libghostty-vt commit this build links against
// (third_party/ghostty/VERSION).
const GhosttyCommit = "f54340ad77814fe319d136e5e915d6a48de8dd23"

type ghosttyMirror struct {
	t     *libghostty.Terminal
	rows  int
	cols  int
	cellW int // cell size in pixels, from the driving client (0: unknown)
	cellH int
	resp  func([]byte)
	// The client's light/dark scheme, for CSI ? 996 n and mode 2031 reports.
	scheme    libghostty.ColorScheme
	hasScheme bool
	pngs      pngCache // Kitty images re-sent in snapshots (kitty_snapshot.go)
}

func newMirror(cols, rows int) Mirror {
	t, err := libghostty.NewTerminal(
		libghostty.WithSize(uint16(cols), uint16(rows)),
		libghostty.WithMaxScrollbackLines(10000),
		// Grapheme clustering (mode 2027), as in Ghostty. Clients set the same
		// default so both sides agree on cell widths.
		libghostty.WithModeDefault(libghostty.ModeGraphemeCluster, true),
	)
	if err != nil {
		panic("vt: libghostty terminal: " + err.Error())
	}
	m := &ghosttyMirror{t: t, rows: rows, cols: cols}
	t.SetEffectWritePty(func(_ *libghostty.Terminal, data []byte) {
		if m.resp != nil {
			m.resp(append([]byte(nil), data...))
		}
	})
	t.SetEffectColorScheme(func(*libghostty.Terminal) (libghostty.ColorScheme, bool) { return m.scheme, m.hasScheme })
	// Pixel sizes for XTWINOPS queries (CSI 14/16 t), which image tools ask
	// before drawing; unknown until a client says.
	t.SetEffectSize(func(*libghostty.Terminal) (libghostty.SizeReportSize, bool) {
		if m.cellW == 0 || m.cellH == 0 {
			return libghostty.SizeReportSize{}, false
		}
		return libghostty.SizeReportSize{Rows: uint16(m.rows), Columns: uint16(m.cols), CellWidth: uint32(m.cellW), CellHeight: uint32(m.cellH)}, true
	})
	// Kitty graphics: images are stored (so the protocol is answered as
	// supported, and placements tracked); clients draw them.
	limit := uint64(kittyStorage)
	_ = t.SetKittyImageStorageLimit(&limit)
	// Images sent as a file path (t=f) aren't read here: the daemon reads
	// them once, only if they're images, and sends them on inline
	// (KittyFiles), so this mirror and every client get the image itself.
	_ = t.SetKittyImageMediumFile(false)
	return m
}

func (m *ghosttyMirror) Write(p []byte) { _, _ = m.t.Write(p) }

func (m *ghosttyMirror) Resize(cols, rows int) {
	m.rows, m.cols = rows, cols
	_ = m.t.Resize(uint16(cols), uint16(rows), uint32(m.cellW), uint32(m.cellH))
}

func (m *ghosttyMirror) SetCellSize(w, h int) {
	if w == m.cellW && h == m.cellH {
		return
	}
	m.cellW, m.cellH = w, h
	_ = m.t.Resize(uint16(max(m.cols, 1)), uint16(max(m.rows, 1)), uint32(w), uint32(h))
}

// kittyStorage is each terminal's Kitty image storage, in bytes.
const kittyStorage = 64 << 20

// PNG images in the Kitty protocol are decoded with Go's image/png.
var _ = func() error {
	return libghostty.SysSetDecodePng(func(data []byte) (*libghostty.SysImage, error) {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		b := img.Bounds()
		rgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
		return &libghostty.SysImage{Width: uint32(b.Dx()), Height: uint32(b.Dy()), Data: rgba.Pix}, nil
	})
}()

// SetResponder routes the terminal's replies to queries (device attributes,
// cursor position reports, …) back to the PTY. The daemon's mirror is the
// only party that answers queries; clients must not.
func (m *ghosttyMirror) SetResponder(fn func([]byte)) { m.resp = fn }

func (m *ghosttyMirror) SetStatusHandler(report func(StatusReport), prompt func()) {
	if report == nil {
		m.t.SetEffectProgramStatus(nil)
	} else {
		m.t.SetEffectProgramStatus(func(_ *libghostty.Terminal, s libghostty.ProgramStatus) {
			st, ok := ghosttyStates[s.State]
			if !ok {
				return // a state from a later revision
			}
			report(StatusReport{State: st, Kind: ghosttyKinds[s.Kind], Progress: int(s.Progress),
				ID: s.ID, App: s.App, Title: s.Title, Message: s.Message})
		})
	}
	if prompt == nil {
		m.t.SetEffectSemanticPrompt(nil)
	} else {
		m.t.SetEffectSemanticPrompt(func(_ *libghostty.Terminal, e libghostty.TerminalSemanticPrompt) {
			if e.Kind == libghostty.SemanticPromptStart && e.PromptKind == libghostty.PromptPrimary {
				prompt()
			}
		})
	}
}

var ghosttyStates = map[libghostty.ProgramStatusState]StatusState{
	libghostty.ProgramStatusStateIdle: StatusIdle, libghostty.ProgramStatusStateWorking: StatusWorking,
	libghostty.ProgramStatusStateDone: StatusDone, libghostty.ProgramStatusStateBlocked: StatusBlocked,
	libghostty.ProgramStatusStateError: StatusError, libghostty.ProgramStatusStateClear: StatusClear,
}

var ghosttyKinds = map[libghostty.ProgramStatusKind]string{
	libghostty.ProgramStatusKindPermission: "permission", libghostty.ProgramStatusKindQuestion: "question",
	libghostty.ProgramStatusKindAuth: "auth",
}

func (m *ghosttyMirror) Snapshot() []byte {
	f, err := libghostty.NewFormatter(m.t,
		libghostty.WithFormatterFormat(libghostty.FormatterFormatVT),
		libghostty.WithFormatterTrim(false),
		// No palette: the snapshot would carry the daemon's default colours and
		// override every client's theme. (Colours a program set with OSC 4 are
		// lost on reattach; that's rare.)
		libghostty.WithFormatterExtraPalette(false),
		libghostty.WithFormatterExtraModes(true),
		libghostty.WithFormatterExtraScrollingRegion(true),
		libghostty.WithFormatterExtraTabstops(true),
		libghostty.WithFormatterExtraPwd(true),
		libghostty.WithFormatterExtraKeyboard(true),
		libghostty.WithFormatterExtraCursor(true),
		libghostty.WithFormatterExtraStyle(true),
		libghostty.WithFormatterExtraHyperlink(true),
		libghostty.WithFormatterExtraKittyKeyboard(true),
		libghostty.WithFormatterExtraCharsets(true),
	)
	if err != nil {
		return []byte("\x1bc")
	}
	defer f.Close()
	out, err := f.Format()
	if err != nil {
		return []byte("\x1bc")
	}
	return append(append([]byte("\x1bc"), out...), m.kittyImages(out)...)
}

func (m *ghosttyMirror) BracketedPaste() bool {
	on, err := m.t.Mode(libghostty.ModeBracketedPaste)
	return err == nil && on
}

func (m *ghosttyMirror) KittyKeyboard() bool {
	f, err := m.t.KittyKeyboardFlags()
	return err == nil && f != 0
}

func (m *ghosttyMirror) Text(scrollback bool) string {
	f, err := libghostty.NewFormatter(m.t,
		libghostty.WithFormatterFormat(libghostty.FormatterFormatPlain),
		libghostty.WithFormatterTrim(true),
	)
	if err != nil {
		return ""
	}
	defer f.Close()
	s, err := f.FormatString()
	if err != nil || scrollback {
		return s
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > m.rows {
		lines = lines[len(lines)-m.rows:]
	}
	return strings.Join(lines, "\n")
}

func (m *ghosttyMirror) Engine() string { return "libghostty-vt@" + GhosttyCommit[:12] }

func (m *ghosttyMirror) Close() { m.t.Close() }

// SetColors makes the mirror's default colours the client's, so OSC 4/10/11
// and CSI ? 996 n get the colours on screen, and reports a light/dark change
// to programs that enabled mode 2031.
func (m *ghosttyMirror) SetColors(c Colors) {
	rgb := func(v uint32) *libghostty.ColorRGB {
		return &libghostty.ColorRGB{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v)}
	}
	_ = m.t.SetColorForeground(rgb(c.Foreground))
	_ = m.t.SetColorBackground(rgb(c.Background))
	_ = m.t.SetColorCursor(rgb(c.Cursor))
	var p libghostty.Palette
	for i := range p {
		p[i] = *rgb(xterm256(i, c.Palette))
	}
	_ = m.t.SetColorPalette(&p)

	scheme := libghostty.ColorSchemeLight
	if c.Dark {
		scheme = libghostty.ColorSchemeDark
	}
	changed := m.hasScheme && m.scheme != scheme
	m.scheme, m.hasScheme = scheme, true
	if on, _ := m.t.Mode(libghostty.ModeColorSchemeReport); changed && on && m.resp != nil {
		if b, err := libghostty.ColorSchemeReportEncode(scheme); err == nil {
			m.resp(b)
		}
	}
}

// xterm256 is palette entry i: the client's ANSI colours, then the standard
// 6x6x6 cube and grey ramp.
func xterm256(i int, ansi []uint32) uint32 {
	if i < 16 {
		if i < len(ansi) {
			return ansi[i]
		}
		return 0
	}
	if i < 232 {
		steps := [6]uint32{0, 95, 135, 175, 215, 255}
		n := i - 16
		return steps[n/36]<<16 | steps[n/6%6]<<8 | steps[n%6]
	}
	v := uint32(8 + (i-232)*10)
	return v<<16 | v<<8 | v
}

func (m *ghosttyMirror) BottomText(n int) string {
	var b strings.Builder
	for y := max(0, m.rows-n); y < m.rows; y++ {
		var line []rune
		for x := 0; x < m.cols; x++ {
			ref, err := m.t.GridRef(libghostty.Point{Tag: libghostty.PointTagActive, X: uint16(x), Y: uint32(y)})
			if err != nil {
				break
			}
			if cell, err := ref.Cell(); err == nil {
				if w, _ := cell.Wide(); w == libghostty.CellWideSpacerTail {
					continue // the second half of a wide character
				}
			}
			cps, _ := ref.Graphemes()
			if len(cps) == 0 {
				line = append(line, ' ')
				continue
			}
			if st, err := ref.Style(); err == nil && st.Faint() {
				line = append(line, ' ')
				continue
			}
			for _, cp := range cps {
				line = append(line, rune(cp))
			}
		}
		b.WriteString(strings.TrimRight(string(line), " "))
		b.WriteByte('\n')
	}
	return b.String()
}
