package daemon

// Run Again: a terminal pane whose program ended (a failed command, a crash;
// a clean exit closes the pane) starts again in the same pane (RestartPane),
// its last screen kept above the new process's. An agent's pane resumes the
// agent's session, with the options it was started with, as waking a
// sleeping one does; any other runs the pane's own command again.

import (
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/holder"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// whyNotRestartLocked is why p can't be started again now, or "" if it
// can. Callers hold p.mu.
func (p *pane) whyNotRestartLocked() string {
	i := p.info
	switch {
	case p.closed:
		return "it's closed"
	case i.Kind != pb.PaneKind_PANE_KIND_TERMINAL && i.Kind != pb.PaneKind_PANE_KIND_UNSPECIFIED:
		return "only a terminal can run again"
	case !i.Exited:
		return "it's still running"
	case i.Asleep || p.waking || p.sleepBusy:
		return "it's asleep: wake it instead"
	case p.connecting:
		return "it's starting already"
	case i.ScheduleRunId != "":
		// Running it again would be a run nobody scheduled.
		return "it's a scheduled run: run its task again instead"
	}
	return ""
}

// restartCommand is what starts a pane's program again: the command line
// that resumes its agent's session (a, the pane's), or nil for the pane's
// own command. It reads the disk: callers hold neither lock.
func (d *Daemon) restartCommand(paneID string, a *pb.Agent) (cmd []string, resumes bool) {
	if a == nil || a.SessionId == "" || sessionFile(a) == "" {
		return nil, false // nothing said in it: nothing to resume
	}
	ag := agents.Get(a.Harness)
	if ag == nil || ag.Resume == nil {
		return nil, false
	}
	return agentShellCommand(ag.ResumeWith(a.SessionId, d.agentArgs(paneID))), true
}

// restartPane is RestartPane. It runs without d.mu held.
func (d *Daemon) restartPane(c *conn, m *pb.RestartPane) error {
	d.mu.Lock()
	p := d.panes[m.PaneId]
	actor := c.auditActor()
	d.mu.Unlock()
	if p == nil {
		return errors.New("no such pane")
	}

	p.mu.Lock()
	why, a := p.whyNotRestartLocked(), proto.CloneOf(p.info.Agent)
	p.mu.Unlock()
	if why != "" {
		return errors.New("it can't run again: " + why)
	}
	cmd, resumes := d.restartCommand(p.id, a)

	p.mu.Lock()
	if why := p.whyNotRestartLocked(); why != "" { // changed meanwhile (run again twice at once)
		p.mu.Unlock()
		return errors.New("it can't run again: " + why)
	}
	// The holder that ran it (still there, showing its screen) ends; the
	// reader, a generation behind, takes nothing of it for the pane's.
	p.postLocked(func(hc *holder.Client) { _ = hc.CloseHolder() })
	p.unlinkLocked()
	p.gen++
	p.pid = 0
	if p.mirror != nil {
		// Its screen, ahead of what the new process shows.
		p.seed, p.seedNote = p.mirror.Snapshot(), "started again"
	}
	p.info.Exited, p.info.ExitStatus, p.info.Interrupted, p.info.StartError = false, 0, false, ""
	harness := ""
	if a := p.info.Agent; a != nil {
		harness = a.Harness
		if resumes {
			a.State, a.StateSinceUnixMs, a.Summary, a.Seen = pb.AgentState_AGENT_STATE_STARTING, time.Now().UnixMilli(), "", true
		} else {
			p.info.Agent = nil // a new one, if it starts one: its hooks say
		}
	}
	p.mu.Unlock()

	d.mu.Lock()
	d.persist(d.store.DeleteRuntime(p.id))
	u := &pb.StateUpdate{}
	d.clearAttentionForPane(p.id, u) // its "exited with status n" in Needs You
	if len(u.ClearedAttentionIds) > 0 {
		d.publish(u)
	}
	d.mu.Unlock()

	if err := p.spawnAsync(cmd); err != nil {
		d.log.Error("couldn't start a pane again", "pane", p.id, "err", err)
		if p.markStartFailed(err) {
			d.mu.Lock()
			d.startFailedLocked(p)
			d.mu.Unlock()
		}
		return err
	}
	d.mu.Lock()
	d.persist(d.store.PutPane(p.snapshotInfo()))
	d.paneChangedIfLive(p)
	d.audit(actor, "restart", p.id, harness)
	d.mu.Unlock()
	d.log.Info("started a pane again", "pane", p.id, "resume", resumes, "by", actor)
	return nil
}
