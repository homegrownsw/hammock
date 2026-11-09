package daemon

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/alexjsp/hammock/internal/client"
	pb "github.com/alexjsp/hammock/internal/proto/hammockv1"
)

// TestReopenClosedGroup: Reopen Closed brings back a Group that
// closed with its tabs, as it was (name, icon, folder, place), its tabs in
// their order; a tab whose Group still exists goes back into it.
func TestReopenClosedGroup(t *testing.T) {
	dir := shortDir(t)
	d := startDaemon(t, dir)
	defer d.Shutdown()
	c := dial(t, dir)
	work, _ := filepath.EvalSymlinks(t.TempDir()) // as the shell reports it
	newGroup := func(name string) string {
		return client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateGroup{CreateGroup: &pb.CreateGroup{Name: name}}}))
	}
	newTab := func(g, name string) string {
		return client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CreateTab{CreateTab: &pb.CreateTab{GroupId: g, Name: name, Pane: &pb.PaneSpec{Command: []string{"/bin/cat"}, Cwd: work}}}}))
	}
	reopen := func() string {
		return client.CreatedID(call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_ReopenClosed{ReopenClosed: &pb.ReopenClosed{}}}))
	}
	first := newGroup("first")
	newTab(first, "x")
	g := newGroup("proj")
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_SetGroupIcon{SetGroupIcon: &pb.SetGroupIcon{GroupId: g, Icon: "🚀"}}})
	newTab(g, "one")
	newTab(g, "two")
	d.mu.Lock()
	order := d.groups[g].Order
	d.mu.Unlock()

	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g}}})
	t1, t2 := reopen(), reopen()

	d.mu.Lock()
	sg := d.groups[g]
	if sg == nil {
		d.mu.Unlock()
		t.Fatal("reopening didn't bring the Group back")
	}
	got := []string{d.tabs[sg.TabIds[0]].Name, d.tabs[sg.TabIds[len(sg.TabIds)-1]].Name}
	name, icon, folder, gotOrder, ids := sg.Name, sg.Icon, sg.Folder, sg.Order, slices.Clone(sg.TabIds)
	d.mu.Unlock()
	if name != "proj" || icon != "🚀" || folder != work || gotOrder != order {
		t.Fatalf("restored Group = %q %q %q order %d, want proj 🚀 %q order %d", name, icon, folder, gotOrder, work, order)
	}
	if !slices.Equal(ids, []string{t1, t2}) || !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("tabs %v (%v), want one, two", ids, got)
	}

	// Its last tab closed on its own leaves the Group: the tab goes back in it.
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: t1}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseTab{CloseTab: &pb.CloseTab{TabId: t2}}})
	call(t, c, &pb.ClientMessage{Msg: &pb.ClientMessage_CloseGroup{CloseGroup: &pb.CloseGroup{GroupId: g}}}) // as ⌘W with Groups not kept
	back := reopen()
	d.mu.Lock()
	in := d.tabs[back].GetGroupId()
	d.mu.Unlock()
	if in != g {
		t.Fatalf("the last tab reopened in %q, want its own Group %q back", in, g)
	}
}
