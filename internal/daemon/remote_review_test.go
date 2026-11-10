package daemon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
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

// A linked computer with no key on record yet (linked by an earlier build)
// never learns it from what it says of itself, only as proven signing in.
func TestLegacyLinkLearnsOnlyProvenKey(t *testing.T) {
	host, _ := startHost(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	uid := userIDFor(pub)
	host.mu.Lock()
	host.comp.links[uid] = &pb.StoredLink{CertSha256: "cc", Record: &pb.Computer{UserId: uid, Name: "Other"}}
	l := newLink(host, &pb.InviteToken{CertSha256: "cc", HostUserId: uid})
	l.linked = true
	host.links["cc"] = l
	// Its state, claiming a key (before signIn has stored the proven one).
	l.applyLocked(&pb.StateUpdate{Computers: &pb.Computers{List: []*pb.Computer{{UserId: uid, Name: "Other", Key: pub}}}}, false)
	got := host.comp.links[uid].Record.Key
	host.mu.Unlock()
	if len(got) != 0 {
		t.Fatal("a link learned its key from its own state")
	}
	// Signing in here proves it.
	if _, err := peerHelloWith(t, host, pub, priv); err != nil {
		t.Fatal(err)
	}
	host.mu.Lock()
	got = host.comp.links[uid].Record.Key
	host.mu.Unlock()
	if !bytes.Equal(got, pub) {
		t.Fatal("the key proven signing in wasn't recorded")
	}
}

// burstTransport delivers a burst of messages at once, as if in one write,
// and records what's written back.
type burstTransport struct {
	in     chan []byte
	closed chan struct{}
	once   sync.Once
	mu     sync.Mutex
	out    [][]byte
}

func (t *burstTransport) Read(ctx context.Context) ([]byte, error) {
	select {
	case b := <-t.in:
		return b, nil
	case <-t.closed:
		return nil, io.EOF
	}
}

func (t *burstTransport) Write(ctx context.Context, b []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.out = append(t.out, b)
	return nil
}

func (t *burstTransport) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

// A failed sign-in hangs up after its answer, and nothing that arrived
// with it is handled: a burst of pairing codes is one guess, not many.
func TestFailedSignInIgnoresTheRestOfTheBurst(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	secret := make([]byte, 16)
	_, _ = rand.Read(secret)
	d.mu.Lock()
	if d.remote.pairings == nil {
		d.remote.pairings = map[string]time.Time{}
	}
	d.remote.pairings[hex.EncodeToString(secret)] = time.Now().Add(time.Minute)
	d.mu.Unlock()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	tr := &burstTransport{in: make(chan []byte, 2), closed: make(chan struct{})}
	for _, s := range [][]byte{make([]byte, 16), secret} {
		b, _ := proto.Marshal(&pb.ClientMessage{Id: 1, Msg: &pb.ClientMessage_Pair{Pair: &pb.Pair{Secret: s, PublicKey: pub, DeviceName: "x"}}})
		tr.in <- b
	}
	done := make(chan struct{})
	go func() {
		d.serveConnWith(tr, func(c *conn) { c.remote, c.srcIP = true, "10.9.9.9" })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the connection wasn't closed after a failed sign-in")
	}
	d.mu.Lock()
	_, unused := d.remote.pairings[hex.EncodeToString(secret)]
	d.mu.Unlock()
	if !unused {
		t.Fatal("a pairing code sent after a failed one, in the same burst, was tried")
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if len(tr.out) != 1 {
		t.Fatalf("%d answers written, want the failure's alone", len(tr.out))
	}
}

// A connection signs in once: a second PeerHello (as someone else, or as a
// stranger) is refused, and it stays who it was.
func TestSecondPeerHelloRefused(t *testing.T) {
	host, _, peer, uid, _, _, _ := sharedSetup(t, pb.Role_ROLE_VIEWER)
	pc := peerConn(t, host, uid)
	host.mu.Lock()
	fp, nonce := host.remote.fingerprint, pc.nonce
	host.mu.Unlock()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	hostNonce := make([]byte, client.NonceSize)
	_, _ = rand.Read(hostNonce)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := peer.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerHello{PeerHello: &pb.PeerHello{
		PublicKey: pub, Signature: ed25519.Sign(priv, client.AuthMessage(client.PeerAuthTag, fp, nonce)),
		Name: "Other", HostNonce: hostNonce,
	}}})
	if err == nil || !strings.Contains(err.Error(), "already signed in") {
		t.Fatalf("second PeerHello: %v", err)
	}
	host.mu.Lock()
	still := pc.peer == uid
	host.mu.Unlock()
	if !still {
		t.Fatal("it isn't who it was any more")
	}
	if _, err := peer.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Authenticate{Authenticate: &pb.Authenticate{DeviceId: "dv_x"}}}); err == nil || !strings.Contains(err.Error(), "already signed in") {
		t.Fatalf("Authenticate after PeerHello: %v", err)
	}
}

// A guest let back in (and watching again) before its log views out of
// reach are let go keeps them: they're judged again first.
func TestLogUnwatchRechecksReach(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	host, h, peer, uid, gid, _, _ := sharedSetup(t, pb.Role_ROLE_VIEWER)
	ltab := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid,
		Pane: &pb.PaneSpec{Log: &pb.LogSource{Kind: "command", Command: []string{`while true; do echo "INFO x"; sleep 0.1; done`}}}}}}))
	host.mu.Lock()
	logPane := host.tabs[ltab].FocusedPaneId
	host.mu.Unlock()
	call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchLog{WatchLog: &pb.WatchLog{PaneId: logPane, Watch: true}}})
	pc := peerConn(t, host, uid)
	host.mu.Lock()
	lp := host.panes[logPane].log
	// Out of reach for a moment, then back in, before the unwatch runs.
	host.tabs[ltab].GroupId = "gr_elsewhere"
	host.revokeStreams(pc)
	host.tabs[ltab].GroupId = gid
	host.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	lp.mu.Lock()
	_, watching := lp.watchers[pc]
	lp.mu.Unlock()
	if !watching {
		t.Fatal("a log view back in reach was let go")
	}
}

// Only a host menu item that copies opens the way to your clipboard; two
// clients' copies are answered each its own, in order; a client that's
// gone gets none.
func TestHostClipboardMenuAndClients(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	d.mu.Lock()
	defer d.mu.Unlock()
	l := newLink(d, &pb.InviteToken{CertSha256: "aa", HostUserId: "us_a"})
	a, b := &conn{d: d}, &conn{d: d}
	menu := func(seq int32, to *conn, items ...*pb.BrowserMenuItem) {
		l.menus[seq] = &pb.BrowserMenu{PaneId: "pn_web", Seq: seq, Kind: "context", Items: items}
		l.menuTo[seq] = to
	}
	choose := func(c *conn, seq, i int32) {
		l.askedCopy(c, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserMenuChoice{BrowserMenuChoice: &pb.BrowserMenuChoice{PaneId: "pn_web", Seq: seq, Index: i}}})
	}
	menu(1, a, &pb.BrowserMenuItem{Title: "Copy", Copies: true}, &pb.BrowserMenuItem{Title: "Back"})
	choose(a, 1, 1)
	if l.clipboardTo("pn_web") != nil {
		t.Fatal("Back opened the clipboard")
	}
	choose(b, 1, 0) // not b's menu
	if l.clipboardTo("pn_web") != nil {
		t.Fatal("another client's menu opened the clipboard")
	}
	// A host that titles anything Copy but marks none: that one only.
	menu(2, a, &pb.BrowserMenuItem{Title: "Copy", Copies: false}, &pb.BrowserMenuItem{Title: "Paste", Copies: true})
	choose(a, 2, 0)
	if l.clipboardTo("pn_web") != nil {
		t.Fatal("a title alone counted when the host marks its items")
	}
	// Two clients copying: each answer its own, oldest first.
	l.askedCopy(a, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserInput{BrowserInput: &pb.BrowserInput{PaneId: "pn_web", Op: "copy"}}})
	l.askedCopy(b, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserInput{BrowserInput: &pb.BrowserInput{PaneId: "pn_web", Op: "copy"}}})
	if l.clipboardTo("pn_web") != a || l.clipboardTo("pn_web") != b || l.clipboardTo("pn_web") != nil {
		t.Fatal("copies not answered each to its own client, in order")
	}
	// An older host (no marks): English titles.
	menu(3, b, &pb.BrowserMenuItem{Title: "Back"}, &pb.BrowserMenuItem{Title: "Copy Link Address"})
	choose(b, 3, 1)
	if l.clipboardTo("pn_web") != b {
		t.Fatal("an older host's Copy Link Address")
	}
	// A client that's gone gets nothing.
	l.askedCopy(a, &pb.ClientMessage{Msg: &pb.ClientMessage_BrowserInput{BrowserInput: &pb.BrowserInput{PaneId: "pn_web", Op: "cut"}}})
	d.links["aa"] = l
	d.localClosed(a)
	delete(d.links, "aa")
	if l.clipboardTo("pn_web") != nil {
		t.Fatal("a copy answered to a client that's gone")
	}
}

// The host marks its context menu's copying items by command, whatever
// their titles (in the Mac's language).
func TestHostMarksCopyingMenuItems(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	c := &conn{d: d, out: make(chan *pb.ServerMessage, 4)}
	d.browsers.noteInput("pn_web", c)
	d.browsers.menu(hostMsg{T: "context", ID: "pn_web", Seq: 7, Items: []hostMenuItem{{Title: "Retour", Cmd: 100}, {Title: "Copier", Cmd: 113}, {Title: "Copier l’adresse du lien", Cmd: 26501}}})
	bm := (<-c.out).GetBrowserMenu()
	if bm == nil || len(bm.Items) != 3 || bm.Items[0].Copies || !bm.Items[1].Copies || !bm.Items[2].Copies {
		t.Fatalf("menu: %v", bm)
	}
}

// A host's Group icons are an emoji or a symbol's name, nothing else; a
// pasted invite's names and a device's name arrive without controls or
// bidi overrides.
func TestCleanIconsInvitesAndDeviceNames(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	d.mu.Lock()
	l := newLink(d, &pb.InviteToken{CertSha256: "aa", HostUserId: "us_a"})
	d.links["aa"] = l
	l.applyLocked(&pb.StateUpdate{Groups: []*pb.Group{
		{Id: "gr_1", Icon: "🚀"}, {Id: "gr_2", Icon: "👨‍👩‍👧"}, {Id: "gr_3", Icon: "\x1b[31m"},
		{Id: "gr_4", Icon: "a long line of text"}, {Id: "gr_5", Icon: "🚀‮"}, {Id: "gr_6", Icon: "star.fill"},
	}}, false)
	for id, want := range map[string]string{"gr_1": "🚀", "gr_2": "👨‍👩‍👧", "gr_3": "", "gr_4": "", "gr_5": "🚀", "gr_6": "star.fill"} {
		if got := l.groups[id].Icon; got != want {
			t.Errorf("%s icon %q, want %q", id, got, want)
		}
	}
	delete(d.links, "aa")
	d.mu.Unlock()

	if got := deviceName(" Phone‮gnp.exe\x1b "); got != "Phonegnp.exe" {
		t.Errorf("device name %q", got)
	}

	tok, _ := proto.Marshal(&pb.InviteToken{HostName: "Host\x1b]0;x\x07⁦", GroupName: "G‮roup\n", Addresses: []string{"127.0.0.1:1"},
		CertSha256: "bb", HostUserId: "us_b", Secret: []byte{1}})
	if _, err := d.joinShared(&conn{d: d, out: make(chan *pb.ServerMessage, 4)}, 0, "hammock://join/"+base64.RawURLEncoding.EncodeToString(tok)); err != errDeferred {
		t.Fatal(err)
	}
	d.mu.Lock()
	jl := d.links["bb"]
	name, group := jl.tok.HostName, jl.tok.GroupName
	d.mu.Unlock()
	jl.close()
	if name != "Host]0;x" || group != "Group" {
		t.Errorf("invite names %q %q", name, group)
	}
}
