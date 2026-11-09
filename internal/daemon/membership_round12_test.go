package daemon

import "testing"

// Round 12: a key introduced through a computer held here only because
// another computer said so (no link of this one's own with it) is held.

// A, D and S are linked; D is off; A unlinks S and goes off. D, back,
// takes S's key K; K links a second key K2 and introduces it to D; K goes
// off; a person links E with D, and E takes K2 (never K). When A is back,
// D holds K and K2, and so does E.
func TestMembershipHeldHopWithoutLink(t *testing.T) {
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
	k, k2 := startComputer(t), startComputer(t)
	hostile(k)
	hostile(k2)
	link(t, s, k)
	waitComputers(t, d, "d takes k from s", onlineWith(k.uid))
	stop(s)
	link(t, k, k2)
	waitComputers(t, d, "d takes k2 from k", onlineWith(k2.uid))
	stop(k)
	e := startComputer(t)
	link(t, e, d)
	waitComputers(t, e, "e takes k2 from d", onlineWith(k2.uid))
	a = restartComputer(t, a)
	waitComputers(t, d, "d holds k", heldFor(k.uid))
	waitComputers(t, d, "d holds k2", heldFor(k2.uid))
	waitComputers(t, e, "e holds k2", heldFor(k2.uid))
	holdComputers(t, e, "and keeps it so", hold, heldFor(k2.uid))
}
