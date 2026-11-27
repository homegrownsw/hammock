package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
)

// ---------- the automation editor ----------
//
// New Automation and Edit, as the Mac's Automations window edits one, in
// the TUI's terms: one sheet of rows read top to bottom (what runs where,
// When, Only If, the prompt, Options), each changed with ↵ through a menu
// of choices or a prompt, and saved with s. Add Trigger is the Mac's menu,
// in its sections, each source a submenu of its events; a trigger's own
// menu sets its fields. The words are schedtrig's, as hmk and the apps say
// them. Triggers aren't merged into one row per kind as the Mac's are:
// each is a row of its own.

type schedEditor struct {
	orig *pb.Schedule // as it was ("" id: a new one)
	s    *pb.Schedule // as edited
	back func()       // after Save or Cancel: what it came from (the list), or nil
	sel  int          // the selected row, of those that can be
	top  int          // the first line shown
	err  string       // what stopped the last Save
	open *spring

	slack      *pb.SlackLink // Slack on its computer, once asked (slackAsked)
	slackAsked bool

	lines    []edLine // as last drawn
	anchorY  int      // the selected row's screen line, for its menu
	anchorX  int
	listY    int // where the lines start on screen, and how many show
	listRows int
}

// edLine is a line of the editor: a section's heading, a row that can be
// selected, or a line under one (its status, what it still needs).
type edLine struct {
	head   string
	label  string // a setting's name ("Notify me"); "" for a sentence (a trigger)
	text   string
	glyph  string
	tone   int // 0 plain, 1 dim, 2 needs something (orange), 3 wrong (red), 4 a link (+ Add)
	sub    bool
	indent int    // a line under a row: where it starts (that row's text's column, less the frame's)
	id     string // what it is, to select it again: "trigger:N", "add-trigger", "add-condition"
	edit   func()
	remove func()
}

func (l edLine) selectable() bool { return l.head == "" && !l.sub && l.edit != nil }

// openScheduleEditor makes (base without an id) or edits a schedule; back
// runs after Save or Cancel (the list it came from).
func (a *App) openScheduleEditor(base *pb.Schedule, back func()) {
	ed := &schedEditor{orig: base, s: cloneSchedule(base), back: back, open: newSpring(gentle)}
	ed.s.Runs = nil
	ed.open.from(0)
	ed.open.to(1)
	a.overlay, a.under = ed, nil
	if slices.ContainsFunc(ed.s.Events, func(t *pb.EventTrigger) bool { return t.GetSlack() != nil }) {
		ed.askSlack(a, nil)
	}
}

// openEditAutomation is Edit Automation…: the automations, to search, and
// the editor on the one chosen.
func (a *App) openEditAutomation() {
	var items []paletteItem
	for _, s := range a.scheduleList() {
		items = append(items, paletteItem{id: "schedule:" + s.Id, title: cmp.Or(s.Name, "Untitled"), sub: scheduleWhen(s) + " · " + a.scheduleGroup(s),
			glyph: a.kindGlyph(s.Kind), run: func() { a.openScheduleEditor(s, nil) }})
	}
	a.openPaletteList(&paletteList{placeholder: "Edit an automation…", items: items})
}

// reopen comes back to the editor from one of its menus or prompts.
func (ed *schedEditor) reopen(a *App) {
	a.overlay, a.under = ed, nil
	a.dirty = true
}

// child marks what was just opened over the editor as its: the editor
// shows under it.
func (ed *schedEditor) child(a *App) { a.under = ed }

func (ed *schedEditor) isCommand() bool { return ed.s.Kind == "command" }

// continues: its runs go on in one session.
func (ed *schedEditor) continues() bool { return ed.s.Session != pb.Schedule_SESSION_NEW }

// canContinue: its agent's sessions resume, so runs can carry one on.
func canContinue(kind string) bool {
	ag := agents.Get(kind)
	return kind != "command" && ag != nil && ag.Resume != nil
}

func (ed *schedEditor) changed() bool {
	o := cloneSchedule(ed.orig)
	o.Runs = nil
	return !proto.Equal(o, ed.s)
}

// ---------- its computer ----------

// thisComputer: id ("" this one) is the computer the TUI's daemon is.
func (a *App) thisComputer(id string) bool {
	if id == "" {
		return true
	}
	c := a.computer(id)
	return c == nil || c.ThisComputer
}

// schedOnMac: s runs on a Mac (This Mac triggers, the calendar, places and
// Shortcuts are only there).
func (a *App) schedOnMac(s *pb.Schedule) bool {
	for _, c := range a.linkedList() {
		if s.ComputerId == "" && c.ThisComputer || s.ComputerId != "" && c.UserId == s.ComputerId {
			if c.Os != "" {
				return c.Os == "macos"
			}
		}
	}
	return runtime.GOOS == "darwin"
}

// scheduleGroups are the Groups s's runs can open in: yours on its computer.
func (a *App) scheduleGroups(computerID string) []*pb.Group {
	var out []*pb.Group
	for _, g := range a.groups {
		if a.thisComputer(computerID) {
			if canSchedule(g) {
				out = append(out, g)
			}
		} else if c := a.computerOf(g); c != nil && c.UserId == computerID {
			out = append(out, g)
		}
	}
	return out
}

// ---------- the lines ----------

func capFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// buildLines lays out the editor: what runs where, When, Only If, the
// prompt, Options.
func (ed *schedEditor) buildLines(a *App) []edLine {
	s := ed.s
	now := time.Now()
	var out []edLine
	row := func(label, text string, edit func()) {
		out = append(out, edLine{label: label, text: text, edit: edit})
	}
	note := func(text string, tone int) {
		if text == "" {
			return
		}
		indent := 2 // under the row's text
		for i := len(out) - 1; i >= 0; i-- {
			if !out[i].sub {
				switch {
				case out[i].label != "":
					indent += ed.labelW(out) + 2
				case out[i].glyph != "":
					indent += 2
				}
				break
			}
		}
		out = append(out, edLine{text: text, tone: tone, sub: true, indent: indent})
	}
	head := func(h string) { out = append(out, edLine{head: " "}, edLine{head: h}) }

	if s.Name != "" {
		row("Name", s.Name, func() { ed.askName(a) })
	} else { // named after its prompt when saved, as the Mac's says
		name := "Untitled"
		if p := strings.TrimSpace(s.Prompt); p != "" {
			name = truncate(strings.SplitN(p, "\n", 2)[0], 40)
		}
		out = append(out, edLine{label: "Name", text: name, tone: 1, edit: func() { ed.askName(a) }})
	}
	row("Group", ed.groupWords(a), func() { ed.pickGroup(a) })
	row("Runs", kindName(cmp.Or(s.Kind, "claude")), func() { ed.pickKind(a) })
	row("Folder", shortPath(s.Cwd), func() { ed.askFolder(a) })
	row("Each run", ed.sessionWords(), func() { ed.pickSession(a) })
	if s.SessionWarning != "" && ed.continues() {
		note("⚠ "+s.SessionWarning, 2)
	}

	head("When")
	switch s.Timing {
	case pb.Schedule_TIMING_REPEAT:
		out = append(out, edLine{glyph: "◷", text: capFirst(scheduleTimes(s)), edit: func() { ed.askTimes(a, nil) },
			remove: func() { s.Timing = pb.Schedule_TIMING_EVENTS }})
	case pb.Schedule_TIMING_ONCE:
		out = append(out, edLine{glyph: "◷", text: "Once, on " + time.UnixMilli(s.OnceUnixMs).Format("Mon 2 Jan 2006 15:04"),
			edit: func() { ed.askOnce(a, nil) }, remove: func() { s.Timing = pb.Schedule_TIMING_EVENTS }})
	}
	for i, t := range s.Events {
		out = append(out, edLine{glyph: triggerGlyph(t), text: schedtrig.Describe(t, a.triggerName), id: "trigger:" + strconv.Itoa(i),
			edit: func() { ed.triggerSheet(a, i) }, remove: func() { ed.removeTrigger(i) }})
		for _, l := range ed.triggerNotes(a, t, now) {
			note(l.text, l.tone)
		}
	}
	out = append(out, edLine{glyph: "+", text: "Add Trigger…", tone: 4, id: "add-trigger", edit: func() { ed.addTriggerMenu(a) }})
	if s.Timing == pb.Schedule_TIMING_EVENTS && len(s.Events) == 0 {
		note("Add a trigger or a schedule: until then it only runs when you run it", 2)
	}
	if len(s.Events) > 0 {
		row("At most once every", cooldownWords(schedtrig.Cooldown(s)), func() { ed.pickCooldown(a) })
	}

	head("Only If")
	for i, c := range s.OnlyIf {
		out = append(out, edLine{glyph: "◇", text: capFirst(schedtrig.DescribeCondition(c)),
			edit: func() { ed.conditionSheet(a, i) }, remove: func() { s.OnlyIf = slices.Delete(s.OnlyIf, i, i+1) }})
		note(schedtrig.MissingCondition(c), 2)
		if (c.Kind == pb.Condition_KIND_WIFI || c.Kind == pb.Condition_KIND_PLACE) && !a.appOpenThere(s) {
			note("Needs Hammock open", 2)
		}
	}
	out = append(out, edLine{glyph: "+", text: "Add Condition…", tone: 4, id: "add-condition", edit: func() { ed.addConditionMenu(a) }})

	if ed.isCommand() {
		head("Command")
	} else {
		head("Prompt")
	}
	prompt := strings.Split(strings.TrimRight(s.Prompt, "\n"), "\n")
	if strings.TrimSpace(s.Prompt) == "" {
		out = append(out, edLine{text: "None yet", tone: 1, edit: func() { ed.askPrompt(a) }})
	} else {
		out = append(out, edLine{text: prompt[0], edit: func() { ed.askPrompt(a) }})
		for i, l := range prompt[1:] {
			if i == 3 {
				note("… "+plural(len(prompt)-4, "more line"), 1)
				break
			}
			note(cmp.Or(l, " "), 0)
		}
	}

	head("Options")
	row("If still running", overlapWords(s.Overlap), func() { ed.pickOverlap(a) })
	if !ed.isCommand() {
		row("Give up after", minutesWords(giveUpMinutes(s))+" waiting for you", func() { ed.pickGiveUp(a) })
	}
	if !ed.continues() {
		row("Keep the last", plural(keptRuns(s), "run"), func() { ed.askKeep(a) })
	}
	row("Notify me", notifyText(effectiveNotify(s)), func() { ed.pickNotify(a) })
	if s.Kind == "claude" {
		tools := "None"
		if n := len(s.AllowedTools); n > 0 {
			tools = plural(n, "tool")
		}
		var edit func()
		if len(s.AllowedTools) > 0 {
			edit = func() { ed.toolsMenu(a) }
		}
		l := edLine{label: "Remembered tools", text: tools, edit: edit}
		if edit == nil {
			l.tone = 1
		}
		out = append(out, l)
	}
	if ed.err != "" {
		out = append(out, edLine{head: " "}, edLine{text: "⚠ " + ed.err, tone: 3, sub: true, indent: 0})
	}
	return out
}

// edNote is a line under a trigger.
type edNote struct {
	text string
	tone int
}

// triggerNotes are the lines under a trigger: what it still needs, why it
// can't fire, how it's been doing; for Slack, the workspace.
func (ed *schedEditor) triggerNotes(a *App, t *pb.EventTrigger, now time.Time) []edNote {
	var out []edNote
	if m := schedtrig.Missing(t); m != "" {
		out = append(out, edNote{m, 2})
	}
	if e := t.Error; e != "" {
		tone := 3
		if e == "Needs Hammock open" {
			tone = 2
		}
		out = append(out, edNote{e, tone})
	} else if m := t.GetMac(); m != nil && schedtrig.MacAppWatched(m.Event) && t.Id == "" && !a.appOpenThere(ed.s) {
		out = append(out, edNote{"Needs Hammock open", 2})
	}
	if t.GetSlack() != nil {
		switch {
		case !ed.slackAsked:
		case ed.slack == nil:
			out = append(out, edNote{"Slack isn't connected: hmk slack connect" + ed.onFlag(a), 2})
		case ed.slack.Problem != "":
			out = append(out, edNote{"Slack (" + ed.slack.Team + "): " + ed.slack.Problem, 3})
		default:
			out = append(out, edNote{"Slack: " + ed.slack.Team, 1})
		}
	}
	line := schedtrig.Status(t, now)
	if r := cmp.Or(t.GetCheck().GetLastResult(), t.GetUrl().GetLastResult()); r != "" && line != "" {
		line = r + " · " + strings.ToLower(line[:1]) + line[1:]
	}
	if line != "" {
		out = append(out, edNote{line, 1})
	}
	return out
}

// appOpenThere: the Hammock app is open on s's computer (Wi-Fi, apps,
// displays and places are watched by it). Not known (an older daemon):
// taken as open.
func (a *App) appOpenThere(s *pb.Schedule) bool {
	for _, c := range a.linkedList() {
		if s.ComputerId == "" && c.ThisComputer || c.UserId == s.ComputerId {
			return c.Notifies
		}
	}
	return true
}

// onFlag is hmk's --on for the automation's computer, when it's another.
func (ed *schedEditor) onFlag(a *App) string {
	if c := a.computer(ed.s.ComputerId); c != nil && !c.ThisComputer {
		return " --on " + c.Name
	}
	return ""
}

func triggerGlyph(t *pb.EventTrigger) string {
	switch schedtrig.Kind(t) {
	case "github":
		return "⎇"
	case "agent":
		return "✦"
	case "exit", "check":
		return "❯"
	case "log":
		return "≡"
	case "files":
		return "▤"
	case "calendar":
		return "▦"
	case "mac":
		if schedtrig.IsPlace(t.GetMac().Event) {
			return "⌖"
		}
		return "⌘"
	case "url":
		return "◎"
	case "slack":
		return "#"
	case "webhook":
		return "↯"
	case "shortcut":
		return "⚡"
	}
	return "•"
}

// ---------- words ----------

func (ed *schedEditor) groupWords(a *App) string {
	if g := a.group(ed.s.GroupId); g != nil {
		return iconTitle(g, a.groupTitle(g))
	}
	if ed.s.GroupName != "" {
		return ed.s.GroupName + " (closed: the next run brings it back)"
	}
	return "The folder's Group, or a new one"
}

func (ed *schedEditor) sessionWords() string {
	switch ed.s.Session {
	case pb.Schedule_SESSION_OWN:
		return "Continue this automation's session"
	case pb.Schedule_SESSION_PICKED:
		return "Continue " + cmp.Or(ed.s.SessionName, "a session you picked")
	}
	if ed.isCommand() && ed.s.CloseOnSuccess {
		return "New tab each run, closed if it succeeds"
	}
	return "New tab each run"
}

func overlapWords(o pb.Schedule_Overlap) string {
	switch o {
	case pb.Schedule_OVERLAP_STOP_PREVIOUS:
		return "Stop the last run, start fresh"
	case pb.Schedule_OVERLAP_RUN_BOTH:
		return "Run both"
	case pb.Schedule_OVERLAP_WAIT:
		return "Wait for it to finish"
	}
	return "Skip this run"
}

func effectiveNotify(s *pb.Schedule) pb.Schedule_Notify {
	if s.Notify != pb.Schedule_NOTIFY_DEFAULT {
		return s.Notify
	}
	if s.Kind == "command" {
		return pb.Schedule_NOTIFY_PROBLEMS
	}
	return pb.Schedule_NOTIFY_READY
}

func notifyText(n pb.Schedule_Notify) string {
	switch n {
	case pb.Schedule_NOTIFY_PROBLEMS:
		return "Only about problems"
	case pb.Schedule_NOTIFY_NEVER:
		return "Never"
	}
	return "When it's ready"
}

func giveUpMinutes(s *pb.Schedule) int {
	if s.GiveUpMinutes > 0 {
		return int(s.GiveUpMinutes)
	}
	return 240
}

func keptRuns(s *pb.Schedule) int {
	if s.KeepRuns > 0 {
		return int(s.KeepRuns)
	}
	return 7
}

// minutesWords: "30 minutes", "4 hours", "1 day".
func minutesWords(m int) string {
	switch {
	case m > 0 && m%(24*60) == 0:
		return plural(m/(24*60), "day")
	case m > 0 && m%60 == 0:
		return plural(m/60, "hour")
	}
	return plural(m, "minute")
}

// cooldownWords is a cooldown as the editor's choice says it: "No limit",
// "10 minutes".
func cooldownWords(secs int) string {
	if secs <= 0 {
		return "No limit"
	}
	return strings.TrimSuffix(schedtrig.CooldownWords(secs), " cooldown")
}

// ---------- changing it ----------

// edChoice is one of a menu's choices.
type edChoice struct {
	label string
	on    bool // the one it is now
	set   func()
}

// pick opens a menu of choices over the editor; choosing one sets it and
// goes on to then (nil: back to the editor), Esc goes back to back (nil:
// the editor).
func (ed *schedEditor) pick(a *App, title string, cs []edChoice, back func()) {
	if back == nil {
		back = func() { ed.reopen(a) }
	}
	items := []menuItem{{label: title, disabled: true}, {sep: true}}
	for _, c := range cs {
		hint := ""
		if c.on {
			hint = "✓"
		}
		items = append(items, menuItem{label: c.label, hint: hint, run: func() { c.set(); back() }})
	}
	a.openMenuBack(ed.anchorX, ed.anchorY, items, back)
	// The one it is now starts selected.
	if m, ok := a.overlay.(*menu); ok {
		if i := slices.IndexFunc(m.items, func(it menuItem) bool { return it.hint == "✓" }); i >= 0 {
			m.sel = i
			m.reveal(a)
		}
	}
	ed.child(a)
}

// ask opens a prompt over the editor for one value; set says what's wrong
// with it (it asks again), else it's kept and the prompt goes back.
func (ed *schedEditor) ask(a *App, title, label, value string, lines []string, set func(string) error, back func()) {
	ed.askAgain(a, title, label, value, lines, "", set, back)
}

func (ed *schedEditor) askAgain(a *App, title, label, value string, lines []string, errText string, set func(string) error, back func()) {
	if back == nil {
		back = func() { ed.reopen(a) }
	}
	a.openDialog(&dialog{title: title, okLabel: "OK", fields: []*field{{label: label, value: value}}, lines: lines, errText: errText,
		cancel: back,
		submit: func(v []string) {
			if err := set(strings.TrimSpace(v[0])); err != nil {
				ed.askAgain(a, title, label, v[0], lines, err.Error(), set, back)
				return
			}
			back()
		}})
	ed.child(a)
}

func (ed *schedEditor) askName(a *App) {
	ed.ask(a, "Name", "Name", ed.s.Name, nil, func(v string) error { ed.s.Name = v; return nil }, nil)
}

func (ed *schedEditor) askFolder(a *App) {
	ed.ask(a, "Folder", "Folder", shortPath(ed.s.Cwd), []string{"Where it runs: a folder on its computer."}, func(v string) error {
		if v == "" {
			return errors.New("give the folder it runs in")
		}
		ed.s.Cwd = expandTilde(v)
		return nil
	}, nil)
}

// The prompt's lines, in a one-line field: each line break shows as ⏎.
const lineMark = " ⏎ "

func (ed *schedEditor) askPrompt(a *App) {
	title, label := "Prompt", "Prompt"
	lines := []string{"What the agent is asked each run. ⏎ marks a new line."}
	if ed.isCommand() {
		title, label = "Command", "Command"
		lines = []string{"The command line each run runs, in its folder."}
	}
	ed.ask(a, title, label, strings.ReplaceAll(ed.s.Prompt, "\n", lineMark), lines, func(v string) error {
		v = strings.ReplaceAll(strings.ReplaceAll(v, lineMark, "\n"), "⏎", "\n")
		if strings.TrimSpace(v) == "" {
			if ed.isCommand() {
				return errors.New("give the command to run")
			}
			return errors.New("give the agent a prompt")
		}
		ed.s.Prompt = v
		return nil
	}, nil)
}

func (ed *schedEditor) pickGroup(a *App) {
	var cs []edChoice
	for _, g := range a.scheduleGroups(ed.s.ComputerId) {
		cs = append(cs, edChoice{label: iconTitle(g, a.groupTitle(g)), on: g.Id == ed.s.GroupId, set: func() {
			ed.s.GroupId, ed.s.GroupName, ed.s.GroupIcon = g.Id, g.Name, g.Icon
			if ed.s.Cwd == "" {
				ed.s.Cwd = g.Folder
			}
		}})
	}
	cs = append(cs, edChoice{label: "The folder's Group, or a new one", on: ed.s.GroupId == "" && ed.s.GroupName == "", set: func() {
		ed.s.GroupId, ed.s.GroupName, ed.s.GroupIcon = "", "", ""
	}})
	ed.pick(a, "Its runs open in", cs, nil)
}

func (ed *schedEditor) pickKind(a *App) {
	var cs []edChoice
	kinds := scheduleKinds()
	if ed.s.Kind != "" && ed.s.Kind != "command" && !slices.Contains(kinds, ed.s.Kind) {
		kinds = append(kinds, ed.s.Kind)
	}
	for _, k := range append(kinds, "command") {
		cs = append(cs, edChoice{label: kindName(k), on: cmp.Or(ed.s.Kind, "claude") == k, set: func() { ed.setKind(k) }})
	}
	ed.pick(a, "Runs", cs, nil)
}

// setKind changes what it runs; one that can't carry a session on makes
// each run a new tab again.
func (ed *schedEditor) setKind(k string) {
	ed.s.Kind = k
	if !canContinue(k) && ed.continues() {
		ed.setSession(pb.Schedule_SESSION_NEW, "", "")
	}
}

func (ed *schedEditor) setSession(mode pb.Schedule_Session, id, name string) {
	if mode != pb.Schedule_SESSION_NEW && (ed.s.Session == pb.Schedule_SESSION_NEW || ed.s.Overlap != pb.Schedule_OVERLAP_SKIP) {
		ed.s.Overlap = pb.Schedule_OVERLAP_WAIT // carrying a session on waits for the last run
	}
	if mode == pb.Schedule_SESSION_NEW && ed.s.Overlap == pb.Schedule_OVERLAP_WAIT {
		ed.s.Overlap = pb.Schedule_OVERLAP_SKIP
	}
	ed.s.Session, ed.s.SessionId, ed.s.SessionName, ed.s.SessionWarning = mode, id, name, ""
}

func (ed *schedEditor) pickSession(a *App) {
	s := ed.s
	if ed.isCommand() {
		ed.pick(a, "Each run", []edChoice{
			{label: "New tab each run", on: !s.CloseOnSuccess, set: func() { s.CloseOnSuccess = false }},
			{label: "New tab each run, closed if it succeeds", on: s.CloseOnSuccess, set: func() { s.CloseOnSuccess = true }},
		}, nil)
		return
	}
	cs := []edChoice{{label: "New tab each run", on: s.Session == pb.Schedule_SESSION_NEW, set: func() { ed.setSession(pb.Schedule_SESSION_NEW, "", "") }}}
	if canContinue(s.Kind) {
		cs = append(cs, edChoice{label: "Continue this automation's session", on: s.Session == pb.Schedule_SESSION_OWN, set: func() {
			if s.Session != pb.Schedule_SESSION_OWN {
				id, name := "", ""
				if ed.orig.Session == pb.Schedule_SESSION_OWN {
					id, name = ed.orig.SessionId, ed.orig.SessionName
				}
				ed.setSession(pb.Schedule_SESSION_OWN, id, name)
			}
		}})
		if s.Session == pb.Schedule_SESSION_PICKED {
			cs = append(cs, edChoice{label: "Continue " + cmp.Or(s.SessionName, "a session you picked"), on: true, set: func() {}})
		}
	}
	back := func() { ed.reopen(a) }
	items := []menuItem{{label: "Each run", disabled: true}, {sep: true}}
	for _, c := range cs {
		hint := ""
		if c.on {
			hint = "✓"
		}
		items = append(items, menuItem{label: c.label, hint: hint, run: func() { c.set(); back() }})
	}
	if canContinue(s.Kind) {
		items = append(items, menuItem{sep: true}, menuItem{label: "Continue a Session…", hint: "›", run: func() { ed.pickASession(a) }})
	} else {
		items = append(items, menuItem{sep: true}, menuItem{label: kindName(s.Kind) + "'s sessions don't resume", disabled: true})
	}
	a.openMenuBack(ed.anchorX, ed.anchorY, items, back)
	ed.child(a)
}

// pickASession is Continue a Session…: open tabs whose agent is its kind,
// then that agent's recent sessions, on its computer, to search.
func (ed *schedEditor) pickASession(a *App) {
	kind := ed.s.Kind
	pick := func(id, name string) {
		ed.setSession(pb.Schedule_SESSION_PICKED, id, name)
		ed.reopen(a)
	}
	var items []paletteItem
	open := map[string]bool{}
	for _, g := range a.scheduleGroups(ed.s.ComputerId) {
		for _, tid := range g.TabIds {
			t := a.tabs[tid]
			if t == nil {
				continue
			}
			for _, pid := range tabIDs(t) {
				p := a.panes[pid]
				if p == nil || p.Agent == nil || p.Agent.Harness != kind || p.Agent.SessionId == "" || open[p.Agent.SessionId] {
					continue
				}
				open[p.Agent.SessionId] = true
				name := a.tabTitle(t)
				id := p.Agent.SessionId
				warning := sessionWarning(p, ed.s, kind)
				items = append(items, paletteItem{id: "tab:" + pid, title: name, sub: "Open tab · " + iconTitle(g, a.groupTitle(g)),
					glyph: a.harnessGlyph(kind), run: func() { pick(id, name); ed.s.SessionWarning = warning }})
			}
		}
	}
	l := &paletteList{placeholder: "Continue a session: an open tab or a recent session…", items: items, cancel: func() { ed.reopen(a) }}
	a.openPaletteList(l)
	ed.child(a)
	if a.c == nil {
		return
	}
	req := &pb.ListSessions{ComputerId: ed.s.ComputerId}
	if a.thisComputer(ed.s.ComputerId) {
		req.ComputerId = ""
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: req}})
		a.actions <- func() {
			if err != nil {
				return
			}
			for _, si := range r.GetSessionList().GetSessions() {
				if si.Harness != kind || open[si.Id] {
					continue
				}
				name := cmp.Or(si.Title, oneLine(si.FirstPrompt), si.Id[:min(8, len(si.Id))])
				id := si.Id
				l.items = append(l.items, paletteItem{id: "session:" + id, title: truncate(name, 60), sub: shortPath(si.Cwd) + " · " + relTime(si.UpdatedUnixMs, time.Now()),
					glyph: a.harnessGlyph(kind), run: func() { pick(id, name) }})
			}
			a.dirty = true
		}
	}()
}

// sessionWarning is why sending runs to p's agent needs care, as the Mac
// says it ("" when it doesn't): its tab lets it do more than an automation
// would (agents.WiderThan, as the daemon tells, which says so too once
// it's saved).
func sessionWarning(p *pb.Pane, s *pb.Schedule, kind string) string {
	if w := agents.WiderThan(p.Command, s); w != "" {
		return "Its tab runs " + kindName(kind) + " with " + w + ", so runs sent there can do more than this automation allows"
	}
	return ""
}

func (ed *schedEditor) pickOverlap(a *App) {
	s := ed.s
	os := []pb.Schedule_Overlap{pb.Schedule_OVERLAP_SKIP, pb.Schedule_OVERLAP_STOP_PREVIOUS, pb.Schedule_OVERLAP_RUN_BOTH}
	if ed.continues() {
		os = []pb.Schedule_Overlap{pb.Schedule_OVERLAP_WAIT, pb.Schedule_OVERLAP_SKIP}
	}
	var cs []edChoice
	for _, o := range os {
		cs = append(cs, edChoice{label: overlapWords(o), on: s.Overlap == o, set: func() { s.Overlap = o }})
	}
	ed.pick(a, "If the last run is still going", cs, nil)
}

// minuteChoices are the choices of a duration, with the one it has now.
func minuteChoices(base []int, cur int, words func(int) string, set func(int)) []edChoice {
	if !slices.Contains(base, cur) {
		base = append(slices.Clone(base), cur)
		slices.Sort(base)
	}
	var cs []edChoice
	for _, m := range base {
		cs = append(cs, edChoice{label: words(m), on: m == cur, set: func() { set(m) }})
	}
	return cs
}

func (ed *schedEditor) pickGiveUp(a *App) {
	ed.pick(a, "Give up after waiting for you", minuteChoices([]int{60, 120, 240, 480, 1440}, giveUpMinutes(ed.s), minutesWords,
		func(m int) { ed.s.GiveUpMinutes = int32(m) }), nil)
}

func (ed *schedEditor) askKeep(a *App) {
	ed.ask(a, "Keep the Last", "Runs", strconv.Itoa(keptRuns(ed.s)), []string{"Older runs' tabs close as new ones open (not ones you've typed in)."}, func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 30 {
			return errors.New("a number of runs, 1 to 30")
		}
		ed.s.KeepRuns = int32(n)
		return nil
	}, nil)
}

func (ed *schedEditor) pickNotify(a *App) {
	var cs []edChoice
	for _, n := range []pb.Schedule_Notify{pb.Schedule_NOTIFY_READY, pb.Schedule_NOTIFY_PROBLEMS, pb.Schedule_NOTIFY_NEVER} {
		cs = append(cs, edChoice{label: notifyText(n), on: effectiveNotify(ed.s) == n, set: func() { ed.s.Notify = n }})
	}
	ed.pick(a, "Notify me", cs, nil)
}

func (ed *schedEditor) pickCooldown(a *App) {
	ed.pick(a, "Runs its triggers start, at most once every", minuteChoices([]int{0, 60, 300, 600, 900, 1800, 3600, 86400, 604800, 2592000},
		schedtrig.Cooldown(ed.s), cooldownWords, func(secs int) { ed.s.CooldownSeconds = proto.Int32(int32(secs)) }), nil)
}

// toolsMenu is Remembered tools: each one, to forget.
func (ed *schedEditor) toolsMenu(a *App) {
	back := func() {
		if len(ed.s.AllowedTools) == 0 {
			ed.reopen(a)
			return
		}
		ed.toolsMenu(a)
	}
	items := []menuItem{{label: "Allowed in every run (↵ forgets one)", disabled: true}, {sep: true}}
	for i, t := range ed.s.AllowedTools {
		items = append(items, menuItem{label: truncate(t, 70), run: func() {
			ed.s.AllowedTools = slices.Delete(slices.Clone(ed.s.AllowedTools), i, i+1)
			back()
		}})
	}
	items = append(items, menuItem{sep: true}, menuItem{label: "Forget All", danger: true, run: func() { ed.s.AllowedTools = nil; ed.reopen(a) }})
	a.openMenuBack(ed.anchorX, ed.anchorY, items, func() { ed.reopen(a) })
	ed.child(a)
}

// askTimes asks for its days and time (or a cron line); then (nil: the
// editor) after.
func (ed *schedEditor) askTimes(a *App, back func()) {
	s := ed.s
	value := whenField(s)
	if s.Timing != pb.Schedule_TIMING_REPEAT {
		value = "weekdays 09:00"
	}
	ed.ask(a, "Schedule", "When", value, []string{
		"every day / weekdays / weekends / mon,wed,fri HH:MM,",
		"or a cron line: cron M H D M W (this computer's time).",
	}, func(v string) error {
		days, minute, cron, err := parseWhen(v)
		if err != nil {
			return err
		}
		s.Timing, s.Weekdays, s.MinuteOfDay, s.Cron = pb.Schedule_TIMING_REPEAT, days, minute, cron
		return nil
	}, back)
}

// askOnce asks for the day and time it runs once.
func (ed *schedEditor) askOnce(a *App, back func()) {
	s := ed.s
	at := time.UnixMilli(s.OnceUnixMs)
	if s.Timing != pb.Schedule_TIMING_ONCE || s.OnceUnixMs == 0 {
		y, m, d := time.Now().AddDate(0, 0, 1).Date()
		at = time.Date(y, m, d, 9, 0, 0, 0, time.Local)
	}
	ed.ask(a, "Once", "On", at.Format("2006-01-02 15:04"), []string{"The day and time it runs, as YYYY-MM-DD HH:MM."}, func(v string) error {
		timing, once, err := parseTiming("once " + v)
		if err != nil {
			return err
		}
		s.Timing, s.OnceUnixMs = timing, once
		return nil
	}, back)
}

// ---------- Add Trigger ----------

// edSource is a source in Add Trigger: a submenu of its events, or (no
// events) one that adds itself.
type edSource struct {
	label  string
	events []edEvent
	add    func() // an entry of its own (Command Check…)
	off    bool
}

// edEvent is one of a source's events: the trigger it adds.
type edEvent struct {
	label string
	make  func() *pb.EventTrigger
}

func gh(e pb.GitHubTrigger_Event) func() *pb.EventTrigger {
	return func() *pb.EventTrigger {
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Github{Github: &pb.GitHubTrigger{Event: e}}}
	}
}

func mac(m *pb.MacTrigger) func() *pb.EventTrigger {
	return func() *pb.EventTrigger {
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Mac{Mac: proto.CloneOf(m)}}
	}
}

func cal(c *pb.CalendarTrigger) func() *pb.EventTrigger {
	return func() *pb.EventTrigger {
		return &pb.EventTrigger{Kind: &pb.EventTrigger_Calendar{Calendar: proto.CloneOf(c)}}
	}
}

// githubEventTitles are GitHub's events as Add Trigger offers them (the
// Mac's words).
var githubEventTitles = []struct {
	e     pb.GitHubTrigger_Event
	title string
}{
	{pb.GitHubTrigger_EVENT_CI_FAILED, "CI Fails on My Pull Request"},
	{pb.GitHubTrigger_EVENT_REVIEW_REQUESTED, "My Review Is Requested"},
	{pb.GitHubTrigger_EVENT_REVIEWED, "My Pull Request Gets a Review or Comment"},
	{pb.GitHubTrigger_EVENT_APPROVED, "My Pull Request Is Approved"},
	{pb.GitHubTrigger_EVENT_MERGED, "My Pull Request Is Merged"},
	{pb.GitHubTrigger_EVENT_LABELED, "An Issue or Pull Request Is Labelled…"},
	{pb.GitHubTrigger_EVENT_ASSIGNED, "Something Is Assigned to Me"},
	{pb.GitHubTrigger_EVENT_ISSUE_OPENED, "A New Issue Is Opened"},
	{pb.GitHubTrigger_EVENT_COMMIT, "A New Commit Is Pushed"},
	{pb.GitHubTrigger_EVENT_RELEASE, "A New Release or Tag"},
	{pb.GitHubTrigger_EVENT_WORKFLOW, "A Workflow Finishes…"},
	{pb.GitHubTrigger_EVENT_NOTIFICATION, "I Get a GitHub Notification"},
}

var slackEventTitles = []struct {
	e     pb.SlackTrigger_Event
	title string
}{
	{pb.SlackTrigger_EVENT_MESSAGE, "A Message in a Channel…"},
	{pb.SlackTrigger_EVENT_MENTION, "I'm Mentioned"},
	{pb.SlackTrigger_EVENT_DM, "A Direct Message"},
	{pb.SlackTrigger_EVENT_THREAD_REPLY, "A Reply in My Thread"},
	{pb.SlackTrigger_EVENT_REACTION, "A Reaction…"},
}

// addTriggerSections is Add Trigger, the Mac's menu: its sections, and in
// each the sources.
func (ed *schedEditor) addTriggerSections(a *App) []struct {
	name    string
	sources []edSource
} {
	onMac := a.schedOnMac(ed.s)
	var github []edEvent
	for _, g := range githubEventTitles {
		github = append(github, edEvent{g.title, gh(g.e)})
	}
	type section = struct {
		name    string
		sources []edSource
	}
	return []section{
		{"Hammock", []edSource{
			{label: "Agents", events: []edEvent{
				{"An Agent Finishes", func() *pb.EventTrigger {
					return &pb.EventTrigger{Kind: &pb.EventTrigger_Agent{Agent: &pb.AgentTrigger{Finished: true}}}
				}},
				{"An Agent Needs You", func() *pb.EventTrigger {
					return &pb.EventTrigger{Kind: &pb.EventTrigger_Agent{Agent: &pb.AgentTrigger{NeedsYou: true}}}
				}},
			}},
			{label: "Commands", events: []edEvent{{"A Command Fails", func() *pb.EventTrigger {
				return &pb.EventTrigger{Kind: &pb.EventTrigger_Exit{Exit: &pb.ExitTrigger{}}}
			}}}},
			{label: "Log Lines", events: []edEvent{{"A Log Line Matches…", func() *pb.EventTrigger {
				l := &pb.LogTrigger{}
				if ps := a.logPanes(ed.s.ComputerId); len(ps) > 0 {
					l.PaneId = ps[0].Id
				}
				return &pb.EventTrigger{Kind: &pb.EventTrigger_Log{Log: l}}
			}}}},
			{label: "Files", events: []edEvent{{"Files Change…", func() *pb.EventTrigger {
				return &pb.EventTrigger{Kind: &pb.EventTrigger_Files{Files: &pb.FilesTrigger{}}}
			}}}},
		}},
		{"Code", []edSource{
			{label: "GitHub", events: github},
			{label: "Command Check…", add: func() {
				ed.addTrigger(a, &pb.EventTrigger{Kind: &pb.EventTrigger_Check{Check: &pb.CommandCheckTrigger{EveryMinutes: 5}}}, true)
			}},
			{label: "URL Check…", add: func() {
				ed.addTrigger(a, &pb.EventTrigger{Kind: &pb.EventTrigger_Url{Url: &pb.UrlCheckTrigger{EveryMinutes: 5}}}, true)
			}},
		}},
		{"Messages", []edSource{
			{label: "Slack", add: func() { ed.slackMenu(a) }},
		}},
		{"This Mac", []edSource{
			{label: "Disk and Battery", off: !onMac, events: []edEvent{
				{"Disk Space Is Low…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_DISK_LOW, Percent: schedtrig.MacDiskPercentDefault})},
				{"Battery Is Low…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_BATTERY_LOW, Percent: schedtrig.MacBatteryPercentDefault})},
				{"Mac Is Plugged In", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLUGGED_IN})},
				{"Mac Is Unplugged", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_UNPLUGGED})},
			}},
			{label: "Away and Back", off: !onMac, events: []edEvent{
				{"I've Been Away…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_AWAY, Minutes: schedtrig.MacAwayDefault})},
				{"I'm Back…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_BACK, Minutes: schedtrig.MacBackDefault})},
				{"Mac Wakes…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_WAKE})},
			}},
			{label: "Apps Open or Quit", off: !onMac, events: []edEvent{
				{"An App Opens…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_APP_OPENED})},
				{"An App Quits…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_APP_QUIT})},
			}},
			{label: "Drives and Displays", off: !onMac, events: []edEvent{
				{"A Drive Is Connected…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_DRIVE})},
				{"A Display Is Connected…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_DISPLAY})},
			}},
			{label: "Wi-Fi", off: !onMac, events: []edEvent{
				{"Mac Joins a Network…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_WIFI_JOINED})},
				{"Mac Leaves a Network…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_WIFI_LEFT})},
			}},
		}},
		{"Time and Place", []edSource{
			{label: "Schedule…", off: ed.s.Timing == pb.Schedule_TIMING_REPEAT, add: func() { ed.askTimes(a, nil) }},
			{label: "Once…", off: ed.s.Timing == pb.Schedule_TIMING_ONCE, add: func() { ed.askOnce(a, nil) }},
			{label: "Calendar", off: !onMac, events: []edEvent{
				{"Before an Event…", cal(&pb.CalendarTrigger{MinutesBefore: 10})},
				{"When an Event Starts…", cal(&pb.CalendarTrigger{})},
				{"When an Event Ends…", cal(&pb.CalendarTrigger{When: pb.CalendarTrigger_WHEN_END})},
				{"When Free Time Begins…", cal(&pb.CalendarTrigger{When: pb.CalendarTrigger_WHEN_FREE, FreeMinutes: 30})},
			}},
			{label: "Places", off: !onMac, events: []edEvent{
				{"Mac Arrives at a Place…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLACE_ARRIVE})},
				{"Mac Leaves a Place…", mac(&pb.MacTrigger{Event: pb.MacTrigger_EVENT_PLACE_LEAVE})},
			}},
		}},
		{"Other", []edSource{
			{label: "Shortcuts", off: !onMac, add: func() {
				ed.addTrigger(a, &pb.EventTrigger{Kind: &pb.EventTrigger_Shortcut{Shortcut: &pb.ShortcutTrigger{}}}, false)
			}},
			{label: "Webhook", add: func() {
				ed.addTrigger(a, &pb.EventTrigger{Kind: &pb.EventTrigger_Webhook{Webhook: &pb.WebhookTrigger{}}}, false)
			}},
		}},
	}
}

// addTriggerMenu is Add Trigger: its sections, each source a submenu (›)
// of its events, or an entry that adds one (…).
func (ed *schedEditor) addTriggerMenu(a *App) {
	var items []menuItem
	for i, sec := range ed.addTriggerSections(a) {
		var rows []menuItem
		for _, src := range sec.sources {
			if src.off && src.events != nil {
				continue // not on this computer (This Mac on Linux)
			}
			if src.label == "Shortcuts" && src.off {
				continue
			}
			it := menuItem{label: "  " + src.label, disabled: src.off}
			if src.events != nil {
				it.hint = "›"
				it.run = func() { ed.eventsMenu(a, src) }
			} else {
				it.run = src.add
			}
			rows = append(rows, it)
		}
		if len(rows) == 0 {
			continue
		}
		if i > 0 {
			items = append(items, menuItem{sep: true})
		}
		items = append(items, menuItem{label: strings.ToUpper(sec.name), disabled: true})
		items = append(items, rows...)
	}
	a.openMenuBack(ed.anchorX, ed.anchorY, items, func() { ed.reopen(a) })
	ed.child(a)
}

// eventsMenu is a source's events; Esc goes back to Add Trigger.
func (ed *schedEditor) eventsMenu(a *App, src edSource) {
	items := []menuItem{{label: src.label, disabled: true}, {sep: true}}
	for _, e := range src.events {
		items = append(items, menuItem{label: e.label, run: func() { ed.addTrigger(a, e.make(), strings.HasSuffix(e.label, "…")) }})
	}
	a.openMenuBack(ed.anchorX+4, ed.anchorY+1, items, func() { ed.addTriggerMenu(a) })
	ed.child(a)
}

// addTrigger adds t, selects its row, and (more: it has something to fill
// in, the entries with …) opens its menu.
func (ed *schedEditor) addTrigger(a *App, t *pb.EventTrigger, more bool) {
	ed.s.Events = append(ed.s.Events, t)
	i := len(ed.s.Events) - 1
	ed.selectID(a, "trigger:"+strconv.Itoa(i))
	if more {
		ed.triggerSheet(a, i)
		return
	}
	ed.reopen(a)
}

// selectID selects the row whose id is id.
func (ed *schedEditor) selectID(a *App, id string) {
	ed.lines = ed.buildLines(a)
	for n, i := range ed.selectables() {
		if ed.lines[i].id == id {
			ed.sel = n
		}
	}
}

func (ed *schedEditor) removeTrigger(i int) {
	if i >= 0 && i < len(ed.s.Events) {
		ed.s.Events = slices.Delete(ed.s.Events, i, i+1)
	}
}

// slackMenu is Add Trigger ▸ Slack: its events when Slack is connected on
// the automation's computer; else where it is (another computer, to move
// here), or how to connect it.
func (ed *schedEditor) slackMenu(a *App) {
	ed.askSlack(a, func() {
		back := func() { ed.addTriggerMenu(a) }
		if ed.slack != nil {
			items := []menuItem{{label: "Slack (" + ed.slack.Team + ")", disabled: true}, {sep: true}}
			for _, e := range slackEventTitles {
				items = append(items, menuItem{label: e.title, run: func() {
					ed.addTrigger(a, &pb.EventTrigger{Kind: &pb.EventTrigger_Slack{Slack: &pb.SlackTrigger{Event: e.e}}}, strings.HasSuffix(e.title, "…"))
				}})
			}
			a.openMenuBack(ed.anchorX+4, ed.anchorY+1, items, back)
			ed.child(a)
			return
		}
		items := []menuItem{{label: "Slack isn't connected here", disabled: true}, {sep: true}}
		for _, c := range a.linkedList() {
			if c.Slack != nil && c.UserId != ed.s.ComputerId && !(ed.s.ComputerId == "" && c.ThisComputer) {
				from := c.UserId
				items = append(items, menuItem{label: "It's connected on " + c.Name + " (" + c.Slack.Team + ")", disabled: true},
					menuItem{label: "Move Slack Here", run: func() { ed.moveSlack(a, from) }}, menuItem{sep: true})
			}
		}
		items = append(items, menuItem{label: "To connect it, in a shell: hmk slack connect" + ed.onFlag(a), disabled: true})
		a.openMenuBack(ed.anchorX+4, ed.anchorY+1, items, back)
		ed.child(a)
	})
}

// askSlack asks the automation's computer how Slack is there, then runs
// then (nil: nothing more).
func (ed *schedEditor) askSlack(a *App, then func()) {
	if a.c == nil {
		ed.slackAsked = true
		if then != nil {
			then()
		}
		return
	}
	on := ed.s.ComputerId
	if a.thisComputer(on) {
		on = ""
	}
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SlackCommand{SlackCommand: &pb.SlackCommand{Action: pb.SlackCommand_ACTION_STATUS, ComputerId: on}}}, func(r *pb.ServerMessage) {
		ed.slack, ed.slackAsked = r.GetSlackReply().GetLink(), true
		a.dirty = true
		if then != nil && (a.overlay == ed || a.under == ed) {
			then()
		}
	})
}

// moveSlack takes Slack's connection from another computer of yours to the
// automation's, then offers Slack's events.
func (ed *schedEditor) moveSlack(a *App, from string) {
	ed.reopen(a)
	if a.c == nil {
		return
	}
	on := ed.s.ComputerId
	if a.thisComputer(on) {
		on = ""
	}
	a.flash("Moving Slack…")
	a.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SlackCommand{SlackCommand: &pb.SlackCommand{Action: pb.SlackCommand_ACTION_MOVE_HERE, ComputerId: on, FromComputerId: from}}}, func(r *pb.ServerMessage) {
		ed.slack, ed.slackAsked = r.GetSlackReply().GetLink(), true
		a.flash("Slack is connected here now")
		if a.overlay == ed {
			ed.slackMenu(a)
		}
	})
}

// ---------- a trigger's menu ----------

// edProp is one of a trigger's (or condition's) fields, as its menu shows
// it: a menu of choices, a prompt, a switch, or an action.
type edProp struct {
	label, value string
	choices      []edChoice
	ask          *edAsk
	toggle       func()
	run          func()
	info         bool // a line of words only
}

type edAsk struct {
	title, label, value string
	lines               []string
	set                 func(string) error
}

// sheet opens a menu of props about one thing (a trigger or a condition),
// headed by its words; Esc goes back to the editor.
func (ed *schedEditor) sheet(a *App, head []menuItem, props []edProp, removeLabel string, remove func(), again func()) {
	back := func() { ed.reopen(a) }
	items := append([]menuItem{}, head...)
	w := 0
	for _, p := range props {
		w = max(w, textWidth(p.label))
	}
	if len(props) > 0 {
		items = append(items, menuItem{sep: true})
	}
	for _, p := range props {
		if p.info {
			items = append(items, menuItem{label: p.label, disabled: true})
			continue
		}
		label := p.label
		if p.value != "" || p.toggle != nil {
			label += strings.Repeat(" ", w-textWidth(p.label)+2) + truncate(p.value, 50)
		}
		it := menuItem{label: label}
		switch {
		case p.choices != nil:
			it.hint, it.run = "›", func() { ed.pick(a, p.label, p.choices, again) }
		case p.ask != nil:
			q := p.ask
			it.hint, it.run = "…", func() { ed.ask(a, q.title, q.label, q.value, q.lines, q.set, again) }
		case p.toggle != nil:
			it.hint, it.run = "↵", func() { p.toggle(); again() }
		case p.run != nil:
			it.run = p.run
		default:
			it.disabled = true
		}
		items = append(items, it)
	}
	items = append(items, menuItem{sep: true}, menuItem{label: removeLabel, hint: "⌫", danger: true, run: func() { remove(); back() }})
	a.openMenuBack(ed.anchorX, ed.anchorY, items, back)
	ed.child(a)
}

// triggerSheet is trigger i's menu: its words and status, its fields, and
// Remove Trigger.
func (ed *schedEditor) triggerSheet(a *App, i int) {
	if i < 0 || i >= len(ed.s.Events) {
		ed.reopen(a)
		return
	}
	t := ed.s.Events[i]
	head := []menuItem{{label: truncate(schedtrig.Describe(t, a.triggerName), 76), disabled: true}}
	for _, n := range ed.triggerNotes(a, t, time.Now()) {
		head = append(head, menuItem{label: truncate(n.text, 76), disabled: true})
	}
	ed.sheet(a, head, ed.triggerProps(a, t), "Remove Trigger", func() { ed.removeTrigger(i) }, func() { ed.triggerSheet(a, i) })
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// textAsk is a prompt for one of a trigger's words; check (nil: any)
// says what's wrong with one.
func textAsk(title, value string, lines []string, set func(string), check func(string) error) *edAsk {
	return &edAsk{title: title, label: title, value: value, lines: lines, set: func(v string) error {
		if check != nil {
			if err := check(v); err != nil {
				return err
			}
		}
		set(v)
		return nil
	}}
}

func required(what string) func(string) error {
	return func(v string) error {
		if v == "" {
			return errors.New(what)
		}
		return nil
	}
}

// triggerProps are a trigger's fields, by its kind (the Mac's sentence's).
func (ed *schedEditor) triggerProps(a *App, t *pb.EventTrigger) []edProp {
	switch k := t.GetKind().(type) {
	case *pb.EventTrigger_Github:
		return ed.githubProps(k.Github)
	case *pb.EventTrigger_Agent:
		ag := k.Agent
		what := func(f, n bool) edChoice {
			label := map[[2]bool]string{{true, false}: "Finishes", {false, true}: "Needs me", {true, true}: "Finishes or needs me"}[[2]bool{f, n}]
			return edChoice{label: label, on: ag.Finished == f && ag.NeedsYou == n, set: func() { ag.Finished, ag.NeedsYou = f, n }}
		}
		cs := []edChoice{what(true, false), what(false, true), what(true, true)}
		cur := ""
		for _, c := range cs {
			if c.on {
				cur = c.label
			}
		}
		return []edProp{{label: "An agent", value: cur, choices: cs}, a.whereProp(ed.s.ComputerId, &ag.PaneId, &ag.GroupId)}
	case *pb.EventTrigger_Exit:
		return []edProp{a.whereProp(ed.s.ComputerId, &k.Exit.PaneId, &k.Exit.GroupId)}
	case *pb.EventTrigger_Log:
		l := k.Log
		var cs []edChoice
		for _, p := range a.logPanes(ed.s.ComputerId) {
			cs = append(cs, edChoice{label: a.triggerName(p.Id), on: p.Id == l.PaneId, set: func() { l.PaneId = p.Id }})
		}
		pane := cmp.Or(a.triggerName(l.PaneId), "None")
		if l.PaneId != "" && a.panes[l.PaneId] == nil {
			pane = "A pane that's closed"
		}
		if len(cs) == 0 {
			cs = []edChoice{{label: "No log panes are open", set: func() {}}}
		}
		return []edProp{
			{label: "Log pane", value: pane, choices: cs},
			{label: "Matches", value: l.Pattern, ask: textAsk("Matches", l.Pattern, []string{"A regular expression; case matters only if it has a capital."},
				func(v string) { l.Pattern = v }, func(v string) error {
					if v == "" {
						return errors.New("what should the line match?")
					}
					_, err := schedtrig.CompileLog(v)
					return err
				})},
		}
	case *pb.EventTrigger_Files:
		f := k.Files
		return []edProp{
			{label: "Files", value: cmp.Or(f.Glob, "Any file"), ask: textAsk("Files", f.Glob, []string{"A glob, like *.swift or src/**/*.go; empty for any file."},
				func(v string) { f.Glob = v }, schedtrig.CheckGlob)},
			{label: "Folder", value: cmp.Or(shortPath(f.Folder), "Its folder"), ask: textAsk("Folder", shortPath(f.Folder), []string{"The folder watched; empty for the automation's own."},
				func(v string) { f.Folder = expandTilde(v) }, nil)},
		}
	case *pb.EventTrigger_Calendar:
		return ed.calendarProps(k.Calendar)
	case *pb.EventTrigger_Mac:
		return ed.macProps(a, k.Mac)
	case *pb.EventTrigger_Check:
		c := k.Check
		var ons []edChoice
		for _, o := range []struct {
			on   pb.CommandCheckTrigger_On
			text string
		}{{pb.CommandCheckTrigger_ON_FAIL, "Starts failing"}, {pb.CommandCheckTrigger_ON_CHANGE, "Changes its output"}, {pb.CommandCheckTrigger_ON_RECOVER, "Passes again"}} {
			ons = append(ons, edChoice{label: o.text, on: c.On == o.on, set: func() { c.On = o.on }})
		}
		return []edProp{
			{label: "Command", value: c.Command, ask: textAsk("Command", c.Command, []string{"Run in the automation's folder, without a pane."},
				func(v string) { c.Command = v }, required("which command should the check run?"))},
			{label: "Fires when it", value: choiceLabel(ons), choices: ons},
			{label: "Checked", value: schedtrig.EveryWords(schedtrig.CheckEvery(c)), choices: everyChoices(schedtrig.CheckEvery(c), func(m int) { c.EveryMinutes = int32(m) })},
			{label: "Time limit", value: secondsWords(int(schedtrig.CheckTimeout(c) / time.Second)), choices: minuteChoices([]int{10, 30, 60, 300, 900, 1800, 3600},
				int(c.TimeoutSeconds), secondsWords, func(s int) { c.TimeoutSeconds = int32(s) })},
		}
	case *pb.EventTrigger_Url:
		u := k.Url
		var ons []edChoice
		for _, o := range []struct {
			on   pb.UrlCheckTrigger_On
			text string
		}{{pb.UrlCheckTrigger_ON_FAIL, "Starts failing"}, {pb.UrlCheckTrigger_ON_CHANGE, "Changes"}, {pb.UrlCheckTrigger_ON_CONTAINS, "Starts containing a text"}} {
			ons = append(ons, edChoice{label: o.text, on: u.On == o.on, set: func() { u.On = o.on }})
		}
		props := []edProp{
			{label: "URL", value: u.Url, ask: textAsk("URL", u.Url, []string{"Failing: an answer other than 2xx, or none."},
				func(v string) { u.Url = v }, schedtrig.CheckURL)},
			{label: "Fires when it", value: choiceLabel(ons), choices: ons},
		}
		if u.On == pb.UrlCheckTrigger_ON_CONTAINS {
			props = append(props, edProp{label: "Text", value: u.Text, ask: textAsk("Text", u.Text, nil, func(v string) { u.Text = v }, required("what text should the page contain?"))})
		}
		return append(props, edProp{label: "Checked", value: schedtrig.EveryWords(schedtrig.URLEvery(u)), choices: everyChoices(schedtrig.URLEvery(u), func(m int) { u.EveryMinutes = int32(m) })})
	case *pb.EventTrigger_Slack:
		return ed.slackProps(k.Slack)
	case *pb.EventTrigger_Webhook:
		if ed.orig.Id == "" || t.Id == "" {
			return []edProp{{label: "Its URL is made when you save", info: true}}
		}
		return []edProp{{label: "Copy Webhook URL", run: func() { ed.reopen(a); a.copyWebhook(ed.orig) }}}
	case *pb.EventTrigger_Shortcut:
		return []edProp{{label: "Use Run Hammock Automation in Shortcuts, on this Mac or your iPhone", info: true}}
	}
	return nil
}

func choiceLabel(cs []edChoice) string {
	for _, c := range cs {
		if c.on {
			return c.label
		}
	}
	return ""
}

func everyChoices(cur int, set func(int)) []edChoice {
	return minuteChoices([]int{1, 5, 10, 15, 30, 60, 180, 360, 720, 1440}, cur, func(m int) string { return capFirst(schedtrig.EveryWords(m)) }, set)
}

// secondsWords: "30 seconds", "5 minutes"; 0 is the default, a minute.
func secondsWords(s int) string {
	switch {
	case s <= 0:
		return "1 minute"
	case s%60 == 0:
		return minutesWords(s / 60)
	}
	return plural(s, "second")
}

func (ed *schedEditor) githubProps(g *pb.GitHubTrigger) []edProp {
	var cs []edChoice
	for _, e := range githubEventTitles {
		cs = append(cs, edChoice{label: strings.TrimSuffix(e.title, "…"), on: g.Event == e.e, set: func() { g.Event = e.e }})
	}
	props := []edProp{{label: "When", value: choiceLabel(cs), choices: cs}}
	switch g.Event {
	case pb.GitHubTrigger_EVENT_LABELED:
		props = append(props, edProp{label: "Label", value: g.Label, ask: textAsk("Label", g.Label, nil, func(v string) { g.Label = v }, required("which label?"))})
	case pb.GitHubTrigger_EVENT_COMMIT:
		props = append(props, edProp{label: "Branch", value: cmp.Or(g.Branch, "The default branch"),
			ask: textAsk("Branch", g.Branch, []string{"Empty for the default branch."}, func(v string) { g.Branch = v }, nil)})
	case pb.GitHubTrigger_EVENT_RELEASE:
		props = append(props, edProp{label: "Tags", value: cmp.Or(g.Tags, "Any tag"),
			ask: textAsk("Tags", g.Tags, []string{"A pattern, like v*; empty for any."}, func(v string) { g.Tags = v }, schedtrig.CheckTags)})
	case pb.GitHubTrigger_EVENT_WORKFLOW:
		props = append(props, edProp{label: "Workflow", value: g.Workflow,
			ask: textAsk("Workflow", g.Workflow, []string{"Its name, or its file's (ci.yml)."}, func(v string) { g.Workflow = v }, required("which workflow?"))})
	}
	repo := cmp.Or(g.Watch, "The folder's")
	if g.Watch == "" && g.Repo != "" {
		repo = "The folder's (" + g.Repo + ")"
	}
	return append(props, edProp{label: "Repository", value: repo, ask: textAsk("Repository", g.Watch,
		[]string{"owner/name, or a github.com link; empty for the folder's."},
		func(v string) { g.Watch = schedtrig.GitHubRepo(v) }, func(v string) error {
			if v != "" && schedtrig.GitHubRepo(v) == "" {
				return errors.New("a repository as owner/name, or its github.com link")
			}
			return nil
		})})
}

func (ed *schedEditor) calendarProps(c *pb.CalendarTrigger) []edProp {
	when := "start"
	switch {
	case c.When == pb.CalendarTrigger_WHEN_END:
		when = "end"
	case c.When == pb.CalendarTrigger_WHEN_FREE:
		when = "free"
	case c.MinutesBefore > 0:
		when = "before"
	}
	whens := []edChoice{
		{label: "Before an event", on: when == "before", set: func() {
			c.When = pb.CalendarTrigger_WHEN_START
			if c.MinutesBefore <= 0 {
				c.MinutesBefore = 10
			}
		}},
		{label: "An event starts", on: when == "start", set: func() { c.When, c.MinutesBefore = pb.CalendarTrigger_WHEN_START, 0 }},
		{label: "An event ends", on: when == "end", set: func() { c.When, c.MinutesBefore = pb.CalendarTrigger_WHEN_END, 0 }},
		{label: "Free time begins", on: when == "free", set: func() {
			c.When, c.MinutesBefore = pb.CalendarTrigger_WHEN_FREE, 0
			if c.FreeMinutes <= 0 {
				c.FreeMinutes = 30
			}
		}},
	}
	props := []edProp{{label: "When", value: choiceLabel(whens), choices: whens}}
	switch when {
	case "before":
		props = append(props, edProp{label: "By", value: plural(int(c.MinutesBefore), "minute"),
			choices: minuteChoices([]int{1, 2, 5, 10, 15, 30, 60}, int(c.MinutesBefore), func(m int) string { return plural(m, "minute") }, func(m int) { c.MinutesBefore = int32(m) })})
	case "free":
		props = append(props, edProp{label: "For at least", value: minutesWords(schedtrig.FreeMinutes(c)),
			choices: minuteChoices([]int{15, 30, 45, 60, 90, 120}, schedtrig.FreeMinutes(c), minutesWords, func(m int) { c.FreeMinutes = int32(m) })})
	}
	if when != "free" {
		props = append(props, edProp{label: "Titled", value: cmp.Or(c.Title, "Anything"),
			ask: textAsk("Title has", c.Title, []string{"Events whose title has this; empty for any."}, func(v string) { c.Title = v }, nil)})
	}
	props = append(props, edProp{label: "Calendar", value: cmp.Or(c.Calendar, "Any calendar"),
		ask: textAsk("Calendar", c.Calendar, []string{"The calendar's name; empty for any."}, func(v string) { c.Calendar = v }, nil)})
	if when != "free" {
		props = append(props, edProp{label: "With other people", value: yesNo(c.WithAttendees), toggle: func() { c.WithAttendees = !c.WithAttendees }})
	}
	return props
}

func (ed *schedEditor) macProps(a *App, m *pb.MacTrigger) []edProp {
	ev := func(label string, pairs ...pb.MacTrigger_Event) edProp {
		words := map[pb.MacTrigger_Event]string{
			pb.MacTrigger_EVENT_PLUGGED_IN: "Plugged in", pb.MacTrigger_EVENT_UNPLUGGED: "Unplugged",
			pb.MacTrigger_EVENT_WIFI_JOINED: "Joins", pb.MacTrigger_EVENT_WIFI_LEFT: "Leaves",
			pb.MacTrigger_EVENT_APP_OPENED: "Opens", pb.MacTrigger_EVENT_APP_QUIT: "Quits",
			pb.MacTrigger_EVENT_PLACE_ARRIVE: "Arrives", pb.MacTrigger_EVENT_PLACE_LEAVE: "Leaves",
		}
		var cs []edChoice
		for _, e := range pairs {
			cs = append(cs, edChoice{label: words[e], on: m.Event == e, set: func() { m.Event = e }})
		}
		return edProp{label: label, value: choiceLabel(cs), choices: cs}
	}
	minutes := func(label string, base []int, zero string) edProp {
		w := func(n int) string {
			if n == 0 && zero != "" {
				return zero
			}
			return minutesWords(n)
		}
		cur := int(m.Minutes)
		if m.Event == pb.MacTrigger_EVENT_BACK {
			cur = int(schedtrig.BackMinutes(m))
		}
		return edProp{label: label, value: w(cur), choices: minuteChoices(base, cur, w, func(n int) { m.Minutes = int32(n) })}
	}
	name := func(label, what string, need bool, lines []string) edProp {
		var check func(string) error
		if need {
			check = required(what)
		}
		return edProp{label: label, value: cmp.Or(m.Name, map[bool]string{false: "Any"}[need]), ask: textAsk(label, m.Name, lines, func(v string) { m.Name = v }, check)}
	}
	var props []edProp
	switch m.Event {
	case pb.MacTrigger_EVENT_DISK_LOW:
		below := fmt.Sprintf("%d%%", max(m.Percent, 1))
		if m.Gigabytes > 0 {
			below = fmt.Sprintf("%dGB", m.Gigabytes)
		}
		props = []edProp{
			{label: "Less free than", value: below, ask: textAsk("Less free than", below, []string{"A share, like 10%, or an amount, like 50GB."}, func(v string) {
				t, _ := schedtrig.Parse("mac:disk<"+v, nil)
				m.Percent, m.Gigabytes = t.GetMac().Percent, t.GetMac().Gigabytes
			}, func(v string) error { _, err := schedtrig.Parse("mac:disk<"+v, nil); return err })},
			{label: "Disk", value: schedtrig.DiskWords(m.Volume), ask: textAsk("Disk", m.Volume, []string{"Its mount point, like /Volumes/Backup; empty for the startup disk."},
				func(v string) { m.Volume = v }, nil)},
		}
	case pb.MacTrigger_EVENT_BATTERY_LOW:
		props = []edProp{{label: "Below", value: fmt.Sprintf("%d%%", max(m.Percent, 1)), choices: minuteChoices([]int{5, 10, 15, 20, 30, 50}, int(m.Percent),
			func(n int) string { return fmt.Sprintf("%d%%", n) }, func(n int) { m.Percent = int32(n) })}}
	case pb.MacTrigger_EVENT_PLUGGED_IN, pb.MacTrigger_EVENT_UNPLUGGED:
		props = []edProp{ev("The Mac is", pb.MacTrigger_EVENT_PLUGGED_IN, pb.MacTrigger_EVENT_UNPLUGGED)}
	case pb.MacTrigger_EVENT_WAKE:
		props = []edProp{minutes("After sleeping", []int{0, 5, 15, 30, 60, 240}, "Any time")}
	case pb.MacTrigger_EVENT_AWAY:
		props = []edProp{minutes("Away for", []int{5, 10, 15, 30, 60, 120}, "")}
	case pb.MacTrigger_EVENT_BACK:
		props = []edProp{minutes("After at least", []int{5, 10, 15, 30, 60, 120}, "")}
	case pb.MacTrigger_EVENT_DRIVE:
		props = []edProp{name("Named", "", false, []string{"An external drive whose name has this; empty for any."})}
	case pb.MacTrigger_EVENT_DISPLAY:
		props = []edProp{name("Named", "", false, []string{"A display whose name has this; empty for any."})}
	case pb.MacTrigger_EVENT_WIFI_JOINED, pb.MacTrigger_EVENT_WIFI_LEFT:
		props = []edProp{ev("The Mac", pb.MacTrigger_EVENT_WIFI_JOINED, pb.MacTrigger_EVENT_WIFI_LEFT),
			name("Network", "", false, []string{"The Wi-Fi network's name; empty for any."})}
	case pb.MacTrigger_EVENT_APP_OPENED, pb.MacTrigger_EVENT_APP_QUIT:
		props = []edProp{ev("The app", pb.MacTrigger_EVENT_APP_OPENED, pb.MacTrigger_EVENT_APP_QUIT),
			name("App", "which app?", true, []string{"Its name or bundle id."})}
	case pb.MacTrigger_EVENT_PLACE_ARRIVE, pb.MacTrigger_EVENT_PLACE_LEAVE:
		props = []edProp{ev("The Mac", pb.MacTrigger_EVENT_PLACE_ARRIVE, pb.MacTrigger_EVENT_PLACE_LEAVE),
			name("Place", "which place?", true, []string{"One of the Mac app's Places, by name (Add Trigger ▸ Places there makes them)."})}
	}
	if schedtrig.MacAppWatched(m.Event) {
		props = append(props, edProp{label: "Watched by the Hammock app: works while it's open on that Mac", info: true})
	}
	return props
}

func (ed *schedEditor) slackProps(sl *pb.SlackTrigger) []edProp {
	var cs []edChoice
	for _, e := range slackEventTitles {
		cs = append(cs, edChoice{label: strings.TrimSuffix(e.title, "…"), on: sl.Event == e.e, set: func() {
			sl.Event = e.e
			if e.e == pb.SlackTrigger_EVENT_DM {
				sl.Channel = ""
			}
		}})
	}
	props := []edProp{{label: "When", value: choiceLabel(cs), choices: cs}}
	if sl.Event == pb.SlackTrigger_EVENT_REACTION {
		props = append(props, edProp{label: "Emoji", value: cmp.Or(sl.Emoji, "Any emoji"),
			ask: textAsk("Emoji", sl.Emoji, []string{"Its name, like eyes; empty for any."}, func(v string) { sl.Emoji = schedtrig.SlackEmoji(v) }, nil)})
	}
	if sl.Event != pb.SlackTrigger_EVENT_DM {
		need := sl.Event == pb.SlackTrigger_EVENT_MESSAGE
		props = append(props, edProp{label: "Channel", value: cmp.Or(schedtrig.SlackChannelWords(sl.Channel), map[bool]string{false: "Any channel"}[need]),
			ask: textAsk("Channel", schedtrig.SlackChannelWords(sl.Channel), []string{"#name, or its id."}, func(v string) { sl.Channel = schedtrig.SlackChannel(v) },
				func(v string) error {
					if need && v == "" {
						return errors.New("which channel?")
					}
					return schedtrig.CheckSlack(&pb.SlackTrigger{Event: pb.SlackTrigger_EVENT_MENTION, Channel: schedtrig.SlackChannel(v)})
				})})
	}
	if sl.Event != pb.SlackTrigger_EVENT_REACTION || sl.Anyone {
		label := "From"
		if sl.Event == pb.SlackTrigger_EVENT_REACTION {
			label = "By"
		}
		props = append(props, edProp{label: label, value: cmp.Or(sl.From, "Anyone"),
			ask: textAsk(label, sl.From, []string{"Their name, display name or Slack id; empty for anyone."}, func(v string) { sl.From = strings.TrimPrefix(v, "@") }, nil)})
	}
	props = append(props, edProp{label: "Containing", value: cmp.Or(sl.Text, "Anything"),
		ask: textAsk("Containing", sl.Text, []string{"Text, or a /regular expression/; empty for anything."}, func(v string) { sl.Text = v }, func(v string) error {
			return schedtrig.CheckSlack(&pb.SlackTrigger{Event: pb.SlackTrigger_EVENT_MENTION, Text: v})
		})})
	switch sl.Event {
	case pb.SlackTrigger_EVENT_MESSAGE:
		props = append(props, edProp{label: "Including mine", value: yesNo(sl.Mine), toggle: func() { sl.Mine = !sl.Mine }})
	case pb.SlackTrigger_EVENT_REACTION:
		props = append(props, edProp{label: "Or anyone does", value: yesNo(sl.Anyone), toggle: func() { sl.Anyone = !sl.Anyone }})
	}
	return props
}

// whereProp is where an agent or command trigger watches: any pane, a
// Group, or a tab, on the automation's computer.
func (a *App) whereProp(computerID string, pane, group *string) edProp {
	cs := []edChoice{{label: "Any pane", on: *pane == "" && *group == "", set: func() { *pane, *group = "", "" }}}
	known := *pane == "" && *group == ""
	for _, g := range a.scheduleGroups(computerID) {
		cs = append(cs, edChoice{label: "Group " + iconTitle(g, a.groupTitle(g)), on: *group == g.Id, set: func() { *pane, *group = "", g.Id }})
		known = known || *group == g.Id
	}
	for _, g := range a.scheduleGroups(computerID) {
		for _, tid := range g.TabIds {
			if t := a.tabs[tid]; t != nil && t.FocusedPaneId != "" {
				pid := t.FocusedPaneId
				cs = append(cs, edChoice{label: a.groupTitle(g) + " ▸ " + a.tabTitle(t), on: *pane == pid, set: func() { *pane, *group = pid, "" }})
				known = known || *pane == pid
			}
		}
	}
	value := choiceLabel(cs)
	if !known {
		value = "A pane that's closed"
		if *pane == "" {
			value = "A Group that's closed"
		}
	}
	return edProp{label: "In", value: value, choices: cs}
}

// logPanes are the log panes on computerID's Groups, a log trigger's choices.
func (a *App) logPanes(computerID string) []*pb.Pane {
	var out []*pb.Pane
	for _, g := range a.scheduleGroups(computerID) {
		for _, tid := range g.TabIds {
			if t := a.tabs[tid]; t != nil {
				for _, pid := range tabIDs(t) {
					if p := a.panes[pid]; p != nil && p.Log != nil {
						out = append(out, p)
					}
				}
			}
		}
	}
	return out
}

// ---------- Only If ----------

func (ed *schedEditor) addConditionMenu(a *App) {
	add := func(c *pb.Condition, more bool) func() {
		return func() {
			ed.s.OnlyIf = append(ed.s.OnlyIf, c)
			if more {
				ed.conditionSheet(a, len(ed.s.OnlyIf)-1)
				return
			}
			ed.reopen(a)
		}
	}
	var items []menuItem
	if a.schedOnMac(ed.s) {
		items = append(items,
			menuItem{label: "Mac Is Plugged In", run: add(&pb.Condition{Kind: pb.Condition_KIND_POWER}, false)},
			menuItem{label: "I'm Away", run: add(&pb.Condition{Kind: pb.Condition_KIND_AWAY}, false)},
			menuItem{label: "On a Wi-Fi Network…", run: add(&pb.Condition{Kind: pb.Condition_KIND_WIFI}, true)},
			menuItem{label: "At a Place…", run: add(&pb.Condition{Kind: pb.Condition_KIND_PLACE}, true)},
			menuItem{label: "Not in a Meeting", run: add(&pb.Condition{Kind: pb.Condition_KIND_FREE}, false)},
			menuItem{sep: true})
	}
	items = append(items,
		menuItem{label: "Between Times…", run: add(&pb.Condition{Kind: pb.Condition_KIND_HOURS, FromMinute: 9 * 60, ToMinute: 17 * 60}, true)},
		menuItem{label: "On Days…", run: add(&pb.Condition{Kind: pb.Condition_KIND_DAYS, Weekdays: []int32{1, 2, 3, 4, 5}}, true)},
		menuItem{label: "Disk Has Space…", run: add(&pb.Condition{Kind: pb.Condition_KIND_DISK, Percent: 10}, true)})
	a.openMenuBack(ed.anchorX, ed.anchorY, append([]menuItem{{label: "ONLY IF", disabled: true}}, items...), func() { ed.reopen(a) })
	ed.child(a)
}

func (ed *schedEditor) conditionSheet(a *App, i int) {
	if i < 0 || i >= len(ed.s.OnlyIf) {
		ed.reopen(a)
		return
	}
	c := ed.s.OnlyIf[i]
	head := []menuItem{{label: "Only if " + schedtrig.DescribeCondition(c), disabled: true}}
	if m := schedtrig.MissingCondition(c); m != "" {
		head = append(head, menuItem{label: m, disabled: true})
	}
	var props []edProp
	switch c.Kind {
	case pb.Condition_KIND_AWAY:
		props = []edProp{{label: "For at least", value: map[bool]string{true: "Any time"}[c.Minutes == 0] + map[bool]string{true: minutesWords(int(c.Minutes))}[c.Minutes > 0],
			choices: minuteChoices([]int{0, 5, 10, 15, 30, 60}, int(c.Minutes), func(m int) string {
				if m == 0 {
					return "Any time"
				}
				return minutesWords(m)
			}, func(m int) { c.Minutes = int32(m) })}}
	case pb.Condition_KIND_WIFI:
		props = []edProp{{label: "Network", value: c.Wifi, ask: textAsk("Network", c.Wifi, []string{"The Wi-Fi network's name."}, func(v string) { c.Wifi = v }, required("which Wi-Fi network?"))},
			{label: "Watched by the Hammock app: holds only while it's open on that Mac", info: true}}
	case pb.Condition_KIND_PLACE:
		props = []edProp{{label: "Place", value: c.Place, ask: textAsk("Place", c.Place, []string{"One of the Mac app's Places, by name."}, func(v string) { c.Place = v }, required("which place?"))},
			{label: "Watched by the Hammock app: holds only while it's open on that Mac", info: true}}
	case pb.Condition_KIND_HOURS:
		v := schedtrig.HHMM(c.FromMinute) + "-" + schedtrig.HHMM(c.ToMinute)
		props = []edProp{{label: "Between", value: v, ask: &edAsk{title: "Between", label: "Hours", value: v, lines: []string{"Like 09:00-17:30 (through midnight if the second is earlier)."},
			set: func(v string) error {
				from, to, err := schedtrig.ParseHours(v)
				if err == nil {
					c.FromMinute, c.ToMinute = from, to
				}
				return err
			}}}}
	case pb.Condition_KIND_DAYS:
		v := strings.TrimPrefix(schedtrig.SpecCondition(c), "days=")
		props = []edProp{{label: "Days", value: schedtrig.DaysWords(c.Weekdays), ask: &edAsk{title: "Days", label: "Days", value: v, lines: []string{"weekdays, weekends, mon-fri, or days like mon,wed,fri."},
			set: func(v string) error {
				days, err := schedtrig.ParseDays(v)
				if err == nil && len(days) == 0 {
					err = errors.New("every day always holds: remove the condition instead")
				}
				if err == nil {
					c.Weekdays = days
				}
				return err
			}}}}
	case pb.Condition_KIND_DISK:
		props = []edProp{{label: "More free than", value: fmt.Sprintf("%d%%", c.Percent), choices: minuteChoices([]int{5, 10, 20, 30, 50}, int(c.Percent),
			func(n int) string { return fmt.Sprintf("%d%%", n) }, func(n int) { c.Percent = int32(n) })}}
	}
	ed.sheet(a, head, props, "Remove Condition", func() { ed.s.OnlyIf = slices.Delete(ed.s.OnlyIf, i, i+1) }, func() { ed.conditionSheet(a, i) })
}

// ---------- saving ----------

// check says what's still missing before it can be saved ("" for nothing).
func (ed *schedEditor) check() string {
	s := ed.s
	switch {
	case strings.TrimSpace(s.Prompt) == "" && s.Kind == "command":
		return "Give the command to run"
	case strings.TrimSpace(s.Prompt) == "":
		return "Give the agent a prompt"
	case strings.TrimSpace(s.Cwd) == "":
		return "Give the folder it runs in"
	case s.Session == pb.Schedule_SESSION_PICKED && s.SessionId == "":
		return "Pick the session its runs continue"
	}
	for _, t := range s.Events {
		if m := schedtrig.Missing(t); m != "" {
			return schedtrig.Describe(t, nil) + ": " + m
		}
	}
	for _, c := range s.OnlyIf {
		if m := schedtrig.MissingCondition(c); m != "" {
			return "Only if: " + m
		}
	}
	return ""
}

// save sends it to the daemon; saved, it goes back (to the list).
func (ed *schedEditor) save(a *App) {
	if e := ed.check(); e != "" {
		ed.err = e
		a.dirty = true
		return
	}
	s := cloneSchedule(ed.s)
	s.Runs, s.NextRunUnixMs, s.Error = nil, 0, ""
	s.Name = strings.TrimSpace(s.Name)
	if a.c == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: s}}})
		a.actions <- func() {
			if err != nil {
				ed.err = err.Error()
				a.dirty = true
				if a.overlay != ed && a.under != ed {
					a.flash("⚠ " + err.Error())
				}
				return
			}
			saved := r.GetScheduleReply().GetSchedule()
			if saved == nil {
				return
			}
			if a.schedules == nil {
				a.schedules = map[string]*pb.Schedule{}
			}
			a.schedules[saved.Id] = saved // before its state update, so the list has it
			if when := nextRunText(saved.NextRunUnixMs, time.Now()); when != "" {
				a.flash("Saved “" + saved.Name + "”: next run " + when)
			} else {
				a.flash("Saved “" + saved.Name + "”")
			}
			if a.overlay == ed {
				a.closeOverlay()
				if ed.back != nil {
					a.openSchedules(saved.Id)
				}
			}
		}
	}()
}

// cancel leaves without saving, asking first if anything's changed.
func (ed *schedEditor) cancel(a *App) {
	leave := func() {
		a.closeOverlay()
		if ed.back != nil {
			ed.back()
		}
	}
	if !ed.changed() {
		leave()
		return
	}
	a.openDialog(&dialog{title: "Discard your changes?", okLabel: "Discard", confirm: true,
		lines:  []string{"What you've changed in this automation isn't saved."},
		cancel: func() { ed.reopen(a) },
		submit: func([]string) { leave() }})
	ed.child(a)
}

// ---------- drawing and keys ----------

func (ed *schedEditor) frame(a *App, n int) rect {
	w := min(96, a.w-4)
	h := min(a.h-2, n+6) // border 2, title, rule, rule, hints
	return rect{(a.w - w) / 2, max(0, min(a.h/10, a.h-h)), w, h}
}

func (ed *schedEditor) selectables() []int {
	var out []int
	for i, l := range ed.lines {
		if l.selectable() {
			out = append(out, i)
		}
	}
	return out
}

func (ed *schedEditor) draw(a *App) {
	if a.overlay == ed && ed.open.step() {
		a.dirty = true
	}
	t := clamp01(ed.open.pos)
	a.dimBehind(t) // (under a menu of its own too)
	ed.lines = ed.buildLines(a)
	sels := ed.selectables()
	ed.sel = max(0, min(ed.sel, len(sels)-1))
	r := ed.frame(a, len(ed.lines))
	r.y += int(math.Round((1 - ed.open.pos) * -2))
	full := r.h
	r.h = max(3, int(math.Round(float64(full)*math.Max(0.4, t))))
	bg := mix(a.th.bg, a.th.menu, t)
	in := a.box(r, bg, mix(bg, a.th.border, t))
	if r.h < full {
		return
	}
	s := a.scr
	plain := Cell{FG: a.th.fg, HasFG: true, BG: bg, HasBG: true}
	rule := func(y int) {
		for x := in.x; x < in.x+in.w; x++ {
			s.Set(x, y, Cell{Ch: "─", FG: a.th.border, HasFG: true, BG: bg, HasBG: true})
		}
	}
	title := "New Automation"
	if ed.orig.Id != "" {
		title = "Edit “" + truncate(cmp.Or(ed.orig.Name, "Untitled"), 50) + "”"
	}
	tc := plain
	tc.Bold = true
	s.Text(in.x+1, in.y, in.w-2, title, tc)
	if c := a.computer(ed.s.ComputerId); c != nil && !c.ThisComputer {
		s.TextRight(in.x+1, in.y, in.w-2, "on "+c.Name, Cell{FG: a.th.dim, HasFG: true, BG: bg, HasBG: true})
	}
	rule(in.y + 1)
	ed.listY, ed.listRows = in.y+2, in.h-4
	selLine := -1
	if len(sels) > 0 {
		selLine = sels[ed.sel]
	}
	// Scroll: the selected row in view, with the lines under it.
	if selLine >= 0 {
		end := selLine
		for end+1 < len(ed.lines) && ed.lines[end+1].sub {
			end++
		}
		if end >= ed.top+ed.listRows {
			ed.top = end - ed.listRows + 1
		}
		if selLine < ed.top {
			ed.top = selLine
		}
		// A heading just above the first row shown comes along.
		if ed.top > 0 && ed.top == selLine && ed.lines[ed.top-1].head != "" {
			ed.top--
		}
	}
	ed.top = max(0, min(ed.top, len(ed.lines)-ed.listRows))
	labelW := ed.labelW(ed.lines)
	for i := 0; i < ed.listRows && ed.top+i < len(ed.lines); i++ {
		l := ed.lines[ed.top+i]
		y := ed.listY + i
		isSel := ed.top+i == selLine
		rowBG := bg
		if isSel {
			rowBG = mix(bg, a.th.menuSel, 0.85)
			ed.anchorX, ed.anchorY = in.x+4, y+1
		}
		s.Paint(in.x, y, in.w, 1, rowBG)
		fg, dim := a.th.fg, a.th.dim
		if isSel {
			fg, dim = RGB{255, 255, 255}, mix(RGB{255, 255, 255}, rowBG, 0.35)
		}
		c := Cell{FG: fg, HasFG: true, BG: rowBG, HasBG: true, Bold: isSel}
		switch l.tone {
		case 1:
			c.FG = dim
		case 2:
			c.FG = a.th.orange
		case 3:
			c.FG = a.th.red
		case 4:
			if !isSel {
				c.FG = a.th.accent
			}
		}
		switch {
		case l.head == " ":
		case l.head != "":
			s.Text(in.x+1, y, in.w-2, strings.ToUpper(l.head), Cell{FG: a.th.dim, HasFG: true, BG: bg, HasBG: true, Bold: true})
		case l.sub:
			if isSel || l.tone == 0 {
				c.FG = dim
			}
			c.Bold = false
			x := in.x + 1 + l.indent
			s.Text(x, y, in.x+in.w-1-x, truncate(l.text, in.x+in.w-1-x), c)
		case l.label != "":
			s.Text(in.x+3, y, labelW, l.label, Cell{FG: dim, HasFG: true, BG: rowBG, HasBG: true})
			x := in.x + 3 + labelW + 2
			s.Text(x, y, in.x+in.w-1-x, truncate(l.text, in.x+in.w-1-x), c)
		default:
			x := in.x + 3
			if l.glyph != "" {
				gc := Cell{FG: a.th.dim, HasFG: true, BG: rowBG, HasBG: true}
				if l.tone == 4 && !isSel {
					gc.FG = a.th.accent
				}
				s.Text(x, y, 1, l.glyph, gc)
				x += 2
			}
			s.Text(x, y, in.x+in.w-1-x, truncate(l.text, in.x+in.w-1-x), c)
		}
	}
	if ed.top > 0 {
		s.TextRight(in.x+1, ed.listY, in.w-2, "↑", Cell{FG: a.th.faint, HasFG: true, BG: bg, HasBG: true})
	}
	if ed.top+ed.listRows < len(ed.lines) {
		s.TextRight(in.x+1, ed.listY+ed.listRows-1, in.w-2, "↓", Cell{FG: a.th.faint, HasFG: true, BG: bg, HasBG: true})
	}
	rule(in.y + in.h - 2)
	hints := "↵ Change   a Add Trigger   o Add Condition   s Save   esc Cancel"
	if selLine >= 0 && ed.lines[selLine].remove != nil {
		hints = "↵ Change   ⌫ Remove   a Add Trigger   o Add Condition   s Save   esc Cancel"
	}
	s.Text(in.x+1, in.y+in.h-1, in.w-2, truncate(hints, in.w-2), Cell{FG: a.th.faint, HasFG: true, BG: bg, HasBG: true})
}

// labelW is the widest setting's name, so their values line up.
func (ed *schedEditor) labelW(lines []edLine) int {
	w := textWidth("At most once every")
	for _, l := range lines {
		w = max(w, textWidth(l.label))
	}
	return w
}

// selected is the selected row.
func (ed *schedEditor) selected() *edLine {
	sels := ed.selectables()
	if ed.sel < 0 || ed.sel >= len(sels) {
		return nil
	}
	return &ed.lines[sels[ed.sel]]
}

func (ed *schedEditor) key(a *App, ks []key) {
	for _, k := range ks {
		if ed.lines == nil {
			ed.lines = ed.buildLines(a)
		}
		n := len(ed.selectables())
		switch k.name {
		case "esc":
			ed.cancel(a)
			return
		case "up", "ctrl-p", "shift-tab":
			ed.sel = max(0, ed.sel-1)
		case "down", "ctrl-n", "tab":
			ed.sel = min(n-1, ed.sel+1)
		case "pgup":
			ed.sel = max(0, ed.sel-max(1, ed.listRows/2))
		case "pgdown":
			ed.sel = min(n-1, ed.sel+max(1, ed.listRows/2))
		case "home":
			ed.sel = 0
		case "end":
			ed.sel = n - 1
		case "enter", "right":
			if l := ed.selected(); l != nil && l.edit != nil {
				l.edit()
				return
			}
		case "backspace", "delete":
			if l := ed.selected(); l != nil && l.remove != nil {
				l.remove()
				ed.lines = ed.buildLines(a)
				a.dirty = true
			}
		case "ctrl-s":
			ed.save(a)
			return
		case "":
			switch k.r {
			case 'j':
				ed.sel = min(n-1, ed.sel+1)
			case 'k':
				ed.sel = max(0, ed.sel-1)
			case 'a', '+':
				ed.selectAdd(a, "add-trigger")
				ed.addTriggerMenu(a)
				return
			case 'o':
				ed.selectAdd(a, "add-condition")
				ed.addConditionMenu(a)
				return
			case 's':
				ed.save(a)
				return
			case 'd':
				if l := ed.selected(); l != nil && l.remove != nil {
					l.remove()
					ed.lines = ed.buildLines(a)
				}
			case 'q':
				ed.cancel(a)
				return
			}
		}
		ed.lines = ed.buildLines(a)
	}
}

// selectAdd selects the "+ Add …" row with id, so its menu opens there.
func (ed *schedEditor) selectAdd(a *App, id string) {
	ed.selectID(a, id)
	ed.anchorY = ed.listY + ed.listRows/2
	for i := 0; i < ed.listRows && ed.top+i < len(ed.lines); i++ {
		if ed.lines[ed.top+i].id == id {
			ed.anchorY = ed.listY + i + 1
		}
	}
}

func (ed *schedEditor) mouse(a *App, m mouseEv) {
	r := ed.frame(a, len(ed.lines))
	if m.wheel != 0 {
		n := len(ed.selectables())
		ed.sel = max(0, min(n-1, ed.sel+m.wheel))
		return
	}
	if !m.press || m.button != 0 {
		return
	}
	if !r.contains(m.x, m.y) {
		return // a click outside doesn't throw away what's been typed
	}
	if m.y >= ed.listY && m.y < ed.listY+ed.listRows {
		i := ed.top + m.y - ed.listY
		for n, li := range ed.selectables() {
			if li == i {
				ed.sel = n
				ed.anchorY = m.y + 1
				if l := ed.selected(); l != nil && l.edit != nil {
					l.edit()
				}
				return
			}
		}
	}
}
