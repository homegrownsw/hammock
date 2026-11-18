package com.homegrownsw.hammock.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.LargeTopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.Prefs
import com.homegrownsw.hammock.Session
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Link
import com.homegrownsw.hammock.client.Workspace

/**
 * What Sessions and Groups share: the computer's name as the title, its
 * connection as a banner while it isn't connected, and their list.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CockpitScaffold(
    session: Session,
    model: AppModel,
    actions: @Composable RowScope.() -> Unit = {},
    content: @Composable (PaddingValues) -> Unit,
) {
    val host by session.host.collectAsStateWithLifecycle()
    val link by model.link.collectAsStateWithLifecycle()
    // A large title that shrinks into the bar as the list scrolls, as the iPhone's.
    val behavior = TopAppBarDefaults.exitUntilCollapsedScrollBehavior()
    Scaffold(
        modifier = Modifier.nestedScroll(behavior.nestedScrollConnection),
        topBar = {
            Column {
                LargeTopAppBar(
                    title = { Text(host?.shortName?.ifEmpty { "Hammock" } ?: "Hammock", maxLines = 1, overflow = TextOverflow.Ellipsis) },
                    actions = actions,
                    scrollBehavior = behavior,
                )
                ConnectionBanner(link, host?.shortName ?: "the computer", model.isDemo)
            }
        },
        contentWindowInsets = androidx.compose.foundation.layout.WindowInsets(0),
    ) { padding -> content(padding) }
}

@Composable
fun ConnectionBanner(link: Link, name: String, demo: Boolean) {
    AnimatedVisibility(link != Link.Connected && !demo) {
        Row(
            Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerHigh).padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            if (link is Link.Failed) {
                Text("Can't reach $name: ${link.message}", style = MaterialTheme.typography.bodySmall, maxLines = 2)
            } else {
                CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp)
                Text("Connecting to $name…", style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}

/** A screen opened from a list: a title, and Back where it has somewhere to go. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DetailScaffold(
    title: String,
    onBack: (() -> Unit)?,
    subtitle: String? = null,
    actions: @Composable RowScope.() -> Unit = {},
    bottomBar: @Composable () -> Unit = {},
    large: Boolean = false,
    content: @Composable (PaddingValues) -> Unit,
) {
    // A tab's own screen (Devices, Settings) has a large title that shrinks as it scrolls, as the iPhone's.
    val behavior = if (large) TopAppBarDefaults.exitUntilCollapsedScrollBehavior() else null
    Scaffold(
        modifier = behavior?.let { Modifier.nestedScroll(it.nestedScrollConnection) } ?: Modifier,
        topBar = {
            if (large) LargeTopAppBar(title = { Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis) }, actions = actions, scrollBehavior = behavior)
            else TopAppBar(
                title = {
                    Column {
                        Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        if (!subtitle.isNullOrEmpty()) Text(subtitle, style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                },
                navigationIcon = { if (onBack != null) IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") } },
                actions = actions,
            )
        },
        bottomBar = bottomBar,
        contentWindowInsets = androidx.compose.foundation.layout.WindowInsets(0),
    ) { padding -> content(padding) }
}

/** The agents' usage limits, as chosen in Settings: a slim bar per window. */
fun LazyListScope.usageLimits(w: Workspace, now: Long) {
    item(key = "usage") {
        val prefs = Prefs.of(LocalContext.current)
        val shown = w.usage.filter { prefs.getBoolean("usage.${it.harness}", false) }
        if (shown.isEmpty()) return@item
        Column {
            SectionHeader("Usage Limits")
            GroupedCard { Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                for (a in shown) {
                    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        Text(AgentCatalog.shortName(a.harness, w.kinds), style = MaterialTheme.typography.labelLarge,
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                        for (win in a.windowsList) UsageRow(win, now)
                    }
                }
            }}
        }
    }
}

@Composable
private fun UsageRow(w: hammock.v1.Hammock.UsageWindow, now: Long) {
    val over = w.resetsUnix > 0 && w.resetsUnix * 1000 <= now
    val used = if (over) 0.0 else w.usedPercent.coerceIn(0.0, 100.0)
    val fill = when {
        used >= 90 -> ErrorColor
        used >= 75 -> NeedsYouColor
        else -> MaterialTheme.colorScheme.primary
    }
    val style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace)
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(w.label, Modifier.width(30.dp), style = style, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Box(Modifier.weight(1f).height(4.dp).clip(RoundedCornerShape(2.dp)).background(MaterialTheme.colorScheme.onSurface.copy(alpha = 0.1f))) {
            Box(Modifier.fillMaxWidth((used / 100).toFloat()).fillMaxHeight().clip(RoundedCornerShape(2.dp)).background(fill))
        }
        Text("${used.toInt()}%", Modifier.width(40.dp), style = style, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(if (over || w.resetsUnix <= 0) "—" else until(w.resetsUnix * 1000, now), Modifier.width(56.dp), style = style,
            color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** "3h30m", "45m", "1d5h", "2d". */
fun until(ms: Long, now: Long): String {
    val m = maxOf(((ms - now) / 60000).toInt(), 0)
    val d = m / 1440
    val h = (m % 1440) / 60
    val mm = m % 60
    if (d >= 2) return "${Math.round(m / 1440.0)}d"
    if (d == 1) return if (h > 0) "1d${h}h" else "1d"
    if (h > 0) return if (mm > 0) "${h}h${mm}m" else "${h}h"
    return "${mm}m"
}
