// Package vt holds the daemon's server-side mirror of each terminal pane's
// state. The real implementation embeds libghostty-vt (build tag "ghostty");
// without it a raw-bytes fallback keeps recent output for replay.
package vt

import (
	"os"
	"path"
	"strings"
	"sync"
)

// Mirror consumes every byte a pane's PTY produces and can reproduce the
// terminal's state for newly attaching clients, MCP reads and heuristics.
// Implementations are not safe for concurrent use; callers serialise access.
type Mirror interface {
	// Write feeds PTY output.
	Write(p []byte)
	// Resize changes the terminal size.
	Resize(cols, rows int)
	// Snapshot returns a VT byte stream that reproduces the current screen
	// (and as much scrollback as is practical) when fed into a fresh terminal
	// of the same size.
	Snapshot() []byte
	// Text returns the visible screen (or screen + scrollback) as plain text.
	Text(scrollback bool) string
	// BottomText is the screen's last n rows as text with faint (dim) text
	// left out, as spaces: what an agent's input box holds, without its
	// placeholder.
	BottomText(n int) string
	// SetCellSize sets the cell size in pixels that size queries (XTWINOPS)
	// and Kitty graphics placements use; 0 means unknown.
	SetCellSize(width, height int)
	// BracketedPaste reports whether the program turned on bracketed paste.
	BracketedPaste() bool
	// KittyKeyboard reports whether the program turned on the kitty keyboard
	// protocol (keys like Enter must then be sent as CSI u).
	KittyKeyboard() bool
	// Engine names the implementation, e.g. "libghostty-vt@<commit>".
	Engine() string
	// SetResponder routes the terminal's replies to queries (device
	// attributes, cursor position reports, …) back to the PTY. The daemon's
	// mirror is the only party that answers queries; clients must not, or
	// programs would see duplicate replies.
	SetResponder(fn func([]byte))
	// SetColors sets the colours the user sees, so colour and light/dark queries
	// are answered truthfully; programs that asked (mode 2031) are told when
	// the scheme flips.
	SetColors(c Colors)
	// Close releases resources.
	Close()
}

// Colors are a client's terminal colours, as 0xRRGGBB.
type Colors struct {
	Foreground, Background, Cursor uint32
	Palette                        []uint32 // ANSI 0-15
	Dark                           bool
}

// New returns the best available mirror for a terminal of the given size.
func New(cols, rows int) Mirror { return newMirror(cols, rows) }

// OSC tracks title (OSC 0/2) and working directory (OSC 7) reports in a PTY
// byte stream, independent of the mirror implementation.
type OSC struct {
	state   int
	buf     []byte
	Title   string
	Cwd     string
	Changed bool // set when Title or Cwd changes; callers reset it
}

const maxOSC = 4096

// Write scans p for OSC sequences.
func (o *OSC) Write(p []byte) {
	for _, b := range p {
		switch o.state {
		case 0: // ground
			if b == 0x1b {
				o.state = 1
			}
		case 1: // after ESC
			if b == ']' {
				o.state, o.buf = 2, o.buf[:0]
			} else {
				o.state = 0
			}
		case 2: // in OSC body
			switch {
			case b == 0x07:
				o.finish()
			case b == 0x1b:
				o.state = 3
			case len(o.buf) < maxOSC:
				o.buf = append(o.buf, b)
			}
		case 3: // ESC inside OSC: expect '\' (ST)
			if b == '\\' {
				o.finish()
			} else {
				o.state = 0
			}
		}
	}
}

func (o *OSC) finish() {
	o.state = 0
	s := string(o.buf)
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != ';' {
		return
	}
	code, val := s[:i], s[i+1:]
	switch code {
	case "0", "2":
		if val != o.Title {
			o.Title, o.Changed = val, true
		}
	case "7":
		if p := fileURLPath(val); p != "" && p != o.Cwd {
			o.Cwd, o.Changed = p, true
		}
	}
}

// fileURLPath extracts the path from file://host/path, percent-decoding it.
// Only this Mac's cwd is taken: the host must be empty, localhost or this
// machine (not a program on an ssh host, or one making things up), and the
// path absolute, clean and free of control characters.
func fileURLPath(u string) string {
	const prefix = "file://"
	if len(u) < len(prefix) || u[:len(prefix)] != prefix {
		return ""
	}
	rest := u[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 || !isLocalHost(percentDecode(rest[:slash])) {
		return ""
	}
	p := percentDecode(rest[slash:])
	if strings.IndexFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 }) >= 0 {
		return ""
	}
	c := path.Clean(p)
	if c != p && c+"/" != p || !path.IsAbs(c) {
		return ""
	}
	return c
}

// localHostname is this machine's name (a seam for tests).
var localHostname = sync.OnceValue(func() string {
	h, _ := os.Hostname()
	return h
})

// isLocalHost reports whether an OSC 7 host names this machine. Shells send
// $HOST, which may or may not carry .local (or the domain), so names are
// compared by their first label too.
func isLocalHost(h string) bool {
	if h == "" || strings.EqualFold(h, "localhost") {
		return true
	}
	me := localHostname()
	if me == "" {
		return false
	}
	first := func(s string) string { s, _, _ = strings.Cut(s, "."); return s }
	return strings.EqualFold(h, me) || strings.EqualFold(first(h), first(me))
}

func percentDecode(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if h, ok := unhex(s[i+1]); ok {
				if l, ok := unhex(s[i+2]); ok {
					out = append(out, h<<4|l)
					i += 2
					continue
				}
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
