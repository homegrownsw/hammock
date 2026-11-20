package com.homegrownsw.hammock.ui

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Link
import androidx.compose.material.icons.filled.SwapHoriz
import androidx.compose.material.icons.outlined.AddBox
import androidx.compose.material.icons.outlined.Bedtime
import androidx.compose.material.icons.outlined.LightMode
import androidx.compose.material.icons.outlined.StopCircle
import androidx.compose.runtime.saveable.rememberSaveable
import hammock.v1.Hammock.AgentState
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.Prefs
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Workspace
import com.homegrownsw.hammock.client.agentStatusLabel
import com.homegrownsw.hammock.client.allPaneIDs
import com.homegrownsw.hammock.client.canAddTabs
import com.homegrownsw.hammock.client.canDrive
import com.homegrownsw.hammock.client.canSleep
import com.homegrownsw.hammock.client.displayName
import com.homegrownsw.hammock.client.isDiff
import com.homegrownsw.hammock.client.isLog
import com.homegrownsw.hammock.client.isTerminal
import com.homegrownsw.hammock.client.sessionURL
import hammock.v1.Hammock.Pane
import hammock.v1.Hammock.PaneKind
import kotlinx.coroutines.launch

/** Each pane's choice of conversation or terminal, once switched: kept per pane. */
internal object PaneModes {
    private fun prefs(ctx: Context) = ctx.getSharedPreferences("paneModes", Context.MODE_PRIVATE)
    fun get(ctx: Context, paneID: String): Boolean? = prefs(ctx).let { if (it.contains(paneID)) it.getBoolean(paneID, true) else null }
    fun set(ctx: Context, paneID: String, agent: Boolean) = prefs(ctx).edit().putBoolean(paneID, agent).apply()
}

/**
 * A tab: its one pane as the whole screen, or with several, one at a time
 * with a strip of their names along the bottom to switch (and swiping
 * between them where the pane doesn't use sideways drags itself).
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun TabScreen(model: AppModel, tabID: String, nav: Nav, showBack: Boolean) {
    val w by model.workspace.collectAsStateWithLifecycle()
    val tab = w.tabs[tabID]
    if (tab == null) {
        DetailScaffold("Tab", if (showBack) nav::pop else null) { EmptyState("Tab Closed", "It was closed on your computer.", Modifier.padding(it)) }
        return
    }
    val ids = stripPaneIDs(tab).filter { w.panes[it] != null }
    if (ids.size <= 1) {
        ids.firstOrNull()?.let { PaneScreen(model, it, nav, showBack) }
            ?: DetailScaffold(w.title(tab), if (showBack) nav::pop else null) { EmptyState("Starting…", "", Modifier.padding(it)) }
        return
    }
    val start = ids.indexOf(tab.focusedPaneId).coerceAtLeast(0)
    val pager = rememberPagerState(start) { ids.size }
    val scope = rememberCoroutineScope()
    var exposing by rememberSaveable { mutableStateOf(false) }
    val current = ids.getOrNull(pager.currentPage) ?: ids.first()
    fun go(id: String) { ids.indexOf(id).takeIf { it >= 0 }?.let { scope.launch { pager.animateScrollToPage(it) } } }
    Box(Modifier.fillMaxSize()) {
        // Swiping sideways across a pane goes to the next, as on the iPhone (not on a page or a
        // simulator's screen, whose own drags come first).
        HorizontalPager(pager, Modifier.fillMaxSize(), key = { ids[it] }, beyondViewportPageCount = 0) { i ->
            PaneScreen(model, ids[i], nav, showBack, tabStrip = {
                PaneStrip(model, w, tab, current, nav, select = ::go, expose = { exposing = true })
            })
        }
        if (exposing) ExposeScreen(model, w, tab, current, nav, pick = { go(it); exposing = false }, dismiss = { exposing = false })
    }
}


/**
 * One pane, by its kind. An agent's offers its conversation or its terminal
 * (remembered per pane; Settings picks the first). Opening it tells the
 * computer it's being looked at (Focus), which marks a finished agent seen.
 */
@Composable
fun PaneScreen(model: AppModel, paneID: String, nav: Nav, showBack: Boolean, tabStrip: (@Composable () -> Unit)? = null) {
    val ctx = LocalContext.current
    val w by model.workspace.collectAsStateWithLifecycle()
    val pane = w.panes[paneID]
    if (pane == null) {
        DetailScaffold("Pane", if (showBack) nav::pop else null) { EmptyState("Pane Closed", "It was closed on your computer.", Modifier.padding(it)) }
        return
    }
    val group = w.groupOfPane(paneID)
    val drive = group?.canDrive != false && group?.offline != true
    val hasAgentView = pane.hasAgent() && AgentCatalog.hasAgentView(pane.agent.harness, w.kinds)
    var showAgent by remember(paneID) {
        mutableStateOf(PaneModes.get(ctx, paneID) ?: (Prefs.of(ctx).getString(Prefs.AGENT_TABS_OPEN_IN, "agent") == "agent"))
    }
    val logMode = pane.isLog
    var showLog by remember(paneID) { mutableStateOf(true) }

    LaunchedEffect(paneID) { model.focus(paneID) }
    // The computer knows which pane the phone shows: it doesn't notify about it then.
    DisposableEffect(paneID) {
        model.reportPresence(listOf(paneID), true, paneID)
        onDispose { model.reportPresence(emptyList(), true) }
    }

    // As on the iPhone: an agent's or a log's pane (which switches its view)
    // and one of several in its tab go by their own name, the tab's under
    // it; a tab's only pane, by the tab's.
    val tab = w.tabs[pane.tabId]
    val switchesView = hasAgentView || logMode
    val several = tab != null && tab.allPaneIDs().size > 1
    val title = if (switchesView || several || tab == null) stripName(pane, w) else w.title(tab)
    val subtitle = if ((switchesView || several) && tab != null) w.title(tab) else null
    DetailScaffold(
        title, if (showBack) nav::pop else null, subtitle = subtitle,
        // One ⋮: which view an agent's or a log's pane shows, then the tab's
        // actions (an editor draws its own ⋮, with them in it).
        actions = {
            if (pane.kind != PaneKind.PANE_KIND_EDITOR) PaneMenu(model, w, pane, drive, nav, view = {
                close ->
                if (switchesView) {
                    val on = if (logMode) showLog else showAgent
                    ViewChoice(if (logMode) "Log" else "Conversation", on) { close(); if (logMode) showLog = true else { showAgent = true; PaneModes.set(ctx, paneID, true) } }
                    ViewChoice("Terminal", !on) { close(); if (logMode) showLog = false else { showAgent = false; PaneModes.set(ctx, paneID, false) } }
                    HorizontalDivider()
                }
            })
        },
        bottomBar = { tabStrip?.invoke() },
    ) { padding ->
        // Clear of the gesture bar (the tab strip does that when it's there) and the keyboard.
        val bottom = if (tabStrip == null) Modifier.navigationBarsPadding() else Modifier
        Column(Modifier.fillMaxSize().padding(padding).consumeWindowInsets(padding).then(bottom).imePadding()) {
            Box(Modifier.weight(1f).fillMaxWidth()) {
                if (pane.asleep && pane.hasAgent()) {
                    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
                        horizontalAlignment = Alignment.CenterHorizontally) {
                        Text("${AgentCatalog.shortName(pane.agent.harness, w.kinds)} is asleep", style = MaterialTheme.typography.titleMedium)
                        Text("Its session resumes when you wake it, or send it a message.", color = MaterialTheme.colorScheme.onSurfaceVariant)
                        if (drive) Button(onClick = { model.sleepPane(paneID, wake = true) }) { Text("Wake Agent") }
                    }
                } else when {
                    hasAgentView && showAgent -> AgentContent(model, pane, drive) { showAgent = false; PaneModes.set(ctx, paneID, false) }
                    logMode && showLog -> LogContent(model, pane)
                    pane.kind == PaneKind.PANE_KIND_EDITOR -> EditorContent(model, pane, drive) { close -> PaneMenuItems(model, w, pane, drive, nav, close) }
                    pane.kind == PaneKind.PANE_KIND_DIFF -> DiffContent(model, pane, drive)
                    pane.kind == PaneKind.PANE_KIND_BROWSER -> BrowserContent(model, pane, drive)
                    pane.kind == PaneKind.PANE_KIND_SIMULATOR -> SimulatorContent(model, pane, drive)
                    else -> TerminalContent(model, pane, drive)
                }
            }
        }
    }
}

@Composable
private fun ViewChoice(label: String, on: Boolean, pick: () -> Unit) = DropdownMenuItem(
    text = { Text(label) }, onClick = pick,
    leadingIcon = { if (on) Icon(Icons.Default.Check, "Shown") else Box(Modifier.size(24.dp)) },
)

/** The pane's ⋮: [view] first (which view it shows), then the tab's actions, then the pane's own when the tab has several. */
@Composable
private fun PaneMenu(model: AppModel, w: Workspace, pane: Pane, drive: Boolean, nav: Nav, view: @Composable (close: () -> Unit) -> Unit = {}) {
    var open by remember { mutableStateOf(false) }
    var renaming by remember { mutableStateOf(false) }
    var moving by remember { mutableStateOf(false) }
    var starting by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val tab = w.tabs[pane.tabId]
    val group = tab?.let { w.groupOf(it) }
    Box {
        IconButton(onClick = { open = true }) { Icon(Icons.Default.MoreVert, "Tab Actions") }
        DropdownMenu(open, { open = false }) {
            view { open = false }
            if (tab != null && group != null) {
                TabActions(model, w, group, tab, close = { open = false }, rename = { renaming = true },
                    newTab = { scope.launch { model.createTab(group.id)?.let { nav.open("tab:$it") } } },
                    startAgent = { starting = true }, move = { moving = true })
            }
            if (tab != null && tab.allPaneIDs().size > 1) {
                HorizontalDivider()
                PaneMenuItems(model, w, pane, drive, nav, close = { open = false })
            }
        }
        DropdownMenu(moving, { moving = false }) {
            for (other in w.groups.filter { it.id != group?.id && it.canAddTabs }) {
                DropdownMenuItem(text = { Text(w.title(other)) }, onClick = { moving = false; tab?.let { model.moveTab(it.id, other.id, other.tabIdsCount) } })
            }
        }
    }
    if (renaming && tab != null) TextPrompt("Rename Tab", w.title(tab), "Name", onDismiss = { renaming = false }) { model.renameTab(tab.id, it); renaming = false }
    if (starting && group != null) StartAgentSheet(model, groupID = group.id, nav = nav) { starting = false }
}

/** What a pane goes by (the iPhone's stripName): an agent by its name, a browser by its page, an editor by its file. */
fun stripName(p: Pane, w: Workspace): String = when (p.kind) {
    PaneKind.PANE_KIND_BROWSER -> p.title.ifEmpty { "Browser" }
    PaneKind.PANE_KIND_SIMULATOR -> p.title.ifEmpty { "Simulator" }
    PaneKind.PANE_KIND_EDITOR -> p.editor.name.ifEmpty { p.displayName }
    PaneKind.PANE_KIND_DIFF -> p.diff.title.ifEmpty { "Diff" }
    else -> if (p.hasAgent() && p.agent.harness.isNotEmpty()) AgentCatalog.shortName(p.agent.harness, w.kinds) else p.displayName
}

/** A pane's actions, in its ⋮ menu (or an editor's own, which carries them). */
@Composable
fun PaneMenuItems(
    model: AppModel, w: Workspace, pane: Pane, drive: Boolean, nav: Nav, close: () -> Unit,
    popAfterClose: Boolean = true, floatActions: (@Composable () -> Unit)? = null,
) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    if (pane.hasAgent() && !pane.exited && drive) {
        val s = pane.agent.state
        if (s == AgentState.AGENT_STATE_WORKING || s == AgentState.AGENT_STATE_NEEDS_INPUT) {
            DropdownMenuItem(text = { Text("Interrupt") }, leadingIcon = { Icon(Icons.Outlined.StopCircle, null) }, onClick = { close(); model.interrupt(pane.id) })
        }
        AgentCatalog.other(pane.agent.harness, w.kinds)?.let { other ->
            DropdownMenuItem(text = { Text("Continue in ${AgentCatalog.shortName(other, w.kinds)}") }, leadingIcon = { Icon(Icons.Default.SwapHoriz, null) },
                onClick = { close(); model.switchAgent(pane.id, other) })
        }
        if (pane.asleep) DropdownMenuItem(text = { Text("Wake Agent") }, leadingIcon = { Icon(Icons.Outlined.LightMode, null) },
            onClick = { close(); model.sleepPane(pane.id, wake = true) })
        else if (pane.canSleep) DropdownMenuItem(text = { Text("Put Agent to Sleep") }, leadingIcon = { Icon(Icons.Outlined.Bedtime, null) },
            onClick = { close(); model.sleepPane(pane.id) })
    }
    pane.sessionURL?.let { url ->
        DropdownMenuItem(text = { Text("Copy Session Link") }, leadingIcon = { Icon(Icons.Default.Link, null) }, onClick = {
            close()
            ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Session link", url))
        })
    }
    val tab = w.tabs[pane.tabId]
    if (pane.hasAgent() || pane.sessionURL != null) HorizontalDivider()
    if (floatActions != null) floatActions()
    else if (drive && tab != null && tab.allPaneIDs().size > 1) {
        DropdownMenuItem(text = { Text("Move to New Tab") }, leadingIcon = { Icon(Icons.Outlined.AddBox, null) }, onClick = {
            close()
            scope.launch { model.breakOut(pane.id)?.let { nav.open("tab:$it") } }
        })
    }
    if (drive) {
        val closer = LocalCloser.current
        DropdownMenuItem(text = { Text("Close Pane", color = ErrorColor) }, leadingIcon = { Icon(Icons.Default.Close, null, tint = ErrorColor) }, onClick = {
            close()
            closer.closePane(model, w, pane) { if (popAfterClose) nav.pop() }
        })
    }
}
