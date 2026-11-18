package com.homegrownsw.hammock.panes

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.FormatIndentDecrease
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowLeft
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.filled.KeyboardTab
import androidx.compose.material.icons.automirrored.filled.Redo
import androidx.compose.material.icons.automirrored.filled.Undo
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.filled.KeyboardHide
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Save
import androidx.compose.material.icons.filled.Search
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
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import hammock.v1.Hammock.EditorInfo
import kotlinx.coroutines.launch

/**
 * An editor pane: the same CodeMirror page as the Mac's and the iPhone's,
 * editing the daemon's buffer. Above it, a line with where the cursor is and
 * the pane's menu (Save, Save As, Rename, Find, Go to Line, Language,
 * Compare with File on Disk, Reload from Disk, and the preview for
 * Markdown); banners for a file changed on disk or read-only; below, while
 * it can be edited, a bar with the keys a phone's keyboard lacks.
 *
 * [extraMenu] adds the app's own items (the tab's actions) to the end of the
 * pane's menu, so there's one menu rather than two; call close() before
 * acting. [settings] are the editor settings (defaults as the Mac's).
 */
@Composable
fun EditorPane(
    model: AppModel,
    pane: Hammock.Pane,
    canDrive: Boolean,
    theme: PaneTheme,
    modifier: Modifier = Modifier,
    settings: EditorSettings = EditorSettings(),
    extraMenu: @Composable ColumnScope.(close: () -> Unit) -> Unit = {},
) {
    val scope = rememberCoroutineScope()
    val session = remember(model, pane.id) { EditorSession(model, pane.id) }
    DisposableEffect(session) {
        session.start()
        onDispose { session.stop() }
    }
    val ws by model.workspace.collectAsState()
    val info = (ws.panes[pane.id] ?: pane).editor
    val state by session.state.collectAsState()
    val error by session.error.collectAsState()
    val editable = canDrive && !info.readOnly

    val bg = color(theme.background)
    val fg = color(theme.foreground)
    val dim = fg.copy(alpha = 0.6f)
    val accent = color(theme.accent)

    var saveAsPath by rememberSaveable { mutableStateOf<String?>(null) }
    var renameTo by rememberSaveable { mutableStateOf<String?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    var confirmOverwrite by remember { mutableStateOf<Pair<String, String>?>(null) }
    var confirmReload by remember { mutableStateOf(false) }
    var pickingLanguage by remember { mutableStateOf(false) }

    fun startSaveAs() {
        saveAsPath = when {
            info.path.isEmpty() -> ""
            info.folder.isNotEmpty() && info.path.startsWith(info.folder + "/") -> info.path.removePrefix(info.folder + "/")
            else -> info.path
        }
    }

    fun save(path: String = "", force: Boolean = false) = scope.launch {
        try {
            session.save(path, force)
        } catch (e: Exception) {
            val m = e.message ?: "Couldn't save it"
            if (!force && ("already exists" in m || "changed on disk" in m)) {
                confirmOverwrite = path to (if ("changed on disk" in m) "${info.name} changed on disk since. Save yours over it?" else m)
            } else {
                problem = m
            }
        }
    }

    fun saveOrAsk() = if (info.path.isEmpty()) startSaveAs() else { save(); Unit }

    fun toggleDiff() {
        if (state.showingDiff) { session.hideDiff(); return }
        scope.launch { try { session.showDiff() } catch (e: Exception) { problem = e.message } }
    }

    fun reload() = scope.launch { try { session.reload() } catch (e: Exception) { problem = e.message } }

    // The page's ⌘S, and links in the preview, act with what's current.
    SideEffect {
        session.onSave = { saveAs -> if (saveAs || info.path.isEmpty()) startSaveAs() else save() }
        session.onOpenFile = { path ->
            if (canDrive) {
                // Another Markdown file opens shown as this one is.
                val ext = path.substringAfterLast('.', "").lowercase()
                val view = if (ext in setOf("md", "markdown", "mdown", "mkd")) state.shownView(info) else EditorInfo.View.VIEW_SOURCE
                scope.launch { try { openEditorBeside(model, pane.id, path, view = view) } catch (e: Exception) { problem = e.message } }
            }
        }
    }

    // The page's settings and the file's details, as they change (sent once it's ready).
    val config = remember(theme, settings, canDrive) { EditorJson.config(theme, settings, readOnly = !canDrive) }
    val infoJSON = EditorJson.info(info, readOnly = !canDrive, view = state.shownView(info))
    LaunchedEffect(session, config.toString()) { session.configure(config) }
    LaunchedEffect(session, infoJSON.toString()) { session.setInfo(infoJSON) }

    Column(modifier.fillMaxSize().background(bg).imePadding()) {
        StatusLine(state, info, fg, dim) {
            EditorMenu(
                session, state, info, canDrive, editable, fg,
                onSave = ::saveOrAsk, onSaveAs = ::startSaveAs, onRename = { renameTo = info.name },
                onLanguage = { pickingLanguage = true }, onToggleDiff = ::toggleDiff,
                onReload = { if (info.dirty) confirmReload = true else reload() },
                extraMenu = extraMenu,
            )
        }
        when {
            state.showingDiff -> Banner("Your version, against the file on disk.", Color(0xFF0A84FF), fg) {
                BannerButton("Done", accent) { session.hideDiff() }
            }
            info.conflict -> Banner("${info.name} changed on disk.", Color(0xFFFF9500), fg) {
                BannerButton("Compare", accent, ::toggleDiff)
                BannerButton("Reload", accent) { confirmReload = true }
                BannerButton("Keep Mine", accent) { scope.launch { runCatching { session.keepMine() } } }
            }
            info.readOnly && canDrive -> Banner("${info.name} is read-only: you don't have permission to change it.", dim, fg) {
                BannerButton("Save As…", accent, ::startSaveAs)
            }
        }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            EditorWebView(session, theme.background)
            if (error.isNotEmpty()) ErrorPill(error, Modifier.align(Alignment.BottomCenter)) { session.error.value = "" }
        }
        if (editable && !state.previewOnly) KeyBar(fg, onSave = ::saveOrAsk, run = session::run)
    }

    saveAsPath?.let { p ->
        TextPrompt(
            title = "Save As",
            message = "A path in ${if (info.folder.isEmpty()) "the Group's folder" else info.folder.substringAfterLast('/')}, or a full path.",
            initial = p, placeholder = "notes.md", confirm = "Save",
            onDismiss = { saveAsPath = null },
        ) { path -> saveAsPath = null; if (path.isNotBlank()) save(path.trim()) }
    }
    renameTo?.let { n ->
        TextPrompt("Rename", "A new name for ${info.name}.", n, info.name, "Rename", onDismiss = { renameTo = null }) { name ->
            renameTo = null
            val t = name.trim()
            if (t.isNotEmpty() && t != info.name) {
                scope.launch { try { session.rename(t) } catch (e: Exception) { problem = "Couldn't rename it: ${e.message}" } }
            }
        }
    }
    confirmOverwrite?.let { (path, message) ->
        AlertDialog(
            onDismissRequest = { confirmOverwrite = null },
            text = { Text(message) },
            confirmButton = { TextButton(onClick = { confirmOverwrite = null; save(path, force = true) }) { Text("Save Anyway") } },
            dismissButton = { TextButton(onClick = { confirmOverwrite = null }) { Text("Cancel") } },
        )
    }
    if (confirmReload) {
        AlertDialog(
            onDismissRequest = { confirmReload = false },
            text = { Text("Discard your changes to ${info.name}?") },
            confirmButton = { TextButton(onClick = { confirmReload = false; reload() }) { Text("Discard My Changes") } },
            dismissButton = { TextButton(onClick = { confirmReload = false }) { Text("Cancel") } },
        )
    }
    problem?.let { p ->
        AlertDialog(onDismissRequest = { problem = null }, text = { Text(p) }, confirmButton = { TextButton(onClick = { problem = null }) { Text("OK") } })
    }
    if (pickingLanguage) {
        LanguagePicker(state.languageNames, state.languageName, onDismiss = { pickingLanguage = false }) { name ->
            pickingLanguage = false
            session.setLanguage(name)
        }
    }
}

@Composable
private fun EditorWebView(session: EditorSession, background: Int) {
    val context = LocalContext.current
    val bridge = remember(session) {
        lateinit var b: PageBridge
        b = PageBridge(context, "editor.html", background, { type, m -> session.onPageMessage(b, type, m) }, session::readImageBlocking)
        b
    }
    DisposableEffect(bridge) {
        session.attachPage(bridge)
        onDispose {
            session.detachPage(bridge)
            bridge.destroy()
        }
    }
    val problem = bridge.problem
    if (problem != null) {
        Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
            Text(problem, color = MaterialTheme.colorScheme.error)
        }
        return
    }
    AndroidView(factory = { bridge.webView }, modifier = Modifier.fillMaxSize(), update = { it.setBackgroundColor(background) })
}

/** Where the cursor is, whether it's edited, and the menu. */
@Composable
private fun StatusLine(state: EditorUiState, info: EditorInfo, fg: Color, dim: Color, menu: @Composable () -> Unit) {
    Row(
        Modifier.fillMaxWidth().heightIn(min = 40.dp).padding(start = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        if (info.dirty) {
            Box(Modifier.size(7.dp).background(dim, CircleShape))
            Text("Edited", color = dim, style = MaterialTheme.typography.labelMedium)
        }
        val where = buildString {
            if (!state.previewOnly) {
                append("Ln ${state.line}, Col ${state.column}")
                if (state.selected > 0) append(" (${state.selected} selected)")
                append("  ·  ")
            }
            append(state.languageName.ifEmpty { "Plain Text" })
        }
        Text(where, color = dim, style = MaterialTheme.typography.labelMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
        menu()
    }
}

@Composable
private fun EditorMenu(
    session: EditorSession,
    state: EditorUiState,
    info: EditorInfo,
    canDrive: Boolean,
    editable: Boolean,
    fg: Color,
    onSave: () -> Unit,
    onSaveAs: () -> Unit,
    onRename: () -> Unit,
    onLanguage: () -> Unit,
    onToggleDiff: () -> Unit,
    onReload: () -> Unit,
    extraMenu: @Composable ColumnScope.(close: () -> Unit) -> Unit,
) {
    var open by remember { mutableStateOf(false) }
    val wide = LocalConfiguration.current.screenWidthDp >= 600
    Box {
        IconButton(onClick = { open = true }) { Icon(Icons.Filled.MoreVert, contentDescription = "More", tint = fg) }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            val close = { open = false }
            if (canDrive) {
                if (editable) MenuItem(close, "Save", action = onSave)
                MenuItem(close, "Save As…", action = onSaveAs)
                if (info.path.isNotEmpty()) MenuItem(close, "Rename…", action = onRename)
                HorizontalDivider()
            }
            if (state.canPreview) {
                // A View member's choice is this device's alone.
                val shown = state.shownView(info)
                if (wide) {
                    // Wide enough for both side by side.
                    for ((v, label) in listOf(
                        EditorInfo.View.VIEW_SOURCE to "Source", EditorInfo.View.VIEW_SPLIT to "Source and Preview", EditorInfo.View.VIEW_PREVIEW to "Preview",
                    )) {
                        MenuItem(close, (if (shown == v) "✓ " else "") + label) { session.setView(v, local = !canDrive) }
                    }
                } else if (shown == EditorInfo.View.VIEW_SOURCE) {
                    MenuItem(close, "Show Preview") { session.setView(EditorInfo.View.VIEW_PREVIEW, local = !canDrive) }
                } else {
                    MenuItem(close, "Show Source") { session.setView(EditorInfo.View.VIEW_SOURCE, local = !canDrive) }
                }
            }
            MenuItem(close, "Find") { session.run("find") }
            if (!state.previewOnly) MenuItem(close, "Go to Line") { session.run("gotoLine") }
            MenuItem(close, "Language: ${state.languageName.ifEmpty { "Plain Text" }}…", enabled = canDrive, action = onLanguage)
            if (info.path.isNotEmpty()) {
                MenuItem(close, if (state.showingDiff) "Hide Comparison" else "Compare with File on Disk", action = onToggleDiff)
                if (editable) MenuItem(close, "Reload from Disk", action = onReload)
            }
            extraMenu(close)
        }
    }
}

/** The keys a phone's keyboard hasn't got. They don't take focus, so the keyboard stays up. */
@Composable
private fun KeyBar(fg: Color, onSave: () -> Unit, run: (String) -> Unit) {
    val keys: List<Triple<ImageVector, String, () -> Unit>> = listOf(
        Triple(Icons.AutoMirrored.Filled.KeyboardTab, "Tab") { run("tab") },
        Triple(Icons.AutoMirrored.Filled.FormatIndentDecrease, "Outdent") { run("outdent") },
        Triple(Icons.AutoMirrored.Filled.KeyboardArrowLeft, "Left") { run("left") },
        Triple(Icons.AutoMirrored.Filled.KeyboardArrowRight, "Right") { run("right") },
        Triple(Icons.Filled.KeyboardArrowUp, "Up") { run("up") },
        Triple(Icons.Filled.KeyboardArrowDown, "Down") { run("down") },
        Triple(Icons.AutoMirrored.Filled.Undo, "Undo") { run("undo") },
        Triple(Icons.AutoMirrored.Filled.Redo, "Redo") { run("redo") },
        Triple(Icons.Filled.Search, "Find") { run("find") },
        Triple(Icons.Filled.Save, "Save", onSave),
        Triple(Icons.Filled.KeyboardHide, "Hide Keyboard") { run("blur") },
    )
    Row(
        Modifier.fillMaxWidth().background(fg.copy(alpha = 0.06f)).horizontalScroll(rememberScrollState()).padding(horizontal = 8.dp, vertical = 6.dp),
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        for ((icon, label, action) in keys) {
            Box(
                Modifier.width(44.dp).heightIn(min = 36.dp).background(fg.copy(alpha = 0.1f), RoundedCornerShape(7.dp))
                    .clickable(onClick = action).semantics { contentDescription = label },
                contentAlignment = Alignment.Center,
            ) { Icon(icon, contentDescription = null, tint = fg) }
        }
    }
}

@Composable
internal fun ErrorPill(text: String, modifier: Modifier = Modifier, onDismiss: () -> Unit) {
    Text(
        text,
        color = MaterialTheme.colorScheme.onSurface,
        style = MaterialTheme.typography.bodyMedium,
        modifier = modifier.padding(12.dp)
            .background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(50))
            .clickable(onClick = onDismiss)
            .padding(horizontal = 14.dp, vertical = 8.dp),
    )
}

@Composable
internal fun TextPrompt(
    title: String,
    message: String,
    initial: String,
    placeholder: String,
    confirm: String,
    onDismiss: () -> Unit,
    onConfirm: (String) -> Unit,
) {
    var text by remember { mutableStateOf(initial) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text(message)
                OutlinedTextField(
                    value = text, onValueChange = { text = it }, singleLine = true, placeholder = { Text(placeholder) },
                    keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.None, autoCorrectEnabled = false),
                )
            }
        },
        confirmButton = { TextButton(onClick = { onConfirm(text) }) { Text(confirm) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

/** The page's languages, to pick one (or go back to detecting it from the name). */
@Composable
private fun LanguagePicker(names: List<String>, current: String, onDismiss: () -> Unit, onPick: (String) -> Unit) {
    var query by remember { mutableStateOf("") }
    val shown = remember(names, query) { names.filter { query.isBlank() || it.contains(query.trim(), ignoreCase = true) } }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Language") },
        text = {
            Column {
                OutlinedTextField(
                    value = query, onValueChange = { query = it }, singleLine = true, placeholder = { Text("Search") },
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.size(8.dp))
                LazyColumn(Modifier.heightIn(max = 360.dp)) {
                    item {
                        Text(
                            "Detect from Name", style = MaterialTheme.typography.bodyLarge,
                            modifier = Modifier.fillMaxWidth().clickable { onPick("") }.padding(vertical = 10.dp),
                        )
                    }
                    items(shown) { n ->
                        Text(
                            (if (n == current) "✓ " else "") + n, style = MaterialTheme.typography.bodyLarge,
                            modifier = Modifier.fillMaxWidth().clickable { onPick(n) }.padding(vertical = 10.dp),
                        )
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
private fun MenuItem(close: () -> Unit, label: String, enabled: Boolean = true, action: () -> Unit) {
    DropdownMenuItem(text = { Text(label) }, enabled = enabled, onClick = { close(); action() })
}
