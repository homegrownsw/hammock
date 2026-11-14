package daemon

// This computer's default agent: default-agent in its config file, the one
// New Agent Tab starts without asking (the apps' New Claude Code Tab, ⌥⌘N,
// `hmk agent` with none named). It goes out in this computer's own record
// (Computer.default_agent), to its clients, paired phones and linked
// computers, so each app offers the default of the computer a Group is on.
// An agent that isn't installed here counts as none.

import (
	"errors"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// errNoDefaultAgent: a start that names no agent, with none set here.
var errNoDefaultAgent = errors.New("no default agent is set on this computer: name one, or set default-agent in Settings ▸ Agents (or the config file)")

// readDefaultAgent is the agent default-agent names ("" for none, or one
// Hammock doesn't know).
func readDefaultAgent() string {
	v, _ := hammockconfig.Value("default-agent")
	if a := agents.Parse(v); a != nil {
		return a.ID
	}
	return ""
}

// defaultAgent is the default agent, if it's installed here ("" if not).
// Callers hold d.mu.
func (d *Daemon) defaultAgent() string {
	id := d.defaultAgentSet
	if id == "" {
		return ""
	}
	if d.agentKinds == nil {
		// Not asked yet: as clients take it (Claude Code and Codex).
		if id == agents.Claude.ID || id == agents.Codex.ID {
			return id
		}
		return ""
	}
	for _, k := range d.agentKinds {
		if k.Id == id && k.Installed {
			return id
		}
	}
	return ""
}

// resolveHarness fills in a start's agent when it names none: the default
// agent here. Callers hold d.mu.
func (d *Daemon) resolveHarness(m *pb.StartAgent) error {
	if m.Harness != "" {
		return nil
	}
	if m.Harness = d.defaultAgent(); m.Harness == "" {
		return errNoDefaultAgent
	}
	return nil
}

// watchDefaultAgent reads default-agent when the config file changes,
// until the daemon stops, telling clients and linked computers (in this
// computer's record) when it does.
func (d *Daemon) watchDefaultAgent() {
	t := time.NewTicker(themeEvery)
	defer t.Stop()
	var last fileStamp
	first := true
	for {
		if s := stamp(hammockconfig.Path()); first || s != last {
			first, last = false, s
			id := readDefaultAgent()
			d.mu.Lock()
			if id != d.defaultAgentSet {
				d.defaultAgentSet = id
				d.publishComputers()
			}
			d.mu.Unlock()
		}
		select {
		case <-d.done:
			return
		case <-t.C:
		}
	}
}
