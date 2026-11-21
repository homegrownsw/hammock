package tui

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A tab in Horizontal Tabs' bar can be dragged onto another place in it,
// and a click without a drag still selects it (on release).
func TestTabBarDrag(t *testing.T) {
	a := &App{}
	for i, id := range []string{"t1", "t2", "t3"} {
		a.addZone(zone{r: rect{i * 20, 1, 20, 1}, kind: zBarTab, id: id, group: "g", idx: i})
	}
	z, _ := a.zoneAt(5, 1)
	a.leftPress(z, mouseEv{x: 5, y: 1, press: true})
	if a.drag == nil || a.drag.kind != dragTab || a.drag.id != "t1" {
		t.Fatalf("press didn't start a drag: %+v", a.drag)
	}
	a.dragMouse(mouseEv{x: 45, y: 1, motion: true})
	if d := a.drag; !d.moved || d.dropGroup != "g" || d.dropIndex != 2 {
		t.Fatalf("drag over the third tab: %+v", d)
	}
}

// Middle-click on a tab closes it, asking first, as any close does, when
// it would stop an agent at work.
func TestTabBarMiddleClickCloses(t *testing.T) {
	a, tab := paletteApp()
	tab("t1", &pb.Pane{Agent: &pb.Agent{Harness: "claude", State: pb.AgentState_AGENT_STATE_WORKING}})
	a.addZone(zone{r: rect{0, 1, 20, 1}, kind: zBarTab, id: "t1", group: "g"})
	a.mouse(mouseEv{x: 5, y: 1, button: 1, press: true})
	if d, ok := a.overlay.(*dialog); !ok || !d.confirm || d.title != "Claude is still working" {
		t.Fatalf("middle-click on a working agent's tab opened %#v", a.overlay)
	}
}

// The wheel over the tab bar steps through its Group's tabs, stopping at
// either end.
func TestTabBarWheelSwitchesTabs(t *testing.T) {
	a, tab := paletteApp()
	for _, id := range []string{"t1", "t2", "t3"} {
		tab(id, &pb.Pane{Kind: pb.PaneKind_PANE_KIND_BROWSER}) // no terminal to attach
		a.tabs[id].GroupId = "g"
	}
	a.groups = []*pb.Group{{Id: "g", TabIds: []string{"t1", "t2", "t3"}}}
	a.selTab = "t1"
	z := zone{kind: zBarTab, id: "t2"}
	for _, c := range []struct {
		wheel int
		want  string
	}{{-1, "t1"}, {1, "t2"}, {1, "t3"}, {1, "t3"}, {-1, "t2"}} {
		a.wheel(z, mouseEv{wheel: c.wheel})
		if a.selTab != c.want {
			t.Fatalf("wheel %+d: %s selected, want %s", c.wheel, a.selTab, c.want)
		}
	}
}

// The selected tab's colour slides along the bar from the tab before:
// part way there, a frame after switching, it's between the two.
func TestTabBarHighlightSlides(t *testing.T) {
	a, tab := paletteApp()
	a.w, a.h, a.scr = 50, 5, NewScreen(50, 5) // narrow: no sidebar
	a.th = newTheme(RGB{0, 0, 0}, RGB{255, 255, 255})
	a.prefs.Layout = "horizontal"
	for _, id := range []string{"t1", "t2"} {
		tab(id)
		a.tabs[id].GroupId = "g"
	}
	a.groups = []*pb.Group{{Id: "g", TabIds: []string{"t1", "t2"}}}
	a.selTab = "t1"
	a.drawTabBar() // in place at t1, the bar's first 23 cells
	a.selTab = "t2"
	a.drawTabBar() // aiming for t2, still at t1
	if bg := a.scr.Get(2, 1).BG; bg != a.th.bg {
		t.Fatalf("the highlight left t1 at once: %v", bg)
	}
	a.anim.step()
	a.anim.step()
	a.drawTabBar()
	pos := a.anim.get("barpill:g", snappy).pos
	if pos <= 0 || pos >= 23 {
		t.Fatalf("highlight at %v two frames on, want between 0 and 23", pos)
	}
	if bg := a.scr.Get(44, 1).BG; bg == a.th.bg {
		t.Error("t2's far end lit before the highlight got there")
	}
}
