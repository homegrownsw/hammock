package daemon

import (
	"sync"
	"testing"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// Round 11: a notice that a key is held for its code doesn't hold an
// honest computer's links made long before; and an introduction backed
// out of is undone.

// #1: A, B, S and H are linked; B and H are off; A unlinks S and goes
// off. B, back, is linked by a person with N; H, back, is introduced to N
// by S (after S's unlink; B off then, so S is the only introducer). When A
// is back, N holds H (its own chain names S after the unlink), but B,
// linked with H long before, keeps H.
func TestMembershipNoticeDoesntHoldHonestLinks(t *testing.T) { noticeAboutHonest(t) }

// noticeAboutHonest is that sequence: N holds H; A and B keep it.
func noticeAboutHonest(t *testing.T) (a, b, h, n *testComputer) {
	t.Helper()
	all := []*testComputer{startComputer(t), startComputer(t), startComputer(t), startComputer(t)}
	for _, x := range all[1:] {
		link(t, x, all[0])
	}
	for _, x := range all {
		var others []string
		for _, y := range all {
			if y != x {
				others = append(others, y.uid)
			}
		}
		waitComputers(t, x, "all four linked", linkedWith(others...))
	}
	a, b, h = all[0], all[3], all[2]
	s := all[1]
	stop(b)
	stop(h)
	waitComputers(t, a, "b and h off", offline(b.uid))
	unlink(t, a, s.uid)
	waitComputers(t, a, "a without s", notWith(s.uid))
	stop(a)
	b = restartComputer(t, b)
	n = startComputer(t)
	link(t, n, b)
	waitComputers(t, n, "n with b and s", linkedWith(b.uid, s.uid))
	stop(b) // (so S is H's only introducer to N)
	h = restartComputer(t, h)
	waitComputers(t, n, "n takes h", onlineWith(h.uid))
	n.d.mu.Lock()
	via := n.d.comp.links[h.uid].GetVia()
	n.d.mu.Unlock()
	if via != s.uid {
		t.Fatalf("n took h through %s, not s", via)
	}
	b = restartComputer(t, b)
	waitComputers(t, b, "b back, with h", onlineWith(h.uid))
	waitComputers(t, n, "n back with b", onlineWith(b.uid))
	a = restartComputer(t, a)
	waitComputers(t, n, "n holds h for its code", heldFor(h.uid))
	holdComputers(t, b, "b keeps h", hold, onlineWith(h.uid))
	both := func(cs *pb.Computers) bool { return online(cs)[b.uid] && online(cs)[a.uid] }
	waitComputers(t, h, "h back with b and a", both)
	holdComputers(t, h, "h keeps b and a", hold, both)
	return a, b, h, n
}

// Backing out of an introduction (its introducer unlinked meanwhile): the
// other side, which had linked, drops the link and doesn't show the
// dialler as refusing it.
func TestMembershipIntroBackOutUndone(t *testing.T) {
	p, v := startComputer(t), startComputer(t)
	link(t, p, v)
	q := startComputer(t)
	dialler, other := p, q
	if q.uid < p.uid {
		dialler, other = q, p
	}
	var once sync.Once
	backedOut := make(chan struct{})
	afterIntroCall = func(d *Daemon) { // (called once the other side has linked)
		if d != dialler.d {
			return
		}
		once.Do(func() {
			d.mu.Lock()
			_ = d.unlinkComputer(v.uid, true) // its introducer gone here, meanwhile
			d.mu.Unlock()
			close(backedOut)
		})
	}
	t.Cleanup(func() { afterIntroCall = nil })
	link(t, q, v)
	select {
	case <-backedOut:
	case <-time.After(15 * time.Second):
		t.Fatal("the introduced link was never asked for")
	}
	waitComputers(t, other, "the other side drops the link", notShown(dialler.uid))
	holdComputers(t, other, "and doesn't show it, refusing or linked", hold, notShown(dialler.uid))
	holdComputers(t, other, "it keeps v", hold, onlineWith(v.uid))
}
