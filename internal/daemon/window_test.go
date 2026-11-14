package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// hmk window / open_window: the Mac app is told to show a pane, tab or
// Group in a window of its own; with no app open, it says so. An agent may
// show what it may read: not another Group's tab.
func TestOpenWindowReachesTheApp(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	mk := func(name string) (group, tab, pane string) {
		group = client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: name}}}))
		tab = client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: group, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
		d.mu.Lock()
		pane = d.tabs[tab].FocusedPaneId
		d.mu.Unlock()
		return
	}
	g, tab, pane := mk("a")
	open := func(c *client.Client, kind pb.OpenWindow_Kind, id string) error {
		_, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenWindow{OpenWindow: &pb.OpenWindow{Kind: kind, Id: id}}})
		return err
	}

	if err := open(ui, pb.OpenWindow_KIND_TAB, tab); err == nil {
		t.Fatal("opened with no app open")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app, err := client.Dial(ctx, SocketPath(dir), "mac", "Hammock")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})

	for _, c := range []struct {
		kind pb.OpenWindow_Kind
		id   string
	}{{pb.OpenWindow_KIND_PANE, pane}, {pb.OpenWindow_KIND_TAB, tab}, {pb.OpenWindow_KIND_GROUP, g}} {
		if err := open(ui, c.kind, c.id); err != nil {
			t.Fatalf("%v: %v", c.kind, err)
		}
		waitEvent(t, app, func(ev *pb.ServerMessage) bool {
			w := ev.GetOpenWindow()
			return w != nil && w.Kind == c.kind && w.Id == c.id
		})
	}
	if err := open(ui, pb.OpenWindow_KIND_TAB, pane); err == nil {
		t.Fatal("a pane's id opened as a tab")
	}
	if err := open(ui, pb.OpenWindow_KIND_UNSPECIFIED, tab); err == nil {
		t.Fatal("opened a window of no kind")
	}

	// An agent in its pane: its own tab and Group, and panes it can read.
	agent := dialAs(t, dir, &caller{pane: pane, agent: true}, "cli")
	if err := open(agent, pb.OpenWindow_KIND_TAB, tab); err != nil {
		t.Fatalf("agent, its own tab: %v", err)
	}
	if err := open(agent, pb.OpenWindow_KIND_GROUP, g); err != nil {
		t.Fatalf("agent, its own Group: %v", err)
	}
}
