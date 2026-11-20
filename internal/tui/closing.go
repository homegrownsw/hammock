package tui

import (
	"fmt"
	"strings"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Closing panes, tabs and Groups: what's asked first, and what's said after.

// warnBusy: ask before closing an agent at work (confirm-close-busy-agent,
// the Mac's Settings ▸ Agents switch).
var warnBusy = true

// reopenHint follows a close: how to get it back (Reopen Closed).
const reopenHint = " · ⌃B T reopens"

// busyAgent: p's agent is at work (working, starting, waiting for you, or
// with subagents still running), so closing p stops it.
func busyAgent(p *pb.Pane) bool {
	if p == nil || p.Agent == nil || p.Exited {
		return false
	}
	switch shownState(p.Agent) {
	case pb.AgentState_AGENT_STATE_WORKING, pb.AgentState_AGENT_STATE_STARTING, pb.AgentState_AGENT_STATE_NEEDS_INPUT:
		return true
	}
	return p.Agent.Subagents > 0
}

// busyWarning is what closing ids would stop, as the Mac says it: a title
// ("Claude is still working") and a line ("Closing stops it."); "" when no
// agent among them is at work, or warnings are off.
func (a *App) busyWarning(ids []string) (title, line string) {
	if !warnBusy {
		return "", ""
	}
	var busy []*pb.Pane
	for _, id := range ids {
		if p := a.panes[id]; busyAgent(p) {
			busy = append(busy, p)
		}
	}
	switch len(busy) {
	case 0:
		return "", ""
	case 1:
		ag := busy[0].Agent
		doing := "is still working"
		switch {
		case shownState(ag) == pb.AgentState_AGENT_STATE_NEEDS_INPUT:
			doing = "is waiting for you"
		case ag.RunningCommand:
			doing = "is running a command"
		case shownState(ag) == pb.AgentState_AGENT_STATE_IDLE:
			doing = "has a subagent still running"
			if ag.Subagents > 1 {
				doing = fmt.Sprintf("has %d subagents still running", ag.Subagents)
			}
		}
		name := a.paneName(busy[0])
		if ag.Harness != "" {
			name = harnessName(ag.Harness)
		}
		return name + " " + doing, "Closing stops it."
	}
	return fmt.Sprintf("%d agents are still at work", len(busy)), "Closing stops them."
}

// confirmClose runs close, first asking if it would lose unsaved edits, then
// if it would stop an agent at work.
func (a *App) confirmClose(ids []string, okLabel string, close func()) {
	a.confirmUnsaved(ids, func() {
		title, line := a.busyWarning(ids)
		if title == "" {
			close()
			return
		}
		a.openDialog(&dialog{title: title, okLabel: okLabel, confirm: true, lines: []string{line}, submit: func([]string) { close() }})
	})
}

// paneName is a pane as a close names it: its agent, its file, else its title.
func (a *App) paneName(p *pb.Pane) string {
	switch {
	case p == nil:
		return "Pane"
	case isEditor(p) && p.GetEditor().GetName() != "":
		return p.GetEditor().GetName()
	case p.Agent != nil && p.Agent.Harness != "":
		return harnessName(p.Agent.Harness)
	case p.Title != "":
		return p.Title
	}
	return "Pane"
}

// closed says what just closed, and how to bring it back.
func (a *App) closed(name string) {
	a.flash("Closed “" + truncate(strings.TrimSpace(name), 40) + "”" + reopenHint)
}
