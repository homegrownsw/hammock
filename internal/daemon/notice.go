package daemon

import (
	"path/filepath"
	"strings"
	"unicode"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// What notifications say and count, the same on every app: a phone's
// pushes (here), the Mac's own notifications and Dock badge (HammockKit's
// AttentionItem.noticeBody, AppModel.badgeCount) and Android's badge
// (World.badgeCount). testdata/client/vectors.json pins them, for each
// language's tests to check.

// noticeBody is what a notification about an agent (or a program
// reporting its status) says, naming it as who ("Claude"): "Claude asks:
// …", "Claude needs your input", "Claude finished", "Claude hit an error".
func noticeBody(item *pb.AttentionItem, who string) string {
	switch item.State {
	case pb.AgentState_AGENT_STATE_NEEDS_INPUT:
		if item.Question {
			return who + " asks: " + item.Summary
		}
		if item.Summary != "" {
			return item.Summary
		}
		return who + " needs your input"
	case pb.AgentState_AGENT_STATE_IDLE:
		if item.Program && item.Summary != "" {
			return who + ": " + item.Summary // what it says it did
		}
		return who + " finished"
	case pb.AgentState_AGENT_STATE_ERRORED:
		if item.Summary == "" {
			return who + " hit an error"
		}
		return who + " " + item.Summary
	}
	return item.Summary
}

// paneWho names what's in a pane for a notification, as the apps do
// (Pane.displayName, capitalised): "Claude", a program's own name. "Agent"
// for no pane.
func paneWho(p *pb.Pane) string {
	if p == nil {
		return "Agent"
	}
	return capitalizeWords(paneDisplayName(p))
}

// paneDisplayName is HammockKit's Pane.displayName: the agent harness if
// known, else a program's name for itself, else the title or command.
func paneDisplayName(p *pb.Pane) string {
	if p.Kind == pb.PaneKind_PANE_KIND_EDITOR {
		if n := p.GetEditor().GetName(); n != "" {
			return n
		}
		return "Untitled"
	}
	a := p.Agent
	if a != nil && a.Harness != "" {
		return a.Harness
	}
	if a != nil && a.Reported {
		for _, n := range append([]string{a.Title, a.App}, p.Program...) {
			if n != "" {
				return n
			}
		}
	}
	if p.Title != "" {
		return p.Title
	}
	if len(p.Command) > 0 {
		return filepath.Base(p.Command[0])
	}
	return "shell"
}

// capitalizeWords is Foundation's String.capitalized: each letter after
// something other than a letter (or an apostrophe) in upper case, the
// rest in lower ("claude-code" → "Claude-Code").
func capitalizeWords(s string) string {
	var b strings.Builder
	prev := rune(0)
	for _, r := range s {
		if unicode.IsLetter(prev) || prev == '\'' || prev == '’' {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(unicode.ToUpper(r))
		}
		prev = r
	}
	return b.String()
}

// countsForBadge: an attention item that adds to the badge (an agent
// waiting for your input; not one that finished or failed).
func countsForBadge(a *pb.AttentionItem) bool {
	return a.State == pb.AgentState_AGENT_STATE_NEEDS_INPUT
}

// badgeCount is the number on Hammock's icon: agents waiting for your
// input, permission requests, join requests and Hammock Key requests,
// your linked computers' (and the Groups shared with you) included, as
// the apps count what this daemon sends them. Callers hold d.mu.
func (d *Daemon) badgeCount() int {
	n := len(d.grants) + len(d.joins)
	if d.hammockKey != nil {
		n += len(d.hammockKey.reqs)
	}
	for _, a := range d.attention {
		if countsForBadge(a) {
			n++
		}
	}
	for _, l := range d.links {
		n += len(l.grants)
		for _, a := range l.attn {
			if countsForBadge(a) {
				n++
			}
		}
	}
	return n
}

// touchesBadge: u can change badgeCount.
func touchesBadge(u *pb.StateUpdate) bool {
	return u.Full || len(u.Attention)+len(u.ClearedAttentionIds)+len(u.Grants)+len(u.ResolvedGrantIds)+
		len(u.JoinRequests)+len(u.ResolvedJoinIds)+len(u.HammockKeyRequests)+len(u.ResolvedHammockKeyIds) > 0
}
