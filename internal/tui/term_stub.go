//go:build !ghostty

package tui

import "errors"

// Available reports whether the TUI can run in this build.
const Available = false

type paneTerm struct{ cols, rows int }

func newPaneTerm(cols, rows int) (*paneTerm, error) {
	return nil, errors.New("the TUI needs a build with libghostty-vt (go build -tags ghostty)")
}

func (p *paneTerm) write([]byte)                                      {}
func (p *paneTerm) reset(cols, rows int)                              {}
func (p *paneTerm) resize(cols, rows int)                             {}
func (p *paneTerm) draw(*Screen, int, int, int, int) (int, int, bool) { return 0, 0, false }
func (p *paneTerm) close()                                            {}
func (p *paneTerm) encodeMouse(mouseEv, int, int) []byte              { return nil }
func (p *paneTerm) wheelAsArrows() bool                               { return false }
func (p *paneTerm) altScreen() bool                                   { return false }
func (p *paneTerm) bracketedPaste() bool                              { return false }
func (p *paneTerm) scroll(int)                                        {}
func (p *paneTerm) scrollToBottom()                                   {}
func (p *paneTerm) scrollback() (int, int, int)                       { return 0, 0, 0 }
