package com.homegrownsw.hammock.agentview

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Assignment
import androidx.compose.material.icons.automirrored.outlined.HelpOutline
import androidx.compose.material.icons.automirrored.outlined.Reply
import androidx.compose.material.icons.automirrored.outlined.Send
import androidx.compose.material.icons.automirrored.outlined.Undo
import androidx.compose.material.icons.outlined.AttachFile
import androidx.compose.material.icons.outlined.AutoAwesome
import androidx.compose.material.icons.outlined.Build
import androidx.compose.material.icons.outlined.Cancel
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.Checklist
import androidx.compose.material.icons.outlined.ChevronRight
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Extension
import androidx.compose.material.icons.outlined.Groups
import androidx.compose.material.icons.outlined.Layers
import androidx.compose.material.icons.outlined.PostAdd
import androidx.compose.material.icons.outlined.Psychology
import androidx.compose.material.icons.outlined.Public
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.CompositingStrategy
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import hammock.v1.Hammock.AgentBlock
import kotlinx.coroutines.delay

/** One row of the conversation. Skips recomposing unless its own blocks change ([AgentItem] is immutable). */
@Composable
internal fun AgentRow(item: AgentItem, session: AgentSession) {
    val b = item.block
    if (b == null) {
        if (item.tools.size == 1) ToolRow(item.tools[0], session) else ToolGroup(item.id, item.tools, session)
        return
    }
    when (b.kind) {
        AgentBlock.Kind.KIND_USER -> UserBubble(b.text, b.richText, b.imagesList.map { it.ref }, emptyList(), session)
        AgentBlock.Kind.KIND_TEXT -> RevealsReply(b.id, b.text.length, session) { MarkdownText(b.text) }
        AgentBlock.Kind.KIND_THINKING -> ThinkingRow(b, item.thoughtSeconds, session)
        AgentBlock.Kind.KIND_TOOL -> if (b.tool == "SendUserFile") SentFiles(b, session) else ToolRow(b, session)
        AgentBlock.Kind.KIND_QUESTION -> QuestionCard(b, session.agentName)
        AgentBlock.Kind.KIND_PLAN -> PlanCard(b)
        AgentBlock.Kind.KIND_SUBAGENT -> SubagentCard(b, session)
        AgentBlock.Kind.KIND_NOTE -> NoteRow(b, session)
        AgentBlock.Kind.KIND_DONE -> DoneRow(b)
        else -> {}
    }
}

/**
 * A reply that arrives while you watch shows from the top down, under a soft
 * edge sliding down it, as if being written (Claude Code writes a reply to
 * its transcript only once it's done). A mask over the reply at its full
 * height, so nothing around it moves. At once for a short reply, and for one
 * loaded rather than arriving.
 */
@Composable
private fun RevealsReply(id: String, length: Int, session: AgentSession, content: @Composable () -> Unit) {
    val arrived = session.arrived[id]
    val reveal = arrived != null && System.currentTimeMillis() - arrived < 1000 && length > 80
    val progress = remember(id) { Animatable(if (reveal) 0f else 1f) }
    LaunchedEffect(id) {
        if (progress.value < 1f) progress.animateTo(1f, tween(minOf(800, maxOf(400, length * 1000 / 1500)), easing = LinearEasing))
    }
    if (progress.value >= 1f) { content(); return }
    Box(
        Modifier.graphicsLayer { compositingStrategy = CompositingStrategy.Offscreen }.drawWithContent {
            drawContent()
            val h = size.height.coerceAtLeast(1f)
            val edge = minOf(90.dp.toPx(), h * 0.5f) / h
            val end = progress.value * (1 + edge)
            drawRect(
                Brush.verticalGradient(
                    0f to Color.Black, (end - edge).coerceIn(0f, 1f) to Color.Black,
                    end.coerceIn(0.0001f, 1f) to Color.Transparent, 1f to Color.Transparent,
                ),
                blendMode = BlendMode.DstIn,
            )
        },
    ) { content() }
}

/** Your message: on the right, in the accent, with its pictures above it. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun UserBubble(text: String, rich: String, refs: List<String>, local: List<OutgoingAttachment>, session: AgentSession, pending: Boolean = false) {
    val c = LocalAgentColors.current
    Row(Modifier.fillMaxWidth().graphicsLayer { alpha = if (pending) 0.7f else 1f }) {
        Spacer(Modifier.weight(1f).widthIn(min = 48.dp))
        Column(horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(6.dp)) {
            refs.forEach { AgentImage(it, session, maxHeight = 220.dp) }
            local.forEach { LocalImage(it, maxHeight = 220.dp) }
            if (text.isNotEmpty() || rich.isNotEmpty()) {
                // A message with attachments in it marks each one's place
                // (U+FFFC): shown as its number, as the composer numbers them.
                val shown = if (rich.isEmpty()) Markdown.inline(text, c.md.copy(link = Color.White)) else buildAnnotatedString {
                    val parts = rich.split('￼')
                    parts.forEachIndexed { k, part ->
                        append(Markdown.inline(part, c.md.copy(link = Color.White)))
                        if (k < parts.size - 1) withStyle(SpanStyle(fontWeight = FontWeight.SemiBold)) { append("[${k + 1}]") }
                    }
                }
                val bubble = Modifier.clip(RoundedCornerShape(20.dp)).background(c.bubble)
                if (pending) {
                    // Waiting to be taken: not selectable, so touching and
                    // holding it opens its menu (Send Now).
                    var menu by remember { mutableStateOf(false) }
                    Box {
                        Text(
                            shown, style = MaterialTheme.typography.bodyLarge, color = Color.White,
                            modifier = bubble.combinedClickable(onClick = {}, onLongClickLabel = "More", onLongClick = { menu = true })
                                .padding(horizontal = 15.dp, vertical = 10.dp),
                        )
                        DropdownMenu(menu, onDismissRequest = { menu = false }) {
                            DropdownMenuItem(
                                text = { Text("Send Now") }, leadingIcon = { Icon(Icons.AutoMirrored.Outlined.Send, null) },
                                enabled = session.canSendNow, onClick = { menu = false; session.sendNow() },
                            )
                        }
                    }
                } else {
                    SelectionContainer {
                        Text(
                            shown, style = MaterialTheme.typography.bodyLarge, color = Color.White,
                            modifier = bubble.padding(horizontal = 15.dp, vertical = 10.dp),
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun LocalImage(a: OutgoingAttachment, maxHeight: androidx.compose.ui.unit.Dp) {
    val bmp = remember(a) { ImageCodec.decode(a.data, 480) }
    if (bmp != null) {
        androidx.compose.foundation.Image(
            remember(bmp) { bmp.asImageBitmap() }, contentDescription = a.name,
            modifier = Modifier.size(width = 160.dp, height = maxHeight.coerceAtMost(160.dp)).clip(RoundedCornerShape(10.dp)),
            contentScale = androidx.compose.ui.layout.ContentScale.Crop,
        )
    }
}

/** A disclosure row: an icon, a label, and a chevron that turns as it opens. */
@Composable
private fun Disclosure(open: Boolean, enabled: Boolean = true, onToggle: () -> Unit, content: @Composable () -> Unit) {
    val c = LocalAgentColors.current
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(6.dp)).clickable(enabled = enabled, onClickLabel = if (open) "Close" else "Open", onClick = onToggle)
            .padding(vertical = 3.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        content()
        if (enabled) Icon(if (open) Icons.Outlined.ExpandMore else Icons.Outlined.ChevronRight, null, tint = c.tertiary, modifier = Modifier.size(16.dp))
    }
}

/** Thinking: its words as a message (the agent's narration), or how long it took. */
@Composable
private fun ThinkingRow(b: AgentBlock, seconds: Int?, session: AgentSession) {
    val c = LocalAgentColors.current
    if (b.text.isNotBlank()) { MarkdownText(b.text.trim()); return }
    val open = session.isOpen(b.id)
    Column {
        Disclosure(open, enabled = b.text.isNotEmpty(), onToggle = { session.setOpen(b.id, !open) }) {
            Icon(Icons.Outlined.Psychology, null, tint = c.secondary, modifier = Modifier.size(16.dp))
            Text(
                seconds?.let { if (it > 1) "Thought for ${it}s" else "Thought briefly" } ?: "Thinking",
                style = MaterialTheme.typography.bodyMedium, color = c.secondary,
            )
        }
        AnimatedVisibility(open, enter = expandVertically() + fadeIn(), exit = shrinkVertically() + fadeOut()) {
            Text(b.text, style = MaterialTheme.typography.bodyMedium, color = c.secondary, modifier = Modifier.padding(start = 24.dp, top = 6.dp))
        }
    }
}

/** A system note, centred; a compaction's summary opens under it. */
@Composable
private fun NoteRow(b: AgentBlock, session: AgentSession) {
    val c = LocalAgentColors.current
    val open = session.isOpen(b.id)
    Column(Modifier.fillMaxWidth()) {
        Row(
            Modifier.fillMaxWidth().clickable(enabled = b.output.isNotEmpty()) { session.setOpen(b.id, !open) }.padding(vertical = 2.dp),
            horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(b.text, style = MaterialTheme.typography.labelMedium, color = c.secondary, textAlign = TextAlign.Center)
            if (b.output.isNotEmpty()) Icon(if (open) Icons.Outlined.ExpandMore else Icons.Outlined.ChevronRight, null, tint = c.secondary, modifier = Modifier.size(14.dp))
        }
        AnimatedVisibility(open) { MarkdownText(b.output, Modifier.padding(top = 8.dp), color = c.secondary) }
    }
}

/** Files the agent sent you (Claude's SendUserFile): its caption and the pictures themselves. */
@Composable
private fun SentFiles(b: AgentBlock, session: AgentSession) {
    val c = LocalAgentColors.current
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            Icon(Icons.Outlined.AttachFile, null, tint = c.secondary, modifier = Modifier.size(16.dp))
            Text(b.title, style = MaterialTheme.typography.bodyMedium, color = c.secondary)
        }
        if (b.text.isNotEmpty()) MarkdownText(b.text)
        b.imagesList.forEach { AgentImage(it.ref, session, maxHeight = 360.dp) }
        val others = b.input.split('\n').filter { n -> n.isNotEmpty() && b.imagesList.none { it.ref.endsWith("/$n") } }
        if (others.isNotEmpty()) Text(others.joinToString(", "), style = codeTextStyle(0.95f), color = c.secondary)
    }
}

/** A turn's end, as Claude Code says it: "Worked for 30m 31s · done 9:47 AM". */
@Composable
private fun DoneRow(b: AgentBlock) {
    val c = LocalAgentColors.current
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        Icon(Icons.Outlined.AutoAwesome, null, tint = c.secondary, modifier = Modifier.size(14.dp))
        Text(TurnWords.words(b.durationMs, b.timeUnixMs), style = MaterialTheme.typography.bodyMedium, color = c.secondary)
    }
}

/** The live line: what the agent says it's doing, with a turning sparkle. */
@Composable
internal fun WorkingRow(status: String) {
    val t = rememberInfiniteTransition(label = "working")
    val turn by t.animateFloat(0f, 360f, infiniteRepeatable(tween(2400, easing = LinearEasing)), label = "turn")
    val breathe by t.animateFloat(0.55f, 1f, infiniteRepeatable(tween(900), RepeatMode.Reverse), label = "breathe")
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Icon(Icons.Outlined.AutoAwesome, null, tint = Color(0xFFFF9500), modifier = Modifier.size(18.dp).rotate(turn).graphicsLayer { alpha = breathe })
        Text(
            status.ifEmpty { "Working…" }, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis,
            color = MaterialTheme.colorScheme.onSurface.copy(alpha = 0.75f),
        )
    }
}

private fun toolIcon(b: AgentBlock): ImageVector = when (b.tool) {
    "Bash" -> Icons.Outlined.Terminal
    "Read" -> Icons.Outlined.Description
    "Write" -> Icons.Outlined.PostAdd
    "Edit", "MultiEdit", "NotebookEdit" -> Icons.Outlined.Edit
    "Grep", "Glob", "ToolSearch" -> Icons.Outlined.Search
    "WebFetch", "WebSearch" -> Icons.Outlined.Public
    "TodoWrite" -> Icons.Outlined.Checklist
    "Skill" -> Icons.Outlined.AutoAwesome
    else -> if (b.tool.startsWith("mcp__")) Icons.Outlined.Extension else Icons.Outlined.Build
}

/** How long a call has been running, ticking each second from its own start (so redrawing doesn't restart it). */
@Composable
private fun RunningTime(sinceUnixMs: Long) {
    if (sinceUnixMs <= 0) return
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(sinceUnixMs) { while (true) { now = System.currentTimeMillis(); delay(1000) } }
    Text(TurnWords.running((now - sinceUnixMs) / 1000), style = MaterialTheme.typography.labelSmall, color = LocalAgentColors.current.tertiary)
}

@Composable
private fun StatusDot(b: AgentBlock) {
    val c = LocalAgentColors.current
    when {
        b.pending -> Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            RunningTime(b.timeUnixMs)
            CircularProgressIndicator(Modifier.size(12.dp), strokeWidth = 1.5.dp)
        }
        b.error -> Icon(Icons.Outlined.Cancel, "Failed", tint = c.removed, modifier = Modifier.size(15.dp))
        else -> Icon(Icons.Outlined.CheckCircle, "Done", tint = c.success, modifier = Modifier.size(15.dp))
    }
}

/**
 * A tool call: its title, how it went, an edit's diff, and what it ran and
 * printed a tap away. A "!" command you ran shows as you typed it, with
 * what it printed open to start with.
 */
@Composable
internal fun ToolRow(b: AgentBlock, session: AgentSession, compact: Boolean = false) {
    val c = LocalAgentColors.current
    val bang = b.isBang()
    val toggled = session.isOpen(b.id)
    val open = if (bang) !toggled else toggled
    // A Read's path is in its title already, and a one-line "!" command in its row.
    val showsInput = b.input.isNotEmpty() && (b.tool == "Bash" || !b.tool.startsWith("Read")) && !(bang && '\n' !in b.input)
    val opens = b.diff.isNotEmpty() || showsInput || b.output.isNotEmpty()
    Column(Modifier.padding(vertical = if (compact) 1.dp else 3.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(6.dp)).clickable(enabled = opens) { session.setOpen(b.id, !toggled) },
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            if (bang) {
                Text("!", style = codeTextStyle(0.88f), fontWeight = FontWeight.Bold, color = c.shell, textAlign = TextAlign.Center, modifier = Modifier.width(18.dp))
                Text(
                    b.input.lineSequence().first(), style = codeTextStyle(0.88f), color = MaterialTheme.colorScheme.onSurface,
                    maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false),
                )
            } else {
                Icon(toolIcon(b), null, tint = c.tertiary, modifier = Modifier.size(18.dp))
                Text(
                    Markdown.inline(b.title.ifEmpty { b.tool }, c.md, links = false), style = MaterialTheme.typography.bodyMedium, color = c.secondary,
                    maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false),
                )
            }
            if (b.added > 0 || b.removed > 0) {
                Text("+${b.added}", style = MaterialTheme.typography.bodyMedium, color = c.added)
                Text("−${b.removed}", style = MaterialTheme.typography.bodyMedium, color = c.removed)
            }
            Spacer(Modifier.weight(1f).width(4.dp))
            StatusDot(b)
            Icon(if (open) Icons.Outlined.ExpandMore else Icons.Outlined.ChevronRight, null, tint = c.tertiary.copy(alpha = if (opens) 1f else 0f), modifier = Modifier.size(16.dp))
        }
        // Pictures it read or made show without opening it.
        if (b.imagesCount > 0) Column(Modifier.padding(start = 26.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            b.imagesList.forEach { AgentImage(it.ref, session, maxHeight = 300.dp) }
        }
        if (!open && b.diff.isNotEmpty()) {
            // An edit shows what changed, as the terminal does: its first lines, the rest a tap away.
            Box(Modifier.padding(start = 26.dp)) { DiffView(b.diff, preview = if (compact) 4 else 8, path = b.diffPath()) { session.setOpen(b.id, true) } }
        }
        AnimatedVisibility(open && opens, enter = expandVertically() + fadeIn(), exit = shrinkVertically() + fadeOut()) {
            Column(Modifier.padding(start = 26.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                if (b.diff.isNotEmpty()) DiffView(b.diff, path = b.diffPath())
                else if (showsInput) CodeBlock(b.input, if (b.tool == "Bash") "bash" else "", maxHeight = 200.dp, wrapsByDefault = true)
                if (b.output.isNotEmpty()) CodeBlock(b.output, "output", maxHeight = 260.dp, wrapsByDefault = true)
            }
        }
    }
}

/** A run of tool calls, as one line until opened. */
@Composable
private fun ToolGroup(id: String, tools: List<AgentBlock>, session: AgentSession) {
    val c = LocalAgentColors.current
    val open = session.isOpen(id)
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(6.dp)).clickable { session.setOpen(id, !open) }.padding(vertical = 3.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Icon(Icons.Outlined.Layers, null, tint = c.tertiary, modifier = Modifier.size(18.dp))
            Text(
                ToolSummary.words(tools.map { it.tool to it.input }), style = MaterialTheme.typography.bodyMedium, color = c.secondary,
                maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false),
            )
            ToolSummary.failed(tools.count { it.error && !it.pending })?.let {
                Text("· $it", style = MaterialTheme.typography.bodyMedium, color = c.warning, maxLines = 1)
            }
            val last = tools.last()
            if (last.pending) {
                Text(
                    "· " + last.title, style = MaterialTheme.typography.bodyMedium, color = c.tertiary, maxLines = 1,
                    overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false),
                )
            }
            Spacer(Modifier.weight(1f).width(4.dp))
            val running = tools.lastOrNull { it.pending }
            if (running != null) {
                RunningTime(running.timeUnixMs)
                CircularProgressIndicator(Modifier.size(12.dp), strokeWidth = 1.5.dp)
            }
            Icon(if (open) Icons.Outlined.ExpandMore else Icons.Outlined.ChevronRight, null, tint = c.tertiary, modifier = Modifier.size(16.dp))
        }
        if (open) {
            Column(Modifier.padding(start = 26.dp), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                tools.forEach { ToolRow(it, session, compact = true) }
            }
        } else if (tools.any { it.imagesCount > 0 }) {
            // Closed, its tools' pictures still show.
            Column(Modifier.padding(start = 26.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                tools.flatMap { it.imagesList }.forEach { AgentImage(it.ref, session, maxHeight = 300.dp) }
            }
        }
    }
}

@Composable
private fun Card(tint: Color, content: @Composable () -> Unit) {
    Column(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(tint.copy(alpha = 0.07f))
            .border(1.dp, tint.copy(alpha = 0.18f), RoundedCornerShape(14.dp)).padding(14.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) { content() }
}

@Composable
private fun CardLabel(icon: ImageVector, text: String, tint: Color = LocalAgentColors.current.secondary) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        Icon(icon, null, tint = tint, modifier = Modifier.size(14.dp))
        Text(text, style = MaterialTheme.typography.labelMedium, color = tint)
    }
}

/** A question the agent asked, and what you answered. */
@Composable
private fun QuestionCard(b: AgentBlock, agentName: String) {
    val c = LocalAgentColors.current
    Card(c.accent) {
        CardLabel(Icons.AutoMirrored.Outlined.HelpOutline, "$agentName asked")
        b.questionsList.forEach { q ->
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Text(q.question, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium)
                if (q.answer.isNotEmpty()) {
                    CardLabel(Icons.AutoMirrored.Outlined.Reply, q.answer, tint = c.accent)
                    if (q.notes.isNotEmpty()) Text(q.notes, style = MaterialTheme.typography.labelMedium, color = c.secondary)
                } else if (b.pending) {
                    Text("Waiting for your answer", style = MaterialTheme.typography.labelMedium, color = c.secondary)
                }
            }
        }
        if (b.output.isNotEmpty() && b.error) Text(b.output, style = MaterialTheme.typography.labelMedium, color = c.secondary)
    }
}

/** A plan presented for approval, and whether it was. */
@Composable
private fun PlanCard(b: AgentBlock) {
    val c = LocalAgentColors.current
    Card(c.plan) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.weight(1f)) { CardLabel(Icons.AutoMirrored.Outlined.Assignment, "Plan") }
            when {
                b.pending -> Text("Waiting for approval", style = MaterialTheme.typography.labelMedium, color = c.secondary)
                b.error -> CardLabel(Icons.AutoMirrored.Outlined.Undo, "Not approved")
                else -> CardLabel(Icons.Outlined.Check, "Approved", tint = c.added)
            }
        }
        MarkdownText(b.text)
    }
}

/** A subagent's run: what it was asked, and what it reported, a tap away. */
@Composable
private fun SubagentCard(b: AgentBlock, session: AgentSession) {
    val c = LocalAgentColors.current
    val open = session.isOpen(b.id)
    Card(c.subagent) {
        Row(
            Modifier.fillMaxWidth().clickable { session.setOpen(b.id, !open) },
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Icon(Icons.Outlined.Groups, null, tint = c.subagent, modifier = Modifier.size(20.dp))
            Column(Modifier.weight(1f)) {
                Text(b.title.ifEmpty { "Subagent" }, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium)
                if (b.text.isNotEmpty()) Text(b.text, style = MaterialTheme.typography.labelMedium, color = c.secondary)
            }
            StatusDot(b)
            Icon(if (open) Icons.Outlined.ExpandMore else Icons.Outlined.ChevronRight, null, tint = c.tertiary, modifier = Modifier.size(16.dp))
        }
        AnimatedVisibility(open) {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                if (b.input.isNotEmpty()) {
                    Text("Asked to", style = MaterialTheme.typography.labelMedium, color = c.secondary)
                    MarkdownText(b.input)
                }
                if (b.output.isNotEmpty()) {
                    HorizontalDivider()
                    Text("Reported", style = MaterialTheme.typography.labelMedium, color = c.secondary)
                    MarkdownText(b.output)
                }
            }
        }
    }
}
