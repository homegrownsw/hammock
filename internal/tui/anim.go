package tui

import (
	"math"

	"github.com/charmbracelet/harmonica"
)

// Animations are damped springs (harmonica), stepped once per frame. The
// app keeps redrawing at frame rate only while one is still moving.

const fps = 60

// spring animates a value toward a target.
type spring struct {
	pos, vel, target float64
	s                harmonica.Spring
	started          bool
}

// Presets, all critically (or nearly) damped and quick: a terminal redraws
// in whole cells, so anything slower reads as lag rather than motion.
var (
	snappy = harmonica.NewSpring(harmonica.FPS(fps), 30, 0.9) // selection, highlights
	gentle = harmonica.NewSpring(harmonica.FPS(fps), 36, 1)   // things that open and close
	quick  = harmonica.NewSpring(harmonica.FPS(fps), 70, 1)   // hover fades
)

func newSpring(s harmonica.Spring) *spring { return &spring{s: s} }

// to sets the target; the first call jumps straight there (nothing to
// animate from yet).
func (s *spring) to(t float64) {
	if !s.started || !animations {
		s.pos, s.target, s.started = t, t, true
		return
	}
	s.target = t
}

// from restarts the animation from v.
func (s *spring) from(v float64) {
	if !animations {
		return // stays where it's going
	}
	s.pos, s.vel, s.started = v, 0, true
}

// step advances one frame and reports whether it's still moving.
func (s *spring) step() bool {
	if s.settled() || !animations {
		s.pos, s.vel = s.target, 0
		return false
	}
	s.pos, s.vel = s.s.Update(s.pos, s.vel, s.target)
	return true
}

func (s *spring) settled() bool {
	return math.Abs(s.pos-s.target) < 0.004 && math.Abs(s.vel) < 0.01
}

// clamp01 limits overshoot where it would look wrong (fades).
func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// springs is a keyed set of springs, created on first use; ones not used
// for a while are dropped.
type springs struct {
	m    map[string]*spring
	used map[string]bool
}

func newSprings() *springs { return &springs{m: map[string]*spring{}, used: map[string]bool{}} }

func (ss *springs) get(key string, preset harmonica.Spring) *spring {
	s := ss.m[key]
	if s == nil {
		s = newSpring(preset)
		ss.m[key] = s
	}
	ss.used[key] = true
	return s
}

// step advances every spring; reports whether any is moving.
func (ss *springs) step() bool {
	moving := false
	for k, s := range ss.m {
		if !ss.used[k] && s.settled() {
			delete(ss.m, k)
			continue
		}
		if s.step() {
			moving = true
		}
	}
	ss.used = map[string]bool{}
	return moving
}

// spinner frames for a working agent.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
