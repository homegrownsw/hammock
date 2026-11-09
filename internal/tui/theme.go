package tui

import (
	"bytes"
	"fmt"
	"strconv"
	"time"

	colorful "github.com/lucasb-eyer/go-colorful"
)

// theme is the TUI's palette, derived from the host terminal's own
// background and foreground (asked for with OSC 10/11), so the sidebar reads
// as a tinted panel over whatever theme the terminal uses, in light or dark,
// as the Mac app's sidebar does over the desktop.
type theme struct {
	dark    bool
	bg, fg  RGB
	sidebar RGB // sidebar panel
	group   RGB // a Group's block while the mouse is over it
	hover   RGB // a hovered tab
	pill    RGB // the selected tab
	dim     RGB // secondary text
	faint   RGB // tertiary text, separators
	border  RGB
	menu    RGB // menus, palette and dialogs
	menuSel RGB
	accent  RGB
	orange  RGB
	indigo  RGB // an agent at work
	amber   RGB // highlights
	green   RGB
	red     RGB
}

func newTheme(bg, fg RGB) theme {
	lum := 0.2126*float64(bg.R) + 0.7152*float64(bg.G) + 0.0722*float64(bg.B)
	t := theme{dark: lum < 128, bg: bg, fg: fg}
	t.accent, t.orange, t.green, t.red = RGB{10, 132, 255}, RGB{255, 159, 10}, RGB{48, 209, 88}, RGB{255, 69, 58}
	t.amber = RGB{255, 196, 40}
	t.indigo = RGB{125, 122, 255}
	if t.dark {
		t.sidebar = mix(bg, fg, 0.055)
		t.group = mix(t.sidebar, fg, 0.035)
		t.hover = mix(t.sidebar, fg, 0.08)
		t.pill = mix(t.sidebar, fg, 0.17)
		t.menu = mix(bg, fg, 0.10)
		t.menuSel = mix(t.menu, t.accent, 0.55)
	} else {
		t.accent, t.orange, t.green, t.red = RGB{0, 122, 255}, RGB{240, 130, 0}, RGB{40, 167, 69}, RGB{230, 50, 40}
		t.amber = RGB{222, 160, 0}
		t.indigo = RGB{88, 86, 214}
		t.sidebar = mix(bg, RGB{0, 0, 0}, 0.055)
		t.group = mix(t.sidebar, RGB{0, 0, 0}, 0.03)
		t.hover = mix(t.sidebar, RGB{255, 255, 255}, 0.45)
		t.pill = RGB{255, 255, 255}
		t.menu = mix(bg, RGB{255, 255, 255}, 0.6)
		t.menuSel = t.accent
	}
	t.dim = mix(fg, bg, 0.42)
	t.faint = mix(fg, bg, 0.68)
	t.border = mix(fg, bg, 0.82)
	return t
}

// mix blends a toward b by t (0..1), in a perceptual space so fades look even.
func mix(a, b RGB, t float64) RGB {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	ca := colorful.Color{R: float64(a.R) / 255, G: float64(a.G) / 255, B: float64(a.B) / 255}
	cb := colorful.Color{R: float64(b.R) / 255, G: float64(b.G) / 255, B: float64(b.B) / 255}
	r, g, bl := ca.BlendLab(cb, t).Clamped().RGB255()
	return RGB{r, g, bl}
}

// queryColors asks the host terminal for its default background and
// foreground. Device Attributes, which every terminal answers, ends the
// wait; anything else that arrived meanwhile is returned for normal input.
func queryColors(write func(string), in <-chan []byte) (bg, fg RGB, ok bool, rest []byte) {
	bg, fg = RGB{30, 30, 30}, RGB{220, 220, 220}
	write("\x1b]11;?\x1b\\\x1b]10;?\x1b\\\x1b[c")
	var buf []byte
	timeout := time.After(400 * time.Millisecond)
	for {
		select {
		case b, open := <-in:
			if !open {
				return bg, fg, ok, buf
			}
			buf = append(buf, b...)
			if i := bytes.Index(buf, []byte("\x1b[?")); i >= 0 {
				if j := bytes.IndexByte(buf[i:], 'c'); j >= 0 {
					reply := buf[:i+j+1]
					if c, found := parseOSCColor(reply, "11"); found {
						bg, ok = c, true
					}
					if c, found := parseOSCColor(reply, "10"); found {
						fg = c
					}
					rest = append(stripReplies(buf[:i]), buf[i+j+1:]...)
					return bg, fg, ok, rest
				}
			}
		case <-timeout:
			return bg, fg, ok, stripReplies(buf)
		}
	}
}

// parseOSCColor finds "ESC ] n ; rgb:RRRR/GGGG/BBBB" in b.
func parseOSCColor(b []byte, n string) (RGB, bool) {
	i := bytes.Index(b, []byte("\x1b]"+n+";rgb:"))
	if i < 0 {
		return RGB{}, false
	}
	s := b[i+len(n)+7:]
	var parts [3]uint8
	for k := 0; k < 3; k++ {
		end := bytes.IndexAny(s, "/\x07\x1b")
		if end < 0 {
			return RGB{}, false
		}
		v, err := strconv.ParseUint(string(s[:end]), 16, 32)
		if err != nil {
			return RGB{}, false
		}
		// 1-4 hex digits, scaled to 8 bits.
		bits := uint(4 * end)
		parts[k] = uint8(v * 255 / (1<<bits - 1))
		s = s[end:]
		if len(s) > 0 && s[0] == '/' {
			s = s[1:]
		}
	}
	return RGB{parts[0], parts[1], parts[2]}, true
}

// stripReplies drops OSC replies (ESC ] ... ST/BEL) from b.
func stripReplies(b []byte) []byte {
	var out []byte
	for len(b) > 0 {
		i := bytes.Index(b, []byte("\x1b]"))
		if i < 0 {
			return append(out, b...)
		}
		out = append(out, b[:i]...)
		end := bytes.IndexAny(b[i:], "\x07")
		st := bytes.Index(b[i:], []byte("\x1b\\"))
		switch {
		case st >= 0 && (end < 0 || st < end):
			b = b[i+st+2:]
		case end >= 0:
			b = b[i+end+1:]
		default:
			return out
		}
	}
	return out
}

func (c RGB) String() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }
