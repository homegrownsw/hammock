package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// Tests link over loopback: Tailscale says every address is one of your
// computers unless a test says otherwise (testWhoIs), and asking isn't
// rate-limited unless a test turns it on.
var (
	whoIsMu   sync.Mutex
	testWhoIs = func(ip string) (tsSource, error) {
		return tsSource{host: "test-" + ip, login: "you@example.com", mine: true}, nil
	}
)

func init() {
	whoIsAt = func(_ context.Context, ip string) (tsSource, error) {
		whoIsMu.Lock()
		f := testWhoIs
		whoIsMu.Unlock()
		return f(ip)
	}
	overTailscale = func(ip net.IP) bool { return isTailscaleIP(ip) || ip.IsLoopback() } // (test computers: loopback stands for the tailnet)
	linkAskEvery = 0
	linkGrace = 2 * time.Second // (30s live: links made in tests settle well within this)
	askWindow = 6 * time.Second
	reintroduceEvery = 2 * time.Second
	touchSlack = 3 * time.Second
}

func setWhoIs(t *testing.T, f func(string) (tsSource, error)) {
	whoIsMu.Lock()
	old := testWhoIs
	testWhoIs = f
	whoIsMu.Unlock()
	t.Cleanup(func() {
		whoIsMu.Lock()
		testWhoIs = old
		whoIsMu.Unlock()
	})
}

// testComputer is a daemon with its remote listener on loopback, as a
// computer of yours.
type testComputer struct {
	d    *Daemon
	dir  string
	addr string
	c    *client.Client
	uid  string
	seen map[string]bool // link requests linkReq has returned
}

func startComputer(t *testing.T) *testComputer {
	t.Helper()
	dir := shortDir(t)
	d, err := New(Config{Dir: dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Remote: true, RemoteListen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(d.Shutdown)
	for deadline := time.Now().Add(5 * time.Second); len(d.RemoteAddrs()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("remote listener did not start")
		}
	}
	addrs := d.RemoteAddrs()
	d.mu.Lock()
	d.cfg.RemoteListen = addrs
	uid := d.id.me.UserId
	d.mu.Unlock()
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	return &testComputer{d: d, dir: dir, addr: addrs[0], c: c, uid: uid}
}

// linkReq waits for a new link request with its code on tc (code: that one).
func linkReq(t *testing.T, tc *testComputer, code string) *pb.LinkRequest {
	t.Helper()
	var req *pb.LinkRequest
	waitEvent(t, tc.c, func(ev *pb.ServerMessage) bool {
		for _, r := range ev.GetState().GetLinkRequests() {
			// Not an earlier link's (its updates may still be queued).
			if r.Code != "" && !r.AcceptedHere && !tc.seen[r.Id] && (code == "" || r.Code == code) {
				req = r
				return true
			}
		}
		return false
	})
	if tc.seen == nil {
		tc.seen = map[string]bool{}
	}
	tc.seen[req.Id] = true
	return req
}

// link links a with b, both people saying the codes match.
func link(t *testing.T, a, b *testComputer) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_LinkComputer{LinkComputer: &pb.LinkComputer{Address: b.addr}}})
		done <- err
	}()
	go func() {
		// Failing before any code shows: say why, not just "timed out".
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("link failed early: %v", err)
			}
			done <- err
		case <-time.After(30 * time.Second):
		}
	}()
	ra := linkReq(t, a, "")
	rb := linkReq(t, b, ra.Code)
	if ra.Code != rb.Code || ra.Incoming || !rb.Incoming {
		t.Fatalf("codes: asking %+v, asked %+v", ra, rb)
	}
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: rb.Id, Accept: true}}})
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: ra.Id, Accept: true}}})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("link: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("link never finished")
	}
}

// waitComputers waits until tc's list of computers satisfies fn.
func waitComputers(t *testing.T, tc *testComputer, what string, fn func(*pb.Computers) bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		tc.d.mu.Lock()
		ok := fn(tc.d.computersState(false))
		tc.d.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			tc.d.mu.Lock()
			cs := tc.d.computersState(false)
			tc.d.mu.Unlock()
			t.Fatalf("%s: computers %v", what, cs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func hasComputer(uid string, online bool) func(*pb.Computers) bool {
	return func(cs *pb.Computers) bool {
		for _, c := range cs.List {
			if c.UserId == uid && (!online || c.Online) {
				return true
			}
		}
		return false
	}
}

// waitGroup waits until tc shows a Group satisfying fn.
func waitGroup(t *testing.T, tc *testComputer, what string, fn func(*pb.Group) bool) *pb.Group {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		tc.d.mu.Lock()
		for _, g := range tc.d.fullState().Groups {
			if fn(g) {
				tc.d.mu.Unlock()
				return g
			}
		}
		tc.d.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatalf("%s: never shown", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLinkedComputers(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	bGroup := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On B"}}}))

	link(t, a, b)
	waitComputers(t, a, "a knows b", hasComputer(b.uid, true))
	waitComputers(t, b, "b knows a", hasComputer(a.uid, true))

	// B's Group shows on A, as its owner.
	g := waitGroup(t, a, "b's group on a", func(g *pb.Group) bool { return g.Id == bGroup })
	if g.HostUserId != b.uid || g.MyRole != pb.Role_ROLE_OWNER {
		t.Fatalf("b's group on a: %+v", g)
	}
	// A's own Groups show on B the same way.
	aGroup := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On A"}}}))
	waitGroup(t, b, "a's group on b", func(g *pb.Group) bool { return g.Id == aGroup && g.MyRole == pb.Role_ROLE_OWNER })

	// A makes a Group on B, and renames it there.
	made := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Made from A", ComputerId: b.uid}}}))
	b.d.mu.Lock()
	_, onB := b.d.groups[made]
	b.d.mu.Unlock()
	if !onB {
		t.Fatal("the Group A made on B isn't on B")
	}
	waitGroup(t, a, "made group on a", func(g *pb.Group) bool { return g.Id == made })
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RenameGroup{RenameGroup: &pb.RenameGroup{GroupId: made, Name: "Renamed"}}})
	waitGroup(t, b, "renamed on b", func(g *pb.Group) bool { return g.Id == made && g.Name == "Renamed" })

	// A tab there, typed into from A.
	tab := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: made,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "stty -echo; exec cat"}}}}}))
	b.d.mu.Lock()
	t2 := b.d.tabs[tab]
	b.d.mu.Unlock()
	if t2 == nil {
		t.Fatal("the tab A made isn't on B")
	}

	// Sessions and folders of B, asked from A.
	if r := call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListFolders{ListFolders: &pb.ListFolders{ComputerId: b.uid}}}); r.GetFolderList() == nil {
		t.Fatalf("folders: %v", r)
	}

	// A third computer, linked with B, is introduced to A.
	c := startComputer(t)
	cGroup := client.CreatedID(call(t, c.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On C"}}}))
	link(t, c, b)
	waitComputers(t, a, "a knows c", hasComputer(c.uid, true))
	waitComputers(t, c, "c knows a", hasComputer(a.uid, true))
	waitGroup(t, a, "c's group on a", func(g *pb.Group) bool { return g.Id == cGroup && g.MyRole == pb.Role_ROLE_OWNER })
	waitGroup(t, c, "a's group on c", func(g *pb.Group) bool { return g.Id == aGroup })
	// Each has its own colour, and they all agree.
	for _, tc := range []*testComputer{a, b, c} {
		waitComputers(t, tc, "distinct colours", func(cs *pb.Computers) bool {
			seen := map[string]bool{}
			for _, x := range cs.List {
				if x.Tint == "" || seen[x.Tint] {
					return false
				}
				seen[x.Tint] = true
			}
			return len(seen) == 3
		})
	}

	// A picks B's icon there (its record is its own): B's colour is kept,
	// and every computer shows the new icon.
	b.d.mu.Lock()
	bTint := b.d.comp.self.Tint
	b.d.mu.Unlock()
	icon := "🛰️"
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetComputerTint{SetComputerTint: &pb.SetComputerTint{UserId: b.uid, Icon: &icon}}})
	for _, tc := range []*testComputer{a, c} {
		waitComputers(t, tc, "b's icon", func(cs *pb.Computers) bool {
			for _, x := range cs.List {
				if x.UserId == b.uid {
					return x.Icon == icon && x.Tint == bTint
				}
			}
			return false
		})
	}
	// Neutral: no colour, which several may have.
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetComputerTint{SetComputerTint: &pb.SetComputerTint{UserId: a.uid, Tint: "none"}}})
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetComputerTint{SetComputerTint: &pb.SetComputerTint{UserId: b.uid, Tint: "none"}}})
	for _, tc := range []*testComputer{a, b, c} {
		waitComputers(t, tc, "a and b neutral", func(cs *pb.Computers) bool {
			n := 0
			for _, x := range cs.List {
				if x.Tint == "none" {
					n++
				}
			}
			return n == 2
		})
	}
	bad := "not an icon"
	if _, err := a.c.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_SetComputerTint{SetComputerTint: &pb.SetComputerTint{UserId: a.uid, Icon: &bad}}}); err == nil {
		t.Fatal("a bad icon was taken")
	}

	// Groups others share with B don't pass on: only B's own Groups show.
	// (B's copy of C's Group isn't sent to A by B; A has it from C.)
	b.d.mu.Lock()
	full := b.d.filterFor(&conn{d: b.d, peer: a.uid}, b.d.fullState())
	b.d.mu.Unlock()
	for _, g := range full.Groups {
		if g.Id == cGroup || g.Id == aGroup {
			t.Fatalf("b passes on %s", g.Name)
		}
	}

	// Unlinking C on A unlinks it everywhere.
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkComputer{UnlinkComputer: &pb.UnlinkComputer{UserId: c.uid}}})
	gone := func(uid string) func(*pb.Computers) bool {
		return func(cs *pb.Computers) bool { return !hasComputer(uid, false)(cs) }
	}
	waitComputers(t, b, "b drops c", gone(c.uid))
	waitComputers(t, c, "c drops everyone", alone)
	deadline := time.Now().Add(10 * time.Second)
	for {
		a.d.mu.Lock()
		_, shown := a.d.routes[cGroup]
		a.d.mu.Unlock()
		if !shown {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("c's group still shows on a")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Off Tailscale (a local network, a VPN), Tailscale isn't asked: anyone
// reaching remote access may ask, and the code check, showing where it
// came from, decides (ADR-0015).
func TestLinkOffTailscale(t *testing.T) {
	old := overTailscale
	overTailscale = isTailscaleIP
	t.Cleanup(func() { overTailscale = old })
	setWhoIs(t, func(ip string) (tsSource, error) {
		t.Errorf("Tailscale asked about %s", ip)
		return tsSource{}, errors.New("not on a tailnet")
	})
	a, b := startComputer(t), startComputer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Nobody at b is linking: refused there, and nothing shown.
	_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_LinkComputer{LinkComputer: &pb.LinkComputer{Address: b.addr}}})
	if err == nil || !strings.Contains(err.Error(), "isn't expecting a link") {
		t.Fatalf("unexpected link: %v", err)
	}
	b.d.mu.Lock()
	n := len(b.d.comp.attempts)
	b.d.mu.Unlock()
	if n != 0 {
		t.Fatal("a request nobody started reached b")
	}
	// A person at b opens Link a Computer (hmk link --accept: an invite).
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateLinkInvite{CreateLinkInvite: &pb.CreateLinkInvite{}}})
	done := make(chan error, 1)
	go func() {
		_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_LinkComputer{LinkComputer: &pb.LinkComputer{Address: b.addr}}})
		done <- err
	}()
	ra := linkReq(t, a, "")
	rb := linkReq(t, b, ra.Code)
	if rb.TailscaleHost != "" || !strings.HasPrefix(rb.Source, "127.0.0.1") || !strings.HasPrefix(ra.Source, "127.0.0.1") {
		t.Errorf("where from: %q %q / %q %q", ra.TailscaleHost, ra.Source, rb.TailscaleHost, rb.Source)
	}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: ra.Id, Accept: true}}})
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: rb.Id, Accept: true}}})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("link: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("link never answered")
	}
}

// A stranger (any key signs in) stays under the limits of a connection
// that hasn't signed in, isn't told this computer's addresses, and goes
// once it has nothing under way.
func TestStrangerStaysLimited(t *testing.T) {
	old := strangerIdle
	strangerIdle = 200 * time.Millisecond
	t.Cleanup(func() { strangerIdle = old })
	b := startComputer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cl, fp, err := client.DialRemoteLearning(ctx, b.addr, "peer", "stranger")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	_, key, _ := ed25519.GenerateKey(nil)
	var nonce []byte
	for ev := range cl.Events {
		if c := ev.GetAuthChallenge(); c != nil {
			nonce = c.Nonce
			break
		}
	}
	hostNonce := make([]byte, client.NonceSize)
	if _, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerHello{PeerHello: peerHelloFor(key, &pb.Person{Name: "x"}, fp, nonce, hostNonce)}}); err != nil {
		t.Fatal(err)
	}
	r, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Hello{Hello: &pb.Hello{}}})
	if err != nil || len(r.GetHelloReply().GetAddresses()) != 0 {
		t.Fatalf("hello to a stranger: %v %v", r, err)
	}
	b.d.mu.Lock()
	limited := 0
	for c := range b.d.conns {
		if c.remote && c.name == "x" && b.d.remote.preAuth[c] != nil {
			limited++
		}
	}
	b.d.mu.Unlock()
	if limited != 1 {
		t.Fatalf("stranger's limits lifted (%d limited)", limited)
	}
	select {
	case <-cl.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("an idle stranger wasn't dropped")
	}
}

func TestLinkDeclined(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_LinkComputer{LinkComputer: &pb.LinkComputer{Address: b.addr}}})
		done <- err
	}()
	ra := linkReq(t, a, "")
	rb := linkReq(t, b, ra.Code)
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: ra.Id, Accept: true}}})
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: rb.Id, Accept: false}}})
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "match") {
			t.Fatalf("declined link: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("declined link never answered")
	}
	a.d.mu.Lock()
	n := a.d.linkedCount()
	a.d.mu.Unlock()
	b.d.mu.Lock()
	n += b.d.linkedCount()
	b.d.mu.Unlock()
	if n != 0 {
		t.Fatal("linked after a decline")
	}
}

func TestLinkCodeCommitment(t *testing.T) {
	x := linkCode("us_a", "ca", "us_b", "cb", make([]byte, 32), make([]byte, 32))
	y := linkCode("us_a", "ca", "us_m", "cb", make([]byte, 32), make([]byte, 32))
	if x == y || len(x) != 7 || x[3] != ' ' {
		t.Fatalf("codes %q %q", x, y)
	}
}

func TestOwnerCheck(t *testing.T) {
	a := startComputer(t)
	gid := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "G"}}}))
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	c := &conn{d: a.d, peer: "us_other"}
	for _, tc := range []struct {
		msg *pb.ClientMessage
		ok  bool
	}{
		{&pb.ClientMessage{Msg: &pb.ClientMessage_RenameGroup{RenameGroup: &pb.RenameGroup{GroupId: gid, Name: "x"}}}, true},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: gid}}}, true},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{ComputerId: a.uid}}}, true},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{ComputerId: "us_third"}}}, false},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_ListSessions{ListSessions: &pb.ListSessions{}}}, true},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_RenameGroup{RenameGroup: &pb.RenameGroup{GroupId: "gp_elsewhere"}}}, false},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: "x"}}}, false},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_RequestGrant{RequestGrant: &pb.RequestGrant{}}}, false},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_SetRemoteAccess{SetRemoteAccess: &pb.SetRemoteAccess{}}}, false},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_UnlinkComputer{UnlinkComputer: &pb.UnlinkComputer{}}}, false},
		{&pb.ClientMessage{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: gid}}}, false},
	} {
		_, err := a.d.ownerCheck(c, tc.msg)
		if (err == nil) != tc.ok {
			t.Errorf("%T: err %v, want ok=%v", tc.msg.Msg, err, tc.ok)
		}
	}
}

// fakeLinked is a linked computer's link on d with one pane needing you,
// online, without a connection behind it.
func fakeLinked(t *testing.T, d *Daemon) *peerLink {
	t.Helper()
	l := newLink(d, &pb.InviteToken{HostName: "Studio", HostUserId: "us_studio", CertSha256: "cert_studio"})
	l.linked, l.online = true, true
	d.links["cert_studio"] = l
	return l
}

func linkedState(full bool, state pb.AgentState) *pb.StateUpdate {
	return &pb.StateUpdate{Full: full,
		Groups:    []*pb.Group{{Id: "gp_s", Name: "S", TabIds: []string{"tb_s"}}},
		Tabs:      []*pb.Tab{{Id: "tb_s", GroupId: "gp_s"}},
		Panes:     []*pb.Pane{{Id: "pn_s", TabId: "tb_s"}},
		Attention: []*pb.AttentionItem{{Id: "at_pn_s", PaneId: "pn_s", State: state}},
	}
}

// A linked computer's agents push to phones once per change: not again on
// each sign-in (a full update), but again when the item changes in place.
func TestLinkedAttentionPushes(t *testing.T) {
	a := startComputer(t)
	var got []string
	pushed = func(event, collapse string) { got = append(got, event) }
	t.Cleanup(func() { pushed = nil })
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	l := fakeLinked(t, a.d)
	l.applyLocked(linkedState(true, pb.AgentState_AGENT_STATE_NEEDS_INPUT), true)
	l.applyLocked(linkedState(true, pb.AgentState_AGENT_STATE_NEEDS_INPUT), true) // reconnected
	if len(got) != 1 || got[0] != "needs_input" {
		t.Fatalf("after a reconnect: pushes %v, want one needs_input", got)
	}
	// Replaced in place (same id): errored is news.
	l.applyLocked(linkedState(false, pb.AgentState_AGENT_STATE_ERRORED), true)
	l.applyLocked(linkedState(true, pb.AgentState_AGENT_STATE_ERRORED), true)
	if len(got) != 2 || got[1] != "errored" {
		t.Fatalf("after a change: pushes %v, want needs_input then errored", got)
	}
	// Raised again in the same state (a new time): news, as on the host.
	again := linkedState(false, pb.AgentState_AGENT_STATE_ERRORED)
	again.Attention[0].CreatedUnixMs = 42
	l.applyLocked(again, true)
	if len(got) != 3 {
		t.Fatalf("raised again: pushes %v, want three", got)
	}
	got = got[:2]
	// The cache, offline, isn't news either.
	l.online = false
	l.applyLocked(linkedState(true, pb.AgentState_AGENT_STATE_IDLE), true)
	if len(got) != 2 {
		t.Fatalf("offline: pushes %v", got)
	}
}

// A closed link (unlinked) shows nothing more, and doesn't save its cache.
func TestClosedLinkStaysClosed(t *testing.T) {
	a := startComputer(t)
	a.d.mu.Lock()
	l := fakeLinked(t, a.d)
	l.applyLocked(linkedState(true, pb.AgentState_AGENT_STATE_NEEDS_INPUT), true)
	a.d.closeLink("cert_studio", l)
	if l.current() || a.d.routes["gp_s"] != nil {
		t.Fatal("closed link still current, or its Group still routed")
	}
	a.d.mu.Unlock()
	l.setOffline()
	if recs, _ := a.d.store.Records("peercache"); recs["cert_studio"] != nil {
		t.Fatal("a closed link saved its cache")
	}
}

// A client connecting after the code showed still sees it.
func TestFullStateHasLinkRequests(t *testing.T) {
	a := startComputer(t)
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	a.d.comp.attempts["lk_1"] = &linkAttempt{req: &pb.LinkRequest{Id: "lk_1", Code: "123 456", Incoming: true}}
	a.d.comp.attempts["lk_2"] = &linkAttempt{req: &pb.LinkRequest{Id: "lk_2"}} // no code yet
	if rs := a.d.fullState().LinkRequests; len(rs) != 1 || rs[0].Id != "lk_1" {
		t.Fatalf("full state's link requests: %v", rs)
	}
}

// Only your own Tailscale user's computers may ask to link.
func TestLinkRefusesOtherTailscaleUsers(t *testing.T) {
	setWhoIs(t, func(ip string) (tsSource, error) {
		return tsSource{host: "mallory-mbp", login: "mallory@example.com"}, nil
	})
	a, b := startComputer(t), startComputer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_LinkComputer{LinkComputer: &pb.LinkComputer{Address: b.addr}}})
	if err == nil || !strings.Contains(err.Error(), "only your own computers") {
		t.Fatalf("asking: %v", err)
	}
	// Asked directly (an asking side that doesn't check): refused there too.
	c := &conn{d: b.d, peer: a.uid, remote: true, srcIP: "100.64.0.9", out: make(chan *pb.ServerMessage, 4), cancel: func() {}}
	a.d.mu.Lock()
	self := a.d.comp.self
	a.d.mu.Unlock()
	hello := &pb.PeerLinkHello{Commitment: make([]byte, 32), Computer: &pb.Computer{UserId: a.uid, CertSha256: "x", Addresses: []string{"100.64.0.9:7433"}, Name: self.Name}}
	if _, err := b.d.peerLinkHelloFrom(c, 7, hello); err != errDeferred {
		t.Fatalf("hello: %v", err)
	}
	select {
	case r := <-c.out:
		if r.ReplyTo != 7 || !strings.Contains(r.GetError().GetMessage(), "own Tailscale user") {
			t.Fatalf("hello from another Tailscale user: %v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer")
	}
	b.d.mu.Lock()
	n := b.d.linkedCount() + len(b.d.comp.attempts)
	b.d.mu.Unlock()
	if n != 0 {
		t.Fatal("linked, or asked, from another Tailscale user")
	}
}

func TestLinkAskRateLimit(t *testing.T) {
	old := linkAskEvery
	linkAskEvery = time.Minute
	t.Cleanup(func() { linkAskEvery = old })
	a := startComputer(t)
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	ask := func(peer, ip string) error {
		return a.d.mayAskToLink(&conn{d: a.d, peer: peer, remote: true, srcIP: ip})
	}
	if err := ask("us_1", "100.64.0.1"); err != nil {
		t.Fatal(err)
	}
	if ask("us_1", "100.64.0.2") == nil || ask("us_2", "100.64.0.1") == nil {
		t.Fatal("asked again at once, by key or by address")
	}
	for i := range linkAsksMax {
		a.d.comp.attempts[fmt.Sprint(i)] = &linkAttempt{c: &conn{}, peer: fmt.Sprint("us_w", i), req: &pb.LinkRequest{}}
	}
	if err := ask("us_3", "100.64.0.3"); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("with %d waiting: %v", linkAsksMax, err)
	}
}

// Agents typing (MCP, hmk send) or a TUI over SSH don't make this the
// computer in use: only lastInputAt, a person at it, does.
func TestAgentInputIsntUse(t *testing.T) {
	a := startComputer(t)
	a.d.mu.Lock()
	agent := &conn{d: a.d, kind: "mcp", lastInput: time.Now()}
	a.d.conns[agent] = struct{}{}
	before := a.d.comp.self.LastInputUnixMs
	a.d.mu.Unlock()
	a.d.noteInput(time.Time{}) // nobody at the keyboard (or the screen's locked)
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	delete(a.d.conns, agent)
	if a.d.comp.self.LastInputUnixMs != before {
		t.Fatal("an agent's typing counted as using this computer")
	}
}

// With nothing linked, startup doesn't touch this computer's record.
func TestNoSelfRecordUnlinked(t *testing.T) {
	a := startComputer(t)
	time.Sleep(200 * time.Millisecond) // the hardware refresh
	if recs, _ := a.d.store.Records("computerself"); len(recs) != 0 {
		t.Fatal("saved this computer's record with nothing linked")
	}
	a.d.mu.Lock()
	self := a.d.computersState(false).List[0]
	a.d.mu.Unlock()
	if self.UserId != a.uid || self.Name == "" {
		t.Fatalf("this computer, unlinked: %+v", self)
	}
}

// restartComputer shuts tc's daemon down and starts it again on the same
// state and address, as after sleep or a restart.
func restartComputer(t *testing.T, tc *testComputer) *testComputer {
	t.Helper()
	tc.c.Close()
	tc.d.Shutdown()
	var d *Daemon
	var err error
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		d, err = New(Config{Dir: tc.dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Remote: true, RemoteListen: []string{tc.addr}})
		if err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(d.Shutdown)
	for deadline := time.Now().Add(5 * time.Second); len(d.RemoteAddrs()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("remote listener did not restart")
		}
	}
	c := dial(t, tc.dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	return &testComputer{d: d, dir: tc.dir, addr: tc.addr, c: c, uid: tc.uid}
}

// Remote access stays on while computers are linked (they reach this one
// through it), and Stop All Sharing leaves them connected.
func TestLinkedNeedsRemoteAndSurvivesStopSharing(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, a, "a knows b", hasComputer(b.uid, true))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SetRemoteAccess{SetRemoteAccess: &pb.SetRemoteAccess{}}})
	if err == nil || !strings.Contains(err.Error(), "unlink them first") {
		t.Fatalf("remote off while linked: %v", err)
	}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_StopSharing{StopSharing: &pb.StopSharing{}}})
	time.Sleep(300 * time.Millisecond)
	waitComputers(t, a, "b still online on a", hasComputer(b.uid, true))
	b.d.mu.Lock()
	l := b.d.linkOf(a.uid)
	online := l != nil && l.online
	b.d.mu.Unlock()
	if !online {
		t.Fatal("stopping sharing dropped b's link to a")
	}
}

// An incoming attempt that ends (expired, asked again) answers the asking
// computer's waiting reveal and confirm, instead of leaving them hanging.
func TestEndedLinkAnswersAsker(t *testing.T) {
	a := startComputer(t)
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	c := &conn{d: a.d, peer: "us_asker", out: make(chan *pb.ServerMessage, 4), cancel: func() {}}
	a.d.comp.attempts["lk_x"] = &linkAttempt{c: c, peer: "us_asker", revealID: 5, confirmID: 6, confirmed: true, req: &pb.LinkRequest{Id: "lk_x", Code: "111 222"}}
	a.d.resolveLink("lk_x", "nobody checked the code in time")
	got := map[uint64]string{}
	for len(c.out) > 0 {
		r := <-c.out
		got[r.ReplyTo] = r.GetError().GetMessage()
	}
	if got[5] == "" || got[6] == "" {
		t.Fatalf("answers to the asker: %v", got)
	}
}

// An asker that hangs up while Tailscale is asked leaves no attempt behind
// (it would count against new asks for ten minutes).
func TestLinkAskerGoneDuringCheck(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	release := make(chan struct{})
	setWhoIs(t, func(string) (tsSource, error) {
		<-release
		return tsSource{host: "x", login: "you@example.com", mine: true}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	c := &conn{d: b.d, peer: a.uid, remote: true, srcIP: "100.64.0.9", out: make(chan *pb.ServerMessage, 4), ctx: ctx, cancel: cancel}
	b.d.mu.Lock()
	b.d.conns[c] = struct{}{}
	b.d.mu.Unlock()
	hello := &pb.PeerLinkHello{Commitment: make([]byte, 32), Computer: &pb.Computer{UserId: a.uid, CertSha256: "x", Addresses: []string{"100.64.0.9:7433"}}}
	if _, err := b.d.peerLinkHelloFrom(c, 7, hello); err != errDeferred {
		t.Fatal(err)
	}
	// It hangs up (as conn.close does: cancel, then forget it).
	cancel()
	b.d.mu.Lock()
	delete(b.d.conns, c)
	b.d.linkConnClosed(c)
	b.d.mu.Unlock()
	close(release)
	time.Sleep(200 * time.Millisecond)
	b.d.mu.Lock()
	n := len(b.d.comp.attempts)
	b.d.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d attempts left behind", n)
	}
}

// An agent started with no Group goes in the folder's Group, or a new one
// (not the first Group, whatever its folder).
func TestStartAgentFolderGroup(t *testing.T) {
	a := startComputer(t)
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Other"}}})
	dir := t.TempDir()
	r := call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{Harness: "claude", Cwd: dir}}})
	tab := client.CreatedID(r)
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	g := a.d.groups[a.d.tabs[tab].GetGroupId()]
	if g == nil || g.Name == "Other" {
		t.Fatalf("started in %v, want a new Group for its folder", g)
	}
}

// An agent on A can read B's panes, but closing or merging B's tabs (and
// so every process in them) asks the person, as for panes it didn't open
// here: it isn't waved through as "no such tab here".
func TestAgentCantCloseLinkedTab(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	ag := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "A"}}}))
	at := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: ag, Pane: cat}}}))
	bg := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "B"}}}))
	bt := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: bg, Pane: cat}}}))
	link(t, a, b)
	waitGroup(t, a, "b's group on a", func(g *pb.Group) bool { return g.Id == bg })
	var bPane string
	for deadline := time.Now().Add(10 * time.Second); bPane == ""; time.Sleep(20 * time.Millisecond) {
		a.d.mu.Lock()
		if l := a.d.routes[bt]; l != nil && l.tabs[bt] != nil {
			bPane = l.tabs[bt].FocusedPaneId
		}
		a.d.mu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("b's tab never reached a")
		}
	}
	a.d.mu.Lock()
	agentPane := a.d.tabs[at].FocusedPaneId
	a.d.mu.Unlock()
	agent := dialAs(t, a.dir, &caller{pane: agentPane, agent: true}, "cli")

	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadPane{ReadPane: &pb.ReadPane{PaneId: bPane}}}); err != nil {
		t.Fatalf("reading b's pane: %v", err)
	}
	for _, m := range []*pb.ClientMessage{
		{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: bt}}},
		{Msg: &pb.ClientMessage_MergeTab{MergeTab: &pb.MergeTab{TabId: bt, IntoPaneId: agentPane}}},
	} {
		if _, err := try(agent, m); err == nil || !strings.Contains(err.Error(), "out of scope") {
			t.Fatalf("%T from an agent: %v", m.Msg, err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	b.d.mu.Lock()
	_, still := b.d.tabs[bt]
	b.d.mu.Unlock()
	if !still {
		t.Fatal("an agent on a closed a tab on b")
	}
}

// stop shuts tc's daemon down (it's off, or asleep).
func stop(tc *testComputer) {
	tc.c.Close()
	tc.d.Shutdown()
}

// holdComputers checks that tc's list satisfies fn at every look for d (a
// condition that must stay true, not just become so).
func holdComputers(t *testing.T, tc *testComputer, what string, d time.Duration, fn func(*pb.Computers) bool) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		tc.d.mu.Lock()
		cs := tc.d.computersState(false)
		tc.d.mu.Unlock()
		if !fn(cs) {
			t.Fatalf("%s: no longer so: %v", what, cs)
		}
	}
}

// openApp connects a Mac app client to tc (it shows notifications there).
func openApp(t *testing.T, tc *testComputer) *client.Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app, err := client.Dial(ctx, SocketPath(tc.dir), "mac", "Hammock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	call(t, app, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	go func() {
		for range app.Events {
		}
	}()
	return app
}

// Whether a computer's app is open reaches the others, both ways: B makes A
// the one that notifies only while A's app is open.
func TestAppOpenReachesOthers(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	app := openApp(t, a) // open before linking: the link's hello says so
	link(t, a, b)
	active := func(want string) func(*pb.Computers) bool {
		return func(cs *pb.Computers) bool { return cs.ActiveId == want }
	}
	waitComputers(t, b, "a (app open) is active", active(a.uid))
	app.Close()
	waitComputers(t, b, "a's app closed: nobody's active", active(""))
	openApp(t, a)
	waitComputers(t, b, "a's app open again", active(a.uid))
	openApp(t, b)             // b used more recently than a (a's never been used)
	b.d.noteInput(time.Now()) // someone at b's keyboard
	waitComputers(t, b, "b, app open and used, is active", active(b.uid))
}

// Input times go to the others only when they matter: not at all from a
// computer whose app is closed, and not every tick from the active one.
func TestInputTimesThrottled(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, b, "linked", hasComputer(a.uid, true))
	seenOnB := func() int64 {
		b.d.mu.Lock()
		defer b.d.mu.Unlock()
		return b.d.comp.links[a.uid].GetRecord().GetLastInputUnixMs()
	}
	a.d.noteInput(time.Now().Add(-2 * time.Minute)) // a's app is closed
	time.Sleep(300 * time.Millisecond)
	if seenOnB() != 0 {
		t.Fatal("a computer that can't notify sent its input time")
	}
	openApp(t, a) // sent with the app opening
	waitComputers(t, b, "a active", func(cs *pb.Computers) bool { return cs.ActiveId == a.uid })
	first := seenOnB()
	if first == 0 {
		t.Fatal("a's input time didn't come with its app opening")
	}
	a.d.noteInput(time.Now().Add(-time.Minute)) // already the active one, a moment later
	time.Sleep(300 * time.Millisecond)
	if seenOnB() != first {
		t.Fatal("the active computer sent its input time again at once")
	}
}

// The input time the throttle held back goes on a later tick, with no
// input since: the last burst of typing before you stop to read reaches
// the others, or they'd count you gone early and send pushes to your phone.
func TestHeldInputTimeSentLater(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, b, "linked", hasComputer(a.uid, true))
	seenOnB := func() int64 {
		b.d.mu.Lock()
		defer b.d.mu.Unlock()
		return b.d.comp.links[a.uid].GetRecord().GetLastInputUnixMs()
	}
	a.d.noteInput(time.Now().Add(-2 * time.Minute))
	openApp(t, a)
	waitComputers(t, b, "a active", func(cs *pb.Computers) bool { return cs.ActiveId == a.uid })
	first := seenOnB()
	last := time.Now().Add(-time.Minute)
	a.d.noteInput(last) // held: sent a moment ago
	time.Sleep(300 * time.Millisecond)
	if seenOnB() != first {
		t.Fatal("sent again at once")
	}
	a.d.mu.Lock()
	a.d.comp.inputAt = time.Now().Add(-inputResend - time.Second)
	a.d.mu.Unlock()
	a.d.noteInput(time.Time{}) // a later tick, nobody typing since
	deadline := time.Now().Add(5 * time.Second)
	for seenOnB() < last.UnixMilli()-1000 {
		if time.Now().After(deadline) {
			t.Fatal("the held input time never reached b")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A Mac whose screen locks stops being the one that notifies, and the
// others stop holding pushes for it, at once rather than once its idle
// time runs out: you've left it.
func TestLockedComputerHandsOver(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	openApp(t, a)
	a.d.noteInput(time.Now())
	waitComputers(t, b, "a active", func(cs *pb.Computers) bool { return cs.ActiveId == a.uid })
	waitFor(t, "b sees a in use", func() bool {
		b.d.mu.Lock()
		defer b.d.mu.Unlock()
		return b.d.inUseElsewhere()
	})
	a.d.mu.Lock()
	a.d.comp.locked = true
	a.d.appChanged()
	a.d.mu.Unlock()
	waitComputers(t, b, "a locked: nobody's active", func(cs *pb.Computers) bool { return cs.ActiveId == "" })
	b.d.mu.Lock()
	elsewhere := b.d.inUseElsewhere()
	b.d.mu.Unlock()
	if elsewhere {
		t.Fatal("a's locked, yet b still counts you there")
	}
	a.d.mu.Lock()
	a.d.comp.locked = false
	a.d.appChanged()
	a.d.mu.Unlock()
	waitComputers(t, b, "a unlocked: active again", func(cs *pb.Computers) bool { return cs.ActiveId == a.uid })
}

// An agent on B that's still waiting stays in A's Needs You when A's link
// to B reconnects (after sleep): the full update doesn't clear it.
func TestLinkedAttentionSurvivesReconnect(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	gid := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "B"}}}))
	tab := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	b.d.mu.Lock()
	pane := b.d.tabs[tab].FocusedPaneId
	b.d.mu.Unlock()
	tdir := allowTranscripts(t)
	tpath := filepath.Join(tdir, "t.jsonl")
	os.WriteFile(tpath, nil, 0o600)
	tpath, _ = filepath.EvalSymlinks(tpath)
	sendHook(t, b.dir, HookEvent{PaneID: pane, Harness: "claude", Payload: json.RawMessage(
		`{"hook_event_name":"Notification","session_id":"abc-123","transcript_path":"` + tpath + `","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`)})
	id := "at_" + pane
	var pmu sync.Mutex
	pushes := 0
	pushed = func(_, collapse string) {
		if collapse == id {
			pmu.Lock()
			pushes++
			pmu.Unlock()
		}
	}
	t.Cleanup(func() { pushed = nil })
	count := func() int { pmu.Lock(); defer pmu.Unlock(); return pushes }
	link(t, a, b)
	waitEvent(t, a.c, func(ev *pb.ServerMessage) bool {
		return slices.ContainsFunc(ev.GetState().GetAttention(), func(x *pb.AttentionItem) bool { return x.Id == id })
	})

	time.Sleep(100 * time.Millisecond)
	before := count() // a's, for its phones (b's own came before the hook)
	if before != 1 {
		t.Fatalf("%d pushes from a for b's waiting agent, want 1", before)
	}
	// The link drops and reconnects.
	a.d.mu.Lock()
	l := a.d.linkOf(b.uid)
	a.d.mu.Unlock()
	l.cmu.Lock()
	old := l.cl
	l.cmu.Unlock()
	old.Close()
	deadline := time.After(10 * time.Second)
	for back := false; !back; {
		select {
		case ev := <-a.c.Events:
			if slices.Contains(ev.GetState().GetClearedAttentionIds(), id) {
				t.Fatal("a's clients were told b's waiting agent no longer needs them")
			}
		case <-time.After(50 * time.Millisecond):
			l.cmu.Lock()
			back = l.cl != nil && l.cl != old
			l.cmu.Unlock()
		case <-deadline:
			t.Fatal("the link never came back")
		}
	}
	time.Sleep(200 * time.Millisecond)
	for len(a.c.Events) > 0 {
		if ev := <-a.c.Events; slices.Contains(ev.GetState().GetClearedAttentionIds(), id) {
			t.Fatal("cleared after the reconnect")
		}
	}
	if n := count(); n != before {
		t.Fatalf("pushes: %d before the reconnect, %d after (the same item, still waiting)", before, n)
	}
}

// Linking tells each computer's clients once: one update with the new
// computer and the request done (not the list, then introductions, then
// the request).
func TestLinkPublishesOnce(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	type seen struct {
		mu      sync.Mutex
		withNew int  // updates whose computers include the other
		inOne   bool // the first of them also resolved the request
		repeats int  // updates the same as the one before
		last    *pb.Computers
	}
	watch := func(tc *testComputer, other string) (*seen, chan struct{}) {
		s, stop := &seen{}, make(chan struct{})
		go func() {
			for {
				select {
				case <-stop:
					return
				case ev := <-tc.c.Events:
					st := ev.GetState()
					if st.GetComputers() == nil || !hasComputer(other, false)(st.GetComputers()) {
						continue
					}
					s.mu.Lock()
					if s.withNew == 0 {
						s.inOne = len(st.GetResolvedLinkIds()) > 0
					}
					if proto.Equal(s.last, st.GetComputers()) {
						s.repeats++
					}
					s.withNew++
					s.last = st.GetComputers()
					s.mu.Unlock()
				}
			}
		}()
		return s, stop
	}
	// link() reads the requests itself; start watching once both codes show.
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_LinkComputer{LinkComputer: &pb.LinkComputer{Address: b.addr}}})
		done <- err
	}()
	ra := linkReq(t, a, "")
	rb := linkReq(t, b, ra.Code)
	sa, stopA := watch(a, b.uid)
	sb, stopB := watch(b, a.uid)
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: rb.Id, Accept: true}}})
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondLink{RespondLink: &pb.RespondLink{Id: ra.Id, Accept: true}}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // the link comes up (a change: it's online)
	close(stopA)
	close(stopB)
	for name, s := range map[string]*seen{"a": sa, "b": sb} {
		s.mu.Lock()
		if !s.inOne || s.repeats > 0 {
			t.Errorf("%s: the link's update resolved the request: %v; %d of %d updates repeated the one before", name, s.inOne, s.repeats, s.withNew)
		}
		s.mu.Unlock()
	}
}

// A confirm that arrives before the reveal doesn't publish a request with
// no code, and a request ends with its resolution sent, whatever it showed.
func TestEarlyConfirmNoEmptyCode(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cl, fp, err := client.DialRemoteLearning(ctx, b.addr, "peer", "asker")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.d.peerSignIn(ctx, cl, fp, b.uid); err != nil {
		t.Fatal(err)
	}
	a.d.mu.Lock()
	a.d.refreshSelf(false)
	self := a.d.computersState(true).List[0]
	a.d.mu.Unlock()
	nonce := make([]byte, 32)
	commit := sha256.Sum256(nonce)
	if _, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerLinkHello{PeerLinkHello: &pb.PeerLinkHello{Commitment: commit[:], Computer: self}}}); err != nil {
		t.Fatal(err)
	}
	_ = cl.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerLinkConfirm{PeerLinkConfirm: &pb.PeerLinkConfirm{Accept: true}}})
	time.Sleep(200 * time.Millisecond)
	cl.Close() // gives up
	var ids []string
	waitEvent(t, b.c, func(ev *pb.ServerMessage) bool {
		for _, r := range ev.GetState().GetLinkRequests() {
			if r.Code == "" {
				t.Fatalf("a request with no code was published: %v", r)
			}
			ids = append(ids, r.Id)
		}
		return len(ev.GetState().GetResolvedLinkIds()) > 0
	})
	b.d.mu.Lock()
	n := len(b.d.comp.attempts)
	b.d.mu.Unlock()
	if n != 0 || len(ids) != 0 {
		t.Fatalf("attempts left %d, requests shown %v", n, ids)
	}
}

// Reopen Closed on A reopens a tab A closed on B, on B, in A's order of
// closes (A's own, closed after it, comes back first).
func TestReopenLinkedClose(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	bg := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On B"}}}))
	bt := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: bg, Name: "b tab", Pane: cat}}}))
	ag := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On A"}}}))
	at := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: ag, Name: "a tab", Pane: cat}}}))
	link(t, a, b)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		a.d.mu.Lock()
		l := a.d.routes[bt]
		a.d.mu.Unlock()
		if l != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("b's tab never reached a")
		}
	}
	tabsIn := func(tc *testComputer, gid string) int {
		tc.d.mu.Lock()
		defer tc.d.mu.Unlock()
		return len(tc.d.groups[gid].GetTabIds())
	}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: bt}}})
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: at}}})
	if tabsIn(b, bg) != 0 || tabsIn(a, ag) != 0 {
		t.Fatal("tabs didn't close")
	}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}})
	if tabsIn(a, ag) != 1 || tabsIn(b, bg) != 0 {
		t.Fatal("the first reopen wasn't a's own tab, closed last")
	}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}})
	if tabsIn(b, bg) != 1 {
		t.Fatal("the tab a closed on b didn't reopen there")
	}
}

// An agent that can't start (its tab can't be made) leaves no empty Group.
func TestStartAgentNoEmptyGroup(t *testing.T) {
	a := startComputer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{Harness: "claude", Cwd: t.TempDir(), TabId: "not a tab id!"}}})
	if err == nil {
		t.Fatal("started with a bad tab id")
	}
	// Not even shown for a moment.
	for end := time.After(300 * time.Millisecond); ; {
		select {
		case ev := <-a.c.Events:
			if len(ev.GetState().GetGroups()) > 0 {
				t.Fatalf("a Group flashed up: %v", ev.GetState().GetGroups())
			}
			continue
		case <-end:
		}
		break
	}
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	if n := len(a.d.groups); n != 0 {
		t.Fatalf("%d Groups left behind", n)
	}
}

// waitRouted waits until a has id (on a linked computer) routed.
func waitRouted(t *testing.T, a *testComputer, id string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		a.d.mu.Lock()
		l := a.d.routes[id]
		a.d.mu.Unlock()
		if l != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reached a", id)
		}
	}
}

// Reopen Closed keeps each computer's own order: B's person doesn't reopen
// what A's closed there, an agent's close (allowed) isn't kept on either,
// and a marker whose entry is gone there (B restarted) falls through to the
// next close in the same press.
func TestReopenAcrossComputersOrder(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	bg := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On B"}}}))
	bt1 := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: bg, Pane: cat}}}))
	bt2 := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: bg, Pane: cat}}}))
	ag := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On A"}}}))
	at := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: ag, Pane: cat}}}))
	agentTab := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: ag, Pane: cat}}}))
	link(t, a, b)
	waitRouted(t, a, bt1)
	waitRouted(t, a, bt2)
	tabsIn := func(tc *testComputer, gid string) int {
		tc.d.mu.Lock()
		defer tc.d.mu.Unlock()
		return len(tc.d.groups[gid].GetTabIds())
	}
	reopen := func(c *client.Client) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}})
		return err
	}

	// An agent on A closes bt2, with the person's permission: kept nowhere.
	a.d.mu.Lock()
	agentPane := a.d.tabs[agentTab].FocusedPaneId
	bPane := a.d.routes[bt2].tabs[bt2].FocusedPaneId
	a.d.mu.Unlock()
	agent := dialAs(t, a.dir, &caller{pane: agentPane, agent: true}, "cli")
	go func() {
		_, _ = try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_RequestGrant{RequestGrant: &pb.RequestGrant{TargetPaneId: bPane, Action: "close"}}})
	}()
	gr := waitEvent(t, a.c, func(ev *pb.ServerMessage) bool { return len(ev.GetState().GetGrants()) > 0 }).GetState().Grants[0]
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondGrant{RespondGrant: &pb.RespondGrant{Id: gr.Id, Decision: pb.RespondGrant_DECISION_ALLOW_ONCE}}})
	// It sets remember itself, as if a person had: ignored.
	if _, err := try(agent, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: bt2, Remember: true}}}); err != nil {
		t.Fatalf("an allowed agent close: %v", err)
	}
	if tabsIn(b, bg) != 1 {
		t.Fatal("the agent's close didn't happen")
	}
	if reopen(b.c) == nil || reopen(a.c) == nil {
		t.Fatal("an agent's close was kept for Reopen Closed")
	}
	b.d.mu.Lock()
	kept := len(b.d.closedTabs)
	b.d.mu.Unlock()
	if kept != 0 {
		t.Fatal("b kept an agent's close (that no marker names)")
	}

	// A's person closes A's tab, then B's bt1.
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: at}}})
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: bt1}}})
	if tabsIn(b, bg) != 0 {
		t.Fatal("bt1 didn't close")
	}
	// B's person: nothing of B's own to reopen (A's close is A's).
	if reopen(b.c) == nil || tabsIn(b, bg) != 0 {
		t.Fatal("B's Reopen Closed reopened what A closed")
	}
	// B restarts: its list (in memory) is gone, so A's marker can't be
	// reopened; A's press falls through to A's own tab.
	b = restartComputer(t, b)
	waitComputers(t, a, "b back", hasComputer(b.uid, true))
	if err := reopen(a.c); err != nil {
		t.Fatalf("reopen on a: %v", err)
	}
	if tabsIn(a, ag) != 2 {
		t.Fatal("a stale marker used up the press instead of reopening a's own tab")
	}
	if reopen(a.c) == nil {
		t.Fatal("the stale marker came back")
	}
}

// Only a computer whose app is open can be the one used last, however new
// its input: one running only hmkd (or a Linux box, which never has the
// app) never takes it, and phone pushes aren't held for it.
func TestOnlyNotifyingComputersActive(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	openApp(t, a)
	bApp := openApp(t, b)
	active := func(want string) func(*pb.Computers) bool {
		return func(cs *pb.Computers) bool { return cs.ActiveId == want }
	}
	elsewhere := func() bool {
		a.d.mu.Lock()
		defer a.d.mu.Unlock()
		return a.d.inUseElsewhere()
	}
	b.d.noteInput(time.Now()) // someone at b, its app open
	waitComputers(t, a, "b, used last with its app open, notifies", active(b.uid))
	if !elsewhere() {
		t.Fatal("a doesn't hold pushes while b (which notifies) is in use")
	}
	bApp.Close() // b now runs only hmkd
	waitComputers(t, a, "b's app closed: a notifies", active(a.uid))
	b.d.noteInput(time.Now().Add(time.Minute)) // b used again, newer still
	holdComputers(t, a, "a stays the one that notifies", time.Second, active(a.uid))
	if elsewhere() {
		t.Fatal("a holds pushes for b, which can't notify")
	}
}

// A new subscriber's first message is its full state, before any delta
// (the app connecting is one, once computers are linked).
func TestSubscribeStateFirst(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, a, "linked", hasComputer(b.uid, true))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app, err := client.Dial(ctx, SocketPath(a.dir), "mac", "Hammock")
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	// Asked without an id, the full state comes as an event: the first.
	if err := app.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-app.Events:
		if !ev.GetState().GetFull() {
			t.Fatalf("first message to a new subscriber: %v", ev)
		}
	case <-ctx.Done():
		t.Fatal("no state")
	}
}

// An agent whose pane can't start (the holder won't spawn) leaves no
// Group, not even one shown for a moment.
func TestStartAgentSpawnFailureNoGroup(t *testing.T) {
	dir := shortDir(t)
	d, err := New(Config{Dir: dir, HolderPath: filepath.Join(dir, "no-such-pane-holder"), Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(d.Shutdown)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if nc, err := net.Dial("unix", SocketPath(dir)); err == nil {
			nc.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
	}
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{Harness: "claude", Cwd: t.TempDir()}}}); err == nil {
		t.Fatal("started with no pane holder")
	}
	for end := time.After(300 * time.Millisecond); ; {
		select {
		case ev := <-c.Events:
			if len(ev.GetState().GetGroups()) > 0 {
				t.Fatalf("a Group flashed up: %v", ev.GetState().GetGroups())
			}
			continue
		case <-end:
		}
		break
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if n := len(d.groups); n != 0 {
		t.Fatalf("%d Groups left behind", n)
	}
}

// A tab closed on another computer that's now off stays the next thing to
// reopen: the press says so, and doesn't reopen an older close instead.
// And a close here made while one there is on its way stays in order.
func TestReopenLinkedOfflineAndOrder(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	bg := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On B"}}}))
	bt1 := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: bg, Pane: cat}}}))
	bt2 := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: bg, Pane: cat}}}))
	ag := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On A"}}}))
	at1 := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: ag, Pane: cat}}}))
	at2 := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: ag, Pane: cat}}}))
	link(t, a, b)
	waitRouted(t, a, bt1)
	waitRouted(t, a, bt2)
	tabsIn := func(tc *testComputer, gid string) int {
		tc.d.mu.Lock()
		defer tc.d.mu.Unlock()
		return len(tc.d.groups[gid].GetTabIds())
	}
	reopen := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}})
		return err
	}

	// Order: B's bt1 is asked to close (no waiting for it), then A's at1
	// closes here at once. at1 is the newer, so it reopens first.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: bt1}}}); err != nil {
		t.Fatal(err)
	}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: at1}}})
	for deadline := time.Now().Add(5 * time.Second); tabsIn(b, bg) != 1; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("bt1 didn't close")
		}
	}
	time.Sleep(100 * time.Millisecond) // its answer is in
	if err := reopen(); err != nil || tabsIn(a, ag) != 2 || tabsIn(b, bg) != 1 {
		t.Fatalf("the first reopen wasn't a's at1 (closed last): %v", err)
	}
	if err := reopen(); err != nil || tabsIn(b, bg) != 2 {
		t.Fatalf("the second reopen wasn't bt1 on b: %v", err)
	}

	// Now A closes at2, then B's bt2; B goes off. Reopening says B is
	// offline, and leaves at2 alone.
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: at2}}})
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: bt2}}})
	stop(b)
	waitComputers(t, a, "b off", func(cs *pb.Computers) bool { return !hasComputer(b.uid, true)(cs) })
	if err := reopen(); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("reopening a close on an offline computer: %v", err)
	}
	if tabsIn(a, ag) != 1 {
		t.Fatal("an older close here was reopened instead")
	}
	// B back (its list, in memory, is gone): the marker falls through to at2.
	b = restartComputer(t, b)
	waitComputers(t, a, "b back", hasComputer(b.uid, true))
	if err := reopen(); err != nil || tabsIn(a, ag) != 2 {
		t.Fatalf("after b's restart, the next reopen wasn't at2: %v", err)
	}
}

// A close asked for on a computer that's off never happened: it leaves no
// marker in your order. (Here routing refuses it before a marker's place is
// taken; the drop in conn.go covers the moment the link is up but sending
// fails, which this can't arrange.)
func TestReopenNoMarkerForUnsentClose(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	bg := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On B"}}}))
	bt := client.CreatedID(call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: bg, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	link(t, a, b)
	waitRouted(t, a, bt)
	stop(b)
	waitComputers(t, a, "b off", func(cs *pb.Computers) bool { return !hasComputer(b.uid, true)(cs) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: bt}}}); err == nil {
		t.Fatal("closed a tab on a computer that's off")
	}
	a.d.mu.Lock()
	n := len(a.d.closedTabs)
	a.d.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d markers left for a close that never happened", n)
	}
}

// A clock running ahead can't make a computer the one in use: an input
// time ahead of its own now is ignored, and what it sends is how long it's
// been idle, which the others turn into a time on their own clocks.
func TestInputClockAhead(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	openApp(t, a)
	openApp(t, b)
	a.d.noteInput(time.Now()) // someone at a
	waitComputers(t, b, "a active", func(cs *pb.Computers) bool { return cs.ActiveId == a.uid })
	b.d.noteInput(time.Now().Add(time.Hour)) // b's clock jumped ahead
	holdComputers(t, a, "a stays active", time.Second, func(cs *pb.Computers) bool { return cs.ActiveId == a.uid })
	b.d.mu.Lock()
	future := b.d.comp.self.LastInputUnixMs > time.Now().UnixMilli()
	b.d.mu.Unlock()
	if future {
		t.Fatal("b kept an input time ahead of its clock")
	}
	time.Sleep(20 * time.Millisecond)
	b.d.noteInput(time.Now()) // someone at b now
	waitComputers(t, a, "b active", func(cs *pb.Computers) bool {
		for _, c := range cs.List {
			if c.UserId == b.uid && c.LastInputUnixMs > time.Now().UnixMilli()+1000 {
				return false // a time ahead of a's own clock
			}
		}
		return cs.ActiveId == b.uid
	})
}

// A linked computer's closes here have their own place on Reopen Closed:
// many of them don't push this computer's own history out.
func TestReopenLinkedClosesOwnCap(t *testing.T) {
	a, b := startComputer(t), startComputer(t)
	cat := &pb.PaneSpec{Command: []string{"/bin/cat"}}
	ag := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "On A"}}}))
	mine := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: ag, Name: "mine", Pane: cat}}}))
	var theirs []string
	for range maxClosedTabs + 3 {
		theirs = append(theirs, client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: ag, Pane: cat}}})))
	}
	link(t, a, b)
	for _, id := range theirs {
		waitRouted(t, b, id)
	}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: mine}}}) // a's own
	for _, id := range theirs {
		call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: id}}}) // b's person, on a
	}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}})
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	found := false
	for _, tid := range a.d.groups[ag].GetTabIds() {
		found = found || a.d.tabs[tid].GetName() == "mine"
	}
	if !found {
		t.Fatal("a's own close was pushed out by b's")
	}
	byB := 0
	for _, ct := range a.d.closedTabs {
		if ct.by == b.uid {
			byB++
		}
	}
	if byB > maxClosedTabs {
		t.Fatalf("%d of b's closes kept, want at most %d", byB, maxClosedTabs)
	}
}

// From a Tailscale address: refused when Tailscale can't be asked (a shared
// tailnet's others mustn't get to ask); off Tailscale when Tailscale says
// it isn't on the tailnet (another VPN in its range).
func TestWhoIsAskingTailscaleDown(t *testing.T) {
	tsCache.Lock()
	was := tsCache.net
	tsCache.net.own = []net.IP{net.ParseIP("100.86.1.1")}
	tsCache.Unlock()
	t.Cleanup(func() { tsCache.Lock(); tsCache.net = was; tsCache.Unlock() })
	setWhoIs(t, func(string) (tsSource, error) { return tsSource{}, errors.New("tailscale: timed out") })
	if _, err := whoIsAsking(context.Background(), "100.64.0.9"); err == nil {
		t.Error("Tailscale couldn't be asked, yet let through")
	}
	setWhoIs(t, func(string) (tsSource, error) { return tsSource{}, errNotOnTailnet })
	if src, err := whoIsAsking(context.Background(), "100.96.0.2"); err != nil || src.source == "" {
		t.Errorf("another VPN in Tailscale's range: %v %+v", err, src)
	}
}

func TestValidIcon(t *testing.T) {
	for _, ok := range []string{"", "macmini", "macpro.gen3.server", "server.rack", "💻", "🖥️", "🏳️‍🌈", "#️⃣", "🇬🇧"} {
		if !validIcon(ok) {
			t.Errorf("validIcon(%q) = false", ok)
		}
	}
	for _, bad := range []string{"Mac Mini", "../etc", "mac mini", "a\nb", "💻\n", "macmini.", ".x", strings.Repeat("😀", 20)} {
		if validIcon(bad) {
			t.Errorf("validIcon(%q) = true", bad)
		}
	}
}
