package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// Smart names for unnamed tabs, with no setup: an agent tab is named after
// the agent's own title for its session (Claude Code puts it in the terminal
// title, "✳ Pricing page rewrite"), and a shell after its git branch
// ("me/eng-89-notify-retries" → "Notify Retries [ENG-89]"). The whole tab
// counts, in this order: an agent's session (the focused pane's, else the
// first in the layout), then a branch (likewise), so a simulator or browser
// the agent opened beside it doesn't rename the tab. A tab with neither (a
// lone browser or simulator) shows its page or device instead. A name sticks
// once set, until the work changes (a new agent session, another branch);
// naming a tab by hand (you, or an agent with `hmk tab name`) always wins.

// refreshTabNames updates auto names. Callers hold d.mu.
func (d *Daemon) refreshTabNames() {
	for _, t := range d.tabs {
		if t.Name != "" {
			continue
		}
		var panes []*pb.Pane
		if p := d.panes[t.FocusedPaneId]; p != nil {
			panes = append(panes, p.snapshotInfo())
		}
		for _, id := range tabPanes(t) {
			if p := d.panes[id]; p != nil && id != t.FocusedPaneId {
				panes = append(panes, p.snapshotInfo())
			}
		}
		name, key := tabAutoName(panes)
		// Sticky: a name stays while its source (session, branch) does.
		if name == "" || key == t.AutoKey && t.AutoName != "" {
			continue
		}
		t.AutoName, t.AutoKey = name, key
		d.persist(d.store.PutTab(t))
		d.publish(&pb.StateUpdate{Tabs: []*pb.Tab{proto.CloneOf(t)}})
	}
}

// tabAutoName is a name for what a tab's panes (the focused one first) are
// doing, and what it's keyed to (it's only replaced when that changes): the
// first agent session title, else the first branch.
func tabAutoName(panes []*pb.Pane) (name, key string) {
	for _, p := range panes {
		if name, key = agentAutoName(p); name != "" {
			return name, key
		}
	}
	for _, p := range panes {
		if name, key = branchAutoName(p); name != "" {
			return name, key
		}
	}
	return "", ""
}

func terminalPane(p *pb.Pane) bool {
	return p.Kind != pb.PaneKind_PANE_KIND_BROWSER && p.Kind != pb.PaneKind_PANE_KIND_SIMULATOR && p.Kind != pb.PaneKind_PANE_KIND_EDITOR
}

// agentAutoName is a running agent's session title.
func agentAutoName(p *pb.Pane) (name, key string) {
	if a := p.Agent; terminalPane(p) && a != nil && a.State != pb.AgentState_AGENT_STATE_EXITED {
		if n := agentTitle(p.Title); n != "" {
			return n, "agent:" + a.Harness + ":" + a.SessionId
		}
	}
	return "", ""
}

// branchAutoName is a shell's git branch (a pane running an agent is named
// by the agent or not at all).
func branchAutoName(p *pb.Pane) (name, key string) {
	if !terminalPane(p) || p.Agent != nil && p.Agent.State != pb.AgentState_AGENT_STATE_EXITED {
		return "", ""
	}
	if b := gitBranch(p.Cwd); b != "" {
		if n := branchName(b); n != "" {
			return n, "branch:" + b
		}
	}
	return "", ""
}

// agentTitle reads an agent's session title from its terminal title: Claude
// Code sets "✳ Title" (a spinner in place of ✳ while working). Titles that
// are just the program or a path aren't names.
func agentTitle(title string) string {
	t := strings.TrimSpace(title)
	r := []rune(t)
	if len(r) < 3 || unicode.IsLetter(r[0]) || unicode.IsDigit(r[0]) || r[1] != ' ' {
		return ""
	}
	t = strings.TrimSpace(string(r[2:]))
	low := strings.ToLower(t)
	if low == "" || agents.IsName(low) || strings.HasPrefix(t, "/") || strings.HasPrefix(t, "~") {
		return ""
	}
	return t
}

// spinnerFrame reports whether a title change is only Claude Code turning
// the spinner in front of its title while it works ("⠂ Title" → "⠐ Title").
// Settling back on ✳ (done) is a real change.
func spinnerFrame(old, title string) bool {
	o, n := []rune(old), []rune(title)
	if len(o) < 3 || len(n) < 3 || o[0] == '✳' || n[0] == '✳' {
		return false
	}
	return agentTitle(old) != "" && string(o[1:]) == string(n[1:]) && agentTitle(title) != ""
}

// gitBranch is the branch checked out in dir's repository (or worktree).
func gitBranch(dir string) string {
	for d := dir; d != "" && d != "/" && d != "."; d = filepath.Dir(d) {
		g := filepath.Join(d, ".git")
		st, err := os.Stat(g)
		if err != nil {
			continue
		}
		head := filepath.Join(g, "HEAD")
		if !st.IsDir() {
			// A worktree: ".git" names the real git dir.
			b, err := os.ReadFile(g)
			if err != nil {
				return ""
			}
			gd, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
			if !ok {
				return ""
			}
			if !filepath.IsAbs(gd) {
				gd = filepath.Join(d, gd)
			}
			head = filepath.Join(gd, "HEAD")
		}
		b, err := os.ReadFile(head)
		if err != nil {
			return ""
		}
		ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: refs/heads/")
		if !ok {
			return "" // detached
		}
		return ref
	}
	return ""
}

var (
	issueKey = regexp.MustCompile(`(?i)^([a-z][a-z0-9]{1,9})-(\d+)$`)
	issueNum = regexp.MustCompile(`^#?(\d+)$`)
)

// branchName makes a tab name from a branch: the last path part's words in
// Title Case (at most three), with an issue key in brackets ("eng-89-…" →
// "[ENG-89]", "1180-…" → "[#1180]"). Default branches aren't names.
func branchName(branch string) string {
	switch branch {
	case "main", "master", "develop", "dev", "trunk":
		return ""
	}
	last := branch
	if i := strings.LastIndex(branch, "/"); i >= 0 {
		last = branch[i+1:]
	}
	words := strings.FieldsFunc(last, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	issue := ""
	if len(words) >= 2 && issueKey.MatchString(words[0]+"-"+words[1]) && !issueNum.MatchString(words[0]) {
		issue = "[" + strings.ToUpper(words[0]+"-"+words[1]) + "]"
		words = words[2:]
	} else if len(words) >= 1 && issueNum.MatchString(words[0]) {
		issue = "[#" + strings.TrimPrefix(words[0], "#") + "]"
		words = words[1:]
	}
	if len(words) > 3 {
		words = words[:3]
	}
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	name := strings.Join(words, " ")
	if issue != "" {
		name = strings.TrimSpace(name + " " + issue)
	}
	return name
}
