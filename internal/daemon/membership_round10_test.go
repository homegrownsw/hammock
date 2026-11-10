package daemon

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Round 10: a link a computer since unlinked introduced is held for its
// code however many computers it passed through, with none of them on.

func onlineWith(uid string) func(*pb.Computers) bool {
	return func(cs *pb.Computers) bool { return online(cs)[uid] }
}

// #1: S's hostile key K reaches B through D (D took it from S after the
// unlink), and D stays off: B holds K anyway, and A never links it.
func TestMembershipChainTwoHops(t *testing.T) {
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
	waitComputers(t, d, "d takes k from s", onlineWith(k.uid))
	stop(s)
	b = restartComputer(t, b)
	waitComputers(t, b, "b takes k from d", onlineWith(k.uid))
	stop(d)
	a = restartComputer(t, a)
	waitComputers(t, b, "b holds k for its code (d off)", heldFor(k.uid))
	holdComputers(t, b, "and keeps it so", hold, heldFor(k.uid))
	holdComputers(t, a, "a never links k", hold, notShown(k.uid))
}

// #1, three hops: K went S → D → E → B, all after S's unlink, and none of
// them is on when B hears it.
func TestMembershipChainThreeHops(t *testing.T) {
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
	waitComputers(t, d, "d takes k from s", onlineWith(k.uid))
	stop(s)
	e := startComputer(t)
	link(t, e, d)
	waitComputers(t, e, "e takes k from d", onlineWith(k.uid))
	stop(d)
	b = restartComputer(t, b)
	link(t, e, b)
	waitComputers(t, b, "b takes k from e", onlineWith(k.uid))
	stop(e)
	a = restartComputer(t, a)
	waitComputers(t, b, "b holds k for its code", heldFor(k.uid))
	holdComputers(t, b, "and keeps it so", hold, heldFor(k.uid))
	holdComputers(t, a, "a never links k", hold, notShown(k.uid))
}

// #2: D hears of S's unlink only from B, linked (by a person) after it was
// asked: K, which S introduced to D in between, is still held for its code.
func TestMembershipRelayerLinkedLater(t *testing.T) {
	a, b, s := mesh(t)
	unlink(t, a, s.uid)
	waitComputers(t, b, "b refuses s", notWith(s.uid))
	d := startComputer(t)
	link(t, d, s)
	k := startComputer(t)
	hostile(k)
	link(t, s, k)
	waitComputers(t, d, "d takes k from s", onlineWith(k.uid))
	link(t, d, b)
	waitComputers(t, d, "d holds k for its code", heldFor(k.uid))
	holdComputers(t, d, "and keeps it so", hold, heldFor(k.uid))
}
