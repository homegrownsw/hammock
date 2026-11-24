package com.homegrownsw.hammock.ui

import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import com.homegrownsw.hammock.LocalAppLocked
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Workspace
import com.homegrownsw.hammock.client.allPaneIDs
import com.homegrownsw.hammock.client.closeWarning
import com.homegrownsw.hammock.client.removeWorktreeQuestion
import hammock.v1.Hammock.Pane
import hammock.v1.Hammock.Tab

/**
 * Closing a tab or pane from anywhere (a swipe, a menu): it goes at once,
 * unless that would stop an agent at work, when it asks first (unless
 * Settings says not to). Held above
 * the screens, so a menu that's gone by then can still ask.
 */
class Closer {
    private class Ask(val title: String, val message: String, val button: String, val go: () -> Unit)

    private var asking by mutableStateOf<Ask?>(null)

    /** Settings ▸ Warn Before Closing an Agent at Work: off, nothing asks. */
    var warnBusy by mutableStateOf(true)

    /** Why closing [panes] needs asking about first, or null. */
    fun warning(panes: List<Pane>): String? = if (warnBusy) closeWarning(panes) else null

    /** Closes [panes] with [go], first asking when that stops an agent at work. */
    fun close(panes: List<Pane>, title: String, button: String, go: () -> Unit) {
        val why = warning(panes)
        if (why == null) go() else asking = Ask(title, why, button, go)
    }

    /** Remove Worktree: always asks, saying what runs and what its tabs stop (they close, and it's gone from disk). */
    fun removeWorktree(model: AppModel, w: Workspace, tab: Tab) {
        val panes = w.tabs.values.filter { it.hasGit() && it.git.worktree == tab.git.worktree }.flatMap { w.panes(it) }
        val q = removeWorktreeQuestion(tab.git, warning(panes))
        asking = if (q.canRemove) Ask(q.title, q.message, "Remove Worktree") { model.removeWorktree(tab.id, tab.git.dirty) }
        else Ask(q.title, q.message, "OK") {}
    }

    fun closeTab(model: AppModel, w: Workspace, tab: Tab) =
        close(w.panes(tab), "Close “${w.title(tab)}”?", "Close Tab") { model.closeTab(tab.id) }

    /** A pane: its tab when it's the tab's only pane. */
    fun closePane(model: AppModel, w: Workspace, pane: Pane, then: () -> Unit = {}) {
        val tab = w.tabs[pane.tabId]
        close(listOf(pane), "Close ${stripName(pane, w)}?", "Close Pane") {
            if (tab != null && tab.allPaneIDs().size == 1) model.closeTab(tab.id) else model.closePane(pane.id)
            then()
        }
    }

    @Composable
    fun Dialog() {
        val a = asking ?: return
        if (!LocalAppLocked.current) AlertDialog(
            onDismissRequest = { asking = null },
            title = { Text(a.title) },
            text = { Text(a.message) },
            confirmButton = { TextButton(onClick = { asking = null; a.go() }) { Text(a.button, color = ErrorColor) } },
            dismissButton = { TextButton(onClick = { asking = null }) { Text("Cancel") } },
        )
    }
}

val LocalCloser = staticCompositionLocalOf { Closer() }
