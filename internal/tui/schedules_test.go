package tui

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestParseWhen(t *testing.T) {
	for _, tc := range []struct {
		in     string
		days   []int32
		minute int32
		cron   string
	}{
		{"weekdays 08:30", []int32{1, 2, 3, 4, 5}, 8*60 + 30, ""},
		{"every day 9:05", nil, 9*60 + 5, ""},
		{"daily at 17", nil, 17 * 60, ""},
		{"Weekends at 10:00", []int32{0, 6}, 10 * 60, ""},
		{"mon,wed, Friday 07:15", []int32{1, 3, 5}, 7*60 + 15, ""},
		{"sun mon tue wed thu fri sat 00:00", nil, 0, ""},
		{"cron 0 * * * *", nil, 0, "0 * * * *"},
		{"*/15 9-17 * * 1-5", nil, 0, "*/15 9-17 * * 1-5"},
		{"30 8 * jan-mar mon-fri", nil, 0, "30 8 * jan-mar mon-fri"},
	} {
		days, minute, cron, err := parseWhen(tc.in)
		if err != nil || !slices.Equal(days, tc.days) || minute != tc.minute || cron != tc.cron {
			t.Errorf("%q: %v %d %q %v", tc.in, days, minute, cron, err)
		}
	}
	for _, bad := range []string{"", "weekdays", "weekdays 25:00", "someday 08:00", "8:5", "cron"} {
		if _, _, _, err := parseWhen(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
	// Five day names with the time forgotten aren't a cron line.
	if _, _, cron, err := parseWhen("mon tue wed thu fri"); err == nil || !strings.Contains(err.Error(), "what time") {
		t.Errorf("mon tue wed thu fri: cron %q, err %v", cron, err)
	}
}

func TestWhenFieldReadsBack(t *testing.T) {
	for _, s := range []*pb.Schedule{
		{Weekdays: []int32{1, 2, 3, 4, 5}, MinuteOfDay: 8*60 + 30},
		{MinuteOfDay: 23*60 + 59},
		{Weekdays: []int32{0, 6}, MinuteOfDay: 60},
		{Weekdays: []int32{2, 4}, MinuteOfDay: 12 * 60},
		{Cron: "0 * * * *"},
	} {
		days, minute, cron, err := parseWhen(whenField(s))
		if err != nil || !slices.Equal(days, s.Weekdays) || minute != s.MinuteOfDay || cron != s.Cron {
			t.Errorf("%q: %v %d %q %v", whenField(s), days, minute, cron, err)
		}
	}
	if got := scheduleWhen(&pb.Schedule{Weekdays: []int32{1, 2, 3, 4, 5}, MinuteOfDay: 8*60 + 30}); got != "weekdays at 08:30" {
		t.Error(got)
	}
	if got := scheduleWhen(&pb.Schedule{Cron: "0 * * * *"}); got != "cron 0 * * * *" {
		t.Error(got)
	}
	if got := scheduleWhen(&pb.Schedule{Weekdays: []int32{1, 3}, MinuteOfDay: 9 * 60}); got != "Mon,Wed at 09:00" {
		t.Error(got)
	}
}

func TestNextRunText(t *testing.T) {
	now := time.Date(2026, 11, 4, 15, 0, 0, 0, time.Local) // a Wednesday
	at := func(d, h, m int) int64 { return time.Date(2026, 11, d, h, m, 0, 0, time.Local).UnixMilli() }
	for _, tc := range []struct {
		ms   int64
		want string
	}{
		{now.Add(12 * time.Minute).UnixMilli(), "in 12 min"},
		{at(4, 17, 0), "today 17:00"},
		{at(5, 8, 30), "tomorrow 08:30"},
		{at(7, 8, 30), "Sat 08:30"},
		{at(20, 8, 30), "20 Nov 08:30"},
		{0, ""},
	} {
		if got := nextRunText(tc.ms, now); got != tc.want {
			t.Errorf("%v: %q, want %q", time.UnixMilli(tc.ms), got, tc.want)
		}
	}
	s := &pb.Schedule{NextRunUnixMs: at(5, 8, 30), SkipUntilUnixMs: at(5, 8, 30) + 1}
	if got := nextText(s, now); got != "skipping tomorrow 08:30" {
		t.Error(got)
	}
	s.Paused = true
	if got := nextText(s, now); got != "paused" {
		t.Error(got)
	}
}

func TestScheduleFromFieldsKeepsTheRest(t *testing.T) {
	base := &pb.Schedule{Id: "sc1", Name: "Old", GroupId: "g", Kind: "claude", Prompt: "old", Cwd: "/tmp",
		Overlap: pb.Schedule_OVERLAP_RUN_BOTH, KeepRuns: 3, GiveUpMinutes: 30, AllowedTools: []string{"Bash(git:*)"},
		Runs: []*pb.ScheduleRun{{Id: "r"}}, NextRunUnixMs: 5}
	s, err := scheduleFromFields(base, []string{" Nightly ", "command", "make test", "~/x", "weekends 22:00", "never"})
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if s.Id != "sc1" || s.Name != "Nightly" || s.Kind != "command" || s.Prompt != "make test" || s.Cwd != home+"/x" ||
		!slices.Equal(s.Weekdays, []int32{0, 6}) || s.MinuteOfDay != 22*60 || s.Notify != pb.Schedule_NOTIFY_NEVER {
		t.Errorf("%v", s)
	}
	if s.Overlap != pb.Schedule_OVERLAP_RUN_BOTH || s.KeepRuns != 3 || s.GiveUpMinutes != 30 || len(s.AllowedTools) != 1 || s.GroupId != "g" {
		t.Errorf("advanced fields lost: %v", s)
	}
	if s.Runs != nil || s.NextRunUnixMs != 0 || len(base.Runs) != 1 {
		t.Errorf("daemon's fields sent back, or base changed: %v / %v", s, base)
	}
	for _, v := range [][]string{
		{"", "bash", "x", "/tmp", "daily 9:00", ""},
		{"", "claude", " ", "/tmp", "daily 9:00", ""},
		{"", "claude", "x", "", "daily 9:00", ""},
		{"", "claude", "x", "/tmp", "daily", ""},
		{"", "claude", "x", "/tmp", "daily 9:00", "loudly"},
	} {
		if _, err := scheduleFromFields(&pb.Schedule{}, v); err == nil {
			t.Errorf("%q: no error", v)
		}
	}
}

func TestApplySchedules(t *testing.T) {
	a, _ := paletteApp()
	a.apply(&pb.StateUpdate{Full: true, Schedules: []*pb.Schedule{{Id: "a", Name: "b"}, {Id: "b", Name: "A"}}})
	if l := a.scheduleList(); len(l) != 2 || l[0].Id != "b" {
		t.Fatalf("%v", l)
	}
	a.apply(&pb.StateUpdate{Schedules: []*pb.Schedule{{Id: "a", Name: "a2"}}, DeletedScheduleIds: []string{"b"}})
	if l := a.scheduleList(); len(l) != 1 || l[0].Name != "a2" {
		t.Fatalf("%v", l)
	}
	a.apply(&pb.StateUpdate{Full: true})
	if len(a.scheduleList()) != 0 {
		t.Fatal("full update kept old schedules")
	}
}

func scheduleApp() *App {
	a := &App{w: 120, h: 34, scr: NewScreen(120, 34), th: newTheme(RGB{30, 30, 30}, RGB{220, 220, 220}),
		anim: newSprings(), actions: make(chan func(), 64), tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{}}
	now := time.Now()
	a.groups = []*pb.Group{{Id: "g", Name: "Work", Folder: "/tmp/work", TabIds: []string{"t1"}}}
	a.tabs["t1"] = &pb.Tab{Id: "t1", GroupId: "g", Name: "Morning briefing", Layout: &pb.Layout{Node: &pb.Layout_PaneId{PaneId: "p1"}}}
	a.panes["p1"] = &pb.Pane{Id: "p1", TabId: "t1", ScheduleId: "sc1", ScheduleRunId: "r1"}
	a.schedules = map[string]*pb.Schedule{
		"sc1": {Id: "sc1", Name: "Morning briefing", GroupId: "g", GroupName: "Work", Kind: "claude", Prompt: "/morning-briefing",
			Cwd: "/tmp/work", Weekdays: []int32{1, 2, 3, 4, 5}, MinuteOfDay: 8*60 + 30, NextRunUnixMs: now.Add(3 * time.Hour).UnixMilli(),
			CreatedBy: "You", Runs: []*pb.ScheduleRun{
				{Id: "r1", Status: pb.ScheduleRun_STATUS_READY, StartedUnixMs: now.Add(-2 * time.Hour).UnixMilli(), TabId: "t1", PaneId: "p1", Note: "All caught up"},
				{Id: "r0", Status: pb.ScheduleRun_STATUS_MISSED, DueUnixMs: now.Add(-26 * time.Hour).UnixMilli(), Note: "The computer was asleep or off", TabClosed: true},
			}},
		"sc2": {Id: "sc2", Name: "Hourly tests", GroupName: "CI", Kind: "command", Prompt: "make test", Cwd: "/tmp",
			Cron: "0 * * * *", Paused: true, Runs: []*pb.ScheduleRun{{Id: "x", Status: pb.ScheduleRun_STATUS_FAILED, ExitCode: 2}}},
	}
	return a
}

func TestScheduleListDraws(t *testing.T) {
	animations = false
	defer func() { animations = true }()
	a := scheduleApp()
	a.openSchedules("")
	a.overlay.draw(a)
	out := screenText(a.scr)
	t.Log("\n" + out)
	for _, want := range []string{"Scheduled Tasks", "2 tasks", "Hourly tests", "Morning briefing", "CI", "Work",
		"cron 0 * * * *", "weekdays at 08:30", "paused", "failed (2)", "✓ ready",
		"Command in /tmp: make test", "r Run Now", "p Resume"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}

	// Down: the morning briefing, with its runs.
	a.overlay.key(a, keysOf([]byte("\x1b[B")))
	a.scr.Clear()
	a.overlay.draw(a)
	out = screenText(a.scr)
	for _, want := range []string{"Claude Code in /tmp/work: /morning-briefing", "made by You", "2 hours ago", "All caught up",
		"● open", "missed", "asleep or off", "p Pause", "s Skip Next"} {
		if !strings.Contains(out, want) {
			t.Errorf("after ↓, missing %q", want)
		}
	}

	// Enter moves into its runs; down to the missed one, whose tab is gone.
	v := a.overlay.(*scheduleView)
	a.overlay.key(a, keysOf([]byte("\r\x1b[B\r")))
	if v.runSel != 1 || a.overlay != v || a.toast != "That run's tab is closed" {
		t.Errorf("runSel %d, overlay %T, toast %q", v.runSel, a.overlay, a.toast)
	}
	// ← goes back to the tasks; ⌫ asks before deleting, and Cancel comes back.
	a.overlay.key(a, keysOf([]byte("\x1b[D")))
	if v.runSel != -1 {
		t.Errorf("runSel %d", v.runSel)
	}
	a.overlay.key(a, keysOf([]byte{0x7f}))
	d, ok := a.overlay.(*dialog)
	if !ok || !strings.Contains(d.title, "Morning briefing") {
		t.Fatalf("%T %+v", a.overlay, a.overlay)
	}
	a.overlay.key(a, keysOf([]byte("\x1b")))
	if back, ok := a.overlay.(*scheduleView); !ok || back.selID != "sc1" {
		t.Errorf("after Cancel: %T", a.overlay)
	}
}

func TestScheduleDialogShowsMistakes(t *testing.T) {
	animations = false
	defer func() { animations = true }()
	a := scheduleApp()
	a.openNewSchedule("g", "", "claude")
	d := a.overlay.(*dialog)
	if d.fields[3].value != "/tmp/work" || d.fields[4].value != "weekdays 09:00" || d.fields[1].value != "claude" {
		t.Errorf("prefilled %q %q %q", d.fields[1].value, d.fields[3].value, d.fields[4].value)
	}
	d.fields[2].value = "Summarise my inbox"
	d.fields[4].value = "weekdays 25:00"
	d.finish(a, true)
	d2, ok := a.overlay.(*dialog)
	if !ok || !strings.Contains(d2.errText, "can't read") || d2.fields[2].value != "Summarise my inbox" {
		t.Fatalf("%T %+v", a.overlay, a.overlay)
	}
	d2.draw(a)
	out := screenText(a.scr)
	t.Log("\n" + out)
	for _, want := range []string{"New Scheduled Task", "Runs as a new tab in Work.", "⚠ can't read", "More options in the Mac app"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestNeedsMenuOffersTheSchedule(t *testing.T) {
	a := scheduleApp()
	a.attention = []*pb.AttentionItem{{Id: "at", PaneId: "p1", State: pb.AgentState_AGENT_STATE_NEEDS_INPUT, Summary: "Bash: git push"}}
	labels := func() []string {
		var out []string
		for _, it := range a.overlay.(*menu).items {
			out = append(out, it.label)
		}
		return out
	}
	a.needsMenu("at", 1, 1)
	if l := labels(); !slices.Contains(l, "Always Allow for This Schedule") || !slices.Contains(l, "Allow") || !slices.Contains(l, "Deny") {
		t.Errorf("%v", l)
	}
	// A command's run, a pane that isn't a run, or a question: no remembering.
	a.schedules["sc1"].Kind = "command"
	a.needsMenu("at", 1, 1)
	if l := labels(); slices.Contains(l, "Always Allow for This Schedule") {
		t.Errorf("%v", l)
	}
	a.attention[0].Question = true
	a.needsMenu("at", 1, 1)
	if l := labels(); slices.Contains(l, "Allow") {
		t.Errorf("%v", l)
	}
}

func TestRunTabsWearAClock(t *testing.T) {
	a := scheduleApp()
	if got := a.tabDetail(a.tabs["t1"]); got != "◷" {
		t.Errorf("%q", got)
	}
	a.panes["p1"].ScheduleId = ""
	if got := a.tabDetail(a.tabs["t1"]); got != "" {
		t.Errorf("%q", got)
	}
}

// A schedule's whole request shows in its menu, however long: a menu
// taller than the terminal scrolls (keys, page keys, the wheel), so its
// actions at the end are always reachable.
func TestLongGrantMenuScrolls(t *testing.T) {
	a := scheduleApp()
	a.h = 20
	a.scr = NewScreen(a.w, a.h)
	long := "Claude in Work ▸ Assistant wants to schedule “Morning briefing”: Claude Code, weekdays at 08:30, in ~/Developer/work (allowed: " +
		strings.Repeat("mcp__claude_ai_Slack__slack_read_thread, ", 60) + "Read): /morning-briefing scheduled"
	a.grants = []*pb.GrantRequest{{Id: "gr1", Action: "schedule", Headline: long}}
	a.grantMenu("gr1", 0, 0)
	m, ok := a.overlay.(*menu)
	if !ok {
		t.Fatal("no menu")
	}
	text := ""
	for _, it := range m.items {
		if it.disabled {
			text += it.label + " "
		}
	}
	if !strings.Contains(text, "/morning-briefing scheduled") || strings.Contains(text, "…") {
		t.Fatalf("the request was cut: %q", text)
	}
	if m.rows(a) >= len(m.items) || m.frame(a).h > a.h {
		t.Fatalf("frame %v for %d items on %d rows", m.frame(a), len(m.items), a.h)
	}
	// Down to the first action scrolls it into view.
	m.key(a, []key{{name: "down"}})
	if m.sel < 0 || m.items[m.sel].disabled || m.sel < m.top || m.sel >= m.top+m.rows(a) {
		t.Fatalf("selected %d, showing %d+%d", m.sel, m.top, m.rows(a))
	}
	m.key(a, []key{{name: "pgup"}})
	top := m.top
	r := m.frame(a)
	m.mouse(a, mouseEv{x: r.x + 2, y: r.y + 2, wheel: 1})
	if m.top != top+1 {
		t.Fatalf("wheel: top %d, was %d", m.top, top)
	}
	m.draw(a) // draws scrolled, without panicking
}
