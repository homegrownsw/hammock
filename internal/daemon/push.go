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

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
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
// is. The phone takes everything it shows, its category included, from
// the decrypted note.

const awayAfter = 2 * time.Minute

// pushClient sends to relays (replaceable in tests).
var pushClient = http.DefaultClient

// pushRelays are the relays a phone may name: ours (at its own domain, and
// at the workers.dev name phones registered before that use), plus any in
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

// pushNote is what a phone shows (encrypted as JSON).
type pushNote struct {
	Title    string `json:"t"`
	Subtitle string `json:"s,omitempty"`
	Body     string `json:"b"`
	Category string `json:"c,omitempty"` // "join", "grant": notification actions
	Pane     string `json:"pane,omitempty"`
	Join     string `json:"join,omitempty"`
	Grant    string `json:"grant,omitempty"`
	Key      string `json:"key,omitempty"` // a Hammock Key request (hammockkey.go)
	Host     string `json:"host"`          // the Mac's certificate fingerprint, as the phone knows it
	// ID is what it replaces on the phone (the collapse id): notifications
	// about the same thing from two Macs (a linked computer's agent) are one.
	ID string `json:"id,omitempty"`
	// At is when it was sent (Unix seconds): the phone drops one much older,
	// so a relay can't replay old notifications.
	At int64 `json:"at,omitempty"`
}

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

// registerPush records (or with no relay id, forgets) a phone's push
// details. Callers hold d.mu.
func (d *Daemon) registerPush(c *conn, m *pb.RegisterPush) error {
	if c.deviceID == "" {
		return errors.New("only paired devices can register for push")
	}
	if m.RelayId == "" {
		return d.store.DeleteRecord("push", c.deviceID)
	}
	if len(m.Key) != 32 || !knownRelay(m.RelayUrl) {
		return errors.New("bad push registration")
	}
	return d.store.PutRecord("push", c.deviceID, m)
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
		if phone || elsewhere && elsewhereIsAway || (screenLocked() || idle >= awayAfter) && !elsewhere {
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
	event, body := attentionPush(item)
	if event == "" {
		return
	}
	title, sub := d.pushTitle(item.PaneId)
	// Until it's answered, seen or replaced.
	d.push(event, item.Id, pushNote{Title: title, Subtitle: sub, Body: body, Pane: item.PaneId},
		func() bool { return d.attention[item.Id] == item })
}

// pushLinkedAttention tells away phones about an agent on a linked
// computer (a phone paired with any of them hears about all; one paired
// with several gets one notification, collapsed by the item's id).
// Callers hold d.mu.
func (d *Daemon) pushLinkedAttention(l *peerLink, item *pb.AttentionItem) {
	event, body := attentionPush(item)
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
	d.push(event, item.Id, pushNote{Title: title, Subtitle: sub, Body: body, Pane: item.PaneId},
		func() bool {
			cur := l.attn[item.Id]
			return cur != nil && d.routes[item.Id] == l && sameAttention(cur, item)
		})
}

// attentionPush is the push event and text for an item that needs you (""
// for none): the same for this computer's agents and linked computers'.
func attentionPush(item *pb.AttentionItem) (event, body string) {
	body = item.Summary
	switch item.State {
	case pb.AgentState_AGENT_STATE_NEEDS_INPUT:
		event, body = "needs_input", cmpOr(body, "Needs your input")
		if item.Question {
			body = "Asks: " + body
		}
	case pb.AgentState_AGENT_STATE_IDLE:
		event, body = "finished", cmpOr(body, "Finished")
	case pb.AgentState_AGENT_STATE_ERRORED:
		event, body = "errored", cmpOr(body, "Hit an error")
	}
	return event, body
}

// push sends a note to every phone that wants this event once you're
// away: now, or when you become so, if pending (called holding d.mu) still
// says it's waiting on you then. Callers hold d.mu.
func (d *Daemon) push(event, collapse string, n pushNote, pending func() bool) {
	if pushed != nil {
		pushed(event, collapse)
	}
	if recs, err := d.store.Records("push"); err != nil || len(recs) == 0 {
		return
	}
	n.Host = d.remote.fingerprint
	if n.Host == "" {
		if _, fp, err := loadOrCreateCert(d.cfg.Dir); err == nil {
			n.Host = fp
		}
	}
	n.ID = collapse
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
	plain, _ := json.Marshal(n)
	type target struct {
		id string
		r  *pb.RegisterPush
	}
	var to []target
	d.mu.Lock()
	if pending != nil && !pending() {
		d.mu.Unlock()
		return
	}
	recs, _ := d.store.Records("push")
	for deviceID, b := range recs {
		r := &pb.RegisterPush{}
		if proto.Unmarshal(b, r) == nil && slices.Contains(r.Events, event) && !d.phoneShowing(deviceID) {
			to = append(to, target{deviceID, r})
		}
	}
	d.mu.Unlock()
	for _, t := range to {
		if sealed, err := seal(t.r.Key, plain); err == nil {
			d.sendPush(t.id, t.r, sealed, n, collapse)
		}
	}
}

// pushNow sends n to these phones now, whether or not you're at the Mac
// (a Hammock Key request asks here and there at once). Callers hold d.mu.
func (d *Daemon) pushNow(deviceIDs []string, n pushNote, collapse string) {
	recs, err := d.store.Records("push")
	if err != nil || len(recs) == 0 {
		return
	}
	n.Host = d.remote.fingerprint
	if n.Host == "" {
		if _, fp, err := loadOrCreateCert(d.cfg.Dir); err == nil {
			n.Host = fp
		}
	}
	n.ID, n.At = collapse, time.Now().Unix()
	plain, _ := json.Marshal(n)
	for _, id := range deviceIDs {
		r := &pb.RegisterPush{}
		if b := recs[id]; b == nil || proto.Unmarshal(b, r) != nil {
			continue
		}
		if sealed, err := seal(r.Key, plain); err == nil {
			go d.sendPush(id, r, sealed, n, collapse)
		}
	}
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

func (d *Daemon) sendPush(deviceID string, r *pb.RegisterPush, sealed []byte, n pushNote, collapse string) {
	body, _ := json.Marshal(map[string]string{
		"id": r.RelayId, "payload": base64.StdEncoding.EncodeToString(sealed),
		"thread":   pushTag(r.Key, "thread", cmpOr(n.Pane, n.Join, n.Grant, n.Key)),
		"collapse": pushTag(r.Key, "collapse", collapse), "category": n.Category,
	})
	if !knownRelay(r.RelayUrl) {
		return // registered before relays were checked
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, relayOrigin(r.RelayUrl)+"/send", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("content-type", "application/json")
	resp, err := pushClient.Do(req)
	if err != nil {
		d.log.Warn("push", "device", deviceID, "err", err)
		return
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		// The phone unregistered (or the app's gone): stop.
		d.mu.Lock()
		d.persist(d.store.DeleteRecord("push", deviceID))
		d.mu.Unlock()
	case resp.StatusCode >= 300:
		// The relay's reason (APNs' own, when it turned the push down).
		reason, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		d.log.Warn("push", "device", deviceID, "status", resp.StatusCode, "reason", strings.TrimSpace(string(reason)))
	}
}
