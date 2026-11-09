package daemon

import (
	"bytes"
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Linked computers (ADR-0013): your own computers, each showing every
// other's Groups. Each keeps its own user key. A linked computer is a peer
// (peer.go) whose scope is every Group here, as an owner (ROLE_OWNER: a
// driver that may also rename, close and make Groups, and use the session
// finder and folders); this computer keeps a link to each of them the same
// way, so their Groups show here among yours.
//
// Membership is direct links only: each computer keeps its own list of the
// computers it's linked with, made by a person (the code check) or by an
// introduction from one it was linked with then (links.go), and its own
// list of keys it refuses. Nothing is relayed or gossiped.

// linkPalette is the badge colours given to computers as they're linked.
var linkPalette = []string{"#2563EB", "#7C3AED", "#15803D", "#C2410C", "#DB2777", "#0891B2", "#A16207", "#4F46E5"}

var tintRE = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// neutralTint is a computer's colour when it's picked to have none (drawn
// in the text's colour): several may have it. ("" is one not picked yet:
// it's given one of linkPalette's.)
const neutralTint = "none"

// validTint reports whether t is a colour a computer may have.
func validTint(t string) bool { return t == neutralTint || tintRE.MatchString(t) }

// symbolRE is an SF Symbol's name, as a computer's icon.
var symbolRE = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9]+)*$`)

// validIcon reports whether s will do as a computer's icon: "" (its kind's
// symbol), an SF Symbol's name, or an emoji (a short run of non-ASCII
// characters, as a keycap's digit with its marks).
func validIcon(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 64 || !utf8.ValidString(s) {
		return false
	}
	ascii := true
	for _, r := range s {
		if r >= 0x80 {
			ascii = false
		} else if !(r >= '0' && r <= '9' || r == '#' || r == '*' || r >= 'a' && r <= 'z' || r == '.') {
			return false
		}
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return !ascii || symbolRE.MatchString(s)
}

// linkTimeout is how long a link waits for its code to be checked.
const linkTimeout = 10 * time.Minute

// linkGrace is how long a link just made is given to be made on the other
// side too: a "doesn't link with you" from it before then is the other side
// catching up, not a refusal. (A variable for tests.)
var linkGrace = 30 * time.Second

type computerSet struct {
	self         *pb.Computer              // this one
	links        map[string]*pb.StoredLink // the computers linked with this one, by user id
	linkedAt     map[string]time.Time      // when each link was made, on this run (linkGrace)
	denied       map[string]bool           // keys unlinked here (or asked to be): refused for good
	refusals     map[string]*pb.Refusal    // each one's refusal: who asked, and when
	suspects     map[string]*pb.Refusal    // keys held for their code (introduced by a computer since unlinked): never linked by introduction
	pending      map[string]*pb.PendingUnlink
	intros       map[string]*intro // introductions offered here, by the user id introduced
	introducedAt time.Time         // when the linked computers were last introduced to each other
	attempts     map[string]*linkAttempt
	askedAt      map[string]time.Time   // when each key and address last asked to link here
	expectUntil  time.Time              // a person here is linking (Link a Computer open, hmk link --accept): off Tailscale, computers may ask until then
	asks         map[string][]time.Time // requests (unlinks, introductions) from each linked computer, lately
	note         string                 // to tell you (after an upgrade: computers to link again)
	delivering   bool                   // unlink requests are being sent (links.go)
	kick         chan struct{}          // try sending them again now
	appOpen      bool                   // as last told to the others (Computer.notifies)
	// the lists last published (to clients, to linked computers)
	sentLocal, sentPeers *pb.Computers
	sentInput            int64     // this one's input time as last sent to them
	inputAt              time.Time // when this computer's last input was last sent to the others
}

// linkAttempt is a link being made: asked from here (outgoing) or by the
// other computer (incoming).
type linkAttempt struct {
	req *pb.LinkRequest
	// outgoing: the person's answer
	answer chan bool
	// incoming: the asking computer's connection and what it sent
	c         *conn
	peer      string
	commit    []byte
	nonce     []byte
	askNonce  []byte // its nonce, revealed (with this one's: the link's id)
	their     *pb.Computer
	revealID  uint64 // its PeerLinkReveal, answered when the person here does
	confirmID uint64 // its PeerLinkConfirm (accepted there), answered once linked
	confirmed bool
	// answered: its reveal, its confirm (else ending the attempt answers them)
	revealDone, confirmDone bool
	expires                 *time.Timer
}

// ---------- this computer ----------

// loadComputers restores linked computers and starts linking to them,
// migrating what earlier builds kept first. Callers hold d.mu.
func (d *Daemon) loadComputers() {
	d.comp.links = map[string]*pb.StoredLink{}
	d.comp.linkedAt = map[string]time.Time{}
	d.comp.denied = map[string]bool{}
	d.comp.refusals = map[string]*pb.Refusal{}
	d.comp.suspects = map[string]*pb.Refusal{}
	d.comp.pending = map[string]*pb.PendingUnlink{}
	d.comp.intros = map[string]*intro{}
	d.comp.attempts = map[string]*linkAttempt{}
	d.comp.askedAt = map[string]time.Time{}
	d.comp.asks = map[string][]time.Time{}
	d.comp.kick = make(chan struct{}, 1)
	self := &pb.Computer{}
	if recs, _ := d.store.Records("computerself"); recs["self"] != nil {
		_ = proto.Unmarshal(recs["self"], self)
	}
	d.comp.self = self
	d.migrateComputers()
	d.comp.note = d.store.Setting("computers.note")
	recs, _ := d.store.Records("complink")
	for uid, b := range recs {
		l := &pb.StoredLink{}
		if proto.Unmarshal(b, l) == nil && uid != d.id.me.UserId && l.Record.GetUserId() == uid {
			d.comp.links[uid] = l
		}
	}
	denied, _ := d.store.Records("compdeny")
	for uid, b := range denied {
		r := &pb.Refusal{}
		_ = proto.Unmarshal(b, r) // (earlier builds kept a Computer: its id and name read the same)
		r.UserId = uid
		d.comp.denied[uid], d.comp.refusals[uid] = true, r
	}
	suspects, _ := d.store.Records("compsuspect")
	for uid, b := range suspects {
		r := &pb.Refusal{}
		if proto.Unmarshal(b, r) == nil {
			r.UserId = uid
			d.comp.suspects[uid] = r
		}
	}
	for uid, l := range d.comp.links {
		if l.Suspect && d.comp.suspects[uid] == nil {
			d.recordSuspect(uid, "")
		}
	}
	pending, _ := d.store.Records("compunlinkreq")
	for k, b := range pending {
		p := &pb.PendingUnlink{}
		if proto.Unmarshal(b, p) == nil {
			d.comp.pending[k] = p
		}
	}
	caches, _ := d.store.Records("peercache")
	for uid := range d.comp.links {
		if !d.isLinked(uid) {
			continue
		}
		if l := d.ensureLink(uid); l != nil && len(l.groups) == 0 {
			if c, ok := caches[linkKey(l.tok)]; ok {
				u := &pb.StateUpdate{}
				if proto.Unmarshal(c, u) == nil {
					l.applyLocked(u, false)
				}
			}
		}
	}
}

// refreshSelf brings this computer's own record up to date (its name,
// model, addresses, certificate), telling the others if it changed.
// Callers hold d.mu.
func (d *Daemon) refreshSelf(publish bool) {
	s := d.comp.self
	hw := knownHardware(s)
	addrs, lan := d.reachAt()
	want := &pb.Computer{
		UserId: d.id.me.UserId, Name: d.macName(), Model: hw.model, Kind: hw.kind, Os: hw.os,
		Fingerprint: keyFingerprint(d.id.me.UserId), Addresses: addrs, CertSha256: d.remote.fingerprint,
		Key: d.id.key.Public().(ed25519.PublicKey), LanSubnets: lan,
	}
	if want.CertSha256 == "" {
		want.CertSha256 = s.CertSha256
	}
	if len(want.Addresses) == 0 {
		want.Addresses, want.LanSubnets = s.Addresses, s.LanSubnets
	}
	if s.UserId == want.UserId && s.Name == want.Name && s.Model == want.Model && s.Kind == want.Kind && s.Os == want.Os &&
		slices.Equal(s.Addresses, want.Addresses) && slices.Equal(s.LanSubnets, want.LanSubnets) && s.CertSha256 == want.CertSha256 && bytes.Equal(s.Key, want.Key) {
		return
	}
	s.UserId, s.Name, s.Model, s.Kind, s.Os, s.Fingerprint, s.Key = want.UserId, want.Name, want.Model, want.Kind, want.Os, want.Fingerprint, want.Key
	s.Addresses, s.LanSubnets, s.CertSha256 = want.Addresses, want.LanSubnets, want.CertSha256
	d.saveSelf()
	if publish {
		d.publishComputers()
	}
}

// saveSelf keeps this computer's own record. Callers hold d.mu.
func (d *Daemon) saveSelf() {
	d.persist(d.store.PutRecord("computerself", "self", d.comp.self))
}

// linkAddrs are the addresses linked computers reach this one at (reachAt,
// netsel.go). Never asks Tailscale, so it's fine under d.mu. Callers hold d.mu.
func (d *Daemon) linkAddrs() []string {
	a, _ := d.reachAt()
	return a
}

// refreshLinked is refreshSelf while any computers are linked (with none,
// there's nobody to tell; linking refreshes first). Callers hold d.mu.
func (d *Daemon) refreshLinked() {
	if d.linkedCount() > 0 {
		d.refreshSelf(true)
	}
}

// isLinkedLocked is isLinked, taking d.mu.
func (d *Daemon) isLinkedLocked(uid string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isLinked(uid)
}

// isLinked: uid is a computer linked with this one (and it with this one,
// as far as this one knows). Callers hold d.mu.
func (d *Daemon) isLinked(uid string) bool {
	l := d.comp.links[uid]
	return l != nil && !l.Refused && !l.Suspect
}

// linkedCount is how many computers are linked with this one. Callers hold d.mu.
func (d *Daemon) linkedCount() int {
	n := 0
	for uid := range d.comp.links {
		if d.isLinked(uid) {
			n++
		}
	}
	return n
}

// linkOf is the link to a linked computer. Callers hold d.mu.
func (d *Daemon) linkOf(uid string) *peerLink {
	for _, l := range d.links {
		if l.linked && l.tok.HostUserId == uid {
			return l
		}
	}
	return nil
}

// onLinked: id (a Group, tab, pane) is on a linked computer. Callers hold d.mu.
func (d *Daemon) onLinked(id string) bool {
	l := d.routes[id]
	return l != nil && l.linked
}

// ---------- what clients and the others are told ----------

// selfRecord is this computer's own record as sent: to a linked computer,
// with how long it's been idle (not a time: clocks differ). Callers hold d.mu.
func (d *Daemon) selfRecord(forPeer bool) *pb.Computer {
	self := proto.CloneOf(d.comp.self)
	self.ThisComputer, self.Online = !forPeer, true
	if self.UserId == "" {
		// Never linked (refreshSelf hasn't run): what's known without asking.
		hw := knownHardware(self)
		self.UserId, self.Fingerprint = d.id.me.UserId, keyFingerprint(d.id.me.UserId)
		self.Name, self.Model, self.Kind, self.Os = cmpOr(d.id.me.DeviceName, "This computer"), hw.model, hw.kind, hw.os
	}
	self.Notifies = d.appIsOpen()
	if forPeer {
		self.LastInputUnixMs, self.IdleMs = 0, 0
		if idle := time.Now().UnixMilli() - d.comp.self.LastInputUnixMs; self.Notifies && d.comp.self.LastInputUnixMs > 0 && idle >= 0 {
			self.IdleMs = idle + 1 // (0 is "not known")
		}
	}
	return self
}

// computersState is the list of computers: this one first, the others by
// name. A linked computer is sent only this one's own record. Callers hold d.mu.
func (d *Daemon) computersState(forPeer bool) *pb.Computers {
	out := &pb.Computers{}
	self := d.selfRecord(forPeer)
	out.List = append(out.List, self)
	if forPeer {
		return out
	}
	out.Note = d.comp.note
	var others []*pb.Computer
	for uid, l := range d.comp.links {
		x := proto.CloneOf(l.Record)
		x.ThisComputer, x.Refused, x.Suspect, x.CertChanged, x.Online = false, l.Refused, l.Suspect, l.CertChanged, false
		if pl := d.linkOf(uid); pl != nil && pl.online && d.isLinked(uid) {
			x.Online = true
		}
		others = append(others, x)
	}
	slices.SortFunc(others, func(a, b *pb.Computer) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.UserId, b.UserId))
	})
	out.List = append(out.List, others...)
	// The one used last notifies: of those whose app is open (a Mac running
	// only hmkd, or a Linux box, can't show them), this one unless another,
	// reachable, was used more recently. None: nobody's app is open, so the
	// first to open it does.
	best := int64(-1)
	if self.Notifies {
		out.ActiveId, best = d.id.me.UserId, self.LastInputUnixMs
	}
	for _, r := range others {
		if r.Online && r.Notifies && r.LastInputUnixMs > best {
			out.ActiveId, best = r.UserId, r.LastInputUnixMs
		}
	}
	return out
}

// appIsOpen: the Mac app is connected here (local, not a guest, not a
// process in one of the panes), so this computer can show notifications.
// Callers hold d.mu.
func (d *Daemon) appIsOpen() bool {
	for c := range d.conns {
		if c.kind == "mac" && !c.remote && c.peer == "" && c.caller == nil && c.subscribed {
			return true
		}
	}
	return false
}

// appChanged tells the others when the app opens or closes here (which
// computer can notify). Callers hold d.mu.
func (d *Daemon) appChanged() {
	if open := d.appIsOpen(); open != d.comp.appOpen {
		d.comp.appOpen = open
		if d.linkedCount() > 0 {
			d.comp.inputAt = time.Now() // its input time goes with it
			d.publishComputers()
		}
	}
}

// publishComputers tells clients (and linked computers) the list changed,
// unless what they'd be told is what they were told last. Callers hold d.mu.
func (d *Daemon) publishComputers() {
	local, peers := d.computersState(false), d.computersState(true)
	peers.List[0].IdleMs = 0 // (changes every call: not a change to tell)
	if proto.Equal(local, d.comp.sentLocal) && proto.Equal(peers, d.comp.sentPeers) {
		return
	}
	d.comp.sentLocal, d.comp.sentPeers, d.comp.sentInput = local, peers, d.comp.self.LastInputUnixMs
	d.publish(&pb.StateUpdate{Computers: local})
}

// inUseElsewhere: another linked computer, reachable, with its app open,
// was used within awayAfter (you're there, and it notifies). Callers hold d.mu.
func (d *Daemon) inUseElsewhere() bool {
	cutoff := time.Now().Add(-awayAfter).UnixMilli()
	for uid, l := range d.comp.links {
		r := l.Record
		if pl := d.linkOf(uid); pl != nil && pl.online && d.isLinked(uid) && r.Notifies && r.LastInputUnixMs > cutoff {
			return true
		}
	}
	return false
}

// watchInput keeps this computer's last-used time current for the others
// (which one notifies), while any are linked.
func (d *Daemon) watchInput() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		}
		d.mu.Lock()
		linked := d.linkedCount() > 0
		d.appChanged()
		d.mu.Unlock()
		if !linked {
			continue
		}
		d.noteInput(lastInputAt())
	}
}

// lastInputAt is when a person last used this computer: the keyboard or
// mouse on a Mac, unless its screen is locked or another user's session is
// in front (zero then). Elsewhere nothing says, and those don't notify.
var lastInputAt = func() time.Time {
	if hardware().os != "macos" || screenLocked() {
		return time.Time{}
	}
	return time.Now().Add(-idleTime())
}

// noteInput records this computer's last use, telling the others when it
// matters: it's now the one used last, or a while has passed. Only what a
// person did at this computer counts (lastInputAt): not agents typing
// through MCP or hmk send, nor a TUI over SSH from another computer. A
// time ahead of now (a clock that jumped) is ignored.
func (d *Daemon) noteInput(at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if at.IsZero() || at.After(time.Now()) {
		return
	}
	ms := at.UnixMilli()
	s := d.comp.self
	if ms <= s.LastInputUnixMs+10_000 {
		return
	}
	s.LastInputUnixMs = ms
	if !d.appIsOpen() {
		return // it can't notify, so nobody uses this (sent when the app opens)
	}
	// Tell them when it takes over in their eyes (what they were last told
	// was behind the others that can notify, and now isn't), else now and
	// then: not every tick while someone types. (Their times are on this
	// computer's clock: see mergeComputers.)
	others := int64(0)
	for uid, l := range d.comp.links {
		if pl := d.linkOf(uid); pl != nil && pl.online && d.isLinked(uid) && l.Record.Notifies {
			others = max(others, l.Record.LastInputUnixMs)
		}
	}
	takesOver := d.comp.sentInput <= others && ms > others
	if takesOver || time.Since(d.comp.inputAt) > time.Minute {
		d.comp.inputAt = time.Now()
		d.publishComputers()
	}
}

// ---------- the links ----------

// mergeComputers takes in what linked computer from sent of itself, over
// its own link: its record (the latest wins), whether its app is open and
// how long it's been idle (a time on this computer's clock); or that it
// doesn't link with this one any more. Records of other computers aren't
// taken from anyone. Callers hold d.mu.
func (d *Daemon) mergeComputers(cs *pb.Computers, from string) {
	l := d.comp.links[from]
	if l == nil || l.Refused || l.Suspect {
		return
	}
	if cs.NotLinked {
		// (When it was linked, on this run: after a restart its grace is
		// over, whatever the clock says.)
		if made := d.comp.linkedAt[from]; !made.IsZero() && time.Since(made) < linkGrace {
			// Just linked: it's still catching up. Not linked and online
			// meanwhile: this sign-in fails, and the link tries again.
			if pl := d.linkOf(from); pl != nil {
				pl.online, pl.notYet = false, true
			}
			return
		}
		d.refuseLink(from)
		return
	}
	var r *pb.Computer
	for _, x := range cs.GetList() {
		if x.GetUserId() == from {
			r = x
		}
	}
	if r == nil {
		return
	}
	n := proto.CloneOf(r)
	n.ThisComputer, n.Online, n.Refused, n.IdleMs = false, false, false, 0
	n.LinkedUnixMs, n.LastSeenUnixMs = l.Record.LinkedUnixMs, l.Record.LastSeenUnixMs
	if n.Key = l.Record.Key; len(n.Key) == 0 && userIDFor(r.Key) == from {
		n.Key = r.Key // (its own, signed in with: an upgraded link learns it so)
	}
	n.LastInputUnixMs = l.Record.LastInputUnixMs
	if r.IdleMs > 0 {
		n.LastInputUnixMs = time.Now().UnixMilli() - (r.IdleMs - 1)
	}
	if !validTint(n.Tint) {
		n.Tint = ""
	}
	if !validIcon(n.Icon) {
		n.Icon = ""
	}
	n.CertSha256 = l.CertSha256 // pinned: a changed one means linking again
	if proto.Equal(n, l.Record) {
		return
	}
	l.Record = n
	d.persist(d.store.PutRecord("complink", from, l))
	d.ensureLink(from) // (its addresses may be new)
	if d.linkedCount() > 0 {
		d.ensureOwnTint()
	}
	d.publishComputers()
}

// cmpBytes is a, else b.
func cmpBytes(a, b []byte) []byte {
	if len(a) > 0 {
		return a
	}
	return b
}

// refuseLink marks uid as not linking with this one any more (it said so):
// shown, with Link Again and Remove, its link let go. Callers hold d.mu.
func (d *Daemon) refuseLink(uid string) {
	l := d.comp.links[uid]
	if l == nil || l.Refused {
		return
	}
	l.Refused = true
	d.persist(d.store.PutRecord("complink", uid, l))
	d.dropLink(uid, nil)
	d.audit("computer", "refused", uid, l.Record.GetName()+" doesn't link with this computer any more")
	d.publishComputers()
}

// addLink records a direct link with r's computer (key and pinned
// certificate as checked), made by a person (via "") or introduced by via,
// with the id both ends know it by. A person's link undoes any unlink of
// it here. The new link is told of the computers this one unlinked (that
// it had links with), and introduced to the others. Callers hold d.mu.
func (d *Daemon) addLink(r *pb.Computer, key []byte, cert, via, reachedAt, linkID string, chain []*pb.IntroHop) {
	uid := r.UserId
	n := proto.CloneOf(r)
	n.ThisComputer, n.Online, n.Refused, n.IdleMs, n.LastInputUnixMs = false, false, false, 0, 0
	n.Key, n.CertSha256, n.Fingerprint = key, cert, keyFingerprint(uid)
	n.LinkedUnixMs = time.Now().UnixMilli()
	if !validTint(n.Tint) {
		n.Tint = "" // it picks its own
	}
	if !validIcon(n.Icon) {
		n.Icon = ""
	}
	if via == "" {
		d.unrefuse(uid) // a person linked it again, here (and checked its code)
		if d.comp.suspects[uid] != nil {
			delete(d.comp.suspects, uid)
			d.persist(d.store.DeleteRecord("compsuspect", uid))
		}
	}
	d.comp.links[uid] = &pb.StoredLink{Record: n, CertSha256: cert, Via: via, ReachedAt: reachedAt, LinkId: linkID, Chain: chain}
	d.comp.linkedAt[uid] = time.Now()
	delete(d.comp.intros, uid)
	// Linked again: a "drop me" still on its way to it (this one left
	// earlier) is moot, and mustn't undo this.
	if k := uid + "|" + d.id.me.UserId; d.comp.pending[k] != nil {
		d.dropPending(k)
	}
	d.persist(d.store.PutRecord("complink", uid, d.comp.links[uid]))
	// Every computer that had a link here and was unlinked: it's refused
	// there too (it may have been introduced there by one that hadn't heard).
	for x, ref := range d.comp.refusals {
		if ref.Link != nil && x != uid {
			d.queueUnlink(x, uid, cmpOr(ref.Origin, d.id.me.UserId), ref.AskedUnixMs, "")
		}
	}
	d.deliverUnlinks()
	d.ensureLink(uid)
	d.resendPeer(uid)
	d.ensureOwnTint()
	d.introduceAll() // to the others: a link with it (links.go)
	how := "by you"
	if via != "" {
		how = "introduced by " + cmpOr(d.comp.links[via].GetRecord().GetName(), via)
	}
	d.audit("computer", "linked", uid, n.Name+" ("+how+")")
}

// dropComputer lets go of a link here (no request to anyone), closing its
// connections but keep's. What was waiting to be sent to it goes too: all
// of it if it's refused, else all but this one's own "drop me" (leaving).
// Callers hold d.mu.
func (d *Daemon) dropComputer(uid string, refused bool, keep *conn) {
	delete(d.comp.links, uid)
	delete(d.comp.linkedAt, uid)
	delete(d.comp.intros, uid)
	d.persist(d.store.DeleteRecord("complink", uid))
	for k, p := range d.comp.pending {
		if p.To == uid && (refused || p.UserId != d.id.me.UserId) {
			d.dropPending(k)
		}
	}
	d.dropLink(uid, keep)
	d.dropClosedOf(uid)
}

// freeTint is a badge colour none of the other computers has. Callers hold d.mu.
func (d *Daemon) freeTint() string {
	used := map[string]bool{}
	for uid, l := range d.comp.links {
		if d.isLinked(uid) {
			used[strings.ToUpper(l.Record.Tint)] = true
		}
	}
	for _, t := range linkPalette {
		if !used[t] {
			return t
		}
	}
	return linkPalette[len(d.comp.links)%len(linkPalette)]
}

// ensureLink keeps a link to linked computer uid (made, or an existing one
// marked linked), dialling where it was reached first, then where it says
// it listens. Callers hold d.mu.
func (d *Daemon) ensureLink(uid string) *peerLink {
	sl := d.comp.links[uid]
	if sl == nil || sl.Refused || sl.Suspect {
		return nil
	}
	r := sl.Record
	addrs := slices.Clone(r.Addresses)
	if at := sl.ReachedAt; at != "" && !slices.Contains(addrs, at) {
		addrs = append([]string{at}, addrs...)
	}
	if sl.CertSha256 == "" || len(addrs) == 0 {
		return nil
	}
	// Its pinned certificate changed (linked again): the old link goes.
	for key, l := range d.links {
		if l.tok.HostUserId == uid && key != sl.CertSha256 && l.linked {
			d.closeLink(key, l)
		}
	}
	l := d.links[sl.CertSha256]
	if l == nil {
		l = newLink(d, &pb.InviteToken{HostName: r.Name, Addresses: addrs, CertSha256: sl.CertSha256, HostUserId: uid, LanSubnets: r.LanSubnets})
		l.linked = true
		d.links[sl.CertSha256] = l
		go l.run()
		return l
	}
	l.linked = true
	l.tok.HostName, l.tok.Addresses, l.tok.LanSubnets = r.Name, addrs, r.LanSubnets
	return l
}

// dropLink stops showing a computer's Groups and lets go of its
// connections here (but keep, still to be answered). Callers hold d.mu.
func (d *Daemon) dropLink(uid string, keep *conn) {
	for key, l := range d.links {
		if l.tok.HostUserId == uid && l.linked {
			d.closeLink(key, l)
		}
	}
	for c := range d.conns {
		if c.peer == uid && c != keep {
			c.cancel()
		}
	}
}

// closeLink forgets a linked computer's link and what it showed. Callers hold d.mu.
func (d *Daemon) closeLink(key string, l *peerLink) {
	l.linked = false
	l.applyLocked(&pb.StateUpdate{Full: true}, true)
	l.online, l.gone = false, true
	delete(d.links, key)
	d.persist(d.store.DeleteRecord("peerlink", key))
	d.persist(d.store.DeleteRecord("peercache", key))
	go l.close()
}

// unlinkComputer is a person here unlinking uid: from all their
// computers (this one refuses it for good, and asks each computer it's
// linked with to do the same: unlinkHere), or only forgetting it here
// (forget: Remove, for one that doesn't link with this one any more); or,
// with this one's own id, this computer leaving: its links are dropped
// here and each asked to drop it (without refusing it). Requests to
// computers that are off are kept until they're delivered (links.go).
// Callers hold d.mu.
func (d *Daemon) unlinkComputer(uid string, forget bool) error {
	me := d.id.me.UserId
	switch {
	case uid == me:
		if len(d.comp.links) == 0 {
			return errors.New("this computer isn't linked")
		}
		for other, l := range d.comp.links {
			d.queueUnlink(me, other, me, 0, l.LinkId)
		}
		for other := range d.comp.links {
			d.dropComputer(other, false, nil)
		}
		d.audit("user", "computer-unlinked", uid, "this computer, from the others")
	case d.comp.links[uid] == nil:
		return errors.New("no such linked computer")
	case forget:
		name := d.comp.links[uid].Record.GetName()
		d.dropComputer(uid, false, nil)
		d.audit("user", "computer-removed", uid, name+", here only")
	default:
		name := d.comp.links[uid].Record.GetName()
		d.unlinkHere(uid, "", me, time.Now().UnixMilli(), true)
		d.audit("user", "computer-unlinked", uid, name)
		// Those it had unlinked here aren't told anything (nothing goes to
		// a refused computer): if it was the stolen one, you link them again.
		var victims []string
		for _, r := range d.comp.refusals {
			if r.By == uid && r.Link != nil {
				victims = append(victims, d.nameOf(r.UserId))
			}
		}
		if len(victims) > 0 {
			slices.Sort(victims)
			d.addNote(name + " had this computer unlink " + strings.Join(victims, ", ") + ": if " + name + " was lost or stolen, link them again.")
		}
	}
	d.publishComputers()
	d.deliverUnlinks()
	return nil
}

// resendPeer gives uid's connections here a fresh view: one that
// subscribed while it was still a stranger (linked since) sees every Group
// now. Callers hold d.mu.
func (d *Daemon) resendPeer(uid string) {
	for c := range d.conns {
		if c.peer == uid {
			d.resendState(c)
		}
	}
}

// certChanged notes whether l's linked computer answers with another
// certificate than the one pinned (shown, with Link Again: the code check
// confirms a new one). Only noted, never trusted.
func (d *Daemon) certChanged(l *peerLink, changed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.certChangedLocked(l, changed)
}

func (d *Daemon) certChangedLocked(l *peerLink, changed bool) {
	sl := d.comp.links[l.tok.HostUserId]
	if !l.linked || sl == nil || sl.CertSha256 != l.tok.CertSha256 || sl.CertChanged == changed {
		return
	}
	sl.CertChanged = changed
	d.persist(d.store.PutRecord("complink", l.tok.HostUserId, sl))
	if changed {
		d.audit("computer", "cert-changed", l.tok.HostUserId, sl.Record.GetName()+" answers with another certificate: link it again to check its code")
	}
	d.publishComputers()
}

// unlinkNotice is what a computer that isn't linked with this one is told
// when it connects: that this one doesn't link with it. That's all: no
// computer of yours is named to it. Callers hold d.mu.
func (d *Daemon) unlinkNotice(uid string) *pb.Computers {
	if uid == "" || d.isLinked(uid) {
		return nil
	}
	return &pb.Computers{NotLinked: true}
}

// setOwnLook sets this computer's badge colour ("": kept) and, if given,
// its icon ("": its kind's symbol). Another's is set by asking it
// (conn.go). Callers hold d.mu.
func (d *Daemon) setOwnLook(tint string, icon *string) error {
	if tint != "" {
		if !validTint(tint) {
			return errors.New("a colour is #RRGGBB, or none")
		}
		for uid, l := range d.comp.links {
			if tint != neutralTint && d.isLinked(uid) && strings.EqualFold(l.Record.Tint, tint) {
				return fmt.Errorf("%s has that colour already", l.Record.Name)
			}
		}
	}
	if icon != nil && !validIcon(*icon) {
		return errors.New("an icon is an SF Symbol's name or an emoji")
	}
	if tint == "" && icon == nil {
		return errors.New("nothing to set: a colour or an icon")
	}
	if tint != "" {
		d.comp.self.Tint = tint
		if tint != neutralTint {
			d.comp.self.Tint = strings.ToUpper(tint)
		}
	}
	if icon != nil {
		d.comp.self.Icon = *icon
	}
	d.saveSelf()
	d.publishComputers()
	return nil
}

// ensureOwnTint gives this computer a badge colour none of the others has,
// reporting whether it changed. Each computer changes only its own: when
// two have the same one, the one with the greater user id gives way, so
// they settle however the records arrive. Callers hold d.mu.
func (d *Daemon) ensureOwnTint() bool {
	s := d.comp.self
	if s.Tint == neutralTint {
		return false // picked: several may have none
	}
	clash := s.Tint == ""
	for uid, l := range d.comp.links {
		if d.isLinked(uid) && strings.EqualFold(l.Record.Tint, s.Tint) && uid < d.id.me.UserId {
			clash = true
		}
	}
	if !clash {
		return false
	}
	s.Tint = d.freeTint()
	d.saveSelf()
	return true
}

// ---------- linking ----------

func linkReqUpdate(r *pb.LinkRequest) *pb.StateUpdate {
	return &pb.StateUpdate{LinkRequests: []*pb.LinkRequest{proto.CloneOf(r)}}
}

// pendingLinks are the link requests showing a code, oldest first (a
// client that connects after the code showed still sees it). Callers hold d.mu.
func (d *Daemon) pendingLinks() []*pb.LinkRequest {
	var out []*pb.LinkRequest
	for _, a := range d.comp.attempts {
		if a.req.Code != "" {
			out = append(out, proto.CloneOf(a.req))
		}
	}
	slices.SortFunc(out, func(a, b *pb.LinkRequest) int { return cmp.Compare(a.CreatedUnixMs, b.CreatedUnixMs) })
	return out
}

// resolveLink ends a link attempt. An incoming one's asking computer, if
// still waiting on an answer (to its reveal or its confirm), gets why
// ("" once answered). Callers hold d.mu.
func (d *Daemon) resolveLink(id, why string) {
	u := &pb.StateUpdate{}
	if d.endLink(id, why, u) {
		d.publish(u)
	}
}

// endLink ends a link attempt as resolveLink does, adding its resolution
// to u (always: clients drop it whether or not they saw it) for the caller
// to publish. Reports whether there was one. Callers hold d.mu.
func (d *Daemon) endLink(id, why string, u *pb.StateUpdate) bool {
	a := d.comp.attempts[id]
	if a == nil {
		return false
	}
	delete(d.comp.attempts, id)
	if a.c != nil && why != "" {
		no := &pb.ServerMessage_Error{Error: &pb.Error{Message: why}}
		if a.revealID != 0 && !a.revealDone {
			a.c.send(&pb.ServerMessage{ReplyTo: a.revealID, Msg: no})
		}
		if a.confirmID != 0 && !a.confirmDone {
			a.c.send(&pb.ServerMessage{ReplyTo: a.confirmID, Msg: no})
		}
	}
	if a.expires != nil {
		a.expires.Stop()
	}
	u.ResolvedLinkIds = append(u.ResolvedLinkIds, id)
	return true
}

// linkCode is the code both computers show: from both keys' ids, both
// certificates and both nonces. Someone in between has a different key or
// certificate on each side, so the codes differ.
func linkCode(askUID, askCert, ansUID, ansCert string, askNonce, ansNonce []byte) string {
	h := sha256.New()
	for _, s := range []string{"hmk-link-code-v1", askUID, askCert, ansUID, ansCert} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	h.Write(askNonce)
	h.Write(ansNonce)
	n := binary.BigEndian.Uint32(h.Sum(nil)) % 1_000_000
	return fmt.Sprintf("%03d %03d", n/1000, n%1000)
}

// ensureRemote turns remote access on (linking needs it on both). Callers hold d.mu.
func (d *Daemon) ensureRemote() error {
	if d.remote.on {
		return nil
	}
	if err := d.startRemote(); err != nil {
		return err
	}
	d.persist(d.store.SetSetting("remote", "true")) // startRemote refreshed this computer's record
	return nil
}

// expectWindow is how long a person opening Link a Computer (or hmk link
// --accept, --invite) lets computers off Tailscale ask to link here.
var expectWindow = 10 * time.Minute

// expectLinks: a person here is linking, so computers off Tailscale may ask
// to link for a while (ADR-0015). Callers hold d.mu.
func (d *Daemon) expectLinks() { d.comp.expectUntil = time.Now().Add(expectWindow) }

// errNotExpecting: a computer off Tailscale asked to link while nobody here
// is linking. Nothing is shown here: a request nobody started is refused.
var errNotExpecting = errors.New("that computer isn't expecting a link: open Link a Computer on it too (or run hmk link --accept there), then try again")

// createLinkInvite is a hammock://link/ link to this computer. Callers hold d.mu.
func (d *Daemon) createLinkInvite() (*pb.ServerMessage, error) {
	d.expectLinks()
	if err := d.ensureRemote(); err != nil {
		return nil, err
	}
	addrs, lan := d.reachAt()
	tok, _ := proto.Marshal(&pb.LinkToken{Name: d.macName(), Addresses: addrs, CertSha256: d.remote.fingerprint, UserId: d.id.me.UserId, LanSubnets: lan})
	return &pb.ServerMessage{Msg: &pb.ServerMessage_Invite{Invite: &pb.Invite{Link: "hammock://link/" + base64.RawURLEncoding.EncodeToString(tok)}}}, nil
}

// parseLinkTarget reads LinkComputer's address: host[:port], or a link.
func parseLinkTarget(s string) (addrs, lan []string, cert, uid string, err error) {
	s = strings.TrimSpace(s)
	if raw, ok := strings.CutPrefix(s, "hammock://link/"); ok {
		b, err := base64.RawURLEncoding.DecodeString(raw)
		tok := &pb.LinkToken{}
		if err != nil || proto.Unmarshal(b, tok) != nil || len(tok.Addresses) == 0 || tok.CertSha256 == "" || tok.UserId == "" {
			return nil, nil, "", "", errors.New("that link is damaged; make a new one")
		}
		return tok.Addresses, tok.LanSubnets, tok.CertSha256, tok.UserId, nil
	}
	if s == "" {
		return nil, nil, "", "", errors.New("which computer?")
	}
	if _, _, err := net.SplitHostPort(s); err != nil {
		s = net.JoinHostPort(strings.Trim(s, "[]"), strconv.Itoa(DefaultRemotePort))
	}
	return []string{s}, nil, "", "", nil
}

// linkComputer links with another computer of yours (LinkComputer). The
// reply comes once linked, or not.
func (d *Daemon) linkComputer(c *conn, id uint64, m *pb.LinkComputer) (*pb.ServerMessage, error) {
	addrs, lan, cert, want, err := parseLinkTarget(m.Address)
	if err != nil {
		return nil, err
	}
	d.warmRemote()
	d.mu.Lock()
	err = d.ensureRemote()
	d.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("linking needs remote access: %w", err)
	}
	go func() {
		reply := &pb.ServerMessage{ReplyTo: id}
		if err := d.runLink(addrs, lan, cert, want); err != nil {
			reply.Msg = &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}
		} else {
			reply.Msg = &pb.ServerMessage_Ok{Ok: &pb.Ok{}}
		}
		if id != 0 {
			c.send(reply)
		}
	}()
	return nil, errDeferred
}

// runLink is the asking side of linking: connect, sign in, exchange
// nonces, show the code, and once both people say it matches, link.
// dialed is a connection made while linking, and the certificate it found.
type dialed struct {
	cl *client.Client
	fp string
}

func (x dialed) Close() error { return x.cl.Close() }

func (d *Daemon) runLink(addrs, lan []string, cert, want string) error {
	ctx, cancel := context.WithTimeout(context.Background(), linkTimeout+time.Minute)
	defer cancel()
	d.mu.Lock()
	name := d.macName()
	d.mu.Unlock()
	got, addr, err, _ := raceDial(ctx, worthTrying(addrs, lan), 10*time.Second, func(ctx context.Context, a string) (dialed, error) {
		if cert != "" {
			cl, err := client.DialRemote(ctx, a, cert, "peer", name)
			return dialed{cl, cert}, err
		}
		cl, fp, err := client.DialRemoteLearning(ctx, a, "peer", name)
		return dialed{cl, fp}, err
	})
	if err != nil {
		return fmt.Errorf("can't reach it (is Hammock running there, with remote access on, on a network this computer is on too?): %w", err)
	}
	cl, fp := got.cl, got.fp
	defer cl.Close()
	// Who it is, shown with the code: over Tailscale, who Tailscale says
	// (the other computer only links with your own Tailscale user's, so
	// say so here first); elsewhere, its address and network.
	host, _, _ := net.SplitHostPort(addr)
	src, srcErr := whoIsAsking(ctx, host)
	if srcErr != nil {
		return fmt.Errorf("%s belongs to %s on Tailscale, not you: only your own computers link", src.host, cmpOr(src.login, "someone else"))
	}
	uid, theirKey, err := d.peerSignIn(ctx, cl, fp, want)
	if err != nil {
		return err
	}
	d.mu.Lock()
	sl := d.comp.links[uid]
	switch {
	case uid == d.id.me.UserId:
		d.mu.Unlock()
		return errors.New("that's this computer")
	case d.isLinked(uid) && sl.CertSha256 == fp && !sl.CertChanged:
		// (Linking again is for one that doesn't link with this one, waits
		// for its code, or whose certificate changed: the check confirms it.)
		d.mu.Unlock()
		return errors.New("it's linked already")
	}
	d.refreshSelf(true)
	self := d.selfRecord(true) // as the others see it (whether its app is open, too)
	refusal := proto.CloneOf(d.comp.refusals[uid])
	unlinkedBy := ""
	if refusal != nil && refusal.By != "" {
		unlinkedBy = d.nameOf(refusal.By)
	}
	d.mu.Unlock()
	if len(self.Addresses) == 0 {
		return errors.New("this computer isn't listening on any network the other could reach it on: choose one in Settings ▸ Devices ▸ Remote Access (or hmk remote networks)")
	}

	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	commit := sha256.Sum256(nonce)
	r, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerLinkHello{PeerLinkHello: &pb.PeerLinkHello{Commitment: commit[:], Computer: self}}})
	if err != nil {
		return err
	}
	pn := r.GetPeerLinkNonce()
	if len(pn.GetNonce()) != 32 || pn.GetComputer().GetUserId() != uid {
		return errors.New("it answered oddly; is it running the same Hammock?")
	}
	// Its own record, for the certificate and key just checked.
	their := proto.CloneOf(pn.Computer)
	if their.CertSha256 != fp || !bytes.Equal(their.Key, theirKey) {
		return errors.New("it answered oddly; is it running the same Hammock?")
	}
	a := &linkAttempt{answer: make(chan bool, 1), req: &pb.LinkRequest{
		Id: newID("lk"), Name: their.Name, Model: their.Model, Kind: their.Kind, Os: their.Os,
		Code:        linkCode(self.UserId, self.CertSha256, uid, fp, nonce, pn.Nonce),
		Fingerprint: keyFingerprint(uid), CreatedUnixMs: time.Now().UnixMilli(),
		TailscaleHost: src.host, TailscaleLogin: src.login, Source: src.source,
		UnlinkedUnixMs: refusal.GetUnixMs(), UnlinkedBy: unlinkedBy,
	}}
	d.mu.Lock()
	d.comp.attempts[a.req.Id] = a
	d.publish(linkReqUpdate(a.req))
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.resolveLink(a.req.Id, "")
		d.mu.Unlock()
	}()

	revealed := make(chan error, 1)
	go func() {
		_, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerLinkReveal{PeerLinkReveal: &pb.PeerLinkReveal{Nonce: nonce}}})
		revealed <- err
	}()
	deadline := time.Now().Add(linkTimeout)
	timeout := time.After(linkTimeout)
	for {
		select {
		case err := <-revealed:
			if err != nil {
				return err
			}
			revealed = nil
			d.mu.Lock()
			a.req.AcceptedThere = true
			d.publish(linkReqUpdate(a.req))
			d.mu.Unlock()
		case ok := <-a.answer:
			if !ok {
				_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerLinkConfirm{PeerLinkConfirm: &pb.PeerLinkConfirm{}}})
				return errors.New("the codes didn't match, so they aren't linked")
			}
			// Waiting for the other person too, but no longer than the code
			// is good for (it answers once they do, or the attempt ends).
			cctx, ccancel := context.WithDeadline(ctx, deadline)
			r, err := cl.Call(cctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerLinkConfirm{PeerLinkConfirm: &pb.PeerLinkConfirm{Accept: true}}})
			ccancel()
			if errors.Is(err, context.DeadlineExceeded) {
				return errors.New("nobody checked the code in time")
			}
			if err != nil {
				// Declined there: its answer to the reveal says so.
				if revealed != nil {
					select {
					case rerr := <-revealed:
						if rerr != nil {
							return rerr
						}
					case <-time.After(2 * time.Second):
					}
				}
				return err
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			for _, x := range r.GetPeerLinked().GetComputers() {
				if x.UserId == uid {
					their = x // its own record, as it is now
				}
			}
			// A person checked the code here: linked, with the key and
			// certificate just checked, reached at addr (tried first).
			d.addLink(their, theirKey, fp, "", addr, linkIDFor(nonce, pn.Nonce), nil)
			d.clearNote()
			d.publishLinked(a.req.Id)
			return nil
		case <-timeout:
			return errors.New("nobody checked the code in time")
		}
	}
}

// peerSignIn signs in to another daemon's remote listener with this
// computer's key, and has it prove its own (bound to its certificate fp),
// returning its user id and key. want, if set, is the id it must have.
func (d *Daemon) peerSignIn(ctx context.Context, cl *client.Client, fp, want string) (string, []byte, error) {
	var nonce []byte
	for nonce == nil {
		select {
		case ev, ok := <-cl.Events:
			if !ok {
				return "", nil, errors.New("closed")
			}
			if c := ev.GetAuthChallenge(); c != nil {
				if len(c.Nonce) != client.NonceSize {
					return "", nil, errors.New("bad challenge")
				}
				nonce = c.Nonce
			}
		case <-ctx.Done():
			return "", nil, ctx.Err()
		}
	}
	d.mu.Lock()
	me := proto.CloneOf(d.id.me)
	key := d.id.key
	d.mu.Unlock()
	if len(me.Avatar) > 256<<10 {
		me.Avatar = nil
	}
	hostNonce := make([]byte, client.NonceSize)
	_, _ = rand.Read(hostNonce)
	w, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerHello{PeerHello: peerHelloFor(key, me, fp, nonce, hostNonce)}})
	if err != nil {
		return "", nil, err
	}
	uid, err := verifyWelcome(w.GetPeerWelcome(), fp, hostNonce)
	if err != nil {
		return "", nil, err
	}
	if want != "" && uid != want {
		return "", nil, errors.New("it isn't the computer the link named")
	}
	return uid, w.GetPeerWelcome().PublicKey, nil
}

// ---------- the other side ----------

// linkAttemptFor is the incoming link on c. Callers hold d.mu.
func (d *Daemon) linkAttemptFor(c *conn) *linkAttempt {
	for _, a := range d.comp.attempts {
		if a.c == c {
			return a
		}
	}
	return nil
}

// Asking to link: at most one ask per key or address in linkAskEvery, and
// linkAsksMax waiting at once (each shows a code and notifies).
// (Variables for tests.)
var (
	linkAskEvery = 10 * time.Second
	linkAsksMax  = 3
)

// overTailscale: ip is a Tailscale address (replaceable in tests, whose
// computers talk over loopback).
var overTailscale = isTailscaleIP

// tsSource is who Tailscale says is at an address (or, not on Tailscale,
// where it is: source).
type tsSource struct {
	host, login string
	mine        bool   // owned by this computer's own Tailscale user
	source      string // not over Tailscale: its address and network ("192.168.1.30 on Wi-Fi")
}

// whoIsAsking is who's at ip, for a link's code check: over Tailscale, who
// Tailscale says (refused if it's someone else's node: on a tailnet shared
// with others, theirs can't ask); elsewhere, its address and network, the
// code check being what decides (ADR-0015). Off the lock.
func whoIsAsking(ctx context.Context, ip string) (tsSource, error) {
	x := net.ParseIP(ip)
	if x == nil || !overTailscale(x) {
		return tsSource{mine: true, source: cachedNetSnapshot().sourceLabel(ip)}, nil
	}
	src, err := whoIsAt(ctx, ip)
	switch {
	case errors.Is(err, errNotOnTailnet), err != nil && len(tailscaleAddrsCached()) == 0:
		// In Tailscale's range but not on the tailnet (Tailscale says so,
		// or this computer isn't on Tailscale at all): another VPN using
		// that range, as Cloudflare WARP does. Treated as off Tailscale.
		return tsSource{mine: true, source: cachedNetSnapshot().sourceLabel(ip)}, nil
	case err != nil:
		// On Tailscale here, but it can't be asked: on a tailnet shared
		// with others, theirs mustn't get to ask.
		return tsSource{}, fmt.Errorf("can't check with Tailscale who's asking: %w", err)
	}
	if !src.mine {
		return src, errors.New("only computers your own Tailscale user owns can link")
	}
	return src, nil
}

// errNotOnTailnet: Tailscale answered, and nobody on the tailnet has that address.
var errNotOnTailnet = errors.New("it isn't a computer on your tailnet")

// whoIsAt asks Tailscale who's at ip (replaceable in tests).
var whoIsAt = func(ctx context.Context, ip string) (tsSource, error) {
	st, err := askTailscaleStatus(ctx)
	if err != nil {
		return tsSource{}, err
	}
	return st.whoIsAt(ip)
}

// whoIsAt finds the node with address ip: its name, its owner's login, and
// whether that's this computer's own user.
func (st *tailscaleStatus) whoIsAt(ip string) (tsSource, error) {
	want := net.ParseIP(ip)
	for _, p := range st.Peer {
		if p == nil {
			continue
		}
		for _, a := range p.TailscaleIPs {
			if x := net.ParseIP(a); x != nil && x.Equal(want) {
				login := ""
				if u := st.User[strconv.FormatInt(p.UserID, 10)]; u != nil {
					login = u.LoginName
				}
				return tsSource{host: cmpOr(p.HostName, strings.Split(p.DNSName, ".")[0]), login: login, mine: p.UserID == st.Self.UserID}, nil
			}
		}
	}
	return tsSource{}, errNotOnTailnet
}

// peerLinkHelloFrom checks who's asking to link (whoIsAsking, off the
// lock), then answers it (peerLinkHello). Over Tailscale, only computers
// your own Tailscale user owns may: on a tailnet shared with others, theirs
// can't. Elsewhere, anyone who reaches remote access may ask: the code
// check on both computers decides.
func (d *Daemon) peerLinkHelloFrom(c *conn, id uint64, m *pb.PeerLinkHello) (*pb.ServerMessage, error) {
	d.warmRemote() // (its record, sent back, has this one's addresses)
	d.mu.Lock()
	err := d.mayAskToLink(c)
	ip := c.srcIP
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		src, err := whoIsAsking(ctx, ip)
		var r *pb.ServerMessage
		switch {
		case err != nil:
			d.mu.Lock()
			who := ip
			if src.host != "" {
				who = src.host + " (" + src.login + ")"
			}
			d.audit("computer", "link-refused", c.peer, who+": "+err.Error())
			d.mu.Unlock()
		default:
			d.mu.Lock()
			if src.source != "" && !d.isLinked(c.peer) && time.Now().After(d.comp.expectUntil) {
				// Off Tailscale, only while a person here is linking: a
				// request nobody started never reaches a screen (ADR-0015).
				err = errNotExpecting
				d.audit("computer", "link-refused", c.peer, src.source+": nobody here is linking")
				d.mu.Unlock()
				break
			}
			if _, open := d.conns[c]; !open || c.ctx.Err() != nil {
				// It hung up while Tailscale was asked: linkConnClosed has
				// been and gone, so don't leave an attempt behind.
				d.mu.Unlock()
				return
			}
			r, err = d.peerLinkHello(c, m, src)
			d.mu.Unlock()
		}
		if err != nil {
			r = &pb.ServerMessage{Msg: &pb.ServerMessage_Error{Error: &pb.Error{Message: err.Error()}}}
		}
		if id != 0 {
			r.ReplyTo = id
			c.send(r)
		}
	}()
	return nil, errDeferred
}

// mayAskToLink: c may ask to link now (not too often, nor too many at
// once), noting that it did. Callers hold d.mu.
func (d *Daemon) mayAskToLink(c *conn) error {
	switch {
	case c.peer == "" || !c.remote || c.srcIP == "":
		return errors.New("only for other computers")
	}
	// (A computer linked already may ask again: the asking one does so only
	// when the link needs its code checked, e.g. its certificate changed.)
	now := time.Now()
	for _, k := range []string{"key:" + c.peer, "ip:" + c.srcIP} {
		if now.Sub(d.comp.askedAt[k]) < linkAskEvery {
			return errors.New("it asked a moment ago: try again shortly")
		}
	}
	waiting := 0
	for _, a := range d.comp.attempts {
		if a.c != nil && a.c != c && a.peer != c.peer {
			waiting++
		}
	}
	if waiting >= linkAsksMax {
		return errors.New("too many computers are asking to link right now")
	}
	for k, t := range d.comp.askedAt {
		if now.Sub(t) > linkAskEvery {
			delete(d.comp.askedAt, k)
		}
	}
	d.comp.askedAt["key:"+c.peer], d.comp.askedAt["ip:"+c.srcIP] = now, now
	return nil
}

// peerLinkHello: another computer (src, as Tailscale says) asks to link.
// Callers hold d.mu.
func (d *Daemon) peerLinkHello(c *conn, m *pb.PeerLinkHello, src tsSource) (*pb.ServerMessage, error) {
	switch {
	case c.peer == "":
		return nil, errors.New("only for other computers")
	case len(m.Commitment) != 32 || m.Computer.GetUserId() != c.peer || m.Computer.GetCertSha256() == "" || len(m.Computer.GetAddresses()) == 0:
		return nil, errors.New("bad link request")
	}
	// One request per computer asking: a new one replaces the last.
	for _, old := range d.comp.attempts {
		if old.c != nil && (old.c == c || old.peer == c.peer) {
			d.resolveLink(old.req.Id, "it asked again: the newer request is the one to check")
		}
	}
	d.refreshSelf(true)
	nonce := make([]byte, 32)
	_, _ = rand.Read(nonce)
	their := proto.CloneOf(m.Computer)
	if len(their.Addresses) > 8 || userIDFor(their.Key) != c.peer {
		return nil, errors.New("bad link request: its record isn't its own")
	}
	a := &linkAttempt{c: c, peer: c.peer, commit: m.Commitment, nonce: nonce, their: their, req: &pb.LinkRequest{
		Id: newID("lk"), Name: their.Name, Model: their.Model, Kind: their.Kind, Os: their.Os,
		Fingerprint: keyFingerprint(c.peer), Incoming: true, CreatedUnixMs: time.Now().UnixMilli(),
		TailscaleHost: src.host, TailscaleLogin: src.login, Source: src.source,
	}}
	if r := d.comp.refusals[c.peer]; r != nil {
		// A computer you unlinked (lost, stolen?) asking to be linked again.
		a.req.UnlinkedUnixMs = r.UnixMs
		if r.By != "" {
			a.req.UnlinkedBy = d.nameOf(r.By)
		}
	}
	d.comp.attempts[a.req.Id] = a
	id := a.req.Id
	a.expires = time.AfterFunc(linkTimeout, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.resolveLink(id, "nobody checked the code in time")
	})
	return &pb.ServerMessage{Msg: &pb.ServerMessage_PeerLinkNonce{PeerLinkNonce: &pb.PeerLinkNonce{Nonce: nonce, Computer: d.computersState(true).List[0]}}}, nil
}

// peerLinkReveal: the asking computer's nonce; both now show the code. The
// reply waits for the person here. Callers hold d.mu.
func (d *Daemon) peerLinkReveal(c *conn, msgID uint64, m *pb.PeerLinkReveal) (*pb.ServerMessage, error) {
	a := d.linkAttemptFor(c)
	if a == nil || a.req.Code != "" {
		return nil, errors.New("no link waiting")
	}
	if sum := sha256.Sum256(m.Nonce); len(m.Nonce) != 32 || string(sum[:]) != string(a.commit) {
		d.resolveLink(a.req.Id, "")
		return nil, errors.New("that nonce isn't the one committed to")
	}
	a.req.Code = linkCode(a.peer, a.their.CertSha256, d.id.me.UserId, d.remote.fingerprint, m.Nonce, a.nonce)
	a.askNonce = m.Nonce
	a.revealID = msgID
	d.publish(linkReqUpdate(a.req))
	d.audit("computer", "link-asked", a.peer, a.their.Name)
	// No phone push: linking needs you at both computers (the asking one
	// shows the same code), and only a computer itself may answer.
	return nil, errDeferred
}

// peerLinkConfirm: the person on the asking computer answered. Callers hold d.mu.
func (d *Daemon) peerLinkConfirm(c *conn, msgID uint64, m *pb.PeerLinkConfirm) (*pb.ServerMessage, error) {
	a := d.linkAttemptFor(c)
	if a == nil {
		return nil, errors.New("no link waiting")
	}
	// It may come before the reveal (sent from another goroutine there):
	// the person here can only answer once the code shows, after it.
	if !m.Accept {
		d.resolveLink(a.req.Id, "cancelled")
		return nil, nil
	}
	a.confirmed, a.confirmID = true, msgID
	a.req.AcceptedThere = true
	if a.req.AcceptedHere {
		d.finishIncoming(a)
	} else if a.req.Code != "" {
		d.publish(linkReqUpdate(a.req)) // (before the reveal there's nothing to show yet)
	}
	return nil, errDeferred
}

// respondLink is the person's answer to a LinkRequest. Callers hold d.mu.
func (d *Daemon) respondLink(m *pb.RespondLink) error {
	a := d.comp.attempts[m.Id]
	if a == nil || a.req.Code == "" {
		return errors.New("no such link request")
	}
	if a.c == nil {
		// Asked from here: runLink takes it on.
		select {
		case a.answer <- m.Accept:
		default:
		}
		if m.Accept {
			a.req.AcceptedHere = true
			d.publish(linkReqUpdate(a.req))
		}
		return nil
	}
	if !m.Accept {
		no := &pb.ServerMessage_Error{Error: &pb.Error{Message: d.macName() + " says the codes don't match, so they aren't linked"}}
		if a.revealID != 0 {
			a.c.send(&pb.ServerMessage{ReplyTo: a.revealID, Msg: no})
		}
		if a.confirmID != 0 {
			a.c.send(&pb.ServerMessage{ReplyTo: a.confirmID, Msg: no})
		}
		a.revealDone, a.confirmDone = true, true
		d.resolveLink(a.req.Id, "")
		d.audit("user", "link-declined", a.peer, a.their.Name)
		return nil
	}
	if !a.req.AcceptedHere {
		a.req.AcceptedHere, a.revealDone = true, true
		a.c.send(&pb.ServerMessage{ReplyTo: a.revealID, Msg: &pb.ServerMessage_Ok{Ok: &pb.Ok{}}})
	}
	if a.confirmed {
		d.finishIncoming(a)
	} else {
		d.publish(linkReqUpdate(a.req))
	}
	return nil
}

// finishIncoming links the asking computer once both people said the code
// matches, sending it this computer's list. Callers hold d.mu.
func (d *Daemon) finishIncoming(a *linkAttempt) {
	// A person checked the code here: linked, with the key it signed in with
	// and the certificate it named (it's dialled with that pinned), reached
	// where it dialled from, at the port it listens on.
	reached := ""
	if len(a.their.Addresses) > 0 && a.c.srcIP != "" {
		if _, port, err := net.SplitHostPort(a.their.Addresses[0]); err == nil {
			reached = net.JoinHostPort(a.c.srcIP, port)
		}
	}
	d.addLink(a.their, a.their.Key, a.their.CertSha256, "", reached, linkIDFor(a.askNonce, a.nonce), nil)
	d.clearNote()
	d.knowPerson(cmpPerson(d.strangers[a.peer], d.people[a.peer], &pb.Person{UserId: a.peer, Name: a.their.Name, DeviceName: a.their.Name}))
	a.c.send(&pb.ServerMessage{ReplyTo: a.confirmID, Msg: &pb.ServerMessage_PeerLinked{PeerLinked: &pb.PeerLinked{Computers: []*pb.Computer{d.selfRecord(true)}}}})
	a.confirmDone = true
	d.publishLinked(a.req.Id)
}

// clearNote drops the note about your computers (a person linked one
// again). Callers hold d.mu.
func (d *Daemon) clearNote() {
	if d.comp.note != "" {
		d.comp.note = ""
		d.persist(d.store.SetSetting("computers.note", ""))
	}
}

// publishLinked tells clients a link is made, once: the computers (noted
// as sent, as publishComputers does) and request reqID done. Callers hold d.mu.
func (d *Daemon) publishLinked(reqID string) {
	u := &pb.StateUpdate{Computers: d.computersState(false)}
	d.comp.sentLocal, d.comp.sentPeers = u.Computers, d.computersState(true)
	d.comp.sentInput = d.comp.sentPeers.List[0].LastInputUnixMs
	d.endLink(reqID, "", u)
	d.publish(u)
}

// linkConnClosed drops the links its connection was asking for. Callers hold d.mu.
func (d *Daemon) linkConnClosed(c *conn) {
	for id, a := range d.comp.attempts {
		if a.c == c {
			d.resolveLink(id, "") // nobody to tell
		}
	}
}

// ---------- the owner's scope ----------

// ownerCheck decides whether a linked computer may send msg: what a
// driver may in every Group here, and its Groups' admin, sessions and
// folders, answering its agents' grants and using its SSH keys (you're
// there); never this computer's devices, sharing or remote access.
// Callers hold d.mu.
func (d *Daemon) ownerCheck(c *conn, msg *pb.ClientMessage) (ignore bool, err error) {
	errNo := errors.New("not available from your other computers")
	local := func(id string) bool {
		return id != "" && (d.groups[id] != nil || d.tabs[id] != nil || d.panes[id] != nil || d.attention[id] != nil || d.grants[id] != nil || d.sched.list[id] != nil)
	}
	here := func(cid *string) error {
		if *cid != "" && *cid != d.id.me.UserId {
			return errNo // never on to a third computer
		}
		*cid = ""
		return nil
	}
	if t := msgTarget(msg); t != "" && !local(t) {
		if _, ok := msg.Msg.(*pb.ClientMessage_DetachPane); ok {
			return false, nil
		}
		return false, errors.New("no such group, tab or pane")
	}
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_Presence:
		m.Presence.VisiblePaneIds = filterSlice(m.Presence.VisiblePaneIds, local)
		if !local(m.Presence.FocusedPaneId) {
			m.Presence.FocusedPaneId = ""
		}
		return false, nil
	case *pb.ClientMessage_Hello, *pb.ClientMessage_Subscribe, *pb.ClientMessage_AttachPane, *pb.ClientMessage_DetachPane,
		*pb.ClientMessage_OpenMedia, *pb.ClientMessage_CloseMedia, *pb.ClientMessage_MediaAck, *pb.ClientMessage_SetMediaView,
		*pb.ClientMessage_BrowserInput, *pb.ClientMessage_BrowserMenuChoice, *pb.ClientMessage_BrowserNavigate,
		*pb.ClientMessage_WatchAgent, *pb.ClientMessage_ReadAgentFile, *pb.ClientMessage_WatchLog, *pb.ClientMessage_ReadLog, *pb.ClientMessage_ClearLog,
		*pb.ClientMessage_EditorPush, *pb.ClientMessage_EditorSelect, *pb.ClientMessage_EditorCommand, *pb.ClientMessage_ReadEditor, *pb.ClientMessage_ListFiles,
		*pb.ClientMessage_ReadGroupIcon, *pb.ClientMessage_SetGroupIcon, *pb.ClientMessage_SetGroupIconImage, *pb.ClientMessage_SetGroupFolder, *pb.ClientMessage_RenameGroup, *pb.ClientMessage_CloseGroup,
		*pb.ClientMessage_ReadPane, *pb.ClientMessage_Resize, *pb.ClientMessage_Focus, *pb.ClientMessage_Input, *pb.ClientMessage_SignalPane,
		*pb.ClientMessage_AgentAnswer, *pb.ClientMessage_AgentSend, *pb.ClientMessage_UploadFile, *pb.ClientMessage_SwitchAgent,
		*pb.ClientMessage_ClosePane, *pb.ClientMessage_CreatePane, *pb.ClientMessage_CreateTab, *pb.ClientMessage_CloseTab, *pb.ClientMessage_RenameTab,
		*pb.ClientMessage_SetLayout, *pb.ClientMessage_BreakOutPane, *pb.ClientMessage_ReplacePane, *pb.ClientMessage_ClearAttention:
		return false, nil
	case *pb.ClientMessage_SaveSchedule:
		// You, from another of your computers: a schedule here.
		if s := m.SaveSchedule.Schedule; s != nil {
			return false, here(&s.ComputerId)
		}
		return false, nil
	case *pb.ClientMessage_DeleteSchedule, *pb.ClientMessage_RunSchedule, *pb.ClientMessage_SetSchedulePaused, *pb.ClientMessage_SkipScheduleRun:
		return false, nil // one here (checked above)
	case *pb.ClientMessage_RespondGrant:
		// You, answering an agent here from another of your computers
		// (that one doesn't let its agents answer: agentCheck).
		return false, nil
	case *pb.ClientMessage_SshAgentCall:
		return false, nil // this computer's SSH keys, for a pane there (sshagent.go)
	case *pb.ClientMessage_ReopenClosed:
		return false, here(&m.ReopenClosed.ComputerId) // its newest close here
	case *pb.ClientMessage_SetComputerTint:
		if m.SetComputerTint.UserId != d.id.me.UserId {
			return false, errNo // this computer's own colour only
		}
		return false, nil
	case *pb.ClientMessage_SimulatorCall:
		return false, nil // about a pane here (checked above), or the devices list
	case *pb.ClientMessage_MoveTab:
		if d.groups[m.MoveTab.GroupId] == nil {
			return false, errors.New("no such group")
		}
		return false, nil
	case *pb.ClientMessage_MergeTab:
		if !local(m.MergeTab.IntoPaneId) {
			return false, errors.New("no such pane")
		}
		return false, nil
	case *pb.ClientMessage_CreateGroup:
		return false, here(&m.CreateGroup.ComputerId)
	case *pb.ClientMessage_StartAgent:
		return false, here(&m.StartAgent.ComputerId)
	case *pb.ClientMessage_ListFolders:
		return false, here(&m.ListFolders.ComputerId)
	case *pb.ClientMessage_ListSessions:
		return false, here(&m.ListSessions.ComputerId)
	case *pb.ClientMessage_ReadSession:
		return false, here(&m.ReadSession.ComputerId)
	case *pb.ClientMessage_ResumeSession:
		return false, here(&m.ResumeSession.ComputerId)
	case *pb.ClientMessage_SessionExcerpts:
		return false, here(&m.SessionExcerpts.ComputerId)
	case *pb.ClientMessage_ListDisplays:
		if m.ListDisplays.RequestAccess != "" {
			return false, errNo // this Mac's Settings only
		}
		return false, here(&m.ListDisplays.ComputerId)
	case *pb.ClientMessage_ScreenInput:
		return false, here(&m.ScreenInput.ComputerId)
	case *pb.ClientMessage_ScreenSnapshot:
		return false, here(&m.ScreenSnapshot.ComputerId)
	}
	return false, errNo
}

// msgComputer is the linked computer a message names (CreateGroup on
// Studio, its sessions, its folders), "" for this one.
func msgComputer(msg *pb.ClientMessage) string {
	switch m := msg.Msg.(type) {
	case *pb.ClientMessage_CreateGroup:
		return m.CreateGroup.ComputerId
	case *pb.ClientMessage_StartAgent:
		if m.StartAgent.GroupId == "" {
			return m.StartAgent.ComputerId
		}
	case *pb.ClientMessage_ListFolders:
		if m.ListFolders.GroupId == "" {
			return m.ListFolders.ComputerId
		}
	case *pb.ClientMessage_SaveSchedule:
		if s := m.SaveSchedule.Schedule; s != nil && s.Id == "" {
			return s.ComputerId // a new schedule on another computer
		}
	case *pb.ClientMessage_ListSessions:
		return m.ListSessions.ComputerId
	case *pb.ClientMessage_ReadSession:
		return m.ReadSession.ComputerId
	case *pb.ClientMessage_ResumeSession:
		return m.ResumeSession.ComputerId
	case *pb.ClientMessage_SessionExcerpts:
		return m.SessionExcerpts.ComputerId
	case *pb.ClientMessage_ListDisplays:
		return m.ListDisplays.ComputerId
	case *pb.ClientMessage_ScreenInput:
		return m.ScreenInput.ComputerId
	case *pb.ClientMessage_ScreenSnapshot:
		return m.ScreenSnapshot.ComputerId
	case *pb.ClientMessage_OpenMedia:
		if m.OpenMedia.Screen {
			return m.OpenMedia.ComputerId
		}
	}
	return ""
}

// ---------- discovery ----------

// tailscaleStatus is the part of `tailscale status --json` discovery reads.
type tailscaleStatus struct {
	BackendState string // "Running", "NeedsLogin", "Stopped", …
	Self         *tailscalePeer
	Peer         map[string]*tailscalePeer
	User         map[string]*tailscaleUser // by user id
}

type tailscaleUser struct {
	LoginName   string
	DisplayName string
}

type tailscalePeer struct {
	HostName     string
	DNSName      string
	OS           string
	UserID       int64
	TailscaleIPs []string
	Online       bool
}

// parseTailscaleStatus reads `tailscale status --json`, saying why it's no
// use when this computer isn't on a tailnet yet (logged out, turned off,
// still connecting: Tailscale then has no Self).
func parseTailscaleStatus(raw []byte) (*tailscaleStatus, error) {
	// Anything printed before the JSON (a warning) isn't part of it.
	if i := bytes.IndexByte(raw, '{'); i > 0 {
		raw = raw[i:]
	}
	var st tailscaleStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("Tailscale's status couldn't be read (%v)", err)
	}
	if st.Self != nil && (st.BackendState == "" || st.BackendState == "Running") {
		return &st, nil
	}
	switch st.BackendState {
	case "NeedsLogin":
		return nil, errors.New("Tailscale isn't logged in on this Mac: log in from its menu bar item")
	case "NeedsMachineAuth":
		return nil, errors.New("this Mac is waiting for approval on your tailnet: approve it in Tailscale's admin console")
	case "Stopped":
		return nil, errors.New("Tailscale is turned off on this Mac: turn it on from its menu bar item")
	case "NoState", "Starting":
		return nil, errors.New("Tailscale is still connecting on this Mac: try again in a moment")
	case "", "Running":
		return nil, errors.New("Tailscale's status couldn't be read (it didn't say which computer this is)")
	}
	return nil, fmt.Errorf("Tailscale isn't connected on this Mac (it says %s)", st.BackendState)
}

// runTailscale runs Tailscale's CLI for its output (replaceable in tests).
var runTailscale = func(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return tailscaleCLI(ctx, path, args...).Output()
}

// askTailscaleStatus runs `tailscale status --json` with each CLI it finds
// until one says it's connected (replaceable in tests): a Mac can have two
// (Homebrew's, the app's), the first asking a tailscaled that isn't the
// one in use.
var askTailscaleStatus = func(ctx context.Context) (*tailscaleStatus, error) {
	var first error
	for _, name := range tailscaleCommands {
		path, err := lookPath(name)
		if err != nil {
			continue
		}
		out, err := runTailscale(ctx, path, "status", "--json")
		if err != nil {
			err = fmt.Errorf("Tailscale didn't answer; is it running? (%s: %v)", path, err)
		} else {
			var st *tailscaleStatus
			if st, err = parseTailscaleStatus(out); err == nil {
				return st, nil
			}
		}
		if first == nil {
			first = err
		}
	}
	if first == nil {
		first = errors.New("Tailscale isn't installed")
	}
	return nil, first
}

// probeHammock: something answers TLS on addr (Hammock's remote listener),
// returning its certificate's fingerprint.
var probeHammock = func(ctx context.Context, addr string) (string, bool) {
	return tlsFingerprint(ctx, addr)
}
