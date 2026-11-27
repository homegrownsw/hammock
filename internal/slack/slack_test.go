package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A fake Slack: no real Slack calls in tests.
func fakeAPI(t *testing.T, h func(method string, form url.Values, w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Header.Get("Authorization") == "" {
			t.Errorf("%s: no token", r.URL.Path)
		}
		h(strings.TrimPrefix(r.URL.Path, "/api/"), r.Form, w)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAuthTestAndScopes(t *testing.T) {
	srv := fakeAPI(t, func(method string, _ url.Values, w http.ResponseWriter) {
		if method != "auth.test" {
			t.Fatalf("called %s", method)
		}
		w.Header().Set("X-OAuth-Scopes", "channels:history, users:read")
		_, _ = w.Write([]byte(`{"ok":true,"url":"https://acme.slack.com/","team":"Acme","team_id":"T1","user":"sam","user_id":"U1"}`))
	})
	c := &Client{Base: srv.URL + "/api/", Token: "xoxp-test"}
	a, err := c.AuthTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a.Team != "Acme" || a.UserID != "U1" || len(a.Scopes) != 2 {
		t.Fatalf("got %+v", a)
	}
	if m := MissingScopes(a.Scopes); len(m) != len(UserScopes)-2 {
		t.Fatalf("missing %v", m)
	}
	if MissingScopes(nil) != nil {
		t.Fatal("scopes not known should be none missing")
	}
}

func TestErrorsAndRateLimits(t *testing.T) {
	var calls atomic.Int32
	srv := fakeAPI(t, func(method string, _ url.Values, w http.ResponseWriter) {
		switch method {
		case "users.info":
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U2","name":"riley","profile":{"display_name":"Ri","real_name":"Riley Example"}}}`))
		default:
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
		}
	})
	c := &Client{Base: srv.URL + "/api/", Token: "xoxp-test"}
	u, err := c.UserInfo(context.Background(), "U2")
	if err != nil || u.Shown() != "Ri" || !u.Is("@riley") || !u.Is("riley example") || u.Is("sam") {
		t.Fatalf("got %+v %v", u, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("a 429 should be tried again after its Retry-After (%d calls)", calls.Load())
	}
	_, err = c.AuthTest(context.Background())
	if !IsAuth(err) {
		t.Fatalf("invalid_auth should be an auth error: %v", err)
	}
	c.MaxWait = time.Millisecond
	calls.Store(0)
	if _, err := c.UserInfo(context.Background(), "U2"); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("a wait over MaxWait should fail: %v", err)
	}
}

func TestHistoryOldestFirstAndPaged(t *testing.T) {
	srv := fakeAPI(t, func(method string, f url.Values, w http.ResponseWriter) {
		if f.Get("oldest") != "100.000000" {
			t.Errorf("oldest %q", f.Get("oldest"))
		}
		if f.Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"103.0","text":"c"},{"ts":"102.0","text":"b"}],"has_more":true,"response_metadata":{"next_cursor":"x"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"101.5","text":"a"}],"has_more":false}`))
	})
	c := &Client{Base: srv.URL + "/api/", Token: "xoxp-test"}
	ms, err := c.History(context.Background(), "C1", "100.000000", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 3 || ms[0].Text != "a" || ms[2].Text != "c" || ms[0].Channel != "C1" {
		t.Fatalf("got %+v", ms)
	}
}

func TestTimestampsAndLinks(t *testing.T) {
	if CompareTS("99.9", "100.0") >= 0 || CompareTS("100.5", "100.40") <= 0 || CompareTS("1.000100", "1.0001") != 0 {
		t.Fatal("CompareTS")
	}
	ts := TimeTS(time.Unix(1700000000, 123456000))
	if ts != "1700000000.123456" || !TSTime(ts).Equal(time.Unix(1700000000, 123456000)) {
		t.Fatalf("ts %s", ts)
	}
	if p := Permalink("https://acme.slack.com/", "C1", "1700000000.123456", ""); p != "https://acme.slack.com/archives/C1/p1700000000123456" {
		t.Fatal(p)
	}
	if p := Permalink("https://acme.slack.com/", "C1", "2.000002", "1.000001"); !strings.HasSuffix(p, "?thread_ts=1.000001&cid=C1") {
		t.Fatal(p)
	}
}

func TestPlainTextAndMentions(t *testing.T) {
	text := "hi <@U1> and <@U2|sam>, see <#C9|deploys> &amp; <https://example.com|the docs> <!here> &lt;3"
	got := PlainText(text, func(id string) string { return map[string]string{"U1": "Al"}[id] })
	want := "hi @Al and @sam, see #deploys & the docs (https://example.com) @here <3"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if !MentionsUser(text, "U1") || MentionsUser(text, "U3") || MentionsUser("<!channel> <!subteam^S1|@team>", "U1") {
		t.Fatal("MentionsUser")
	}
}

func TestCheckTokens(t *testing.T) {
	if CheckTokens("xoxp-1", "xapp-1") != "" || CheckTokens("xoxb-1", "xapp-1") == "" || CheckTokens("xoxp-1", "xoxp-2") == "" || CheckTokens("xoxp-1 x", "xapp-1") == "" {
		t.Fatal("CheckTokens")
	}
	var m map[string]any
	u, _ := url.Parse(ManifestURL())
	if err := json.Unmarshal([]byte(u.Query().Get("manifest_json")), &m); err != nil || u.Query().Get("new_app") != "1" {
		t.Fatalf("manifest URL: %v", err)
	}
}

// A fake Socket Mode server: hello, an event (which must be acked), then
// a refresh; the socket connects again at once.
func TestSocketAcksAndReconnects(t *testing.T) {
	var (
		mu    sync.Mutex
		acks  []string
		opens atomic.Int32
	)
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		n := opens.Load()
		_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"hello"}`))
		ev := `{"type":"events_api","envelope_id":"e` + string(rune('0'+n)) + `","payload":{"event_id":"Ev1","event":{"type":"message","channel":"C1","user":"U2","text":"hi","ts":"1.0"}}}`
		_ = c.Write(ctx, websocket.MessageText, []byte(ev))
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var a struct {
			ID string `json:"envelope_id"`
		}
		_ = json.Unmarshal(data, &a)
		mu.Lock()
		acks = append(acks, a.ID)
		mu.Unlock()
		if n == 1 {
			_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"disconnect","reason":"refresh_requested"}`))
		}
		_, _, _ = c.Read(ctx) // until it closes
	}))
	defer ws.Close()
	api := fakeAPI(t, func(method string, _ url.Values, w http.ResponseWriter) {
		opens.Add(1)
		_, _ = w.Write([]byte(`{"ok":true,"url":"ws` + strings.TrimPrefix(ws.URL, "http") + `"}`))
	})
	events := make(chan Event, 4)
	var opened atomic.Int32
	s := &Socket{
		App:     &Client{Base: api.URL + "/api/", Token: "xapp-test"},
		OnEvent: func(e Event) { events <- e },
		OnState: func(open bool, err error) {
			if open {
				opened.Add(1)
			}
		},
		Backoff: func(int) time.Duration { return 10 * time.Millisecond },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	for i := 0; i < 2; i++ {
		select {
		case e := <-events:
			if e.Type != "message" || e.ID != "Ev1" {
				t.Fatalf("event %+v", e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
		}
	}
	for i := 0; i < 500; i++ { // (the ack is written before the event is handed on; the fake reads it after)
		mu.Lock()
		n := len(acks)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(acks) < 2 || acks[0] != "e1" || acks[1] != "e2" || opened.Load() < 2 {
		t.Fatalf("acks %v, opened %d", acks, opened.Load())
	}
}

func TestSocketStopsOnRefusedToken(t *testing.T) {
	api := fakeAPI(t, func(string, url.Values, http.ResponseWriter) {})
	api.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	})
	var why error
	s := &Socket{App: &Client{Base: api.URL + "/api/", Token: "xapp-bad"}, OnState: func(_ bool, err error) { why = err }}
	err := s.Run(context.Background())
	if !IsAuth(err) || !IsAuth(why) {
		t.Fatalf("got %v / %v", err, why)
	}
}
