package com.homegrownsw.hammock.client

import hammock.v1.Hammock.Group
import hammock.v1.Hammock.Tab
import hammock.v1.Hammock.TabGit

/**
 * Git worktrees (the daemon's worktrees.go): a tab knows its branch and the
 * branch's pull request, and once that's merged, Remove Worktree is offered
 * for a tab in a linked worktree.
 */

/** Remove Worktree is offered for [tab] in [g]: its branch was merged a while ago, on one of your computers (not a Group shared with you). */
fun canRemoveWorktree(tab: Tab, g: Group?): Boolean =
    tab.hasGit() && tab.git.removable && g != null && !g.offline && (!g.isRemote || g.isOnLinkedComputer)

/** The worktree's folder's name ("pricing"). */
val TabGit.worktreeName: String get() = worktree.trimEnd('/').substringAfterLast('/')

/** Whether the tab shows a git mark: a linked worktree, or a pull request. */
val TabGit.shown: Boolean get() = worktree.isNotEmpty() || (hasPr() && pr.number > 0)

/** The tab's git state in words: "Worktree on pricing · Pull request #42, merged". */
val TabGit.summary: String get() = listOfNotNull(
    branch.takeIf { it.isNotEmpty() }?.let { (if (worktree.isEmpty()) "On " else "Worktree on ") + it },
    if (hasPr() && pr.number > 0) "Pull request #${pr.number}, ${pr.state}" + (if (pr.title.isEmpty()) "" else ": ${pr.title}") else null,
).joinToString(" · ")

/** What Remove Worktree's confirmation says: its title and message. */
fun removeWorktreeQuestion(git: TabGit): Pair<String, String> {
    var message = (if (git.pr.state == "closed") "Its pull request was closed." else "Its branch is merged.") + " The tabs in it close."
    if (git.dirty) message += " It has uncommitted changes, which are lost."
    return "Remove the worktree “${git.worktreeName}”?" to message
}
