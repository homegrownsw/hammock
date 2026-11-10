package agents

// Live usage readers (live.go) for agents whose CLI keeps a bearer token in
// a file or the Keychain and whose service answers with percentages. None
// refreshes a sign-in: their refresh tokens rotate, and refreshing would
// sign the CLI itself out. Sources: CodexBar (steipete/CodexBar,
// Sources/CodexBarCore/Providers) and t3code (pingdotgg/t3code,
// packages/provider-*/src/server/usageLimits.ts), and each CLI's own source
// where it's open; every endpoint is the service's own, undocumented.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// ---- Cursor ----

// cursorUsageURL is the RPC cursor-agent's /usage asks (t3code
// provider-cursor usageLimits.ts).
var cursorUsageURL = "https://api2.cursor.sh/aiserver.v1.DashboardService/GetCurrentPeriodUsage"

// cursorLiveUsage: this billing period's use of Cursor's plan, with
// cursor-agent's sign-in (Keychain service cursor-access-token, account
// cursor-user; CURSOR_AUTH_TOKEN instead when set).
func cursorLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	token := os.Getenv("CURSOR_AUTH_TOKEN")
	if token == "" {
		b, err := keychainSecret(ctx, "cursor-access-token", "cursor-user")
		if err != nil {
			return nil, 0, err
		}
		token = string(b)
	}
	if token == "" || expired(jwtExpiry(token)) {
		return nil, 0, errNoSignIn
	}
	var r struct {
		Start     flexNum `json:"billingCycleStart"` // Unix ms
		End       flexNum `json:"billingCycleEnd"`
		PlanUsage *struct {
			TotalPercentUsed *float64 `json:"totalPercentUsed"`
			Limit            flexNum  `json:"limit"`
			TotalSpend       flexNum  `json:"totalSpend"`
		} `json:"planUsage"`
	}
	wait, err := fetchJSON(ctx, liveRequest{method: "POST", url: cursorUsageURL, body: struct{}{}, header: map[string]string{
		"Authorization":            "Bearer " + token,
		"Connect-Protocol-Version": "1",
		"x-cursor-client-type":     "cli",
	}}, &r)
	if err != nil {
		return nil, wait, err
	}
	if r.PlanUsage == nil {
		return liveUsageOf()
	}
	used := percentOf(float64(r.PlanUsage.TotalSpend), float64(r.PlanUsage.Limit))
	if r.PlanUsage.TotalPercentUsed != nil {
		used = *r.PlanUsage.TotalPercentUsed
	}
	minutes := int32(oneMonth)
	if r.End > r.Start && r.Start > 0 {
		minutes = int32((r.End - r.Start) / 60000)
	}
	return liveUsageOf(liveWindow(minutes, used, int64(r.End)/1000))
}

// ---- Cline ----

// clineUsageURL is ClinePass's usage limits (CodexBar's clinepass plugin).
var clineUsageURL = "https://api.cline.bot/api/v1/users/me/plan/usage-limits"

// clineProvidersFile is where Cline's CLI keeps its providers' settings,
// its sign-in among them.
func clineProvidersFile() string {
	if p := os.Getenv("CLINE_PROVIDER_SETTINGS_PATH"); p != "" {
		return p
	}
	if d := os.Getenv("CLINE_DATA_DIR"); d != "" {
		return filepath.Join(d, "settings", "providers.json")
	}
	if d := os.Getenv("CLINE_DIR"); d != "" {
		return filepath.Join(d, "data", "settings", "providers.json")
	}
	return home(".cline", "data", "settings", "providers.json")
}

// clineLiveUsage: ClinePass's 5-hour, weekly and monthly limits, with the
// Cline CLI's sign-in.
func clineLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	var f struct {
		Providers struct {
			Cline struct {
				Settings struct {
					Auth struct {
						AccessToken string  `json:"accessToken"`
						ExpiresAt   flexNum `json:"expiresAt"` // Unix ms
					} `json:"auth"`
				} `json:"settings"`
			} `json:"cline"`
		} `json:"providers"`
	}
	if err := readSignIn(clineProvidersFile(), &f); err != nil {
		return nil, 0, err
	}
	auth := f.Providers.Cline.Settings.Auth
	if auth.AccessToken == "" || (auth.ExpiresAt > 0 && expired(time.UnixMilli(int64(auth.ExpiresAt)))) {
		return nil, 0, errNoSignIn
	}
	token := auth.AccessToken
	if !strings.HasPrefix(token, "workos:") {
		token = "workos:" + token
	}
	var r struct {
		Data struct {
			Limits []struct {
				Type        string   `json:"type"`
				PercentUsed float64  `json:"percentUsed"`
				ResetsAt    flexTime `json:"resetsAt"`
			} `json:"limits"`
		} `json:"data"`
	}
	wait, err := fetchJSON(ctx, liveRequest{url: clineUsageURL, header: map[string]string{"Authorization": "Bearer " + token}}, &r)
	if err != nil {
		return nil, wait, err
	}
	var ws []*pb.UsageWindow
	for _, l := range r.Data.Limits {
		if m := map[string]int32{"five_hour": fiveHours, "weekly": oneWeek, "monthly": oneMonth}[l.Type]; m > 0 {
			ws = append(ws, liveWindow(m, l.PercentUsed, l.ResetsAt.unix()))
		}
	}
	return liveUsageOf(ws...)
}

// ---- Grok ----

// grokBillingURL is the Grok CLI's credits (t3code provider-grok
// usageLimits.ts).
var grokBillingURL = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"

// grokLiveUsage: the Grok CLI subscription's credits this period (a week or
// a month), with its sign-in from ~/.grok/auth.json (GROK_HOME). Skipped
// when the CLI is set up to use an API key or another endpoint.
func grokLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	for _, k := range []string{"XAI_API_KEY", "GROK_AUTH", "GROK_CLI_CHAT_PROXY_BASE_URL"} {
		if os.Getenv(k) != "" {
			return nil, 0, fmt.Errorf("%s is set", k)
		}
	}
	dir := envOr("GROK_HOME", home(".grok"))
	var scopes map[string]struct {
		Key       string   `json:"key"`
		ExpiresAt flexTime `json:"expires_at"`
		AuthMode  string   `json:"auth_mode"`
	}
	if err := readSignIn(filepath.Join(dir, "auth.json"), &scopes); err != nil {
		return nil, 0, err
	}
	token := ""
	for _, prefix := range []string{"https://auth.x.ai::", "https://accounts.x.ai/sign-in"} {
		for scope, e := range scopes {
			if token == "" && strings.HasPrefix(scope, prefix) && e.Key != "" && e.AuthMode != "api_key" && !expired(e.ExpiresAt.time()) {
				token = e.Key
			}
		}
	}
	if token == "" {
		return nil, 0, errNoSignIn
	}
	var r struct {
		Config struct {
			CreditUsagePercent *float64 `json:"creditUsagePercent"`
			CurrentPeriod      struct {
				Type  string   `json:"type"`
				Start flexTime `json:"start"`
				End   flexTime `json:"end"`
			} `json:"currentPeriod"`
			BillingPeriodEnd flexTime `json:"billingPeriodEnd"`
			OnDemandCap      struct {
				Val flexNum `json:"val"`
			} `json:"onDemandCap"`
			OnDemandUsed struct {
				Val flexNum `json:"val"`
			} `json:"onDemandUsed"`
		} `json:"config"`
	}
	wait, err := fetchJSON(ctx, liveRequest{url: grokBillingURL, header: map[string]string{
		"Authorization":    "Bearer " + token,
		"x-xai-token-auth": "xai-grok-cli",
	}}, &r)
	if err != nil {
		return nil, wait, err
	}
	c := r.Config
	minutes := map[string]int32{"USAGE_PERIOD_TYPE_WEEKLY": oneWeek, "USAGE_PERIOD_TYPE_MONTHLY": oneMonth}[c.CurrentPeriod.Type]
	if s, e := c.CurrentPeriod.Start.time(), c.CurrentPeriod.End.time(); !s.IsZero() && e.After(s) {
		minutes = int32(e.Sub(s) / time.Minute)
	}
	if minutes == 0 {
		return liveUsageOf()
	}
	used := percentOf(float64(c.OnDemandUsed.Val), float64(c.OnDemandCap.Val))
	if c.CreditUsagePercent != nil {
		used = *c.CreditUsagePercent
	}
	reset := c.CurrentPeriod.End.unix()
	if reset == 0 {
		reset = c.BillingPeriodEnd.unix()
	}
	return liveUsageOf(liveWindow(minutes, used, reset))
}

// ---- Kilo ----

// kiloAPI is Kilo's service (KILO_API_URL moves it: serviceURL).
func kiloAPI() string {
	return strings.TrimRight(serviceURL("KILO_API_URL", "https://api.kilo.ai"), "/")
}

// kiloLiveUsage: this month's use of a Kilo Pass, with the Kilo CLI's
// sign-in from $XDG_DATA_HOME/kilo/auth.json (Kilo's own: a token good for
// a year; kilo-gateway src/api/kilo-pass.ts).
func kiloLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	var f struct {
		Kilo struct {
			Type      string  `json:"type"`
			Access    string  `json:"access"`
			Key       string  `json:"key"`
			Expires   flexNum `json:"expires"` // Unix ms
			AccountID string  `json:"accountId"`
		} `json:"kilo"`
	}
	if err := readSignIn(filepath.Join(xdg("XDG_DATA_HOME", ".local", "share"), "kilo", "auth.json"), &f); err != nil {
		return nil, 0, err
	}
	token := f.Kilo.Access
	if f.Kilo.Type == "api" {
		token = f.Kilo.Key
	}
	if token == "" || (f.Kilo.Expires > 0 && expired(time.UnixMilli(int64(f.Kilo.Expires)))) {
		return nil, 0, errNoSignIn
	}
	header := map[string]string{"Authorization": "Bearer " + token}
	if f.Kilo.AccountID != "" {
		header["X-KILOCODE-ORGANIZATIONID"] = f.Kilo.AccountID
	}
	var r []struct {
		Result struct {
			Data struct {
				JSON struct {
					Subscription *struct {
						Status        string   `json:"status"`
						Base          float64  `json:"currentPeriodBaseCreditsUsd"`
						Bonus         float64  `json:"currentPeriodBonusCreditsUsd"`
						Used          float64  `json:"currentPeriodUsageUsd"`
						NextBillingAt flexTime `json:"nextBillingAt"`
						NextRenewalAt flexTime `json:"nextRenewalAt"`
					} `json:"subscription"`
				} `json:"json"`
			} `json:"data"`
		} `json:"result"`
	}
	u := kiloAPI() + "/api/trpc/kiloPass.getState?batch=1&input=" + url.QueryEscape(`{"0":null}`)
	wait, err := fetchJSON(ctx, liveRequest{url: u, header: header}, &r)
	if err != nil {
		return nil, wait, err
	}
	if len(r) == 0 || r[0].Result.Data.JSON.Subscription == nil {
		return liveUsageOf()
	}
	s := r[0].Result.Data.JSON.Subscription
	switch s.Status {
	case "active", "past_due", "trialing":
	default:
		return liveUsageOf()
	}
	reset := s.NextBillingAt.unix()
	if reset == 0 {
		reset = s.NextRenewalAt.unix()
	}
	return liveUsageOf(liveWindow(oneMonth, percentOf(s.Used, s.Base+s.Bonus), reset))
}

// ---- Kimi Code ----

// kimiLiveUsage: Kimi Code's 5-hour, weekly and monthly limits, with the
// kimi CLI's sign-in (~/.kimi-code/credentials/kimi-code.json, or the older
// Python CLI's ~/.kimi/credentials). Its token lasts minutes, refreshed by
// kimi as it runs, so this mostly answers while kimi does. Facts from
// kimi-code packages/oauth/src/managed-usage.ts.
func kimiLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	for _, k := range []string{"KIMI_CODE_BASE_URL", "KIMI_CODE_OAUTH_HOST", "KIMI_OAUTH_HOST"} {
		if os.Getenv(k) != "" {
			return nil, 0, fmt.Errorf("%s is set", k)
		}
	}
	codeHome := envOr("KIMI_CODE_HOME", home(".kimi-code"))
	var cred struct {
		AccessToken string  `json:"access_token"`
		ExpiresAt   flexNum `json:"expires_at"` // Unix seconds
	}
	err := readSignIn(filepath.Join(codeHome, "credentials", "kimi-code.json"), &cred)
	if err != nil {
		err = readSignIn(filepath.Join(envOr("KIMI_SHARE_DIR", home(".kimi")), "credentials", "kimi-code.json"), &cred)
	}
	if err != nil {
		return nil, 0, err
	}
	if cred.AccessToken == "" || (cred.ExpiresAt > 0 && expired(time.Unix(int64(cred.ExpiresAt), 0))) {
		return nil, 0, errNoSignIn
	}
	base := kimiUsageBase
	if region, _ := os.ReadFile(filepath.Join(codeHome, "region")); strings.TrimSpace(string(region)) == "global" {
		base = kimiUsageBaseGlobal
	}
	var r struct {
		Usages map[string]struct {
			UsedRatio *float64 `json:"used_ratio"`
			ResetTime flexTime `json:"reset_time"`
		} `json:"usages"`
	}
	wait, err := fetchJSON(ctx, liveRequest{url: base + "/usages", header: map[string]string{
		"Authorization":  "Bearer " + cred.AccessToken,
		"X-Msh-Platform": "kimi_code_cli",
	}}, &r)
	if err != nil {
		return nil, wait, err
	}
	var ws []*pb.UsageWindow
	for key, m := range map[string]int32{"limit_5h": fiveHours, "limit_7d": oneWeek, "limit_month_total": oneMonth} {
		if u, ok := r.Usages[key]; ok && u.UsedRatio != nil {
			ws = append(ws, liveWindow(m, *u.UsedRatio*100, u.ResetTime.unix()))
		}
	}
	return liveUsageOf(ws...)
}

// Kimi Code's API, in mainland China and elsewhere (~/.kimi-code/region).
var (
	kimiUsageBase       = "https://api.kimi.com/coding/v1"
	kimiUsageBaseGlobal = "https://api.kimi.ai/coding/v1"
)

// ---- OpenCode Go ----

// openCodeGoUsageURL is OpenCode Go's limits (OpenCode's own client asks
// it: packages/core/src/usage-limits.ts).
var openCodeGoUsageURL = "https://opencode.ai/zen/go/v1/usage"

// openCodeLiveUsage: an OpenCode Go subscription's 5-hour, weekly and
// monthly limits, with the opencode.ai sign-in OpenCode keeps: in its
// database's credential table (OpenCode 2), else auth.json.
func openCodeLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	token, org := openCodeGoSignIn(ctx)
	if token == "" {
		return nil, 0, errNoSignIn
	}
	header := map[string]string{"Authorization": "Bearer " + token}
	if org != "" {
		header["x-opencode-org-id"] = org
	}
	type window struct {
		Percent  *float64 `json:"percent"`
		ResetsAt flexTime `json:"resetsAt"`
	}
	var r struct {
		Usage map[string]window `json:"usage"`
	}
	wait, err := fetchJSON(ctx, liveRequest{url: openCodeGoUsageURL, header: header}, &r)
	if err != nil {
		return nil, wait, err
	}
	var ws []*pb.UsageWindow
	for key, m := range map[string]int32{"rolling": fiveHours, "weekly": oneWeek, "monthly": oneMonth} {
		if w, ok := r.Usage[key]; ok && w.Percent != nil {
			ws = append(ws, liveWindow(m, *w.Percent, w.ResetsAt.unix()))
		}
	}
	return liveUsageOf(ws...)
}

// openCodeGoSignIn is OpenCode's opencode.ai key or (unexpired) token, and
// its organisation, if any.
func openCodeGoSignIn(ctx context.Context) (token, org string) {
	data := filepath.Join(xdg("XDG_DATA_HOME", ".local", "share"), "opencode")
	type cred struct {
		Type     string  `json:"type"`
		Key      string  `json:"key"`
		Access   string  `json:"access"`
		Expires  flexNum `json:"expires"`
		Metadata struct {
			Server string `json:"server"`
			OrgID  string `json:"orgID"`
		} `json:"metadata"`
	}
	use := func(c cred) bool {
		if s := c.Metadata.Server; s != "" && !strings.Contains(s, "opencode.ai") {
			return false
		}
		t := c.Key
		if c.Type == "oauth" {
			if c.Expires > 0 && expired(time.UnixMilli(int64(c.Expires))) {
				return false
			}
			t = c.Access
		}
		token, org = t, c.Metadata.OrgID
		return token != ""
	}
	db := envOr("OPENCODE_DB", filepath.Join(data, "opencode.db"))
	if _, err := os.Stat(db); err == nil {
		if h, err := sql.Open("sqlite", "file:"+db+"?mode=ro&_pragma=busy_timeout(2000)"); err == nil {
			var v string
			err := h.QueryRowContext(ctx, `SELECT value FROM credential WHERE integration_id IN ('opencode-go','opencode') AND active IS NOT 0
				ORDER BY active DESC, time_created DESC, id DESC LIMIT 1`).Scan(&v)
			h.Close()
			var c cred
			if err == nil && json.Unmarshal([]byte(v), &c) == nil && use(c) {
				return token, org
			}
		}
	}
	var auth map[string]cred
	if readSignIn(filepath.Join(data, "auth.json"), &auth) == nil {
		for _, k := range []string{"opencode-go", "opencode"} {
			if c, ok := auth[k]; ok && c.Type != "oauth" && use(c) {
				return token, org
			}
		}
	}
	return "", ""
}
