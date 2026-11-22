package com.homegrownsw.hammock.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.material.icons.filled.CloudOff
import androidx.compose.material3.TextButton
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.rememberCoroutineScope
import kotlinx.coroutines.launch
import androidx.compose.ui.text.font.FontWeight
import com.homegrownsw.hammock.client.LinkHistory
import com.homegrownsw.hammock.client.LinkStatus
import kotlinx.coroutines.delay
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
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
 * connection as a banner while it isn't connected (ConnectionBanner), and their list.
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
                ConnectionBanner()
            }
        },
        contentWindowInsets = androidx.compose.foundation.layout.WindowInsets(0),
    ) { padding -> content(padding) }
}

/** What the connection banner shows, worked out once ([LinkWatcher]) for every screen under it. */
class LinkBar(val model: AppModel, val host: String, val status: LinkStatus?, val history: LinkHistory, val now: Long)

val LocalLinkBar = compositionLocalOf<LinkBar?> { null }

/** Whether this screen shows the banner: not what's open beside a list on a wide screen (the list has it). */
val LocalShowsLinkBanner = compositionLocalOf { true }

/** The link's been down a moment (the banner shows): what's on screen may be out of date, and answering from it can't work. */
@Composable
fun linkDown(): Boolean = LocalLinkBar.current?.status != null

/** Live parts of a row (its state, how long ago) dimmed while the link's down: they may be out of date. */
@Composable
fun Modifier.staleWhenLinkDown(): Modifier {
    val alpha by animateFloatAsState(if (linkDown()) 0.45f else 1f, label = "stale")
    return this.alpha(alpha)
}

/**
 * Works out the link's status ([LinkStatus]) for the screens under it, on a clock while the link's
 * down, and says it to TalkBack as it changes. Once, round the workspace.
 */
@Composable
fun LinkWatcher(model: AppModel, host: String, content: @Composable () -> Unit) {
    val link by model.link.collectAsStateWithLifecycle()
    val history by model.history.collectAsStateWithLifecycle()
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(history.downSinceMs) {
        now = System.currentTimeMillis()
        if (history.downSinceMs == null) return@LaunchedEffect
        while (true) {
            delay(1000)
            now = System.currentTimeMillis()
        }
    }
    val status = LinkStatus.shown(link == Link.Connected, history, now)
    val view = LocalView.current
    var said by remember { mutableStateOf<LinkStatus?>(null) }
    LaunchedEffect(status) {
        LinkStatus.announcement(said, status, host)?.let { view.announceForAccessibility(it) }
        said = status
    }
    CompositionLocalProvider(LocalLinkBar provides LinkBar(model, host, status, history, now)) { content() }
}

/**
 * The link to the computer while it isn't connected, under a screen's top bar: connecting,
 * reconnecting, or can't reach it (why, and how old what's shown is), with Try Again. Nothing
 * while connected, or for a blip.
 */
@Composable
fun ConnectionBanner() {
    val bar = LocalLinkBar.current ?: return
    if (!LocalShowsLinkBanner.current) return
    // Kept while it slides away.
    var last by remember { mutableStateOf(bar.status) }
    if (bar.status != null) last = bar.status
    AnimatedVisibility(bar.status != null, enter = expandVertically() + fadeIn(), exit = shrinkVertically() + fadeOut()) {
        val s = last ?: return@AnimatedVisibility
        Row(
            Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerHigh).padding(start = 16.dp, end = 8.dp, top = 8.dp, bottom = 8.dp),
            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Box(Modifier.size(20.dp), contentAlignment = Alignment.Center) {
                if (s == LinkStatus.Unreachable) Icon(Icons.Default.CloudOff, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.primary)
                else CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            }
            Column(Modifier.weight(1f).semantics(mergeDescendants = true) {}) {
                Text(s.title(bar.host), style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold, maxLines = 1,
                    overflow = TextOverflow.Ellipsis)
                s.detail(bar.history.reason, bar.history.lastLiveMs, bar.now)?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 2)
                }
            }
            if (s == LinkStatus.Unreachable) TextButton(onClick = { bar.model.reconnectNow() }) { Text("Try Again") }
        }
    }
}

/** The screen being shared, and whether its bar goes under screens' top bars (the navigation bar's hidden) rather than above the navigation bar. */
class ScreenBar(val share: com.homegrownsw.hammock.media.ScreenShare, val atTop: Boolean)

val LocalScreenBar = compositionLocalOf<ScreenBar?> { null }

/** The minimised screen under this screen's top bar, while the navigation bar's hidden. */
@Composable
fun ScreenShareTopBar() {
    val b = LocalScreenBar.current ?: return
    if (b.atTop) com.homegrownsw.hammock.media.ScreenShareBar(b.share, top = true)
}

/** A list that pulls to refresh: dials the computer now if it's down, and checks the link if it's up. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun RefreshBox(model: AppModel, modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    var refreshing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    PullToRefreshBox(refreshing, onRefresh = {
        refreshing = true
        scope.launch { model.refresh(); refreshing = false }
    }, modifier.fillMaxSize()) { content() }
}

/** The first load, before the computer's state is here: a spinner, not an empty list (the banner says from where, once it's slow). */
@Composable
fun LoadingState(modifier: Modifier = Modifier) {
    Box(modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator(Modifier.semantics { contentDescription = "Loading" })
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
        topBar = { Column {
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
            // The link to the computer, on every screen, panes included.
            ConnectionBanner()
            // A computer's screen minimised, where the navigation bar's hidden (a pane on a phone).
            ScreenShareTopBar()
        } },
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
