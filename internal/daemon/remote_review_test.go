package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// peerConn is host's connection from the peer signed in as uid.
func peerConn(t *testing.T, host *Daemon, uid string) *conn {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		host.mu.Lock()
		for c := range host.conns {
			if c.peer == uid {
				host.mu.Unlock()
				return c
			}
		}
		host.mu.Unlock()
	}
	t.Fatal("no connection from the peer")
	return nil
}

// A linked computer watching this Mac's screen keeps watching when a pane
// closes (a display is in no Group, but linked computers may watch it), and
// keeps the menus of panes it still reaches.
func TestLinkedScreenSurvivesPaneClose(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("screen-sharing = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	host, dir := startHost(t)
	enc := &fakeEncoder{d: host, s: map[string]*fakeSession{}}
	host.media.screenSource = func() frameSource { return enc }
	h := dial(t, dir)
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "G"}}}))
	keep := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	closing := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	host.mu.Lock()
	keptPane, closedPane := host.tabs[keep].FocusedPaneId, host.tabs[closing].FocusedPaneId
	host.mu.Unlock()

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	uid := userIDFor(pub)
	host.mu.Lock()
	host.comp.links[uid] = &pb.StoredLink{Record: &pb.Computer{UserId: uid, Key: pub}}
	host.mu.Unlock()
	peer, err := peerHelloWith(t, host, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	sid := call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{Screen: true, InBand: true}}}).GetMediaTicket().SessionId
	pc := peerConn(t, host, uid)
	host.browsers.mu.Lock()
	host.browsers.menus = map[int]*remoteMenu{1: {c: pc, pane: keptPane, kind: "context"}, 2: {c: pc, pane: closedPane, kind: "context"}}
	host.browsers.mu.Unlock()

	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: closing}}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		host.browsers.mu.Lock()
		closed := host.browsers.menus[2] == nil
		host.browsers.mu.Unlock()
		if closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the closed pane's menu is still open")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if len(host.media.matching(func(ms *mediaSession) bool { return ms.id == sid })) == 0 {
		t.Fatal("the linked computer's screen session ended when a pane closed")
	}
	host.browsers.mu.Lock()
	kept := host.browsers.menus[1] != nil
	host.browsers.mu.Unlock()
	if !kept {
		t.Fatal("a menu in a pane still in reach was closed")
	}
}

// A guest's link to a host where it's in no Group any more (removed while
// it was away, say), with no join waiting, ends once it has signed in and
// seen so, rather than signing in again and again as a stranger.
func TestGuestLinkEndsWhenInNoGroup(t *testing.T) {
	host, _ := startHost(t)
	guest := startDaemon(t, shortDir(t))
	addrs := host.RemoteAddrs()
	host.mu.Lock()
	tok := &pb.InviteToken{HostName: "host", Addresses: addrs, CertSha256: host.remote.fingerprint, HostUserId: host.id.me.UserId}
	host.mu.Unlock()
	guest.mu.Lock()
	l := newLink(guest, tok)
	// What it last saw there: a Group it has since been removed from.
	l.applyLocked(&pb.StateUpdate{Full: true, Groups: []*pb.Group{{Id: "gr_gone"}}}, false)
	guest.links[linkKey(tok)] = l
	guest.persist(guest.store.PutRecord("peerlink", linkKey(tok), tok))
	guest.mu.Unlock()
	go l.run()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		guest.mu.Lock()
		gone := guest.links[linkKey(tok)] == nil
		recs, _ := guest.store.Records("peerlink")
		guest.mu.Unlock()
		if gone && len(recs) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the link to a host it's in no Group on is still kept")
		}
	}
	select {
	case <-l.stop:
	case <-time.After(3 * time.Second):
		t.Fatal("the link still redials")
	}
}

// Closing a shared Group disconnects its guests once they're in no Group
// here, as removing them does.
func TestClosingSharedGroupHangsUpGuests(t *testing.T) {
	for _, unsub := range []bool{false, true} {
		_, h, peer, _, gid, _, _ := sharedSetupAs(t, pb.Role_ROLE_VIEWER, !unsub)
		call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: gid}}})
		select {
		case <-peer.Done():
		case <-time.After(3 * time.Second):
			t.Fatalf("guest (subscribed %v) of a closed Group still connected", !unsub)
		}
	}
}

// Failed sign-ins are audited once a minute per IPv6 /64 (not per
// address), and only so many a minute in all; the rest are counted in one
// row.
func TestFailedSignInAuditBudget(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	d.mu.Lock()
	defer d.mu.Unlock()
	count := func() (n int, more string) {
		evs, _ := d.store.RecentAudit(10000)
		for _, e := range evs {
			if e.Action == "auth-failed" {
				n++
				if e.Detail != "" {
					more = e.Detail
				}
			}
		}
		return n, more
	}
	for i := range 50 {
		d.auditSignInFailed(&conn{srcIP: fmt.Sprintf("2001:db8:1:2::%x", i+1)}, "remote", "auth-failed", "", "")
	}
	if n, _ := count(); n != 1 {
		t.Fatalf("one /64: %d rows, want 1", n)
	}
	for i := range 200 {
		d.auditSignInFailed(&conn{srcIP: fmt.Sprintf("2001:db8:%x::1", i+10)}, "remote", "auth-failed", "", "")
	}
	if n, _ := count(); n != failAuditBudget {
		t.Fatalf("many sources: %d rows, want %d", n, failAuditBudget)
	}
	if d.remote.failMore != 200-(failAuditBudget-1) {
		t.Fatalf("counted %d more, want %d", d.remote.failMore, 200-(failAuditBudget-1))
	}
	d.mu.Unlock()
	d.auditMoreFailed()
	d.mu.Lock()
	if n, more := count(); n != failAuditBudget+1 || more != "181 more failed sign-ins, not recorded one by one" {
		t.Fatalf("after the minute: %d rows, %q", n, more)
	}
}

// fakePeerConn is a signed-in remote connection from uid with key, as the
// daemon tracks it.
func fakePeerConn(d *Daemon, uid string, key []byte) *conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{d: d, remote: true, authed: true, peer: uid, peerKey: key, kind: "peer", ctx: ctx, cancel: cancel, out: make(chan *pb.ServerMessage, 64)}
	d.conns[c] = struct{}{}
	return c
}

// Approving a join records the key of the connection that asked, and
// closes any other connection signed in under the same id with another key.
func TestApprovedJoinKeepsItsOwnKey(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	uid := userIDFor(pub)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.groups["gr_x"] = &pb.Group{Id: "gr_x"}
	impostor := fakePeerConn(d, uid, other) // (a key whose id collides)
	asker := fakePeerConn(d, uid, pub)
	d.people[uid] = &pb.Person{UserId: uid, Name: "Guest"}
	d.joins["jr_x"] = &pendingJoin{req: &pb.JoinRequest{Id: "jr_x", GroupId: "gr_x", UserId: uid}, c: asker, msgID: 1}
	if err := d.respondJoin(&pb.RespondJoin{Id: "jr_x", Role: pb.Role_ROLE_VIEWER}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.knownKey(uid), pub) {
		t.Fatal("the key on record isn't the approved join's")
	}
	if impostor.ctx.Err() == nil {
		t.Fatal("a connection with another key under the same id stays signed in as the member")
	}
	if asker.ctx.Err() != nil {
		t.Fatal("the approved connection was closed")
	}
}
