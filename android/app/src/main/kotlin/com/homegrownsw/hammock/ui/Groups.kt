package com.homegrownsw.hammock.ui

import android.content.Context
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
import com.homegrownsw.hammock.client.Link
import com.homegrownsw.hammock.client.Workspace
import com.homegrownsw.hammock.client.agentProgress
import com.homegrownsw.hammock.client.canAddTabs
import com.homegrownsw.hammock.client.canDrive
import com.homegrownsw.hammock.client.canSleep
import com.homegrownsw.hammock.client.isOnLinkedComputer
import com.homegrownsw.hammock.client.isRemote
import com.homegrownsw.hammock.client.shownState
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
    var collapsed by remember { mutableStateOf(Collapsed.get(ctx)) }
    var renaming by remember { mutableStateOf<Tab?>(null) }
    var editing by remember { mutableStateOf<Group?>(null) }
    var creating by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf<Group?>(null) }
    var startingIn by remember { mutableStateOf<String?>(null) }
    var computer by remember { mutableStateOf("") }
    val scope = rememberCoroutineScope()

    fun toggle(g: Group) {
        val ids = (if (g.id in collapsed) collapsed - g.id else collapsed + g.id).intersect(w.groups.map { it.id }.toSet())
        collapsed = ids
        Collapsed.set(ctx, ids)
    }
    fun newTab(g: Group) = scope.launch { model.createTab(g.id)?.let { nav.open("tab:$it") } }

    val shown = w.groups.filter { g ->
        when {
            computer.isEmpty() -> true
            computer == w.myUserID -> !g.isOnLinkedComputer
            else -> g.isOnLinkedComputer && g.hostUserId == computer
        }
    }

    CockpitScaffold(session, model, actions = {
        IconButton(onClick = { creating = true }, enabled = link == Link.Connected) { Icon(Icons.Outlined.AddBox, "New Group") }
    }) { padding ->
        if (link == Link.Connected && w.groups.isEmpty()) {
            EmptyState("No Groups", "Groups of tabs on your computer show up here.", Modifier.padding(padding), "New Group") { creating = true }
            return@CockpitScaffold
        }
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(top = padding.calculateTopPadding(), bottom = 24.dp)) {
            if (w.computers.any { !it.thisComputer }) {
                item(key = "computers") { ComputerChips(w, computer) { computer = it } }
            }
            for (g in shown) {
                val isCollapsed = g.id in collapsed
                item(key = "g:" + g.id, contentType = "header") {
                    GroupHeader(w, g, isCollapsed, toggle = { toggle(g) }, newTab = { newTab(g) }, startAgent = { startingIn = g.id },
                        edit = { editing = g }, delete = { deleting = g }, leave = { model.leaveShared(g.id) })
                }
                if (!isCollapsed) {
                    if (g.tabIdsCount == 0) item(key = "empty:" + g.id) {
                        Text("No tabs. Open a terminal or start an agent from the Group's + menu.", Modifier.padding(horizontal = 56.dp, vertical = 8.dp),
                            style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    items(w.tabs(g), key = { "t:" + it.id }) { tab ->
                        val canClose = g.canDrive && !g.offline
                        Swipeable(enabled = canClose, onClose = { model.closeTab(tab.id) }) {
                            TabRow(model, w, g, tab, selected = nav.stack.lastOrNull() == "tab:" + tab.id,
                                open = { nav.open("tab:" + tab.id) }, rename = { renaming = tab }, newTab = { newTab(g) },
                                startAgent = { startingIn = g.id })
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
        AlertDialog(
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
    startingIn?.let { gid -> StartAgentSheet(model, groupID = gid, nav = nav) { startingIn = null } }
}

@Composable
private fun ComputerChips(w: Workspace, selected: String, select: (String) -> Unit) {
    androidx.compose.foundation.lazy.LazyRow(
        contentPadding = PaddingValues(horizontal = 16.dp, vertical = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        item { androidx.compose.material3.FilterChip(selected == "", { select("") }, { Text("All") }) }
        item { androidx.compose.material3.FilterChip(selected == w.myUserID, { select(w.myUserID) }, { Text(w.hostName.removeSuffix(".local").ifEmpty { "This Computer" }) }) }
        items(w.computers.filter { !it.thisComputer }, key = { it.userId }) { c ->
            androidx.compose.material3.FilterChip(selected == c.userId, { select(c.userId) }, { Text(c.name) })
        }
    }
}

@Composable
private fun GroupHeader(
    w: Workspace, g: Group, collapsed: Boolean, toggle: () -> Unit, newTab: () -> Unit, startAgent: () -> Unit,
    edit: () -> Unit, delete: () -> Unit, leave: () -> Unit,
) {
    val angle by animateFloatAsState(if (collapsed) -90f else 0f, label = "chevron")
    var menu by remember { mutableStateOf(false) }
    var plus by remember { mutableStateOf(false) }
    Row(
        Modifier.fillMaxWidth().clickable(onClick = toggle).padding(start = 16.dp, end = 4.dp, top = 12.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Default.ExpandMore, if (collapsed) "Expand" else "Collapse", Modifier.rotate(angle).size(20.dp),
            tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Box(Modifier.padding(horizontal = 8.dp)) { GroupIcon(g, 18) }
        Column(Modifier.weight(1f)) {
            Text(w.title(g), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (g.isRemote && !g.isOnLinkedComputer) {
                val host = w.people[g.hostUserId]?.name ?: "someone"
                Text(if (g.offline) "Shared by $host · offline" else "Shared by $host", style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
        if (g.canAddTabs) Box {
            IconButton(onClick = { plus = true }) { Icon(Icons.Default.Add, "New Tab") }
            DropdownMenu(plus, { plus = false }) {
                DropdownMenuItem(text = { Text("Terminal") }, onClick = { plus = false; newTab() })
                DropdownMenuItem(text = { Text("Agent…") }, onClick = { plus = false; startAgent() })
            }
        }
        Box {
            IconButton(onClick = { menu = true }) { Icon(Icons.Default.MoreVert, "Group Actions") }
            DropdownMenu(menu, { menu = false }) {
                if (g.isRemote && !g.isOnLinkedComputer) {
                    DropdownMenuItem(text = { Text("Leave Group") }, onClick = { menu = false; leave() })
                } else {
                    DropdownMenuItem(text = { Text("Edit Group…") }, onClick = { menu = false; edit() })
                    DropdownMenuItem(text = { Text("Delete Group…", color = ErrorColor) }, onClick = { menu = false; delete() })
                }
            }
        }
    }
}

/** A tab: its state, its name, and its agent's latest. Long-press for its actions. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun TabRow(model: AppModel, w: Workspace, g: Group, tab: Tab, selected: Boolean, open: () -> Unit, rename: () -> Unit,
                   newTab: () -> Unit, startAgent: () -> Unit) {
    var menu by remember { mutableStateOf(false) }
    var moving by remember { mutableStateOf(false) }
    val agent = w.shownAgentPane(tab)
    val state = w.agentState(tab)
    val resting = agent != null && !agent.exited && agent.agent.shownState == AgentState.AGENT_STATE_IDLE && agent.agent.seen
    val schedule = w.panes(tab).firstNotNullOfOrNull { p -> p.scheduleId.takeIf { it.isNotEmpty() }?.let { w.schedules[it] } }
    Box {
        Row(
            Modifier.fillMaxWidth()
                .background(if (selected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent)
                .combinedClickable(onClick = open, onLongClick = { menu = true })
                .padding(start = 44.dp, end = 16.dp, top = 10.dp, bottom = 10.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            StatusIcon(state, resting = resting, asleep = agent?.asleep == true, progress = agent?.agentProgress)
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(5.dp)) {
                    Text(w.title(tab), style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    if (schedule != null) Icon(Icons.Default.Schedule, "Scheduled run of ${schedule.name}", Modifier.size(14.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                val summary = w.panes(tab).firstOrNull { it.hasAgent() }?.agent?.summary.orEmpty()
                if (summary.isNotEmpty()) Text(plainInline(summary), style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        DropdownMenu(menu, { menu = false }) {
            TabActions(model, w, g, tab, close = { menu = false }, rename = rename, newTab = newTab, startAgent = startAgent, move = { moving = true })
        }
        DropdownMenu(moving, { moving = false }) {
            for (other in w.groups.filter { it.id != g.id && it.canAddTabs }) {
                DropdownMenuItem(text = { Text(w.title(other)) }, onClick = { moving = false; model.moveTab(tab.id, other.id, other.tabIdsCount) })
            }
        }
    }
}

/** A tab's actions (long-press on its row, or its screen's menu). */
@Composable
fun TabActions(model: AppModel, w: Workspace, g: Group, tab: Tab, close: () -> Unit, rename: () -> Unit, newTab: () -> Unit,
               startAgent: () -> Unit, move: () -> Unit) {
    val drive = g.canDrive && !g.offline
    val agent = w.shownAgentPane(tab)
    DropdownMenuItem(text = { Text("Rename…") }, enabled = drive, onClick = { close(); rename() })
    if (g.canAddTabs) {
        DropdownMenuItem(text = { Text("New Tab in Group") }, onClick = { close(); newTab() })
        DropdownMenuItem(text = { Text("Start an Agent…") }, onClick = { close(); startAgent() })
    }
    if (drive && w.groups.count { it.canAddTabs } > 1) DropdownMenuItem(text = { Text("Move to Group…") }, onClick = { close(); move() })
    if (agent != null && drive) {
        if (agent.asleep) DropdownMenuItem(text = { Text("Wake Agent") }, onClick = { close(); model.sleepPane(agent.id, wake = true) })
        else if (agent.canSleep) DropdownMenuItem(text = { Text("Put Agent to Sleep") }, onClick = { close(); model.sleepPane(agent.id) })
    }
    HorizontalDivider()
    DropdownMenuItem(text = { Text("Close Tab", color = ErrorColor) }, enabled = drive, onClick = { close(); model.closeTab(tab.id) })
}

/** A one-line text prompt (rename). */
@Composable
fun TextPrompt(title: String, initial: String, label: String, confirm: String = "Save", onDismiss: () -> Unit, onDone: (String) -> Unit) {
    var text by remember { mutableStateOf(initial) }
    AlertDialog(
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
    AlertDialog(
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
