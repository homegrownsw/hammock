package daemon

import (
	"context"
	"time"

	"google.golang.org/protobuf/proto"
	"slices"
	"testing"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestFloatingPanes(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tb := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: cat}}}))
	d.mu.Lock()
	p1 := d.tabs[tb].FocusedPaneId
	d.mu.Unlock()
	split := func(target string) string {
		return client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: target, Spec: cat}}}))
	}
	float := func(m *pb.FloatPane) error {
		_, err := callErr(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_FloatPane{FloatPane: m}})
		return err
	}
	state := func() (layout []string, floats []*pb.FloatingPane, focused string) {
		d.mu.Lock()
		defer d.mu.Unlock()
		tab := d.tabs[tb]
		return layoutPanes(tab.Layout), slices.Clone(tab.Floating), tab.FocusedPaneId
	}

	// A tab's only pane in its layout can't float.
	if err := float(&pb.FloatPane{Action: pb.FloatPane_ACTION_FLOAT, PaneId: p1}); err == nil {
		t.Fatal("floated the only pane")
	}

	// A new floating pane: over the tab, focused, bottom right.
	f1 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: p1, Floating: true, Spec: cat}}}))
	layout, floats, focused := state()
	if !slices.Equal(layout, []string{p1}) || len(floats) != 1 || floats[0].PaneId != f1 || focused != f1 {
		t.Fatalf("layout=%v floats=%v focused=%s", layout, floats, focused)
	}
	if fr := floats[0].Frame; fr.X+fr.W > 1 || fr.Y+fr.H > 1 || fr.X < 0.4 || fr.Y < 0.4 {
		t.Fatalf("frame %v", fr)
	}

	// Lift a split pane out (its frame clamped), then dock it back where it was.
	p2 := split(p1)
	if err := float(&pb.FloatPane{Action: pb.FloatPane_ACTION_FLOAT, PaneId: p2, Frame: &pb.Frame{X: -1, Y: 0.9, W: 0.05, H: 2}}); err != nil {
		t.Fatal(err)
	}
	layout, floats, _ = state()
	if !slices.Equal(layout, []string{p1}) || len(floats) != 2 || floats[1].PaneId != p2 || floats[1].OriginPaneId != p1 {
		t.Fatalf("after float: layout=%v floats=%v", layout, floats)
	}
	if fr := floats[1].Frame; fr.X != 0 || fr.W != minFloatSize || fr.H != 1 || fr.Y != 0 {
		t.Fatalf("frame not clamped: %v", fr)
	}
	if err := float(&pb.FloatPane{Action: pb.FloatPane_ACTION_DOCK, PaneId: p2}); err != nil {
		t.Fatal(err)
	}
	if layout, floats, _ = state(); !slices.Equal(layout, []string{p1, p2}) || len(floats) != 1 {
		t.Fatalf("after dock: layout=%v floats=%v", layout, floats)
	}

	// Focusing a minimised float brings it back to the front.
	f2 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: p1, Floating: true, Spec: cat}}}))
	if err := float(&pb.FloatPane{Action: pb.FloatPane_ACTION_MINIMISE, PaneId: f1}); err != nil {
		t.Fatal(err)
	}
	if _, floats, _ = state(); !floats[0].Minimised || floats[1].PaneId != f2 {
		t.Fatalf("after minimise: %v", floats)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Focus{Focus: &pb.Focus{PaneId: f1}}})
	if _, floats, focused = state(); floats[1].PaneId != f1 || floats[1].Minimised || focused != f1 {
		t.Fatalf("after focus: %v focused=%s", floats, focused)
	}

	// Toggle all: both minimised, focus back in the layout; again, both back.
	if err := float(&pb.FloatPane{Action: pb.FloatPane_ACTION_TOGGLE_ALL, TabId: tb}); err != nil {
		t.Fatal(err)
	}
	_, floats, focused = state()
	if !floats[0].Minimised || !floats[1].Minimised || !slices.Contains([]string{p1, p2}, focused) {
		t.Fatalf("after toggle: %v focused=%s", floats, focused)
	}
	if err := float(&pb.FloatPane{Action: pb.FloatPane_ACTION_TOGGLE_ALL, TabId: tb}); err != nil {
		t.Fatal(err)
	}
	if _, floats, _ = state(); floats[0].Minimised || floats[1].Minimised {
		t.Fatalf("after second toggle: %v", floats)
	}

	// Closing the layout's panes: the front float docks in as the layout.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: p2}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ClosePane{ClosePane: &pb.ClosePane{PaneId: p1}}})
	layout, floats, _ = state()
	if !slices.Equal(layout, []string{f1}) || len(floats) != 1 || floats[0].PaneId != f2 {
		t.Fatalf("after closing the layout: layout=%v floats=%v", layout, floats)
	}

	// Closing the tab closes its floats too.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tb}}})
	d.mu.Lock()
	gone := d.panes[f2] == nil
	d.mu.Unlock()
	if !gone {
		t.Fatal("float outlived its tab")
	}
}

func TestFloatOrigin(t *testing.T) {
	// a | (b over c): c sits after b, vertically; a before (b over c).
	l := &pb.Layout{Node: &pb.Layout_Split{Split: &pb.Split{Direction: pb.Split_DIRECTION_HORIZONTAL, Children: []*pb.Layout{
		leaf("a"),
		{Node: &pb.Layout_Split{Split: &pb.Split{Direction: pb.Split_DIRECTION_VERTICAL, Children: []*pb.Layout{leaf("b"), leaf("c")}}}},
	}}}}
	if n, dir, before := origin(l, "c"); n != "b" || dir != pb.Split_DIRECTION_VERTICAL || before {
		t.Fatalf("c: %s %v %v", n, dir, before)
	}
	if n, dir, before := origin(l, "a"); n != "b" || dir != pb.Split_DIRECTION_HORIZONTAL || !before {
		t.Fatalf("a: %s %v %v", n, dir, before)
	}
	// Taken out and docked back, the layout is as it was.
	for _, id := range []string{"a", "b", "c"} {
		want := layoutPanes(l)
		n, dir, before := origin(l, id)
		cp := removeFromLayout(cloneLayout(l), id)
		if !splitLayout(cp, n, id, dir, before) || !slices.Equal(layoutPanes(cp), want) {
			t.Fatalf("%s: got %v want %v", id, layoutPanes(cp), want)
		}
	}
}

func callErr(t *testing.T, c *client.Client, m *pb.ClientMessage) (*pb.ServerMessage, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Call(ctx, m)
}

func cloneLayout(l *pb.Layout) *pb.Layout { return proto.CloneOf(l) }
