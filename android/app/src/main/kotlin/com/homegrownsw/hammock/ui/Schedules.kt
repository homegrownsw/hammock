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
import androidx.compose.material.icons.filled.Cancel
import androidx.compose.material.icons.automirrored.outlined.Article
import androidx.compose.material.icons.automirrored.filled.Undo
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.PauseCircle
import androidx.compose.material.icons.outlined.PlayCircle
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material3.FilledTonalButton
import androidx.compose.foundation.layout.heightIn
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
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.semantics
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
import hammock.v1.Hammock.EventTrigger
import hammock.v1.Hammock.GitHubTrigger
import hammock.v1.Hammock.Schedule
import hammock.v1.Hammock.ScheduleRun
import hammock.v1.Hammock.ScheduleRun.Status
import kotlinx.coroutines.CoroutineScope
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
    Status.STATUS_FAILED -> Icons.Default.Cancel; Status.STATUS_GAVE_UP -> Icons.Default.HourglassBottom; Status.STATUS_SKIPPED -> Icons.Default.SkipNext
    Status.STATUS_MISSED -> Icons.Default.Bedtime; Status.STATUS_STOPPED -> Icons.Default.Block; else -> Icons.Default.RadioButtonUnchecked
}
@Composable fun Status.color(): Color = when (this) {
    Status.STATUS_RUNNING -> StatusPalette.indigo(); Status.STATUS_WAITING -> StatusPalette.amber(); Status.STATUS_READY -> StatusPalette.green()
    Status.STATUS_FAILED, Status.STATUS_GAVE_UP -> StatusPalette.red(); else -> MaterialTheme.colorScheme.onSurfaceVariant
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

    /**
     * When it runs: its times, "Once, …", "When CI fails on my pull request" (only on a trigger), or
     * its time and "+ 2 triggers", as the Mac and iPhone say it (HammockKit's Schedule.whenText).
     */
    fun whenText(s: Schedule, ctx: android.content.Context, w: Workspace? = null): String {
        val times = timesText(s, ctx)
        val n = s.eventsCount
        return when {
            times == null && n == 0 -> "Never"
            times == null && n == 1 -> "When " + lowercasedFirst(trigger(s.getEvents(0), w))
            times == null -> "On $n triggers"
            n == 0 -> times
            else -> "$times + $n trigger" + if (n == 1) "" else "s"
        }
    }

    /** When it runs by the clock (null: only on its triggers). */
    fun timesText(s: Schedule, ctx: android.content.Context): String? = when (s.timing) {
        Schedule.Timing.TIMING_ONCE -> "Once, " + date(s.onceUnixMs, System.currentTimeMillis(), ctx)
        Schedule.Timing.TIMING_EVENTS -> null
        else -> if (s.cron.isNotEmpty()) "Cron ${s.cron}" else {
            val cal = Calendar.getInstance().apply { set(Calendar.HOUR_OF_DAY, s.minuteOfDay / 60); set(Calendar.MINUTE, s.minuteOfDay % 60) }
            days(s.weekdaysList) + " at " + time(cal.timeInMillis, ctx)
        }
    }

    /** "CI …" stays as it is (two capitals); "An agent …" becomes "an agent …". */
    private fun lowercasedFirst(t: String): String {
        if (t.isEmpty() || !t[0].isUpperCase()) return t
        if (t.length > 1 && t[1].isUpperCase()) return t
        return t[0].lowercaseChar() + t.substring(1)
    }

    /** A pane's (by its tab) or Group's name, for words about triggers. */
    private fun placeName(w: Workspace?, id: String): String? {
        if (w == null) return null
        w.groups.firstOrNull { it.id == id }?.let { return w.title(it) }
        val p = w.panes[id] ?: return null
        val t = w.tabs[p.tabId] ?: return null
        return t.name.ifEmpty { t.autoName }
    }

    /** What fires a trigger, in words (HammockKit's EventTrigger.text). */
    fun trigger(t: EventTrigger, w: Workspace?): String {
        fun place(pane: String, group: String): String {
            val id = pane.ifEmpty { group }
            if (id.isEmpty()) return ""
            if (w == null) return " in $id"
            placeName(w, id)?.takeIf { it.isNotEmpty() }?.let { return " in $it" }
            return if (pane.isEmpty()) " in a Group that’s closed" else " in a pane that’s closed"
        }
        return when (t.kindCase) {
            EventTrigger.KindCase.GITHUB -> {
                val g = t.github
                val repo = if (g.repo.isEmpty()) "" else " (${g.repo})"
                when (g.event) {
                    GitHubTrigger.Event.EVENT_CI_FAILED -> "CI fails on my pull request or branch"
                    GitHubTrigger.Event.EVENT_REVIEW_REQUESTED -> "My review is requested"
                    GitHubTrigger.Event.EVENT_REVIEWED -> "My pull request gets a review or comment"
                    GitHubTrigger.Event.EVENT_LABELED -> "An issue or pull request is labelled “${g.label}”"
                    GitHubTrigger.Event.EVENT_ASSIGNED -> "An issue or pull request is assigned to me"
                    GitHubTrigger.Event.EVENT_MERGED -> "My pull request is merged"
                    else -> "GitHub"
                } + repo
            }
            EventTrigger.KindCase.AGENT -> {
                val a = t.agent
                val what = if (a.finished && a.needsYou) "finishes or needs me" else if (a.needsYou) "needs me" else "finishes"
                "An agent $what" + place(a.paneId, a.groupId)
            }
            EventTrigger.KindCase.EXIT -> "A command fails" + place(t.exit.paneId, t.exit.groupId)
            EventTrigger.KindCase.LOG -> "A log line matches /${t.log.pattern}/" + place(t.log.paneId, "")
            EventTrigger.KindCase.FILES -> {
                val f = t.files
                var out = if (f.glob.isEmpty()) "Files change" else "${f.glob} changes"
                if (f.folder.isNotEmpty()) out += " in " + f.folder.replace(Regex("^/(Users|home)/[^/]+"), "~")
                out
            }
            EventTrigger.KindCase.CALENDAR -> {
                val c = t.calendar
                var out = when (c.minutesBefore) {
                    0 -> "At the start of events"
                    1 -> "1 minute before events"
                    else -> "${c.minutesBefore} minutes before events"
                }
                if (c.title.isNotEmpty()) out += " titled “${c.title}”"
                if (c.calendar.isNotEmpty()) out += " in ${c.calendar}"
                if (c.withAttendees) out += " with other people"
                out
            }
            EventTrigger.KindCase.WEBHOOK -> "Its webhook is called"
            else -> "An unknown event"
        }
    }

    /** What a list of runs marks it with: what started it ("CI failed on #42"), Run Early's time, "Run Now". */
    fun marker(r: ScheduleRun, now: Long, ctx: android.content.Context): String? = when {
        r.trigger.isNotEmpty() -> r.trigger
        r.early -> "Instead of " + date(r.dueUnixMs, now, ctx).removePrefix("Today at ")
        r.manual -> "Run Now"
        else -> null
    }

    fun group(w: Workspace, s: Schedule): String {
        w.groups.firstOrNull { it.id == s.groupId }?.let { g -> return (if (g.icon.isEmpty()) "" else g.icon + " ") + w.title(g) }
        return (if (s.groupIcon.isEmpty()) "" else s.groupIcon + " ") + s.groupName
    }

    fun where(w: Workspace, s: Schedule, ctx: android.content.Context) =
        listOf(group(w, s), kindName(s.kind, w.kinds), whenText(s, ctx, w)).filter { it.isNotEmpty() }.joinToString(" · ")

    fun next(s: Schedule, now: Long, ctx: android.content.Context): String = when {
        s.error.isNotEmpty() -> s.error
        s.paused -> "Paused"
        s.nextRunUnixMs <= 0 && s.eventsCount > 0 -> "When triggered"
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

/** A schedule's computer, when it's another of yours: its badge goes beside the name. */
fun Workspace.computerOf(s: Schedule): Hammock.Computer? {
    if (s.computerId.isEmpty() || s.computerId == myUserID) return null
    return computers.firstOrNull { it.userId == s.computerId && !it.thisComputer }
}

/** A run that's ready: its schedule, Group and time, what it said, and what it showed. */
@Composable
fun ReadyRunRow(w: Workspace, s: Schedule, r: ScheduleRun, now: Long, onClick: () -> Unit) {
    val ctx = LocalContext.current
    val quiet = MaterialTheme.colorScheme.onSurfaceVariant
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 12.dp), horizontalArrangement = Arrangement.spacedBy(14.dp)) {
        Icon(r.status.icon, r.status.label, tint = r.status.color(), modifier = Modifier.padding(top = 2.dp).size(22.dp))
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(s.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                    w.computerOf(s)?.let { ComputerIcon(it, 13.dp) }
                }
                Text(ScheduleWords.time(r.endedUnixMs, ctx), style = MaterialTheme.typography.bodyMedium, color = quiet)
            }
            Text(ScheduleWords.group(w, s), style = MaterialTheme.typography.bodyMedium, color = quiet, maxLines = 1)
            if (r.note.isNotEmpty()) Text(plainInline(r.note), style = MaterialTheme.typography.bodyMedium, maxLines = 3)
            ScheduleWords.output(r)?.let {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                    Icon(Icons.AutoMirrored.Outlined.Article, null, Modifier.size(15.dp), tint = quiet)
                    Text(it, style = MaterialTheme.typography.bodySmall, color = quiet, maxLines = 1)
                }
            }
        }
    }
}

/** "Coming Up": the next few, with Skip and Run (Run Early when it's soon); See All opens the list. */
fun LazyListScope.comingUp(model: AppModel, w: Workspace, nav: Nav, now: Long) {
    section("Coming Up") { TextButton(onClick = { nav.open("schedules") }) { Text("See All") } }
    val next = sorted(w.schedules.values).filter { it.upcoming }.take(3)
    if (next.isEmpty()) {
        item(key = "nothing") {
            val paused = w.schedules.values.count { it.paused }
            CardRow(0, 1) {
                Text(if (paused > 0) "Nothing coming up: ${if (paused == 1) "1 automation is" else "$paused automations are"} paused." else "Nothing coming up.",
                    Modifier.padding(16.dp), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
    cardItems(next, key = { "next:" + it.id }, dividerStart = 16.dp) { s -> ComingUpRow(model, w, s, now, nav) }
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

/** A small tinted capsule button, as the iPhone's bordered ones. */
@Composable
fun SmallTonalButton(text: String, enabled: Boolean = true, onClick: () -> Unit) =
    FilledTonalButton(onClick = onClick, enabled = enabled, colors = tintedButtonColors(), contentPadding = PaddingValues(horizontal = 14.dp), modifier = Modifier.heightIn(min = 36.dp)) { Text(text) }

/**
 * A schedule's actions, for its touch-and-hold menu (the iPhone's ScheduleMenuIOS): Run Now, Run
 * Early, Skip, Pause or Resume, and [details] unless the row opens them itself. [scope] is the row's:
 * the menu's own goes with it as it closes.
 */
@Composable
private fun ScheduleMenuItems(model: AppModel, s: Schedule, nav: Nav, now: Long, scope: CoroutineScope, close: () -> Unit, details: (() -> Unit)? = null) {
    val ctx = LocalContext.current
    for (a in scheduleActions(s, details = details != null)) {
        val (icon, act) = when (a) {
            ScheduleAction.RUN_NOW -> Icons.Default.PlayArrow to { scope.launch { runScheduleNow(model, s, false, nav) }; Unit }
            ScheduleAction.RUN_EARLY -> Icons.Outlined.PlayArrow to { scope.launch { runScheduleNow(model, s, true, nav) }; Unit }
            ScheduleAction.SKIP -> Icons.Default.SkipNext to { model.skipScheduleRun(s.id, true); Unit }
            ScheduleAction.DONT_SKIP -> Icons.AutoMirrored.Filled.Undo to { model.skipScheduleRun(s.id, false); Unit }
            ScheduleAction.PAUSE -> Icons.Outlined.PauseCircle to { model.setSchedulePaused(s.id, true); Unit }
            ScheduleAction.RESUME -> Icons.Outlined.PlayCircle to { model.setSchedulePaused(s.id, false); Unit }
            ScheduleAction.DETAILS -> Icons.Outlined.Info to { details?.invoke(); Unit }
        }
        if (a == ScheduleAction.DETAILS) HorizontalDivider()
        val title = if (a == ScheduleAction.RUN_EARLY) "Run Now Instead of ${ScheduleWords.nextShort(s, now, ctx)}" else a.title
        DropdownMenuItem(text = { Text(title) }, leadingIcon = { Icon(icon, null) }, onClick = { close(); act() })
    }
}

/** What a schedule's touch-and-hold menu offers. */
enum class ScheduleAction(val title: String) {
    RUN_NOW("Run Now"), RUN_EARLY("Run Early"), SKIP("Skip Next Run"), DONT_SKIP("Don’t Skip Next Run"),
    PAUSE("Pause"), RESUME("Resume"), DETAILS("Details"),
}

/**
 * A schedule's menu, in order, as the iPhone's: Run Now; Run Early in place of its next run, when
 * one's due and not skipped; Skip (or Don't Skip) that run; Pause or Resume; then [details].
 */
fun scheduleActions(s: Schedule, details: Boolean): List<ScheduleAction> = buildList {
    add(ScheduleAction.RUN_NOW)
    if (s.canRunEarly) add(ScheduleAction.RUN_EARLY)
    if (s.upcoming) add(if (s.skipping) ScheduleAction.DONT_SKIP else ScheduleAction.SKIP)
    add(if (s.paused) ScheduleAction.RESUME else ScheduleAction.PAUSE)
    if (details) add(ScheduleAction.DETAILS)
}

/** Pause or Resume, as a schedule row's swipe left does it, and TalkBack's action for the row. */
@Composable
private fun pauseSwipe(s: Schedule) =
    SwipeAction(if (s.paused) "Resume" else "Pause", if (s.paused) Icons.Default.PlayArrow else Icons.Default.Pause, StatusPalette.amber())

private fun Modifier.pauseAction(model: AppModel, s: Schedule): Modifier = semantics {
    customActions = listOf(CustomAccessibilityAction(if (s.paused) "Resume" else "Pause") { model.setSchedulePaused(s.id, !s.paused); true })
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ComingUpRow(model: AppModel, w: Workspace, s: Schedule, now: Long, nav: Nav) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val early = s.runsEarlyFirst(now)
    var menu by remember { mutableStateOf(false) }
    Box {
        Row(Modifier.fillMaxWidth().combinedClickable(onLongClickLabel = "Actions", onLongClick = { menu = true }) { nav.open("schedule:" + s.id) }
            .padding(horizontal = 16.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(5.dp)) {
                    Text(s.name, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false))
                    w.computerOf(s)?.let { ComputerIcon(it, 12.dp) }
                }
                val running = s.runsList.firstOrNull()?.let { if (it.status == Status.STATUS_RUNNING || it.status == Status.STATUS_WAITING) it.status.label + " · " else "" } ?: ""
                val group = ScheduleWords.group(w, s)
                Text(
                    running + ScheduleWords.date(s.nextRunUnixMs, now, ctx) + (if (group.isEmpty()) "" else " · $group"),
                    style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1,
                    textDecoration = if (s.skipping) TextDecoration.LineThrough else null, overflow = TextOverflow.Ellipsis,
                )
            }
            SmallTonalButton(if (s.skipping) "Don’t Skip" else "Skip") { model.skipScheduleRun(s.id, !s.skipping) }
            SmallTonalButton(if (early) "Run Early" else "Run") { scope.launch { runScheduleNow(model, s, early, nav) } }
        }
        DropdownMenu(menu, { menu = false }) {
            ScheduleMenuItems(model, s, nav, now, scope, close = { menu = false }, details = { nav.open("schedule:" + s.id) })
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
    val quiet = MaterialTheme.colorScheme.onSurfaceVariant
    DetailTop("Automations", nav, showBack) { padding ->
        if (w.schedules.isEmpty()) {
            EmptyState("No Automations", "Run Claude Code, Codex or a command at set times, or when something happens: each run opens a tab in its Group. Create automations on your computer (or with hmk automation).", Modifier.padding(padding))
            return@DetailTop
        }
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(top = padding.calculateTopPadding() + 12.dp, bottom = 24.dp)) {
            cardItems(sorted(w.schedules.values), key = { it.id }, dividerStart = 16.dp) { s ->
                Swipeable(enabled = true, onSwipe = { model.setSchedulePaused(s.id, !s.paused) }, action = pauseSwipe(s)) {
                    ScheduleListRow(model, w, s, now, nav)
                }
            }
            item { MacOnlyFooter() }
        }
    }
}

/**
 * A scheduled task in the list: its name, last run, where and when. Tap for its details, touch and
 * hold for its actions (the details aside), swipe left to pause or resume it.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ScheduleListRow(model: AppModel, w: Workspace, s: Schedule, now: Long, nav: Nav) {
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val quiet = MaterialTheme.colorScheme.onSurfaceVariant
    var menu by remember { mutableStateOf(false) }
    Box {
        Row(
            Modifier.fillMaxWidth()
                .combinedClickable(onLongClickLabel = "Actions", onLongClick = { menu = true }) { nav.push("schedule:" + s.id) }
                .pauseAction(model, s)
                .padding(start = 16.dp, end = 12.dp, top = 12.dp, bottom = 12.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                        Text(s.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                        w.computerOf(s)?.let { ComputerIcon(it, 13.dp) }
                    }
                    s.runsList.firstOrNull()?.let { Icon(it.status.icon, "Last run: " + it.status.label, Modifier.size(20.dp), tint = it.status.color()) }
                }
                Text(ScheduleWords.where(w, s, ctx), style = MaterialTheme.typography.bodyMedium, color = quiet, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(ScheduleWords.next(s, now, ctx), style = MaterialTheme.typography.bodySmall,
                    color = if (s.error.isEmpty()) quiet.copy(alpha = 0.75f) else NeedsYouColor)
            }
            Chevron()
        }
        DropdownMenu(menu, { menu = false }) { ScheduleMenuItems(model, s, nav, now, scope, close = { menu = false }) }
    }
}

@Composable
private fun MacOnlyFooter() = Text(
    "Create and edit automations on your computer (or with hmk automation).",
    Modifier.padding(horizontal = CardInset + 16.dp, vertical = 10.dp), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
)

/** An action in a card: its icon and words in the accent, as the iPhone's. */
@Composable
private fun ActionRow(text: String, icon: ImageVector, onClick: () -> Unit) {
    val accent = MaterialTheme.colorScheme.primary
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Icon(icon, null, tint = accent)
        Text(text, color = accent, style = MaterialTheme.typography.bodyLarge)
    }
}

/** One scheduled task: its actions, when and what it runs, and its runs. */
@Composable
fun ScheduleScreen(model: AppModel, id: String, nav: Nav, showBack: Boolean) {
    val w by model.workspace.collectAsStateWithLifecycle()
    val ctx = LocalContext.current
    val now = rememberNow(60_000)
    val scope = rememberCoroutineScope()
    val s = w.schedules[id]
    DetailTop(s?.name ?: "Automation", nav, showBack) { padding ->
        if (s == null) {
            EmptyState("Automation Deleted", "It was deleted on your computer.", Modifier.padding(padding))
            return@DetailTop
        }
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(top = padding.calculateTopPadding() + 12.dp, bottom = 24.dp)) {
            item {
                GroupedCard {
                    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            Text(s.name, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
                            w.computerOf(s)?.let { ComputerIcon(it, 16.dp) }
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
            }
            item { Spacer(Modifier.size(20.dp)) }
            item {
                GroupedCard {
                    ActionRow("Run Now", Icons.Default.PlayArrow) { scope.launch { runScheduleNow(model, s, false, nav) } }
                    if (s.canRunEarly) {
                        CardDivider(56.dp)
                        ActionRow("Run Now Instead of ${ScheduleWords.nextShort(s, now, ctx)}", Icons.Outlined.PlayArrow) { scope.launch { runScheduleNow(model, s, true, nav) } }
                    }
                    if (s.upcoming) {
                        CardDivider(56.dp)
                        ActionRow(if (s.skipping) "Don’t Skip Next Run" else "Skip Next Run", Icons.Default.SkipNext) { model.skipScheduleRun(s.id, !s.skipping) }
                    }
                    CardDivider(56.dp)
                    ActionRow(if (s.paused) "Resume" else "Pause", if (s.paused) Icons.Default.PlayArrow else Icons.Outlined.PauseCircle) { model.setSchedulePaused(s.id, !s.paused) }
                }
            }
            item {
                SectionHeader("Details")
                GroupedCard {
                    val rows = buildList {
                        add(Triple("When", ScheduleWords.timesText(s, ctx) ?: "Only when triggered", false))
                        add(Triple("Next Run", ScheduleWords.next(s, now, ctx).removePrefix("Next: "), false))
                        add(Triple("Runs", ScheduleWords.kindName(s.kind, w.kinds), false))
                        add(Triple(if (s.kind == "command") "Command" else "Prompt", s.prompt.ifEmpty { "None" }, s.kind == "command"))
                        add(Triple("Folder", s.cwd.replace(Regex("^/(Users|home)/[^/]+"), "~"), true))
                        add(Triple("Group", ScheduleWords.group(w, s), false))
                        if (s.kind == "claude" && s.allowedToolsCount > 0) add(Triple("Always Allowed", s.allowedToolsList.joinToString(", "), false))
                        add(Triple("Made By", s.createdBy.ifEmpty { "You" } + if (s.createdUnixMs > 0) " · " + ScheduleWords.date(s.createdUnixMs, now, ctx) else "", false))
                    }
                    rows.forEachIndexed { i, (label, value, mono) ->
                        if (i > 0) CardDivider()
                        Detail(label, value, mono)
                    }
                }
                if (s.eventsCount > 0) {
                    SectionHeader("Triggers")
                    GroupedCard {
                        s.eventsList.forEachIndexed { i, t ->
                            if (i > 0) CardDivider()
                            Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                Text(ScheduleWords.trigger(t, w), style = MaterialTheme.typography.bodyLarge)
                                if (t.error.isNotEmpty()) Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                    Icon(Icons.Default.Warning, null, tint = NeedsYouColor, modifier = Modifier.size(16.dp))
                                    Text(t.error, style = MaterialTheme.typography.bodySmall, color = NeedsYouColor)
                                }
                            }
                        }
                        CardDivider()
                        val cd = if (s.hasCooldownSeconds()) maxOf(s.cooldownSeconds, 0) else 60
                        Detail("Cooldown", if (cd == 0) "None" else if (cd % 60 == 0) "${cd / 60} min" else "$cd s")
                    }
                }
                SectionHeader("Runs")
            }
            if (s.runsCount == 0) item {
                CardRow(0, 1) { Text("None yet.", Modifier.padding(16.dp), color = MaterialTheme.colorScheme.onSurfaceVariant) }
            }
            cardItems(s.runsList, key = { it.id }) { r ->
                val linked = hasTab(w, r) || (s.kind != "command" && r.sessionId.isNotEmpty())
                Row(
                    Modifier.fillMaxWidth().let { if (linked) it.clickable { openRun(nav, w, s, r, model) } else it }.padding(start = 16.dp, end = 12.dp, top = 12.dp, bottom = 12.dp),
                    horizontalArrangement = Arrangement.spacedBy(14.dp), verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(r.status.icon, r.status.label, tint = r.status.color(), modifier = Modifier.size(22.dp))
                    Column(Modifier.weight(1f)) {
                        Row {
                            val whenMs = if (r.startedUnixMs > 0) r.startedUnixMs else if (r.dueUnixMs > 0) r.dueUnixMs else r.endedUnixMs
                            Text(if (whenMs > 0) ScheduleWords.date(whenMs, now, ctx) else r.status.label, fontWeight = FontWeight.Medium, modifier = Modifier.weight(1f))
                            ScheduleWords.duration(r, now)?.let { Text(it, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                        }
                        ScheduleWords.marker(r, now, ctx)?.let {
                            Text(it, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary, maxLines = 1)
                        }
                        Text(ScheduleWords.note(r, s), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2)
                        ScheduleWords.output(r)?.let {
                            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                                Icon(Icons.AutoMirrored.Outlined.Article, null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                                Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
                            }
                        }
                        if (linked && !hasTab(w, r)) Text("Resume Session", color = MaterialTheme.colorScheme.primary, style = MaterialTheme.typography.bodySmall)
                    }
                    if (linked) Chevron()
                }
            }
            item { MacOnlyFooter() }
        }
    }
}

/** A detail: its label and value on one line, or the value under it when long (a prompt, a folder). */
@Composable
private fun Detail(label: String, value: String, mono: Boolean = false) {
    val long = mono || value.length > 28
    if (long) {
        Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text(label, style = MaterialTheme.typography.bodyLarge)
            Text(value, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, fontFamily = if (mono) FontFamily.Monospace else null)
        }
    } else {
        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 14.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(label, style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
            Text(value, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}
