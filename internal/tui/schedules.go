package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// ---------- scheduled tasks ----------
//
// The daemon runs agents and commands on a schedule (internal/daemon/
// schedules.go), each run a new tab in the schedule's Group. Here: the
// Scheduled Tasks list (⌃B @), with each one's recent runs, and the dialog
// that makes or edits one. Only the simple fields: the rest (overlap, give
// up, runs kept, tools, Codex's sandbox) stay as they are, or at their
// defaults, and are set in the Mac app or with hmk schedule.

// applySchedules takes a state update's schedules.
func (a *App) applySchedules(u *pb.StateUpdate) {
	if u.Full {
		a.schedules = nil
	}
	if len(u.Schedules) > 0 && a.schedules == nil {
		a.schedules = map[string]*pb.Schedule{}
	}
	for _, s := range u.Schedules {
		a.schedules[s.Id] = s
	}
	for _, id := range u.DeletedScheduleIds {
		delete(a.schedules, id)
	}
}

// scheduleList is the schedules in the order the list shows them: by name.
func (a *App) scheduleList() []*pb.Schedule {
	out := make([]*pb.Schedule, 0, len(a.schedules))
	for _, s := range a.schedules {
		out = append(out, s)
	}
	slices.SortFunc(out, func(x, y *pb.Schedule) int {
		if c := strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)); c != 0 {
			return c
		}
		return strings.Compare(x.Id, y.Id)
	})
	return out
}

// runSchedule is a pane's scheduled task, if it's a run of one.
func (a *App) runSchedule(p *pb.Pane) *pb.Schedule {
	if p == nil || p.ScheduleId == "" {
		return nil
	}
	return a.schedules[p.ScheduleId]
}

// isRunTab: one of the tab's panes is a scheduled run.
func (a *App) isRunTab(t *pb.Tab) bool {
	for _, id := range tabIDs(t) {
		if p := a.panes[id]; p != nil && p.ScheduleId != "" {
			return true
		}
	}
	return false
}

// canSchedule: new schedules can go in this Group (yours, on this computer:
// the daemon makes them here only).
func canSchedule(g *pb.Group) bool { return g != nil && g.HostUserId == "" }

// ---------- in words ----------

var dayNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// scheduleWhen says when a schedule runs: "weekdays at 08:30", "cron 0 * * * *".
func scheduleWhen(s *pb.Schedule) string {
	if s.Cron != "" {
		return "cron " + s.Cron
	}
	at := fmt.Sprintf("%02d:%02d", s.MinuteOfDay/60, s.MinuteOfDay%60)
	return dayWords(s.Weekdays) + " at " + at
}

func dayWords(days []int32) string {
	switch {
	case len(days) == 0 || len(days) == 7:
		return "every day"
	case slices.Equal(days, []int32{1, 2, 3, 4, 5}):
		return "weekdays"
	case slices.Equal(days, []int32{0, 6}):
		return "weekends"
	}
	var ds []string
	for _, w := range days {
		if w >= 0 && w < 7 {
			ds = append(ds, dayNames[w])
		}
	}
	return strings.Join(ds, ",")
}

// whenField is what the dialog's When field shows for s, as parseWhen reads it.
func whenField(s *pb.Schedule) string {
	if s.Cron != "" {
		return "cron " + s.Cron
	}
	return fmt.Sprintf("%s %02d:%02d", dayWords(s.Weekdays), s.MinuteOfDay/60, s.MinuteOfDay%60)
}

// parseWhen reads the When field: "every day 08:30", "weekdays 8:30",
// "weekends at 10:00", "mon,wed,fri 09:00", or a cron line ("cron 0 * * * *",
// or just its five fields).
func parseWhen(v string) (days []int32, minute int32, cron string, err error) {
	v = strings.TrimSpace(v)
	low := strings.ToLower(v)
	if rest, ok := strings.CutPrefix(low, "cron"); ok && (rest == "" || rest[0] == ' ') {
		cron = strings.TrimSpace(v[4:])
		if cron == "" {
			return nil, 0, "", errors.New("give the cron line's five fields, e.g. cron 0 * * * *")
		}
		return nil, 0, cron, nil
	}
	if f := strings.Fields(v); len(f) == 5 && !strings.Contains(v, ":") {
		return nil, 0, v, nil
	}
	set := map[int32]bool{}
	minute = -1
	for _, w := range strings.FieldsFunc(low, func(r rune) bool { return r == ' ' || r == ',' }) {
		switch w {
		case "at", "on", "every", "each", "day", "days", "daily", "everyday", "and":
			continue
		case "weekdays", "weekday":
			for d := int32(1); d <= 5; d++ {
				set[d] = true
			}
			continue
		case "weekends", "weekend":
			set[0], set[6] = true, true
			continue
		}
		if m, ok := parseClock(w); ok {
			minute = m
			continue
		}
		if i := slices.IndexFunc(dayNames, func(n string) bool { return len(w) >= 3 && strings.HasPrefix(w, strings.ToLower(n)) }); i >= 0 {
			set[int32(i)] = true
			continue
		}
		return nil, 0, "", fmt.Errorf("can't read “%s” in When: try “weekdays 08:30” or “cron 0 * * * *”", w)
	}
	if minute < 0 {
		return nil, 0, "", errors.New("say what time it runs, e.g. “weekdays 08:30”")
	}
	for d := int32(0); d < 7; d++ {
		if set[d] {
			days = append(days, d)
		}
	}
	if len(days) == 7 {
		days = nil
	}
	return days, minute, "", nil
}

// parseClock reads "08:30", "8:30" or "8" (whole hours).
func parseClock(w string) (int32, bool) {
	hs, ms, colon := strings.Cut(w, ":")
	h, err := strconv.Atoi(hs)
	if err != nil || h < 0 || h > 23 || len(hs) > 2 {
		return 0, false
	}
	m := 0
	if colon {
		if m, err = strconv.Atoi(ms); err != nil || len(ms) != 2 || m < 0 || m > 59 {
			return 0, false
		}
	}
	return int32(h*60 + m), true
}

func parseKind(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return "claude", nil
	case "command", "cmd", "shell":
		return "command", nil
	}
	if ag := agents.Parse(v); ag != nil && ag.Schedulable {
		return ag.ID, nil
	}
	return "", fmt.Errorf("Kind is %s or command, not “%s”", strings.Join(scheduleKinds(), ", "), strings.TrimSpace(v))
}

// scheduleKinds are the agents a schedule can run.
func scheduleKinds() []string {
	var out []string
	for _, ag := range agents.All {
		if ag.Schedulable {
			out = append(out, ag.ID)
		}
	}
	return out
}

var notifyWords = map[pb.Schedule_Notify]string{
	pb.Schedule_NOTIFY_DEFAULT: "", pb.Schedule_NOTIFY_READY: "ready",
	pb.Schedule_NOTIFY_PROBLEMS: "problems", pb.Schedule_NOTIFY_NEVER: "never",
}

func parseNotify(v string) (pb.Schedule_Notify, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "default":
		return pb.Schedule_NOTIFY_DEFAULT, nil
	case "ready", "always", "done":
		return pb.Schedule_NOTIFY_READY, nil
	case "problems", "problem", "errors", "failures":
		return pb.Schedule_NOTIFY_PROBLEMS, nil
	case "never", "none", "off", "no":
		return pb.Schedule_NOTIFY_NEVER, nil
	}
	return 0, fmt.Errorf("Notify is ready, problems or never, not “%s”", strings.TrimSpace(v))
}

func kindName(k string) string {
	if ag := agents.Get(k); ag != nil {
		return ag.Name
	}
	return "Command"
}

func (a *App) kindGlyph(k string) glyph {
	if k == "command" {
		return glyph{"❯", a.th.dim, false}
	}
	return a.harnessGlyph(k)
}

// nextRunText is when a run is next due, as people say it: "in 12 min",
// "today 17:00", "tomorrow 08:30", "Fri 08:30", "12 Nov 08:30".
func nextRunText(ms int64, now time.Time) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(ms).In(now.Location())
	d := t.Sub(now)
	day := func(t time.Time) time.Time {
		y, m, dd := t.Date()
		return time.Date(y, m, dd, 0, 0, 0, 0, t.Location())
	}
	days := int(math.Round(day(t).Sub(day(now)).Hours() / 24))
	at := t.Format("15:04")
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("in %d min", int(math.Ceil(d.Minutes())))
	case days == 0:
		return "today " + at
	case days == 1:
		return "tomorrow " + at
	case days < 7:
		return t.Format("Mon") + " " + at
	case t.Year() == now.Year():
		return t.Format("2 Jan") + " " + at
	}
	return t.Format("2 Jan 2006") + " " + at
}

// nextText is a schedule's next run, or why there isn't one.
func nextText(s *pb.Schedule, now time.Time) string {
	switch {
	case s.Paused:
		return "paused"
	case s.Error != "":
		return "can't run"
	case s.NextRunUnixMs == 0:
		return "never"
	case s.SkipUntilUnixMs > s.NextRunUnixMs:
		return "skipping " + nextRunText(s.NextRunUnixMs, now)
	}
	return nextRunText(s.NextRunUnixMs, now)
}

// runStatus is a run's status as a glyph and a word or two.
func (a *App) runStatus(r *pb.ScheduleRun) (glyph, string) {
	switch r.Status {
	case pb.ScheduleRun_STATUS_RUNNING:
		return glyph{a.orbit(1), a.th.indigo, false}, "running"
	case pb.ScheduleRun_STATUS_WAITING:
		return glyph{"!", a.th.orange, true}, "needs you"
	case pb.ScheduleRun_STATUS_READY:
		return glyph{"✓", a.th.green, true}, "ready"
	case pb.ScheduleRun_STATUS_FAILED:
		if r.ExitCode != 0 {
			return glyph{"✗", a.th.red, true}, fmt.Sprintf("failed (%d)", r.ExitCode)
		}
		return glyph{"✗", a.th.red, true}, "failed"
	case pb.ScheduleRun_STATUS_GAVE_UP:
		return glyph{"✗", a.th.red, true}, "gave up"
	case pb.ScheduleRun_STATUS_SKIPPED:
		return glyph{"↷", a.th.dim, false}, "skipped"
	case pb.ScheduleRun_STATUS_MISSED:
		return glyph{"–", a.th.dim, false}, "missed"
	case pb.ScheduleRun_STATUS_STOPPED:
		return glyph{"■", a.th.dim, false}, "stopped"
	}
	return glyph{"·", a.th.faint, false}, ""
}

func runTime(r *pb.ScheduleRun) int64 {
	if r.StartedUnixMs != 0 {
		return r.StartedUnixMs
	}
	return r.DueUnixMs
}

// runTab is the run's tab, while it's open.
func (a *App) runTab(r *pb.ScheduleRun) string {
	if r == nil || r.TabClosed || r.TabId == "" || a.tabs[r.TabId] == nil {
		return ""
	}
	return r.TabId
}

// scheduleGroup is where a schedule's runs open, in words, with the
// computer it's on when that's another of yours.
func (a *App) scheduleGroup(s *pb.Schedule) string {
	name := s.GroupName
	if s.GroupIcon != "" {
		name = s.GroupIcon + " " + name
	}
	if g := a.group(s.GroupId); g != nil {
		name = iconTitle(g, a.groupTitle(g))
	}
	if c := a.computer(s.ComputerId); c != nil && !c.ThisComputer {
		name += " · " + c.Name
	}
	return name
}

// ---------- actions ----------

func (a *App) runScheduleNow(s *pb.Schedule) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: s.Id}}}, func(r *pb.ServerMessage) {
		if id := client.CreatedID(r); id != "" {
			a.selectTab(id) // on another computer, its tab shows once it comes back
		}
		a.flash("Running “" + s.Name + "”")
	})
}

func (a *App) setSchedulePaused(s *pb.Schedule, paused bool) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: s.Id, Paused: paused}}}, func(*pb.ServerMessage) {
		if paused {
			a.flash("Paused “" + s.Name + "”")
		} else {
			a.flash("Resumed “" + s.Name + "”")
		}
	})
}

// toggleSkip skips the next run, or undoes that.
func (a *App) toggleSkip(s *pb.Schedule) {
	skip := !(s.SkipUntilUnixMs > s.NextRunUnixMs && s.NextRunUnixMs != 0)
	if skip && s.NextRunUnixMs == 0 {
		a.flash("“" + s.Name + "” isn't due to run")
		return
	}
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SkipScheduleRun{SkipScheduleRun: &pb.SkipScheduleRun{ScheduleId: s.Id, Skip: skip}}}, func(*pb.ServerMessage) {
		if skip {
			a.flash("Skipping the next run of “" + s.Name + "”")
		} else {
			a.flash("“" + s.Name + "” runs next time")
		}
	})
}

// confirmDeleteSchedule asks before deleting s; then (or on Cancel) back
// to the list.
func (a *App) confirmDeleteSchedule(s *pb.Schedule) {
	back := func() { a.openSchedules(s.Id) }
	a.openDialog(&dialog{title: "Delete “" + truncate(s.Name, 40) + "”?", okLabel: "Delete", confirm: true,
		lines:  []string{"It won't run again. Tabs of its runs stay open."},
		cancel: back,
		submit: func([]string) {
			a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_DeleteSchedule{DeleteSchedule: &pb.DeleteSchedule{ScheduleId: s.Id}}}, func(*pb.ServerMessage) {
				a.flash("Deleted “" + s.Name + "”")
			})
			a.openSchedules("")
		}})
}

// answerAgent is Needs You's Allow / Deny (and Always Allow for This
// Schedule) for an agent's pane, typed by the daemon while it shows a prompt.
func (a *App) answerAgent(paneID string, d pb.AgentAnswer_Decision) {
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_AgentAnswer{AgentAnswer: &pb.AgentAnswer{PaneId: paneID, Decision: d}}}, func(*pb.ServerMessage) {
		if d == pb.AgentAnswer_DECISION_ALLOW_SCHEDULE {
			a.flash("Allowed, and for this schedule's later runs")
		}
	})
}

// needsMenu is a Needs You row's menu: show it, and for a permission
// prompt Allow / Deny, and for a scheduled Claude run Always Allow for
// This Schedule.
func (a *App) needsMenu(id string, x, y int) {
	var at *pb.AttentionItem
	for _, it := range a.attention {
		if it.Id == id {
			at = it
		}
	}
	if at == nil {
		return
	}
	pane := at.PaneId
	items := []menuItem{{label: truncate(cmp.Or(oneLine(at.Summary), "Needs you"), 60), disabled: true}, {sep: true},
		{label: "Show", run: func() { a.reveal(pane) }}}
	if at.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT && !at.Question {
		items = append(items, menuItem{sep: true},
			menuItem{label: "Allow", run: func() { a.answerAgent(pane, pb.AgentAnswer_DECISION_ALLOW) }})
		if s := a.runSchedule(a.panes[pane]); s != nil && s.Kind == "claude" {
			items = append(items, menuItem{label: "Always Allow for This Schedule", run: func() { a.answerAgent(pane, pb.AgentAnswer_DECISION_ALLOW_SCHEDULE) }})
		}
		items = append(items, menuItem{label: "Deny", danger: true, run: func() { a.answerAgent(pane, pb.AgentAnswer_DECISION_DENY) }})
	}
	a.openMenu(x, y, items)
}

// ---------- the dialog ----------

// openNewSchedule asks for a new scheduled task in group ("": the daemon
// picks the Group working in its folder, or makes one), in dir ("": the
// Group's folder), running kind.
func (a *App) openNewSchedule(group, dir, kind string) {
	a.openScheduleDialog(a.newSchedule(group, dir, kind), nil, "", nil)
}

// newSchedule is a new schedule's starting point: weekdays at 09:00.
func (a *App) newSchedule(group, dir, kind string) *pb.Schedule {
	s := &pb.Schedule{GroupId: group, Kind: kind, Weekdays: []int32{1, 2, 3, 4, 5}, MinuteOfDay: 9 * 60}
	g := a.group(group)
	if !canSchedule(g) {
		s.GroupId, g = "", nil
	}
	if dir == "" && g != nil {
		dir = g.Folder
	}
	if dir == "" {
		if p := a.panes[a.focus]; p != nil {
			dir = p.Cwd
		}
	}
	s.Cwd = dir
	return s
}

// scheduleThis schedules what a tab does: its folder and Group, and its
// agent if it has one, starting from the agent's session: named after the
// tab (as you named it, else the session's title), with the first prompt you
// sent in it (on one line: the field is one line).
func (a *App) scheduleThis(t *pb.Tab) {
	kind, dir := "claude", ""
	var agent *pb.Agent
	if p := a.panes[t.FocusedPaneId]; p != nil {
		dir = p.Cwd
		agent = p.Agent
		if ag := agents.Get(agent.GetHarness()); ag != nil && ag.Schedulable {
			kind = ag.ID
		}
	}
	s := a.newSchedule(t.GroupId, dir, kind)
	if agent == nil || agent.SessionId == "" {
		a.openScheduleDialog(s, nil, "", nil)
		return
	}
	s.Name = t.Name
	if s.Name == "" && strings.HasPrefix(t.AutoKey, "agent:") {
		s.Name = t.AutoName
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		r, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SessionExcerpts{SessionExcerpts: &pb.SessionExcerpts{
			Harness: agent.Harness, Id: agent.SessionId, FirstPrompt: true}}})
		a.actions <- func() {
			if ex := r.GetSessionExcerpt(); err == nil && ex != nil {
				if si := ex.Session; s.Name == "" && si != nil && si.Title != si.FirstPrompt {
					s.Name = si.Title
				}
				if m := ex.Messages; len(m) > 0 {
					s.Prompt = strings.Join(strings.Fields(m[0].Text), " ")
				}
			}
			a.openScheduleDialog(s, nil, "", nil)
		}
	}()
}

// openScheduleDialog makes (base without an id) or edits a schedule. vals
// are the fields as last typed, and errText what the daemon said about
// them; back runs on Cancel (the list it came from).
func (a *App) openScheduleDialog(base *pb.Schedule, vals []string, errText string, back func()) {
	if vals == nil {
		vals = []string{base.Name, cmp.Or(base.Kind, "claude"), base.Prompt, shortPath(base.Cwd), whenField(base), notifyWords[base.Notify]}
	}
	title, ok := "New Scheduled Task", "Schedule"
	if base.Id != "" {
		title, ok = "Edit “"+truncate(base.Name, 36)+"”", "Save"
	}
	where := "Runs as a new tab in the Group working in its folder."
	if base.GroupId != "" {
		name := base.GroupName
		if g := a.group(base.GroupId); g != nil {
			name = iconTitle(g, a.groupTitle(g))
		}
		where = "Runs as a new tab in " + name + "."
	}
	labels := []string{"Name", "Kind", "Prompt", "Folder", "When", "Notify"}
	if strings.EqualFold(strings.TrimSpace(vals[1]), "command") {
		labels[2] = "Command"
	}
	fields := make([]*field, len(labels))
	for i, l := range labels {
		fields[i] = &field{label: l, value: vals[i]}
	}
	a.openDialog(&dialog{title: title, okLabel: ok, fields: fields, errText: errText, cancel: back,
		lines: []string{
			where,
			"Kind: claude, codex or command (the prompt is then its command line).",
			"When: every day / weekdays / weekends / mon,wed HH:MM, or cron M H D M W.",
			"Notify: ready, problems or never (blank: ready for agents, problems for commands).",
			"More options in the Mac app or `hmk schedule`.",
			"",
		},
		submit: func(v []string) {
			s, err := scheduleFromFields(base, v)
			if err != nil {
				a.openScheduleDialog(base, v, err.Error(), back)
				return
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				r, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: s}}})
				a.actions <- func() {
					if err != nil {
						if a.overlay == nil {
							a.openScheduleDialog(base, v, err.Error(), back)
						} else {
							a.flash("⚠ " + err.Error())
						}
						return
					}
					saved := r.GetScheduleReply().GetSchedule()
					if saved != nil {
						if a.schedules == nil {
							a.schedules = map[string]*pb.Schedule{}
						}
						a.schedules[saved.Id] = saved // before its state update, so the list has it
						when := nextRunText(saved.NextRunUnixMs, time.Now())
						if when == "" {
							a.flash("Saved “" + saved.Name + "”")
						} else {
							a.flash("Saved “" + saved.Name + "”: next run " + when)
						}
						if back != nil && a.overlay == nil {
							a.openSchedules(saved.Id)
						}
					}
				}
			}()
		}})
}

// scheduleFromFields is base with the dialog's fields (name, kind, prompt,
// folder, when, notify) put in; the rest of base stays as it was.
func scheduleFromFields(base *pb.Schedule, v []string) (*pb.Schedule, error) {
	s := &pb.Schedule{}
	if base != nil {
		s = cloneSchedule(base)
	}
	kind, err := parseKind(v[1])
	if err != nil {
		return nil, err
	}
	days, minute, cron, err := parseWhen(v[4])
	if err != nil {
		return nil, err
	}
	notify, err := parseNotify(v[5])
	if err != nil {
		return nil, err
	}
	s.Name, s.Kind, s.Prompt = strings.TrimSpace(v[0]), kind, strings.TrimSpace(v[2])
	if s.Prompt == "" {
		if kind == "command" {
			return nil, errors.New("give the command to run")
		}
		return nil, errors.New("give the agent a prompt")
	}
	s.Cwd = expandTilde(strings.TrimSpace(v[3]))
	if s.Cwd == "" {
		return nil, errors.New("give the folder it runs in")
	}
	s.Weekdays, s.MinuteOfDay, s.Cron, s.Notify = days, minute, cron, notify
	// The daemon's own fields aren't sent back.
	s.Runs, s.NextRunUnixMs, s.Error = nil, 0, ""
	return s, nil
}

func cloneSchedule(s *pb.Schedule) *pb.Schedule { return proto.CloneOf(s) }

// ---------- the list ----------

type scheduleView struct {
	selID  string // the selected schedule
	runSel int    // -1: the schedules have the keyboard; else the selected run
	open   *spring
}

// openSchedules shows the Scheduled Tasks list, with id selected ("": the first).
func (a *App) openSchedules(id string) {
	v := &scheduleView{selID: id, runSel: -1, open: newSpring(gentle)}
	v.open.from(0)
	v.open.to(1)
	a.overlay = v
}

func (v *scheduleView) list(a *App) ([]*pb.Schedule, int) {
	l := a.scheduleList()
	i := slices.IndexFunc(l, func(s *pb.Schedule) bool { return s.Id == v.selID })
	if i < 0 && len(l) > 0 {
		i = 0
		v.selID = l[0].Id
	}
	return l, i
}

func (v *scheduleView) selected(a *App) *pb.Schedule {
	l, i := v.list(a)
	if i < 0 {
		return nil
	}
	return l[i]
}

// The layout: the title, the schedules, the selected one's detail and
// runs, and the keys.
const (
	schedRunRows    = 6
	schedDetailRows = 2
)

type schedLayout struct {
	frame             rect
	inner             rect
	listY, listRows   int
	detailY           int
	runsY, runRows    int
	hintY             int
	nameW, groupW     int
	whenW, nextW, okW int
}

func (v *scheduleView) layout(a *App, n int) schedLayout {
	w := min(112, a.w-4)
	listRows := min(max(n, 3), 10)
	runRows := schedRunRows
	// border 2, title 1, rule 1, list, rule 1, detail, runs title 1, runs, rule 1, hints 1
	chrome := 2 + 1 + 1 + 1 + schedDetailRows + 1 + 1 + 1
	room := a.h - 2
	for chrome+listRows+runRows > room && runRows > 1 {
		runRows--
	}
	for chrome+listRows+runRows > room && listRows > 1 {
		listRows--
	}
	h := chrome + listRows + runRows
	r := rect{(a.w - w) / 2, max(0, min(a.h/8, a.h-h)), w, h}
	in := rect{r.x + 1, r.y + 1, r.w - 2, r.h - 2}
	l := schedLayout{frame: r, inner: in, listY: in.y + 2, listRows: listRows}
	l.detailY = l.listY + listRows + 1
	l.runsY = l.detailY + schedDetailRows + 1
	l.runRows = runRows
	l.hintY = in.y + in.h - 1
	// Columns, right to left: last run, next run, when, Group; the name gets the rest.
	l.okW, l.nextW, l.whenW, l.groupW = 13, 20, 24, 22
	if in.w < 96 {
		l.groupW = 0
	}
	if in.w < 72 {
		l.whenW = 0
	}
	l.nameW = in.w - 3 - l.okW - l.nextW - l.whenW - l.groupW
	return l
}

func (v *scheduleView) draw(a *App) {
	if v.open.step() {
		a.dirty = true
	}
	t := clamp01(v.open.pos)
	a.dimBehind(t)
	list, sel := v.list(a)
	l := v.layout(a, len(list))
	r := l.frame
	r.y += int(math.Round((1 - v.open.pos) * -2)) // drops in
	full := r.h
	r.h = max(3, int(math.Round(float64(full)*math.Max(0.4, t))))
	bg := mix(a.th.bg, a.th.menu, t)
	in := a.box(r, bg, mix(bg, a.th.border, t))
	if r.h < full {
		return // still opening
	}
	dy := r.y - l.frame.y
	s := a.scr
	plain := Cell{FG: a.th.fg, HasFG: true, BG: bg, HasBG: true}
	dimC := Cell{FG: a.th.dim, HasFG: true, BG: bg, HasBG: true}
	rule := func(y int) {
		for x := in.x; x < in.x+in.w; x++ {
			s.Set(x, y, Cell{Ch: "─", FG: a.th.border, HasFG: true, BG: bg, HasBG: true})
		}
	}
	title := plain
	title.Bold = true
	s.Text(in.x+1, in.y, in.w-2, "Scheduled Tasks", title)
	s.TextRight(in.x+1, in.y, in.w-2, plural(len(list), "task"), dimC)
	rule(in.y + 1)
	now := time.Now()

	// The schedules.
	top := max(0, sel-l.listRows+1)
	if len(list) == 0 {
		s.Text(in.x+3, l.listY+dy, in.w-4, "No scheduled tasks yet: n makes one", dimC)
	}
	for i := 0; i < l.listRows && top+i < len(list); i++ {
		sc := list[top+i]
		y := l.listY + dy + i
		isSel := top+i == sel
		rowBG := bg
		if isSel {
			rowBG = mix(bg, a.th.menuSel, 0.85)
			if v.runSel >= 0 {
				rowBG = mix(bg, a.th.menuSel, 0.35) // the runs have the keyboard
			}
		}
		s.Paint(in.x, y, in.w, 1, rowBG)
		fg, dim := a.th.fg, a.th.dim
		if isSel && v.runSel < 0 {
			fg, dim = RGB{255, 255, 255}, mix(RGB{255, 255, 255}, rowBG, 0.35)
		}
		if sc.Paused {
			fg = dim
		}
		g := a.kindGlyph(sc.Kind)
		s.Text(in.x+1, y, 1, g.ch, Cell{FG: g.col, HasFG: true, BG: rowBG, HasBG: true, Bold: g.bold})
		dc := Cell{FG: dim, HasFG: true, BG: rowBG, HasBG: true}
		x := in.x + in.w - 1
		col := func(w int, text string, c Cell) {
			if w == 0 {
				return
			}
			x -= w
			s.Text(x+1, y, w-1, truncate(text, w-1), c)
		}
		// The last run.
		x -= l.okW
		if len(sc.Runs) > 0 {
			rg, word := a.runStatus(sc.Runs[0])
			s.Text(x+1, y, 1, rg.ch, Cell{FG: rg.col, HasFG: true, BG: rowBG, HasBG: true, Bold: rg.bold})
			s.Text(x+3, y, l.okW-3, truncate(word, l.okW-3), dc)
		}
		nc := dc
		if sc.Error != "" {
			nc.FG = a.th.red
		}
		col(l.nextW, nextText(sc, now), nc)
		col(l.whenW, scheduleWhen(sc), dc)
		col(l.groupW, a.scheduleGroup(sc), dc)
		room := x - (in.x + 3)
		s.Text(in.x+3, y, room, truncate(sc.Name, room), Cell{FG: fg, HasFG: true, BG: rowBG, HasBG: true, Bold: isSel})
	}
	rule(l.detailY + dy - 1)

	// The selected one: what it runs, where, and anything wrong.
	if sel >= 0 {
		sc := list[sel]
		what := kindName(sc.Kind) + " in " + shortPath(sc.Cwd) + ": " + oneLine(sc.Prompt)
		s.Text(in.x+1, l.detailY+dy, in.w-2, truncate(what, in.w-2), plain)
		var more []string
		if l.groupW == 0 {
			more = append(more, a.scheduleGroup(sc))
		}
		if l.whenW == 0 {
			more = append(more, scheduleWhen(sc))
		}
		if sc.CreatedBy != "" {
			more = append(more, "made by "+sc.CreatedBy)
		}
		if n := len(sc.AllowedTools); n > 0 {
			more = append(more, plural(n, "tool")+" allowed")
		}
		line, c := strings.Join(more, " · "), dimC
		if sc.Error != "" {
			line, c = "⚠ "+sc.Error, Cell{FG: a.th.red, HasFG: true, BG: bg, HasBG: true}
		}
		s.Text(in.x+1, l.detailY+dy+1, in.w-2, truncate(line, in.w-2), c)

		// Its recent runs.
		s.Text(in.x+1, l.runsY+dy-1, in.w-2, "Recent runs", Cell{FG: a.th.dim, HasFG: true, BG: bg, HasBG: true, Bold: true})
		if len(sc.Runs) == 0 {
			s.Text(in.x+3, l.runsY+dy, in.w-4, "None yet", dimC)
		}
		rtop := max(0, v.runSel-l.runRows+1)
		for i := 0; i < l.runRows && rtop+i < len(sc.Runs); i++ {
			run := sc.Runs[rtop+i]
			y := l.runsY + dy + i
			isSel := rtop+i == v.runSel
			rowBG := bg
			if isSel {
				rowBG = mix(bg, a.th.menuSel, 0.85)
			}
			s.Paint(in.x, y, in.w, 1, rowBG)
			fg, dim := a.th.fg, a.th.dim
			if isSel {
				fg, dim = RGB{255, 255, 255}, mix(RGB{255, 255, 255}, rowBG, 0.35)
			}
			rg, word := a.runStatus(run)
			s.Text(in.x+1, y, 1, rg.ch, Cell{FG: rg.col, HasFG: true, BG: rowBG, HasBG: true, Bold: rg.bold})
			s.Text(in.x+3, y, 12, word, Cell{FG: fg, HasFG: true, BG: rowBG, HasBG: true, Bold: isSel})
			when := relTime(runTime(run), now)
			switch {
			case run.Manual:
				when += " (Run Now)"
			case run.CatchUp:
				when += " (late)"
			}
			dc := Cell{FG: dim, HasFG: true, BG: rowBG, HasBG: true}
			s.Text(in.x+16, y, 24, truncate(when, 23), dc)
			open := ""
			if a.runTab(run) != "" {
				open = "● open"
				oc := dc
				oc.FG = a.th.green
				if isSel {
					oc.FG = RGB{255, 255, 255}
				}
				s.TextRight(in.x+1, y, in.w-2, open, oc)
			}
			room := in.w - 42 - textWidth(open) - 2
			s.Text(in.x+41, y, room, truncate(oneLine(run.Note), room), dc)
		}
	}
	rule(l.hintY + dy - 1)
	hints := "n New   esc Close"
	if sel >= 0 {
		sc := list[sel]
		pause := "p Pause"
		if sc.Paused {
			pause = "p Resume"
		}
		skip := "s Skip Next"
		if sc.SkipUntilUnixMs > sc.NextRunUnixMs && sc.NextRunUnixMs != 0 {
			skip = "s Don't Skip"
		}
		hints = "↵/→ Runs   r Run Now   " + pause + "   " + skip + "   e Edit   n New   ⌫ Delete   esc Close"
		if v.runSel >= 0 {
			hints = "↵ Go to Tab   ← Tasks   r Run Now   esc Close"
		}
	}
	s.Text(in.x+1, l.hintY+dy, in.w-2, truncate(hints, in.w-2), Cell{FG: a.th.faint, HasFG: true, BG: bg, HasBG: true})
}

func (v *scheduleView) key(a *App, ks []key) {
	for _, k := range ks {
		list, sel := v.list(a)
		var sc *pb.Schedule
		if sel >= 0 {
			sc = list[sel]
		}
		if k.name == "esc" {
			a.closeOverlay()
			return
		}
		if v.runSel >= 0 && sc != nil {
			switch k.name {
			case "up", "ctrl-p":
				v.runSel = max(0, v.runSel-1)
			case "down", "ctrl-n":
				v.runSel = min(len(sc.Runs)-1, v.runSel+1)
			case "left", "shift-tab", "backspace":
				v.runSel = -1
			case "enter":
				v.openRun(a, sc, v.runSel)
				return
			case "":
				if k.r == 'r' {
					a.closeOverlay()
					a.runScheduleNow(sc)
					return
				}
			}
			continue
		}
		switch k.name {
		case "up", "ctrl-p":
			if sel > 0 {
				v.selID = list[sel-1].Id
			}
		case "down", "ctrl-n":
			if sel >= 0 && sel < len(list)-1 {
				v.selID = list[sel+1].Id
			}
		case "enter", "right", "tab":
			if sc != nil && len(sc.Runs) > 0 {
				v.runSel = 0
			}
		case "backspace", "delete":
			if sc != nil {
				a.confirmDeleteSchedule(sc)
				return
			}
		case "":
			switch k.r {
			case 'n':
				back := func() { a.openSchedules(v.selID) }
				a.openScheduleDialog(a.newSchedule(a.currentGroup(), "", "claude"), nil, "", back)
				return
			case 'q':
				a.closeOverlay()
				return
			}
			if sc == nil {
				continue
			}
			switch k.r {
			case 'r':
				a.closeOverlay()
				a.runScheduleNow(sc)
				return
			case 'p':
				a.setSchedulePaused(sc, !sc.Paused)
			case 's':
				a.toggleSkip(sc)
			case 'e':
				a.openScheduleDialog(sc, nil, "", func() { a.openSchedules(sc.Id) })
				return
			case 'd':
				a.confirmDeleteSchedule(sc)
				return
			case 'j':
				if sel < len(list)-1 {
					v.selID = list[sel+1].Id
				}
			case 'k':
				if sel > 0 {
					v.selID = list[sel-1].Id
				}
			}
		}
	}
}

// openRun goes to a run's tab, if it's still open.
func (v *scheduleView) openRun(a *App, sc *pb.Schedule, i int) {
	if i < 0 || i >= len(sc.Runs) {
		return
	}
	run := sc.Runs[i]
	tab := a.runTab(run)
	if tab == "" {
		a.flash("That run's tab is closed")
		return
	}
	a.closeOverlay()
	if run.PaneId != "" && a.panes[run.PaneId] != nil {
		a.reveal(run.PaneId)
		return
	}
	a.selectTab(tab)
}

func (v *scheduleView) mouse(a *App, m mouseEv) {
	list, sel := v.list(a)
	l := v.layout(a, len(list))
	if m.wheel != 0 {
		if v.runSel >= 0 && sel >= 0 {
			v.runSel = max(0, min(len(list[sel].Runs)-1, v.runSel+m.wheel))
		} else if sel >= 0 {
			v.selID = list[max(0, min(len(list)-1, sel+m.wheel))].Id
		}
		return
	}
	if !m.press {
		return
	}
	if !l.frame.contains(m.x, m.y) {
		a.closeOverlay()
		return
	}
	if m.y >= l.listY && m.y < l.listY+l.listRows {
		top := max(0, sel-l.listRows+1)
		if i := top + m.y - l.listY; i < len(list) {
			v.selID, v.runSel = list[i].Id, -1
			if m.button == 2 {
				a.scheduleMenu(list[i], m.x, m.y)
			}
		}
		return
	}
	if sel >= 0 && m.y >= l.runsY && m.y < l.runsY+l.runRows && m.button == 0 {
		sc := list[sel]
		rtop := max(0, v.runSel-l.runRows+1)
		if i := rtop + m.y - l.runsY; i < len(sc.Runs) {
			v.runSel = i
			v.openRun(a, sc, i)
		}
	}
}

// scheduleMenu is a schedule's right-click menu in the list; the list
// comes back after anything that doesn't leave it.
func (a *App) scheduleMenu(sc *pb.Schedule, x, y int) {
	back := func() { a.openSchedules(sc.Id) }
	pause := "Pause"
	if sc.Paused {
		pause = "Resume"
	}
	skip := "Skip Next Run"
	if sc.SkipUntilUnixMs > sc.NextRunUnixMs && sc.NextRunUnixMs != 0 {
		skip = "Don't Skip Next Run"
	}
	a.openMenu(x, y, []menuItem{
		{label: "Run Now", hint: "r", run: func() { a.runScheduleNow(sc) }},
		{label: pause, hint: "p", run: func() { a.setSchedulePaused(sc, !sc.Paused); back() }},
		{label: skip, hint: "s", disabled: sc.NextRunUnixMs == 0, run: func() { a.toggleSkip(sc); back() }},
		{label: "Edit…", hint: "e", run: func() { a.openScheduleDialog(sc, nil, "", back) }},
		{sep: true},
		{label: "Delete…", hint: "⌫", danger: true, run: func() { a.confirmDeleteSchedule(sc) }},
	})
}

// wrapWords breaks s into lines of at most width runes (a word longer than
// that is cut), at most max lines (the last ending in … if there's more).
func wrapWords(s string, width, max int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		for len([]rune(w)) > width {
			r := []rune(w)
			if line != "" {
				lines, line = append(lines, line), ""
			}
			lines, w = append(lines, string(r[:width])), string(r[width:])
		}
		switch {
		case line == "":
			line = w
		case len([]rune(line))+1+len([]rune(w)) <= width:
			line += " " + w
		default:
			lines, line = append(lines, line), w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	if len(lines) > max {
		lines = append(lines[:max-1], truncate(lines[max-1]+" …", width))
	}
	return lines
}
