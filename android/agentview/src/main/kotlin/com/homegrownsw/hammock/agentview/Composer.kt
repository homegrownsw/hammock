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
import androidx.compose.ui.input.key.isAltPressed
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.isShiftPressed
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

/** What's been sent from each pane's composer, for ↑ to bring back ([ComposerHistory]). */
private object History {
    private fun prefs(context: Context) = context.getSharedPreferences("hammock.agentview.history", Context.MODE_PRIVATE)
    @Synchronized fun load(context: Context, pane: String): List<String> = runCatching {
        val a = org.json.JSONArray(prefs(context).getString(pane, "[]") ?: "[]")
        List(a.length()) { a.getString(it) }
    }.getOrDefault(emptyList())
    /**
     * Adds [text] to [pane]'s, read and written here under one lock (a send
     * before the composer's own read finishes can't wipe the rest), for the
     * [ComposerHistory.PANES] panes written to last (the rest dropped). Off the
     * main thread.
     */
    @Synchronized fun record(context: Context, pane: String, text: String) {
        val list = ComposerHistory.recorded(load(context, pane), text)
        val p = prefs(context)
        val order = runCatching { org.json.JSONArray(p.getString(ORDER, "[]") ?: "[]").let { a -> List(a.length()) { a.getString(it) } } }.getOrDefault(emptyList())
        val (kept, dropped) = ComposerHistory.panesKept(order, pane)
        p.edit().apply {
            putString(pane, org.json.JSONArray(list).toString())
            putString(ORDER, org.json.JSONArray(kept).toString())
            dropped.forEach { remove(it) }
        }.apply()
    }
    private const val ORDER = "\u0000order"
}

/** Your messages in the conversation, oldest first, and "!" commands as you typed them. */
private fun sentMessages(state: AgentViewState): List<String> = state.blocks.mapNotNull { b ->
    when {
        b.kind == hammock.v1.Hammock.AgentBlock.Kind.KIND_USER -> b.text
        b.sentByYou() -> "!" + b.input
        else -> null
    }
}

/** A row offered over the box as you type: a "/command", or a file for an "@" mention ([detail]: its folder). */
private data class Suggestion(val insert: String, val label: String, val detail: String = "", val file: Boolean = false)

/** The most attached to one message. */
private const val MAX_ATTACHMENTS = 8

/**
 * The message box: text the full width, attachments in a tray along its top,
 * and a row of controls under it (attach, the model and context, the mic, and
 * send, which stops the agent while it works and nothing's typed).
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun Composer(session: AgentSession, state: AgentViewState, imageSize: AttachmentSize, active: Boolean = true) {
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
    // An input method still composing a word (Japanese, Chinese…): its keys
    // are its own, and the word isn't typed yet.
    val composing = value.composition != null
    // An "@file" being typed (Claude Code and Codex take file mentions).
    val mention = if (token == null && value.selection.collapsed && session.harness in setOf("claude", "codex")) SlashToken.at(value.selection.start, text, '@') else null
    val mentionQuery = mention?.let { text.substring(it.first + 1, it.last + 1) }
    // The files last found, this pane's, shown until those for what's typed
    // now come (not blinking out with each key).
    // (pane, query) they were found for, and the files.
    var files by remember(session.paneID) { mutableStateOf<Triple<String, String, List<String>>?>(null) }
    LaunchedEffect(mentionQuery, composing) {
        // The "@" word's done: its files aren't offered for the next.
        val q = mentionQuery ?: run { files = null; return@LaunchedEffect }
        if (composing) return@LaunchedEffect
        delay(120)
        files = Triple(session.paneID, q, session.mentionFiles(q))
    }
    val tokenText = (token ?: mention)?.let { text.substring(it.first, it.last + 1) }
    // Esc puts the suggestions away for the token they were for, while it's typed.
    var dismissed by remember(session.paneID) { mutableStateOf<String?>(null) }
    LaunchedEffect(tokenText) { if (tokenText != dismissed) dismissed = null }
    val suggestions: List<Suggestion> = when {
        tokenText != null && tokenText == dismissed -> emptyList()
        token != null -> SlashToken.suggestions(token, text, state.commands, state.startCommands).map { Suggestion(it, it) }
        mentionQuery != null && files.let { f -> f != null && SlashToken.mentionShows(f.first, f.second, session.paneID, mentionQuery) } -> files!!.third.map {
            Suggestion(SlashToken.mention(it), it.substringAfterLast('/'), it.substringBeforeLast('/', ""), file = true)
        }
        else -> emptyList()
    }
    // The highlighted one (↑ and ↓ move it; Tab takes it, and Return once it's
    // been moved), from the top of each new list.
    var picked by remember { mutableIntStateOf(0) }
    var pickedByKey by remember { mutableStateOf(false) }
    LaunchedEffect(suggestions) { picked = 0; pickedByKey = false }
    val highlighted = suggestions.getOrNull(picked.coerceIn(0, maxOf(suggestions.size - 1, 0)))
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

    // ↑ and ↓ on a hardware keyboard: what's been sent from this pane, as a
    // shell's history, keeping what you were typing to come back to. What's
    // been sent from here is read once, off the main thread.
    val history = remember(session.paneID) { ComposerHistory<String>() }
    // A cache of what's kept on disk (History does the read-modify-write), with
    // what's been sent since it was read laid on top: a load finishing after a
    // send doesn't drop it.
    var sentHere by remember(session.paneID) { mutableStateOf<List<String>>(emptyList()) }
    val sentSince = remember(session.paneID) { mutableListOf<String>() }
    LaunchedEffect(session.paneID) {
        val loaded = withContext(Dispatchers.IO) { History.load(context, session.paneID) }
        sentHere = ComposerHistory.withSent(loaded, sentSince)
    }
    fun historyKey(up: Boolean): Boolean {
        if (dictation.active || composing) return false
        val cursor = if (value.selection.collapsed) value.selection.start else null
        // ↑ steps on from anywhere on the first line of a line brought back
        // unedited (edited, from its very start); ↓ from the last line of any
        // brought back, its edit kept.
        if (!ComposerHistory.steps(up, text, cursor, if (up) history.unchanged(text) else history.browsing)) return false
        val shown = if (up) {
            history.up(text, text) { ComposerHistory.merged(sentHere, sentMessages(state)) } ?: return false
        } else {
            val (entry, draft) = history.down(text, text) ?: return false
            entry ?: draft ?: ""
        }
        justSent = null // brought back on purpose, not a ghost of what was sent
        // Another message in the box: a failed send's error (and Retry) is done with, as typing does.
        if (state.sendError != null) session.clearSendError()
        setText(shown)
        return true
    }

    fun complete(s: Suggestion) {
        val tk = (if (s.file) mention else token) ?: return
        val (t, cursor) = SlashToken.complete(tk, s.insert, text)
        if (state.sendError != null) session.clearSendError() // edited, as typing does
        value = TextFieldValue(t, TextRange(cursor.coerceAtMost(t.length)))
    }

    // A hardware keyboard over the suggestions: ↑ and ↓ move, Tab takes the
    // highlighted one, Return too once it's been moved (else Return starts a
    // line, as it always does here), Esc puts them away.
    fun suggestionKey(key: Key): Boolean {
        if (composing) return false
        val h = highlighted ?: return false
        when (key) {
            Key.DirectionUp -> { picked = (picked - 1 + suggestions.size) % suggestions.size; pickedByKey = true }
            Key.DirectionDown -> { picked = (picked + 1) % suggestions.size; pickedByKey = true }
            Key.Tab -> complete(h)
            Key.Enter -> if (SlashToken.returnTakes(suggesting = true, moved = pickedByKey)) complete(h) else return false
            Key.Escape -> dismissed = tokenText
            else -> return false
        }
        return true
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
        // A prompt's on screen (the card is taking this box's place): the agent's
        // terminal shows it, so a send mustn't type into it. The text stays.
        if (!canSend || state.prompt.kind != hammock.v1.Hammock.AgentPrompt.Kind.KIND_NONE) return
        val sentText = text
        sentSince.add(sentText)
        sentHere = ComposerHistory.recorded(sentHere, sentText)
        val pane = session.paneID
        scope.launch(Dispatchers.IO) { History.record(context, pane, sentText) }
        history.reset()
        dismissed = null
        files = null
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

    // Saved once typing pauses, and on leaving. While stepping through what
    // was sent, what you were typing is the draft, not the message shown.
    LaunchedEffect(text) { delay(400); Drafts.save(context, session.paneID, history.savedDraft ?: text) }
    DisposableEffect(session.paneID) {
        onDispose {
            dictation.cancel()
            Drafts.save(context, session.paneID, history.savedDraft ?: value.text)
        }
    }
    // Back as a prompt that had the keyboard ends, in the pane on screen with a
    // hardware keyboard: the keyboard's the box's again (as on the iPhone).
    val hardKeyboard = androidx.compose.ui.platform.LocalConfiguration.current.hardKeyboardHidden == android.content.res.Configuration.HARDKEYBOARDHIDDEN_NO
    LaunchedEffect(Unit) {
        val retake = PromptKeys.composerRetakes(session.promptHadKeys, session.promptEndedAtMs, PromptKeys.now(), active, hardKeyboard)
        session.promptHadKeys = false
        if (retake) { delay(100); runCatching { focus.requestFocus() } }
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
                        val on = s == highlighted && suggestions.size > 1
                        Row(
                            Modifier.fillMaxWidth().padding(horizontal = 6.dp).clip(RoundedCornerShape(12.dp))
                                .background(if (on) c.accent.copy(alpha = 0.14f) else androidx.compose.ui.graphics.Color.Transparent)
                                .clickable { complete(s) }.padding(horizontal = 10.dp, vertical = 9.dp),
                            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            if (s.file) Icon(Icons.Outlined.Description, null, tint = c.secondary, modifier = Modifier.size(16.dp))
                            Text(s.label, style = codeTextStyle(1f), maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                            if (s.detail.isNotEmpty()) {
                                Text(s.detail, style = MaterialTheme.typography.labelMedium, color = c.secondary, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
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
                            if (it.text != value.text) {
                                // Typing: a prompt card coming now waits before taking the keys.
                                session.composerEditedAtMs = PromptKeys.now()
                            }
                            value = it
                        },
                        textStyle = textStyle, cursorBrush = SolidColor(c.accent), maxLines = 8,
                        visualTransformation = transformation,
                        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                        modifier = Modifier.fillMaxWidth().heightIn(min = 24.dp).focusRequester(focus)
                            .onPreviewKeyEvent { e ->
                            if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                            when {
                                // A hardware keyboard: ⌘/Ctrl-Return sends; Return alone starts a line, as
                                // it always has here (or takes a suggestion once ↑ or ↓ has moved to it).
                                e.key == Key.Enter && (e.isCtrlPressed || e.isMetaPressed) -> { if (stops) session.interrupt() else send(); true }
                                e.key == Key.Escape && dictation.active -> { dictation.stop(); true }
                                // Not with Shift (selecting), Ctrl, Alt or Meta held: those are the field's.
                                !e.isShiftPressed && !e.isCtrlPressed && !e.isAltPressed && !e.isMetaPressed && suggestionKey(e.key) -> true
                                (e.key == Key.DirectionUp || e.key == Key.DirectionDown) &&
                                    !e.isShiftPressed && !e.isCtrlPressed && !e.isAltPressed && !e.isMetaPressed -> historyKey(e.key == Key.DirectionUp)
                                else -> false
                            }
                        },
                    )
                }
                Row(Modifier.fillMaxWidth().padding(start = 8.dp, end = 8.dp, bottom = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    Box {
                        IconButton(onClick = { attachMenu = true }, colors = IconButtonDefaults.iconButtonColors(containerColor = c.fill), modifier = Modifier.size(36.dp)) {
                            Icon(Icons.Outlined.Add, "Attach files or photos")
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
