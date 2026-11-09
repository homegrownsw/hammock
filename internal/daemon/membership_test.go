package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Membership (ADR-0013: direct links only), through real daemons and
// clients. These use only what any build has (who's linked, and reachable),
// so they also run against earlier designs. Checks that must stay true are
// held (holdComputers), not taken at the first poll.

const hold = 1500 * time.Millisecond

// online is the computers (other than this one) tc lists as linked and
// reachable.
func online(cs *pb.Computers) map[string]bool {
	out := map[string]bool{}
	for _, c := range cs.List {
		if !c.ThisComputer && c.Online && !boolField(c, "refused") {
			out[c.UserId] = true
		}
	}
	return out
}

// boolField reads a bool field by name (false where a build has no such field).
func boolField(c *pb.Computer, name string) bool {
	f := c.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name))
	return f != nil && c.ProtoReflect().Get(f).Bool()
}

// linkedWith: tc is linked with exactly these, all reachable.
func linkedWith(uids ...string) func(*pb.Computers) bool {
	return func(cs *pb.Computers) bool {
		on := online(cs)
		if len(on) != len(uids) {
			return false
		}
		for _, u := range uids {
			if !on[u] {
				return false
			}
		}
		return true
	}
}

func alone(cs *pb.Computers) bool { return len(online(cs)) == 0 }

// notWith: none of these is linked and reachable from tc.
func notWith(uids ...string) func(*pb.Computers) bool {
	return func(cs *pb.Computers) bool {
		on := online(cs)
		for _, u := range uids {
			if on[u] {
				return false
			}
		}
		return true
	}
}

// refused: tc shows uid as not linking with it any more (with Link Again).
func refused(uid string) func(*pb.Computers) bool {
	return func(cs *pb.Computers) bool {
		for _, c := range cs.List {
			if c.UserId == uid {
				return boolField(c, "refused") && !c.Online
			}
		}
		return false
	}
}

// mesh links a, b and c: a with b, then c with b (c is introduced to a).
func mesh(t *testing.T) (a, b, c *testComputer) {
	t.Helper()
	a, b, c = startComputer(t), startComputer(t), startComputer(t)
	link(t, a, b)
	link(t, c, b)
	waitComputers(t, a, "a, b, c linked", linkedWith(b.uid, c.uid))
	waitComputers(t, c, "c, a, b linked", linkedWith(a.uid, b.uid))
	return a, b, c
}

// unlink is Unlink on on: of uid from all its computers (of itself: leaving).
func unlink(t *testing.T, on *testComputer, uid string) {
	t.Helper()
	call(t, on.c, &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkComputer{UnlinkComputer: &pb.UnlinkComputer{UserId: uid}}})
}

func offline(uid string) func(*pb.Computers) bool { return notWith(uid) }

// A leaves (Unlink This Computer), and a person links A with B again: A
// gets C back too (leaving isn't an unlink: nobody refuses A's key).
func TestMembershipLeaveAndRejoin(t *testing.T) {
	a, b, c := mesh(t)
	unlink(t, a, a.uid)
	waitComputers(t, a, "a alone", alone)
	waitComputers(t, b, "b keeps c", linkedWith(c.uid))
	waitComputers(t, c, "c keeps b", linkedWith(b.uid))
	holdComputers(t, c, "c keeps b only", hold, linkedWith(b.uid))

	link(t, a, b)
	waitComputers(t, a, "a gets b and c back", linkedWith(b.uid, c.uid))
	holdComputers(t, a, "a stays with b and c", hold, linkedWith(b.uid, c.uid))
	holdComputers(t, c, "c with a and b", hold, linkedWith(a.uid, b.uid))
}

// A unlinks B (from all) while B is off; B wakes; a person links A and B
// again. A links B (a person did the code check there); C was asked to
// unlink B and refuses it until a person links them there too.
func TestMembershipRelinkAfterOfflineUnlink(t *testing.T) {
	a, b, c := mesh(t)
	stop(b)
	waitComputers(t, a, "b off", offline(b.uid))
	unlink(t, a, b.uid)
	waitComputers(t, c, "c drops b", linkedWith(a.uid))
	b = restartComputer(t, b)
	waitComputers(t, b, "b finds out", alone)
	holdComputers(t, b, "b stays out", hold, alone)
	waitComputers(t, b, "b shows a as not linking with it", refused(a.uid))

	link(t, a, b)
	waitComputers(t, b, "b linked with a", linkedWith(a.uid))
	holdComputers(t, b, "b with a only (c refuses it)", hold, linkedWith(a.uid))
	holdComputers(t, c, "c with a only", hold, linkedWith(a.uid))
	holdComputers(t, a, "a with b and c", hold, linkedWith(b.uid, c.uid))
}

// unlinkedWhileOff: A unlinks B (from all) while B is off, and B links E
// before it hears; then A and C come back (in the order asked).
func unlinkedWhileOff(t *testing.T, aFirst bool) (a, b, c, e *testComputer) {
	t.Helper()
	a, b, c = mesh(t)
	stop(b)
	waitComputers(t, a, "b off", offline(b.uid))
	unlink(t, a, b.uid)
	waitComputers(t, c, "c drops b", linkedWith(a.uid))
	stop(a)
	stop(c)
	b = restartComputer(t, b)
	e = startComputer(t)
	link(t, b, e)
	waitComputers(t, e, "e linked with b", func(cs *pb.Computers) bool { return online(cs)[b.uid] })
	if aFirst {
		a = restartComputer(t, a)
		waitComputers(t, b, "b without a (a back)", notWith(a.uid))
		c = restartComputer(t, c)
	} else {
		a, c = restartComputer(t, a), restartComputer(t, c)
	}
	waitComputers(t, b, "b with e only", linkedWith(e.uid))
	holdComputers(t, b, "b stays with e only", hold, linkedWith(e.uid))
	holdComputers(t, e, "e stays with b only", hold, linkedWith(b.uid))
	holdComputers(t, a, "a stays with c only", hold, linkedWith(c.uid))
	holdComputers(t, c, "c stays with a only", hold, linkedWith(a.uid))
	return a, b, c, e
}

// Linking another computer doesn't cancel an unlink (B linked E before it
// heard), and the computers B had aren't added back (to B or E).
func TestMembershipLinkingOtherDoesntCancel(t *testing.T) { unlinkedWhileOff(t, false) }

// The same, A back before C.
func TestMembershipABackBeforeC(t *testing.T) { unlinkedWhileOff(t, true) }

// An unlinked computer introduced again through new computers: A links F,
// F links E (linked with B). A and C get F and E, and refuse B.
func TestMembershipUnlinkedRefusedThroughNew(t *testing.T) {
	a, b, c, e := unlinkedWhileOff(t, false)
	f := startComputer(t)
	link(t, a, f)
	link(t, f, e)
	waitComputers(t, a, "a with c, f, e", linkedWith(c.uid, f.uid, e.uid))
	waitComputers(t, c, "c with a, f, e", linkedWith(a.uid, f.uid, e.uid))
	holdComputers(t, a, "a never links b", hold, notWith(b.uid))
	holdComputers(t, c, "c never links b", hold, notWith(b.uid))
	holdComputers(t, b, "b without a and c", hold, notWith(a.uid, c.uid))
}

// The same, E restarting before A and C are back.
func TestMembershipERestartsFirst(t *testing.T) {
	a, b, c := mesh(t)
	stop(b)
	waitComputers(t, a, "b off", offline(b.uid))
	unlink(t, a, b.uid)
	waitComputers(t, c, "c drops b", linkedWith(a.uid))
	stop(a)
	stop(c)
	b = restartComputer(t, b)
	e := startComputer(t)
	link(t, b, e)
	e = restartComputer(t, e)
	waitComputers(t, e, "e back, with b", func(cs *pb.Computers) bool { return online(cs)[b.uid] })
	a, c = restartComputer(t, a), restartComputer(t, c)
	waitComputers(t, e, "e with b only", linkedWith(b.uid))
	holdComputers(t, e, "e stays with b only", hold, linkedWith(b.uid))
	holdComputers(t, b, "b stays with e only", hold, linkedWith(e.uid))
}

// A chain made while B didn't know it was unlinked: B links E, E links F
// (E restarting partway). B, E and F keep each other; A and C never get any
// of them.
func TestMembershipChain(t *testing.T) {
	a, b, c := mesh(t)
	stop(b)
	waitComputers(t, a, "b off", offline(b.uid))
	unlink(t, a, b.uid)
	waitComputers(t, c, "c drops b", linkedWith(a.uid))
	stop(a)
	stop(c)
	b = restartComputer(t, b)
	e, f := startComputer(t), startComputer(t)
	link(t, b, e)
	e = restartComputer(t, e)
	link(t, e, f)
	waitComputers(t, f, "f linked with b and e", linkedWith(b.uid, e.uid))
	a, c = restartComputer(t, a), restartComputer(t, c)
	for _, x := range []struct {
		tc   *testComputer
		with []string
	}{{b, []string{e.uid, f.uid}}, {e, []string{b.uid, f.uid}}, {f, []string{b.uid, e.uid}}, {a, []string{c.uid}}, {c, []string{a.uid}}} {
		waitComputers(t, x.tc, "settled", linkedWith(x.with...))
		holdComputers(t, x.tc, "stays settled", hold, linkedWith(x.with...))
	}
}

// Unlinked while off: it finds out when it's back, and stays out.
func TestMembershipUnlinkReachesOffline(t *testing.T) {
	a, b, c := mesh(t)
	stop(b)
	waitComputers(t, a, "b off", offline(b.uid))
	unlink(t, a, b.uid)
	b = restartComputer(t, b)
	waitComputers(t, b, "b finds out: a doesn't link with it", refused(a.uid))
	waitComputers(t, b, "nor c", refused(c.uid))
	holdComputers(t, b, "b stays out", hold, alone)
	holdComputers(t, a, "a keeps c only", hold, linkedWith(c.uid))
	holdComputers(t, c, "c keeps a only", hold, linkedWith(a.uid))
}

// An unlinked computer that changes its record (renamed, and an input time
// from a clock run ahead) doesn't come back through the computer that was
// off. (Clocks themselves: TestInputClockAhead.)
func TestMembershipRenameDoesntRelink(t *testing.T) {
	a, x, d := mesh(t)
	stop(d)
	waitComputers(t, a, "d off", offline(d.uid))
	unlink(t, a, x.uid)
	waitComputers(t, x, "x out", alone)
	x.d.noteInput(time.Now().Add(time.Hour))
	name := "Renamed after its unlink"
	call(t, x.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetProfile{SetProfile: &pb.SetProfile{Name: "You", DeviceName: &name}}})
	d = restartComputer(t, d)
	waitComputers(t, d, "d drops x", linkedWith(a.uid))
	holdComputers(t, d, "d stays without x", hold, linkedWith(a.uid))
	holdComputers(t, a, "a stays without x", hold, linkedWith(d.uid))
	holdComputers(t, x, "x stays out", hold, alone)
}

// A computer whose database was reset (same key and certificate): the
// others show it as not linking with them any more (never linked and
// online with nothing), and a person links it again.
func TestMembershipResetComputer(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, a, "linked", linkedWith(b.uid))
	stop(b)
	for _, f := range []string{"state.db", "state.db-wal", "state.db-shm"} {
		os.Remove(filepath.Join(b.dir, f))
	}
	b = restartComputer(t, b)
	waitComputers(t, a, "a shows b as not linking with it", refused(b.uid))
	holdComputers(t, a, "and never as linked", hold, notWith(b.uid))
	link(t, a, b)
	waitComputers(t, a, "linked again", linkedWith(b.uid))
	holdComputers(t, b, "b with a", hold, linkedWith(a.uid))
}

// A leaves while B is off: B, back, drops A (not refusing it: leaving isn't
// an unlink), and stays so; A is alone.
func TestMembershipLeaveWhileOtherOff(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, b, "linked", linkedWith(a.uid))
	stop(b)
	waitComputers(t, a, "b off", offline(b.uid))
	unlink(t, a, a.uid)
	waitComputers(t, a, "a alone", alone)
	b = restartComputer(t, b)
	gone := func(cs *pb.Computers) bool { return !hasComputer(a.uid, false)(cs) }
	waitComputers(t, b, "b drops a", gone)
	holdComputers(t, b, "b stays without a", hold, gone)
	b.d.mu.Lock()
	refuses := b.d.comp.denied[a.uid]
	b.d.mu.Unlock()
	if refuses {
		t.Fatal("b refuses a, which only left")
	}
	holdComputers(t, a, "a stays alone", hold, alone)
}

// Two computers introduce the same pair (P links V1, then V2; both are
// linked with Q): P and Q link, whichever introduction each heard last.
func TestMembershipTwoIntroducers(t *testing.T) {
	v1, v2, q := startComputer(t), startComputer(t), startComputer(t)
	link(t, v1, q)
	link(t, v2, q)
	waitComputers(t, q, "q with v1 and v2", linkedWith(v1.uid, v2.uid))
	p := startComputer(t)
	link(t, p, v1)
	link(t, p, v2)
	waitComputers(t, p, "p with all three", linkedWith(v1.uid, v2.uid, q.uid))
	// (The side that answered an introduction dials back once linked, and
	// may find the other not linked yet: online a moment later.)
	waitComputers(t, q, "q with all three", linkedWith(v1.uid, v2.uid, p.uid))
	holdComputers(t, q, "q stays with all three", hold, linkedWith(v1.uid, v2.uid, p.uid))
}

// A computer reset in a mesh of three: linked again with one of them by a
// person, it gets the other back too (its old, refused record there doesn't
// stand in the way).
func TestMembershipResetInMeshRelinks(t *testing.T) {
	a, b, c := mesh(t)
	stop(c)
	for _, f := range []string{"state.db", "state.db-wal", "state.db-shm"} {
		os.Remove(filepath.Join(c.dir, f))
	}
	c = restartComputer(t, c)
	waitComputers(t, a, "a shows c not linking", refused(c.uid))
	waitComputers(t, b, "b shows c not linking", refused(c.uid))
	link(t, c, a)
	waitComputers(t, c, "c with a and b again", linkedWith(a.uid, b.uid))
	holdComputers(t, b, "b with a and c", hold, linkedWith(a.uid, c.uid))
}
