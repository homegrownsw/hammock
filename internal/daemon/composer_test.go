package daemon

import (
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// TestComposerDraftIsShared: what's typed in a pane's agent view box on one
// of your devices goes to the others watching it, and to one that starts
// watching later; setting the same text again sends nothing new.
func TestComposerDraftIsShared(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	a, b := dial(t, dir), dial(t, dir)
	g := client.CreatedID(call(t, a, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, a, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	watch := func(c *client.Client) *pb.AgentView {
		t.Helper()
		call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: paneID, Watch: true}}})
		var v *pb.AgentView
		waitEvent(t, c, func(ev *pb.ServerMessage) bool { v = ev.GetAgentView(); return v != nil && v.Replace })
		return v
	}
	set := func(c *client.Client, text, writer string) {
		t.Helper()
		call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetComposerDraft{SetComposerDraft: &pb.SetComposerDraft{PaneId: paneID, Text: text, Writer: writer}}})
	}

	if v := watch(a); v.ComposerRev != 0 || v.ComposerDraft != "" {
		t.Fatalf("a box never typed in: rev %d %q", v.ComposerRev, v.ComposerDraft)
	}
	watch(b)
	set(a, "fix the flaky test", "box-a")
	waitEvent(t, b, func(ev *pb.ServerMessage) bool {
		v := ev.GetAgentView()
		return v != nil && v.ComposerRev == 1 && v.ComposerDraft == "fix the flaky test" && v.ComposerWriter == "box-a"
	})
	set(a, "fix the flaky test", "box-a") // unchanged: no new rev
	set(b, "", "box-b")                   // sent from b: emptied
	waitEvent(t, a, func(ev *pb.ServerMessage) bool {
		v := ev.GetAgentView()
		return v != nil && v.ComposerRev == 2 && v.ComposerDraft == "" && v.ComposerWriter == "box-b"
	})

	// One that starts watching now gets it with the rest.
	set(b, "and then the docs", "box-b")
	c := dial(t, dir)
	if v := watch(c); v.ComposerRev != 3 || v.ComposerDraft != "and then the docs" {
		t.Fatalf("new watcher got rev %d %q", v.ComposerRev, v.ComposerDraft)
	}
}

// A guest's box is their own: a Drive member's draft is dropped, not kept,
// and theirs isn't sent yours.
func TestGuestsDontShareTheComposer(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	g := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tab := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.groups[g].Members = []*pb.Member{{UserId: "us_guest", Role: pb.Role_ROLE_DRIVER}}
	d.mu.Unlock()
	guest := &conn{d: d, peer: "us_guest"}
	set := &pb.ClientMessage{Msg: &pb.ClientMessage_SetComposerDraft{SetComposerDraft: &pb.SetComposerDraft{PaneId: paneID, Text: "hi"}}}
	if ignore, err := guest.peerCheck(set); err != nil || !ignore {
		t.Fatalf("a guest's draft: ignore %v, err %v", ignore, err)
	}
	if d.getsComposer(guest) {
		t.Fatal("a guest gets your draft")
	}
	if !d.getsComposer(&conn{d: d}) {
		t.Fatal("your own app doesn't get it")
	}
}
