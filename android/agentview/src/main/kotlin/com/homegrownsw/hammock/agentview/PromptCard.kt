package com.homegrownsw.hammock.agentview

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Assignment
import androidx.compose.material.icons.automirrored.outlined.HelpOutline
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowLeft
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowRight
import androidx.compose.material.icons.outlined.ArrowUpward
import androidx.compose.material.icons.outlined.CheckBox
import androidx.compose.material.icons.outlined.CheckBoxOutlineBlank
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.Security
import androidx.compose.material.icons.outlined.Warning
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.focusable
import android.content.res.Configuration
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.isImeVisible
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.ui.input.pointer.pointerInput
import kotlinx.coroutines.delay
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isAltPressed
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import hammock.v1.Hammock.AgentPrompt
import kotlinx.coroutines.launch

/**
 * The prompt on the agent's screen, to answer here: a permission, a question
 * (options, ticks, typed answers, previews with notes), or a plan. Scrolls past
 * [maxHeight]. When an answer can't be matched to the screen, "Answer in
 * Terminal" takes you there.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
internal fun PromptCard(session: AgentSession, state: AgentViewState, maxHeight: Dp, onOpenTerminal: () -> Unit, active: Boolean = true) {
    val c = LocalAgentColors.current
    val p = state.prompt
    val scope = rememberCoroutineScope()
    val haptics = LocalHapticFeedback.current
    val pending = remember(state.blocks, p.blockId) { state.blocks.lastOrNull { it.id == p.blockId } }
    // Reset whenever the prompt changes: the next question, or a new prompt
    // with the same options (its own state, and its own moment before keys count).
    val key = p.promptId to p.optionsList
    var ticked by remember(key) { mutableStateOf(p.checkedList.withIndex().filter { it.value }.map { it.index + 1 }.toSet()) }
    var typing by remember(key) { mutableStateOf(false) }
    var otherText by remember(key) { mutableStateOf("") }
    var notes by remember(key) { mutableStateOf("") }
    var chosen by remember(key) { mutableIntStateOf(if (p.selected > 0) p.selected else 1) }
    var tapped by remember(key) { mutableIntStateOf(0) }
    LaunchedEffect(state.answering) { if (!state.answering) tapped = 0 }

    val name = session.agentName
    val heading = when (p.kind) {
        AgentPrompt.Kind.KIND_PERMISSION -> pending?.let { "$name wants to: ${it.title}" } ?: "$name needs permission"
        AgentPrompt.Kind.KIND_PLAN -> "Approve the plan?"
        else -> "$name is asking"
    }
    val tint = when (p.kind) {
        AgentPrompt.Kind.KIND_PERMISSION -> c.warning
        AgentPrompt.Kind.KIND_PLAN -> c.plan
        else -> c.accent
    }
    val icon = when (p.kind) {
        AgentPrompt.Kind.KIND_PERMISSION -> Icons.Outlined.Security
        AgentPrompt.Kind.KIND_PLAN -> Icons.AutoMirrored.Outlined.Assignment
        else -> Icons.AutoMirrored.Outlined.HelpOutline
    }
    // Which of several questions this is: the one shown among the call's,
    // else guessed from the tab bar ("☒ Colour ☐ Fruits ✔ Submit") as the
    // first unanswered. Null for a single question.
    val position: Pair<Int, Int>? = run {
        if (p.tabs.isEmpty()) return@run null
        val qs = pending?.questionsList
        if (qs != null && qs.size > 1) {
            val i = qs.indexOfFirst { it.question == p.title }
            if (i >= 0) return@run (i + 1) to qs.size
        }
        val done = p.tabs.count { it == '☒' }
        val total = p.tabs.count { it == '☒' || it == '☐' }
        if (total > 1) minOf(done + 1, total) to total else null
    }

    fun answer(option: Int, text: String = "", n: String = "") {
        tapped = option
        scope.launch { session.answer(option, text, n) }
    }
    // Multi-select's Continue: the ticked options, and what's typed in the
    // "other" option's field (ticked along with them).
    fun sendTicked() {
        if (state.answering) return
        val t = if (typing) otherText.trim() else ""
        scope.launch { session.answer(ticked.sorted(), t) }
    }
    fun sendOther() {
        val t = otherText.trim()
        if (t.isEmpty() || state.answering) return
        if (p.multiSelect) sendTicked() else answer(p.other, t)
    }
    // In a multi-select question, whether option n is a checkbox: the
    // options after them ("Chat about this") answer at once. (A daemon that
    // doesn't say: all of them.)
    fun isCheckbox(n: Int) = p.multiSelect && (p.checkboxes == 0 || n <= p.checkboxes)
    fun sendChosen() {
        if (chosen < 1 || state.answering) return
        answer(chosen, n = notes.trim())
    }
    // Option n chosen, by a tap or its number key.
    fun pick(n: Int) {
        when {
            n == p.other -> typing = true
            isCheckbox(n) -> ticked = if (n in ticked) ticked - n else ticked + n
            p.notes -> { chosen = n; haptics.performHapticFeedback(HapticFeedbackType.TextHandleMove) }
            else -> answer(n)
        }
    }

    // A hardware keyboard: the card takes 1–9, ← and → and Esc while you're
    // not typing in one of its fields. By itself in the pane on screen
    // ([active]): at once when nothing's been typed in the message box for a
    // second, else once that second's passed; not while the software keyboard
    // is up for another field. A tap on the card gives them to it too.
    // Focusable only with a hardware keyboard, so it's no extra TalkBack stop.
    val hardKeyboard = LocalConfiguration.current.hardKeyboardHidden == Configuration.HARDKEYBOARDHIDDEN_NO
    val imeUp by rememberUpdatedState(WindowInsets.isImeVisible)
    val keys = remember { FocusRequester() }
    var keysFocused by remember { mutableStateOf(false) }
    // When this question appeared: number keys only count a moment after.
    val shownAt = remember(key) { System.currentTimeMillis() }
    // When the card took the keyboard, and when it last saw a key (any key).
    var keysTakenAt by remember { mutableLongStateOf(0L) }
    var lastKeyAt by remember { mutableLongStateOf(0L) }
    LaunchedEffect(key, typing, hardKeyboard, active) {
        if (!hardKeyboard || typing || !active) return@LaunchedEffect
        val wait = PromptKeys.keyDelayMs(session.composerEditedAtMs, System.currentTimeMillis())
        if (wait > 0) delay(wait)
        if (!imeUp) runCatching { keys.requestFocus() }
    }
    Surface(
        shape = RoundedCornerShape(24.dp), tonalElevation = 3.dp, shadowElevation = 6.dp,
        modifier = Modifier.fillMaxWidth().heightIn(max = maxHeight).focusRequester(keys)
            // Whether the card (or one of its fields) has the keyboard, for the
            // message box after it; and a tap on its background gives it the keys
            // (an option's own tap is taken first).
            .onFocusChanged {
                if (it.isFocused && !keysFocused) keysTakenAt = System.currentTimeMillis()
                keysFocused = it.isFocused
                if (it.hasFocus) session.promptHadKeys = true
            }
            .pointerInput(hardKeyboard) { if (hardKeyboard) detectTapGestures { runCatching { keys.requestFocus() } } }
            .focusable(enabled = hardKeyboard).onPreviewKeyEvent { e ->
            // Only the card's own focus: not while typing in one of its fields.
            if (e.type != KeyEventType.KeyDown || !keysFocused) return@onPreviewKeyEvent false
            // Every key the card sees counts as typing, ignored ones too: a digit
            // only picks after a second's quiet (PromptKeys.acceptsPick).
            val now = System.currentTimeMillis()
            val quiet = PromptKeys.acceptsPick(now, shownAt, keysTakenAt, lastKeyAt)
            lastKeyAt = now
            val k = when (e.key) {
                Key.Escape -> PromptKeys.Key.ESCAPE
                Key.DirectionLeft -> PromptKeys.Key.LEFT
                Key.DirectionRight -> PromptKeys.Key.RIGHT
                else -> PromptKeys.Key.OTHER
            }
            val digit = when (e.key) {
                Key.One, Key.NumPad1 -> 1; Key.Two, Key.NumPad2 -> 2; Key.Three, Key.NumPad3 -> 3
                Key.Four, Key.NumPad4 -> 4; Key.Five, Key.NumPad5 -> 5; Key.Six, Key.NumPad6 -> 6
                Key.Seven, Key.NumPad7 -> 7; Key.Eight, Key.NumPad8 -> 8; Key.Nine, Key.NumPad9 -> 9
                else -> 0
            }
            // Shift with a digit is its symbol ("!"), as on the Mac: not a pick.
            val mods = e.isCtrlPressed || e.isMetaPressed || e.isAltPressed || (digit > 0 && e.isShiftPressed)
            when (val a = PromptKeys.action(digit, k, mods, p.optionsCount, position != null)) {
                is PromptKeys.Action.Pick -> if (!state.answering && quiet) pick(a.n)
                PromptKeys.Action.Previous -> if ((position?.first ?: 0) > 1) session.keys("\u001b[D")
                PromptKeys.Action.Next -> session.keys("\u001b[C")
                PromptKeys.Action.Escape -> onOpenTerminal()
                null -> return@onPreviewKeyEvent false
            }
            true
        },
    ) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(14.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                Icon(icon, null, tint = tint, modifier = Modifier.size(16.dp))
                Text(heading, style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.SemiBold, color = c.secondary, modifier = Modifier.weight(1f, fill = false))
                if (position != null) Text("· ${position.first} of ${position.second}", style = MaterialTheme.typography.labelMedium, color = c.tertiary)
                Spacer(Modifier.weight(1f))
                if (position != null) {
                    // Through the questions, as ← and → do in Claude's form.
                    IconButton(onClick = { session.keys("\u001b[D") }, enabled = position.first > 1, modifier = Modifier.size(32.dp)) {
                        Icon(Icons.AutoMirrored.Outlined.KeyboardArrowLeft, "Previous question")
                    }
                    IconButton(onClick = { session.keys("\u001b[C") }, modifier = Modifier.size(32.dp)) {
                        Icon(Icons.AutoMirrored.Outlined.KeyboardArrowRight, "Next question")
                    }
                }
            }
            if (pending != null && p.kind == AgentPrompt.Kind.KIND_PERMISSION && pending.input.isNotEmpty()) {
                CodeBlock(pending.input, if (pending.tool == "Bash") "bash" else "", maxHeight = 140.dp, wrapsByDefault = true)
            }
            if (p.title.isNotEmpty()) Text(p.title, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.SemiBold)
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                p.optionsList.forEachIndexed { i, label ->
                    val n = i + 1
                    if (n == p.other && typing) {
                        OtherField(
                            text = otherText, onText = { t ->
                                // Return in a field that grows: send, not a new line.
                                if (t.contains('\n')) { otherText = t.replace("\n", ""); sendOther() } else otherText = t
                            },
                            placeholder = if (p.kind == AgentPrompt.Kind.KIND_PLAN) "What should $name change?" else "Your answer",
                            sending = state.answering && tapped == n,
                            onSend = ::sendOther, onCancel = { typing = false; otherText = "" },
                        )
                    } else {
                        val desc = p.descriptionsList.getOrElse(i) { "" }
                        val isOther = n == p.other
                        val isBox = isCheckbox(n) && !isOther
                        val picked = p.notes && n == chosen
                        OptionRow(
                            n, label, desc, isOther = isOther, multi = isBox, ticked = n in ticked, picked = picked,
                            spinning = tapped == n && state.answering, enabled = !state.answering,
                        ) { pick(n) }
                    }
                }
            }
            if (p.notes) {
                // A question with previews: the picked option's (a mockup,
                // monospaced, as Claude shows it beside the list), notes, Answer.
                val preview = p.previewsList.getOrElse(chosen - 1) { "" }
                if (preview.isNotEmpty()) {
                    Text(
                        preview, style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace), softWrap = false,
                        modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(Color.Black.copy(alpha = 0.18f))
                            .border(1.dp, c.fill, RoundedCornerShape(10.dp)).horizontalScroll(rememberScrollState()).padding(12.dp),
                    )
                }
                Row(verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Box(Modifier.weight(1f).clip(RoundedCornerShape(10.dp)).background(c.fill).padding(horizontal = 12.dp, vertical = 10.dp)) {
                        if (notes.isEmpty()) Text("Notes (optional)", style = MaterialTheme.typography.bodyLarge, color = c.tertiary)
                        BasicTextField(
                            notes, { t -> if (t.contains('\n')) { notes = t.replace("\n", ""); sendChosen() } else notes = t },
                            textStyle = MaterialTheme.typography.bodyLarge.copy(color = MaterialTheme.colorScheme.onSurface),
                            cursorBrush = SolidColor(c.accent), maxLines = 5,
                            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send), keyboardActions = KeyboardActions(onSend = { sendChosen() }),
                            // Esc leaves the notes, the keys back to the card (not Back out of the pane).
                            modifier = Modifier.fillMaxWidth().onPreviewKeyEvent { e ->
                                if (e.type == KeyEventType.KeyDown && e.key == Key.Escape && hardKeyboard) { runCatching { keys.requestFocus() }; true } else false
                            },
                        )
                    }
                    Button(onClick = ::sendChosen, enabled = chosen >= 1 && !state.answering, colors = ButtonDefaults.buttonColors(containerColor = c.accent, contentColor = c.onAccent)) {
                        if (state.answering) { CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp, color = c.onAccent); Spacer(Modifier.size(6.dp)) }
                        Text("Answer $chosen")
                    }
                }
            }
            if (p.multiSelect) {
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    Button(
                        onClick = ::sendTicked, enabled = !state.answering,
                        colors = ButtonDefaults.buttonColors(containerColor = c.accent, contentColor = c.onAccent),
                    ) { Text("Continue") }
                }
            }
            val err = state.answerError
            if (err != null) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Icon(Icons.Outlined.Warning, null, tint = c.warning, modifier = Modifier.size(16.dp))
                    Text(err, style = MaterialTheme.typography.labelMedium, modifier = Modifier.weight(1f))
                    TextButton(onClick = { session.clearAnswerError(); onOpenTerminal() }) { Text("Answer in Terminal") }
                }
            }
        }
    }
}

@Composable
private fun OptionRow(
    n: Int, label: String, desc: String, isOther: Boolean, multi: Boolean, ticked: Boolean, picked: Boolean,
    spinning: Boolean, enabled: Boolean, onClick: () -> Unit,
) {
    val c = LocalAgentColors.current
    val (text, recommended) = CardText.recommended(label)
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp))
            .background(if (picked) c.accent.copy(alpha = 0.14f) else c.fill)
            .border(if (picked) 1.5.dp else 0.dp, if (picked) c.accent else Color.Transparent, RoundedCornerShape(10.dp))
            .clickable(enabled = enabled, onClick = onClick).padding(horizontal = 12.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        when {
            multi && !isOther -> Icon(if (ticked) Icons.Outlined.CheckBox else Icons.Outlined.CheckBoxOutlineBlank, null, tint = c.accent, modifier = Modifier.size(20.dp))
            isOther -> Box(Modifier.size(22.dp).clip(CircleShape).background(c.fill), contentAlignment = Alignment.Center) {
                Icon(Icons.Outlined.Edit, null, modifier = Modifier.size(13.dp))
            }
            else -> Box(Modifier.size(22.dp).clip(CircleShape).background(if (picked) c.accent else c.fill), contentAlignment = Alignment.Center) {
                Text("$n", style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.SemiBold, color = if (picked) c.onAccent else MaterialTheme.colorScheme.onSurface)
            }
        }
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(text, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f, fill = false))
                if (recommended) {
                    Text(
                        "Recommended", style = MaterialTheme.typography.labelSmall, fontWeight = FontWeight.SemiBold, color = c.accent,
                        modifier = Modifier.clip(CircleShape).background(c.accent.copy(alpha = 0.15f)).padding(horizontal = 6.dp, vertical = 1.dp),
                    )
                }
            }
            if (desc.isNotEmpty() && desc != label) Text(desc, style = MaterialTheme.typography.labelMedium, color = c.secondary)
        }
        if (spinning) CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
    }
}

/** The "other" option, become the field you type your answer in, where it was: focused at once, sent with Return or its button. */
@Composable
private fun OtherField(text: String, onText: (String) -> Unit, placeholder: String, sending: Boolean, onSend: () -> Unit, onCancel: () -> Unit) {
    val c = LocalAgentColors.current
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    val empty = text.isBlank()
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(c.accent.copy(alpha = 0.14f))
            .border(1.5.dp, c.accent, RoundedCornerShape(10.dp)).padding(start = 12.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.weight(1f).padding(vertical = 8.dp)) {
            if (text.isEmpty()) Text(placeholder, style = MaterialTheme.typography.bodyLarge, color = c.tertiary)
            BasicTextField(
                text, onText, textStyle = MaterialTheme.typography.bodyLarge.copy(color = MaterialTheme.colorScheme.onSurface),
                cursorBrush = SolidColor(c.accent), maxLines = 6,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send), keyboardActions = KeyboardActions(onSend = { onSend() }),
                // Esc stops typing the answer, as on the iPhone and the Mac.
                modifier = Modifier.fillMaxWidth().focusRequester(focus).onPreviewKeyEvent { e ->
                    if (e.type == KeyEventType.KeyDown && e.key == Key.Escape) { onCancel(); true } else false
                },
            )
        }
        if (sending) CircularProgressIndicator(Modifier.padding(10.dp).size(18.dp), strokeWidth = 2.dp)
        else IconButton(onClick = { if (empty) onCancel() else onSend() }) {
            Icon(if (empty) Icons.Outlined.Close else Icons.Outlined.ArrowUpward, if (empty) "Cancel" else "Send", tint = if (empty) c.secondary else c.accent)
        }
    }
}
