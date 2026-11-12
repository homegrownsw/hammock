package tui

import "testing"

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
