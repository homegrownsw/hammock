package com.homegrownsw.hammock.agentview

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.provider.OpenableColumns
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.ArrowUpward
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Mic
import androidx.compose.material.icons.outlined.Photo
import androidx.compose.material.icons.outlined.Stop
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.Warning
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilledIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.IconButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.OffsetMapping
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.input.TransformedText
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Something attached in the composer: shown in the tray, sent with the message at [size] (or the default). */
private class ComposerAttachment(
    val id: Long,
    val name: String,
    val data: ByteArray,
    val thumb: ImageBitmap?,
    val size: AttachmentSize? = null,
)

/** What's typed but not sent, per pane, through leaving the screen and the app being killed. */
private object Drafts {
    private fun prefs(context: Context) = context.getSharedPreferences("hammock.agentview.drafts", Context.MODE_PRIVATE)
    fun load(context: Context, pane: String): String = prefs(context).getString(pane, "") ?: ""
    fun save(context: Context, pane: String, text: String) {
        val p = prefs(context)
        if (text.isBlank()) { if (p.contains(pane)) p.edit().remove(pane).apply() } else if (p.getString(pane, null) != text) p.edit().putString(pane, text).apply()
    }
}

/** The most attached to one message. */
private const val MAX_ATTACHMENTS = 8

/**
 * The message box: text the full width, attachments in a tray along its top,
 * and a row of controls under it (attach, the model and context, the mic, and
 * send, which stops the agent while it works and nothing's typed).
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun Composer(session: AgentSession, state: AgentViewState, imageSize: AttachmentSize) {
    val c = LocalAgentColors.current
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var value by remember(session.paneID) { mutableStateOf(Drafts.load(context, session.paneID).let { TextFieldValue(it, TextRange(it.length)) }) }
    val attachments = remember(session.paneID) { mutableStateListOf<ComposerAttachment>() }
    var loading by remember { mutableIntStateOf(0) }
    var attachMenu by remember { mutableStateOf(false) }
    var attachError by remember { mutableStateOf<String?>(null) }
    // What was last taken from the terminal's input box (kept in step while
    // unedited), and whether a send is waiting for that box to empty.
    var tookDraft by remember(session.paneID) { mutableStateOf("") }
    var awaitingClear by remember(session.paneID) { mutableStateOf(false) }
    // What was just sent, and until when anything of it coming back (the
    // terminal briefly holding it as it's typed there) is a ghost.
    var justSent by remember { mutableStateOf<Pair<String, Long>?>(null) }
    val focus = remember { FocusRequester() }
    val dictation = remember { Dictation(context) }
    val canDictate = remember { Dictation.available(context) }
    var nextID by remember { mutableIntStateOf(1) }

    val text = value.text
    val shellCommand = attachments.isEmpty() && session.hasShellMode && text.trimStart().startsWith("!")
    val token = if (value.selection.collapsed) SlashToken.at(value.selection.start, text) else null
    val suggestions = token?.let { SlashToken.suggestions(it, text, state.commands, state.startCommands) } ?: emptyList()
    val canSend = (text.isNotBlank() || attachments.isNotEmpty()) && loading == 0
    val stops = state.working && text.isEmpty() && attachments.isEmpty() && !dictation.active

    fun isGhost(s: String): Boolean {
        val j = justSent ?: return false
        if (System.currentTimeMillis() > j.second) return false
        if (s.contains("[Image #") || s.contains("/hammock/attachments/")) return true
        fun bare(t: String) = t.filterNot { it.isWhitespace() }
        val a = bare(s)
        val b = bare(j.first)
        if (a.isEmpty()) return false
        return b.contains(a) || a.startsWith(b) || s.startsWith("[Pasted text")
    }

    fun setText(t: String) { value = TextFieldValue(t, TextRange(t.length)) }

    fun complete(command: String) {
        val tk = token ?: return
        val (t, cursor) = SlashToken.complete(tk, command, text)
        value = TextFieldValue(t, TextRange(cursor.coerceAtMost(t.length)))
    }

    fun attach(name: String, data: ByteArray) {
        if (attachments.size >= MAX_ATTACHMENTS) { attachError = "You can attach up to $MAX_ATTACHMENTS pictures to a message."; return }
        val id = nextID++.toLong()
        loading++
        scope.launch {
            val thumb = withContext(Dispatchers.Default) { ImageCodec.decode(data, 256)?.asImageBitmap() }
            loading--
            if (thumb == null) { attachError = "Couldn't read that picture."; return@launch }
            attachments.add(ComposerAttachment(id, name, data, thumb))
        }
    }

    fun attachUris(uris: List<Uri>) {
        for (uri in uris) {
            loading++
            scope.launch {
                val loaded = withContext(Dispatchers.IO) {
                    runCatching { context.contentResolver.openInputStream(uri)?.use { it.readBytes() } }.getOrNull()?.let { displayName(context, uri) to it }
                }
                loading--
                if (loaded == null) attachError = "Couldn't load that picture." else attach(loaded.first, loaded.second)
            }
        }
    }

    val photos = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(4)) { attachUris(it) }
    val documents = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { attachUris(it) }

    fun startDictation() {
        dictation.start(text, state.commands) { t -> value = TextFieldValue(t, TextRange(t.length)) }
    }
    val micPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) startDictation() else dictation.problem = "Hammock needs the microphone to dictate."
    }
    fun toggleDictation() {
        if (dictation.active) { dictation.stop(); return }
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) startDictation()
        else micPermission.launch(Manifest.permission.RECORD_AUDIO)
    }

    fun send() {
        if (dictation.active) {
            // The last words settle first, then it all goes.
            dictation.stop()
            scope.launch { delay(600); send() }
            return
        }
        if (!canSend) return
        val sentText = text
        val sent = attachments.toList()
        attachments.clear()
        setText("")
        Drafts.save(context, session.paneID, "")
        // The terminal's box is emptied as this goes: don't take its old text back meanwhile.
        if (state.draft.isNotEmpty()) awaitingClear = true
        tookDraft = ""
        justSent = sentText to System.currentTimeMillis() + 4000
        scope.launch {
            // Pictures at the size they go at.
            val files = withContext(Dispatchers.Default) { sent.map { ImageCodec.resized(it.name, it.data, it.size ?: imageSize) } }
            if (session.send(sentText, files)) return@launch
            // Didn't go (not connected): give it back, unless something new's been typed.
            justSent = null
            awaitingClear = false
            if (value.text.isEmpty()) {
                setText(sentText)
                attachments.addAll(0, sent)
            }
        }
    }

    // What's typed in the terminal comes into the box while it's empty (or
    // still just that), so sending, which replaces it there, loses nothing.
    LaunchedEffect(state.draft) {
        val d = state.draft
        if (dictation.active) return@LaunchedEffect
        if (d.isNotEmpty() && isGhost(d)) return@LaunchedEffect
        if (awaitingClear) { if (d.isEmpty()) awaitingClear = false; return@LaunchedEffect }
        if ((value.text.isEmpty() || value.text == tookDraft) && attachments.isEmpty()) {
            setText(d)
            tookDraft = d
        }
    }
    val showsTerminalDraft = !awaitingClear && state.draft.isNotEmpty() && text != tookDraft && !text.contains(state.draft) && !isGhost(state.draft)

    // Saved once typing pauses, and on leaving.
    LaunchedEffect(text) { delay(400); Drafts.save(context, session.paneID, text) }
    DisposableEffect(session.paneID) {
        onDispose {
            dictation.cancel()
            Drafts.save(context, session.paneID, value.text)
        }
    }
    // A new chat (an agent just started): straight to typing.
    LaunchedEffect(state.heard, state.blocks.isEmpty()) {
        if (state.heard && state.blocks.isEmpty()) { delay(350); runCatching { focus.requestFocus() } }
    }

    val highlight = if (shellCommand) c.shell else c.accent
    val commandSet = remember(state.commands) { state.commands.toSet() }
    val transformation = remember(commandSet, state.startCommands, highlight) {
        VisualTransformation { t ->
            val ranges = SlashToken.highlights(t.text, commandSet, state.startCommands)
            if (ranges.isEmpty()) TransformedText(t, OffsetMapping.Identity)
            else TransformedText(
                AnnotatedString(t.text, ranges.map { AnnotatedString.Range(SpanStyle(color = highlight, fontWeight = FontWeight.Medium), it.first, it.last + 1) }),
                OffsetMapping.Identity,
            )
        }
    }
    val border by animateColorAsState(if (shellCommand) c.shell else c.shell.copy(alpha = 0f), label = "shell")

    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        AnimatedVisibility(suggestions.isNotEmpty(), enter = expandVertically() + fadeIn(), exit = shrinkVertically() + fadeOut()) {
            Surface(shape = RoundedCornerShape(20.dp), tonalElevation = 3.dp, shadowElevation = 4.dp, modifier = Modifier.fillMaxWidth()) {
                Column(Modifier.padding(vertical = 6.dp)) {
                    for (s in suggestions) {
                        Text(
                            s, style = codeTextStyle(1f), maxLines = 1, overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.fillMaxWidth().clickable { complete(s) }.padding(horizontal = 16.dp, vertical = 9.dp),
                        )
                    }
                }
            }
        }
        dictation.problem?.let { p ->
            Notice(Icons.Outlined.Warning, p, action = if (p.contains("microphone")) "Settings" else null, onAction = {
                context.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", context.packageName, null)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }) { dictation.problem = null }
        }
        attachError?.let { e -> Notice(Icons.Outlined.Warning, e, null, {}) { attachError = null } }
        // The message didn't go (it's back in the box): why, and Retry.
        state.sendError?.let { e -> Notice(Icons.Outlined.Warning, e, if (canSend) "Retry" else null, onAction = { send() }) { session.clearSendError() } }
        if (showsTerminalDraft) {
            Notice(Icons.Outlined.Terminal, "Typed in the terminal: ${state.draft}", "Add", onAction = {
                setText(state.draft + if (text.isEmpty()) "" else " $text")
                tookDraft = ""
            }, onDismiss = null)
        }
        Surface(
            shape = RoundedCornerShape(22.dp), tonalElevation = 3.dp, shadowElevation = 4.dp,
            modifier = Modifier.fillMaxWidth().border(1.5.dp, border, RoundedCornerShape(22.dp)),
        ) {
            Column(Modifier.clickable(indication = null, interactionSource = null) { runCatching { focus.requestFocus() } }) {
                if (attachments.isNotEmpty()) {
                    Row(
                        Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(start = 12.dp, end = 12.dp, top = 12.dp),
                        horizontalArrangement = Arrangement.spacedBy(10.dp),
                    ) {
                        attachments.forEachIndexed { i, a ->
                            TrayThumb(a, i + 1, imageSize, onSize = { s -> attachments[i] = ComposerAttachment(a.id, a.name, a.data, a.thumb, s) }) {
                                attachments.removeAll { it.id == a.id }
                            }
                        }
                    }
                }
                if (shellCommand) {
                    Row(Modifier.padding(start = 16.dp, top = 10.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                        Icon(Icons.Outlined.Terminal, null, tint = c.shell, modifier = Modifier.size(14.dp))
                        Text("Shell command", style = MaterialTheme.typography.labelMedium, color = c.shell)
                    }
                }
                val textStyle = (if (shellCommand) codeTextStyle(1f) else MaterialTheme.typography.bodyLarge).copy(color = MaterialTheme.colorScheme.onSurface)
                Box(Modifier.fillMaxWidth().padding(start = 16.dp, end = 16.dp, top = 13.dp, bottom = 8.dp)) {
                    if (text.isEmpty()) Text("Message ${session.agentName}…", style = textStyle, color = c.tertiary)
                    BasicTextField(
                        value, {
                            // Editing the message that didn't go: its error's done with.
                            if (it.text != value.text && state.sendError != null) session.clearSendError()
                            value = it
                        },
                        textStyle = textStyle, cursorBrush = SolidColor(c.accent), maxLines = 8,
                        visualTransformation = transformation,
                        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                        modifier = Modifier.fillMaxWidth().heightIn(min = 24.dp).focusRequester(focus).onPreviewKeyEvent { e ->
                            if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                            when {
                                // A hardware keyboard: ⌘/Ctrl-Return sends, Tab takes the top suggestion.
                                e.key == Key.Enter && (e.isCtrlPressed || e.isMetaPressed) -> { if (stops) session.interrupt() else send(); true }
                                e.key == Key.Tab && suggestions.isNotEmpty() -> { complete(suggestions.first()); true }
                                e.key == Key.Escape && dictation.active -> { dictation.stop(); true }
                                else -> false
                            }
                        },
                    )
                }
                Row(Modifier.fillMaxWidth().padding(start = 8.dp, end = 8.dp, bottom = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    Box {
                        IconButton(onClick = { attachMenu = true }, colors = IconButtonDefaults.iconButtonColors(containerColor = c.fill), modifier = Modifier.size(36.dp)) {
                            Icon(Icons.Outlined.Add, "Attach pictures")
                        }
                        DropdownMenu(attachMenu, onDismissRequest = { attachMenu = false }) {
                            DropdownMenuItem(text = { Text("Photos") }, leadingIcon = { Icon(Icons.Outlined.Photo, null) }, onClick = {
                                attachMenu = false
                                photos.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                            })
                            DropdownMenuItem(text = { Text("Files") }, leadingIcon = { Icon(Icons.Outlined.Description, null) }, onClick = {
                                attachMenu = false
                                documents.launch(arrayOf("image/*"))
                            })
                        }
                    }
                    if (loading > 0) {
                        CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp)
                        Text("Loading…", style = MaterialTheme.typography.labelMedium, color = c.secondary)
                    }
                    if (dictation.active) {
                        DictationLevels(dictation)
                        Spacer(Modifier.weight(1f))
                    } else {
                        Box(Modifier.weight(1f)) { if (loading == 0) AgentStatusBar(session, state, canDrive = true) }
                    }
                    if (canDictate || dictation.active) {
                        IconButton(
                            onClick = ::toggleDictation, modifier = Modifier.size(36.dp),
                            colors = IconButtonDefaults.iconButtonColors(
                                containerColor = if (dictation.active) c.accent.copy(alpha = 0.18f) else androidx.compose.ui.graphics.Color.Transparent,
                                contentColor = if (dictation.active) c.accent else c.secondary,
                            ),
                        ) { Icon(Icons.Outlined.Mic, if (dictation.active) "Stop dictating" else "Dictate") }
                    }
                    FilledIconButton(
                        onClick = { if (stops) session.interrupt() else send() },
                        enabled = stops || canSend,
                        modifier = Modifier.size(36.dp),
                        colors = IconButtonDefaults.filledIconButtonColors(
                            containerColor = if (shellCommand && !stops) c.shell else c.accent, contentColor = c.onAccent,
                        ),
                    ) {
                        Icon(
                            if (stops) Icons.Outlined.Stop else Icons.Outlined.ArrowUpward,
                            if (stops) "Interrupt" else if (shellCommand) "Run" else "Send",
                        )
                    }
                }
            }
        }
    }
}

/** A line above the box: a notice, with an action and a way to dismiss it. */
@Composable
private fun Notice(icon: androidx.compose.ui.graphics.vector.ImageVector, text: String, action: String?, onAction: () -> Unit, onDismiss: (() -> Unit)?) {
    val c = LocalAgentColors.current
    Surface(shape = RoundedCornerShape(18.dp), tonalElevation = 3.dp, shadowElevation = 2.dp, modifier = Modifier.fillMaxWidth()) {
        Row(Modifier.padding(start = 12.dp, end = 4.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Icon(icon, null, tint = c.secondary, modifier = Modifier.size(16.dp))
            Text(text, style = MaterialTheme.typography.labelMedium, color = c.secondary, maxLines = 2, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f).padding(vertical = 10.dp))
            if (action != null) TextButton(onClick = onAction) { Text(action) }
            if (onDismiss != null) IconButton(onClick = onDismiss, modifier = Modifier.size(32.dp)) { Icon(Icons.Outlined.Close, "Dismiss", modifier = Modifier.size(16.dp)) }
        }
    }
}

/** An attachment in the tray: its picture and number. Tap to look at it; hold for the size it's sent at, or to remove it. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun TrayThumb(a: ComposerAttachment, number: Int, defaultSize: AttachmentSize, onSize: (AttachmentSize?) -> Unit, onRemove: () -> Unit) {
    val c = LocalAgentColors.current
    val open = LocalOpenImage.current
    var menu by remember { mutableStateOf(false) }
    Box {
        Box(Modifier.size(64.dp).clip(RoundedCornerShape(12.dp)).combinedClickable(onClick = { a.thumb?.let { t -> ImageCodec.decode(a.data, 2048)?.asImageBitmap()?.let(open) ?: open(t) } }, onLongClick = { menu = true })) {
            if (a.thumb != null) Image(a.thumb, a.name, contentScale = ContentScale.Crop, modifier = Modifier.size(64.dp))
            Text(
                "$number", style = MaterialTheme.typography.labelSmall, fontWeight = FontWeight.Bold, color = c.onAccent,
                modifier = Modifier.align(Alignment.BottomStart).padding(4.dp).clip(CircleShape).background(c.accent).padding(horizontal = 6.dp, vertical = 1.dp),
            )
        }
        Box(
            Modifier.align(Alignment.TopEnd).offset(x = 6.dp, y = (-6).dp).size(22.dp).clip(CircleShape)
                .background(MaterialTheme.colorScheme.inverseSurface).clickable(onClickLabel = "Remove", onClick = onRemove),
            contentAlignment = Alignment.Center,
        ) { Icon(Icons.Outlined.Close, "Remove", tint = MaterialTheme.colorScheme.inverseOnSurface, modifier = Modifier.size(14.dp)) }
        DropdownMenu(menu, onDismissRequest = { menu = false }) {
            Text("Send at", style = MaterialTheme.typography.labelSmall, color = c.secondary, modifier = Modifier.padding(horizontal = 16.dp, vertical = 6.dp))
            for (s in AttachmentSize.entries) {
                val current = (a.size ?: defaultSize) == s
                DropdownMenuItem(
                    text = { Text(s.label + if (s == defaultSize) " (default)" else "") },
                    onClick = { menu = false; onSize(if (s == defaultSize) null else s) },
                    trailingIcon = if (current) ({ Icon(Icons.Outlined.Check, "Chosen") }) else null,
                )
            }
            DropdownMenuItem(text = { Text("Remove") }, onClick = { menu = false; onRemove() })
        }
    }
}

private fun displayName(context: Context, uri: Uri): String {
    val fromProvider = runCatching {
        context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { cur ->
            if (cur.moveToFirst()) cur.getString(0) else null
        }
    }.getOrNull()
    return fromProvider?.takeIf { it.isNotBlank() } ?: "photo.jpg"
}
