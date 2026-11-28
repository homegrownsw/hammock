package daemon

// Scheduled tasks' messages (schedules.go): saving, deleting, running,
// pausing and skipping schedules, and hmk show. A person's are done at
// once; an agent's saves and deletes ask you first (a grant showing the
// schedule in Hammock's words), and run when you allow them.

import (
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// maxAskedPrompt is the longest prompt (or command) an agent may schedule.
// Every client shows all of a request (the Mac's and iPhone's cards in
// full, the TUI's menu scrolling), so this is only a sanity limit: one
// longer belongs in a script or skill the schedule runs.
const maxAskedPrompt = 4000

func scheduleReply(s *pb.Schedule) *pb.ServerMessage {
	return &pb.ServerMessage{Msg: &pb.ServerMessage_ScheduleReply{ScheduleReply: &pb.ScheduleReply{Schedule: proto.CloneOf(s)}}}
}

// handleSchedule handles the schedule messages and ShowOutput. Callers hold d.mu.
func (d *Daemon) handleSchedule(c *conn, msg *pb.ClientMessage) (*pb.ServerMessage, error) {
	agent := c.caller.isAgent()
	find := func(id string) (*pb.Schedule, error) {
		if s := d.sched.list[id]; s != nil {
			return s, nil
		}
		return nil, fmt.Errorf("no such schedule %q", id)
	}
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_SaveSchedule:
		in := m.SaveSchedule.Schedule
		if in == nil {
			return nil, errors.New("no schedule")
		}
		if agent {
			return nil, d.askSchedule(c, msg.Id, in, false, m.SaveSchedule.Description)
		}
		s, err := d.putSchedule(in, "You")
		if err != nil {
			return nil, err
		}
		if _, ok := d.sched.madeBy[s.Id]; ok {
			// Yours now: the agent that made it asks again.
			delete(d.sched.madeBy, s.Id)
			d.saveScheduleMakers()
		}
		return scheduleReply(s), nil

	case *pb.ClientMessage_DeleteSchedule:
		s, err := find(m.DeleteSchedule.ScheduleId)
		if err != nil {
			return nil, err
		}
		if agent {
			return nil, d.askSchedule(c, msg.Id, s, true, m.DeleteSchedule.Description)
		}
		return nil, d.deleteSchedule(s.Id)

	case *pb.ClientMessage_RunSchedule, *pb.ClientMessage_SetSchedulePaused, *pb.ClientMessage_SkipScheduleRun:
		var id, verb string
		var do func(s *pb.Schedule) (*pb.ServerMessage, error)
		switch m := msg.Msg.(type) {
		case *pb.ClientMessage_RunSchedule:
			rs := m.RunSchedule
			early, trigger, data := rs.Early, rs.Trigger, rs.Data
			id, verb = rs.ScheduleId, "run the automation %s now"
			switch {
			case trigger && data != "":
				verb = "trigger the automation %s with: " + shownText(oneLine(data, 300))
			case trigger:
				verb = "trigger the automation %s"
			case early:
				verb = "run the automation %s now, instead of at its next time"
			}
			if agent && (rs.Source != "" || rs.Label != "" || len(rs.Files) > 0) {
				// Only your apps say it's from Shortcuts or Tasker, and with
				// what label and files: none of it is in what you'd approve.
				return nil, errors.New("an agent can't set a trigger's source, label or files")
			}
			if len(data) > maxHookBody && rs.Source == "" { // (Shortcuts': cut)
				return nil, fmt.Errorf("the data is over %d KB", maxHookBody>>10)
			}
			do = func(s *pb.Schedule) (*pb.ServerMessage, error) {
				if trigger {
					return d.triggerRun(s, rs)
				}
				if early {
					return d.runEarly(s)
				}
				r, err := d.startRun(s, time.Time{}, true)
				d.saveSchedule(s)
				if err != nil {
					return nil, err
				}
				return created(r.TabId), nil
			}
		case *pb.ClientMessage_SetSchedulePaused:
			paused := m.SetSchedulePaused.Paused
			id, verb = m.SetSchedulePaused.ScheduleId, map[bool]string{true: "pause", false: "resume"}[paused]+" the automation %s"
			do = func(s *pb.Schedule) (*pb.ServerMessage, error) {
				s.Paused = paused
				d.scheduleNext(s, schedTime())
				d.saveSchedule(s)
				d.audit(c.auditActor(), map[bool]string{true: "schedule-pause", false: "schedule-resume"}[s.Paused], s.Id, s.Name)
				return nil, nil
			}
		case *pb.ClientMessage_SkipScheduleRun:
			skip := m.SkipScheduleRun.Skip
			id, verb = m.SkipScheduleRun.ScheduleId, map[bool]string{true: "skip the next run of", false: "stop skipping the next run of"}[skip]+" the automation %s"
			do = func(s *pb.Schedule) (*pb.ServerMessage, error) {
				if skip {
					if s.NextRunUnixMs == 0 {
						return nil, errors.New("it isn't due to run")
					}
					s.SkipUntilUnixMs = s.NextRunUnixMs + 1
				} else {
					s.SkipUntilUnixMs = 0
				}
				d.saveSchedule(s)
				return nil, nil
			}
		}
		s, err := find(id)
		if err != nil {
			return nil, err
		}
		_, run := msg.Msg.(*pb.ClientMessage_RunSchedule)
		sandboxRun := run && agent && c.caller.sandboxed
		if agent && (!d.agentsSchedule(c, id) || sandboxRun) {
			// Not one it made, or run from a sandbox (its run is outside
			// it: sandbox.go): you say whether it may (as for saving one).
			who := d.agentWho(c.caller.pane)
			if sandboxRun {
				who += sandboxedWho
			}
			headline := who + " wants to " + fmt.Sprintf(verb, d.scheduleSummary(s))
			title := who + " wants to " + fmt.Sprintf(verb, scheduleTitle(s))
			return nil, d.askScheduleGrant(c, msg.Id, headline, title, d.scheduleDetails(s), "", func() (*pb.ServerMessage, error) {
				s, err := find(id)
				if err != nil {
					return nil, err
				}
				return do(s)
			})
		}
		return do(s)

	}
	return nil, errors.New("unknown schedule message")
}

// runEarly is Run Early: s's next run, started now. It's recorded as due
// then, and s next runs at the time after it. Callers hold d.mu.
func (d *Daemon) runEarly(s *pb.Schedule) (*pb.ServerMessage, error) {
	if s.Paused || s.NextRunUnixMs == 0 {
		return nil, errors.New("it isn't due to run: use Run Now")
	}
	if s.SkipUntilUnixMs > s.NextRunUnixMs {
		return nil, errors.New("its next run is skipped: use Run Now")
	}
	due := time.UnixMilli(s.NextRunUnixMs)
	r, err := d.startRun(s, due, true)
	r.Early = true
	d.scheduleNext(s, due)
	d.saveSchedule(s)
	if err != nil {
		return nil, err
	}
	return created(r.TabId), nil
}

// askSchedule asks you whether an agent may save (or delete) a schedule:
// a grant in Needs You, worded by the daemon from the schedule itself, and
// on Allow it's done and the agent told. Callers hold d.mu.
func (d *Daemon) askSchedule(c *conn, replyTo uint64, in *pb.Schedule, del bool, why string) error {
	self := c.caller.pane
	who := d.agentWho(self)
	var headline, title string
	var details []*pb.GrantDetail
	switch {
	case del:
		headline = who + " wants to delete the automation " + d.scheduleSummary(in)
		title = who + " wants to delete the automation " + scheduleTitle(in)
		details = d.scheduleDetails(in)
	default:
		s, err := d.cleanSchedule(in) // a mistake is said now, not after you've allowed it
		if err != nil {
			return err
		}
		// You're shown all of it to allow: so it's kept to what fits.
		if n := len([]rune(s.Prompt)); n > maxAskedPrompt {
			return fmt.Errorf("the prompt is %d characters (at most %d): put the work in a script or skill and schedule that", n, maxAskedPrompt)
		}
		if s.Id != "" && d.sched.list[s.Id] == nil {
			return fmt.Errorf("no such schedule %q", s.Id)
		}
		verb, short := "add the automation", "add the automation"
		if s.Id != "" {
			verb, short = "change the automation to", "change the automation"
		}
		if c.caller.sandboxed {
			who += sandboxedWho
		}
		headline = who + " wants to " + verb + " " + d.scheduleSummary(s)
		title = who + " wants to " + short + " " + scheduleTitle(s)
		details = d.scheduleDetails(s)

	}
	in = proto.CloneOf(in)
	return d.askScheduleGrant(c, replyTo, headline, title, details, why, func() (*pb.ServerMessage, error) {
		if del {
			if err := d.deleteSchedule(in.Id); err != nil {
				return nil, err
			}
			return &pb.ServerMessage{Msg: &pb.ServerMessage_Ok{Ok: &pb.Ok{}}}, nil
		}
		s, err := d.putSchedule(in, cmpOr(d.agentWho(self), "An agent"))
		if err != nil {
			return nil, err
		}
		if in.Id == "" {
			// Its own: it may run, pause and skip it without asking again.
			proc, session := d.agentIdentity(self)
			d.sched.madeBy[s.Id] = schedMaker{Pane: self, Proc: proc, Session: session}
			d.saveScheduleMakers()
		}
		return scheduleReply(s), nil
	})
}

// askScheduleGrant asks you (a grant in Needs You) whether an agent may do
// something to a schedule, headline saying what; on Allow it's done and
// the agent told. Callers hold d.mu.
func (d *Daemon) askScheduleGrant(c *conn, replyTo uint64, headline, title string, details []*pb.GrantDetail, why string, do func() (*pb.ServerMessage, error)) error {
	self := c.caller.pane
	d.openGrant(&pendingGrant{
		req: &pb.GrantRequest{
			Id: newID("gr"), RequesterPaneId: self, TargetPaneId: self, Action: "schedule",
			Headline: headline, Title: title, Details: details, Description: agentNote(why), CreatedUnixMs: time.Now().UnixMilli(),
		},
		done: func(allowed bool) {
			if !allowed {
				c.replyErr(replyTo, errors.New("the person said no (or didn't answer)"))
				return
			}
			r, err := do()
			if err != nil {
				c.replyErr(replyTo, err)
				return
			}
			if r == nil {
				r = &pb.ServerMessage{Msg: &pb.ServerMessage_Ok{Ok: &pb.Ok{}}}
			}
			r.ReplyTo = replyTo
			c.send(r)
		},
	})
	return errDeferred
}

// agentsSchedule reports whether the agent c is (or the agent whose pane
// it runs in) made schedule id: the one in that pane then, not a later
// agent there. Callers hold d.mu.
func (d *Daemon) agentsSchedule(c *conn, id string) bool {
	m, ok := d.sched.madeBy[id]
	if !ok || m.Pane == "" || !d.owners(c.caller.pane)[m.Pane] {
		return false
	}
	proc, session := d.agentIdentity(m.Pane)
	if m.Session != "" && session != "" && m.Session != session {
		return false // another conversation
	}
	return m.Proc != "" && m.Proc == proc
}

// agentIdentity is which agent runs in pane now: its harness's process
// (pid and start time; the pane's own process where there's no harness
// to be found), and its vendor session id when known. Callers hold d.mu.
func (d *Daemon) agentIdentity(pane string) (proc, session string) {
	p := d.panes[pane]
	if p == nil {
		return "", ""
	}
	if a := p.snapshotInfo().Agent; a != nil && a.SessionId != "" {
		session = a.Harness + ":" + a.SessionId
	}
	p.mu.Lock()
	pid := p.pid
	p.mu.Unlock()
	if pid <= 0 {
		return "", session
	}
	if !isAgentProcess(pid) {
		if _, a := findAgentProc(pid); a > 0 {
			pid = a
		}
	}
	t, err := procinfo.StartTime(pid)
	if err != nil {
		return "", session
	}
	return fmt.Sprintf("%d@%d", pid, t.UnixNano()), session
}

// showOutputMsg is hmk show / show_output: in the browser pane named, or
// for (beside) the caller's own pane unless another is named. Callers
// don't hold d.mu (the file is read off it).
func (d *Daemon) showOutputMsg(c *conn, m *pb.ShowOutput) (*pb.ServerMessage, error) {
	next := m.PaneId
	if next == "" && c.caller != nil && c.caller.pane != outsidePane {
		next = c.caller.pane
	}
	if next == "" {
		next = c.fromPane
	}
	if next == "" && m.IntoPaneId == "" {
		return nil, errors.New("show beside which pane? Run it in a Hammock pane, or name one")
	}
	id, err := d.showOutput(next, m.IntoPaneId, m.Target)
	if err != nil {
		return nil, err
	}
	return created(id), nil
}
