package daemon

import (
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Round 13: a computer decides from facts it holds itself (refusals, with
// their asked times, and its own links' chains); a notice is only a
// pointer to recheck.

// After N holds H (round 11), H leaves and a person links it again with
// A: A and B, which vouched for H all along (their own clean links), take
// it again; they never recorded it as held.
func TestMembershipVouchedNoticeChangesNothing(t *testing.T) {
	t.Parallel()
	a, b, h, _ := noticeAboutHonest(t)
	for _, tc := range []*testComputer{a, b} {
		tc.d.mu.Lock()
		held := tc.d.comp.suspects[h.uid] != nil
		tc.d.mu.Unlock()
		if held {
			t.Fatalf("%s records h as held, though its own link vouches for it", tc.uid)
		}
	}
	unlink(t, h, h.uid)
	waitComputers(t, h, "h alone", alone)
	waitComputers(t, b, "b drops h", notShown(h.uid))
	link(t, h, a)
	waitComputers(t, a, "a with h again", onlineWith(h.uid))
	waitComputers(t, b, "b takes h again, from a", onlineWith(h.uid))
	holdComputers(t, b, "and keeps it", hold, onlineWith(h.uid))
}

// Three hostile keys in a row (K → K2 → K3, after S's unlink): E takes
// only K3, from D, with K and K2 off. When A is back, E holds K3 and never
// links K or K2.
func TestMembershipHostileChainOfThree(t *testing.T) {
	t.Parallel()
	a, d, s := startComputer(t), startComputer(t), startComputer(t)
	link(t, d, a)
	link(t, s, a)
	waitComputers(t, d, "d with a and s", linkedWith(a.uid, s.uid))
	stop(d)
	waitComputers(t, a, "d off", offline(d.uid))
	unlink(t, a, s.uid)
	waitComputers(t, a, "a without s", notWith(s.uid))
	stop(a)
	d = restartComputer(t, d)
	k, k2, k3 := startComputer(t), startComputer(t), startComputer(t)
	for _, x := range []*testComputer{k, k2, k3} {
		hostile(x)
	}
	link(t, s, k)
	waitComputers(t, d, "d takes k from s", onlineWith(k.uid))
	stop(s)
	link(t, k, k2)
	waitComputers(t, d, "d takes k2", onlineWith(k2.uid))
	stop(k)
	link(t, k2, k3)
	waitComputers(t, d, "d takes k3", onlineWith(k3.uid))
	stop(k2)
	e := startComputer(t)
	link(t, e, d)
	waitComputers(t, e, "e takes k3 from d", onlineWith(k3.uid))
	a = restartComputer(t, a)
	waitComputers(t, e, "e holds k3", heldFor(k3.uid))
	holdComputers(t, e, "and keeps it so, never linking k or k2", hold, func(cs *pb.Computers) bool {
		return heldFor(k3.uid)(cs) && notWith(k.uid, k2.uid)(cs)
	})
}
