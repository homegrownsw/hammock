package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/slack"
)

// fakeSlack is Slack's Web API and a Socket Mode connection, made up:
// tests never reach the real Slack.
type fakeSlack struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	conns   []*websocket.Conn
	history map[string][]map[string]any // channel → its messages (newest first, as Slack)
	acks    []string
	n       int
}

func newFakeSlack(t *testing.T) *fakeSlack {
	f := &fakeSlack{t: t, history: map[string][]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSlack) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/ws" {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, c)
		f.mu.Unlock()
		_ = c.Write(r.Context(), websocket.MessageText, []byte(`{"type":"hello"}`))
		for {
			_, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var a struct {
				ID string `json:"envelope_id"`
			}
			_ = json.Unmarshal(data, &a)
			f.mu.Lock()
			f.acks = append(f.acks, a.ID)
			f.mu.Unlock()
		}
	}
	_ = r.ParseForm()
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	reply := func(v map[string]any) {
		v["ok"] = true
		_ = json.NewEncoder(w).Encode(v)
	}
	switch strings.TrimPrefix(r.URL.Path, "/api/") {
	case "auth.test":
		if tok != "xoxp-good" {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
			return
		}
		w.Header().Set("X-OAuth-Scopes", strings.Join(slack.UserScopes, ","))
		reply(map[string]any{"url": "https://acme.slack.com/", "team": "Acme", "team_id": "T1", "user": "me", "user_id": "UME"})
	case "apps.connections.open":
		if tok != "xapp-good" {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
			return
		}
		reply(map[string]any{"url": "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/ws"})
	case "users.info":
		names := map[string]string{"U1": "sam", "UME": "me"}
		id := r.Form.Get("user")
		reply(map[string]any{"user": map[string]any{"id": id, "name": names[id], "profile": map[string]any{"display_name": names[id]}}})
	case "conversations.info":
		id := r.Form.Get("channel")
		ch := map[string]any{"id": id, "name": map[string]string{"C1": "deploys", "C2": "ops"}[id], "is_im": strings.HasPrefix(id, "D")}
		reply(map[string]any{"channel": ch})
	case "conversations.list":
		reply(map[string]any{"channels": []map[string]any{{"id": "C1", "name": "deploys"}, {"id": "C2", "name": "ops"}}})
	case "conversations.history", "conversations.replies":
		ch, oldest, latest := r.Form.Get("channel"), r.Form.Get("oldest"), r.Form.Get("latest")
		f.mu.Lock()
		var out []map[string]any
		for _, m := range f.history[ch] {
			ts := m["ts"].(string)
			if (oldest == "" || slack.CompareTS(ts, oldest) >= 0) && (latest == "" || slack.CompareTS(ts, latest) <= 0) {
				out = append(out, m)
			}
		}
		f.mu.Unlock()
		reply(map[string]any{"messages": out})
	default:
		_, _ = w.Write([]byte(`{"ok":false,"error":"unknown_method"}`))
	}
}

// send sends an event down the newest connection.
func (f *fakeSlack) send(event map[string]any) {
	f.t.Helper()
	f.mu.Lock()
	f.n++
	env := map[string]any{"type": "events_api", "envelope_id": fmt.Sprintf("env%d", f.n), "payload": map[string]any{"event_id": fmt.Sprintf("Ev%d", f.n), "event": event}}
	c := f.conns[len(f.conns)-1]
	f.mu.Unlock()
	b, _ := json.Marshal(env)
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		f.t.Fatal(err)
	}
}

// refresh asks the connection to make a new one (as Slack does now and then).
func (f *fakeSlack) refresh() {
	f.mu.Lock()
	c := f.conns[len(f.conns)-1]
	f.mu.Unlock()
	_ = c.Write(context.Background(), websocket.MessageText, []byte(`{"type":"disconnect","reason":"refresh_requested"}`))
}

func (f *fakeSlack) connections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.conns)
}

// tsAt is a Slack timestamp a little after now.
var tsSeq int

func tsNow() string {
	tsSeq++
	return slack.TimeTS(time.Now().Add(time.Duration(tsSeq) * time.Millisecond))
}

func slackSave(t *testing.T, c *client.Client, work string, trig *pb.SlackTrigger) string {
	t.Helper()
	return call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SaveSchedule{SaveSchedule: &pb.SaveSchedule{Schedule: &pb.Schedule{
		Name: "Slack", Kind: "command", Cwd: work, Timing: pb.Schedule_TIMING_EVENTS, CooldownSeconds: proto.Int32(0),
		Events: []*pb.EventTrigger{{Kind: &pb.EventTrigger_Slack{Slack: trig}}},
		Prompt: `printf '%s|%s|%s|%s|%s' "$HMK_EVENT" "$HMK_EVENT_SUMMARY" "$HMK_EVENT_AUTHOR" "$HMK_EVENT_CHANNEL" "$HMK_EVENT_BODY" > "out-$(date +%s%N)"`,
	}}}}).GetScheduleReply().GetSchedule().GetId()
}

func outputs(work string) []string {
	m, _ := filepath.Glob(filepath.Join(work, "out-*"))
	var out []string
	for _, f := range m {
		b, _ := os.ReadFile(f)
		out = append(out, string(b))
	}
	return out
}

// Slack: connecting checks the tokens and keeps them; a connection opens
// while an automation has a Slack trigger; each kind fires for its own
// events (with what happened in the run, the author's name, the channel's)
// and not for your own messages or others' reactions; what came while it
// was away is caught up; paused, it closes; disconnecting forgets the
// tokens, and the triggers say so.
func TestSlackTriggers(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	f := newFakeSlack(t)
	oldBase, oldTick, oldBackoff := slackAPIBase, slackTick, slackBackoff
	slackAPIBase, slackTick, slackBackoff = f.srv.URL+"/api/", 50*time.Millisecond, func(int) time.Duration { return 20 * time.Millisecond }
	t.Cleanup(func() { slackAPIBase, slackTick, slackBackoff = oldBase, oldTick, oldBackoff })
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	slackCmd := func(m *pb.SlackCommand) (*pb.ServerMessage, error) {
		return try(c, &pb.ClientMessage{Msg: &pb.ClientMessage_SlackCommand{SlackCommand: m}})
	}

	// Not connected: the trigger says so.
	works := map[string]string{}
	ids := map[string]string{}
	for _, k := range []string{"message", "dm", "mention", "reply", "reaction"} {
		works[k] = t.TempDir()
	}
	ids["message"] = slackSave(t, c, works["message"], &pb.SlackTrigger{Event: pb.SlackTrigger_EVENT_MESSAGE, Channel: "#Deploys", Text: "failed"})
	if e := schedOf(d, ids["message"]).Events[0]; e.GetSlack().Channel != "deploys" || !strings.Contains(e.Error, "Slack isn't connected") {
		t.Fatalf("saved %v", e)
	}
	// Bad tokens are refused, and not kept.
	for _, bad := range [][2]string{{"xoxp-bad", "xapp-good"}, {"xoxp-good", "xapp-bad"}, {"xoxb-good", "xapp-good"}, {"", ""}} {
		if _, err := slackCmd(&pb.SlackCommand{Action: pb.SlackCommand_ACTION_CONNECT, UserToken: bad[0], AppToken: bad[1]}); err == nil {
			t.Fatalf("connected with %v", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "slack-tokens.json")); err == nil {
		t.Fatal("kept refused tokens")
	}
	r, err := slackCmd(&pb.SlackCommand{Action: pb.SlackCommand_ACTION_CONNECT, UserToken: " xoxp-good ", AppToken: "xapp-good"})
	if err != nil {
		t.Fatal(err)
	}
	if l := r.GetSlackReply().GetLink(); l.GetTeam() != "Acme" || l.GetUser() != "me" || !strings.Contains(r.GetSlackReply().GetManifestUrl(), "manifest_json=") {
		t.Fatalf("reply %v", r)
	}
	if st, err := os.Stat(filepath.Join(dir, "slack-tokens.json")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("tokens file: %v %v", st, err)
	}
	ids["dm"] = slackSave(t, c, works["dm"], &pb.SlackTrigger{Event: pb.SlackTrigger_EVENT_DM, From: "@Sam"})
	ids["mention"] = slackSave(t, c, works["mention"], &pb.SlackTrigger{Event: pb.SlackTrigger_EVENT_MENTION})
	ids["reply"] = slackSave(t, c, works["reply"], &pb.SlackTrigger{Event: pb.SlackTrigger_EVENT_THREAD_REPLY, Channel: "deploys"})
	ids["reaction"] = slackSave(t, c, works["reaction"], &pb.SlackTrigger{Event: pb.SlackTrigger_EVENT_REACTION, Emoji: ":eyes:"})
	waitFor(t, "the connection", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.slack.listening
	})
	d.mu.Lock()
	self := d.selfRecord(true)
	d.mu.Unlock()
	if self.GetSlack().GetTeam() != "Acme" || !self.GetSlack().GetListening() {
		t.Fatalf("record %v", self.GetSlack())
	}
	if e := schedOf(d, ids["message"]).Events[0]; e.Error != "" {
		t.Fatalf("connected, still: %q", e.Error)
	}

	msg := func(channel, user, text string, extra map[string]any) map[string]any {
		m := map[string]any{"type": "message", "channel": channel, "user": user, "text": text, "ts": tsNow()}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	f.send(msg("C1", "U1", "the deploy FAILED, <@U1> look", map[string]any{"channel_type": "channel"}))
	f.send(msg("C1", "UME", "my deploy failed", map[string]any{"channel_type": "channel"})) // mine: no
	f.send(msg("C2", "U1", "ops failed", map[string]any{"channel_type": "channel"}))        // another channel: no
	f.send(msg("D1", "U1", "a word in private", map[string]any{"channel_type": "im"}))
	f.send(msg("D1", "UME", "my own DM", map[string]any{"channel_type": "im"})) // mine: no
	f.send(msg("C2", "U1", "hey <@UME>, a question", map[string]any{"channel_type": "channel"}))
	f.send(msg("C2", "U1", "<!here> everyone", map[string]any{"channel_type": "channel"})) // not a mention of me
	parent := tsNow()
	f.send(msg("C1", "U1", "replying to you", map[string]any{"channel_type": "channel", "thread_ts": parent, "parent_user_id": "UME"}))
	f.send(msg("C1", "U1", "replying to sam", map[string]any{"channel_type": "channel", "thread_ts": parent, "parent_user_id": "U1"}))
	reacted := tsNow()
	f.mu.Lock()
	f.history["C1"] = []map[string]any{{"type": "message", "user": "U1", "text": "worth a look", "ts": reacted}}
	f.mu.Unlock()
	f.send(map[string]any{"type": "reaction_added", "user": "U1", "reaction": "eyes", "item": map[string]any{"type": "message", "channel": "C1", "ts": reacted}, "event_ts": tsNow()}) // not mine: no
	f.send(map[string]any{"type": "reaction_added", "user": "UME", "reaction": "eyes", "item": map[string]any{"type": "message", "channel": "C1", "ts": reacted}, "event_ts": tsNow()})

	want := map[string]string{
		"message":  "slack|A Slack message in #deploys|sam|#deploys|the deploy FAILED, @sam look",
		"dm":       "slack|A Slack direct message to you|sam|direct message|a word in private",
		"mention":  "slack|You were mentioned in Slack|sam|#ops|hey @me, a question",
		"reply":    "slack|A reply in a Slack thread you started|sam|#deploys|replying to you",
		"reaction": "slack|You reacted with :eyes: in Slack|sam|#deploys|worth a look",
	}
	for deadline := time.Now().Add(slow(10 * time.Second)); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		n := 0
		for k := range want {
			if len(outputs(works[k])) > 0 {
				n++
			}
		}
		if n == len(want) {
			break
		}
	}
	time.Sleep(200 * time.Millisecond) // (nothing more comes)
	for k, w := range want {
		if out := outputs(works[k]); len(out) != 1 || out[0] != w {
			t.Errorf("%s: %q, want %q", k, out, w)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	// Each envelope was acknowledged.
	waitFor(t, "acks", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.acks) >= f.n })

	// The same event again (Slack retrying) fires nothing more.
	f.send(map[string]any{"type": "message", "channel": "D1", "user": "U1", "text": "again", "ts": "1.0", "channel_type": "im"})
	// Caught up: what #deploys had while the connection was being made again.
	missed := tsNow()
	f.mu.Lock()
	f.history["C1"] = append([]map[string]any{{"type": "message", "user": "U1", "text": "missed: build failed", "ts": missed}}, f.history["C1"]...)
	f.mu.Unlock()
	before := f.connections()
	f.refresh()
	waitFor(t, "a new connection", func() bool { return f.connections() > before })
	waitFor(t, "the missed message", func() bool { return len(outputs(works["message"])) >= 2 })
	time.Sleep(300 * time.Millisecond)
	if out := outputs(works["message"]); len(out) != 2 || !strings.HasSuffix(strings.Join(out, "\n"), "missed: build failed") && !strings.Contains(strings.Join(out, "\n"), "missed: build failed") {
		t.Fatalf("caught up: %q", out)
	}
	if n := len(outputs(works["dm"])); n != 1 {
		t.Fatalf("dm fired %d times", n)
	}

	// A client here can't take the tokens (only another computer of yours).
	if _, err := slackCmd(&pb.SlackCommand{Action: pb.SlackCommand_ACTION_HAND_OVER}); err == nil {
		t.Fatal("a local client was handed the tokens")
	}

	// All paused: the connection closes, the tokens stay.
	for _, id := range ids {
		call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetSchedulePaused{SetSchedulePaused: &pb.SetSchedulePaused{ScheduleId: id, Paused: true}}})
	}
	waitFor(t, "the connection to close", func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.slack.cancel == nil && !d.slack.listening && d.slack.tok != nil && d.slackLink().GetIdle()
	})

	// Disconnecting forgets them.
	if _, err := slackCmd(&pb.SlackCommand{Action: pb.SlackCommand_ACTION_DISCONNECT}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "slack-tokens.json")); err == nil {
		t.Fatal("tokens kept after disconnecting")
	}
	if e := schedOf(d, ids["dm"]).Events[0]; !strings.Contains(e.Error, "isn't connected") {
		t.Fatalf("after disconnecting: %q", e.Error)
	}
}

// One computer holds Slack: the others' records say which, and a Slack
// trigger elsewhere says so; Move Here takes the tokens over the link, and
// the first forgets them.
func TestSlackMovesBetweenComputers(t *testing.T) {
	f := newFakeSlack(t)
	oldBase := slackAPIBase
	slackAPIBase = f.srv.URL + "/api/"
	t.Cleanup(func() { slackAPIBase = oldBase })
	a, b := startComputer(t), startComputer(t)
	link(t, a, b)
	waitComputers(t, b, "a online", hasComputer(a.uid, true))
	connect := &pb.SlackCommand{Action: pb.SlackCommand_ACTION_CONNECT, UserToken: "xoxp-good", AppToken: "xapp-good"}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SlackCommand{SlackCommand: connect}})
	waitComputers(t, b, "a's Slack", func(cs *pb.Computers) bool {
		for _, c := range cs.List {
			if c.UserId == a.uid && c.GetSlack().GetTeam() == "Acme" {
				return true
			}
		}
		return false
	})
	work := t.TempDir()
	id := slackSave(t, b.c, work, &pb.SlackTrigger{Event: pb.SlackTrigger_EVENT_DM})
	if e := schedOf(b.d, id).Events[0].Error; !strings.Contains(e, "Slack is connected on ") {
		t.Fatalf("on b: %q", e)
	}
	// Only another computer of yours may be handed the tokens: not one of a's clients.
	if _, err := try(a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SlackCommand{SlackCommand: &pb.SlackCommand{Action: pb.SlackCommand_ACTION_HAND_OVER}}}); err == nil {
		t.Fatal("handed over to a client")
	}
	r := call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SlackCommand{SlackCommand: &pb.SlackCommand{Action: pb.SlackCommand_ACTION_MOVE_HERE, FromComputerId: a.uid}}})
	if r.GetSlackReply().GetLink().GetTeam() != "Acme" || r.GetSlackReply().GetUserToken() != "" {
		t.Fatalf("moved: %v", r)
	}
	a.d.mu.Lock()
	gone := a.d.slack.tok == nil
	a.d.mu.Unlock()
	if !gone {
		t.Fatal("a kept the tokens")
	}
	if _, err := os.Stat(filepath.Join(a.dir, "slack-tokens.json")); err == nil {
		t.Fatal("a's tokens file is still there")
	}
	waitFor(t, "b's trigger to be able to fire", func() bool { return schedOf(b.d, id).Events[0].Error == "" })
	// From b, a's Slack can be set up too (sent on over the link).
	call(t, b.c, &pb.ClientMessage{Msg: &pb.ClientMessage_SlackCommand{SlackCommand: &pb.SlackCommand{Action: pb.SlackCommand_ACTION_STATUS, ComputerId: a.uid}}})
}
