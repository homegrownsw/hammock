package daemon

// Continuing a session (Schedule.session): each run of an agent schedule
// goes into one session, its own (started by its first run) or one of
// yours, instead of a new tab and session each time.
//
// A run is queued, then sent once it can be: into the session's tab when
// its agent is free (not working or asking you, no menu open, at its
// prompt) and nothing's typed in its message box, in the terminal or an
// app's composer, or kept from when it went to sleep. What's typed is
// never cleared: the run waits. With the session's tab closed, it's
// resumed in a new tab of the schedule's Group with the schedule's own
// options (its allowed tools; Codex's sandbox and approval), unless it's
// open on a linked computer. A tab of yours a run goes into (borrowed)
// stays yours: nothing marks it the schedule's, so it isn't closed, kept
// or stopped, and still sets off automations, and giving up on a run only
// marks it. A queued run waits at most the schedule's give-up time.

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/agentview"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// continued: s's runs carry on one session.
func continued(s *pb.Schedule) bool {
	return s.Session != pb.Schedule_SESSION_NEW && s.Kind != "command"
}

// continuedPrompt is what's typed into the session for a run: a line
// saying which automation sent it, then its prompt (with its events'
// details, fenced as data). The line is all the session is told of it.
func continuedPrompt(s *pb.Schedule, evs []*schedEvent) string {
	prompt := s.Prompt
	if len(evs) > 0 {
		prompt = eventPrompt(prompt, evs)
	}
	return "[From the Hammock automation “" + cmpOr(s.Name, "Automation") + "”]\n" + prompt
}

// cleanSession checks the session s is to carry on (old: s as it was).
// Callers hold d.mu.
func (d *Daemon) cleanSession(s, old *pb.Schedule) error {
	switch s.Session {
	case pb.Schedule_SESSION_NEW:
		s.SessionId, s.SessionName, s.SessionWarning = "", "", ""
		if s.Overlap == pb.Schedule_OVERLAP_WAIT {
			s.Overlap = pb.Schedule_OVERLAP_SKIP
		}
		return nil
	case pb.Schedule_SESSION_OWN, pb.Schedule_SESSION_PICKED:
	default:
		return fmt.Errorf("unknown session choice %d", s.Session)
	}
	a := agents.Get(s.Kind)
	if s.Kind == "command" || a == nil || a.Resume == nil {
		what := "A command"
		if a != nil {
			what = a.Name
		}
		return fmt.Errorf("%s can't carry on a session: give each run a new tab", what)
	}
	// Wait (the default) or Skip: a run never stops the session's last.
	// Skip is overlap's zero, so it's only taken as chosen once the
	// schedule already carried on a session.
	if s.Overlap != pb.Schedule_OVERLAP_SKIP || old == nil || old.Session == pb.Schedule_SESSION_NEW {
		s.Overlap = pb.Schedule_OVERLAP_WAIT
	}
	if s.Session == pb.Schedule_SESSION_OWN {
		s.SessionId = "" // the daemon's: its first run's, kept across edits
		if old != nil && old.Session == pb.Schedule_SESSION_OWN && old.Kind == s.Kind {
			s.SessionId, s.SessionName = old.SessionId, old.SessionName
		}
	} else {
		s.SessionId = strings.TrimSpace(s.SessionId)
		if s.SessionId == "" {
			return errors.New("choose the session runs go into")
		}
		if strings.ContainsFunc(s.SessionId, func(r rune) bool { return r <= ' ' || r == 0x7f || bidiControl(r) }) || len(s.SessionId) > 200 {
			return errors.New("that isn't a session id")
		}
		s.SessionName = oneLine(shownText(s.SessionName), 80)
		if old != nil && old.SessionId == s.SessionId && s.SessionName == "" {
			s.SessionName = old.SessionName
		}
	}
	d.labelSession(s)
	return nil
}

// labelSession fills in what s's session is called (its tab's name, when
// it's open here) and whether its tab lets the agent do more than s would.
// changed: either did. Callers hold d.mu.
func (d *Daemon) labelSession(s *pb.Schedule) (changed bool) {
	if s.SessionId == "" {
		changed = s.SessionWarning != ""
		s.SessionWarning = ""
		return changed
	}
	name, warn := s.SessionName, ""
	if p := d.sessionPane(s.Kind, s.SessionId); p != nil {
		if t := d.tabs[p.tabID()]; t != nil && strings.TrimSpace(t.Name) != "" {
			name = oneLine(t.Name, 80)
		}
		if p.snapshotInfo().ScheduleId != s.Id || s.Id == "" {
			warn = d.widerSession(p, s)
		}
	}
	changed = name != s.SessionName || warn != s.SessionWarning
	s.SessionName, s.SessionWarning = name, warn
	return changed
}

// widerSession says how p's agent may do more than a run of s would
// ("" when it may not, or that can't be told). Callers hold d.mu.
func (d *Daemon) widerSession(p *pane, s *pb.Schedule) string {
	p.mu.Lock()
	pid, asleep, wake := p.pid, p.info.Asleep, p.wakeCmd
	p.mu.Unlock()
	var argv []string
	if asleep {
		argv = strings.Fields(wake) // (what it resumes with when it wakes)
	} else if _, apid := findAgentProc(pid); apid != 0 {
		argv = procinfo.Args(apid)
	} else if cmd := p.snapshotInfo().Command; len(cmd) > 0 && agents.ByProcess(filepath.Base(cmd[0])) != nil {
		argv = cmd // (the pane's own command: its process not found yet)
	}
	flag := agents.WiderThan(argv, s)
	if flag == "" {
		return ""
	}
	name := "Its agent"
	if a := agents.Get(s.Kind); a != nil {
		name = a.Name
	}
	return fmt.Sprintf("Its tab runs %s with %s, so runs sent there can do more than this automation allows", name, flag)
}

// sessionPane is the pane on this computer running session id of agent
// kind (asleep too), if one is. Callers hold d.mu.
func (d *Daemon) sessionPane(kind, id string) *pane {
	if id == "" {
		return nil
	}
	var found *pane
	for _, p := range d.panes {
		info := p.snapshotInfo()
		a := info.Agent
		if a == nil || a.SessionId != id || a.Harness != "" && a.Harness != kind || info.Exited ||
			a.State == pb.AgentState_AGENT_STATE_EXITED && !info.Asleep {
			continue
		}
		if found == nil || p.id < found.id {
			found = p
		}
	}
	return found
}

// sessionElsewhere is the linked computer (its name) with session id open
// in one of its panes, as far as this one knows (only those online now
// say what they have open), or "". Callers hold d.mu.
func (d *Daemon) sessionElsewhere(id string) string {
	for _, l := range d.links {
		if !l.linked || l.gone {
			continue
		}
		for _, p := range l.panes {
			if p.GetAgent().GetSessionId() == id && !p.Exited {
				return strings.TrimSuffix(d.computerName(l.tok.GetHostUserId()), ".local")
			}
		}
	}
	return ""
}

// followSession: s carries on the session in p's tab (the last run went
// there), which has moved to a new session (/clear, or another resumed
// there): s follows it. Callers hold d.mu.
func (d *Daemon) followSession(s *pb.Schedule, p *pane) {
	var last *pb.ScheduleRun
	for _, r := range s.Runs {
		if r.PaneId != "" && r.Continued {
			last = r
			break
		}
	}
	a := p.snapshotInfo().Agent
	if last == nil || last.PaneId != p.id || a == nil || a.SessionId == "" || a.SessionId == s.SessionId ||
		a.Harness != "" && a.Harness != s.Kind {
		return
	}
	s.SessionId = a.SessionId
	d.labelSession(s)
	d.saveSchedule(s)
}

// startContinued queues r (just made) to go into s's session, and sends it
// if it can go now. Callers hold d.mu.
func (d *Daemon) startContinued(s *pb.Schedule, r *pb.ScheduleRun, manual bool, evs []*schedEvent) *pb.ScheduleRun {
	now := schedTime()
	r.Continued = true
	going, waiting := false, false
	for _, x := range s.Runs {
		switch {
		case x.Status == pb.ScheduleRun_STATUS_QUEUED:
			waiting = true
		case runActive(x):
			going = true
		}
	}
	skip := func(note string) *pb.ScheduleRun {
		r.Status, r.Note, r.StartedUnixMs = pb.ScheduleRun_STATUS_SKIPPED, note, 0
		d.noteRun(s, r)
		d.audit("schedule:"+s.Id, "schedule-skip", s.Id, s.Name+": "+strings.ToLower(note[:1])+note[1:])
		return r
	}
	switch {
	case waiting:
		return skip("A run was already waiting to go into its session")
	case going && s.Overlap == pb.Schedule_OVERLAP_SKIP && !manual:
		return skip("The last run was still going")
	}
	r.Status, r.WaitingSinceUnixMs = pb.ScheduleRun_STATUS_QUEUED, now.UnixMilli()
	if going {
		r.Note = "Waiting for the last run to finish"
	}
	d.sched.queued[r.Id] = continuedPrompt(s, evs)
	d.saveQueued()
	d.noteRun(s, r)
	d.followQueued(s, now, true)
	return r
}

// followQueued gives up on s's run waiting to go into its session once
// it's waited the schedule's give-up time, and sends it (send) if it can
// go now. changed: s needs saving. Callers hold d.mu.
func (d *Daemon) followQueued(s *pb.Schedule, now time.Time, send bool) (changed bool) {
	var q *pb.ScheduleRun // the oldest waiting
	going := false
	for _, r := range s.Runs {
		switch {
		case r.Status == pb.ScheduleRun_STATUS_QUEUED:
			q = r
		case runActive(r):
			going = true
		}
	}
	if q == nil || d.sched.sending[q.Id] {
		return false
	}
	giveUp := time.Duration(cmpOrInt(s.GiveUpMinutes, defaultGiveUp)) * time.Minute
	if now.Sub(time.UnixMilli(q.WaitingSinceUnixMs)) >= giveUp {
		why := "Gave up after waiting " + humanMinutes(giveUp) + " to send it"
		if q.Note != "" {
			why += " (" + strings.ToLower(q.Note[:1]) + q.Note[1:] + ")"
		}
		d.endQueued(s, q, pb.ScheduleRun_STATUS_GAVE_UP, why)
		return true
	}
	if !continued(s) {
		// Changed back to a new tab each run since: it's dropped.
		d.dropQueued(q)
		q.Status, q.Note, q.WaitingSinceUnixMs, q.StartedUnixMs = pb.ScheduleRun_STATUS_SKIPPED, "Its automation no longer carries on a session", 0, 0
		return true
	}
	if going {
		return d.noteQueued(q, "Waiting for the last run to finish")
	}
	if !send {
		return false
	}
	msg, ok := d.sched.queued[q.Id]
	if !ok {
		d.endQueued(s, q, pb.ScheduleRun_STATUS_FAILED, "Its message was lost")
		return true
	}
	if s.SessionId == "" {
		// This automation's own session, before its first run: a new tab
		// whose session is its from now on.
		d.openContinued(s, q, msg, "")
		return true
	}
	if p := d.sessionPane(s.Kind, s.SessionId); p != nil {
		return d.sendContinued(s, q, p, msg)
	}
	if where := d.sessionElsewhere(s.SessionId); where != "" {
		d.endQueued(s, q, pb.ScheduleRun_STATUS_FAILED, "Its session is open on "+where+": close it there, or run the automation there")
		return true
	}
	d.openContinued(s, q, msg, s.SessionId)
	return true
}

// noteQueued says why q is still waiting. Callers hold d.mu.
func (d *Daemon) noteQueued(q *pb.ScheduleRun, why string) bool {
	if q.Note == why {
		return false
	}
	q.Note = why
	return true
}

// endQueued ends a run that never went in. Callers hold d.mu.
func (d *Daemon) endQueued(s *pb.Schedule, q *pb.ScheduleRun, status pb.ScheduleRun_Status, why string) {
	d.dropQueued(q)
	if status == pb.ScheduleRun_STATUS_FAILED {
		s.Error = ""
	}
	d.endRun(s, q, status, why)
	if status == pb.ScheduleRun_STATUS_GAVE_UP {
		d.audit("schedule:"+s.Id, "schedule-gave-up", s.Id, s.Name+": "+why)
	}
}

// dropQueued forgets r's message, if it's waiting. Callers hold d.mu.
func (d *Daemon) dropQueued(r *pb.ScheduleRun) {
	if _, ok := d.sched.queued[r.Id]; ok {
		delete(d.sched.queued, r.Id)
		d.saveQueued()
	}
}

// saveQueued keeps queued runs' messages, for a restart. Callers hold d.mu.
func (d *Daemon) saveQueued() {
	b, _ := json.Marshal(d.sched.queued)
	d.persist(d.store.SetSetting("schedule_queued", string(b)))
}

// openContinued starts q in a new tab of s's Group: session id resumed
// with s's own options (its tab was closed), or ("") a new session that
// becomes s's. The message is typed once the agent's at its prompt.
// Callers hold d.mu.
func (d *Daemon) openContinued(s *pb.Schedule, q *pb.ScheduleRun, msg, id string) {
	now := schedTime()
	cwd := cleanDir(s.Cwd)
	if cwd == "" {
		d.endQueued(s, q, pb.ScheduleRun_STATUS_FAILED, "There's no folder "+s.Cwd)
		return
	}
	a := agents.Get(s.Kind)
	var typed, later string
	if id == "" {
		run := proto.CloneOf(s)
		run.Prompt = msg
		typed, later = startCommand(a, msg, run)
	} else {
		typed, later = a.ScheduleResume(s, id), msg
	}
	p, err := d.openRunTab(s, q, now, &pb.PaneSpec{Cwd: cwd}, typed)
	if err != nil {
		d.endQueued(s, q, pb.ScheduleRun_STATUS_FAILED, err.Error())
		return
	}
	d.dropQueued(q)
	q.StartedUnixMs, q.WaitingSinceUnixMs, q.Note = now.UnixMilli(), 0, ""
	if id != "" {
		q.SessionId = id
		p.mu.Lock()
		p.info.Agent.SessionId = id // (known before its hooks say: no second copy meanwhile)
		p.mu.Unlock()
	}
	d.audit("schedule:"+s.Id, "schedule-run", q.TabId, s.Name)
	d.typeFirstPrompt(p, later)
}

// cleanDir is dir if it's a folder that's there, else "".
func cleanDir(dir string) string {
	dir = expandHome(dir)
	if !isDir(dir) {
		return ""
	}
	return dir
}

// sendContinued sends q into p, the session's tab, if its agent can take
// it now: marked under way first (so its turn is followed from the start),
// then typed in off the lock. Callers hold d.mu.
func (d *Daemon) sendContinued(s *pb.Schedule, q *pb.ScheduleRun, p *pane, msg string) bool {
	if why := d.sessionBusy(p); why != "" {
		return d.noteQueued(q, why)
	}
	owned := p.snapshotInfo().ScheduleId == s.Id
	if d.labelSession(s) {
		d.saveSchedule(s)
	}
	now := schedTime()
	prev := proto.CloneOf(q)
	q.Status, q.StartedUnixMs, q.WaitingSinceUnixMs, q.Note = pb.ScheduleRun_STATUS_RUNNING, now.UnixMilli(), 0, ""
	q.TabId, q.PaneId, q.Borrowed, q.SessionId = p.tabID(), p.id, !owned, s.SessionId
	if owned {
		// The schedule's own tab: this run's now.
		p.mu.Lock()
		p.info.ScheduleRunId = q.Id
		p.mu.Unlock()
		d.persist(d.store.PutPane(p.snapshotInfo()))
		d.paneChanged(p)
	}
	d.sched.sending[q.Id] = true
	go func() {
		err := d.typeIntoSession(p, msg)
		d.mu.Lock()
		defer d.mu.Unlock()
		delete(d.sched.sending, q.Id)
		cur := d.sched.list[s.Id]
		if cur == nil {
			return
		}
		s := cur
		r := runByID(s, q.Id)
		if r == nil {
			return
		}
		switch {
		case err == nil:
			d.dropQueued(r)
			d.audit("schedule:"+s.Id, "schedule-run", r.TabId, s.Name)
		case errors.Is(err, errSessionBusy):
			// Someone got there first: back in the queue, as it was.
			r.Status, r.StartedUnixMs, r.WaitingSinceUnixMs = prev.Status, prev.StartedUnixMs, prev.WaitingSinceUnixMs
			r.TabId, r.PaneId, r.Borrowed, r.SessionId = prev.TabId, prev.PaneId, prev.Borrowed, prev.SessionId
			r.Note = strings.TrimPrefix(err.Error(), errSessionBusy.Error()+": ")
		default:
			d.dropQueued(r)
			d.endRun(s, r, pb.ScheduleRun_STATUS_FAILED, "Couldn't send it into its session: "+err.Error())
		}
		d.saveSchedule(s)
	}()
	return true
}

// runByID is s's run id, if it has it.
func runByID(s *pb.Schedule, id string) *pb.ScheduleRun {
	for _, r := range s.Runs {
		if r.Id == id {
			return r
		}
	}
	return nil
}

// errSessionBusy: the session can't take a run right now (it's said why).
var errSessionBusy = errors.New("busy")

// sessionBusy says why p's agent can't take a run now ("" if it can).
// Callers hold d.mu.
func (d *Daemon) sessionBusy(p *pane) string {
	info := p.snapshotInfo()
	a := info.Agent
	p.mu.Lock()
	wake, composer, waking := p.wakeDraft, p.composer.text, p.waking || p.sleepBusy
	p.mu.Unlock()
	typed := "Something's typed in its message box"
	switch {
	case strings.TrimSpace(composer) != "":
		return typed
	case info.Asleep && wake != "":
		return typed
	case info.Asleep:
		return "" // it wakes to take it
	case waking:
		return "Its agent is waking"
	case a == nil:
		return "Its agent isn't running"
	case a.State == pb.AgentState_AGENT_STATE_WORKING || a.State == pb.AgentState_AGENT_STATE_STARTING:
		return "Its agent is working"
	case a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT:
		return "Its agent is waiting for you"
	case a.Subagents > 0 || a.RunningCommand || a.Shells > 0:
		return "Its agent is still working"
	case a.State == pb.AgentState_AGENT_STATE_ERRORED && p.waitingOnLimit():
		return "Its agent is waiting for its usage limit to reset"
	}
	return screenBusy(p)
}

// screenBusy says why p's screen shows its agent can't take a message
// now ("" if it can): a menu open, a subagent shown, something typed, or
// not at its prompt yet.
func screenBusy(p *pane) string {
	screen := p.text(false)
	switch {
	case len(agentview.ReadScreen(screen).Options) > 0:
		return "A menu is open in it"
	case agentview.ReadAgentList(screen).ViewingSubagent():
		return "It's showing a subagent"
	case agentview.InputDraft(p.bottomText(20)) != "":
		return "Something's typed in its message box"
	case !p.agentReady(screen):
		return "Its agent isn't at its prompt yet"
	}
	return ""
}

// typeIntoSession types msg into p's agent's empty message box, and Enter:
// waking it first if it's asleep. Never over what's typed there: that
// (or anything else in the way) is errSessionBusy, and nothing's typed.
// It takes a moment: not under d.mu.
func (d *Daemon) typeIntoSession(p *pane, msg string) error {
	p.mu.Lock()
	asleep, wake := p.info.Asleep, p.wakeDraft
	p.mu.Unlock()
	if asleep {
		if wake != "" {
			return fmt.Errorf("%w: Something's typed in its message box", errSessionBusy)
		}
		if err := d.wakeAndWait(p, "automation"); err != nil {
			return err
		}
		// Resumed: at its prompt once it's said so, or drawn it.
		for deadline := time.Now().Add(firstPromptWait); !p.agentReady(p.text(false)); {
			if time.Now().After(deadline) {
				return errors.New("its agent didn't reach its prompt after waking")
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	busy := func() error {
		p.mu.Lock()
		composer := p.composer.text
		a := proto.CloneOf(p.info.Agent)
		p.mu.Unlock()
		why := screenBusy(p)
		switch {
		case strings.TrimSpace(composer) != "":
			why = "Something's typed in its message box"
		case a == nil:
			why = "Its agent isn't running"
		case a.State == pb.AgentState_AGENT_STATE_WORKING || a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT:
			why = "Its agent is busy"
		}
		if why != "" {
			return fmt.Errorf("%w: %s", errSessionBusy, why)
		}
		return nil
	}
	if err := busy(); err != nil {
		return err
	}
	if _, err := p.input(nil, firstPromptPaste(msg, p.bracketedPaste())); err != nil {
		return err
	}
	time.Sleep(150 * time.Millisecond) // a separate burst, or it's part of the paste
	_, err := p.input(nil, agentview.Enter(p.kittyKeys()))
	return err
}

// borrowedRunIn: a run sent into p, a tab of yours, is under way: what its
// agent does then isn't an event for an automation that carries on a
// session (two of them answering each other's turns would never stop).
// Callers hold d.mu.
func (d *Daemon) borrowedRunIn(p *pane) bool {
	for _, s := range d.sched.list {
		if slices.ContainsFunc(s.Runs, func(r *pb.ScheduleRun) bool { return r.Borrowed && r.PaneId == p.id && runActive(r) }) {
			return true
		}
	}
	return false
}
