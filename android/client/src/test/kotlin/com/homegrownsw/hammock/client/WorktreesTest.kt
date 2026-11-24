package com.homegrownsw.hammock.client

import hammock.v1.Hammock.Group
import hammock.v1.Hammock.PullRequest
import hammock.v1.Hammock.Role
import hammock.v1.Hammock.Tab
import hammock.v1.Hammock.TabGit
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Remove Worktree is offered once the daemon says so, only on your own computers; the mark says what the branch is. */
class WorktreesTest {
    private val merged = TabGit.newBuilder().setBranch("pricing").setWorktree("/Users/me/Developer/app/.claude/worktrees/pricing")
        .setPr(PullRequest.newBuilder().setNumber(42).setState("merged")).setDone(true)

    @Test fun offeredOnceRemovable() {
        val mine = Group.getDefaultInstance()
        assertFalse(canRemoveWorktree(Tab.newBuilder().setGit(merged).build(), mine))
        val tab = Tab.newBuilder().setGit(merged.clone().setRemovable(true)).build()
        assertTrue(canRemoveWorktree(tab, mine))
        assertTrue(canRemoveWorktree(tab, Group.newBuilder().setHostUserId("u").setMyRole(Role.ROLE_OWNER).build()))
        assertFalse("a Group shared with you", canRemoveWorktree(tab, Group.newBuilder().setHostUserId("u").setMyRole(Role.ROLE_DRIVER).build()))
        assertFalse(canRemoveWorktree(tab, null))
    }

    @Test fun inWords() {
        val g = merged.build()
        assertTrue(g.shown)
        assertEquals("pricing", g.worktreeName)
        assertEquals("Worktree on pricing · Pull request #42, merged", g.summary)
        assertEquals("Remove the worktree “pricing”?" to "Its branch is merged. The tabs in it close.", removeWorktreeQuestion(g))
        assertFalse(TabGit.newBuilder().setBranch("main").build().shown)
    }
}
