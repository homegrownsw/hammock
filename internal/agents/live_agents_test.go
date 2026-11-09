package agents

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// fakeJWT is a JWT (unsigned) that expires at exp.
func fakeJWT(exp time.Time) string {
	claims, _ := json.Marshal(map[string]int64{"exp": exp.Unix()})
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
}

// writeFile writes data to path under a fresh folder, making its folders.
func writeLiveFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// setURL points *v at u for the test.
func setURL(t *testing.T, v *string, u string) {
	old := *v
	*v = u
	t.Cleanup(func() { *v = old })
}

// wantWindows checks a's windows' labels and used percentages.
func wantWindows(t *testing.T, a *pb.AgentUsage, err error, want ...any) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Windows) != len(want)/2 {
		t.Fatalf("windows: %v", a.Windows)
	}
	for i, w := range a.Windows {
		if w.Label != want[2*i] || w.UsedPercent != want[2*i+1] {
			t.Errorf("window %d: %s %v, want %v %v", i, w.Label, w.UsedPercent, want[2*i], want[2*i+1])
		}
	}
}

// seen records the requests a fake service had.
type seen struct{ auth, path, body string }

func serve(t *testing.T, answer string, secrets map[string]string) (*seen, string) {
	s := &seen{}
	srv := liveServer(t, func(w http.ResponseWriter, r *http.Request) {
		s.auth, s.path = r.Header.Get("Authorization"), r.URL.RequestURI()
		var b strings.Builder
		if r.Body != nil {
			buf := make([]byte, 4096)
			n, _ := r.Body.Read(buf)
			b.Write(buf[:n])
		}
		s.body = b.String()
		w.Write([]byte(answer))
	}, secrets)
	return s, srv.URL
}

func TestCursorLiveUsage(t *testing.T) {
	t.Setenv("CURSOR_AUTH_TOKEN", "")
	tok := fakeJWT(time.Now().Add(24 * time.Hour))
	s, u := serve(t, `{"billingCycleStart":"1762000000000","billingCycleEnd":"1764592000000","planUsage":{"totalSpend":1234,"limit":2000,"totalPercentUsed":61.7}}`,
		map[string]string{"cursor-access-token": tok})
	setURL(t, &cursorUsageURL, u)
	a, _, err := cursorLiveUsage(context.Background())
	wantWindows(t, a, err, "30d", 61.7)
	if s.auth != "Bearer "+tok || a.Windows[0].ResetsUnix != 1764592000 {
		t.Errorf("%q %v", s.auth, a.Windows[0])
	}
	// An expired token isn't sent.
	keychainSecret = func(context.Context, string, string) ([]byte, error) {
		return []byte(fakeJWT(time.Now().Add(-time.Hour))), nil
	}
	if _, _, err := cursorLiveUsage(context.Background()); err == nil {
		t.Error("expired token used")
	}
}

func TestClineLiveUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLINE_PROVIDER_SETTINGS_PATH", filepath.Join(dir, "providers.json"))
	writeLiveFile(t, filepath.Join(dir, "providers.json"), `{"providers":{"cline":{"settings":{"auth":{"accessToken":"abc","expiresAt":`+
		itoa(time.Now().Add(time.Hour).UnixMilli())+`}}}}}`)
	s, u := serve(t, `{"success":true,"data":{"limits":[{"type":"weekly","percentUsed":40,"resetsAt":"2026-11-12T00:00:00Z"},{"type":"five_hour","percentUsed":23.4,"resetsAt":null},{"type":"other","percentUsed":99}]}}`, nil)
	setURL(t, &clineUsageURL, u)
	a, _, err := clineLiveUsage(context.Background())
	wantWindows(t, a, err, "5h", 23.4, "1w", 40.0)
	if s.auth != "Bearer workos:abc" {
		t.Errorf("auth %q", s.auth)
	}
}

func TestGrokLiveUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GROK_HOME", dir)
	for _, k := range []string{"XAI_API_KEY", "GROK_AUTH", "GROK_CLI_CHAT_PROXY_BASE_URL"} {
		t.Setenv(k, "")
	}
	writeLiveFile(t, filepath.Join(dir, "auth.json"), `{"https://auth.x.ai::abc":{"key":"tok","expires_at":"`+time.Now().Add(48*time.Hour).Format(time.RFC3339)+`","auth_mode":"oauth"}}`)
	s, u := serve(t, `{"config":{"creditUsagePercent":37.5,"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY","start":"2026-11-03T00:00:00Z","end":"2026-11-10T00:00:00Z"}}}`, nil)
	setURL(t, &grokBillingURL, u)
	a, _, err := grokLiveUsage(context.Background())
	wantWindows(t, a, err, "1w", 37.5)
	if s.auth != "Bearer tok" || a.Windows[0].ResetsUnix != unixOf("2026-11-10T00:00:00Z") {
		t.Errorf("%q %v", s.auth, a.Windows[0])
	}
}

func TestKiloLiveUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	writeLiveFile(t, filepath.Join(dir, "kilo", "auth.json"), `{"kilo":{"type":"oauth","access":"tok","expires":`+itoa(time.Now().Add(time.Hour).UnixMilli())+`,"accountId":"org1"}}`)
	s, u := serve(t, `[{"result":{"data":{"json":{"subscription":{"status":"active","currentPeriodBaseCreditsUsd":40,"currentPeriodBonusCreditsUsd":10,"currentPeriodUsageUsd":12.5,"nextBillingAt":"2026-12-01T00:00:00Z"}}}}}]`, nil)
	t.Setenv("KILO_API_URL", u)
	a, _, err := kiloLiveUsage(context.Background())
	wantWindows(t, a, err, "30d", 25.0)
	if s.auth != "Bearer tok" || !strings.HasPrefix(s.path, "/api/trpc/kiloPass.getState?batch=1") {
		t.Errorf("%q %q", s.auth, s.path)
	}
}

func TestKimiLiveUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", dir)
	for _, k := range []string{"KIMI_CODE_BASE_URL", "KIMI_CODE_OAUTH_HOST", "KIMI_OAUTH_HOST"} {
		t.Setenv(k, "")
	}
	writeLiveFile(t, filepath.Join(dir, "credentials", "kimi-code.json"), `{"access_token":"tok","expires_at":`+itoa(time.Now().Add(10*time.Minute).Unix())+`}`)
	s, u := serve(t, `{"usages":{"limit_5h":{"used_ratio":0.42,"reset_time":"2026-11-08T15:00:00Z"},"limit_7d":{"used_ratio":0.1},"limit_month_total":{"used_ratio":0.05}}}`, nil)
	setURL(t, &kimiUsageBase, u)
	a, _, err := kimiLiveUsage(context.Background())
	wantWindows(t, a, err, "5h", 42.0, "1w", 10.0, "30d", 5.0)
	if s.auth != "Bearer tok" || s.path != "/usages" {
		t.Errorf("%q %q", s.auth, s.path)
	}
}

func TestOpenCodeLiveUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("OPENCODE_DB", "")
	db := filepath.Join(dir, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	h, err := sql.Open("sqlite", "file:"+db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(`CREATE TABLE credential (id TEXT, integration_id TEXT, value TEXT, active INTEGER, time_created INTEGER);
		INSERT INTO credential VALUES ('1','opencode-go','{"type":"key","key":"k1","metadata":{"server":"https://opencode.ai/console","orgID":"o1"}}',1,1),
		('2','anthropic','{"type":"key","key":"nope"}',1,2)`); err != nil {
		t.Fatal(err)
	}
	h.Close()
	s, u := serve(t, `{"usage":{"rolling":{"status":"ok","percent":12.5},"weekly":{"percent":30,"resetsAt":"2026-11-10T00:00:00Z"}}}`, nil)
	setURL(t, &openCodeGoUsageURL, u)
	a, _, err := openCodeLiveUsage(context.Background())
	wantWindows(t, a, err, "5h", 12.5, "1w", 30.0)
	if s.auth != "Bearer k1" {
		t.Errorf("auth %q", s.auth)
	}
}

func TestAmpLiveUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("AMP_API_KEY", "")
	s, u := serve(t, `{"ok":true,"result":{"displayText":"Signed in as someone\nAmp Free: $3 / $10 remaining (replenishes +$0.5/hour)\nAmp Pro Tier: agent usage $37.50 of $50 remaining - resets upon renewal in 9 days\nIndividual credits: $5 remaining"}}`, nil)
	t.Setenv("AMP_URL", u)
	writeLiveFile(t, filepath.Join(dir, "amp", "secrets.json"), `{"apiKey@`+u+`/":"key1"}`)
	a, _, err := ampLiveUsage(context.Background())
	wantWindows(t, a, err, "20h", 70.0, "30d", 25.0)
	if s.auth != "Bearer key1" || !strings.Contains(s.body, "userDisplayBalanceInfo") || a.Windows[1].ResetsUnix == 0 {
		t.Errorf("%q %q %v", s.auth, s.body, a.Windows)
	}
	if ws := parseAmpBalance("Amp Free: 40% remaining today (resets daily)", time.Now()); len(ws) != 1 || ws[0].UsedPercent != 60 || ws[0].Label != "1d" {
		t.Errorf("free today: %v", ws)
	}
	if ws := parseAmpBalance("Individual credits: $5 remaining", time.Now()); len(ws) != 0 {
		t.Errorf("credits alone: %v", ws)
	}
}

func TestDroidLiveUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FACTORY_HOME_OVERRIDE", dir)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCMWithNonceSize(block, 16)
	iv := []byte("0123456789abcdef")
	tok := fakeJWT(time.Now().Add(time.Hour))
	sealed := gcm.Seal(nil, iv, []byte(`{"access_token":"`+tok+`","refresh_token":"r"}`), nil)
	ct, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	b64 := base64.StdEncoding.EncodeToString
	writeLiveFile(t, filepath.Join(dir, "auth.v2.keyring"), b64(iv)+":"+b64(tag)+":"+b64(ct))
	s, u := serve(t, `{"usesTokenRateLimitsBilling":true,"limits":{"standard":{"fiveHour":{"usedPercent":12.5,"secondsRemaining":8100},"weekly":{"usedPercent":40,"windowEnd":"2020-01-01T00:00:00Z"}}}}`,
		map[string]string{"Factory CLI": b64(key)})
	setURL(t, &droidLimitsURL, u)
	a, _, err := droidLiveUsage(context.Background())
	wantWindows(t, a, err, "5h", 12.5, "1w", 0.0)
	if s.auth != "Bearer "+tok {
		t.Errorf("auth %q", s.auth)
	}
}

func TestKiroLiveUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KIRO_DATA_DIR", dir)
	h, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "data.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Exec(`CREATE TABLE auth_kv (key TEXT PRIMARY KEY, value TEXT); CREATE TABLE state (key TEXT PRIMARY KEY, value TEXT);
		INSERT INTO auth_kv VALUES ('kirocli:odic:token', '{"access_token":"tok","expires_at":"` + time.Now().Add(30*time.Minute).UTC().Format(time.RFC3339) + `"}');
		INSERT INTO state VALUES ('api.codewhisperer.profile', '{"arn":"arn:aws:codewhisperer:us-east-1:123:profile/X","profile_name":"p"}')`); err != nil {
		t.Fatal(err)
	}
	h.Close()
	s, u := serve(t, `{"usageBreakdownList":[{"resourceType":"CREDIT","currentUsageWithPrecision":120,"usageLimitWithPrecision":1000,"currentOveragesWithPrecision":20,"nextDateReset":1764547200}]}`, nil)
	old := kiroUsageHost["us-east-1"]
	kiroUsageHost["us-east-1"] = u + "/"
	t.Cleanup(func() { kiroUsageHost["us-east-1"] = old })
	a, _, err := kiroLiveUsage(context.Background())
	wantWindows(t, a, err, "30d", 10.0)
	if s.auth != "Bearer tok" || !strings.Contains(s.body, "profile/X") || a.Windows[0].ResetsUnix != 1764547200 {
		t.Errorf("%q %q %v", s.auth, s.body, a.Windows)
	}
}

func TestCodexLiveUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	tok := fakeJWT(time.Now().Add(time.Hour))
	writeLiveFile(t, filepath.Join(dir, "auth.json"), `{"OPENAI_API_KEY":null,"tokens":{"access_token":"`+tok+`","account_id":"acct"}}`)
	var account string
	srv := liveServer(t, func(w http.ResponseWriter, r *http.Request) {
		account = r.Header.Get("ChatGPT-Account-Id")
		w.Write([]byte(`{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":5,"limit_window_seconds":604800,"reset_at":1735920000},
			"secondary_window":{"used_percent":15,"limit_window_seconds":18000,"reset_at":1735401600}}}`))
	}, nil)
	setURL(t, &codexUsageURL, srv.URL)
	a, _, err := codexLiveUsage(context.Background())
	wantWindows(t, a, err, "5h", 15.0, "1w", 5.0)
	if account != "acct" || a.Windows[0].ResetsUnix != 1735401600 {
		t.Errorf("%q %v", account, a.Windows)
	}
}

func TestCopilotLiveUsage(t *testing.T) {
	t.Setenv("COPILOT_GITHUB_TOKEN", "")
	s, u := serve(t, `{"copilot_plan":"individual_pro","quota_reset_date":"2026-12-01","quota_snapshots":{
		"premium_interactions":{"entitlement":300,"remaining":210,"percent_remaining":70.0,"unlimited":false},
		"chat":{"entitlement":-1,"unlimited":true}}}`, map[string]string{"copilot-cli": "gho_abc"})
	setURL(t, &copilotUserURL, u)
	a, _, err := copilotLiveUsage(context.Background())
	wantWindows(t, a, err, "30d", 30.0)
	if s.auth != "token gho_abc" || a.Windows[0].ResetsUnix != unixOf("2026-12-01T00:00:00Z") {
		t.Errorf("%q %v", s.auth, a.Windows)
	}
	// The free plan: the closer of chat and completions to its limit.
	_, u = serve(t, `{"copilot_plan":"free","monthly_quotas":{"chat":50,"completions":2000},"limited_user_quotas":{"chat":40,"completions":1000},"limited_user_reset_date":"2026-12-01"}`,
		map[string]string{"copilot-cli": `{"token":"gho_x"}`})
	setURL(t, &copilotUserURL, u)
	a, _, err = copilotLiveUsage(context.Background())
	wantWindows(t, a, err, "30d", 50.0)
}

func TestGeminiLiveUsage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GEMINI_CLI_HOME", dir)
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	writeLiveFile(t, filepath.Join(dir, ".gemini", "oauth_creds.json"), `{"access_token":"ya29","expiry_date":`+itoa(time.Now().Add(30*time.Minute).UnixMilli())+`}`)
	var paths []string
	srv := liveServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, ":loadCodeAssist") {
			w.Write([]byte(`{"currentTier":{"id":"standard-tier"},"cloudaicompanionProject":"proj-1"}`))
			return
		}
		w.Write([]byte(`{"buckets":[{"modelId":"gemini-2.5-pro","remainingFraction":0.82,"resetTime":"2026-11-09T07:00:00Z"},{"modelId":"gemini-2.5-flash","remainingFraction":0.95}]}`))
	}, nil)
	setURL(t, &geminiCodeAssist, srv.URL+"/v1internal")
	a, _, err := geminiLiveUsage(context.Background())
	if err != nil || len(a.Windows) != 1 || a.Windows[0].Label != "1d" || a.Windows[0].UsedPercent < 17.9 || a.Windows[0].UsedPercent > 18.1 {
		t.Fatalf("%v %v", a, err)
	}
	if len(paths) != 2 {
		t.Errorf("requests: %v", paths)
	}
}

func TestAntigravityUsage(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	out := []byte(`{"status":"SUCCESS","command":{"name":"usage","data":{"groups":[
		{"name":"Gemini Models","buckets":[
			{"id":"gemini-weekly","window":"weekly","remaining_fraction":0.86,"reset_time":"2026-09-17T18:40:27Z"},
			{"id":"gemini-5h","window":"5h","remaining_fraction":0.95,"reset_time":"2026-09-13T03:48:04Z"}]},
		{"name":"Claude and GPT models","buckets":[
			{"id":"3p-weekly","window":"weekly","remaining_fraction":1,"reset_time":"2026-09-20T00:00:00Z"},
			{"id":"3p-5h","window":"5h","remaining_fraction":0.7,"reset_time":"2026-09-13T02:00:00Z"},
			{"id":"off","window":"5h","remaining_fraction":0,"disabled":true}]}]}}}`)
	a, _, err := parseAntigravityUsage(out, now)
	wantWindows(t, a, err, "5h", 30.0, "1w", 14.0)
	if a.Windows[1].ResetsUnix != unixOf("2026-09-17T18:40:27Z") {
		t.Errorf("reset %v", a.Windows[1])
	}
	if _, _, err := parseAntigravityUsage([]byte(`{"status":"AUTH_REQUIRED"}`), now); err == nil {
		t.Error("not signed in: no error")
	}
}

func TestNamesakes(t *testing.T) {
	for _, c := range []struct {
		args []string
		tool bool
	}{
		{[]string{"goose"}, false},
		{[]string{"goose", "session"}, false},
		{[]string{"goose", "run", "-t", "fix it"}, false},
		{[]string{"goose", "up"}, true},
		{[]string{"goose", "-dir", "db/migrations", "postgres", "dbname=x", "status"}, true},
		{[]string{"goose", "--dir=db", "sqlite3", "./app.db", "up"}, true},
	} {
		if got := Goose.Namesake("/opt/homebrew/bin/goose", c.args); got != c.tool {
			t.Errorf("goose %v: migration tool %v", c.args, got)
		}
	}
	if !Amp.Namesake("/Users/x/.cargo/bin/amp", []string{"amp", "notes.txt"}) || Amp.Namesake("/Users/x/.amp/bin/amp", []string{"amp"}) {
		t.Error("amp editor")
	}
}

func TestNoBrowserDirComesBack(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	d, err := noBrowserDir()
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(d)
	d2, err := noBrowserDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d2, "open")); err != nil {
		t.Fatal("not made again:", err)
	}
}
