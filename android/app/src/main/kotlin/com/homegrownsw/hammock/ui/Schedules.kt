package com.homegrownsw.hammock.ui

import android.text.format.DateFormat
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Block
import androidx.compose.material.icons.filled.Bedtime
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Error
import androidx.compose.material.icons.filled.HourglassBottom
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.RadioButtonUnchecked
import androidx.compose.material.icons.filled.SkipNext
import androidx.compose.material.icons.filled.Sync
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Workspace
import hammock.v1.Hammock
import hammock.v1.Hammock.Schedule
import hammock.v1.Hammock.ScheduleRun
import hammock.v1.Hammock.ScheduleRun.Status
import kotlinx.coroutines.launch
import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale

// Scheduled tasks on the phone (the iPhone's ScheduledTasks.swift): what
// ran and is ready, what's coming up, Run Now, Skip and Pause. Making and
// editing them is the computer's (or hmk schedule's).

val Status.ended get() = this !in setOf(Status.STATUS_UNSPECIFIED, Status.STATUS_RUNNING, Status.STATUS_WAITING)
val Status.label get() = when (this) {
    Status.STATUS_RUNNING -> "Running"; Status.STATUS_WAITING -> "Waiting for you"; Status.STATUS_READY -> "Ready"
    Status.STATUS_FAILED -> "Failed"; Status.STATUS_GAVE_UP -> "Gave up"; Status.STATUS_SKIPPED -> "Skipped"
    Status.STATUS_MISSED -> "Missed"; Status.STATUS_STOPPED -> "Stopped"; else -> "Unknown"
}
val Status.icon: ImageVector get() = when (this) {
    Status.STATUS_RUNNING -> Icons.Default.Sync; Status.STATUS_WAITING -> Icons.Default.Error; Status.STATUS_READY -> Icons.Default.CheckCircle
    Status.STATUS_FAILED -> Icons.Default.Error; Status.STATUS_GAVE_UP -> Icons.Default.HourglassBottom; Status.STATUS_SKIPPED -> Icons.Default.SkipNext
    Status.STATUS_MISSED -> Icons.Default.Bedtime; Status.STATUS_STOPPED -> Icons.Default.Block; else -> Icons.Default.RadioButtonUnchecked
}
@Composable fun Status.color(): Color = when (this) {
    Status.STATUS_RUNNING -> Color(0xFF3B82F6); Status.STATUS_WAITING -> NeedsYouColor; Status.STATUS_READY -> Color(0xFF34A853)
    Status.STATUS_FAILED, Status.STATUS_GAVE_UP -> ErrorColor; else -> MaterialTheme.colorScheme.onSurfaceVariant
}

val Schedule.skipping get() = nextRunUnixMs > 0 && skipUntilUnixMs > nextRunUnixMs
val Schedule.upcoming get() = nextRunUnixMs > 0 && !paused
val Schedule.canRunEarly get() = upcoming && !skipping
fun Schedule.runsEarlyFirst(now: Long) = canRunEarly && nextRunUnixMs - now <= 12 * 3600 * 1000L

object ScheduleWords {
    fun kindName(kind: String, kinds: List<Hammock.AgentKind>) = when (kind) {
        "command" -> "Command"; "" -> "Claude Code"; else -> AgentCatalog.name(kind, kinds)
    }

    fun time(ms: Long, ctx: android.content.Context): String =
        SimpleDateFormat(if (DateFormat.is24HourFormat(ctx)) "HH:mm" else "h:mm a", Locale.getDefault()).format(Date(ms))

    /** "Today at 08:30", "Tomorrow at 08:30", "Thu 6 Nov at 08:30". */
    fun date(ms: Long, now: Long, ctx: android.content.Context): String {
        val t = time(ms, ctx)
        val c = Calendar.getInstance().apply { timeInMillis = ms }
        val n = Calendar.getInstance().apply { timeInMillis = now }
        fun sameDay(dayOffset: Int): Boolean {
            val d = (n.clone() as Calendar).apply { add(Calendar.DAY_OF_YEAR, dayOffset) }
            return d.get(Calendar.YEAR) == c.get(Calendar.YEAR) && d.get(Calendar.DAY_OF_YEAR) == c.get(Calendar.DAY_OF_YEAR)
        }
        return when {
            sameDay(0) -> "Today at $t"
            sameDay(1) -> "Tomorrow at $t"
            sameDay(-1) -> "Yesterday at $t"
            c.get(Calendar.YEAR) == n.get(Calendar.YEAR) -> SimpleDateFormat("EEE d MMM", Locale.getDefault()).format(Date(ms)) + " at $t"
            else -> SimpleDateFormat("d MMM yyyy", Locale.getDefault()).format(Date(ms)) + " at $t"
        }
    }

    fun days(days: List<Int>): String {
        val set = days.toSet()
        if (set.isEmpty() || set.size == 7) return "Every day"
        if (set == setOf(1, 2, 3, 4, 5)) return "Weekdays"
        if (set == setOf(0, 6)) return "Weekends"
        val names = java.text.DateFormatSymbols.getInstance().shortWeekdays // 1 = Sunday
        return listOf(1, 2, 3, 4, 5, 6, 0).filter { it in set }.joinToString(", ") { names[it + 1] }
    }

    fun whenText(s: Schedule, ctx: android.content.Context): String {
        if (s.cron.isNotEmpty()) return "Cron ${s.cron}"
        val cal = Calendar.getInstance().apply { set(Calendar.HOUR_OF_DAY, s.minuteOfDay / 60); set(Calendar.MINUTE, s.minuteOfDay % 60) }
        return days(s.weekdaysList) + " at " + time(cal.timeInMillis, ctx)
    }

    fun group(w: Workspace, s: Schedule): String {
        w.groups.firstOrNull { it.id == s.groupId }?.let { g -> return (if (g.icon.isEmpty()) "" else g.icon + " ") + w.title(g) }
        return (if (s.groupIcon.isEmpty()) "" else s.groupIcon + " ") + s.groupName
    }

    fun where(w: Workspace, s: Schedule, ctx: android.content.Context) =
        listOf(group(w, s), kindName(s.kind, w.kinds), whenText(s, ctx)).filter { it.isNotEmpty() }.joinToString(" · ")

    fun next(s: Schedule, now: Long, ctx: android.content.Context): String = when {
        s.error.isNotEmpty() -> s.error
        s.paused -> "Paused"
        s.nextRunUnixMs <= 0 -> "Not due again"
        s.skipping -> "Skipping " + date(s.nextRunUnixMs, now, ctx)
        else -> "Next: " + date(s.nextRunUnixMs, now, ctx)
    }

    fun note(r: ScheduleRun, s: Schedule) = when {
        r.note.isNotEmpty() -> r.note
        s.kind == "command" && r.status == Status.STATUS_FAILED -> "Exit code ${r.exitCode}"
        else -> r.status.label
    }

    fun output(r: ScheduleRun): String? {
        if (r.output.isEmpty()) return null
        val name = runCatching { java.net.URI(r.output) }.getOrNull()?.takeIf { it.scheme != null && it.scheme != "file" }?.host
            ?: r.output.substringAfterLast('/')
        return "Showed $name"
    }

    fun duration(r: ScheduleRun, now: Long): String? {
        if (r.startedUnixMs <= 0) return null
        val end = if (r.endedUnixMs > 0) r.endedUnixMs else now
        val secs = maxOf(0L, (end - r.startedUnixMs) / 1000)
        if (secs < 60) return "${secs}s"
        val m = secs / 60
        return if (m < 60) "${m}m" else if (m % 60 == 0L) "${m / 60}h" else "${m / 60}h ${m % 60}m"
    }

    fun nextShort(s: Schedule, now: Long, ctx: android.content.Context) = date(s.nextRunUnixMs, now, ctx).removePrefix("Today at ")
}

fun hasTab(w: Workspace, r: ScheduleRun) = !r.tabClosed && r.tabId.isNotEmpty() && w.tabs[r.tabId] != null

/** Runs that finished in the last 12 hours and haven't been looked at. */
fun readyRuns(w: Workspace, now: Long): List<Pair<Schedule, ScheduleRun>> {
    val since = now - 12 * 3600 * 1000L
    val out = ArrayList<Pair<Schedule, ScheduleRun>>()
    for (s in w.schedules.values) for (r in s.runsList) {
        if (r.status != Status.STATUS_READY || r.endedUnixMs < since || !hasTab(w, r)) continue
        val p = w.panes[r.paneId]
        if (p != null && p.hasAgent() && (p.agent.seen || p.agent.state != Hammock.AgentState.AGENT_STATE_IDLE)) continue
        out += s to r
    }
    return out.sortedByDescending { it.second.endedUnixMs }
}

/** Goes to a run: its pane or tab, or (closed) its agent's session resumed in a new tab. */
fun openRun(nav: Nav, w: Workspace, s: Schedule, r: ScheduleRun, model: AppModel) {
    if (hasTab(w, r)) {
        nav.push(if (w.panes[r.paneId] != null) "pane:" + r.paneId else "tab:" + r.tabId)
        return
    }
    if (s.kind == "command" || r.sessionId.isEmpty()) return
    model.scope.launch {
        val info = Hammock.SessionInfo.newBuilder().setHarness(s.kind.ifEmpty { "claude" }).setId(r.sessionId).build()
        val res = model.resumeSession(info, groupID = if (w.groups.any { it.id == s.groupId }) s.groupId else "",
            computerID = if (s.computerId != w.myUserID) s.computerId else "") ?: return@launch
        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.Main) {
            nav.push(if (res.paneId.isNotEmpty()) "pane:" + res.paneId else "tab:" + res.tabId)
        }
    }
}

/** Runs it now (early: in place of its next run), and opens its tab once it's here. */
suspend fun runScheduleNow(model: AppModel, s: Schedule, early: Boolean, nav: Nav) {
    val r = try {
        model.call { setRunSchedule(Hammock.RunSchedule.newBuilder().setScheduleId(s.id).setEarly(early).build()) }
    } catch (e: Exception) {
        model.showToast("Couldn’t run “${s.name}”: ${e.message}")
        return
    }
    val tab = r.created.id
    repeat(50) {
        if (model.workspace.value.tabs[tab] != null) {
            kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.Main) { nav.push("tab:$tab") }
            return
        }
        kotlinx.coroutines.delay(40)
    }
}

@Composable
fun ReadyRunRow(w: Workspace, s: Schedule, r: ScheduleRun, now: Long, onClick: () -> Unit) {
    val ctx = LocalContext.current
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 20.dp, vertical = 12.dp), horizontalArrangement = Arrangement.spacedBy(14.dp)) {
        Icon(r.status.icon, r.status.label, tint = r.status.color(), modifier = Modifier.size(20.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Row {
                Text(s.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, modifier = Modifier.weight(1f))
                Text(ScheduleWords.time(r.endedUnixMs, ctx), color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Text(ScheduleWords.group(w, s), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
            if (r.note.isNotEmpty()) Text(plainInline(r.note), style = MaterialTheme.typography.bodyMedium, maxLines = 3)
            ScheduleWords.output(r)?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
    }
}

/** "Coming Up": the next few, with Skip and Run (Run Early when it's soon); See All opens the list. */
fun LazyListScope.comingUp(model: AppModel, w: Workspace, nav: Nav, now: Long) {
    item(key = "h:Coming Up") {
        Row(Modifier.fillMaxWidth().padding(start = 20.dp, end = 8.dp, top = 12.dp), verticalAlignment = Alignment.CenterVertically) {
            Text("Coming Up", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary, modifier = Modifier.weight(1f))
            TextButton(onClick = { nav.open("schedules") }) { Text("See All") }
        }
    }
    val next = sorted(w.schedules.values).filter { it.upcoming }.take(3)
    if (next.isEmpty()) {
        item(key = "nothing") {
            val paused = w.schedules.values.count { it.paused }
            Text(if (paused > 0) "Nothing coming up: ${if (paused == 1) "1 task is" else "$paused tasks are"} paused." else "Nothing coming up.",
                Modifier.padding(horizontal = 20.dp, vertical = 8.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
    items(next, key = { "next:" + it.id }) { s -> ComingUpRow(model, w, s, now, nav) }
}

fun sorted(all: Collection<Schedule>): List<Schedule> = all.sortedWith { a, b ->
    val x = a.nextRunUnixMs
    val y = b.nextRunUnixMs
    when {
        (x > 0) != (y > 0) -> if (x > 0) -1 else 1
        x != y && x > 0 -> x.compareTo(y)
        else -> a.name.compareTo(b.name, ignoreCase = true).takeIf { it != 0 } ?: a.id.compareTo(b.id)
    }
}

@Composable
private fun ComingUpRow(model: AppModel, w: Workspace, s: Schedule, now: Long, nav: Nav) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val early = s.runsEarlyFirst(now)
    Row(Modifier.fillMaxWidth().clickable { nav.open("schedule:" + s.id) }.padding(horizontal = 20.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Column(Modifier.weight(1f)) {
            Text(s.name, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium, maxLines = 1)
            val running = s.runsList.firstOrNull()?.let { if (it.status == Status.STATUS_RUNNING || it.status == Status.STATUS_WAITING) it.status.label + " · " else "" } ?: ""
            val group = ScheduleWords.group(w, s)
            Text(
                running + ScheduleWords.date(s.nextRunUnixMs, now, ctx) + (if (group.isEmpty()) "" else " · $group"),
                style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1,
                textDecoration = if (s.skipping) TextDecoration.LineThrough else null, overflow = TextOverflow.Ellipsis,
            )
        }
        OutlinedButton(onClick = { model.skipScheduleRun(s.id, !s.skipping) }, contentPadding = PaddingValues(horizontal = 12.dp)) {
            Text(if (s.skipping) "Don’t Skip" else "Skip")
        }
        OutlinedButton(onClick = { scope.launch { runScheduleNow(model, s, early, nav) } }, contentPadding = PaddingValues(horizontal = 12.dp)) {
            Text(if (early) "Run Early" else "Run")
        }
    }
}

@Composable
private fun DetailTop(title: String, nav: Nav, showBack: Boolean, content: @Composable (PaddingValues) -> Unit) =
    DetailScaffold(title, if (showBack) nav::pop else null, content = content)

/** Every scheduled task, soonest first, then paused and finished ones. */
@Composable
fun SchedulesScreen(model: AppModel, nav: Nav, showBack: Boolean) {
    val w by model.workspace.collectAsStateWithLifecycle()
    val ctx = LocalContext.current
    val now = rememberNow(60_000)
    DetailTop("Scheduled Tasks", nav, showBack) { padding ->
        if (w.schedules.isEmpty()) {
            EmptyState("No Scheduled Tasks", "Run Claude Code, Codex or a command at set times: each run opens a tab in its Group. Create scheduled tasks on your computer (or with hmk schedule).", Modifier.padding(padding))
            return@DetailTop
        }
        LazyColumn(Modifier.fillMaxSize(), contentPadding = padding) {
            items(sorted(w.schedules.values), key = { it.id }) { s ->
                ListItem(
                    modifier = Modifier.clickable { nav.push("schedule:" + s.id) },
                    headlineContent = { Text(s.name, fontWeight = FontWeight.Medium, maxLines = 1) },
                    supportingContent = {
                        Column {
                            Text(ScheduleWords.where(w, s, ctx), maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(ScheduleWords.next(s, now, ctx), style = MaterialTheme.typography.bodySmall,
                                color = if (s.error.isEmpty()) MaterialTheme.colorScheme.onSurfaceVariant else NeedsYouColor)
                        }
                    },
                    trailingContent = { s.runsList.firstOrNull()?.let { Icon(it.status.icon, "Last run: " + it.status.label, tint = it.status.color()) } },
                )
            }
            item { MacOnlyFooter() }
        }
    }
}

@Composable
private fun MacOnlyFooter() = Text(
    "Create and edit scheduled tasks on your computer (or with hmk schedule).",
    Modifier.padding(20.dp), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
)

/** One scheduled task: its actions, when and what it runs, and its runs. */
@Composable
fun ScheduleScreen(model: AppModel, id: String, nav: Nav, showBack: Boolean) {
    val w by model.workspace.collectAsStateWithLifecycle()
    val ctx = LocalContext.current
    val now = rememberNow(60_000)
    val scope = rememberCoroutineScope()
    val s = w.schedules[id]
    DetailTop(s?.name ?: "Scheduled Task", nav, showBack) { padding ->
        if (s == null) {
            EmptyState("Scheduled Task Deleted", "It was deleted on your computer.", Modifier.padding(padding))
            return@DetailTop
        }
        LazyColumn(Modifier.fillMaxSize(), contentPadding = padding) {
            item {
                Column(Modifier.padding(20.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text(s.name, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
                        if (s.paused) Text("Paused", Modifier.background(MaterialTheme.colorScheme.surfaceContainerHighest, RoundedCornerShape(50))
                            .padding(horizontal = 8.dp, vertical = 2.dp), style = MaterialTheme.typography.labelMedium)
                    }
                    Text(ScheduleWords.where(w, s, ctx), color = MaterialTheme.colorScheme.onSurfaceVariant)
                    if (s.error.isNotEmpty()) Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        Icon(Icons.Default.Warning, null, tint = NeedsYouColor, modifier = Modifier.size(18.dp))
                        Text(s.error, color = NeedsYouColor)
                    }
                }
            }
            item {
                ListItem(modifier = Modifier.clickable { scope.launch { runScheduleNow(model, s, false, nav) } },
                    headlineContent = { Text("Run Now") }, leadingContent = { Icon(Icons.Default.PlayArrow, null) })
                if (s.canRunEarly) ListItem(modifier = Modifier.clickable { scope.launch { runScheduleNow(model, s, true, nav) } },
                    headlineContent = { Text("Run Now Instead of ${ScheduleWords.nextShort(s, now, ctx)}") }, leadingContent = { Icon(Icons.Default.PlayArrow, null) })
                if (s.upcoming) ListItem(modifier = Modifier.clickable { model.skipScheduleRun(s.id, !s.skipping) },
                    headlineContent = { Text(if (s.skipping) "Don’t Skip Next Run" else "Skip Next Run") }, leadingContent = { Icon(Icons.Default.SkipNext, null) })
                ListItem(modifier = Modifier.clickable { model.setSchedulePaused(s.id, !s.paused) },
                    headlineContent = { Text(if (s.paused) "Resume" else "Pause") }, leadingContent = { Icon(if (s.paused) Icons.Default.PlayArrow else Icons.Default.Pause, null) })
                HorizontalDivider()
            }
            item {
                section2("Details")
                Detail("When", ScheduleWords.whenText(s, ctx))
                Detail("Next Run", ScheduleWords.next(s, now, ctx).removePrefix("Next: "))
                Detail("Runs", ScheduleWords.kindName(s.kind, w.kinds))
                Detail(if (s.kind == "command") "Command" else "Prompt", s.prompt.ifEmpty { "None" }, mono = s.kind == "command")
                Detail("Folder", s.cwd.replace(Regex("^/(Users|home)/[^/]+"), "~"), mono = true)
                Detail("Group", ScheduleWords.group(w, s))
                if (s.kind == "claude" && s.allowedToolsCount > 0) Detail("Always Allowed", s.allowedToolsList.joinToString(", "))
                Detail("Made By", s.createdBy.ifEmpty { "You" } + if (s.createdUnixMs > 0) " · " + ScheduleWords.date(s.createdUnixMs, now, ctx) else "")
                section2("Runs")
                if (s.runsCount == 0) Text("None yet.", Modifier.padding(horizontal = 20.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            items(s.runsList, key = { it.id }) { r ->
                val linked = hasTab(w, r) || (s.kind != "command" && r.sessionId.isNotEmpty())
                Row(
                    Modifier.fillMaxWidth().let { if (linked) it.clickable { openRun(nav, w, s, r, model) } else it }.padding(horizontal = 20.dp, vertical = 8.dp),
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Icon(r.status.icon, r.status.label, tint = r.status.color(), modifier = Modifier.size(18.dp).padding(top = 2.dp))
                    Column(Modifier.weight(1f)) {
                        Row {
                            val whenMs = if (r.startedUnixMs > 0) r.startedUnixMs else if (r.dueUnixMs > 0) r.dueUnixMs else r.endedUnixMs
                            Text(if (whenMs > 0) ScheduleWords.date(whenMs, now, ctx) else r.status.label, fontWeight = FontWeight.Medium, modifier = Modifier.weight(1f))
                            ScheduleWords.duration(r, now)?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                        }
                        Text(ScheduleWords.note(r, s), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2)
                        if (linked && !hasTab(w, r)) Text("Resume Session", color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
            item { MacOnlyFooter() }
        }
    }
}

@Composable
private fun section2(title: String) = Text(title, Modifier.padding(start = 20.dp, top = 20.dp, bottom = 6.dp),
    style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary)

@Composable
private fun Detail(label: String, value: String, mono: Boolean = false) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 6.dp)) {
        Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, style = MaterialTheme.typography.bodyMedium, fontFamily = if (mono) FontFamily.Monospace else null)
    }
}
