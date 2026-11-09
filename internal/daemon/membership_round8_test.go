package daemon

import (
	"testing"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// What a computer unlinked (or still linked, but stolen) can still do with
// unlink requests, and links it introduced further down the line: through
// real daemons and clients.

// hostile makes tc ignore unlink requests (and notices that a key is
// suspect), as a stolen computer's daemon could.
func hostile(tc *testComputer) { ignoresUnlinks.Store(tc.d, true) }

func unlinkReq(uid string, ageMs int64, origin string) *pb.ClientMessage {
	r := &pb.UnlinkRequest{UserId: uid}
	setStringField(r, "origin", origin)
	setIntField(r, "age_ms", ageMs)
	return &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkRequest{UnlinkRequest: r}}
}

// four links a, b, c and d (each with the others, through b's introductions).
func four(t *testing.T) (a, b, c, d *testComputer) {
	t.Helper()
	a, b, c, d = startComputer(t), startComputer(t), startComputer(t), startComputer(t)
	link(t, a, b)
	link(t, c, b)
	link(t, d, b)
	for _, x := range []struct {
		tc   *testComputer
		with []string
	}{{a, []string{b.uid, c.uid, d.uid}}, {b, []string{a.uid, c.uid, d.uid}}, {c, []string{a.uid, b.uid, d.uid}}, {d, []string{a.uid, b.uid, c.uid}}} {
		waitComputers(t, x.tc, "all four linked", linkedWith(x.with...))
	}
	return a, b, c, d
}

const tenYears = 10 * 365 * 24 * int64(time.Hour/time.Millisecond)

// #1: once a person at A unlinks C, C (refused) can't get A refused: not
// by asking back on any computer, however old it says its request is, and
// not again after A is linked again.
func TestMembershipUnlinkedCantAskBack(t *testing.T) {
	a, b, c, d := four(t)
	unlink(t, a, c.uid)
	for _, tc := range []*testComputer{b, d} {
		waitComputers(t, tc, "drops c", linkedWith(a.uid, map[*testComputer]string{b: d.uid, d: b.uid}[tc]))
	}
	for _, tc := range []*testComputer{b, d} {
		cl := strangerTo(t, c, tc)
		for _, m := range []*pb.ClientMessage{unlinkReq(a.uid, 0, ""), unlinkReq(a.uid, tenYears, c.uid), unlinkReq(a.uid, 1000, a.uid)} {
			if send(t, cl, m) == nil {
				t.Fatal("a computer unlinked by a person took its request to unlink that one")
			}
		}
	}
	holdComputers(t, a, "a keeps b and d", hold, linkedWith(b.uid, d.uid))
	holdComputers(t, b, "b keeps a and d", hold, linkedWith(a.uid, d.uid))
	holdComputers(t, d, "d keeps a and b", hold, linkedWith(a.uid, b.uid))
	// A computer linked later isn't told to refuse a either.
	e := startComputer(t)
	link(t, e, b)
	waitComputers(t, e, "e with a, b and d", linkedWith(a.uid, b.uid, d.uid))
	holdComputers(t, e, "and stays so", hold, linkedWith(a.uid, b.uid, d.uid))
}

// #1, the other order: C, stolen but still linked, asks B to unlink A
// before B hears that a person at A unlinked C (A and B were apart). B
// doesn't take A's request back (round 9: never from a computer refused
// here): it shows the conflict, A says to link them again, and that
// relink delivers it. C can't ask about A afterwards either.
func TestMembershipAskBackOnceAndOnlyInTheRace(t *testing.T) {
	a, b, c := mesh(t)
	stop(b)
	waitComputers(t, a, "b off", offline(b.uid))
	unlink(t, a, c.uid) // waits for b
	stop(a)
	b = restartComputer(t, b)
	waitComputers(t, b, "b back, with c", func(cs *pb.Computers) bool { return online(cs)[c.uid] })
	if err := send(t, strangerTo(t, c, b), unlinkReq(a.uid, 0, "")); err != nil {
		t.Fatal(err)
	}
	waitComputers(t, b, "b drops a, as c asked", notShown(a.uid))
	a = restartComputer(t, a)
	waitComputers(t, b, "b shows the conflict", hasNote)
	waitComputers(t, a, "a says to link b again", hasNote)
	waitComputers(t, a, "a shows b not linking", refused(b.uid))
	link(t, a, b)
	waitComputers(t, b, "b drops c, once linked with a again", notWith(c.uid))
	holdComputers(t, b, "b never keeps c", hold, notWith(c.uid))
	if err := send(t, strangerTo(t, c, b), unlinkReq(a.uid, tenYears, c.uid)); err == nil {
		t.Fatal("c asked about a")
	}
	holdComputers(t, b, "b keeps a", hold, linkedWith(a.uid))
}

// #2: links a computer since unlinked introduced are dropped further down
// the line too, even when the key behind them ignores unlink requests. A,
// B and S are linked, and D through B; B and D are off. A unlinks S and
// goes off. B, back, takes S's own key K (hostile); S goes off; D, back,
// takes K from B. When A is back, B and D both hold K as waiting for its
// code, and A never links it.
func TestMembershipSuspectPassedOn(t *testing.T) {
	a, b, s := mesh(t)
	d := startComputer(t)
	link(t, d, b)
	waitComputers(t, d, "d with all", linkedWith(a.uid, b.uid, s.uid))
	stop(b)
	stop(d)
	waitComputers(t, a, "b and d off", offline(b.uid))
	unlink(t, a, s.uid)
	waitComputers(t, a, "a without s", notWith(s.uid))
	stop(a)
	k := startComputer(t)
	hostile(k)
	b = restartComputer(t, b)
	link(t, s, k)
	waitComputers(t, b, "b takes k from s", func(cs *pb.Computers) bool { return online(cs)[k.uid] })
	stop(s)
	d = restartComputer(t, d)
	waitComputers(t, d, "d takes k from b", func(cs *pb.Computers) bool { return online(cs)[k.uid] })
	a = restartComputer(t, a)
	isSuspect := func(cs *pb.Computers) bool {
		x := computerIn(cs, k.uid)
		return x != nil && boolField(x, "suspect") && !x.Online
	}
	waitComputers(t, b, "b holds k as waiting for its code", isSuspect)
	waitComputers(t, d, "d too", isSuspect)
	holdComputers(t, d, "and keeps it so", hold, isSuspect)
	holdComputers(t, a, "a never links k", hold, notWith(k.uid))
}

// #3: a stolen computer, still linked, can't say its unlink of A was asked
// long ago (to have every link A ever introduced checked again): links A
// introduced before it was in touch aren't touched.
func TestMembershipForgedAgeBounded(t *testing.T) {
	a, b, x := mesh(t) // a and x through b...
	c := startComputer(t)
	link(t, c, a) // ...and c introduced to b by a
	waitComputers(t, b, "b with all", linkedWith(a.uid, x.uid, c.uid))
	time.Sleep(6 * time.Second) // (all in touch a while)
	if err := send(t, strangerTo(t, x, b), unlinkReq(a.uid, tenYears, x.uid)); err != nil {
		t.Fatal(err)
	}
	waitComputers(t, b, "b drops a, as x asked", notWith(a.uid))
	holdComputers(t, b, "c, introduced by a long before, stays linked", hold, func(cs *pb.Computers) bool {
		y := computerIn(cs, c.uid)
		return y != nil && !boolField(y, "suspect") && y.Online
	})
}

// #4: a computer that unlinks X as asked doesn't send anything to the
// computers it refused at X's request (they may be X's accomplices).
func TestMembershipNothingSentToRefused(t *testing.T) {
	a, b, x := mesh(t)
	y := startComputer(t)
	link(t, y, b)
	waitComputers(t, b, "b with all", linkedWith(a.uid, x.uid, y.uid))
	if err := send(t, strangerTo(t, x, b), unlinkReq(y.uid, 0, "")); err != nil {
		t.Fatal(err)
	}
	waitComputers(t, b, "b drops y, as x asked", notShown(y.uid))
	stop(y)
	unlink(t, a, x.uid)
	waitComputers(t, b, "b drops x", notWith(x.uid))
	for end := time.Now().Add(hold); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		b.d.mu.Lock()
		for _, p := range b.d.comp.pending {
			if p.To == y.uid {
				b.d.mu.Unlock()
				t.Fatalf("b means to tell y, which it refused: %v", p)
			}
		}
		b.d.mu.Unlock()
	}
}

// #5: Remove on a link waiting for its code (introduced by a computer since
// unlinked) doesn't let an introduction link it again.
func TestMembershipRemovedSuspectStaysOut(t *testing.T) {
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
	waitComputers(t, b, "b holds k for its code", func(cs *pb.Computers) bool {
		x := computerIn(cs, k.uid)
		return x != nil && boolField(x, "suspect")
	})
	m := &pb.UnlinkComputer{UserId: k.uid}
	setBoolField(m, "forget", true)
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkComputer{UnlinkComputer: m}})
	waitComputers(t, b, "b removes k", notShown(k.uid))
	_ = send(t, strangerTo(t, a, b), &pb.ClientMessage{Msg: &pb.ClientMessage_IntroduceComputers{IntroduceComputers: &pb.IntroduceComputers{Computers: []*pb.Computer{recordOf(k)}}}})
	for end := time.Now().Add(hold); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		b.d.mu.Lock()
		in := b.d.comp.intros[k.uid]
		b.d.mu.Unlock()
		if in != nil {
			t.Fatal("b took an introduction of k, removed while it waited for its code")
		}
	}
	holdComputers(t, b, "b never links k", hold, notWith(k.uid))
}
