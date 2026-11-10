package daemon

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// A paired phone registers; when an agent needs input while nobody's at the
// Mac, the relay gets a note only the phone can read.
func TestPushWhileAway(t *testing.T) {
	sent := make(chan map[string]string, 4)
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/send" {
			sent <- body
		}
	}))
	defer relay.Close()
	// Read by held pushes' goroutines while the test changes them.
	var idle atomic.Int64
	var locked atomic.Bool
	idle.Store(int64(time.Hour))
	oldClient, oldIdle, oldLocked, oldRelays, oldPoll := pushClient, idleTime, screenLocked, pushRelays, pushPoll
	pushClient, pushRelays, pushPoll = relay.Client(), []string{relayOrigin(relay.URL)}, 50*time.Millisecond
	idleTime = func() time.Duration { return time.Duration(idle.Load()) }
	screenLocked = locked.Load
	defer func() {
		pushClient, idleTime, screenLocked, pushRelays, pushPoll = oldClient, oldIdle, oldLocked, oldRelays, oldPoll
	}()

	dir := shortDir(t)
	d, err := New(Config{Dir: dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Remote: true, RemoteListen: []string{"127.0.0.1:0"}})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	defer d.Shutdown()
	for deadline := time.Now().Add(5 * time.Second); len(d.RemoteAddrs()) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("remote listener did not start")
		}
	}
	local := dial(t, dir)
	pairing := call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}}).GetPairing()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	phone, err := client.DialRemote(ctx, d.RemoteAddrs()[0], pairing.CertSha256, "ios", "phone")
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()
	waitEvent(t, phone, func(ev *pb.ServerMessage) bool { return ev.GetAuthChallenge() != nil })
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := phone.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Pair{Pair: &pb.Pair{Secret: pairing.Secret, DeviceName: "phone", Platform: "ios", PublicKey: pub}}}); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	// Only a known relay.
	for _, u := range []string{"https://evil.example", relay.URL + "/x", "http" + strings.TrimPrefix(relay.URL, "https")} {
		if _, err := phone.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RegisterPush{RegisterPush: &pb.RegisterPush{
			RelayUrl: u, RelayId: "relay-id-1", Key: key, Events: []string{"needs_input"},
		}}}); err == nil {
			t.Fatalf("registered relay %s", u)
		}
	}
	if _, err := phone.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RegisterPush{RegisterPush: &pb.RegisterPush{
		RelayUrl: relay.URL, RelayId: "relay-id-1", Key: key, Events: []string{"needs_input"},
	}}}); err != nil {
		t.Fatal(err)
	}
	// The phone goes to the background (it would show things itself otherwise).
	call(t, phone, &pb.ClientMessage{Msg: &pb.ClientMessage_Presence{Presence: &pb.Presence{Foreground: false}}})

	call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Atlas"}}}))
	tab := client.CreatedID(call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Name: "Map Judder",
		Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	paneID := d.tabs[tab].FocusedPaneId
	d.mu.Unlock()
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(
		`{"hook_event_name":"Notification","session_id":"s","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`)})

	var got map[string]string
	select {
	case got = <-sent:
	case <-time.After(10 * time.Second):
		t.Fatal("no push sent")
	}
	// The relay sees tags, not the pane's or the item's ids.
	if got["id"] != "relay-id-1" || got["thread"] != pushTag(key, "thread", paneID) || got["collapse"] != pushTag(key, "collapse", "at_"+paneID) ||
		strings.Contains(got["thread"]+got["collapse"], paneID) {
		t.Fatalf("push %v", got)
	}
	sealed, _ := base64.StdEncoding.DecodeString(got["payload"])
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	var note pushNote
	_ = json.Unmarshal(plain, &note)
	if note.Title != "Map Judder" || note.Subtitle != "Atlas" || note.Pane != paneID || note.Host == "" ||
		note.ID != "at_"+paneID || time.Since(time.Unix(note.At, 0)) > time.Minute {
		t.Fatalf("note %+v", note)
	}

	// At the Mac (just used), nothing is pushed, even with another app in
	// front of Hammock.
	idle.Store(0)
	mac, err := client.Dial(ctx, SocketPath(dir), "mac", "Mac")
	if err != nil {
		t.Fatal(err)
	}
	defer mac.Close()
	call(t, mac, &pb.ClientMessage{Msg: &pb.ClientMessage_Presence{Presence: &pb.Presence{Foreground: false}}})
	needsYou := func() {
		sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","prompt":"go"}`)})
		sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(
			`{"hook_event_name":"Notification","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`)})
	}
	quiet := func(why string) {
		t.Helper()
		select {
		case got := <-sent:
			t.Fatalf("pushed %s: %v", why, got)
		case <-time.After(time.Second):
		}
	}
	arrives := func(why string) {
		t.Helper()
		select {
		case <-sent:
		case <-time.After(5 * time.Second):
			t.Fatalf("not pushed %s", why)
		}
	}
	needsYou()
	quiet("while at the Mac")

	// Held: you walk away with it still waiting, and it goes once you're idle.
	idle.Store(int64(time.Hour))
	arrives("once idle")

	// Dealt with before you go: dropped.
	idle.Store(0)
	needsYou()
	quiet("while at the Mac")
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","prompt":"ok"}`)})
	idle.Store(int64(time.Hour))
	quiet("after it was answered")

	// Locked, it's pushed straight away.
	idle.Store(0)
	locked.Store(true)
	needsYou()
	arrives("with the screen locked")
}

// A tag is the same for the same phone and id, and tells nothing of the id:
// different for another phone, another id, or the other kind.
func TestPushTag(t *testing.T) {
	k1, k2 := make([]byte, 32), make([]byte, 32)
	k2[0] = 1
	a := pushTag(k1, "thread", "pn_abc")
	if len(a) != 32 || a != pushTag(k1, "thread", "pn_abc") {
		t.Fatalf("tag %q", a)
	}
	for _, b := range []string{pushTag(k2, "thread", "pn_abc"), pushTag(k1, "thread", "pn_abd"), pushTag(k1, "collapse", "pn_abc")} {
		if b == a {
			t.Fatalf("same tag %q", b)
		}
	}
	if pushTag(k1, "thread", "") != "" {
		t.Fatal("a tag for no id")
	}
}

// Ours, at its own domain and at the workers.dev name phones registered
// before the move use; nothing else.
func TestKnownRelays(t *testing.T) {
	for _, u := range []string{"https://push.homegrownsw.com", "https://hmk-push.homegrownsw.workers.dev", "https://PUSH.homegrownsw.com/"} {
		if !knownRelay(u) {
			t.Errorf("%s should be allowed", u)
		}
	}
	for _, u := range []string{"http://push.homegrownsw.com", "https://push.homegrownsw.com.evil.example", "https://evil.example", "https://push.homegrownsw.com/x"} {
		if knownRelay(u) {
			t.Errorf("%s shouldn't be allowed", u)
		}
	}
}
