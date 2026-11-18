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
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
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
@Composable
internal fun PromptCard(session: AgentSession, state: AgentViewState, maxHeight: Dp, onOpenTerminal: () -> Unit) {
    val c = LocalAgentColors.current
    val p = state.prompt
    val scope = rememberCoroutineScope()
    val haptics = LocalHapticFeedback.current
    val pending = remember(state.blocks, p.blockId) { state.blocks.lastOrNull { it.id == p.blockId } }
    // Reset whenever the prompt's options change (the next question).
    val key = p.optionsList
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
    fun sendOther() {
        val t = otherText.trim()
        if (t.isEmpty() || state.answering) return
        answer(p.other, t)
    }
    fun sendChosen() {
        if (chosen < 1 || state.answering) return
        answer(chosen, n = notes.trim())
    }

    Surface(
        shape = RoundedCornerShape(24.dp), tonalElevation = 3.dp, shadowElevation = 6.dp,
        modifier = Modifier.fillMaxWidth().heightIn(max = maxHeight),
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
                        val picked = p.notes && n == chosen
                        OptionRow(
                            n, label, desc, isOther = isOther, multi = p.multiSelect, ticked = n in ticked, picked = picked,
                            spinning = tapped == n && state.answering, enabled = !state.answering,
                        ) {
                            when {
                                isOther -> typing = true
                                p.multiSelect -> ticked = if (n in ticked) ticked - n else ticked + n
                                p.notes -> { chosen = n; haptics.performHapticFeedback(HapticFeedbackType.TextHandleMove) }
                                else -> answer(n)
                            }
                        }
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
                            modifier = Modifier.fillMaxWidth(),
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
                        onClick = { scope.launch { session.answer(ticked.sorted()) } }, enabled = !state.answering,
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
                modifier = Modifier.fillMaxWidth().focusRequester(focus),
            )
        }
        if (sending) CircularProgressIndicator(Modifier.padding(10.dp).size(18.dp), strokeWidth = 2.dp)
        else IconButton(onClick = { if (empty) onCancel() else onSend() }) {
            Icon(if (empty) Icons.Outlined.Close else Icons.Outlined.ArrowUpward, if (empty) "Cancel" else "Send", tint = if (empty) c.secondary else c.accent)
        }
    }
}
