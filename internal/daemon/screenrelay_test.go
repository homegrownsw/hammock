package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func screenSharingOn(t *testing.T) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("HMK_CONFIG_DIR", cfg)
	if err := os.WriteFile(filepath.Join(cfg, "config"), []byte("screen-sharing = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func screenSessions(d *Daemon) []*mediaSession {
	return d.media.matching(func(s *mediaSession) bool { return strings.HasPrefix(s.paneID, "screen:") })
}

// Two viewers on linked Mac A watching Mac B's screen through A share A's
// one connection to B, but B tells them apart: each viewer's End ends only
// its own sessions, and input is taken only from a viewer watching.
func TestRelayedScreenViewersApart(t *testing.T) {
	screenSharingOn(t)
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, a, "B online on A", hasComputer(b.uid, true))
	enc := &fakeEncoder{d: b.d, s: map[string]*fakeSession{}}
	b.d.media.screenSource = func() frameSource { return enc }

	one, two, idle := a.c, dial(t, a.dir), dial(t, a.dir)
	open := func(bg bool) *pb.ClientMessage {
		return &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{Screen: true, InBand: true, ComputerId: b.uid, Background: bg}}}
	}
	call(t, one, open(false))
	call(t, one, open(true)) // its background layer
	sidTwo := call(t, two, open(false)).GetMediaTicket().SessionId

	var ss []*mediaSession
	for deadline := time.Now().Add(5 * time.Second); len(ss) < 3; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("sessions on B: %d", len(ss))
		}
		ss = screenSessions(b.d)
	}
	viewers := map[string]int{}
	for _, s := range ss {
		if s.owner != ss[0].owner {
			t.Fatal("relayed sessions on more than A's one connection")
		}
		viewers[s.viewer]++
	}
	if len(viewers) != 2 || viewers[""] != 0 {
		t.Fatalf("viewers on B: %v", viewers)
	}

	st := call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_ServerStatus{ServerStatus: &pb.ServerStatus{}}}).GetServerStatusReply()
	if len(st.ScreenViewers) != 2 {
		t.Fatalf("menu bar viewers: %+v", st.ScreenViewers)
	}
	for _, v := range st.ScreenViewers {
		if v.Kind != "computer" || v.Name == "" {
			t.Fatalf("A's own app as a viewer: %+v", v)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := &pb.ClientMessage{Msg: &pb.ClientMessage_ScreenInput{ScreenInput: &pb.ScreenInput{ComputerId: b.uid, Op: "mouse", ArgsJson: `{"type":"move","x":0.5,"y":0.5}`}}}
	if _, err := idle.Call(ctx, input); err == nil || !strings.Contains(err.Error(), "open the screen") {
		t.Fatalf("input from a client of A's not watching: %v", err)
	}
	// Watching: it reaches B's screen host (not running here), as two's.
	if _, err := two.Call(ctx, input); err != nil && strings.Contains(err.Error(), "open the screen") {
		t.Fatalf("input from a watching viewer: %v", err)
	}
	for _, s := range screenSessions(b.d) {
		s.mu.Lock()
		controlling := time.Since(s.inputAt) < 5*time.Second
		s.mu.Unlock()
		if controlling != (s.viewer == viewerOf(t, a, sidTwo)) {
			t.Fatalf("session %s (viewer %s) controlling %v", s.id, s.viewer, controlling)
		}
	}

	// End two's session on B: only two's ends.
	var twoOnB string
	for _, v := range st.ScreenViewers {
		for _, s := range screenSessions(b.d) {
			if s.id == v.SessionId && s.viewer == viewerOf(t, a, sidTwo) {
				twoOnB = s.id
			}
		}
	}
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_EndScreenSession{EndScreenSession: &pb.EndScreenSession{SessionId: twoOnB}}})
	if ended := waitEvent(t, two, func(ev *pb.ServerMessage) bool { return ev.GetMediaEnded() != nil }).GetMediaEnded(); ended.SessionId != sidTwo {
		t.Fatalf("ended %+v", ended)
	}
	time.Sleep(200 * time.Millisecond)
	if left := screenSessions(b.d); len(left) != 2 {
		t.Fatalf("ending one viewer's left %d sessions on B, want the other's 2", len(left))
	}
}

// viewerOf is the viewer id A gave the client whose relayed session is sid.
func viewerOf(t *testing.T, a *testComputer, sid string) string {
	t.Helper()
	a.d.media.mu.Lock()
	r := a.d.media.relays[sid]
	a.d.media.mu.Unlock()
	if r == nil {
		t.Fatalf("no relay %s on A", sid)
	}
	a.d.mu.Lock()
	defer a.d.mu.Unlock()
	return r.owner.viewerID
}

// The host names a relayed viewer as its linked computer says (a phone of
// its), and takes no viewer naming from anyone but a linked computer.
func TestRelayedScreenViewerNaming(t *testing.T) {
	screenSharingOn(t)
	host, dir := startHost(t)
	enc := &fakeEncoder{d: host, s: map[string]*fakeSession{}}
	host.media.screenSource = func() frameSource { return enc }
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	uid := userIDFor(pub)
	host.mu.Lock()
	host.comp.links[uid] = &pb.StoredLink{Record: &pb.Computer{UserId: uid, Key: pub, Name: "Studio"}}
	host.mu.Unlock()
	peer, err := peerHelloWith(t, host, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	open := func(id, name, kind string) *pb.ClientMessage {
		return &pb.ClientMessage{Msg: &pb.ClientMessage_OpenMedia{OpenMedia: &pb.OpenMedia{Screen: true, InBand: true, ViewerId: id, ViewerName: name, ViewerKind: kind}}}
	}
	phone := call(t, peer, open("sv_phone", "Sam's iPhone", "phone")).GetMediaTicket().SessionId
	mac := call(t, peer, open("sv_mac", "", "")).GetMediaTicket().SessionId
	local := dial(t, dir)
	own := call(t, local, open("sv_spoof", "Spoof", "phone")).GetMediaTicket().SessionId

	st := call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_ServerStatus{ServerStatus: &pb.ServerStatus{}}}).GetServerStatusReply()
	got := map[string]string{}
	for _, v := range st.ScreenViewers {
		got[v.SessionId] = v.Kind + ":" + v.Name
	}
	if got[phone] != "phone:Sam's iPhone" || got[mac] != "computer:Studio" || strings.Contains(got[own], "Spoof") {
		t.Fatalf("viewers: %v", got)
	}
	// This Mac's own app watches as itself: input without a viewer id counts.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	input := &pb.ClientMessage{Msg: &pb.ClientMessage_ScreenInput{ScreenInput: &pb.ScreenInput{Op: "mouse", ArgsJson: `{"type":"move","x":0.5,"y":0.5}`}}}
	if _, err := local.Call(ctx, input); err != nil && strings.Contains(err.Error(), "open the screen") {
		t.Fatalf("local input: %v", err)
	}
	// The linked computer's input names a viewer, and only one watching counts.
	input.GetScreenInput().ViewerId = "sv_other"
	if _, err := peer.Call(ctx, input); err == nil || !strings.Contains(err.Error(), "open the screen") {
		t.Fatalf("input for a viewer not watching: %v", err)
	}
}
