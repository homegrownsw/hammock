package tui

import (
	"strings"
	"testing"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// the palette lists the tabs that need you first, then unseen
// finished ones, then the rest by when you last opened them.

func paletteApp() (*App, func(id string, panes ...*pb.Pane)) {
	a := &App{anim: newSprings(), tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{}, opened: map[string]int64{}}
	return a, func(id string, panes ...*pb.Pane) {
		l := &pb.Layout{Node: &pb.Layout_PaneId{PaneId: id + "-p"}}
		if len(panes) == 0 {
			panes = []*pb.Pane{{}}
		}
		if len(panes) > 1 {
			var kids []*pb.Layout
			for i, p := range panes {
				p.Id = id + "-p" + string(rune('0'+i))
				kids = append(kids, &pb.Layout{Node: &pb.Layout_PaneId{PaneId: p.Id}})
			}
			l = &pb.Layout{Node: &pb.Layout_Split{Split: &pb.Split{Children: kids}}}
		} else {
			panes[0].Id = id + "-p"
		}
		a.tabs[id] = &pb.Tab{Id: id, Layout: l}
		for _, p := range panes {
			a.panes[p.Id] = p
		}
	}
}

func agentPane(s pb.AgentState, since int64, seen bool) *pb.Pane {
	return &pb.Pane{Agent: &pb.Agent{State: s, StateSinceUnixMs: since, Seen: seen}}
}

func checkOrder(t *testing.T, a *App, ids []string, want ...string) {
	t.Helper()
	if got := a.paletteOrder(ids); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%v, want %v", got, want)
	}
}

func TestPaletteOrder(t *testing.T) {
	a, add := paletteApp()
	a.opened = map[string]int64{"c": 20, "seen": 10}
	add("shell")
	add("c")
	add("working", agentPane(pb.AgentState_AGENT_STATE_WORKING, 0, false))
	add("done", agentPane(pb.AgentState_AGENT_STATE_IDLE, 5, false))
	add("failed", agentPane(pb.AgentState_AGENT_STATE_ERRORED, 9, false))
	add("seen", agentPane(pb.AgentState_AGENT_STATE_IDLE, 6, true))
	add("old", agentPane(pb.AgentState_AGENT_STATE_NEEDS_INPUT, 1, false))
	add("new", agentPane(pb.AgentState_AGENT_STATE_NEEDS_INPUT, 2, false))
	checkOrder(t, a, []string{"shell", "c", "working", "done", "failed", "seen", "old", "new"},
		"new", "old", "failed", "done", "c", "seen", "shell", "working")
}

func TestPaletteOrderPutsTheTabYoureOnLast(t *testing.T) {
	a, add := paletteApp()
	a.opened = map[string]int64{"a": 10, "b": 20}
	add("a")
	add("b")
	add("c")
	a.selTab = "b"
	checkOrder(t, a, []string{"a", "b", "c"}, "a", "c", "b")
}

func TestPaletteOrderAFailureYouveSeen(t *testing.T) {
	a, add := paletteApp()
	add("x")
	add("failed", agentPane(pb.AgentState_AGENT_STATE_ERRORED, 9, false))
	checkOrder(t, a, []string{"x", "failed"}, "failed", "x")
	// Opened since it failed (the daemon never marks it seen).
	a.opened = map[string]int64{"failed": 10, "x": 5}
	checkOrder(t, a, []string{"x", "failed"}, "failed", "x")
	a.opened["x"] = 11
	checkOrder(t, a, []string{"x", "failed"}, "x", "failed")
}

func TestPaletteOrderTheMostUrgentPaneSpeaksForItsTab(t *testing.T) {
	a, add := paletteApp()
	add("x")
	add("split", agentPane(pb.AgentState_AGENT_STATE_WORKING, 0, false), agentPane(pb.AgentState_AGENT_STATE_NEEDS_INPUT, 1, false))
	add("closed", &pb.Pane{Exited: true, Agent: &pb.Agent{State: pb.AgentState_AGENT_STATE_NEEDS_INPUT}})
	// A command you ran while it was idle: at work, not finished.
	add("cmd", &pb.Pane{Agent: &pb.Agent{State: pb.AgentState_AGENT_STATE_IDLE, RunningCommand: true}})
	checkOrder(t, a, []string{"x", "closed", "cmd", "split"}, "split", "x", "closed", "cmd")
}

func TestATabThatArrivesLaterIsNotedOpened(t *testing.T) {
	a, _ := paletteApp()
	a.selectTab("new") // not here yet
	if a.opened["new"] != 0 {
		t.Fatal("noted before it arrived")
	}
	a.apply(&pb.StateUpdate{
		Groups: []*pb.Group{{Id: "g", TabIds: []string{"new"}}},
		Tabs:   []*pb.Tab{{Id: "new", GroupId: "g", Layout: &pb.Layout{Node: &pb.Layout_PaneId{PaneId: "p"}}}},
		Panes:  []*pb.Pane{{Id: "p", TabId: "new", Kind: pb.PaneKind_PANE_KIND_BROWSER}}, // no terminal to attach
	})
	if a.selTab != "new" || a.opened["new"] == 0 {
		t.Errorf("selTab %q, opened %v", a.selTab, a.opened)
	}
}
