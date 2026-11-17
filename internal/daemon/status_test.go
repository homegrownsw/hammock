package daemon

import (
	"slices"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// The menu bar's status: agents waiting on you (or failed) in Needs You,
// oldest first; finished ones not yet seen in Finished, newest first; each
// with its tab's title; and Hammock's release version beside the build's.
func TestServerStatusListsAgentsByTab(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)

	groupID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	tab := func(name string) string {
		id := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
			GroupId: groupID, Name: name, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}},
		}}}))
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.tabs[id].FocusedPaneId
	}
	asking, failed, doneOld, doneNew, working := tab("Asks"), tab("Fails"), tab("Old"), tab("New"), tab("Busy")

	d.mu.Lock()
	for _, a := range []*pb.AttentionItem{
		{PaneId: asking, State: pb.AgentState_AGENT_STATE_NEEDS_INPUT, CreatedUnixMs: 300},
		{PaneId: failed, State: pb.AgentState_AGENT_STATE_ERRORED, CreatedUnixMs: 100},
		{PaneId: doneOld, State: pb.AgentState_AGENT_STATE_IDLE, CreatedUnixMs: 100},
		{PaneId: doneNew, State: pb.AgentState_AGENT_STATE_IDLE, CreatedUnixMs: 200},
		{PaneId: working, State: pb.AgentState_AGENT_STATE_WORKING, CreatedUnixMs: 400},
	} {
		a.Id = "at_" + a.PaneId
		d.attention[a.Id] = a
	}
	d.mu.Unlock()

	r := d.serverStatus().GetServerStatusReply()
	panes := func(items []*pb.AttentionItem) (ids []string) {
		for _, a := range items {
			ids = append(ids, a.PaneId)
		}
		return ids
	}
	if got, want := panes(r.NeedsYou), []string{failed, asking}; !slices.Equal(got, want) {
		t.Errorf("needs you %v, want %v (failed, then asking: oldest first)", got, want)
	}
	if got, want := panes(r.Finished), []string{doneNew, doneOld}; !slices.Equal(got, want) {
		t.Errorf("finished %v, want %v (newest first)", got, want)
	}
	for pane, name := range map[string]string{asking: "Asks", failed: "Fails", doneOld: "Old", doneNew: "New"} {
		if r.Places[pane] != name {
			t.Errorf("place of %s: %q, want %q", name, r.Places[pane], name)
		}
	}
	if _, ok := r.Places[working]; ok {
		t.Errorf("a working agent's pane is named: it's in neither list")
	}
	if r.ProductVersion != ProductVersion || r.Version != Version {
		t.Errorf("versions %q, %q; want %q, %q", r.ProductVersion, r.Version, ProductVersion, Version)
	}
}
