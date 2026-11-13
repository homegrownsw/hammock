package daemon

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/state"
)

// Unlinks that reach every computer and can't be blocked, links made by a
// computer since unlinked, and what a person is told: through real daemons
// and clients. Fields newer than the build under test are read and set by
// name, so these also run (and fail) against earlier builds.

func intField(m proto.Message, name string) int64 {
	f := m.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
	if f == nil {
		return 0
	}
	return m.ProtoReflect().Get(f).Int()
}

func setBoolField(m proto.Message, name string, v bool) {
	if f := m.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name)); f != nil {
		m.ProtoReflect().Set(f, protoreflect.ValueOfBool(v))
	}
}

func computerIn(cs *pb.Computers, uid string) *pb.Computer {
	for _, c := range cs.List {
		if c.UserId == uid {
			return c
		}
	}
	return nil
}

func refuses(tc *testComputer, uid string) bool {
	tc.d.mu.Lock()
	defer tc.d.mu.Unlock()
	return tc.d.comp.denied[uid]
}

func hasNote(cs *pb.Computers) bool { return cs.Note != "" }

func shown(uid string) func(*pb.Computers) bool { return hasComputer(uid, false) }

func notShown(uid string) func(*pb.Computers) bool {
	return func(cs *pb.Computers) bool { return !hasComputer(uid, false)(cs) }
}

func pendingCount(tc *testComputer) int {
	tc.d.mu.Lock()
	defer tc.d.mu.Unlock()
	return len(tc.d.comp.pending)
}

// #1: an unlink reaches computers linked after it was asked. A, B and S
// are linked, B off; A unlinks S and goes off; B, back (still linked with
// S), links E, which gets S through B. When A is back, B drops S, and so
// does E.
func TestMembershipUnlinkReachesLaterLinks(t *testing.T) {
	t.Parallel()
	a, b, s := mesh(t)
	stop(b)
	waitComputers(t, a, "b off", offline(b.uid))
	unlink(t, a, s.uid)
	waitComputers(t, a, "a without s", notWith(s.uid))
	stop(a)
	b = restartComputer(t, b)
	waitComputers(t, b, "b still with s", linkedWith(s.uid))
	e := startComputer(t)
	link(t, b, e)
	waitComputers(t, e, "e gets s through b", linkedWith(b.uid, s.uid))
	a = restartComputer(t, a)
	waitComputers(t, b, "b drops s", notWith(s.uid))
	waitComputers(t, e, "e drops s too", notWith(s.uid))
	holdComputers(t, e, "e stays without s", hold, notWith(s.uid))
	if !refuses(e, s.uid) {
		t.Fatal("e doesn't refuse s")
	}
}

// #2: a stolen computer, still linked, can't block its own unlink. A, B
// and C are linked; C (stolen) asks B to unlink A; then a person at A
// unlinks C. A says B didn't take it; once a person links A and B again,
// B drops C. (A and B apart: TestMembershipAskBackOnceAndOnlyInTheRace.)
func TestMembershipStolenCantBlockItsUnlink(t *testing.T) {
	t.Parallel()
	a, b, c := mesh(t)
	if err := send(t, strangerTo(t, c, b), &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkRequest{UnlinkRequest: &pb.UnlinkRequest{UserId: a.uid}}}); err != nil {
		t.Fatal(err)
	}
	waitComputers(t, b, "b drops a, as c asked", notWith(a.uid))
	unlink(t, a, c.uid)
	// (B takes nothing from A once it refuses it, ADR-0013: A's request
	// waits for A and B to be linked again.)
	waitComputers(t, a, "a says b didn't take its unlink", hasNote)
	waitComputers(t, a, "a shows b not linking with it", refused(b.uid))
	link(t, a, b)
	waitComputers(t, b, "b drops c, once linked with a again", notWith(c.uid))
	holdComputers(t, b, "b never keeps c", hold, notWith(c.uid))
	if !refuses(b, c.uid) {
		t.Fatal("b doesn't refuse c")
	}
}

// #3: unlink requests are kept until delivered, however long that takes:
// one a month old, for a computer that's off, still reaches it.
func TestMembershipUnlinkKeptUntilDelivered(t *testing.T) {
	t.Parallel()
	a, b, c := mesh(t)
	stop(c)
	waitComputers(t, a, "c off", offline(c.uid))
	unlink(t, a, b.uid)
	waitComputers(t, a, "a without b", notWith(b.uid))
	stop(a)
	month := time.Now().Add(-31 * 24 * time.Hour).UnixMilli()
	n := 0
	withStore(t, a, func(st *state.Store) {
		recs, _ := st.Records("compunlinkreq")
		for k, raw := range recs {
			p := &pb.PendingUnlink{}
			if proto.Unmarshal(raw, p) == nil {
				p.QueuedUnixMs = month
				st.PutRecord("compunlinkreq", k, p)
				n++
			}
		}
	})
	if n == 0 {
		t.Fatal("no request waiting for c")
	}
	a = restartComputer(t, a)
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if pendingCount(a) == 0 {
			t.Fatal("a gave up on its request to c")
		}
	}
	c = restartComputer(t, c)
	// Delivered first: until then c may show b offline (c has just restarted, and b redials it), which
	// notWith takes for dropped, and b back a moment later.
	for end := time.Now().Add(15 * time.Second); pendingCount(a) > 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("a's request never reached c")
		}
	}
	waitComputers(t, c, "c drops b", notWith(b.uid))
	holdComputers(t, c, "and stays so", hold, notWith(b.uid))
}

// #4: a stolen computer's own key isn't passed on. A unlinks S while B is
// off; S links a key of its own (K); B, back, takes S's introduction of K;
// when A is back B drops S, and K with it, and A never links K.
func TestMembershipLinksOfUnlinkedNotPassedOn(t *testing.T) {
	t.Parallel()
	a, b, s := mesh(t)
	stop(b)
	waitComputers(t, a, "b off", offline(b.uid))
	unlink(t, a, s.uid)
	waitComputers(t, a, "a without s", notWith(s.uid))
	stop(a)
	k := startComputer(t)
	link(t, s, k)
	b = restartComputer(t, b)
	waitComputers(t, b, "b takes k from s", func(cs *pb.Computers) bool { return online(cs)[k.uid] })
	a = restartComputer(t, a)
	waitComputers(t, b, "b drops s", notWith(s.uid))
	waitComputers(t, b, "and k, which s introduced", notWith(k.uid))
	holdComputers(t, b, "b keeps them out", hold, notWith(s.uid, k.uid))
	holdComputers(t, a, "a never links k", hold, notWith(k.uid))
	b.d.mu.Lock()
	kc := computerIn(b.d.computersState(false), k.uid)
	b.d.mu.Unlock()
	if kc != nil && !boolField(kc, "suspect") {
		t.Fatalf("b shows k as %v, not as needing its code checked", kc)
	}
}

// #5: an unlinked computer asking for anything says it was unlinked: a
// link request from it, and a request to join a shared Group.
func TestMembershipUnlinkedSaysSo(t *testing.T) {
	t.Parallel()
	a, x := unlinkedX(t)
	waitComputers(t, x, "x shows a not linking", refused(a.uid))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = x.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_LinkComputer{LinkComputer: &pb.LinkComputer{Address: a.addr}}})
	}()
	rx := linkReq(t, x, "")
	ra := linkReq(t, a, rx.Code)
	if intField(ra, "unlinked_unix_ms") == 0 {
		t.Fatalf("a link request from an unlinked computer doesn't say so: %v", ra)
	}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: ra.Id, Accept: false}}})

	gid := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Shared"}}}))
	inv := call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: gid}}}).GetInvite().Link
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(inv, "hammock://join/"))
	tok := &pb.InviteToken{}
	if err := proto.Unmarshal(raw, tok); err != nil {
		t.Fatal(err)
	}
	cl := strangerTo(t, x, a)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerJoin{PeerJoin: &pb.PeerJoin{Secret: tok.Secret}}})
	}()
	var jr *pb.JoinRequest
	waitEvent(t, a.c, func(ev *pb.ServerMessage) bool {
		for _, r := range ev.GetState().GetJoinRequests() {
			jr = r
			return true
		}
		return false
	})
	if intField(jr, "unlinked_unix_ms") == 0 {
		t.Fatalf("a join request from an unlinked computer doesn't say so: %v", jr)
	}
}

// #6: a computer whose certificate changed (its tls/ made again, same
// key) shows so, and linking again (the code check) replaces the pin.
func TestMembershipChangedCertRelinks(t *testing.T) {
	t.Parallel()
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, a, "linked", linkedWith(b.uid))
	stop(b)
	if err := os.RemoveAll(filepath.Join(b.dir, "tls")); err != nil {
		t.Fatal(err)
	}
	b = restartComputer(t, b)
	waitComputers(t, a, "a shows b's certificate changed", func(cs *pb.Computers) bool {
		c := computerIn(cs, b.uid)
		return c != nil && !c.Online && boolField(c, "cert_changed")
	})
	link(t, a, b)
	waitComputers(t, a, "linked again", linkedWith(b.uid))
	holdComputers(t, a, "a with b", hold, linkedWith(b.uid))
	holdComputers(t, b, "b with a", hold, linkedWith(a.uid))
}

// #7: upgrading from signed facts, a computer unlinked once and linked
// again by a person since is linked (the newer link decides).
func TestMigrateFromFactsRelinked(t *testing.T) {
	a, x := startComputer(t), startComputer(t)
	keyOf := func(tc *testComputer) []byte {
		tc.d.mu.Lock()
		defer tc.d.mu.Unlock()
		return tc.d.id.key.Public().(ed25519.PublicKey)
	}
	old := &pb.LegacyLink{Id: "ln_ax1", AKey: keyOf(a), BKey: keyOf(x)}
	again := &pb.LegacyLink{Id: "ln_ax2", AKey: keyOf(a), BKey: keyOf(x)}
	un := &pb.LegacyUnlink{Id: "ul_x", UserId: x.uid, LinkIds: []string{"ln_ax1"}}
	la, lx := legacyOf(a, ""), legacyOf(x, "")
	stop(a)
	stop(x)
	put := func(st *state.Store, r *pb.LegacyComputer) {
		st.PutRecord("memlink", old.Id, old)
		st.PutRecord("memlink", again.Id, again)
		st.PutRecord("memunlink", un.Id, un)
		st.PutRecord("computer", r.UserId, r)
	}
	withStore(t, a, func(st *state.Store) { put(st, lx) })
	withStore(t, x, func(st *state.Store) { put(st, la) })
	a, x = restartComputer(t, a), restartComputer(t, x)
	waitComputers(t, a, "a linked with x", linkedWith(x.uid))
	holdComputers(t, a, "and stays so", hold, linkedWith(x.uid))
	holdComputers(t, x, "x with a", hold, linkedWith(a.uid))
}

// #8: leaving, then linking again at once, works; and a late "drop me"
// from before (resent, overtaken) doesn't undo the new link.
func TestMembershipLeaveThenRelinkAtOnce(t *testing.T) {
	t.Parallel()
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, b, "linked", linkedWith(a.uid))
	unlink(t, a, a.uid)
	waitComputers(t, b, "b drops a", notShown(a.uid))
	link(t, a, b)
	waitComputers(t, a, "linked again", linkedWith(b.uid))
	waitComputers(t, b, "b too", linkedWith(a.uid))
	_ = send(t, strangerTo(t, a, b), &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkRequest{UnlinkRequest: &pb.UnlinkRequest{UserId: a.uid}}})
	holdComputers(t, b, "b keeps the new link", hold, linkedWith(a.uid))
	holdComputers(t, a, "a too", hold, linkedWith(b.uid))
	if n := pendingCount(a); n != 0 {
		t.Fatalf("a still has %d requests waiting (its leave was answered)", n)
	}
}

// #9: an introduction that fails (here: offered while the introducer had
// asked too much of this one lately) is offered again: the two link.
func TestMembershipIntroductionRetried(t *testing.T) {
	t.Parallel()
	p, v := startComputer(t), startComputer(t)
	link(t, p, v)
	waitComputers(t, p, "linked", linkedWith(v.uid))
	cl := strangerTo(t, v, p)
	for range asksPerMinute {
		_ = send(t, cl, junkUnlink())
	}
	q := startComputer(t)
	link(t, q, v)
	waitComputers(t, p, "p and q link in the end", linkedWith(v.uid, q.uid))
	waitComputers(t, q, "q too", linkedWith(v.uid, p.uid))
}

func junkUnlink() *pb.ClientMessage {
	_, k, _ := ed25519.GenerateKey(nil)
	return &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkRequest{UnlinkRequest: &pb.UnlinkRequest{UserId: userIDFor(k.Public().(ed25519.PublicKey))}}}
}

// #10: limits are per computer. One that fills this one's introductions
// with junk doesn't crowd out another's; and a list of refused keys full
// (of old ones) doesn't stop an honest unlink.
func TestMembershipLimitsPerSender(t *testing.T) {
	t.Parallel()
	p, v := startComputer(t), startComputer(t)
	link(t, p, v)
	waitComputers(t, p, "linked", linkedWith(v.uid))
	var junk []*pb.Computer
	for range maxIntros {
		pub, _, _ := ed25519.GenerateKey(nil)
		uid := userIDFor(pub)
		junk = append(junk, &pb.Computer{UserId: uid, Key: pub, Name: "Junk", CertSha256: strings.Repeat("00", 32), Addresses: []string{"127.0.0.1:1"}})
	}
	if err := send(t, strangerTo(t, v, p), &pb.ClientMessage{Msg: &pb.ClientMessage_IntroduceComputers{IntroduceComputers: &pb.IntroduceComputers{Computers: junk}}}); err != nil {
		t.Fatal(err)
	}
	w, q := startComputer(t), startComputer(t)
	link(t, w, q)
	link(t, p, w)
	waitComputers(t, p, "p links q, through w", linkedWith(v.uid, w.uid, q.uid))

	// The refused list full: b's unlink of x still reaches a.
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, a, "a, b linked", linkedWith(b.uid))
	stop(a)
	withStore(t, a, func(st *state.Store) {
		for range maxDenied {
			pub, _, _ := ed25519.GenerateKey(nil)
			st.PutRecord("compdeny", userIDFor(pub), &pb.Computer{UserId: userIDFor(pub), Name: "Old"})
		}
	})
	x := startComputer(t)
	link(t, b, x)
	waitComputers(t, b, "b, x linked", linkedWith(x.uid))
	unlink(t, b, x.uid)
	a = restartComputer(t, a)
	for end := time.Now().Add(15 * time.Second); !refuses(a, x.uid); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("a never took b's unlink of x")
		}
	}
}

// #11: dropping a computer drops what was waiting for it: B, unlinked,
// isn't told of A's other unlinks (here, of C) when it's back.
func TestMembershipDroppedHearsNothing(t *testing.T) {
	t.Parallel()
	a, b, c := mesh(t)
	stop(b)
	waitComputers(t, a, "b off", offline(b.uid))
	unlink(t, a, c.uid) // a request to b, waiting
	unlink(t, a, b.uid)
	for _, p := range func() []*pb.PendingUnlink {
		a.d.mu.Lock()
		defer a.d.mu.Unlock()
		var out []*pb.PendingUnlink
		for _, p := range a.d.comp.pending {
			out = append(out, p)
		}
		return out
	}() {
		if p.To == b.uid {
			t.Fatalf("a still means to tell b, which it unlinked: %v", p)
		}
	}
	b = restartComputer(t, b)
	waitComputers(t, b, "b back, with c", linkedWith(c.uid))
	holdComputers(t, b, "b isn't told of c's unlink", hold, linkedWith(c.uid))
}

// #12: remote access listening on every address (":7433"), started only
// when linking, advertises this computer's Tailscale addresses, looked up
// first (never a record with none).
func TestLinkFromWildcardListen(t *testing.T) {
	oldCmds, oldIfs := tailscaleCommands, interfaceAddrs
	tailscaleCommands = nil
	interfaceAddrs = func() []ifAddr { return []ifAddr{{"utun9", net.ParseIP("100.100.1.2"), nil}} }
	t.Cleanup(func() { tailscaleCommands, interfaceAddrs = oldCmds, oldIfs })
	emptyCache := func() {
		tsCache.Lock()
		tsCache.net, tsCache.at = tsNet{}, time.Time{}
		tsCache.Unlock()
	}
	t.Cleanup(emptyCache)
	// Its certificate made earlier (making one looks the addresses up), and
	// b started first (these daemons share the process's cache).
	first, b := startComputer(t), startComputer(t)
	stop(first)
	emptyCache()
	d, err := New(Config{Dir: first.dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		RemoteListen: []string{"0.0.0.0:0"}})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(d.Shutdown)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(first.dir, "run", "hmkd.sock")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
	}
	c := dial(t, first.dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	a := &testComputer{d: d, dir: first.dir, c: c, uid: first.uid}
	link(t, a, b)
	waitComputers(t, b, "b linked with a", linkedWith(a.uid))
	_, port, _ := net.SplitHostPort(d.RemoteAddrs()[0])
	b.d.mu.Lock()
	addrs := b.d.comp.links[a.uid].GetRecord().GetAddresses()
	b.d.mu.Unlock()
	if len(addrs) == 0 || addrs[0] != "100.100.1.2:"+port {
		t.Fatalf("a advertised %v", addrs)
	}
}

// #13: after a restart, a link's grace is over: one whose time was in the
// future (the clock moved back since) is shown as not linking once the
// other says so, not retried for ever.
func TestMembershipGraceAfterRestart(t *testing.T) {
	t.Parallel()
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, a, "linked", linkedWith(b.uid))
	stop(a)
	stop(b)
	withStore(t, a, func(st *state.Store) {
		recs, _ := st.Records("complink")
		l := &pb.StoredLink{}
		if proto.Unmarshal(recs[b.uid], l) != nil {
			t.Fatal("no link to b")
		}
		l.Record.LinkedUnixMs = time.Now().Add(time.Hour).UnixMilli()
		st.PutRecord("complink", b.uid, l)
	})
	for _, f := range []string{"state.db", "state.db-wal", "state.db-shm"} {
		os.Remove(filepath.Join(b.dir, f))
	}
	a, b = restartComputer(t, a), restartComputer(t, b)
	waitComputers(t, a, "a shows b not linking", refused(b.uid))
}

// #14: Remove (on one that doesn't link with this one any more) forgets it
// here only: nobody is asked to refuse it.
func TestMembershipRemoveForgetsHereOnly(t *testing.T) {
	t.Parallel()
	a, b, c := mesh(t)
	stop(b)
	for _, f := range []string{"state.db", "state.db-wal", "state.db-shm"} {
		os.Remove(filepath.Join(b.dir, f))
	}
	b = restartComputer(t, b)
	waitComputers(t, a, "a shows b not linking", refused(b.uid))
	waitComputers(t, c, "c too", refused(b.uid))
	m := &pb.UnlinkComputer{UserId: b.uid}
	setBoolField(m, "forget", true)
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkComputer{UnlinkComputer: m}})
	waitComputers(t, a, "a forgets b", notShown(b.uid))
	holdComputers(t, c, "c still shows b, unasked", hold, refused(b.uid))
	if refuses(a, b.uid) || refuses(c, b.uid) {
		t.Fatal("Remove refused b's key")
	}
	holdComputers(t, a, "a keeps c", hold, linkedWith(c.uid))
}

// #16: an upgrade skips a record whose key isn't its computer's own.
func TestMigrateSkipsMismatchedKey(t *testing.T) {
	a, b, x := startComputer(t), startComputer(t), startComputer(t)
	lb := legacyOf(b, "")
	x.d.mu.Lock()
	lb.Key = x.d.id.key.Public().(ed25519.PublicKey)
	x.d.mu.Unlock()
	stop(a)
	withStore(t, a, func(st *state.Store) { st.PutRecord("computer", lb.UserId, lb) })
	a = restartComputer(t, a)
	a.d.mu.Lock()
	l := a.d.comp.links[b.uid]
	a.d.mu.Unlock()
	if l != nil {
		t.Fatalf("kept a record whose key isn't its own: %v", l)
	}
}

func setStringField(m proto.Message, name, v string) {
	if f := m.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name)); f != nil {
		m.ProtoReflect().Set(f, protoreflect.ValueOfString(v))
	}
}

func setIntField(m proto.Message, name string, v int64) {
	if f := m.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name)); f != nil {
		m.ProtoReflect().Set(f, protoreflect.ValueOfInt64(v))
	}
}
