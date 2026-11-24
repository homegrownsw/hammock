package tui

import (
	"context"
	"path/filepath"
	"strconv"
	"time"

	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
)

// prMark is a tab's pull request, for the end of its row: "#42", "#42✓"
// merged, "#42✕" closed; "" with none.
func prMark(t *pb.Tab) string {
	pr := t.GetGit().GetPr()
	if pr == nil || pr.Number == 0 {
		return ""
	}
	mark := "#" + strconv.Itoa(int(pr.Number))
	switch pr.State {
	case "merged":
		mark += "✓"
	case "closed":
		mark += "✕"
	}
	return mark
}

// canRemoveWorktree: Remove Worktree is offered for t (a linked worktree
// whose branch is merged, on one of your computers, not a shared Group's).
func (a *App) canRemoveWorktree(t *pb.Tab) bool {
	if t == nil || !t.GetGit().GetRemovable() {
		return false
	}
	g := a.group(t.GroupId)
	return g != nil && (g.HostUserId == "" || a.computerOf(g) != nil)
}

// removeWorktree asks, then removes t's worktree (the repository's own
// way) and the tabs in it.
func (a *App) removeWorktree(t *pb.Tab) {
	git := t.GetGit()
	lines := []string{tildeHome(git.Worktree), "Its branch is merged. The tabs in it close."}
	if git.Dirty {
		lines = append(lines, "It has uncommitted changes: they're lost.")
	}
	id, force := t.Id, git.Dirty
	a.openDialog(&dialog{title: "Remove the Worktree “" + filepath.Base(git.Worktree) + "”?", okLabel: "Remove Worktree", confirm: true, lines: lines,
		submit: func([]string) {
			a.flash("Removing the worktree…")
			go func() {
				// The repository's own recipe may take a while.
				ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
				defer cancel()
				_, err := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_RemoveWorktree{RemoveWorktree: &pb.RemoveWorktree{TabId: id, Force: force}}})
				a.actions <- func() {
					if err != nil {
						a.flash("⚠ " + err.Error())
						return
					}
					a.flash("Removed the worktree “" + filepath.Base(git.Worktree) + "”")
				}
			}()
		}})
}

// startInWorktree starts an agent in a new worktree (StartAgent.worktree):
// the repository's recipe makes it first, which may take a while.
func (a *App) startInWorktree(m *pb.ClientMessage) {
	a.flash("Making the worktree…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 11*time.Minute)
		defer cancel()
		r, err := a.c.Call(ctx, m)
		a.actions <- func() {
			if err != nil {
				a.flash("⚠ " + err.Error())
				return
			}
			a.selectTab(r.GetCreated().GetId())
		}
	}()
}
