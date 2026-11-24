package com.homegrownsw.hammock.ui

import android.content.Context
import com.homegrownsw.hammock.LocalAppLocked
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.filled.Logout
import androidx.compose.material.icons.filled.AutoAwesome
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.DragHandle
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.zIndex
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
import androidx.compose.material.icons.filled.RemoveCircle
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.LocalContentColor
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.draw.clip
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Schedule
import androidx.compose.material.icons.outlined.AddBox
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.Session
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.restingWithSubagents
import com.homegrownsw.hammock.client.restingWithWork
import com.homegrownsw.hammock.client.Link
import com.homegrownsw.hammock.client.Workspace
import com.homegrownsw.hammock.client.agentProgress
import com.homegrownsw.hammock.client.canAddTabs
import com.homegrownsw.hammock.client.canRemoveWorktree
import com.homegrownsw.hammock.client.shown
import com.homegrownsw.hammock.client.canDrive
import com.homegrownsw.hammock.client.canSleep
import com.homegrownsw.hammock.client.isOnLinkedComputer
import com.homegrownsw.hammock.client.isRemote
import com.homegrownsw.hammock.client.shownState
import com.homegrownsw.hammock.client.waitingOnLimit
import hammock.v1.Hammock
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.Group
import hammock.v1.Hammock.Tab
import kotlinx.coroutines.launch

/** Collapsed Groups: this phone's own, kept between launches. */
private object Collapsed {
    private fun prefs(ctx: Context) = ctx.getSharedPreferences("groups", Context.MODE_PRIVATE)
    fun get(ctx: Context): Set<String> = prefs(ctx).getStringSet("collapsed", emptySet()) ?: emptySet()
    fun set(ctx: Context, ids: Set<String>) = prefs(ctx).edit().putStringSet("collapsed", ids).apply()
}

/** Every Group and tab, as organised on the computer, and organised from here. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun GroupsScreen(session: Session, model: AppModel, nav: Nav) {
    val ctx = LocalContext.current
    val w by model.workspace.collectAsStateWithLifecycle()
    val link by model.link.collectAsStateWithLifecycle()
    val history by model.history.collectAsStateWithLifecycle()
    val closer = LocalCloser.current
    var collapsed by remember { mutableStateOf(Collapsed.get(ctx)) }
    var renaming by remember { mutableStateOf<Tab?>(null) }
    var editing by remember { mutableStateOf<Group?>(null) }
    var creating by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf<Group?>(null) }
    var leaving by remember { mutableStateOf<Group?>(null) }
    var startingIn by remember { mutableStateOf<String?>(null) }
    var computer by remember { mutableStateOf("") }
    val scope = rememberCoroutineScope()

    fun toggle(g: Group) {
        val ids = (if (g.id in collapsed) collapsed - g.id else collapsed + g.id).intersect(w.groups.map { it.id }.toSet())
        collapsed = ids
        Collapsed.set(ctx, ids)
    }
    fun newTab(g: Group) = scope.launch { model.createTab(g.id)?.let { nav.open("tab:$it") } }
    fun newFilesTab(g: Group) = scope.launch { model.createFilesTab(g.id)?.let { nav.open("tab:$it") } }

    val shown = w.groups.filter { g ->
        when {
            computer.isEmpty() -> true
            computer == w.myUserID -> !g.isOnLinkedComputer
            else -> g.isOnLinkedComputer && g.hostUserId == computer
        }
    }

    var editingOrder by rememberSaveable { mutableStateOf(false) }
    // Edit mode: the tab being dragged by its handle, and how far it's gone.
    val listState = rememberLazyListState()
    var dragging by remember { mutableStateOf<String?>(null) }
    var dragBy by remember { mutableFloatStateOf(0f) }
    fun drop(tabs: List<Tab>, g: Group) {
        val id = dragging ?: return
        val rows = listState.layoutInfo.visibleItemsInfo.map { RowSpan(it.key, it.offset, it.size) }
        val to = dropIndex(rows, tabs.map { "t:" + it.id }, "t:$id", dragBy)
        val from = tabs.indexOfFirst { it.id == id }
        dragging = null
        dragBy = 0f
        if (to != null && from >= 0 && to != from) model.moveTab(id, g.id, to)
    }
    CockpitScaffold(session, model, actions = {
        // The iPhone's Edit: reorder and close tabs in place.
        if (w.groups.isNotEmpty()) TextButton(onClick = { editingOrder = !editingOrder }) { Text(if (editingOrder) "Done" else "Edit") }
        IconButton(onClick = { creating = true }, enabled = link == Link.Connected) { Icon(Icons.Outlined.AddBox, "New Group") }
    }) { padding ->
        if (link == Link.Connected && w.groups.isEmpty()) {
            EmptyState("No Groups", "Groups of tabs on your computer show up here.", Modifier.padding(padding), "New Group") { creating = true }
            return@CockpitScaffold
        }
        if (w.groups.isEmpty() && !history.everConnected) {
            LoadingState(Modifier.padding(padding))
            return@CockpitScaffold
        }
        RefreshBox(model, Modifier.padding(top = padding.calculateTopPadding())) {
        LazyColumn(Modifier.fillMaxSize(), state = listState, contentPadding = PaddingValues(bottom = 24.dp)) {
            if (w.computers.any { !it.thisComputer }) {
                item(key = "computers") { ComputerChips(w, computer) { computer = it } }
            }
            for (g in shown) {
                val isCollapsed = g.id in collapsed
                item(key = "g:" + g.id, contentType = "header") {
                    GroupHeader(w, g, isCollapsed, toggle = { toggle(g) }, newTab = { newTab(g) }, newFilesTab = { newFilesTab(g) },
                        startAgent = { startingIn = g.id },
                        edit = { editing = g }, delete = { deleting = g }, leave = { leaving = g }, modifier = Modifier.animateItem())
                }
                if (!isCollapsed) {
                    if (g.tabIdsCount == 0) item(key = "empty:" + g.id) {
                        CardRow(0, 1, modifier = Modifier.animateItem()) {
                            Text(
                                if (g.canAddTabs) "Nothing open here yet." else if (g.offline) "Its computer can't be reached right now."
                                else "Only people who can drive can open tabs here.",
                                Modifier.padding(16.dp), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    val tabs = w.tabs(g)
                    val canClose = g.canDrive && !g.offline && link == Link.Connected
                    cardItems(tabs, key = { "t:" + it.id }, rowModifier = { tab ->
                        if (tab.id == dragging) Modifier.zIndex(1f).graphicsLayer { translationY = dragBy } else Modifier
                    }) { tab ->
                        val i = tabs.indexOf(tab)
                        Swipeable(enabled = canClose && !editingOrder, onSwipe = { closer.closeTab(model, w, tab) }) {
                            TabRow(model, w, g, tab, selected = nav.stack.lastOrNull() == "tab:" + tab.id || tab.id == dragging,
                                open = { nav.open("tab:" + tab.id) }, rename = { renaming = tab }, newTab = { newTab(g) }, newFilesTab = { newFilesTab(g) },
                                startAgent = { startingIn = g.id },
                                editing = if (editingOrder && canClose) TabEditing(
                                    up = if (i > 0) ({ model.moveTab(tab.id, g.id, i - 1) }) else null,
                                    down = if (i < tabs.size - 1) ({ model.moveTab(tab.id, g.id, i + 1) }) else null,
                                    close = { closer.closeTab(model, w, tab) },
                                    dragStart = { dragging = tab.id; dragBy = 0f },
                                    dragBy = { dragBy += it },
                                    dragEnd = { drop(tabs, g) },
                                ) else null)
                        }
                    }
                }
            }
        }
        }
    }

    renaming?.let { t ->
        TextPrompt("Rename Tab", w.title(t), "Name", onDismiss = { renaming = null }) { model.renameTab(t.id, it); renaming = null }
    }
    if (creating) GroupEditor(model, null, onDismiss = { creating = false }) { tab -> creating = false; tab?.let { nav.open("tab:$it") } }
    editing?.let { g -> GroupEditor(model, g, onDismiss = { editing = null }) { editing = null } }
    deleting?.let { g ->
        val n = g.tabIdsCount
        if (!LocalAppLocked.current) AlertDialog(
            onDismissRequest = { deleting = null },
            title = { Text("Delete “${w.title(g)}”?") },
            text = { Text("Everything running in its tabs on your computer is stopped.") },
            confirmButton = {
                TextButton(onClick = { model.closeGroup(g.id); deleting = null }) {
                    Text(if (n == 0) "Delete Group" else "Delete Group and Close ${if (n == 1) "1 Tab" else "$n Tabs"}", color = ErrorColor)
                }
            },
            dismissButton = { TextButton(onClick = { deleting = null }) { Text("Cancel") } },
        )
    }
    leaving?.let { g ->
        if (!LocalAppLocked.current) AlertDialog(
            onDismissRequest = { leaving = null },
            title = { Text("Leave “${w.title(g)}”?") },
            text = { Text("To come back, join it again from its invite link.") },
            confirmButton = {
                TextButton(onClick = { model.leaveShared(g.id); leaving = null }) { Text("Leave Group", color = ErrorColor) }
            },
            dismissButton = { TextButton(onClick = { leaving = null }) { Text("Cancel") } },
        )
    }
    startingIn?.let { gid -> StartAgentSheet(model, groupID = gid, nav = nav) { startingIn = null } }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ComputerChips(w: Workspace, selected: String, select: (String) -> Unit) {
    val me = w.computers.firstOrNull { it.thisComputer }
    androidx.compose.foundation.lazy.LazyRow(
        contentPadding = PaddingValues(horizontal = CardInset, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        item { FilterChip(selected == "", { select("") }, { Text("All") }) }
        item {
            FilterChip(selected == w.myUserID, { select(w.myUserID) }, { Text(w.hostName.removeSuffix(".local").ifEmpty { "This Computer" }) },
                leadingIcon = me?.let { c -> { ComputerIcon(c, 16.dp) } })
        }
        items(w.computers.filter { !it.thisComputer }, key = { it.userId }) { c ->
            FilterChip(selected == c.userId, { select(c.userId) }, { Text(c.name) }, leadingIcon = { ComputerIcon(c, 16.dp) })
        }
    }
}

/**
 * A Group's header, as the iPhone's: its picture and name (which collapse and
 * expand it, the chevron turning), its computer's badge or who shared it,
 * what a collapsed one's agents are doing; then + (a terminal or an agent) and
 * its menu.
 */
@Composable
private fun GroupHeader(
    w: Workspace, g: Group, collapsed: Boolean, toggle: () -> Unit, newTab: () -> Unit, newFilesTab: () -> Unit, startAgent: () -> Unit,
    edit: () -> Unit, delete: () -> Unit, leave: () -> Unit, modifier: Modifier = Modifier,
) {
    val angle by animateFloatAsState(if (collapsed) 0f else 90f, label = "chevron")
    var menu by remember { mutableStateOf(false) }
    var plus by remember { mutableStateOf(false) }
    val quiet = MaterialTheme.colorScheme.onSurfaceVariant
    val linked = if (g.isOnLinkedComputer) w.computers.firstOrNull { it.userId == g.hostUserId } else null
    Row(
        modifier.fillMaxWidth().padding(start = CardInset + 4.dp, end = CardInset - 8.dp, top = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.weight(1f)) { Row(
            Modifier.clip(RoundedCornerShape(10.dp)).clickable(onClick = toggle).padding(vertical = 10.dp, horizontal = 4.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            GroupIcon(g, 24, fallback = false)
            Text(w.title(g), style = MaterialTheme.typography.titleSmall, color = quiet, maxLines = 1, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false))
            if (linked != null) {
                ComputerIcon(linked, 14.dp)
                if (g.offline) Text("asleep", style = MaterialTheme.typography.bodySmall, color = quiet)
            } else if (g.isRemote) {
                val host = w.people[g.hostUserId]?.name ?: "someone"
                Text("· $host" + if (g.offline) " · offline" else "", style = MaterialTheme.typography.bodyMedium, color = quiet, maxLines = 1)
            }
            Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, if (collapsed) "Expand" else "Collapse", Modifier.rotate(angle).size(18.dp),
                tint = quiet.copy(alpha = 0.6f))
            if (collapsed) {
                val states = w.tabs(g).mapNotNull { w.agentState(it) }
                listOf(AgentState.AGENT_STATE_NEEDS_INPUT, AgentState.AGENT_STATE_ERRORED, AgentState.AGENT_STATE_WORKING,
                    AgentState.AGENT_STATE_IDLE, AgentState.AGENT_STATE_STARTING).firstOrNull { it in states }?.let { StatusIcon(it, size = 16.dp) }
                if (g.tabIdsCount > 0) Text("${g.tabIdsCount}", style = MaterialTheme.typography.bodyMedium, color = quiet)
            }
        } }
        val accent = MaterialTheme.colorScheme.primary
        Box {
            IconButton(onClick = { plus = true }, enabled = g.canAddTabs) { Icon(Icons.Default.Add, "New Tab", tint = if (g.canAddTabs) accent else quiet.copy(alpha = 0.4f)) }
            DropdownMenu(plus, { plus = false }) {
                DropdownMenuItem(text = { Text("Terminal") }, leadingIcon = { Icon(Icons.Outlined.Terminal, null) }, onClick = { plus = false; newTab() })
                DropdownMenuItem(text = { Text("Agent…") }, leadingIcon = { Icon(Icons.Default.AutoAwesome, null) }, onClick = { plus = false; startAgent() })
                DropdownMenuItem(text = { Text("Files") }, leadingIcon = { Icon(Icons.Outlined.Folder, null) }, onClick = { plus = false; newFilesTab() })
            }
        }
        Box {
            IconButton(onClick = { menu = true }) { Icon(Icons.Default.MoreVert, "Group Actions", tint = accent) }
            DropdownMenu(menu, { menu = false }) {
                if (g.isRemote && !g.isOnLinkedComputer) {
                    DropdownMenuItem(text = { Text("Leave Group", color = ErrorColor) }, leadingIcon = { Icon(Icons.AutoMirrored.Filled.Logout, null, tint = ErrorColor) },
                        onClick = { menu = false; leave() })
                } else {
                    DropdownMenuItem(text = { Text("Edit Group…") }, leadingIcon = { Icon(Icons.Default.Edit, null) }, onClick = { menu = false; edit() })
                    HorizontalDivider()
                    DropdownMenuItem(text = { Text("Delete Group", color = ErrorColor) }, leadingIcon = { Icon(Icons.Default.Delete, null, tint = ErrorColor) },
                        onClick = { menu = false; delete() })
                }
            }
        }
    }
}

/**
 * Edit mode's controls on a tab's row: close it, or drag its handle to move it within its Group
 * ([dragStart], [dragBy] pixels, [dragEnd] to drop it); [up] and [down] are TalkBack's Move Up and
 * Move Down, null at either end.
 */
class TabEditing(
    val up: (() -> Unit)?, val down: (() -> Unit)?, val close: () -> Unit,
    val dragStart: () -> Unit, val dragBy: (Float) -> Unit, val dragEnd: () -> Unit,
)

/** A row on screen: its key, where it starts and how tall it is, in the list's pixels. */
data class RowSpan(val key: Any, val offset: Int, val size: Int)

/**
 * Where a row dragged [by] pixels lands among [keys] (its Group's tabs, in order): the index of the
 * row its middle is now over, of those on screen, or the first or last when it's beyond them.
 */
fun dropIndex(rows: List<RowSpan>, keys: List<Any>, dragged: Any, by: Float): Int? {
    val me = rows.firstOrNull { it.key == dragged } ?: return null
    val middle = me.offset + me.size / 2f + by
    val mine = rows.filter { it.key in keys }.sortedBy { it.offset }
    val over = mine.firstOrNull { middle < it.offset + it.size } ?: mine.lastOrNull() ?: return null
    return keys.indexOf(over.key)
}

/** A tab: its state (or program), its name, and its agent's latest. Long-press for its actions. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun TabRow(model: AppModel, w: Workspace, g: Group, tab: Tab, selected: Boolean, open: () -> Unit, rename: () -> Unit,
                   newTab: () -> Unit, newFilesTab: () -> Unit, startAgent: () -> Unit, editing: TabEditing? = null) {
    var menu by remember { mutableStateOf(false) }
    var moving by remember { mutableStateOf(false) }
    val agent = w.shownAgentPane(tab)
    val state = w.agentState(tab)
    val resting = agent != null && !agent.exited && agent.agent.shownState == AgentState.AGENT_STATE_IDLE && agent.agent.seen
    val schedule = w.panes(tab).firstNotNullOfOrNull { p -> p.scheduleId.takeIf { it.isNotEmpty() }?.let { w.schedules[it] } }
    val quiet = MaterialTheme.colorScheme.onSurfaceVariant
    Box {
        Row(
            Modifier.fillMaxWidth()
                .background(if (selected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent)
                .combinedClickable(onClick = open, onLongClick = { menu = true })
                .closeAction(LocalSwipeClose.current)
                .moveActions(editing)
                .padding(start = 16.dp, end = 12.dp, top = 11.dp, bottom = 11.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            if (editing != null) {
                IconButton(onClick = editing.close, Modifier.size(24.dp)) { Icon(Icons.Default.RemoveCircle, "Close Tab", tint = ErrorColor) }
            }
            Box(Modifier.size(24.dp), contentAlignment = Alignment.Center) {
                if (state != null || resting || agent?.asleep == true) {
                    StatusIcon(state, resting = resting, shells = agent?.agent?.restingWithWork == true,
                        subagents = agent?.agent?.restingWithSubagents == true, asleep = agent?.asleep == true, progress = agent?.agentProgress, size = 22.dp,
                        modifier = Modifier.staleWhenLinkDown(), waiting = w.panes(tab).any { it.hasAgent() && !it.exited && it.agent.waitingOnLimit })
                } else {
                    CompositionLocalProvider(LocalContentColor provides quiet) {
                        PaneIcon(w.panes[tab.focusedPaneId] ?: w.panes(tab).firstOrNull(), 20.dp)
                    }
                }
            }
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(5.dp)) {
                    Text(w.title(tab), style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false))
                    if (tab.hasGit() && tab.git.shown) GitMark(tab.git, quiet)
                    if (schedule != null) Icon(Icons.Default.Schedule, "Scheduled run of ${schedule.name}", Modifier.size(14.dp),
                        tint = quiet.copy(alpha = 0.6f))
                }
                val summary = w.panes(tab).firstOrNull { it.hasAgent() }?.agent?.summary.orEmpty()
                if (summary.isNotEmpty()) Text(plainInline(summary), style = MaterialTheme.typography.bodySmall,
                    color = quiet, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (editing != null) {
                DragHandle(editing)
            } else {
                Chevron()
            }
        }
        DropdownMenu(menu, { menu = false }) {
            TabActions(model, w, g, tab, close = { menu = false }, rename = rename, newTab = newTab, newFilesTab = newFilesTab, startAgent = startAgent,
                move = { moving = true })
        }
        DropdownMenu(moving, { moving = false }) {
            for (other in w.groups.filter { it.id != g.id && it.canAddTabs }) {
                DropdownMenuItem(text = { Text(w.title(other)) }, onClick = { moving = false; model.moveTab(tab.id, other.id, other.tabIdsCount) })
            }
        }
    }
}

/** Edit mode's handle: drag it up or down to move the tab. TalkBack moves it with the row's actions instead. */
@Composable
private fun DragHandle(editing: TabEditing) {
    val e by rememberUpdatedState(editing)
    Box(
        Modifier.size(40.dp).clearAndSetSemantics {}.pointerInput(Unit) {
            detectVerticalDragGestures(
                onDragStart = { e.dragStart() },
                onDragEnd = { e.dragEnd() },
                onDragCancel = { e.dragEnd() },
            ) { change, dy ->
                change.consume()
                e.dragBy(dy)
            }
        },
        contentAlignment = Alignment.Center,
    ) { Icon(Icons.Default.DragHandle, null, tint = MaterialTheme.colorScheme.onSurfaceVariant) }
}

/** TalkBack's Move Up and Move Down for a tab's row in Edit mode, where it can go that way. */
private fun Modifier.moveActions(editing: TabEditing?): Modifier {
    if (editing == null) return this
    val actions = listOfNotNull(
        editing.up?.let { up -> CustomAccessibilityAction("Move Up") { up(); true } },
        editing.down?.let { down -> CustomAccessibilityAction("Move Down") { down(); true } },
    )
    return if (actions.isEmpty()) this else semantics { customActions = actions }
}

/** A tab's actions (long-press on its row, or its screen's menu). */
@Composable
fun TabActions(model: AppModel, w: Workspace, g: Group, tab: Tab, close: () -> Unit, rename: () -> Unit, newTab: () -> Unit,
               newFilesTab: () -> Unit, startAgent: () -> Unit, move: () -> Unit) {
    val drive = g.canDrive && !g.offline
    val agent = w.shownAgentPane(tab)
    val closer = LocalCloser.current
    DropdownMenuItem(text = { Text("Rename…") }, enabled = drive, onClick = { close(); rename() })
    if (g.canAddTabs) {
        DropdownMenuItem(text = { Text("New Tab in Group") }, onClick = { close(); newTab() })
        DropdownMenuItem(text = { Text("Start an Agent…") }, onClick = { close(); startAgent() })
        DropdownMenuItem(text = { Text("New Files Tab") }, onClick = { close(); newFilesTab() })
    }
    if (drive && w.groups.count { it.canAddTabs } > 1) DropdownMenuItem(text = { Text("Move to Group…") }, onClick = { close(); move() })
    if (agent != null && drive) {
        if (agent.asleep) DropdownMenuItem(text = { Text("Wake Agent") }, onClick = { close(); model.sleepPane(agent.id, wake = true) })
        else if (agent.canSleep) DropdownMenuItem(text = { Text("Put Agent to Sleep") }, onClick = { close(); model.sleepPane(agent.id) })
    }
    HorizontalDivider()
    if (canRemoveWorktree(tab, g)) DropdownMenuItem(text = { Text("Remove Worktree…", color = ErrorColor) }, onClick = { close(); closer.removeWorktree(model, w, tab) })
    DropdownMenuItem(text = { Text("Close Tab", color = ErrorColor) }, enabled = drive, onClick = { close(); closer.closeTab(model, w, tab) })
}

/** A one-line text prompt (rename). */
@Composable
fun TextPrompt(title: String, initial: String, label: String, confirm: String = "Save", onDismiss: () -> Unit, onDone: (String) -> Unit) {
    var text by remember { mutableStateOf(initial) }
    if (!LocalAppLocked.current) AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { OutlinedTextField(text, { text = it }, label = { Text(label) }, singleLine = true) },
        confirmButton = { TextButton(onClick = { onDone(text.trim()) }) { Text(confirm) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

/** A new Group (with a first tab), or one's name and emoji. */
@Composable
fun GroupEditor(model: AppModel, group: Group?, onDismiss: () -> Unit, onDone: (tabID: String?) -> Unit) {
    val w by model.workspace.collectAsStateWithLifecycle()
    var name by remember { mutableStateOf(group?.name ?: "") }
    var icon by remember { mutableStateOf(group?.icon ?: "") }
    val scope = rememberCoroutineScope()
    if (!LocalAppLocked.current) AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (group == null) "New Group" else "Edit Group") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                OutlinedTextField(name, { name = it }, label = { Text("Name") }, singleLine = true,
                    placeholder = { Text(group?.let { w.title(it) } ?: "Named after its folder") })
                OutlinedTextField(icon, { v -> icon = v.trim().let { s -> if (s.isEmpty()) "" else firstGrapheme(s) } }, label = { Text("Emoji") },
                    singleLine = true, placeholder = { Text("None") })
            }
        },
        confirmButton = {
            TextButton(onClick = {
                scope.launch {
                    if (group == null) {
                        val id = model.createGroup(name.trim())
                        if (id != null) {
                            if (icon.isNotEmpty()) model.setGroupIcon(id, icon)
                            onDone(model.createTab(id))
                        } else onDone(null)
                    } else {
                        if (name.trim() != group.name) model.renameGroup(group.id, name.trim())
                        if (icon != group.icon) model.setGroupIcon(group.id, icon)
                        onDone(null)
                    }
                }
            }) { Text(if (group == null) "Create" else "Save") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

private fun firstGrapheme(s: String): String {
    val it = java.text.BreakIterator.getCharacterInstance()
    it.setText(s)
    val end = it.next()
    return if (end > 0) s.substring(0, end) else s
}
