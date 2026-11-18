package com.homegrownsw.hammock.panes

import androidx.compose.runtime.Immutable
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import hammock.v1.Hammock.LogEntry
import hammock.v1.Hammock.LogFacet
import hammock.v1.Hammock.LogFilter
import hammock.v1.Hammock.LogLevel
import hammock.v1.Hammock.LogView
import hammock.v1.Hammock.ServerMessage
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** What a log pane shows: the entries the daemon sent for the filter set here, and the filter. */
@Immutable
data class LogState(
    val entries: List<LogEntry> = emptyList(),
    val facets: List<LogFacet> = emptyList(),
    /** Older matching entries the daemon still has. */
    val hasMore: Boolean = false,
    val total: Long = 0,
    val loadingOlder: Boolean = false,
    val loaded: Boolean = false,
    val error: String = "",
    val text: String = "",
    val levels: Set<LogLevel> = emptySet(),
    val only: List<String> = emptyList(),
    val hide: List<String> = emptyList(),
    /** Bumped when entries arrive at the end (the list scrolls to follow). */
    val appended: Int = 0,
) {
    val isFiltered: Boolean get() = text.isNotEmpty() || levels.isNotEmpty() || only.isNotEmpty() || hide.isNotEmpty()

    val filter: LogFilter get() = LogFilter.newBuilder().setText(text).addAllLevels(levels.sortedBy { it.number })
        .addAllOnly(only).addAllHide(hide).build()

    /** The facets of one kind ("process", "tag", "level"), as name, key and count, most first (as the daemon sends them). */
    fun facets(kind: String): List<Triple<String, String, Long>> = facets.mapNotNull { f ->
        val i = f.key.indexOf(':')
        if (i < 0 || f.key.substring(0, i) != kind) null else Triple(f.key.substring(i + 1), f.key, f.count)
    }

    /** The levels the log has, lowest first. */
    val presentLevels: List<LogLevel> get() = facets("level").mapNotNull { levelNamed(it.first) }.sortedBy { it.number }
}

fun levelNamed(name: String): LogLevel? = when (name) {
    "debug" -> LogLevel.LOG_LEVEL_DEBUG
    "info" -> LogLevel.LOG_LEVEL_INFO
    "notice" -> LogLevel.LOG_LEVEL_NOTICE
    "warning" -> LogLevel.LOG_LEVEL_WARNING
    "error" -> LogLevel.LOG_LEVEL_ERROR
    "fault" -> LogLevel.LOG_LEVEL_FAULT
    else -> null
}

val LogLevel.label: String get() = when (this) {
    LogLevel.LOG_LEVEL_DEBUG -> "Debug"
    LogLevel.LOG_LEVEL_NOTICE -> "Notice"
    LogLevel.LOG_LEVEL_WARNING -> "Warning"
    LogLevel.LOG_LEVEL_ERROR -> "Error"
    LogLevel.LOG_LEVEL_FAULT -> "Fault"
    else -> "Info"
}

/** As the entries' column shows it. */
val LogLevel.short: String get() = when (this) {
    LogLevel.LOG_LEVEL_DEBUG -> "DEBUG"
    LogLevel.LOG_LEVEL_NOTICE -> "NOTE"
    LogLevel.LOG_LEVEL_WARNING -> "WARN"
    LogLevel.LOG_LEVEL_ERROR -> "ERROR"
    LogLevel.LOG_LEVEL_FAULT -> "FAULT"
    else -> "INFO"
}

/**
 * The log's state and how the daemon's LogViews change it, apart from the
 * connection (so it's tested on the JVM). The daemon filters; this keeps
 * the newest few thousand entries, and drops pages for a filter that's been
 * replaced. Thread-safe: updates arrive on the model's thread, filters
 * change on the main one.
 */
class LogFeed {
    private val lock = Any()
    private val _state = MutableStateFlow(LogState())
    val state: StateFlow<LogState> = _state.asStateFlow()

    /** Waiting for the page of a new filter: entries for the old one still on their way are dropped. */
    private var awaiting: LogFilter? = null

    /** The filter to watch with now (it's awaited). */
    fun watching(): LogFilter = synchronized(lock) {
        val f = _state.value.filter
        awaiting = f
        f
    }

    fun apply(v: LogView) = synchronized(lock) {
        var s = _state.value
        if (v.error.isNotEmpty()) s = s.copy(error = v.error)
        if ((v.facetsCount > 0 || v.replace) && v.facetsList != s.facets) s = s.copy(facets = v.facetsList)
        if (v.total > 0 || v.replace) s = s.copy(total = v.total)
        when {
            v.replace -> {
                // Only the page for the filter asked for last.
                val a = awaiting
                if (a != null && v.filter != a) { _state.value = s; return@synchronized }
                awaiting = null
                s = s.copy(entries = v.entriesList, hasMore = v.hasMore, loaded = true, appended = s.appended + 1)
            }
            awaiting != null -> {}
            v.older -> {
                s = s.copy(loadingOlder = false)
                if (v.filter == s.filter) s = s.copy(entries = v.entriesList + s.entries, hasMore = v.hasMore)
            }
            v.entriesCount > 0 -> {
                var es = s.entries + v.entriesList
                var more = s.hasMore
                if (es.size > CAP + CAP / 5) {
                    es = es.subList(es.size - CAP, es.size).toList()
                    more = true
                }
                s = s.copy(entries = es, hasMore = more, appended = s.appended + 1)
            }
        }
        _state.value = s
    }

    fun setText(t: String): Boolean = change { if (it.text == t) null else it.copy(text = t) }

    fun toggleLevel(l: LogLevel) = change { it.copy(levels = if (l in it.levels) it.levels - l else it.levels + l) }

    fun setLevels(ls: Set<LogLevel>) = change { it.copy(levels = ls) }

    /** Show only entries with key ("process:Safari"), or stop. */
    fun toggleOnly(key: String) = change {
        it.copy(hide = it.hide - key, only = if (key in it.only) it.only - key else it.only + key)
    }

    /** Hide entries with key, or show them again. */
    fun toggleHide(key: String) = change {
        it.copy(only = it.only - key, hide = if (key in it.hide) it.hide - key else it.hide + key)
    }

    fun clearFilters() = change { it.copy(text = "", levels = emptySet(), only = emptyList(), hide = emptyList()) }

    /** Where to page older entries in from (null: nothing to ask for), marking it loading. */
    fun olderFrom(): Long? = synchronized(lock) {
        val s = _state.value
        val first = s.entries.firstOrNull()
        if (!s.hasMore || s.loadingOlder || first == null) return@synchronized null
        _state.value = s.copy(loadingOlder = true)
        first.seq
    }

    /** Changes the filter; whether it changed (then it's to be watched again). */
    private fun change(fn: (LogState) -> LogState?): Boolean = synchronized(lock) {
        val next = fn(_state.value) ?: return@synchronized false
        _state.value = next
        true
    }

    companion object {
        /** The most kept here; the oldest go (and can be paged back in). */
        const val CAP = 5000
    }
}

/**
 * One log pane's view (as the iPhone's LogSession): watches the pane's log
 * with the filter set here, again after every reconnect and whenever the
 * filter changes (text after a pause).
 */
class LogSession(val model: AppModel, val paneID: String) {
    val feed = LogFeed()
    val state: StateFlow<LogState> get() = feed.state
    private var stops: List<() -> Unit> = emptyList()
    private var textJob: Job? = null

    fun start() {
        if (stops.isNotEmpty()) return
        stops = listOf(
            model.listen(ServerMessage.MsgCase.LOG_VIEW) { msg -> if (msg.logView.paneId == paneID) feed.apply(msg.logView) },
            model.onEachConnection { watch() },
        )
    }

    fun stop() {
        stops.forEach { it() }
        stops = emptyList()
        textJob?.cancel()
        model.send { setWatchLog(Hammock.WatchLog.newBuilder().setPaneId(paneID).setWatch(false).build()) }
    }

    private fun watch() {
        val f = feed.watching()
        model.send { setWatchLog(Hammock.WatchLog.newBuilder().setPaneId(paneID).setWatch(true).setFilter(f).setLimit(500).build()) }
    }

    fun setText(t: String) {
        if (!feed.setText(t)) return
        textJob?.cancel()
        textJob = model.scope.launch {
            delay(200)
            watch()
        }
    }

    fun toggleLevel(l: LogLevel) { feed.toggleLevel(l); watch() }
    fun setLevels(ls: Set<LogLevel>) { feed.setLevels(ls); watch() }
    fun toggleOnly(key: String) { feed.toggleOnly(key); watch() }
    fun toggleHide(key: String) { feed.toggleHide(key); watch() }
    fun clearFilters() { feed.clearFilters(); watch() }

    fun loadOlder() {
        val before = feed.olderFrom() ?: return
        val f = feed.state.value.filter
        model.send {
            setWatchLog(Hammock.WatchLog.newBuilder().setPaneId(paneID).setWatch(true).setFilter(f).setBeforeSeq(before).setLimit(500).build())
        }
    }

    /** Forgets the entries held for the pane (the terminal keeps its output). */
    fun clearLog() = model.send { setClearLog(Hammock.ClearLog.newBuilder().setPaneId(paneID).build()) }
}
