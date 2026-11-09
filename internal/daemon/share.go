package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Hosting shared Groups (ADR-0008). A guest's daemon connects to the remote
// listener as a peer, proves who it is (PeerHello, signing the nonce, bound
// to this host's certificate, with its user key) and asks to join with a Group's invite secret; the host approves
// each person once per Group, as a viewer or a driver. A peer only ever sees
// the Groups it's a member of, and everything it sends is checked against
// its role there.

const typingFor = 2 * time.Second

type pendingJoin struct {
	req   *pb.JoinRequest
	c     *conn
	msgID uint64
}

func memberRole(g *pb.Group, uid string) pb.Role {
	for _, m := range g.GetMembers() {
		if m.UserId == uid {
			return m.Role
		}
	}
	return pb.Role_ROLE_UNSPECIFIED
}

func guestRole(r pb.Role) bool { return r == pb.Role_ROLE_VIEWER || r == pb.Role_ROLE_DRIVER }

// loadPeople restores known people and clears members' live presence.
// Callers hold d.mu.
func (d *Daemon) loadPeople() {
	recs, _ := d.store.Records("person")
	for id, b := range recs {
		p := &pb.Person{}
		if proto.Unmarshal(b, p) == nil {
			d.people[id] = p
			d.letIn[id] = true
		}
	}
	for _, g := range d.groups {
		for _, m := range g.Members {
			m.Online, m.TabId, m.PaneId, m.Typing = false, "", "", false
		}
	}
}

// peerHello authenticates a guest daemon, and proves to it that this is the
// host its invite named (signing its nonce with this Mac's user key).
// Callers hold d.mu.
func (d *Daemon) peerHello(c *conn, m *pb.PeerHello) (*pb.ServerMessage, error) {
	if len(m.PublicKey) != ed25519.PublicKeySize || len(m.HostNonce) != client.NonceSize ||
		!ed25519.Verify(m.PublicKey, client.AuthMessage(client.PeerAuthTag, d.remote.fingerprint, c.nonce), m.Signature) {
		d.audit("peer", "auth-failed", "", "")
		return nil, errors.New("authentication failed")
	}
	uid := userIDFor(m.PublicKey)
	if uid == d.id.me.UserId {
		return nil, errors.New("that's this Mac")
	}
	c.authed, c.peer, c.kind, c.name = true, uid, "peer", m.Name
	c.fromPane = "" // its Hello may have claimed one of this Mac's panes
	p := &pb.Person{UserId: uid, Name: m.Name, DeviceName: m.DeviceName, Avatar: m.Avatar}
	if len(p.Avatar) > 256<<10 {
		p.Avatar = nil
	}
	if d.unlinksFor(uid) {
		d.deliverUnlinks() // it's back: what's waiting for it goes now
	}
	if len(d.scope(uid)) > 0 || d.isLinked(uid) {
		d.remoteAuthed(c)
		d.knowPerson(p)
		u := &pb.StateUpdate{People: []*pb.Person{proto.CloneOf(p)}}
		d.setOnline(uid, true, u)
		d.publish(u)
		d.audit("peer:"+m.Name, "connected", uid, m.DeviceName)
	} else {
		// Not a member anywhere: remembered (in memory) only once it asks to join.
		if len(d.strangers) >= maxStrangers {
			for k := range d.strangers {
				delete(d.strangers, k)
				break
			}
		}
		d.strangers[uid] = p
		// Anyone can make a key, so a stranger stays under the limits of a
		// connection that hasn't signed in (ADR-0015), and goes once it
		// has nothing under way here: no link waiting on its code, no
		// request to join.
		d.watchStranger(c)
	}
	return &pb.ServerMessage{Msg: &pb.ServerMessage_PeerWelcome{PeerWelcome: &pb.PeerWelcome{
		PublicKey: d.id.key.Public().(ed25519.PublicKey),
		Signature: ed25519.Sign(d.id.key, client.AuthMessage(client.PeerHostTag, d.remote.fingerprint, m.HostNonce)),
	}}}, nil
}

// maxStrangers is how many signed-in strangers' profiles are kept (in memory).
const maxStrangers = 64

// strangerIdle is how long a stranger's connection may stay with nothing
// under way (a variable for tests).
var strangerIdle = 30 * time.Second

// watchStranger drops c (a signed-in stranger) once it has nothing under
// way here, checking every strangerIdle. Callers hold d.mu.
func (d *Daemon) watchStranger(c *conn) {
	if !c.remote {
		return
	}
	var check func()
	check = func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if _, open := d.conns[c]; !open || c.ctx.Err() != nil {
			return
		}
		if d.remote.preAuth[c] == nil {
			return // a member or linked now: its limits lifted
		}
		busy := false
		for _, a := range d.comp.attempts {
			busy = busy || a.c == c
		}
		for _, j := range d.joins {
			busy = busy || j.c == c
		}
		if !busy {
			c.cancel()
			return
		}
		time.AfterFunc(strangerIdle, check)
	}
	time.AfterFunc(strangerIdle, check)
}

// knowPerson records a member's (or soon-to-be member's) profile, lifting
// the limits on its connections here. Callers hold d.mu.
func (d *Daemon) knowPerson(p *pb.Person) {
	for c := range d.conns {
		if c.remote && c.peer == p.UserId && c.authed {
			d.remoteAuthed(c)
		}
	}
	delete(d.strangers, p.UserId)
	d.people[p.UserId] = p
	d.letIn[p.UserId] = true
	d.persist(d.store.PutRecord("person", p.UserId, p))
}

// setOnline marks a person's membership rows online or not. Callers hold d.mu.
func (d *Daemon) setOnline(uid string, on bool, u *pb.StateUpdate) {
	for _, g := range d.groups {
		for _, m := range g.Members {
			if m.UserId == uid && m.Online != on {
				m.Online = on
				if !on {
					m.TabId, m.PaneId, m.Typing = "", "", false
				}
				u.Groups = append(u.Groups, proto.CloneOf(g))
			}
		}
	}
}

func (d *Daemon) peerConnected(uid string) bool {
	for c := range d.conns {
		if c.peer == uid {
			return true
		}
	}
	return false
}

// peerClosed tidies up after a guest's connection ends. Callers hold d.mu.
func (d *Daemon) peerClosed(c *conn) {
	d.linkConnClosed(c)
	u := &pb.StateUpdate{}
	for id, j := range d.joins {
		if j.c == c {
			delete(d.joins, id)
			u.ResolvedJoinIds = append(u.ResolvedJoinIds, id)
		}
	}
	if !d.peerConnected(c.peer) {
		d.setOnline(c.peer, false, u)
		delete(d.strangers, c.peer)
	}
	if len(u.Groups) > 0 || len(u.ResolvedJoinIds) > 0 {
		d.publish(u)
	}
}

// ---------- invites ----------

func inviteKey(groupID string) string { return "invite." + groupID }

// createInvite returns the Group's invite link, making (or with regenerate,
// replacing) its secret. Callers hold d.mu.
func (d *Daemon) createInvite(m *pb.CreateInvite) (*pb.ServerMessage, error) {
	g, ok := d.groups[m.GroupId]
	if !ok {
		return nil, errors.New("no such group")
	}
	if !d.remote.on {
		return nil, errors.New("turn on remote access first, so guests can reach this Mac")
	}
	secret := d.store.Setting(inviteKey(g.Id))
	if secret == "" || m.Regenerate {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		secret = hex.EncodeToString(b)
		if err := d.store.SetSetting(inviteKey(g.Id), secret); err != nil {
			return nil, err
		}
	}
	if !g.InviteActive {
		g.InviteActive = true
		d.persist(d.store.PutGroup(g))
		d.publish(&pb.StateUpdate{Groups: []*pb.Group{proto.CloneOf(g)}})
	}
	raw, _ := hex.DecodeString(secret)
	host := d.macName()
	addrs := d.remote.addrs
	if len(d.cfg.RemoteListen) == 0 {
		// Guests come over Tailscale only (ADR-0015): it must be listened on.
		if !slices.ContainsFunc(d.remote.snap.listening(), func(n hostNet) bool { return n.id == netTailscale }) {
			return nil, errors.New("people you share with reach this Mac over Tailscale: choose Tailscale in Settings ▸ Devices ▸ Remote Access (or hmk remote enable tailscale)")
		}
		addrs = nil
		for _, ip := range tailscaleAddrsCached() {
			addrs = append(addrs, net.JoinHostPort(ip.String(), strconv.Itoa(remotePort())))
		}
	}
	tok, _ := proto.Marshal(&pb.InviteToken{
		HostName: host, Addresses: addrs, CertSha256: d.remote.fingerprint, Secret: raw,
		GroupName: groupLabel(g), HostUserId: d.id.me.UserId,
	})
	return &pb.ServerMessage{Msg: &pb.ServerMessage_Invite{Invite: &pb.Invite{
		Link: "hammock://join/" + base64.RawURLEncoding.EncodeToString(tok),
	}}}, nil
}

// groupLabel is a Group's name for others (its folder when unnamed).
func groupLabel(g *pb.Group) string {
	if g.Name != "" {
		return g.Name
	}
	return "Shared Group"
}

// revokeInvite stops the Group's link working. Callers hold d.mu.
func (d *Daemon) revokeInvite(groupID string) error {
	g, ok := d.groups[groupID]
	if !ok {
		return errors.New("no such group")
	}
	d.persist(d.store.SetSetting(inviteKey(groupID), ""))
	u := &pb.StateUpdate{}
	if g.InviteActive {
		g.InviteActive = false
		d.persist(d.store.PutGroup(g))
		u.Groups = []*pb.Group{proto.CloneOf(g)}
	}
	// Those still waiting on the link are turned away with it.
	for id, j := range d.joins {
		if j.req.GroupId == groupID {
			delete(d.joins, id)
			u.ResolvedJoinIds = append(u.ResolvedJoinIds, id)
			j.c.send(&pb.ServerMessage{ReplyTo: j.msgID, Msg: &pb.ServerMessage_Error{Error: &pb.Error{Message: "this invite link no longer works; ask for a new one"}}})
		}
	}
	if len(u.Groups)+len(u.ResolvedJoinIds) > 0 {
		d.publish(u)
	}
	return nil
}

// peerJoin asks to join the Group whose invite secret this is; the reply
// waits for the host. Callers hold d.mu.
func (d *Daemon) peerJoin(c *conn, msgID uint64, m *pb.PeerJoin) (*pb.ServerMessage, error) {
	want := hex.EncodeToString(m.Secret)
	var g *pb.Group
	for _, cand := range d.groups {
		if s := d.store.Setting(inviteKey(cand.Id)); s != "" && subtle.ConstantTimeCompare([]byte(s), []byte(want)) == 1 {
			g = cand
		}
	}
	if g == nil {
		d.audit("peer:"+c.name, "join-rejected", "", "invalid invite")
		return nil, errors.New("this invite link no longer works; ask for a new one")
	}
	if r := memberRole(g, c.peer); guestRole(r) {
		return joined(g.Id, r), nil
	}
	u := &pb.StateUpdate{}
	for id, j := range d.joins {
		if j.req.GroupId == g.Id && j.req.UserId == c.peer {
			delete(d.joins, id) // asking again replaces the old request
			u.ResolvedJoinIds = append(u.ResolvedJoinIds, id)
		}
	}
	if p := d.strangers[c.peer]; p != nil {
		d.people[c.peer] = p // for the join card; kept once approved
	}
	req := &pb.JoinRequest{Id: newID("jr"), GroupId: g.Id, UserId: c.peer, CreatedUnixMs: time.Now().UnixMilli()}
	req.KeyFingerprint, req.NewKey, req.NameHasOtherKey = d.joinKeyCheck(c.peer, c.name)
	if r := d.comp.refusals[c.peer]; r != nil {
		req.UnlinkedUnixMs = r.UnixMs // a computer you unlinked (lost, stolen?)
	}
	d.joins[req.Id] = &pendingJoin{req: req, c: c, msgID: msgID}
	u.JoinRequests = []*pb.JoinRequest{proto.CloneOf(req)}
	if p := d.people[c.peer]; p != nil {
		u.People = []*pb.Person{proto.CloneOf(p)}
	}
	d.publish(u)
	d.audit("peer:"+c.name, "join-requested", g.Id, "")
	who := c.name
	if p := d.people[c.peer]; p != nil && p.DeviceName != "" {
		who += " (" + p.DeviceName + ")"
	}
	body := who + " is asking to join. Let them view, or open Hammock to let them drive."
	switch {
	case req.UnlinkedUnixMs > 0:
		body = who + " is asking to join. It's a computer you unlinked from your computers, on " + time.UnixMilli(req.UnlinkedUnixMs).Format("2 Jan 2006") + "."
	case req.NameHasOtherKey:
		body = who + " is asking to join, but " + c.name + " usually joins with a different key."
	case req.NewKey:
		body = who + " is asking to join. First time this key has asked to join."
	}
	d.push("join", req.Id, pushNote{Title: c.name + " wants to join", Subtitle: strings.TrimSpace(g.Icon + " " + groupLabel(g)),
		Body: body, Category: "join", Join: req.Id}, func() bool { return d.joins[req.Id] != nil })
	return nil, errDeferred
}

// joinKeyCheck is what the host should know about a joining guest's key:
// its fingerprint, whether it's never been let in, and whether someone let
// in under the same name used another key (someone may be posing as them).
// Callers hold d.mu.
func (d *Daemon) joinKeyCheck(uid, name string) (fingerprint string, newKey, nameHasOtherKey bool) {
	fingerprint = keyFingerprint(uid)
	if d.letIn[uid] && !d.comp.denied[uid] { // (let in while it was linked: not familiar now)
		return fingerprint, false, false
	}
	name = strings.TrimSpace(name)
	for id := range d.letIn {
		if p := d.people[id]; id != uid && p != nil && name != "" && strings.EqualFold(strings.TrimSpace(p.Name), name) {
			nameHasOtherKey = true
		}
	}
	return fingerprint, true, nameHasOtherKey
}

// keyFingerprint is a user key's short fingerprint, the user id's hex in
// fours: "3f2a 91c0 7be4 1d05".
func keyFingerprint(uid string) string {
	hex := strings.TrimPrefix(uid, "us_")
	var parts []string
	for len(hex) > 4 {
		parts = append(parts, hex[:4])
		hex = hex[4:]
	}
	return strings.Join(append(parts, hex), " ")
}

func joined(groupID string, r pb.Role) *pb.ServerMessage {
	return &pb.ServerMessage{Msg: &pb.ServerMessage_PeerJoined{PeerJoined: &pb.PeerJoined{GroupId: groupID, Role: r}}}
}

// respondJoin is the host's answer to a join request. Callers hold d.mu.
func (d *Daemon) respondJoin(m *pb.RespondJoin) error {
	j, ok := d.joins[m.Id]
	if !ok {
		return errors.New("no such join request")
	}
	delete(d.joins, m.Id)
	d.publish(&pb.StateUpdate{ResolvedJoinIds: []string{m.Id}})
	g := d.groups[j.req.GroupId]
	name := d.people[j.req.UserId].GetName()
	if g == nil || !guestRole(m.Role) {
		j.c.send(&pb.ServerMessage{ReplyTo: j.msgID, Msg: &pb.ServerMessage_Error{Error: &pb.Error{Message: d.id.me.Name + " declined"}}})
		d.audit("user", "join-declined", j.req.GroupId, name)
		return nil
	}
	d.setMemberRole(g, j.req.UserId, m.Role)
	d.resendState(j.c) // the Group first, then the answer
	j.c.send(&pb.ServerMessage{ReplyTo: j.msgID, Msg: joined(g.Id, m.Role).Msg})
	d.audit("user", "join-approved", g.Id, name+" as "+roleName(m.Role))
	return nil
}

func roleName(r pb.Role) string {
	switch r {
	case pb.Role_ROLE_VIEWER:
		return "viewer"
	case pb.Role_ROLE_DRIVER:
		return "driver"
	}
	return "host"
}

// setMemberRole adds, changes or (ROLE_UNSPECIFIED) removes someone. A Group
// with guests lists the host too, for presence. Callers hold d.mu.
func (d *Daemon) setMemberRole(g *pb.Group, uid string, r pb.Role) {
	g.Members = slices.DeleteFunc(g.Members, func(m *pb.Member) bool { return m.UserId == uid })
	if guestRole(r) {
		if !slices.ContainsFunc(g.Members, func(m *pb.Member) bool { return m.Role == pb.Role_ROLE_HOST }) {
			g.Members = append([]*pb.Member{{UserId: d.id.me.UserId, Role: pb.Role_ROLE_HOST, Online: true}}, g.Members...)
		}
		g.Members = append(g.Members, &pb.Member{UserId: uid, Role: r, Online: d.peerConnected(uid)})
		if p := cmpPerson(d.strangers[uid], d.people[uid]); p != nil {
			d.knowPerson(p)
		}
	}
	if !slices.ContainsFunc(g.Members, func(m *pb.Member) bool { return guestRole(m.Role) }) {
		g.Members = nil
	}
	d.persist(d.store.PutGroup(g))
	d.publish(&pb.StateUpdate{Groups: []*pb.Group{proto.CloneOf(g)}})
}

// setMember is the host changing or removing someone. Callers hold d.mu.
func (d *Daemon) setMember(m *pb.SetMember) error {
	g, ok := d.groups[m.GroupId]
	if !ok {
		return errors.New("no such group")
	}
	if m.Role == pb.Role_ROLE_HOST || m.UserId == d.id.me.UserId {
		return errors.New("that's you")
	}
	d.setMemberRole(g, m.UserId, m.Role)
	d.audit("user", "member-"+map[bool]string{true: "changed", false: "removed"}[guestRole(m.Role)], g.Id, d.people[m.UserId].GetName())
	for c := range d.conns {
		if c.peer == m.UserId {
			d.resendState(c)
		}
	}
	return nil
}

// stopSharing unshares a Group, or every Group (the kill switch: everyone
// is disconnected and every link stops working). Callers hold d.mu.
func (d *Daemon) stopSharing(groupID string) error {
	if groupID != "" {
		if _, ok := d.groups[groupID]; !ok {
			return errors.New("no such group")
		}
	}
	u := &pb.StateUpdate{}
	for _, g := range d.groups {
		if groupID != "" && g.Id != groupID {
			continue
		}
		d.persist(d.store.SetSetting(inviteKey(g.Id), ""))
		if g.InviteActive || len(g.Members) > 0 {
			g.InviteActive, g.Members = false, nil
			d.persist(d.store.PutGroup(g))
			u.Groups = append(u.Groups, proto.CloneOf(g))
		}
	}
	for id, j := range d.joins {
		if groupID == "" || j.req.GroupId == groupID {
			delete(d.joins, id)
			u.ResolvedJoinIds = append(u.ResolvedJoinIds, id)
			j.c.send(&pb.ServerMessage{ReplyTo: j.msgID, Msg: &pb.ServerMessage_Error{Error: &pb.Error{Message: "sharing stopped"}}})
		}
	}
	d.publish(u)
	for c := range d.conns {
		switch {
		case c.peer == "", d.isLinked(c.peer):
			// Not a guest: your own clients, and your linked computers,
			// which see every Group whether it's shared or not.
		case groupID == "":
			c.cancel()
		default:
			d.resendState(c)
		}
	}
	d.audit("user", "sharing-stopped", groupID, "")
	return nil
}

// ---------- what a peer sees ----------

// scope is the Groups a guest is a member of, with their role. Callers hold d.mu.
func (d *Daemon) scope(uid string) map[string]pb.Role {
	out := map[string]pb.Role{}
	if d.isLinked(uid) {
		// Your other computer: every Group here.
		for id := range d.groups {
			out[id] = pb.Role_ROLE_OWNER
		}
		return out
	}
	for _, g := range d.groups {
		if r := memberRole(g, uid); guestRole(r) {
			out[g.Id] = r
		}
	}
	return out
}

func (d *Daemon) groupOfTab(id string) string {
	if t := d.tabs[id]; t != nil {
		return t.GroupId
	}
	return ""
}

// resendState gives a peer a fresh, full view (after its Groups changed).
// Callers hold d.mu.
func (d *Daemon) resendState(c *conn) {
	if !c.subscribed {
		return
	}
	c.seen = map[string]bool{}
	if u := d.filterFor(c, d.fullState()); u != nil {
		c.send(&pb.ServerMessage{Msg: &pb.ServerMessage_State{State: u}})
	}
	d.revokeStreams(c)
}

// revokeStreams stops a peer's screens and agent views of panes no longer in
// its Groups (it was removed, sharing stopped, or a tab or pane moved out).
// Callers hold d.mu.
func (d *Daemon) revokeStreams(c *conn) {
	if c.peer == "" {
		return
	}
	sc := d.scope(c.peer)
	outside := func(paneID string) bool {
		_, in := sc[d.groupOfPane(paneID)]
		return !in
	}
	for id, p := range d.panes {
		if outside(id) {
			p.detach(c)
		}
	}
	// Video of panes now outside, and menus its input opened (it may have
	// lost Drive too, so it can't answer them). Off the daemon lock: ending a
	// session takes it.
	var gone []*mediaSession
	for _, ms := range d.media.matching(func(ms *mediaSession) bool { return ms.owner == c }) {
		if outside(ms.paneID) {
			gone = append(gone, ms)
		}
	}
	go func() {
		for _, ms := range gone {
			ms.end("you no longer have access to that pane")
		}
		d.browsers.closeMenusFor(c)
	}()
	var ws []*agentWatch
	for _, w := range d.agentWatches {
		if outside(w.p.id) {
			ws = append(ws, w)
		}
	}
	if len(ws) == 0 {
		return
	}
	// Off the daemon lock: a watch's lock comes first.
	go func() {
		for _, w := range ws {
			w.mu.Lock()
			has := w.conns[c]
			w.mu.Unlock()
			if !has {
				continue
			}
			d.mu.Lock()
			still := outside(w.p.id)
			d.mu.Unlock()
			if still {
				w.drop(c)
			}
		}
	}()
}

// filterFor is the part of u a peer may see: its Groups and what's in them.
// Something it saw that's now out of reach arrives as deleted. nil means
// nothing for it. Callers hold d.mu.
func (d *Daemon) filterFor(c *conn, u *pb.StateUpdate) *pb.StateUpdate {
	sc := d.scope(c.peer)
	if c.seen == nil {
		c.seen = map[string]bool{}
	}
	out := &pb.StateUpdate{Version: u.Version, Full: u.Full}
	show := func(id string, in bool) bool {
		if in {
			c.seen[id] = true
			return true
		}
		return false
	}
	gone := func(id string, list *[]string) {
		if c.seen[id] {
			delete(c.seen, id)
			*list = append(*list, id)
		}
	}
	people := map[string]bool{d.id.me.UserId: true}
	for _, g := range u.Groups {
		if _, in := sc[g.Id]; show(g.Id, in) {
			x := proto.CloneOf(g)
			x.InviteActive = false
			out.Groups = append(out.Groups, x)
			for _, m := range g.Members {
				people[m.UserId] = true
			}
		} else {
			gone(g.Id, &out.DeletedGroupIds)
		}
	}
	for _, t := range u.Tabs {
		if _, in := sc[t.GroupId]; show(t.Id, in) {
			out.Tabs = append(out.Tabs, t)
		} else {
			gone(t.Id, &out.DeletedTabIds)
		}
	}
	for _, p := range u.Panes {
		if _, in := sc[d.groupOfTab(p.TabId)]; show(p.Id, in) {
			out.Panes = append(out.Panes, p)
		} else {
			gone(p.Id, &out.DeletedPaneIds)
		}
	}
	for _, id := range u.DeletedGroupIds {
		gone(id, &out.DeletedGroupIds)
	}
	for _, id := range u.DeletedTabIds {
		gone(id, &out.DeletedTabIds)
	}
	for _, id := range u.DeletedPaneIds {
		gone(id, &out.DeletedPaneIds)
	}
	// Agents needing input: for drivers, who can answer.
	for _, a := range u.Attention {
		if r := sc[d.groupOfPane(a.PaneId)]; (r == pb.Role_ROLE_DRIVER || r == pb.Role_ROLE_OWNER) && show(a.Id, true) {
			out.Attention = append(out.Attention, a)
		}
	}
	for _, id := range u.ClearedAttentionIds {
		gone(id, &out.ClearedAttentionIds)
	}
	// Agents asking for permission: your other computers only (you answer
	// from wherever you are), never guests.
	if d.isLinked(c.peer) {
		for _, g := range u.Grants {
			if d.grants[g.Id] != nil && show(g.Id, true) { // this computer's own (not a third's, nor theirs back)
				out.Grants = append(out.Grants, g)
			}
		}
		for _, id := range u.ResolvedGrantIds {
			gone(id, &out.ResolvedGrantIds)
		}
	}
	for _, p := range u.People {
		if people[p.UserId] || len(sc) > 0 && p.UserId == d.id.me.UserId {
			x := proto.CloneOf(p)
			x.Me = false
			out.People = append(out.People, x)
		}
	}
	// Scheduled tasks: your other computers only, this one's own.
	if d.isLinked(c.peer) {
		for _, s := range u.Schedules {
			if d.sched.list[s.Id] != nil && show(s.Id, true) {
				out.Schedules = append(out.Schedules, s)
			}
		}
		for _, id := range u.DeletedScheduleIds {
			gone(id, &out.DeletedScheduleIds)
		}
	}
	if u.Computers != nil && d.isLinked(c.peer) {
		out.Computers = d.computersState(true)
	} else if u.Full {
		out.Computers = d.unlinkNotice(c.peer) // unlinked while it was away
	}
	if u.Full {
		for uid := range people {
			if p := d.personFor(uid); p != nil && !slices.ContainsFunc(out.People, func(q *pb.Person) bool { return q.UserId == uid }) {
				x := proto.CloneOf(p)
				x.Me = false
				out.People = append(out.People, x)
			}
		}
		return out
	}
	if len(out.Groups)+len(out.Tabs)+len(out.Panes)+len(out.DeletedGroupIds)+len(out.DeletedTabIds)+len(out.DeletedPaneIds)+
		len(out.Attention)+len(out.ClearedAttentionIds)+len(out.People)+len(out.Grants)+len(out.ResolvedGrantIds)+
		len(out.Schedules)+len(out.DeletedScheduleIds) == 0 && out.Computers == nil {
		return nil
	}
	return out
}

// cmpPerson is the first non-nil person.
func cmpPerson(ps ...*pb.Person) *pb.Person {
	for _, p := range ps {
		if p != nil {
			return p
		}
	}
	return nil
}

func (d *Daemon) personFor(uid string) *pb.Person {
	if uid == d.id.me.UserId {
		return d.id.me
	}
	return d.people[uid]
}

// peerCheck decides whether a guest may send msg. ignore=true means accept
// and do nothing (a viewer's resize or focus). Callers don't hold d.mu.
func (c *conn) peerCheck(msg *pb.ClientMessage) (ignore bool, err error) {
	d := c.d
	d.mu.Lock()
	defer d.mu.Unlock()
	scrubPeerMsg(msg)
	switch msg.Msg.(type) {
	case *pb.ClientMessage_PeerLinkHello, *pb.ClientMessage_PeerLinkReveal, *pb.ClientMessage_PeerLinkConfirm,
		*pb.ClientMessage_PeerIntroLink, *pb.ClientMessage_IntroduceComputers, *pb.ClientMessage_UnlinkRequest:
		return false, nil // linking, introductions and unlink requests (computers.go and links.go check who's asking)
	}
	if d.isLinked(c.peer) {
		return d.ownerCheck(c, msg)
	}
	sc := d.scope(c.peer)
	errNo := errors.New("not available in a shared Group")
	roleOf := func(groupID string) (pb.Role, error) {
		if r, ok := sc[groupID]; ok {
			return r, nil
		}
		return 0, errors.New("no such group, tab or pane")
	}
	pane := func(id string) (pb.Role, error) { return roleOf(d.groupOfPane(id)) }
	tab := func(id string) (pb.Role, error) { return roleOf(d.groupOfTab(id)) }
	drive := func(r pb.Role, err error) (bool, error) {
		if err != nil {
			return false, err
		}
		if r != pb.Role_ROLE_DRIVER {
			return false, errors.New("you can view this Group, not drive it")
		}
		return false, nil
	}
	view := func(_ pb.Role, err error) (bool, error) { return false, err }
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_Presence:
		// Only panes it can see count (it showing a pane of yours mutes
		// your notifications for it).
		inScope := func(id string) bool { _, in := sc[d.groupOfPane(id)]; return in }
		m.Presence.VisiblePaneIds = filterSlice(m.Presence.VisiblePaneIds, inScope)
		if !inScope(m.Presence.FocusedPaneId) {
			m.Presence.FocusedPaneId = ""
		}
		return false, nil
	case *pb.ClientMessage_Hello, *pb.ClientMessage_Subscribe, *pb.ClientMessage_PeerJoin:
		return false, nil
	case *pb.ClientMessage_AttachPane:
		return view(pane(m.AttachPane.PaneId))
	case *pb.ClientMessage_DetachPane:
		return false, nil
	case *pb.ClientMessage_OpenMedia:
		if m.OpenMedia.Screen {
			return false, errors.New("only your own devices can share this Mac's screen")
		}
		return view(pane(m.OpenMedia.PaneId))
	case *pb.ClientMessage_CloseMedia, *pb.ClientMessage_MediaAck, *pb.ClientMessage_SetMediaView:
		return false, nil // only the session's own connection can use it
	case *pb.ClientMessage_SimulatorCall:
		switch m.SimulatorCall.Op {
		case "screenshot":
			return view(pane(m.SimulatorCall.PaneId))
		case "tap", "swipe", "touch", "text", "key", "copy", "button":
			return drive(pane(m.SimulatorCall.PaneId))
		}
	case *pb.ClientMessage_BrowserInput:
		return drive(pane(m.BrowserInput.PaneId))
	case *pb.ClientMessage_BrowserMenuChoice:
		return drive(pane(m.BrowserMenuChoice.PaneId))
	case *pb.ClientMessage_WatchAgent:
		return view(pane(m.WatchAgent.PaneId))
	case *pb.ClientMessage_ReadAgentFile:
		return view(pane(m.ReadAgentFile.PaneId))
	case *pb.ClientMessage_WatchLog:
		return view(pane(m.WatchLog.PaneId))
	case *pb.ClientMessage_ReadLog:
		return view(pane(m.ReadLog.PaneId))
	case *pb.ClientMessage_ClearLog:
		return drive(pane(m.ClearLog.PaneId))
	case *pb.ClientMessage_ReadEditor:
		return view(pane(m.ReadEditor.PaneId))
	case *pb.ClientMessage_EditorSelect:
		// A viewer's cursor isn't kept: it can't point agents at things.
		r, err := pane(m.EditorSelect.PaneId)
		return err == nil && r != pb.Role_ROLE_DRIVER, err
	case *pb.ClientMessage_EditorPush:
		return drive(pane(m.EditorPush.PaneId))
	case *pb.ClientMessage_EditorCommand:
		// Saving elsewhere than the Group's folder: refused by the save itself.
		return drive(pane(m.EditorCommand.PaneId))
	case *pb.ClientMessage_ListFiles:
		if m.ListFiles.GroupId != "" {
			return drive(roleOf(m.ListFiles.GroupId))
		}
		return drive(pane(m.ListFiles.PaneId))
	case *pb.ClientMessage_StartAgent:
		return drive(roleOf(m.StartAgent.GroupId))
	case *pb.ClientMessage_ReadGroupIcon:
		return view(roleOf(m.ReadGroupIcon.GroupId))
	case *pb.ClientMessage_ListFolders:
		// Drivers can run commands in its folders anyway.
		return drive(roleOf(m.ListFolders.GroupId))
	case *pb.ClientMessage_ReadPane:
		return view(pane(m.ReadPane.PaneId))
	case *pb.ClientMessage_Resize:
		r, err := pane(m.Resize.PaneId)
		return err == nil && r != pb.Role_ROLE_DRIVER, err
	case *pb.ClientMessage_Focus:
		r, err := pane(m.Focus.PaneId)
		return err == nil && r != pb.Role_ROLE_DRIVER, err
	case *pb.ClientMessage_Input:
		return drive(pane(m.Input.PaneId))
	case *pb.ClientMessage_SignalPane:
		return drive(pane(m.SignalPane.PaneId))
	case *pb.ClientMessage_AgentAnswer:
		if m.AgentAnswer.Decision == pb.AgentAnswer_DECISION_ALLOW_SCHEDULE {
			return false, errors.New("only the host can change a scheduled task")
		}
		return drive(pane(m.AgentAnswer.PaneId))
	case *pb.ClientMessage_AgentSend:
		return drive(pane(m.AgentSend.PaneId))
	case *pb.ClientMessage_BrowserNavigate:
		return drive(pane(m.BrowserNavigate.PaneId))
	case *pb.ClientMessage_UploadFile:
		return drive(pane(m.UploadFile.PaneId))
	case *pb.ClientMessage_SwitchAgent:
		return drive(pane(m.SwitchAgent.PaneId))
	case *pb.ClientMessage_ClosePane:
		return drive(pane(m.ClosePane.PaneId))
	case *pb.ClientMessage_CreatePane:
		if err := d.guestEditorPath(m.CreatePane.Spec, d.groupOfPane(m.CreatePane.SplitPaneId)); err != nil {
			return false, err
		}
		return drive(pane(m.CreatePane.SplitPaneId))
	case *pb.ClientMessage_CreateTab:
		if err := d.guestEditorPath(m.CreateTab.Pane, m.CreateTab.GroupId); err != nil {
			return false, err
		}
		return drive(roleOf(m.CreateTab.GroupId))
	case *pb.ClientMessage_CloseTab:
		return drive(tab(m.CloseTab.TabId))
	case *pb.ClientMessage_RenameTab:
		return drive(tab(m.RenameTab.TabId))
	case *pb.ClientMessage_SetLayout:
		return drive(tab(m.SetLayout.TabId))
	case *pb.ClientMessage_FloatPane:
		// As the layout: Drive members arrange the tab, View members watch.
		if m.FloatPane.PaneId == "" {
			return drive(tab(m.FloatPane.TabId))
		}
		return drive(pane(m.FloatPane.PaneId))
	case *pb.ClientMessage_MoveTab:
		if g := d.groupOfTab(m.MoveTab.TabId); g != m.MoveTab.GroupId {
			return false, errNo
		}
		return drive(tab(m.MoveTab.TabId))
	case *pb.ClientMessage_MergeTab:
		// Within the Group only.
		if d.groupOfTab(m.MergeTab.TabId) != d.groupOfPane(m.MergeTab.IntoPaneId) {
			return false, errNo
		}
		return drive(tab(m.MergeTab.TabId))
	case *pb.ClientMessage_BreakOutPane:
		return drive(pane(m.BreakOutPane.PaneId))
	case *pb.ClientMessage_ReplacePane:
		if err := d.guestEditorPath(m.ReplacePane.Spec, d.groupOfPane(m.ReplacePane.PaneId)); err != nil {
			return false, err
		}
		return drive(pane(m.ReplacePane.PaneId))
	case *pb.ClientMessage_ClearAttention:
		if a := d.attention[m.ClearAttention.Id]; a != nil {
			return drive(pane(a.PaneId))
		}
		return false, nil
	case *pb.ClientMessage_SetMember:
		// A guest may only leave.
		if m.SetMember.UserId == c.peer && m.SetMember.Role == pb.Role_ROLE_UNSPECIFIED {
			return view(roleOf(m.SetMember.GroupId))
		}
	}
	return false, errNo
}

// scrubPeerMsg clears what a guest mustn't claim: which of this Mac's panes
// asked for something (an agent's pane, here, means something else).
func scrubPeerMsg(msg *pb.ClientMessage) {
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_Hello:
		m.Hello.FromPaneId = ""
	case *pb.ClientMessage_CreateTab:
		if m.CreateTab.Pane != nil {
			m.CreateTab.Pane.CreatedByPaneId = ""
		}
	case *pb.ClientMessage_CreatePane:
		if m.CreatePane.Spec != nil {
			m.CreatePane.Spec.CreatedByPaneId = ""
		}
	case *pb.ClientMessage_ReplacePane:
		if m.ReplacePane.Spec != nil {
			m.ReplacePane.Spec.CreatedByPaneId = ""
		}
	case *pb.ClientMessage_RequestGrant:
		m.RequestGrant.RequesterPaneId = ""
	}
}

// ---------- presence ----------

// peerPresence records where a guest is. Callers hold d.mu.
func (d *Daemon) peerPresence(uid, paneID string) {
	u := &pb.StateUpdate{}
	tabID, groupID := "", d.groupOfPane(paneID)
	if groupID != "" {
		tabID = d.panes[paneID].tabID()
	}
	for _, g := range d.groups {
		for _, m := range g.Members {
			if m.UserId != uid {
				continue
			}
			want, wantTab := "", ""
			if g.Id == groupID {
				want, wantTab = paneID, tabID
			}
			if m.PaneId != want || m.TabId != wantTab {
				m.PaneId, m.TabId = want, wantTab
				u.Groups = append(u.Groups, proto.CloneOf(g))
			}
		}
	}
	if len(u.Groups) > 0 {
		d.publish(u)
	}
}

// typed shows someone's typing glow in a shared Group for a moment.
// Callers hold d.mu.
func (d *Daemon) typed(uid, paneID string) {
	g := d.groups[d.groupOfPane(paneID)]
	if g == nil || len(g.Members) == 0 {
		return
	}
	set := func(on bool) {
		for _, m := range g.Members {
			if m.UserId == uid && m.Typing != on {
				m.Typing = on
				if on {
					m.PaneId, m.TabId = paneID, d.panes[paneID].tabID()
				}
				d.publish(&pb.StateUpdate{Groups: []*pb.Group{proto.CloneOf(g)}})
			}
		}
	}
	set(true)
	if t := d.typing[uid]; t != nil {
		t.Stop()
	}
	d.typing[uid] = time.AfterFunc(typingFor, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.groups[g.Id] == g {
			set(false)
		}
	})
}
