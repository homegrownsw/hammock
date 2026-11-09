package agents

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// liveServer points the live fetchers' requests at handler, with the
// Keychain answering secrets[service].
func liveServer(t *testing.T, handler http.HandlerFunc, secrets map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	oldClient, oldKeychain := liveClient, keychainSecret
	liveClient = srv.Client()
	keychainSecret = func(_ context.Context, service, _ string) ([]byte, error) {
		if s, ok := secrets[service]; ok {
			return []byte(s), nil
		}
		return nil, errNoSignIn
	}
	t.Cleanup(func() { liveClient, keychainSecret = oldClient, oldKeychain })
	return srv
}

func TestClaudeLiveUsage(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	var auth, beta string
	srv := liveServer(t, func(w http.ResponseWriter, r *http.Request) {
		auth, beta = r.Header.Get("Authorization"), r.Header.Get("anthropic-beta")
		w.Write([]byte(`{"five_hour":{"utilization":67,"resets_at":"2026-11-07T12:09:59+00:00"},"seven_day":{"utilization":22,"resets_at":"2026-11-12T11:59:59+00:00"}}`))
	}, map[string]string{claudeKeychainItem: `{"claudeAiOauth":{"accessToken":"tok","expiresAt":` + itoa(time.Now().Add(time.Hour).UnixMilli()) + `}}`})
	old := claudeUsageURL
	claudeUsageURL = srv.URL
	t.Cleanup(func() { claudeUsageURL = old })

	a, _, err := claudeLiveUsage(context.Background())
	if err != nil || len(a.Windows) != 2 || a.Windows[0].UsedPercent != 67 || a.Windows[1].Label != "1w" {
		t.Fatalf("%v %v", a, err)
	}
	if auth != "Bearer tok" || beta != "oauth-2025-04-20" {
		t.Errorf("headers: %q %q", auth, beta)
	}

	// An expired sign-in isn't sent.
	keychainSecret = func(context.Context, string, string) ([]byte, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"old","expiresAt":1}}`), nil
	}
	auth = ""
	if _, _, err := claudeLiveUsage(context.Background()); !errors.Is(err, errNoSignIn) || auth != "" {
		t.Errorf("expired: %v (sent %q)", err, auth)
	}
}

func TestFetchJSONThrottled(t *testing.T) {
	srv := liveServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}, nil)
	var out any
	wait, err := fetchJSON(context.Background(), liveRequest{url: srv.URL}, &out)
	if err == nil || wait != 2*time.Minute {
		t.Errorf("%v %v", wait, err)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
