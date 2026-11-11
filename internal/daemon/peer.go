package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Joining Groups others share with you (ADR-0008). For each host Mac, this
// daemon keeps one link to the host's daemon: it signs in with this Mac's
// user key, subscribes, and keeps a copy of the shared Groups, which your
// clients see among your own (dimmed with their last state while the host
// is unreachable). What your clients do in them is sent on to the host,
// which checks it against your role; what comes back (screens, agent
// views) goes to whoever's viewing.

type peerLink struct {
	d   *Daemon
	tok *pb.InviteToken // the host: name, addresses, certificate (no secret kept)

	cmu    sync.Mutex
	cl     *client.Client
	addr   string        // the host address cl reached (for video over QUIC)
	ready  chan struct{} // closed while connected; replaced on each reconnect
	stop   chan struct{}
	closed bool

	// guarded by d.mu
	groups   map[string]*pb.Group
	tabs     map[string]*pb.Tab
	panes    map[string]*pb.Pane
	attn     map[string]*pb.AttentionItem
	grants   map[string]*pb.GrantRequest // a linked computer's agents asking you (not cached: they wait on it)
	scheds   map[string]*pb.Schedule     // a linked computer's scheduled tasks (schedules.go)
	people   map[string]*pb.Person
	online   bool
	onlineAt time.Time                 // (guarded by d.mu) when it last came online
	attached map[string]map[*conn]bool // remote pane → your clients viewing it
	watching map[string]map[*conn]bool // remote pane → your clients watching its agent
	paging   map[string]*conn          // remote pane → who asked for older agent history
	logs     map[string]*pb.WatchLog   // remote log pane → the watch your client last sent (one client at a time)
	logTo    map[string]*conn
	inputs   map[string]*conn          // remote browser pane → who last gave it input (gets its menus)
	copyAsk  map[string][]copyRequest  // remote browser pane → copies your clients asked of it, not yet answered (oldest first)
	menus    map[int32]*pb.BrowserMenu // menus shown to your clients, by the host's seq (dismissed if they leave)
	menuTo   map[int32]*conn
	sentAt   string // the presence last sent
	pending  int    // joins waiting on the host
	savedAt  time.Time
	linked   bool // a linked computer of yours (computers.go): every Group, as owner
	notYet   bool // (guarded by d.mu) it didn't link with this one yet: sign in again shortly
	gone     bool // closed (left, unlinked): shows nothing more
}

func linkKey(t *pb.InviteToken) string { return t.CertSha256 }

// copyRequest is a client asking a host's browser pane for its selection (⌘C,
// ⌘X, or a menu item such as Copy): the one time the host's text may go
// to that client's clipboard.
type copyRequest struct {
	c  *conn
	at time.Time
}

// copyAnswerWithin is how long after asking a host's answer is taken.
const copyAnswerWithin = 5 * time.Second

// askedCopy notes that c asked pane for a copy, if msg does: a copy or cut,
// or choosing a menu item of the host's that copies. Callers hold d.mu.
func (l *peerLink) askedCopy(c *conn, msg *pb.ClientMessage) {
	pane := ""
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_BrowserInput:
		if m.BrowserInput.Op == "copy" || m.BrowserInput.Op == "cut" {
			pane = m.BrowserInput.PaneId
		}
	case *pb.ClientMessage_BrowserMenuChoice:
		ch := m.BrowserMenuChoice
		if bm := l.menus[ch.Seq]; bm != nil && l.menuTo[ch.Seq] == c && bm.PaneId == ch.PaneId && copyItem(bm, int(ch.Index)) {
			pane = ch.PaneId
		}
	}
	if pane == "" {
		return
	}
	// Answers come in the order asked: each client its own.
	q := slices.DeleteFunc(l.copyAsk[pane], func(a copyRequest) bool { return time.Since(a.at) > copyAnswerWithin })
	if len(q) >= 8 {
		q = q[1:]
	}
	l.copyAsk[pane] = append(q, copyRequest{c, time.Now()})
}

// copyItem: item i of a host's context menu copies (Copy, Cut, Copy Link
// Address). A host from before it said so is taken at its word in English.
func copyItem(bm *pb.BrowserMenu, i int) bool {
	if bm.Kind != "context" || i < 0 || i >= len(bm.Items) {
		return false
	}
	if slices.ContainsFunc(bm.Items, func(it *pb.BrowserMenuItem) bool { return it.Copies }) {
		return bm.Items[i].Copies
	}
	switch bm.Items[i].Title {
	case "Copy", "Cut", "Copy Link Address":
		return true
	}
	return false
}

// clipboardTo is who a host's BrowserClipboard for pane goes to: the client
// that asked for a copy of it first, just now, once. A host can't
// otherwise set your clipboard. Callers hold d.mu.
func (l *peerLink) clipboardTo(pane string) *conn {
	q := l.copyAsk[pane]
	for len(q) > 0 {
		a := q[0]
		q = q[1:]
		if time.Since(a.at) <= copyAnswerWithin {
			l.setCopyAsk(pane, q)
			return a.c
		}
	}
	l.setCopyAsk(pane, nil)
	return nil
}

func (l *peerLink) setCopyAsk(pane string, q []copyRequest) {
	if len(q) == 0 {
		delete(l.copyAsk, pane)
	} else {
		l.copyAsk[pane] = q
	}
}

func newLink(d *Daemon, tok *pb.InviteToken) *peerLink {
	return &peerLink{d: d, tok: tok, ready: make(chan struct{}), stop: make(chan struct{}),
		groups: map[string]*pb.Group{}, tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{},
		attn: map[string]*pb.AttentionItem{}, grants: map[string]*pb.GrantRequest{}, scheds: map[string]*pb.Schedule{}, people: map[string]*pb.Person{},
		attached: map[string]map[*conn]bool{}, watching: map[string]map[*conn]bool{}, paging: map[string]*conn{}, logs: map[string]*pb.WatchLog{}, logTo: map[string]*conn{}, inputs: map[string]*conn{}, copyAsk: map[string][]copyRequest{}, menus: map[int32]*pb.BrowserMenu{}, menuTo: map[int32]*conn{}}
}

// loadLinks restores links to hosts (with their last known state, offline
// until they reconnect) and starts connecting. Callers hold d.mu.
func (d *Daemon) loadLinks() {
	recs, _ := d.store.Records("peerlink")
	caches, _ := d.store.Records("peercache")
	for key, b := range recs {
		tok := &pb.InviteToken{}
		if proto.Unmarshal(b, tok) != nil {
			continue
		}
		tok.HostName, tok.GroupName = cleanLine(tok.HostName, nameMax), cleanLine(tok.GroupName, nameMax) // (kept by an earlier build)
		l := newLink(d, tok)
		if c, ok := caches[key]; ok {
			u := &pb.StateUpdate{}
			if proto.Unmarshal(c, u) == nil {
				l.applyLocked(u, false)
			}
		}
		d.links[key] = l
		go l.run()
	}
}

// ---------- joining and leaving ----------

// joinShared joins a Group from its invite link; the reply waits for the
// host's approval.
func (d *Daemon) joinShared(c *conn, id uint64, link string) (*pb.ServerMessage, error) {
	raw, ok := strings.CutPrefix(strings.TrimSpace(link), "hammock://join/")
	if !ok {
		return nil, errors.New("that isn't a Hammock invite link")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	tok := &pb.InviteToken{}
	if err != nil || proto.Unmarshal(b, tok) != nil || len(tok.Addresses) == 0 || tok.CertSha256 == "" || tok.HostUserId == "" {
		return nil, errors.New("that invite link is damaged; ask for a new one")
	}
	// Its names are shown, and put in errors and the audit log.
	tok.HostName, tok.GroupName = cleanLine(strings.TrimSpace(tok.HostName), nameMax), cleanLine(strings.TrimSpace(tok.GroupName), nameMax)
	if tok.HostUserId == d.id.me.UserId {
		return nil, errors.New("that Group is on this Mac")
	}
	secret := tok.Secret
	d.mu.Lock()
	l := d.links[linkKey(tok)]
	if l == nil {
		keep := proto.CloneOf(tok)
		keep.Secret = nil
		l = newLink(d, keep)
		d.links[linkKey(tok)] = l
		d.persist(d.store.PutRecord("peerlink", linkKey(tok), keep))
		go l.run()
	} else {
		l.tok.Addresses = tok.Addresses // the host may have moved
	}
	l.pending++
	d.mu.Unlock()

	go func() {
		reply := &pb.ServerMessage{ReplyTo: id}
		groupID, err := l.join(secret)
		if err != nil {
			reply.Msg = &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}
		} else {
			reply.Msg = &pb.ServerMessage_Created{Created: &pb.Created{Id: groupID}}
		}
		d.mu.Lock()
		l.pending--
		d.mu.Unlock()
		if id != 0 {
			c.send(reply)
		}
		if err != nil {
			l.tidy() // the Group's own update arrives separately on success
		}
	}()
	return nil, errDeferred
}

// join asks the host to let us in, waiting (up to an hour) for its answer.
func (l *peerLink) join(secret []byte) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	cl, err := l.connected(ctx, 30*time.Second)
	if err != nil {
		return "", err
	}
	r, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerJoin{PeerJoin: &pb.PeerJoin{Secret: secret}}})
	if err != nil {
		return "", err
	}
	return r.GetPeerJoined().GetGroupId(), nil
}

// leaveShared leaves a Group someone shared with you.
func (d *Daemon) leaveShared(groupID string) error {
	d.mu.Lock()
	l := d.routes[groupID]
	d.mu.Unlock()
	if l == nil || l.groups[groupID] == nil {
		return errors.New("that isn't a Group shared with you")
	}
	if l.linked {
		return errors.New("that Group is on your other computer: close it instead")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if cl, err := l.connected(ctx, 5*time.Second); err == nil {
		if _, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SetMember{SetMember: &pb.SetMember{GroupId: groupID, UserId: d.id.me.UserId}}}); err != nil {
			return err
		}
		return nil // the host's update removes it
	}
	// The host is offline: forget it here; it'll be gone next time too.
	d.mu.Lock()
	l.applyLocked(&pb.StateUpdate{DeletedGroupIds: []string{groupID}}, true)
	d.mu.Unlock()
	l.tidy()
	return nil
}

// tidy ends a link that no longer has any Groups or joins waiting.
func (l *peerLink) tidy() {
	d := l.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(l.groups) > 0 || l.pending > 0 || l.linked || d.links[linkKey(l.tok)] != l {
		return
	}
	l.gone = true
	delete(d.links, linkKey(l.tok))
	d.persist(d.store.DeleteRecord("peerlink", linkKey(l.tok)))
	d.persist(d.store.DeleteRecord("peercache", linkKey(l.tok)))
	go l.close()
}

// ---------- the connection ----------

// current: l hasn't been closed (by leaving or unlinking). Callers hold d.mu.
func (l *peerLink) current() bool { return !l.gone }

func (l *peerLink) close() {
	l.cmu.Lock()
	defer l.cmu.Unlock()
	if !l.closed {
		l.closed = true
		close(l.stop)
		if l.cl != nil {
			l.cl.Close()
		}
	}
}

// connected waits (up to wait) for the link to be up.
func (l *peerLink) connected(ctx context.Context, wait time.Duration) (*client.Client, error) {
	l.cmu.Lock()
	ready, cl := l.ready, l.cl
	l.cmu.Unlock()
	select {
	case <-ready:
		return cl, nil
	default:
	}
	select {
	case <-ready:
		l.cmu.Lock()
		defer l.cmu.Unlock()
		if l.cl == nil {
			// It connected and has dropped again since (a hang-up).
			return nil, errors.New("link dropped")
		}
		return l.cl, nil
	case <-time.After(wait):
		l.d.mu.Lock()
		name := l.tok.HostName
		l.d.mu.Unlock()
		return nil, fmt.Errorf("can't reach %s; is it on, with Tailscale running?", strings.TrimSuffix(name, ".local"))
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-l.stop:
		return nil, errors.New("link closed")
	}
}

// run keeps the link up, reconnecting with backoff.
// errNotLinkedYet: a link just made isn't made on the other side yet; its
// link tries again every second (not backing off) until linkGrace is up.
var errNotLinkedYet = errors.New("not linked there yet")

func (l *peerLink) run() {
	backoff := time.Second
	for {
		select {
		case <-l.stop:
			return
		default:
		}
		cl, err := l.dial()
		if err == nil {
			backoff = time.Second
			// In no Group there and no join waiting (removed, its Groups
			// closed or unshared, or left while it was away): the link ends
			// here, rather than signing in again and again as a stranger.
			l.tidy()
			l.serve(cl)
		} else {
			l.d.mu.Lock()
			host := l.tok.HostName // (ensureLink renames it, under d.mu)
			l.d.mu.Unlock()
			l.d.log.Debug("peer link", "host", host, "err", err)
		}
		l.setOffline()
		select {
		case <-l.stop:
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second) // (a link just made, too: its grace is short)
	}
}

// dial connects, signs in and subscribes, applying the host's full state.
// Its addresses are tried at once (those on a local network only from
// that network: worthTrying), the first to sign in kept.
func (l *peerLink) dial() (*client.Client, error) {
	l.d.mu.Lock()
	addrs := slices.Clone(l.tok.Addresses) // (ensureLink updates them, under d.mu)
	lan := slices.Clone(l.tok.LanSubnets)
	l.d.mu.Unlock()
	try := worthTrying(addrs, lan)
	if len(try) == 0 {
		return nil, errors.New("not on any network it's reached on")
	}
	cl, addr, err, errs := raceDial(context.Background(), try, 10*time.Second, func(ctx context.Context, addr string) (*client.Client, error) {
		cl, err := client.DialRemote(ctx, addr, l.tok.CertSha256, "peer", l.d.id.me.DeviceName)
		if err != nil {
			return nil, err
		}
		if err := l.signIn(ctx, cl); err != nil {
			cl.Close()
			return nil, err
		}
		return cl, nil
	})
	if err != nil {
		// Something else answers where it was: its certificate changed (set
		// up again). Only at a Tailscale address is that its own: a local
		// network's or a VPN's may be another computer's by now (or, away
		// from that network, anyone's), and a name anyone's answer.
		for a, e := range errs {
			if cm := (*client.CertMismatchError)(nil); errors.As(e, &cm) && tailscaleAddr(a) {
				l.d.certChanged(l, true)
				break
			}
		}
		return nil, err
	}
	l.cmu.Lock()
	l.addr = addr
	l.cmu.Unlock()
	return cl, nil
}

func (l *peerLink) signIn(ctx context.Context, cl *client.Client) error {
	var nonce []byte
	for nonce == nil {
		select {
		case ev, ok := <-cl.Events:
			if !ok {
				return errors.New("closed")
			}
			if c := ev.GetAuthChallenge(); c != nil {
				if len(c.Nonce) != client.NonceSize {
					return errors.New("bad challenge")
				}
				nonce = c.Nonce
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	d := l.d
	d.mu.Lock()
	me := proto.CloneOf(d.id.me)
	key := d.id.key
	d.mu.Unlock()
	if len(me.Avatar) > 256<<10 {
		me.Avatar = nil // the host wouldn't keep it, and it must fit before sign-in
	}
	hostNonce := make([]byte, client.NonceSize)
	_, _ = rand.Read(hostNonce)
	welcome, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerHello{PeerHello: peerHelloFor(key, me, l.tok.CertSha256, nonce, hostNonce)}})
	if err != nil {
		return err
	}
	// The host proves it's the one the invite named: its user key, bound to
	// this certificate and our nonce.
	if uid, err := verifyWelcome(welcome.GetPeerWelcome(), l.tok.CertSha256, hostNonce); err != nil || uid != l.tok.HostUserId {
		return errors.New("the host couldn't prove who it is")
	}
	r, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	if err != nil {
		return err
	}
	d.mu.Lock()
	if !l.current() {
		// Closed (unlinked, left) while signing in: show nothing of it.
		d.mu.Unlock()
		return errors.New("link closed")
	}
	if sl := d.comp.links[l.tok.HostUserId]; l.linked && sl != nil && len(sl.Record.GetKey()) > 0 &&
		!bytes.Equal(sl.Record.Key, welcome.GetPeerWelcome().GetPublicKey()) {
		// Its id, but not the key it was linked with.
		d.mu.Unlock()
		return errors.New("the host couldn't prove who it is")
	}
	l.online, l.onlineAt = true, time.Now()
	l.applyLocked(r.GetState(), true)
	if l.notYet {
		// A link just made, not yet made there: try again (not shown as
		// linked and online meanwhile).
		l.notYet, l.online = false, false
		d.mu.Unlock()
		return errNotLinkedYet
	}
	l.sentAt = ""
	d.certChangedLocked(l, false)
	if l.linked {
		// Its key, as it just proved it (a link from an earlier build may
		// not have it yet: introductions need it).
		if sl := d.comp.links[l.tok.HostUserId]; sl != nil && len(sl.Record.GetKey()) == 0 {
			sl.Record.Key = welcome.GetPeerWelcome().GetPublicKey()
			d.persist(d.store.PutRecord("complink", l.tok.HostUserId, sl))
		}
		d.publishComputers()
		// It and the others are introduced to each other, both ways (one
		// that was off, or whose key wasn't known, hears it now).
		d.introduceAll()
		d.deliverUnlinks() // requests waiting for it
	}
	// Pick up where your clients were.
	for pane := range l.attached {
		_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: pane}}})
	}
	for pane := range l.watching {
		_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: pane, Watch: true}}})
	}
	for _, w := range l.logs {
		_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchLog{WatchLog: w}})
	}
	d.mu.Unlock()
	l.cmu.Lock()
	if l.closed {
		l.cmu.Unlock()
		return errors.New("link closed")
	}
	l.cl = cl
	close(l.ready)
	l.cmu.Unlock()
	d.mu.Lock()
	d.presenceChanged()
	d.mu.Unlock()
	return nil
}

// serve relays what the host sends until the connection ends.
func (l *peerLink) serve(cl *client.Client) {
	d := l.d
	for ev := range cl.Events {
		d.mu.Lock()
		if !l.current() {
			d.mu.Unlock() // closed: what was already on its way is dropped
			continue
		}
		switch m := ev.Msg.(type) {
		case *pb.ServerMessage_State:
			l.applyLocked(m.State, true)
		case *pb.ServerMessage_PaneSnapshot:
			for c := range l.attached[m.PaneSnapshot.PaneId] {
				c.send(ev)
			}
		case *pb.ServerMessage_PaneOutput:
			for c := range l.attached[m.PaneOutput.PaneId] {
				c.send(ev)
			}
		case *pb.ServerMessage_EditorUpdate:
			for c := range l.attached[m.EditorUpdate.PaneId] {
				c.send(ev)
			}
		case *pb.ServerMessage_BrowserMenu:
			if c := l.inputs[m.BrowserMenu.PaneId]; c != nil {
				l.menus[m.BrowserMenu.Seq], l.menuTo[m.BrowserMenu.Seq] = m.BrowserMenu, c
				c.send(ev)
			} else {
				l.dismissMenus([]*pb.BrowserMenu{m.BrowserMenu})
			}
		case *pb.ServerMessage_ScreenCursor:
			l.d.media.relayCursor(l, m.ScreenCursor)
		case *pb.ServerMessage_ScreenState:
			l.d.media.relayState(l, m.ScreenState)
		case *pb.ServerMessage_BrowserClipboard:
			// Only in answer to a copy your client asked for.
			if c := l.clipboardTo(m.BrowserClipboard.PaneId); c != nil {
				c.send(ev)
			}
		case *pb.ServerMessage_LogView:
			if c := l.logTo[m.LogView.PaneId]; c != nil {
				c.send(ev)
			}
		case *pb.ServerMessage_AgentView:
			pane := m.AgentView.PaneId
			if m.AgentView.Older {
				if c := l.paging[pane]; c != nil {
					c.send(ev)
				}
			} else {
				for c := range l.watching[pane] {
					c.send(ev)
				}
			}
		}
		d.mu.Unlock()
	}
	l.cmu.Lock()
	l.cl = nil
	l.ready = make(chan struct{})
	l.cmu.Unlock()
}

// setOffline dims the host's Groups until it's back.
func (l *peerLink) setOffline() {
	d := l.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if !l.current() || !l.online && len(l.groups) == 0 {
		return
	}
	wasOnline := l.online
	l.online = false
	if l.linked && wasOnline {
		if sl := d.comp.links[l.tok.HostUserId]; sl != nil {
			sl.Record.LastSeenUnixMs = time.Now().UnixMilli()
			d.persist(d.store.PutRecord("complink", l.tok.HostUserId, sl))
		}
		d.publishComputers()
	}
	u := &pb.StateUpdate{}
	for _, g := range l.groups {
		g.Offline = true
		u.Groups = append(u.Groups, proto.CloneOf(g))
		for _, m := range g.Members {
			m.Online, m.Typing = false, false
		}
	}
	for id := range l.grants {
		u.ResolvedGrantIds = append(u.ResolvedGrantIds, id) // (it asks again when it's back)
	}
	l.drop(u)
	l.save(true)
	if len(u.Groups)+len(u.ResolvedGrantIds) > 0 {
		d.publish(u)
	}
}

// ---------- the host's state, here ----------

// applyLocked merges an update from the host into the copy kept here and
// tells your clients. Ids that clash with your own (or another host's) are
// ignored. Callers
// hold d.mu.
func (l *peerLink) applyLocked(u *pb.StateUpdate, publish bool) {
	d := l.d
	if u == nil {
		return
	}
	cleanHostState(u)
	out := &pb.StateUpdate{}
	if u.Full {
		keep := map[string]bool{}
		for _, g := range u.Groups {
			keep[g.Id] = true
		}
		for _, t := range u.Tabs {
			keep[t.Id] = true
		}
		for _, p := range u.Panes {
			keep[p.Id] = true
		}
		for _, a := range u.Attention {
			keep[a.Id] = true
		}
		for id := range l.groups {
			if !keep[id] {
				out.DeletedGroupIds = append(out.DeletedGroupIds, id)
			}
		}
		for id := range l.tabs {
			if !keep[id] {
				out.DeletedTabIds = append(out.DeletedTabIds, id)
			}
		}
		for id := range l.panes {
			if !keep[id] {
				out.DeletedPaneIds = append(out.DeletedPaneIds, id)
			}
		}
		// Only what no longer needs you: clients add the items sent, then
		// remove those cleared, so clearing one that's still waiting (after
		// each wake) would drop it from Needs You.
		for id := range l.attn {
			if !keep[id] {
				out.ClearedAttentionIds = append(out.ClearedAttentionIds, id)
			}
		}
		for _, g := range u.Grants {
			keep[g.Id] = true
		}
		for id := range l.grants {
			if !keep[id] {
				out.ResolvedGrantIds = append(out.ResolvedGrantIds, id)
			}
		}
		for _, s := range u.Schedules {
			keep[s.Id] = true
		}
		for id := range l.scheds {
			if !keep[id] || !l.linked {
				out.DeletedScheduleIds = append(out.DeletedScheduleIds, id)
			}
		}
		l.drop(out)
	}
	// A linked computer's scheduled tasks (never a guest's host's).
	for _, s := range u.Schedules {
		if !l.linked || d.sched.list[s.Id] != nil {
			continue
		}
		if o := d.routes[s.Id]; o != nil && o != l {
			continue
		}
		s = proto.CloneOf(s)
		s.ComputerId = l.tok.HostUserId
		l.scheds[s.Id] = s
		d.routes[s.Id] = l
		out.Schedules = append(out.Schedules, s)
	}
	// Ids that are this Mac's, or another host's, stay theirs.
	local := func(id string) bool {
		if o := d.routes[id]; o != nil && o != l {
			return true
		}
		return d.groups[id] != nil || d.tabs[id] != nil || d.panes[id] != nil
	}
	for _, g := range u.Groups {
		if local(g.Id) {
			continue
		}
		g = proto.CloneOf(g)
		g.HostUserId = l.tok.HostUserId // proven when the link signed in
		g.MyRole = memberRole(g, d.id.me.UserId)
		if l.linked {
			g.MyRole = pb.Role_ROLE_OWNER
		}
		g.Offline = false
		g.Order = d.sharedOrder(g.Id)
		g.InviteActive = false
		l.groups[g.Id] = g
		d.routes[g.Id] = l
		out.Groups = append(out.Groups, proto.CloneOf(g))
	}
	// A host's tabs go only in its own Groups, its panes in its own tabs,
	// and what needs you only for its own panes: never into this Mac's.
	for _, t := range u.Tabs {
		if local(t.Id) || l.groups[t.GroupId] == nil {
			continue
		}
		l.tabs[t.Id] = t
		d.routes[t.Id] = l
		out.Tabs = append(out.Tabs, t)
	}
	for _, p := range u.Panes {
		if local(p.Id) || l.tabs[p.TabId] == nil {
			continue
		}
		l.panes[p.Id] = p
		d.routes[p.Id] = l
		out.Panes = append(out.Panes, p)
	}
	for _, a := range u.Attention {
		if d.attention[a.Id] != nil || local(a.Id) || l.panes[a.PaneId] == nil {
			continue
		}
		// For a linked computer's phones: only items raised since the last
		// update are news (not those a reconnect's full update sends again).
		if was := l.attn[a.Id]; l.linked && l.online && (was == nil || !sameAttention(was, a)) {
			d.pushLinkedAttention(l, a)
		}
		l.attn[a.Id] = a
		d.routes[a.Id] = l
		out.Attention = append(out.Attention, a)
	}
	// A linked computer's agents asking for permission (yours to answer
	// from here): only for its own panes. Never a guest's host's.
	for _, g := range u.Grants {
		if !l.linked || d.grants[g.Id] != nil || local(g.Id) || l.panes[g.RequesterPaneId] == nil {
			continue
		}
		l.grants[g.Id] = g
		d.routes[g.Id] = l
		out.Grants = append(out.Grants, g)
	}
	// People: only the host and members of its Groups, and never someone
	// known here first-hand (you, your guests) or through another host.
	known := map[string]bool{l.tok.HostUserId: true}
	for _, g := range l.groups {
		for _, m := range g.Members {
			known[m.UserId] = true
		}
	}
	for _, p := range u.People {
		if p.UserId == d.id.me.UserId || !known[p.UserId] || d.people[p.UserId] != nil || d.otherLinkKnows(l, p.UserId) {
			continue
		}
		p = proto.CloneOf(p)
		p.Me = false
		l.people[p.UserId] = p
		out.People = append(out.People, p)
	}
	gone := &pb.StateUpdate{DeletedGroupIds: u.DeletedGroupIds, DeletedTabIds: u.DeletedTabIds,
		DeletedPaneIds: u.DeletedPaneIds, ClearedAttentionIds: u.ClearedAttentionIds, ResolvedGrantIds: u.ResolvedGrantIds,
		DeletedScheduleIds: u.DeletedScheduleIds}
	l.drop(gone)
	out.DeletedScheduleIds = append(out.DeletedScheduleIds, gone.DeletedScheduleIds...)
	out.ResolvedGrantIds = append(out.ResolvedGrantIds, gone.ResolvedGrantIds...)
	out.DeletedGroupIds = append(out.DeletedGroupIds, gone.DeletedGroupIds...)
	out.DeletedTabIds = append(out.DeletedTabIds, gone.DeletedTabIds...)
	out.DeletedPaneIds = append(out.DeletedPaneIds, gone.DeletedPaneIds...)
	out.ClearedAttentionIds = append(out.ClearedAttentionIds, gone.ClearedAttentionIds...)
	l.save(u.Full)
	if publish {
		d.publish(out)
	}
	if l.linked && u.Computers != nil {
		d.mergeComputers(u.Computers, l.tok.HostUserId)
	}
}

// sameAttention: b is the same raising of a's item (a push for a is still
// the right one for b). The host raises an item anew, with a new time, each
// time it pushes its own.
func sameAttention(a, b *pb.AttentionItem) bool {
	return a.CreatedUnixMs == b.CreatedUnixMs && a.State == b.State && a.Question == b.Question && a.Summary == b.Summary
}

// otherLinkKnows: a link other than l has this person. Callers hold d.mu.
func (d *Daemon) otherLinkKnows(l *peerLink, uid string) bool {
	for _, o := range d.links {
		if o != l && o.people[uid] != nil {
			return true
		}
	}
	return false
}

// drop forgets the ids in u (keeping only those that were ours). Callers hold d.mu.
func (l *peerLink) drop(u *pb.StateUpdate) {
	filter := func(ids []string, has func(string) bool, del func(string)) []string {
		var kept []string
		for _, id := range ids {
			if has(id) {
				del(id)
				if l.d.routes[id] == l {
					delete(l.d.routes, id)
				}
				kept = append(kept, id)
			}
		}
		return kept
	}
	u.DeletedGroupIds = filter(u.DeletedGroupIds, func(id string) bool { return l.groups[id] != nil }, func(id string) { delete(l.groups, id) })
	u.DeletedTabIds = filter(u.DeletedTabIds, func(id string) bool { return l.tabs[id] != nil }, func(id string) { delete(l.tabs, id) })
	u.DeletedPaneIds = filter(u.DeletedPaneIds, func(id string) bool { return l.panes[id] != nil }, func(id string) {
		delete(l.panes, id)
		delete(l.attached, id)
		delete(l.watching, id)
		delete(l.logs, id)
		delete(l.logTo, id)
	})
	u.ClearedAttentionIds = filter(u.ClearedAttentionIds, func(id string) bool { return l.attn[id] != nil }, func(id string) { delete(l.attn, id) })
	u.ResolvedGrantIds = filter(u.ResolvedGrantIds, func(id string) bool { return l.grants[id] != nil }, func(id string) { delete(l.grants, id) })
	u.DeletedScheduleIds = filter(u.DeletedScheduleIds, func(id string) bool { return l.scheds[id] != nil }, func(id string) { delete(l.scheds, id) })
}

// save keeps the host's state for when it's offline (at most every few
// seconds unless now). Callers hold d.mu.
func (l *peerLink) save(now bool) {
	if !now && time.Since(l.savedAt) < 5*time.Second {
		return
	}
	l.savedAt = time.Now()
	snap := &pb.StateUpdate{Full: true}
	l.fill(snap)
	l.d.persist(l.d.store.PutRecord("peercache", linkKey(l.tok), snap))
}

// fill adds the host's Groups (and what's in them) to u. Callers hold d.mu.
func (l *peerLink) fill(u *pb.StateUpdate) {
	for _, g := range l.groups {
		x := proto.CloneOf(g)
		x.Offline = !l.online
		u.Groups = append(u.Groups, x)
	}
	for _, t := range l.tabs {
		u.Tabs = append(u.Tabs, proto.CloneOf(t))
	}
	for _, p := range l.panes {
		u.Panes = append(u.Panes, proto.CloneOf(p))
	}
	for _, a := range l.attn {
		u.Attention = append(u.Attention, proto.CloneOf(a))
	}
	for _, p := range l.people {
		u.People = append(u.People, proto.CloneOf(p))
	}
}

// remoteState adds every link's Groups to a full state, and linked
// computers' grant requests. Callers hold d.mu.
func (d *Daemon) remoteState(u *pb.StateUpdate) {
	for _, l := range d.links {
		l.fill(u)
		for _, g := range l.grants {
			u.Grants = append(u.Grants, proto.CloneOf(g))
		}
		for _, s := range l.scheds {
			u.Schedules = append(u.Schedules, proto.CloneOf(s))
		}
	}
}

// ---------- your clients, in shared Groups ----------

// isAgent: an agent's connection (the MCP server, or the CLI run inside a pane).
func (c *conn) isAgent() bool { return c.kind == "mcp" || (c.kind == "cli" && c.fromPane != "") }

func filterSlice[T any](s []T, keep func(T) bool) []T {
	var out []T
	for _, x := range s {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}

// msgTarget is the Group, tab, pane or attention item a message is about.
func msgTarget(msg *pb.ClientMessage) string {
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_CreateTab:
		return m.CreateTab.GroupId
	case *pb.ClientMessage_SaveSchedule:
		if s := m.SaveSchedule.Schedule; s != nil {
			if s.Id == "" {
				return s.GroupId // a new one: made where its Group is
			}
			return s.Id
		}
	case *pb.ClientMessage_DeleteSchedule:
		return m.DeleteSchedule.ScheduleId
	case *pb.ClientMessage_RunSchedule:
		return m.RunSchedule.ScheduleId
	case *pb.ClientMessage_SetSchedulePaused:
		return m.SetSchedulePaused.ScheduleId
	case *pb.ClientMessage_SkipScheduleRun:
		return m.SkipScheduleRun.ScheduleId
	case *pb.ClientMessage_ShowOutput:
		return cmpOr(m.ShowOutput.IntoPaneId, m.ShowOutput.PaneId)
	case *pb.ClientMessage_StartAgent:
		return m.StartAgent.GroupId
	case *pb.ClientMessage_ListFolders:
		return m.ListFolders.GroupId
	case *pb.ClientMessage_RenameGroup:
		return m.RenameGroup.GroupId
	case *pb.ClientMessage_SetGroupIcon:
		return m.SetGroupIcon.GroupId
	case *pb.ClientMessage_SetGroupFolder:
		return m.SetGroupFolder.GroupId
	case *pb.ClientMessage_SetGroupIconImage:
		return m.SetGroupIconImage.GroupId
	case *pb.ClientMessage_ReadGroupIcon:
		return m.ReadGroupIcon.GroupId
	case *pb.ClientMessage_CloseGroup:
		return m.CloseGroup.GroupId
	case *pb.ClientMessage_CreateInvite:
		return m.CreateInvite.GroupId
	case *pb.ClientMessage_StopSharing:
		return m.StopSharing.GroupId
	case *pb.ClientMessage_RenameTab:
		return m.RenameTab.TabId
	case *pb.ClientMessage_CloseTab:
		return m.CloseTab.TabId
	case *pb.ClientMessage_SetLayout:
		return m.SetLayout.TabId
	case *pb.ClientMessage_FloatPane:
		return floatTarget(m.FloatPane)
	case *pb.ClientMessage_MoveTab:
		return m.MoveTab.TabId
	case *pb.ClientMessage_MergeTab:
		return m.MergeTab.TabId
	case *pb.ClientMessage_BreakOutPane:
		return m.BreakOutPane.PaneId
	case *pb.ClientMessage_ReplacePane:
		return m.ReplacePane.PaneId
	case *pb.ClientMessage_CreatePane:
		return m.CreatePane.SplitPaneId
	case *pb.ClientMessage_ClosePane:
		return m.ClosePane.PaneId
	case *pb.ClientMessage_AttachPane:
		return m.AttachPane.PaneId
	case *pb.ClientMessage_DetachPane:
		return m.DetachPane.PaneId
	case *pb.ClientMessage_Input:
		return m.Input.PaneId
	case *pb.ClientMessage_Resize:
		return m.Resize.PaneId
	case *pb.ClientMessage_Focus:
		return m.Focus.PaneId
	case *pb.ClientMessage_ReadPane:
		return m.ReadPane.PaneId
	case *pb.ClientMessage_SignalPane:
		return m.SignalPane.PaneId
	case *pb.ClientMessage_OpenMedia:
		return m.OpenMedia.PaneId
	case *pb.ClientMessage_SimulatorCall:
		return m.SimulatorCall.PaneId
	case *pb.ClientMessage_BrowserInput:
		return m.BrowserInput.PaneId
	case *pb.ClientMessage_BrowserMenuChoice:
		return m.BrowserMenuChoice.PaneId
	case *pb.ClientMessage_BrowserNavigate:
		return m.BrowserNavigate.PaneId
	case *pb.ClientMessage_WatchAgent:
		return m.WatchAgent.PaneId
	case *pb.ClientMessage_AgentAnswer:
		return m.AgentAnswer.PaneId
	case *pb.ClientMessage_AgentSend:
		return m.AgentSend.PaneId
	case *pb.ClientMessage_ReadAgentFile:
		return m.ReadAgentFile.PaneId
	case *pb.ClientMessage_WatchLog:
		return m.WatchLog.PaneId
	case *pb.ClientMessage_ReadLog:
		return m.ReadLog.PaneId
	case *pb.ClientMessage_ClearLog:
		return m.ClearLog.PaneId
	case *pb.ClientMessage_EditorPush:
		return m.EditorPush.PaneId
	case *pb.ClientMessage_EditorSelect:
		return m.EditorSelect.PaneId
	case *pb.ClientMessage_EditorCommand:
		return m.EditorCommand.PaneId
	case *pb.ClientMessage_ReadEditor:
		return m.ReadEditor.PaneId
	case *pb.ClientMessage_ReadDiff:
		return m.ReadDiff.PaneId
	case *pb.ClientMessage_ReadDiffFile:
		return m.ReadDiffFile.PaneId
	case *pb.ClientMessage_DiffCommand:
		return m.DiffCommand.PaneId
	case *pb.ClientMessage_ReadReview:
		return m.ReadReview.PaneId
	case *pb.ClientMessage_ReviewCommand:
		return m.ReviewCommand.PaneId
	case *pb.ClientMessage_ListFiles:
		if m.ListFiles.GroupId != "" {
			return m.ListFiles.GroupId
		}
		return m.ListFiles.PaneId
	case *pb.ClientMessage_SwitchAgent:
		return m.SwitchAgent.PaneId
	case *pb.ClientMessage_UploadFile:
		return m.UploadFile.PaneId
	case *pb.ClientMessage_ClearAttention:
		return m.ClearAttention.Id
	case *pb.ClientMessage_RespondGrant:
		return m.RespondGrant.Id
	}
	return ""
}

// routeFor is the link a message from your client goes to, if it's about
// something in a Group shared with you.
func (d *Daemon) routeFor(c *conn, msg *pb.ClientMessage) (*peerLink, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// One of your other computers, named (a new Group there, its sessions).
	if cid := msgComputer(msg); cid != "" && cid != d.id.me.UserId {
		l := d.linkOf(cid)
		if l == nil || !d.isLinked(cid) {
			return nil, errors.New("no such linked computer")
		}
		if !l.online {
			return nil, fmt.Errorf("%s is offline", strings.TrimSuffix(l.tok.HostName, ".local"))
		}
		return l, nil
	}
	id := msgTarget(msg)
	if id == "" {
		return nil, nil
	}
	l := d.routes[id]
	if l == nil {
		return nil, nil
	}
	if l.linked {
		// Your own computer: its Groups are yours to change.
		if !l.online {
			if _, ok := msg.Msg.(*pb.ClientMessage_DetachPane); !ok {
				return nil, fmt.Errorf("%s is offline", strings.TrimSuffix(l.tok.HostName, ".local"))
			}
		}
		switch msg.Msg.(type) {
		case *pb.ClientMessage_CreateInvite, *pb.ClientMessage_StopSharing:
			return nil, errors.New("share it from the computer it's on")
		}
		return l, nil
	}
	// Your agents' scope is checked before this (agentCheck, caller.go):
	// they reach a shared Group's panes only with a grant.
	switch msg.Msg.(type) {
	case *pb.ClientMessage_CloseGroup:
		return nil, errors.New("that Group is shared with you: leave it instead")
	case *pb.ClientMessage_RenameGroup, *pb.ClientMessage_SetGroupIcon, *pb.ClientMessage_SetGroupIconImage, *pb.ClientMessage_SetGroupFolder, *pb.ClientMessage_CreateInvite, *pb.ClientMessage_StopSharing:
		return nil, errors.New("only its host can change that")
	}
	if !l.online {
		if _, ok := msg.Msg.(*pb.ClientMessage_DetachPane); !ok {
			return nil, fmt.Errorf("%s is offline", strings.TrimSuffix(l.tok.HostName, ".local"))
		}
	}
	return l, nil
}

// forward sends a message from your client on to the host, replying (and
// relaying what it streams back) as the host does.
func (l *peerLink) forward(c *conn, msg *pb.ClientMessage) (*pb.ServerMessage, error) {
	return l.forwardThen(c, msg, nil)
}

// call sends msg over the link and waits for the reply (off d.mu).
func (l *peerLink) call(msg *pb.ClientMessage) (*pb.ServerMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cl, err := l.connected(ctx, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return cl.Call(ctx, msg)
}

// forwardThen is forward, with then (if set) given the host's answer
// before the client is: it returns what the client gets. Off d.mu.
func (l *peerLink) forwardThen(c *conn, msg *pb.ClientMessage, then func(*pb.ServerMessage, error) *pb.ServerMessage) (*pb.ServerMessage, error) {
	d := l.d
	d.mu.Lock()
	l.askedCopy(c, msg)
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_AttachPane:
		set(l.attached, m.AttachPane.PaneId, c, true)
	case *pb.ClientMessage_BrowserInput:
		l.inputs[m.BrowserInput.PaneId] = c
	case *pb.ClientMessage_BrowserMenuChoice:
		delete(l.menus, m.BrowserMenuChoice.Seq)
		delete(l.menuTo, m.BrowserMenuChoice.Seq)
	case *pb.ClientMessage_DetachPane:
		if !set(l.attached, m.DetachPane.PaneId, c, false) {
			d.mu.Unlock()
			return nil, nil // others are still viewing it
		}
	case *pb.ClientMessage_WatchAgent:
		w := m.WatchAgent
		switch {
		case w.BeforeBlockId != "":
			l.paging[w.PaneId] = c
		case w.Watch:
			set(l.watching, w.PaneId, c, true)
		default:
			if !set(l.watching, w.PaneId, c, false) {
				d.mu.Unlock()
				return nil, nil
			}
		}
	case *pb.ClientMessage_WatchLog:
		w := m.WatchLog
		switch {
		case w.BeforeSeq != 0:
			l.logTo[w.PaneId] = c
		case w.Watch:
			l.logs[w.PaneId], l.logTo[w.PaneId] = proto.CloneOf(w), c
		case l.logTo[w.PaneId] != c:
			d.mu.Unlock()
			return nil, nil // another client's watch stays
		default:
			delete(l.logs, w.PaneId)
			delete(l.logTo, w.PaneId)
		}
	case *pb.ClientMessage_Focus:
		c.focus = m.Focus.PaneId
		d.presenceChanged()
	}
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	cl, err := l.connected(ctx, 2*time.Second)
	cancel()
	if err != nil {
		return nil, err
	}
	up := proto.CloneOf(msg)
	if msg.Id == 0 && then == nil {
		return nil, cl.Send(context.Background(), up)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := cl.Call(ctx, up)
		if then != nil {
			r = then(r, err)
		}
		if err != nil && (then == nil || r == nil) {
			r = &pb.ServerMessage{Msg: &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}}
		}
		if msg.Id == 0 {
			return // (then has had the answer; nobody asked for it here)
		}
		r = proto.CloneOf(r)
		r.ReplyTo = msg.Id
		c.send(r)
	}()
	return nil, errDeferred
}

// set adds or removes c from m[key]; removing reports whether m[key] is now
// empty (so the host can stop sending it). Callers hold d.mu.
func set(m map[string]map[*conn]bool, key string, c *conn, on bool) bool {
	if on {
		if m[key] == nil {
			m[key] = map[*conn]bool{}
		}
		m[key][c] = true
		return false
	}
	delete(m[key], c)
	if len(m[key]) == 0 {
		delete(m, key)
		return true
	}
	return false
}

// localClosed lets the hosts know a client of yours stopped viewing.
// Callers hold d.mu.
func (d *Daemon) localClosed(c *conn) {
	for _, l := range d.links {
		var detach, unwatch, unlog []string
		for pane, cs := range l.attached {
			if cs[c] && set(l.attached, pane, c, false) {
				detach = append(detach, pane)
			}
		}
		for pane, cs := range l.watching {
			if cs[c] && set(l.watching, pane, c, false) {
				unwatch = append(unwatch, pane)
			}
		}
		for pane, pc := range l.paging {
			if pc == c {
				delete(l.paging, pane)
			}
		}
		for pane, lc := range l.logTo {
			if lc == c {
				delete(l.logTo, pane)
				delete(l.logs, pane)
				unlog = append(unlog, pane)
			}
		}
		// Its open page menus are dismissed, or the host's page waits on them.
		for pane, ic := range l.inputs {
			if ic == c {
				delete(l.inputs, pane)
			}
		}
		// Nor does a copy it asked for go to anyone else.
		for pane, q := range l.copyAsk {
			l.setCopyAsk(pane, slices.DeleteFunc(q, func(a copyRequest) bool { return a.c == c }))
		}
		var open []*pb.BrowserMenu
		for seq, mc := range l.menuTo {
			if mc == c {
				open = append(open, l.menus[seq])
				delete(l.menus, seq)
				delete(l.menuTo, seq)
			}
		}
		l.dismissMenus(open)
		if len(detach)+len(unwatch)+len(unlog) == 0 {
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cl, err := l.connected(ctx, time.Second)
			if err != nil {
				return
			}
			for _, p := range detach {
				_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_DetachPane{DetachPane: &pb.DetachPane{PaneId: p}}})
			}
			for _, p := range unwatch {
				_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: p}}})
			}
			for _, p := range unlog {
				_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchLog{WatchLog: &pb.WatchLog{PaneId: p}}})
			}
		}()
	}
	d.presenceChanged()
}

// presenceChanged tells each host where you are in its Groups: the pane a
// client of yours last focused there (a frontmost one first; a window in the
// background still counts as being there). Callers hold d.mu.
func (d *Daemon) presenceChanged() {
	for _, l := range d.links {
		at, front := "", false
		var best time.Time
		for c := range d.conns {
			if c.peer != "" || l.panes[c.focus] == nil {
				continue
			}
			fg := c.presence != nil && c.presence.Foreground
			if at == "" || (fg && !front) || (fg == front && c.lastInput.After(best)) {
				at, front, best = c.focus, fg, c.lastInput
			}
		}
		if at == l.sentAt || !l.online {
			continue
		}
		l.sentAt = at
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if cl, err := l.connected(ctx, time.Second); err == nil {
				_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Presence{Presence: &pb.Presence{Foreground: at != "", FocusedPaneId: at}}})
			}
		}()
	}
}

// uploadFile saves a file dropped on a pane and types its path.
func (d *Daemon) uploadFile(c *conn, m *pb.UploadFile) error {
	d.mu.Lock()
	p := d.panes[m.PaneId]
	d.mu.Unlock()
	if p == nil {
		return fmt.Errorf("no such pane %q", m.PaneId)
	}
	path, err := saveAttachment(&pb.AgentAttachment{Name: m.Name, Data: m.Data})
	if err != nil {
		return err
	}
	text := shellQuote(path) + " "
	if p.bracketedPaste() {
		text = "\x1b[200~" + text + "\x1b[201~"
	}
	_, err = p.input(c, []byte(text))
	return err
}

// dismissMenus answers the host's page menus with "none" (their viewer is
// gone). Callers hold d.mu.
func (l *peerLink) dismissMenus(menus []*pb.BrowserMenu) {
	if len(menus) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cl, err := l.connected(ctx, time.Second)
		if err != nil {
			return
		}
		for _, m := range menus {
			_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserMenuChoice{BrowserMenuChoice: &pb.BrowserMenuChoice{
				PaneId: m.PaneId, Seq: m.Seq, Kind: m.Kind, Index: -1}}})
		}
	}()
}
