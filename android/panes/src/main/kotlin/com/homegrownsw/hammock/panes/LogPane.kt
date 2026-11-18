package com.homegrownsw.hammock.panes

import android.content.ClipData
import android.content.ClipboardManager
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Label
import androidx.compose.material.icons.filled.Apps
import androidx.compose.material.icons.filled.Cancel
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.FilterList
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.VerticalAlignBottom
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import hammock.v1.Hammock.LogEntry
import hammock.v1.Hammock.LogLevel
import kotlinx.coroutines.flow.distinctUntilChanged
import java.text.NumberFormat
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * A log pane: the pane's output parsed into entries (the daemon does it),
 * filtered by text (or /regex/), level, process and tag, following the end
 * as entries come until you scroll up. Tap an entry for all of it and its
 * fields; touch and hold for Copy, Show Only and Hide.
 */
@Composable
fun LogPane(model: AppModel, pane: Hammock.Pane, theme: PaneTheme, modifier: Modifier = Modifier) {
    val session = remember(model, pane.id) { LogSession(model, pane.id) }
    DisposableEffect(session) {
        session.start()
        onDispose { session.stop() }
    }
    val state by session.state.collectAsState()
    var text by rememberSaveable(pane.id) { mutableStateOf(state.text) }
    var follow by rememberSaveable(pane.id) { mutableStateOf(true) }
    val fg = color(theme.foreground)
    val colors = LogColors(fg = fg, dim = fg.copy(alpha = 0.6f), faint = fg.copy(alpha = 0.4f), accent = color(theme.accent), dark = theme.dark)

    Column(modifier.fillMaxSize().background(color(theme.background)).imePadding()) {
        FilterBar(session, state, text, { text = it; session.setText(it) }, follow, { follow = it }, colors)
        HorizontalDivider(color = fg.copy(alpha = 0.12f))
        if (state.entries.isEmpty()) {
            Empty(state, colors) { text = ""; session.clearFilters() }
        } else {
            Entries(session, state, follow, { follow = it }, colors)
        }
    }
}

internal data class LogColors(val fg: Color, val dim: Color, val faint: Color, val accent: Color, val dark: Boolean)

@Composable
private fun Empty(state: LogState, c: LogColors, clear: () -> Unit) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        when {
            state.error.isNotEmpty() -> Text(state.error, color = c.dim, modifier = Modifier.padding(24.dp))
            !state.loaded -> CircularProgressIndicator(Modifier.size(24.dp), color = c.dim, strokeWidth = 2.dp)
            state.isFiltered -> Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Text("Nothing matches", color = c.dim, style = MaterialTheme.typography.titleMedium)
                TextButton(onClick = clear) { Text("Clear Filters", color = c.accent) }
            }
            else -> Text("Waiting for the log…", color = c.dim)
        }
    }
}

@Composable
private fun Entries(session: LogSession, state: LogState, follow: Boolean, setFollow: (Boolean) -> Unit, c: LogColors) {
    val list = rememberLazyListState()
    var expanded by remember { mutableStateOf(setOf<Long>()) }
    val extra = if (state.hasMore) 1 else 0
    // New entries at the end: follow them, while following.
    LaunchedEffect(state.appended, follow) {
        if (follow && state.entries.isNotEmpty()) list.scrollToItem(state.entries.size - 1 + extra)
    }
    // Scrolled up by hand: stop following; back at the end: follow again.
    LaunchedEffect(list) {
        snapshotFlow {
            val info = list.layoutInfo
            val last = info.visibleItemsInfo.lastOrNull()
            val atEnd = last == null || (last.index == info.totalItemsCount - 1 && last.offset + last.size <= info.viewportEndOffset + 24)
            atEnd to list.isScrollInProgress
        }.distinctUntilChanged().collect { (atEnd, scrolling) ->
            if (atEnd) setFollow(true) else if (scrolling) setFollow(false)
        }
    }
    LazyColumn(state = list, modifier = Modifier.fillMaxSize()) {
        if (state.hasMore) {
            item(key = "older") {
                LaunchedEffect(Unit) { if (!follow) session.loadOlder() }
                TextButton(onClick = { session.loadOlder() }, modifier = Modifier.fillMaxWidth()) {
                    Text(if (state.loadingOlder) "Loading…" else "Show Older", color = c.accent, style = MaterialTheme.typography.labelMedium)
                }
            }
        }
        items(state.entries, key = { it.seq }) { e ->
            LogRow(e, state.text, e.seq in expanded, session, c) {
                expanded = if (e.seq in expanded) expanded - e.seq else expanded + e.seq
            }
        }
    }
}

private val timeFormat = object : ThreadLocal<SimpleDateFormat>() {
    override fun initialValue() = SimpleDateFormat("HH:mm:ss.SSS", Locale.ROOT)
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun LogRow(e: LogEntry, highlight: String, expanded: Boolean, session: LogSession, c: LogColors, toggle: () -> Unit) {
    var menu by remember { mutableStateOf(false) }
    val context = LocalContext.current
    val level = levelColor(e.level)
    val style = TextStyle(fontFamily = FontFamily.Monospace, fontSize = 12.sp, lineHeight = 16.sp)
    val lines = e.message.count { it == '\n' }
    Box {
        Column(
            Modifier.fillMaxWidth().background(rowTint(e.level)).combinedClickable(onClick = toggle, onLongClick = { menu = true })
                .padding(horizontal = 10.dp, vertical = 3.dp),
        ) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(timeFormat.get()!!.format(Date(e.timeUnixMs)), style = style, color = c.dim)
                Text(e.level.short, style = style.copy(fontWeight = FontWeight.SemiBold), color = level, modifier = Modifier.width(44.dp))
                if (e.process.isNotEmpty()) Text(e.process, style = style, color = c.dim, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                if (e.tag.isNotEmpty()) Text(e.tag, style = style, color = c.faint, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
                if (!expanded && lines > 0) Text("+$lines", style = style.copy(fontSize = 10.sp), color = c.faint)
            }
            // On a phone the message gets a line of its own: the columns leave too little beside them.
            Text(
                highlighted(if (expanded) e.message else e.message.substringBefore('\n'), highlight, c.dark),
                style = style,
                color = if (e.level == LogLevel.LOG_LEVEL_DEBUG) c.dim else c.fg,
                maxLines = if (expanded) Int.MAX_VALUE else 2,
                overflow = TextOverflow.Ellipsis,
            )
            if (expanded) Details(e, style, c)
        }
        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
            fun close() { menu = false }
            DropdownMenuItem(text = { Text("Copy Message") }, onClick = {
                close()
                context.getSystemService(ClipboardManager::class.java)?.setPrimaryClip(ClipData.newPlainText("Log entry", e.message))
            })
            if (e.process.isNotEmpty()) {
                HorizontalDivider()
                DropdownMenuItem(text = { Text("Show Only ${e.process}") }, onClick = { close(); session.toggleOnly("process:" + e.process) })
                DropdownMenuItem(text = { Text("Hide ${e.process}") }, onClick = { close(); session.toggleHide("process:" + e.process) })
            }
            if (e.tag.isNotEmpty()) {
                HorizontalDivider()
                DropdownMenuItem(text = { Text("Show Only ${e.tag}") }, onClick = { close(); session.toggleOnly("tag:" + e.tag) })
                DropdownMenuItem(text = { Text("Hide ${e.tag}") }, onClick = { close(); session.toggleHide("tag:" + e.tag) })
            }
            HorizontalDivider()
            DropdownMenuItem(text = { Text("Show Only ${e.level.label}") }, onClick = { close(); session.setLevels(setOf(e.level)) })
        }
    }
}

@Composable
private fun Details(e: LogEntry, style: TextStyle, c: LogColors) {
    val rows = buildList {
        if (e.pid != 0) add("pid" to e.pid.toString())
        if (e.category.isNotEmpty()) add("category" to e.category)
        addAll(e.fieldsMap.toSortedMap().toList())
    }
    if (rows.isEmpty()) return
    Column(Modifier.padding(start = 16.dp, top = 4.dp)) {
        for ((k, v) in rows) {
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Text(k, style = style, color = c.dim)
                Text(v, style = style, color = c.fg)
            }
        }
    }
}

/** The filter's text marked in a message (not for a /regex/: the daemon matches those). */
internal fun highlighted(message: String, query: String, dark: Boolean): AnnotatedString {
    val q = query.trim()
    if (q.isEmpty() || (q.length > 2 && q.startsWith("/") && q.endsWith("/"))) return AnnotatedString(message)
    return buildAnnotatedString {
        append(message)
        var from = 0
        while (true) {
            val i = message.indexOf(q, from, ignoreCase = true)
            if (i < 0) break
            addStyle(SpanStyle(background = Color(0xFFFFD60A).copy(alpha = if (dark) 0.4f else 0.5f)), i, i + q.length)
            from = i + q.length
        }
    }
}

internal fun levelColor(l: LogLevel): Color = when (l) {
    LogLevel.LOG_LEVEL_DEBUG -> Color(0xFF8E8E93)
    LogLevel.LOG_LEVEL_NOTICE -> Color(0xFF30B0C7)
    LogLevel.LOG_LEVEL_WARNING -> Color(0xFFFF9500)
    LogLevel.LOG_LEVEL_ERROR -> Color(0xFFFF3B30)
    LogLevel.LOG_LEVEL_FAULT -> Color(0xFFAF52DE)
    else -> Color(0xFF0A84FF)
}

private fun rowTint(l: LogLevel): Color = when (l) {
    LogLevel.LOG_LEVEL_WARNING -> Color(0xFFFF9500).copy(alpha = 0.07f)
    LogLevel.LOG_LEVEL_ERROR -> Color(0xFFFF3B30).copy(alpha = 0.08f)
    LogLevel.LOG_LEVEL_FAULT -> Color(0xFFAF52DE).copy(alpha = 0.1f)
    else -> Color.Transparent
}

/** Text, follow and clear; level chips, process and tag menus and the count; the filters in use. */
@Composable
private fun FilterBar(
    session: LogSession,
    state: LogState,
    text: String,
    setText: (String) -> Unit,
    follow: Boolean,
    setFollow: (Boolean) -> Unit,
    c: LogColors,
) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 10.dp, vertical = 6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Row(
                Modifier.weight(1f).background(c.fg.copy(alpha = 0.08f), RoundedCornerShape(8.dp)).padding(horizontal = 8.dp, vertical = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(Icons.Filled.FilterList, contentDescription = null, tint = c.dim, modifier = Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp))
                Box(Modifier.weight(1f)) {
                    if (text.isEmpty()) Text("Filter (text or /regex/)", color = c.faint, style = MaterialTheme.typography.bodyMedium)
                    BasicTextField(
                        value = text, onValueChange = setText, singleLine = true,
                        textStyle = MaterialTheme.typography.bodyMedium.copy(color = c.fg),
                        cursorBrush = SolidColor(c.accent),
                        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.None, autoCorrectEnabled = false),
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
                if (text.isNotEmpty()) {
                    Icon(
                        Icons.Filled.Cancel, contentDescription = "Clear", tint = c.faint,
                        modifier = Modifier.size(18.dp).clickable { setText("") },
                    )
                }
            }
            IconButton(onClick = { setFollow(!follow) }) {
                Icon(
                    if (follow) Icons.Filled.VerticalAlignBottom else Icons.Filled.Pause,
                    contentDescription = if (follow) "Following new entries" else "Paused: follow new entries",
                    tint = if (follow) c.accent else c.dim,
                )
            }
            IconButton(onClick = { session.clearLog() }) {
                Icon(Icons.Filled.Delete, contentDescription = "Clear the log", tint = c.dim)
            }
        }
        Row(
            Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            for (l in state.presentLevels) {
                val on = l in state.levels
                val lc = levelColor(l)
                Chip(l.label, if (on) lc else lc.copy(alpha = 0.14f), if (on) Color.White else lc, onClick = { session.toggleLevel(l) })
            }
            FacetMenu(session, state, "process", "Process", c)
            FacetMenu(session, state, "tag", "Tag", c)
            Text(count(state), color = c.dim, style = MaterialTheme.typography.labelSmall, modifier = Modifier.padding(start = 4.dp))
        }
        if (state.only.isNotEmpty() || state.hide.isNotEmpty()) {
            Row(
                Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                for (k in state.only) Token(k, hidden = false, c) { session.toggleOnly(k) }
                for (k in state.hide) Token(k, hidden = true, c) { session.toggleHide(k) }
                TextButton(onClick = { setText(""); session.clearFilters() }) { Text("Clear", color = c.accent, style = MaterialTheme.typography.labelMedium) }
            }
        }
    }
}

private fun count(s: LogState): String {
    val n = s.entries.size
    if (s.isFiltered) return "$n${if (s.hasMore) "+" else ""} shown"
    return "${NumberFormat.getIntegerInstance().format(s.total)} entries"
}

@Composable
private fun FacetMenu(session: LogSession, state: LogState, kind: String, title: String, c: LogColors) {
    var open by remember { mutableStateOf(false) }
    val fs = state.facets(kind)
    Box {
        Chip(
            "$title ▾", c.fg.copy(alpha = if (fs.isEmpty()) 0.04f else 0.1f), if (fs.isEmpty()) c.faint else c.fg,
            onClick = { if (fs.isNotEmpty()) open = true },
        )
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            for ((name, key, n) in fs.take(40)) {
                val on = key in state.only
                DropdownMenuItem(
                    text = { Text((if (on) "✓ " else "") + "$name  ${NumberFormat.getIntegerInstance().format(n)}") },
                    onClick = { session.toggleOnly(key) },
                )
            }
        }
    }
}

@Composable
private fun Token(key: String, hidden: Boolean, c: LogColors, remove: () -> Unit) {
    val kind = key.substringBefore(':', "")
    val name = if (':' in key) key.substringAfter(':') else key
    Row(
        Modifier.background(c.accent.copy(alpha = 0.16f), RoundedCornerShape(50)).padding(start = 8.dp, end = 4.dp, top = 3.dp, bottom = 3.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Icon(if (kind == "tag") Icons.AutoMirrored.Filled.Label else Icons.Filled.Apps, contentDescription = null, tint = c.fg, modifier = Modifier.size(12.dp))
        Text(if (hidden) "Not $name" else name, color = c.fg, style = MaterialTheme.typography.labelSmall, maxLines = 1)
        Icon(Icons.Filled.Close, contentDescription = "Remove", tint = c.dim, modifier = Modifier.size(16.dp).clickable(onClick = remove))
    }
}
