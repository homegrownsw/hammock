package tui

import "testing"

// Animations must finish fast enough not to feel like lag.
func TestSpringsSettleQuickly(t *testing.T) {
	for name, c := range map[string]struct {
		s      *spring
		frames int // at 60fps
	}{"snappy": {newSpring(snappy), 9}, "gentle": {newSpring(gentle), 9}, "quick": {newSpring(quick), 5}} {
		c.s.from(0)
		c.s.to(1)
		n := 0
		for c.s.step() {
			n++
		}
		// "Close enough to look done": within 5%.
		c.s.from(0)
		c.s.to(1)
		near := 0
		for c.s.pos < 0.95 {
			c.s.step()
			near++
		}
		t.Logf("%s: 95%% after %d frames, settled after %d", name, near, n)
		if near > c.frames {
			t.Errorf("%s takes %d frames to get there", name, near)
		}
	}
}

func TestSpringsSnapWithAnimationsOff(t *testing.T) {
	animations = false
	defer func() { animations = true }()
	s := newSpring(gentle)
	s.to(0)
	s.from(0)
	s.to(1)
	if s.pos != 1 || s.step() {
		t.Fatalf("pos %v: want an instant jump with tui-animations = false", s.pos)
	}
}
