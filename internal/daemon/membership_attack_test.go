package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/state"
)

// Membership against a computer that's been unlinked (lost, stolen) or was
// never yours, and upgrades from earlier builds' stores: through real
// daemons and their own protocol.

// strangerTo connects to tc's remote listener as computer s (signed in with
// s's key), as any computer on the tailnet could.
func strangerTo(t *testing.T, s, tc *testComputer) *client.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cl, fp, err := client.DialRemoteLearning(ctx, tc.addr, "peer", "stranger")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	if _, _, err := s.d.peerSignIn(ctx, cl, fp, tc.uid); err != nil {
		t.Fatal(err)
	}
	return cl
}

func send(t *testing.T, cl *client.Client, m *pb.ClientMessage) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := cl.Call(ctx, m)
	return err
}

func recordOf(tc *testComputer) *pb.Computer {
	tc.d.mu.Lock()
	defer tc.d.mu.Unlock()
	tc.d.refreshSelf(false)
	return tc.d.selfRecord(true)
}

// unlinkedX links a and x, then a unlinks x (from all): x is out.
func unlinkedX(t *testing.T) (a, x *testComputer) {
	t.Helper()
	a, x = startComputer(t), startComputer(t)
	link(t, a, x)
	waitComputers(t, a, "linked", linkedWith(x.uid))
	unlink(t, a, x.uid)
	waitComputers(t, a, "x out", alone)
	waitComputers(t, x, "x knows", alone)
	return a, x
}

// Round 6 #1: a key the unlinked computer controls, relayed in. X links a
// key of its own (K, a person's code check between two of X's), then
// introduces K to A, and K asks A to link because X introduced it: refused.
func TestMembershipMintedKeyNotRelayedIn(t *testing.T) {
	a, x := unlinkedX(t)
	k := startComputer(t)
	link(t, x, k)
	waitComputers(t, x, "x links k", linkedWith(k.uid))
	cl := strangerTo(t, x, a)
	kr := recordOf(k)
	if err := send(t, cl, &pb.ClientMessage{Msg: &pb.ClientMessage_IntroduceComputers{IntroduceComputers: &pb.IntroduceComputers{Computers: []*pb.Computer{kr}}}}); err == nil {
		t.Fatal("a took introductions from a computer it unlinked")
	}
	kc := strangerTo(t, k, a)
	if err := send(t, kc, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerIntroLink{PeerIntroLink: &pb.PeerIntroLink{Via: x.uid, Computer: kr}}}); err == nil {
		t.Fatal("a linked a key introduced by a computer it unlinked")
	}
	holdComputers(t, a, "a stays alone", hold, alone)
	a.d.mu.Lock()
	n := len(a.d.comp.intros)
	a.d.mu.Unlock()
	if n != 0 {
		t.Fatalf("a kept %d introductions from strangers", n)
	}
}

// Round 6 #2: a key that learns of new links and vetoes them. A unlinks X,
// then links C. X, connecting, learns nothing of A's computers, and its
// request to unlink C is ignored.
func TestMembershipUnlinkedCantLearnOrVeto(t *testing.T) {
	a, x := unlinkedX(t)
	c := startComputer(t)
	link(t, a, c)
	waitComputers(t, a, "a links c", linkedWith(c.uid))
	cl := strangerTo(t, x, a)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	if err != nil {
		t.Fatal(err)
	}
	cs := r.GetState().GetComputers()
	if !cs.GetNotLinked() || len(cs.GetList()) > 0 {
		t.Fatalf("an unlinked computer was told %v", cs)
	}
	if err := send(t, cl, &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkRequest{UnlinkRequest: &pb.UnlinkRequest{UserId: c.uid}}}); err == nil {
		t.Fatal("a took an unlink request from a computer it unlinked")
	}
	holdComputers(t, a, "a keeps c", hold, linkedWith(c.uid))
	holdComputers(t, c, "c keeps a", hold, linkedWith(a.uid))
}

// Round 6 #3: blocking its own unlink. X, unlinked by A, asks to link with
// C claiming A introduced it, and "leaves" (to clear its refusal): A and C
// still refuse it.
func TestMembershipUnlinkedCantUndoItsUnlink(t *testing.T) {
	a, x, c := startComputer(t), startComputer(t), startComputer(t)
	link(t, a, x)
	link(t, c, a)
	waitComputers(t, c, "c with a and x", linkedWith(a.uid, x.uid))
	unlink(t, a, x.uid)
	waitComputers(t, c, "c drops x", linkedWith(a.uid))
	xr := recordOf(x)
	for _, to := range []*testComputer{a, c} {
		cl := strangerTo(t, x, to)
		_ = send(t, cl, &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkRequest{UnlinkRequest: &pb.UnlinkRequest{UserId: x.uid}}})
		if err := send(t, cl, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerIntroLink{PeerIntroLink: &pb.PeerIntroLink{Via: a.uid, Computer: xr}}}); err == nil {
			t.Fatal("an unlinked computer linked by claiming an introduction")
		}
	}
	for _, tc := range []*testComputer{a, c} {
		tc.d.mu.Lock()
		denied := tc.d.comp.denied[x.uid]
		tc.d.mu.Unlock()
		if !denied {
			t.Fatalf("%s no longer refuses x", tc.uid)
		}
		holdComputers(t, tc, "stays without x", hold, notWith(x.uid))
	}
}

// Round 6 #4: flooding with junk unlinks. A stranger's are all refused and
// none kept; a linked computer's are taken a few a minute.
func TestMembershipUnlinkFloodLimited(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, a, "linked", linkedWith(b.uid))
	s := startComputer(t)
	junk := func(i int) *pb.ClientMessage {
		_, k, _ := ed25519.GenerateKey(nil)
		return &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkRequest{UnlinkRequest: &pb.UnlinkRequest{UserId: userIDFor(k.Public().(ed25519.PublicKey))}}}
	}
	sc := strangerTo(t, s, a)
	for i := range 200 {
		if send(t, sc, junk(i)) == nil {
			t.Fatal("a took an unlink request from a stranger")
		}
	}
	bc := strangerTo(t, b, a) // b's key: linked with a
	taken := 0
	for i := range 200 {
		if send(t, bc, junk(i)) == nil {
			taken++
		}
	}
	a.d.mu.Lock()
	denied := len(a.d.comp.denied)
	a.d.mu.Unlock()
	if taken > asksPerMinute || denied > asksPerMinute {
		t.Fatalf("took %d of a linked computer's 200 junk requests (%d kept): want at most %d", taken, denied, asksPerMinute)
	}
	holdComputers(t, a, "a keeps b", hold, linkedWith(b.uid))
}

// A link that finished on one side only (the asking computer never heard
// the answer) resolves: the other shows it doesn't link with it any more,
// and never as linked and online.
func TestMembershipOneSidedResolves(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cl, fp, err := client.DialRemoteLearning(ctx, b.addr, "peer", "asker")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.d.peerSignIn(ctx, cl, fp, b.uid); err != nil {
		t.Fatal(err)
	}
	self := recordOf(a)
	nonce := make([]byte, 32)
	commit := sha256.Sum256(nonce)
	if _, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerLinkHello{PeerLinkHello: &pb.PeerLinkHello{Commitment: commit[:], Computer: self}}}); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerLinkReveal{PeerLinkReveal: &pb.PeerLinkReveal{Nonce: nonce}}})
	}()
	rb := linkReq(t, b, "")
	_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerLinkConfirm{PeerLinkConfirm: &pb.PeerLinkConfirm{Accept: true}}})
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: rb.Id, Accept: true}}})
	waitComputers(t, b, "b has the link", func(cs *pb.Computers) bool { return hasComputer(a.uid, false)(cs) })
	cl.Close() // a never hears it
	waitComputers(t, b, "b shows a as not linking with it", refused(a.uid))
	holdComputers(t, b, "and never as linked", hold, notWith(a.uid))
	holdComputers(t, a, "a alone", hold, alone)
}

// ---------- upgrades ----------

// withStore writes into a stopped computer's store.
func withStore(t *testing.T, tc *testComputer, f func(*state.Store)) {
	t.Helper()
	st, err := state.Open(filepath.Join(tc.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	f(st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func legacyOf(tc *testComputer, via string) *pb.LegacyComputer {
	r := recordOf(tc)
	return &pb.LegacyComputer{UserId: tc.uid, Name: r.Name, Addresses: r.Addresses, CertSha256: r.CertSha256, Via: via, LinkedUnixMs: 1}
}

// From the record format (f191de3–443bf28): computers linked directly stay
// linked; an introduced one is dropped (named, to link again: here it comes
// back through the others); a tombstone is refused; the old records go.
func TestMigrateFromRecords(t *testing.T) {
	a, b, c := startComputer(t), startComputer(t), startComputer(t)
	la, lb, lc := legacyOf(a, ""), legacyOf(b, ""), legacyOf(c, "")
	_, gone, _ := ed25519.GenerateKey(nil)
	goneID := userIDFor(gone.Public().(ed25519.PublicKey))
	for _, tc := range []*testComputer{a, b, c} {
		stop(tc)
	}
	put := func(st *state.Store, r *pb.LegacyComputer) { st.PutRecord("computer", r.UserId, r) }
	withStore(t, a, func(st *state.Store) {
		put(st, lb)
		lcVia := legacyOf(c, b.uid) // introduced by b
		lcVia.Addresses, lcVia.CertSha256 = lc.Addresses, lc.CertSha256
		put(st, lcVia)
		put(st, &pb.LegacyComputer{UserId: goneID, Name: "Gone", Unlinked: true})
		st.SetSetting("computers.out", "12345")
	})
	withStore(t, b, func(st *state.Store) { put(st, la); put(st, lc) })
	withStore(t, c, func(st *state.Store) { put(st, lb) })
	a, b, c = restartComputer(t, a), restartComputer(t, b), restartComputer(t, c)
	waitComputers(t, a, "a with b, and c again (through b)", linkedWith(b.uid, c.uid))
	holdComputers(t, a, "a stays with b and c", hold, linkedWith(b.uid, c.uid))
	holdComputers(t, c, "c with a and b", hold, linkedWith(a.uid, b.uid))
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	if !a.d.comp.denied[goneID] {
		t.Fatal("an unlinked computer isn't refused after the upgrade")
	}
	if !strings.Contains(a.d.comp.note, lc.Name) {
		t.Fatalf("no note to link the introduced computer again: %q", a.d.comp.note)
	}
	if recs, _ := a.d.store.Records("computer"); len(recs) != 0 || a.d.store.Setting("computers.out") != "" {
		t.Fatal("old records or settings left")
	}
}

// From the signed-facts format (f925107–4f250ef): a link of this
// computer's that no unlink names stays a direct link; one between two
// others is dropped (and comes back through an introduction); an unlink of
// the other end refuses it; the old facts go.
func TestMigrateFromFacts(t *testing.T) {
	a, b, c, x := startComputer(t), startComputer(t), startComputer(t), startComputer(t)
	keyOf := func(tc *testComputer) []byte {
		tc.d.mu.Lock()
		defer tc.d.mu.Unlock()
		return tc.d.id.key.Public().(ed25519.PublicKey)
	}
	ab := &pb.LegacyLink{Id: "ln_ab", AKey: keyOf(a), BKey: keyOf(b)}
	bc := &pb.LegacyLink{Id: "ln_bc", AKey: keyOf(b), BKey: keyOf(c)}
	ax := &pb.LegacyLink{Id: "ln_ax", AKey: keyOf(a), BKey: keyOf(x)}
	unx := &pb.LegacyUnlink{Id: "ul_x", UserId: x.uid, LinkIds: []string{"ln_ax"}}
	la, lb, lc, lx := legacyOf(a, ""), legacyOf(b, ""), legacyOf(c, ""), legacyOf(x, "")
	for _, tc := range []*testComputer{a, b, c, x} {
		stop(tc)
	}
	facts := func(st *state.Store, links []*pb.LegacyLink, unlinks []*pb.LegacyUnlink, recs ...*pb.LegacyComputer) {
		for _, l := range links {
			st.PutRecord("memlink", l.Id, l)
		}
		for _, u := range unlinks {
			st.PutRecord("memunlink", u.Id, u)
		}
		for _, r := range recs {
			st.PutRecord("computer", r.UserId, r)
		}
	}
	withStore(t, a, func(st *state.Store) {
		facts(st, []*pb.LegacyLink{ab, bc, ax}, []*pb.LegacyUnlink{unx}, lb, lc, lx)
	})
	withStore(t, b, func(st *state.Store) { facts(st, []*pb.LegacyLink{ab, bc}, nil, la, lc) })
	withStore(t, c, func(st *state.Store) { facts(st, []*pb.LegacyLink{ab, bc}, nil, la, lb) })
	a, b, c = restartComputer(t, a), restartComputer(t, b), restartComputer(t, c)
	waitComputers(t, a, "a with b, and c (introduced by b)", linkedWith(b.uid, c.uid))
	holdComputers(t, a, "a stays with b and c, not x", hold, linkedWith(b.uid, c.uid))
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	if !a.d.comp.denied[x.uid] {
		t.Fatal("x, unlinked before the upgrade, isn't refused")
	}
	for _, kind := range []string{"memlink", "memunlink", "computer"} {
		if recs, _ := a.d.store.Records(kind); len(recs) != 0 {
			t.Fatalf("old %s records left", kind)
		}
	}
}

// An introduction counts only while its introducer is still linked: C is
// offered K by A, then C unlinks A; K's link because "A introduced us" is
// refused. (And, as a check, the same while A is linked is taken.)
func TestMembershipIntroducerUnlinkedFirst(t *testing.T) {
	a, c := startComputer(t), startComputer(t)
	link(t, a, c)
	waitComputers(t, c, "linked", linkedWith(a.uid))
	offer := func(k *testComputer) {
		cl := strangerTo(t, a, c) // over A's key: linked with C
		if err := send(t, cl, &pb.ClientMessage{Msg: &pb.ClientMessage_IntroduceComputers{IntroduceComputers: &pb.IntroduceComputers{Computers: []*pb.Computer{recordOf(k)}}}}); err != nil {
			t.Fatal(err)
		}
	}
	ask := func(k *testComputer) error {
		return send(t, strangerTo(t, k, c), &pb.ClientMessage{Msg: &pb.ClientMessage_PeerIntroLink{PeerIntroLink: &pb.PeerIntroLink{Via: a.uid, Computer: recordOf(k)}}})
	}
	// While A is linked: taken.
	k1 := startComputer(t)
	offer(k1)
	if err := ask(k1); err != nil {
		t.Fatalf("an introduction by a linked computer was refused: %v", err)
	}
	waitComputers(t, c, "c links k1", func(cs *pb.Computers) bool { return hasComputer(k1.uid, false)(cs) })
	// Offered, then A unlinked: refused.
	k2 := startComputer(t)
	offer(k2)
	unlink(t, c, a.uid)
	if err := ask(k2); err == nil {
		t.Fatal("linked because of an introduction from a computer since unlinked")
	}
	holdComputers(t, c, "c never links k2", hold, func(cs *pb.Computers) bool { return !hasComputer(k2.uid, false)(cs) })
}
