package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// TestGroupFolder: a Group remembers its last focused terminal's
// folder, keeps it once its last tab has closed, opens new tabs there, and
// takes one set by hand (absolute, existing directories only).
func TestGroupFolder(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	work, _ := filepath.EvalSymlinks(t.TempDir())
	other, _ := filepath.EvalSymlinks(t.TempDir())
	folder := func(g string) string {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.groups[g].Folder
	}

	g := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: "g"}}}))
	tb := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}, Cwd: work}}}}))
	if f := folder(g); f != work {
		t.Fatalf("folder after its first tab = %q, want %q", f, work)
	}

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tb}}})
	d.mu.Lock()
	kept, tabs := d.groups[g] != nil, len(d.groups[g].GetTabIds())
	d.mu.Unlock()
	if !kept || tabs != 0 {
		t.Fatalf("after closing its last tab: kept=%v tabs=%d", kept, tabs)
	}
	if f := folder(g); f != work {
		t.Fatalf("folder after its last tab closed = %q, want %q", f, work)
	}
	if l := func() string { d.mu.Lock(); defer d.mu.Unlock(); return d.groupLabel(g) }(); l != "g" {
		t.Fatalf("label = %q", l)
	}

	// A new tab with no folder of its own opens where the Group worked.
	tb = client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	p := d.panes[d.tabs[tb].FocusedPaneId]
	d.mu.Unlock()
	if cwd := p.cwd(); cwd != work {
		t.Fatalf("new tab's folder = %q, want %q", cwd, work)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tb}}})

	set := func(f string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupFolder{SetGroupFolder: &pb.SetGroupFolder{GroupId: g, Folder: f}}})
		return err
	}
	if err := set(other); err != nil {
		t.Fatal(err)
	}
	if f := folder(g); f != other {
		t.Fatalf("folder after SetGroupFolder = %q, want %q", f, other)
	}
	for _, bad := range []string{"relative/dir", filepath.Join(other, "missing"), ""} {
		if err := set(bad); err == nil {
			t.Errorf("SetGroupFolder(%q) was accepted", bad)
		}
	}
	if f := folder(g); f != other {
		t.Fatalf("a refused folder changed it to %q", f)
	}

	// Set by hand while a terminal is open in another folder: new tabs go
	// where it was set, until you work in a terminal there again.
	tb = client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}, Cwd: work}}}}))
	if f := folder(g); f != work {
		t.Fatalf("folder after a new tab in %q = %q", work, f)
	}
	if err := set(other); err != nil {
		t.Fatal(err)
	}
	tb2 := client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}}}}}))
	d.mu.Lock()
	p2 := d.panes[d.tabs[tb2].FocusedPaneId]
	d.mu.Unlock()
	if cwd := p2.cwd(); cwd != other {
		t.Fatalf("a new tab after setting the folder opened in %q, want %q", cwd, other)
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tb}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: tb2}}})

	// An unnamed Group is named after the folder it remembers.
	d.mu.Lock()
	d.groups[g].Name = ""
	label := d.groupLabel(g)
	d.mu.Unlock()
	if label != filepath.Base(other) {
		t.Fatalf("unnamed empty Group's label = %q, want %q", label, filepath.Base(other))
	}
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g}}})
}
