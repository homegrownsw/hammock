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
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
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
private object PaneModes {
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
    val ids = tab.allPaneIDs().filter { w.panes[it] != null }
    if (ids.size <= 1) {
        ids.firstOrNull()?.let { PaneScreen(model, it, nav, showBack) }
            ?: DetailScaffold(w.title(tab), if (showBack) nav::pop else null) { EmptyState("Starting…", "", Modifier.padding(it)) }
        return
    }
    val start = ids.indexOf(tab.focusedPaneId).coerceAtLeast(0)
    val pager = rememberPagerState(start) { ids.size }
    val scope = rememberCoroutineScope()
    Column(Modifier.fillMaxSize()) {
        HorizontalPager(pager, Modifier.weight(1f), userScrollEnabled = false, key = { ids[it] }, beyondViewportPageCount = 0) { i ->
            PaneScreen(model, ids[i], nav, showBack, tabStrip = {
                LazyRow(
                    Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainer).navigationBarsPadding(),
                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    itemsIndexed(ids, key = { _, id -> id }) { j, id ->
                        val p = w.panes[id] ?: return@itemsIndexed
                        val selected = j == pager.currentPage
                        Row(
                            Modifier.clip(RoundedCornerShape(50))
                                .background(if (selected) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surfaceContainerHigh)
                                .clickable { scope.launch { pager.animateScrollToPage(j) } }
                                .padding(horizontal = 12.dp, vertical = 6.dp),
                            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp),
                        ) {
                            if (p.hasAgent()) StatusIcon(p.agent.state, size = 14.dp)
                            Text(paneLabel(p), style = MaterialTheme.typography.labelLarge, maxLines = 1)
                        }
                    }
                }
            })
        }
    }
}

fun paneLabel(p: Pane): String = when {
    p.hasAgent() && p.agent.harness.isNotEmpty() -> p.agent.harness.replaceFirstChar { it.uppercase() }
    p.kind == PaneKind.PANE_KIND_BROWSER -> p.title.ifEmpty { "Browser" }
    p.kind == PaneKind.PANE_KIND_SIMULATOR -> p.title.ifEmpty { "Simulator" }
    p.isLog -> "Log"
    p.isDiff -> "Diff"
    else -> p.displayName
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
    DisposableEffect(paneID) {
        model.reportPresence(listOf(paneID), true, paneID)
        onDispose { }
    }

    val title = w.tabs[pane.tabId]?.let { w.title(it) } ?: pane.displayName
    val subtitle = when {
        pane.hasAgent() -> listOf(AgentCatalog.shortName(pane.agent.harness, w.kinds), pane.agentStatusLabel).filter { it.isNotEmpty() }.joinToString(" · ")
        else -> pane.cwd.replace(Regex("^/(Users|home)/[^/]+"), "~")
    }
    DetailScaffold(
        title, if (showBack) nav::pop else null, subtitle = subtitle,
        actions = { PaneMenu(model, w, pane, drive, nav) },
        bottomBar = { tabStrip?.invoke() },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            if (hasAgentView || logMode) {
                SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp)) {
                    val first = if (logMode) "Log" else "Conversation"
                    val on = if (logMode) showLog else showAgent
                    SegmentedButton(on, { if (logMode) showLog = true else { showAgent = true; PaneModes.set(ctx, paneID, true) } },
                        SegmentedButtonDefaults.itemShape(0, 2)) { Text(first) }
                    SegmentedButton(!on, { if (logMode) showLog = false else { showAgent = false; PaneModes.set(ctx, paneID, false) } },
                        SegmentedButtonDefaults.itemShape(1, 2)) { Text("Terminal") }
                }
            }
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
                    pane.kind == PaneKind.PANE_KIND_EDITOR -> EditorContent(model, pane, drive)
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
private fun PaneMenu(model: AppModel, w: Workspace, pane: Pane, drive: Boolean, nav: Nav) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var open by remember { mutableStateOf(false) }
    Box {
        IconButton(onClick = { open = true }) { Icon(Icons.Default.MoreVert, "Pane Actions") }
        DropdownMenu(open, { open = false }) {
            if (pane.hasAgent() && drive) {
                DropdownMenuItem(text = { Text("Interrupt") }, onClick = { open = false; model.interrupt(pane.id) })
                AgentCatalog.other(pane.agent.harness, w.kinds)?.let { other ->
                    DropdownMenuItem(text = { Text("Continue in ${AgentCatalog.shortName(other, w.kinds)}") },
                        onClick = { open = false; model.switchAgent(pane.id, other) })
                }
                if (pane.asleep) DropdownMenuItem(text = { Text("Wake Agent") }, onClick = { open = false; model.sleepPane(pane.id, wake = true) })
                else if (pane.canSleep) DropdownMenuItem(text = { Text("Put Agent to Sleep") }, onClick = { open = false; model.sleepPane(pane.id) })
            }
            pane.sessionURL?.let { url ->
                DropdownMenuItem(text = { Text("Copy Session Link") }, onClick = {
                    open = false
                    ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText("Session link", url))
                })
            }
            val tab = w.tabs[pane.tabId]
            if (drive && tab != null && tab.allPaneIDs().size > 1) {
                DropdownMenuItem(text = { Text("Move to New Tab") }, onClick = {
                    open = false
                    scope.launch { model.breakOut(pane.id)?.let { nav.open("tab:$it") } }
                })
            }
            if (drive) {
                HorizontalDivider()
                DropdownMenuItem(text = { Text("Close Pane", color = ErrorColor) }, onClick = {
                    open = false
                    if (tab != null && tab.allPaneIDs().size == 1) model.closeTab(tab.id) else model.closePane(pane.id)
                    nav.pop()
                })
            }
        }
    }
}
