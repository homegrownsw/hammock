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
	"syscall"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
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

// startAgent opens a tab in m.Cwd and starts the agent there, as the
// pane's command (agentShellCommand), its prompt cleaned of control
// characters: nothing is typed into a shell. creator is the pane of the
// agent (or other scoped caller) that asked, "" for you: the new pane is
// that agent's, as one it opened with CreateTab would be, so it doesn't
// count as yours.
func (d *Daemon) startAgent(m *pb.StartAgent, creator string) (*pb.ServerMessage, error) {
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
	// Asks the login shell, at most every few minutes: before d.mu.
	cmd, later := startCommand(ag, m.Prompt, nil)
	cmd = agentCommand(ag, cmd)
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
	spec := &pb.PaneSpec{Cwd: cwd, Command: agentShellCommand(cmd), CreatedByPaneId: creator}
	t, err := d.createTab(gid, "", spec, m.TabId, "")
	if err != nil {
		if made {
			d.dropQuietGroup(gid) // the pane couldn't start: never shown
		}
		d.mu.Unlock()
		return nil, err
	}
	p := d.panes[t.FocusedPaneId]
	if p != nil && creator != "" {
		d.markAgentMade(p.id) // before anyone can act on it as yours
	}
	d.mu.Unlock()
	if p == nil {
		return nil, errors.New("the new tab has no pane")
	}
	p.mu.Lock()
	// Known before its hooks say so.
	p.info.Agent = &pb.Agent{Harness: m.Harness, State: pb.AgentState_AGENT_STATE_STARTING, StateSinceUnixMs: time.Now().UnixMilli(), Heuristic: true}
	p.mu.Unlock()
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
	at     time.Time
	list   []string
	onPath map[string]bool // commands the login shell finds itself
}

func installedAgents() []string {
	agentsCache.Lock()
	defer agentsCache.Unlock()
	if time.Since(agentsCache.at) < 3*time.Minute && agentsCache.onPath != nil {
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
	agentsCache.at, agentsCache.list, agentsCache.onPath = time.Now(), list, found
	if err != nil && ctx.Err() != nil {
		agentsCache.onPath = map[string]bool{} // not known: asked again next time
		agentsCache.at = time.Time{}
	}
	return list
}

// agentCommand is typed (a command line starting ag) with ag's command
// replaced by where your interactive shell finds it when the login shell
// doesn't find it itself: started with `-l -c` (agentShellCommand), an
// agent on a PATH or alias only ~/.zshrc sets (Claude's own installer,
// nvm) isn't found, but it was when its command line was typed into an
// interactive shell. Only there: never a folder it's often installed in
// that isn't on your PATH (~/.npm/bin, ~/.local/bin), where a program of
// yours could plant one of that name. Not found, it's run by name, and the
// shell says so.
func agentCommand(ag *agents.Agent, typed string) string {
	rest, ok := strings.CutPrefix(typed, ag.Command)
	if !ok || rest != "" && rest[0] != ' ' {
		return typed
	}
	installedAgents()
	agentsCache.Lock()
	known, on := agentsCache.onPath != nil, agentsCache.onPath[ag.Command]
	agentsCache.Unlock()
	if !known || on {
		return typed
	}
	if p := interactivePath(ag.Command); p != "" {
		return shellQuote(p) + rest
	}
	return typed
}

// interactiveCache is what interactive shells said of commands lately.
var interactiveCache struct {
	sync.Mutex
	at   map[string]time.Time
	path map[string]string
}

// interactivePath is where your interactive login shell finds the command
// name (`$SHELL -l -i -c 'command -v name'`: asked at most every few
// minutes, for at most five seconds), or "" if it doesn't, or only as
// something that isn't a program.
func interactivePath(name string) string {
	interactiveCache.Lock()
	defer interactiveCache.Unlock()
	if at, ok := interactiveCache.at[name]; ok && time.Since(at) < 3*time.Minute {
		return interactiveCache.path[name]
	}
	if interactiveCache.at == nil {
		interactiveCache.at, interactiveCache.path = map[string]time.Time{}, map[string]string{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, loginShell(), "-l", "-i", "-c", "command -v "+shellQuote(name))
	cmd.Stdin = nil // (/dev/null: nothing to read, no terminal to take)
	// Its own process group, ended whole on a timeout (with whatever an
	// rc file started), and not waited on past it for output.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	out, _ := cmd.Output()
	p := commandPath(name, string(out))
	interactiveCache.at[name], interactiveCache.path[name] = time.Now(), p
	return p
}

// commandPath reads what `command -v name` printed (the last line: an rc
// file may print before it): an absolute path, or an alias to one (zsh's
// "alias name=…", bash's "alias name='…'"), to an executable; else "".
func commandPath(name, out string) string {
	var last string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			last = l
		}
	}
	if a, ok := strings.CutPrefix(last, "alias "+name+"="); ok {
		a = strings.Trim(a, "'\"")
		last, _, _ = strings.Cut(a, " ")
		last = expandHome(last)
	}
	if !filepath.IsAbs(last) {
		return ""
	}
	if st, err := os.Stat(last); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
		return ""
	}
	return filepath.Clean(last)
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

// installedAt: an executable named name is in one of agentInstallDirs
// (for listing it as installed only: never run from there unless your
// shell finds it there, agentCommand).
func installedAt(name string) bool { return installedPath(name) != "" }

// installedPath is the first executable named name in agentInstallDirs, or "".
func installedPath(name string) string {
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
				return f
			}
		}
	}
	return ""
}
