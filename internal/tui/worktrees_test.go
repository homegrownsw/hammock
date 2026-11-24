package tui

import (
	"slices"
	"testing"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// A tab's pull request shows at the end of its row; Remove Worktree is in
// its palette once the daemon offers it, and asks first.
func TestWorktreeTab(t *testing.T) {
	a, tab := paletteApp()
	a.groups = []*pb.Group{{Id: "g", TabIds: []string{"t1"}}}
	tab("t1")
	a.selTab = "t1"
	tb := a.tabs["t1"]
	tb.GroupId = "g"
	if prMark(tb) != "" {
		t.Fatal("a mark with no pull request")
	}
	tb.Git = &pb.TabGit{Branch: "pricing", Worktree: "/Users/me/Developer/app/.claude/worktrees/pricing", Pr: &pb.PullRequest{Number: 42, State: "open"}}
	if m := prMark(tb); m != "#42" {
		t.Fatalf("open: %q", m)
	}
	ids := func() []string {
		var out []string
		for _, it := range a.paletteItems() {
			out = append(out, it.id)
		}
		return out
	}
	if slices.Contains(ids(), "action:remove-worktree") {
		t.Fatal("offered before it's merged")
	}
	tb.Git.Pr.State, tb.Git.Done, tb.Git.Removable = "merged", true, true
	if m := prMark(tb); m != "#42✓" {
		t.Fatalf("merged: %q", m)
	}
	if !slices.Contains(ids(), "action:remove-worktree") {
		t.Fatal("not offered once merged")
	}
	a.removeWorktree(tb)
	if d, ok := a.overlay.(*dialog); !ok || !d.confirm || d.okLabel != "Remove Worktree" {
		t.Fatalf("didn't ask first: %#v", a.overlay)
	}
	// Not in a Group shared with you.
	a.groups[0].HostUserId = "someone"
	if a.canRemoveWorktree(tb) {
		t.Fatal("offered in a shared Group")
	}
}
