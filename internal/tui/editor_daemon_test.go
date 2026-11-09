package tui

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	"github.com/alexjsp/hammock/internal/daemon"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
	"github.com/alexjsp/hammock/internal/textbuf"
)

// An editor pane against a real daemon: it attaches, edits while another
// client edits too (one of them is refused and rebases), saves, and both
// end with the same text, which is the file's.
func TestEditorAgainstDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "hmk-tui") // short: it holds a socket
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	folder := t.TempDir()
	file := filepath.Join(folder, "notes.txt")
	os.WriteFile(file, []byte("one\ntwo\n"), 0o644)

	d, err := daemon.New(daemon.Config{Dir: dir, HolderPath: "/nonexistent", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	go d.Serve()
	t.Cleanup(d.Shutdown)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if c, err := net.Dial("unix", daemon.SocketPath(dir)); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no daemon")
		}
	}
	dial := func() *client.Client {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := client.Dial(ctx, daemon.SocketPath(dir), "tui", t.Name())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	call := func(c *client.Client, m *pb.ClientMessage) *pb.ServerMessage {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, err := c.Call(ctx, m)
		if err != nil {
			t.Fatalf("%T: %v", m.Msg, err)
		}
		return r
	}

	c := dial()
	a := &App{c: c, anim: newSprings(), th: newTheme(RGB{0, 0, 0}, RGB{255, 255, 255}), scr: NewScreen(80, 24),
		tabs: map[string]*pb.Tab{}, panes: map[string]*pb.Pane{}, terms: map[string]*paneTerm{}, sizes: map[string][2]int{},
		editors: map[string]*editorView{}, paneRects: map[string]rect{}, actions: make(chan func(), 64)}
	a.apply(call(c, &pb.ClientMessage{Msg: &pb.ClientMessage_Subscribe{Subscribe: &pb.Subscribe{}}}).GetState())
	gid := client.CreatedID(call(c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "Ed"}}}))
	call(c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: gid, Folder: folder}}})
	tid := client.CreatedID(call(c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{
		GroupId: gid, Pane: &pb.PaneSpec{Editor: &pb.EditorOpen{Path: "notes.txt"}},
	}}}))

	// pump runs the TUI's loop (the daemon's messages, then what calls
	// hand back) until done says so.
	pump := func(what string, done func() bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for !done() {
			select {
			case ev := <-c.Events:
				a.handle(ev)
			case f := <-a.actions:
				f()
			case <-deadline:
				if v := a.editors[a.focus]; v != nil {
					t.Logf("text %q v%d unconfirmed %d in flight %d waiting %v save %v", v.doc.text, v.doc.version, len(v.doc.unconfirmed), v.doc.inFlight, v.doc.waiting, v.save)
				}
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	var pid string
	pump("the tab", func() bool {
		if tb := a.tabs[tid]; tb != nil && isEditor(a.panes[tb.FocusedPaneId]) {
			pid = tb.FocusedPaneId
			return true
		}
		return false
	})
	a.selTab, a.focus = tid, pid
	a.syncEditors(map[string]rect{pid: {0, 0, 80, 24}})
	v := a.editors[pid]
	pump("the snapshot", func() bool { return v.loaded })
	if got := v.doc.text.String(); got != "one\ntwo\n" {
		t.Fatalf("snapshot: %q", got)
	}

	// Another client gets in first: it inserts at the start at version 0
	// while the TUI types at the end of line one, also at version 0.
	other := dial()
	call(other, &pb.ClientMessage{Msg: &pb.ClientMessage_AttachPane{AttachPane: &pb.AttachPane{PaneId: pid}}})
	cs, _ := textbuf.Build(8, []textbuf.Replace{{From: 0, To: 0, Text: textbuf.FromString("zero\n")}})
	r := call(other, &pb.ClientMessage{Msg: &pb.ClientMessage_EditorPush{EditorPush: &pb.EditorPush{
		PaneId: pid, Version: 0, Updates: []*pb.EditorChange{{Changes: cs.JSON(), ClientId: "other"}},
	}}})
	if !r.GetEditorPushReply().GetAccepted() {
		t.Fatal("the other client's push was refused")
	}
	v.doc.moveTo(3, false)
	a.editorInput(v, []byte("!!"))            // pushed at version 0: refused, then rebased
	a.editorInput(v, []byte("\x1b[1;2D\x13")) // shift-left, then ⌃S
	pump("the edits to settle", func() bool { return v.doc.confirmed() && v.doc.version == 3 && v.save == nil })
	if got := v.doc.text.String(); got != "zero\none!!\ntwo\n" {
		t.Fatalf("after rebasing: %q", got)
	}
	if v.doc.anchor != 10 || v.doc.head != 9 {
		t.Fatalf("selection %d..%d", v.doc.anchor, v.doc.head)
	}
	pump("the save", func() bool {
		b, _ := os.ReadFile(file)
		return string(b) == "zero\none!!\ntwo\n"
	})
	pump("the pane to be clean", func() bool { return !a.panes[pid].GetEditor().GetDirty() })

	// Undo goes to the daemon like any edit.
	a.editorInput(v, []byte("\x1a"))
	pump("the undo", func() bool { return v.doc.confirmed() && v.doc.text.String() == "zero\none\ntwo\n" })
	ru := call(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadEditor{ReadEditor: &pb.ReadEditor{PaneId: pid}}}).GetEditorUpdate()
	if ru.Text != "zero\none\ntwo\n" {
		t.Fatalf("daemon after undo: %q", ru.Text)
	}
	pump("the dirty mark", func() bool { return a.panes[pid].GetEditor().GetDirty() })

	// An external editor writing the file: pushed and saved.
	t.Setenv("VISUAL", "sed -i.bak s/two/TWO/")
	a.out = io.Discard
	a.startExternal(v)
	pump("the external edit", func() bool {
		b, _ := os.ReadFile(file)
		return a.ext == nil && string(b) == "zero\none\nTWO\n"
	})
	if got := v.doc.text.String(); got != "zero\none\nTWO\n" {
		t.Fatalf("after the external editor: %q", got)
	}

	// The selection is reported once it settles.
	v.selAt = time.Now().Add(-time.Second)
	a.editorTick()
	pump("the selection", func() bool {
		ru := call(c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReadEditor{ReadEditor: &pb.ReadEditor{PaneId: pid}}}).GetEditorUpdate()
		return ru.Selection != nil && int(ru.Selection.Head) == v.doc.head
	})
}
