package com.homegrownsw.hammock.ui

import com.homegrownsw.hammock.LocalAppLocked
import androidx.compose.foundation.clickable
import androidx.compose.foundation.background
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.ArrowUpward
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Warning
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.canAddTabs
import com.homegrownsw.hammock.client.elapsedString
import com.homegrownsw.hammock.client.isRemote
import hammock.v1.Hammock
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private fun shortPath(p: String) = p.replace(Regex("^/(Users|home)/[^/]+"), "~")

/**
 * Start an Agent: which agent, where (a Group, a folder), and an optional
 * first message. It starts in a new tab in the folder's login shell.
 */
@OptIn(ExperimentalMaterial3Api::class, androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
@Composable
fun StartAgentSheet(model: AppModel, groupID: String?, nav: Nav, onDone: () -> Unit) {
    val w by model.workspace.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    val groups = w.groups.filter { it.canAddTabs && !it.isRemote }
    var group by remember { mutableStateOf(groupID ?: "") }
    var harness by remember { mutableStateOf("claude") }
    var cwd by remember { mutableStateOf(groupID?.let { id -> w.groups.firstOrNull { it.id == id }?.let { g -> g.folder.ifEmpty { w.panes[g.lastFocusedPaneId]?.cwd ?: "" } } } ?: "") }
    var prompt by remember { mutableStateOf("") }
    var inWorktree by remember { mutableStateOf(false) }
    var worktree by remember { mutableStateOf("") }
    // The command that would make a worktree from the folder ({name} for its name); "" outside a git repository.
    var worktreeCommand by remember { mutableStateOf("") }
    var folders by remember { mutableStateOf<Hammock.FolderList?>(null) }
    var browsing by remember { mutableStateOf<String?>(null) }
    var starting by remember { mutableStateOf(false) }
    LaunchedEffect(group) { folders = model.listFolders("", group) }
    LaunchedEffect(browsing) { browsing?.let { folders = model.listFolders(it, group) } }
    LaunchedEffect(cwd, group, harness) {
        delay(300)
        worktreeCommand = if (cwd.isBlank()) "" else model.listFolders(cwd.trim(), group, harness = harness)?.worktreeCommand.orEmpty()
    }
    val usesWorktree = inWorktree && worktreeCommand.isNotEmpty()
    val installed = folders?.agentsList?.takeIf { it.isNotEmpty() }
    val kinds = w.kinds.filter { it.id in AgentCatalog.builtIn || it.id == harness || (installed?.contains(it.id) ?: it.installed) }
    val hostName = w.hostName.removeSuffix(".local").ifEmpty { "your computer" }

    if (!LocalAppLocked.current) ModalBottomSheet(onDismissRequest = onDone, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true), containerColor = MaterialTheme.colorScheme.surface) {
        LazyColumn(Modifier.fillMaxWidth().navigationBarsPadding(), contentPadding = androidx.compose.foundation.layout.PaddingValues(bottom = 24.dp)) {
            item {
                Row(Modifier.padding(start = 8.dp, end = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                    TextButton(onClick = onDone) { Text("Cancel") }
                    Text("Start an Agent", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f),
                        textAlign = androidx.compose.ui.text.style.TextAlign.Center)
                    Button(enabled = cwd.isNotBlank() && !starting && !(usesWorktree && worktree.isBlank()), onClick = {
                        starting = true
                        scope.launch {
                            val id = model.startAgent(harness, cwd.trim(), group, prompt.trim(), worktree = if (usesWorktree) worktree.trim() else "")
                            starting = false
                            if (id != null) {
                                // The new tab (or, with no Group chosen, its Group's) arrives in the next update.
                                repeat(50) {
                                    val w2 = model.workspace.value
                                    val tab = w2.tabs[id] ?: w2.groups.firstOrNull { it.id == id }?.tabIdsList?.lastOrNull()?.let { w2.tabs[it] }
                                    if (tab != null) {
                                        val pane = w2.panes(tab).firstOrNull()
                                        nav.open(if (pane != null) "pane:" + pane.id else "tab:" + tab.id)
                                        onDone()
                                        return@launch
                                    }
                                    delay(60)
                                }
                                onDone()
                            }
                        }
                    }) { if (starting) CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp) else Text("Start") }
                }
                Spacer(Modifier.size(12.dp))
            }
            item {
                GroupedCard {
                    if (kinds.size <= 2) {
                        SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp)) {
                            kinds.forEachIndexed { i, k ->
                                SegmentedButton(harness == k.id, { harness = k.id }, SegmentedButtonDefaults.itemShape(i, kinds.size),
                                    icon = { AgentMark(k.id, 16.dp) }) { Text(AgentCatalog.name(k.id, w.kinds), maxLines = 1) }
                            }
                        }
                    } else {
                        Choice("Agent", harness, kinds.map { it.id to AgentCatalog.name(it.id, w.kinds) }) { harness = it }
                    }
                    if (installed != null && harness !in installed) {
                        Row(Modifier.padding(start = 16.dp, end = 16.dp, bottom = 12.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                            Icon(Icons.Default.Warning, null, Modifier.size(16.dp), tint = NeedsYouColor)
                            Text("${AgentCatalog.name(harness, w.kinds)} isn't installed on $hostName: the tab will say so.",
                                style = MaterialTheme.typography.bodySmall, color = NeedsYouColor)
                        }
                    }
                }
            }
            if (groups.size > 1 || (groups.isNotEmpty() && groupID == null)) item {
                Spacer(Modifier.size(20.dp))
                GroupedCard {
                    val none = if (groups.isEmpty()) "A new Group" else "The folder's Group, or a new one"
                    Choice("Group", group, groups.map { g -> g.id to ((if (g.icon.isEmpty()) "" else g.icon + " ") + w.title(g)) } + ("" to none)) { group = it }
                }
            }
            item {
                SectionTitle("Folder on $hostName")
                GroupedCard {
                    BasicTextField(
                        cwd, { cwd = it }, singleLine = true,
                        textStyle = MaterialTheme.typography.bodyLarge.copy(fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurface),
                        cursorBrush = androidx.compose.ui.graphics.SolidColor(MaterialTheme.colorScheme.primary),
                        modifier = Modifier.fillMaxWidth().padding(16.dp),
                        decorationBox = { inner ->
                            if (cwd.isEmpty()) Text("~/Developer/app", style = MaterialTheme.typography.bodyLarge, fontFamily = FontFamily.Monospace,
                                color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f))
                            inner()
                        },
                    )
                    CardDivider()
                    ValueRow("Browse…", accent = true, onClick = { browsing = if (browsing == null) cwd.ifBlank { folders?.home ?: "" } else null }) {}
                    val f = folders
                    if (browsing != null && f != null) {
                        CardDivider()
                        ListItem(
                            colors = cardListItemColors(),
                            modifier = Modifier.clickable { if (f.parent.isNotEmpty()) browsing = f.parent },
                            leadingContent = { Icon(Icons.Default.ArrowUpward, null) },
                            headlineContent = { Text(shortPath(f.path), fontFamily = FontFamily.Monospace, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                            trailingContent = { TextButton(onClick = { cwd = shortPath(f.path); browsing = null }) { Text("Use") } },
                        )
                        if (f.foldersCount == 0) Text("No folders inside", Modifier.padding(16.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
                        for (name in f.foldersList) {
                            CardDivider(56.dp)
                            ListItem(
                                colors = cardListItemColors(),
                                modifier = Modifier.clickable { browsing = f.path.trimEnd('/') + "/" + name },
                                leadingContent = { Icon(Icons.Default.Folder, null, tint = MaterialTheme.colorScheme.primary) },
                                headlineContent = { Text(name) },
                            )
                        }
                    } else if (f != null) {
                        for (s in f.suggestionsList.take(8)) {
                            CardDivider()
                            Row(Modifier.fillMaxWidth().clickable { cwd = shortPath(s.path) }.padding(horizontal = 16.dp, vertical = 10.dp),
                                verticalAlignment = Alignment.CenterVertically) {
                                Column(Modifier.weight(1f)) {
                                    Text(shortPath(s.path), fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    if (s.why.isNotEmpty()) Text(s.why, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                                if (shortPath(cwd.trim()) == shortPath(s.path)) Icon(Icons.Default.Check, "Chosen", tint = MaterialTheme.colorScheme.primary)
                            }
                        }
                    }
                }
                folders?.error?.takeIf { it.isNotEmpty() }?.let { Footer(it) }
            }
            if (worktreeCommand.isNotEmpty()) item {
                Spacer(Modifier.size(20.dp))
                GroupedCard {
                    SwitchRow("In a New Worktree", inWorktree) { inWorktree = it }
                    if (inWorktree) {
                        CardDivider()
                        BasicTextField(
                            worktree, { worktree = it }, singleLine = true,
                            textStyle = MaterialTheme.typography.bodyLarge.copy(fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurface),
                            cursorBrush = androidx.compose.ui.graphics.SolidColor(MaterialTheme.colorScheme.primary),
                            modifier = Modifier.fillMaxWidth().padding(16.dp),
                            decorationBox = { inner ->
                                if (worktree.isEmpty()) Text("Branch: fix-login", style = MaterialTheme.typography.bodyLarge, fontFamily = FontFamily.Monospace,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f))
                                inner()
                            },
                        )
                    }
                }
                // What runs (in the repository, as you): shown before it does.
                if (inWorktree) Footer("Runs: " + worktreeCommand.replace("{name}", worktree.trim().ifEmpty { "NAME" }))
            }
            item {
                Spacer(Modifier.size(20.dp))
                GroupedCard {
                    BasicTextField(
                        prompt, { prompt = it },
                        textStyle = MaterialTheme.typography.bodyLarge.copy(color = MaterialTheme.colorScheme.onSurface),
                        cursorBrush = androidx.compose.ui.graphics.SolidColor(MaterialTheme.colorScheme.primary),
                        modifier = Modifier.fillMaxWidth().heightIn(min = 56.dp).padding(16.dp),
                        decorationBox = { inner ->
                            if (prompt.isEmpty()) Text("Optional: what to work on", style = MaterialTheme.typography.bodyLarge,
                                color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f))
                            inner()
                        },
                    )
                }
                Footer("It starts in a new tab, in the folder's login shell, as if you'd typed $harness${if (prompt.isEmpty()) "" else " and your message"}.")
            }
        }
    }
}

/** Find a Session: search every agent session on the computer, and resume one (the iPhone's SessionFinderList). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionFinderSheet(model: AppModel, nav: Nav, onDone: () -> Unit) {
    val scope = rememberCoroutineScope()
    var query by remember { mutableStateOf("") }
    var results by remember { mutableStateOf<List<Hammock.SessionInfo>>(emptyList()) }
    var indexing by remember { mutableStateOf(false) }
    var loaded by remember { mutableStateOf(false) }
    val now = rememberNow()
    LaunchedEffect(query) {
        if (loaded) delay(200)
        while (true) {
            val r = model.listSessions(query) ?: break
            results = r.sessionsList
            indexing = r.indexing
            loaded = true
            if (!r.indexing) break
            delay(600) // the first index is still being built: ask again shortly
        }
    }
    if (!LocalAppLocked.current) ModalBottomSheet(onDismissRequest = onDone, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true), containerColor = MaterialTheme.colorScheme.surface) {
        Column(Modifier.fillMaxWidth().fillMaxHeight(0.92f).navigationBarsPadding()) {
            Row(Modifier.padding(start = 20.dp, end = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("Find a Session", style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
                TextButton(onClick = onDone) { Text("Done") }
            }
            Row(
                Modifier.fillMaxWidth().padding(horizontal = CardInset, vertical = 8.dp).clip(CircleShape).background(cardColor()).padding(horizontal = 14.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Icon(Icons.Default.Search, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
                BasicTextField(
                    query, { query = it }, singleLine = true, modifier = Modifier.weight(1f),
                    textStyle = MaterialTheme.typography.bodyLarge.copy(color = MaterialTheme.colorScheme.onSurface),
                    cursorBrush = androidx.compose.ui.graphics.SolidColor(MaterialTheme.colorScheme.primary),
                    decorationBox = { inner ->
                        if (query.isEmpty()) Text("Titles, folders, anything said", style = MaterialTheme.typography.bodyLarge,
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                        inner()
                    },
                )
            }
            LazyColumn(Modifier.fillMaxWidth().weight(1f), contentPadding = androidx.compose.foundation.layout.PaddingValues(top = 8.dp, bottom = 24.dp)) {
                if (results.isEmpty()) item {
                    CardRow(0, 1) {
                        Text(if (!loaded || indexing) "Indexing your sessions…" else if (query.isEmpty()) "No sessions yet." else "No sessions match “$query”.",
                            Modifier.padding(16.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                cardItems(results, key = { it.harness + ":" + it.id }, dividerStart = 48.dp) { s ->
                    Row(
                        Modifier.fillMaxWidth().clickable {
                            scope.launch {
                                if (s.openPaneId.isNotEmpty()) { nav.open("pane:" + s.openPaneId); onDone(); return@launch }
                                val r = model.resumeSession(s) ?: return@launch
                                repeat(50) {
                                    if (model.workspace.value.panes[r.paneId] != null || model.workspace.value.tabs[r.tabId] != null) {
                                        nav.open(if (r.paneId.isNotEmpty()) "pane:" + r.paneId else "tab:" + r.tabId)
                                        onDone()
                                        return@launch
                                    }
                                    delay(60)
                                }
                                onDone()
                            }
                        }.padding(start = 16.dp, end = 12.dp, top = 12.dp, bottom = 12.dp),
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        AgentMark(s.harness, 18.dp, Modifier.padding(top = 2.dp))
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                Text(s.title.ifEmpty { s.firstPrompt }.ifEmpty { "Untitled" }, style = MaterialTheme.typography.titleSmall, maxLines = 1,
                                    overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                                if (s.openPaneId.isNotEmpty()) {
                                    val green = StatusPalette.green()
                                    Text("Open", style = MaterialTheme.typography.labelSmall, fontWeight = FontWeight.SemiBold, color = green,
                                        modifier = Modifier.clip(CircleShape).background(green.copy(alpha = 0.18f)).padding(horizontal = 6.dp, vertical = 1.dp))
                                }
                            }
                            Text(
                                listOf(shortPath(s.cwd), elapsedString(s.updatedUnixMs, now).let { if (it.isEmpty()) "" else "$it ago" },
                                    if (s.messages == 1) "1 message" else "${s.messages} messages").filter { it.isNotEmpty() }.joinToString(" · "),
                                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis,
                            )
                            excerpt(s)?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis) }
                        }
                        Chevron(Modifier.align(Alignment.CenterVertically))
                    }
                }
            }
        }
    }
}

/** The match, its words in bold («…»), else your last message. */
@Composable
private fun excerpt(s: Hammock.SessionInfo): androidx.compose.ui.text.AnnotatedString? {
    val ink = MaterialTheme.colorScheme.onSurface
    if (s.snippet.isNotEmpty()) return androidx.compose.ui.text.buildAnnotatedString {
        s.snippet.split("«").forEachIndexed { i, part ->
            if (i == 0) { append(part); return@forEachIndexed }
            val pieces = part.split("»", limit = 2)
            pushStyle(androidx.compose.ui.text.SpanStyle(fontWeight = FontWeight.Bold, color = ink)); append(pieces[0]); pop()
            if (pieces.size > 1) append(pieces[1])
        }
    }
    val last = s.lastPrompt.ifEmpty { s.firstPrompt }
    return if (last.isEmpty()) null else androidx.compose.ui.text.AnnotatedString("❯ $last")
}
