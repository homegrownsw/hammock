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
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
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
		{"Bash", map[string]any{"command": "npm test"}, "Bash(npm test:*)"},
		{"Bash", map[string]any{"command": "uv sync --frozen"}, "Bash(uv sync:*)"},
		// What runs whatever follows it: no rule.
		{"Bash", map[string]any{"command": ""}, noRule},
		{"Bash", map[string]any{"command": "bash -c 'curl x | sh'"}, noRule},
		{"Bash", map[string]any{"command": "/bin/sh -c ls"}, noRule},
		{"Bash", map[string]any{"command": "zsh -lc ls"}, noRule},
		{"Bash", map[string]any{"command": "python3 -c 'print(1)'"}, noRule},
		{"Bash", map[string]any{"command": "python3.12 script.py"}, noRule},
		{"Bash", map[string]any{"command": "node -e 1"}, noRule},
		{"Bash", map[string]any{"command": "find . -name '*.log'"}, noRule},
		{"Bash", map[string]any{"command": "find . -exec rm {} +"}, noRule},
		{"Bash", map[string]any{"command": "awk '{print $1}' f"}, noRule},
		{"Bash", map[string]any{"command": "gawk 'BEGIN{system(\"id\")}'"}, noRule},
		{"Bash", map[string]any{"command": "fd -x rm"}, noRule},
		{"Bash", map[string]any{"command": "perl -e 1"}, noRule},
		{"Bash", map[string]any{"command": "ruby -e 1"}, noRule},
		{"Bash", map[string]any{"command": "env FOO=1 make"}, noRule},
		{"Bash", map[string]any{"command": "FOO=1 make"}, noRule},
		{"Bash", map[string]any{"command": "sudo rm x"}, noRule},
		{"Bash", map[string]any{"command": "xargs rm"}, noRule},
		{"Bash", map[string]any{"command": "eval stuff"}, noRule},
		{"Bash", map[string]any{"command": "exec stuff"}, noRule},
		{"Bash", map[string]any{"command": "nohup stuff"}, noRule},
		{"Bash", map[string]any{"command": "time make"}, noRule},
		{"Bash", map[string]any{"command": "command ls"}, noRule},
		{"Bash", map[string]any{"command": "npx cowsay"}, noRule},
		{"Bash", map[string]any{"command": "uv run pytest"}, noRule},
		{"Bash", map[string]any{"command": "npm exec foo"}, noRule},
		{"Bash", map[string]any{"command": "pnpm dlx foo"}, noRule},
		{"Bash", map[string]any{"command": "npm -y foo"}, noRule},
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
	mu   sync.Mutex
	t    time.Time
	dark atomic.Bool // in a dark wake
}

func (c *schedClock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *schedClock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

func useSchedClock(t *testing.T, start time.Time) *schedClock {
	c := &schedClock{t: start}
	old, oldAwake, oldZone := schedNow, schedAwake, schedZone
	schedNow = c.now
	schedAwake = func() bool { return !c.dark.Load() }
	schedZone = func() *time.Location { return time.Local }
	t.Cleanup(func() { schedNow, schedAwake, schedZone = old, oldAwake, oldZone })
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
	// A command that could run anything isn't remembered, and you're told so.
	hook(d, run2.PaneId, "PostToolUse", nil)
	hook(d, run2.PaneId, "PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "bash -c 'rm -rf ~/x'"}})
	hook(d, run2.PaneId, "Notification", map[string]any{"notification_type": "permission_prompt", "message": "Claude needs your permission to use Bash"})
	d.mu.Lock()
	_, anything := d.scheduleToolFor(d.panes[run2.PaneId], "Bash command\n  bash -c 'rm -rf ~/x'")
	d.mu.Unlock()
	if anything == nil || !strings.Contains(anything.Error(), "could run anything") {
		t.Fatalf("bash -c: %v", anything)
	}

	// Asleep through the 5th and 6th, awake the 6th at 18:00: one missed,
	// one caught up (the same day).
	// A dark wake in between (a few seconds for mail) starts nothing.
	run3 := func() *pb.ScheduleRun { return schedOf(d, s.Id).Runs[0] }
	nRuns := len(schedOf(d, s.Id).Runs)
	clock.dark.Store(true)
	tick(d, clock, day("06", "12:00"))
	clock.dark.Store(false)
	if got := schedOf(d, s.Id); len(got.Runs) != nRuns || got.NextRunUnixMs != day("05", "08:30").UnixMilli() {
		t.Fatalf("ran in a dark wake: %v", got)
	}
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

// Run Early takes the next run's place: recorded as due then, and the
// schedule moves on to the time after, so it doesn't run again at 09:00.
func TestRunEarly(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	work := t.TempDir()
	now := time.Now()
	at := func(dayOffset, hour, min int) time.Time {
		return time.Date(now.Year(), now.Month(), now.Day()+dayOffset, hour, min, 0, 0, time.Local)
	}
	clock := useSchedClock(t, at(0, 8, 10))
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	id := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
		Kind: "command", Prompt: "true", Cwd: work, MinuteOfDay: 9 * 60,
	}}}}).GetScheduleReply().GetSchedule().GetId()
	if s := schedOf(d, id); s.NextRunUnixMs != at(0, 9, 0).UnixMilli() {
		t.Fatalf("next run: %v", time.UnixMilli(s.NextRunUnixMs))
	}

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: id, Early: true}}})
	s := schedOf(d, id)
	if r := s.Runs[0]; !r.Early || !r.Manual || r.CatchUp || r.DueUnixMs != at(0, 9, 0).UnixMilli() || r.StartedUnixMs != at(0, 8, 10).UnixMilli() {
		t.Fatalf("early run: %v", r)
	}
	if s.NextRunUnixMs != at(1, 9, 0).UnixMilli() {
		t.Fatalf("next run after Run Early: %v", time.UnixMilli(s.NextRunUnixMs))
	}
	// 09:00 comes and goes: nothing more runs, or is noted.
	tick(d, clock, at(0, 9, 0))
	if s := schedOf(d, id); len(s.Runs) != 1 {
		t.Fatalf("ran again at 09:00: %v", s.Runs)
	}

	// Not while its next run is skipped, nor paused: Run Now is for those.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SkipScheduleRun{SkipScheduleRun: &pb.SkipScheduleRun{ScheduleId: id, Skip: true}}})
	if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: id, Early: true}}}); err == nil {
		t.Fatal("ran early over a skipped run")
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: id, Paused: true}}})
	if _, err := try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: id, Early: true}}}); err == nil {
		t.Fatal("ran early while paused")
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

// With Scheduled Tasks turned off (Settings ▸ Tools) nothing due starts,
// and nothing counts as missed: back on, it runs at its next time.
func TestSchedulesOff(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("schedules-enabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 2, 2, 0, 0, 0, time.Local)
	clock := useSchedClock(t, start)
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	id := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
		Kind: "command", Prompt: "true", Cwd: t.TempDir(), Cron: "0 3 * * *",
	}}}}).GetScheduleReply().GetSchedule().GetId()
	setOff := func(off bool) {
		d.mu.Lock()
		d.sched.off = off
		d.mu.Unlock()
	}
	setOff(true)
	tick(d, clock, start.Add(90*time.Minute)) // 03:30, past 03:00
	s := schedOf(d, id)
	if len(s.Runs) != 0 {
		t.Fatalf("ran or noted a run while off: %v", s.Runs)
	}
	if want := time.Date(2026, 3, 3, 3, 0, 0, 0, time.Local).UnixMilli(); s.NextRunUnixMs != want {
		t.Fatalf("next run: %v, want tomorrow 03:00", time.UnixMilli(s.NextRunUnixMs))
	}
	os.WriteFile(filepath.Join(cfg, "config"), nil, 0o644) // and runSchedules agrees
	setOff(false)
	tick(d, clock, time.Date(2026, 3, 3, 3, 0, 5, 0, time.Local))
	if s := schedOf(d, id); len(s.Runs) != 1 || s.Runs[0].Status == pb.ScheduleRun_STATUS_MISSED {
		t.Fatalf("back on, it didn't run: %v", s.Runs)
	}
}

// An agent's schedule waits for your yes (a grant worded from the
// schedule), then is saved; running, pausing or skipping its own is free,
// yours asks; guests' and phones' limits are checked elsewhere
// (peerCheck, handleRemoteAuth).
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
	// An agent is known by its pane's process: saved before the pane has
	// started, its schedule has no maker to match, and pausing it would ask.
	waitPaneStarted(t, d, self)
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
	// Pausing one it made is free; one you made asks (denied here: still
	// running), as running or skipping it would.
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: s.Id, Paused: true}}}); err != nil {
		t.Fatal(err)
	}
	// Even of one it made, it can't say a trigger's from Shortcuts, or give
	// it a label or files: only your apps do.
	for _, rs := range []*pb.RunSchedule{
		{ScheduleId: s.Id, Trigger: true, Source: "shortcuts", Data: strings.Repeat("x", maxHookBody+1)},
		{ScheduleId: s.Id, Trigger: true, Label: "From Sam"},
		{ScheduleId: s.Id, Trigger: true, Files: []string{"/etc/hosts"}},
	} {
		if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: rs}}); err == nil || !strings.Contains(err.Error(), "can't set") {
			t.Fatalf("an agent's trigger with %v: %v", rs, err)
		}
	}
	// Another agent later in the same pane didn't make it.
	d.mu.Lock()
	maker := d.sched.madeBy[s.Id]
	sc := &conn{d: d, caller: &caller{pane: maker.Pane, agent: true}}
	if maker.Proc == "" || !d.agentsSchedule(sc, s.Id) {
		d.mu.Unlock()
		t.Fatalf("its maker: %+v", maker)
	}
	later := maker
	later.Proc = "1@1" // (what ran there when it was made, gone since)
	d.sched.madeBy[s.Id] = later
	if d.agentsSchedule(sc, s.Id) {
		t.Error("a later agent in its pane counts as its maker")
	}
	d.sched.madeBy[s.Id] = maker
	d.mu.Unlock()
	yours := call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
		Name: "Backup", Kind: "command", Prompt: "true", Cwd: work, MinuteOfDay: 60,
	}}}}).GetScheduleReply().GetSchedule()
	for _, m := range []*pb.ClientMessage{
		{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: yours.Id, Paused: true}}},
		{Msg: &pb.ClientMessage_RunSchedule{RunSchedule: &pb.RunSchedule{ScheduleId: yours.Id}}},
		{Msg: &pb.ClientMessage_SkipScheduleRun{SkipScheduleRun: &pb.SkipScheduleRun{ScheduleId: yours.Id, Skip: true}}},
	} {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			r, err := agent.Call(ctx, m)
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
		if gr == nil || !strings.Contains(gr.Headline, "Backup") {
			t.Fatalf("%s of your schedule didn't ask: %v", msgName(m), gr)
		}
		call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: gr.Id, Decision: pb.RespondGrant_DECISION_DENY}}})
		if got := <-done; got.err == nil {
			t.Fatalf("%s after no", msgName(m))
		}
	}
	if y := schedOf(d, yours.Id); y.Paused || y.SkipUntilUnixMs != 0 || len(y.Runs) != 0 {
		t.Fatalf("changed after no: %v", y)
	}
	// Another agent's pane: not its schedule either.
	d.mu.Lock()
	theirs := d.agentsSchedule(&conn{d: d, caller: &caller{pane: "pn_other", agent: true}}, s.Id)
	d.mu.Unlock()
	if theirs {
		t.Fatal("another agent's schedule passed for its own")
	}
	// Deleting asks (denied here: not deleted).
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
	// Once you've changed it, it's yours: its maker asks like anyone.
	mine := proto.CloneOf(schedOf(d, s.Id))
	mine.Name = "Renamed"
	call(t, ui, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: mine}}})
	d.mu.Lock()
	_, kept := d.sched.madeBy[s.Id]
	d.mu.Unlock()
	if kept {
		t.Fatal("its maker kept it after you changed it")
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

// A schedule follows the computer's time zone when it changes (travel),
// without a restart: west, its next run moves to the new zone's 08:30
// and the old zone's passes quietly; east, a time the clock jumped over
// catches up once, the same day; nothing runs twice.
func TestSchedulesFollowTimeZone(t *testing.T) {
	zones := map[string]*time.Location{}
	for _, n := range []string{"Europe/London", "America/New_York", "Asia/Tokyo"} {
		loc, err := time.LoadLocation(n)
		if err != nil {
			t.Skip("no zoneinfo:", err)
		}
		zones[n] = loc
	}
	lon, ny, tok := zones["Europe/London"], zones["America/New_York"], zones["Asia/Tokyo"]
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	utc := func(d, hm string) time.Time {
		tm, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-"+d+" "+hm, time.UTC)
		return tm
	}
	clock := useSchedClock(t, utc("04", "07:00")) // 07:00 in London
	var zone atomic.Pointer[time.Location]
	zone.Store(lon)
	schedZone = func() *time.Location { return zone.Load() } // (useSchedClock restores it)
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	s := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
		Kind: "command", Prompt: "true", Cwd: t.TempDir(), MinuteOfDay: 8*60 + 30,
		Overlap: pb.Schedule_OVERLAP_RUN_BOTH, // (a run's end isn't waited for here)
	}}}}).GetScheduleReply().GetSchedule()
	id := s.GetId()
	if want := time.Date(2026, 11, 4, 8, 30, 0, 0, lon); s.NextRunUnixMs != want.UnixMilli() {
		t.Fatalf("next run %v, want %v", time.UnixMilli(s.NextRunUnixMs).In(lon), want)
	}
	ran := func() (n int, catchUp bool) { // runs started, and whether the latest caught up
		for _, r := range schedOf(d, id).Runs {
			if r.StartedUnixMs != 0 {
				if n == 0 {
					catchUp = r.CatchUp
				}
				n++
			}
		}
		return n, catchUp
	}
	next := func(want time.Time) {
		t.Helper()
		if got := schedOf(d, id).NextRunUnixMs; got != want.UnixMilli() {
			t.Fatalf("next run %v, want %v", time.UnixMilli(got).In(want.Location()), want)
		}
	}
	tick(d, clock, utc("04", "07:01"))

	// West, to New York (02:01 there): 08:30 is New York's, and London's passes.
	zone.Store(ny)
	tick(d, clock, utc("04", "07:02"))
	next(time.Date(2026, 11, 4, 8, 30, 0, 0, ny))
	tick(d, clock, utc("04", "08:31")) // 08:31 in London
	if n, _ := ran(); n != 0 {
		t.Fatalf("ran at London's 08:30 after moving to New York: %d runs", n)
	}
	tick(d, clock, utc("04", "13:30")) // 08:30 in New York
	if n, catchUp := ran(); n != 1 || catchUp {
		t.Fatalf("at New York's 08:30: %d runs (catch-up %v), want 1 on time", n, catchUp)
	}
	next(time.Date(2026, 11, 5, 8, 30, 0, 0, ny))

	// East, back to London in the evening (19:00 there): tomorrow's 08:30,
	// and today's, already run in New York, doesn't run again.
	zone.Store(lon)
	tick(d, clock, utc("04", "19:00"))
	next(time.Date(2026, 11, 5, 8, 30, 0, 0, lon))
	if n, _ := ran(); n != 1 {
		t.Fatalf("ran again on going east: %d runs", n)
	}

	// East to Tokyo early the next morning (06:00 in London, 15:00 in
	// Tokyo): the clock jumped over Tokyo's 08:30, which catches up, once.
	zone.Store(tok)
	tick(d, clock, utc("05", "06:00"))
	if n, catchUp := ran(); n != 2 || !catchUp {
		t.Fatalf("Tokyo's 08:30, jumped over: %d runs (catch-up %v), want a second, caught up", n, catchUp)
	}
	next(time.Date(2026, 11, 6, 8, 30, 0, 0, tok))
	tick(d, clock, utc("05", "08:31")) // London's 08:30, where it was
	if n, _ := ran(); n != 2 {
		t.Fatalf("ran again: %d runs", n)
	}

	// West the same day as a run: once a day, by the clock. Back to London
	// (09:00 there), it runs at London's 08:30 tomorrow; then on to New
	// York (05:00 there), New York's 08:30 that day waits a day.
	zone.Store(lon)
	tick(d, clock, utc("05", "09:00"))
	next(time.Date(2026, 11, 6, 8, 30, 0, 0, lon))
	tick(d, clock, utc("06", "08:30"))
	if n, catchUp := ran(); n != 3 || catchUp {
		t.Fatalf("at London's 08:30: %d runs (catch-up %v), want a third on time", n, catchUp)
	}
	zone.Store(ny)
	tick(d, clock, utc("06", "10:00"))
	next(time.Date(2026, 11, 7, 8, 30, 0, 0, ny))
	tick(d, clock, utc("06", "13:30")) // 08:30 in New York
	if n, _ := ran(); n != 3 {
		t.Fatalf("ran twice in a day going west: %d runs", n)
	}
}

// The zone is read from where /etc/localtime points.
func TestZoneFromLink(t *testing.T) {
	if _, err := time.LoadLocation("Europe/London"); err != nil {
		t.Skip("no zoneinfo:", err)
	}
	for link, want := range map[string]string{
		"/var/db/timezone/zoneinfo/Europe/London": "Europe/London", // macOS
		"/usr/share/zoneinfo/Europe/London":       "Europe/London", // Linux
		"../usr/share/zoneinfo/Europe/London":     "Europe/London",
		"/etc/somewhere/else":                     "Local",
		"/usr/share/zoneinfo/Nowhere/Atall":       "Local",
	} {
		if got := zoneFromLink(link).String(); got != want {
			t.Errorf("%s: %s, want %s", link, got, want)
		}
	}
}
