package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

func TestEditorRename(t *testing.T) {
	dir := shortDir(t)
	folder, _ := filepath.EvalSymlinks(t.TempDir())
	file := filepath.Join(folder, "a.txt")
	os.WriteFile(file, []byte("one\n"), 0o644)
	os.WriteFile(filepath.Join(folder, "taken.txt"), []byte("x\n"), 0o644)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	_, paneID := newEditorTab(t, d, c, folder, file)
	rename := func(to string) error {
		_, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{
			PaneId: paneID, Action: pb.EditorCommand_ACTION_RENAME, Path: to}}})
		return err
	}

	// Unsaved edits go with it, still unsaved.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorPush{EditorPush: &pb.EditorPush{
		PaneId: paneID, Version: 0, Updates: []*pb.EditorChange{{Changes: `[4,[0,"two\n"]]`}},
	}}})
	if err := rename("taken.txt"); err == nil {
		t.Fatal("renamed over another file")
	}
	if err := rename("b.md"); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(folder, "b.md")
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("the old name's still there")
	}
	if data, _ := os.ReadFile(to); string(data) != "one\n" {
		t.Fatalf("moved file: %q", data)
	}
	ed := paneEditor(d, paneID)
	if ed.GetPath() != to || ed.GetName() != "b.md" || !ed.GetDirty() || ed.GetMissing() {
		t.Fatalf("after rename: %+v", ed)
	}
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadEditor{ReadEditor: &pb.ReadEditor{PaneId: paneID}}})
	if r.GetEditorUpdate().Text != "one\ntwo\n" {
		t.Fatalf("buffer: %q", r.GetEditorUpdate().Text)
	}
	// Into a subfolder, relative to the file's own.
	os.Mkdir(filepath.Join(folder, "sub"), 0o755)
	if err := rename("sub/c.md"); err != nil {
		t.Fatal(err)
	}
	if paneEditor(d, paneID).GetPath() != filepath.Join(folder, "sub", "c.md") {
		t.Fatalf("after moving: %+v", paneEditor(d, paneID))
	}
}

func TestEditorFollowsMoves(t *testing.T) {
	dir := shortDir(t)
	folder, _ := filepath.EvalSymlinks(t.TempDir())
	file := filepath.Join(folder, "a.go")
	os.WriteFile(file, []byte("package a\n"), 0o644)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	_, paneID := newEditorTab(t, d, c, folder, file)
	path := func() string { return paneEditor(d, paneID).GetPath() }

	// Renamed in its folder (the Finder, git mv).
	b := filepath.Join(folder, "b.go")
	if err := os.Rename(file, b); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "following the rename", func() bool { return path() == b })
	if paneEditor(d, paneID).GetMissing() {
		t.Fatal("missing after a rename")
	}

	// Moved to another folder: followed where the system can say where.
	if runtime.GOOS == "darwin" {
		other := filepath.Join(folder, "pkg")
		os.Mkdir(other, 0o755)
		moved := filepath.Join(other, "b.go")
		if err := os.Rename(b, moved); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "following the move", func() bool { return path() == moved })
		b = moved
	}

	// Moved to the Trash: deleted, not followed.
	trash := filepath.Join(folder, ".Trash")
	os.Mkdir(trash, 0o755)
	if err := os.Rename(b, filepath.Join(trash, "b.go")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "missing", func() bool { return paneEditor(d, paneID).GetMissing() })
	if path() != b {
		t.Fatalf("followed into the Trash: %s", path())
	}
}
