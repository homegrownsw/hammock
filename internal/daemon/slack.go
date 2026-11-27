package daemon

// Slack triggers (pb.SlackTrigger): a message in a channel, a mention, a
// DM, a reply in a thread you started, or a reaction of yours. Each person
// makes their own Slack app from Hammock's manifest (slack.ManifestURL:
// read-only user scopes, Socket Mode) and gives this computer its two
// tokens (SlackCommand: the Mac's Connect Slack…, or hmk slack connect):
// a user token (xoxp-), so it sees what you see, as you, and an app-level
// token (xapp-), to open a Socket Mode connection, so nothing here needs
// to be reachable from the internet. Hammock never posts to Slack.
//
// One computer of yours holds the connection (Computer.slack says which;
// MOVE_HERE takes it from another, which hands its tokens over its link
// and forgets them): Slack hands each event to one of an app's open
// connections, so two computers listening would each miss some.
//
// The connection is open only while an automation that's on has a Slack
// trigger (and Automations are on); the tokens are kept when the last one
// goes, until you disconnect. Each envelope is acknowledged at once
// (internal/slack's Socket); events are then matched against the triggers
// in one goroutine (worker), which looks up names (users.info,
// conversations.info) and keeps them a while. When a connection opens
// (after a sleep, a dropped network) the channels triggers name are caught
// up from their history since the last message seen there (at most a day,
// and never from before the trigger was made). DMs, mentions and reactions
// elsewhere aren't caught up: Slack sends an event again for a few minutes
// if it isn't acknowledged, which covers a short drop, not a night asleep.
//
// Loop guards: your own messages don't count (unless a channel trigger
// says mine), as an agent's run may post as you; only your own reactions
// count (unless anyone), as anyone can react to anything.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/schedtrig"
	"github.com/homegrownsw/hammock/internal/slack"
)

var (
	// slackAPIBase is Slack's Web API (tests: a fake's).
	slackAPIBase = slack.DefaultBase
	// slackTick is how often the watcher looks whether the connection
	// should be open (it's also woken as triggers change).
	slackTick = 15 * time.Second
	// slackBackoff is the socket's wait between tries (nil: its own).
	slackBackoff func(int) time.Duration
	// slackCatchUpMax is how far back a channel is caught up, at most.
	slackCatchUpMax = 24 * time.Hour
)

// slackCatchUpLimit is the most messages caught up in one channel.
const slackCatchUpLimit = 200

// slackTokens are your Slack app's tokens, and who they are.
//
// They're kept in a file of their own in the state directory (slackFile),
// readable only by you, not the Keychain: the daemon runs with nobody to
// answer a Keychain prompt, a development build (β, signed ad hoc) is a
// different program to the Keychain after every rebuild and would lose
// them, and the Linux daemon has no Keychain. The state directory is
// already only yours (0700), and holds what's as sensitive (your
// computers' keys, the webhooks' secrets).
type slackTokens struct {
	User     string `json:"user_token"`
	App      string `json:"app_token"`
	TeamID   string `json:"team_id"`
	Team     string `json:"team"`
	URL      string `json:"url"`
	UserID   string `json:"user_id"`
	UserName string `json:"user"`
}

// slackState is the Slack connection. Guarded by d.mu.
type slackState struct {
	loaded    bool
	tok       *slackTokens // nil: not connected
	gen       int          // counts changes of the tokens
	cancel    func()       // ends the running connection (nil: none)
	runGen    int          // the tokens it runs with (gen then)
	listening bool
	problem   string
	failedGen int // the tokens Slack refused (not tried again until they change, or a while passes)
	failedAt  time.Time
	wake      chan struct{}
	// noChannel are channels triggers name that Slack doesn't have (as
	// last looked up): their triggers say so.
	noChannel map[string]bool
	// last is the latest message seen in each channel (its ts), for
	// catching up (kept: "slack_seen", now and then).
	last      map[string]string
	lastSaved time.Time
}

// slackFile is where the tokens are kept.
func (d *Daemon) slackFile() string { return filepath.Join(d.cfg.Dir, "slack-tokens.json") }

// loadSlack reads the tokens and what was last seen. Callers hold d.mu.
func (d *Daemon) loadSlack() {
	if d.slack.loaded {
		return
	}
	d.slack.loaded = true
	d.slack.last = map[string]string{}
	_ = json.Unmarshal([]byte(d.store.Setting("slack_seen")), &d.slack.last)
	if d.slack.last == nil {
		d.slack.last = map[string]string{}
	}
	b, err := os.ReadFile(d.slackFile())
	if err != nil {
		return
	}
	var t slackTokens
	if json.Unmarshal(b, &t) == nil && t.User != "" && t.App != "" {
		d.slack.tok = &t
		d.slack.gen++
	}
}

// setSlackTokens keeps (or, nil, forgets) the tokens, ending a connection
// made with the old ones. Callers hold d.mu.
func (d *Daemon) setSlackTokens(t *slackTokens) error {
	d.loadSlack()
	path := d.slackFile()
	if t == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		d.slack.last = map[string]string{}
		d.persist(d.store.SetSetting("slack_seen", "{}"))
	} else {
		b, _ := json.Marshal(t)
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
		if old := d.slack.tok; old == nil || old.TeamID != t.TeamID {
			d.slack.last = map[string]string{} // (another workspace's channels)
		}
	}
	d.slack.tok = t
	d.slack.gen++
	d.slack.problem, d.slack.noChannel = "", nil
	d.stopSlack()
	d.slackChanged()
	d.wakeSlack()
	return nil
}

// slackLink is this computer's Slack connection, for its record (nil:
// none). Callers hold d.mu.
func (d *Daemon) slackLink() *pb.SlackLink {
	t := d.slack.tok
	if t == nil {
		return nil
	}
	return &pb.SlackLink{TeamId: t.TeamID, Team: t.Team, Url: t.URL, UserId: t.UserID, User: t.UserName,
		Listening: d.slack.listening, Problem: d.slack.problem, Idle: d.slack.cancel == nil && d.slack.problem == ""}
}

// slackChanged tells clients and your other computers how Slack is now,
// and the triggers what they can't do. Callers hold d.mu.
func (d *Daemon) slackChanged() {
	d.publishComputers()
	d.slackErrors()
}

// slackHolder is the linked computer that holds Slack, by name ("" none).
// Callers hold d.mu.
func (d *Daemon) slackHolder() string {
	for uid, l := range d.comp.links {
		if d.isLinked(uid) && l.GetRecord().GetSlack().GetTeamId() != "" {
			return strings.TrimSuffix(cmpOr(l.GetRecord().GetName(), "another computer"), ".local")
		}
	}
	return ""
}

// slackErrors says on each Slack trigger why it can't fire now, if so.
// Callers hold d.mu.
func (d *Daemon) slackErrors() {
	d.loadSlack()
	why := ""
	switch {
	case d.slack.tok == nil:
		if h := d.slackHolder(); h != "" {
			why = "Slack is connected on " + h + ", not this computer: move it here from Add Trigger ▸ Messages ▸ Slack"
		} else {
			why = "Slack isn't connected: Add Trigger ▸ Messages ▸ Slack ▸ Connect Slack…, or hmk slack connect"
		}
	case d.slack.problem != "":
		why = d.slack.problem
	}
	for _, s := range d.sched.list {
		for _, t := range s.Events {
			sl := t.GetSlack()
			if sl == nil {
				continue
			}
			msg := why
			if msg == "" && sl.Channel != "" && d.slack.noChannel[sl.Channel] {
				msg = "There's no channel " + schedtrig.SlackChannelWords(sl.Channel) + " in Slack that you can see"
			}
			d.setTriggerError(s, t, msg)
		}
	}
}

// wakeSlack has the watcher look again now. Callers hold d.mu.
func (d *Daemon) wakeSlack() {
	if d.slack.wake == nil {
		return
	}
	select {
	case d.slack.wake <- struct{}{}:
	default:
	}
}

func isSlackTrigger(t *pb.EventTrigger) bool { return t.GetSlack() != nil }

// slackKey is what a Slack trigger watches: a change arms it afresh (it's
// armed as it's saved: triggersChanged).
func slackKey(t *pb.EventTrigger) string { return "slack:" + schedtrig.Spec(t) }

// watchSlack opens and closes the connection as it's wanted, until the
// daemon stops.
func (d *Daemon) watchSlack() {
	wake := make(chan struct{}, 1)
	d.mu.Lock()
	d.loadSlack()
	d.slack.wake = wake
	d.mu.Unlock()
	t := time.NewTicker(slackTick)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			d.mu.Lock()
			d.stopSlack()
			d.mu.Unlock()
			return
		case <-t.C:
		case <-wake:
		}
		d.mu.Lock()
		d.syncSlack()
		d.mu.Unlock()
	}
}

// syncSlack opens the connection if it's wanted and closed, or closes it
// if it's open and not. Callers hold d.mu.
func (d *Daemon) syncSlack() {
	sl := &d.slack
	refused := sl.failedGen == sl.gen && time.Since(sl.failedAt) < 10*time.Minute
	want := !d.stopped && d.sched.ready && sl.tok != nil && !refused && d.anyTrigger(isSlackTrigger)
	switch {
	case want && sl.cancel == nil:
		ctx, cancel := context.WithCancel(context.Background())
		sl.cancel, sl.runGen = cancel, sl.gen
		go d.runSlack(ctx, sl.gen, *sl.tok)
		d.publishComputers()
	case !want && sl.cancel != nil:
		d.stopSlack()
		d.publishComputers()
	}
}

// stopSlack ends the connection, if one's open. Callers hold d.mu.
func (d *Daemon) stopSlack() {
	if d.slack.cancel != nil {
		d.slack.cancel()
		d.slack.cancel = nil
	}
	d.slack.listening = false
}

// slackItem is the worker's next job: an event, or catching up.
type slackItem struct {
	ev      *slack.Event
	catchUp bool
}

// runSlack keeps a connection open with tok until ctx ends.
func (d *Daemon) runSlack(ctx context.Context, gen int, tok slackTokens) {
	w := &slackWorker{d: d, gen: gen, tok: tok, api: &slack.Client{Base: slackAPIBase, Token: tok.User},
		items: make(chan slackItem, 512), users: map[string]slackUser{}, chans: map[string]slack.Channel{},
		parents: map[string]string{}, seen: map[string]time.Time{}}
	go w.run(ctx)
	sock := &slack.Socket{
		App:     &slack.Client{Base: slackAPIBase, Token: tok.App},
		Backoff: slackBackoff,
		OnEvent: func(ev slack.Event) {
			select {
			case w.items <- slackItem{ev: &ev}:
			default:
				d.log.Warn("slack: too many events waiting; one dropped")
			}
		},
		OnState: func(open bool, err error) {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.slack.gen != gen || d.slack.cancel == nil {
				return
			}
			problem := ""
			if err != nil {
				problem = slackProblem(err)
			}
			if d.slack.listening != open || d.slack.problem != problem {
				d.slack.listening, d.slack.problem = open, problem
				d.slackChanged()
			}
			if open {
				select {
				case w.items <- slackItem{catchUp: true}:
				default:
				}
			}
		},
	}
	err := sock.Run(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil && d.slack.gen == gen {
		d.log.Warn("slack: connection refused", "err", err)
		d.slack.failedGen, d.slack.failedAt = gen, time.Now()
		d.slack.listening, d.slack.problem = false, slackProblem(err)
		if d.slack.runGen == gen {
			d.slack.cancel = nil
		}
		d.slackChanged()
	}
}

// slackProblem says what err means, for the triggers and Settings.
func slackProblem(err error) string {
	switch {
	case errors.Is(err, slack.ErrDisabled):
		return "Socket Mode is off for your Slack app: turn it on in its settings"
	case slack.IsAuth(err):
		return "Slack refused the tokens (" + slackCode(err) + "): change them, or connect again"
	}
	return "Can't reach Slack: " + oneLine(err.Error(), 120)
}

// slackCode is Slack's error code in err ("" if it isn't Slack's).
func slackCode(err error) string {
	var e *slack.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// slackUser is someone, as looked up (when).
type slackUser struct {
	u  slack.User
	at time.Time
}

// slackWorker matches one connection's events against the triggers, one
// at a time. Its lookups are its own (only its goroutine uses them).
type slackWorker struct {
	d       *Daemon
	gen     int
	tok     slackTokens
	api     *slack.Client
	items   chan slackItem
	users   map[string]slackUser
	chans   map[string]slack.Channel // by id
	byName  map[string]string        // channel name → id
	listed  time.Time
	parents map[string]string    // channel|thread ts → who started it
	seen    map[string]time.Time // channel|ts (and reactions) handled
}

func (w *slackWorker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case it := <-w.items:
			if it.catchUp {
				w.catchUp(ctx)
			} else if it.ev != nil {
				w.event(ctx, *it.ev)
			}
		}
	}
}

// user is someone, looked up (and kept an hour).
func (w *slackWorker) user(ctx context.Context, id string) slack.User {
	if id == "" {
		return slack.User{}
	}
	if c, ok := w.users[id]; ok && time.Since(c.at) < time.Hour {
		return c.u
	}
	u, err := w.api.UserInfo(ctx, id)
	if err != nil {
		u = slack.User{ID: id}
	}
	if len(w.users) > 5000 {
		clear(w.users)
	}
	w.users[id] = slackUser{u, time.Now()}
	return u
}

// channel is a conversation, looked up (kept).
func (w *slackWorker) channel(ctx context.Context, id string) slack.Channel {
	if c, ok := w.chans[id]; ok {
		return c
	}
	c, err := w.api.ConversationInfo(ctx, id)
	if err != nil {
		return slack.Channel{ID: id, IM: strings.HasPrefix(id, "D")}
	}
	w.chans[id] = c
	return c
}

// channelID finds a channel by its name (the list kept ten minutes).
func (w *slackWorker) channelID(ctx context.Context, name string) (string, bool) {
	if schedtrig.SlackChannelIsID(name) {
		return name, true
	}
	if w.byName == nil || time.Since(w.listed) > 10*time.Minute {
		list, err := w.api.Channels(ctx)
		if err != nil {
			return "", false
		}
		w.byName, w.listed = map[string]string{}, time.Now()
		for _, c := range list {
			w.byName[strings.ToLower(c.Name)] = c.ID
			w.chans[c.ID] = c
		}
	}
	id, ok := w.byName[name]
	return id, ok
}

// firstSeen: key hasn't been handled before (and now has). What was
// handled two hours ago is forgotten: Slack doesn't send an event again
// after that.
func (w *slackWorker) firstSeen(key string) bool {
	now := time.Now()
	if _, ok := w.seen[key]; ok {
		return false
	}
	if len(w.seen) > 2000 {
		for k, at := range w.seen {
			if now.Sub(at) > 2*time.Hour {
				delete(w.seen, k)
			}
		}
	}
	w.seen[key] = now
	return true
}

// event handles one event from the socket.
func (w *slackWorker) event(ctx context.Context, ev slack.Event) {
	switch ev.Type {
	case "message":
		var m slack.Message
		if json.Unmarshal(ev.Event, &m) != nil {
			return
		}
		w.message(ctx, m, false)
	case "reaction_added":
		var r struct {
			User     string `json:"user"`
			Reaction string `json:"reaction"`
			EventTS  string `json:"event_ts"`
			Item     struct {
				Type    string `json:"type"`
				Channel string `json:"channel"`
				TS      string `json:"ts"`
			} `json:"item"`
		}
		if json.Unmarshal(ev.Event, &r) != nil || r.Item.Type != "message" || r.Item.Channel == "" {
			return
		}
		if !w.firstSeen("r|" + r.Item.Channel + "|" + r.Item.TS + "|" + r.User + "|" + r.Reaction) {
			return
		}
		w.reaction(ctx, r.User, schedtrig.SlackEmoji(r.Reaction), r.Item.Channel, r.Item.TS, cmpOr(r.EventTS, r.Item.TS))
	}
}

// slackCandidate is a trigger that may fire for what happened, as it was.
type slackCandidate struct {
	schedule, trigger string
	t                 *pb.SlackTrigger
	armed             time.Time
}

// candidates are the triggers of events like ok says, as they are now.
func (w *slackWorker) candidates(ok func(*pb.SlackTrigger) bool) []slackCandidate {
	d := w.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.slack.gen != w.gen {
		return nil // (the tokens changed: this connection is ending)
	}
	var out []slackCandidate
	for _, s := range d.sched.list {
		for _, t := range d.eventsOf(s) {
			sl := t.GetSlack()
			if sl == nil || !ok(sl) {
				continue
			}
			st := d.trigStateFor(t.Id, slackKey(t))
			out = append(out, slackCandidate{s.Id, t.Id, proto.CloneOf(sl), time.UnixMilli(st.Armed)})
		}
	}
	return out
}

// noteLast keeps the latest message seen in a channel (saved now and then).
func (w *slackWorker) noteLast(channel, ts string) {
	d := w.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.slack.gen != w.gen || slack.CompareTS(ts, d.slack.last[channel]) <= 0 {
		return
	}
	d.slack.last[channel] = ts
	if time.Since(d.slack.lastSaved) > 30*time.Second {
		d.slack.lastSaved = time.Now()
		b, _ := json.Marshal(d.slack.last)
		d.persist(d.store.SetSetting("slack_seen", string(b)))
	}
}

// message handles a message (live, or caught up from a channel's history).
func (w *slackWorker) message(ctx context.Context, m slack.Message, caughtUp bool) {
	if !m.Counts() || m.Channel == "" || !w.firstSeen(m.Channel+"|"+m.TS) {
		return
	}
	if !strings.HasPrefix(m.Channel, "D") {
		w.noteLast(m.Channel, m.TS)
	}
	me := w.tok.UserID
	mine := m.User == me && m.BotID == ""
	dm := m.ChannelType == "im" || m.ChannelType == "mpim" || strings.HasPrefix(m.Channel, "D")
	if m.ChannelType == "" && !caughtUp && !dm {
		c := w.channel(ctx, m.Channel)
		dm = c.IM || c.MPIM
	}
	mention := slack.MentionsUser(m.Text, me)
	cands := w.candidates(func(t *pb.SlackTrigger) bool {
		switch t.Event {
		case pb.SlackTrigger_EVENT_MESSAGE:
			return !mine || t.Mine
		case pb.SlackTrigger_EVENT_MENTION:
			return !mine && mention
		case pb.SlackTrigger_EVENT_DM:
			return !mine && dm
		case pb.SlackTrigger_EVENT_THREAD_REPLY:
			return !mine && m.IsReply()
		}
		return false
	})
	if len(cands) == 0 {
		return
	}
	at := slack.TSTime(m.TS)
	author := w.author(ctx, m)
	text := slack.PlainText(m.Text, func(id string) string { return w.user(ctx, id).Shown() })
	fired := map[string]bool{}
	for _, c := range cands {
		if fired[c.schedule] || at.Before(c.armed) || !w.where(ctx, c.t, m.Channel) || !slackFrom(c.t.From, author) || !slackTextMatches(c.t.Text, text) {
			continue
		}
		if c.t.Event == pb.SlackTrigger_EVENT_THREAD_REPLY && w.threadStarter(ctx, m) != me {
			continue
		}
		fired[c.schedule] = true
		w.fire(ctx, c, m, author, text, "", "")
	}
}

// reaction handles a reaction someone (by) added to a message.
func (w *slackWorker) reaction(ctx context.Context, by, emoji, channel, ts, eventTS string) {
	me := w.tok.UserID
	cands := w.candidates(func(t *pb.SlackTrigger) bool {
		return t.Event == pb.SlackTrigger_EVENT_REACTION && (by == me || t.Anyone) && (t.Emoji == "" || t.Emoji == emoji)
	})
	if len(cands) == 0 {
		return
	}
	m, err := w.api.Message(ctx, channel, ts)
	if err != nil {
		w.d.log.Info("slack: the reacted message", "err", err)
		return
	}
	reactor := w.user(ctx, by)
	author := w.author(ctx, m)
	text := slack.PlainText(m.Text, func(id string) string { return w.user(ctx, id).Shown() })
	at := slack.TSTime(eventTS)
	fired := map[string]bool{}
	for _, c := range cands {
		if fired[c.schedule] || at.Before(c.armed) || !w.where(ctx, c.t, channel) || !slackFrom(c.t.From, reactor) || !slackTextMatches(c.t.Text, text) {
			continue
		}
		fired[c.schedule] = true
		w.fire(ctx, c, m, author, text, emoji, reactor.Shown())
	}
}

// author is who sent m (a bot by its name).
func (w *slackWorker) author(ctx context.Context, m slack.Message) slack.User {
	if b := m.BotName(); b != "" && m.User == "" {
		return slack.User{ID: m.BotID, Name: b, Bot: true}
	}
	return w.user(ctx, m.User)
}

// threadStarter is who started the thread m replies in ("" if it can't
// be told).
func (w *slackWorker) threadStarter(ctx context.Context, m slack.Message) string {
	if m.ParentUserID != "" {
		return m.ParentUserID
	}
	key := m.Channel + "|" + m.ThreadTS
	if u, ok := w.parents[key]; ok {
		return u
	}
	p, err := w.api.Message(ctx, m.Channel, m.ThreadTS)
	if err != nil {
		return ""
	}
	if len(w.parents) > 2000 {
		clear(w.parents)
	}
	w.parents[key] = p.User
	return p.User
}

// where: a message in channel is where t watches (t's channel, or anywhere).
func (w *slackWorker) where(ctx context.Context, t *pb.SlackTrigger, channel string) bool {
	if t.Channel == "" {
		return true
	}
	if schedtrig.SlackChannelIsID(t.Channel) {
		return t.Channel == channel
	}
	return strings.EqualFold(w.channel(ctx, channel).Name, t.Channel)
}

// slackFrom: u is who from names ("" anyone).
func slackFrom(from string, u slack.User) bool {
	return from == "" || u.Is(from)
}

// slackTextMatches: text is what filter asks for ("" anything).
func slackTextMatches(filter, text string) bool {
	match, err := schedtrig.SlackText(filter)
	return err == nil && (match == nil || match(text))
}

// fire starts c's automation's run for m.
func (w *slackWorker) fire(ctx context.Context, c slackCandidate, m slack.Message, author slack.User, text, emoji, reactor string) {
	where := "direct message"
	if ch := w.channel(ctx, m.Channel); ch.MPIM {
		where = "group DM"
	} else if !ch.IM && ch.Name != "" {
		where = "#" + ch.Name
	} else if !ch.IM && !strings.HasPrefix(m.Channel, "D") {
		where = m.Channel
	}
	authorName := author.Shown()
	if author.Bot {
		authorName += " (a bot)"
	}
	fields := [][2]string{{"channel", where}, {"channel_id", m.Channel}, {"author", authorName}, {"author_id", author.ID},
		{"ts", m.TS}, {"thread_ts", m.ThreadTS}, {"permalink", slack.Permalink(w.tok.URL, m.Channel, m.TS, m.ThreadTS)}}
	if reactor != "" {
		fields = append(fields, [2]string{"emoji", ":" + emoji + ":"}, [2]string{"reacted_by", reactor})
	}
	// The summary is Hammock's own words, from the trigger (never Slack's).
	var summary string
	switch t := c.t; t.Event {
	case pb.SlackTrigger_EVENT_MESSAGE:
		summary = "A Slack message in " + schedtrig.SlackChannelWords(t.Channel)
	case pb.SlackTrigger_EVENT_MENTION:
		summary = "You were mentioned in Slack"
	case pb.SlackTrigger_EVENT_DM:
		summary = "A Slack direct message to you"
	case pb.SlackTrigger_EVENT_THREAD_REPLY:
		summary = "A reply in a Slack thread you started"
	case pb.SlackTrigger_EVENT_REACTION:
		who := "You"
		if t.Anyone {
			who = "Someone"
		}
		if t.Emoji != "" {
			summary = who + " reacted with :" + t.Emoji + ": in Slack"
		} else {
			summary = who + " reacted to a Slack message"
		}
	}
	d := w.d
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.sched.list[c.schedule]
	if s == nil || d.slack.gen != w.gen || triggerOf(s, c.trigger) == nil || d.eventsOf(s) == nil {
		return
	}
	_, _, _ = d.fireEvent(s, &schedEvent{Trigger: c.trigger, Kind: "slack", Summary: summary, Source: "Slack", Fields: fields, Body: text})
}

// catchUp reads what the channels triggers name had while the connection
// was closed: since the last message seen there, the trigger was made, or
// a day ago, whichever's latest.
func (w *slackWorker) catchUp(ctx context.Context) {
	type want struct {
		name  string
		since time.Time
	}
	var chans []want
	d := w.d
	d.mu.Lock()
	if d.slack.gen != w.gen {
		d.mu.Unlock()
		return
	}
	floor := time.Now().Add(-slackCatchUpMax)
	byName := map[string]time.Time{}
	for _, s := range d.sched.list {
		for _, t := range d.eventsOf(s) {
			sl := t.GetSlack()
			if sl == nil || sl.Channel == "" {
				continue
			}
			armed := time.UnixMilli(d.trigStateFor(t.Id, slackKey(t)).Armed)
			if cur, ok := byName[sl.Channel]; !ok || armed.Before(cur) {
				byName[sl.Channel] = armed
			}
		}
	}
	for n, armed := range byName {
		chans = append(chans, want{n, armed})
	}
	last := map[string]string{}
	for k, v := range d.slack.last {
		last[k] = v
	}
	d.mu.Unlock()
	slices.SortFunc(chans, func(a, b want) int { return strings.Compare(a.name, b.name) })
	missing := map[string]bool{}
	for _, c := range chans {
		id, ok := w.channelID(ctx, c.name)
		if !ok {
			if w.byName != nil {
				missing[c.name] = true
			}
			continue
		}
		since := c.since
		if since.Before(floor) {
			since = floor
		}
		oldest := slack.TimeTS(since)
		if l := last[id]; l != "" && slack.CompareTS(l, oldest) > 0 {
			oldest = l
		}
		msgs, err := w.api.History(ctx, id, oldest, slackCatchUpLimit)
		if err != nil {
			d.log.Info("slack: catching up", "channel", c.name, "err", err)
			continue
		}
		for _, m := range msgs {
			if m.TS == oldest {
				continue
			}
			m.Channel, m.ChannelType = id, "channel"
			w.message(ctx, m, true)
		}
	}
	d.mu.Lock()
	if d.slack.gen == w.gen && !maps.Equal(d.slack.noChannel, missing) {
		d.slack.noChannel = missing
		d.slackErrors()
	}
	d.mu.Unlock()
}

// ---------- connecting ----------

// slackCheck asks Slack whether the tokens work: who the user token is
// (with the scopes it needs, when Slack says them), and that the app-level
// token may open a connection.
func slackCheck(ctx context.Context, user, app string) (*slackTokens, error) {
	user, app = strings.TrimSpace(user), strings.TrimSpace(app)
	if why := slack.CheckTokens(user, app); why != "" {
		return nil, errors.New(why)
	}
	a, err := (&slack.Client{Base: slackAPIBase, Token: user}).AuthTest(ctx)
	switch {
	case slack.IsAuth(err):
		return nil, fmt.Errorf("Slack refused the user token (%s): copy the User OAuth Token again", slackCode(err))
	case err != nil:
		return nil, fmt.Errorf("can't reach Slack: %v", err)
	case a.BotID != "":
		return nil, errors.New("that's a bot's token: use the User OAuth Token (xoxp-…)")
	}
	if miss := slack.MissingScopes(a.Scopes); len(miss) > 0 {
		return nil, fmt.Errorf("the user token doesn't have %s: add them under OAuth & Permissions ▸ User Token Scopes, reinstall the app, and copy its token again", strings.Join(miss, ", "))
	}
	_, err = (&slack.Client{Base: slackAPIBase, Token: app}).OpenConnection(ctx)
	switch {
	case slack.IsAuth(err):
		return nil, fmt.Errorf("Slack refused the app-level token (%s): make one with the connections:write scope (Basic Information ▸ App-Level Tokens), and turn Socket Mode on", slackCode(err))
	case err != nil:
		if code := slackCode(err); code != "" {
			return nil, fmt.Errorf("Slack refused the app-level token (%s): turn Socket Mode on in your app's settings", code)
		}
		return nil, fmt.Errorf("can't reach Slack: %v", err)
	}
	return &slackTokens{User: user, App: app, TeamID: a.TeamID, Team: cleanLine(a.Team, nameMax), URL: cleanLine(a.URL, 200),
		UserID: a.UserID, UserName: cleanLine(a.User, nameMax)}, nil
}

// slackReply is how Slack is now, for a SlackCommand's reply. Callers hold d.mu.
func (d *Daemon) slackReply() *pb.ServerMessage {
	return &pb.ServerMessage{Msg: &pb.ServerMessage_SlackReply{SlackReply: &pb.SlackReply{Link: d.slackLink(), ManifestUrl: slack.ManifestURL()}}}
}

// slackCommand is SlackCommand, here (a message for another computer was
// sent on before this). Not under d.mu: connecting asks Slack, and moving
// asks another computer.
func (d *Daemon) slackCommand(c *conn, m *pb.SlackCommand) (*pb.ServerMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	switch m.Action {
	case pb.SlackCommand_ACTION_STATUS:
		d.mu.Lock()
		defer d.mu.Unlock()
		d.loadSlack()
		return d.slackReply(), nil
	case pb.SlackCommand_ACTION_CONNECT:
		tok, err := slackCheck(ctx, m.UserToken, m.AppToken)
		if err != nil {
			return nil, err
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if err := d.setSlackTokens(tok); err != nil {
			return nil, err
		}
		d.audit(c.auditActor(), "slack-connect", tok.TeamID, tok.Team)
		return d.slackReply(), nil
	case pb.SlackCommand_ACTION_DISCONNECT:
		d.mu.Lock()
		defer d.mu.Unlock()
		if err := d.setSlackTokens(nil); err != nil {
			return nil, err
		}
		d.audit(c.auditActor(), "slack-disconnect", "", "")
		return d.slackReply(), nil
	case pb.SlackCommand_ACTION_MOVE_HERE:
		d.mu.Lock()
		from := m.FromComputerId
		l := d.linkOf(from)
		ok := from != "" && from != d.id.me.UserId && l != nil && d.isLinked(from)
		d.mu.Unlock()
		if !ok {
			return nil, errors.New("no such linked computer")
		}
		r, err := l.call(&pb.ClientMessage{Msg: &pb.ClientMessage_SlackCommand{SlackCommand: &pb.SlackCommand{Action: pb.SlackCommand_ACTION_HAND_OVER}}})
		if err != nil {
			return nil, fmt.Errorf("couldn't take Slack from that computer: %v", err)
		}
		h := r.GetSlackReply()
		tok, err := slackCheck(ctx, h.GetUserToken(), h.GetAppToken())
		if err != nil {
			// That computer has forgotten them: it's connected nowhere now.
			return nil, fmt.Errorf("the tokens that computer had didn't work here: %v. Connect Slack again", err)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		if err := d.setSlackTokens(tok); err != nil {
			return nil, err
		}
		d.audit(c.auditActor(), "slack-move", tok.TeamID, tok.Team)
		return d.slackReply(), nil
	case pb.SlackCommand_ACTION_HAND_OVER:
		d.mu.Lock()
		defer d.mu.Unlock()
		// Only to another of your computers, over its link (never to a
		// client here: the tokens don't leave the daemons).
		if c.peer == "" || !d.isLinked(c.peer) {
			return nil, errors.New("only for your other computers")
		}
		d.loadSlack()
		tok := d.slack.tok
		if tok == nil {
			return nil, errors.New("Slack isn't connected there any more")
		}
		if err := d.setSlackTokens(nil); err != nil {
			return nil, err
		}
		d.audit(c.auditActor(), "slack-hand-over", tok.TeamID, tok.Team)
		return &pb.ServerMessage{Msg: &pb.ServerMessage_SlackReply{SlackReply: &pb.SlackReply{UserToken: tok.User, AppToken: tok.App}}}, nil
	}
	return nil, errors.New("unknown Slack command (from a newer Hammock?)")
}
