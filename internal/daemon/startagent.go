package daemon

// Starting an agent from a menu: a new tab in a folder running Claude
// Code or Codex, and the folders to choose from (suggested, or browsed) so
// a phone can pick one on this Mac.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/alexjsp/hammock/internal/agents"
	"github.com/alexjsp/hammock/internal/holder"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// expandHome turns a leading ~ into the home folder.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// startAgent opens a tab in m.Cwd and starts the agent there.
func (d *Daemon) startAgent(m *pb.StartAgent) (*pb.ServerMessage, error) {
	ag := agents.Get(m.Harness)
	if ag == nil {
		return nil, fmt.Errorf("Hammock doesn't know an agent %q", m.Harness)
	}
	cwd := filepath.Clean(expandHome(strings.TrimSpace(m.Cwd)))
	if !filepath.IsAbs(cwd) {
		return nil, errors.New("the folder must be an absolute path")
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("there's no folder %s", cwd)
	}
	d.mu.Lock()
	gid := m.GroupId
	if gid == "" {
		gid = d.groupUsingDir(cwd)
	}
	made := gid == ""
	if err := d.checkTabID(m.TabId); err != nil {
		d.mu.Unlock()
		return nil, err // before a Group is made for it: nothing to show
	}
	if made {
		// No Group uses that folder: a new one for it (named after it),
		// shown with its tab (createTab publishes both), or not at all.
		gid = d.createGroupQuiet("").Id
	}
	t, err := d.createTab(gid, "", &pb.PaneSpec{Cwd: cwd}, m.TabId)
	if err != nil {
		if made {
			d.dropQuietGroup(gid) // the pane couldn't start: never shown
		}
		d.mu.Unlock()
		return nil, err
	}
	p := d.panes[t.FocusedPaneId]
	d.mu.Unlock()
	if p == nil {
		return nil, errors.New("the new tab has no pane")
	}
	cmd, later := startCommand(ag, m.Prompt, nil)
	p.mu.Lock()
	// Known before its hooks say so.
	p.info.Agent = &pb.Agent{Harness: m.Harness, State: pb.AgentState_AGENT_STATE_STARTING, StateSinceUnixMs: time.Now().UnixMilli(), Heuristic: true}
	ok := p.postLocked(func(hc *holder.Client) { _ = hc.Input([]byte(cmd + "\r")) })
	p.mu.Unlock()
	if !ok {
		return nil, errors.New("the new tab's shell isn't running")
	}
	d.typeFirstPrompt(p, later)
	d.mu.Lock()
	d.paneChangedIfLive(p)
	d.mu.Unlock()
	return created(t.Id), nil
}

// groupFolder is where a Group works: the folder it keeps (its last focused
// terminal's as it changes, or one set by hand), else, for a Group from
// before folders were kept, its last focused terminal's or its first
// terminal's. Callers hold d.mu.
func (d *Daemon) groupFolder(g *pb.Group) string {
	if g.Folder != "" {
		return g.Folder
	}
	cwdOf := func(id string) string {
		if p := d.panes[id]; p != nil && p.isTerminal() {
			return p.cwd()
		}
		return ""
	}
	if c := cwdOf(g.LastFocusedPaneId); c != "" {
		return c
	}
	for _, tid := range g.TabIds {
		if t := d.tabs[tid]; t != nil {
			for _, id := range tabPanes(t) {
				if c := cwdOf(id); c != "" {
					return c
				}
			}
		}
	}
	return ""
}

// listFolders answers ListFolders.
func (d *Daemon) listFolders(m *pb.ListFolders) *pb.FolderList {
	home, _ := os.UserHomeDir()
	out := &pb.FolderList{Home: home, Agents: installedAgents()}

	// Suggestions: this Group's folder, the others', recent sessions'.
	seen := map[string]bool{}
	add := func(path, why string) {
		if path == "" || seen[path] || len(out.Suggestions) >= 30 {
			return
		}
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			return
		}
		seen[path] = true
		out.Suggestions = append(out.Suggestions, &pb.FolderSuggestion{Path: path, Why: why})
	}
	d.mu.Lock()
	if g := d.groups[m.GroupId]; g != nil {
		add(d.groupFolder(g), "This Group")
	}
	gs := make([]*pb.Group, 0, len(d.groups))
	for _, g := range d.groups {
		gs = append(gs, g)
	}
	slices.SortFunc(gs, func(a, b *pb.Group) int { return int(a.Order - b.Order) })
	type named struct{ path, why string }
	var groups []named
	for _, g := range gs {
		if f := d.groupFolder(g); f != "" {
			why := g.Name
			if why == "" {
				why = "A Group"
			}
			groups = append(groups, named{f, why})
		}
	}
	d.mu.Unlock()
	for _, g := range groups {
		add(g.path, g.why)
	}
	if x, _, err := d.sessions.ready(); err == nil && x != nil {
		if ss, err := x.Search("", 200); err == nil {
			for _, s := range ss {
				add(s.Cwd, "Recent session")
			}
		}
	}

	if m.Path != "" {
		dir := filepath.Clean(expandHome(m.Path))
		if !filepath.IsAbs(dir) {
			out.Error = "the folder must be an absolute path"
			return out
		}
		out.Path = dir
		if dir != "/" {
			out.Parent = filepath.Dir(dir)
		}
		ents, err := os.ReadDir(dir)
		if err != nil {
			out.Error = fmt.Sprintf("can't read %s", dir)
			return out
		}
		for _, e := range ents {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			isDir := e.IsDir()
			if e.Type()&os.ModeSymlink != 0 {
				if st, err := os.Stat(filepath.Join(dir, name)); err == nil {
					isDir = st.IsDir()
				}
			}
			if isDir {
				out.Folders = append(out.Folders, name)
			}
			if len(out.Folders) >= 1000 {
				break
			}
		}
		slices.SortFunc(out.Folders, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	}
	return out
}

// installedAgents: the ids of the agents the login shell finds (asked
// at most every few minutes), sorted.
var agentsCache struct {
	sync.Mutex
	at   time.Time
	list []string
}

func installedAgents() []string {
	agentsCache.Lock()
	defer agentsCache.Unlock()
	if time.Since(agentsCache.at) < 3*time.Minute {
		return agentsCache.list
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var script strings.Builder
	for _, a := range agents.All {
		script.WriteString("command -v " + a.Command + "; ")
	}
	out, err := exec.CommandContext(ctx, loginShell(), "-l", "-c", script.String()).Output()
	found := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if a, ok := strings.CutPrefix(l, "alias "); ok {
			l, _, _ = strings.Cut(a, "=") // zsh says an alias as it is
		}
		found[filepath.Base(l)] = true
	}
	var list []string
	for _, a := range agents.All {
		if found[a.Command] {
			list = append(list, a.ID)
		}
	}
	// The login shell skips ~/.zshrc (it isn't interactive), where
	// installers often put their PATH (Claude's own, nvm) or an alias (the
	// old ~/.claude/local install): look where they install too.
	for _, a := range []string{"claude", "codex"} {
		if !slices.Contains(list, a) && installedAt(a) {
			list = append(list, a)
		}
	}
	slices.Sort(list)
	if err != nil && len(list) == 0 && ctx.Err() != nil {
		// Timed out: say Claude Code and Codex, as before, and let the shell say otherwise.
		list = []string{"claude", "codex"}
	}
	agentsCache.at, agentsCache.list = time.Now(), list
	return list
}

// agentInstallDirs are where claude and codex are usually installed, as
// globs ("~" is home).
var agentInstallDirs = []string{
	"~/.local/bin", "~/.claude/local", "~/.claude/bin",
	"/opt/homebrew/bin", "/usr/local/bin",
	"~/.npm-global/bin", "~/.npm/bin", "~/.bun/bin", "~/.volta/bin", "~/Library/pnpm",
	"~/.nvm/versions/node/*/bin", "~/.fnm/node-versions/*/installation/bin",
	"~/.local/share/mise/shims", "~/.asdf/shims",
}

// installedAt: an executable named name is in one of agentInstallDirs.
func installedAt(name string) bool {
	home, _ := os.UserHomeDir()
	for _, d := range agentInstallDirs {
		if rest, ok := strings.CutPrefix(d, "~/"); ok {
			if home == "" {
				continue
			}
			d = filepath.Join(home, rest)
		}
		found, _ := filepath.Glob(filepath.Join(d, name))
		for _, f := range found {
			if st, err := os.Stat(f); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				return true
			}
		}
	}
	return false
}
