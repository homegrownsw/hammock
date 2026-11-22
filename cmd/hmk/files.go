package main

// hmk files: open a files pane (a folder's tree) beside this pane, or show a
// file in the one in this tab.

import (
	"errors"
	"fmt"
	"os"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const filesUsage = `usage: hmk files [FOLDER] [--reveal PATH] [--right | --tab | --split PANE | --group G]`

// filesInTab finds a files pane in tab tabID.
func (w *world) filesInTab(tabID string) string {
	t := w.tabs[tabID]
	if t == nil {
		return ""
	}
	for _, id := range tabPaneIDs(t) {
		if p := w.panes[id]; p != nil && p.Kind == pb.PaneKind_PANE_KIND_FILES {
			return id
		}
	}
	return ""
}

func runFiles(w *world, call caller, args []string) error {
	usage := errors.New(filesUsage)
	args = args[1:]
	var folder, reveal, split, group string
	var right, newTab bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s wants a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--reveal":
			reveal, err = value()
		case "--split":
			split, err = value()
		case "--group":
			group, err = value()
		case "--right":
			right = true
		case "--tab":
			newTab = true
		default:
			if len(a) > 0 && a[0] == '-' || folder != "" {
				return usage
			}
			folder = a
		}
		if err != nil {
			return err
		}
	}
	wd, _ := os.Getwd()
	if folder != "" {
		folder = resolvePath(folder, wd)
	}
	if reveal != "" {
		reveal = resolvePath(reveal, wd)
	}

	beside := split
	if beside != "" {
		var err error
		if beside, err = w.resolve(beside, "pane"); err != nil {
			return err
		}
	} else if !newTab && group == "" {
		if me := paneEnv("PANE_ID"); w.panes[me] != nil {
			beside = me
		}
	}
	// A files pane in the tab already: shown there.
	if beside != "" && folder == "" {
		if id := w.filesInTab(w.panes[beside].TabId); id != "" {
			if reveal != "" {
				if _, err := call(&pb.ClientMessage{Msg: &pb.ClientMessage_FilesCommand{FilesCommand: &pb.FilesCommand{
					PaneId: id, Action: pb.FilesCommand_ACTION_REVEAL, Path: reveal,
				}}}); err != nil {
					return err
				}
			}
			fmt.Println(id)
			return nil
		}
	}
	spec := &pb.PaneSpec{CreatedByPaneId: paneEnv("PANE_ID"), Files: &pb.FilesOpen{Root: folder, Reveal: reveal}}
	var m *pb.ClientMessage
	if beside != "" {
		m = &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{
			SplitPaneId: beside, Direction: pb.Split_DIRECTION_HORIZONTAL, Before: !right, Spec: spec,
		}}}
	} else {
		gid, err := w.browserGroup(group)
		if err != nil {
			return err
		}
		m = &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: spec}}}
	}
	res, err := call(m)
	if err != nil {
		return err
	}
	fmt.Println(tabPane(call, client.CreatedID(res)))
	return nil
}
