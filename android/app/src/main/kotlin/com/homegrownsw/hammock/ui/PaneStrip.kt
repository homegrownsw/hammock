package com.homegrownsw.hammock.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Article
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Error
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.outlined.Minimize
import androidx.compose.material.icons.outlined.OpenInNew
import androidx.compose.material.icons.outlined.VerticalSplit
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.homegrownsw.hammock.agentview.AgentSession
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Workspace
import com.homegrownsw.hammock.client.agentProgress
import com.homegrownsw.hammock.client.canDrive
import com.homegrownsw.hammock.client.isLog
import com.homegrownsw.hammock.client.paneIDs
import com.homegrownsw.hammock.client.shownState
import com.homegrownsw.hammock.terminal.TerminalPane
import com.homegrownsw.hammock.terminal.TerminalSession
import hammock.v1.Hammock
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.Layout
import hammock.v1.Hammock.Pane
import hammock.v1.Hammock.PaneKind
import hammock.v1.Hammock.Split
import hammock.v1.Hammock.Tab

// A tab of several panes, as on the iPhone (PaneStrip.swift): a strip of
// pills along the bottom to switch between them, after a button that opens
// Exposé, the tab laid out as on the computer with each pane live.

/** The order the strip goes through a tab's panes: its splits' in reading order, then its floats, front first. */
fun stripPaneIDs(tab: Tab): List<String> = tab.layout.paneIDs() + tab.floatingList.reversed().map { it.paneId }

/** The tab's panes as pills, the current one marked, after the Exposé button (its icon the tab's layout). Floats after a divider. */
@Composable
fun PaneStrip(model: AppModel, w: Workspace, tab: Tab, current: String, nav: Nav, select: (String) -> Unit, expose: () -> Unit) {
    val tiled = tab.layout.paneIDs().filter { w.panes[it] != null }
    val floats = tab.floatingList.reversed().filter { w.panes[it.paneId] != null }
    val list = rememberLazyListState()
    val all = tiled + floats.map { it.paneId }
    LaunchedEffect(current) {
        val i = all.indexOf(current).let { if (it >= tiled.size) it + 1 else it }
        if (i >= 0) list.animateScrollToItem(maxOf(0, i - 1))
    }
    Surface(color = MaterialTheme.colorScheme.surfaceContainer) {
        Column(Modifier.navigationBarsPadding()) {
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f))
            Row(Modifier.fillMaxWidth().padding(start = 12.dp, top = 8.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier.size(44.dp).clip(CircleShape).background(MaterialTheme.colorScheme.surfaceContainerHighest)
                        .clickable(onClick = expose).semantics { contentDescription = "Show All Panes" },
                    contentAlignment = Alignment.Center,
                ) { LayoutGlyph(tab.layout, current, Modifier.size(20.dp)) }
                VerticalDivider(Modifier.height(26.dp).padding(horizontal = 8.dp))
                LazyRow(state = list, horizontalArrangement = Arrangement.spacedBy(8.dp), contentPadding = androidx.compose.foundation.layout.PaddingValues(end = 12.dp)) {
                    items(tiled, key = { it }) { id -> StripPill(model, w, id, id == current, null, nav, select) }
                    if (floats.isNotEmpty()) {
                        item(key = "floats") { VerticalDivider(Modifier.height(26.dp).padding(top = 7.dp)) }
                        items(floats, key = { it.paneId }) { f -> StripPill(model, w, f.paneId, f.paneId == current, f, nav, select) }
                    }
                }
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun StripPill(model: AppModel, w: Workspace, id: String, current: Boolean, floating: Hammock.FloatingPane?, nav: Nav, select: (String) -> Unit) {
    var menu by remember { mutableStateOf(false) }
    Box {
        PanePill(w.panes[id], w, current, floating != null, floating?.minimised == true,
            Modifier.combinedClickable(onClick = { select(id) }, onLongClick = { menu = true }))
        DropdownMenu(menu, { menu = false }) {
            w.panes[id]?.let { PaneTouchMenu(model, w, it, floating, nav) { menu = false } }
        }
    }
}

/** One pane in the strip: its icon and name, and what it needs you to know (an agent at work or waiting, finished and unseen, how far along). */
@Composable
fun PanePill(p: Pane?, w: Workspace, current: Boolean, floating: Boolean, minimised: Boolean, modifier: Modifier = Modifier) {
    val accent = MaterialTheme.colorScheme.primary
    val amber = StatusPalette.amber()
    val waiting = p?.hasAgent() == true && p.agent.shownState == AgentState.AGENT_STATE_NEEDS_INPUT
    val ink = when { waiting -> amber; current -> accent; else -> MaterialTheme.colorScheme.onSurfaceVariant }
    val fill = when {
        waiting -> amber.copy(alpha = if (current) 0.22f else 0.12f)
        current -> accent.copy(alpha = 0.16f)
        else -> MaterialTheme.colorScheme.surfaceContainerLowest
    }
    val edge = when { waiting -> amber; current -> accent; else -> MaterialTheme.colorScheme.outlineVariant }
    val edgeWidth = if (current || waiting) 1.5.dp else 1.dp
    val progress = p?.agentProgress
    Row(
        modifier.height(40.dp).clip(CircleShape).background(fill)
            .drawBehind {
                val sw = edgeWidth.toPx()
                val r = size.height / 2
                drawRoundRect(edge.copy(alpha = if (floating && !current && !waiting) 0.8f else 1f), Offset(sw / 2, sw / 2), Size(size.width - sw, size.height - sw),
                    CornerRadius(r - sw / 2), style = Stroke(sw, pathEffect = if (floating) PathEffect.dashPathEffect(floatArrayOf(3.dp.toPx(), 3.dp.toPx())) else null))
                if (progress != null) {
                    drawRect(if (waiting) amber else accent, Offset(0f, size.height - 3.dp.toPx()), Size(size.width * progress.coerceIn(0, 100) / 100f, 3.dp.toPx()))
                }
            }
            .padding(horizontal = 14.dp)
            .semantics { selected = current },
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        val alpha = if (minimised && !current) 0.6f else 1f
        CompositionLocalProvider(LocalContentColor provides ink.copy(alpha = alpha)) {
            Box(Modifier.size(16.dp), contentAlignment = Alignment.Center) { PaneIcon(p, 16.dp, Modifier) }
            Text(p?.let { stripName(it, w) } ?: "", style = MaterialTheme.typography.labelLarge,
                fontWeight = if (current) FontWeight.SemiBold else FontWeight.Normal, maxLines = 1, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.widthIn(max = 140.dp))
            if (p != null && p.hasAgent() && !p.exited) {
                val s = p.agent.shownState
                if (s in setOf(AgentState.AGENT_STATE_WORKING, AgentState.AGENT_STATE_NEEDS_INPUT, AgentState.AGENT_STATE_STARTING, AgentState.AGENT_STATE_ERRORED)) {
                    StatusIcon(s, asleep = p.asleep, size = 14.dp)
                } else if (s == AgentState.AGENT_STATE_IDLE && !p.agent.seen) {
                    Box(Modifier.size(7.dp).clip(CircleShape).background(accent).semantics { contentDescription = "Finished" })
                }
            }
        }
    }
}

/** Each pane's rectangle in [rect], splits divided by their weights (the iPhone's PaneLayout.rects). */
fun Layout.rects(rect: Rect, gap: Float): List<Pair<String, Rect>> = when (nodeCase) {
    Layout.NodeCase.PANE_ID -> listOf(paneId to rect)
    Layout.NodeCase.SPLIT -> {
        val s = split
        val n = s.childrenCount
        if (n == 0) emptyList() else {
            val side = s.direction != Split.Direction.DIRECTION_VERTICAL
            val w = if (s.weightsCount == n) s.weightsList else List(n) { 1f / n }
            val total = w.sum()
            val span = (if (side) rect.width else rect.height) - gap * (n - 1)
            var at = if (side) rect.left else rect.top
            val out = ArrayList<Pair<String, Rect>>()
            s.childrenList.forEachIndexed { i, child ->
                val len = span * (if (total > 0) w[i] / total else 1f / n)
                val r = if (side) Rect(at, rect.top, at + len, rect.bottom) else Rect(rect.left, at, rect.right, at + len)
                out += child.rects(r, gap)
                at += len + gap
            }
            out
        }
    }
    else -> emptyList()
}

/** The tab's split layout in miniature (Exposé's button): a box per pane, the current one filled. */
@Composable
fun LayoutGlyph(layout: Layout, current: String, modifier: Modifier = Modifier) {
    val ink = MaterialTheme.colorScheme.onSurface
    Canvas(modifier) {
        val line = 1.5.dp.toPx()
        for ((id, r) in layout.rects(Rect(line / 2, line / 2, size.width - line / 2, size.height - line / 2), 3.dp.toPx())) {
            val radius = CornerRadius(minOf(3.dp.toPx(), r.width / 3, r.height / 3))
            if (id == current) drawRoundRect(ink, r.topLeft, r.size, radius)
            drawRoundRect(ink, r.topLeft, r.size, radius, style = Stroke(line))
        }
    }
}

/** A pane's touch-and-hold menu (its pill, its Exposé tile): its agent's actions, a float's place on the computer, moving and closing it. */
@Composable
fun PaneTouchMenu(model: AppModel, w: Workspace, pane: Pane, floating: Hammock.FloatingPane?, nav: Nav, close: () -> Unit) {
    val group = w.groupOfPane(pane.id)
    val drive = group?.canDrive != false && group?.offline != true
    PaneMenuItems(model, w, pane, drive, nav, close, popAfterClose = false, floatActions = floating?.let { f ->
        {
            fun float(a: Hammock.FloatPane.Action) = model.send { setFloatPane(Hammock.FloatPane.newBuilder().setAction(a).setPaneId(pane.id).build()) }
            if (f.minimised) DropdownMenuItem(text = { Text("Restore on the Computer") }, leadingIcon = { Icon(Icons.Outlined.OpenInNew, null) }, enabled = drive,
                onClick = { close(); float(Hammock.FloatPane.Action.ACTION_RESTORE) })
            else DropdownMenuItem(text = { Text("Minimise on the Computer") }, leadingIcon = { Icon(Icons.Outlined.Minimize, null) }, enabled = drive,
                onClick = { close(); float(Hammock.FloatPane.Action.ACTION_MINIMISE) })
            DropdownMenuItem(text = { Text("Dock in the Splits") }, leadingIcon = { Icon(Icons.Outlined.VerticalSplit, null) }, enabled = drive,
                onClick = { close(); float(Hammock.FloatPane.Action.ACTION_DOCK) })
        }
    })
}

// MARK: Exposé

/** The tab laid out as on the computer, each pane live (only while this is open): tap one to go to it. Its floats underneath, front first. */
@Composable
fun ExposeScreen(model: AppModel, w: Workspace, tab: Tab, current: String, nav: Nav, pick: (String) -> Unit, dismiss: () -> Unit) {
    BackHandler(onBack = dismiss)
    Surface(Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.surfaceContainerLow) {
        Column(Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().padding(horizontal = 12.dp).padding(top = 8.dp, bottom = 4.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(w.title(tab), style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    val n = tab.layout.paneIDs().size
                    val panes = if (n == 1) "1 pane" else "$n panes"
                    Text(if (tab.floatingCount == 0) panes else "$panes · ${tab.floatingCount} floating",
                        style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                FilledTonalButton(onClick = dismiss, colors = tintedButtonColors()) { Text("Done") }
            }
            Box(Modifier.weight(1f).fillMaxWidth()) { ExposeLayout(model, w, tab.layout, current, nav, pick) }
            if (tab.floatingCount > 0) {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("Floating", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    LazyRow(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        items(tab.floatingList.reversed(), key = { it.paneId }) { f ->
                            ExposeTile(model, w, f.paneId, f.paneId == current, f, nav, pick, Modifier.width(170.dp).height(110.dp))
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun ExposeLayout(model: AppModel, w: Workspace, layout: Layout, current: String, nav: Nav, pick: (String) -> Unit) {
    when (layout.nodeCase) {
        Layout.NodeCase.PANE_ID -> ExposeTile(model, w, layout.paneId, layout.paneId == current, null, nav, pick, Modifier.fillMaxSize())
        Layout.NodeCase.SPLIT -> {
            val s = layout.split
            val n = s.childrenCount
            val weights = if (s.weightsCount == n) s.weightsList else List(n) { 1f / n }
            if (s.direction != Split.Direction.DIRECTION_VERTICAL) {
                Row(Modifier.fillMaxSize(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    s.childrenList.forEachIndexed { i, c -> Box(Modifier.weight(weights[i].coerceAtLeast(0.01f)).fillMaxHeight()) { ExposeLayout(model, w, c, current, nav, pick) } }
                }
            } else {
                Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    s.childrenList.forEachIndexed { i, c -> Box(Modifier.weight(weights[i].coerceAtLeast(0.01f)).fillMaxWidth()) { ExposeLayout(model, w, c, current, nav, pick) } }
                }
            }
        }
        else -> {}
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ExposeTile(model: AppModel, w: Workspace, id: String, current: Boolean, floating: Hammock.FloatingPane?, nav: Nav, pick: (String) -> Unit, modifier: Modifier) {
    var menu by remember { mutableStateOf(false) }
    Box(modifier) {
        LiveTile(model, w, id, current, floating?.minimised == true, Modifier.fillMaxSize().combinedClickable(onClick = { pick(id) }, onLongClick = { menu = true }))
        DropdownMenu(menu, { menu = false }) {
            w.panes[id]?.let { PaneTouchMenu(model, w, it, floating, nav) { menu = false } }
        }
    }
}

/** A pane, live, small: its name and state over what it shows (its terminal, its agent's latest, its file or diff). */
@Composable
private fun LiveTile(model: AppModel, w: Workspace, id: String, current: Boolean, minimised: Boolean, modifier: Modifier) {
    val p = w.panes[id]
    val accent = MaterialTheme.colorScheme.primary
    Column(
        modifier.alpha(if (minimised) 0.6f else 1f).clip(RoundedCornerShape(14.dp)).background(cardColor())
            .then(if (current) Modifier.border(2.5.dp, accent, RoundedCornerShape(14.dp)) else Modifier)
            .padding(8.dp).semantics { selected = current },
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            PaneIcon(p, 14.dp)
            Text(p?.let { stripName(it, w) } ?: "", style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.SemiBold, maxLines = 1,
                overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            if (minimised) Text("Minimised", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            else if (p != null && p.hasAgent() && !p.exited && p.agent.shownState != AgentState.AGENT_STATE_UNSPECIFIED) {
                StatusIcon(p.agent.shownState, asleep = p.asleep, size = 14.dp)
            }
        }
        Box(Modifier.weight(1f).fillMaxWidth().clip(RoundedCornerShape(8.dp))) {
            if (p != null) when {
                p.kind == PaneKind.PANE_KIND_BROWSER || p.kind == PaneKind.PANE_KIND_SIMULATOR -> Box(
                    Modifier.fillMaxSize().background(MaterialTheme.colorScheme.onSurface.copy(alpha = 0.08f)), contentAlignment = Alignment.Center,
                ) { CompositionLocalProvider(LocalContentColor provides MaterialTheme.colorScheme.onSurfaceVariant) { PaneIcon(p, 28.dp) } }
                p.kind == PaneKind.PANE_KIND_EDITOR -> FileTile(p)
                p.kind == PaneKind.PANE_KIND_DIFF -> DiffTile(p)
                !p.isLog && p.hasAgent() && AgentCatalog.hasAgentView(p.agent.harness, w.kinds) && showsAgent(p.id) -> AgentTailTile(model, p)
                else -> TerminalTile(model, p)
            }
        }
    }
}

/** Whether an agent's pane shows its conversation (as it opens on this phone), not its terminal. */
@Composable
private fun showsAgent(paneID: String): Boolean {
    val ctx = androidx.compose.ui.platform.LocalContext.current
    return PaneModes.get(ctx, paneID) ?: (com.homegrownsw.hammock.Prefs.of(ctx).getString(com.homegrownsw.hammock.Prefs.AGENT_TABS_OPEN_IN, "agent") == "agent")
}

/** A terminal pane's tile: its screen, small, the whole grid fitted in. */
@Composable
private fun TerminalTile(model: AppModel, p: Pane) {
    val session = remember(model, p.id) { TerminalSession(p.id, p.cols.coerceAtLeast(1), p.rows.coerceAtLeast(1)) }
    DisposableEffect(session) {
        model.attach(p.id, session)
        onDispose { model.detach(p.id, session); session.close() }
    }
    TerminalPane(session, onInput = {}, onResize = { _, _, _, _ -> }, readOnly = true, active = false, theme = rememberTerminalTheme(model),
        fontSize = 6.sp, modifier = Modifier.fillMaxSize())
}

/** An agent pane's tile: your last message and what's come since, its words and latest tools. */
@Composable
private fun AgentTailTile(model: AppModel, p: Pane) {
    val session = remember(model, p.id) { AgentSession.of(model, p.id) }
    DisposableEffect(session) { session.start(); onDispose { session.stop() } }
    val state by session.state.collectAsState()
    val tail = agentTail(state.blocks)
    val accent = MaterialTheme.colorScheme.primary
    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(6.dp, Alignment.Bottom)) {
        for (b in tail) when (b.kind) {
            Hammock.AgentBlock.Kind.KIND_USER -> Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.CenterEnd) {
                Text(b.text, style = MaterialTheme.typography.bodySmall, maxLines = 3, overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.clip(RoundedCornerShape(10.dp)).background(accent.copy(alpha = 0.18f)).padding(horizontal = 8.dp, vertical = 5.dp))
            }
            Hammock.AgentBlock.Kind.KIND_TOOL -> Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                val c = if (b.error) StatusPalette.red() else MaterialTheme.colorScheme.onSurfaceVariant
                Icon(if (b.pending) Icons.Default.Settings else if (b.error) Icons.Default.Error else Icons.Default.Check, null, Modifier.size(12.dp), tint = c)
                Text(b.title, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace, color = c, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            else -> Text(plainInline(b.text), style = MaterialTheme.typography.bodySmall)
        }
        if (state.blocks.isEmpty() && p.agent.summary.isNotEmpty()) {
            Text(p.agent.summary, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

/** Your last message, the agent's last two messages since and its last two tools, in order (the iPhone's AgentTail). */
private fun agentTail(blocks: List<Hammock.AgentBlock>): List<Hammock.AgentBlock> {
    val K = Hammock.AgentBlock.Kind.KIND_USER
    val all = blocks.filter {
        when (it.kind) {
            Hammock.AgentBlock.Kind.KIND_USER, Hammock.AgentBlock.Kind.KIND_TEXT -> it.text.isNotEmpty()
            Hammock.AgentBlock.Kind.KIND_TOOL -> it.title.isNotEmpty()
            else -> false
        }
    }
    val asked = all.indexOfLast { it.kind == K }
    val since = if (asked >= 0) all.drop(asked + 1) else all
    val words = since.filter { it.kind == Hammock.AgentBlock.Kind.KIND_TEXT }.takeLast(2).map { it.id }.toSet()
    val tools = since.filter { it.kind == Hammock.AgentBlock.Kind.KIND_TOOL }.takeLast(2).map { it.id }.toSet()
    val out = since.filter { it.id in words || it.id in tools }.toMutableList()
    if (asked >= 0) {
        out.add(0, all[asked])
        if (words.isEmpty()) all.subList(0, asked).lastOrNull { it.kind == Hammock.AgentBlock.Kind.KIND_TEXT }?.let { out.add(0, it) }
    }
    return out
}

/** An editor pane's tile: its file, and whether it has unsaved edits. */
@Composable
private fun FileTile(p: Pane) {
    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Bottom) {
        Box(Modifier.weight(1f).fillMaxWidth(), contentAlignment = Alignment.Center) {
            Icon(Icons.AutoMirrored.Filled.Article, null, Modifier.size(28.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.5f))
        }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            if (p.editor.dirty) Box(Modifier.size(6.dp).clip(CircleShape).background(StatusPalette.amber()).semantics { contentDescription = "Edited" })
            Text(p.editor.path.ifEmpty { "Untitled" }.replace(Regex("^/(Users|home)/[^/]+"), "~"), style = MaterialTheme.typography.labelSmall,
                fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

/** A diff pane's tile: a picture of its changes in proportion (the phone has the counts, not the diff), how much, and its review threads. */
@Composable
private fun DiffTile(p: Pane) {
    val d = p.diff
    val green = StatusPalette.green()
    val red = StatusPalette.red()
    val grey = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.3f)
    val lines = remember(d.added, d.removed, d.files) { diffSketch(d.added, d.removed, d.files) }
    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Box(Modifier.weight(1f).fillMaxWidth()) {
            Canvas(Modifier.fillMaxSize()) {
                var y = 0f
                val h = 4.dp.toPx()
                for ((kind, width) in lines) {
                    if (y + h > size.height) break
                    if (kind != 0) drawRect(if (kind > 0) green else red, Offset(0f, y), Size(2.dp.toPx(), h))
                    val x = 6.dp.toPx()
                    drawRoundRect(if (kind > 0) green.copy(alpha = 0.5f) else if (kind < 0) red.copy(alpha = 0.5f) else grey,
                        Offset(x, y), Size((size.width - x) * width, h), CornerRadius(h / 2))
                    y += h + 3.dp.toPx()
                }
            }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            Text(if (d.files == 1) "1 file" else "${d.files} files", style = MaterialTheme.typography.bodySmall)
            Text("+${d.added}", style = MaterialTheme.typography.bodySmall, color = green)
            Text("−${d.removed}", style = MaterialTheme.typography.bodySmall, color = red)
        }
        val threads = listOfNotNull(
            d.openThreads.takeIf { it > 0 }?.let { if (it == 1) "1 thread" else "$it threads" },
            d.unread.takeIf { it > 0 }?.let { "$it new" },
        )
        if (threads.isNotEmpty()) Text(threads.joinToString(" · "), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        else if (d.branch.isNotEmpty()) Text(d.branch, style = MaterialTheme.typography.labelSmall, fontFamily = FontFamily.Monospace,
            color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
    }
}

/** Made-up lines in a diff's proportions (removed -1, added 1, context 0, each with a length), the same every time for the same counts. */
private fun diffSketch(added: Int, removed: Int, files: Int): List<Pair<Int, Float>> {
    var seed = (added * 31 + removed * 17 + files * 7 + 1).toLong()
    fun next(): Float { seed = (seed * 1103515245 + 12345) and 0x7fffffff; return (seed % 1000) / 1000f }
    val total = (added + removed).coerceAtLeast(1)
    val changed = 18
    val plus = (changed * added / total.toFloat()).toInt()
    val out = ArrayList<Pair<Int, Float>>()
    var a = plus
    var r = changed - plus
    if (added + removed == 0) { a = 0; r = 0 }
    repeat(30) {
        val roll = next()
        val kind = when {
            roll < 0.35f -> 0
            a > 0 && (r == 0 || next() < a / (a + r).toFloat()) -> { a--; 1 }
            r > 0 -> { r--; -1 }
            else -> 0
        }
        out += kind to (0.3f + next() * 0.65f)
    }
    return out
}
