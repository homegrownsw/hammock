package agents

// Live usage readers (live.go) for Codex, Copilot, Gemini CLI and
// Antigravity CLI.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// ---- Codex ----

// codexUsageURL is what Codex itself asks for its limits
// (codex-rs backend-client, client.rs).
var codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

// codexLiveUsage: Codex's 5-hour and weekly limits from ChatGPT, with the
// ChatGPT sign-in Codex keeps in $CODEX_HOME/auth.json. Its token is used
// while fresh (Codex refreshes it as it runs; refreshing it here would
// rotate Codex's own).
func codexLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	dir := envOr("CODEX_HOME", home(".codex"))
	var auth struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := readSignIn(filepath.Join(dir, "auth.json"), &auth); err != nil {
		return nil, 0, err
	}
	tok := auth.Tokens.AccessToken
	if tok == "" || expired(jwtExpiry(tok)) {
		return nil, 0, errNoSignIn
	}
	header := map[string]string{"Authorization": "Bearer " + tok, "User-Agent": "codex-cli"}
	if auth.Tokens.AccountID != "" {
		header["ChatGPT-Account-Id"] = auth.Tokens.AccountID
	}
	type window struct {
		UsedPercent   *float64 `json:"used_percent"`
		WindowSeconds float64  `json:"limit_window_seconds"`
		ResetAt       flexTime `json:"reset_at"`
	}
	var r struct {
		RateLimit struct {
			Primary   *window `json:"primary_window"`
			Secondary *window `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	wait, err := fetchJSON(ctx, liveRequest{url: codexUsageURL, header: header}, &r)
	if err != nil {
		return nil, wait, err
	}
	var ws []*pb.UsageWindow
	for _, w := range []*window{r.RateLimit.Primary, r.RateLimit.Secondary} {
		if w != nil && w.UsedPercent != nil && w.WindowSeconds > 0 {
			ws = append(ws, liveWindow(int32(w.WindowSeconds/60), *w.UsedPercent, w.ResetAt.unix()))
		}
	}
	return liveUsageOf(ws...)
}

// ---- Copilot ----

// copilotUserURL is what GitHub's Copilot clients ask for the plan's quotas
// (CodexBar CopilotUsageFetcher.swift).
var copilotUserURL = "https://api.github.com/copilot_internal/user"

// copilotLiveUsage: this month's Copilot premium requests (and chat and
// completions on the free plan), with the Copilot CLI's GitHub sign-in:
// COPILOT_GITHUB_TOKEN, else its Keychain item (copilot-cli).
func copilotLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	token := os.Getenv("COPILOT_GITHUB_TOKEN")
	if token == "" {
		b, err := keychainSecret(ctx, "copilot-cli", "")
		if err != nil {
			return nil, 0, err
		}
		token = copilotTokenOf(b)
	}
	if token == "" {
		return nil, 0, errNoSignIn
	}
	type snapshot struct {
		Entitlement      float64  `json:"entitlement"`
		Remaining        float64  `json:"remaining"`
		PercentRemaining *float64 `json:"percent_remaining"`
		Unlimited        bool     `json:"unlimited"`
		ResetAt          flexTime `json:"quota_reset_at"`
	}
	var r struct {
		QuotaResetDate string              `json:"quota_reset_date"`
		Snapshots      map[string]snapshot `json:"quota_snapshots"`
		Monthly        map[string]float64  `json:"monthly_quotas"`
		Left           map[string]float64  `json:"limited_user_quotas"`
		LimitedReset   string              `json:"limited_user_reset_date"`
	}
	wait, err := fetchJSON(ctx, liveRequest{url: copilotUserURL, header: map[string]string{
		"Authorization":         "token " + token,
		"Editor-Version":        "vscode/1.96.2",
		"Editor-Plugin-Version": "copilot-chat/0.26.7",
		"X-Github-Api-Version":  "2025-04-01",
	}}, &r)
	if err != nil {
		return nil, wait, err
	}
	day := func(s string) int64 {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t.Unix()
		}
		return unixOf(s)
	}
	var ws []*pb.UsageWindow
	if s, ok := r.Snapshots["premium_interactions"]; ok && !s.Unlimited && s.Entitlement > 0 {
		used := percentOf(s.Entitlement-s.Remaining, s.Entitlement)
		if s.PercentRemaining != nil {
			used = 100 - *s.PercentRemaining
		}
		reset := s.ResetAt.unix()
		if reset == 0 {
			reset = day(r.QuotaResetDate)
		}
		ws = append(ws, liveWindow(oneMonth, used, reset))
	}
	if len(ws) == 0 { // the free plan: chat and completions a month
		for _, k := range []string{"chat", "completions"} {
			if limit, left := r.Monthly[k], r.Left[k]; limit > 0 {
				ws = append(ws, liveWindow(oneMonth, percentOf(limit-left, limit), day(r.LimitedReset)))
			}
		}
		if len(ws) > 1 && ws[1].UsedPercent > ws[0].UsedPercent {
			ws = ws[1:] // the closer of the two to its limit
		}
		ws = ws[:min(len(ws), 1)]
	}
	return liveUsageOf(ws...)
}

// copilotTokenOf is the GitHub token in the Copilot CLI's Keychain item: the
// token itself, or JSON carrying it.
func copilotTokenOf(b []byte) string {
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "{") {
		return s
	}
	var v map[string]any
	if json.Unmarshal([]byte(s), &v) != nil {
		return ""
	}
	for _, k := range []string{"token", "access_token", "accessToken", "oauth_token"} {
		if t, ok := v[k].(string); ok {
			return t
		}
	}
	return ""
}

// ---- Gemini CLI ----

// geminiCodeAssist is Gemini CLI's Code Assist service (its code_assist
// server.ts and setup.ts).
var geminiCodeAssist = "https://cloudcode-pa.googleapis.com/v1internal"

// geminiLiveUsage: Gemini's daily request quota, for the model closest to
// its limit, with Gemini CLI's Google sign-in from ~/.gemini/
// oauth_creds.json (GEMINI_CLI_HOME), used while fresh: refreshing it would
// take Gemini CLI's own client secret. Google serves this to Workspace and
// paid Code Assist accounts only, since June 2026.
func geminiLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	var cred struct {
		AccessToken string  `json:"access_token"`
		ExpiryDate  flexNum `json:"expiry_date"` // Unix ms
	}
	if err := readSignIn(filepath.Join(envOr("GEMINI_CLI_HOME", home()), ".gemini", "oauth_creds.json"), &cred); err != nil {
		return nil, 0, err
	}
	if cred.AccessToken == "" || (cred.ExpiryDate > 0 && expired(time.UnixMilli(int64(cred.ExpiryDate)))) {
		return nil, 0, errNoSignIn
	}
	header := map[string]string{"Authorization": "Bearer " + cred.AccessToken}
	load := map[string]any{"metadata": map[string]string{"ideType": "IDE_UNSPECIFIED", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}}
	if p := os.Getenv("GOOGLE_CLOUD_PROJECT"); p != "" {
		load["cloudaicompanionProject"] = p
	}
	var setup struct {
		Project string `json:"cloudaicompanionProject"`
	}
	if wait, err := fetchJSON(ctx, liveRequest{method: "POST", url: geminiCodeAssist + ":loadCodeAssist", header: header, body: load}, &setup); err != nil {
		return nil, wait, err
	}
	if setup.Project == "" {
		return nil, 0, errors.New("gemini: no Code Assist project (this account may not be served)")
	}
	var r struct {
		Buckets []struct {
			ModelID           string   `json:"modelId"`
			RemainingFraction *float64 `json:"remainingFraction"`
			ResetTime         flexTime `json:"resetTime"`
		} `json:"buckets"`
	}
	wait, err := fetchJSON(ctx, liveRequest{method: "POST", url: geminiCodeAssist + ":retrieveUserQuota", header: header,
		body: map[string]string{"project": setup.Project}}, &r)
	if err != nil {
		return nil, wait, err
	}
	var worst *pb.UsageWindow
	for _, b := range r.Buckets {
		if b.RemainingFraction == nil {
			continue
		}
		if w := liveWindow(oneDay, (1-*b.RemainingFraction)*100, b.ResetTime.unix()); worst == nil || w.UsedPercent > worst.UsedPercent {
			worst = w
		}
	}
	return liveUsageOf(worst)
}

// ---- Antigravity CLI ----

// antigravityLiveUsage: Antigravity's 5-hour and weekly limits, from agy's
// own `/usage` in print mode, which answers without starting a turn or
// spending quota (agy's changelog). agy reads its own sign-in; Hammock keeps
// it from opening a browser when that has lapsed.
func antigravityLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	agy := antigravityBinary()
	if agy == "" {
		return nil, 0, errNoSignIn
	}
	out, err := antigravityRun(ctx, agy)
	if err != nil {
		return nil, 0, err
	}
	return parseAntigravityUsage(out, time.Now())
}

// antigravityBinary is agy's path: on PATH, else where its installer puts it.
func antigravityBinary() string {
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	if p := home(".local", "bin", "agy"); isExecutable(p) {
		return p
	}
	return ""
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0
}

// antigravityRun runs agy's /usage (a test's own).
var antigravityRun = func(ctx context.Context, agy string) ([]byte, error) {
	stub, err := noBrowserDir()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, agy, "-p", "/usage", "--output-format", "json", "--print-timeout", "25s")
	cmd.Env = append(os.Environ(), "AGY_CLI_DISABLE_AUTO_UPDATE=true", "BROWSER="+filepath.Join(stub, "open"),
		"PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"), "NO_COLOR=1")
	cmd.Stdin = nil
	return cmd.Output()
}

// noBrowserDir is a folder whose open and xdg-open do nothing, put first
// on a CLI's PATH so a lapsed sign-in can't open a browser unasked. Made
// again whenever it's gone (macOS clears old files from $TMPDIR).
func noBrowserDir() (string, error) {
	noBrowser.Lock()
	defer noBrowser.Unlock()
	if noBrowser.dir != "" {
		if _, err := os.Stat(filepath.Join(noBrowser.dir, "xdg-open")); err == nil {
			return noBrowser.dir, nil
		}
		os.RemoveAll(noBrowser.dir)
		noBrowser.dir = ""
	}
	dir, err := os.MkdirTemp("", "hmk-no-browser-")
	if err != nil {
		return "", err
	}
	for _, name := range []string{"open", "xdg-open"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	noBrowser.dir = dir
	return dir, nil
}

var noBrowser struct {
	sync.Mutex
	dir string
}

// parseAntigravityUsage reads agy's /usage JSON: each group's 5-hour and
// weekly buckets, keeping the one closest to its limit for each.
func parseAntigravityUsage(out []byte, now time.Time) (*pb.AgentUsage, time.Duration, error) {
	var r struct {
		Status  string `json:"status"`
		Command struct {
			Data struct {
				Groups []struct {
					Buckets []struct {
						Window            string   `json:"window"`
						RemainingFraction *float64 `json:"remaining_fraction"`
						ResetTime         flexTime `json:"reset_time"`
						Disabled          bool     `json:"disabled"`
					} `json:"buckets"`
				} `json:"groups"`
			} `json:"data"`
		} `json:"command"`
	}
	if i := strings.IndexByte(string(out), '{'); i >= 0 {
		out = out[i:]
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, 0, errors.New("agy /usage: " + err.Error())
	}
	if r.Status != "" && r.Status != "SUCCESS" {
		return nil, 0, errors.New("agy /usage: " + r.Status)
	}
	worst := map[int32]*pb.UsageWindow{}
	for _, g := range r.Command.Data.Groups {
		for _, b := range g.Buckets {
			m := map[string]int32{"5h": fiveHours, "weekly": oneWeek}[b.Window]
			if m == 0 || b.Disabled || b.RemainingFraction == nil {
				continue
			}
			reset := b.ResetTime.unix()
			// An untouched bucket says it resets a whole window from now:
			// not a real reset.
			if reset >= now.Add(time.Duration(m)*time.Minute-time.Minute).Unix() && *b.RemainingFraction >= 1 {
				reset = 0
			}
			if w := liveWindow(m, (1-*b.RemainingFraction)*100, reset); worst[m] == nil || w.UsedPercent > worst[m].UsedPercent {
				worst[m] = w
			}
		}
	}
	return liveUsageOf(worst[fiveHours], worst[oneWeek])
}
