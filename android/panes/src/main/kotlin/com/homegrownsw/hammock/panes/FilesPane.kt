package com.homegrownsw.hammock.panes

import android.content.ClipData
import android.content.ClipboardManager
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.DriveFileMove
import androidx.compose.material.icons.automirrored.outlined.NoteAdd
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.ArrowUpward
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.CreateNewFolder
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.DriveFileRenameOutline
import androidx.compose.material.icons.outlined.FileCopy
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.Language
import androidx.compose.material.icons.outlined.UnfoldLess
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material.icons.outlined.VisibilityOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import kotlinx.coroutines.launch

/**
 * A files pane: the Mac's and the iPhone's page (files.html) at touch
 * sizes. A tap on a file opens it in a pane beside (an editor, or a browser
 * for pages, images and PDFs); touch and hold has the rest, in a sheet, and
 * Move to… picks a folder.
 */
@Composable
fun FilesPane(model: AppModel, pane: Hammock.Pane, canDrive: Boolean, theme: PaneTheme, modifier: Modifier = Modifier) {
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    val show by rememberUpdatedState(LocalShowPane.current)
    val session = remember(model, pane.id) { FilesSession.acquire(model, pane.id) }
    DisposableEffect(session) { onDispose { FilesSession.release(session) } }
    val ws by model.workspace.collectAsState()
    val info = (ws.panes[pane.id] ?: pane).files
    val error by session.error.collectAsState()

    val config = FilesJson.config(theme, canDrive)
    LaunchedEffect(session, config.toString()) { session.configure(config) }
    val infoJSON = FilesJson.info(info)
    LaunchedEffect(session, infoJSON.toString()) { session.setInfo(infoJSON) }

    var menu by remember { mutableStateOf<Pair<List<String>, List<Boolean>>?>(null) }
    var trashing by remember { mutableStateOf<List<String>?>(null) }
    var moving by remember { mutableStateOf<List<String>?>(null) }
    val drive by rememberUpdatedState(canDrive)

    fun open(path: String, browser: Boolean = false) = scope.launch {
        try {
            openFromFiles(model, pane.id, path, browser, show)?.let { session.error.value = it }
        } catch (e: Exception) {
            session.error.value = e.message ?: "Couldn't open it"
        }
    }

    val bridge = remember(session) {
        lateinit var b: PageBridge
        b = PageBridge(context, "files.html", theme.background, { type, m ->
            when (type) {
                "open" -> if (!m.optBoolean("dir")) m.optString("path").takeIf { it.isNotEmpty() }?.let { open(it) }
                "menu" -> menu = FilesJson.strings(m.optJSONArray("paths")) to FilesJson.bools(m.optJSONArray("dirs"))
                "trash" -> if (drive) FilesJson.strings(m.optJSONArray("paths")).takeIf { it.isNotEmpty() }?.let { trashing = it }
                else -> session.onPageMessage(b, type, m)
            }
        })
        b
    }
    DisposableEffect(bridge) {
        session.attachPage(bridge)
        onDispose {
            session.detachPage(bridge)
            bridge.destroy()
        }
    }

    Box(modifier.fillMaxSize().background(color(theme.background))) {
        val problem = bridge.problem
        if (problem != null) {
            Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
                Text(problem, color = MaterialTheme.colorScheme.error)
            }
        } else {
            AndroidView(factory = { bridge.webView }, modifier = Modifier.fillMaxSize(), update = { it.setBackgroundColor(theme.background) })
        }
        if (error.isNotEmpty()) ErrorPill(error, Modifier.align(Alignment.BottomCenter)) { session.error.value = "" }
    }

    menu?.let { (paths, dirs) ->
        FilesMenuSheet(paths, FilesMenu.actions(paths, dirs, info, canDrive), onDismiss = { menu = null }) { action ->
            menu = null
            when (action) {
                FilesAction.OPEN -> open(paths[0])
                FilesAction.OPEN_IN_BROWSER -> open(paths[0], browser = true)
                FilesAction.SHOW_ONLY_FOLDER -> session.setRoot(paths[0])
                FilesAction.RENAME -> bridge.call("command", "rename")
                FilesAction.NEW_FILE -> bridge.call("command", "newFile")
                FilesAction.NEW_FOLDER -> bridge.call("command", "newFolder")
                FilesAction.DUPLICATE -> session.duplicate(paths)
                FilesAction.MOVE_TO -> moving = paths
                FilesAction.COPY_PATH, FilesAction.COPY_PATHS -> context.getSystemService(ClipboardManager::class.java)
                    ?.setPrimaryClip(ClipData.newPlainText(if (paths.size == 1) "Path" else "Paths", paths.joinToString("\n")))
                FilesAction.TRASH -> trashing = paths
                FilesAction.SHOW_HIDDEN -> session.setHidden(true)
                FilesAction.HIDE_HIDDEN -> session.setHidden(false)
                FilesAction.COLLAPSE_ALL -> bridge.call("command", "collapseAll")
                FilesAction.GROUP_FOLDER -> session.setRoot("")
            }
        }
    }

    trashing?.let { paths ->
        AlertDialog(
            onDismissRequest = { trashing = null },
            title = { Text(FilesMenu.trashQuestion(paths)) },
            confirmButton = {
                TextButton(onClick = { trashing = null; session.trash(paths) }) { Text("Move to Trash", color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(onClick = { trashing = null }) { Text("Cancel") } },
        )
    }

    moving?.let { paths ->
        FolderPicker(session, info.root, paths, onDismiss = { moving = null }) { dest ->
            moving = null
            session.move(paths, dest)
        }
    }
}

/** Which editor the files pane opened last (pane → editor), to put the next file in while it's unchanged. Main thread. */
private val linkedEditor = HashMap<String, String>()

/**
 * Opens path from files pane filesPane in a pane beside it and shows that
 * pane; or says why not (a file a phone can't show).
 */
internal suspend fun openFromFiles(model: AppModel, filesPane: String, path: String, browser: Boolean, show: (String) -> Unit): String? {
    val opener = if (browser) FileOpener.BROWSER else FilesOpening.opener(path)
    val spec = Hammock.PaneSpec.newBuilder()
    when (opener) {
        FileOpener.ELSEWHERE -> return "${base(path)} opens on the computer it's on"
        FileOpener.BROWSER -> spec.setUrl(FilesOpening.fileURL(path))
        FileOpener.EDITOR -> {
            spec.setEditor(Hammock.EditorOpen.newBuilder().setPath(path))
            when (val t = FilesOpening.editorTarget(model.workspace.value, filesPane, path, linkedEditor[filesPane])) {
                is FilesOpening.EditorTarget.Show -> {
                    model.focus(t.paneID)
                    show(t.paneID)
                    return null
                }
                is FilesOpening.EditorTarget.Replace -> {
                    val r = model.call { setReplacePane(Hammock.ReplacePane.newBuilder().setPaneId(t.paneID).setSpec(spec).build()) }
                    val id = r.created.id.ifEmpty { t.paneID }
                    linkedEditor[filesPane] = id
                    model.focus(id)
                    show(id)
                    return null
                }
                FilesOpening.EditorTarget.Split -> {}
            }
        }
    }
    val r = model.call { setCreatePane(Hammock.CreatePane.newBuilder().setSplitPaneId(filesPane).setSpec(spec).build()) }
    val id = r.created.id
    if (id.isEmpty()) return null
    if (opener == FileOpener.EDITOR) linkedEditor[filesPane] = id
    model.focus(id)
    show(id)
    return null
}

private fun icon(a: FilesAction): ImageVector = when (a) {
    FilesAction.OPEN -> Icons.AutoMirrored.Outlined.OpenInNew
    FilesAction.OPEN_IN_BROWSER -> Icons.Outlined.Language
    FilesAction.SHOW_ONLY_FOLDER -> Icons.Outlined.FolderOpen
    FilesAction.RENAME -> Icons.Outlined.DriveFileRenameOutline
    FilesAction.NEW_FILE -> Icons.AutoMirrored.Outlined.NoteAdd
    FilesAction.NEW_FOLDER -> Icons.Outlined.CreateNewFolder
    FilesAction.DUPLICATE -> Icons.Outlined.FileCopy
    FilesAction.MOVE_TO -> Icons.AutoMirrored.Outlined.DriveFileMove
    FilesAction.COPY_PATH, FilesAction.COPY_PATHS -> Icons.Outlined.ContentCopy
    FilesAction.TRASH -> Icons.Outlined.Delete
    FilesAction.SHOW_HIDDEN -> Icons.Outlined.Visibility
    FilesAction.HIDE_HIDDEN -> Icons.Outlined.VisibilityOff
    FilesAction.COLLAPSE_ALL -> Icons.Outlined.UnfoldLess
    FilesAction.GROUP_FOLDER -> Icons.Outlined.Home
}

/** Touch and hold's actions, in a sheet: the item's name (or how many) over them. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun FilesMenuSheet(
    paths: List<String>, actions: List<FilesAction>, onDismiss: () -> Unit, pick: (FilesAction) -> Unit,
) {
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(bottom = 8.dp)) {
            val title = FilesMenu.title(paths)
            if (title.isNotEmpty()) {
                Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, maxLines = 1,
                    overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(horizontal = 24.dp, vertical = 8.dp))
            }
            for (a in actions) {
                if (a == FilesAction.TRASH || (a == FilesAction.COPY_PATH || a == FilesAction.COPY_PATHS) && actions.first() != a) HorizontalDivider()
                val tint = if (a == FilesAction.TRASH) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface
                ListItem(
                    headlineContent = { Text(a.label, color = tint) },
                    leadingContent = { Icon(icon(a), null, tint = tint) },
                    colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainerLow),
                    modifier = Modifier.clickable { pick(a) },
                )
            }
        }
    }
}

/** Move to…: a folder under the pane's, gone into a level at a time. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun FolderPicker(session: FilesSession, root: String, paths: List<String>, onDismiss: () -> Unit, done: (String) -> Unit) {
    var folder by remember { mutableStateOf(root) }
    var subfolders by remember { mutableStateOf<List<String>>(emptyList()) }
    var loading by remember { mutableStateOf(true) }
    var failed by remember { mutableStateOf("") }
    LaunchedEffect(folder) {
        loading = true
        val l = session.listDir(folder)
        subfolders = l?.entriesList.orEmpty().filter { it.dir && !it.name.startsWith(".") }.map { it.name }
        failed = when {
            l == null -> "Couldn't list this folder"
            l.error.isNotEmpty() -> l.error
            else -> ""
        }
        loading = false
    }
    fun join(name: String) = if (folder.endsWith("/")) folder + name else "$folder/$name"
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding()) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                TextButton(onClick = onDismiss) { Text("Cancel") }
                Text(base(folder), style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, maxLines = 1,
                    overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f).padding(horizontal = 8.dp))
                // Not into where they are already, or into one of themselves.
                val here = folder !in paths && paths.none { folder.startsWith("$it/") } && paths.any { parent(it) != folder }
                TextButton(onClick = { done(folder) }, enabled = here) { Text("Move Here") }
            }
            HorizontalDivider()
            LazyColumn(Modifier.fillMaxWidth().heightIn(min = 240.dp, max = 520.dp)) {
                if (folder != root && folder.startsWith("$root/")) {
                    item {
                        ListItem(
                            headlineContent = { Text("Enclosing Folder") },
                            leadingContent = { Icon(Icons.Outlined.ArrowUpward, null) },
                            modifier = Modifier.clickable { folder = parent(folder) },
                        )
                    }
                }
                items(subfolders, key = { it }) { name ->
                    val path = join(name)
                    val moved = path in paths
                    ListItem(
                        headlineContent = { Text(name, color = if (moved) MaterialTheme.colorScheme.onSurface.copy(alpha = 0.38f) else MaterialTheme.colorScheme.onSurface) },
                        leadingContent = { Icon(Icons.Outlined.Folder, null) },
                        modifier = if (moved) Modifier else Modifier.clickable { folder = path },
                    )
                }
                if (!loading && subfolders.isEmpty()) {
                    item {
                        Text(failed.ifEmpty { "No folders in here" }, color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 24.dp, vertical = 16.dp))
                    }
                }
            }
        }
    }
}
