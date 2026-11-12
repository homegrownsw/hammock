package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// newSizedTab makes a tab running a program that prints the size it starts
// at, and returns its pane, attached on c.
func newSizedTab(t *testing.T, c *client.Client, cols, rows uint32) string {
	t.Helper()
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Size"}}}))
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID,
		Pane:    &pb.PaneSpec{Command: []string{"/bin/sh", "-c", `echo "size:$(stty size)"; exec cat`}, Cwd: "/tmp", Cols: cols, Rows: rows},
	}}}))
	var paneID string
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		for _, tb := range ev.GetState().GetTabs() {
			if tb.Id == tabID {
				paneID = tb.FocusedPaneId
			}
		}
		return paneID != ""
	})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	return paneID
}

func dialViewer(t *testing.T, dir string) *client.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := client.Dial(ctx, SocketPath(dir), "tui", t.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func longPaneSizeWait(t *testing.T) {
	old := newPaneSizeWait
	newPaneSizeWait = 30 * time.Second // longer than waitOutput waits: only a size starts it
	t.Cleanup(func() { newPaneSizeWait = old })
}

// With a client that shows panes connected, a new pane's program starts
// at the size it says, not the one the pane was made at.
func TestNewPaneStartsAtViewersSize(t *testing.T) {
	longPaneSizeWait(t)
	dir := shortDir(t)
	startDaemon(t, dir)
	c := dialViewer(t, dir)
	paneID := newSizedTab(t, c, 80, 24)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Resize{Resize: &pb.Resize{PaneId: paneID, Cols: 90, Rows: 20}}})
	if out := waitOutput(t, c, paneID, "size:20 90"); strings.Contains(out, "size:24 80") {
		t.Fatalf("started at the size it was made at: %q", out)
	}
}

// A pane made at the right size still starts when its client says so,
// though the size doesn't change.
func TestNewPaneStartsWhenGuessWasRight(t *testing.T) {
	longPaneSizeWait(t)
	dir := shortDir(t)
	startDaemon(t, dir)
	c := dialViewer(t, dir)
	paneID := newSizedTab(t, c, 80, 24)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Resize{Resize: &pb.Resize{PaneId: paneID, Cols: 80, Rows: 24}}})
	waitOutput(t, c, paneID, "size:24 80")
}

// With no client that shows panes, nothing will say a size: it starts at once.
func TestNewPaneStartsAtOnceWithoutViewer(t *testing.T) {
	longPaneSizeWait(t)
	dir := shortDir(t)
	startDaemon(t, dir)
	c := dial(t, dir) // "test": shows no panes
	paneID := newSizedTab(t, c, 80, 24)
	waitOutput(t, c, paneID, "size:24 80")
}
