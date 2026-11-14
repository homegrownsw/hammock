package daemon

import (
	"errors"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// openWindow asks this Mac's app to show a pane, tab or Group in a window
// of its own (hmk window, MCP open_window). The window shows the workspace's
// own pane, tab or Group, so it opens here whichever computer that's on: a
// linked computer's tab, or one in a Group shared with you, is in this
// app's workspace like any. Callers hold d.mu.
func (d *Daemon) openWindow(m *pb.OpenWindow) error {
	if m.Id == "" {
		return errors.New("no pane, tab or Group to show")
	}
	known := d.routes[m.Id] != nil
	switch m.Kind {
	case pb.OpenWindow_KIND_PANE:
		known = known || d.panes[m.Id] != nil
	case pb.OpenWindow_KIND_TAB:
		known = known || d.tabs[m.Id] != nil
	case pb.OpenWindow_KIND_GROUP:
		known = known || d.groups[m.Id] != nil
	default:
		return errors.New("open a window for a pane, a tab or a Group")
	}
	if !known {
		return errors.New("no such " + windowKindName(m.Kind))
	}
	shown := false
	for o := range d.conns {
		// The app: local, not a guest, not a process in one of the panes.
		if o.kind == "mac" && !o.remote && o.peer == "" && o.caller == nil && o.subscribed {
			o.send(&pb.ServerMessage{Msg: &pb.ServerMessage_OpenWindow{OpenWindow: m}})
			shown = true
		}
	}
	if !shown {
		return errors.New("Hammock isn't open on this Mac")
	}
	return nil
}

func windowKindName(k pb.OpenWindow_Kind) string {
	switch k {
	case pb.OpenWindow_KIND_TAB:
		return "tab"
	case pb.OpenWindow_KIND_GROUP:
		return "Group"
	}
	return "pane"
}
