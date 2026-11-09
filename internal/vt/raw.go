//go:build !ghostty

package vt

import "regexp"

// Emulated reports a full terminal emulator behind the mirror (libghostty-vt):
// without one, a mirror's text is recent output, not the screen.
const Emulated = false

// rawMirror is the fallback used when built without libghostty-vt: it keeps
// the most recent output and replays it after a terminal reset. Good enough
// for shells and line-oriented output; full-screen apps may need a redraw.
type rawMirror struct {
	buf []byte
}

const rawKeep = 512 << 10

func newMirror(cols, rows int) Mirror { return &rawMirror{} }

func (m *rawMirror) Write(p []byte) {
	m.buf = append(m.buf, p...)
	if len(m.buf) > 2*rawKeep {
		m.buf = append([]byte(nil), m.buf[len(m.buf)-rawKeep:]...)
	}
}

func (m *rawMirror) Resize(cols, rows int) {}

func (m *rawMirror) Snapshot() []byte {
	data := m.buf
	if len(data) > rawKeep {
		data = data[len(data)-rawKeep:]
	}
	// RIS (full reset) first so the replay starts from a clean terminal.
	return append([]byte("\x1bc"), data...)
}

var ansi = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]|\r`)

func (m *rawMirror) Text(scrollback bool) string {
	return ansi.ReplaceAllString(string(m.buf), "")
}

func (m *rawMirror) Engine() string { return "raw" }

func (m *rawMirror) BracketedPaste() bool { return false }

func (m *rawMirror) KittyKeyboard() bool { return false }

func (m *rawMirror) SetCellSize(int, int) {}

func (m *rawMirror) Close() {}

func (m *rawMirror) SetResponder(fn func([]byte)) {}
func (m *rawMirror) SetColors(Colors)             {}

func (m *rawMirror) BottomText(n int) string { return "" }
