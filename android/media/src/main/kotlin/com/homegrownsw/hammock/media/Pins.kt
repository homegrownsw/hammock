package com.homegrownsw.hammock.media

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.PushPin
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
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
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppCover
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import hammock.v1.Hammock.PinCommand.Action
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlin.math.roundToInt

// Design notes on a browser or simulator pane (the daemon's pins.go): touch
// and hold on the page or screen to drop a numbered pin, write what should
// change there, and send them all to an agent as one message. The board is
// the pane's, the same on every device; pin mode (whether touches drop pins
// or reach the page) is this screen's own.

/** A pin command for [paneID]; null if it failed (model.lastError says why). */
internal suspend fun AppModel.pinCommand(paneID: String, action: Action, build: Hammock.PinCommand.Builder.() -> Unit = {}): Hammock.PinReply? {
    lastError = null
    val r = request { setPinCommand(Hammock.PinCommand.newBuilder().setPaneId(paneID).setAction(action).apply(build)) } ?: return null
    return r.pinReply
}

/**
 * The pane's pins over [page] (the page or screen as drawn, in this view's
 * coordinates; badges kept inside [bounds], the part of it showing). A badge
 * opens its note. In pin mode, a touch and hold drops a pin and nothing
 * reaches the page; otherwise only the badges take touches.
 */
@Composable
internal fun PinOverlay(
    model: AppModel,
    pane: Hammock.Pane,
    pinMode: Boolean,
    page: Rect,
    bounds: Rect,
    scope: CoroutineScope,
    onEdit: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val pins = pane.pins.pinsList
    if (pins.isEmpty() && !pinMode) return
    val density = LocalDensity.current
    val haptics = LocalHapticFeedback.current
    val accent = MaterialTheme.colorScheme.primary
    val now by rememberUpdatedState(page)
    val edit by rememberUpdatedState(onEdit)
    val badge = 24.dp
    val radius = with(density) { badge.toPx() / 2 }
    val lift = with(density) { Offset(14.dp.toPx(), -14.dp.toPx()) }
    Box(modifier) {
        if (pinMode) {
            Box(Modifier.fillMaxSize().pointerInput(pane.id) {
                detectTapGestures(onLongPress = { p ->
                    val (x, y) = PinLayout.fraction(p, now) ?: return@detectTapGestures
                    haptics.performHapticFeedback(HapticFeedbackType.LongPress)
                    scope.launch {
                        val r = model.pinCommand(pane.id, Action.ACTION_ADD) { setX(x); setY(y) }
                        if (r == null) model.showToast("Couldn't pin a note: ${model.lastError ?: "unknown error"}")
                        else if (r.pinId.isNotEmpty()) edit(r.pinId)
                    }
                })
            })
        }
        Canvas(Modifier.fillMaxSize()) {
            for (pin in pins) {
                PinLayout.outline(pin, page)?.let { r ->
                    drawRect(accent, r.topLeft, r.size, style = Stroke(1.5.dp.toPx()))
                }
                val p = PinLayout.point(pin.x, pin.y, page)
                if (!PinLayout.shows(p, bounds)) continue
                drawCircle(Color.White, 4.5.dp.toPx(), p)
                drawCircle(accent, 3.dp.toPx(), p)
            }
        }
        for (pin in pins) {
            val p = PinLayout.point(pin.x, pin.y, page)
            if (!PinLayout.shows(p, bounds)) continue
            val c = PinLayout.badgeCentre(p, lift, radius, bounds)
            Box(
                Modifier
                    .offset { IntOffset((c.x - radius).roundToInt(), (c.y - radius).roundToInt()) }
                    .size(badge)
                    .shadow(3.dp, CircleShape)
                    .background(accent, CircleShape)
                    .border(2.dp, Color.White, CircleShape)
                    .clickable { onEdit(pin.id) }
                    .semantics { contentDescription = "Note ${pin.number}" + if (pin.note.isNotEmpty()) ": ${pin.note}" else "" },
                contentAlignment = Alignment.Center,
            ) {
                Text("${pin.number}", color = Color.White, fontWeight = FontWeight.Bold, fontSize = 12.sp, maxLines = 1)
            }
        }
    }
}

/**
 * Along the pane's bottom while pin mode is on or it has pins: how many,
 * Clear, Send (to the agent named, or another chosen from its menu), and
 * Done (or Add, to pin more). Sends on [scope], the pane's: the bar can go before the reply comes.
 */
@Composable
internal fun PinBar(model: AppModel, pane: Hammock.Pane, pinMode: Boolean, canDrive: Boolean, scope: CoroutineScope, onPinMode: (Boolean) -> Unit) {
    val board = pane.pins
    val count = board.pinsCount
    AnimatedVisibility(canDrive && (pinMode || count > 0), enter = expandVertically() + fadeIn(), exit = shrinkVertically() + fadeOut()) {
        val w by model.workspace.collectAsStateWithLifecycle()
        var sending by remember { mutableStateOf(false) }
        var choosing by remember { mutableStateOf(false) }
        var clearing by remember { mutableStateOf(false) }
        fun agentName(p: Hammock.Pane) = AgentCatalog.shortName(p.agent.harness, w.kinds)
        val target = w.panes[board.targetPaneId]?.takeIf { it.hasAgent() }
        val targetName = target?.let(::agentName)
        val group = w.groupOfPane(pane.id)
        val targets = PinLayout.targets(group?.let { g -> w.tabs(g).flatMap { w.panes(it) } } ?: emptyList(), pane.id)
        val labels = PinLayout.targetLabels(targets, ::agentName) { p -> w.tabs[p.tabId]?.let { w.title(it) } ?: "" }

        fun send() {
            val name = targetName ?: "the agent"
            sending = true
            scope.launch {
                val r = model.pinCommand(pane.id, Action.ACTION_SEND)
                sending = false
                when {
                    r == null -> model.showToast("Couldn't send the notes: ${model.lastError ?: "unknown error"}")
                    r.queued -> model.showToast("Held until $name has your answer: it's waiting on you")
                    else -> { model.showToast("Notes sent to $name"); onPinMode(false) }
                }
            }
        }

        fun command(action: Action, what: String, build: Hammock.PinCommand.Builder.() -> Unit = {}) = scope.launch {
            if (model.pinCommand(pane.id, action, build) == null) model.showToast("Couldn't $what: ${model.lastError ?: "unknown error"}")
        }

        Surface(
            shape = RoundedCornerShape(20.dp), tonalElevation = 3.dp, shadowElevation = 4.dp,
            modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp),
        ) {
            Column(Modifier.padding(start = 16.dp, end = 8.dp, top = 4.dp, bottom = 8.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        PinLayout.status(count, board.queued), style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f),
                    )
                    if (count > 0) TextButton(onClick = { clearing = true }) { Text("Clear") }
                    if (pinMode) TextButton(onClick = { onPinMode(false) }) { Text("Done") }
                    else TextButton(onClick = { onPinMode(true) }) { Text("Add") }
                }
                Row(Modifier.padding(end = 8.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                    Button(
                        onClick = ::send, enabled = count > 0 && target != null && !sending && !board.queued,
                        modifier = Modifier.weight(1f).heightIn(min = 40.dp),
                    ) { Text(if (targetName != null) "Send to $targetName" else "No Agent to Send To", maxLines = 1, overflow = TextOverflow.Ellipsis) }
                    Box {
                        TextButton(onClick = { choosing = true }, modifier = Modifier.semantics { contentDescription = "Choose Agent" }) {
                            Icon(Icons.Filled.ArrowDropDown, null)
                        }
                        DropdownMenu(choosing, { choosing = false }) {
                            DropdownMenuItem(
                                text = { Text("Let Hammock choose") },
                                leadingIcon = { Checked(!board.targetChosen) },
                                onClick = { choosing = false; command(Action.ACTION_SET_TARGET, "choose the agent") { setTargetPaneId("") } },
                            )
                            if (targets.isNotEmpty()) HorizontalDivider()
                            targets.forEachIndexed { i, p ->
                                DropdownMenuItem(
                                    text = { Text(labels[i]) },
                                    leadingIcon = { Checked(board.targetChosen && board.targetPaneId == p.id) },
                                    onClick = { choosing = false; command(Action.ACTION_SET_TARGET, "choose the agent") { setTargetPaneId(p.id) } },
                                )
                            }
                        }
                    }
                }
            }
        }

        val locked by AppCover.locked.collectAsState()
        if (clearing && !locked) AlertDialog(
            onDismissRequest = { clearing = false },
            title = { Text(if (count == 1) "Clear the Note?" else "Clear $count Notes?") },
            confirmButton = { TextButton(onClick = { clearing = false; command(Action.ACTION_CLEAR, "clear the notes") }) { Text("Clear") } },
            dismissButton = { TextButton(onClick = { clearing = false }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun Checked(on: Boolean) = if (on) Icon(Icons.Filled.Check, "Chosen") else Spacer(Modifier.width(24.dp))

/**
 * A pin's note: its number and what's under it, and what should change
 * there. Saved as it closes (Done, or swiping it away); Delete Pin removes it.
 * Closes itself if the pin goes (sent, cleared, deleted elsewhere).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun PinNoteSheet(model: AppModel, pane: Hammock.Pane, pinID: String, canDrive: Boolean, scope: CoroutineScope, onClose: () -> Unit) {
    val pin = pane.pins.pinsList.firstOrNull { it.id == pinID }
    // A new pin's id can come before the board with it: wait for it, then close once it's gone.
    var seen by remember(pinID) { mutableStateOf(false) }
    LaunchedEffect(pin == null) { if (pin != null) seen = true else if (seen) onClose() }
    val locked by AppCover.locked.collectAsState()
    if (pin == null || locked) return
    // The pane's scope sends: this one goes with the sheet.
    val sheetScope = rememberCoroutineScope()
    val state = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val original = remember(pinID) { pin.note }
    var text by remember(pinID) { mutableStateOf(pin.note) }

    fun save() {
        if (canDrive && text != original) scope.launch {
            if (model.pinCommand(pane.id, Action.ACTION_NOTE) { setPinId(pinID); setNote(text) } == null) {
                model.showToast("Couldn't save the note: ${model.lastError ?: "unknown error"}")
            }
        }
    }
    fun hide(then: () -> Unit) = sheetScope.launch { state.hide() }.invokeOnCompletion { then(); onClose() }

    ModalBottomSheet(onDismissRequest = { save(); onClose() }, sheetState = state) {
        Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                Box(Modifier.size(28.dp).background(MaterialTheme.colorScheme.primary, CircleShape), contentAlignment = Alignment.Center) {
                    Text("${pin.number}", color = Color.White, fontWeight = FontWeight.Bold, fontSize = 13.sp)
                }
                Column(Modifier.weight(1f)) {
                    Text("Note ${pin.number}", style = MaterialTheme.typography.titleMedium)
                    if (pin.what.isNotEmpty()) {
                        Text(pin.what, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
            OutlinedTextField(
                text, { text = it }, enabled = canDrive,
                placeholder = { Text("What should change here?") },
                minLines = 3, maxLines = 8, modifier = Modifier.fillMaxWidth(),
            )
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                if (canDrive) TextButton(onClick = {
                    hide {
                        scope.launch {
                            if (model.pinCommand(pane.id, Action.ACTION_DELETE) { setPinId(pinID) } == null) {
                                model.showToast("Couldn't delete the pin: ${model.lastError ?: "unknown error"}")
                            }
                        }
                    }
                }) { Text("Delete Pin", color = MaterialTheme.colorScheme.error) }
                Spacer(Modifier.weight(1f))
                Button(onClick = { hide { save() } }) { Text("Done") }
            }
        }
    }
}
