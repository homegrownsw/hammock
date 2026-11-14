package daemon

import (
	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Keeping the computer awake: with the config file's agent-keep-awake on
// (off by default), the daemon holds a power assertion while any agent is
// working (or starting), so the computer doesn't sleep when it's left
// alone mid-turn, and lets it go once none is. The display can still
// sleep, and closing a laptop's lid still sleeps it. An agent waiting for
// you doesn't count: it's going nowhere until you're back. On a Mac it's
// an IOKit assertion, listed by `pmset -g assertions`; elsewhere nothing.

// keepAwakeOn reads agent-keep-awake.
func keepAwakeOn() bool { return hammockconfig.Bool("agent-keep-awake", false) }

// keepAwakeHold takes (true) or lets go of (false) the assertion, and
// reports whether it's now held. A variable for tests.
var keepAwakeHold = holdAwake

// agentsAtWork counts the panes whose agent is working or starting, and
// not asleep. Callers hold d.mu.
func (d *Daemon) agentsAtWork() int {
	n := 0
	for _, p := range d.panes {
		p.mu.Lock()
		a, asleep, exited := p.info.Agent, p.info.Asleep, p.info.Exited
		p.mu.Unlock()
		if a == nil || asleep || exited {
			continue
		}
		if s := a.State; s == pb.AgentState_AGENT_STATE_WORKING || s == pb.AgentState_AGENT_STATE_STARTING {
			n++
		}
	}
	return n
}

// keepAwake takes or lets go of the assertion for the agents at work now
// (pollCwds, every tick). The config file is only read while one is, or
// it's held. Callers hold d.mu.
func (d *Daemon) keepAwake() {
	want := d.agentsAtWork() > 0
	if !want && !d.awake {
		return
	}
	want = want && keepAwakeOn()
	if want == d.awake {
		return
	}
	d.awake = keepAwakeHold(want)
	if want && !d.awake && !d.awakeWarned {
		d.awakeWarned = true
		d.log.Warn("couldn't keep the computer awake for working agents")
	}
}

// releaseAwake lets go of the assertion (shutdown). Callers hold d.mu.
func (d *Daemon) releaseAwake() {
	if d.awake {
		d.awake = keepAwakeHold(false)
	}
}
