package slack

import (
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// UserScopes are what Hammock's Slack app asks for, all to read as you:
// the four kinds of conversation's messages (channels, private channels,
// DMs, group DMs), reactions, people's names, and channels' names (to find
// a channel a trigger names, and name it in a run).
var UserScopes = []string{
	"channels:history", "groups:history", "im:history", "mpim:history",
	"reactions:read", "users:read", "channels:read", "groups:read",
}

// UserEvents are the events it's sent, as you.
var UserEvents = []string{"message.channels", "message.groups", "message.im", "message.mpim", "reaction_added"}

// Manifest is Hammock's Slack app: user scopes only (no bot), Socket
// Mode, and nothing it could post with.
func Manifest() map[string]any {
	return map[string]any{
		"display_information": map[string]any{
			"name":        "Hammock",
			"description": "Starts your Hammock automations when things happen in Slack. It only reads, as you, and never posts.",
		},
		"oauth_config": map[string]any{
			"scopes": map[string]any{"user": UserScopes},
		},
		"settings": map[string]any{
			"event_subscriptions":    map[string]any{"user_events": UserEvents},
			"interactivity":          map[string]any{"is_enabled": false},
			"org_deploy_enabled":     false,
			"socket_mode_enabled":    true,
			"token_rotation_enabled": false,
		},
	}
}

// ManifestURL opens Slack's Create an App with Hammock's manifest filled in.
func ManifestURL() string {
	b, _ := json.Marshal(Manifest())
	return "https://api.slack.com/apps?new_app=1&manifest_json=" + url.QueryEscape(string(b))
}

// MissingScopes are the UserScopes have lacks (have nil: not known, so none).
func MissingScopes(have []string) []string {
	if have == nil {
		return nil
	}
	var out []string
	for _, s := range UserScopes {
		if !slices.Contains(have, s) {
			out = append(out, s)
		}
	}
	return out
}

// CheckTokens says what's wrong with the two tokens as pasted, if anything.
func CheckTokens(user, app string) string {
	switch {
	case user == "" || app == "":
		return "both tokens are needed: the User OAuth Token (xoxp-…) and an app-level token (xapp-…)"
	case strings.HasPrefix(user, "xoxb-"):
		return "that's a bot token (xoxb-): use the User OAuth Token (xoxp-…) from OAuth & Permissions"
	case !strings.HasPrefix(user, "xoxp-"):
		return "the user token should start xoxp- (OAuth & Permissions ▸ User OAuth Token)"
	case !strings.HasPrefix(app, "xapp-"):
		return "the app-level token should start xapp- (Basic Information ▸ App-Level Tokens)"
	case strings.ContainsFunc(user+app, func(r rune) bool { return r <= ' ' || r > '~' }):
		return "a token has spaces or other characters in it: copy it again"
	}
	return ""
}

var (
	userRef    = regexp.MustCompile(`<@([UW][A-Z0-9]+)(?:\|([^>]*))?>`)
	channelRef = regexp.MustCompile(`<#([CGD][A-Z0-9]+)(?:\|([^>]*))?>`)
	specialRef = regexp.MustCompile(`<!([^>|]+)(?:\|([^>]*))?>`)
	linkRef    = regexp.MustCompile(`<((?:https?|mailto):[^>|]+)(?:\|([^>]*))?>`)
)

// MentionsUser: text mentions the user id by name (<@U…>), not @here,
// @channel, @everyone or a user group.
func MentionsUser(text, id string) bool {
	if id == "" {
		return false
	}
	for _, m := range userRef.FindAllStringSubmatch(text, -1) {
		if m[1] == id {
			return true
		}
	}
	return false
}

// PlainText is a message's text as a person reads it: mentions as @name
// (name looks a person up by id: "" if it can't), channels as #name,
// links as their words and address, &lt; &gt; &amp; as < > &.
func PlainText(text string, name func(id string) string) string {
	text = userRef.ReplaceAllStringFunc(text, func(s string) string {
		m := userRef.FindStringSubmatch(s)
		n := m[2]
		if n == "" && name != nil {
			n = name(m[1])
		}
		return "@" + cmpOr(n, m[1])
	})
	text = channelRef.ReplaceAllStringFunc(text, func(s string) string {
		m := channelRef.FindStringSubmatch(s)
		return "#" + cmpOr(m[2], m[1])
	})
	text = specialRef.ReplaceAllStringFunc(text, func(s string) string {
		m := specialRef.FindStringSubmatch(s)
		if m[2] != "" {
			return m[2]
		}
		if k, _, _ := strings.Cut(m[1], "^"); k == "here" || k == "channel" || k == "everyone" {
			return "@" + k
		}
		return "@" + m[1]
	})
	text = linkRef.ReplaceAllStringFunc(text, func(s string) string {
		m := linkRef.FindStringSubmatch(s)
		if m[2] == "" || m[2] == m[1] {
			return m[1]
		}
		return m[2] + " (" + m[1] + ")"
	})
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(text)
}
