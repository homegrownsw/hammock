package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/agents"
	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// gitRepo makes a repository with one commit on main, with no global git
// config (signing, hooks) in the way.
func testRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Sam")
	t.Setenv("GIT_AUTHOR_EMAIL", "you@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Sam")
	t.Setenv("GIT_COMMITTER_EMAIL", "you@example.com")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "app")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(repo, "README"), "hi\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "first")
	return repo
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGitCheckoutOf(t *testing.T) {
	repo := testRepo(t)
	sub := filepath.Join(repo, "src")
	os.Mkdir(sub, 0o755)
	co, ok := gitCheckoutOf(sub)
	if !ok || co.top != repo || co.repo != repo || co.branch != "main" || co.linked {
		t.Fatalf("main checkout: %+v %v", co, ok)
	}
	wt := filepath.Join(repo, ".claude", "worktrees", "pricing")
	git(t, repo, "worktree", "add", "-q", "-b", "pricing", wt)
	co, ok = gitCheckoutOf(wt)
	if !ok || co.top != wt || co.repo != repo || co.branch != "pricing" || !co.linked {
		t.Fatalf("linked worktree: %+v %v", co, ok)
	}
	git(t, wt, "checkout", "-q", "--detach")
	if co, _ = gitCheckoutOf(wt); co.branch != "" {
		t.Fatalf("detached: branch %q", co.branch)
	}
	if _, ok := gitCheckoutOf(t.TempDir()); ok {
		t.Fatal("a checkout outside any repository")
	}
}

func TestWorktreeCommand(t *testing.T) {
	repo := testRepo(t)
	check := func(harness, want string) {
		t.Helper()
		if got := worktreeCommand(filepath.Join(repo), harness); got != want {
			t.Fatalf("%s: %q, want %q", harness, got, want)
		}
	}
	check("codex", "git worktree add -b {name} .claude/worktrees/{name}")
	check("claude", "claude --worktree {name}")
	writeFile(t, filepath.Join(repo, "package.json"), `{"scripts": {"worktree:add": "x"}}`)
	writeFile(t, filepath.Join(repo, "pnpm-lock.yaml"), "")
	writeFile(t, filepath.Join(repo, "yarn.lock"), "")
	check("claude", "pnpm run 'worktree:add' {name}") // (the first lockfile, always)
	writeFile(t, filepath.Join(repo, "Makefile"), "worktree-new:\n\tgit worktree add $(NAME)\nVAR := 1\n")
	check("claude", "make worktree-new NAME={name}")
	// A justfile: only a recipe that takes an argument, not a setting.
	writeFile(t, filepath.Join(repo, "justfile"), "set shell := [\"bash\"]\nworktree:\n    echo\n# worktree-add name:\n[private]\n@worktree-create name *args: deps\n    echo {{name}}\n")
	check("claude", "just worktree-create {name}")
	writeFile(t, filepath.Join(repo, ".hammock", "config"), "# mine\nworktree-create = \"scripts/wt {name} {path}\"\n")
	check("claude", "scripts/wt {name} '"+filepath.Join(repo, ".claude", "worktrees", "{name}")+"'")
	if worktreeCommand(t.TempDir(), "claude") != "" {
		t.Fatal("a command outside a repository")
	}
}

func TestMakeWorktreeWithGit(t *testing.T) {
	repo := testRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), ".env\nnode_modules/\n")
	writeFile(t, filepath.Join(repo, ".env"), "TOKEN=x\n")
	writeFile(t, filepath.Join(repo, "node_modules", "a.js"), "")
	writeFile(t, filepath.Join(repo, ".worktreeinclude"), ".env\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "ignore")

	// Codex has no worktree option of its own: git makes it.
	w, err := makeWorktree(filepath.Join(repo), "fix/login", agents.Get("codex"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(repo, ".claude", "worktrees", "fix-login")
	if w.path != want || w.agentFlag != "" {
		t.Fatalf("made %+v, want %s", w, want)
	}
	if co, _ := gitCheckoutOf(want); co.branch != "fix/login" || !co.linked {
		t.Fatalf("its checkout: %+v", co)
	}
	if b, err := os.ReadFile(filepath.Join(want, ".env")); err != nil || string(b) != "TOKEN=x\n" {
		t.Fatalf(".env not copied: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(want, "node_modules")); err == nil {
		t.Fatal("copied what .worktreeinclude doesn't name")
	}
	if s := git(t, repo, "status", "--porcelain"); s != "" {
		t.Fatalf("the worktree shows in the repository's status: %q", s)
	}
	// Again (a start that failed after making it): reused.
	if w, err := makeWorktree(repo, "fix/login", agents.Get("codex")); err != nil || !w.reused || w.path != want {
		t.Fatalf("again: %+v %v", w, err)
	}
	// An existing branch is checked out, not made again.
	git(t, repo, "branch", "old")
	if w, err := makeWorktree(repo, "old", nil); err != nil || filepath.Base(w.path) != "old" {
		t.Fatalf("existing branch: %+v %v", w, err)
	}
	for _, bad := range []string{"", "-x", "a..b", "a b", "x.lock", "a/"} {
		if _, err := makeWorktree(repo, bad, nil); err == nil {
			t.Errorf("made a worktree called %q", bad)
		}
	}
	if _, err := makeWorktree(t.TempDir(), "x", nil); err == nil {
		t.Error("made a worktree outside a repository")
	}
}

func TestMakeWorktreeAgentsOwn(t *testing.T) {
	repo := testRepo(t)
	w, err := makeWorktree(repo, "pricing", agents.Claude)
	if err != nil || w.agentFlag != "--worktree 'pricing'" || w.startIn != repo || w.path != filepath.Join(repo, ".claude", "worktrees", "pricing") {
		t.Fatalf("Claude Code's own: %+v %v", w, err)
	}
	if cmd := withAgentOption(agents.Claude, "claude 'do it'", w.agentFlag); cmd != "claude --worktree 'pricing' 'do it'" {
		t.Fatalf("command: %s", cmd)
	}
	// A folder for them set in .hammock/config: git, there.
	writeFile(t, filepath.Join(repo, ".hammock", "config"), "worktree-dir = ../wt\n")
	w, err = makeWorktree(repo, "pricing", agents.Claude)
	if want := filepath.Join(filepath.Dir(repo), "wt", "pricing"); err != nil || w.path != want {
		t.Fatalf("worktree-dir: %+v %v, want %s", w, err, want)
	}
}

func TestMakeWorktreeRepoCommand(t *testing.T) {
	repo := testRepo(t)
	t.Setenv("SHELL", "/bin/sh")
	writeFile(t, filepath.Join(repo, ".hammock", "config"),
		"worktree-create = git worktree add -q -b wt-{name} ../elsewhere/{name}\n")
	w, err := makeWorktree(repo, "pricing", agents.Claude)
	if want := filepath.Join(filepath.Dir(repo), "elsewhere", "pricing"); err != nil || evalPath(w.path) != want {
		t.Fatalf("made %+v %v, want %s", w, err, want)
	}
	// One that fails says why.
	writeFile(t, filepath.Join(repo, ".hammock", "config"), "worktree-create = echo nope >&2; exit 3\n")
	if _, err := makeWorktree(repo, "x", nil); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("failure: %v", err)
	}
	// One that makes nothing.
	writeFile(t, filepath.Join(repo, ".hammock", "config"), "worktree-create = true\n")
	if _, err := makeWorktree(repo, "y", nil); err == nil || !strings.Contains(err.Error(), "no new worktree") {
		t.Fatalf("nothing made: %v", err)
	}
}

func TestMakeWorktreeJustRecipe(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("no just")
	}
	repo := testRepo(t)
	t.Setenv("SHELL", "/bin/sh")
	writeFile(t, filepath.Join(repo, "justfile"), "worktree name:\n    git worktree add -q trees/{{name}} -b {{name}}\n")
	w, err := makeWorktree(repo, "pricing", agents.Claude)
	if want := filepath.Join(repo, "trees", "pricing"); err != nil || evalPath(w.path) != want {
		t.Fatalf("made %+v %v, want %s", w, err, want)
	}
}

func TestRemoveWorktree(t *testing.T) {
	repo := testRepo(t)
	d := &Daemon{tabs: map[string]*pb.Tab{}, panes: map[string]*pane{}}
	wt := filepath.Join(repo, ".claude", "worktrees", "pricing")
	git(t, repo, "worktree", "add", "-q", "-b", "pricing", wt)
	writeFile(t, filepath.Join(wt, "new.txt"), "x")
	if err := d.removeWorktree(&pb.RemoveWorktree{Path: wt}, false); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("removed one with uncommitted changes: %v", err)
	}
	if err := d.removeWorktree(&pb.RemoveWorktree{Path: repo}, false); err == nil {
		t.Fatal("removed the main folder")
	}
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-q", "-m", "new")
	git(t, repo, "merge", "-q", "--ff-only", "pricing")
	if err := d.removeWorktree(&pb.RemoveWorktree{Path: wt}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); err == nil {
		t.Fatal("still there")
	}
	if out, _ := gitOut(repo, "branch", "--list", "pricing"); strings.TrimSpace(out) != "" {
		t.Fatal("its merged branch is kept")
	}
	// Forced, with changes; an unmerged branch is kept.
	git(t, repo, "worktree", "add", "-q", "-b", "wip", wt)
	writeFile(t, filepath.Join(wt, "wip.txt"), "x")
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-q", "-m", "wip")
	writeFile(t, filepath.Join(wt, "more.txt"), "x")
	if err := d.removeWorktree(&pb.RemoveWorktree{Path: wt, Force: true}, false); err != nil {
		t.Fatal(err)
	}
	if out, _ := gitOut(repo, "branch", "--list", "wip"); strings.TrimSpace(out) == "" {
		t.Fatal("an unmerged branch was deleted")
	}
}

// Only the branch's own pull request counts: not a fork's of the same
// name, nor an older branch's that had the name (its head isn't a commit
// this branch has pointed at).
func TestPickPR(t *testing.T) {
	merged := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	prs := []ghPR{
		{Number: 9, State: "OPEN", IsDraft: true, HeadRefOid: "fork", IsCrossRepository: true},
		{Number: 8, State: "OPEN", HeadRefOid: "mine"},
		{Number: 7, State: "MERGED", HeadRefOid: "old", MergedAt: merged, ClosedAt: merged},
	}
	if pr, _ := pickPR(prs, map[string]bool{"mine": true, "fork": true}); pr.GetNumber() != 8 || pr.GetState() != "open" {
		t.Fatalf("its own: %v", pr)
	}
	if pr, _ := pickPR(prs, map[string]bool{"new": true}); pr != nil {
		t.Fatalf("an older branch's of the same name: %v", pr)
	}
	pr, at := pickPR(prs, map[string]bool{"old": true})
	if pr.GetState() != "merged" || !at.Equal(merged) {
		t.Fatalf("merged: %v %v", pr, at)
	}
}

// A fresh branch brought up to date with main (a merge, a pull) isn't
// "merged"; one with a commit of its own, in main, is.
func TestMergedNeedsOwnCommit(t *testing.T) {
	repo := testRepo(t)
	wt := filepath.Join(repo, ".claude", "worktrees", "fresh")
	git(t, repo, "worktree", "add", "-q", "-b", "fresh", wt)
	writeFile(t, filepath.Join(repo, "more"), "x")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "main moves on")
	git(t, wt, "merge", "-q", "main")
	if h := branchHistoryOf(repo, "fresh"); h.ownCommit || !gitInDefault(repo, "fresh", "main") {
		t.Fatalf("brought up to date: %+v", h)
	}
	writeFile(t, filepath.Join(wt, "work"), "x")
	git(t, wt, "add", ".")
	git(t, wt, "commit", "-q", "-m", "work")
	git(t, repo, "merge", "-q", "--ff-only", "fresh")
	if h := branchHistoryOf(repo, "fresh"); !h.ownCommit || !gitInDefault(repo, "fresh", "main") || h.created.IsZero() {
		t.Fatalf("merged: %+v", h)
	}
}

// What removes a worktree, and with what name: its folder within the
// worktrees' folder (feat/x), not its last part; the repository's own
// command won't take uncommitted changes, even forced.
func TestRemoveCommand(t *testing.T) {
	repo := testRepo(t)
	wt := filepath.Join(repo, ".claude", "worktrees", "feat", "x")
	if run, own := removeCommand(repo, wt, "feat/x", true); own || run != "git worktree remove --force '"+wt+"'" {
		t.Fatalf("git: %q %v", run, own)
	}
	writeFile(t, filepath.Join(repo, "justfile"), "worktree-remove name:\n    echo {{name}}\n")
	if run, own := removeCommand(repo, wt, "feat/x", true); !own || run != "just worktree-remove 'feat/x'" {
		t.Fatalf("recipe: %q %v", run, own)
	}
	git(t, repo, "worktree", "add", "-q", "-b", "feat/x", wt)
	writeFile(t, filepath.Join(wt, "new.txt"), "x")
	d := &Daemon{tabs: map[string]*pb.Tab{}, panes: map[string]*pane{}}
	if err := d.removeWorktree(&pb.RemoveWorktree{Path: wt, Force: true}, false); err == nil || !strings.Contains(err.Error(), "commit or discard") {
		t.Fatalf("a recipe told to drop changes: %v", err)
	}
}

// An agent that makes its own worktree as it starts (Claude Code's
// --worktree): its tab is in that worktree once it's there, though its
// shell is still in the repository.
func TestAgentsOwnWorktreeTab(t *testing.T) {
	repo := testRepo(t)
	if n := agentWorktreeName([]string{"/bin/zsh", "-l", "-c", "'/Users/me/.local/bin/claude' --worktree 'pricing' 'go'; cd x; exec zsh -l"}); n != "pricing" {
		t.Fatalf("name %q", n)
	}
	a := startComputer(t)
	g := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "App"}}}))
	tab := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g,
		Pane: &pb.PaneSpec{Cwd: repo, Command: []string{"/bin/sh", "-c", ": claude --worktree 'pricing'; exec cat"}}}}}))
	tabGit := func() *pb.TabGit {
		a.d.refreshTabGit()
		a.d.mu.Lock()
		defer a.d.mu.Unlock()
		return a.d.tabs[tab].GetGit()
	}
	if tg := tabGit(); tg.GetWorktree() != "" || tg.GetBranch() != "main" {
		t.Fatalf("before it's made: %v", tg)
	}
	wt := filepath.Join(repo, ".claude", "worktrees", "pricing")
	git(t, repo, "worktree", "add", "-q", "-b", "worktree-pricing", wt)
	if tg := tabGit(); tg.GetWorktree() != wt || tg.GetBranch() != "worktree-pricing" {
		t.Fatalf("once it's made: %v", tg)
	}
}

// A tab in a worktree knows its branch; once the branch's pull request is
// merged, Remove Worktree is offered after the grace period, and removing
// it closes the tab.
func TestTabWorktreeLifecycle(t *testing.T) {
	repo := testRepo(t)
	wt := filepath.Join(repo, ".claude", "worktrees", "pricing")
	git(t, repo, "worktree", "add", "-q", "-b", "pricing", wt)
	git(t, repo, "remote", "add", "origin", "https://github.com/acme/widgets.git")

	tip := git(t, wt, "rev-parse", "HEAD")
	merged := false
	oldAsk, oldGrace := askBranchPRs, worktreeGrace
	t.Cleanup(func() { askBranchPRs, worktreeGrace = oldAsk, oldGrace })
	askBranchPRs = func(repo, branch string) ([]ghPR, bool) {
		if branch != "pricing" {
			return nil, true
		}
		if merged {
			at := time.Now().Add(-time.Hour) // (before the branch was made: no help with the grace)
			return []ghPR{{Number: 42, State: "MERGED", HeadRefOid: tip, MergedAt: at, ClosedAt: at}}, true
		}
		return []ghPR{{Number: 42, State: "OPEN", HeadRefOid: tip}}, true
	}

	a := startComputer(t)
	g := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "App"}}}))
	tab := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Cwd: wt, Command: []string{"/bin/cat"}}}}}))
	tabGit := func() *pb.TabGit {
		a.d.refreshTabGit()
		a.d.mu.Lock()
		defer a.d.mu.Unlock()
		return a.d.tabs[tab].GetGit()
	}
	if tg := tabGit(); tg.GetBranch() != "pricing" || tg.GetWorktree() != wt || tg.GetRepo() != repo {
		t.Fatalf("tab's git: %v", tg)
	}
	a.d.checkBranches()
	if tg := tabGit(); tg.GetPr().GetNumber() != 42 || tg.GetDone() || tg.GetRemovable() {
		t.Fatalf("open pull request: %v", tg)
	}
	merged = true
	due := func() {
		a.d.branches.mu.Lock()
		for _, s := range a.d.branches.status {
			s.prAsked = time.Time{} // due again
		}
		a.d.branches.mu.Unlock()
	}
	due()
	worktreeGrace = time.Minute
	a.d.checkBranches()
	if tg := tabGit(); !tg.GetDone() || tg.GetRemovable() || tg.GetPr().GetState() != "merged" {
		t.Fatalf("merged (by gh's word an hour ago, before the branch was made): offered at once: %v", tg)
	}
	worktreeGrace = 0
	due()
	a.d.checkBranches()
	if tg := tabGit(); !tg.GetRemovable() || tg.GetDirty() || tg.GetRemoveWith() != "git worktree remove '"+wt+"'" {
		t.Fatalf("after the grace period: %v", tg)
	}
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_RemoveWorktree{RemoveWorktree: &pb.RemoveWorktree{TabId: tab}}})
	a.d.mu.Lock()
	_, still := a.d.tabs[tab]
	a.d.mu.Unlock()
	if still {
		t.Fatal("the tab in the removed worktree is still open")
	}
	if _, err := os.Stat(wt); err == nil {
		t.Fatal("the worktree is still there")
	}
}

// hmk agent --worktree: the tab starts in the new worktree, in the Group
// of the folder it was made from.
func TestStartAgentInNewWorktree(t *testing.T) {
	repo := testRepo(t)
	a := startComputer(t)
	g := client.CreatedID(call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "App"}}}))
	call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Cwd: repo, Command: []string{"/bin/cat"}}}}})
	r := call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{Harness: "codex", Cwd: repo, Worktree: "pricing"}}})
	tab := client.CreatedID(r)
	a.d.mu.Lock()
	tb := a.d.tabs[tab]
	p := a.d.panes[tb.GetFocusedPaneId()]
	a.d.mu.Unlock()
	if tb.GetGroupId() != g {
		t.Fatalf("started in Group %s, want the repository's %s", tb.GetGroupId(), g)
	}
	if want := filepath.Join(repo, ".claude", "worktrees", "pricing"); p == nil || p.cwd() != want {
		t.Fatalf("started in %v, want %s", p, want)
	}
	// Beside a pane: refused (a worktree is a tab's).
	if _, err := a.c.Call(t.Context(), &pb.ClientMessage{Msg: &pb.ClientMessage_StartAgent{StartAgent: &pb.StartAgent{Harness: "codex", Cwd: repo, Worktree: "x", SplitPaneId: tb.FocusedPaneId}}}); err == nil {
		t.Fatal("started a worktree's agent in a split")
	}
	// The folder list says how one would be made.
	fl := call(t, a.c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListFolders{ListFolders: &pb.ListFolders{Path: repo, Harness: "codex"}}}).GetFolderList()
	if fl.GetWorktreeCommand() != "git worktree add -b {name} .claude/worktrees/{name}" {
		t.Fatalf("worktree_command %q", fl.GetWorktreeCommand())
	}
}
