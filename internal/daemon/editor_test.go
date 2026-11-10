package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"github.com/homegrownsw/hammock/internal/textbuf"
)

func editorCall(c *client.Client, m *pb.ClientMessage) (*pb.ServerMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Call(ctx, m)
}

// newEditorTab makes a Group in folder and an editor tab on path.
func newEditorTab(t *testing.T, d *Daemon, c *client.Client, folder, path string) (groupID, paneID string) {
	t.Helper()
	groupID = client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Ed"}}}))
	d.mu.Lock()
	d.groups[groupID].Folder = folder
	d.mu.Unlock()
	tabID := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: groupID, Pane: &pb.PaneSpec{Editor: &pb.EditorOpen{Path: path, Line: 2}},
	}}}))
	d.mu.Lock()
	paneID = d.tabs[tabID].FocusedPaneId
	d.mu.Unlock()
	return groupID, paneID
}

func editorSnapshot(t *testing.T, c *client.Client, paneID string) *pb.EditorUpdate {
	t.Helper()
	return waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		u := ev.GetEditorUpdate()
		return u != nil && u.PaneId == paneID && u.Snapshot
	}).GetEditorUpdate()
}

func paneEditor(d *Daemon, id string) *pb.EditorInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p := d.panes[id]; p != nil {
		return p.snapshotInfo().Editor
	}
	return nil
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// waitUnlocked waits for a shut-down daemon's state dir to be free: under
// -race a child process forked as it ran can briefly hold its lock.
func waitUnlocked(t *testing.T, dir string) {
	t.Helper()
	waitFor(t, "the state dir's lock", func() bool {
		f, err := lockDir(dir)
		if err == nil {
			f.Close()
		}
		return err == nil
	})
}

func TestEditorEditSaveAndShare(t *testing.T) {
	dir := shortDir(t)
	folder := t.TempDir()
	file := filepath.Join(folder, "notes.txt")
	os.WriteFile(file, []byte("one\r\ntwo\r\n"), 0o640)
	d := startDaemon(t, dir)
	a, b := dial(t, dir), dial(t, dir)
	call(t, a, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}})
	_, paneID := newEditorTab(t, d, a, folder, "notes.txt")

	call(t, a, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	snap := editorSnapshot(t, a, paneID)
	if snap.Text != "one\ntwo\n" || snap.Version != 0 {
		t.Fatalf("snapshot %q v%d", snap.Text, snap.Version)
	}
	if s := snap.Selection; s == nil || s.Anchor != 4 {
		t.Fatalf("opened at line 2: selection %v", s)
	}
	call(t, b, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	editorSnapshot(t, b, paneID)

	// a types "ONE " at the start.
	push := func(c *client.Client, v uint64, changes string) bool {
		r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorPush{EditorPush: &pb.EditorPush{
			PaneId: paneID, Version: v, Updates: []*pb.EditorChange{{Changes: changes, ClientId: "x"}},
		}}})
		return r.GetEditorPushReply().GetAccepted()
	}
	if !push(a, 0, `[[0,"ONE "],8]`) {
		t.Fatal("push at the current version refused")
	}
	// b, behind, is refused, and gets a's update.
	if push(b, 0, `[8,[0,"three"]]`) {
		t.Fatal("stale push taken")
	}
	waitEvent(t, b, func(ev *pb.ServerMessage) bool {
		u := ev.GetEditorUpdate()
		return u != nil && u.Version == 1 && len(u.Updates) == 1
	})
	if !push(b, 1, `[12,[0,"three"]]`) {
		t.Fatal("rebased push refused")
	}
	waitFor(t, "dirty", func() bool { return paneEditor(d, paneID).GetDirty() })

	call(t, a, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{PaneId: paneID, Action: pb.EditorCommand_ACTION_SAVE}}})
	data, _ := os.ReadFile(file)
	if string(data) != "ONE one\r\ntwo\r\nthree" {
		t.Fatalf("saved %q", data)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", st.Mode())
	}
	waitFor(t, "clean", func() bool { return !paneEditor(d, paneID).GetDirty() })

	// A second pane on the same file shares the buffer.
	d.mu.Lock()
	tab := d.panes[paneID].tabID()
	d.mu.Unlock()
	_ = tab
	p2 := client.CreatedID(call(t, a, &pb.ClientMessage{Msg: &pb.ClientMessage_CreatePane{CreatePane: &pb.CreatePane{
		SplitPaneId: paneID, Spec: &pb.PaneSpec{Editor: &pb.EditorOpen{Path: file}},
	}}}))
	r := call(t, a, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadEditor{ReadEditor: &pb.ReadEditor{PaneId: p2}}})
	if r.GetEditorUpdate().Text != "ONE one\ntwo\nthree" || r.GetEditorUpdate().Version != 2 {
		t.Fatalf("second pane: %q v%d", r.GetEditorUpdate().Text, r.GetEditorUpdate().Version)
	}
}

func TestEditorDiskChanges(t *testing.T) {
	dir := shortDir(t)
	folder := t.TempDir()
	file := filepath.Join(folder, "a.go")
	os.WriteFile(file, []byte("package a\n"), 0o644)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	_, paneID := newEditorTab(t, d, c, folder, file)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	editorSnapshot(t, c, paneID)

	// Clean: an agent's change reloads it, as an update.
	time.Sleep(20 * time.Millisecond)
	os.WriteFile(file, []byte("package a\n\nfunc F() {}\n"), 0o644)
	waitEvent(t, c, func(ev *pb.ServerMessage) bool {
		u := ev.GetEditorUpdate()
		return u != nil && len(u.Updates) == 1 && u.Updates[0].ClientId == "disk"
	})

	// Dirty: a conflict, and saving needs force.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorPush{EditorPush: &pb.EditorPush{
		PaneId: paneID, Version: 1, Updates: []*pb.EditorChange{{Changes: `[23,[0,"// mine\n"]]`}},
	}}})
	time.Sleep(20 * time.Millisecond)
	os.WriteFile(file, []byte("package b\n"), 0o644)
	waitFor(t, "conflict", func() bool { return paneEditor(d, paneID).GetConflict() })
	if _, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{PaneId: paneID, Action: pb.EditorCommand_ACTION_SAVE}}}); err == nil {
		t.Fatal("saved over a conflict")
	}
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadEditor{ReadEditor: &pb.ReadEditor{PaneId: paneID, Disk: true}}})
	if r.GetEditorUpdate().DiskText != "package b\n" || !strings.Contains(r.GetEditorUpdate().Text, "// mine") {
		t.Fatalf("diff texts: %+v", r.GetEditorUpdate())
	}
	// Reload takes the disk's.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{PaneId: paneID, Action: pb.EditorCommand_ACTION_RELOAD}}})
	r = call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadEditor{ReadEditor: &pb.ReadEditor{PaneId: paneID}}})
	if r.GetEditorUpdate().Text != "package b\n" || paneEditor(d, paneID).GetDirty() || paneEditor(d, paneID).GetConflict() {
		t.Fatalf("after reload: %q %+v", r.GetEditorUpdate().Text, paneEditor(d, paneID))
	}
}

func TestEditorReadOnly(t *testing.T) {
	dir := shortDir(t)
	folder := t.TempDir()
	file := filepath.Join(folder, "locked.txt")
	os.WriteFile(file, []byte("keep\n"), 0o444)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	_, paneID := newEditorTab(t, d, c, folder, file)
	if !paneEditor(d, paneID).GetReadOnly() {
		t.Fatal("not marked read-only")
	}
	// No edits, and no saving over it.
	if _, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorPush{EditorPush: &pb.EditorPush{
		PaneId: paneID, Version: 0, Updates: []*pb.EditorChange{{Changes: `[[0,"x"],5]`}},
	}}}); err == nil {
		t.Fatal("edited a read-only file")
	}
	if _, err := editorCall(c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{PaneId: paneID, Action: pb.EditorCommand_ACTION_SAVE}}}); err == nil {
		t.Fatal("saved over a read-only file")
	}
	if data, _ := os.ReadFile(file); string(data) != "keep\n" {
		t.Fatalf("file now %q", data)
	}
	// Save As makes a copy you can edit.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{PaneId: paneID, Action: pb.EditorCommand_ACTION_SAVE, Path: "copy.txt"}}})
	if data, _ := os.ReadFile(filepath.Join(folder, "copy.txt")); string(data) != "keep\n" {
		t.Fatalf("copy %q", data)
	}
	if paneEditor(d, paneID).GetReadOnly() {
		t.Fatal("the copy is read-only")
	}
}

func TestEditorUnsavedSurvivesRestart(t *testing.T) {
	dir := shortDir(t)
	folder := t.TempDir()
	d := startDaemon(t, dir)
	c := dial(t, dir)
	_, paneID := newEditorTab(t, d, c, folder, "")
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorPush{EditorPush: &pb.EditorPush{
		PaneId: paneID, Version: 0, Updates: []*pb.EditorChange{{Changes: `[[0,"draft"]]`}},
	}}})
	if e := paneEditor(d, paneID); !e.Dirty || e.Name != "Untitled" {
		t.Fatalf("untitled: %+v", e)
	}
	c.Close()
	d.Shutdown()
	waitUnlocked(t, dir)

	d = startDaemon(t, dir)
	c = dial(t, dir)
	r := call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadEditor{ReadEditor: &pb.ReadEditor{PaneId: paneID}}})
	if r.GetEditorUpdate().Text != "draft" || !paneEditor(d, paneID).GetDirty() {
		t.Fatalf("after restart: %q", r.GetEditorUpdate().Text)
	}
	// Save As names it.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{PaneId: paneID, Action: pb.EditorCommand_ACTION_SAVE, Path: "draft.md"}}})
	if data, _ := os.ReadFile(filepath.Join(folder, "draft.md")); string(data) != "draft" {
		t.Fatalf("saved as: %q", data)
	}
	if e := paneEditor(d, paneID); e.Name != "draft.md" || e.Dirty {
		t.Fatalf("after save as: %+v", e)
	}
	// A kept buffer no pane has (a crash between closing and forgetting it)
	// is dropped on restart, not given to the next pane on that file.
	d.persist(d.store.PutRecord(editorRecord, filepath.Join(folder, "gone.txt"), &pb.EditorUpdate{Text: "stale"}))
	c.Close()
	d.Shutdown()
	waitUnlocked(t, dir)

	d = startDaemon(t, dir)
	c = dial(t, dir)
	if e := paneEditor(d, paneID); e.GetPath() == "" || filepath.Base(e.GetPath()) != "draft.md" || e.Dirty {
		t.Fatalf("saved as, after restart: %+v", e)
	}
	if recs, _ := d.store.Records(editorRecord); len(recs) != 0 {
		t.Fatalf("records left: %d", len(recs))
	}
}

func TestEditorRefusesBinaryAndLarge(t *testing.T) {
	folder := t.TempDir()
	bin := filepath.Join(folder, "x.bin")
	os.WriteFile(bin, []byte{0x7f, 'E', 'L', 'F', 0, 1}, 0o644)
	if _, _, err := readTextFile(bin, false); err == nil || !strings.Contains(err.Error(), "isn't a text file") {
		t.Fatalf("binary: %v", err)
	}
	big := filepath.Join(folder, "big.log")
	os.WriteFile(big, []byte(strings.Repeat("x", editorLargeFile+1)), 0o644)
	if _, _, err := readTextFile(big, false); err == nil || !strings.HasPrefix(err.Error(), errLargeFile) {
		t.Fatalf("large: %v", err)
	}
	if _, _, err := readTextFile(big, true); err != nil {
		t.Fatalf("large, asked: %v", err)
	}
	// A pipe would block the read (under d.mu) until something wrote to it.
	fifo := filepath.Join(folder, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTextFile(fifo, true); err == nil || !strings.Contains(err.Error(), "isn't a regular file") {
		t.Fatalf("fifo: %v", err)
	}
}

func TestEditorSelectAndAgents(t *testing.T) {
	dir := shortDir(t)
	folder := t.TempDir()
	os.WriteFile(filepath.Join(folder, "f.txt"), []byte("a\nbb\nccc\n"), 0o644)
	d := startDaemon(t, dir)
	c := dial(t, dir)
	_, paneID := newEditorTab(t, d, c, folder, "f.txt")
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: paneID}}})
	editorSnapshot(t, c, paneID)
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorCommand{EditorCommand: &pb.EditorCommand{
		PaneId: paneID, Action: pb.EditorCommand_ACTION_SELECT, Line: 2, EndLine: 3,
	}}})
	u := waitEvent(t, c, func(ev *pb.ServerMessage) bool { return ev.GetEditorUpdate().GetSelection() != nil }).GetEditorUpdate()
	if u.Selection.Anchor != 2 || u.Selection.Head != 8 {
		t.Fatalf("selection %v", u.Selection)
	}
}

func TestListFilesRanks(t *testing.T) {
	paths := []string{"internal/daemon/editor.go", "apple/Sources/HammockMac/EditorPane.swift", "README.md", "internal/textbuf/changeset.go", "docs/editors.md"}
	got := rankFiles(paths, "edpane", 10)
	if len(got) == 0 || got[0] != "apple/Sources/HammockMac/EditorPane.swift" {
		t.Fatalf("edpane: %v", got)
	}
	got = rankFiles(paths, "editor", 10)
	if got[0] != "internal/daemon/editor.go" {
		t.Fatalf("editor: %v", got)
	}
	if len(rankFiles(paths, "zzz", 10)) != 0 {
		t.Fatal("matched nothing-alike")
	}
	folder := t.TempDir()
	os.MkdirAll(filepath.Join(folder, "node_modules", "x"), 0o755)
	os.MkdirAll(filepath.Join(folder, "src"), 0o755)
	os.WriteFile(filepath.Join(folder, "node_modules", "x", "i.js"), nil, 0o644)
	os.WriteFile(filepath.Join(folder, "src", "main.go"), nil, 0o644)
	list, _ := scanFiles(folder)
	if len(list) != 1 || list[0] != "src/main.go" {
		t.Fatalf("scan: %v", list)
	}
}

func TestEditorConfigAndIndent(t *testing.T) {
	folder := t.TempDir()
	os.WriteFile(filepath.Join(folder, ".editorconfig"), []byte("root = true\n[*]\nindent_style = space\nindent_size = 4\n[*.{go,mk}]\nindent_style = tab\n"), 0o644)
	if w, s := indentFor(filepath.Join(folder, "x.go"), nil); s != "tabs" || w != 4 {
		t.Fatalf("go: %d %s", w, s)
	}
	if w, s := indentFor(filepath.Join(folder, "x.py"), nil); s != "spaces" || w != 4 {
		t.Fatalf("py: %d %s", w, s)
	}
	if w, s := indentFor("", textbuf.FromString("a:\n  b:\n    c\n  d\n")); s != "spaces" || w != 2 {
		t.Fatalf("detected: %d %s", w, s)
	}
}
