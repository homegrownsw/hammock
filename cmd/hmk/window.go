package main

import (
	"fmt"
	"os"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// runWindow is hmk window open pane|tab|group [REF]: the Mac app shows the
// pane, tab or Group in a window of its own (Open in New Window), the same
// one as in the workspace. With no REF, the one this runs in.
func runWindow(w *world, call func(*pb.ClientMessage) (*pb.ServerMessage, error), args []string) error {
	const use = "usage: hmk window open pane|tab|group [REF]"
	if len(args) < 2 || args[0] != "open" {
		return fmt.Errorf(use)
	}
	kinds := map[string]pb.OpenWindow_Kind{"pane": pb.OpenWindow_KIND_PANE, "tab": pb.OpenWindow_KIND_TAB, "group": pb.OpenWindow_KIND_GROUP}
	kind, ok := kinds[args[1]]
	if !ok {
		return fmt.Errorf(use)
	}
	var id string
	if len(args) > 2 {
		var err error
		if id, err = w.resolve(args[2], args[1]); err != nil {
			return err
		}
	} else {
		here := os.Getenv("HMK_PANE_ID")
		p := w.panes[here]
		if p == nil {
			return fmt.Errorf("not in a Hammock pane: say which %s (%s)", args[1], use)
		}
		switch kind {
		case pb.OpenWindow_KIND_PANE:
			id = p.Id
		case pb.OpenWindow_KIND_TAB:
			id = p.TabId
		case pb.OpenWindow_KIND_GROUP:
			if t := w.tabs[p.TabId]; t != nil {
				id = t.GroupId
			}
		}
	}
	_, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_OpenWindow{OpenWindow: &pb.OpenWindow{Kind: kind, Id: id}}})
	return err
}
