package daemon

// Resuming after a usage limit: an agent whose turn ended at its usage
// limit (its plan's allowance used up: Claude Code's 5-hour or weekly
// limit, Codex's; agents.Agent.HitLimit) carries on once the limit resets,
// unless agent-resume-after-limit = false. Its Agent says when
// (limit_resume), which the apps show ("Continues at 15:01", with Cancel).
//
// Its error shows as one until you've seen it (focusPane, or the pane on
// screen in front as it stopped: Agent.seen); then it's a calm wait, out of
// Needs You. Cancelled, or if it can't carry on, it's an error again, in
// Needs You.
//
// How it carries on: an agent with a wait of its own (Claude Code's
// "continue automatically", agents.Agent.LimitWait) is left to it. When its
// limit menu offers that wait, Hammock picks it for you; once its screen
// says it's set to continue, Hammock shows its time and types nothing, and
// its carrying on (a hook: it's working) is the end of it. Cancel sends it
// Esc, which cancels its own; its "Automatic continue cancelled" cancels
// Hammock's. Any other agent (Codex, an older Claude Code) is sent
// "continue".
//
// When: a moment after the reset (limitResumeDelay), the reset as the
// agent said it in its error, else as its usage windows say (the latest
// reset of those used up, usage.go). When neither says, Hammock tries
// after an hour, then two, four and eight as the limit is hit again: one
// "continue" each time, never more, and never sooner than five minutes
// after one that met the limit again.
//
// Not sent if the agent moved on meanwhile (anything but the same errored
// stop), someone typed in its pane since (that cancels it, even while it's
// being typed: they've taken over), or something's typed in its message
// box. Put off while a menu's open (it would answer it) or, after the pane
// was started again (a restart, restoreHolder), until the agent's said
// something or its prompt's drawn; a few times, then it's an error again.
// A sleeping agent is woken for it. Its Agent is kept with the pane, so a
// pending resume outlives a daemon restart; it's checked on the daemon's
// 2-second tick (pollCwds), not timed.
//
// A passing rate limit or an overloaded API isn't waited out: agents retry
// those themselves before they give up, and nothing says when trying again
// would work. A scheduled run waiting on a limit stays running (followRun)
// rather than failing; cancelled, it fails as an error would.
//
// Codex runs no hook when an error ends its turn: its session file says
// (agents.Agent.TurnFailed, followed here while its hooks are quiet, off
// d.mu, reading only what was appended).

import (
	"errors"
	"math/rand/v2"
	"os"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/agentview"
	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// limitResumeKey is the config file's setting (on by default).
const limitResumeKey = "agent-resume-after-limit"

// continueText is what an agent is sent once its limit resets.
const continueText = "continue"

func limitResumeOn() bool { return hammockconfig.Bool(limitResumeKey, true) }

// limitResumeDelay is how long after the reset the agent is sent
// "continue": a minute or two, so the service has reset by then, and
// agents stopped at the same limit don't all go at once.
var limitResumeDelay = func() time.Duration {
	return time.Minute + time.Duration(rand.Int64N(int64(time.Minute)))
}

// limitRetry is how long Hammock waits to try again when nothing says when
// the limit resets: an hour, doubling with each try that hits it again, to
// eight.
func limitRetry(tries int) time.Duration {
	return time.Hour << min(max(tries, 0), 3)
}

const (
	// limitResumeFar is the furthest off a reset is believed: a weekly
	// limit's is at most a week away; one further is a misreading.
	limitResumeFar = 8 * 24 * time.Hour
	// limitAgainWait is the least wait after "continue" met the limit
	// again (doubling with each, to an hour).
	limitAgainWait = 5 * time.Minute
	// limitMenuWait is how long Hammock waits to look again when the agent
	// is showing a menu at the reset; limitReadyWait, when its pane was
	// started again and it hasn't drawn its prompt.
	limitMenuWait  = 5 * time.Minute
	limitReadyWait = 30 * time.Second
	// limitRearms is how many times a resume is put off before it's given
	// up on (an error again); limitPicks, how many times its own wait is
	// picked for it.
	limitRearms = 6
	limitPicks  = 3
	// limitAgentGrace is how long after its own time an agent that waits
	// by itself has to carry on before it's taken not to have.
	limitAgentGrace = 15 * time.Minute
	// limitSentGrace is how long after "continue" is sent an agent still
	// errored is taken to be on its way.
	limitSentGrace = time.Minute
	// turnFailedQuiet is how long an agent's hooks are quiet before its
	// session file is read for a turn an error ended; turnLocateEvery, how
	// often a session file that wasn't found is looked for again.
	turnFailedQuiet = 3 * time.Second
	turnLocateEvery = 30 * time.Second
)

// limitActor is who the audit log says sent "continue": Hammock itself.
const limitActor = "Hammock"

// limitState is a pane's resuming after a usage limit (beside its Agent's
// limit_resume, which is what's shown and kept).
type limitState struct {
	hit       int64     // the errored stop (its StateSinceUnixMs) that was a usage limit; 0: none
	tries     int       // "continue"s that met the limit again
	sent      time.Time // when "continue" was last sent
	sending   bool      // it's being sent
	takenOver bool      // someone typed while it was
	cancelled bool      // cancelled in Hammock: the agent's own wait isn't taken up again
	picks     int       // times its own wait was picked for it
	rearms    int       // times it was put off
	turn      struct {  // its session file, followed (checkTurnFailed)
		path, sid string
		tail      agents.TurnTail
		missAt    time.Time
		busy      bool
	}
}

// limitResumeFor is when p's agent is to carry on after hook h, if it ended
// its turn at its usage limit and the setting's on; nil otherwise. Callers
// hold d.mu (for its usage), not p.mu.
func (d *Daemon) limitResumeFor(p *pane, harness string, h agents.Hook, now time.Time) *pb.LimitResume {
	lim, ok := agents.Get(harness).HitLimit(h, now)
	if !ok || !limitResumeOn() {
		return nil
	}
	at, known := lim.Resets, !lim.Resets.IsZero()
	if !known {
		for _, u := range d.usage.GetAgents() {
			if u.Harness == harness {
				at = agents.ExhaustedReset(u.Windows, now)
			}
		}
		known = !at.IsZero()
	}
	if known && at.After(now.Add(limitResumeFar)) {
		known = false
	}
	p.mu.Lock()
	tries := min(p.limit.tries, 8)
	p.mu.Unlock()
	switch {
	case !known:
		at = now.Add(limitRetry(tries))
	case at.Before(now):
		at = now.Add(limitResumeDelay())
	default:
		at = at.Add(limitResumeDelay())
	}
	// Sent "continue" and stopped at the limit again: whatever the reset
	// said, it wasn't yet. Never straight back.
	if tries > 0 {
		if soonest := now.Add(min(limitAgainWait<<(tries-1), time.Hour)); at.Before(soonest) {
			at = soonest
		}
	}
	return &pb.LimitResume{AtUnixMs: at.UnixMilli(), ResetKnown: known, HitUnixMs: now.UnixMilli()}
}

// limitHit notes that p's agent's errored stop since since was its usage
// limit: what's tried for it starts afresh. Callers hold p.mu.
func (p *pane) limitHitLocked(since int64) {
	p.limit.hit, p.limit.picks, p.limit.rearms, p.limit.cancelled, p.limit.takenOver = since, 0, 0, false, false
}

// keepWaiting: a hook that, for an agent waiting out its usage limit, isn't
// news: Claude's "waiting for your input" a minute after it stopped, or its
// session starting again (a restart, restoreHolder resuming it). Callers
// hold p.mu.
func (p *pane) keepWaitingLocked(event string, state pb.AgentState, sid string) bool {
	a := p.info.Agent
	if a.GetState() != pb.AgentState_AGENT_STATE_ERRORED || a.GetLimitResume() == nil {
		return false
	}
	switch event {
	case "Notification":
		return state == pb.AgentState_AGENT_STATE_IDLE
	case "SessionStart":
		return sid == "" || sid == a.SessionId
	}
	return false
}

// resumeLimitedAgents carries on each agent whose limit has reset, follows
// the agents that wait by themselves, and forgets the resumes that no
// longer apply. Each tick; callers hold d.mu.
func (d *Daemon) resumeLimitedAgents() {
	var on *bool // the setting, read once there's something to check
	setting := func() bool {
		if on == nil {
			v := limitResumeOn()
			on = &v
		}
		return *on
	}
	now := time.Now()
	for _, p := range d.panes {
		p.mu.Lock()
		a := p.info.Agent
		lr := a.GetLimitResume()
		errored := a.GetState() == pb.AgentState_AGENT_STATE_ERRORED
		ag := agents.Get(a.GetHarness())
		// An agent with a wait of its own, stopped at its limit: its screen
		// says what it's doing (unless you cancelled it here).
		watch := errored && ag != nil && ag.LimitWait != nil && p.limit.hit == a.StateSinceUnixMs && !p.limit.cancelled && !p.limit.sending
		if lr == nil && !watch {
			p.mu.Unlock()
			continue
		}
		p.mu.Unlock()
		if !setting() {
			if lr != nil {
				d.dropLimitResume(p, limitOff)
			}
			continue
		}
		if watch {
			d.followAgentWait(p, ag, now)
		}
		p.mu.Lock()
		a = p.info.Agent
		lr = a.GetLimitResume()
		if lr == nil {
			p.mu.Unlock()
			continue
		}
		stale := a.State != pb.AgentState_AGENT_STATE_ERRORED || p.info.Exited || p.closed
		at := time.UnixMilli(lr.AtUnixMs)
		gaveUp := !stale && lr.AgentContinues && now.After(at.Add(limitAgentGrace))
		due := !stale && !lr.AgentContinues && !now.Before(at)
		since := a.StateSinceUnixMs
		if due {
			// Being sent: typing now still takes over (userInput).
			a.LimitResume = nil
			p.limit.sending, p.limit.takenOver, p.limit.sent = true, false, now
		}
		p.mu.Unlock()
		switch {
		case stale:
			d.dropLimitResume(p, limitMovedOn)
		case gaveUp:
			d.log.Info("an agent didn't carry on by itself after its usage limit", "pane", p.id)
			d.dropLimitResume(p, limitFailed)
		case due:
			d.limitResumeChanged(p)
			go d.continueAfterLimit(p, since)
		}
	}
}

// followAgentWait reads the screen of an agent that waits for its usage
// limit by itself (Claude Code's own "continue automatically"): set to
// continue, Hammock shows its time and stands down; cancelled there, so is
// Hammock's; offered in its menu while Hammock's waiting, it's picked.
// Callers hold d.mu.
func (d *Daemon) followAgentWait(p *pane, ag *agents.Agent, now time.Time) {
	w := ag.LimitWait(p.text(false), now)
	p.mu.Lock()
	a := p.info.Agent
	lr := a.GetLimitResume()
	switch {
	case w.Cancelled:
		p.mu.Unlock()
		if lr != nil && lr.AgentContinues {
			d.dropLimitResume(p, limitCancelled)
		}
		return
	case w.Armed:
		next := &pb.LimitResume{AtUnixMs: lr.GetAtUnixMs(), ResetKnown: lr.GetResetKnown(), HitUnixMs: cmpOrInt64(lr.GetHitUnixMs(), a.StateSinceUnixMs), AgentContinues: true}
		if !w.At.IsZero() {
			next.AtUnixMs, next.ResetKnown = w.At.UnixMilli(), true
		}
		if next.AtUnixMs == 0 {
			next.AtUnixMs = now.UnixMilli() // it says "shortly"
		}
		changed := lr == nil || !lr.AgentContinues || lr.AtUnixMs != next.AtUnixMs
		if changed {
			a.LimitResume = next
		}
		p.mu.Unlock()
		if changed {
			d.limitResumeChanged(p)
		}
		return
	case w.Offer > 0 && lr != nil && !lr.AgentContinues && p.limit.picks < limitPicks:
		p.limit.picks++
		p.mu.Unlock()
		go d.pickAgentWait(p, ag)
		return
	}
	p.mu.Unlock()
}

// pickAgentWait picks, in the agent's limit menu, its own "wait here, then
// continue automatically", while the menu still shows it. Not under d.mu.
func (d *Daemon) pickAgentWait(p *pane, ag *agents.Agent) {
	text := p.text(false)
	w := ag.LimitWait(text, time.Now())
	sc := agentview.ReadScreen(text)
	if w.Offer == 0 || w.Offer > len(sc.Options) {
		return
	}
	cur := &pb.AgentPrompt{Options: sc.Options, PromptId: sc.Key, Selected: int32(sc.Selected)}
	steps, err := agentview.AnswerSteps(cur, &pb.AgentAnswer{Option: int32(w.Offer), OptionsSeen: sc.Options, PromptId: sc.Key}, sc.Arrows, p.kittyKeys())
	if err == nil {
		err = d.runSteps(nil, p, steps)
	}
	if err != nil {
		d.log.Info("couldn't pick an agent's own wait for its usage limit", "pane", p.id, "err", err)
		return
	}
	d.log.Info("picked an agent's own wait for its usage limit", "pane", p.id)
}

// limitResumeChanged saves and publishes p after its resume changed, and
// lets a scheduled run that was waiting on it end (followRun: not while
// "continue" is on its way). Callers hold d.mu.
func (d *Daemon) limitResumeChanged(p *pane) {
	info := p.snapshotInfo()
	d.persist(d.store.PutPane(info))
	d.publish(&pb.StateUpdate{Panes: []*pb.Pane{info}})
	d.scheduleAgentChanged(p)
}

// continueAfterLimit sends p's agent "continue", if it's still where its
// limit left it (errored since since), ready, with nothing typed for it and
// nobody typing. It takes a moment: not under d.mu.
func (d *Daemon) continueAfterLimit(p *pane, since int64) {
	done := func(why limitEnd) {
		d.mu.Lock()
		p.mu.Lock()
		p.limit.sending = false
		p.mu.Unlock()
		if why != limitSent {
			d.endLimitResume(p, why)
		}
		d.mu.Unlock()
	}
	still := func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		a := p.info.Agent
		return !p.limit.takenOver && a != nil && a.State == pb.AgentState_AGENT_STATE_ERRORED && a.StateSinceUnixMs == since && !p.closed && !p.info.Exited
	}
	if !still() {
		done(limitMovedOn)
		return
	}
	p.mu.Lock()
	asleep, draft := p.info.Asleep, p.wakeDraft
	p.mu.Unlock()
	if asleep {
		if draft != "" {
			d.log.Info("didn't continue an agent after its usage limit: something's typed in its message box", "pane", p.id)
			done(limitFailed)
			return
		}
		if err := d.wakeAndWait(p, limitActor); err != nil {
			d.log.Info("didn't continue an agent after its usage limit", "pane", p.id, "err", err)
			done(limitFailed)
			return
		}
	} else {
		screen := p.text(false)
		switch {
		case agentview.InputDraft(p.bottomText(20)) != "":
			d.log.Info("didn't continue an agent after its usage limit: something's typed in its message box", "pane", p.id)
			done(limitFailed)
			return
		case len(agentview.ReadScreen(screen).Options) > 0:
			// A menu's open: typing would answer it. Look again later.
			d.putOffLimitResume(p, since, limitMenuWait)
			return
		case !p.agentReady(screen):
			// Started again (a restart) and not at its prompt yet.
			d.putOffLimitResume(p, since, limitReadyWait)
			return
		}
		if !still() {
			done(limitMovedOn)
			return
		}
	}
	if err := d.typeContinue(p, still); err != nil {
		d.log.Info("didn't continue an agent after its usage limit", "pane", p.id, "err", err)
		if errors.Is(err, errTypedOver) {
			done(limitMovedOn)
		} else {
			done(limitFailed)
		}
		return
	}
	d.mu.Lock()
	p.mu.Lock()
	p.limit.sending = false
	p.limit.tries = min(p.limit.tries+1, 8)
	p.mu.Unlock()
	d.audit(limitActor, "limit-continue", p.id, "")
	d.mu.Unlock()
	d.log.Info("continued an agent after its usage limit", "pane", p.id)
}

// errTypedOver: someone typed while "continue" was being sent.
var errTypedOver = errors.New("someone typed in it")

// typeContinue types "continue" into the agent's empty message box, then
// Enter, unless someone types meanwhile: only Enter for what it typed (the
// box still holds just that), never clearing what they did.
func (d *Daemon) typeContinue(p *pane, still func() bool) error {
	if !still() {
		return errTypedOver
	}
	if _, err := p.input(nil, firstPromptPaste(continueText, p.bracketedPaste())); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond) // a separate burst, or it's part of the paste
	if !still() {
		return errTypedOver
	}
	if got := agentview.InputDraft(p.bottomText(20)); got != "" && got != continueText {
		return errTypedOver // more than it typed: someone's
	}
	_, err := p.input(nil, agentview.Enter(p.kittyKeys()))
	return err
}

// agentReady: p's agent can take a message: it's said something since its
// process started (its hooks), or its prompt is on screen with no menu.
func (p *pane) agentReady(screen string) bool {
	p.mu.Lock()
	start, hookAt := p.agentStart, p.hookAt
	p.mu.Unlock()
	if start.IsZero() || hookAt.After(start) {
		return true
	}
	return agentview.HasInput(screen) && len(agentview.ReadScreen(screen).Options) == 0
}

// putOffLimitResume waits after before trying again, as long as nothing's
// changed (no cancel, no typing, the same stop) and it hasn't been put off
// too often already: then it's given up on, an error again.
func (d *Daemon) putOffLimitResume(p *pane, since int64, after time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p.mu.Lock()
	a := p.info.Agent
	p.limit.sending = false
	ok := !p.limit.takenOver && !p.limit.cancelled && a != nil && a.State == pb.AgentState_AGENT_STATE_ERRORED &&
		a.StateSinceUnixMs == since && a.LimitResume == nil && p.limit.rearms < limitRearms
	if ok {
		p.limit.rearms++
		a.LimitResume = &pb.LimitResume{AtUnixMs: time.Now().Add(after).UnixMilli(), ResetKnown: true, HitUnixMs: since}
	}
	taken := p.limit.takenOver
	p.mu.Unlock()
	switch {
	case ok:
		d.limitResumeChanged(p)
	case !taken:
		d.endLimitResume(p, limitFailed)
	}
}

// limitEnd is why a resume ended.
type limitEnd int

const (
	limitSent      limitEnd = iota // "continue" went
	limitMovedOn                   // the agent moved on, or someone typed: nothing to say
	limitTyped                     // someone typed in its pane
	limitCancelled                 // cancelled: in Hammock, or in the agent's own wait
	limitFailed                    // it couldn't carry on: an error again
	limitOff                       // the setting was turned off
)

// cancelLimitResume is CancelLimitResume: p's agent won't carry on by
// itself when its limit resets; one waiting with its own wait is sent Esc,
// which cancels that.
func (d *Daemon) cancelLimitResume(c *conn, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.panes[id]
	if p == nil {
		return errors.New("no such pane")
	}
	p.mu.Lock()
	lr := p.info.Agent.GetLimitResume()
	sending := p.limit.sending
	if lr != nil || sending {
		p.limit.cancelled = true
		p.limit.takenOver = sending
	}
	p.mu.Unlock()
	if lr == nil && !sending {
		return errors.New("its agent isn't waiting for a usage limit to reset")
	}
	if lr.GetAgentContinues() {
		if ag := agents.Get(p.snapshotInfo().GetAgent().GetHarness()); ag != nil && ag.LimitWait != nil && ag.LimitWait(p.text(false), time.Now()).Armed {
			_, _ = p.input(nil, []byte{0x1b}) // its own wait's "Esc to cancel"
		}
	}
	d.dropLimitResume(p, limitCancelled)
	d.audit(c.auditActor(), "limit-cancel", p.id, "")
	return nil
}

// dropLimitResume forgets p's pending resume, if it has one, for why, and
// says so. Callers hold d.mu.
func (d *Daemon) dropLimitResume(p *pane, why limitEnd) bool {
	p.mu.Lock()
	a := p.info.Agent
	had := a.GetLimitResume() != nil || p.limit.sending
	if why == limitTyped && p.limit.sending {
		p.limit.takenOver = true
	}
	if why == limitTyped || why == limitCancelled {
		p.limit.tries = 0
	}
	if a != nil {
		a.LimitResume = nil
	}
	p.mu.Unlock()
	if had {
		d.endLimitResume(p, why)
	}
	return had
}

// endLimitResume settles p after its resume ended for why: cancelled, or
// failed, its agent's error is back, in Needs You (unless it moved on, or
// someone's typing in it). Callers hold d.mu.
func (d *Daemon) endLimitResume(p *pane, why limitEnd) {
	p.mu.Lock()
	a := p.info.Agent
	if a != nil {
		a.LimitResume = nil
	}
	back := (why == limitCancelled || why == limitFailed || why == limitOff) && a.GetState() == pb.AgentState_AGENT_STATE_ERRORED && !p.info.Exited
	var summary string
	if back {
		a.Seen = false
		summary = a.Summary
	}
	p.mu.Unlock()
	u := &pb.StateUpdate{}
	if back {
		d.raiseAttention(p, pb.AgentState_AGENT_STATE_ERRORED, summary, u)
	}
	info := p.snapshotInfo()
	d.persist(d.store.PutPane(info))
	u.Panes = append(u.Panes, info)
	d.publish(u)
	d.scheduleAgentChanged(p)
}

// seeLimitWait: you've seen p's agent stopped at its usage limit (focused
// it): it's a calm wait now, out of Needs You. Callers hold d.mu.
func (d *Daemon) seeLimitWait(p *pane, u *pb.StateUpdate) bool {
	p.mu.Lock()
	a := p.info.Agent
	ack := a != nil && !a.Seen && a.State == pb.AgentState_AGENT_STATE_ERRORED && a.LimitResume != nil
	if ack {
		a.Seen = true
	}
	p.mu.Unlock()
	if ack {
		d.clearAttentionForPane(p.id, u)
	}
	return ack
}

// waitingOnLimit: p's agent, errored, is waiting for its usage limit to
// reset, or "continue" is on its way (it'll be working in a moment): a
// scheduled run's carries on. Callers hold d.mu, not p.mu.
func (p *pane) waitingOnLimit() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info.Agent.GetLimitResume() != nil || p.limit.sending || time.Since(p.limit.sent) < limitSentGrace
}

// checkTurnFailed follows the session file of a hooked agent whose hooks
// don't say when an error ends its turn (Codex's; agents.Agent.TurnFailed)
// for one that did, taking it as the StopFailure no hook sent: while it's
// working with its hooks quiet a moment, reading only what was appended,
// off d.mu. A session file that isn't found is looked for again every
// turnLocateEvery; another session (Codex's /new) is followed in its own.
// Callers hold d.mu.
func (d *Daemon) checkTurnFailed(p *pane) {
	p.mu.Lock()
	a := p.info.Agent
	if a == nil || a.Heuristic || a.Reported || a.State != pb.AgentState_AGENT_STATE_WORKING || time.Since(p.hookAt) < turnFailedQuiet || p.limit.turn.busy {
		p.mu.Unlock()
		return
	}
	ag := agents.Get(a.Harness)
	if ag == nil || ag.TurnFailed == nil {
		p.mu.Unlock()
		return
	}
	harness, sid, since := a.Harness, a.SessionId, a.StateSinceUnixMs
	path := safeTranscript(a.TranscriptPath)
	t := &p.limit.turn
	if path == "" && t.sid == sid && t.path != "" {
		path = t.path
	}
	if path == "" && t.sid == sid && time.Since(t.missAt) < turnLocateEvery {
		p.mu.Unlock()
		return
	}
	if t.sid != sid || t.path != path && path != "" {
		t.tail, t.path = nil, ""
	}
	t.sid, t.busy = sid, true
	tail := t.tail
	p.mu.Unlock()
	go func() {
		if path == "" {
			path = sessionFile(&pb.Agent{Harness: harness, SessionId: sid})
		}
		var h agents.Hook
		var ok bool
		if path != "" {
			if _, err := os.Stat(path); err == nil {
				if tail == nil {
					tail = ag.TurnFailed(path)
				}
				h, ok = tail.Next(time.UnixMilli(since))
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		p.mu.Lock()
		t := &p.limit.turn
		t.busy = false
		if t.sid == sid {
			if path == "" {
				t.missAt = time.Now()
			} else {
				t.path, t.tail = path, tail
			}
		}
		a := p.info.Agent
		still := a != nil && a.State == pb.AgentState_AGENT_STATE_WORKING && a.StateSinceUnixMs == since && a.SessionId == sid
		p.mu.Unlock()
		if ok && still {
			d.agentHub.handle(HookEvent{PaneID: p.id, Harness: harness, parsed: &h})
		}
	}()
}

func cmpOrInt64(a, b int64) int64 {
	if a != 0 {
		return a
	}
	return b
}
