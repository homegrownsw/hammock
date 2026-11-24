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

// worktreeTabs are the tabs in t's worktree (they close with it), and
// their panes.
func (a *App) worktreeTabs(t *pb.Tab) (tabs []*pb.Tab, panes []string) {
	for _, o := range a.tabs {
		if o.GetGit().GetWorktree() == t.GetGit().GetWorktree() {
			tabs = append(tabs, o)
			panes = append(panes, tabIDs(o)...)
		}
	}
	return tabs, panes
}

// removeWorktree asks, saying what will run and what's still running
// there, then removes t's worktree (the repository's own way) and the tabs
// in it.
func (a *App) removeWorktree(t *pb.Tab) {
	git := t.GetGit()
	name := filepath.Base(git.Worktree)
	lines := []string{tildeHome(git.Worktree)}
	if git.NeedsClean {
		// The repository's own command won't drop them: nothing to confirm.
		lines = append(lines, "It has uncommitted changes. Commit or discard them first: "+git.RemoveWith+" won't remove it with them.")
		a.openDialog(&dialog{title: "Remove the Worktree “" + name + "”", okLabel: "OK", confirm: true, lines: lines, submit: func([]string) {}})
		return
	}
	what := "Its branch is merged."
	if git.GetPr().GetState() == "closed" {
		what = "Its pull request was closed."
	}
	lines = append(lines, what+" The tabs in it close.", "Runs: "+git.RemoveWith)
	if git.Dirty {
		lines = append(lines, "It has uncommitted changes: they're lost.")
	}
	_, panes := a.worktreeTabs(t)
	if _, busy := a.busyWarning(panes); busy != "" {
		lines = append(lines, busy)
	}
	id, force := t.Id, git.Dirty
	a.openDialog(&dialog{title: "Remove the Worktree “" + name + "”?", okLabel: "Remove Worktree", confirm: true, lines: lines,
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
					a.flash("Removed the worktree “" + name + "”")
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

// worktreeCommandFor asks how a worktree would be made from dir for
// harness (the command, as it'll run), and hands it to then on the UI's
// loop: "" outside a git repository.
func (a *App) worktreeCommandFor(group, dir, harness string, then func(how string)) {
	if a.c == nil || dir == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		r, _ := a.c.Call(ctx, &pb.ClientMessage{Msg: &pb.ClientMessage_ListFolders{ListFolders: &pb.ListFolders{Path: dir, GroupId: group, Harness: harness}}})
		how := r.GetFolderList().GetWorktreeCommand()
		a.actions <- func() { then(how) }
	}()
}
