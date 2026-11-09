package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// claudeKeychainItem is where Claude Code keeps its sign-in on a Mac (with
// CLAUDE_CONFIG_DIR set it's named differently, and isn't looked for).
const claudeKeychainItem = "Claude Code-credentials"

// claudeCredentials reads Claude Code's sign-in: from the Keychain on a
// Mac, and elsewhere from ~/.claude/.credentials.json, where it keeps it
// without one.
func claudeCredentials(ctx context.Context) ([]byte, error) {
	if runtime.GOOS == "darwin" {
		return keychainSecret(ctx, claudeKeychainItem, "")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoSignIn, err)
	}
	return data, nil
}

// claudeUsageURL is the endpoint Claude Code's /usage asks.
var claudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

// claudeLiveUsage asks Anthropic for Claude's usage, as Claude Code's /usage
// does, with Claude Code's sign-in (claudeCredentials).
func claudeLiveUsage(ctx context.Context) (*pb.AgentUsage, time.Duration, error) {
	if os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		return nil, 0, fmt.Errorf("CLAUDE_CONFIG_DIR is set")
	}
	secret, err := claudeCredentials(ctx)
	if err != nil {
		return nil, 0, err
	}
	var cred struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"` // Unix ms
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal(secret, &cred) != nil || cred.OAuth.AccessToken == "" {
		return nil, 0, errNoSignIn
	}
	if cred.OAuth.ExpiresAt > 0 && expired(time.UnixMilli(cred.OAuth.ExpiresAt)) {
		return nil, 0, errNoSignIn
	}
	var body json.RawMessage
	wait, err := fetchJSON(ctx, liveRequest{url: claudeUsageURL, header: map[string]string{
		"Authorization":  "Bearer " + cred.OAuth.AccessToken,
		"anthropic-beta": "oauth-2025-04-20",
	}}, &body)
	if err != nil {
		return nil, wait, err
	}
	a := ParseClaudeWindows(body, time.Now().UnixMilli())
	if a == nil {
		return nil, 0, fmt.Errorf("usage: no windows in the answer")
	}
	return a, 0, nil
}
