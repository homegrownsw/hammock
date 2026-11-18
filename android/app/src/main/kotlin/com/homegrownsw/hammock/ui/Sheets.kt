package com.homegrownsw.hammock.ui

import androidx.compose.foundation.clickable
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
    var folders by remember { mutableStateOf<Hammock.FolderList?>(null) }
    var browsing by remember { mutableStateOf<String?>(null) }
    var starting by remember { mutableStateOf(false) }
    LaunchedEffect(group) { folders = model.listFolders("", group) }
    LaunchedEffect(browsing) { browsing?.let { folders = model.listFolders(it, group) } }
    val installed = folders?.agentsList?.takeIf { it.isNotEmpty() }
    val kinds = w.kinds.filter { it.id in AgentCatalog.builtIn || it.id == harness || (installed?.contains(it.id) ?: it.installed) }

    ModalBottomSheet(onDismissRequest = onDone, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        LazyColumn(Modifier.fillMaxWidth().navigationBarsPadding(), contentPadding = androidx.compose.foundation.layout.PaddingValues(bottom = 24.dp)) {
            item {
                Row(Modifier.padding(horizontal = 20.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text("Start an Agent", style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
                    TextButton(onClick = onDone) { Text("Cancel") }
                    Button(enabled = cwd.isNotBlank() && !starting, onClick = {
                        starting = true
                        scope.launch {
                            val id = model.startAgent(harness, cwd.trim(), group, prompt.trim())
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
            }
            item {
                SectionTitle("Agent")
                LazyRow(contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 20.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    items(kinds, key = { it.id }) { k -> FilterChip(harness == k.id, { harness = k.id }, { Text(AgentCatalog.name(k.id, w.kinds)) }) }
                }
            }
            if (groups.isNotEmpty()) item {
                SectionTitle("Group")
                LazyRow(contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 20.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    item { FilterChip(group == "", { group = "" }, { Text("By Folder") }) }
                    items(groups, key = { it.id }) { g -> FilterChip(group == g.id, { group = g.id }, { Text((if (g.icon.isEmpty()) "" else g.icon + " ") + w.title(g)) }) }
                }
            }
            item {
                SectionTitle("Folder on ${w.hostName.removeSuffix(".local").ifEmpty { "your computer" }}")
                OutlinedTextField(cwd, { cwd = it }, Modifier.fillMaxWidth().padding(horizontal = 20.dp), singleLine = true,
                    placeholder = { Text("~/Developer/app") }, textStyle = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace),
                    trailingIcon = { IconButton(onClick = { browsing = cwd.ifBlank { folders?.home ?: "" } }) { Icon(Icons.Default.Folder, "Browse") } })
                folders?.error?.takeIf { it.isNotEmpty() }?.let { Text(it, Modifier.padding(horizontal = 20.dp, vertical = 4.dp), color = MaterialTheme.colorScheme.error) }
            }
            val f = folders
            if (browsing != null && f != null) {
                item {
                    ListItem(
                        modifier = Modifier.clickable { if (f.parent.isNotEmpty()) browsing = f.parent },
                        leadingContent = { Icon(Icons.AutoMirrored.Filled.ArrowBack, null) },
                        headlineContent = { Text(shortPath(f.path), fontFamily = FontFamily.Monospace, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        trailingContent = { TextButton(onClick = { cwd = f.path; browsing = null }) { Text("Choose") } },
                    )
                }
                if (f.foldersCount == 0) item { Text("No folders inside", Modifier.padding(20.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }
                items(f.foldersList, key = { "f:$it" }) { name ->
                    ListItem(
                        modifier = Modifier.clickable { browsing = f.path.trimEnd('/') + "/" + name },
                        leadingContent = { Icon(Icons.Default.Folder, null, tint = MaterialTheme.colorScheme.primary) },
                        headlineContent = { Text(name) },
                    )
                }
            } else if (f != null && f.suggestionsCount > 0) {
                items(f.suggestionsList, key = { "s:" + it.path }) { s ->
                    ListItem(
                        modifier = Modifier.clickable { cwd = s.path },
                        leadingContent = { Icon(Icons.Default.Folder, null, tint = MaterialTheme.colorScheme.onSurfaceVariant) },
                        headlineContent = { Text(shortPath(s.path), fontFamily = FontFamily.Monospace, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        supportingContent = { if (s.why.isNotEmpty()) Text(s.why) },
                    )
                }
            }
            item {
                SectionTitle("First Message")
                OutlinedTextField(prompt, { prompt = it }, Modifier.fillMaxWidth().padding(horizontal = 20.dp).heightIn(min = 96.dp),
                    placeholder = { Text("Optional: what to work on") })
                Footer("It starts in a new tab, in the folder's login shell, as if you'd typed $harness${if (prompt.isEmpty()) "" else " and your message"}.")
            }
        }
    }
}

/** Find a Session: search every agent session on the computer, read one, resume it. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionFinderSheet(model: AppModel, nav: Nav, onDone: () -> Unit) {
    val w by model.workspace.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    var query by remember { mutableStateOf("") }
    var results by remember { mutableStateOf<List<Hammock.SessionInfo>>(emptyList()) }
    var indexing by remember { mutableStateOf(false) }
    val now = rememberNow()
    LaunchedEffect(query) {
        delay(if (query.isEmpty()) 0 else 200)
        while (true) {
            val r = model.listSessions(query) ?: break
            results = r.sessionsList
            indexing = r.indexing
            if (!r.indexing) break
            delay(1000) // the first index is still being built: ask again shortly
        }
    }
    ModalBottomSheet(onDismissRequest = onDone, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding()) {
            Row(Modifier.padding(horizontal = 20.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("Find a Session", style = MaterialTheme.typography.titleLarge, modifier = Modifier.weight(1f))
                TextButton(onClick = onDone) { Text("Done") }
            }
            OutlinedTextField(query, { query = it }, Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 8.dp), singleLine = true,
                leadingIcon = { Icon(Icons.Default.Search, null) }, placeholder = { Text("Search your agents' sessions") })
            if (indexing) Text("Indexing sessions…", Modifier.padding(horizontal = 20.dp), style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant)
            LazyColumn(Modifier.fillMaxWidth().heightIn(max = 900.dp)) {
                items(results, key = { it.harness + ":" + it.id }) { s ->
                    ListItem(
                        modifier = Modifier.clickable {
                            scope.launch {
                                if (s.openPanePid()) { nav.open("pane:" + s.openPaneId); onDone(); return@launch }
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
                        },
                        headlineContent = { Text(s.title.ifEmpty { s.firstPrompt }.ifEmpty { "Untitled" }, maxLines = 2, overflow = TextOverflow.Ellipsis) },
                        supportingContent = {
                            Column {
                                if (s.snippet.isNotEmpty()) Text(plainInline(s.snippet), maxLines = 2, overflow = TextOverflow.Ellipsis)
                                Text(
                                    listOf(AgentCatalog.shortName(s.harness, w.kinds), shortPath(s.cwd), elapsedString(s.updatedUnixMs, now) + " ago")
                                        .filter { it.isNotEmpty() }.joinToString(" · "),
                                    style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis,
                                )
                            }
                        },
                        trailingContent = { if (s.openPaneId.isNotEmpty()) Text("Open", color = MaterialTheme.colorScheme.primary) },
                    )
                }
                item { Spacer(Modifier.size(24.dp)) }
            }
        }
    }
}

private fun Hammock.SessionInfo.openPanePid() = openPaneId.isNotEmpty()
