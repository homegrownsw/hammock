package daemon

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A paired phone registers; when an agent needs input while nobody's at the
// Mac, the relay gets a note only the phone can read.
func TestPushWhileAway(t *testing.T) {
	sent := make(chan map[string]string, 4)
	cleared := make(chan map[string]string, 16)
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := relayBody(r)
		switch r.URL.Path {
		case "/send":
			sent <- body
		case "/clear":
			select {
			case cleared <- body:
			default:
			}
		}
	}))
	defer relay.Close()
	// Read by held pushes' goroutines while the test changes them.
	var idle atomic.Int64
	var locked atomic.Bool
	idle.Store(int64(time.Hour))
	oldClient, oldIdle, oldLocked, oldRelays, oldPoll, oldDelay := pushClient, idleTime, screenLocked, pushRelays, pushPoll, clearDelay
	pushClient, pushRelays, pushPoll, clearDelay = relay.Client(), []string{relayOrigin(relay.URL)}, 50*time.Millisecond, 0
	idleTime = func() time.Duration { return time.Duration(idle.Load()) }
	screenLocked = locked.Load
	defer func() {
		pushClient, idleTime, screenLocked, pushRelays, pushPoll, clearDelay = oldClient, oldIdle, oldLocked, oldRelays, oldPoll, oldDelay
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
		RelayUrl: relay.URL, RelayId: "relay-id-1", Key: key, Events: []string{"needs_input", badgeEvent},
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
	open := func(payload string) pushNote {
		t.Helper()
		sealed, _ := base64.StdEncoding.DecodeString(payload)
		block, _ := aes.NewCipher(key)
		gcm, _ := cipher.NewGCM(block)
		plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		var note pushNote
		_ = json.Unmarshal(plain, &note)
		return note
	}
	note := open(got["payload"])
	if note.Title != "Map Judder" || note.Subtitle != "Atlas" || note.Pane != paneID || note.Host == "" ||
		note.ID != "at_"+paneID || !note.Live || time.Since(time.Unix(note.At, 0)) > time.Minute ||
		note.Kind != "needs_input" || note.Badge == nil || *note.Badge != 1 ||
		note.Category != "" || note.Prompt != "" { // no menu on its screen: nothing to Allow
		t.Fatalf("note %+v", note)
	}
	// The count only in the ciphertext: the relay never sees it.
	if _, ok := got["badge"]; ok {
		t.Fatalf("relay given a badge: %v", got)
	}

	// Answered (here, at the Mac): the phone is told to take it down,
	// silently, saying only which notification.
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","prompt":"yes"}`)})
	select {
	case c := <-cleared:
		n := open(c["payload"])
		if c["id"] != "relay-id-1" || c["collapse"] != "" || c["thread"] != "" ||
			n.ID != "at_"+paneID || !n.Clear || n.Host != note.Host || n.Title != "" || n.Pane != "" || n.Badge == nil || *n.Badge != 0 {
			t.Fatalf("clear %v: %+v", c, n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no clear sent once it was answered")
	}
	// Once only: the same id resolved again tells no one.
	d.mu.Lock()
	d.publish(&pb.StateUpdate{ClearedAttentionIds: []string{"at_" + paneID}})
	d.mu.Unlock()
	select {
	case c := <-cleared:
		t.Fatalf("cleared again: %v", c)
	case <-time.After(300 * time.Millisecond):
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

// relayBody is what the daemon sent a test relay, its values as text.
func relayBody(r *http.Request) map[string]string {
	var raw map[string]any
	_ = json.NewDecoder(r.Body).Decode(&raw)
	body := map[string]string{}
	for k, v := range raw {
		body[k] = fmt.Sprint(v)
	}
	return body
}

// A phone left showing a number is told when nothing's left to count,
// though what it was told of wasn't resolved (it finished, rather than
// being cleared); not while something still counts, and once only.
func TestPushClearsBadge(t *testing.T) {
	cleared := make(chan map[string]string, 4)
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/clear" {
			cleared <- relayBody(r)
		}
	}))
	defer relay.Close()
	oldClient, oldRelays, oldDelay := pushClient, pushRelays, clearDelay
	pushClient, pushRelays, clearDelay = relay.Client(), []string{relayOrigin(relay.URL)}, 0
	defer func() { pushClient, pushRelays, clearDelay = oldClient, oldRelays, oldDelay }()

	dir := shortDir(t)
	d, err := New(Config{Dir: dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Shutdown()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	d.mu.Lock()
	for id, events := range map[string][]string{"phone": {"needs_input", badgeEvent}, "old-phone": {"needs_input"}} {
		r := &pb.RegisterPush{RelayUrl: relay.URL, RelayId: id, Key: key, Events: events}
		if err := d.store.PutRecord("push", id, r); err != nil {
			t.Fatal(err)
		}
		d.noteBadge(id, r, 1) // as if each was pushed about a pane needing input
	}
	item := &pb.AttentionItem{Id: "at_pn_1", PaneId: "pn_1", State: pb.AgentState_AGENT_STATE_NEEDS_INPUT}
	d.attention[item.Id] = item
	other := &pb.AttentionItem{Id: "at_pn_2", PaneId: "pn_2", State: pb.AgentState_AGENT_STATE_NEEDS_INPUT}
	d.attention[other.Id] = other
	d.publish(&pb.StateUpdate{Attention: []*pb.AttentionItem{other}})
	d.mu.Unlock()
	none := func(why string) {
		t.Helper()
		select {
		case c := <-cleared:
			t.Fatalf("cleared %s: %v", why, c)
		case <-time.After(300 * time.Millisecond):
		}
	}
	none("with two waiting")

	// One finishes: one still counts.
	d.mu.Lock()
	item.State = pb.AgentState_AGENT_STATE_IDLE
	d.publish(&pb.StateUpdate{Attention: []*pb.AttentionItem{item}})
	d.mu.Unlock()
	none("with one still waiting")

	// The other goes: the phone that shows a number hears it's none.
	d.mu.Lock()
	delete(d.attention, other.Id)
	d.publish(&pb.StateUpdate{ClearedAttentionIds: []string{other.Id}})
	d.mu.Unlock()
	select {
	case c := <-cleared:
		sealed, _ := base64.StdEncoding.DecodeString(c["payload"])
		block, _ := aes.NewCipher(key)
		gcm, _ := cipher.NewGCM(block)
		plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
		if err != nil {
			t.Fatal(err)
		}
		var n pushNote
		_ = json.Unmarshal(plain, &n)
		if c["id"] != "phone" || c["badge"] != "" || !n.Clear || n.ID != "" || n.Badge == nil || *n.Badge != 0 {
			t.Fatalf("clear %v: %+v", c, n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no clear once nothing counted")
	}
	none("to a phone that shows no number, or twice")
	d.mu.Lock()
	d.publish(&pb.StateUpdate{Attention: []*pb.AttentionItem{item}})
	d.mu.Unlock()
	none("again")
}

// A send the relay turns away with 429 (its rate limit) is tried once more,
// a little later, unless it's been dealt with meanwhile; other refusals
// aren't retried.
func TestPushRetriesAfterSlowDown(t *testing.T) {
	var status []int // what the relay answers, in turn (200 once they run out)
	var mu sync.Mutex
	sends := make(chan string, 8)
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		code := http.StatusOK
		if len(status) > 0 {
			code, status = status[0], status[1:]
		}
		mu.Unlock()
		w.WriteHeader(code)
		sends <- body["payload"]
	}))
	defer relay.Close()
	oldClient, oldRelays, oldRetry := pushClient, pushRelays, pushRetryAfter
	pushClient, pushRelays, pushRetryAfter = relay.Client(), []string{relayOrigin(relay.URL)}, 50*time.Millisecond
	defer func() { pushClient, pushRelays, pushRetryAfter = oldClient, oldRelays, oldRetry }()

	d := &Daemon{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := &pb.RegisterPush{RelayUrl: relay.URL, RelayId: "relay-id-1", Key: make([]byte, 32)}
	send := func(codes []int, pending func() bool) {
		t.Helper()
		mu.Lock()
		status = codes
		mu.Unlock()
		d.sendPush("phone", r, []byte("sealed"), pushNote{}, "at_pn_1", pending)
	}
	count := func(want int, why string) {
		t.Helper()
		got := 0
		for deadline := time.After(time.Second); ; {
			select {
			case p := <-sends:
				if p != base64.StdEncoding.EncodeToString([]byte("sealed")) {
					t.Fatalf("%s: sent %q", why, p)
				}
				got++
			case <-deadline:
				if got != want {
					t.Fatalf("%s: %d sends, want %d", why, got, want)
				}
				return
			}
		}
	}

	send([]int{http.StatusTooManyRequests}, nil)
	count(2, "429 then 200")
	send([]int{http.StatusTooManyRequests, http.StatusTooManyRequests, http.StatusTooManyRequests}, nil)
	count(2, "429 every time: one retry only")
	answered := false
	send([]int{http.StatusTooManyRequests}, func() bool { return !answered })
	d.mu.Lock()
	answered = true // dealt with before the retry
	d.mu.Unlock()
	count(1, "answered before the retry")
	send([]int{http.StatusInternalServerError}, nil)
	count(1, "500")

	// A Hammock Key request's (pushNow): pending until clearPushes takes it
	// out of pushSent.
	d.mu.Lock()
	d.notePushed("hk_1", "phone")
	d.mu.Unlock()
	send([]int{http.StatusTooManyRequests}, d.stillPushed("hk_1", "phone"))
	count(2, "a Hammock Key request still waiting")
	send([]int{http.StatusTooManyRequests}, d.stillPushed("hk_1", "phone"))
	d.mu.Lock()
	delete(d.pushSent, "hk_1")
	d.mu.Unlock()
	count(1, "a Hammock Key request answered before the retry")
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

// Phones that registered at the workers.dev name are sent to at the domain;
// unknown relays at nowhere.
func TestRelayEndpoint(t *testing.T) {
	for raw, want := range map[string]string{
		"https://hmk-push.homegrownsw.workers.dev":  "https://push.homegrownsw.com",
		"https://HMK-PUSH.homegrownsw.workers.dev/": "https://push.homegrownsw.com",
		"https://push.homegrownsw.com":              "https://push.homegrownsw.com",
		"https://evil.example":                      "",
		"http://hmk-push.homegrownsw.workers.dev":   "",
	} {
		if got := relayEndpoint(raw); got != want {
			t.Errorf("relayEndpoint(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A send and a clear for a phone registered at a relay's old origin go to
// its new one.
func TestPushGoesToMovedRelay(t *testing.T) {
	paths := make(chan string, 4)
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { paths <- r.URL.Path }))
	defer relay.Close()
	const old = "https://old-relay.example"
	oldClient, oldRelays, oldMoved, oldDelay := pushClient, pushRelays, relayMoved, clearDelay
	pushClient, pushRelays, relayMoved, clearDelay = relay.Client(), []string{relayOrigin(relay.URL), old}, map[string]string{old: relayOrigin(relay.URL)}, 0
	defer func() { pushClient, pushRelays, relayMoved, clearDelay = oldClient, oldRelays, oldMoved, oldDelay }()

	d := &Daemon{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := &pb.RegisterPush{RelayUrl: old, RelayId: "relay-id-1", Key: make([]byte, 32)}
	if code := d.postPush("phone", r, []byte("sealed"), pushNote{}, "", false); code != http.StatusOK {
		t.Fatalf("send: status %d", code)
	}
	d.sendClear("phone", r, []byte("sealed"))
	for _, want := range []string{"/send", "/clear"} {
		select {
		case p := <-paths:
			if p != want {
				t.Fatalf("got %s, want %s", p, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("no %s at the new relay", want)
		}
	}
}

// Only an agent's permission prompt, with the prompt its screen shows, is
// sent as one Allow and Deny answer (agentPromptCategory, and the prompt
// it's for): not a question, another program, a finished or failed agent,
// or a prompt not read yet.
func TestAgentPromptNote(t *testing.T) {
	needs := pb.AgentState_AGENT_STATE_NEEDS_INPUT
	for _, c := range []struct {
		name string
		item *pb.AttentionItem
		want bool
	}{
		{"permission", &pb.AttentionItem{State: needs, PromptId: "k1"}, true},
		{"not read yet", &pb.AttentionItem{State: needs}, false},
		{"question", &pb.AttentionItem{State: needs, Question: true, PromptId: "k1"}, false},
		{"program", &pb.AttentionItem{State: needs, Program: true, PromptId: "k1"}, false},
		{"finished", &pb.AttentionItem{State: pb.AgentState_AGENT_STATE_IDLE, PromptId: "k1"}, false},
		{"errored", &pb.AttentionItem{State: pb.AgentState_AGENT_STATE_ERRORED, PromptId: "k1"}, false},
		{"gone", nil, false},
	} {
		var n pushNote
		agentPromptNote(&n, c.item)
		if got := n.Category == agentPromptCategory && n.Prompt == "k1"; got != c.want || (!c.want && (n.Category != "" || n.Prompt != "")) {
			t.Errorf("%s: %+v", c.name, n)
		}
	}
	// The relay is told only the categories it knows.
	for c, want := range map[string]string{"join": "join", "grant": "grant", "hammockkey": "hammockkey", agentPromptCategory: "", "": ""} {
		if got := relayCategory(c); got != want {
			t.Errorf("relayCategory(%q) = %q", c, got)
		}
	}
}

// An agent's permission prompt reaches an away phone with its Allow and
// Deny in the sealed note (the prompt as it is when it's sent, read after
// the item was raised), and nothing of it in what the relay sees; other
// agents' notes have none.
func TestPushAgentPrompt(t *testing.T) {
	sent := make(chan map[string]string, 8)
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/send" {
			sent <- relayBody(r)
		}
	}))
	defer relay.Close()
	var idle atomic.Int64 // at the Mac until the test goes
	oldClient, oldRelays, oldIdle, oldLocked, oldPoll := pushClient, pushRelays, idleTime, screenLocked, pushPoll
	pushClient, pushRelays, pushPoll = relay.Client(), []string{relayOrigin(relay.URL)}, 20*time.Millisecond
	idleTime = func() time.Duration { return time.Duration(idle.Load()) }
	screenLocked = func() bool { return false }
	defer func() {
		pushClient, pushRelays, idleTime, screenLocked, pushPoll = oldClient, oldRelays, oldIdle, oldLocked, oldPoll
	}()

	dir := shortDir(t)
	d, err := New(Config{Dir: dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Shutdown()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	events := []string{"needs_input", "finished", "errored", "program_needs_input"}
	d.mu.Lock()
	if err := d.store.PutRecord("push", "phone", &pb.RegisterPush{RelayUrl: relay.URL, RelayId: "phone", Key: key, Events: events}); err != nil {
		t.Fatal(err)
	}
	d.mu.Unlock()
	open := func(body map[string]string) pushNote {
		t.Helper()
		sealed, _ := base64.StdEncoding.DecodeString(body["payload"])
		block, _ := aes.NewCipher(key)
		gcm, _ := cipher.NewGCM(block)
		plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
		if err != nil {
			t.Fatal(err)
		}
		var n pushNote
		_ = json.Unmarshal(plain, &n)
		return n
	}
	push := func(item *pb.AttentionItem, then func()) pushNote {
		t.Helper()
		idle.Store(0)
		d.mu.Lock()
		d.attention[item.Id] = item
		d.pushAttention(item)
		if then != nil {
			then() // while it waits for you to go
		}
		d.mu.Unlock()
		idle.Store(int64(time.Hour))
		select {
		case body := <-sent:
			if body["category"] != "" || strings.Contains(fmt.Sprint(body), "agentprompt") || strings.Contains(fmt.Sprint(body), "prompt-key") {
				t.Fatalf("relay told %v", body)
			}
			return open(body)
		case <-time.After(5 * time.Second):
			t.Fatalf("%s not pushed", item.Id)
		}
		return pushNote{}
	}

	needs := pb.AgentState_AGENT_STATE_NEEDS_INPUT
	perm := &pb.AttentionItem{Id: "at_pn_1", PaneId: "pn_1", State: needs, Summary: "Claude needs your permission to use Bash"}
	n := push(perm, func() { perm.PromptId = "prompt-key" })
	if n.Category != agentPromptCategory || n.Prompt != "prompt-key" || n.Pane != "pn_1" || n.ID != "at_pn_1" || !n.Live || n.Kind != "needs_input" {
		t.Fatalf("permission prompt: %+v", n)
	}
	for _, item := range []*pb.AttentionItem{
		{Id: "at_pn_2", PaneId: "pn_2", State: needs}, // no prompt on screen
		{Id: "at_pn_3", PaneId: "pn_3", State: needs, Question: true, PromptId: "prompt-key"},
		{Id: "at_pn_4", PaneId: "pn_4", State: needs, Program: true, PromptId: "prompt-key"},
		{Id: "at_pn_5", PaneId: "pn_5", State: pb.AgentState_AGENT_STATE_IDLE, PromptId: "prompt-key"},
		{Id: "at_pn_6", PaneId: "pn_6", State: pb.AgentState_AGENT_STATE_ERRORED, PromptId: "prompt-key"},
	} {
		if n := push(item, nil); n.Category != "" || n.Prompt != "" {
			t.Fatalf("%s: %+v", item.Id, n)
		}
	}
}
