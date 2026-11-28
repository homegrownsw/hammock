package com.homegrownsw.hammock.ui

import com.homegrownsw.hammock.LocalAppLocked
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.History
import androidx.compose.material.icons.filled.PanTool
import androidx.compose.material.icons.filled.Schedule
import androidx.compose.material.icons.filled.ViewAgenda
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.Session
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.restingWithSubagents
import com.homegrownsw.hammock.client.restingWithWork
import com.homegrownsw.hammock.client.Link
import com.homegrownsw.hammock.client.Workspace
import com.homegrownsw.hammock.client.PromptPreview
import com.homegrownsw.hammock.client.agentProgress
import com.homegrownsw.hammock.client.allowsFromCard
import com.homegrownsw.hammock.client.promptPreview
import com.homegrownsw.hammock.client.agentStatusLabel
import com.homegrownsw.hammock.client.limitResumeLabel
import com.homegrownsw.hammock.client.agentNote
import com.homegrownsw.hammock.client.canAllowAlways
import com.homegrownsw.hammock.client.canAddTabs
import com.homegrownsw.hammock.client.canDrive
import com.homegrownsw.hammock.client.displayName
import com.homegrownsw.hammock.client.elapsedString
import com.homegrownsw.hammock.client.hasDetails
import com.homegrownsw.hammock.client.titleText
import com.homegrownsw.hammock.agentview.GrantDetails
import com.homegrownsw.hammock.client.label
import com.homegrownsw.hammock.client.originText
import com.homegrownsw.hammock.client.shownState
import com.homegrownsw.hammock.client.waitingOnLimit
import hammock.v1.Hammock
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.Pane
import hammock.v1.Hammock.Tab
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.drop

/** A tab with an agent, as its most pressing agent pane. */
data class SessionRow(val tab: Tab, val pane: Pane)

/** Most pressing first: needs you, working, finished and not yet looked at, then at rest. */
data class SessionRows(val needsYou: List<SessionRow>, val working: List<SessionRow>, val finished: List<SessionRow>, val recent: List<SessionRow>) {
    val isEmpty get() = needsYou.isEmpty() && working.isEmpty() && finished.isEmpty() && recent.isEmpty()

    companion object {
        private val rank = mapOf(
            AgentState.AGENT_STATE_NEEDS_INPUT to 0, AgentState.AGENT_STATE_ERRORED to 1,
            AgentState.AGENT_STATE_WORKING to 2, AgentState.AGENT_STATE_STARTING to 2, AgentState.AGENT_STATE_IDLE to 3,
        )

        fun of(w: Workspace): SessionRows {
            val needs = ArrayList<SessionRow>()
            val working = ArrayList<SessionRow>()
            val finished = ArrayList<SessionRow>()
            val recent = ArrayList<SessionRow>()
            for (tab in w.orderedTabs) {
                val p = w.panes(tab).filter { it.hasAgent() && !it.exited && rank[it.agent.shownState] != null }
                    .minByOrNull { rank[it.agent.shownState]!! } ?: continue
                val row = SessionRow(tab, p)
                when (p.agent.shownState) {
                    AgentState.AGENT_STATE_NEEDS_INPUT, AgentState.AGENT_STATE_ERRORED -> needs += row
                    AgentState.AGENT_STATE_WORKING, AgentState.AGENT_STATE_STARTING -> working += row
                    // Waiting out a usage limit is at rest, seen or not.
                    else -> if (!p.agent.seen && !p.agent.waitingOnLimit) finished += row else recent += row
                }
            }
            val newest = compareByDescending<SessionRow> { it.pane.agent.stateSinceUnixMs }
            return SessionRows(needs.sortedWith(newest), working.sortedWith(newest), finished.sortedWith(newest), recent.sortedWith(newest))
        }
    }
}

/** A minute-ish clock, for "3m ago" texts. */
@Composable
fun rememberNow(periodMs: Long = 30_000): Long {
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(periodMs) {
        while (true) {
            delay(periodMs)
            now = System.currentTimeMillis()
        }
    }
    return now
}

/** Agents by urgency: what needs you, scheduled runs ready, what's working, what finished. */
@Composable
fun SessionsScreen(session: Session, model: AppModel, nav: Nav) {
    val w by model.workspace.collectAsStateWithLifecycle()
    val link by model.link.collectAsStateWithLifecycle()
    val history by model.history.collectAsStateWithLifecycle()
    val now = rememberNow()
    var showingAllRecent by rememberSaveable { mutableStateOf(false) }
    var finding by remember { mutableStateOf(false) }
    var starting by remember { mutableStateOf(false) }
    var closing by remember { mutableStateOf<Pane?>(null) }
    val rows = SessionRows.of(w)
    val ready = readyRuns(w, now)
    val waiting = w.attention.filter { it.state == AgentState.AGENT_STATE_NEEDS_INPUT || it.state == AgentState.AGENT_STATE_ERRORED }
    val readyPanes = ready.map { it.second.paneId }.toSet()
    val others = rows.needsYou.filter { r -> waiting.none { it.paneId == r.pane.id } }

    fun close(p: Pane) {
        val tab = w.tabs[p.tabId]
        if (tab != null && w.panes(tab).size == 1) model.closeTab(tab.id) else model.closePane(p.id)
    }
    val closer = LocalCloser.current
    fun requestClose(p: Pane) {
        if (closer.warning(listOf(p)) != null || (w.tabs[p.tabId]?.let { w.panes(it).size } ?: 1) > 1) closing = p else close(p)
    }
    fun canClose(p: Pane) = link == Link.Connected && w.groupOfPane(p.id)?.let { it.canDrive && !it.offline } ?: false

    CockpitScaffold(session, model, actions = {
        IconButton(onClick = { finding = true }, enabled = link == Link.Connected) { Icon(Icons.Default.History, "Find a Session") }
        IconButton(onClick = { starting = true }, enabled = link == Link.Connected && w.groups.any { it.canAddTabs }) {
            Icon(Icons.Default.Add, "Start an Agent")
        }
    }) { padding ->
        val empty = link == Link.Connected && w.groups.isNotEmpty() && w.grants.isEmpty() && rows.isEmpty && w.schedules.isEmpty() && w.joinRequests.isEmpty()
        if (empty) {
            EmptyState(
                "No Agent Sessions", "Agents running on your computer show up here.",
                Modifier.padding(padding), action = "Start an Agent…",
            ) { starting = true }
            return@CockpitScaffold
        }
        if (w.groups.isEmpty() && !history.everConnected) {
            LoadingState(Modifier.padding(padding))
            return@CockpitScaffold
        }
        RefreshBox(model, Modifier.padding(top = padding.calculateTopPadding())) {
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
            if (w.grants.isNotEmpty() || waiting.isNotEmpty() || others.isNotEmpty() || w.joinRequests.isNotEmpty()) {
                section("Needs You")
                // Each its own card, as the iPhone's: their buttons need the room.
                items(w.joinRequests, key = { "join:" + it.id }) { Box(Modifier.animateItem()) { JoinCard(model, w, it) } }
                items(w.grants, key = { "grant:" + it.id }) { g ->
                    Box(Modifier.animateItem()) {
                        // The pane shows the whole request, in either view.
                        GrantCard(model, g, open = w.panes[g.requesterPaneId]?.let { p -> { nav.open("pane:" + p.id) } })
                    }
                }
                items(waiting, key = { "att:" + it.id }) { item ->
                    val p = w.panes[item.paneId]
                    Card(padded = false, modifier = Modifier.animateItem()) {
                        Swipeable(enabled = p != null && canClose(p), onSwipe = { p?.let(::requestClose) }) {
                            AttentionCard(model, w, item, nav) { nav.open("pane:" + item.paneId) }
                        }
                    }
                }
                cardItems(others, key = { "need:" + it.tab.id }) { r -> SessionLink(model, w, r, nav, ::canClose, ::requestClose, now) }
            }
            if (ready.isNotEmpty()) {
                section("Ready for You")
                cardItems(ready, key = { "ready:" + it.second.id }) { (s, run) -> ReadyRunRow(w, s, run, now) { openRun(nav, w, s, run, model) } }
            }
            val working = rows.working.filter { it.pane.id !in readyPanes }
            if (working.isNotEmpty()) {
                section("Working")
                cardItems(working, key = { "work:" + it.tab.id }) { r -> SessionLink(model, w, r, nav, ::canClose, ::requestClose, now) }
            }
            val finished = rows.finished.filter { it.pane.id !in readyPanes }
            if (finished.isNotEmpty()) {
                section("Finished")
                cardItems(finished, key = { "done:" + it.tab.id }) { r -> SessionLink(model, w, r, nav, ::canClose, ::requestClose, now) }
            }
            val recentAll = rows.recent.filter { it.pane.id !in readyPanes }
            if (recentAll.isNotEmpty()) {
                section("Recent")
                // The last 48 hours', then the rest on asking (all of them when none are that new).
                val cutoff = now - 48 * 3600 * 1000L
                val fresh = recentAll.filter { it.pane.agent.stateSinceUnixMs >= cutoff }
                val shown = if (showingAllRecent || fresh.isEmpty()) recentAll else fresh
                val more = fresh.isNotEmpty() && fresh.size < recentAll.size
                val n = shown.size + if (more) 1 else 0
                shown.forEachIndexed { i, r ->
                    item(key = "recent:" + r.tab.id) { CardRow(i, n, modifier = Modifier.animateItem()) { SessionLink(model, w, r, nav, ::canClose, ::requestClose, now) } }
                }
                if (more) {
                    item(key = "more") {
                        CardRow(n - 1, n, modifier = Modifier.animateItem()) {
                            TextButton(onClick = { showingAllRecent = !showingAllRecent }, Modifier.padding(horizontal = 4.dp)) {
                                Text(if (showingAllRecent) "Show Less" else "Show More…")
                            }
                        }
                    }
                }
            }
            if (w.schedules.isNotEmpty()) comingUp(model, w, nav, now)
            usageLimits(w, now)
        }
        }
    }
    closing?.let { p ->
        val title = w.tabs[p.tabId]?.let { w.title(it) } ?: p.displayName
        if (!LocalAppLocked.current) AlertDialog(
            onDismissRequest = { closing = null },
            title = { Text("Close “$title”?") },
            text = { Text(sessionCloseNote(w, p, closer.warning(listOf(p)))) },
            confirmButton = { TextButton(onClick = { close(p); closing = null }) { Text("Close Session", color = ErrorColor) } },
            dismissButton = { TextButton(onClick = { closing = null }) { Text("Cancel") } },
        )
    }
    if (finding) SessionFinderSheet(model, nav) { finding = false }
    if (starting) StartAgentSheet(model, groupID = null, nav = nav) { starting = false }
}

private fun sessionCloseNote(w: Workspace, p: Pane, warning: String?): String {
    val others = w.tabs[p.tabId]?.let { w.panes(it).size - 1 } ?: 0
    return warning ?: if (others > 0) "Only the agent's pane closes; the rest of its tab keeps running." else ""
}

/**
 * What a Swipeable's swipe does, for its row to offer TalkBack, which can't
 * swipe (closeAction): null outside one, or where it can't close.
 */
val LocalSwipeClose = compositionLocalOf<(() -> Unit)?> { null }

/**
 * TalkBack's Close action, doing what swiping the row does (asking first, as
 * the swipe does, when it would stop an agent at work). On the row's own
 * clickable node: TalkBack only offers the actions of the node it's on.
 */
fun Modifier.closeAction(close: (() -> Unit)?): Modifier =
    if (close == null) this else semantics { customActions = listOf(CustomAccessibilityAction("Close") { close(); true }) }

/** What a row's swipe left does when it isn't closing it (a schedule's Pause): its name, icon and colour. */
class SwipeAction(val label: String, val icon: ImageVector, val color: Color)

/**
 * Swipe left to close, where you can (your own Groups, or ones you drive), or to do [action]
 * instead. The row springs back either way.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun Swipeable(enabled: Boolean, onSwipe: () -> Unit, action: SwipeAction? = null, content: @Composable () -> Unit) {
    if (!enabled) return content()
    val state = rememberSwipeToDismissBoxState(confirmValueChange = {
        if (it == SwipeToDismissBoxValue.EndToStart) onSwipe()
        false // the row goes when the computer says it's closed, or stays when it asks first
    })
    // A tap as the swipe passes the point where letting go closes (not on
    // the way back: it springs back after every close, asked first or not).
    val view = LocalView.current
    LaunchedEffect(state) {
        snapshotFlow { state.targetValue == SwipeToDismissBoxValue.EndToStart }
            .drop(1)
            .collect { if (it) view.hapticThreshold() }
    }
    SwipeToDismissBox(
        state = state,
        enableDismissFromStartToEnd = false,
        backgroundContent = {
            // Only while it's swiped: drawn at rest, it shows round a card's corners.
            if (state.dismissDirection == SwipeToDismissBoxValue.EndToStart) {
                Box(Modifier.fillMaxSize().background(action?.color ?: ErrorColor).padding(horizontal = 24.dp), contentAlignment = Alignment.CenterEnd) {
                    Icon(action?.icon ?: Icons.Default.Close, action?.label ?: "Close", tint = Color.White)
                }
            }
        },
    ) {
        Box(Modifier.background(cardColor())) {
            // The row's TalkBack Close (closeAction); another action's row offers its own.
            if (action == null) CompositionLocalProvider(LocalSwipeClose provides onSwipe) { content() } else content()
        }
    }
}

@Composable
private fun SessionLink(
    model: AppModel, w: Workspace, r: SessionRow, nav: Nav,
    canClose: (Pane) -> Boolean, close: (Pane) -> Unit, now: Long,
) {
    var menu by remember { mutableStateOf(false) }
    Box {
        Swipeable(enabled = canClose(r.pane), onSwipe = { close(r.pane) }) {
            SessionRowView(w, r, now, selected = nav.stack.lastOrNull() == "pane:" + r.pane.id, onLongClick = { menu = true }) { nav.open("pane:" + r.pane.id) }
        }
        SessionHoldMenu(model, w, r.pane.id, nav, menu) { menu = false }
    }
}

/** An answer to an agent's permission prompt, from a session's touch-and-hold menu. */
enum class PromptAnswer(val label: String, val decision: Hammock.AgentAnswer.Decision) {
    ALLOW("Allow", Hammock.AgentAnswer.Decision.DECISION_ALLOW),
    ALLOW_SCHEDULE("Always Allow for This Automation", Hammock.AgentAnswer.Decision.DECISION_ALLOW_SCHEDULE),
    DENY("Deny", Hammock.AgentAnswer.Decision.DECISION_DENY),
}

/**
 * The answers a session's touch-and-hold menu offers its agent's prompt, top first, as its card
 * does: none for a question or nothing asked, Deny alone when the card can't show all of it (open
 * the pane to see it first), and Always Allow for This Schedule too for a scheduled Claude run.
 */
fun promptAnswers(item: Hammock.AttentionItem?, scheduledClaude: Boolean): List<PromptAnswer> = when {
    item == null || item.state != AgentState.AGENT_STATE_NEEDS_INPUT || item.question -> emptyList()
    !item.allowsFromCard -> listOf(PromptAnswer.DENY)
    scheduledClaude -> listOf(PromptAnswer.ALLOW, PromptAnswer.ALLOW_SCHEDULE, PromptAnswer.DENY)
    else -> listOf(PromptAnswer.ALLOW, PromptAnswer.DENY)
}

/**
 * A session's touch-and-hold menu: Allow and Deny first while its agent waits on a permission
 * prompt, then the pane's own actions.
 */
@Composable
private fun SessionHoldMenu(model: AppModel, w: Workspace, paneID: String, nav: Nav, open: Boolean, dismiss: () -> Unit) {
    val view = LocalView.current
    val pane = w.panes[paneID] ?: return
    val drive = w.groupOfPane(paneID)?.canDrive ?: false
    val item = w.attention.firstOrNull { it.paneId == paneID && it.state == AgentState.AGENT_STATE_NEEDS_INPUT && !it.question }
    val answers = promptAnswers(item, scheduledClaude = w.schedules[pane.scheduleId]?.kind == "claude")
    val enabled = drive && !linkDown()
    DropdownMenu(open, dismiss) {
        if (item != null && answers.isNotEmpty()) {
            for (a in answers) {
                DropdownMenuItem(text = { Text(a.label) }, enabled = enabled, onClick = {
                    dismiss()
                    if (a == PromptAnswer.DENY) view.hapticReject() else view.hapticConfirm()
                    model.decide(item, a.decision)
                })
            }
            HorizontalDivider()
        }
        PaneMenuItems(model, w, pane, drive, nav, close = dismiss, popAfterClose = false)
    }
}

/**
 * A session as a messages app shows a conversation: its name and when, its
 * latest (the agent's reply, what it's doing, or what it's asking), then
 * quietly its Group.
 */
@Composable
@OptIn(ExperimentalFoundationApi::class)
fun SessionRowView(w: Workspace, r: SessionRow, now: Long, selected: Boolean = false, onLongClick: (() -> Unit)? = null, onClick: () -> Unit) {
    val a = r.pane.agent
    val idleSeen = a.shownState == AgentState.AGENT_STATE_IDLE && a.seen
    Row(
        Modifier.fillMaxWidth()
            .background(if (selected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent)
            .combinedClickable(onClick = onClick, onLongClick = onLongClick, onLongClickLabel = onLongClick?.let { "Actions" })
            .closeAction(LocalSwipeClose.current)
            .padding(start = 16.dp, end = 12.dp, top = 12.dp, bottom = 12.dp),
        horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically,
    ) {
        StatusIcon(if (idleSeen) null else a.shownState, resting = idleSeen, shells = idleSeen && a.restingWithWork,
            subagents = idleSeen && a.restingWithSubagents, asleep = r.pane.asleep, progress = r.pane.agentProgress,
            size = 22.dp, modifier = Modifier.align(Alignment.Top).padding(top = 1.dp).staleWhenLinkDown(), waiting = a.waitingOnLimit)
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(w.title(r.tab), style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f))
                Spacer(Modifier.width(8.dp))
                Text(elapsedString(a.stateSinceUnixMs, now), style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.staleWhenLinkDown())
            }
            Text(
                plainInline(a.summary.ifEmpty { r.pane.agentStatusLabel }), style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis,
            )
            // Stopped at its usage limit: when it carries on by itself.
            r.pane.limitResumeLabel()?.let {
                Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
            }
            w.groupOf(r.tab)?.let { g ->
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(5.dp)) {
                    GroupIcon(g, 14)
                    Text(w.title(g), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
                }
            }
        }
        Chevron()
    }
}

/** Markdown's inline marks taken out, for a one-line summary. */
fun plainInline(s: String): String = s.replace(Regex("\\*\\*|__|`"), "").replace(Regex("\\[([^]]+)]\\([^)]+\\)"), "$1")


/** A card: the Needs You items stand out from plain rows. */
@Composable
fun Card(padded: Boolean = true, modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Box(
        modifier.fillMaxWidth().padding(horizontal = CardInset, vertical = 5.dp)
            .clip(RoundedCornerShape(20.dp)).background(cardColor())
            .then(if (padded) Modifier.padding(16.dp) else Modifier),
    ) { content() }
}

/** An agent waiting on you, with Allow / Deny right there for a permission prompt. */
@Composable
@OptIn(ExperimentalFoundationApi::class)
fun AttentionCard(model: AppModel, w: Workspace, item: Hammock.AttentionItem, nav: Nav, open: () -> Unit) {
    val view = LocalView.current
    val pane = w.panes[item.paneId]
    val schedule = pane?.scheduleId?.let { w.schedules[it] }
    var menu by remember { mutableStateOf(false) }
    Box(Modifier.combinedClickable(onClick = open, onLongClick = { menu = true }, onLongClickLabel = "Actions").closeAction(LocalSwipeClose.current).padding(16.dp)) {
        // Touch and hold: Allow and Deny, then the pane's actions, as a session row's.
        SessionHoldMenu(model, w, item.paneId, nav, menu) { menu = false }
        run {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    StatusIcon(item.state, size = 22.dp, modifier = Modifier.padding(top = 1.dp))
                    Column(Modifier.weight(1f)) {
                        Text(pane?.let { w.tabs[it.tabId] }?.let { w.title(it) } ?: "Agent", style = MaterialTheme.typography.titleMedium)
                        if (schedule != null) {
                            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                                Icon(Icons.Default.Schedule, null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                                Text("Automation · ${schedule.name}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                        Text(plainInline(item.summary.ifEmpty { item.state.label }), style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                item.promptPreview?.let { PromptPreviewBlock(it) }
                if (item.state == AgentState.AGENT_STATE_NEEDS_INPUT) {
                    if (item.question) {
                        Row { Spacer(Modifier.weight(1f)); Button(onClick = open) { Text("Answer") } }
                    } else {
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                            FilledTonalButton(colors = tintedButtonColors(), enabled = !linkDown(), onClick = { view.hapticReject(); model.decide(item, Hammock.AgentAnswer.Decision.DECISION_DENY) }) { Text("Deny") }
                            if (item.allowsFromCard) FilledTonalButton(colors = tintedButtonColors(), onClick = open) { Text("Open") }
                            Spacer(Modifier.weight(1f))
                            // More than the card shows: see all of it in the pane first.
                            if (!item.allowsFromCard) Button(onClick = open) { Text("Review") }
                            else if (schedule != null && schedule.kind == "claude") AllowMenu(model, item)
                            else Button(enabled = !linkDown(), onClick = { view.hapticConfirm(); model.decide(item, Hammock.AgentAnswer.Decision.DECISION_ALLOW) }) { Text("Allow") }
                        }
                    }
                }
            }
        }
    }
}

/** A Needs You card's prompt: its text in a monospaced block, and how much was left out. */
@Composable
private fun PromptPreviewBlock(preview: PromptPreview) {
    Column(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(6.dp)).background(MaterialTheme.colorScheme.surfaceVariant).padding(8.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Text(preview.text, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace)
        preview.moreLabel?.let {
            Text(it, style = MaterialTheme.typography.bodySmall, fontWeight = FontWeight.SemiBold, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
private fun AllowMenu(model: AppModel, item: Hammock.AttentionItem) {
    val view = LocalView.current
    var open by remember { mutableStateOf(false) }
    Box {
        Button(enabled = !linkDown(), onClick = { open = true }) {
            Text("Allow")
            Icon(Icons.Default.KeyboardArrowDown, "Allow options", Modifier.padding(start = 2.dp).size(18.dp))
        }
        DropdownMenu(open, { open = false }) {
            DropdownMenuItem(text = { Text("Allow Once") }, onClick = { open = false; view.hapticConfirm(); model.decide(item, Hammock.AgentAnswer.Decision.DECISION_ALLOW) })
            DropdownMenuItem(text = { Text("Always Allow for This Automation") },
                onClick = { open = false; view.hapticConfirm(); model.decide(item, Hammock.AgentAnswer.Decision.DECISION_ALLOW_SCHEDULE) })
        }
    }
}

/** An agent asking for permission to do something beyond its Group; tapped, [open] shows the pane that asked. */
@Composable
fun GrantCard(model: AppModel, grant: Hammock.GrantRequest, open: (() -> Unit)? = null) {
    val view = LocalView.current
    Card(padded = false) {
        Box(Modifier.then(if (open != null) Modifier.clickable(onClick = open) else Modifier).padding(16.dp)) {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    StatusIcon(AgentState.AGENT_STATE_NEEDS_INPUT, size = 22.dp)
                    Text(grant.titleText, style = MaterialTheme.typography.titleMedium, maxLines = if (grant.person || (grant.action == "schedule" && !grant.hasDetails)) 20 else 4)
                }
                if (grant.hasDetails) GrantDetails(grant.detailsList, compact = true)
                grant.originText?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3) }
                grant.agentNote?.let { Text("The agent says: “$it”", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 3) }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    FilledTonalButton(colors = tintedButtonColors(), enabled = !linkDown(), onClick = { view.hapticReject(); model.respond(grant, Hammock.RespondGrant.Decision.DECISION_DENY) }) { Text("Deny") }
                    Spacer(Modifier.weight(1f))
                    if (grant.canAllowAlways) FilledTonalButton(colors = tintedButtonColors(), enabled = !linkDown(), onClick = { view.hapticConfirm(); model.respond(grant, Hammock.RespondGrant.Decision.DECISION_ALLOW_GROUP) }) { Text("Always") }
                    Button(enabled = !linkDown(), onClick = { view.hapticConfirm(); model.respond(grant, Hammock.RespondGrant.Decision.DECISION_ALLOW_ONCE) }) { Text("Allow") }
                }
            }
        }
    }
}

/** Someone asking to join a Group you share. */
@Composable
fun JoinCard(model: AppModel, w: Workspace, r: Hammock.JoinRequest) {
    val view = LocalView.current
    val who = w.people[r.userId]
    val group = w.groups.firstOrNull { it.id == r.groupId }
    Card {
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("${who?.name?.ifEmpty { null } ?: "Someone"} wants to join", style = MaterialTheme.typography.titleMedium)
            Text(listOfNotNull(group?.let { w.title(it) }, who?.deviceName).filter { it.isNotEmpty() }.joinToString(" · "),
                style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            if (r.keyFingerprint.isNotEmpty()) Text("Key ${r.keyFingerprint}", fontFamily = FontFamily.Monospace,
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            val warning = when {
                r.unlinkedUnixMs > 0 -> "A computer you unlinked from your computers"
                r.nameHasOtherKey -> "${who?.name ?: "This person"} usually joins with a different key"
                r.newKey -> "First time this key has asked to join"
                else -> null
            }
            warning?.let {
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Default.Warning, null, tint = NeedsYouColor, modifier = Modifier.size(16.dp))
                    Text(it, color = NeedsYouColor, style = MaterialTheme.typography.bodyMedium)
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FilledTonalButton(colors = tintedButtonColors(), enabled = !linkDown(), onClick = { view.hapticReject(); model.respondJoin(r, Hammock.Role.ROLE_UNSPECIFIED) }) { Text("Decline") }
                Spacer(Modifier.weight(1f))
                FilledTonalButton(colors = tintedButtonColors(), enabled = !linkDown(), onClick = { view.hapticConfirm(); model.respondJoin(r, Hammock.Role.ROLE_VIEWER) }) { Text("Let View") }
                Button(enabled = !linkDown(), onClick = { view.hapticConfirm(); model.respondJoin(r, Hammock.Role.ROLE_DRIVER) }) { Text("Let Drive") }
            }
        }
    }
}

@Composable
fun EmptyState(title: String, text: String, modifier: Modifier = Modifier, action: String? = null, onAction: () -> Unit = {}) {
    Column(
        modifier.fillMaxSize().padding(32.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(title, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
        Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (action != null) Button(onClick = onAction, Modifier.padding(top = 8.dp)) { Text(action) }
    }
}
