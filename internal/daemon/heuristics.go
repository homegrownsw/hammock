package daemon

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Heuristic agent detection, used when an agent's hooks aren't installed:
// find a known agent process under the pane's shell and infer its state
// from the mirrored screen. Hook events always win; heuristics only run
// for panes that haven't sent a hook recently.

const hookFreshness = 2 * time.Minute

// agentName returns the harness pid runs, or "". The process name alone
// isn't enough: Claude Code's native install runs a binary named after its
// version (~/.local/share/claude/versions/2.1.316), so its executable's
// path is checked too, by name or by a versions folder under the harness.
func agentName(pid int) string {
	path, _ := procinfo.Path(pid)
	// The agent a, unless pid is another program by its name.
	is := func(a *agents.Agent) string {
		if a.Namesake != nil && a.Namesake(path, procinfo.Args(pid)) {
			return ""
		}
		return a.ID
	}
	if name, err := procinfo.Name(pid); err == nil {
		if a := agents.ByProcess(name); a != nil {
			return is(a)
		}
	}
	if path == "" {
		return ""
	}
	if a := agents.ByProcess(filepath.Base(path)); a != nil {
		return is(a)
	}
	parts := strings.Split(strings.ToLower(filepath.ToSlash(path)), "/")
	for i := 0; i+2 < len(parts); i++ {
		if a := agents.ByProcess(parts[i]); a != nil && parts[i+1] == "versions" {
			return is(a)
		}
	}
	// A script run by its interpreter (node …/bin/gemini): the script's name.
	if name, err := procinfo.Name(pid); err == nil && isInterpreter(name) {
		args := procinfo.Args(pid)
		for _, arg := range args[min(1, len(args)):] {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if a := agents.ByProcess(filepath.Base(arg)); a != nil {
				return a.ID
			}
			break
		}
	}
	return ""
}

// scriptRunners run agents written as scripts.
var scriptRunners = map[string]bool{"node": true, "bun": true, "deno": true, "python": true, "python3": true}

// isInterpreter: name is one of scriptRunners, or a versioned Python
// ("python3.12": the process a script's shebang runs is named after the
// executable it resolves to, as uv's and pipx's tool installs' are).
func isInterpreter(name string) bool {
	name = strings.ToLower(name)
	if scriptRunners[name] {
		return true
	}
	v, ok := strings.CutPrefix(name, "python")
	return ok && v != "" && strings.Trim(v, "0123456789.") == ""
}

// resumeID is the session the agent under pid was started to resume
// (`codex resume ID` on its command line), if its agent says.
func resumeID(pid int) string {
	h, apid := findAgentProc(pid)
	a := agents.Get(h)
	if a == nil || a.ResumeID == nil {
		return ""
	}
	return a.ResumeID(procinfo.Args(apid))
}

// findAgent returns the harness name of a known agent process among pid's
// descendants (a few levels deep), or "".
func findAgent(pid int) string {
	h, _ := findAgentProc(pid)
	return h
}

// findAgentProc is findAgent with the agent's own process id.
func findAgentProc(pid int) (string, int) {
	frontier := []int{pid}
	for depth := 0; depth < 4 && len(frontier) > 0; depth++ {
		var next []int
		for _, p := range frontier {
			for _, c := range procinfo.Children(p) {
				if h := agentName(c); h != "" {
					return h, c
				}
				next = append(next, c)
			}
		}
		frontier = next
	}
	return "", 0
}

// inferState guesses an agent's state from the bottom of its screen.
func inferState(harness, screen string) (pb.AgentState, string) {
	a := agents.Get(harness)
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	if len(lines) > 25 {
		lines = lines[len(lines)-25:]
	}
	tail := strings.Join(lines, "\n")
	switch {
	case a.NeedsInputRE().MatchString(tail):
		return pb.AgentState_AGENT_STATE_NEEDS_INPUT, "Waiting for your approval"
	case a != nil && a.Screen.Idle != nil:
		if a.Screen.Idle.MatchString(tail) {
			return pb.AgentState_AGENT_STATE_IDLE, ""
		}
		return pb.AgentState_AGENT_STATE_WORKING, ""
	case a.WorkingRE().MatchString(tail):
		return pb.AgentState_AGENT_STATE_WORKING, ""
	}
	return pb.AgentState_AGENT_STATE_IDLE, ""
}

// justSwitched: an agent a switch typed in, whose process may not have
// started yet (it keeps its converted session meanwhile).
func justSwitched(a *pb.Agent) bool {
	return a.State == pb.AgentState_AGENT_STATE_STARTING && time.Since(time.UnixMilli(a.StateSinceUnixMs)) < 15*time.Second
}

// detectAgents runs heuristics over all panes. Callers hold d.mu.
func (d *Daemon) detectAgents() {
	for _, p := range d.panes {
		p.mu.Lock()
		pid, exited, hookAt, reported := p.pid, p.info.Exited, p.hookAt, p.info.Agent.GetReported()
		var screen string
		if p.mirror != nil && !reported {
			screen = p.mirror.Text(false)
		}
		p.mu.Unlock()
		if exited || pid == 0 || reported {
			continue // (what a program reports beats reading its screen)
		}
		if time.Since(hookAt) < hookFreshness {
			d.screenAsks(p, screen)
			continue
		}
		harness := findAgent(pid)
		u := &pb.StateUpdate{}
		changed := false

		p.mu.Lock()
		a := p.info.Agent
		switch {
		case harness == "" && a != nil && a.Heuristic && !justSwitched(a):
			// The agent process went away.
			p.info.Agent = nil
			changed = true
		case harness != "":
			state, summary := inferState(harness, screen)
			if a == nil || a.Harness != harness {
				a = &pb.Agent{Harness: harness, Heuristic: true}
				p.info.Agent = a
				changed = true
			}
			if a.Heuristic && a.State != state {
				prev := a.State
				a.State, a.StateSinceUnixMs = state, time.Now().UnixMilli()
				a.Seen = state == pb.AgentState_AGENT_STATE_IDLE && (prev != pb.AgentState_AGENT_STATE_WORKING || d.watched(p.id))
				a.Summary = summary
				changed = true
				seen := a.Seen
				p.mu.Unlock()
				switch {
				case state == pb.AgentState_AGENT_STATE_NEEDS_INPUT,
					state == pb.AgentState_AGENT_STATE_IDLE && prev == pb.AgentState_AGENT_STATE_WORKING && !seen:
					d.raiseAttention(p, state, summary, u)
				case state == pb.AgentState_AGENT_STATE_WORKING:
					d.clearAttentionForPane(p.id, u)
				}
				p.mu.Lock()
			}
		}
		cleared := harness == "" && changed
		p.mu.Unlock()
		if cleared {
			d.clearAttentionForPane(p.id, u)
		}
		if changed || len(u.ClearedAttentionIds) > 0 {
			info := p.snapshotInfo()
			d.persist(d.store.PutPane(info))
			u.Panes = append(u.Panes, info)
			d.publish(u)
			d.scheduleAgentChanged(p) // a scheduled run: started, waiting or done
		}
	}
}

// screenAsks: an agent whose hooks are flowing but never say it's asking
// for permission (Cursor's) waits for you while they last said working;
// its screen says when. Callers hold d.mu.
func (d *Daemon) screenAsks(p *pane, screen string) {
	p.mu.Lock()
	a := p.info.Agent
	ag := agents.Get(a.GetHarness())
	if a == nil || ag == nil || !ag.ScreenAsks || a.State != pb.AgentState_AGENT_STATE_WORKING || !ag.NeedsInputRE().MatchString(screenTail(screen)) {
		p.mu.Unlock()
		return
	}
	const summary = "Waiting for your approval"
	a.State, a.StateSinceUnixMs, a.Summary, a.Seen = pb.AgentState_AGENT_STATE_NEEDS_INPUT, time.Now().UnixMilli(), summary, false
	p.mu.Unlock()
	u := &pb.StateUpdate{}
	d.raiseAttention(p, pb.AgentState_AGENT_STATE_NEEDS_INPUT, summary, u)
	info := p.snapshotInfo()
	d.persist(d.store.PutPane(info))
	u.Panes = append(u.Panes, info)
	d.publish(u)
	d.scheduleAgentChanged(p)
}

// screenTail is the last 25 lines of a screen, where agents ask.
func screenTail(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	return strings.Join(lines[max(0, len(lines)-25):], "\n")
}
