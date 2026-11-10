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

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
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
			id, verb = m.RunSchedule.ScheduleId, "run the scheduled task %s now"
			do = func(s *pb.Schedule) (*pb.ServerMessage, error) {
				r, err := d.startRun(s, time.Time{}, true)
				d.saveSchedule(s)
				if err != nil {
					return nil, err
				}
				return created(r.TabId), nil
			}
		case *pb.ClientMessage_SetSchedulePaused:
			paused := m.SetSchedulePaused.Paused
			id, verb = m.SetSchedulePaused.ScheduleId, map[bool]string{true: "pause", false: "resume"}[paused]+" the scheduled task %s"
			do = func(s *pb.Schedule) (*pb.ServerMessage, error) {
				s.Paused = paused
				d.scheduleNext(s, schedNow())
				d.saveSchedule(s)
				d.audit(c.auditActor(), map[bool]string{true: "schedule-pause", false: "schedule-resume"}[s.Paused], s.Id, s.Name)
				return nil, nil
			}
		case *pb.ClientMessage_SkipScheduleRun:
			skip := m.SkipScheduleRun.Skip
			id, verb = m.SkipScheduleRun.ScheduleId, map[bool]string{true: "skip the next run of", false: "stop skipping the next run of"}[skip]+" the scheduled task %s"
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
		if agent && !d.agentsSchedule(c, id) {
			// Not one it made: you say whether it may (as for saving one).
			headline := d.agentWho(c.caller.pane) + " wants to " + fmt.Sprintf(verb, scheduleSummary(s))
			return nil, d.askScheduleGrant(c, msg.Id, headline, "", func() (*pb.ServerMessage, error) {
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

// askSchedule asks you whether an agent may save (or delete) a schedule:
// a grant in Needs You, worded by the daemon from the schedule itself, and
// on Allow it's done and the agent told. Callers hold d.mu.
func (d *Daemon) askSchedule(c *conn, replyTo uint64, in *pb.Schedule, del bool, why string) error {
	self := c.caller.pane
	who := d.agentWho(self)
	var headline string
	switch {
	case del:
		headline = who + " wants to delete the scheduled task " + scheduleSummary(in)
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
		verb := "schedule"
		if s.Id != "" {
			verb = "change the scheduled task to"
		}
		headline = who + " wants to " + verb + " " + scheduleSummary(s)

	}
	in = proto.CloneOf(in)
	return d.askScheduleGrant(c, replyTo, headline, why, func() (*pb.ServerMessage, error) {
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
			d.sched.madeBy[s.Id] = self
			d.saveScheduleMakers()
		}
		return scheduleReply(s), nil
	})
}

// askScheduleGrant asks you (a grant in Needs You) whether an agent may do
// something to a schedule, headline saying what; on Allow it's done and
// the agent told. Callers hold d.mu.
func (d *Daemon) askScheduleGrant(c *conn, replyTo uint64, headline, why string, do func() (*pb.ServerMessage, error)) error {
	self := c.caller.pane
	d.openGrant(&pendingGrant{
		req: &pb.GrantRequest{
			Id: newID("gr"), RequesterPaneId: self, TargetPaneId: self, Action: "schedule",
			Headline: headline, Description: agentNote(why), CreatedUnixMs: time.Now().UnixMilli(),
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
// it runs in) made schedule id. Callers hold d.mu.
func (d *Daemon) agentsSchedule(c *conn, id string) bool {
	maker := d.sched.madeBy[id]
	return maker != "" && d.owners(c.caller.pane)[maker]
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
