package daemon

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/procinfo"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/vt"
	"google.golang.org/protobuf/proto"
)

// The Program Status Protocol (OSC 7501): a program in a pane says what
// it's doing (idle, working, done, blocked on you, failed) and why. The
// pane's mirror (libghostty-vt) checks each report and answers a program
// that asks whether it's supported; the records are kept here, per pane
// (vt.StatusRecords), and the one that most needs you becomes the pane's
// agent state, so every indicator, Needs You and notifications follow it
// as they follow agents:
//
//   - blocked is waiting for you (kind question: it asks), error has
//     failed, done has finished (until you've looked), working works, idle
//     rests;
//   - any program may report, not only agents: one that isn't an agent
//     Hammock knows (brew, terraform, a script) gets an Agent with no
//     harness, going by its title or app, and its Needs You items say
//     program (they have their own notification settings);
//   - hooks win: an agent whose hooks reach Hammock is told by them, its
//     reports only kept (Agent.records); reports win over reading the
//     screen, which stops while they last;
//   - there's no heartbeat: working and blocked records (and idle ones) go
//     when the program ends or the shell is back at its prompt (OSC 133 A,
//     or the shell in front again, polled), done and error once you've
//     seen them and then the program's gone.

// statusEvent is a report, or a new prompt, from a mirror's Write.
type statusEvent struct {
	report vt.StatusReport
	prompt bool
}

// maxStatusQueue bounds the events kept from one Write (a flood keeps the
// latest).
const maxStatusQueue = 256

// statusGap is the least time between Needs You items (and so
// notifications) raised from one pane's reports: a program can change state
// as fast as it can write. Later changes wait out the gap, the latest wins.
var statusGap = 3 * time.Second

// watchStatus has the pane's mirror pass on reports and prompts. They come
// during Write, under p.mu, and are applied after it. Callers hold p.mu.
func (p *pane) watchStatusLocked() {
	p.mirror.SetStatusHandler(
		func(r vt.StatusReport) { p.queueStatusLocked(statusEvent{report: r}) },
		func() { p.queueStatusLocked(statusEvent{prompt: true}) },
	)
}

func (p *pane) queueStatusLocked(e statusEvent) {
	if len(p.statusQueue) >= maxStatusQueue {
		p.statusQueue = p.statusQueue[1:]
	}
	p.statusQueue = append(p.statusQueue, e)
}

// takeStatusLocked takes the events queued by the last Write. Callers hold p.mu.
func (p *pane) takeStatusLocked() []statusEvent {
	q := p.statusQueue
	p.statusQueue = nil
	return q
}

// applyStatus applies a Write's reports and prompts. Callers hold d.mu.
func (d *Daemon) applyStatus(p *pane, events []statusEvent) {
	p.mu.Lock()
	changed := false
	for _, e := range events {
		if e.prompt {
			changed = p.status.Ended() || changed
			continue
		}
		// Something reported: a program, not the shell, is in front.
		p.shellFront = false
		changed = p.status.Apply(e.report) || changed
	}
	p.mu.Unlock()
	if changed {
		d.syncStatus(p)
	}
}

// statusEnded drops what a program's end, or the shell at its prompt
// again, ends. Callers hold d.mu.
func (d *Daemon) statusEnded(p *pane) {
	p.mu.Lock()
	changed := p.status.Ended()
	p.mu.Unlock()
	if changed {
		d.syncStatus(p)
	}
}

// statusSeen: you've looked at the pane, so what it finished or failed at
// has been seen; with its program gone (the shell's at its prompt), it
// goes. Reports whether it changed anything (and published it). Callers
// hold d.mu.
func (d *Daemon) statusSeen(p *pane) bool {
	p.mu.Lock()
	changed := p.status.Seen()
	if changed && p.shellFront {
		p.status.Ended()
	}
	p.mu.Unlock()
	if changed {
		d.syncStatus(p)
	}
	return changed
}

// pollStatus, every few seconds: the shell back in front ends what was
// reported, as a new prompt (OSC 133 A) does for shells that say so.
// Callers hold d.mu.
func (d *Daemon) pollStatus(p *pane) {
	p.mu.Lock()
	n, pid, was := p.status.Len(), p.pid, p.shellFront
	p.mu.Unlock()
	if n == 0 || pid == 0 {
		return
	}
	front := shellInFront(pid)
	p.mu.Lock()
	p.shellFront = front
	p.mu.Unlock()
	if front && !was {
		d.statusEnded(p)
	}
}

// shellInFront reports whether pid is a shell, at its prompt: nothing else
// in its terminal's foreground. A pane whose own command reports (hmk tab
// new -- brew upgrade) never is: its records go when it ends.
func shellInFront(pid int) bool {
	name, err := procinfo.Name(pid)
	if err != nil || !shells[strings.TrimPrefix(filepath.Base(name), "-")] {
		return false
	}
	pgid, tpgid, err := procinfo.Groups(pid)
	return err == nil && (tpgid == 0 || tpgid == pgid)
}

// reportsOwn: the pane's agent state is the program's reports to set, not
// its hooks'. Callers hold p.mu.
func reportsOwn(a *pb.Agent) bool {
	return a == nil || a.Heuristic || a.Reported || a.State == pb.AgentState_AGENT_STATE_EXITED
}

// reportedHarness is the agent a program that reports is, by its app
// ("claude-code" is Claude), or "".
func reportedHarness(app string) string {
	app = strings.ToLower(app)
	if alias, ok := programAliases[app]; ok {
		app = alias
	}
	if a := agents.Get(app); a != nil {
		return a.ID
	}
	if a := agents.ByProcess(app); a != nil {
		return a.ID
	}
	return ""
}

// statusAgentState is the agent state a record stands for, whether it's
// already been seen, and what to say about it.
func statusAgentState(r vt.StatusRecord, watched bool) (pb.AgentState, bool, string) {
	say := r.Message
	switch r.State {
	case vt.StatusWorking:
		return pb.AgentState_AGENT_STATE_WORKING, false, labelled(r, say)
	case vt.StatusBlocked:
		if say == "" {
			switch r.Kind {
			case "permission":
				say = "Waiting for your approval"
			case "question":
				say = "Has a question for you"
			case "auth":
				say = "Needs you to sign in"
			default:
				say = "Waiting for you"
			}
		}
		return pb.AgentState_AGENT_STATE_NEEDS_INPUT, false, labelled(r, say)
	case vt.StatusError:
		if !r.Seen {
			return pb.AgentState_AGENT_STATE_ERRORED, false, labelled(r, cmpOr(say, "Failed"))
		}
	case vt.StatusDone:
		return pb.AgentState_AGENT_STATE_IDLE, r.Seen || watched, labelled(r, cmpOr(say, "Finished"))
	}
	return pb.AgentState_AGENT_STATE_IDLE, true, labelled(r, say)
}

// labelled puts a record's title before what it says, for records other
// than the root (the pane says who the root is).
func labelled(r vt.StatusRecord, say string) string {
	if r.ID == "" || r.Title == "" {
		return firstLine(say)
	}
	if say == "" {
		return firstLine(r.Title)
	}
	return firstLine(r.Title + ": " + say)
}

func statusProto(recs []vt.StatusRecord) []*pb.ProgramStatus {
	out := make([]*pb.ProgramStatus, 0, len(recs))
	for _, r := range recs {
		s := &pb.ProgramStatus{Id: r.ID, State: r.State.String(), Kind: r.Kind, App: r.App, Title: r.Title, Message: r.Message, Seen: r.Seen}
		if r.Progress >= 0 {
			s.Progress = proto.Int32(int32(r.Progress))
		}
		out = append(out, s)
	}
	return out
}

// restoreStatus puts back the records a pane had when the daemon stopped
// (kept with its agent). Callers hold p.mu, or own p.
func (p *pane) restoreStatus() {
	var recs []vt.StatusRecord
	for _, s := range p.info.GetAgent().GetRecords() {
		st, ok := vt.ParseStatusState(s.State)
		if !ok || st == vt.StatusClear {
			continue
		}
		r := vt.StatusRecord{StatusReport: vt.StatusReport{State: st, Kind: s.Kind, Progress: -1, ID: s.Id, App: s.App, Title: s.Title, Message: s.Message}, Seen: s.Seen}
		if s.Progress != nil {
			r.Progress = int(*s.Progress)
		}
		recs = append(recs, r)
	}
	p.status.Restore(recs)
}

// syncStatus makes the pane's agent what its records say, raising or
// clearing its Needs You item, and publishes it. Callers hold d.mu.
func (d *Daemon) syncStatus(p *pane) {
	watched := d.watched(p.id)
	u := &pb.StateUpdate{}
	p.mu.Lock()
	a := p.info.Agent
	recs := p.status.Records()
	top, any := p.status.Top()
	switch {
	case !reportsOwn(a):
		// Its hooks tell: its reports are only kept.
		a.Records = statusProto(recs)
		p.mu.Unlock()
		d.publishPane(p, u)
		return
	case !any:
		if a == nil || !a.Reported {
			p.mu.Unlock()
			return
		}
		if a.Harness != "" {
			// An agent that stopped reporting: its screen tells again.
			a.Reported, a.Heuristic, a.Progress, a.Records, a.App, a.Title = false, true, nil, nil, "", ""
		} else {
			p.info.Agent = nil
		}
		p.mu.Unlock()
		d.clearAttentionForPane(p.id, u)
		d.publishPane(p, u)
		return
	}
	if top.State == vt.StatusDone && !top.Seen && watched {
		p.status.Seen() // finished in front of you
		top.Seen = true
	}
	state, seen, summary := statusAgentState(top, watched)
	if a == nil {
		a = &pb.Agent{}
		p.info.Agent = a
	}
	root, _ := p.status.Root()
	app := cmpOr(root.App, top.App)
	if h := reportedHarness(app); h != "" {
		a.Harness = h
	} else if !a.Reported && !a.Heuristic {
		a.Harness = "" // what ran here before (an agent that's exited) isn't this
	}
	a.Reported, a.Heuristic = true, false
	a.App, a.Title, a.Records = app, root.Title, statusProto(recs)
	a.Progress = nil
	if top.Progress >= 0 {
		a.Progress = proto.Int32(int32(top.Progress))
	}
	prev, prevSeen, prevSummary := a.State, a.Seen, a.Summary
	if a.State != state || a.Seen != seen {
		a.State, a.StateSinceUnixMs, a.Seen = state, time.Now().UnixMilli(), seen
	}
	a.Summary = summary
	news := prev != state || prevSeen != seen || prevSummary != summary
	p.mu.Unlock()

	switch {
	case wantsAttention(state, seen):
		if news {
			d.statusAttention(p, u)
		}
	default:
		d.clearAttentionForPane(p.id, u)
	}
	d.publishPane(p, u)
	d.scheduleAgentChanged(p)
}

// wantsAttention: an agent in state (seen) has something for you.
func wantsAttention(state pb.AgentState, seen bool) bool {
	return state == pb.AgentState_AGENT_STATE_NEEDS_INPUT || state == pb.AgentState_AGENT_STATE_ERRORED ||
		state == pb.AgentState_AGENT_STATE_IDLE && !seen
}

// statusAttention raises the pane's Needs You item for its reported state,
// at most once every statusGap: sooner, it waits for the gap's end, and
// whatever the program says then is raised. Callers hold d.mu.
func (d *Daemon) statusAttention(p *pane, u *pb.StateUpdate) {
	p.mu.Lock()
	wait := statusGap - time.Since(p.statusRaised)
	if wait > 0 {
		if p.statusTimer == nil {
			p.statusTimer = time.AfterFunc(wait, func() { d.statusAttentionLater(p) })
		}
		p.mu.Unlock()
		return
	}
	p.statusRaised = time.Now()
	a := p.info.Agent
	state, summary, program := a.GetState(), a.GetSummary(), a.GetHarness() == ""
	top, _ := p.status.Top()
	p.mu.Unlock()
	d.raiseAttentionAs(p, state, summary, program, top.Kind == "question", u)
}

// statusAttentionLater raises what was held back by the gap, if the pane
// still wants it and doesn't already have it.
func (d *Daemon) statusAttentionLater(p *pane) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p.mu.Lock()
	p.statusTimer = nil
	a := p.info.Agent
	want := a != nil && a.Reported && wantsAttention(a.State, a.Seen)
	state, summary := a.GetState(), a.GetSummary()
	p.mu.Unlock()
	if d.panes[p.id] != p || !want {
		return
	}
	if at := d.attention["at_"+p.id]; at != nil && at.State == state && at.Summary == summary {
		return
	}
	u := &pb.StateUpdate{}
	d.statusAttention(p, u)
	d.publish(u)
}

// publishPane persists the pane and publishes it with u. Callers hold d.mu.
func (d *Daemon) publishPane(p *pane, u *pb.StateUpdate) {
	if d.panes[p.id] != p {
		return
	}
	info := p.snapshotInfo()
	d.persist(d.store.PutPane(info))
	u.Panes = append(u.Panes, info)
	d.publish(u)
}
