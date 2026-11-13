package tui

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The Orbit glyphs: work turns an indigo arc; needs-you and errors sit
// still whatever the frame; a seen, finished agent shows nothing.
func TestAgentGlyphOrbit(t *testing.T) {
	a := &App{th: newTheme(RGB{30, 30, 30}, RGB{230, 230, 230})}
	pane := func(s pb.AgentState) *pb.Pane { return &pb.Pane{Agent: &pb.Agent{Harness: "claude", State: s}} }

	working := pane(pb.AgentState_AGENT_STATE_WORKING)
	seen := map[string]bool{}
	for f := 0; f < fps*3; f += fps / 10 {
		a.frame = f
		g, ok := a.agentGlyph(working)
		if !ok || g.col != a.th.indigo {
			t.Fatalf("working at frame %d: %+v, %v", f, g, ok)
		}
		seen[g.ch] = true
	}
	if len(seen) != len(orbitFrames) {
		t.Errorf("working arc showed %v, want all of %v", seen, orbitFrames)
	}

	for _, s := range []pb.AgentState{pb.AgentState_AGENT_STATE_NEEDS_INPUT, pb.AgentState_AGENT_STATE_ERRORED} {
		a.frame = 0
		first, _ := a.agentGlyph(pane(s))
		for f := 1; f < fps*3; f += 7 {
			a.frame = f
			if g, _ := a.agentGlyph(pane(s)); g != first {
				t.Errorf("%v changed at frame %d: %+v → %+v", s, f, first, g)
			}
		}
	}

	// Starting turns at half the speed, dimmer.
	a.frame = 0
	s0, _ := a.agentGlyph(pane(pb.AgentState_AGENT_STATE_STARTING))
	a.frame = fps * 6 / 10
	s1, _ := a.agentGlyph(pane(pb.AgentState_AGENT_STATE_STARTING))
	if s0.ch != s1.ch || s0.col == a.th.indigo {
		t.Errorf("starting: %+v then %+v", s0, s1)
	}

	// Only work keeps the screen redrawing: an agent waiting for you doesn't,
	// a finished one running a command you typed does.
	a.panes = map[string]*pb.Pane{"p": pane(pb.AgentState_AGENT_STATE_NEEDS_INPUT)}
	if a.hasLiveGlyphs() {
		t.Error("needs input still animates")
	}
	running := pane(pb.AgentState_AGENT_STATE_IDLE)
	running.Agent.RunningCommand = true
	a.panes = map[string]*pb.Pane{"p": running}
	if !a.hasLiveGlyphs() {
		t.Error("a command the agent's running doesn't animate")
	}

	done := pane(pb.AgentState_AGENT_STATE_IDLE)
	if g, ok := a.agentGlyph(done); !ok || g.ch != "✓" {
		t.Errorf("finished: %+v, %v", g, ok)
	}
	done.Agent.Seen = true
	if _, ok := a.agentGlyph(done); ok {
		t.Error("a finished agent that was seen still shows a glyph")
	}

	// Asleep: a moon, whatever it was doing when it went to sleep.
	done.Asleep = true
	if g, ok := a.agentGlyph(done); !ok || g.ch != "☾" {
		t.Errorf("asleep: %+v, %v", g, ok)
	}
	idle := pane(pb.AgentState_AGENT_STATE_IDLE)
	idle.Agent.SessionId = "s"
	if !canSleep(idle) || canSleep(done) {
		t.Errorf("canSleep: idle %v, asleep %v", canSleep(idle), canSleep(done))
	}
	idle.Agent.State = pb.AgentState_AGENT_STATE_WORKING
	if canSleep(idle) {
		t.Error("a working agent can be put to sleep")
	}
}
