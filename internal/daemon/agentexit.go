package daemon

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// An agent whose hooks are installed says it's ending (SessionEnd), unless
// it can't: it crashed, was killed (kill -9, out of memory) or its terminal
// ended its processes. Then its process going away is what says so. A hook
// comes from a process the agent runs, so the agent is the process between
// the hook's sender and the pane's shell: whatever it's called (node, bun,
// Claude's versioned binary), and whether or not the shell has job control.
// While that process lasts, stopped (^Z) or not, the agent isn't over.

// goneTicks is how many polls running an agent's process must be missing
// for: a moment's absence (a switch, a resume starting) isn't an end.
const goneTicks = 2

// quietShellAge is how long a pane's shell must have run before, at its
// prompt with nothing under it, it says its agent ended (when the agent's
// process isn't known: after the daemon restarts, before its next hook).
// Younger, it may be about to start one (a resumed session).
var quietShellAge = 30 * time.Second

// procAncestors is pid and the processes above it, nearest first.
func procAncestors(pid int) []int {
	var out []int
	for i := 0; i < 64 && pid > 1; i++ {
		out = append(out, pid)
		pp, err := procinfo.Parent(pid)
		if err != nil || pp == pid {
			break
		}
		pid = pp
	}
	return out
}

// isShell reports whether pid is a shell.
func isShell(pid int) bool {
	name, err := procinfo.Name(pid)
	return err == nil && shells[strings.TrimPrefix(filepath.Base(name), "-")]
}

// noteAgentProc remembers the process p's agent runs as, from a hook's
// sender and its ancestors (from, nearest first): the one under the pane's
// shell. A pane whose own command isn't a shell needs none: its agent ending
// ends the pane. Callers hold d.mu.
func noteAgentProc(p *pane, from []int) {
	p.mu.Lock()
	pid, known := p.pid, p.agentProc
	p.mu.Unlock()
	i := slices.Index(from, pid)
	if i < 1 || from[i-1] == known || !isShell(pid) {
		return
	}
	st, err := procinfo.StartTime(from[i-1])
	if err != nil {
		return
	}
	p.mu.Lock()
	p.agentProc, p.agentProcStart, p.agentGone = from[i-1], st, 0
	p.mu.Unlock()
}

// hookedAgentGone reports whether p's agent, which its hooks tell about,
// has gone without saying so, for goneTicks polls running. Callers hold d.mu.
func hookedAgentGone(p *pane) bool {
	p.mu.Lock()
	a, pid, proc, start := p.info.Agent, p.pid, p.agentProc, p.agentProcStart
	skip := a == nil || a.Heuristic || a.Reported || a.State == pb.AgentState_AGENT_STATE_EXITED || justSwitched(a) ||
		p.info.Asleep || p.sleepBusy || p.waking || p.closed || p.info.Exited || pid == 0
	if skip {
		p.agentGone = 0
	}
	p.mu.Unlock()
	if skip {
		return false
	}
	var gone bool
	if proc != 0 {
		st, err := procinfo.StartTime(proc)
		gone = err != nil || !st.Equal(start) // (its id reused by another)
		if gone {
			// Started again as another process (its hooks will say).
			if _, c := findAgentProc(pid); c != 0 {
				if st, err := procinfo.StartTime(c); err == nil {
					p.mu.Lock()
					p.agentProc, p.agentProcStart, p.agentGone = c, st, 0
					p.mu.Unlock()
					return false
				}
			}
		}
	} else {
		gone = quietShell(pid)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !gone {
		p.agentGone = 0
		return false
	}
	p.agentGone++
	return p.agentGone >= goneTicks
}

// quietShell: pid is a shell at its prompt with no process under it (no
// agent, nor one stopped with ^Z), and has been for a while.
func quietShell(pid int) bool {
	if !shellInFront(pid) || len(procinfo.Children(pid)) > 0 {
		return false
	}
	st, err := procinfo.StartTime(pid)
	return err == nil && time.Since(st) >= quietShellAge
}

// endHookedAgent ends p's agent as its SessionEnd would have: no longer
// working or waiting for you, its Needs You item gone, a scheduled run of it
// over, and its session not resumed after a restart. Callers hold d.mu.
func (d *Daemon) endHookedAgent(p *pane) {
	p.mu.Lock()
	a := p.info.Agent
	if a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT {
		a.Summary = "" // what it asked: nobody's asking now
	}
	a.State, a.StateSinceUnixMs, a.Seen = pb.AgentState_AGENT_STATE_EXITED, time.Now().UnixMilli(), false
	p.asking, p.pendingTool, p.askedFor, p.askedRule, p.progressTurn = nil, "", "", "", false
	p.agentProc, p.agentProcStart, p.agentGone = 0, time.Time{}, 0
	p.mu.Unlock()
	u := &pb.StateUpdate{}
	d.clearAttentionForPane(p.id, u)
	info := p.snapshotInfo()
	d.persist(d.store.PutPane(info))
	u.Panes = append(u.Panes, info)
	d.publish(u)
	d.scheduleAgentChanged(p) // a scheduled run: over
}
