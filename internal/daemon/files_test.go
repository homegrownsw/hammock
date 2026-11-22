package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func paneFiles(d *Daemon, id string) *pb.FilesInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p := d.panes[id]; p != nil {
		return p.snapshotInfo().Files
	}
	return nil
}

func entryNames(l *pb.DirListing) []string {
	var out []string
	for _, e := range l.Entries {
		out = append(out, e.Name)
	}
	return out
}

func entry(l *pb.DirListing, name string) *pb.DirEntry {
	for _, e := range l.Entries {
		if e.Name == name {
			return e
		}
	}
	return nil
}

func TestFilesPane(t *testing.T) {
	dir := shortDir(t)
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(repo, "src", "deep"), 0o755)
	os.MkdirAll(filepath.Join(repo, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules/\n*.log\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "file10.txt"), []byte("ten\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "file2.txt"), []byte("two\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "src", "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "src", "deep", "b.go"), []byte("package b\n"), 0o644)
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "First")
	os.WriteFile(filepath.Join(repo, "src", "deep", "b.go"), []byte("package b // changed\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "debug.log"), []byte("log\n"), 0o644)

	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	// No terminal: its folder would move the Group's under the test.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: g, Folder: repo}}})

	// No root: the Group's folder, followed.
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, Pane: &pb.PaneSpec{Files: &pb.FilesOpen{Reveal: "src/deep/b.go"}},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()
	f := paneFiles(d, paneID)
	if f.Root != repo || !f.FollowGroup || f.Repo != repo {
		t.Fatalf("info %v", f)
	}
	// Revealing opened the folders down to it, and selected it.
	if !slices.Equal(f.Expanded, []string{filepath.Join(repo, "src"), filepath.Join(repo, "src", "deep")}) && !slices.Equal(f.Expanded, []string{filepath.Join(repo, "src", "deep"), filepath.Join(repo, "src")}) {
		t.Fatalf("expanded %v", f.Expanded)
	}
	if !slices.Equal(f.Selected, []string{filepath.Join(repo, "src", "deep", "b.go")}) || f.RevealSeq != 1 {
		t.Fatalf("selected %v seq %d", f.Selected, f.RevealSeq)
	}

	list := func(path string) *pb.DirListing {
		t.Helper()
		return call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListDir{ListDir: &pb.ListDir{PaneId: paneID, Path: path}}}).GetDirListing()
	}
	l := list("")
	// Folders first, then by name, numbers by value.
	want := []string{".git", "node_modules", "src", ".gitignore", "debug.log", "file2.txt", "file10.txt", "new.txt"}
	if !slices.Equal(entryNames(l), want) {
		t.Fatalf("entries %v, want %v", entryNames(l), want)
	}
	if e := entry(l, "src"); !e.Dir || e.Git != "M" {
		t.Fatalf("src %v", e)
	}
	if e := entry(l, "new.txt"); e.Git != "U" {
		t.Fatalf("new.txt %v", e)
	}
	if e := entry(l, "node_modules"); !e.Ignored {
		t.Fatalf("node_modules %v", e)
	}
	if e := entry(l, "debug.log"); !e.Ignored {
		t.Fatalf("debug.log %v", e)
	}
	if e := entry(l, "file2.txt"); e.Git != "" || e.Ignored || e.Size != 4 {
		t.Fatalf("file2.txt %v", e)
	}
	// Inside an ignored folder: everything is.
	if e := entry(list("node_modules"), "x"); e == nil || !e.Ignored {
		t.Fatalf("node_modules/x %v", e)
	}
	if e := entry(list(filepath.Join(repo, "src", "deep")), "b.go"); e.Git != "M" {
		t.Fatalf("b.go %v", e)
	}

	cmd := func(m *pb.FilesCommand) error {
		m.PaneId = paneID
		_, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_FilesCommand{FilesCommand: m}})
		return err
	}
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_COLLAPSE, Paths: []string{filepath.Join(repo, "src")}}); err != nil {
		t.Fatal(err)
	}
	// Closing a folder keeps those inside it open, for when it's opened again.
	if f := paneFiles(d, paneID); !slices.Equal(f.Expanded, []string{filepath.Join(repo, "src", "deep")}) {
		t.Fatalf("expanded %v", f.Expanded)
	}
	if got := visibleDirs(paneFiles(d, paneID)); !slices.Equal(got, []string{repo}) {
		t.Fatalf("visible %v", got)
	}
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_SHOW_HIDDEN, On: true}); err != nil {
		t.Fatal(err)
	}
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_SELECT, Paths: []string{"file2.txt", "new.txt"}}); err != nil {
		t.Fatal(err)
	}
	if f := paneFiles(d, paneID); !f.ShowHidden || !slices.Equal(f.Selected, []string{filepath.Join(repo, "file2.txt"), filepath.Join(repo, "new.txt")}) {
		t.Fatalf("info %v", f)
	}

	// Choosing a folder stops following the Group's; "" goes back.
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_SET_ROOT, Path: "src"}); err != nil {
		t.Fatal(err)
	}
	if f := paneFiles(d, paneID); f.Root != filepath.Join(repo, "src") || f.FollowGroup || !slices.Equal(f.Expanded, []string{filepath.Join(repo, "src", "deep")}) || len(f.Selected) != 0 {
		t.Fatalf("info %v", f)
	}
	d.mu.Lock()
	title := d.panes[paneID].snapshotInfo().Title
	d.mu.Unlock()
	if title != "src" {
		t.Fatalf("title %q", title)
	}
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_SET_ROOT}); err != nil {
		t.Fatal(err)
	}
	if f := paneFiles(d, paneID); f.Root != repo || !f.FollowGroup {
		t.Fatalf("info %v", f)
	}

	// The Group moving into a folder it shows leaves it be; out of it, moves it.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: g, Folder: filepath.Join(repo, "src")}}})
	if f := paneFiles(d, paneID); f.Root != repo {
		t.Fatalf("root %s", f.Root)
	}
	other := t.TempDir()
	if r, err := filepath.EvalSymlinks(other); err == nil {
		other = r
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: g, Folder: other}}})
	if f := paneFiles(d, paneID); f.Root != other || f.Repo != "" || len(f.Expanded) != 0 {
		t.Fatalf("info %v", f)
	}

	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_SET_ROOT, Path: filepath.Join(repo, "nope")}); err == nil {
		t.Fatal("a root that isn't there")
	}
	if l := list(filepath.Join(other, "gone")); l.Error == "" {
		t.Fatalf("listing %v", l)
	}
}

func TestFilesPaneWatch(t *testing.T) {
	dir := shortDir(t)
	repo := t.TempDir()
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(repo, "src"), 0o755)
	os.WriteFile(filepath.Join(repo, "src", "a.go"), []byte("package a\n"), 0o644)
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "First")

	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, Pane: &pb.PaneSpec{Files: &pb.FilesOpen{Root: repo}},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()
	if f := paneFiles(d, paneID); f.FollowGroup {
		t.Fatalf("info %v", f)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ListDir{ListDir: &pb.ListDir{PaneId: paneID}}})

	changed := func(dir string) {
		t.Helper()
		waitEvent(t, c, func(ev *pb.ServerMessage) bool {
			fc := ev.GetFilesChanged()
			return fc != nil && fc.PaneId == paneID && slices.Contains(fc.Paths, dir)
		})
	}
	// A new file in the root.
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b\n"), 0o644)
	changed(repo)

	// src isn't open: a change in it is only the root's, by its git status.
	os.WriteFile(filepath.Join(repo, "src", "a.go"), []byte("package a // changed\n"), 0o644)
	changed(repo)

	// Opened, it's watched too.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_FilesCommand{FilesCommand: &pb.FilesCommand{
		PaneId: paneID, Action: pb.FilesCommand_ACTION_EXPAND, Paths: []string{filepath.Join(repo, "src")},
	}}})
	os.WriteFile(filepath.Join(repo, "src", "c.go"), []byte("package a\n"), 0o644)
	changed(filepath.Join(repo, "src"))

	// Committing changes only git's status: both folders are told.
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "-q", "-m", "Second")
	changed(filepath.Join(repo, "src"))
}

func TestGitStatusParse(t *testing.T) {
	g := &gitStatus{codes: map[string]string{}}
	g.parse(" M a/b/c.go\x00?? new/\x00R  to.go\x00from.go\x00UU x/conflict.go\x00A  a/added.go\x00")
	cases := map[string]string{
		"a/b/c.go": "M", "a/b": "M", "a": "M", "new": "U", "new/inside.txt": "U",
		"to.go": "R", "from.go": "", "x": "C", "a/added.go": "A", "other": "",
	}
	for p, want := range cases {
		if got := g.status(p, false); got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
	o := &gitStatus{codes: map[string]string{}}
	o.parse(" M a/b/c.go\x00")
	if g.sameIn(o, "/r", "/r/a/b") != true || g.sameIn(o, "/r", "/r") {
		t.Error("sameIn")
	}
}

func TestNaturalLess(t *testing.T) {
	names := []string{"file10", "File2", "file1", "a", "B", "file02b"}
	slices.SortFunc(names, func(a, b string) int {
		if naturalLess(a, b) {
			return -1
		}
		if naturalLess(b, a) {
			return 1
		}
		return 0
	})
	want := []string{"a", "B", "file1", "File2", "file02b", "file10"}
	if !slices.Equal(names, want) {
		t.Fatalf("%v, want %v", names, want)
	}
}
