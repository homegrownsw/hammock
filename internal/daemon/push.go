package daemon

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Push notifications to paired phones (ADR-0007), while you're away from
// the Mac: its screen is locked, or you haven't touched it for a couple of
// minutes. Each phone registers a relay id and its own
// key; notifications are encrypted for it here and the relay only ever
// forwards ciphertext. The phone's notification extension decrypts them.
//
// What the relay (and Apple) see of a send, besides the phone's relay id:
// the ciphertext; a thread and a collapse tag, each a keyed hash of an id
// (pushTag: the same pane or item gives the same tag for that phone, but
// the ids themselves don't leave the Mac); and the category ("join",
// "grant", "hammockkey" or none), which says what kind of notification it
// is. The phone takes everything it shows, its category and the count on
// its icon included, from the decrypted note. A clear (clearPushes) is
// ciphertext only: the relay learns when something a phone was told of is
// dealt with, not what.

const awayAfter = 2 * time.Minute

// pushClient sends to relays (replaceable in tests).
var pushClient = http.DefaultClient

// pushRelays are the relays a phone may name: ours (at its own domain, and
// at the workers.dev name phones registered before that use, which sends go
// to the domain instead of: relayMoved), plus any in
// HMK_PUSH_RELAY (comma-separated, as the iPhone build takes it) for a
// relay of your own. Replaceable in tests.
var pushRelays = func() []string {
	out := []string{"https://push.homegrownsw.com", "https://hmk-push.homegrownsw.workers.dev"}
	for _, r := range strings.Split(os.Getenv("HMK_PUSH_RELAY"), ",") {
		if o := relayOrigin(strings.TrimSpace(r)); o != "" {
			out = append(out, o)
		}
	}
	return out
}()

// relayOrigin is an https URL's origin, or "" if it isn't a plain one (no
// user, path, query or fragment).
func relayOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return ""
	}
	return "https://" + strings.ToLower(u.Host)
}

// knownRelay: a relay URL a phone may register (see pushRelays).
func knownRelay(raw string) bool {
	o := relayOrigin(raw)
	return o != "" && slices.Contains(pushRelays, o)
}

// relayMoved maps a relay's old origin to where it's reached now. Ours left
// workers.dev for its own domain (behind the WAF's rules); phones built
// before still name the old one, which reaches the same Worker and
// database, so their relay ids work at the new one. Replaceable in tests.
var relayMoved = map[string]string{
	"https://hmk-push.homegrownsw.workers.dev": "https://push.homegrownsw.com",
}

// relayEndpoint is the origin to contact for a phone's registered relay URL
// (sends, clears), moved if it has (relayMoved); "" if it isn't a known
// relay.
func relayEndpoint(raw string) string {
	if !knownRelay(raw) {
		return ""
	}
	o := relayOrigin(raw)
	if to, ok := relayMoved[o]; ok {
		return to
	}
	return o
}

// pushNote is what a phone shows (encrypted as JSON).
type pushNote struct {
	Title    string `json:"t"`
	Subtitle string `json:"s,omitempty"`
	Body     string `json:"b"`
	Category string `json:"c,omitempty"` // "join", "grant", agentPromptCategory…: notification actions
	Pane     string `json:"pane,omitempty"`
	// Prompt is the agent's permission prompt that Allow and Deny on an
	// agentPromptCategory note answer (AttentionItem.prompt_id, sent back as
	// AgentAnswer.prompt_id): never another one.
	Prompt string `json:"prompt,omitempty"`
	Join   string `json:"join,omitempty"`
	Grant  string `json:"grant,omitempty"`
	Key    string `json:"key,omitempty"` // a Hammock Key request (hammockkey.go)
	Host   string `json:"host"`          // the Mac's certificate fingerprint, as the phone knows it
	// ID is what it replaces on the phone (the collapse id): notifications
	// about the same thing from two Macs (a linked computer's agent) are one.
	ID string `json:"id,omitempty"`
	// At is when it was sent (Unix seconds): the phone drops one much older,
	// so a relay can't replay old notifications.
	At int64 `json:"at,omitempty"`
	// Live: about something that gets resolved (answered, seen, cleared,
	// withdrawn), and goes from the phone once it is: when a clear for its ID
	// arrives, or the app finds it's no longer live.
	Live bool `json:"live,omitempty"`
	// Clear: not a notification but word that what ID was about is
	// resolved: the phone takes down what it showed for it (sent silently,
	// clearPushes). With no ID, only the badge changed (to none).
	Clear bool `json:"clear,omitempty"`
	// Kind is what happened, for a phone to file it by (Android's
	// channels): the event ("needs_input", "finished", "errored", their
	// "program_" forms, "join"), or "grant" or "hammockkey".
	Kind string `json:"k,omitempty"`
	// Badge is the number for Hammock's icon as it's sent (badgeCount),
	// on a clear too.
	Badge *int `json:"n,omitempty"`
	// Activity is an Android phone's Live Activity (live.go), Kind
	// "live": shown, or updated, as an ongoing notification. With
	// ActivityEnd it ends: it goes then (Unix seconds; at or before At, at
	// once), showing Activity until it does.
	Activity    *liveContent `json:"L,omitempty"`
	ActivityEnd int64        `json:"le,omitempty"`
	// fill, if set, finishes the note as it's sent (holding d.mu): what it's
	// about may have changed while it waited for you to go.
	fill func(*pushNote)
	// leave, if set, are the phones (phoneTag) another computer tells about
	// this itself, as it says when the note is sent (holding d.mu): they're
	// not sent it from here too.
	leave func() []string
}

// agentPromptCategory is the category of a note about an agent's
// permission prompt (Claude Code's or Codex's menu, read off its screen):
// its notification offers Allow and Deny, which answer it as Needs You's
// do (AgentAnswer's decision). In the sealed note only: the relay is never
// told it (relayCategory).
const agentPromptCategory = "agentprompt"

// agentPromptNote makes n about item's permission prompt, if it's waiting
// on one the screen shows: Allow and Deny, for that prompt. Not for a
// question (there's nothing to allow), another program, or a prompt not
// read yet.
func agentPromptNote(n *pushNote, item *pb.AttentionItem) {
	if item == nil || item.State != pb.AgentState_AGENT_STATE_NEEDS_INPUT || item.Question || item.Program || item.PromptId == "" {
		return
	}
	n.Category, n.Prompt = agentPromptCategory, item.PromptId
}

// relayCategory is what the relay is told of a note's category: only
// those it knows (it sets APNs' and FCM's from them); the rest stay in the
// sealed note, where the phone takes its category from anyway.
func relayCategory(c string) string {
	switch c {
	case "join", "grant", "hammockkey":
		return c
	}
	return ""
}

// badgeEvent is what a phone puts in its events (RegisterPush) when it
// shows the count on its icon (from the note's Badge): it's told, with a
// silent push of its own, when that drops to nothing. Older phones show
// none, so they aren't sent those.
const badgeEvent = "badge"

// pushTag stands in for an id in what the relay sees (a notification's
// thread and collapse id): a hash keyed by this phone's push key, so it
// stays the same for the same id but can't be turned back into it, nor
// matched with another phone's. Empty for none.
func pushTag(key []byte, kind, id string) string {
	if id == "" {
		return ""
	}
	tk := hmac.New(sha256.New, key)
	tk.Write([]byte("hammock push tag")) // its own key, not the cipher's
	m := hmac.New(sha256.New, tk.Sum(nil))
	m.Write([]byte(kind + "\x00" + id))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

// phoneTag stands in for a phone in what linked computers are told of the
// phones this one pushes to (Computer.push_phones): a hash of where its
// notifications go (the relay and its id there, the same at every computer
// it's paired with), so another of your computers paired with it too can
// tell it's the same phone, and leave telling it about this one's agents to
// this one. Not the relay id itself: that's half of what sends to it.
func phoneTag(r *pb.RegisterPush) string {
	if r.GetRelayId() == "" {
		return ""
	}
	h := sha256.Sum256([]byte("hammock push phone\x00" + relayOrigin(r.RelayUrl) + "\x00" + r.RelayId))
	return hex.EncodeToString(h[:16])
}

// maxPushPhones is how many phones a computer's record names
// (Computer.push_phones); more are left out, and only get told twice.
const maxPushPhones = 32

// pushPhones are the phones this computer pushes its agents to (phoneTag),
// sorted, for its record as linked computers get it: none while phones
// can't connect to act on them (as push). Callers hold d.mu.
func (d *Daemon) pushPhones() []string {
	if d.idErr != nil || d.outOnly() {
		return nil // (remote access off: none pushed to, push)
	}
	recs, _ := d.store.Records("push")
	var out []string
	for _, b := range recs {
		r := &pb.RegisterPush{}
		if proto.Unmarshal(b, r) == nil {
			if t := phoneTag(r); t != "" && !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	slices.Sort(out)
	return out[:min(len(out), maxPushPhones)]
}

// validPhoneTag: a phoneTag as a linked computer sent it.
func validPhoneTag(t string) bool {
	if len(t) != 32 {
		return false
	}
	_, err := hex.DecodeString(t)
	return err == nil
}

// registerPush records (or with no relay id, forgets) a phone's push
// details. Callers hold d.mu.
func (d *Daemon) registerPush(c *conn, m *pb.RegisterPush) error {
	if c.deviceID == "" {
		return errors.New("only paired devices can register for push")
	}
	if m.RelayId == "" {
		err := d.store.DeleteRecord("push", c.deviceID)
		d.liveRegistered(c.deviceID, m)
		d.publishComputers() // (its phones: Computer.push_phones)
		return err
	}
	if len(m.Key) != 32 || !knownRelay(m.RelayUrl) || len(m.LiveStartToken) > 400 || len(m.LiveToken) > 400 {
		return errors.New("bad push registration")
	}
	if err := d.store.PutRecord("push", c.deviceID, m); err != nil {
		return err
	}
	d.liveRegistered(c.deviceID, m)
	d.publishComputers()
	return nil
}

// idleTime reads how long since this computer's keyboard or mouse was
// touched, and screenLocked whether its screen is locked (replaceable in
// tests; presence_darwin.go, presence_other.go).
var (
	idleTime     = hostIdleTime
	screenLocked = hostScreenLocked
)

// waitUntilAway waits for you to leave the Mac (its screen locked, left
// untouched for awayAfter, or Hammock used on your phone since; with
// elsewhereIsAway, at another of your computers too) while
// pending holds and for at most hold: true once you're away, false if it
// stopped being pending, the time ran out or ctx ended. Off d.mu; pending is called
// under it.
func (d *Daemon) waitUntilAway(ctx context.Context, hold time.Duration, elsewhereIsAway bool, pending func() bool) bool {
	still := func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return pending == nil || pending()
	}
	for start := time.Now(); ; {
		if time.Since(start) > hold || !still() {
			return false
		}
		idle := idleTime() // off the lock: it asks IOKit
		d.mu.Lock()
		elsewhere := d.inUseElsewhere() // at another of your computers, which tells you
		phone := d.usingPhone(idle)
		d.mu.Unlock()
		if awayRule(phone, elsewhere, elsewhereIsAway, screenLocked(), idle) {
			return true
		}
		if elsewhere {
			idle = 0
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(min(awayAfter-idle+100*time.Millisecond, pushPoll)):
		}
	}
}

// headlessIdle is how long a computer without a screen (presence_other.go)
// counts as untouched: always longer than any wait for you to leave it.
const headlessIdle = 1000 * time.Hour

// awayRule: you're away from this computer, as pushes wait for
// (waitUntilAway): you've used Hammock on a phone since you last touched
// it, or it's locked or untouched for awayAfter while you're not at
// another of your computers (or, elsewhereIsAway, you are).
func awayRule(phone, elsewhere, elsewhereIsAway, locked bool, idle time.Duration) bool {
	return phone || elsewhere && elsewhereIsAway || (locked || idle >= awayAfter) && !elsewhere
}

// phoneUsed records you doing something in Hammock on a paired phone now
// (phoneAction). Callers hold d.mu.
func (d *Daemon) phoneUsed() { d.phoneAt = time.Now() }

// usingPhone: you've used Hammock on a phone since you last used any of
// your computers: this Mac's keyboard or mouse (touched idle ago; a
// computer without a screen never is), or a linked computer's, as it last
// said. So that's where you are, whatever this Mac's screen shows. Not
// just this Mac's: at another Mac all day, this one is untouched since the
// morning, and the phone opening Hammock once (it connects to this one)
// would send every push there while you sit at the other. Callers hold d.mu.
func (d *Daemon) usingPhone(idle time.Duration) bool {
	if d.phoneAt.IsZero() {
		return false
	}
	now := time.Now()
	since := now.Add(-idle)
	for uid, l := range d.comp.links {
		if !d.isLinked(uid) {
			continue
		}
		// (A time ahead of now: this clock stepped back since. Ignored, or
		// the phone wouldn't count until it caught up.)
		if at := time.UnixMilli(l.GetRecord().GetLastInputUnixMs()); at.After(since) && !at.After(now) {
			since = at
		}
	}
	return d.phoneAt.After(since)
}

// phoneAction: a message a phone sends because a person did something
// there (opened a pane, typed, answered), not what it fetches by itself
// while it's open (a computer's tile, a screen's frames).
func phoneAction(msg *pb.ClientMessage) bool {
	switch msg.Msg.(type) {
	case *pb.ClientMessage_Input, *pb.ClientMessage_Focus, *pb.ClientMessage_AttachPane,
		*pb.ClientMessage_RespondGrant, *pb.ClientMessage_RespondHammockKey,
		*pb.ClientMessage_CreateTab, *pb.ClientMessage_CreatePane,
		*pb.ClientMessage_CloseTab, *pb.ClientMessage_ClosePane, *pb.ClientMessage_SignalPane,
		// The agent view's prompt and answers, starting or resuming one:
		// what you mostly do with an agent from the phone.
		*pb.ClientMessage_AgentSend, *pb.ClientMessage_AgentAnswer,
		*pb.ClientMessage_StartAgent, *pb.ClientMessage_ResumeSession,
		*pb.ClientMessage_BrowserInput, *pb.ClientMessage_BrowserNavigate, *pb.ClientMessage_UploadFile:
		return true
	}
	return false
}

// phoneShowing: the phone has Hammock open in front (it shows things itself).
// Callers hold d.mu.
func (d *Daemon) phoneShowing(deviceID string) bool {
	for c := range d.conns {
		if c.deviceID == deviceID && c.presence != nil && c.presence.Foreground {
			return true
		}
	}
	return false
}

// pushTitle names a pane for a notification: its tab and Group.
func (d *Daemon) pushTitle(paneID string) (title, subtitle string) {
	title = "Agent"
	if p := d.panes[paneID]; p != nil {
		if t := d.tabs[p.tabID()]; t != nil {
			title = cmpOr(t.Name, t.AutoName, p.snapshotInfo().Title, title)
			if g := d.groups[t.GroupId]; g != nil {
				subtitle = strings.TrimSpace(g.Icon + " " + g.Name)
			}
		}
	}
	return title, subtitle
}

func cmpOr(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// pushAttention tells away phones about an agent that needs you, finished
// or failed. Callers hold d.mu.
func (d *Daemon) pushAttention(item *pb.AttentionItem) {
	var who *pb.Pane
	if p := d.panes[item.PaneId]; p != nil {
		who = p.snapshotInfo()
	}
	event, body := attentionPush(item, paneWho(who))
	if event == "" {
		return
	}
	title, sub := d.pushTitle(item.PaneId)
	// Until it's answered, seen or replaced. Its prompt as it is when it's
	// sent (read off the screen, maybe after the item was raised).
	d.push(event, item.Id, pushNote{Title: title, Subtitle: sub, Body: body, Pane: item.PaneId,
		fill: func(n *pushNote) { agentPromptNote(n, item) }},
		func() bool { return d.attention[item.Id] == item })
}

// pushLinkedAttention tells away phones about an agent on a linked
// computer (a phone paired with any of them hears about all), but not the
// phones that computer pushes to itself (Computer.push_phones): a phone
// paired with both is told once, by the computer the agent is on. (One
// paired with several that don't say, from an earlier build, is told by
// each, and the phone puts the later in place of the earlier.)
// Callers hold d.mu.
func (d *Daemon) pushLinkedAttention(l *peerLink, item *pb.AttentionItem) {
	event, body := attentionPush(item, paneWho(l.panes[item.PaneId]))
	if event == "" {
		return
	}
	title, sub := "Agent", strings.TrimSuffix(l.tok.HostName, ".local")
	if p := l.panes[item.PaneId]; p != nil {
		if t := l.tabs[p.TabId]; t != nil {
			title = cmpOr(t.Name, t.AutoName, p.Title, title)
			if g := l.groups[t.GroupId]; g != nil {
				sub = strings.TrimSpace(g.Icon+" "+g.Name) + " · " + sub
			}
		}
	}
	// Answered through this computer, which passes it on (d.routes).
	uid := l.tok.HostUserId
	d.push(event, item.Id, pushNote{Title: title, Subtitle: sub, Body: body, Pane: item.PaneId,
		fill:  func(n *pushNote) { agentPromptNote(n, l.attn[item.Id]) },
		leave: func() []string { return d.comp.links[uid].GetRecord().GetPushPhones() }},
		func() bool {
			cur := l.attn[item.Id]
			return cur != nil && d.routes[item.Id] == l && sameAttention(cur, item)
		})
}

// attentionPush is the push event and text for an item that needs you (""
// for none), naming what's in its pane as who (paneWho): the same for this
// computer's agents and linked computers', and as the Mac says it
// (noticeBody).
func attentionPush(item *pb.AttentionItem, who string) (event, body string) {
	switch item.State {
	case pb.AgentState_AGENT_STATE_NEEDS_INPUT:
		event = "needs_input"
	case pb.AgentState_AGENT_STATE_IDLE:
		event = "finished"
	case pb.AgentState_AGENT_STATE_ERRORED:
		event = "errored"
	default:
		return "", ""
	}
	// Another program reporting its status (programstatus.go): phones ask
	// for these on their own (an older phone doesn't know them).
	if item.Program {
		event = "program_" + event
	}
	return event, noticeBody(item, who)
}

// push sends a note to every phone that wants this event once you're
// away: now, or when you become so, if pending (called holding d.mu) still
// says it's waiting on you then. Callers hold d.mu.
func (d *Daemon) push(event, collapse string, n pushNote, pending func() bool) {
	if d.idErr != nil || d.outOnly() {
		return // phones can't connect to act on it (identityGate; remote access off: nothing goes out)
	}
	if pushed != nil {
		pushed(event, collapse)
	}
	if recs, err := d.store.Records("push"); err != nil || len(recs) == 0 {
		return
	}
	n.Host = d.pushHost()
	n.ID = collapse
	if n.Kind == "" {
		n.Kind = event
	}
	// Whatever is pending until something resolves it (all but a
	// scheduled run's result) is cleared from the phone when it is.
	n.Live = pending != nil
	go d.pushWhenAway(event, collapse, n, pending)
}

// pushed, in tests, hears of each push asked for (before any phone is
// looked for). Called holding d.mu.
var pushed func(event, collapse string)

// pushPoll is how often a held push checks whether you've gone (locked the
// screen, or let the idle time run out); replaceable in tests.
var pushPoll = 15 * time.Second

// pushHold is how long a push waits for you to go before it's dropped: by
// then you've had it on the Mac a good while.
const pushHold = time.Hour

// pushWhenAway sends n when you're away from the Mac: the screen locked,
// the Mac untouched for a while, whatever app is in front (at the Mac, its
// own notifications tell you), or you've used Hammock on your phone since
// you last touched the Mac. Until then it waits, and drops it if it's no
// longer pending: answered, seen, or moved on.
func (d *Daemon) pushWhenAway(event, collapse string, n pushNote, pending func() bool) {
	if !d.waitUntilAway(context.Background(), pushHold, false, pending) {
		return
	}
	n.At = time.Now().Unix()
	type target struct {
		id string
		r  *pb.RegisterPush
	}
	var to []target
	d.mu.Lock()
	if pending != nil && !pending() || d.outOnly() { // (remote access turned off meanwhile)
		d.mu.Unlock()
		return
	}
	if n.fill != nil {
		n.fill(&n)
	}
	var leave []string
	if n.leave != nil {
		leave = n.leave()
	}
	recs, _ := d.store.Records("push")
	for deviceID, b := range recs {
		r := &pb.RegisterPush{}
		if proto.Unmarshal(b, r) == nil && slices.Contains(r.Events, event) && !d.phoneShowing(deviceID) && !slices.Contains(leave, phoneTag(r)) {
			to = append(to, target{deviceID, r})
		}
	}
	count := d.badgeCount()
	n.Badge = &count
	for _, t := range to {
		if n.Live {
			d.notePushed(collapse, t.id)
		}
		d.noteBadge(t.id, t.r, count)
	}
	d.mu.Unlock()
	plain, _ := json.Marshal(n)
	for _, t := range to {
		if sealed, err := seal(t.r.Key, plain); err == nil {
			d.sendPush(t.id, t.r, sealed, n, collapse, pending)
		}
	}
}

// pushNow sends n to these phones now, whether or not you're at the Mac
// (a Hammock Key request asks here and there at once). Callers hold d.mu.
func (d *Daemon) pushNow(deviceIDs []string, n pushNote, collapse string) {
	if d.idErr != nil || d.outOnly() {
		return // as push
	}
	recs, err := d.store.Records("push")
	if err != nil || len(recs) == 0 {
		return
	}
	n.Host = d.pushHost()
	n.ID, n.At, n.Live = collapse, time.Now().Unix(), true
	count := d.badgeCount()
	n.Badge = &count
	plain, _ := json.Marshal(n)
	for _, id := range deviceIDs {
		r := &pb.RegisterPush{}
		if b := recs[id]; b == nil || proto.Unmarshal(b, r) != nil {
			continue
		}
		if sealed, err := seal(r.Key, plain); err == nil {
			d.notePushed(collapse, id)
			d.noteBadge(id, r, count)
			go d.sendPush(id, r, sealed, n, collapse, d.stillPushed(collapse, id))
		}
	}
}

// pushHost is this Mac as phones know it: its certificate's fingerprint.
// Callers hold d.mu.
func (d *Daemon) pushHost() string {
	if d.remote.fingerprint != "" {
		return d.remote.fingerprint
	}
	_, fp, _ := loadOrCreateCert(d.cfg.Dir)
	return fp
}

// notePushed records that a phone was sent a notification about id, so it's
// told when that's resolved (clearPushes). In memory only: after the daemon
// restarts, phones tidy up when the app next connects instead. Callers
// hold d.mu.
func (d *Daemon) notePushed(id, deviceID string) {
	if d.pushSent == nil {
		d.pushSent = map[string][]string{}
	}
	if !slices.Contains(d.pushSent[id], deviceID) {
		d.pushSent[id] = append(d.pushSent[id], deviceID)
	}
}

// noteBadge records the count a phone that shows one (badgeEvent) was
// last sent, so it's told when that drops to nothing (clearPushes). In
// memory only, as pushSent. Callers hold d.mu.
func (d *Daemon) noteBadge(deviceID string, r *pb.RegisterPush, count int) {
	if !slices.Contains(r.Events, badgeEvent) {
		return
	}
	if d.pushBadge == nil {
		d.pushBadge = map[string]int{}
	}
	d.pushBadge[deviceID] = count
}

// clearPushes: what u resolves (attention items cleared, grants, join and
// Hammock Key requests answered or withdrawn) is no longer anything to be
// told about. The phones that were told get a silent push saying so, and
// take its notification out of Notification Center, wherever it was dealt
// with; each says what the badge is now. And once nothing's left to
// count, a phone left showing a number is told so, with a silent push of
// its own if it isn't getting one already. Callers hold d.mu.
func (d *Daemon) clearPushes(u *pb.StateUpdate) {
	if len(d.pushSent) == 0 && len(d.pushBadge) == 0 || d.outOnly() {
		return // (remote access off: nothing goes out, as push)
	}
	told := map[string][]string{}
	for _, l := range [][]string{u.ClearedAttentionIds, u.ResolvedGrantIds, u.ResolvedJoinIds, u.ResolvedHammockKeyIds} {
		for _, id := range l {
			if phones := d.pushSent[id]; len(phones) > 0 {
				told[id] = phones
				delete(d.pushSent, id)
			}
		}
	}
	shown := false // a phone shows a number
	for _, n := range d.pushBadge {
		shown = shown || n > 0
	}
	if len(told) == 0 && !(shown && touchesBadge(u)) {
		return
	}
	count := d.badgeCount()
	if len(told) == 0 && count > 0 {
		return
	}
	recs, _ := d.store.Records("push")
	host, at := d.pushHost(), time.Now().Unix()
	tell := func(id, deviceID string) {
		r := &pb.RegisterPush{}
		if b := recs[deviceID]; b == nil || proto.Unmarshal(b, r) != nil {
			return
		}
		plain, _ := json.Marshal(pushNote{ID: id, Host: host, At: at, Clear: true, Badge: &count})
		if sealed, err := seal(r.Key, plain); err == nil {
			d.noteBadge(deviceID, r, count)
			go d.sendClear(deviceID, r, sealed)
		}
	}
	cleared := map[string]bool{}
	for id, phones := range told {
		for _, deviceID := range phones {
			tell(id, deviceID)
			cleared[deviceID] = true
		}
	}
	if count == 0 {
		for deviceID, n := range d.pushBadge {
			if n > 0 && !cleared[deviceID] {
				tell("", deviceID)
			}
		}
		clear(d.pushBadge) // none shown anywhere now
	}
}

// clearDelay holds a clear back a moment: the notification it clears may
// still be on its way (sent just before it was dealt with), and APNs
// doesn't keep a silent push behind an alert. Replaceable in tests.
var clearDelay = 2 * time.Second

// sendClear asks the relay for a silent push. Best effort: a relay from
// before clears answers 404 for the path, which says nothing about the
// phone (unlike a send's), and the app tidies up when it next opens anyway.
func (d *Daemon) sendClear(deviceID string, r *pb.RegisterPush, sealed []byte) {
	relay := relayEndpoint(r.RelayUrl)
	if relay == "" {
		return
	}
	time.Sleep(clearDelay)
	body, _ := json.Marshal(map[string]string{"id": r.RelayId, "payload": base64.StdEncoding.EncodeToString(sealed)})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relay+"/clear", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("content-type", "application/json")
	resp, err := pushClient.Do(req)
	if err != nil {
		d.log.Debug("push clear", "device", deviceID, "err", err)
		return
	}
	resp.Body.Close()
}

// seal encrypts with AES-256-GCM: nonce | ciphertext | tag (CryptoKit's
// AES.GCM.SealedBox.combined).
func seal(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

// pushRetryAfter is how long a send the relay turned away with 429 waits
// before its one retry: past the relay's rate limit's 10-second block.
// Replaceable in tests.
var pushRetryAfter = 15 * time.Second

// sendPush asks the relay to send a notification to a phone. If the relay
// says slow down (429), it tries once more, pushRetryAfter later, in its own
// goroutine (other phones' sends don't wait for it), unless pending (called
// holding d.mu; nil for always) says it's been dealt with meanwhile.
func (d *Daemon) sendPush(deviceID string, r *pb.RegisterPush, sealed []byte, n pushNote, collapse string, pending func() bool) {
	if d.postPush(deviceID, r, sealed, n, collapse, false) != http.StatusTooManyRequests {
		return
	}
	after := pushRetryAfter
	go func() {
		time.Sleep(after)
		d.mu.Lock()
		still := pending == nil || pending()
		d.mu.Unlock()
		if still {
			d.postPush(deviceID, r, sealed, n, collapse, true)
		}
	}()
}

// stillPushed: pending for a note sent with notePushed, until clearPushes
// says it's resolved. Callers hold d.mu.
func (d *Daemon) stillPushed(collapse, deviceID string) func() bool {
	return func() bool { return slices.Contains(d.pushSent[collapse], deviceID) }
}

// postPush makes one send to the relay, and returns its status (0 if it
// wasn't made or got no answer). A 429 is only logged on the retry
// (sendPush): the first one is retried.
func (d *Daemon) postPush(deviceID string, r *pb.RegisterPush, sealed []byte, n pushNote, collapse string, retry bool) int {
	body, _ := json.Marshal(map[string]string{
		"id": r.RelayId, "payload": base64.StdEncoding.EncodeToString(sealed),
		"thread":   pushTag(r.Key, "thread", cmpOr(n.Pane, n.Join, n.Grant, n.Key)),
		"collapse": pushTag(r.Key, "collapse", collapse), "category": relayCategory(n.Category),
	})
	relay := relayEndpoint(r.RelayUrl)
	if relay == "" {
		return 0 // registered before relays were checked
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relay+"/send", bytes.NewReader(body))
	if err != nil {
		return 0
	}
	req.Header.Set("content-type", "application/json")
	resp, err := pushClient.Do(req)
	if err != nil {
		d.log.Warn("push", "device", deviceID, "err", err)
		return 0
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		// The phone unregistered (or the app's gone): stop.
		d.mu.Lock()
		d.persist(d.store.DeleteRecord("push", deviceID))
		d.publishComputers()
		d.mu.Unlock()
	case resp.StatusCode == http.StatusTooManyRequests && !retry:
		d.log.Debug("push: relay says slow down, retrying", "device", deviceID, "after", pushRetryAfter)
	case resp.StatusCode >= 300:
		// The relay's reason (APNs' own, when it turned the push down).
		reason, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		d.log.Warn("push", "device", deviceID, "status", resp.StatusCode, "reason", strings.TrimSpace(string(reason)))
	}
	return resp.StatusCode
}
