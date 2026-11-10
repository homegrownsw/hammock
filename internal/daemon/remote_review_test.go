package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
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
