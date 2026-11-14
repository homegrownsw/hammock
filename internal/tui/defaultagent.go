package tui

// The default agent (default-agent in a computer's config, sent in its
// Computer record): New Claude Code Tab starts it without the Start an
// Agent dialog, beside New Agent Tab…, which still asks. Each computer has
// its own; a Group's is its computer's, and a Group someone shared with you
// has none.

import (
	"cmp"
	"os"
	"strings"

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

// agentName is an agent's name: "Claude Code".
func agentName(harness string) string {
	if ag := agents.Get(harness); ag != nil {
		return ag.Name
	}
	return harness
}

// defaultAgentTitle is its item: "New Claude Code Tab" (on: " on Studio").
func defaultAgentTitle(harness, on string) string {
	name := agentName(harness)
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

// groupOfPane is the Group a pane's tab is in.
func (a *App) groupOfPane(id string) string {
	if p := a.panes[id]; p != nil {
		if t := a.tabs[p.TabId]; t != nil {
			return t.GroupId
		}
	}
	return ""
}

// startAgentBeside starts an agent in a new pane split off paneID
// (harness "": its computer's default), in that pane's folder unless cwd
// says otherwise, and focuses it.
func (a *App) startAgentBeside(paneID string, dir pb.Split_Direction, harness, cwd, prompt string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{
		Harness: harness, Cwd: cwd, Prompt: prompt, SplitPaneId: paneID, Direction: dir,
	}}}, func(r *pb.ServerMessage) { a.setFocus(client.CreatedID(r)) })
}

// openStartAgentBeside asks which agent, where and what first, as
// openStartAgent does, for a pane split off paneID.
func (a *App) openStartAgentBeside(paneID string, dir pb.Split_Direction) {
	cwd := ""
	if p := a.panes[paneID]; p != nil {
		cwd = tildeHome(p.Cwd)
	}
	a.openDialog(&dialog{title: "Start an Agent", okLabel: "Start",
		fields: []*field{{label: "Agent (" + strings.Join(agents.IDs(), ", ") + ")", value: cmp.Or(a.defaultAgentIn(a.groupOfPane(paneID)), "claude")},
			{label: "Folder", value: cwd}, {label: "First message (optional)"}},
		submit: func(v []string) {
			a.startAgentBeside(paneID, dir, parseHarness(v[0]), strings.TrimSpace(v[1]), strings.TrimSpace(v[2]))
		}})
}

// parseHarness is the agent typed in a Start an Agent dialog, by its id
// when Hammock knows it.
func parseHarness(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if ag := agents.Parse(s); ag != nil {
		return ag.ID
	}
	return s
}

// tildeHome writes the home folder as ~.
func tildeHome(dir string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, home) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}

// startDefaultAgentOn starts a linked computer's default agent there, in
// its home folder (in the Group there that uses it, or a new one).
func (a *App) startDefaultAgentOn(computer string) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{Cwd: "~", ComputerId: computer}}},
		func(r *pb.ServerMessage) { a.selectTab(client.CreatedID(r)) })
}
