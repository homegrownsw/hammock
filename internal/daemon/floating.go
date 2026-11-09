package daemon

import (
	"errors"
	"fmt"
	"slices"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// Floating panes: a tab's panes floating over its layout (Tab.floating,
// back to front) rather than in it. A tab always keeps a layout: the last
// pane in it can't float, and when it closes the front float docks in.
// Frames are fractions of the tab's area, so every client (the Mac, the
// TUI) draws the same arrangement at its own size.

// The smallest float, as a fraction of the tab: clients also hold it to a
// minimum in points or cells.
const minFloatSize = 0.15

// newFloatFrame is where a new floating pane opens: the tab's bottom right,
// half its width and a little under half its height, each one more a step
// up and left of the ones already there so they don't sit exactly on top of
// each other.
func newFloatFrame(t *pb.Tab) *pb.Frame {
	const w, h, step = 0.5, 0.45, 0.04
	n := 0
	for _, f := range t.Floating {
		if !f.Minimised {
			n++
		}
	}
	off := float32(n%5) * step
	return &pb.Frame{X: 1 - w - 0.03 - off, Y: 1 - h - 0.05 - off, W: w, H: h}
}

// clampFrame keeps a frame inside the tab and no smaller than minFloatSize.
func clampFrame(f *pb.Frame) *pb.Frame {
	c := &pb.Frame{W: f.GetW(), H: f.GetH(), X: f.GetX(), Y: f.GetY()}
	c.W = min(max(c.W, minFloatSize), 1)
	c.H = min(max(c.H, minFloatSize), 1)
	c.X = min(max(c.X, 0), 1-c.W)
	c.Y = min(max(c.Y, 0), 1-c.H)
	return c
}

// tabPanes lists every pane of a tab: its layout's, then its floats' (back
// to front).
func tabPanes(t *pb.Tab) []string {
	ids := layoutPanes(t.GetLayout())
	for _, f := range t.GetFloating() {
		ids = append(ids, f.PaneId)
	}
	return ids
}

// floatIndex is where pane id is in t's floats, or -1.
func floatIndex(t *pb.Tab, id string) int {
	return slices.IndexFunc(t.GetFloating(), func(f *pb.FloatingPane) bool { return f.PaneId == id })
}

// origin is where id sits in layout, to put it back there: a pane beside it,
// the direction of the split they share, and whether id comes before it.
func origin(l *pb.Layout, id string) (string, pb.Split_Direction, bool) {
	s := l.GetSplit()
	if s == nil {
		return "", 0, false
	}
	for i, c := range s.Children {
		if c.GetPaneId() == id {
			if i > 0 {
				ids := layoutPanes(s.Children[i-1])
				return ids[len(ids)-1], s.Direction, false
			}
			if len(s.Children) > 1 {
				return layoutPanes(s.Children[1])[0], s.Direction, true
			}
			return "", 0, false
		}
		if n, dir, before := origin(c, id); n != "" {
			return n, dir, before
		}
	}
	return "", 0, false
}

// createFloating starts a pane floating over targetID's tab. Callers hold d.mu.
func (d *Daemon) createFloating(targetID string, spec *pb.PaneSpec, frame *pb.Frame) (*pane, error) {
	target, ok := d.panes[targetID]
	if !ok {
		return nil, fmt.Errorf("no such pane %q", targetID)
	}
	t := d.tabs[target.tabID()]
	if t == nil {
		return nil, fmt.Errorf("pane %q has no tab", targetID)
	}
	if spec == nil {
		spec = &pb.PaneSpec{}
	}
	if spec.Cwd == "" {
		spec.Cwd = target.cwd()
	}
	p, err := d.startPane(t, spec)
	if err != nil {
		return nil, err
	}
	if frame == nil {
		frame = newFloatFrame(t)
	}
	t.Floating = append(t.Floating, &pb.FloatingPane{PaneId: p.id, Frame: clampFrame(frame)})
	d.noteFocus(t.Id, targetID)
	d.noteFocus(t.Id, p.id)
	t.FocusedPaneId = p.id
	d.persist(d.store.PutTab(t))
	d.publish(&pb.StateUpdate{Tabs: []*pb.Tab{proto.CloneOf(t)}, Panes: []*pb.Pane{p.snapshotInfo()}})
	return p, nil
}

// floatTarget is what a FloatPane acts on: the tab for TOGGLE_ALL (or when
// no pane is named), else the pane. Checks and routing use it, so they look
// at the same id floatPane does.
func floatTarget(m *pb.FloatPane) string {
	if m.Action == pb.FloatPane_ACTION_TOGGLE_ALL || m.PaneId == "" {
		return m.TabId
	}
	return m.PaneId
}

// floatPane applies a FloatPane request. Callers hold d.mu.
func (d *Daemon) floatPane(m *pb.FloatPane) error {
	if m.Action == pb.FloatPane_ACTION_TOGGLE_ALL {
		t := d.tabs[m.TabId]
		if t == nil {
			return fmt.Errorf("no such tab %q", m.TabId)
		}
		if !toggleAllFloats(t) {
			return nil
		}
		if i := floatIndex(t, t.FocusedPaneId); i >= 0 && t.Floating[i].Minimised {
			t.FocusedPaneId = d.previousFocus(t, t.FocusedPaneId)
		}
		return d.tabChanged(t)
	}
	p, ok := d.panes[m.PaneId]
	if !ok {
		return fmt.Errorf("no such pane %q", m.PaneId)
	}
	t := d.tabs[p.tabID()]
	if t == nil {
		return fmt.Errorf("pane %q has no tab", m.PaneId)
	}
	i := floatIndex(t, m.PaneId)
	if m.Action == pb.FloatPane_ACTION_FLOAT {
		if i >= 0 {
			return nil // already floating
		}
		if len(layoutPanes(t.Layout)) < 2 {
			return errors.New("a tab's only pane in its layout can't float")
		}
		next, dir, before := origin(t.Layout, m.PaneId)
		t.Layout = removeFromLayout(t.Layout, m.PaneId)
		frame := m.Frame
		if frame == nil {
			frame = newFloatFrame(t)
		}
		t.Floating = append(t.Floating, &pb.FloatingPane{
			PaneId: m.PaneId, Frame: clampFrame(frame),
			OriginPaneId: next, OriginDirection: dir, OriginBefore: before,
		})
		d.noteFocus(t.Id, m.PaneId)
		t.FocusedPaneId = m.PaneId
		return d.tabChanged(t)
	}
	if i < 0 {
		return errors.New("the pane isn't floating")
	}
	f := t.Floating[i]
	switch m.Action {
	case pb.FloatPane_ACTION_DOCK:
		if err := d.dock(t, f, m.TargetPaneId, m.Direction, m.Before); err != nil {
			return err
		}
		d.noteFocus(t.Id, m.PaneId)
		t.FocusedPaneId = m.PaneId
	case pb.FloatPane_ACTION_FRAME:
		if m.Frame == nil {
			return errors.New("frame is required")
		}
		f.Frame = clampFrame(m.Frame)
		f.Zoomed = false
	case pb.FloatPane_ACTION_MINIMISE:
		if f.Minimised {
			return nil
		}
		f.Minimised, f.MinimisedByAll = true, false
		if t.FocusedPaneId == f.PaneId {
			t.FocusedPaneId = d.previousFocus(t, f.PaneId)
		}
	case pb.FloatPane_ACTION_RESTORE, pb.FloatPane_ACTION_RAISE:
		raiseFloat(t, f.PaneId)
		d.noteFocus(t.Id, f.PaneId)
		t.FocusedPaneId = f.PaneId
	case pb.FloatPane_ACTION_ZOOM:
		f.Zoomed = !f.Zoomed
		raiseFloat(t, f.PaneId)
	default:
		return fmt.Errorf("unknown action %v", m.Action)
	}
	return d.tabChanged(t)
}

// dock moves float f into t's layout: beside target (dir, before), or with
// no target back where it came from, else right of the focused pane.
func (d *Daemon) dock(t *pb.Tab, f *pb.FloatingPane, target string, dir pb.Split_Direction, before bool) error {
	inLayout := func(id string) bool { return id != "" && slices.Contains(layoutPanes(t.Layout), id) }
	if target == "" {
		target, dir, before = f.OriginPaneId, f.OriginDirection, f.OriginBefore
		if !inLayout(target) {
			target, dir, before = t.FocusedPaneId, pb.Split_DIRECTION_HORIZONTAL, false
		}
		if !inLayout(target) {
			// Focus is on a float: its last focused pane in the layout, else the first.
			target = layoutPanes(t.Layout)[0]
			o := d.focusOrder[t.Id]
			for _, id := range slices.Backward(o) {
				if inLayout(id) {
					target = id
					break
				}
			}
		}
	}
	if !inLayout(target) {
		return fmt.Errorf("pane %q is not in its tab's layout", target)
	}
	if dir == pb.Split_DIRECTION_UNSPECIFIED {
		dir = pb.Split_DIRECTION_HORIZONTAL
	}
	if !splitLayout(t.Layout, target, f.PaneId, dir, before) {
		return fmt.Errorf("pane %q is not in its tab's layout", target)
	}
	t.Floating = slices.DeleteFunc(t.Floating, func(x *pb.FloatingPane) bool { return x == f })
	return nil
}

// raiseFloat brings a float to the front, off the shelf. Reports whether
// that changed anything.
func raiseFloat(t *pb.Tab, id string) bool {
	i := floatIndex(t, id)
	if i < 0 {
		return false
	}
	f := t.Floating[i]
	changed := f.Minimised || i != len(t.Floating)-1
	f.Minimised, f.MinimisedByAll = false, false
	t.Floating = append(slices.Delete(t.Floating, i, i+1), f)
	return changed
}

// toggleAllFloats minimises every float showing, or when none is, restores
// those it minimised. Reports whether anything changed.
func toggleAllFloats(t *pb.Tab) bool {
	changed := false
	for _, f := range t.Floating {
		if !f.Minimised {
			f.Minimised, f.MinimisedByAll = true, true
			changed = true
		}
	}
	if changed {
		return true
	}
	for _, f := range t.Floating {
		if f.MinimisedByAll {
			f.Minimised, f.MinimisedByAll = false, false
			changed = true
		}
	}
	return changed
}

// tabChanged persists and publishes t. Callers hold d.mu.
func (d *Daemon) tabChanged(t *pb.Tab) error {
	d.persist(d.store.PutTab(t))
	d.publish(&pb.StateUpdate{Tabs: []*pb.Tab{proto.CloneOf(t)}})
	return nil
}

// removeFloat takes pane id off t's floats. Reports whether it was one.
func removeFloat(t *pb.Tab, id string) bool {
	n := len(t.Floating)
	t.Floating = slices.DeleteFunc(t.Floating, func(f *pb.FloatingPane) bool { return f.PaneId == id })
	return len(t.Floating) != n
}

// promoteFloat docks t's front float as its whole layout, when the layout's
// last pane has gone. Reports whether there was one.
func promoteFloat(t *pb.Tab) bool {
	if t.Layout != nil || len(t.Floating) == 0 {
		return false
	}
	// The front one showing, else the front one on the shelf.
	i := len(t.Floating) - 1
	for j := len(t.Floating) - 1; j >= 0; j-- {
		if !t.Floating[j].Minimised {
			i = j
			break
		}
	}
	t.Layout = leaf(t.Floating[i].PaneId)
	t.Floating = slices.Delete(t.Floating, i, i+1)
	return true
}

// pruneFloats drops floats whose panes are gone or also in the layout (a
// tab stored by a daemon that crashed mid-change), and gives a tab with
// none in its layout one of them. Reports whether t changed.
func (d *Daemon) pruneFloats(t *pb.Tab) bool {
	n := len(t.Floating)
	inLayout := layoutPanes(t.Layout)
	seen := map[string]bool{}
	t.Floating = slices.DeleteFunc(t.Floating, func(f *pb.FloatingPane) bool {
		gone := d.panes[f.PaneId] == nil || slices.Contains(inLayout, f.PaneId) || seen[f.PaneId]
		seen[f.PaneId] = true
		return gone
	})
	changed := len(t.Floating) != n
	return promoteFloat(t) || changed
}
