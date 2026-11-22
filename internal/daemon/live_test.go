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
	"sync/atomic"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestLiveDoing(t *testing.T) {
	for _, c := range []struct{ tool, input, want string }{
		{"Edit", `{"file_path":"/Users/me/Developer/app/AppModel.swift"}`, "Editing AppModel.swift"},
		{"Read", `{"file_path":"/tmp/x/notes.md"}`, "Reading notes.md"},
		{"Write", `{"file_path":"main.go"}`, "Writing main.go"},
		{"Bash", `{"command":"go test ./...","description":"Run the tests"}`, "Run the tests"},
		{"Bash", `{"command":"go test ./...\ngo vet"}`, "Running go test ./... (+1 more line)"},
		{"exec_command", `{"cmd":["bash","-lc","npm test"]}`, "Running npm test"},
		{"apply_patch", `{}`, "Editing"},
		{"Grep", `{"pattern":"liveDoing"}`, "Searching for liveDoing"},
		{"WebSearch", `{"query":"ActivityKit push"}`, "Searching the web for ActivityKit push"},
		{"WebFetch", `{"url":"https://example.com/docs/a"}`, "Reading example.com"},
		{"TodoWrite", `{}`, "Updating its tasks"},
		{"Task", `{"description":"Find the bug"}`, "Running a subagent: Find the bug"},
		{"mcp__hammock__list_panes", `{}`, "Using list panes"},
		{"Frobnicate", `{}`, "Using Frobnicate"},
	} {
		if got := liveDoing(c.tool, json.RawMessage(c.input)); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.tool, c.input, got, c.want)
		}
	}
}

func TestPickLive(t *testing.T) {
	a := func(pane string, needs bool, at int64) liveAgent {
		return liveAgent{content: liveContent{Pane: pane}, needs: needs, at: at}
	}
	pick := func(agents []liveAgent, current string) (string, int) {
		s, o, ok := pickLive(agents, current)
		if !ok {
			return "", 0
		}
		return s.content.Pane, len(o)
	}
	if p, _ := pick(nil, ""); p != "" {
		t.Fatalf("picked %q from none", p)
	}
	// The one that's needed you longest, whatever works.
	if p, n := pick([]liveAgent{a("w1", false, 900), a("n1", true, 500), a("n2", true, 100)}, "w1"); p != "n2" || n != 2 {
		t.Fatalf("picked %q (+%d), want n2", p, n)
	}
	// None needs you: the most recently active…
	if p, _ := pick([]liveAgent{a("w1", false, 100), a("w2", false, 300), a("w3", false, 200)}, ""); p != "w2" {
		t.Fatalf("picked %q, want w2", p)
	}
	// …unless one already shown still works: they don't take turns.
	if p, _ := pick([]liveAgent{a("w1", false, 100), a("w2", false, 300)}, "w1"); p != "w1" {
		t.Fatalf("picked %q, want w1 (shown)", p)
	}
}

func TestLiveMore(t *testing.T) {
	w, n := liveAgent{}, liveAgent{needs: true}
	for _, c := range []struct {
		others []liveAgent
		want   string
	}{
		{nil, ""},
		{[]liveAgent{w}, "+1 more working"},
		{[]liveAgent{w, w}, "+2 more working"},
		{[]liveAgent{n}, "+1 more needs you"},
		{[]liveAgent{n, n}, "+2 more need you"},
		{[]liveAgent{w, n}, "+2 more: 1 needs you"},
		{[]liveAgent{w, n, n}, "+3 more: 2 need you"},
	} {
		if got := liveMore(c.others); got != c.want {
			t.Errorf("%d others: %q, want %q", len(c.others), got, c.want)
		}
	}
}

func TestLiveAccent(t *testing.T) {
	for _, c := range []struct {
		theme       *pb.HostTheme
		light, dark string
	}{
		{nil, "#1F7A57", "#3FB88A"},
		{&pb.HostTheme{Name: "Ocean"}, "#2B6CC4", "#72A8F0"},
		{&pb.HostTheme{Name: "Native"}, "", ""},
		{&pb.HostTheme{Name: "custom", AgentColor: "#112233"}, "#112233", "#112233"},
		{&pb.HostTheme{Name: "custom", AgentColor: "light:#112233,dark:#445566"}, "#112233", "#445566"},
		{&pb.HostTheme{Name: "custom", AgentColor: "Ocean"}, "", ""},
	} {
		if l, d := liveAccent(c.theme); l != c.light || d != c.dark {
			t.Errorf("%v: %s %s, want %s %s", c.theme, l, d, c.light, c.dark)
		}
	}
}

func TestAwayRule(t *testing.T) {
	for _, c := range []struct {
		phone, elsewhere, elsewhereIsAway, locked bool
		idle                                      time.Duration
		want                                      bool
	}{
		{false, false, false, false, 0, false},
		{false, false, false, false, awayAfter, true},
		{false, false, false, true, 0, true},
		{true, false, false, false, 0, true},
		{false, true, false, true, time.Hour, false}, // at another of your computers
		{false, true, true, false, 0, true},
	} {
		if got := awayRule(c.phone, c.elsewhere, c.elsewhereIsAway, c.locked, c.idle); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}

// liveRelay is a test relay's Live Activity sends (and sealed ones, for
// Android).
type liveSend struct {
	ID      string         `json:"id"`
	Payload string         `json:"payload"`
	Live    map[string]any `json:"live"`
}

// livePhoneFor pairs a phone (platform "ios" or "android") with d and
// registers it for pushes with events.
func livePhoneFor(t *testing.T, d *Daemon, dir, platform string, reg *pb.RegisterPush) *client.Client {
	t.Helper()
	local := dial(t, dir)
	pairing := call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePairing{CreatePairing: &pb.CreatePairing{}}}).GetPairing()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	phone, err := client.DialRemote(ctx, d.RemoteAddrs()[0], pairing.CertSha256, platform, "phone")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { phone.Close() })
	waitEvent(t, phone, func(ev *pb.ServerMessage) bool { return ev.GetAuthChallenge() != nil })
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := phone.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_Pair{Pair: &pb.Pair{Secret: pairing.Secret, DeviceName: "phone", Platform: platform, PublicKey: pub}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := phone.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RegisterPush{RegisterPush: reg}}); err != nil {
		t.Fatal(err)
	}
	call(t, phone, &pb.ClientMessage{Msg: &pb.ClientMessage_Presence{Presence: &pb.Presence{Foreground: false}}})
	return phone
}

// liveDaemon starts a daemon with remote access, its relay a test one
// whose sends arrive on the channel, you at the Mac until idle is set.
func liveDaemon(t *testing.T) (d *Daemon, dir string, sends chan liveSend, idle *atomic.Int64, relayURL string) {
	sends = make(chan liveSend, 64)
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/send" {
			return
		}
		var s liveSend
		_ = json.NewDecoder(r.Body).Decode(&s)
		sends <- s
	}))
	t.Cleanup(relay.Close)
	idle = &atomic.Int64{}
	oldClient, oldIdle, oldLocked, oldRelays, oldPoll, oldGap := pushClient, idleTime, screenLocked, pushRelays, livePoll, liveGap
	pushClient, pushRelays, livePoll, liveGap = relay.Client(), []string{relayOrigin(relay.URL)}, 20*time.Millisecond, 0
	idleTime = func() time.Duration { return time.Duration(idle.Load()) }
	screenLocked = func() bool { return false }
	t.Cleanup(func() {
		pushClient, idleTime, screenLocked, pushRelays, livePoll, liveGap = oldClient, oldIdle, oldLocked, oldRelays, oldPoll, oldGap
	})
	dir = shortDir(t)
	var err error
	d, err = New(Config{Dir: dir, HolderPath: holderBin, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
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
	return d, dir, sends, idle, relay.URL
}

// liveAgentPane makes a tab in a Group with an agent at work in it.
func liveAgentPane(t *testing.T, dir string) string {
	local := dial(t, dir)
	g := client.CreatedID(call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Atlas"}}}))
	tab := client.CreatedID(call(t, local, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Name: "Map Judder",
		Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	return tab
}

func paneOfTab(d *Daemon, tab string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tabs[tab].FocusedPaneId
}

// An iPhone with Live Activities on: started by push once you're away and
// an agent works (never while you're at the Mac), updated with the token it
// then gives, and ended as soon as you're back.
func TestLiveActivityWhileAway(t *testing.T) {
	d, dir, sends, idle, relayURL := liveDaemon(t)
	key := make([]byte, 32)
	phone := livePhoneFor(t, d, dir, "ios", &pb.RegisterPush{RelayUrl: relayURL, RelayId: "relay-id-1", Key: key,
		Events: []string{"needs_input", liveEvent}, LiveStartToken: "aa11"})
	// A phone without them on is sent nothing of it.
	livePhoneFor(t, d, dir, "ios", &pb.RegisterPush{RelayUrl: relayURL, RelayId: "relay-id-2", Key: key, Events: []string{"needs_input"}, LiveStartToken: "bb22"})

	paneID := paneOfTab(d, liveAgentPane(t, dir))
	tool := func(name, input string) {
		sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(
			`{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"` + name + `","tool_input":` + input + `}`)})
	}
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","session_id":"s","prompt":"go"}`)})
	tool("Edit", `{"file_path":"/x/AppModel.swift"}`)

	next := func(why string) liveSend {
		t.Helper()
		for {
			select {
			case s := <-sends:
				if s.Live == nil {
					continue // a notification
				}
				return s
			case <-time.After(5 * time.Second):
				t.Fatalf("no Live Activity push %s", why)
			}
		}
	}
	quiet := func(why string) {
		t.Helper()
		deadline := time.After(400 * time.Millisecond)
		for {
			select {
			case s := <-sends:
				if s.Live != nil {
					t.Fatalf("Live Activity push %s: %v", why, s.Live)
				}
			case <-deadline:
				return
			}
		}
	}
	quiet("while at the Mac")

	idle.Store(int64(time.Hour))
	s := next("once away")
	state, _ := s.Live["state"].(map[string]any)
	attrs, _ := s.Live["attributes"].(map[string]any)
	if s.ID != "relay-id-1" || s.Live["event"] != "start" || s.Live["token"] != "aa11" || attrs["host"] == "" ||
		state["agent"] != "Claude" || state["tab"] != "Map Judder" || state["group"] != "Atlas" || state["state"] != liveWorking ||
		state["doing"] != "Editing AppModel.swift" || state["pane"] != paneID {
		t.Fatalf("start %+v", s)
	}
	// Apps are told too (an iPhone with Hammock open draws its own).
	d.mu.Lock()
	shown := d.live.shown
	d.mu.Unlock()
	if shown == "" {
		t.Fatal("not published")
	}
	// No token to update it with yet: nothing more.
	tool("Read", `{"file_path":"/x/b.go"}`)
	quiet("without its token")

	// The phone gives its token: updates go with it.
	call(t, phone, &pb.ClientMessage{Msg: &pb.ClientMessage_RegisterPush{RegisterPush: &pb.RegisterPush{RelayUrl: relayURL, RelayId: "relay-id-1", Key: key,
		Events: []string{"needs_input", liveEvent}, LiveStartToken: "aa11", LiveToken: "cc33"}}})
	tool("Bash", `{"command":"go test ./..."}`)
	for {
		s = next("after a new tool")
		if st, _ := s.Live["state"].(map[string]any); st["doing"] == "Running go test ./..." {
			break
		}
	}
	if s.Live["event"] != "update" || s.Live["token"] != "cc33" {
		t.Fatalf("update %+v", s)
	}

	// Back at the Mac: ended at once.
	idle.Store(0)
	s = next("once back")
	if s.Live["event"] != "end" || s.Live["token"] != "cc33" || int64(s.Live["dismiss"].(float64)) > time.Now().Unix()+1 {
		t.Fatalf("end %+v", s)
	}
	quiet("after it ended")
	d.mu.Lock()
	shown = d.live.shown
	d.mu.Unlock()
	if shown != "" {
		t.Fatalf("still published: %s", shown)
	}

	// Away again, it starts again; the agent stops, and it ends a while
	// later, saying it finished.
	idle.Store(int64(time.Hour))
	if s = next("away again"); s.Live["event"] != "start" {
		t.Fatalf("restart %+v", s)
	}
	call(t, phone, &pb.ClientMessage{Msg: &pb.ClientMessage_RegisterPush{RegisterPush: &pb.RegisterPush{RelayUrl: relayURL, RelayId: "relay-id-1", Key: key,
		Events: []string{"needs_input", liveEvent}, LiveStartToken: "aa11", LiveToken: "dd44"}}})
	sendHook(t, dir, HookEvent{PaneID: paneID, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"Stop","session_id":"s","last_assistant_message":"All done."}`)})
	s = next("once it stopped")
	state, _ = s.Live["state"].(map[string]any)
	if s.Live["event"] != "end" || s.Live["token"] != "dd44" || state["state"] != liveFinished || state["status"] != "Finished" ||
		int64(s.Live["dismiss"].(float64)) < time.Now().Add(liveLinger).Unix()-5 {
		t.Fatalf("finished %+v", s)
	}
}

// An agent waiting on a permission: shown first, with its prompt to answer.
// Android's goes in its encrypted note.
func TestLiveActivityAndroidNeedsYou(t *testing.T) {
	d, dir, sends, idle, relayURL := liveDaemon(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	livePhoneFor(t, d, dir, "android", &pb.RegisterPush{RelayUrl: relayURL, RelayId: "relay-id-1", Key: key, Events: []string{liveEvent}})
	working := paneOfTab(d, liveAgentPane(t, dir))
	waiting := paneOfTab(d, liveAgentPane(t, dir))
	sendHook(t, dir, HookEvent{PaneID: working, Harness: "claude", Payload: json.RawMessage(`{"hook_event_name":"UserPromptSubmit","session_id":"s","prompt":"go"}`)})
	sendHook(t, dir, HookEvent{PaneID: waiting, Harness: "claude", Payload: json.RawMessage(
		`{"hook_event_name":"Notification","session_id":"t","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`)})
	d.mu.Lock()
	for _, a := range d.attention {
		if a.PaneId == waiting {
			a.PromptId = "pr_1" // as the screen would say
		}
	}
	d.mu.Unlock()
	idle.Store(int64(time.Hour))
	open := func(payload string) pushNote {
		t.Helper()
		sealed, _ := base64.StdEncoding.DecodeString(payload)
		block, _ := aes.NewCipher(key)
		gcm, _ := cipher.NewGCM(block)
		plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		var n pushNote
		_ = json.Unmarshal(plain, &n)
		return n
	}
	deadline := time.After(5 * time.Second)
	for {
		select {
		case s := <-sends:
			if s.Live != nil {
				t.Fatalf("Android sent a plain Live Activity: %v", s.Live)
			}
			n := open(s.Payload)
			if n.Kind != liveEvent {
				continue
			}
			c := n.Activity
			if c == nil || c.State != liveNeedsYou || c.Pane != waiting || c.Prompt != "pr_1" || c.More != "+1 more working" ||
				c.Others != 1 || c.Waiting != 1 || c.Status != "Needs you" || c.Doing == "" || n.ActivityEnd != 0 {
				t.Fatalf("live note %+v %+v", n, c)
			}
			return
		case <-deadline:
			t.Fatal("no live note")
		}
	}
}
