package tui

import (
	"fmt"
	"strings"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// needsApp has six tabs, each one agent's pane; attention for the given
// panes, in Needs You's order, with "done" ones finished.
func needsApp(states map[string]pb.AgentState, order ...string) *App {
	a := &App{w: 100, h: 30, scr: NewScreen(100, 30), th: newTheme(RGB{30, 30, 30}, RGB{220, 220, 220}),
		anim: newSprings(), actions: make(chan func(), 64), tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{}}
	g := &pb.Group{Id: "g", Name: "Work"}
	for i := 1; i <= 6; i++ {
		t, p := fmt.Sprintf("t%d", i), fmt.Sprintf("p%d", i)
		g.TabIds = append(g.TabIds, t)
		a.tabs[t] = &pb.Tab{Id: t, GroupId: "g", Name: "Agent " + t, FocusedPaneId: p, Layout: &pb.Layout{Node: &pb.Layout_PaneId{PaneId: p}}}
		a.panes[p] = &pb.Pane{Id: p, TabId: t}
	}
	a.groups = []*pb.Group{g}
	for _, p := range order {
		a.attention = append(a.attention, &pb.AttentionItem{Id: "at_" + p, PaneId: p, State: states[p], Summary: "asks " + p})
	}
	return a
}

const (
	waiting  = pb.AgentState_AGENT_STATE_NEEDS_INPUT
	failed   = pb.AgentState_AGENT_STATE_ERRORED
	finished = pb.AgentState_AGENT_STATE_IDLE
)

// ⌃B a goes through the agents waiting on you in Needs You's order,
// skipping finished ones, and wraps; ⌃B A goes back.
func TestNextNeedingYouCycles(t *testing.T) {
	a := needsApp(map[string]pb.AgentState{"p2": waiting, "p4": finished, "p5": failed}, "p5", "p4", "p2")
	if got := strings.Join(a.needsYouPanes(), ","); got != "p5,p2" {
		t.Fatalf("needsYouPanes %s", got)
	}
	for _, c := range []struct {
		from string
		d    int
		want string
	}{
		{"p1", 1, "p5"},  // not waiting: the first
		{"p5", 1, "p2"},  // the next
		{"p2", 1, "p5"},  // wraps
		{"p5", -1, "p2"}, // back, wrapping
		{"p4", -1, "p2"}, // finished: back starts at the last
	} {
		a.focus = c.from
		if got := a.nextNeedingYou(c.d); got != c.want {
			t.Errorf("from %s by %d: %s, want %s", c.from, c.d, got, c.want)
		}
	}
}

func TestNextNeedingYouWithNone(t *testing.T) {
	a := needsApp(map[string]pb.AgentState{"p1": finished}, "p1")
	a.selTab, a.focus = "t3", "p3"
	a.command([]byte{'a'})
	if a.selTab != "t3" || !strings.Contains(a.toast, "No agent") {
		t.Fatalf("moved to %s, toast %q", a.selTab, a.toast)
	}
}

// More than Needs You has room for: "+N more", which opens the palette
// listing only what needs you.
func TestNeedsYouMoreRow(t *testing.T) {
	animations = false
	defer func() { animations = true }()
	states := map[string]pb.AgentState{}
	order := []string{"p1", "p2", "p3", "p4", "p5", "p6"}
	for _, p := range order {
		states[p] = waiting
	}
	a := needsApp(states, order...)
	a.grants = []*pb.GrantRequest{{Id: "gr1", Action: "open"}}
	if g, n, more := a.needsLayout(); g != 1 || n != 3 || more != 3 {
		t.Fatalf("layout %d %d %d", g, n, more)
	}
	a.drawSidebar()
	out := screenText(a.scr)
	t.Log("\n" + out)
	if !strings.Contains(out, "Needs You  7") || !strings.Contains(out, "+3 more") {
		t.Fatalf("sidebar:\n%s", out)
	}
	var more *zone
	for i := range a.zones {
		if a.zones[i].kind == zNeedsMore {
			more = &a.zones[i]
		}
	}
	if more == nil {
		t.Fatal("no +N more zone")
	}
	a.mouse(mouseEv{x: more.r.x + 2, y: more.r.y, press: true})
	p, ok := a.overlay.(*palette)
	if !ok || !p.needs {
		t.Fatalf("overlay %T", a.overlay)
	}
	items := p.filtered(a)
	if len(items) != 7 || items[0].id != "grant:gr1" || items[1].id != "needs:at_p1" {
		t.Fatalf("items %+v", items)
	}
	p.query = "agent t6"
	if items := p.filtered(a); len(items) != 1 || items[0].id != "needs:at_p6" {
		t.Fatalf("filtered %+v", items)
	}

	// Room for them all: no row.
	a.overlay, a.grants, a.attention = nil, nil, a.attention[:4]
	a.scr = NewScreen(a.w, a.h)
	a.zones = nil
	a.drawSidebar()
	if out := screenText(a.scr); strings.Contains(out, "more") {
		t.Fatalf("sidebar:\n%s", out)
	}
}

// A row new to Needs You pulses once; ones there at startup (a full
// state) don't, nor one sent again.
func TestNeedsYouNewRowsPulse(t *testing.T) {
	a, _ := paletteApp()
	a.apply(&pb.StateUpdate{Full: true, Attention: []*pb.AttentionItem{{Id: "old"}}})
	if a.pulseAmount("needs:old") != 0 {
		t.Error("a row there at startup pulsed")
	}
	a.apply(&pb.StateUpdate{Attention: []*pb.AttentionItem{{Id: "new"}}, Grants: []*pb.GrantRequest{{Id: "g"}}})
	if a.pulseAmount("needs:new") != 1 || a.pulseAmount("grant:g") != 1 {
		t.Error("a new row didn't pulse")
	}
	for range 120 {
		a.anim.step()
	}
	a.apply(&pb.StateUpdate{Attention: []*pb.AttentionItem{{Id: "new", Summary: "changed"}}})
	if v := a.pulseAmount("needs:new"); v > 0.01 {
		t.Errorf("a row sent again pulsed again (%v)", v)
	}
}
