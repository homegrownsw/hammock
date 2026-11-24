package daemon

// Worktrees: Hammock knows the git checkout each tab works in (its branch,
// a linked worktree's folder, the branch's pull request), starts an agent
// in a new worktree made the repository's own way, and offers to remove a
// worktree once its branch is merged, never by itself.
//
// Knowing needs no setup: a tab's checkout is read from its panes' folders
// (gitCheckoutOf: a few small files, each poll). Whether its branch is done
// is asked of git (merged into the default branch) and gh (its pull
// request), now and then, off the lock (watchBranches).
//
// A new worktree is made the first of these ways that applies
// (worktreeCreate):
//  1. .hammock/config in the repository: worktree-create, a command with
//     {name} and {path} in it; worktree-dir, the folder new ones go in.
//  2. a recipe the repository has: a justfile recipe, Makefile target or
//     package.json script named worktree (or worktree-add, …).
//  3. the agent's own option (Claude Code's --worktree NAME).
//  4. git worktree add -b NAME .claude/worktrees/NAME, then the git-ignored
//     files .worktreeinclude names copied in.
//
// Removing one (RemoveWorktree): .hammock/config's worktree-remove, a
// recipe (worktree-remove, …), else git worktree remove and the branch
// deleted if it's merged; then the tabs in it close.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/homegrownsw/hammock/internal/agents"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/regfile"
)

// defaultWorktreeDir is where new worktrees go in their repository, unless
// .hammock/config says otherwise: where Claude Code puts its own.
const defaultWorktreeDir = ".claude/worktrees"

var (
	// worktreeGrace is how long after a branch is done Remove Worktree is
	// offered: an agent that cleans up after itself gets there first.
	worktreeGrace = 10 * time.Minute
	// branchEvery is how often tabs' branches are checked (git: cheap);
	// their pull requests less often (prDue).
	branchEvery = time.Minute
	// worktreeTimeout bounds a command that makes or removes a worktree.
	worktreeTimeout = 10 * time.Minute
	// askBranchPR asks gh for a branch's pull request (tests replace it).
	askBranchPR = ghBranchPR
)

// checkout is a git checkout: a repository's main folder, or a linked
// worktree of it.
type checkout struct {
	top    string // this checkout's folder
	repo   string // the repository's main folder
	gitDir string // its own git dir
	branch string // "" when detached
	linked bool   // a linked worktree (git worktree add), not the main folder
}

// gitCheckoutOf is the checkout dir is in, read from git's files (no git
// run): false outside one.
func gitCheckoutOf(dir string) (checkout, bool) {
	if dir == "" || !filepath.IsAbs(dir) {
		return checkout{}, false
	}
	for d := filepath.Clean(dir); d != "/" && d != "."; d = filepath.Dir(d) {
		g := filepath.Join(d, ".git")
		st, err := os.Stat(g)
		if err != nil {
			continue
		}
		co := checkout{top: d, repo: d, gitDir: g}
		if !st.IsDir() {
			// A worktree (or a submodule): ".git" names its git dir, and a
			// worktree's names the repository's (commondir).
			b, err := regfile.ReadFile(g, 4096) // not a FIFO in some folder the shell's in
			if err != nil {
				return checkout{}, false
			}
			gd, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
			if !ok {
				return checkout{}, false
			}
			if !filepath.IsAbs(gd) {
				gd = filepath.Join(d, gd)
			}
			co.gitDir = filepath.Clean(gd)
			if b, err := regfile.ReadFile(filepath.Join(co.gitDir, "commondir"), 4096); err == nil {
				common := strings.TrimSpace(string(b))
				if !filepath.IsAbs(common) {
					common = filepath.Join(co.gitDir, common)
				}
				common = filepath.Clean(common)
				co.linked = true
				if filepath.Base(common) == ".git" {
					co.repo = filepath.Dir(common)
				} else {
					co.repo = common // a bare repository's worktree
				}
			}
		}
		head, err := regfile.ReadFile(filepath.Join(co.gitDir, "HEAD"), 4096)
		if err != nil {
			return checkout{}, false
		}
		if b, ok := strings.CutPrefix(strings.TrimSpace(string(head)), "ref: refs/heads/"); ok {
			co.branch = b // else detached
		}
		return co, true
	}
	return checkout{}, false
}

// ── Tabs' checkouts ─────────────────────────────────────────────────────

// branchWatch is what's known of tabs' branches beyond their files: merged,
// their pull requests. Its own lock, taken after d.mu (never before).
type branchWatch struct {
	mu     sync.Mutex
	kick   chan struct{}
	status map[string]*branchStatus // branchKey
	repos  map[string]*repoFacts    // by the repository's main folder
}

type branchStatus struct {
	pr       *pb.PullRequest
	prAsked  time.Time
	prDoneAt time.Time // merged or closed, as gh says
	merged   bool      // into the default branch, as git says
	doneSeen time.Time // when this daemon first saw it done
	dirty    bool      // its worktree has uncommitted changes (looked at once it's done)
}

// done: merged, or its pull request merged or closed.
func (s *branchStatus) done() bool {
	return s.merged || s.pr != nil && (s.pr.State == "merged" || s.pr.State == "closed")
}

// doneAt is when it was done: gh's word, else when this daemon saw it.
func (s *branchStatus) doneAt() time.Time {
	if !s.prDoneAt.IsZero() && (s.doneSeen.IsZero() || s.prDoneAt.Before(s.doneSeen)) {
		return s.prDoneAt
	}
	return s.doneSeen
}

type repoFacts struct {
	asked      time.Time
	github     bool   // a remote on github.com: gh can say
	defaultRef string // "main", the branch merged into
}

func branchKey(repo, branch string) string { return repo + "\x00" + branch }

// kicker is the channel that has watchBranches look now.
func (w *branchWatch) kicker() chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.kick == nil {
		w.kick = make(chan struct{}, 1)
	}
	return w.kick
}

func (w *branchWatch) poke() {
	select {
	case w.kicker() <- struct{}{}:
	default:
	}
}

// refreshTabGit sets each tab's Git from its panes' folders (the focused
// terminal's first, then the others') and what's known of the branch.
// Callers hold d.mu.
func (d *Daemon) refreshTabGit() {
	seen := map[string]*checkout{}
	now := time.Now()
	for _, t := range d.tabs {
		g := d.tabGit(t, seen, now)
		if proto.Equal(g, t.Git) {
			continue
		}
		if g.GetBranch() != t.Git.GetBranch() || g.GetRepo() != t.Git.GetRepo() {
			d.branches.poke() // a branch it hasn't looked at
		}
		t.Git = g
		d.publish(&pb.StateUpdate{Tabs: []*pb.Tab{proto.CloneOf(t)}})
	}
}

// tabGit is t's checkout, or nil. seen caches checkouts by folder for one
// pass. Callers hold d.mu.
func (d *Daemon) tabGit(t *pb.Tab, seen map[string]*checkout, now time.Time) *pb.TabGit {
	ids := tabPanes(t)
	if i := slices.Index(ids, t.FocusedPaneId); i > 0 {
		ids = append([]string{t.FocusedPaneId}, slices.Delete(slices.Clone(ids), i, i+1)...)
	}
	var co *checkout
	for _, id := range ids {
		p := d.panes[id]
		if p == nil || !p.isTerminal() {
			continue
		}
		dir := p.cwd()
		if dir == "" {
			continue
		}
		c, ok := seen[dir]
		if !ok {
			if found, isRepo := gitCheckoutOf(dir); isRepo {
				c = &found
			}
			seen[dir] = c
		}
		if c != nil {
			co = c
			break
		}
	}
	if co == nil {
		return nil
	}
	g := &pb.TabGit{Repo: co.repo, Branch: co.branch}
	if co.linked {
		g.Worktree = co.top
	}
	if co.branch == "" {
		return g
	}
	d.branches.mu.Lock()
	defer d.branches.mu.Unlock()
	if s := d.branches.status[branchKey(co.repo, co.branch)]; s != nil {
		g.Pr = s.pr
		g.Done = s.done()
		if g.Done && co.linked && now.Sub(s.doneAt()) >= worktreeGrace {
			g.Removable, g.Dirty = true, s.dirty
		}
	}
	return g
}

// watchBranches looks at tabs' branches every branchEvery, and when a tab
// comes to a new one: merged (git), their pull requests (gh, less often).
func (d *Daemon) watchBranches() {
	t := time.NewTicker(branchEvery)
	defer t.Stop()
	kick := d.branches.kicker()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		case <-kick:
		}
		d.checkBranches()
	}
}

// checkBranches is one look at every tab's branch (off d.mu while git and
// gh run), then the tabs updated.
func (d *Daemon) checkBranches() {
	type target struct{ repo, branch, worktree string }
	targets := map[string]target{}
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	for _, t := range d.tabs {
		if g := t.Git; g != nil && g.Branch != "" {
			k := branchKey(g.Repo, g.Branch)
			if tg, ok := targets[k]; !ok || tg.worktree == "" {
				targets[k] = target{g.Repo, g.Branch, g.Worktree}
			}
		}
	}
	d.mu.Unlock()

	now := time.Now()
	for k, tg := range targets {
		d.checkBranch(k, tg.repo, tg.branch, tg.worktree, now)
	}
	d.branches.mu.Lock()
	for k := range d.branches.status {
		if _, ok := targets[k]; !ok {
			delete(d.branches.status, k) // no tab on it now
		}
	}
	d.branches.mu.Unlock()

	d.mu.Lock()
	if !d.stopped {
		d.refreshTabGit()
	}
	d.mu.Unlock()
}

// checkBranch updates one branch's status.
func (d *Daemon) checkBranch(key, repo, branch, worktree string, now time.Time) {
	facts := d.repoFactsOf(repo, now)
	if facts.defaultRef == "" || branch == facts.defaultRef || defaultBranch(branch) {
		return // the default branch is never done
	}
	d.branches.mu.Lock()
	if d.branches.status == nil {
		d.branches.status = map[string]*branchStatus{}
	}
	s := d.branches.status[key]
	if s == nil {
		s = &branchStatus{}
		d.branches.status[key] = s
	}
	askPR := facts.github && now.Sub(s.prAsked) >= prDue(s.pr)
	d.branches.mu.Unlock()

	merged := gitMerged(repo, branch, facts.defaultRef)
	var pr *pb.PullRequest
	var prDone time.Time
	prOK := false
	if askPR {
		pr, prDone, prOK = askBranchPR(repo, branch)
	}

	d.branches.mu.Lock()
	s.merged = merged
	if askPR {
		s.prAsked = now
		if prOK {
			s.pr, s.prDoneAt = pr, prDone
		}
	}
	done := s.done()
	switch {
	case !done:
		s.doneSeen = time.Time{}
	case s.doneSeen.IsZero():
		s.doneSeen = now
	}
	check := done && worktree != ""
	d.branches.mu.Unlock()

	dirty := false
	if check {
		dirty = gitDirty(worktree)
	}
	d.branches.mu.Lock()
	s.dirty = dirty
	d.branches.mu.Unlock()
}

// prDue is how long a branch's pull request is left before gh is asked
// again: an open one every few minutes, none (or a finished one) less often.
func prDue(pr *pb.PullRequest) time.Duration {
	switch pr.GetState() {
	case "open", "draft":
		return 3 * time.Minute
	case "merged", "closed":
		return 30 * time.Minute
	}
	return 10 * time.Minute
}

// defaultBranch: a name that's a default branch anywhere.
func defaultBranch(b string) bool {
	switch b {
	case "main", "master", "trunk", "develop", "dev":
		return true
	}
	return false
}

// repoFactsOf is what's known of a repository (asked again every ten minutes).
func (d *Daemon) repoFactsOf(repo string, now time.Time) repoFacts {
	d.branches.mu.Lock()
	if f := d.branches.repos[repo]; f != nil && now.Sub(f.asked) < 10*time.Minute {
		d.branches.mu.Unlock()
		return *f
	}
	d.branches.mu.Unlock()
	f := &repoFacts{asked: now}
	if out, err := gitOut(repo, "remote", "-v"); err == nil {
		f.github = strings.Contains(out, "github.com")
	}
	if out, err := gitOut(repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		f.defaultRef = strings.TrimPrefix(strings.TrimSpace(out), "origin/")
	}
	if f.defaultRef == "" {
		for _, b := range []string{"main", "master", "trunk"} {
			if _, err := gitOut(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
				f.defaultRef = b
				break
			}
		}
	}
	d.branches.mu.Lock()
	if d.branches.repos == nil {
		d.branches.repos = map[string]*repoFacts{}
	}
	d.branches.repos[repo] = f
	d.branches.mu.Unlock()
	return *f
}

// gitOut runs git in dir, without taking optional locks, for its output.
func gitOut(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-optional-locks", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	return string(out), err
}

// gitMerged reports whether branch has been worked on (it moved since it
// was made: a new branch, still where it began, isn't "merged") and is in
// the default branch, here or on origin.
func gitMerged(repo, branch, def string) bool {
	log, err := gitOut(repo, "reflog", "show", "--format=%H", "refs/heads/"+branch, "--")
	if err != nil || strings.Count(strings.TrimSpace(log), "\n") < 1 {
		return false
	}
	for _, into := range []string{"refs/heads/" + def, "refs/remotes/origin/" + def} {
		if _, err := gitOut(repo, "merge-base", "--is-ancestor", "refs/heads/"+branch, into); err == nil {
			return true
		}
	}
	return false
}

// gitDirty reports whether a checkout has uncommitted changes, untracked
// files included (git worktree remove refuses those too).
func gitDirty(dir string) bool {
	out, err := gitOut(dir, "status", "--porcelain", "--untracked-files=normal")
	return err == nil && strings.TrimSpace(out) != ""
}

// ghPath is gh, on the PATH or where it's usually installed; "" without it.
func ghPath() string {
	if p, err := exec.LookPath("gh"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh", "/usr/bin/gh"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ghBranchPR asks gh for branch's pull request in repo (the open one, else
// the latest): nil, ok with none; not ok when gh can't say (not installed,
// not signed in, not a GitHub repository).
func ghBranchPR(repo, branch string) (*pb.PullRequest, time.Time, bool) {
	gh := ghPath()
	if gh == "" {
		return nil, time.Time{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "pr", "list", "--head", branch, "--state", "all", "--limit", "5",
		"--json", "number,state,url,title,isDraft,mergedAt,closedAt")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")
	out, err := cmd.Output()
	if err != nil {
		return nil, time.Time{}, false
	}
	return parsePRList(out)
}

// ghPR is one of gh pr list's pull requests.
type ghPR struct {
	Number   int32     `json:"number"`
	State    string    `json:"state"`
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	IsDraft  bool      `json:"isDraft"`
	MergedAt time.Time `json:"mergedAt"`
	ClosedAt time.Time `json:"closedAt"`
}

// parsePRList reads gh pr list's JSON: the open pull request, else the first
// (gh lists the newest first), with when it was merged or closed.
func parsePRList(out []byte) (*pb.PullRequest, time.Time, bool) {
	var prs []ghPR
	if json.Unmarshal(out, &prs) != nil {
		return nil, time.Time{}, false
	}
	if len(prs) == 0 {
		return nil, time.Time{}, true
	}
	i := slices.IndexFunc(prs, func(p ghPR) bool { return p.State == "OPEN" })
	if i < 0 {
		i = 0
	}
	p := prs[i]
	pr := &pb.PullRequest{Number: p.Number, State: strings.ToLower(p.State), Url: p.URL, Title: p.Title}
	if pr.State == "open" && p.IsDraft {
		pr.State = "draft"
	}
	at := p.ClosedAt
	if !p.MergedAt.IsZero() {
		at = p.MergedAt
	}
	return pr, at, true
}

// ── Making and removing them ────────────────────────────────────────────

// worktreeName: a name a new worktree (and its branch) can have.
var worktreeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

func checkWorktreeName(name string) error {
	if !worktreeName.MatchString(name) || strings.Contains(name, "..") || strings.HasSuffix(name, "/") ||
		strings.HasSuffix(name, ".lock") || strings.Contains(name, "//") || len(name) > 100 {
		return fmt.Errorf("%q can't name a worktree: use letters, digits, - _ . and /", name)
	}
	return nil
}

// repoConfig is a repository's .hammock/config (Ghostty's syntax, as
// Hammock's own): the keys that concern worktrees.
type repoConfig struct {
	create, remove, dir string
}

func readRepoConfig(repo string) repoConfig {
	var c repoConfig
	f, err := os.Open(filepath.Join(repo, ".hammock", "config"))
	if err != nil {
		return c
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		switch strings.TrimSpace(k) {
		case "worktree-create":
			c.create = v
		case "worktree-remove":
			c.remove = v
		case "worktree-dir":
			c.dir = v
		}
	}
	return c
}

// worktreeDir is the folder new worktrees go in, in repo.
func (c repoConfig) worktreeDir(repo string) string {
	dir := cmpOr(c.dir, defaultWorktreeDir)
	dir = expandHome(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repo, dir)
	}
	return filepath.Clean(dir)
}

// Recipes' names, most likely first.
var (
	createRecipes = []string{"worktree", "worktree-add", "worktree-new", "worktree-create", "new-worktree", "add-worktree", "create-worktree", "worktree:add", "worktree:new", "worktree:create"}
	removeRecipes = []string{"worktree-remove", "worktree-rm", "worktree-delete", "remove-worktree", "rm-worktree", "delete-worktree", "worktree:remove", "worktree:rm"}
)

// recipe is a repository's own command for a worktree: run as "run NAME".
type recipe struct {
	run   string // "just worktree", "make worktree", "npm run worktree --"
	label string // as people see it: "just worktree"
	make  bool   // NAME=… rather than an argument
}

func (r recipe) command(name string) string {
	if r.make {
		return r.run + " NAME=" + shellQuote(name)
	}
	return r.run + " " + shellQuote(name)
}

// findRecipe is repo's recipe of one of names: a justfile recipe that
// takes an argument, a Makefile target, or a package.json script.
func findRecipe(repo string, names []string) (recipe, bool) {
	for _, f := range []string{"justfile", "Justfile", ".justfile"} {
		if have := justRecipes(filepath.Join(repo, f)); have != nil {
			for _, n := range names {
				if have[n] {
					return recipe{run: "just " + n, label: "just " + n}, true
				}
			}
			break
		}
	}
	for _, f := range []string{"GNUmakefile", "makefile", "Makefile"} {
		if have := makeTargets(filepath.Join(repo, f)); have != nil {
			for _, n := range names {
				if have[n] {
					return recipe{run: "make " + n, label: "make " + n, make: true}, true
				}
			}
			break
		}
	}
	if scripts := packageScripts(repo); scripts != nil {
		pm := "npm"
		for lock, m := range map[string]string{"pnpm-lock.yaml": "pnpm", "yarn.lock": "yarn", "bun.lockb": "bun", "bun.lock": "bun"} {
			if _, err := os.Stat(filepath.Join(repo, lock)); err == nil {
				pm = m
			}
		}
		for _, n := range names {
			if scripts[n] {
				run := pm + " run " + shellQuote(n)
				if pm == "npm" {
					run += " --" // its arguments, not npm's
				}
				return recipe{run: run, label: pm + " run " + n}, true
			}
		}
	}
	return recipe{}, false
}

// justRecipes are the recipes in a justfile that take an argument (nil: no
// such file).
func justRecipes(path string) map[string]bool {
	b, err := regfile.ReadFile(path, 1<<20)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || line[0] == '[' {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 || strings.HasPrefix(line[i:], ":=") {
			continue // not a recipe (an assignment, a setting)
		}
		f := strings.Fields(line[:i])
		if len(f) < 2 { // a recipe with no argument can't be given a name
			continue
		}
		name := strings.TrimPrefix(f[0], "@")
		switch name {
		case "set", "alias", "export", "import", "mod":
			continue
		}
		out[name] = true
	}
	return out
}

var makeTarget = regexp.MustCompile(`^([A-Za-z0-9_.:-]+?)\s*::?(?:[^=]|$)`)

// makeTargets are a Makefile's targets (nil: no such file).
func makeTargets(path string) map[string]bool {
	b, err := regfile.ReadFile(path, 1<<20)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if m := makeTarget.FindStringSubmatch(line); m != nil {
			out[m[1]] = true
		}
	}
	return out
}

// packageScripts are package.json's scripts (nil: none).
func packageScripts(repo string) map[string]bool {
	b, err := regfile.ReadFile(filepath.Join(repo, "package.json"), 1<<20)
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil || len(pkg.Scripts) == 0 {
		return nil
	}
	out := map[string]bool{}
	for k := range pkg.Scripts {
		out[k] = true
	}
	return out
}

// worktreeHow is how a new worktree would be made in repo, for people:
// ".hammock/config", "just worktree", …, or "git" (git worktree add, or an
// agent's own option).
func worktreeHow(repo string) string {
	if readRepoConfig(repo).create != "" {
		return ".hammock/config"
	}
	if r, ok := findRecipe(repo, createRecipes); ok {
		return r.label
	}
	return "git"
}

// madeWorktree is a new worktree: its folder, or (made by the agent as it
// starts) the agent's option to add to its command and the folder to start
// it in.
type madeWorktree struct {
	path      string
	agentFlag string // "--worktree 'name'"
	startIn   string // with agentFlag: the repository
}

// makeWorktree makes a worktree called name from the repository dir is in,
// the first way that applies (the file's comment), and says where it is.
func makeWorktree(dir, name string, ag *agents.Agent) (madeWorktree, error) {
	if err := checkWorktreeName(name); err != nil {
		return madeWorktree{}, err
	}
	co, ok := gitCheckoutOf(dir)
	if !ok {
		return madeWorktree{}, fmt.Errorf("%s isn't in a git repository", dir)
	}
	repo := co.repo
	cfg := readRepoConfig(repo)
	path := filepath.Join(cfg.worktreeDir(repo), strings.ReplaceAll(name, "/", "-"))
	var run string
	if cfg.create != "" {
		run = strings.NewReplacer("{name}", shellQuote(name), "{path}", shellQuote(path)).Replace(cfg.create)
	} else if r, ok := findRecipe(repo, createRecipes); ok {
		run = r.command(name)
	}
	if run != "" {
		before, _ := worktreeList(repo)
		if _, err := runInRepo(repo, run); err != nil {
			return madeWorktree{}, err
		}
		after, err := worktreeList(repo)
		if err != nil {
			return madeWorktree{}, err
		}
		if p := newWorktree(before, after, name, path); p != "" {
			return madeWorktree{path: p}, nil
		}
		return madeWorktree{}, fmt.Errorf("ran %s, but no new worktree appeared", run)
	}
	if ag != nil && ag.Worktree != "" && cfg.dir == "" {
		return madeWorktree{agentFlag: ag.Worktree + " " + shellQuote(name), startIn: repo}, nil
	}
	if err := gitAddWorktree(co, path, name); err != nil {
		return madeWorktree{}, err
	}
	copyWorktreeIncludes(repo, path)
	return madeWorktree{path: path}, nil
}

// worktreeEntry is one of git worktree list's.
type worktreeEntry struct{ path, branch string }

// worktreeList is repo's worktrees (git worktree list --porcelain).
func worktreeList(repo string) ([]worktreeEntry, error) {
	out, err := gitOut(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var list []worktreeEntry
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			list = append(list, worktreeEntry{path: filepath.Clean(p)})
		} else if b, ok := strings.CutPrefix(line, "branch "); ok && len(list) > 0 {
			list[len(list)-1].branch = strings.TrimPrefix(b, "refs/heads/")
		}
	}
	return list, nil
}

// newWorktree is the worktree a command made: the one in after that wasn't
// in before (the one on branch name, or at path, if there's more than one);
// else one already on branch name or at path.
func newWorktree(before, after []worktreeEntry, name, path string) string {
	was := map[string]bool{}
	for _, w := range before {
		was[evalPath(w.path)] = true
	}
	var made []worktreeEntry
	for _, w := range after {
		if !was[evalPath(w.path)] {
			made = append(made, w)
		}
	}
	if len(made) == 1 {
		return made[0].path
	}
	for _, set := range [][]worktreeEntry{made, after} {
		for _, w := range set {
			if w.branch == name || evalPath(w.path) == evalPath(path) {
				return w.path
			}
		}
	}
	return ""
}

// evalPath is p with its symlinks resolved (/tmp, /var on a Mac), else as it is.
func evalPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// gitAddWorktree makes a worktree at path on branch name: a new branch from
// what co has checked out, or the branch if it's there already. Its folder
// is kept out of the repository's status (.git/info/exclude) when it's in it.
func gitAddWorktree(co checkout, path, name string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s is there already", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	args := []string{"worktree", "add", path, name}
	if _, err := gitOut(co.repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err != nil {
		args = []string{"worktree", "add", "-b", name, path}
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", co.top}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s failed: %s", strings.Join(args, " "), tailLines(out, 8))
	}
	excludeWorktrees(co.repo, path)
	return nil
}

// excludeWorktrees adds the folder worktrees go in to the repository's
// .git/info/exclude when it's inside the repository and not ignored yet,
// so they don't show as untracked there.
func excludeWorktrees(repo, path string) {
	rel, err := filepath.Rel(repo, filepath.Dir(path))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return
	}
	if _, err := gitOut(repo, "check-ignore", "-q", rel+"/"); err == nil {
		return // ignored already
	}
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	if st, err := os.Stat(filepath.Join(repo, ".git")); err != nil || !st.IsDir() {
		return
	}
	_ = os.MkdirAll(filepath.Dir(exclude), 0o755)
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n# Worktrees Hammock made\n/%s/\n", filepath.ToSlash(rel))
}

// copyWorktreeIncludes copies into a new worktree the files in repo that
// are git-ignored and match .worktreeinclude's patterns (gitignore's
// syntax), as Claude Code does: .env files and the like, which a fresh
// checkout lacks.
func copyWorktreeIncludes(repo, path string) {
	inc := filepath.Join(repo, ".worktreeinclude")
	if _, err := os.Stat(inc); err != nil {
		return
	}
	out, err := gitOut(repo, "ls-files", "-z", "--others", "--ignored", "--exclude-from="+inc)
	if err != nil {
		return
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 || len(files) > 1000 {
		return // (a pattern matching a whole node_modules isn't meant)
	}
	ignored := map[string]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "check-ignore", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(files, "\x00") + "\x00")
	if out, err := cmd.Output(); err == nil || len(out) > 0 {
		for _, f := range strings.Split(string(out), "\x00") {
			ignored[f] = true
		}
	}
	for _, f := range files {
		if !ignored[f] {
			continue // tracked or not ignored: the checkout has it already, or it isn't meant
		}
		copyInclude(filepath.Join(repo, f), filepath.Join(path, f))
	}
}

// copyInclude copies a regular file, keeping its mode; one there already is kept.
func copyInclude(from, to string) {
	st, err := os.Lstat(from)
	if err != nil || !st.Mode().IsRegular() {
		return
	}
	if _, err := os.Lstat(to); err == nil {
		return
	}
	in, err := os.Open(from)
	if err != nil {
		return
	}
	defer in.Close()
	if os.MkdirAll(filepath.Dir(to), 0o755) != nil {
		return
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err != nil || cerr != nil {
		os.Remove(to)
	}
}

// runInRepo runs a command line as your login shell does, in repo, for at
// most worktreeTimeout: its output, and an error that ends with the last
// of it when it fails.
func runInRepo(repo, command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), worktreeTimeout)
	defer cancel()
	shell := loginShell()
	switch filepath.Base(shell) {
	case "sh", "bash", "zsh", "dash", "ksh", "mksh", "yash", "fish":
	default:
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-l", "-c", command)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "GIT_TERMINAL_PROMPT=0")
	// Its own process group, ended whole on a timeout.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("%s took longer than %s: %s", command, worktreeTimeout, tailLines(out, 12))
	}
	if err != nil {
		return string(out), fmt.Errorf("%s failed (%v): %s", command, err, tailLines(out, 12))
	}
	return string(out), nil
}

// tailLines is out's last n lines, trimmed.
func tailLines(out []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) > n {
		lines = append([]string{"…"}, lines[len(lines)-n:]...)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// errWorktreeDirty: refused, the worktree has uncommitted changes.
var errWorktreeDirty = errors.New("it has uncommitted changes")

// removeWorktree answers RemoveWorktree: the worktree (the tab's, or at
// path) removed the repository's own way, then the tabs in it closed. Runs
// off d.mu.
func (d *Daemon) removeWorktree(m *pb.RemoveWorktree) error {
	path := m.Path
	if m.TabId != "" {
		d.mu.Lock()
		t := d.tabs[m.TabId]
		if t == nil {
			d.mu.Unlock()
			return fmt.Errorf("no such tab %q", m.TabId)
		}
		path = t.Git.GetWorktree()
		d.mu.Unlock()
		if path == "" {
			return errors.New("that tab isn't in a linked worktree")
		}
	}
	path = filepath.Clean(expandHome(strings.TrimSpace(path)))
	if !filepath.IsAbs(path) {
		return errors.New("the worktree's folder must be an absolute path")
	}
	co, ok := gitCheckoutOf(path)
	if !ok {
		return fmt.Errorf("%s isn't in a git repository", path)
	}
	if !co.linked {
		return fmt.Errorf("%s is the repository's main folder, not a worktree of it", co.top)
	}
	path = co.top
	if !m.Force && gitDirty(path) {
		return fmt.Errorf("%s: %w", path, errWorktreeDirty)
	}
	repo := co.repo
	cfg := readRepoConfig(repo)
	name := filepath.Base(path)
	var run string
	if cfg.remove != "" {
		run = strings.NewReplacer("{name}", shellQuote(name), "{path}", shellQuote(path), "{branch}", shellQuote(co.branch)).Replace(cfg.remove)
	} else if r, ok := findRecipe(repo, removeRecipes); ok {
		run = r.command(name)
	}
	if run != "" {
		if _, err := runInRepo(repo, run); err != nil {
			return err
		}
	} else {
		args := []string{"worktree", "remove", path}
		if m.Force {
			args = []string{"worktree", "remove", "--force", path}
		}
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s failed: %s", strings.Join(args, " "), tailLines(out, 8))
		}
		if co.branch != "" {
			_, _ = gitOut(repo, "branch", "-d", co.branch) // only if it's merged: kept otherwise
		}
	}
	if list, err := worktreeList(repo); err == nil && slices.ContainsFunc(list, func(w worktreeEntry) bool { return evalPath(w.path) == evalPath(path) }) {
		return fmt.Errorf("ran %s, but the worktree is still there", cmpOr(run, "git worktree remove"))
	}

	// The tabs in it close: there's nothing for them to do there now.
	d.mu.Lock()
	defer d.mu.Unlock()
	var close []string
	for id, t := range d.tabs {
		if w := t.Git.GetWorktree(); w != "" && evalPath(w) == evalPath(path) || d.tabOnlyIn(t, path) {
			close = append(close, id)
		}
	}
	for _, id := range close {
		_ = d.closeTab(id)
	}
	d.branches.poke()
	return nil
}

// tabOnlyIn reports whether every terminal of t's with a folder was in dir
// (or under it). Callers hold d.mu.
func (d *Daemon) tabOnlyIn(t *pb.Tab, dir string) bool {
	in := false
	for _, id := range tabPanes(t) {
		p := d.panes[id]
		if p == nil || !p.isTerminal() {
			continue
		}
		c := p.cwd()
		if c == "" {
			continue
		}
		if c != dir && !strings.HasPrefix(c, dir+string(filepath.Separator)) {
			return false
		}
		in = true
	}
	return in
}

// withAgentOption puts an option of the agent's (its --worktree) into the
// command line that starts it, after its program.
func withAgentOption(ag *agents.Agent, cmd, opt string) string {
	if rest, ok := strings.CutPrefix(cmd, ag.Command); ok && (rest == "" || rest[0] == ' ') {
		return ag.Command + " " + opt + rest
	}
	return cmd + " " + opt
}
