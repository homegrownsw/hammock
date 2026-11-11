package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Sam", "GIT_AUTHOR_EMAIL=sam@example.com",
		"GIT_COMMITTER_NAME=Sam", "GIT_COMMITTER_EMAIL=sam@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func paneDiff(d *Daemon, id string) *pb.DiffInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p := d.panes[id]; p != nil {
		return p.snapshotInfo().Diff
	}
	return nil
}

func TestDiffPane(t *testing.T) {
	dir := shortDir(t)
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"), 0o644)
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "First")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n2\n"), 0o644)

	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	first := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, Pane: &pb.PaneSpec{Cwd: repo},
	}}}))
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Cwd: repo}}}})
	// Opened from the first tab: the diff's tab goes just after it.
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, AfterTabId: first, Pane: &pb.PaneSpec{Diff: &pb.DiffOpen{Repo: filepath.Join(repo, ".")}},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	order := slices.Clone(d.groups[g].TabIds)
	name := d.tabs[tabID].Name
	d.mu.Unlock()
	if order[1] != tabID || name != "Review" {
		t.Fatalf("tabs %v (diff %s, named %q)", order, tabID, name)
	}
	di := paneDiff(d, paneID)
	if di.Source.Kind != pb.DiffSource_KIND_UNCOMMITTED || di.Title != "Uncommitted changes" || di.Repo != repo {
		t.Fatalf("info %v", di)
	}

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiff{ReadDiff: &pb.ReadDiff{PaneId: paneID}}}).GetDiffContent()
	if len(r.Files) != 1 || r.Files[0].Path != "a.txt" || r.Generation != 1 || len(r.Commits) != 1 || r.DefaultBranch != "main" {
		t.Fatalf("content %v", r)
	}
	if di := paneDiff(d, paneID); di.Files != 1 || di.Added != 1 || di.Removed != 1 || di.Generation != 1 {
		t.Fatalf("counts %v", di)
	}
	ft := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiffFile{ReadDiffFile: &pb.ReadDiffFile{PaneId: paneID, Path: "a.txt", Generation: 1}}}).GetDiffFileText()
	if ft.OldText != "one\ntwo\n" || ft.NewText != "one\n2\n" {
		t.Fatalf("sides %q %q", ft.OldText, ft.NewText)
	}

	// The working tree moves on: the pane says so, and a refresh catches up.
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("new\n"), 0o644)
	waitFor(t, "the diff to go stale", func() bool { return paneDiff(d, paneID).GetStale() })
	r = call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiff{ReadDiff: &pb.ReadDiff{PaneId: paneID, Refresh: true}}}).GetDiffContent()
	if len(r.Files) != 2 || r.Generation != 2 {
		t.Fatalf("refreshed %v", r)
	}
	if di := paneDiff(d, paneID); di.Stale || di.Files != 2 {
		t.Fatalf("after refresh %v", di)
	}
	if _, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadDiffFile{ReadDiffFile: &pb.ReadDiffFile{PaneId: paneID, Path: "a.txt", Generation: 1}}}); err == nil {
		t.Error("reading a file of an earlier generation")
	}

	// Another source: the commit itself.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_DiffCommand{DiffCommand: &pb.DiffCommand{
		PaneId: paneID, Action: pb.DiffCommand_ACTION_SET_SOURCE, Source: &pb.DiffSource{Kind: pb.DiffSource_KIND_COMMIT, Rev: "HEAD"},
	}}})
	if di := paneDiff(d, paneID); di.Source.Kind != pb.DiffSource_KIND_COMMIT || di.Title[8:] != "First" || di.Files != 1 || di.Added != 2 {
		t.Fatalf("commit %v", di)
	}
	if _, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_DiffCommand{DiffCommand: &pb.DiffCommand{
		PaneId: paneID, Action: pb.DiffCommand_ACTION_SET_SOURCE, Source: &pb.DiffSource{Kind: pb.DiffSource_KIND_COMMIT, Rev: "nope"},
	}}}); err == nil {
		t.Error("a commit that isn't there was taken")
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_DiffCommand{DiffCommand: &pb.DiffCommand{
		PaneId: paneID, Action: pb.DiffCommand_ACTION_SET_VIEW, View: &pb.DiffView{Style: "unified", Whitespace: "sideways"},
	}}})
	if v := paneDiff(d, paneID).View; v.Style != "unified" || v.Whitespace != "" {
		t.Fatalf("view %v", v)
	}

	// Not a repository: refused when opening.
	if _, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, Pane: &pb.PaneSpec{Diff: &pb.DiffOpen{Repo: t.TempDir()}},
	}}}); err == nil {
		t.Error("a diff pane outside a repository")
	}
}
