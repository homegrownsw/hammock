package tui

import (
	"bytes"
	"strconv"
	"time"
	"unicode/utf8"
)

// Input from the host terminal, split into what the TUI acts on (mouse
// reports, pastes, focus changes) and the bytes it passes to panes as typed.

type evKind int

const (
	evBytes evKind = iota
	evMouse
	evPaste
	evFocus
)

type mouseEv struct {
	x, y             int
	button           int // 0 left, 1 middle, 2 right, 3 none
	press, release   bool
	motion           bool
	wheel            int // -1 up, +1 down, 0 none
	shift, alt, ctrl bool
}

type event struct {
	kind  evKind
	data  []byte
	mouse mouseEv
	focus bool
}

// inputParser keeps sequences split across reads.
type inputParser struct {
	pending []byte
	held    time.Time // when pending was kept
	paste   *bytes.Buffer
}

// holdFor is how long a sequence cut off at the end of a read waits for the
// rest before it's passed on as typed: the rest of a terminal's report
// arrives well within it.
const holdFor = 50 * time.Millisecond

// stale passes on a held partial sequence that nothing has followed for
// holdFor, as typed bytes, so it isn't kept forever. A partial paste end
// marker stays: the paste waits for its end either way.
func (p *inputParser) stale(now time.Time) []event {
	if len(p.pending) == 0 || p.paste != nil || now.Sub(p.held) < holdFor {
		return nil
	}
	b := p.pending
	p.pending = nil
	return []event{{kind: evBytes, data: b}}
}

var (
	pasteStart = []byte("\x1b[200~")
	pasteEnd   = []byte("\x1b[201~")
)

func (p *inputParser) parse(b []byte) []event {
	if len(p.pending) > 0 {
		b = append(p.pending, b...)
		p.pending = nil
	}
	var out []event
	var plain []byte
	flush := func() {
		if len(plain) > 0 {
			out = append(out, event{kind: evBytes, data: plain})
			plain = nil
		}
	}
	for len(b) > 0 {
		if p.paste != nil {
			if i := bytes.Index(b, pasteEnd); i >= 0 {
				p.paste.Write(b[:i])
				out = append(out, event{kind: evPaste, data: p.paste.Bytes()})
				p.paste = nil
				b = b[i+len(pasteEnd):]
				continue
			}
			// Keep a possible partial end marker for the next read.
			keep := min(len(b), len(pasteEnd)-1)
			p.paste.Write(b[:len(b)-keep])
			p.pending = append([]byte(nil), b[len(b)-keep:]...)
			return out
		}
		if b[0] != 0x1b {
			plain = append(plain, b[0])
			b = b[1:]
			continue
		}
		switch {
		case bytes.HasPrefix(b, []byte("\x1b[<")):
			end := bytes.IndexAny(b, "Mm")
			if end < 0 {
				if len(b) < 32 {
					flush()
					p.hold(b)
					return out
				}
				plain = append(plain, b[0])
				b = b[1:]
				continue
			}
			if m, ok := parseSGRMouse(b[3:end], b[end] == 'm'); ok {
				flush()
				out = append(out, event{kind: evMouse, mouse: m})
			}
			b = b[end+1:]
		case bytes.HasPrefix(b, pasteStart):
			flush()
			p.paste = &bytes.Buffer{}
			b = b[len(pasteStart):]
		case bytes.HasPrefix(b, []byte("\x1b[I")), bytes.HasPrefix(b, []byte("\x1b[O")):
			flush()
			out = append(out, event{kind: evFocus, focus: b[2] == 'I'})
			b = b[3:]
		case len(b) < len(pasteStart) && bytes.HasPrefix(pasteStart, b) && len(b) > 1:
			// A sequence cut off mid-way (from ESC [ on, which may yet be a
			// mouse report): wait for the rest. A lone ESC is the Escape key.
			flush()
			p.hold(b)
			return out
		default:
			plain = append(plain, b[0])
			b = b[1:]
		}
	}
	flush()
	return out
}

func (p *inputParser) hold(b []byte) {
	p.pending = append([]byte(nil), b...)
	p.held = time.Now()
}

// parseSGRMouse decodes "Cb;Cx;Cy" (1-based cells).
func parseSGRMouse(s []byte, release bool) (mouseEv, bool) {
	parts := bytes.Split(s, []byte(";"))
	if len(parts) != 3 {
		return mouseEv{}, false
	}
	cb, e1 := strconv.Atoi(string(parts[0]))
	cx, e2 := strconv.Atoi(string(parts[1]))
	cy, e3 := strconv.Atoi(string(parts[2]))
	if e1 != nil || e2 != nil || e3 != nil {
		return mouseEv{}, false
	}
	m := mouseEv{x: cx - 1, y: cy - 1, shift: cb&4 != 0, alt: cb&8 != 0, ctrl: cb&16 != 0}
	switch {
	case cb&64 != 0:
		if cb&3 == 0 {
			m.wheel = -1
		} else if cb&3 == 1 {
			m.wheel = 1
		}
		m.button = 3
	case cb&32 != 0:
		m.motion = true
		m.button = cb & 3
	default:
		m.button = cb & 3
		m.press, m.release = !release, release
	}
	return m, true
}

// key is one decoded keypress, for menus, the palette and text fields.
type key struct {
	name string // "enter", "esc", "backspace", "up", "down", "left", "right", "tab", "shift-tab", "home", "end", "delete", "ctrl-a".."ctrl-z", "alt-enter"; "" for a character
	r    rune
}

// keysOf decodes typed bytes into keys. A lone ESC at the end of a read is
// the Escape key.
func keysOf(b []byte) []key {
	var out []key
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == '\r' || c == '\n':
			out = append(out, key{name: "enter"})
		case c == '\t':
			out = append(out, key{name: "tab"})
		case c == 0x7f || c == 0x08:
			out = append(out, key{name: "backspace"})
		case c == 0x1b:
			seqs := map[string]string{
				"\x1b[A": "up", "\x1b[B": "down", "\x1b[C": "right", "\x1b[D": "left",
				"\x1bOA": "up", "\x1bOB": "down", "\x1bOC": "right", "\x1bOD": "left",
				"\x1b[H": "home", "\x1b[F": "end", "\x1b[3~": "delete", "\x1b[Z": "shift-tab",
				"\x1b[1~": "home", "\x1b[4~": "end", "\x1b[5~": "pgup", "\x1b[6~": "pgdown",
				"\x1b\r": "alt-enter", "\x1b\n": "alt-enter",
			}
			matched := false
			for s, name := range seqs {
				if bytes.HasPrefix(b, []byte(s)) {
					out = append(out, key{name: name})
					b = b[len(s):]
					matched = true
					break
				}
			}
			if matched {
				continue
			}
			if len(b) == 1 || (b[1] != '[' && b[1] != 'O') {
				out = append(out, key{name: "esc"})
			} else {
				// Unknown CSI: skip to its final byte.
				i := 2
				for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
					i++
				}
				b = b[min(i+1, len(b)):]
				continue
			}
		case c < 0x20:
			out = append(out, key{name: "ctrl-" + string(rune('a'+c-1))})
		default:
			r, n := utf8.DecodeRune(b)
			out = append(out, key{r: r})
			b = b[n:]
			continue
		}
		b = b[1:]
	}
	return out
}
