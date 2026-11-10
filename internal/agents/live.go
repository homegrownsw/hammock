package agents

// Usage limits asked of each agent's own service (Agent.LiveUsage), only
// while usage-live is on in Hammock's config (Settings ▸ Agents), with the
// sign-in the agent's CLI keeps on this Mac. The sign-in is read for each
// request, sent only to that agent's own service, and never kept or written
// back: an expired one is skipped until the CLI refreshes it, and limits
// then come from what the CLI keeps on disk, if anything.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// errNoSignIn: the agent's CLI isn't signed in here (or its sign-in has
// expired), so there's nothing to ask with.
var errNoSignIn = errors.New("not signed in")

// liveClient is the HTTP client live usage asks with (a test's server's).
var liveClient = &http.Client{Timeout: 20 * time.Second}

// liveRequest is one request to an agent's service.
type liveRequest struct {
	method, url string
	header      map[string]string
	body        any // sent as JSON when set
}

// fetchJSON makes r and decodes its JSON answer into out. wait is how long
// a throttled answer (429, 503) asked to wait.
func fetchJSON(ctx context.Context, r liveRequest, out any) (wait time.Duration, err error) {
	var body io.Reader
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	method := r.method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, r.url, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "hammock")
	req.Header.Set("Accept", "application/json")
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range r.header {
		req.Header.Set(k, v)
	}
	resp, err := liveClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode/100 != 2 {
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s > 0 {
			wait = min(time.Duration(s)*time.Second, time.Hour)
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return wait, fmt.Errorf("%s: %w", resp.Status, errNoSignIn)
		}
		return wait, fmt.Errorf("usage: %s", resp.Status)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return 0, fmt.Errorf("usage: %w", err)
	}
	return 0, nil
}

// keychainSecret is the password of the Keychain item service (and account,
// if given), as the agent's CLI saved it. macOS may ask once to allow it.
var keychainSecret = func(ctx context.Context, service, account string) ([]byte, error) {
	args := []string{"find-generic-password", "-s", service}
	if account != "" {
		args = append(args, "-a", account)
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/security", append(args, "-w")...).Output()
	if err != nil {
		return nil, fmt.Errorf("keychain %q: %w", service, errNoSignIn)
	}
	return bytes.TrimSpace(out), nil
}

// serviceURL is the agent's service, def, or where env moves it, as its
// CLI does, but only to an https one (or http on this Mac), so a sign-in
// isn't sent in the clear.
func serviceURL(env, def string) string {
	v := strings.TrimSpace(os.Getenv(env))
	if v == "" {
		return def
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return def
	}
	switch h := u.Hostname(); {
	case u.Scheme == "https", u.Scheme == "http" && (h == "localhost" || h == "127.0.0.1" || h == "::1"):
		return v
	}
	return def
}

// readSignIn decodes the JSON file at path (an agent's saved sign-in).
func readSignIn(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, errNoSignIn)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// expired: a token that expires at (zero: unknown) has, or will within a
// minute.
func expired(at time.Time) bool { return !at.IsZero() && time.Until(at) < time.Minute }

// percentOf is used out of limit as a percentage (0 with no limit).
func percentOf(used, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return min(max(used/limit*100, 0), 100)
}

// unixOf parses an RFC 3339 time as Unix seconds (0 when it isn't one).
func unixOf(s string) int64 {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.Unix()
	}
	return 0
}

// liveWindow is a usage window minutes long, used percent of it, resetting
// at reset (Unix seconds, 0: not known).
func liveWindow(minutes int32, used float64, reset int64) *pb.UsageWindow {
	return &pb.UsageWindow{Label: windowLabel(minutes), UsedPercent: math.Round(min(max(used, 0), 100)*100) / 100, WindowMinutes: minutes, ResetsUnix: reset}
}

// Window lengths, in minutes.
const (
	fiveHours = 300
	oneDay    = 1440
	oneWeek   = 10080
	oneMonth  = 43200
)

// liveUsageOf is an answer with windows ws (nil ones left out), shortest
// first; an error when there are none.
func liveUsageOf(ws ...*pb.UsageWindow) (*pb.AgentUsage, time.Duration, error) {
	a := &pb.AgentUsage{FetchedUnixMs: time.Now().UnixMilli()}
	for _, w := range ws {
		if w != nil {
			a.Windows = append(a.Windows, w)
		}
	}
	if len(a.Windows) == 0 {
		return nil, 0, errors.New("usage: no limits in the answer")
	}
	sort.SliceStable(a.Windows, func(i, j int) bool { return a.Windows[i].WindowMinutes < a.Windows[j].WindowMinutes })
	return a, 0, nil
}

// jwtExpiry is when a JWT expires (zero when it isn't one, or doesn't say).
func jwtExpiry(token string) time.Time {
	parts := strings.Split(strings.TrimPrefix(token, "workos:"), ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}
	}
	var c struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(data, &c) != nil || c.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(c.Exp), 0)
}

// flexNum is a number that may come as a JSON string ("1762000000000", as
// Connect sends int64s).
type flexNum float64

func (n *flexNum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // not a number: left as 0
	}
	*n = flexNum(f)
	return nil
}

// flexTime is a time given as RFC 3339, or as Unix seconds or milliseconds
// (number or string).
type flexTime time.Time

func (t *flexTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if u := unixOf(s); u != 0 {
		*t = flexTime(time.Unix(u, 0))
		return nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		if f > 1e12 {
			*t = flexTime(time.UnixMilli(int64(f)))
		} else {
			*t = flexTime(time.Unix(int64(f), 0))
		}
	}
	return nil
}

// unix is t's Unix seconds (0 when unset).
func (t flexTime) unix() int64 {
	if time.Time(t).IsZero() {
		return 0
	}
	return time.Time(t).Unix()
}

// time is t (zero when unset).
func (t flexTime) time() time.Time { return time.Time(t) }
