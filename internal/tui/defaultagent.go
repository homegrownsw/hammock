package tui

// The default agent (default-agent in a computer's config, sent in its
// Computer record): New Claude Code Tab starts it without the Start an
// Agent dialog, beside New Agent Tab…, which still asks. Each computer has
// its own; a Group's is its computer's, and a Group someone shared with you
// has none.

import (
	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// defaultAgentIn is the default agent of the computer a Group is on ("":
// none; no such Group: this computer's).
func (a *App) defaultAgentIn(groupID string) string {
	g := a.group(groupID)
	if g != nil && g.HostUserId != "" && g.MyRole != pb.Role_ROLE_OWNER {
		return "" // shared with you
	}
	if g != nil {
		if c := a.computerOf(g); c != nil {
			return c.DefaultAgent
		}
	}
	for _, c := range a.linkedList() {
		if c.ThisComputer {
			return c.DefaultAgent
		}
	}
	return ""
}

// defaultAgentTitle is its item: "New Claude Code Tab" (on: " on Studio").
func defaultAgentTitle(harness, on string) string {
	name := harness
	if ag := agents.Get(harness); ag != nil {
		name = ag.Name
	}
	if on != "" {
		return "New " + name + " Tab on " + on
	}
	return "New " + name + " Tab"
}

// startDefaultAgent starts the Group's computer's default agent in a new
// tab there, in the Group's own folder (else home), as the Mac's does.
func (a *App) startDefaultAgent(group string) {
	if group == "" {
		group = a.currentGroup()
	}
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_ListFolders{ListFolders: &pb.ListFolders{GroupId: group}}}, func(r *pb.ServerMessage) {
		dir := "~"
		for _, s := range r.GetFolderList().GetSuggestions() {
			if s.Why == "This Group" {
				dir = s.Path
				break
			}
		}
		// No agent named: the daemon there starts its default.
		a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{Cwd: dir, GroupId: group}}},
			func(r *pb.ServerMessage) { a.selectTab(client.CreatedID(r)) })
	})
}

// startDefaultAgentOn starts a linked computer's default agent there, in
// its home folder (in the Group there that uses it, or a new one).
func (a *App) startDefaultAgentOn(computer string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{Cwd: "~", ComputerId: computer}}},
		func(r *pb.ServerMessage) { a.selectTab(client.CreatedID(r)) })
}
