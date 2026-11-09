package agents

// Live usage readers (live.go) for Amp, Factory's Droid and Kiro, whose
// sign-ins or answers need more than a bearer token and a percentage.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// ---- Amp ----

// ampURL is Amp's service (AMP_URL moves it: serviceURL), with a trailing
// slash, as its secrets are keyed.
func ampURL() string {
	return strings.TrimRight(serviceURL("AMP_URL", "https://ampcode.com"), "/") + "/"
}

// ampLiveUsage: Amp's balance as `amp usage` shows it (its free daily
// allowance, or a paid tier's monthly usage), asked with the API key Amp's
// CLI keeps in $XDG_DATA_HOME/amp/secrets.json (or AMP_API_KEY). Credits
// bought on their own have no limit, so they aren't a window. Facts from
// CodexBar's AmpUsageFetcher.swift and AmpUsageParser.swift.
func ampLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	key := os.Getenv("AMP_API_KEY")
	if key == "" {
		var secrets map[string]any
		if err := readSignIn(filepath.Join(xdg("XDG_DATA_HOME", ".local", "share"), "amp", "secrets.json"), &secrets); err != nil {
			return nil, 0, err
		}
		key, _ = secrets["apiKey@"+ampURL()].(string)
	}
	if key == "" {
		return nil, 0, errNoSignIn
	}
	var r struct {
		OK     bool `json:"ok"`
		Result struct {
			DisplayText string `json:"displayText"`
		} `json:"result"`
	}
	wait, err := fetchJSON(ctx, liveRequest{method: "POST", url: ampURL() + "api/internal?userDisplayBalanceInfo",
		header: map[string]string{"Authorization": "Bearer " + key},
		body:   map[string]any{"method": "userDisplayBalanceInfo", "params": map[string]any{}}}, &r)
	if err != nil {
		return nil, wait, err
	}
	if !r.OK {
		return nil, 0, errNoSignIn
	}
	return liveUsageOf(parseAmpBalance(r.Result.DisplayText, time.Now())...)
}

var (
	// "Amp Free: $3.20 / $10 remaining (replenishes +$0.42/hour)"
	ampFreeDollarsRE = regexp.MustCompile(`Amp Free: \$([\d.,]+) / \$([\d.,]+) remaining(?: \(replenishes \+\$([\d.,]+)/hour\))?`)
	// "Amp Free: 40% remaining today (resets daily)"
	ampFreePercentRE = regexp.MustCompile(`Amp Free: (\d+(?:\.\d+)?)% remaining today`)
	// "Amp Pro Tier: agent usage $12.50 of $50 remaining … resets upon renewal in 9 days"
	ampTierRE = regexp.MustCompile(`Tier: agent usage \$([\d.,]+) of \$([\d.,]+) remaining.*?(?:in (\d+) days?)?(?:\n|$)`)
	// "Subscription Pro: 70% other usage and 90% orb usage remaining - resets upon renewal in 9 days"
	ampSubscriptionRE = regexp.MustCompile(`Subscription [^:]*: (\d+(?:\.\d+)?)% other usage.*?remaining(?:.*?in (\d+) days?)?`)
)

// parseAmpBalance reads the windows in `amp usage`'s text.
func parseAmpBalance(text string, now time.Time) []*pb.UsageWindow {
	num := func(s string) float64 { f, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64); return f }
	inDays := func(s string) int64 {
		if d := num(s); d > 0 {
			return now.Add(time.Duration(d) * 24 * time.Hour).Unix()
		}
		return 0
	}
	var ws []*pb.UsageWindow
	if m := ampFreeDollarsRE.FindStringSubmatch(text); m != nil {
		left, total := num(m[1]), num(m[2])
		minutes := int32(oneDay) // it refills by the hour; a day, unless it says
		if per := num(m[3]); per > 0 && total > 0 {
			minutes = int32(total / per * 60)
		}
		ws = append(ws, liveWindow(minutes, percentOf(total-left, total), 0))
	} else if m := ampFreePercentRE.FindStringSubmatch(text); m != nil {
		ws = append(ws, liveWindow(oneDay, 100-num(m[1]), 0))
	}
	if m := ampTierRE.FindStringSubmatch(text); m != nil {
		left, total := num(m[1]), num(m[2])
		ws = append(ws, liveWindow(oneMonth, percentOf(total-left, total), inDays(m[3])))
	} else if m := ampSubscriptionRE.FindStringSubmatch(text); m != nil {
		ws = append(ws, liveWindow(oneMonth, 100-num(m[1]), inDays(m[2])))
	}
	return ws
}

// ---- Factory (Droid) ----

// droidLimitsURL is Factory's usage limits (CodexBar
// FactoryStatusProbe.swift).
var droidLimitsURL = "https://api.factory.ai/api/billing/limits"

// droidLiveUsage: Factory's 5-hour, weekly and monthly limits, with
// droid's sign-in: AES-GCM encrypted in ~/.factory (FACTORY_HOME_OVERRIDE),
// its key in the Keychain (or beside it). Its token is short-lived and
// refreshing it would rotate droid's own, so this answers while droid's
// is fresh.
func droidLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	token, err := droidToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	type window struct {
		UsedPercent      *float64 `json:"usedPercent"`
		WindowEnd        flexTime `json:"windowEnd"`
		SecondsRemaining *float64 `json:"secondsRemaining"`
	}
	var r struct {
		Limits struct {
			Standard map[string]window `json:"standard"`
		} `json:"limits"`
	}
	wait, err := fetchJSON(ctx, liveRequest{url: droidLimitsURL, header: map[string]string{"Authorization": "Bearer " + token}}, &r)
	if err != nil {
		return nil, wait, err
	}
	now := time.Now()
	var ws []*pb.UsageWindow
	for key, m := range map[string]int32{"fiveHour": fiveHours, "weekly": oneWeek, "monthly": oneMonth} {
		w, ok := r.Limits.Standard[key]
		if !ok || w.UsedPercent == nil {
			continue
		}
		reset, used := w.WindowEnd.unix(), *w.UsedPercent
		if w.SecondsRemaining != nil {
			reset = now.Add(time.Duration(*w.SecondsRemaining) * time.Second).Unix()
		} else if reset != 0 && reset < now.Unix() {
			used = 0 // the window has ended: a new one starts with the next use
		}
		ws = append(ws, liveWindow(m, used, reset))
	}
	return liveUsageOf(ws...)
}

// droidToken decrypts droid's saved sign-in and returns its access token,
// if it's still good.
func droidToken(ctx context.Context) (string, error) {
	dir := envOr("FACTORY_HOME_OVERRIDE", home(".factory"))
	for _, store := range []struct{ file, account string }{
		{"auth.v2.keyring", "auth-encryption-key"},
		{"auth.v2.loginkeychain", "auth-encryption-key-security-cli"},
		{"auth.v2.file", ""},
	} {
		data, err := os.ReadFile(filepath.Join(dir, store.file))
		if err != nil {
			continue
		}
		var key []byte
		if store.account != "" {
			key, err = keychainSecret(ctx, "Factory CLI", store.account)
		} else {
			key, err = os.ReadFile(filepath.Join(dir, "auth.v2.key"))
		}
		if err != nil {
			continue // its key not to be had (denied in the Keychain): the next store
		}
		plain, err := droidDecrypt(data, key)
		if err != nil {
			continue
		}
		var a struct {
			AccessToken string `json:"access_token"`
		}
		if json.Unmarshal(plain, &a) != nil || a.AccessToken == "" || expired(jwtExpiry(a.AccessToken)) {
			continue
		}
		return a.AccessToken, nil
	}
	return "", errNoSignIn
}

// droidDecrypt opens droid's "iv:tag:ciphertext" (each base64), AES-256-GCM
// with a 16-byte nonce, under key (base64).
func droidDecrypt(data, key []byte) ([]byte, error) {
	parts := strings.Split(strings.TrimSpace(string(data)), ":")
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(key)))
	if len(parts) != 3 || err != nil {
		return nil, errors.New("droid's sign-in: not in the format expected")
	}
	var b [3][]byte
	for i, p := range parts {
		if b[i], err = base64.StdEncoding.DecodeString(p); err != nil {
			return nil, err
		}
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, len(b[0]))
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, b[0], append(b[2], b[1]...), nil)
}

// ---- Kiro ----

// kiroUsageHost is where Kiro's usage is asked, by its profile's region
// (CodexBar KiroUsageLimitsAPI.swift).
var kiroUsageHost = map[string]string{
	"us-east-1":    "https://codewhisperer.us-east-1.amazonaws.com/",
	"eu-central-1": "https://q.eu-central-1.amazonaws.com/",
}

// kiroLiveUsage: this month's Kiro credits, with kiro-cli's sign-in from its
// database (read only). Its token lasts about an hour, refreshed by
// kiro-cli as it runs.
func kiroLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	dir := os.Getenv("KIRO_DATA_DIR")
	if dir == "" {
		dir = home("Library", "Application Support", "kiro-cli")
	}
	db := filepath.Join(dir, "data.sqlite3")
	if _, err := os.Stat(db); err != nil {
		return nil, 0, errNoSignIn
	}
	h, err := sql.Open("sqlite", "file:"+db+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, 0, err
	}
	defer h.Close()
	type kiroToken struct {
		AccessToken string   `json:"access_token"`
		ExpiresAt   flexTime `json:"expires_at"`
	}
	// Its sign-in with AWS (OIDC), else with Google or GitHub: the first
	// that hasn't expired.
	var tok kiroToken
	var v string
	for _, k := range []string{"kirocli:odic:token", "kirocli:social:token"} {
		var t kiroToken
		if h.QueryRowContext(ctx, `SELECT value FROM auth_kv WHERE key = ?`, k).Scan(&v) == nil && json.Unmarshal([]byte(v), &t) == nil && t.AccessToken != "" && !expired(t.ExpiresAt.time()) {
			tok = t
			break
		}
	}
	if tok.AccessToken == "" {
		return nil, 0, errNoSignIn
	}
	var profile struct {
		ARN string `json:"arn"`
	}
	body := map[string]string{}
	host := kiroUsageHost["us-east-1"]
	if h.QueryRowContext(ctx, `SELECT value FROM state WHERE key = 'api.codewhisperer.profile'`).Scan(&v) == nil && json.Unmarshal([]byte(v), &profile) == nil && profile.ARN != "" {
		body["profileArn"] = profile.ARN
		if f := strings.Split(profile.ARN, ":"); len(f) > 3 {
			if host = kiroUsageHost[f[3]]; host == "" {
				return nil, 0, errors.New("kiro: a region Hammock doesn't know: " + f[3])
			}
		}
	}
	var r struct {
		List []struct {
			ResourceType string   `json:"resourceType"`
			Used         float64  `json:"currentUsageWithPrecision"`
			Limit        float64  `json:"usageLimitWithPrecision"`
			Overages     float64  `json:"currentOveragesWithPrecision"`
			NextReset    flexTime `json:"nextDateReset"`
		} `json:"usageBreakdownList"`
		NextReset flexTime `json:"nextDateReset"`
	}
	wait, err := fetchJSON(ctx, liveRequest{method: "POST", url: host, body: body, header: map[string]string{
		"Authorization": "Bearer " + tok.AccessToken,
		"Content-Type":  "application/x-amz-json-1.0",
		"X-Amz-Target":  "AmazonCodeWhispererService.GetUsageLimits",
	}}, &r)
	if err != nil {
		return nil, wait, err
	}
	for _, u := range r.List {
		if u.ResourceType != "CREDIT" || u.Limit <= 0 {
			continue
		}
		reset := u.NextReset.unix()
		if reset == 0 {
			reset = r.NextReset.unix()
		}
		return liveUsageOf(liveWindow(oneMonth, percentOf(u.Used-u.Overages, u.Limit), reset))
	}
	return liveUsageOf()
}
