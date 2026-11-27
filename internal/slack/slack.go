// Package slack is the little of Slack's Web API and Socket Mode that
// Hammock's Slack triggers need (the daemon's slack.go), with a person's
// own Slack app's tokens: a user token (xoxp-) to read as them, and an
// app-level token (xapp-) to open Socket Mode connections. Hammock only
// reads: nothing here posts, reacts or changes anything in Slack.
//
// Rate limits: a 429 is waited out (its Retry-After, up to MaxWait) and
// the call tried again.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultBase is Slack's Web API.
const DefaultBase = "https://slack.com/api/"

// Client calls Slack's Web API with one token.
type Client struct {
	Base    string // "": DefaultBase (tests: a fake's)
	Token   string
	HTTP    *http.Client  // nil: a default one
	MaxWait time.Duration // the longest Retry-After waited (0: 2 minutes)
}

// Error is Slack's refusal: its error code ("invalid_auth").
type Error struct {
	Method, Code string
}

func (e *Error) Error() string { return "Slack's " + e.Method + ": " + e.Code }

// IsAuth: err is Slack refusing the token itself (revoked, wrong kind,
// not allowed): trying again won't help until it's changed.
func IsAuth(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Code {
	case "invalid_auth", "not_authed", "token_revoked", "token_expired", "account_inactive",
		"not_allowed_token_type", "invalid_token", "no_permission", "missing_scope", "team_access_not_granted":
		return true
	}
	return false
}

var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

// Call calls method with params, decoding its answer into out (when not
// nil). Its response headers are returned (auth.test's scopes).
func (c *Client) Call(ctx context.Context, method string, params url.Values, out any) (http.Header, error) {
	base := c.Base
	if base == "" {
		base = DefaultBase
	}
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	maxWait := c.MaxWait
	if maxWait == 0 {
		maxWait = 2 * time.Minute
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+method, strings.NewReader(params.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("User-Agent", "Hammock")
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 5 {
			wait := RetryAfter(resp.Header)
			if wait > maxWait {
				return nil, fmt.Errorf("Slack's %s: rate limited for %s", method, wait.Round(time.Second))
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("Slack's %s: HTTP %d", method, resp.StatusCode)
		}
		var head struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &head); err != nil {
			return nil, fmt.Errorf("Slack's %s: %v", method, err)
		}
		if !head.OK {
			return resp.Header, &Error{Method: method, Code: cmpOr(head.Error, "unknown_error")}
		}
		if out != nil {
			if err := json.Unmarshal(body, out); err != nil {
				return nil, fmt.Errorf("Slack's %s: %v", method, err)
			}
		}
		return resp.Header, nil
	}
}

// RetryAfter is how long a 429 asks to wait (at least a second).
func RetryAfter(h http.Header) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After")))
	if err != nil || n < 1 {
		n = 1
	}
	return time.Duration(n) * time.Second
}

// Auth is who a user token is (auth.test).
type Auth struct {
	URL    string `json:"url"` // the workspace: https://acme.slack.com/
	Team   string `json:"team"`
	TeamID string `json:"team_id"`
	User   string `json:"user"`
	UserID string `json:"user_id"`
	BotID  string `json:"bot_id"`
	// Scopes are the token's, when Slack says them (its x-oauth-scopes
	// header); nil when it doesn't.
	Scopes []string `json:"-"`
}

// AuthTest is who the token is.
func (c *Client) AuthTest(ctx context.Context) (Auth, error) {
	var a Auth
	h, err := c.Call(ctx, "auth.test", nil, &a)
	if err != nil {
		return Auth{}, err
	}
	if s := h.Get("X-OAuth-Scopes"); s != "" {
		for _, x := range strings.Split(s, ",") {
			if x = strings.TrimSpace(x); x != "" {
				a.Scopes = append(a.Scopes, x)
			}
		}
	}
	return a, nil
}

// OpenConnection asks for a Socket Mode connection's address (with the
// app-level token).
func (c *Client) OpenConnection(ctx context.Context) (string, error) {
	var r struct {
		URL string `json:"url"`
	}
	if _, err := c.Call(ctx, "apps.connections.open", nil, &r); err != nil {
		return "", err
	}
	if r.URL == "" {
		return "", errors.New("Slack gave no connection address")
	}
	return r.URL, nil
}

// User is someone in the workspace (or a bot).
type User struct {
	ID          string
	Name        string // their handle
	RealName    string
	DisplayName string
	Bot         bool
}

// Shown is how they're named in Slack: their display name, else their
// full name, else their handle.
func (u User) Shown() string { return cmpOr(u.DisplayName, u.RealName, u.Name, u.ID) }

// Is: they go by s (their id, handle, display or full name; any case, an
// @ before it or not).
func (u User) Is(s string) bool {
	s = strings.TrimPrefix(strings.TrimSpace(s), "@")
	if s == "" {
		return false
	}
	if s == u.ID {
		return true
	}
	for _, n := range []string{u.Name, u.RealName, u.DisplayName} {
		if n != "" && strings.EqualFold(n, s) {
			return true
		}
	}
	return false
}

// UserInfo is someone, by id (users.info).
func (c *Client) UserInfo(ctx context.Context, id string) (User, error) {
	var r struct {
		User struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			IsBot   bool   `json:"is_bot"`
			Profile struct {
				RealName    string `json:"real_name"`
				DisplayName string `json:"display_name"`
			} `json:"profile"`
		} `json:"user"`
	}
	if _, err := c.Call(ctx, "users.info", url.Values{"user": {id}}, &r); err != nil {
		return User{}, err
	}
	u := r.User
	return User{ID: u.ID, Name: u.Name, RealName: u.Profile.RealName, DisplayName: u.Profile.DisplayName, Bot: u.IsBot}, nil
}

// Channel is a conversation: a channel, a DM or a group DM.
type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	IM      bool   `json:"is_im"`
	MPIM    bool   `json:"is_mpim"`
	Private bool   `json:"is_private"`
}

// ConversationInfo is a conversation, by id (conversations.info).
func (c *Client) ConversationInfo(ctx context.Context, id string) (Channel, error) {
	var r struct {
		Channel Channel `json:"channel"`
	}
	if _, err := c.Call(ctx, "conversations.info", url.Values{"channel": {id}}, &r); err != nil {
		return Channel{}, err
	}
	return r.Channel, nil
}

// Channels are the workspace's channels you can see (public ones, and
// private ones you're in), not archived (conversations.list, at most
// maxPages pages).
func (c *Client) Channels(ctx context.Context) ([]Channel, error) {
	const maxPages = 10
	var out []Channel
	cursor := ""
	for page := 0; page < maxPages; page++ {
		var r struct {
			Channels []Channel `json:"channels"`
			Meta     struct {
				Next string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		p := url.Values{"types": {"public_channel,private_channel"}, "exclude_archived": {"true"}, "limit": {"999"}}
		if cursor != "" {
			p.Set("cursor", cursor)
		}
		if _, err := c.Call(ctx, "conversations.list", p, &r); err != nil {
			return nil, err
		}
		out = append(out, r.Channels...)
		if cursor = r.Meta.Next; cursor == "" {
			break
		}
	}
	return out, nil
}

// Message is a message, as an event or in a conversation's history.
type Message struct {
	Type         string `json:"type"`
	Subtype      string `json:"subtype"`
	Channel      string `json:"channel"`
	ChannelType  string `json:"channel_type"` // events: channel, group, im, mpim
	User         string `json:"user"`
	BotID        string `json:"bot_id"`
	Username     string `json:"username"` // a bot's
	Text         string `json:"text"`
	TS           string `json:"ts"`
	ThreadTS     string `json:"thread_ts"`
	ParentUserID string `json:"parent_user_id"`
	BotProfile   *struct {
		Name string `json:"name"`
	} `json:"bot_profile"`
	Hidden bool `json:"hidden"`
}

// IsReply: it's a reply in a thread (not the thread's first message).
func (m Message) IsReply() bool { return m.ThreadTS != "" && m.ThreadTS != m.TS }

// BotName is a bot's name, for a message a bot sent ("" if not one).
func (m Message) BotName() string {
	if m.BotProfile != nil && m.BotProfile.Name != "" {
		return m.BotProfile.Name
	}
	if m.BotID != "" || m.Subtype == "bot_message" {
		return cmpOr(m.Username, m.BotID)
	}
	return ""
}

// Counts: a message someone wrote (or a bot sent), not an edit, a deletion
// or a notice that someone joined.
func (m Message) Counts() bool {
	if m.Hidden || m.TS == "" {
		return false
	}
	switch m.Subtype {
	case "", "bot_message", "thread_broadcast", "file_share", "me_message":
		return true
	}
	return false
}

// History is a conversation's messages after oldest (a ts; "": any), up
// to limit, oldest first (conversations.history).
func (c *Client) History(ctx context.Context, channel, oldest string, limit int) ([]Message, error) {
	var out []Message
	cursor := ""
	for len(out) < limit {
		var r struct {
			Messages []Message `json:"messages"`
			HasMore  bool      `json:"has_more"`
			Meta     struct {
				Next string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		p := url.Values{"channel": {channel}, "limit": {strconv.Itoa(min(limit-len(out), 200))}}
		if oldest != "" {
			p.Set("oldest", oldest)
		}
		if cursor != "" {
			p.Set("cursor", cursor)
		}
		if _, err := c.Call(ctx, "conversations.history", p, &r); err != nil {
			return nil, err
		}
		out = append(out, r.Messages...)
		if cursor = r.Meta.Next; !r.HasMore || cursor == "" {
			break
		}
	}
	for i := range out {
		out[i].Channel = channel
	}
	// Slack gives newest first.
	slices.SortStableFunc(out, func(a, b Message) int { return CompareTS(a.TS, b.TS) })
	return out, nil
}

// Message is one message, by its ts: in the conversation, or a reply in a
// thread (conversations.history, then conversations.replies).
func (c *Client) Message(ctx context.Context, channel, ts string) (Message, error) {
	one := func(method string, p url.Values) (Message, bool, error) {
		var r struct {
			Messages []Message `json:"messages"`
		}
		if _, err := c.Call(ctx, method, p, &r); err != nil {
			return Message{}, false, err
		}
		for _, m := range r.Messages {
			if m.TS == ts {
				m.Channel = channel
				return m, true, nil
			}
		}
		return Message{}, false, nil
	}
	p := url.Values{"channel": {channel}, "latest": {ts}, "oldest": {ts}, "inclusive": {"true"}, "limit": {"1"}}
	if m, ok, err := one("conversations.history", p); err != nil || ok {
		return m, err
	}
	p = url.Values{"channel": {channel}, "ts": {ts}, "latest": {ts}, "oldest": {ts}, "inclusive": {"true"}, "limit": {"1"}}
	if m, ok, err := one("conversations.replies", p); err != nil || ok {
		return m, err
	}
	return Message{}, errors.New("that message isn't there any more")
}

// CompareTS orders two of Slack's timestamps ("1700000000.000100").
func CompareTS(a, b string) int {
	as, af, _ := strings.Cut(a, ".")
	bs, bf, _ := strings.Cut(b, ".")
	if len(as) != len(bs) {
		return len(as) - len(bs)
	}
	if c := strings.Compare(as, bs); c != 0 {
		return c
	}
	for len(af) < len(bf) {
		af += "0"
	}
	for len(bf) < len(af) {
		bf += "0"
	}
	return strings.Compare(af, bf)
}

// TSTime is a Slack timestamp as a time (zero if it isn't one).
func TSTime(ts string) time.Time {
	s, f, _ := strings.Cut(ts, ".")
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	for len(f) < 6 {
		f += "0"
	}
	usec, _ := strconv.ParseInt(f[:6], 10, 64)
	return time.Unix(sec, usec*1000)
}

// TimeTS is a time as a Slack timestamp.
func TimeTS(t time.Time) string {
	return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)
}

// Permalink is a message's link in the workspace at base (auth.test's url):
// a reply's opens its thread.
func Permalink(base, channel, ts, threadTS string) string {
	if base == "" || channel == "" || ts == "" {
		return ""
	}
	u := strings.TrimSuffix(base, "/") + "/archives/" + channel + "/p" + strings.Replace(ts, ".", "", 1)
	if threadTS != "" && threadTS != ts {
		u += "?thread_ts=" + threadTS + "&cid=" + channel
	}
	return u
}

func cmpOr(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
