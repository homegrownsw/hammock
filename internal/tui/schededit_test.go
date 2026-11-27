package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// automationApp is scheduleApp on a Mac with the app open, and an automation
// with a trigger of most kinds, conditions and a continued session (test
// data only).
func automationApp() (*App, *pb.Schedule) {
	a := scheduleApp()
	a.h = 60
	a.scr = NewScreen(a.w, a.h)
	a.computers = &pb.Computers{List: []*pb.Computer{{UserId: "us_me", Name: "Studio", Os: "macos", ThisComputer: true, Notifies: false}}}
	now := time.Now()
	sc := &pb.Schedule{Id: "sc3", Name: "Fix failing CI", GroupId: "g", GroupName: "Work", Kind: "claude", Prompt: "CI failed.\nFind out why and fix it.",
		Cwd: "/tmp/work", Timing: pb.Schedule_TIMING_EVENTS, CooldownSeconds: proto.Int32(300), Session: pb.Schedule_SESSION_OWN, Overlap: pb.Schedule_OVERLAP_WAIT,
		AllowedTools: []string{"Bash(git push:*)", "Edit"},
		Events: []*pb.EventTrigger{
			{Id: "tr1", Kind: &pb.EventTrigger_Github{Github: &pb.GitHubTrigger{Event: pb.GitHubTrigger_EVENT_CI_FAILED, Repo: "acme/widgets"}},
				CheckedUnixMs: now.Add(-2 * time.Minute).UnixMilli()},
			{Id: "tr2", Kind: &pb.EventTrigger_Mac{Mac: &pb.MacTrigger{Event: pb.MacTrigger_EVENT_WIFI_JOINED, Name: "Office"}}, Error: "Needs Hammock open"},
			{Id: "tr3", Kind: &pb.EventTrigger_Check{Check: &pb.CommandCheckTrigger{Command: "make test", EveryMinutes: 5, LastResult: "Passed"}},
				CheckedUnixMs: now.Add(-time.Minute).UnixMilli(), Held: "waiting for its cooldown"},
		},
		OnlyIf: []*pb.Condition{{Kind: pb.Condition_KIND_DAYS, Weekdays: []int32{1, 2, 3, 4, 5}}},
	}
	a.schedules["sc3"] = sc
	return a, sc
}

func drawn(a *App) string {
	a.scr.Clear()
	if a.under != nil && a.under != a.overlay {
		a.under.draw(a)
	}
	a.overlay.draw(a)
	return screenText(a.scr)
}

func TestScheduleEditorDraws(t *testing.T) {
	animations = false
	defer func() { animations = true }()
	a, sc := automationApp()
	a.openScheduleEditor(sc, nil)
	out := drawn(a)
	t.Log("\n" + out)
	for _, want := range []string{
		"Edit “Fix failing CI”", "Name", "Work", "Claude Code", "Continue this automation's session",
		"WHEN", "CI fails on my pull request or branch (acme/widgets)", "Last checked 2 min ago · never fired",
		"The Mac joins the Wi-Fi “Office”", "Needs Hammock open",
		"“make test” starts failing, checked every 5 minutes", "Passed · last checked 1 min ago · never fired · waiting for its cooldown",
		"+ Add Trigger…", "At most once every", "5 minutes",
		"ONLY IF", "On weekdays", "+ Add Condition…",
		"PROMPT", "CI failed.", "Find out why and fix it.",
		"OPTIONS", "Wait for it to finish", "Give up after", "4 hours waiting for you", "When it's ready", "2 tools",
		"↵ Change", "a Add Trigger", "s Save",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Continuing a session: no Keep the last.
	if strings.Contains(out, "Keep the last") {
		t.Error("Keep the last shown for a continued session")
	}
}

// Add Trigger is the Mac's menu: its sections, a source's events in a
// submenu that Esc leaves for the sections, and an event with something to
// fill in opens its trigger's menu.
func TestAddTriggerMenu(t *testing.T) {
	animations = false
	defer func() { animations = true }()
	a, _ := automationApp()
	a.openNewSchedule("g", "", "claude")
	ed := a.overlay.(*schedEditor)
	drawn(a)
	a.overlay.key(a, keysOf([]byte("a")))
	m, ok := a.overlay.(*menu)
	if !ok || a.under != ed {
		t.Fatalf("%T, under %T", a.overlay, a.under)
	}
	var labels []string
	for _, it := range m.items {
		labels = append(labels, strings.TrimSpace(it.label))
	}
	got := strings.Join(labels, "|")
	for _, want := range []string{"HAMMOCK|Agents|Commands|Log Lines|Files", "CODE|GitHub|Command Check…|URL Check…", "MESSAGES|Slack",
		"THIS MAC|Disk and Battery|Away and Back|Apps Open or Quit|Drives and Displays|Wi-Fi",
		"TIME AND PLACE|Schedule…|Once…|Calendar|Places", "OTHER|Shortcuts|Webhook"} {
		if !strings.Contains(got, want) {
			t.Errorf("menu %q lacks %q", got, want)
		}
	}
	// The new automation has a schedule: Schedule… is there but can't be chosen.
	if i := slices.Index(labels, "Schedule…"); i < 0 || !m.items[i].disabled {
		t.Errorf("Schedule… should be disabled")
	}
	out := drawn(a)
	t.Log("\n" + out)
	if !strings.Contains(out, "New Automation") || !strings.Contains(out, "HAMMOCK") {
		t.Errorf("the editor isn't under its menu:\n%s", out)
	}

	choose := func(label string) {
		t.Helper()
		m := a.overlay.(*menu)
		i := slices.IndexFunc(m.items, func(it menuItem) bool { return strings.TrimSpace(it.label) == label })
		if i < 0 {
			t.Fatalf("no %q in %T", label, a.overlay)
		}
		m.sel = i
		m.key(a, keysOf([]byte("\r")))
	}
	choose("GitHub")
	choose("My Pull Request Is Merged")
	if a.overlay != ed || len(ed.s.Events) != 1 || ed.s.Events[0].GetGithub().GetEvent() != pb.GitHubTrigger_EVENT_MERGED {
		t.Fatalf("after adding: %T %v", a.overlay, ed.s.Events)
	}
	if l := ed.selected(); l == nil || l.id != "trigger:0" {
		t.Errorf("the new trigger isn't selected: %+v", l)
	}

	// Esc from a source's events goes back to the sections, and from them to the editor.
	a.overlay.key(a, keysOf([]byte("a")))
	choose("Agents")
	a.overlay.key(a, keysOf([]byte("\x1b")))
	if m, ok := a.overlay.(*menu); !ok || strings.TrimSpace(m.items[0].label) != "HAMMOCK" {
		t.Fatalf("Esc didn't go back to the sections: %T", a.overlay)
	}
	a.overlay.key(a, keysOf([]byte("\x1b")))
	if a.overlay != ed || a.under != nil {
		t.Fatalf("Esc didn't go back to the editor: %T", a.overlay)
	}

	// A labelled trigger asks which label, in its menu; set, it says so.
	a.overlay.key(a, keysOf([]byte("a")))
	choose("GitHub")
	choose("An Issue or Pull Request Is Labelled…")
	sheet := a.overlay.(*menu)
	t.Log("\n" + drawn(a))
	if !slices.ContainsFunc(sheet.items, func(it menuItem) bool { return it.label == "Which label?" }) {
		t.Errorf("the trigger's menu doesn't ask which label")
	}
	choose("Label")
	d, ok := a.overlay.(*dialog)
	if !ok {
		t.Fatalf("Label: %T", a.overlay)
	}
	d.finish(a, true) // empty: asks again
	d, ok = a.overlay.(*dialog)
	if !ok || d.errText == "" {
		t.Fatalf("an empty label was taken: %T", a.overlay)
	}
	d.fields[0].value = "needs triage"
	d.finish(a, true)
	if _, ok := a.overlay.(*menu); !ok {
		t.Fatalf("after the label: %T, want its menu again", a.overlay)
	}
	if g := ed.s.Events[1].GetGithub(); g.Label != "needs triage" {
		t.Errorf("label %q", g.Label)
	}
	a.overlay.key(a, keysOf([]byte("\x1b")))
	if a.overlay != ed {
		t.Fatalf("%T", a.overlay)
	}
	out = drawn(a)
	if !strings.Contains(out, "An issue or pull request is labelled “needs triage”") || strings.Contains(out, "Which label?") {
		t.Errorf("\n%s", out)
	}

	// ⌫ removes the selected trigger.
	ed.selectID(a, "trigger:0")
	a.overlay.key(a, keysOf([]byte{0x7f}))
	if len(ed.s.Events) != 1 || ed.s.Events[0].GetGithub().GetEvent() != pb.GitHubTrigger_EVENT_LABELED {
		t.Errorf("after ⌫: %v", ed.s.Events)
	}
}

// Off a Mac, This Mac, the calendar, places and Shortcuts aren't offered.
func TestAddTriggerOffAMac(t *testing.T) {
	a, _ := automationApp()
	a.computers.List[0].Os = "linux"
	a.openNewSchedule("g", "", "claude")
	drawn(a)
	a.overlay.key(a, keysOf([]byte("a")))
	for _, it := range a.overlay.(*menu).items {
		switch strings.TrimSpace(it.label) {
		case "THIS MAC", "Wi-Fi", "Calendar", "Places", "Shortcuts":
			t.Errorf("%q offered on Linux", it.label)
		}
	}
}

func TestScheduleEditorSessionAndKind(t *testing.T) {
	a, sc := automationApp()
	a.openScheduleEditor(sc, nil)
	ed := a.overlay.(*schedEditor)
	ed.setKind("command")
	if ed.s.Session != pb.Schedule_SESSION_NEW || ed.s.Overlap != pb.Schedule_OVERLAP_SKIP {
		t.Errorf("a command still continues a session: %v %v", ed.s.Session, ed.s.Overlap)
	}
	if sc.Kind != "claude" {
		t.Error("the editor changed the automation itself")
	}
	ed.setKind("claude")
	ed.setSession(pb.Schedule_SESSION_OWN, "", "")
	if ed.s.Overlap != pb.Schedule_OVERLAP_WAIT {
		t.Errorf("continuing doesn't wait: %v", ed.s.Overlap)
	}
}

// Continue a Session… lists open tabs running its agent; one picked is
// where runs go, with a warning when its tab allows more. Esc goes back.
func TestScheduleEditorPicksASession(t *testing.T) {
	a, _ := automationApp()
	a.panes["p1"].Agent = &pb.Agent{Harness: "claude", SessionId: "sess-1"}
	a.panes["p1"].Command = []string{"claude", "--dangerously-skip-permissions"}
	a.openNewSchedule("g", "", "claude")
	ed := a.overlay.(*schedEditor)
	ed.pickASession(a)
	p, ok := a.overlay.(*palette)
	if !ok || a.under != ed || len(p.list.items) != 1 || p.list.items[0].title != "Morning briefing" {
		t.Fatalf("%T %+v", a.overlay, a.overlay)
	}
	p.key(a, keysOf([]byte("\x1b")))
	if a.overlay != ed {
		t.Fatalf("Esc: %T", a.overlay)
	}
	ed.pickASession(a)
	drawn(a)
	a.overlay.(*palette).key(a, keysOf([]byte("\r")))
	if a.overlay != ed || ed.s.Session != pb.Schedule_SESSION_PICKED || ed.s.SessionId != "sess-1" || ed.s.Overlap != pb.Schedule_OVERLAP_WAIT ||
		!strings.Contains(ed.s.SessionWarning, "--dangerously-skip-permissions") {
		t.Errorf("%T %v %q %v %q", a.overlay, ed.s.Session, ed.s.SessionId, ed.s.Overlap, ed.s.SessionWarning)
	}
}

// Save says what's missing before it sends anything; Esc with changes asks
// first, and Cancel there comes back.
func TestScheduleEditorSaveAndCancel(t *testing.T) {
	animations = false
	defer func() { animations = true }()
	a, _ := automationApp()
	a.openNewSchedule("g", "", "claude")
	ed := a.overlay.(*schedEditor)
	a.overlay.key(a, keysOf([]byte("s")))
	if ed.err != "Give the agent a prompt" {
		t.Errorf("err %q", ed.err)
	}
	ed.s.Prompt = "Summarise my inbox"
	ed.s.Events = append(ed.s.Events, &pb.EventTrigger{Kind: &pb.EventTrigger_Url{Url: &pb.UrlCheckTrigger{}}})
	ed.save(a)
	if !strings.Contains(ed.err, "Which URL") {
		t.Errorf("err %q", ed.err)
	}
	if out := drawn(a); !strings.Contains(out, "⚠ ") {
		t.Errorf("the problem isn't shown:\n%s", out)
	}
	a.overlay.key(a, keysOf([]byte("\x1b")))
	d, ok := a.overlay.(*dialog)
	if !ok || !strings.Contains(d.title, "Discard") {
		t.Fatalf("%T", a.overlay)
	}
	d.finish(a, false)
	if a.overlay != ed {
		t.Fatalf("Cancel: %T", a.overlay)
	}
	a.overlay.key(a, keysOf([]byte("\x1b")))
	a.overlay.(*dialog).finish(a, true)
	if a.overlay != nil {
		t.Errorf("Discard: %T", a.overlay)
	}
}

// Only If: added from its menu, a condition with something to say opens its
// own menu; a bad value is asked again.
func TestScheduleEditorConditions(t *testing.T) {
	animations = false
	defer func() { animations = true }()
	a, sc := automationApp()
	a.openScheduleEditor(sc, nil)
	ed := a.overlay.(*schedEditor)
	drawn(a)
	a.overlay.key(a, keysOf([]byte("o")))
	m := a.overlay.(*menu)
	i := slices.IndexFunc(m.items, func(it menuItem) bool { return it.label == "Between Times…" })
	m.sel = i
	m.key(a, keysOf([]byte("\r")))
	sheet := a.overlay.(*menu)
	j := slices.IndexFunc(sheet.items, func(it menuItem) bool { return strings.HasPrefix(it.label, "Between") })
	sheet.sel = j
	sheet.key(a, keysOf([]byte("\r")))
	d := a.overlay.(*dialog)
	d.fields[0].value = "25:00-26:00"
	d.finish(a, true)
	d = a.overlay.(*dialog)
	if d.errText == "" {
		t.Fatal("bad hours taken")
	}
	d.fields[0].value = "22:00-06:30"
	d.finish(a, true)
	c := ed.s.OnlyIf[1]
	if c.FromMinute != 22*60 || c.ToMinute != 6*60+30 {
		t.Errorf("%v", c)
	}
	a.overlay.key(a, keysOf([]byte("\x1b")))
	if out := drawn(a); !strings.Contains(out, "Between 22:00 and 06:30") {
		t.Errorf("\n%s", out)
	}
}

// Every trigger kind's menu opens, and every one of its fields' choices or
// prompts with it.
func TestEveryTriggerSheetOpens(t *testing.T) {
	animations = false
	defer func() { animations = true }()
	a, sc := automationApp()
	a.openScheduleEditor(sc, nil)
	ed := a.overlay.(*schedEditor)
	drawn(a)
	for _, sec := range ed.addTriggerSections(a) {
		for _, src := range sec.sources {
			for _, e := range src.events {
				tr := e.make()
				ed.s.Events = append(ed.s.Events, tr)
				for _, p := range ed.triggerProps(a, tr) {
					if p.choices != nil {
						for _, c := range p.choices {
							c.set()
						}
					}
				}
			}
		}
	}
	for _, k := range []pb.SlackTrigger_Event{pb.SlackTrigger_EVENT_MESSAGE, pb.SlackTrigger_EVENT_REACTION, pb.SlackTrigger_EVENT_DM} {
		ed.s.Events = append(ed.s.Events, &pb.EventTrigger{Kind: &pb.EventTrigger_Slack{Slack: &pb.SlackTrigger{Event: k}}})
	}
	ed.s.Events = append(ed.s.Events, &pb.EventTrigger{Kind: &pb.EventTrigger_Webhook{Webhook: &pb.WebhookTrigger{}}},
		&pb.EventTrigger{Kind: &pb.EventTrigger_Url{Url: &pb.UrlCheckTrigger{On: pb.UrlCheckTrigger_ON_CONTAINS}}})
	for i := range ed.s.Events {
		ed.triggerSheet(a, i)
		if _, ok := a.overlay.(*menu); !ok {
			t.Fatalf("trigger %d: %T", i, a.overlay)
		}
		drawn(a)
	}
	ed.reopen(a)
	drawn(a) // all of them, scrolled
}

// A tab's agent that may do more than the automation says so, as the
// daemon tells it (agents.WiderThan): its options, with their values too.
func TestSessionWarning(t *testing.T) {
	s := &pb.Schedule{Kind: "claude", AllowedTools: []string{"Read"}}
	for _, tc := range []struct {
		cmd  []string
		want string
	}{
		{[]string{"claude"}, ""},
		{[]string{"claude", "--dangerously-skip-permissions"}, "--dangerously-skip-permissions"},
		{[]string{"claude", "--permission-mode", "acceptEdits"}, "--permission-mode acceptEdits"},
		{[]string{"claude", "--allowedTools", "Read,Bash"}, "--allowedTools Bash"},
		{[]string{"claude", "--allowedTools", "Read"}, ""},
	} {
		got := sessionWarning(&pb.Pane{Command: tc.cmd}, s, "claude")
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, " with "+tc.want+",") {
			t.Errorf("%v: %q, want %q", tc.cmd, got, tc.want)
		}
	}
}
