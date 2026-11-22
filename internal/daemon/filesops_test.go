package daemon

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// fakeTrash puts moveToTrash's Trash in a folder of the test's (the real one
// is yours).
func fakeTrash(t *testing.T) string {
	t.Helper()
	trash := t.TempDir()
	oldMove, oldRestore := moveToTrash, restoreFromTrash
	moveToTrash = func(p string) (string, error) {
		if !exists(p) {
			return "", os.ErrNotExist
		}
		to := freeName(filepath.Join(trash, filepath.Base(p)))
		return to, os.Rename(p, to)
	}
	restoreFromTrash = func(where, original string) error { return os.Rename(where, original) }
	t.Cleanup(func() { moveToTrash, restoreFromTrash = oldMove, oldRestore })
	return trash
}

func TestFilesOps(t *testing.T) {
	trash := fakeTrash(t)
	dir := shortDir(t)
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	os.MkdirAll(filepath.Join(root, "src", "deep"), 0o755)
	os.WriteFile(filepath.Join(root, "src", "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(root, "src", "deep", "b.go"), []byte("package b\n"), 0o644)
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hi\n"), 0o644)

	d := startDaemon(t, dir)
	c := dial(t, dir)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Work"}}}))
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: g, Pane: &pb.PaneSpec{Files: &pb.FilesOpen{Root: root}},
	}}}))
	d.mu.Lock()
	paneID := d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()
	cmd := func(m *pb.FilesCommand) error {
		m.PaneId = paneID
		_, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_FilesCommand{FilesCommand: m}})
		return err
	}
	must := func(m *pb.FilesCommand) {
		t.Helper()
		if err := cmd(m); err != nil {
			t.Fatalf("%v: %v", m.Action, err)
		}
	}
	at := func(rel string) string { return filepath.Join(root, rel) }
	sel := func(want ...string) {
		t.Helper()
		var full []string
		for _, w := range want {
			full = append(full, at(w))
		}
		if f := paneFiles(d, paneID); !slices.Equal(f.Selected, full) {
			t.Fatalf("selected %v, want %v", f.Selected, full)
		}
	}
	there := func(rel string, want bool) {
		t.Helper()
		if exists(at(rel)) != want {
			t.Fatalf("%s there: %v, want %v", rel, !want, want)
		}
	}

	// src/deep open, then src renamed: it stays open under its new name.
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_EXPAND, Paths: []string{at("src"), at("src/deep")}})
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_RENAME, Path: at("src"), Name: "lib"})
	there("lib/deep/b.go", true)
	sel("lib")
	if f := paneFiles(d, paneID); !slices.Equal(f.Expanded, []string{at("lib"), at("lib/deep")}) {
		t.Fatalf("expanded %v", f.Expanded)
	}
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_RENAME, Path: at("notes.txt"), Name: "lib"}); err == nil {
		t.Fatal("renamed onto a folder that's there")
	}
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_RENAME, Path: at("notes.txt"), Name: "a/b"}); err == nil {
		t.Fatal("a name with a / in it")
	}
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_UNDO})
	there("src/a.go", true)
	there("lib", false)
	sel("src")

	// Created, selected, its folder opened.
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_CREATE_FILE, Path: at("src/deep"), Name: "new.go"})
	there("src/deep/new.go", true)
	sel("src/deep/new.go")
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_CREATE_FOLDER, Name: "docs"})
	if st, err := os.Stat(at("docs")); err != nil || !st.IsDir() {
		t.Fatal("no docs folder")
	}
	// Undoing a creation trashes it.
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_UNDO})
	there("docs", false)
	if !exists(filepath.Join(trash, "docs")) {
		t.Fatal("docs not in the Trash")
	}

	// Moving two, one inside the other's folder: what's in a folder goes with it.
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_CREATE_FOLDER, Name: "dest"})
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_MOVE, Path: at("dest"), Paths: []string{at("src/deep"), at("src/deep/b.go"), at("notes.txt")}})
	there("dest/deep/b.go", true)
	there("dest/notes.txt", true)
	there("notes.txt", false)
	sel("dest/deep", "dest/notes.txt")
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_MOVE, Path: at("dest/deep"), Paths: []string{at("dest")}}); err == nil {
		t.Fatal("moved a folder into itself")
	}
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_UNDO})
	there("src/deep/b.go", true)
	there("notes.txt", true)

	// Copying into the same folder: a copy alongside, as the Finder names it.
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_COPY, Path: root, Paths: []string{at("notes.txt"), at("src")}})
	there("notes copy.txt", true)
	there("src copy/deep/b.go", true)
	if b, _ := os.ReadFile(at("notes copy.txt")); string(b) != "hi\n" {
		t.Fatalf("copy has %q", b)
	}
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_COPY, Path: root, Paths: []string{at("notes.txt")}})
	there("notes copy 2.txt", true)

	// Trashed, then put back.
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_TRASH, Paths: []string{at("src/a.go"), at("notes copy.txt")}})
	there("src/a.go", false)
	there("notes copy.txt", false)
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_UNDO})
	there("src/a.go", true)
	there("notes copy.txt", true)

	// Copied in from outside (a drop from the Finder): fine; trashing outside it isn't.
	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "dropped.txt"), []byte("x"), 0o644)
	must(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_COPY, Path: at("src"), Paths: []string{filepath.Join(out, "dropped.txt")}})
	there("src/dropped.txt", true)
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_TRASH, Paths: []string{out}}); err == nil || !strings.Contains(err.Error(), "isn't in this pane's folder") {
		t.Fatalf("trashed outside: %v", err)
	}
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_TRASH, Paths: []string{root}}); err == nil {
		t.Fatal("trashed the pane's own folder")
	}
	// Undone as far as it goes.
	for cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_UNDO}) == nil {
	}
	if err := cmd(&pb.FilesCommand{Action: pb.FilesCommand_ACTION_UNDO}); err == nil || err.Error() != "nothing to undo" {
		t.Fatalf("undo: %v", err)
	}
}

func TestFreeName(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "a copy.txt"), nil, 0o644)
	os.Mkdir(filepath.Join(dir, "v1.2"), 0o755)
	if got := filepath.Base(freeName(filepath.Join(dir, "a.txt"))); got != "a copy 2.txt" {
		t.Fatal(got)
	}
	if got := filepath.Base(freeName(filepath.Join(dir, "v1.2"))); got != "v1.2 copy" {
		t.Fatal(got)
	}
	if got := filepath.Base(freeName(filepath.Join(dir, "b"))); got != "b" {
		t.Fatal(got)
	}
}
