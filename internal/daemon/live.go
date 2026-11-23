package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/hammockconfig"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Live Activities: a phone that turned them on (Settings ▸ Notifications,
// "live" in its RegisterPush events) shows what your agents are doing
// while you're away from this Mac: the iPhone on its Lock Screen and in
// the Dynamic Island, Android as an ongoing notification (a Live Update
// on Android 16). One for all of this Mac's agents: the one that most
// needs you, else the one most recently at work, and how many others are.
//
// It starts once you're away (the same rule as notifications:
// awayNow) and an agent works or waits for you, never while you're at the
// Mac (an iPhone's would show in its menu bar too); it ends as soon as
// you're back, and a little after the last agent stops (showing that it
// finished). This file decides all of it, and builds what's shown
// (liveContent): the phones only draw it.
//
// An iPhone's goes through the relay in plain text: Apple renders a Live
// Activity's content itself, so it can't be encrypted for the phone. The
// relay (and Apple) then see agent names, Groups and what they're doing,
// which is why it's off unless you turn it on. Android's is in the
// encrypted note, like any notification. An iPhone with Hammock open
// starts and updates its own, from the StateUpdate's live_activity.

// liveContent is what a Live Activity shows: the iPhone's content-state
// (HammockKit's LiveContent decodes it) and Android's ongoing notification
// (LiveContent.kt), the same JSON. Its words are made here.
type liveContent struct {
	Agent      string `json:"agent"`                // who: "Claude"
	Harness    string `json:"harness,omitempty"`    // for its icon
	Tab        string `json:"tab,omitempty"`        // the tab's name
	Group      string `json:"group,omitempty"`      // the Group's name…
	GroupIcon  string `json:"groupIcon,omitempty"`  // …and its emoji
	Place      string `json:"place,omitempty"`      // where, in words: "🗺 Atlas · Map Judder"
	State      string `json:"state"`                // liveWorking, liveNeedsYou, liveFinished or liveErrored
	Status     string `json:"status"`               // the state in words: "Working"
	Doing      string `json:"doing,omitempty"`      // what it's doing, or asks
	Since      int64  `json:"since"`                // when it began (Unix seconds): its timer
	Context    *int   `json:"ctx,omitempty"`        // how full its context is, 0–100, when known
	Pane       string `json:"pane,omitempty"`       // to open it
	Prompt     string `json:"prompt,omitempty"`     // a permission prompt it can be answered from (AgentAnswer.prompt_id)
	Item       string `json:"item,omitempty"`       // the Needs You item it answers (AttentionItem.id), cleared once answered
	More       string `json:"more,omitempty"`       // the others: "+2 more working"
	Others     int    `json:"others"`               // how many others work or wait
	Waiting    int    `json:"waiting"`              // the count on Hammock's icon (badgeCount)
	Accent     string `json:"accent,omitempty"`     // the theme's accent, "#RRGGBB" (light)…
	AccentDark string `json:"accentDark,omitempty"` // …and in the dark
}

const (
	liveWorking  = "working"
	liveNeedsYou = "needs_you"
	liveFinished = "finished"
	liveErrored  = "errored"
)

// liveEvent is the RegisterPush event of a phone with Live Activities on.
const liveEvent = "live"

var (
	// livePoll is how often it looks again: whether you're back at the
	// Mac, and what the agents are doing. Replaceable in tests.
	livePoll = 3 * time.Second
	// liveGap is the least time between two updates saying only what an
	// agent is doing now (a state, agent or prompt changing goes at once):
	// Apple budgets a Live Activity's updates. Replaceable in tests.
	liveGap = 10 * time.Second
	// liveLinger is how long one stays saying the agents stopped before
	// it ends; back at the Mac, it goes at once. An iPhone's still runs
	// meanwhile, so an agent at work again takes it up rather than
	// starting a second beside it.
	liveLinger = 2 * time.Minute
	// liveStaleAfter: an iPhone's shows as out of date this long after its last
	// update (the Mac asleep or gone), until the next.
	liveStaleAfter = 2 * time.Hour
	// liveTokenWait: an iPhone's activity started by push may still be
	// waiting for its update token this long, so a registration without
	// one doesn't yet mean it's gone.
	liveTokenWait = time.Minute
)

// livePhone is a phone with Live Activities on, and its activity.
type livePhone struct {
	reg      *pb.RegisterPush
	android  bool
	started  bool      // one is showing (started here, or the phone says so)
	startAt  time.Time // when it was started here
	token    string    // its update token (an iPhone's)
	sent     string    // the content last sent it, as JSON
	sentAt   time.Time
	declined bool // you dismissed it: not again until you've been back at the Mac
	// finishing: an iPhone's says the agents stopped, and ends then.
	finishing time.Time
}

// liveHub is the Live Activities' state (under d.mu).
type liveHub struct {
	phones map[string]*livePhone // by device id
	loaded bool                  // phones read from the store since a registration changed
	pane   string                // the agent shown: kept while it still works, so two don't take turns
	last   *liveContent          // what was shown last, for its finished state
	shown  string                // the content published to apps (StateUpdate.live_activity), as JSON
	wake   chan struct{}
}

// liveDoing words what a tool does, for "what it's doing now": "Editing
// AppModel.swift", "Running go test ./...". The same kinds as the agent
// view's tool lines (HammockKit's ToolSummary.Kind): Claude's names,
// Codex's, and other agents' spellings.
func liveDoing(tool string, input json.RawMessage) string {
	var in struct {
		Command     any    `json:"command"`
		Cmd         any    `json:"cmd"`
		FilePath    string `json:"file_path"`
		Path        string `json:"path"`
		Description string `json:"description"`
		Pattern     string `json:"pattern"`
		Query       string `json:"query"`
		URL         string `json:"url"`
		Skill       string `json:"skill"`
	}
	_ = json.Unmarshal(input, &in)
	file := path.Base(cmpOr(in.FilePath, in.Path))
	if file == "." || file == "/" {
		file = ""
	}
	command := firstLineMore(cmpOr(commandText(in.Command), commandText(in.Cmd)))
	with := func(verb, what string) string {
		if what == "" {
			return verb
		}
		return verb + " " + what
	}
	if strings.HasPrefix(tool, "mcp__") {
		parts := strings.Split(tool, "__")
		return "Using " + strings.ReplaceAll(parts[len(parts)-1], "_", " ")
	}
	switch strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(tool)) {
	case "read", "readfile", "view", "viewimage", "cat":
		return with("Reading", file)
	case "edit", "multiedit", "notebookedit", "editnotebook", "strreplace", "searchreplace", "editfile", "applypatch", "patch":
		return with("Editing", file)
	case "write", "createfile", "writefile":
		return with("Writing", file)
	case "bash", "shell", "exec", "execcommand", "localshell", "shellcommand", "runterminalcmd", "terminal":
		// Claude says what a command is for ("Run the tests"): that, as it's
		// clearer than the command.
		if in.Description != "" {
			return firstLine(in.Description)
		}
		return with("Running", command)
	case "writestdin":
		return "Running a command"
	case "grep", "glob", "search", "find", "rg", "ripgrep", "grepsearch", "globfilesearch", "codebasesearch", "semanticsearch":
		return with("Searching for", firstLine(cmpOr(in.Pattern, in.Query)))
	case "websearch":
		return with("Searching the web for", firstLine(in.Query))
	case "webfetch", "fetch":
		return with("Reading", hostOf(in.URL))
	case "todowrite", "taskcreate", "taskupdate", "updateplan", "plan":
		return "Updating its tasks"
	case "task", "agent":
		return with("Running a subagent:", firstLine(in.Description))
	case "skill":
		return with("Using the skill", in.Skill)
	}
	return "Using " + tool
}

// commandText is a tool's command, given as a string or a list of words.
func commandText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var w []string
		for _, x := range c {
			if s, ok := x.(string); ok {
				w = append(w, s)
			}
		}
		// Codex's ["bash", "-lc", "go test ./..."]: the script.
		if len(w) == 3 && (w[1] == "-lc" || w[1] == "-c") {
			return w[2]
		}
		return strings.Join(w, " ")
	}
	return ""
}

func hostOf(raw string) string {
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	h, _, _ := strings.Cut(raw, "/")
	return h
}

// liveAgent is an agent a Live Activity could show.
type liveAgent struct {
	content liveContent
	needs   bool  // it needs you (else it's working)
	at      int64 // needs you: since when (the longest waiting first); working: its last activity (the latest first)
}

// pickLive is the agent to show: the one that's needed you longest, else
// the one shown already if it still works (so two busy agents don't take
// turns), else the one most recently at work. ok is false with none.
func pickLive(agents []liveAgent, current string) (shown liveAgent, others []liveAgent, ok bool) {
	if len(agents) == 0 {
		return liveAgent{}, nil, false
	}
	sorted := slices.Clone(agents)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.needs != b.needs {
			return a.needs
		}
		if !a.needs && (a.content.Pane == current) != (b.content.Pane == current) {
			return a.content.Pane == current
		}
		if a.needs {
			return a.at < b.at
		}
		return a.at > b.at
	})
	return sorted[0], sorted[1:], true
}

// liveMore says how many others there are: "+2 more working", "+1 more
// needs you", "+3 more: 1 needs you".
func liveMore(others []liveAgent) string {
	working, needs := 0, 0
	for _, a := range others {
		if a.needs {
			needs++
		} else {
			working++
		}
	}
	switch {
	case working+needs == 0:
		return ""
	case needs == 0:
		return fmt.Sprintf("+%d more working", working)
	case working == 0 && needs == 1:
		return "+1 more needs you"
	case working == 0:
		return fmt.Sprintf("+%d more need you", needs)
	}
	return fmt.Sprintf("+%d more: %d %s you", working+needs, needs, map[bool]string{true: "needs", false: "need"}[needs == 1])
}

// liveStatus is a state in words.
func liveStatus(state string) string {
	switch state {
	case liveNeedsYou:
		return "Needs you"
	case liveFinished:
		return "Finished"
	case liveErrored:
		return "Error"
	}
	return "Working"
}

// liveAgents are this Mac's agents at work or waiting for you, as a Live
// Activity would show each one; ctx is how full each pane's context is,
// where its agent view knows (read off d.mu: liveContexts). Callers hold d.mu.
func (d *Daemon) liveAgents(ctx map[string]int) []liveAgent {
	needs := map[string]*pb.AttentionItem{}
	for _, a := range d.attention {
		if a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT && !a.Program {
			if b := needs[a.PaneId]; b == nil || a.CreatedUnixMs < b.CreatedUnixMs {
				needs[a.PaneId] = a
			}
		}
	}
	var out []liveAgent
	for id, p := range d.panes {
		info := p.snapshotInfo()
		a := info.Agent
		if a == nil || a.Harness == "" {
			continue // an agent's, not another program's
		}
		item := needs[id]
		working := a.State == pb.AgentState_AGENT_STATE_WORKING || a.RunningCommand
		if item == nil && !working {
			continue
		}
		c := d.liveAbout(id, info)
		if v, ok := ctx[id]; ok {
			c.Context = &v
		}
		la := liveAgent{content: c}
		if item != nil {
			la.needs, la.at = true, item.CreatedUnixMs
			c := &la.content
			c.State, c.Since = liveNeedsYou, item.CreatedUnixMs/1000
			c.Doing = clip(cmpOr(item.Summary, paneWho(info)+" needs your input"), 160)
			if !item.Question && item.PromptId != "" {
				c.Prompt, c.Item = item.PromptId, item.Id
			}
		} else {
			p.mu.Lock()
			doing, last := p.doing, p.hookAt
			p.mu.Unlock()
			if doing == "" && a.Reported && len(a.Records) > 0 {
				doing = a.Records[0].Message // what it reports it's doing
			}
			la.at = max(last.UnixMilli(), a.StateSinceUnixMs, info.LastOutputUnixMs)
			c := &la.content
			c.State, c.Since = liveWorking, a.StateSinceUnixMs/1000
			c.Doing = clip(cmpOr(doing, "Thinking"), 160)
		}
		la.content.Status = liveStatus(la.content.State)
		out = append(out, la)
	}
	return out
}

// liveAbout is what a Live Activity says of a pane whatever its state: who,
// where, and the theme. Callers hold d.mu.
func (d *Daemon) liveAbout(paneID string, info *pb.Pane) liveContent {
	c := liveContent{Agent: clip(paneWho(info), 40), Harness: info.GetAgent().GetHarness(), Pane: paneID}
	if t := d.tabs[info.TabId]; t != nil {
		c.Tab = clip(cmpOr(t.Name, t.AutoName, info.Title), 60)
		if g := d.groups[t.GroupId]; g != nil {
			c.Group, c.GroupIcon = clip(g.Name, 60), clip(g.Icon, 8)
		}
	}
	c.Place = strings.TrimSpace(c.GroupIcon + " " + c.Group)
	if c.Tab != "" && c.Tab != c.Group {
		c.Place = strings.TrimPrefix(c.Place+" · "+c.Tab, " · ")
	}
	c.Accent, c.AccentDark = liveAccent(d.theme)
	return c
}

// liveAccent is the theme's accent colour, light and dark ("#RRGGBB"; ""
// for the phone's own: Native, or a custom theme without one).
func liveAccent(t *pb.HostTheme) (light, dark string) {
	name := hammockconfig.DefaultTheme
	if t != nil && t.Name != "" {
		name = t.Name
	}
	if name == hammockconfig.CustomTheme {
		// agent-color, when it's a colour (not a name the apps know).
		c := strings.TrimSpace(t.GetAgentColor())
		if lt, dk, ok := strings.Cut(c, ","); ok {
			lt, dk = strings.TrimPrefix(strings.TrimSpace(lt), "light:"), strings.TrimPrefix(strings.TrimSpace(dk), "dark:")
			if hexColour(lt) && hexColour(dk) {
				return lt, dk
			}
		}
		if hexColour(c) {
			return c, c
		}
		return "", ""
	}
	for _, th := range hammockconfig.Curated {
		if th.Name == name && th.AccentLight+th.AccentDark != 0 {
			return fmt.Sprintf("#%06X", th.AccentLight), fmt.Sprintf("#%06X", th.AccentDark)
		}
	}
	return "", ""
}

func hexColour(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, r := range s[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// liveContexts reads how full each agent's context is, where an agent view
// is open on it (it's the one that reads the session). Off d.mu: a watch's
// own lock isn't taken under it.
func (d *Daemon) liveContexts() map[string]int {
	d.mu.Lock()
	ws := map[string]*agentWatch{}
	for id, w := range d.agentWatches {
		if w.sub == "" && w.shell == "" {
			ws[id] = w
		}
	}
	d.mu.Unlock()
	out := map[string]int{}
	for id, w := range ws {
		w.mu.Lock()
		c := w.ctx
		w.mu.Unlock()
		if c.window > 0 && c.tokens > 0 {
			out[id] = int(min(100, c.tokens*100/c.window))
		}
	}
	return out
}

// livePhones are the phones with Live Activities on, read again from the
// store after a registration changes. Callers hold d.mu.
func (d *Daemon) livePhones() map[string]*livePhone {
	h := &d.live
	if h.loaded {
		return h.phones
	}
	h.loaded = true
	recs, _ := d.store.Records("push")
	devs, _ := d.store.Devices()
	android := map[string]bool{}
	for _, dv := range devs {
		android[dv.ID] = dv.Platform == "android"
	}
	phones := map[string]*livePhone{}
	for id, b := range recs {
		r := &pb.RegisterPush{}
		if proto.Unmarshal(b, r) != nil || !slices.Contains(r.Events, liveEvent) {
			continue
		}
		lp := h.phones[id]
		if lp == nil {
			lp = &livePhone{}
		}
		lp.reg, lp.android = r, android[id]
		phones[id] = lp
	}
	h.phones = phones
	return phones
}

// liveRegistered takes in a phone's registration (registerPush, which has
// stored it): its tokens, and whether its activity still runs. Callers
// hold d.mu.
func (d *Daemon) liveRegistered(deviceID string, m *pb.RegisterPush) {
	h := &d.live
	was := h.phones[deviceID]
	h.loaded = false
	lp := d.livePhones()[deviceID]
	switch {
	case lp == nil:
		if was != nil && was.started {
			// Turned off: what it shows goes (the phone ends its own too).
			d.liveEnd(deviceID, was, 0)
		}
	case m.LiveToken != "":
		lp.started, lp.token = true, m.LiveToken
	case lp.started && time.Since(lp.startAt) > liveTokenWait:
		// None runs (dismissed, or ended on the phone): not again until
		// you've been back at the Mac.
		lp.started, lp.token, lp.sent, lp.declined, lp.finishing = false, "", "", true, time.Time{}
	}
	d.liveWake()
}

// liveWake has the loop look now.
func (d *Daemon) liveWake() {
	select {
	case d.live.wake <- struct{}{}:
	default:
	}
}

// watchLive keeps the phones' Live Activities as they should be.
func (d *Daemon) watchLive() {
	t := time.NewTicker(livePoll)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		case <-d.live.wake:
		}
		d.liveTick()
	}
}

// liveTick looks at whether you're away and what the agents are doing, and
// starts, updates or ends each phone's Live Activity to match.
func (d *Daemon) liveTick() {
	d.mu.Lock()
	none := len(d.livePhones()) == 0 && d.live.shown == ""
	d.mu.Unlock()
	if none {
		return
	}
	ctx := d.liveContexts()
	idle := idleTime() // off the lock: it asks IOKit
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.idErr != nil {
		return // as push
	}
	h := &d.live
	phones := d.livePhones()
	// Stricter than pushes (waitUntilAway): only once you've used Hammock
	// on a phone since you last touched any of your computers. The Mac
	// locked or left a while (a call, reading) isn't enough: an activity
	// then would show up too often, on the Mac's menu bar too.
	away := d.usingPhone(idle)
	var want *liveContent
	if shown, others, ok := pickLive(d.liveAgents(ctx), h.pane); ok && away && len(phones) > 0 {
		c := shown.content
		c.More, c.Others, c.Waiting = liveMore(others), len(others), d.badgeCount()
		want, h.pane = &c, c.Pane
	}
	// What apps are told (an iPhone with Hammock open draws its own).
	shownJSON := ""
	if want != nil {
		b, _ := json.Marshal(want)
		shownJSON = string(b)
	}
	if shownJSON != h.shown {
		h.shown = shownJSON
		d.publish(&pb.StateUpdate{LiveActivity: &pb.LiveActivity{Content: shownJSON}})
	}
	for id, lp := range phones {
		if !away {
			lp.declined = false
		}
		switch {
		case want != nil && !lp.declined:
			d.liveSend(id, lp, *want)
		case lp.started && !away:
			d.liveEnd(id, lp, 0) // back at the Mac: at once
		case lp.started:
			d.liveFinish(id, lp) // nothing at work: says so a while
		}
	}
	if want != nil {
		h.last = want
	} else if !away || len(phones) == 0 {
		h.pane, h.last = "", nil
	}
}

// liveSend starts or updates a phone's Live Activity. Callers hold d.mu.
func (d *Daemon) liveSend(deviceID string, lp *livePhone, c liveContent) {
	lp.finishing = time.Time{} // at work again: no longer ending
	b, _ := json.Marshal(c)
	now := time.Now()
	if lp.started && string(b) == lp.sent {
		return
	}
	var was liveContent
	_ = json.Unmarshal([]byte(lp.sent), &was)
	changed := !lp.started || was.State != c.State || was.Pane != c.Pane || was.Prompt != c.Prompt || was.Others != c.Others
	if !changed && now.Sub(lp.sentAt) < liveGap {
		return // only what it's doing: soon
	}
	if !lp.android && d.phoneShowing(deviceID) {
		// Hammock is open there: it starts or updates its own (from the
		// StateUpdate), and says its token.
		lp.sent, lp.sentAt = string(b), now
		return
	}
	if !lp.android && (lp.started && lp.token == "" || !lp.started && lp.reg.LiveStartToken == "") {
		return // no token to send with (yet)
	}
	start := !lp.started
	if start {
		lp.started, lp.startAt, lp.token = true, now, ""
	}
	lp.sent, lp.sentAt = string(b), now
	priority := 5
	if changed && (start || c.State == liveNeedsYou) {
		priority = 10 // it needs you, or it's new: shown at once
	}
	if lp.android {
		d.liveSealed(deviceID, lp.reg, &c, 0)
		return
	}
	body := map[string]any{"token": lp.token, "event": "update", "state": c, "stale": now.Add(liveStaleAfter).Unix(), "priority": priority}
	if start {
		body["token"], body["event"] = lp.reg.LiveStartToken, "start"
		body["attributes"] = map[string]string{"host": d.pushHost(), "mac": clip(d.macName(), 40)}
		body["alert"] = map[string]string{"title": c.Agent, "body": c.Status + ": " + c.Doing}
	}
	go d.postLive(deviceID, lp.reg, body)
}

// liveFinish has a phone's Live Activity say its agent finished (or
// failed), for liveLinger, then end. An iPhone's is updated to say so and
// ended later, not ended at once and left showing: ended, it can't be
// taken up again, and an agent at work again meanwhile would start a
// second. Callers hold d.mu.
func (d *Daemon) liveFinish(deviceID string, lp *livePhone) {
	if lp.android || lp.token == "" {
		d.liveEnd(deviceID, lp, liveLinger)
		return
	}
	now := time.Now()
	if !lp.finishing.IsZero() {
		if now.After(lp.finishing) {
			d.liveEnd(deviceID, lp, 0)
		}
		return
	}
	lp.finishing = now.Add(liveLinger)
	final := d.liveFinal()
	b, _ := json.Marshal(final)
	lp.sent, lp.sentAt = string(b), now
	go d.postLive(deviceID, lp.reg, map[string]any{"token": lp.token, "event": "update", "state": final, "stale": lp.finishing.Unix(), "priority": 10})
}

// liveFinal is what a Live Activity says once its agents stopped: the one
// shown last, finished (or failed). Callers hold d.mu.
func (d *Daemon) liveFinal() liveContent {
	final := liveContent{State: liveFinished}
	if l := d.live.last; l != nil {
		final = *l
		final.State, final.Prompt, final.Item, final.More, final.Others = liveFinished, "", "", "", 0
		final.Waiting = d.badgeCount()
		if p := d.panes[final.Pane]; p != nil {
			if a := p.snapshotInfo().Agent; a != nil {
				if a.State == pb.AgentState_AGENT_STATE_ERRORED {
					final.State = liveErrored
				}
				final.Doing = clip(a.Summary, 160)
				final.Since = a.StateSinceUnixMs / 1000
			}
		}
	}
	final.Status = liveStatus(final.State)
	return final
}

// liveEnd ends a phone's Live Activity: at once, or after linger showing
// that its agent finished (or failed). Callers hold d.mu.
func (d *Daemon) liveEnd(deviceID string, lp *livePhone, linger time.Duration) {
	final := d.liveFinal()
	token := lp.token
	lp.started, lp.token, lp.sent, lp.finishing = false, "", "", time.Time{}
	dismiss := time.Now().Add(linger).Unix()
	if lp.android {
		d.liveSealed(deviceID, lp.reg, &final, dismiss)
		return
	}
	if token == "" {
		return // the phone ends its own when it next connects (or Hammock opens)
	}
	go d.postLive(deviceID, lp.reg, map[string]any{"token": token, "event": "end", "state": final, "dismiss": dismiss, "priority": 10})
}

// liveSealed sends an Android phone its Live Activity in an encrypted
// note (end: it ends, gone by then). Callers hold d.mu.
func (d *Daemon) liveSealed(deviceID string, r *pb.RegisterPush, c *liveContent, end int64) {
	n := pushNote{Host: d.pushHost(), ID: "live", At: time.Now().Unix(), Kind: liveEvent, Activity: c, ActivityEnd: end}
	plain, _ := json.Marshal(n)
	if sealed, err := seal(r.Key, plain); err == nil {
		go d.postPush(deviceID, r, sealed, n, "live", false)
	}
}

// postLive asks the relay to send an iPhone's Live Activity push (in plain
// text: see the top). A token Apple turns down is forgotten, not the
// phone (it still gets notifications).
func (d *Daemon) postLive(deviceID string, r *pb.RegisterPush, live map[string]any) {
	relay := relayEndpoint(r.RelayUrl)
	if relay == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"id": r.RelayId, "live": live})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relay+"/send", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("content-type", "application/json")
	resp, err := pushClient.Do(req)
	if err != nil {
		d.log.Warn("live activity", "device", deviceID, "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 {
		return
	}
	reason, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	d.log.Warn("live activity", "device", deviceID, "status", resp.StatusCode, "reason", strings.TrimSpace(string(reason)))
	if resp.StatusCode != http.StatusGone {
		return
	}
	// The token's no good: an update's (the activity's gone) or the
	// push-to-start one (the phone sends a new one when it has it).
	d.mu.Lock()
	defer d.mu.Unlock()
	if lp := d.live.phones[deviceID]; lp != nil {
		if live["event"] == "start" {
			lp.reg = proto.CloneOf(lp.reg)
			lp.reg.LiveStartToken = ""
		}
		lp.started, lp.token, lp.sent, lp.finishing = false, "", "", time.Time{}
	}
}
