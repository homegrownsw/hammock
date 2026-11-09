package daemon

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Between linked computers (ADR-0013, direct links only): introductions,
// and unlink requests. Each computer acts only on what a computer it's
// linked with tells it, and only about its own links; an unlink it acts on
// it passes on.

// Limits on what each linked computer may ask of this one (each its own:
// one can't crowd out another's; nothing limits a person here).
const (
	asksPerMinute = 64   // unlink requests and introductions, from each, per askWindow
	maxDenied     = 1024 // keys refused here at its request (that weren't linked here)
	maxIntros     = 64   // introductions it has waiting here
	maxChain      = 8    // introducers in a link's chain (a longer introduction isn't taken)
)

// Variables for tests.
var (
	askWindow        = time.Minute
	reintroduceEvery = 10 * time.Minute // introductions offered again while links are up
	// A request waiting for a computer is sent within this of their link
	// coming up: one that arrives later was asked since (touchBound).
	touchSlack = 15 * time.Minute
)

// afterIntroCall (tests) runs once an introduced link's other side has
// answered, before this one checks it still wants it.
var afterIntroCall func(*Daemon)

// ignoresUnlinks holds daemons (in tests) that ignore unlink requests and
// notices, as a stolen computer's could.
var ignoresUnlinks sync.Map

var (
	errNotLinkedHere = errors.New("this computer doesn't link with yours")
	errTooManyAsks   = errors.New("too many requests: try again shortly")
)

// intro is an introduction offered here of rec's computer, by each of vias
// (several computers may introduce the same one; any still linked will do).
// It's tried until it's made, or none of them is linked with this one.
type intro struct {
	rec     *pb.Computer              // its key, pinned certificate and addresses, as the latest introducer knows them
	vias    map[string]bool           // who introduced it
	chains  map[string][]*pb.IntroHop // each one's chain of introducers for it (this clock)
	next    time.Time                 // when to dial it (the computer with the smaller id dials)
	tries   int
	dialing bool
}

// liveVias are in's introducers still linked with this one, in order.
// Callers hold d.mu.
func (d *Daemon) liveVias(in *intro) []string {
	var out []string
	for v := range in.vias {
		if d.isLinked(v) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}

// allowAsk: linked computer uid may ask something of this one now (a few
// a minute). Callers hold d.mu.
func (d *Daemon) allowAsk(uid string) bool {
	cut := time.Now().Add(-askWindow)
	recent := slices.DeleteFunc(d.comp.asks[uid], func(t time.Time) bool { return t.Before(cut) })
	if len(recent) >= asksPerMinute {
		d.comp.asks[uid] = recent
		return false
	}
	d.comp.asks[uid] = append(recent, time.Now())
	return true
}

// suspect: uid's link here waits for a code check (introduced by a
// computer since unlinked). Callers hold d.mu.
func (d *Daemon) suspect(uid string) bool {
	l := d.comp.links[uid]
	return l != nil && l.Suspect
}

// heldForCode: uid is linked here only by a person's code check (its link
// waits for one, or it was held so, here or by a computer that said so).
// Callers hold d.mu.
func (d *Daemon) heldForCode(uid string) bool { return d.suspect(uid) || d.comp.suspects[uid] != nil }

// badChain: a chain of introducers (this computer's clock) with a computer
// refused here and taken from after its unlink was asked (it could be its
// own key), or held here for its code, isn't taken. One held only because
// another computer said so is vouched for by this one's own clean link
// with it, if there is one (an honest computer, linked long before: what
// it introduced stands); without one, it taints the chain. Callers hold d.mu.
func (d *Daemon) badChain(chain []*pb.IntroHop) bool {
	for _, h := range chain {
		r := d.comp.refusals[h.By]
		if d.suspect(h.By) || d.comp.suspects[h.By] != nil && !d.cleanLink(h.By) ||
			r != nil && r.AskedUnixMs > 0 && h.AtUnixMs >= r.AskedUnixMs {
			return true
		}
	}
	return false
}

// cleanLink: this computer has its own link with uid, not held for its
// code. Callers hold d.mu.
func (d *Daemon) cleanLink(uid string) bool {
	l := d.comp.links[uid]
	return l != nil && !l.Suspect
}

// hopsOf is l's chain of introducers (an earlier build's link: its via,
// taken when it was linked here). Callers hold d.mu.
func hopsOf(l *pb.StoredLink) []*pb.IntroHop {
	if len(l.Chain) == 0 && l.Via != "" {
		return []*pb.IntroHop{{By: l.Via, AtUnixMs: l.Record.GetLinkedUnixMs()}}
	}
	return l.Chain
}

// linkedSince is when uid was linked here (as linked now, or as it was when
// refused; 0 if never). Callers hold d.mu.
func (d *Daemon) linkedSince(uid string) int64 {
	if l := d.comp.links[uid]; l != nil {
		return l.Record.GetLinkedUnixMs()
	}
	return d.comp.refusals[uid].GetLink().GetRecord().GetLinkedUnixMs()
}

// touchBound is the earliest computer uid can have asked something it's
// delivering only now, as far as this one can tell: not before it was
// linked here, nor before it was last out of touch (connected for a while,
// it would have sent it then). Its own word on when isn't taken further
// back. Callers hold d.mu.
func (d *Daemon) touchBound(uid string) int64 {
	if l := d.linkOf(uid); l != nil && l.online && !l.onlineAt.IsZero() && time.Since(l.onlineAt) > touchSlack {
		return time.Now().Add(-touchSlack).UnixMilli()
	}
	if sl := d.comp.links[uid]; sl != nil {
		return max(sl.Record.GetLastSeenUnixMs(), sl.Record.GetLinkedUnixMs())
	}
	return 0
}

// linkIDFor is a link's id as both computers work it out from the code
// check's nonces.
func linkIDFor(askNonce, ansNonce []byte) string {
	h := sha256.Sum256(slices.Concat([]byte("hmk-link-id-v1\x00"), askNonce, ansNonce))
	return "ln_" + hex.EncodeToString(h[:8])
}

// addNote adds a line to what Your Computers tells you (once). Callers hold d.mu.
func (d *Daemon) addNote(s string) {
	if strings.Contains(d.comp.note, s) {
		return
	}
	d.comp.note = strings.TrimSpace(d.comp.note + "\n" + s)
	d.persist(d.store.SetSetting("computers.note", d.comp.note))
	d.publishComputers()
}

// nameOf is what a computer is called here, as linked or refused. Callers hold d.mu.
func (d *Daemon) nameOf(uid string) string {
	if l := d.comp.links[uid]; l != nil && l.Record.GetName() != "" {
		return l.Record.GetName()
	}
	if r := d.comp.refusals[uid]; r != nil {
		if r.Name != "" {
			return r.Name
		}
		if n := r.Link.GetRecord().GetName(); n != "" {
			return n
		}
	}
	return "another computer (" + keyFingerprint(uid) + ")"
}

// ---------- introductions ----------

// introductionsFor are the computers this one is linked with, but to, as
// they're known here (key, pinned certificate, where to reach it). Callers
// hold d.mu.
func (d *Daemon) introductionsFor(to string) []*pb.Computer {
	var out []*pb.Computer
	for uid, l := range d.comp.links {
		if uid == to || !d.isLinked(uid) || len(l.Record.GetKey()) == 0 {
			continue
		}
		r := proto.CloneOf(l.Record)
		r.CertSha256, r.Online, r.Notifies, r.LastInputUnixMs, r.LastSeenUnixMs = l.CertSha256, false, false, 0, 0
		for _, h := range hopsOf(l) {
			r.Chain = append(r.Chain, &pb.IntroHop{By: h.By, AgoMs: max(time.Now().UnixMilli()-h.AtUnixMs, 0)})
		}
		if l.ReachedAt != "" && !slices.Contains(r.Addresses, l.ReachedAt) {
			r.Addresses = append([]string{l.ReachedAt}, r.Addresses...)
		}
		out = append(out, r)
	}
	return out
}

// introduce offers linked computer uid a link with each of the others (it
// ignores those it has, or refuses). Callers hold d.mu.
func (d *Daemon) introduce(uid string) {
	l := d.linkOf(uid)
	if l == nil || !l.online {
		return // when its link connects
	}
	list := d.introductionsFor(uid)
	if len(list) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if cl, err := l.connected(ctx, 5*time.Second); err == nil {
			_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_IntroduceComputers{IntroduceComputers: &pb.IntroduceComputers{Computers: list}}})
		}
	}()
}

// introduceAll introduces every linked computer to the others (a link was
// just made, or connected, or it's been a while). Callers hold d.mu.
func (d *Daemon) introduceAll() {
	d.comp.introducedAt = time.Now()
	for uid := range d.comp.links {
		if d.isLinked(uid) {
			d.introduce(uid)
		}
	}
}

// takeIntroductions is IntroduceComputers from c's computer: each one this
// one isn't linked with, and doesn't refuse, waits to be linked directly.
func (d *Daemon) takeIntroductions(c *conn, m *pb.IntroduceComputers) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.isLinked(c.peer) {
		return errNotLinkedHere
	}
	if !d.allowAsk(c.peer) {
		return errTooManyAsks
	}
	n := 0 // its introductions waiting here
	for _, in := range d.comp.intros {
		if in.vias[c.peer] {
			n++
		}
	}
	for _, r := range m.GetComputers()[:min(len(m.GetComputers()), maxIntros)] {
		cleanComputer(r)
		uid := r.GetUserId()
		// (A record of it here that says it doesn't link with this one, a
		// reset, say, doesn't stop it: its own refusal list does, there. One
		// waiting for its code to be checked here does.)
		if uid == "" || uid == d.id.me.UserId || uid == c.peer || d.isLinked(uid) || d.heldForCode(uid) || d.comp.denied[uid] ||
			d.badChain(localChain(r.Chain)) ||
			userIDFor(r.Key) != uid || !d.keyMatches(uid, r.Key) || r.CertSha256 == "" || len(r.Addresses) == 0 || len(r.Addresses) > 8 {
			continue
		}
		if len(r.Chain) >= maxChain {
			d.addNote(d.nameOf(c.peer) + " introduced " + cmpOr(r.Name, keyFingerprint(uid)) + ", but through too many computers to check: link it with this one directly if you want it.")
			continue
		}
		in := d.comp.intros[uid]
		if in == nil || !in.vias[c.peer] {
			if n >= maxIntros {
				continue
			}
			n++
		}
		if in == nil {
			in = &intro{vias: map[string]bool{}, chains: map[string][]*pb.IntroHop{}}
			d.comp.intros[uid] = in
		}
		in.rec, in.vias[c.peer] = proto.CloneOf(r), true
		in.chains[c.peer] = localChain(r.Chain)
		in.next, in.tries = time.Time{}, 0 // offered again: try at once
	}
	return nil
}

// runIntros links the computers introduced here: the one of each pair with
// the smaller user id dials the other, and both check, then, that they're
// still linked with a computer that introduced them. It also offers the
// introductions again now and then, so one that failed is tried again.
func (d *Daemon) runIntros() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		}
		d.mu.Lock()
		if time.Since(d.comp.introducedAt) >= reintroduceEvery {
			d.introduceAll()
		}
		for uid, in := range d.comp.intros {
			for v := range in.vias {
				if d.badChain(in.chains[v]) {
					delete(in.vias, v) // (its chain has a computer since unlinked in it)
				}
			}
			vias := d.liveVias(in)
			if len(vias) == 0 || d.comp.denied[uid] || d.isLinked(uid) || d.heldForCode(uid) {
				delete(d.comp.intros, uid)
				continue
			}
			if d.id.me.UserId < uid && !in.dialing && !time.Now().Before(in.next) {
				// Each on its own: one that's off doesn't hold up the rest.
				in.dialing = true
				chains := map[string][]*pb.IntroHop{}
				for _, v := range vias {
					chains[v] = in.chains[v]
				}
				go func(uid string, vias []string, in *intro, rec *pb.Computer) {
					err := d.introLink(uid, vias, chains, rec)
					d.mu.Lock()
					defer d.mu.Unlock()
					in.dialing = false
					if err != nil && d.comp.intros[uid] == in {
						in.tries++
						in.next = time.Now().Add(min(time.Second<<min(in.tries, 9), 5*time.Minute))
						d.log.Debug("introduced link", "to", uid, "err", err)
					}
				}(uid, vias, in, proto.CloneOf(in.rec))
			}
		}
		d.mu.Unlock()
	}
}

// introLink dials an introduced computer (with the certificate its
// introducers gave), proves this one's key and checks its, and links if it
// agrees. If this one backs out after it linked there, it's told to drop
// that link. Off d.mu.
func (d *Daemon) introLink(uid string, vias []string, chains map[string][]*pb.IntroHop, rec *pb.Computer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cl, addr, err, _ := raceDial(ctx, worthTrying(rec.Addresses, rec.LanSubnets), 5*time.Second, func(ctx context.Context, a string) (*client.Client, error) {
		return client.DialRemote(ctx, a, rec.CertSha256, "peer", "intro")
	})
	if err != nil {
		return err
	}
	defer cl.Close()
	got, key, err := d.peerSignIn(ctx, cl, rec.CertSha256, uid)
	if err != nil {
		return err
	}
	if got != uid || !bytes.Equal(key, rec.Key) {
		return errors.New("not the computer introduced")
	}
	d.mu.Lock()
	d.refreshSelf(false)
	self := d.selfRecord(true)
	d.mu.Unlock()
	linkID := newID("ln")
	r, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerIntroLink{PeerIntroLink: &pb.PeerIntroLink{
		Via: vias[0], Vias: vias, Computer: self, LinkId: linkID}}})
	if err != nil {
		return err
	}
	if afterIntroCall != nil {
		afterIntroCall(d)
	}
	ans := r.GetPeerIntroLinked()
	their := ans.GetComputer()
	via := cmpOr(ans.GetVia(), vias[0])
	if their.GetUserId() != uid || !slices.Contains(vias, via) {
		return errors.New("it answered oddly")
	}
	linkID = cmpOr(ans.GetLinkId(), linkID)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.isLinked(uid) {
		return nil // linked meanwhile
	}
	chain := introChain(via, chains[via])
	if !d.isLinked(via) || d.comp.denied[uid] || d.heldForCode(uid) || d.badChain(chain) {
		// Not introduced any more (its introducer unlinked, or it was):
		// it linked there, so it drops that link.
		to := proto.CloneOf(rec)
		to.Addresses = append([]string{addr}, slices.DeleteFunc(slices.Clone(rec.Addresses), func(a string) bool { return a == addr })...)
		d.putPending(&pb.PendingUnlink{UserId: d.id.me.UserId, To: uid, ToRecord: to, ToCert: rec.CertSha256, LinkId: linkID, QueuedUnixMs: time.Now().UnixMilli()})
		d.deliverUnlinks()
		return errors.New("no longer introduced")
	}
	d.addLink(their, rec.Key, rec.CertSha256, via, addr, linkID, chain)
	d.publishComputers()
	return nil
}

// peerIntroLink is the other side of introLink: c's computer (signed in
// with its key) asks to link because computers it names introduced them.
// Callers hold d.mu.
func (d *Daemon) peerIntroLink(c *conn, m *pb.PeerIntroLink) (*pb.ServerMessage, error) {
	in := d.comp.intros[c.peer]
	r := m.GetComputer()
	switch {
	case c.peer == "" || !c.remote:
		return nil, errors.New("only for other computers")
	case d.isLinked(c.peer):
		l := d.comp.links[c.peer]
		return &pb.ServerMessage{Msg: &pb.ServerMessage_PeerIntroLinked{PeerIntroLinked: &pb.PeerIntroLinked{
			Computer: d.selfRecord(true), Via: cmpOr(l.Via, m.Via), LinkId: l.LinkId}}}, nil
	case d.comp.denied[c.peer]:
		return nil, errors.New("this computer unlinked yours")
	case d.heldForCode(c.peer):
		return nil, errors.New("its code needs checking here first")
	case in == nil:
		return nil, errors.New("not introduced (yet)")
	}
	vias := m.GetVias()
	if len(vias) == 0 && m.Via != "" {
		vias = []string{m.Via}
	}
	via, tainted := "", false
	for _, v := range vias {
		if in.vias[v] && d.isLinked(v) {
			if d.badChain(introChain(v, in.chains[v])) {
				tainted = true // (try the next one)
				continue
			}
			via = v
			break
		}
	}
	chain := introChain(via, in.chains[via])
	switch {
	case via == "" && tainted:
		return nil, errors.New("introduced through a computer since unlinked")
	case via == "":
		return nil, errors.New("not introduced by a computer linked with this one (yet)")
	case r.GetUserId() != c.peer || r.GetCertSha256() != in.rec.CertSha256 || userIDFor(in.rec.Key) != c.peer ||
		!bytes.Equal(in.rec.Key, c.peerKey):
		return nil, errors.New("not the computer introduced")
	}
	reached := ""
	if len(in.rec.Addresses) > 0 && c.srcIP != "" {
		if _, port, err := net.SplitHostPort(in.rec.Addresses[0]); err == nil {
			reached = net.JoinHostPort(c.srcIP, port) // where it dialled from, at the port it listens on
		}
	}
	d.addLink(r, in.rec.Key, in.rec.CertSha256, via, reached, m.LinkId, chain)
	d.publishComputers()
	return &pb.ServerMessage{Msg: &pb.ServerMessage_PeerIntroLinked{PeerIntroLinked: &pb.PeerIntroLinked{
		Computer: d.selfRecord(true), Via: via, LinkId: m.LinkId}}}, nil
}

// ---------- unlinking ----------

// refuse refuses uid's key here for good (until a person links it here
// again): by a person here (by ""), or as computer by asked (origin: where
// a person asked; asked: when, on this computer's clock), keeping its
// link, if it had one, to answer it later. Callers hold d.mu.
func (d *Daemon) refuse(uid, by, origin string, asked int64, link *pb.StoredLink) {
	r := d.comp.refusals[uid]
	if r == nil {
		r = &pb.Refusal{UserId: uid, By: by, Origin: origin, UnixMs: time.Now().UnixMilli(), AskedUnixMs: asked}
	} else if by == "" {
		r.By, r.Origin = "", "" // a person here refused it too: its word isn't taken any more
	}
	if r.Name == "" {
		r.Name = link.GetRecord().GetName()
	}
	if link != nil && r.Link == nil {
		r.Link = proto.CloneOf(link)
	}
	if asked > 0 && (r.AskedUnixMs == 0 || asked < r.AskedUnixMs) {
		r.AskedUnixMs = asked
	}
	d.comp.denied[uid], d.comp.refusals[uid] = true, r
	d.persist(d.store.PutRecord("compdeny", uid, r))
}

// unrefuse: a person linked uid here again. Callers hold d.mu.
func (d *Daemon) unrefuse(uid string) {
	if d.comp.denied[uid] {
		delete(d.comp.denied, uid)
		delete(d.comp.refusals, uid)
		d.persist(d.store.DeleteRecord("compdeny", uid))
	}
}

// refusedAt is how many keys this one refused at by's request without
// having a link with them (its share of maxDenied). Callers hold d.mu.
func (d *Daemon) refusedAt(by string) int {
	n := 0
	for _, r := range d.comp.refusals {
		if r.By == by && r.Link == nil {
			n++
		}
	}
	return n
}

// unlinkHere unlinks uid here (refused, as by asked, or a person here: by
// ""), and passes the request on to each computer this one is linked with.
// Nothing goes to a computer refused here. mark: links uid introduced since
// the unlink was asked wait for a code check (whether this one heard first
// or not). Callers hold d.mu.
func (d *Daemon) unlinkHere(uid, by, origin string, asked int64, mark bool) {
	l := d.comp.links[uid]
	d.refuse(uid, by, origin, asked, l) // (refused already: keeps the earliest time it's told)
	if mark {
		// However it heard first (passed on, or straight from the computer
		// where a person asked), links uid introduced since are held.
		d.suspectIntroducedBy(uid, cmp.Or(d.comp.refusals[uid].GetAskedUnixMs(), asked))
	}
	if l == nil {
		return // (it had no link here: nothing to pass on)
	}
	d.dropComputer(uid, true, nil)
	for to := range d.comp.links {
		if to != by && to != origin && !d.suspect(to) {
			d.queueUnlink(uid, to, origin, asked, "")
		}
	}
	d.deliverUnlinks()
}

// suspectIntroducedBy marks the links x introduced since asked (when a
// person asked for x's unlink), and those they introduced, as waiting for
// a code check: not linked meanwhile, nor passed on. Callers hold d.mu.
func (d *Daemon) suspectIntroducedBy(x string, asked int64) {
	type hold struct {
		by    string
		since int64
	}
	queue := []hold{{x, asked}}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for uid, l := range d.comp.links {
			if l.Suspect || !slices.ContainsFunc(hopsOf(l), func(h *pb.IntroHop) bool { return h.By == v.by && h.AtUnixMs >= v.since }) {
				continue // (taken from v before its unlink was asked: v was yours then)
			}
			l.Suspect = true
			d.persist(d.store.PutRecord("complink", uid, l))
			d.dropLink(uid, nil)
			d.audit("computer", "suspect", uid, l.Record.GetName()+": introduced through "+d.nameOf(x)+" after it was unlinked; check its code to keep it")
			d.holdForCode(uid, "")
			queue = append(queue, hold{uid, 0}) // (all it introduced: it never was yours)
		}
	}
}

// localChain is an introduction's chain on this computer's clock.
func localChain(in []*pb.IntroHop) []*pb.IntroHop {
	now := time.Now().UnixMilli()
	out := make([]*pb.IntroHop, 0, len(in))
	for _, h := range in {
		out = append(out, &pb.IntroHop{By: h.By, AtUnixMs: now - max(h.AgoMs, 0)})
	}
	return out
}

// introChain is a link's chain, introduced by via (now) with via's chain.
func introChain(via string, rest []*pb.IntroHop) []*pb.IntroHop {
	return append([]*pb.IntroHop{{By: via, AtUnixMs: time.Now().UnixMilli()}}, rest...)
}

// recordSuspect keeps uid as held for its code here (said by by, "" this
// one): an introduction of it isn't taken. Callers hold d.mu.
func (d *Daemon) recordSuspect(uid, by string) bool {
	if d.comp.suspects[uid] != nil {
		return false
	}
	r := &pb.Refusal{UserId: uid, Name: d.comp.links[uid].GetRecord().GetName(), By: by, UnixMs: time.Now().UnixMilli()}
	d.comp.suspects[uid] = r
	d.persist(d.store.PutRecord("compsuspect", uid, r))
	delete(d.comp.intros, uid)
	return true
}

// holdForCode keeps uid held for its code here and tells the computers
// this one is linked with (but by, who said so), once. Callers hold d.mu.
func (d *Daemon) holdForCode(uid, by string) {
	if !d.recordSuspect(uid, by) {
		return
	}
	for to := range d.comp.links {
		if to != by && to != uid && !d.suspect(to) {
			d.queueNotice(uid, to)
		}
	}
	d.deliverUnlinks()
}

// takeSuspect is a notice from linked computer from that uid waits for
// its code there. It's only a pointer to recheck (ADR-0013): this computer
// decides from what it holds itself. If it has its own link with uid, that
// link's chain decides: tainted here, it's held (and passed on, as any link
// this computer holds); clean, it vouches for uid, and nothing changes here
// (nor is it passed on: the computers the sender is linked with have it).
// With no link of its own, nothing here vouches for uid: it isn't linked by
// an introduction from now on, the links here through it are held, and the
// notice is passed on. Callers hold d.mu.
func (d *Daemon) takeSuspect(uid, from string) error {
	if d.comp.suspects[uid] != nil || uid == d.id.me.UserId {
		return nil
	}
	if l := d.comp.links[uid]; l != nil && !l.Suspect {
		if l.Via == "" || !d.badChain(hopsOf(l)) {
			return nil // vouched for by this one's own clean link
		}
		l.Suspect = true
		d.persist(d.store.PutRecord("complink", uid, l))
		d.dropLink(uid, nil)
		d.audit("computer", "suspect", uid, l.Record.GetName()+": introduced through a computer since unlinked; check its code to keep it")
		d.holdForCode(uid, from)
		d.suspectIntroducedBy(uid, 0) // (it never was yours: all it introduced)
		d.publishComputers()
		return nil
	}
	n := 0
	for _, r := range d.comp.suspects {
		if r.By == from {
			n++
		}
	}
	if n >= maxDenied {
		return errors.New("too many computers held for their code")
	}
	d.holdForCode(uid, from)
	d.suspectIntroducedBy(uid, 0) // (nothing here vouches for it: what it introduced is held)
	d.publishComputers()
	return nil
}

// reachOf is how to reach computer to with a request: its link here, or
// the one it had when it was refused. Callers hold d.mu.
func (d *Daemon) reachOf(to string) (*pb.Computer, string) {
	l := d.comp.links[to]
	if l == nil {
		l = d.comp.refusals[to].GetLink()
	}
	if l == nil {
		return nil, ""
	}
	rec := proto.CloneOf(l.Record)
	if l.ReachedAt != "" && !slices.Contains(rec.Addresses, l.ReachedAt) {
		rec.Addresses = append([]string{l.ReachedAt}, rec.Addresses...)
	}
	return rec, l.CertSha256
}

// queueUnlink keeps a request that computer to unlink user (this one's own
// id: drop link linkID with this one), until it's delivered. Callers hold d.mu.
func (d *Daemon) queueUnlink(user, to, origin string, asked int64, linkID string) {
	rec, cert := d.reachOf(to)
	if rec == nil || cert == "" {
		return
	}
	d.putPending(&pb.PendingUnlink{UserId: user, To: to, ToRecord: rec, ToCert: cert, QueuedUnixMs: time.Now().UnixMilli(),
		LinkId: linkID, Origin: origin, AskedUnixMs: asked})
}

// queueNotice keeps a notice to computer to that uid waits for its code.
// Callers hold d.mu.
func (d *Daemon) queueNotice(uid, to string) {
	rec, cert := d.reachOf(to)
	if rec == nil || cert == "" {
		return
	}
	d.putPending(&pb.PendingUnlink{UserId: uid, To: to, ToRecord: rec, ToCert: cert, QueuedUnixMs: time.Now().UnixMilli(), Suspect: true})
}

func (d *Daemon) putPending(p *pb.PendingUnlink) {
	k := p.To + "|" + p.UserId
	if p.Suspect {
		k += "|held"
	}
	d.comp.pending[k] = p
	d.persist(d.store.PutRecord("compunlinkreq", k, p))
}

func (d *Daemon) dropPending(k string) {
	delete(d.comp.pending, k)
	d.persist(d.store.DeleteRecord("compunlinkreq", k))
}

// deliverUnlinks sends the requests waiting (now, and again until each is
// delivered). Callers hold d.mu.
func (d *Daemon) deliverUnlinks() {
	if len(d.comp.pending) == 0 {
		return
	}
	if d.comp.delivering {
		select {
		case d.comp.kick <- struct{}{}: // try again now (one it's for connected)
		default:
		}
		return
	}
	d.comp.delivering = true
	go d.runDeliveries()
}

// unlinksFor: requests are waiting for computer uid. Callers hold d.mu.
func (d *Daemon) unlinksFor(uid string) bool {
	for _, p := range d.comp.pending {
		if p.To == uid {
			return true
		}
	}
	return false
}

// runDeliveries sends the requests waiting until each is delivered: kept
// however long its computer is off, and dropped only when it is (here).
func (d *Daemon) runDeliveries() {
	wait := time.Second
	for {
		d.mu.Lock()
		var todo []string
		for k := range d.comp.pending {
			todo = append(todo, k)
		}
		if len(todo) == 0 {
			d.comp.delivering = false
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
		// Each on its own (one that's off doesn't hold up the rest).
		var wg sync.WaitGroup
		for _, k := range todo {
			d.mu.Lock()
			p := d.comp.pending[k]
			d.mu.Unlock()
			if p == nil {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := d.deliverUnlink(p)
				d.mu.Lock()
				defer d.mu.Unlock()
				if d.comp.pending[k] != p {
					return
				}
				to := cmpOr(p.ToRecord.GetName(), "another computer")
				switch {
				case err == nil:
					d.dropPending(k)
					if r.GetUnlinkTaken().GetNotLinked() && p.UserId != d.id.me.UserId && !p.Suspect {
						d.addNote(to + " unlinked " + d.nameOf(p.UserId) + " as you asked, but doesn't link with this computer (another of your computers asked it not to): link them again.")
					}
				case err.Error() == errNotLinkedHere.Error() && p.UserId != d.id.me.UserId && !p.Suspect:
					// Kept: linking them again sends it.
					d.addNote(to + " didn't take your request to unlink " + d.nameOf(p.UserId) + ": it doesn't link with this computer. Link them again, and it's sent.")
				}
			}()
		}
		wg.Wait()
		select {
		case <-d.done:
			return
		case <-d.comp.kick:
			wait = time.Second
		case <-time.After(wait):
			wait = min(wait*2, 10*time.Minute)
		}
	}
}

// deliverUnlink sends one request: over the link to its computer if it's
// up, else by dialling it (its link may be gone: this one left, or it was
// refused here). Off d.mu.
func (d *Daemon) deliverUnlink(p *pb.PendingUnlink) (*pb.ServerMessage, error) {
	req := &pb.UnlinkRequest{UserId: p.UserId, LinkId: p.LinkId, Origin: p.Origin, Suspect: p.Suspect}
	if p.AskedUnixMs > 0 {
		req.AgeMs = max(time.Now().UnixMilli()-p.AskedUnixMs, 0)
	}
	msg := &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkRequest{UnlinkRequest: req}}
	d.mu.Lock()
	l := d.linkOf(p.To)
	up := l != nil && l.online
	d.mu.Unlock()
	if up {
		return l.call(msg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cl, _, err, _ := raceDial(ctx, worthTrying(p.ToRecord.GetAddresses(), p.ToRecord.GetLanSubnets()), 5*time.Second, func(ctx context.Context, a string) (*client.Client, error) {
		return client.DialRemote(ctx, a, p.ToCert, "peer", "unlink")
	})
	if err != nil {
		return nil, err
	}
	defer cl.Close()
	if _, _, err := d.peerSignIn(ctx, cl, p.ToCert, p.To); err != nil {
		return nil, err
	}
	return cl.Call(ctx, msg)
}

// takeUnlinkRequest is UnlinkRequest from c's computer: it's leaving (its
// own id: link link_id dropped, not refused), says a key waits for its
// code (suspect), or asks this one to unlink another (refused here for
// good, linked here or not, and passed on). Taken from a computer linked
// with this one, a few a minute; never from one refused here.
func (d *Daemon) takeUnlinkRequest(c *conn, m *pb.UnlinkRequest) (*pb.ServerMessage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	uid, from, me := m.GetUserId(), c.peer, d.id.me.UserId
	taken := func() *pb.ServerMessage {
		return &pb.ServerMessage{Msg: &pb.ServerMessage_UnlinkTaken{UnlinkTaken: &pb.UnlinkTaken{NotLinked: !d.isLinked(from)}}}
	}
	if _, ok := ignoresUnlinks.Load(d); ok {
		return taken(), nil
	}
	if uid == from {
		// Leaving: the link it names goes (a late copy for an older link,
		// overtaken by a newer one, is moot).
		l := d.comp.links[from]
		if l == nil || l.LinkId != m.LinkId {
			return taken(), nil
		}
		name := l.Record.GetName()
		d.dropComputer(from, false, c)
		c.hangUp.Store(true) // once it's answered
		d.audit("computer", "left", uid, name+" left your other computers")
		d.publishComputers()
		return taken(), nil
	}
	if l := d.comp.links[from]; l == nil || l.Suspect {
		// Never from a computer refused here. If it was refused because
		// uid asked, and asks back, say so: the person decides.
		if r := d.comp.refusals[from]; r != nil && r.By == uid && uid != "" && !m.Suspect {
			by, it := d.nameOf(uid), d.nameOf(from)
			d.addNote(by + " asked this computer to unlink " + it + ", and " + it + " has since asked to unlink " + by + ". If " + by + " is the one you lost, link " + it + " again here.")
		}
		return nil, errNotLinkedHere
	}
	if !d.allowAsk(from) {
		return nil, errTooManyAsks
	}
	if uid == "" || uid == me {
		return taken(), nil
	}
	if m.Suspect {
		return taken(), d.takeSuspect(uid, from)
	}
	if m.Origin == me {
		return taken(), nil // this one's own unlink, passed back to it
	}
	if d.comp.links[uid] == nil && !d.comp.denied[uid] && d.refusedAt(from) >= maxDenied {
		return nil, errors.New("too many unlinked computers")
	}
	// When a person asked is its word: from the computer where they asked,
	// never earlier than it was last out of touch here; passed on, never
	// earlier than that computer was linked here.
	origin := cmpOr(m.Origin, from)
	asked := time.Now().UnixMilli() - max(m.AgeMs, 0)
	if origin == from {
		asked = max(asked, d.touchBound(from))
	} else {
		// (Not when the computer passing it on was linked here: it may have
		// been linked since, and links made in between would go unmarked.)
		asked = max(asked, d.linkedSince(origin))
	}
	name, fromName := d.nameOf(uid), d.nameOf(from)
	d.unlinkHere(uid, from, origin, asked, true)
	d.audit("computer", "unlinked", uid, name+", as "+fromName+" asked")
	d.publishComputers()
	return taken(), nil
}
