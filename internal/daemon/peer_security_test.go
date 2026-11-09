package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// startHost runs a daemon with a remote listener on loopback.
func startHost(t *testing.T) (*Daemon, string) {
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
	return d, dir
}

// A guest only signs in to the host its invite named.
func TestPeerLinkChecksHost(t *testing.T) {
	host, _ := startHost(t)
	guest := startDaemon(t, shortDir(t))
	host.mu.Lock()
	fp, hostUser := host.remote.fingerprint, host.id.me.UserId
	host.mu.Unlock()
	addrs := host.RemoteAddrs()

	good := newLink(guest, &pb.InviteToken{Addresses: addrs, CertSha256: fp, HostUserId: hostUser})
	cl, err := good.dial()
	if err != nil {
		t.Fatalf("signing in to the right host: %v", err)
	}
	cl.Close()

	bad := newLink(guest, &pb.InviteToken{Addresses: addrs, CertSha256: fp, HostUserId: "us_0000000000000000"})
	if cl, err := bad.dial(); err == nil {
		cl.Close()
		t.Fatal("signed in to a host that isn't the invite's")
	}
}

// A host's update can't describe people it has no business describing, nor
// overwrite someone known first-hand or through another host.
func TestPeerLinkPeople(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	d.mu.Lock()
	defer d.mu.Unlock()
	a := newLink(d, &pb.InviteToken{CertSha256: "aa", HostUserId: "us_a"})
	b := newLink(d, &pb.InviteToken{CertSha256: "bb", HostUserId: "us_b"})
	d.links["aa"], d.links["bb"] = a, b
	d.people["us_guest"] = &pb.Person{UserId: "us_guest", Name: "Guest"}
	a.applyLocked(&pb.StateUpdate{
		Groups: []*pb.Group{{Id: "gr_a", Members: []*pb.Member{{UserId: "us_a", Role: pb.Role_ROLE_HOST}, {UserId: "us_friend", Role: pb.Role_ROLE_DRIVER}}}},
		People: []*pb.Person{{UserId: "us_a", Name: "A"}, {UserId: "us_friend", Name: "Friend"}},
	}, false)
	b.applyLocked(&pb.StateUpdate{
		Groups: []*pb.Group{{Id: "gr_b", Members: []*pb.Member{{UserId: "us_b", Role: pb.Role_ROLE_HOST},
			{UserId: "us_friend", Role: pb.Role_ROLE_VIEWER}, {UserId: "us_guest", Role: pb.Role_ROLE_VIEWER}}}},
		People: []*pb.Person{{UserId: "us_b", Name: "B", Me: true}, {UserId: "us_friend", Name: "Impostor"},
			{UserId: "us_stranger", Name: "Stranger"}, {UserId: "us_guest", Name: "Fake guest"}, {UserId: d.id.me.UserId, Name: "Not you"}},
	}, false)
	if a.people["us_friend"].GetName() != "Friend" || b.people["us_friend"] != nil {
		t.Error("b overwrote a's person")
	}
	if b.people["us_b"] == nil || b.people["us_b"].Me || b.people["us_stranger"] != nil || b.people["us_guest"] != nil || b.people[d.id.me.UserId] != nil {
		t.Errorf("b's people: %v", b.people)
	}
}

// A host's update can't take over ids another host's link has.
func TestPeerLinkRoutes(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	d.mu.Lock()
	defer d.mu.Unlock()
	a := newLink(d, &pb.InviteToken{CertSha256: "aa", HostUserId: "us_a"})
	b := newLink(d, &pb.InviteToken{CertSha256: "bb", HostUserId: "us_b"})
	d.links["aa"], d.links["bb"] = a, b
	a.applyLocked(&pb.StateUpdate{
		Groups:    []*pb.Group{{Id: "gr_a"}},
		Tabs:      []*pb.Tab{{Id: "tb_a", GroupId: "gr_a"}},
		Panes:     []*pb.Pane{{Id: "pn_a", TabId: "tb_a"}},
		Attention: []*pb.AttentionItem{{Id: "at_a", PaneId: "pn_a"}},
	}, false)
	b.applyLocked(&pb.StateUpdate{
		Groups:    []*pb.Group{{Id: "gr_a"}, {Id: "gr_b"}},
		Tabs:      []*pb.Tab{{Id: "tb_a", GroupId: "gr_b"}},
		Panes:     []*pb.Pane{{Id: "pn_a", TabId: "tb_a"}},
		Attention: []*pb.AttentionItem{{Id: "at_a", PaneId: "pn_a"}},
	}, false)
	for _, id := range []string{"gr_a", "tb_a", "pn_a", "at_a"} {
		if d.routes[id] != a {
			t.Errorf("%s rerouted", id)
		}
	}
	if d.routes["gr_b"] != b || b.groups["gr_a"] != nil || b.tabs["tb_a"] != nil || b.panes["pn_a"] != nil || b.attn["at_a"] != nil {
		t.Error("b kept a's ids")
	}
	// Its own ids it can still update.
	a.applyLocked(&pb.StateUpdate{Groups: []*pb.Group{{Id: "gr_a", Name: "Renamed"}}}, false)
	if a.groups["gr_a"].GetName() != "Renamed" {
		t.Error("a couldn't update its own Group")
	}
}

// A host's tabs, panes and what needs you stay in its own Groups: it can't
// put a pane among this Mac's, or raise Needs You for one of them.
func TestPeerLinkStaysInItsGroups(t *testing.T) {
	d := startDaemon(t, shortDir(t))
	d.mu.Lock()
	defer d.mu.Unlock()
	d.groups["gr_mine"] = &pb.Group{Id: "gr_mine"}
	d.tabs["tb_mine"] = &pb.Tab{Id: "tb_mine", GroupId: "gr_mine"}
	a := newLink(d, &pb.InviteToken{CertSha256: "aa", HostUserId: "us_a"})
	d.links["aa"] = a
	a.applyLocked(&pb.StateUpdate{
		Groups:    []*pb.Group{{Id: "gr_a"}},
		Tabs:      []*pb.Tab{{Id: "tb_a", GroupId: "gr_a"}, {Id: "tb_in_mine", GroupId: "gr_mine"}},
		Panes:     []*pb.Pane{{Id: "pn_a", TabId: "tb_a"}, {Id: "pn_in_mine", TabId: "tb_mine"}},
		Attention: []*pb.AttentionItem{{Id: "at_a", PaneId: "pn_a"}, {Id: "at_mine", PaneId: "pn_mine"}},
	}, false)
	for _, id := range []string{"tb_in_mine", "pn_in_mine", "at_mine"} {
		if d.routes[id] != nil {
			t.Errorf("%s taken", id)
		}
	}
	if a.tabs["tb_a"] == nil || a.panes["pn_a"] == nil || a.attn["at_a"] == nil {
		t.Error("the host's own were dropped")
	}
}

// rawPeer signs in to host as a guest daemon would, with a new user key.
func rawPeer(t *testing.T, host *Daemon) (*client.Client, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host.mu.Lock()
	fp := host.remote.fingerprint
	host.mu.Unlock()
	cl, err := client.DialRemote(ctx, host.RemoteAddrs()[0], fp, "peer", "guest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	nonce := waitEvent(t, cl, func(ev *pb.ServerMessage) bool { return ev.GetAuthChallenge() != nil }).GetAuthChallenge().Nonce
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	hostNonce := make([]byte, client.NonceSize)
	_, _ = rand.Read(hostNonce)
	if _, err := cl.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerHello{PeerHello: &pb.PeerHello{
		PublicKey: pub, Signature: ed25519.Sign(priv, client.AuthMessage(client.PeerAuthTag, fp, nonce)),
		Name: "Guest", DeviceName: "guest-mac", HostNonce: hostNonce,
	}}}); err != nil {
		t.Fatal(err)
	}
	return cl, userIDFor(pub)
}

// sharedSetup makes a Group with a running pane, shares it, and lets a raw
// peer in with role r.
func sharedSetup(t *testing.T, r pb.Role) (host *Daemon, h, peer *client.Client, uid, gid, tab, paneID string) {
	t.Helper()
	return sharedSetupAs(t, r, true)
}

// sharedSetupAs is sharedSetup, with a peer that subscribes or (as a
// modified guest daemon might) never does.
func sharedSetupAs(t *testing.T, r pb.Role, subscribe bool) (host *Daemon, h, peer *client.Client, uid, gid, tab, paneID string) {
	t.Helper()
	host, dir := startHost(t)
	h = dial(t, dir)
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid = client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Shared"}}}))
	tab = client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "stty -echo; exec cat"}}}}}))
	host.mu.Lock()
	paneID = host.tabs[tab].FocusedPaneId
	host.mu.Unlock()
	link := call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: gid}}}).GetInvite().Link
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "hammock://join/"))
	tok := &pb.InviteToken{}
	if err := proto.Unmarshal(raw, tok); err != nil {
		t.Fatal(err)
	}
	peer, uid = rawPeer(t, host)
	if subscribe {
		call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := peer.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerJoin{PeerJoin: &pb.PeerJoin{Secret: tok.Secret}}})
		done <- err
	}()
	req := waitEvent(t, h, func(ev *pb.ServerMessage) bool { return len(ev.GetState().GetJoinRequests()) > 0 }).GetState().JoinRequests[0]
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_RespondJoin{RespondJoin: &pb.RespondJoin{Id: req.Id, Role: r}}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return
}

// noOutput fails if c gets output for paneID within a moment.
func noOutput(t *testing.T, c *client.Client, paneID string) {
	t.Helper()
	deadline := time.After(700 * time.Millisecond)
	for {
		select {
		case ev := <-c.Events:
			if ev.GetPaneOutput().GetPaneId() == paneID || ev.GetPaneSnapshot().GetPaneId() == paneID {
				t.Fatal("still streaming a pane out of reach")
			}
		case <-deadline:
			return
		}
	}
}

// Removing a guest, or moving a tab out of the shared Group, stops its
// streams, whether or not its daemon ever subscribed (a modified one needn't).
func TestRevokedPeerStopsStreaming(t *testing.T) {
	for _, how := range []string{"remove", "move", "stop", "remove-unsubscribed", "move-unsubscribed", "stop-unsubscribed"} {
		t.Run(how, func(t *testing.T) {
			how, unsub := strings.CutSuffix(how, "-unsubscribed")
			host, h, peer, uid, gid, tab, paneID := sharedSetupAs(t, pb.Role_ROLE_DRIVER, !unsub)
			enc := &fakeEncoder{d: host, s: map[string]*fakeSession{}}
			host.media.sourceFor = func(*pane) frameSource { return enc }
			call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{PaneId: paneID, InBand: true}}})
			call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
			call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: paneID, Watch: true}}})
			call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("before\n")}}})
			waitOutput(t, peer, paneID, "before")
			switch how {
			case "remove":
				call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_SetMember{SetMember: &pb.SetMember{GroupId: gid, UserId: uid}}})
			case "move":
				other := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Private"}}}))
				call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_MoveTab{MoveTab: &pb.MoveTab{TabId: tab, GroupId: other}}})
			case "stop":
				call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_StopSharing{StopSharing: &pb.StopSharing{GroupId: gid}}})
			}
			time.Sleep(100 * time.Millisecond)
			for len(peer.Events) > 0 {
				<-peer.Events
			}
			call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_Input{Input: &pb.Input{PaneId: paneID, Data: []byte("after\n")}}})
			noOutput(t, peer, paneID)
			// The watch may linger (for a quick return), but not with the peer on it.
			host.mu.Lock()
			w := host.agentWatches[paneID]
			host.mu.Unlock()
			if w != nil {
				w.mu.Lock()
				n := len(w.conns)
				w.mu.Unlock()
				if n > 0 {
					t.Fatal("still watching the agent of a pane out of reach")
				}
			}
			for deadline := time.Now().Add(3 * time.Second); len(host.media.matching(func(*mediaSession) bool { return true })) > 0; time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("still sending video of a pane out of reach")
				}
			}
		})
	}
}

// A guest in no Group here any more is disconnected (after the update that
// says so).
func TestRemovedPeerDisconnected(t *testing.T) {
	for _, unsub := range []bool{false, true} {
		_, h, peer, uid, gid, _, _ := sharedSetupAs(t, pb.Role_ROLE_VIEWER, !unsub)
		call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_SetMember{SetMember: &pb.SetMember{GroupId: gid, UserId: uid}}})
		select {
		case <-peer.Done():
		case <-time.After(3 * time.Second):
			t.Fatalf("removed guest (subscribed %v) still connected", !unsub)
		}
	}
	// Leaving its last Group: the answer arrives, then the connection goes.
	_, _, peer, uid, gid, _, _ := sharedSetup(t, pb.Role_ROLE_VIEWER)
	call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_SetMember{SetMember: &pb.SetMember{GroupId: gid, UserId: uid}}})
	select {
	case <-peer.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("guest that left still connected")
	}
}

// A log pane moved out of a guest's Group stops sending it entries (the
// guest, still in that Group, stays connected).
func TestRevokedPeerStopsLogView(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	host, h, peer, _, gid, _, _ := sharedSetup(t, pb.Role_ROLE_VIEWER)
	ltab := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: gid,
		Pane: &pb.PaneSpec{Log: &pb.LogSource{Kind: "command", Command: []string{`i=0; while true; do echo "INFO line $i"; i=$((i+1)); sleep 0.05; done`}}}}}}))
	host.mu.Lock()
	logPane := host.tabs[ltab].FocusedPaneId
	host.mu.Unlock()
	call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchLog{WatchLog: &pb.WatchLog{PaneId: logPane, Watch: true}}})
	waitEvent(t, peer, func(ev *pb.ServerMessage) bool {
		v := ev.GetLogView()
		return v.GetPaneId() == logPane && !v.Replace && len(v.Entries) > 0
	})
	other := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Private"}}}))
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_MoveTab{MoveTab: &pb.MoveTab{TabId: ltab, GroupId: other}}})
	time.Sleep(200 * time.Millisecond)
	for len(peer.Events) > 0 {
		<-peer.Events
	}
	deadline := time.After(time.Second)
	for {
		select {
		case ev, ok := <-peer.Events:
			if !ok {
				t.Fatal("a guest still in a Group was disconnected")
			}
			if v := ev.GetLogView(); v.GetPaneId() == logPane && len(v.Entries) > 0 {
				t.Fatal("still sending log entries of a pane out of reach")
			}
		case <-deadline:
			return
		}
	}
}

// Toggling every float acts on the tab named, so a guest can't do it to a
// tab outside its Group by naming a pane inside it.
func TestPeerFloatToggleAllChecksTab(t *testing.T) {
	_, h, peer, _, _, tab, paneID := sharedSetup(t, pb.Role_ROLE_DRIVER)
	other := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Private"}}}))
	private := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: other,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "exec cat"}}}}}))
	toggle := func(paneID, tabID string) error {
		_, err := peer.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_FloatPane{FloatPane: &pb.FloatPane{
			Action: pb.FloatPane_ACTION_TOGGLE_ALL, PaneId: paneID, TabId: tabID}}})
		return err
	}
	if err := toggle(paneID, private); err == nil {
		t.Fatal("toggled the floats of a tab outside the Group, naming a pane inside it")
	}
	if err := toggle("", private); err == nil {
		t.Fatal("toggled the floats of a tab outside the Group")
	}
	if err := toggle("", tab); err != nil {
		t.Fatalf("toggling its own tab's floats: %v", err)
	}
	m := &pb.ClientMessage{Msg: &pb.ClientMessage_FloatPane{FloatPane: &pb.FloatPane{Action: pb.FloatPane_ACTION_TOGGLE_ALL, PaneId: paneID, TabId: private}}}
	if got := msgTarget(m); got != private {
		t.Fatalf("routed by %q, not the tab", got)
	}
}

// A guest reads agent files only for an agent it's watching itself.
func TestPeerReadAgentFileNeedsWatch(t *testing.T) {
	_, h, peer, _, _, _, paneID := sharedSetup(t, pb.Role_ROLE_VIEWER)
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_WatchAgent{WatchAgent: &pb.WatchAgent{PaneId: paneID, Watch: true}}})
	_, err := peer.Call(context.Background(), &pb.ClientMessage{Msg: &pb.ClientMessage_ReadAgentFile{ReadAgentFile: &pb.ReadAgentFile{PaneId: paneID, Ref: "file:/etc/hosts"}}})
	if err == nil || !strings.Contains(err.Error(), "not watching") {
		t.Fatalf("read without watching: %v", err)
	}
}

// A viewer gets only images from the agent view, by their content; a
// driver gets any file the session referenced.
func TestViewerReadsOnlyImages(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	png := []byte("\x89PNG\r\n\x1a\n0000")
	src := write("main.go", []byte("package main"))
	disguised := write("secret.png", []byte("-----BEGIN OPENSSH PRIVATE KEY-----"))
	shot := write("shot.dat", png)
	for _, tc := range []struct {
		path          string
		viewer, ok    bool
		wantMediaType string
	}{
		{src, true, false, ""},
		{disguised, true, false, ""},
		{shot, true, true, "image/png"},
		{src, false, true, "text/"},
		{disguised, false, true, "image/png"},
	} {
		mt, _, err := readReferencedFile(tc.path, tc.viewer)
		if (err == nil) != tc.ok || (tc.ok && !strings.HasPrefix(mt, tc.wantMediaType)) {
			t.Errorf("%s (viewer %v): %q, %v", filepath.Base(tc.path), tc.viewer, mt, err)
		}
		if !tc.ok && tc.viewer && err != errViewerFile {
			t.Errorf("%s: %v, want the viewer's message", filepath.Base(tc.path), err)
		}
	}
}

// Before signing in, a remote connection learns nothing about the host, can
// only send small messages, must sign in quickly, and only a few may wait.
func TestRemotePreAuthLimits(t *testing.T) {
	host, _ := startHost(t)
	host.mu.Lock()
	fp := host.remote.fingerprint
	host.mu.Unlock()
	addr := host.RemoteAddrs()[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dialRaw := func() (*client.Client, error) { return client.DialRemote(ctx, addr, fp, "ios", "x") }

	c, err := dialRaw()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hr := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Hello{Hello: &pb.Hello{ProtocolVersion: 1}}}).GetHelloReply()
	if hr.HostName != "" || hr.UserId != "" {
		t.Fatalf("unauthenticated hello learned %q %q", hr.HostName, hr.UserId)
	}
	// A big message before signing in ends the connection.
	_ = c.Send(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Pair{Pair: &pb.Pair{Secret: make([]byte, 2<<20)}}})
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("oversized pre-auth message accepted")
	}

	// Only so many may wait from one address.
	var waiting []*client.Client
	for range maxPreAuthPerIP {
		c, err := dialRaw()
		if err != nil {
			t.Fatal(err)
		}
		waiting = append(waiting, c)
	}
	if c, err := dialRaw(); err == nil {
		c.Close()
		t.Fatal("more unauthenticated connections than allowed")
	}
	for _, c := range waiting {
		c.Close()
	}
	// …and a slot frees once one goes.
	ok := false
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if c, err := dialRaw(); err == nil {
			c.Close()
			ok = true
			break
		}
	}
	if !ok {
		t.Fatal("slots never freed")
	}
}

func TestRemoteAuthTimeout(t *testing.T) {
	old := authTimeout
	authTimeout = 300 * time.Millisecond
	defer func() { authTimeout = old }()
	host, _ := startHost(t)
	host.mu.Lock()
	fp := host.remote.fingerprint
	host.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	idle, err := client.DialRemote(ctx, host.RemoteAddrs()[0], fp, "ios", "x")
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	signedIn, _ := rawPeer(t, host)
	select {
	case <-idle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("an unauthenticated connection stayed open")
	}
	select {
	case <-signedIn.Done():
		t.Fatal("a signed-in connection was closed")
	case <-time.After(500 * time.Millisecond):
	}
}

// A peer that signs in without being a member or asking to join leaves no
// trace on disk, in the audit log or with your clients.
func TestStrangerPeerLeavesNoTrace(t *testing.T) {
	host, dir := startHost(t)
	h := dial(t, dir)
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	_, uid := rawPeer(t, host)
	host.mu.Lock()
	_, inPeople := host.people[uid]
	recs, _ := host.store.Records("person")
	host.mu.Unlock()
	if inPeople || recs[uid] != nil {
		t.Fatal("stranger remembered")
	}
	events, _ := host.store.RecentAudit(50)
	for _, e := range events {
		if e.Action == "connected" {
			t.Fatal("stranger audited")
		}
	}
	for _, p := range call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState().People {
		if p.UserId == uid {
			t.Fatal("stranger published")
		}
	}
}

// Once approved, a guest is remembered.
func TestApprovedPeerRemembered(t *testing.T) {
	host, _, _, uid, _, _, _ := sharedSetup(t, pb.Role_ROLE_VIEWER)
	host.mu.Lock()
	defer host.mu.Unlock()
	recs, _ := host.store.Records("person")
	if host.people[uid].GetName() != "Guest" || recs[uid] == nil || host.strangers[uid] != nil {
		t.Fatal("approved guest not remembered")
	}
}

// A join request tells the host about the guest's key: its fingerprint,
// whether it's new, and whether a name it's seen comes with another key.
func TestJoinKeyCheck(t *testing.T) {
	host, _, _, uid, _, _, _ := sharedSetup(t, pb.Role_ROLE_VIEWER)
	host.mu.Lock()
	defer host.mu.Unlock()
	fp, isNew, other := host.joinKeyCheck(uid, "Guest")
	if want := keyFingerprint(uid); fp != want || len(fp) != 19 || isNew || other {
		t.Fatalf("known key: %q %v %v (want %q)", fp, isNew, other, want)
	}
	if _, isNew, other := host.joinKeyCheck("us_0123456789abcdef", " guest "); !isNew || !other {
		t.Fatalf("a known name with a new key: new %v, other key %v", isNew, other)
	}
	if _, isNew, other := host.joinKeyCheck("us_0123456789abcdef", "Someone Else"); !isNew || other {
		t.Fatalf("a new person: new %v, other key %v", isNew, other)
	}
	if fp := keyFingerprint("us_3f2a91c07be41d05"); fp != "3f2a 91c0 7be4 1d05" {
		t.Fatalf("fingerprint %q", fp)
	}
}

// Where a remote connection may come from: listening on a chosen network's
// own address, anything that reached it (routed there, as a forwarded port
// is); at a Tailscale address, only Tailscale; listening on a wildcard,
// only Tailscale; on explicit addresses (tests), Tailscale and loopback.
func TestRemoteSourceOK(t *testing.T) {
	d := &Daemon{}
	for _, tc := range []struct {
		src, local string
		explicit   bool
		ok         bool
	}{
		{"100.101.102.103", "100.86.1.1", false, true},
		{"fd7a:115c:a1e0::1", "fd7a:115c:a1e0::2", false, true},
		{"192.168.1.30", "100.86.1.1", false, false},  // at Tailscale's address, from elsewhere
		{"192.168.1.30", "192.168.1.20", false, true}, // a chosen local network
		{"203.0.113.9", "192.168.1.20", false, true},  // a forwarded port
		{"192.168.1.30", "", false, false},            // a wildcard: Tailscale only
		{"192.168.1.20", "127.0.0.1", true, false},
		{"127.0.0.1", "", false, false},
		{"127.0.0.1", "127.0.0.1", true, true},
		{"100.128.0.1", "100.86.1.1", false, false},
	} {
		d.cfg.RemoteListen = nil
		if tc.explicit {
			d.cfg.RemoteListen = []string{"127.0.0.1:0"}
		}
		if got := d.remoteSourceOK(net.ParseIP(tc.src), net.ParseIP(tc.local)); got != tc.ok {
			t.Errorf("%s at %s (explicit %v): %v", tc.src, tc.local, tc.explicit, got)
		}
	}
}

// A guest can't claim one of this Mac's panes asked for something.
func TestPeerCantClaimPanes(t *testing.T) {
	host, _, peer, _, _, tab, paneID := sharedSetup(t, pb.Role_ROLE_DRIVER)
	r := call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{SplitPaneId: paneID,
		Spec: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "exec cat"}, CreatedByPaneId: paneID}}}})
	host.mu.Lock()
	defer host.mu.Unlock()
	p := host.panes[client.CreatedID(r)]
	if p == nil || p.snapshotInfo().CreatedByPaneId != "" || p.tabID() != tab {
		t.Fatalf("pane %v", p.snapshotInfo())
	}
}

// A guest showing panes it can't see doesn't mute your notifications for them.
func TestPeerPresenceStaysInScope(t *testing.T) {
	host, h, peer, _, _, _, paneID := sharedSetup(t, pb.Role_ROLE_VIEWER)
	other := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Private"}}}))
	tab := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: other,
		Pane: &pb.PaneSpec{Command: []string{"/bin/sh", "-c", "exec cat"}}}}}))
	host.mu.Lock()
	private := host.tabs[tab].FocusedPaneId
	host.mu.Unlock()
	call(t, peer, &pb.ClientMessage{Msg: &pb.ClientMessage_Presence{Presence: &pb.Presence{Foreground: true,
		VisiblePaneIds: []string{paneID, private}, FocusedPaneId: private}}})
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.watched(private) || !host.watched(paneID) {
		t.Fatalf("watched: private %v, shared %v", host.watched(private), host.watched(paneID))
	}
}

// Turning off a link turns away those still waiting on it.
func TestRevokeInviteEndsPendingJoins(t *testing.T) {
	host, dir := startHost(t)
	h := dial(t, dir)
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	gid := client.CreatedID(call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Shared"}}}))
	link := call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateInvite{CreateInvite: &pb.CreateInvite{GroupId: gid}}}).GetInvite().Link
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "hammock://join/"))
	tok := &pb.InviteToken{}
	_ = proto.Unmarshal(raw, tok)
	peer, _ := rawPeer(t, host)
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := peer.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_PeerJoin{PeerJoin: &pb.PeerJoin{Secret: tok.Secret}}})
		done <- err
	}()
	waitEvent(t, h, func(ev *pb.ServerMessage) bool { return len(ev.GetState().GetJoinRequests()) > 0 })
	call(t, h, &pb.ClientMessage{Msg: &pb.ClientMessage_RevokeInvite{RevokeInvite: &pb.RevokeInvite{GroupId: gid}}})
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("join answered yes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("join still waiting")
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	if len(host.joins) != 0 {
		t.Fatal("join request left behind")
	}
}
