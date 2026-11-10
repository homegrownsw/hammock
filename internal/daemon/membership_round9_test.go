package daemon

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Round 9: links an unlinked computer introduced are held for their code
// however the unlink arrives, and a refused computer can't ask back.

func heldFor(uid string) func(*pb.Computers) bool {
	return func(cs *pb.Computers) bool {
		x := computerIn(cs, uid)
		return x != nil && boolField(x, "suspect") && !x.Online
	}
}

// #1: the unlink of S reaches D only by relay (B's), and S's own hostile
// key K reached D from S after the unlink: D holds K for its code, and so
// does B (which took K from D), however late A's own request comes.
func TestMembershipRelayMarks(t *testing.T) {
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
	d = restartComputer(t, d)
	k := startComputer(t)
	hostile(k)
	link(t, s, k)
	waitComputers(t, d, "d takes k from s", func(cs *pb.Computers) bool { return online(cs)[k.uid] })
	stop(s)
	b = restartComputer(t, b)
	waitComputers(t, b, "b takes k from d", func(cs *pb.Computers) bool { return online(cs)[k.uid] })
	stop(d)
	a = restartComputer(t, a)
	waitComputers(t, b, "b refuses s", notShown(s.uid))
	stop(a)
	d = restartComputer(t, d)
	waitComputers(t, d, "d holds k for its code (the relay)", heldFor(k.uid))
	a = restartComputer(t, a)
	holdComputers(t, d, "d keeps holding k", hold, heldFor(k.uid))
	waitComputers(t, b, "b holds k too", heldFor(k.uid))
	holdComputers(t, a, "a never links k", hold, notWith(k.uid))
}

// #1, on a full mesh: B's relay reaches D before A's own request does.
func TestMembershipRelayFirstMarks(t *testing.T) {
	a, b, s, d := four(t)
	stop(d)
	waitComputers(t, a, "d off", offline(d.uid))
	unlink(t, a, s.uid)
	waitComputers(t, b, "b refuses s", notWith(s.uid))
	stop(a)
	stop(b)
	k := startComputer(t)
	hostile(k)
	d = restartComputer(t, d)
	link(t, s, k)
	waitComputers(t, d, "d takes k from s", func(cs *pb.Computers) bool { return online(cs)[k.uid] })
	b = restartComputer(t, b)
	waitComputers(t, d, "d holds k for its code (the relay first)", heldFor(k.uid))
	a = restartComputer(t, a)
	holdComputers(t, d, "d keeps holding k", hold, heldFor(k.uid))
}

// #2: a stolen computer that was off when you unlinked it can't, back,
// have the computer you unlinked it from refused anywhere.
func TestMembershipNoAskBack(t *testing.T) {
	a, b, c, d := four(t)
	stop(c)
	waitComputers(t, a, "c off", offline(c.uid))
	unlink(t, a, c.uid)
	waitComputers(t, b, "b drops c", linkedWith(a.uid, d.uid))
	waitComputers(t, d, "d drops c", linkedWith(a.uid, b.uid))
	c = restartComputer(t, c)
	for _, tc := range []*testComputer{b, d} {
		if send(t, strangerTo(t, c, tc), unlinkReq(a.uid, tenYears, c.uid)) == nil {
			t.Fatal("a computer refused here took back its unlink")
		}
	}
	holdComputers(t, a, "a keeps b and d", hold, linkedWith(b.uid, d.uid))
	holdComputers(t, b, "b keeps a", hold, linkedWith(a.uid, d.uid))
	holdComputers(t, d, "d keeps a", hold, linkedWith(a.uid, b.uid))
}
