package daemon

// Sleeping agents: an agent left idle for the config file's agent-sleep
// (3h, 6h, 12h, 24h, 48h; off by default), or put to sleep by hand
// (SleepPane), has its pane's process ended to free its memory: its
// holder, the shell and the agent with everything it started. The pane
// stays as it was, its screen kept and its agent shown as it was, marked
// asleep (pb.Pane.asleep): the apps dim it, with a Wake button over it.
// That button (SleepPane with wake), typing in it, or sending its agent a
// message or an answer starts a new process that resumes the session,
// with the options the agent was started with (agents.Agent.ResumeFlags),
// where it ran. Focusing it doesn't: that's too easily done by accident.
//
// Only an agent at rest sleeps: one that finished its turn (or ended it
// on an error), not one working or waiting for you (a permission prompt
// or a question it wouldn't ask again), with no shells, subagents or
// other programs running in the pane, whose session is known and can be
// resumed; a scheduled run's never does. A session nothing's been said in
// has nothing to resume: it wakes as a new one, with the same options.
//
// What was typed in its input box and not sent is typed in again once
// it's up. What's typed while it wakes is kept until its agent is at its prompt,
// then typed in order; a message sent from the agent view waits for that
// too. A sleeping pane stays asleep when the daemon restarts or the Mac
// does (restoreHolder): its record (sleepRecord) keeps the command that
// wakes it.

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/agentview"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
	"github.com/homegrownsw/hammock/internal/holder"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/vt"
)

// sleepDraftRecord is the store's kind for what was typed, not sent, in a
// sleeping pane's agent's input box (wrapperspb.StringValue): typed into
// it again when it wakes.
const sleepDraftRecord = "sleepdraft"

// sleepRecord is the store's kind for a sleeping pane's wake command (a
// command line for the login shell, wrapperspb.StringValue).
const sleepRecord = "sleep"

// sleepEvery is how often idle agents are looked for (pollCwds' ticks).
const sleepEvery = 30 // × 2 s

// wakeWait is how long a woken agent has to reach its prompt before
// what was typed meanwhile is typed anyway.
var wakeWait = 60 * time.Second

// agentSleepAfter is how long an agent rests before it's put to sleep (the
// config file's agent-sleep), or 0: never.
func agentSleepAfter() time.Duration {
	v, _ := hammockconfig.Value("agent-sleep")
	return parseAgentSleep(v)
}

// parseAgentSleep reads agent-sleep: off (or empty) or a duration of a
// minute or more ("3h"); anything else is off.
func parseAgentSleep(v string) time.Duration {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "off", "false", "never", "0":
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Minute {
		return 0
	}
	return d
}

// whyAwakeLocked is why p's agent can't be put to sleep now, or "" if it
// can. Callers hold p.mu.
func (p *pane) whyAwakeLocked() string {
	i, a := p.info, p.info.Agent
	switch {
	case i.Kind != pb.PaneKind_PANE_KIND_TERMINAL && i.Kind != pb.PaneKind_PANE_KIND_UNSPECIFIED:
		return "only a terminal's agent can sleep"
	case i.Asleep:
		return "it's asleep already"
	case p.sleepBusy || p.waking:
		return "it's waking or going to sleep"
	case i.Exited || p.pid == 0 || p.ops == nil || p.connecting || p.closed:
		return "its terminal isn't running"
	case a == nil || a.State == pb.AgentState_AGENT_STATE_EXITED:
		return "there's no agent in this pane"
	case i.ScheduleRunId != "":
		return "it's a scheduled run"
	case a.SessionId == "":
		return "its session isn't known (hmk hooks install tells Hammock)"
	}
	if ag := agents.Get(a.Harness); ag == nil || ag.Resume == nil {
		return "its sessions can't be resumed"
	}
	switch {
	case a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT:
		return "it's waiting for you"
	case a.State != pb.AgentState_AGENT_STATE_IDLE && a.State != pb.AgentState_AGENT_STATE_ERRORED:
		return "it's working"
	case a.Shells > 0 || a.RunningCommand:
		return "it has commands running"
	case a.Subagents > 0:
		return "its subagents are still working"
	}
	for _, name := range i.Processes {
		if name != a.Harness {
			return "other programs are running in its terminal"
		}
	}
	return ""
}

// restingSinceLocked is when p's agent last did anything: its state
// changed, a hook came, or someone typed in its pane. Its screen isn't
// counted: what an idle agent redraws by itself (a status line with a
// clock) isn't activity. Callers hold p.mu.
func (p *pane) restingSinceLocked() time.Time {
	t := time.UnixMilli(p.info.Agent.GetStateSinceUnixMs())
	for _, u := range []time.Time{p.hookAt, p.lastIn} {
		if u.After(t) {
			t = u
		}
	}
	return t
}

// sleepIdleAgents puts to sleep the agents that have rested for
// agent-sleep. Callers hold d.mu.
func (d *Daemon) sleepIdleAgents() {
	after := agentSleepAfter()
	if after == 0 {
		return
	}
	for _, p := range d.panes {
		p.mu.Lock()
		due := p.whyAwakeLocked() == "" && time.Since(p.restingSinceLocked()) >= after
		p.mu.Unlock()
		if due {
			go func() {
				if err := d.sleepPane(p, "daemon"); err != nil {
					d.log.Info("didn't put an idle agent to sleep", "pane", p.id, "err", err)
				}
			}()
		}
	}
}

// sleepPane puts p's agent to sleep: it ends the pane's process, keeping
// its screen and the command that resumes its session. actor is who asked,
// for the audit log. It runs without d.mu held.
func (d *Daemon) sleepPane(p *pane, actor string) error {
	p.mu.Lock()
	if why := p.whyAwakeLocked(); why != "" {
		p.mu.Unlock()
		return errors.New("its agent can't sleep now: " + why)
	}
	p.sleepBusy = true
	pid, a := p.pid, proto.CloneOf(p.info.Agent)
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.sleepBusy = false
		p.mu.Unlock()
	}()

	harness, apid := findAgentProc(pid)
	if apid == 0 || harness != a.Harness {
		return errors.New("its agent can't sleep now: its process isn't running")
	}
	ag, argv := agents.Get(harness), procinfo.Args(apid)
	cmd := ag.ResumeWith(a.SessionId, argv)
	if sessionFile(a) == "" {
		// Nothing's been said in it, so there's nothing to resume (and
		// nothing to lose): it starts afresh, a new session.
		cmd = ag.StartWith(argv)
	}
	cwd, _ := procinfo.Cwd(apid) // where its sessions are kept (Claude's, by folder)
	p.saveScrollback(true)       // a restart restores it from there
	// What's typed in its input box, not sent: back in it when it wakes.
	// Anywhere on its screen (a new session's box is near the top), its
	// placeholder left out.
	draft := agentview.InputDraft(p.bottomText(math.MaxInt32))

	p.mu.Lock()
	p.sleepBusy = false
	why := p.whyAwakeLocked()
	if why == "" && p.pid != pid {
		why = "its terminal restarted"
	}
	if why != "" {
		p.mu.Unlock()
		return errors.New("its agent can't sleep now: " + why)
	}
	p.info.Asleep = true
	if cwd != "" && isDir(cwd) {
		p.info.Cwd = cwd
	}
	p.wakeCmd, p.wakeDraft = cmd, draft
	if p.mirror != nil {
		// Its screen stays, but not the agent's mouse reports and the like:
		// moving the mouse over it mustn't wake it.
		p.mirror.Write(modesOff)
		p.sendSnapshotsLocked()
	}
	// Its holder ends with what it runs; the reader, a generation behind,
	// takes neither the connection closing nor the exit for the pane's.
	p.postLocked(func(hc *holder.Client) { _ = hc.CloseHolder() })
	p.unlinkLocked()
	p.gen++
	p.pid = 0
	info := proto.CloneOf(p.info)
	p.mu.Unlock()

	d.mu.Lock()
	defer d.mu.Unlock()
	d.persist(d.store.PutRecord(sleepRecord, p.id, wrapperspb.String(cmd)))
	if draft != "" {
		d.persist(d.store.PutRecord(sleepDraftRecord, p.id, wrapperspb.String(draft)))
	}
	d.persist(d.store.DeleteRuntime(p.id))
	d.persist(d.store.PutPane(info))
	d.audit(actor, "sleep", p.id, harness)
	d.paneChangedIfLive(p)
	d.log.Info("put an agent to sleep", "pane", p.id, "harness", harness, "by", actor)
	return nil
}

// wakePane wakes p's sleeping agent: a new process resumes its session.
// The channel returned closes once it's at its prompt (or wakeWait has
// passed), nil when p wasn't asleep. It runs without d.mu held.
func (d *Daemon) wakePane(p *pane, actor string) (<-chan struct{}, error) {
	p.mu.Lock()
	if p.waking {
		ch := p.awake
		p.mu.Unlock()
		return ch, nil
	}
	if !p.info.Asleep || p.closed {
		p.mu.Unlock()
		return nil, nil
	}
	cmd := p.wakeCmd
	if a := p.info.Agent; cmd == "" && a != nil {
		if ag := agents.Get(a.Harness); ag != nil && ag.Resume != nil {
			cmd = ag.Resume(a.SessionId)
		}
	}
	p.info.Asleep = false
	p.waking, p.awake = true, make(chan struct{})
	ch := p.awake
	if p.mirror != nil {
		// Its screen, ahead of what the new process shows.
		p.seed, p.seedNote = p.mirror.Snapshot(), "resumed after sleeping"
	}
	if a := p.info.Agent; a != nil {
		a.State, a.StateSinceUnixMs, a.Summary, a.Seen = pb.AgentState_AGENT_STATE_STARTING, time.Now().UnixMilli(), "", true
	}
	p.mu.Unlock()

	var err error
	if cmd == "" {
		err = errors.New("there's no command to resume its session with")
	} else {
		err = p.spawnAsync(agentShellCommand(cmd))
	}
	d.mu.Lock()
	d.persist(d.store.DeleteRecord(sleepRecord, p.id))
	d.persist(d.store.DeleteRecord(sleepDraftRecord, p.id))
	if err == nil {
		d.audit(actor, "wake", p.id, "")
		d.log.Info("woke a sleeping agent", "pane", p.id, "by", actor)
	}
	d.mu.Unlock()
	if err != nil {
		d.log.Error("couldn't wake a sleeping agent", "pane", p.id, "err", err)
		if p.markStartFailed(err) {
			d.mu.Lock()
			d.startFailedLocked(p)
			d.mu.Unlock()
		}
		p.wakeDone()
		return ch, err
	}
	d.mu.Lock()
	d.persist(d.store.PutPane(p.snapshotInfo()))
	d.paneChangedIfLive(p)
	d.mu.Unlock()
	go func() {
		deadline := time.Now().Add(wakeWait)
		for time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
			info := p.snapshotInfo()
			if info.Exited {
				break
			}
			if s := info.Agent.GetState(); s != pb.AgentState_AGENT_STATE_STARTING && s != pb.AgentState_AGENT_STATE_UNSPECIFIED {
				time.Sleep(700 * time.Millisecond) // its input box drawn, not just its process up
				break
			}
		}
		p.wakeDone()
	}()
	return ch, nil
}

// wakeDone ends p's waking: what was typed meanwhile goes in, in order,
// and whoever waits on it is told.
func (p *pane) wakeDone() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.waking {
		return
	}
	if p.wakeDraft != "" {
		bracketed := p.mirror != nil && p.mirror.BracketedPaste()
		p.wakeQueue = append([][]byte{firstPromptPaste(p.wakeDraft, bracketed)}, p.wakeQueue...)
		p.wakeDraft = ""
	}
	for _, b := range p.wakeQueue {
		p.postLocked(func(hc *holder.Client) { _ = hc.Input(b) })
	}
	p.wakeQueue, p.waking = nil, false
	close(p.awake)
}

// wakeAndWait wakes p if it's asleep and waits until its agent is at its
// prompt, for what types into it as a message (sendAgent, switchAgent).
func (d *Daemon) wakeAndWait(p *pane, actor string) error {
	ch, err := d.wakePane(p, actor)
	if err != nil {
		return err
	}
	if ch != nil {
		<-ch
	}
	return nil
}

// wakeFor wakes the pane msg talks to (typing in it, answering its
// agent), if it's asleep; a message from the agent view wakes it in
// sendAgent. It returns at once: input sent meanwhile waits for it.
func (c *conn) wakeFor(msg *pb.ClientMessage) {
	var id string
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_Input:
		if onlyReports(m.Input.Data) {
			return // the mouse moved over it: not typing
		}
		id = m.Input.PaneId
	case *pb.ClientMessage_AgentAnswer:
		id = m.AgentAnswer.PaneId
	default:
		return
	}
	d := c.d
	d.mu.Lock()
	p := d.panes[id]
	d.mu.Unlock()
	if p == nil || !p.isAsleep() {
		return
	}
	if _, err := d.wakePane(p, auditActorOf(c)); err != nil {
		d.log.Warn("couldn't wake the pane's agent", "pane", id, "err", err)
	}
}

// reportRE is one report a terminal sends unasked: a mouse event (SGR,
// urxvt or X10) or focus in or out.
var reportRE = regexp.MustCompile(`^\x1b\[(?:<[0-9;]*[Mm]|M[\s\S]{3}|[0-9;]*M|[IO])`)

// onlyReports reports whether data is nothing but such reports.
func onlyReports(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	for len(data) > 0 {
		loc := reportRE.FindIndex(data)
		if loc == nil {
			return false
		}
		data = data[loc[1]:]
	}
	return true
}

// auditActorOf is c.auditActor, taking d.mu for it.
func auditActorOf(c *conn) string {
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	return c.auditActor()
}

func (p *pane) isAsleep() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info.Asleep
}

// setPaneAsleep is SleepPane: puts the pane's agent to sleep, or wakes it.
func (d *Daemon) setPaneAsleep(c *conn, m *pb.SleepPane) error {
	d.mu.Lock()
	p := d.panes[m.PaneId]
	actor := c.auditActor()
	d.mu.Unlock()
	if p == nil {
		return errors.New("no such pane")
	}
	if m.Wake {
		_, err := d.wakePane(p, actor)
		return err
	}
	return d.sleepPane(p, actor)
}

// restoreAsleep gives a pane that was asleep when the daemon stopped its
// saved screen back, and the command that wakes it, starting nothing.
// Callers hold d.mu.
func (d *Daemon) restoreAsleep(p *pane) {
	var cmd, draft wrapperspb.StringValue
	if recs, err := d.store.Records(sleepRecord); err == nil {
		if b, ok := recs[p.id]; ok {
			_ = proto.Unmarshal(b, &cmd)
		}
	}
	if recs, err := d.store.Records(sleepDraftRecord); err == nil {
		if b, ok := recs[p.id]; ok {
			_ = proto.Unmarshal(b, &draft)
		}
	}
	p.loadScrollback()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.wakeCmd, p.wakeDraft = cmd.GetValue(), draft.GetValue()
	p.mirror = vt.New(int(p.info.Cols), int(p.info.Rows))
	if c := d.colors.Load(); c != nil {
		p.mirror.SetColors(*c)
	}
	if len(p.seed) > 0 {
		p.mirror.Write(p.seed)
		p.seed = nil
	}
	p.mirror.Write(modesOff)
	p.sendSnapshotsLocked()
}
