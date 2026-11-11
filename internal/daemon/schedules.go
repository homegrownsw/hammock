package daemon

// Scheduled tasks: the daemon starts an agent (Claude Code, Codex) with a
// prompt, or a command, on a schedule, each run a new tab in the
// schedule's Group, and keeps a record of how each run went. A run waiting
// on you goes to Needs You like any agent; one that waits too long is
// stopped. Runs missed while the computer slept catch up once, the same
// day (or within one interval, for cron).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/client"
	"github.com/homegrownsw/hammock/internal/holder"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

const (
	defaultKeepRuns = 7
	defaultGiveUp   = 240 // minutes
	maxRunRecords   = 30
	maxMissedNoted  = 3
	// lateAfter: a run started this long after it was due says it caught up.
	lateAfter = 2 * time.Minute
	// agentStartGrace: a run's agent not in its pane this long after it
	// started has failed to start (or, having worked, has gone).
	agentStartGrace = time.Minute
)

// commandRun: r ran a command (not an agent), whatever the schedule's
// kind is now. Runs from before kinds were kept go by the schedule's.
func commandRun(s *pb.Schedule, r *pb.ScheduleRun) bool { return cmpOr(r.Kind, s.Kind) == "command" }

// schedTick is how often schedules are checked (wall-clock times are
// compared, so a sleep in between is noticed on waking).
var schedTick = 15 * time.Second

// schedNow is the clock (tests replace it).
var schedNow = time.Now

// schedState is the daemon's scheduled tasks. Guarded by d.mu.
type schedState struct {
	list    map[string]*pb.Schedule // this computer's
	sawWork map[string]bool         // run id → its agent has started working (so idle means done)
	sawProc map[string]bool         // run id → its agent's process has been seen in its pane
	// madeBy is the agent that made each schedule it made (with your
	// leave): that agent may run, pause and skip it without asking again
	// (schedulemsgs.go). Kept in the "schedule_makers" setting.
	madeBy map[string]schedMaker
}

// schedMaker is the agent that made a schedule: its pane, and which agent
// was running there (agentIdentity), so a later one in the same pane
// doesn't inherit it.
type schedMaker struct {
	Pane    string `json:"pane"`
	Proc    string `json:"proc,omitempty"`
	Session string `json:"session,omitempty"`
}

func (d *Daemon) loadSchedules() {
	d.sched = schedState{list: map[string]*pb.Schedule{}, sawWork: map[string]bool{}, sawProc: map[string]bool{}, madeBy: map[string]schedMaker{}}
	if json.Unmarshal([]byte(d.store.Setting("schedule_makers")), &d.sched.madeBy) != nil {
		d.sched.madeBy = map[string]schedMaker{} // (an earlier build's, by pane alone: they ask again)
	}
	recs, err := d.store.Records("schedule")
	if err != nil {
		d.log.Error("load schedules", "err", err)
		return
	}
	for id, data := range recs {
		s := &pb.Schedule{}
		if err := proto.Unmarshal(data, s); err != nil {
			d.log.Warn("bad schedule record", "id", id, "err", err)
			continue
		}
		d.sched.list[id] = s
		for _, r := range s.Runs {
			if runActive(r) {
				d.sched.sawWork[r.Id] = true // it was under way before the restart
			}
		}
	}
	for _, s := range d.sched.list {
		s.ComputerId = d.id.me.UserId
		// A next run already set stays, even if it's past: the first check
		// then catches it up, or notes it missed (the computer was off).
		if s.NextRunUnixMs == 0 {
			d.scheduleNext(s, schedNow())
		}
	}
}

// runSchedules checks schedules until the daemon stops.
func (d *Daemon) runSchedules() {
	t := time.NewTicker(schedTick)
	defer t.Stop()
	for {
		d.mu.Lock()
		d.checkSchedules(schedNow())
		d.mu.Unlock()
		select {
		case <-d.done:
			return
		case <-t.C:
		}
	}
}

// checkSchedules starts what's due and follows the runs under way.
// Callers hold d.mu.
func (d *Daemon) checkSchedules(now time.Time) {
	if d.stopped {
		return
	}
	for _, s := range d.sched.list {
		changed := d.followRuns(s, now)
		if d.refreshScheduleGroup(s) {
			changed = true
		}
		if d.dueSchedule(s, now) {
			changed = true
		}
		if changed {
			d.saveSchedule(s)
		}
	}
}

// ---------- when ----------

func scheduleSpec(s *pb.Schedule) (*cronSpec, error) {
	if strings.TrimSpace(s.Cron) != "" {
		return parseCron(s.Cron)
	}
	return weekdaySpec(s.Weekdays, s.MinuteOfDay), nil
}

// scheduleNext sets when s next runs, after t. Callers hold d.mu.
func (d *Daemon) scheduleNext(s *pb.Schedule, t time.Time) {
	s.NextRunUnixMs = 0
	if s.Paused {
		return
	}
	spec, err := scheduleSpec(s)
	if err != nil {
		s.Error = err.Error()
		return
	}
	if n := spec.next(t); !n.IsZero() {
		s.NextRunUnixMs = n.UnixMilli()
	}
}

// catchUpOK: a run due at due may still start at now. A daily (weekday)
// schedule catches up the same day; a cron one within one interval.
func catchUpOK(s *pb.Schedule, due, now time.Time) bool {
	if strings.TrimSpace(s.Cron) == "" {
		y1, m1, d1 := due.Date()
		y2, m2, d2 := now.Date()
		return y1 == y2 && m1 == m2 && d1 == d2
	}
	spec, err := parseCron(s.Cron)
	if err != nil {
		return false
	}
	n := spec.next(due)
	return n.IsZero() || now.Before(n)
}

// dueSchedule starts s if it's due (or notes runs missed while asleep).
// Callers hold d.mu.
func (d *Daemon) dueSchedule(s *pb.Schedule, now time.Time) bool {
	if s.Paused || s.NextRunUnixMs == 0 || now.UnixMilli() < s.NextRunUnixMs {
		return false
	}
	spec, err := scheduleSpec(s)
	if err != nil {
		s.Error, s.NextRunUnixMs = err.Error(), 0
		return true
	}
	// Every time it was due up to now: the latest may run, the rest were missed.
	var dues []time.Time
	for t := time.UnixMilli(s.NextRunUnixMs); !t.IsZero() && !t.After(now) && len(dues) < 10000; t = spec.next(t) {
		dues = append(dues, t)
	}
	if len(dues) == 0 {
		d.scheduleNext(s, now)
		return true
	}
	latest := dues[len(dues)-1]
	missed := dues[:len(dues)-1]
	if len(missed) > maxMissedNoted {
		missed = missed[len(missed)-maxMissedNoted:]
	}
	for _, t := range missed {
		d.noteRun(s, &pb.ScheduleRun{Id: newID("sr"), DueUnixMs: t.UnixMilli(), Status: pb.ScheduleRun_STATUS_MISSED, Note: "The computer was asleep or off"})
	}
	switch {
	case latest.UnixMilli() < s.SkipUntilUnixMs:
		d.noteRun(s, &pb.ScheduleRun{Id: newID("sr"), DueUnixMs: latest.UnixMilli(), Status: pb.ScheduleRun_STATUS_SKIPPED, Note: "Skipped by you"})
	case !catchUpOK(s, latest, now):
		d.noteRun(s, &pb.ScheduleRun{Id: newID("sr"), DueUnixMs: latest.UnixMilli(), Status: pb.ScheduleRun_STATUS_MISSED, Note: "The computer was asleep or off"})
	default:
		if _, err := d.startRun(s, latest, false); err != nil {
			d.log.Warn("scheduled run", "schedule", s.Id, "err", err)
		}
	}
	if s.SkipUntilUnixMs != 0 && s.SkipUntilUnixMs <= now.UnixMilli() {
		s.SkipUntilUnixMs = 0
	}
	d.scheduleNext(s, now)
	return true
}

// ---------- running ----------

func runActive(r *pb.ScheduleRun) bool {
	return r.Status == pb.ScheduleRun_STATUS_RUNNING || r.Status == pb.ScheduleRun_STATUS_WAITING
}

// noteRun adds a run record, newest first, keeping the last maxRunRecords.
// Callers hold d.mu.
func (d *Daemon) noteRun(s *pb.Schedule, r *pb.ScheduleRun) {
	s.Runs = append([]*pb.ScheduleRun{r}, s.Runs...)
	// The oldest go first, but never one under way (it's still followed).
	for i := len(s.Runs) - 1; i >= 0 && len(s.Runs) > maxRunRecords; i-- {
		if !runActive(s.Runs[i]) {
			s.Runs = slices.Delete(s.Runs, i, i+1)
		}
	}
}

// startRun opens a run of s in a new tab of its Group (made again if it's
// been closed). due is when it was due (zero: Run Now). Callers hold d.mu.
func (d *Daemon) startRun(s *pb.Schedule, due time.Time, manual bool) (*pb.ScheduleRun, error) {
	now := schedNow()
	r := &pb.ScheduleRun{Id: newID("sr"), StartedUnixMs: now.UnixMilli(), Manual: manual, Kind: s.Kind}
	if !due.IsZero() {
		r.DueUnixMs = due.UnixMilli()
		r.CatchUp = now.Sub(due) > lateAfter
	}
	var active []*pb.ScheduleRun
	for _, x := range s.Runs {
		if runActive(x) {
			active = append(active, x)
		}
	}
	if len(active) > 0 {
		switch s.Overlap {
		case pb.Schedule_OVERLAP_SKIP:
			if !manual {
				r.Status, r.Note, r.StartedUnixMs = pb.ScheduleRun_STATUS_SKIPPED, "The last run was still going", 0
				d.noteRun(s, r)
				d.audit("schedule:"+s.Id, "schedule-skip", s.Id, s.Name+": the last run was still going")
				return r, nil
			}
		case pb.Schedule_OVERLAP_STOP_PREVIOUS:
			for _, x := range active {
				d.stopRun(s, x, pb.ScheduleRun_STATUS_STOPPED, "Stopped for a newer run")
			}
		}
	}
	fail := func(err error) (*pb.ScheduleRun, error) {
		s.Error = err.Error()
		r.Status, r.Note, r.EndedUnixMs = pb.ScheduleRun_STATUS_FAILED, err.Error(), now.UnixMilli()
		d.noteRun(s, r)
		d.notifyRun(s, r)
		return r, err
	}
	cwd := filepath.Clean(expandHome(s.Cwd))
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return fail(fmt.Errorf("there's no folder %s", s.Cwd))
	}
	g := d.groups[s.GroupId]
	made := g == nil
	if made {
		// Closed since: brought back, as it was named.
		g = d.createGroupQuiet(s.GroupName)
		g.Icon, g.Folder = s.GroupIcon, cwd
		d.persist(d.store.PutGroup(g))
		s.GroupId = g.Id
	}
	spec := &pb.PaneSpec{Cwd: cwd}
	var typed, later string // later: a first message typed once it's at its prompt
	if s.Kind == "command" {
		spec.Command = []string{loginShell(), "-l", "-c", s.Prompt}
	} else if a := agents.Get(s.Kind); a != nil && a.Schedulable {
		typed, later = startCommand(a, s.Prompt, s)
	} else {
		return fail(fmt.Errorf("unknown kind %q", s.Kind))
	}
	if typed != "" {
		// Started as the pane's command (as a resumed agent is), not typed
		// into its shell (agentShellCommand: a long tool list would be cut
		// at the line buffer's 1024 bytes). A shell stays once it exits.
		spec.Command = agentShellCommand(typed)
	}
	t, err := d.createTab(g.Id, runTabName(s, now), spec, "", "")
	if err != nil {
		if made {
			d.dropQuietGroup(g.Id)
			s.GroupId = ""
		}
		return fail(err)
	}
	p := d.panes[t.FocusedPaneId]
	if p == nil {
		return fail(errors.New("the run's tab has no pane"))
	}
	s.Error = ""
	r.Status, r.TabId, r.PaneId = pb.ScheduleRun_STATUS_RUNNING, t.Id, p.id
	p.mu.Lock()
	p.info.ScheduleId, p.info.ScheduleRunId = s.Id, r.Id
	if typed != "" {
		p.info.Agent = &pb.Agent{Harness: s.Kind, State: pb.AgentState_AGENT_STATE_STARTING, StateSinceUnixMs: now.UnixMilli(), Heuristic: true}
	}
	p.mu.Unlock()
	d.persist(d.store.PutPane(p.snapshotInfo()))
	d.paneChanged(p)
	d.noteRun(s, r)
	d.audit("schedule:"+s.Id, "schedule-run", t.Id, s.Name)
	d.closeOldRuns(s)
	d.typeFirstPrompt(p, later)
	return r, nil
}

// runTabName names a run's tab: the schedule and when it started.
func runTabName(s *pb.Schedule, t time.Time) string {
	name := cmpOr(s.Name, "Scheduled task")
	return name + " · " + t.Format("Mon 15:04")
}

// stopRun ends a run under way: Esc to an agent, ^C to a command. Its
// pane stays. Callers hold d.mu.
func (d *Daemon) stopRun(s *pb.Schedule, r *pb.ScheduleRun, status pb.ScheduleRun_Status, note string) {
	if p := d.panes[r.PaneId]; p != nil {
		key := "\x1b"
		if commandRun(s, r) {
			key = "\x03"
		}
		p.mu.Lock()
		p.postLocked(func(hc *holder.Client) { _ = hc.Input([]byte(key)) })
		p.mu.Unlock()
		u := &pb.StateUpdate{}
		d.clearAttentionForPane(p.id, u)
		if len(u.ClearedAttentionIds) > 0 {
			d.publish(u)
		}
	}
	r.Status, r.Note, r.EndedUnixMs, r.WaitingSinceUnixMs = status, note, schedNow().UnixMilli(), 0
	delete(d.sched.sawWork, r.Id)
	d.notifyRun(s, r)
}

// followRuns brings s's runs under way up to date with their panes.
// Callers hold d.mu.
func (d *Daemon) followRuns(s *pb.Schedule, now time.Time) bool {
	changed := false
	for _, r := range s.Runs {
		if r.TabId != "" && !r.TabClosed && d.tabs[r.TabId] == nil {
			r.TabClosed, changed = true, true
		}
		if !runActive(r) {
			continue
		}
		if d.followRun(s, r, now) {
			changed = true
		}
	}
	return changed
}

// followRun updates one run from its pane. Callers hold d.mu.
func (d *Daemon) followRun(s *pb.Schedule, r *pb.ScheduleRun, now time.Time) bool {
	p := d.panes[r.PaneId]
	if p == nil {
		r.Status, r.Note, r.EndedUnixMs = pb.ScheduleRun_STATUS_STOPPED, "Its tab was closed", now.UnixMilli()
		delete(d.sched.sawWork, r.Id)
		return true
	}
	info := p.snapshotInfo()
	if commandRun(s, r) {
		return false // its exit says how it went (scheduleCommandExited)
	}
	if info.Exited {
		return d.endRun(s, r, pb.ScheduleRun_STATUS_FAILED, "Its shell exited")
	}
	a := info.Agent
	if a == nil {
		// No agent in the pane: it never started (not installed, or it
		// exited at once), or it's gone without saying so (no hooks).
		if now.Sub(time.UnixMilli(r.StartedUnixMs)) < agentStartGrace {
			return false
		}
		if d.sched.sawWork[r.Id] {
			return d.endRun(s, r, pb.ScheduleRun_STATUS_READY, "The agent exited")
		}
		return d.endRun(s, r, pb.ScheduleRun_STATUS_FAILED, "The agent didn't start: is "+cmpOr(r.Kind, s.Kind)+" installed, and does it start in "+s.Cwd+"?")
	}
	if a.SessionId != "" && r.SessionId != a.SessionId {
		r.SessionId = a.SessionId
	}
	// Its process gone (crashed, killed) with no hook to say so: ended.
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	if slices.Contains(info.Processes, cmpOr(r.Kind, s.Kind)) || pid != 0 && findAgent(pid) == cmpOr(r.Kind, s.Kind) {
		d.sched.sawProc[r.Id] = true
	} else if d.sched.sawProc[r.Id] {
		delete(d.sched.sawProc, r.Id)
		if d.sched.sawWork[r.Id] {
			return d.endRun(s, r, pb.ScheduleRun_STATUS_READY, cmpOr(a.Summary, "The agent exited"))
		}
		return d.endRun(s, r, pb.ScheduleRun_STATUS_FAILED, "The agent exited before it started")
	}
	switch a.State {
	case pb.AgentState_AGENT_STATE_WORKING:
		d.sched.sawWork[r.Id] = true
		if r.Status != pb.ScheduleRun_STATUS_RUNNING {
			r.Status, r.WaitingSinceUnixMs = pb.ScheduleRun_STATUS_RUNNING, 0
			return true
		}
	case pb.AgentState_AGENT_STATE_NEEDS_INPUT:
		d.sched.sawWork[r.Id] = true
		if r.Status != pb.ScheduleRun_STATUS_WAITING {
			r.Status, r.WaitingSinceUnixMs = pb.ScheduleRun_STATUS_WAITING, now.UnixMilli()
			return true
		}
		giveUp := time.Duration(cmpOrInt(s.GiveUpMinutes, defaultGiveUp)) * time.Minute
		if now.Sub(time.UnixMilli(r.WaitingSinceUnixMs)) >= giveUp {
			d.stopRun(s, r, pb.ScheduleRun_STATUS_GAVE_UP, "Gave up after waiting "+humanMinutes(giveUp)+" for you")
			d.audit("schedule:"+s.Id, "schedule-gave-up", r.TabId, s.Name)
			return true
		}
	case pb.AgentState_AGENT_STATE_IDLE:
		if !d.sched.sawWork[r.Id] || a.Subagents > 0 || a.RunningCommand {
			return false
		}
		return d.endRun(s, r, pb.ScheduleRun_STATUS_READY, a.Summary)
	case pb.AgentState_AGENT_STATE_ERRORED:
		return d.endRun(s, r, pb.ScheduleRun_STATUS_FAILED, cmpOr(a.Summary, "The agent hit an error"))
	case pb.AgentState_AGENT_STATE_EXITED:
		if d.sched.sawWork[r.Id] {
			return d.endRun(s, r, pb.ScheduleRun_STATUS_READY, a.Summary)
		}
		return d.endRun(s, r, pb.ScheduleRun_STATUS_FAILED, "The agent exited before it started")
	}
	return false
}

// endRun finishes a run and tells you, as the schedule says. Callers hold d.mu.
func (d *Daemon) endRun(s *pb.Schedule, r *pb.ScheduleRun, status pb.ScheduleRun_Status, note string) bool {
	r.Status, r.Note, r.EndedUnixMs, r.WaitingSinceUnixMs = status, firstLine(note), schedNow().UnixMilli(), 0
	delete(d.sched.sawWork, r.Id)
	delete(d.sched.sawProc, r.Id)
	d.notifyRun(s, r)
	d.closeOldRuns(s)
	return true
}

// scheduleTurnDone notes that a run's agent finished a turn (so idle now
// means done, even if it was never seen working). Callers hold d.mu.
func (d *Daemon) scheduleTurnDone(p *pane) {
	info := p.snapshotInfo()
	if s := d.sched.list[info.ScheduleId]; s != nil {
		for _, r := range s.Runs {
			if r.Id == info.ScheduleRunId && runActive(r) {
				d.sched.sawWork[r.Id] = true // (not for one ended: nothing would clear it)
			}
		}
	}
}

// scheduleAgentChanged follows a run's agent as soon as its state changes
// (not at the next tick). Callers hold d.mu.
func (d *Daemon) scheduleAgentChanged(p *pane) {
	info := p.snapshotInfo()
	s := d.sched.list[info.ScheduleId]
	if s == nil {
		return
	}
	for _, r := range s.Runs {
		if r.Id == info.ScheduleRunId && runActive(r) {
			if d.followRun(s, r, schedNow()) {
				d.saveSchedule(s)
			}
			return
		}
	}
}

// scheduleCommandExited records how a command run went, and says whether
// its pane stays open. Callers hold d.mu.
func (d *Daemon) scheduleCommandExited(p *pane, status int32) (keepOpen bool) {
	info := p.snapshotInfo()
	s := d.sched.list[info.ScheduleId]
	if s == nil {
		return false
	}
	for _, r := range s.Runs {
		if r.Id != info.ScheduleRunId {
			continue
		}
		if !commandRun(s, r) {
			return false
		}
		r.ExitCode = status
		if runActive(r) {
			if status == 0 {
				d.endRun(s, r, pb.ScheduleRun_STATUS_READY, "")
			} else {
				d.endRun(s, r, pb.ScheduleRun_STATUS_FAILED, fmt.Sprintf("Exited with status %d", status))
			}
		}
		d.saveSchedule(s)
		return status != 0 || !s.CloseOnSuccess
	}
	return false
}

// keepRun marks a run you've typed into: its tab stays when newer runs
// come. Callers hold d.mu.
func (d *Daemon) keepRun(p *pane) {
	tab := p.tabID()
	for _, s := range d.sched.list {
		for _, r := range s.Runs {
			if r.TabId == tab && !r.TabClosed {
				if !r.Kept {
					r.Kept = true
					d.saveSchedule(s)
				}
				return
			}
		}
	}
}

// runOwnsTab: the run's tab holds only the run's pane and panes it opened
// (hmk show), and is still in the schedule's Group: closing it closes
// nothing of yours. Callers hold d.mu.
func (d *Daemon) runOwnsTab(s *pb.Schedule, r *pb.ScheduleRun) bool {
	t := d.tabs[r.TabId]
	if t == nil || t.GroupId != s.GroupId {
		return false
	}
	for _, id := range tabPanes(t) {
		if id == r.PaneId {
			continue
		}
		if p := d.panes[id]; p == nil || p.snapshotInfo().CreatedByPaneId != r.PaneId {
			return false
		}
	}
	return true
}

// closeOldRuns closes run tabs beyond keep_runs (oldest first), leaving
// runs under way and ones you've typed into. Callers hold d.mu.
func (d *Daemon) closeOldRuns(s *pb.Schedule) {
	keep := int(cmpOrInt(s.KeepRuns, defaultKeepRuns))
	n := 0
	for _, r := range s.Runs {
		if r.TabId == "" || r.TabClosed || r.Kept {
			continue
		}
		if d.tabs[r.TabId] == nil {
			r.TabClosed = true
			continue
		}
		n++
		if n > keep && !runActive(r) && d.runOwnsTab(s, r) {
			_ = d.closeTab(r.TabId)
			r.TabClosed = true
		}
	}
}

// refreshScheduleGroup keeps the Group's name and emoji, to bring it back
// with. Callers hold d.mu.
func (d *Daemon) refreshScheduleGroup(s *pb.Schedule) bool {
	g := d.groups[s.GroupId]
	if g == nil || g.Name == s.GroupName && g.Icon == s.GroupIcon {
		return false
	}
	s.GroupName, s.GroupIcon = g.Name, g.Icon
	return true
}

// notifyRun tells away phones how a run went, as the schedule says (the
// Mac and iPhone apps notify from the run's status themselves).
// Callers hold d.mu.
func (d *Daemon) notifyRun(s *pb.Schedule, r *pb.ScheduleRun) {
	ready := r.Status == pb.ScheduleRun_STATUS_READY
	problem := r.Status == pb.ScheduleRun_STATUS_FAILED || r.Status == pb.ScheduleRun_STATUS_GAVE_UP
	if !ready && !problem || !scheduleNotifies(s, ready) {
		return
	}
	body := r.Note
	switch r.Status {
	case pb.ScheduleRun_STATUS_READY:
		body = cmpOr(body, "Ready")
	case pb.ScheduleRun_STATUS_FAILED:
		body = "Failed: " + cmpOr(body, "see its tab")
	case pb.ScheduleRun_STATUS_GAVE_UP:
		body = cmpOr(body, "Gave up waiting for you")
	}
	sub := ""
	if g := d.groups[s.GroupId]; g != nil {
		sub = strings.TrimSpace(g.Icon + " " + groupLabel(g))
	}
	event := "finished"
	if problem {
		event = "errored"
	}
	d.push(event, r.Id, pushNote{Title: cmpOr(s.Name, "Scheduled task"), Subtitle: sub, Body: body, Pane: r.PaneId}, nil)
}

// scheduleNotifies: s wants a notification for a run that's ready (or,
// with ready false, one that went wrong).
func scheduleNotifies(s *pb.Schedule, ready bool) bool {
	switch s.Notify {
	case pb.Schedule_NOTIFY_READY:
		return true
	case pb.Schedule_NOTIFY_PROBLEMS:
		return !ready
	case pb.Schedule_NOTIFY_NEVER:
		return false
	}
	return !ready || s.Kind != "command" // by kind: agents when ready, commands only about problems
}

// ---------- changing them ----------

// saveSchedule persists s and tells clients. Callers hold d.mu.
func (d *Daemon) saveSchedule(s *pb.Schedule) {
	d.persist(d.store.PutRecord("schedule", s.Id, s))
	d.publish(&pb.StateUpdate{Schedules: []*pb.Schedule{proto.CloneOf(s)}})
}

// cleanSchedule checks a schedule as sent and fills in what's left out.
// Callers hold d.mu.
func (d *Daemon) cleanSchedule(in *pb.Schedule) (*pb.Schedule, error) {
	s := proto.CloneOf(in)
	s.Kind = strings.ToLower(strings.TrimSpace(s.Kind))
	if s.Kind == "" {
		s.Kind = "claude"
	}
	if a := agents.Get(s.Kind); s.Kind != "command" && (a == nil || !a.Schedulable) {
		return nil, fmt.Errorf("a schedule runs an agent (%s) or a command, not %q", strings.Join(schedulableKinds(), ", "), s.Kind)
	}
	s.Prompt = strings.TrimSpace(s.Prompt)
	// An agent's prompt is typed into the tab's shell: a control character
	// there (^C, ^U, Esc, a tab) would act on the line, not be part of it.
	if s.Kind != "command" && strings.ContainsFunc(s.Prompt, func(r rune) bool { return r != '\n' && (r < 0x20 || r == 0x7f) }) {
		return nil, errors.New("the prompt can't contain control characters")
	}
	if strings.ContainsFunc(s.Prompt+s.Name, bidiControl) {
		return nil, errors.New("the prompt and name can't contain characters that reorder text")
	}
	if strings.ContainsRune(s.Prompt, 0) {
		return nil, errors.New("the command can't contain a NUL")
	}
	if s.Prompt == "" {
		if s.Kind == "command" {
			return nil, errors.New("give the command to run")
		}
		return nil, errors.New("give the agent a prompt")
	}
	cwd := filepath.Clean(expandHome(strings.TrimSpace(s.Cwd)))
	if !filepath.IsAbs(cwd) {
		return nil, errors.New("the folder must be an absolute path")
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("there's no folder %s", cwd)
	}
	s.Cwd = cwd
	s.Name = oneLine(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s.Name), 80)
	if s.Name == "" {
		s.Name = oneLine(s.Prompt, 40)
	}
	s.Cron = strings.TrimSpace(s.Cron)
	if s.Cron != "" {
		if _, err := parseCron(s.Cron); err != nil {
			return nil, err
		}
	}
	if s.MinuteOfDay < 0 || s.MinuteOfDay >= 24*60 {
		return nil, errors.New("the time of day must be between 00:00 and 23:59")
	}
	days := map[int32]bool{}
	for _, w := range s.Weekdays {
		if w < 0 || w > 6 {
			return nil, fmt.Errorf("weekday %d: 0 is Sunday, 6 Saturday", w)
		}
		days[w] = true
	}
	s.Weekdays = nil
	for w := int32(0); w < 7; w++ {
		if days[w] {
			s.Weekdays = append(s.Weekdays, w)
		}
	}
	if len(s.Weekdays) == 7 {
		s.Weekdays = nil
	}
	if s.KeepRuns < 0 || s.GiveUpMinutes < 0 {
		return nil, errors.New("keep_runs and give_up_minutes can't be negative")
	}
	switch s.CodexSandbox {
	case "", "read-only", "workspace-write", "danger-full-access":
	default:
		return nil, fmt.Errorf("codex sandbox %q: read-only, workspace-write or danger-full-access", s.CodexSandbox)
	}
	switch s.CodexApproval {
	case "", "untrusted", "on-failure", "on-request", "never":
	default:
		return nil, fmt.Errorf("codex approval %q: untrusted, on-failure, on-request or never", s.CodexApproval)
	}
	var tools []string
	for _, t := range s.AllowedTools {
		if t = strings.TrimSpace(t); t != "" && !slices.Contains(tools, t) {
			tools = append(tools, t)
		}
	}
	s.AllowedTools = tools
	// The Group: the one named, else the one working in that folder, else
	// a new one for it (named after the folder).
	if s.GroupId != "" && d.groups[s.GroupId] == nil {
		// Its own Group, closed since: kept (the next run brings it back).
		old := d.sched.list[s.Id]
		if old == nil || s.Id == "" || old.GroupId != s.GroupId {
			return nil, fmt.Errorf("no such group %q", s.GroupId)
		}
		s.GroupName, s.GroupIcon = old.GroupName, old.GroupIcon
		return s, nil
	}
	if s.GroupId == "" {
		s.GroupId = d.groupUsingDir(cwd)
	}
	if g := d.groups[s.GroupId]; g != nil {
		s.GroupName, s.GroupIcon = g.Name, g.Icon
	} else if s.GroupName == "" {
		s.GroupName = client.DirTitle(cwd)
	}
	return s, nil
}

// putSchedule creates (no id) or replaces a schedule. who: who made it, in
// words. Callers hold d.mu.
func (d *Daemon) putSchedule(in *pb.Schedule, who string) (*pb.Schedule, error) {
	s, err := d.cleanSchedule(in)
	if err != nil {
		return nil, err
	}
	old := d.sched.list[s.Id]
	if s.Id == "" {
		s.Id = newID("sc")
		s.CreatedUnixMs = schedNow().UnixMilli()
		s.CreatedBy = who
	} else if old == nil {
		return nil, fmt.Errorf("no such schedule %q", s.Id)
	} else {
		// The daemon's own fields stay as they were.
		s.Runs, s.CreatedUnixMs, s.CreatedBy, s.SkipUntilUnixMs = old.Runs, old.CreatedUnixMs, old.CreatedBy, old.SkipUntilUnixMs
	}
	s.ComputerId, s.Error = d.id.me.UserId, ""
	d.scheduleNext(s, schedNow())
	d.sched.list[s.Id] = s
	d.saveSchedule(s)
	action := "schedule-create"
	if old != nil {
		action = "schedule-edit"
	}
	d.audit("schedule:"+s.Id, action, s.Id, scheduleSummary(s))
	return s, nil
}

// schedulableKinds are the agents a schedule can run.
func schedulableKinds() []string {
	var out []string
	for _, a := range agents.All {
		if a.Schedulable {
			out = append(out, a.ID)
		}
	}
	return out
}

// scheduleSummary describes a schedule in words, for grants and the audit
// log: "Morning briefing: Claude Code, weekdays at 08:30, in ~/Notes: /morning-briefing".
func scheduleSummary(s *pb.Schedule) string {
	what := "the command"
	if a := agents.Get(s.Kind); a != nil {
		what = a.Name
	}
	home, _ := os.UserHomeDir()
	dir := s.Cwd
	if home != "" && strings.HasPrefix(dir, home) {
		dir = "~" + strings.TrimPrefix(dir, home)
	}
	// What it may do first (a card shows a few lines), then the prompt.
	out := fmt.Sprintf("“%s”: %s, %s, in %s", s.Name, what, scheduleWhen(s), dir)
	if s.Kind == "codex" {
		out += fmt.Sprintf(" (sandbox: %s, approval: %s)", cmpOr(s.CodexSandbox, "your Codex setting"), cmpOr(s.CodexApproval, "your Codex setting"))
	}
	if len(s.AllowedTools) > 0 {
		out += fmt.Sprintf(" (allowed: %s)", strings.Join(s.AllowedTools, ", "))
	}
	out += ": " + shownText(s.Prompt) // whole: every client shows all of a request
	return out
}

// scheduleWhen says when a schedule runs, in words.
func scheduleWhen(s *pb.Schedule) string {
	if s.Cron != "" {
		return "cron “" + s.Cron + "”"
	}
	at := fmt.Sprintf("%02d:%02d", s.MinuteOfDay/60, s.MinuteOfDay%60)
	names := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	switch {
	case len(s.Weekdays) == 0:
		return "every day at " + at
	case slices.Equal(s.Weekdays, []int32{1, 2, 3, 4, 5}):
		return "weekdays at " + at
	case slices.Equal(s.Weekdays, []int32{0, 6}):
		return "weekends at " + at
	}
	var ds []string
	for _, w := range s.Weekdays {
		ds = append(ds, names[w])
	}
	return strings.Join(ds, ", ") + " at " + at
}

func (d *Daemon) deleteSchedule(id string) error {
	s := d.sched.list[id]
	if s == nil {
		return fmt.Errorf("no such schedule %q", id)
	}
	delete(d.sched.list, id)
	if _, ok := d.sched.madeBy[id]; ok {
		delete(d.sched.madeBy, id)
		d.saveScheduleMakers()
	}
	d.persist(d.store.DeleteRecord("schedule", id))
	d.publish(&pb.StateUpdate{DeletedScheduleIds: []string{id}})
	d.audit("schedule:"+id, "schedule-delete", id, scheduleSummary(s))
	return nil
}

// ---------- remembering tools (Always Allow for This Schedule) ----------

// toolRule is the --allowedTools rule for a tool call, as Claude's own
// "don't ask again" words it: a Bash command by its first word or two, a
// web fetch by its site, anything else by the tool's name.
func toolRule(tool string, input map[string]any) string {
	switch tool {
	case "":
		return ""
	case "Bash":
		cmd, _ := input["command"].(string)
		f := strings.Fields(firstLine(cmd))
		if len(f) == 0 {
			return "Bash"
		}
		prefix := f[0]
		if len(f) > 1 && !strings.HasPrefix(f[1], "-") && !strings.ContainsAny(f[1], "/.'\"$|&;<>") {
			prefix += " " + f[1]
		}
		return "Bash(" + prefix + ":*)"
	case "WebFetch":
		if u, _ := input["url"].(string); u != "" {
			if pu, err := url.Parse(u); err == nil && pu.Hostname() != "" {
				return "WebFetch(domain:" + pu.Hostname() + ")"
			}
		}
	}
	return tool
}

// scheduleToolFor is the rule to remember for the tool p's agent is asking
// to use: the call it made just before its permission prompt came, if
// Claude's own words for the prompt name that tool and, for a command, the
// screen shows it. Callers hold d.mu.
func (d *Daemon) scheduleToolFor(p *pane, screen string) (string, error) {
	info := p.snapshotInfo()
	s := d.sched.list[info.ScheduleId]
	if s == nil {
		return "", errors.New("this pane isn't a scheduled run")
	}
	if s.Kind != "claude" {
		return "", errors.New("only Claude runs remember tools")
	}
	p.mu.Lock()
	rule, asked := p.askedRule, p.askedFor
	p.mu.Unlock()
	if rule == "" || !askedAbout(asked, rule) || !commandShown(screen, rule) {
		return "", errors.New("can't tell which tool it's asking for: allow it once instead")
	}
	return rule, nil
}

// rememberScheduleTool adds rule to p's schedule's allowed tools. Callers hold d.mu.
func (d *Daemon) rememberScheduleTool(p *pane, rule string) {
	s := d.sched.list[p.snapshotInfo().ScheduleId]
	if s == nil || rule == "" || slices.Contains(s.AllowedTools, rule) {
		return
	}
	s.AllowedTools = append(s.AllowedTools, rule)
	d.saveSchedule(s)
	d.audit("user", "schedule-allow", s.Id, s.Name+": "+rule)
}

// ---------- hmk show ----------

// showOutput opens target in a browser pane: into, if one's named;
// otherwise the one shown in for nextTo before, or one in nextTo's tab
// already on that page, or a new one beside nextTo. The file is read (and
// Markdown rendered) off the lock. Callers don't hold d.mu.
func (d *Daemon) showOutput(nextTo, into, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("show what? Give a file or a URL")
	}
	d.mu.Lock()
	_, err := d.showDest(nextTo, into)
	cwd := ""
	if p := d.panes[nextTo]; p != nil {
		cwd = p.cwd()
	}
	d.mu.Unlock()
	if err != nil {
		return "", err
	}
	page, label, err := d.outputURL(target, cwd)
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.placeOutput(nextTo, into, page, label)
}

// showDest checks the panes a show names: into must be a browser pane,
// and without one, nextTo must exist. Callers hold d.mu.
func (d *Daemon) showDest(nextTo, into string) (*pane, error) {
	if into != "" {
		p := d.panes[into]
		if p == nil {
			return nil, fmt.Errorf("no such pane %q", into)
		}
		if !p.isBrowser() || strings.HasPrefix(p.snapshotInfo().Url, DevToolsURL) {
			return nil, fmt.Errorf("%s isn't a browser pane showing a page (hmk show --split PANE opens one beside it)", into)
		}
		return p, nil
	}
	if d.panes[nextTo] == nil {
		return nil, fmt.Errorf("no such pane %q", nextTo)
	}
	return nil, nil
}

// placeOutput puts page in the browser pane into, or (with none named) the
// one showPaneFor finds, or a new one beside nextTo. Callers hold d.mu.
func (d *Daemon) placeOutput(nextTo, into, page, label string) (string, error) {
	dest, err := d.showDest(nextTo, into)
	if err != nil {
		return "", err
	}
	if dest == nil {
		dest = d.showPaneFor(d.panes[nextTo], page)
	}
	id := ""
	if dest != nil {
		id = dest.id
		d.browsers.navigate(id, "load", page)
		// Its address now (the page confirms it as it loads), so a show
		// looking for a pane already on a page doesn't find this one's last.
		dest.mu.Lock()
		dest.info.Url = page
		dest.mu.Unlock()
		d.paneChangedIfLive(dest)
		if into == "" {
			d.markShown(dest, nextTo)
		}
	} else {
		np, err := d.splitPane(nextTo, pb.Split_DIRECTION_HORIZONTAL, &pb.PaneSpec{Url: page, CreatedByPaneId: nextTo}, false)
		if err != nil {
			return "", err
		}
		id = np.id
		d.markShown(np, nextTo)
	}
	// A scheduled run: what it showed is its output.
	if p := d.panes[nextTo]; p != nil {
		info := p.snapshotInfo()
		if s := d.sched.list[info.ScheduleId]; s != nil {
			for _, r := range s.Runs {
				if r.Id == info.ScheduleRunId {
					r.Output = label
					d.saveSchedule(s)
					break
				}
			}
		}
	}
	return id, nil
}

// showPaneFor is the browser pane in p's tab to show page in for p: the
// one shown in for p before (kept with that pane, so it outlasts p's agent
// and the daemon restarting), else one already on page (the first, by id);
// nil if there's neither. Callers hold d.mu.
func (d *Daemon) showPaneFor(p *pane, page string) *pane {
	tab, want := p.tabID(), pageKey(page)
	var same *pane
	for _, q := range d.panes {
		if q == p || !q.isBrowser() || q.tabID() != tab {
			continue
		}
		info := q.snapshotInfo()
		if info.ShownForPaneId == p.id {
			return q
		}
		if pageKey(info.Url) == want && (same == nil || q.id < same.id) {
			same = q
		}
	}
	return same
}

// markShown records that q shows things for pane id, unless it already
// shows them for another pane that's still open. Callers hold d.mu.
func (d *Daemon) markShown(q *pane, id string) {
	q.mu.Lock()
	was := q.info.ShownForPaneId
	if was == id || (was != "" && d.panes[was] != nil) {
		q.mu.Unlock()
		return
	}
	q.info.ShownForPaneId = id
	q.mu.Unlock()
	d.paneChangedIfLive(q)
}

// pageKey is a URL as compared to find a pane already on that page: the
// path decoded (%20 and a space are the same), scheme and host in lower
// case, no fragment.
func pageKey(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil {
		return strings.TrimSpace(s)
	}
	scheme, host, path := strings.ToLower(u.Scheme), strings.ToLower(u.Host), u.Path
	if scheme == "file" {
		if host == "localhost" {
			host = ""
		}
		path = filepath.Clean(path)
	} else if path == "" {
		path = "/"
	}
	k := scheme + "://" + host + path
	if u.RawQuery != "" {
		k += "?" + u.RawQuery
	}
	return k
}

// outputURL is the page for a file or URL: Markdown is rendered (to a page
// kept with the daemon's data), files open as file:// pages.
func (d *Daemon) outputURL(target, cwd string) (page, label string, err error) {
	if u, err := url.Parse(target); err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "file") {
		if u.Scheme != "file" {
			return target, target, nil
		}
		target = u.Path
	}
	path := expandHome(target)
	if !filepath.IsAbs(path) {
		if cwd == "" {
			return "", "", errors.New("give an absolute path")
		}
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	st, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("can't show %s: %w", path, err)
	}
	if !st.Mode().IsRegular() {
		return "", "", fmt.Errorf("%s isn't a file", path)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		html, err := d.renderMarkdownFile(path)
		if err != nil {
			return "", "", err
		}
		return fileURL(html), path, nil
	}
	return fileURL(path), path, nil
}

func fileURL(path string) string { return (&url.URL{Scheme: "file", Path: path}).String() }

func cmpOrInt(v, def int32) int32 {
	if v > 0 {
		return v
	}
	return def
}

// humanMinutes says a duration in hours or minutes ("4 hours", "90 minutes").
func humanMinutes(t time.Duration) string {
	m := int(t.Minutes())
	switch {
	case m%60 == 0 && m == 60:
		return "an hour"
	case m%60 == 0:
		return fmt.Sprintf("%d hours", m/60)
	case m == 1:
		return "a minute"
	}
	return fmt.Sprintf("%d minutes", m)
}

// scheduleRunActive: p is a scheduled run still under way. Callers hold d.mu.
func (d *Daemon) scheduleRunActive(p *pane) bool {
	info := p.snapshotInfo()
	if s := d.sched.list[info.ScheduleId]; s != nil {
		for _, r := range s.Runs {
			if r.Id == info.ScheduleRunId {
				return runActive(r)
			}
		}
	}
	return false
}

// commandShown: a Bash rule's command words are on screen (the prompt
// shows the command; a subagent's at the same time isn't what it shows).
func commandShown(screen, rule string) bool {
	if screen == "" || !strings.HasPrefix(rule, "Bash(") {
		return true // (no screen in tests; other tools: by name)
	}
	return strings.Contains(screen, strings.TrimSuffix(strings.TrimPrefix(rule, "Bash("), ":*)"))
}

// askedAbout: Claude's permission notification ("Claude needs your
// permission to use Bash") names rule's tool: its name, or an MCP tool's
// own part of it. With none, it can't be told.
func askedAbout(asked, rule string) bool {
	if asked == "" {
		return false
	}
	tool, _, _ := strings.Cut(rule, "(")
	if strings.HasPrefix(tool, "mcp__") {
		tool = tool[strings.LastIndex(tool, "__")+2:]
	}
	for _, w := range strings.FieldsFunc(asked, func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-') }) {
		if w == tool {
			return true
		}
	}
	return false
}

// bidiControl: characters that reorder text as it's shown (so what's shown
// can read differently from what runs).
func bidiControl(r rune) bool { return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069) }

// directionMark: invisible marks that only nudge direction (often pasted
// along with right-to-left text): fine to keep, left out when shown.
func directionMark(r rune) bool { return r == 0x061C || r == 0x200E || r == 0x200F }

// shownText is s as the person is shown it: one line (newlines as ⏎), no
// control or reordering characters.
func shownText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == 0x2028 || r == 0x2029:
			return '⏎'
		case r < 0x20 || (r >= 0x7f && r <= 0x9f) || bidiControl(r) || directionMark(r):
			return -1
		}
		return r
	}, s)
}

// saveScheduleMakers keeps sched.madeBy. Callers hold d.mu.
func (d *Daemon) saveScheduleMakers() {
	b, _ := json.Marshal(d.sched.madeBy)
	d.persist(d.store.SetSetting("schedule_makers", string(b)))
}
