package tui

import (
	"testing"
	"time"
)

// A mouse report split right after ESC [ is put back together, not sent to
// the pane as bytes and text.
func TestParseMouseSplitAfterCSI(t *testing.T) {
	var p inputParser
	if ev := p.parse([]byte("ab\x1b[")); len(ev) != 1 || ev[0].kind != evBytes || string(ev[0].data) != "ab" {
		t.Fatalf("first read: %+v", ev)
	}
	ev := p.parse([]byte("<35;20;10M"))
	if len(ev) != 1 || ev[0].kind != evMouse || ev[0].mouse.x != 19 || ev[0].mouse.y != 9 {
		t.Fatalf("second read: %+v", ev)
	}
}

// A held ESC [ that turns out to be something else goes on intact.
func TestParseHeldCSIOtherSequence(t *testing.T) {
	var p inputParser
	p.parse([]byte("\x1b["))
	ev := p.parse([]byte("A"))
	if len(ev) != 1 || ev[0].kind != evBytes || string(ev[0].data) != "\x1b[A" {
		t.Fatalf("got %+v", ev)
	}
}

// A lone ESC at the end of a read is the Escape key, not held.
func TestParseLoneEscNotHeld(t *testing.T) {
	var p inputParser
	ev := p.parse([]byte("\x1b"))
	if len(ev) != 1 || string(ev[0].data) != "\x1b" || len(p.pending) != 0 {
		t.Fatalf("got %+v, pending %q", ev, p.pending)
	}
}

// What's held is passed on once nothing has followed it for a while.
func TestParseHeldGoesStale(t *testing.T) {
	var p inputParser
	p.parse([]byte("\x1b["))
	if ev := p.stale(time.Now()); ev != nil {
		t.Fatalf("passed on too soon: %+v", ev)
	}
	ev := p.stale(time.Now().Add(holdFor))
	if len(ev) != 1 || ev[0].kind != evBytes || string(ev[0].data) != "\x1b[" || len(p.pending) != 0 {
		t.Fatalf("got %+v, pending %q", ev, p.pending)
	}
}
