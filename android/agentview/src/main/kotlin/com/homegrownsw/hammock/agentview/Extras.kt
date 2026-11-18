package com.homegrownsw.hammock.agentview

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Article
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.CallMerge
import androidx.compose.material.icons.outlined.BarChart
import androidx.compose.material.icons.outlined.CalendarMonth
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.Checklist
import androidx.compose.material.icons.outlined.ChevronRight
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.ExpandLess
import androidx.compose.material.icons.outlined.Map
import androidx.compose.material.icons.outlined.RadioButtonUnchecked
import androidx.compose.material.icons.outlined.Restaurant
import androidx.compose.material.icons.outlined.Slideshow
import androidx.compose.material.icons.outlined.SportsEsports
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import hammock.v1.Hammock.AgentLink
import hammock.v1.Hammock.AgentShell
import hammock.v1.Hammock.AgentSubagent
import hammock.v1.Hammock.AgentTask
import kotlinx.coroutines.launch

/** What floats above the composer: the session's pull requests and artifacts, and its task list while any is left to do. */
@Composable
internal fun SessionExtras(state: AgentViewState) {
    val open = state.tasks.any { it.status != AgentTask.Status.STATUS_COMPLETED }
    if (state.links.isEmpty() && !open) return
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        if (state.links.isNotEmpty()) LinkChips(state.links)
        if (open) TaskListCard(state.tasks)
    }
}

private fun artifactIcon(word: String): ImageVector = when (word.lowercase()) {
    "chart", "graph", "dashboard" -> Icons.Outlined.BarChart
    "calendar" -> Icons.Outlined.CalendarMonth
    "map" -> Icons.Outlined.Map
    "code" -> Icons.Outlined.Code
    "recipe", "food" -> Icons.Outlined.Restaurant
    "slides", "deck" -> Icons.Outlined.Slideshow
    "doc", "document", "notes" -> Icons.Outlined.Description
    "list", "checklist", "tracker" -> Icons.Outlined.Checklist
    "game" -> Icons.Outlined.SportsEsports
    else -> Icons.AutoMirrored.Outlined.Article
}

/** As GitHub colours them. */
private fun prColor(state: String, fallback: Color) = when (state.uppercase()) {
    "OPEN" -> Color(0xFF34C759)
    "MERGED" -> Color(0xFFAF52DE)
    "CLOSED" -> Color(0xFFFF3B30)
    else -> fallback
}

/** The session's pull requests and artifacts, newest first: tap to open, hold to copy the link. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun LinkChips(links: List<AgentLink>) {
    val c = LocalAgentColors.current
    val uri = LocalUriHandler.current
    val clipboard = LocalClipboardManager.current
    Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        for (link in links.asReversed()) {
            var menu by remember { mutableStateOf(false) }
            val label = if (link.kind == AgentLink.Kind.KIND_PULL_REQUEST) {
                listOf("Pull request #${link.number}" + if (link.repo.isEmpty()) "" else " in ${link.repo}", link.title, CardText.prState(link.state))
                    .filter { it.isNotEmpty() }.joinToString(" · ")
            } else "Artifact: ${link.title}"
            Box {
                Surface(shape = CircleShape, tonalElevation = 3.dp, shadowElevation = 2.dp) {
                    Row(
                        Modifier.combinedClickable(onClick = { runCatching { uri.openUri(link.url) } }, onLongClick = { menu = true })
                            .semantics { contentDescription = label }
                            .widthIn(max = 260.dp).padding(horizontal = 12.dp, vertical = 7.dp),
                        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp),
                    ) {
                        if (link.kind == AgentLink.Kind.KIND_PULL_REQUEST) {
                            Icon(Icons.AutoMirrored.Outlined.CallMerge, null, tint = prColor(link.state, c.secondary), modifier = Modifier.size(16.dp))
                            Text("#${link.number}", style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.SemiBold)
                            if (link.title.isNotEmpty()) Text(link.title, style = MaterialTheme.typography.labelLarge, color = c.secondary, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        } else {
                            Icon(artifactIcon(link.icon), null, tint = c.accent, modifier = Modifier.size(16.dp))
                            Text(link.title.ifEmpty { "Artifact" }, style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
                DropdownMenu(menu, onDismissRequest = { menu = false }) {
                    DropdownMenuItem(text = { Text("Open") }, onClick = { menu = false; runCatching { uri.openUri(link.url) } })
                    DropdownMenuItem(text = { Text("Copy Link") }, onClick = { menu = false; clipboard.setText(AnnotatedString(link.url)) })
                }
            }
        }
    }
}

/** A ring filled to [fraction]. */
@Composable
internal fun ProgressRing(fraction: Float, tint: Color, track: Color, modifier: Modifier, width: Float = 3f) {
    val shown by animateFloatAsState(fraction, tween(300), label = "ring")
    Canvas(modifier) {
        val w = width * density
        val inset = w / 2
        val sz = Size(size.width - w, size.height - w)
        drawArc(track, 0f, 360f, false, Offset(inset, inset), sz, style = Stroke(w))
        drawArc(tint, -90f, 360f * shown, false, Offset(inset, inset), sz, style = Stroke(w, cap = StrokeCap.Round))
    }
}

/** The agent's task list: how far through it is and what it's doing now, opened to the whole list. */
@Composable
private fun TaskListCard(tasks: List<AgentTask>) {
    val c = LocalAgentColors.current
    var expanded by remember { mutableStateOf(false) }
    val done = tasks.count { it.status == AgentTask.Status.STATUS_COMPLETED }
    val current = tasks.firstOrNull { it.status == AgentTask.Status.STATUS_IN_PROGRESS } ?: tasks.firstOrNull { it.status == AgentTask.Status.STATUS_PENDING }
    val turn by animateFloatAsState(if (expanded) 180f else 0f, label = "chevron")
    Surface(shape = RoundedCornerShape(18.dp), tonalElevation = 3.dp, shadowElevation = 2.dp, modifier = Modifier.fillMaxWidth()) {
        Column {
            Row(
                Modifier.fillMaxWidth().clickable(onClickLabel = if (expanded) "Hide the list" else "Show the list") { expanded = !expanded }
                    .semantics { contentDescription = "Tasks, $done of ${tasks.size} done" }
                    .padding(horizontal = 14.dp, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                ProgressRing(if (tasks.isEmpty()) 0f else done.toFloat() / tasks.size, c.accent, c.fill, Modifier.size(18.dp))
                Text("$done of ${tasks.size}", style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.SemiBold)
                if (current != null && !expanded) {
                    Text(CardText.saying(current), style = MaterialTheme.typography.labelLarge, color = c.secondary, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                } else Spacer(Modifier.weight(1f))
                Icon(Icons.Outlined.ExpandLess, null, tint = c.secondary, modifier = Modifier.size(18.dp).rotate(turn))
            }
            AnimatedVisibility(expanded) {
                Column(Modifier.padding(start = 14.dp, end = 14.dp, bottom = 12.dp), verticalArrangement = Arrangement.spacedBy(7.dp)) {
                    for (t in tasks) {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(9.dp)) {
                            when (t.status) {
                                AgentTask.Status.STATUS_COMPLETED -> Icon(Icons.Outlined.CheckCircle, "Done", tint = c.secondary, modifier = Modifier.size(16.dp))
                                AgentTask.Status.STATUS_IN_PROGRESS -> CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp, color = c.accent)
                                else -> Icon(Icons.Outlined.RadioButtonUnchecked, "To do", tint = c.tertiary, modifier = Modifier.size(16.dp))
                            }
                            val doneTask = t.status == AgentTask.Status.STATUS_COMPLETED
                            Text(
                                if (t.status == AgentTask.Status.STATUS_IN_PROGRESS) CardText.saying(t) else t.subject,
                                style = MaterialTheme.typography.labelLarge,
                                fontWeight = if (t.status == AgentTask.Status.STATUS_IN_PROGRESS) FontWeight.SemiBold else FontWeight.Normal,
                                textDecoration = if (doneTask) TextDecoration.LineThrough else null,
                                color = if (doneTask) c.secondary else MaterialTheme.colorScheme.onSurface,
                            )
                        }
                    }
                }
            }
        }
    }
}

/** What the conversation can show instead of the agent's own: a subagent's transcript, or a background shell's output. */
sealed class SideView {
    data class Subagent(val id: String) : SideView()
    data class Shell(val id: String) : SideView()
}

/** What the agent has running beside its conversation: its subagents and background shells. Tap one to watch it. */
@Composable
internal fun BackgroundStrip(subagents: List<AgentSubagent>, shells: List<AgentShell>, open: (SideView) -> Unit) {
    val c = LocalAgentColors.current
    val label = listOfNotNull(
        subagents.takeIf { it.isNotEmpty() }?.let { if (it.size == 1) "Subagent" else "${it.size} subagents" },
        shells.takeIf { it.isNotEmpty() }?.let { if (it.size == 1) "Shell" else "${it.size} shells" },
    ).joinToString(" · ")
    Surface(tonalElevation = 2.dp, modifier = Modifier.fillMaxWidth()) {
        Row(
            Modifier.horizontalScroll(rememberScrollState()).padding(horizontal = 18.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text(label, style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.SemiBold, color = c.secondary)
            for (a in subagents) StripChip(a.description.ifEmpty { a.agentType }, { CircularProgressIndicator(Modifier.size(10.dp), strokeWidth = 1.5.dp) }) { open(SideView.Subagent(a.id)) }
            for (s in shells) StripChip(CardText.title(s), { Icon(Icons.Outlined.Terminal, null, tint = c.secondary, modifier = Modifier.size(14.dp)) }) { open(SideView.Shell(s.id)) }
        }
    }
}

@Composable
private fun StripChip(title: String, icon: @Composable () -> Unit, onClick: () -> Unit) {
    val c = LocalAgentColors.current
    Row(
        Modifier.clip(CircleShape).background(c.fill).clickable(onClick = onClick).padding(horizontal = 10.dp, vertical = 5.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        icon()
        Text(title, style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 220.dp))
        Icon(Icons.Outlined.ChevronRight, null, tint = c.tertiary, modifier = Modifier.size(14.dp))
    }
}

/** Above a subagent's transcript or a shell's output: what it is, whether it's still going, and the way back. */
@Composable
internal fun SideHeader(title: String, detail: String?, status: String, working: Boolean, failed: Boolean, back: () -> Unit) {
    val c = LocalAgentColors.current
    Surface(tonalElevation = 2.dp, modifier = Modifier.fillMaxWidth()) {
        Row(Modifier.padding(end = 18.dp, top = 4.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            IconButton(onClick = back) { Icon(Icons.AutoMirrored.Outlined.ArrowBack, "Back") }
            Box(Modifier.size(14.dp), contentAlignment = Alignment.Center) {
                when {
                    working -> CircularProgressIndicator(Modifier.size(12.dp), strokeWidth = 1.5.dp)
                    failed -> Box(Modifier.size(10.dp).clip(CircleShape).background(c.removed))
                    else -> Icon(Icons.Outlined.Check, null, tint = c.success, modifier = Modifier.size(14.dp))
                }
            }
            Column(Modifier.weight(1f)) {
                Text(title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (detail != null) Text(detail, style = codeTextStyle(0.75f), color = c.secondary, maxLines = 2, overflow = TextOverflow.Ellipsis)
                Text(status, style = MaterialTheme.typography.labelMedium, color = c.secondary, maxLines = 1)
            }
        }
    }
}

/** A background shell's output, following its end as it's written (unless you've scrolled up). */
@Composable
internal fun ShellOutputView(state: AgentViewState, modifier: Modifier) {
    val c = LocalAgentColors.current
    val lines = remember(state.shellOutput) { state.shellOutput.removeSuffix("\r").removeSuffix("\n").split('\n') }
    val list = rememberLazyListState()
    val atEnd by remember { derivedStateOf { !list.canScrollForward } }
    var wasAtEnd by remember { mutableStateOf(true) }
    LaunchedEffect(lines.size, state.shellOutput.length) {
        if (wasAtEnd && lines.isNotEmpty()) list.scrollToItem(lines.size - 1, Int.MAX_VALUE / 2)
    }
    LaunchedEffect(atEnd) { wasAtEnd = atEnd }
    when {
        state.error.isNotEmpty() -> Empty("No output", state.error, modifier)
        state.shellOutput.isEmpty() -> Empty("No output yet", null, modifier)
        else -> SelectionContainer(modifier) {
            LazyColumn(Modifier.fillMaxSize(), state = list, contentPadding = PaddingValues(horizontal = 18.dp, vertical = 12.dp)) {
                items(lines.size, key = { it }) { i -> Text(lines[i].ifEmpty { " " }, style = codeTextStyle(), color = MaterialTheme.colorScheme.onSurface) }
            }
        }
    }
}

@Composable
internal fun Empty(title: String, detail: String?, modifier: Modifier = Modifier) {
    val c = LocalAgentColors.current
    Column(modifier.fillMaxWidth().padding(top = 60.dp, start = 24.dp, end = 24.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Text(title, style = MaterialTheme.typography.titleMedium)
        if (detail != null) Text(detail, style = MaterialTheme.typography.bodyMedium, color = c.secondary)
    }
}

/** The agent's model and effort (a menu to switch them), and how full its context is. */
@Composable
internal fun AgentStatusBar(session: AgentSession, state: AgentViewState, canDrive: Boolean) {
    val c = LocalAgentColors.current
    val scope = rememberCoroutineScope()
    var menu by remember { mutableStateOf(false) }
    val style = MaterialTheme.typography.labelMedium
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        if (state.model.isNotEmpty()) Box {
            Text(
                ModelNames.displayName(state.model) + if (state.effort.isEmpty()) "" else "  " + ModelNames.effortName(state.effort),
                style = style, color = c.secondary, maxLines = 1,
                modifier = Modifier.clip(RoundedCornerShape(6.dp)).clickable(enabled = canDrive, onClickLabel = "Switch model") {
                    // Codex picks a model in a picker of its own: it shows here as a prompt.
                    if (session.harness == "claude") menu = true else scope.launch { session.send("/model", pending = false) }
                }.padding(horizontal = 6.dp, vertical = 4.dp),
            )
            DropdownMenu(menu, onDismissRequest = { menu = false }) {
                Text("Model", style = MaterialTheme.typography.labelSmall, color = c.secondary, modifier = Modifier.padding(horizontal = 16.dp, vertical = 6.dp))
                for ((label, alias) in ModelNames.choices) {
                    DropdownMenuItem(text = { Text(label) }, onClick = { menu = false; session.switchModel(alias) })
                }
                // Once a reply has said (models without effort levels don't).
                if (state.effort.isNotEmpty()) {
                    HorizontalDivider()
                    Text("Effort", style = MaterialTheme.typography.labelSmall, color = c.secondary, modifier = Modifier.padding(horizontal = 16.dp, vertical = 6.dp))
                    for ((label, level) in ModelNames.efforts) {
                        DropdownMenuItem(
                            text = { Text(label) }, onClick = { menu = false; session.switchEffort(level) },
                            trailingIcon = if (level == state.effort) ({ Icon(Icons.Outlined.Check, "Current") }) else null,
                        )
                    }
                }
            }
        }
        if (state.contextWindow > 0) {
            val f = (state.contextTokens.toFloat() / state.contextWindow).coerceIn(0f, 1f)
            val tint = when { f > 0.9f -> c.removed; f > 0.75f -> c.warning; else -> c.secondary }
            VerticalDivider(Modifier.height(12.dp))
            Row(
                verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp),
                modifier = Modifier.semantics { contentDescription = "${ModelNames.tokens(state.contextTokens)} of ${ModelNames.tokens(state.contextWindow)} tokens" },
            ) {
                ProgressRing(f, tint, c.secondary.copy(alpha = 0.25f), Modifier.size(12.dp), width = 2.5f)
                Text("${(f * 100).toInt()}% context", style = style, color = if (f > 0.75f) tint else c.secondary, maxLines = 1)
            }
        }
        Spacer(Modifier.width(0.dp))
    }
}
