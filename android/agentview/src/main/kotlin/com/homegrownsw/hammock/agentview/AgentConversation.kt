package com.homegrownsw.hammock.agentview

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ArrowDownward
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import hammock.v1.Hammock.AgentBlock
import hammock.v1.Hammock.AgentPrompt
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/**
 * A pane's conversation with its agent: the rows, the prompt it's showing
 * (to answer here), what it has running (subagents and background shells, to
 * watch), its task list and links, and the composer.
 *
 * [canDrive]: false for a Group shared with you to watch (no answering or
 * messaging). [onOpenTerminal] flips the pane back to its terminal (offered
 * when an answer can't be matched to the screen). [agentAccent] is the app
 * theme's accent; everything else comes from MaterialTheme. [fonts] is how
 * it reads (Settings ▸ Text). [onOpenFile] opens a path the session
 * referenced; without it, tapping one copies it.
 *
 * Watches the session while shown (start/stop).
 */
@Composable
fun AgentConversation(
    session: AgentSession,
    canDrive: Boolean,
    onOpenTerminal: () -> Unit,
    modifier: Modifier = Modifier,
    agentAccent: Color = MaterialTheme.colorScheme.primary,
    /** Your messages' colour (the theme's bubble); the accent when not given. */
    bubble: Color = agentAccent,
    imageSize: AttachmentSize = AttachmentSize.MEDIUM,
    fonts: AgentFonts = AgentFonts(),
    onOpenFile: ((String) -> Unit)? = null,
) {
    val base = MaterialTheme.typography
    val typography = remember(base, fonts) { base.forAgent(fonts) }
    MaterialTheme(colorScheme = MaterialTheme.colorScheme, shapes = MaterialTheme.shapes, typography = typography) {
        CompositionLocalProvider(LocalAgentFonts provides fonts) {
            Conversation(session, canDrive, onOpenTerminal, modifier, agentAccent, bubble, imageSize, onOpenFile)
        }
    }
}

@Composable
private fun Conversation(
    session: AgentSession,
    canDrive: Boolean,
    onOpenTerminal: () -> Unit,
    modifier: Modifier,
    agentAccent: Color,
    bubble: Color,
    imageSize: AttachmentSize,
    onOpenFile: ((String) -> Unit)?,
) {
    val colors = rememberAgentColors(agentAccent, bubble)
    DisposableEffect(session) {
        session.start()
        onDispose { session.stop() }
    }
    val state by session.state.collectAsStateWithLifecycle()

    // A subagent or background shell being looked at instead (read only).
    var viewing by remember(session) { mutableStateOf<SideView?>(null) }
    BackHandler(enabled = viewing != null) { viewing = null }
    val side = remember(viewing) {
        when (val v = viewing) {
            is SideView.Subagent -> AgentSession(session.model, session.paneID, subagentID = v.id, lingerMs = 0)
            is SideView.Shell -> AgentSession(session.model, session.paneID, shellID = v.id, lingerMs = 0)
            null -> null
        }
    }
    DisposableEffect(side) {
        side?.start()
        onDispose { side?.stop() }
    }

    var viewed by remember { mutableStateOf<ImageBitmap?>(null) }
    var asking by remember { mutableStateOf<String?>(null) }
    val system = LocalUriHandler.current
    val clipboard = LocalClipboardManager.current
    val openFile by rememberUpdatedState(onOpenFile)
    // Links in the conversation: web and mail links open; file links go to
    // onOpenFile; any other scheme (it could open any app) asks first.
    val links = remember(system) {
        object : UriHandler {
            override fun openUri(uri: String) {
                when (val o = LinkPolicy.opening(uri)) {
                    LinkPolicy.Opening.Web, LinkPolicy.Opening.Direct -> runCatching { system.openUri(uri) }.onFailure { session.model.showToast("Nothing on this phone opens that link") }
                    is LinkPolicy.Opening.File -> openFile?.invoke(o.path) ?: run {
                        clipboard.setText(AnnotatedString(o.path))
                        session.model.showToast("Copied the path")
                    }
                    LinkPolicy.Opening.Ask -> asking = uri
                }
            }
        }
    }

    CompositionLocalProvider(LocalAgentColors provides colors, LocalUriHandler provides links, LocalOpenImage provides { viewed = it }) {
        BoxWithConstraints(modifier.fillMaxSize()) {
            val height = maxHeight
            Column(Modifier.fillMaxSize().imePadding()) {
                val v = viewing
                when {
                    v is SideView.Subagent -> {
                        val sub = state.subagents.firstOrNull { it.id == v.id }
                        val sideState = side?.state?.collectAsStateWithLifecycle()?.value
                        val working = sideState?.working ?: (sub?.finished == false)
                        SideHeader(
                            sub?.let { it.description.ifEmpty { it.agentType } } ?: "Subagent", null,
                            if (working) "Subagent · working" else "Subagent · finished", working, false,
                        ) { viewing = null }
                    }
                    v is SideView.Shell -> {
                        val sh = state.shells.firstOrNull { it.id == v.id }
                        SideHeader(
                            sh?.let { CardText.title(it) } ?: "Shell",
                            sh?.takeIf { it.description.isNotEmpty() }?.command,
                            when { sh == null -> "Shell"; !sh.finished -> "Background shell · running"; else -> sh.summary.ifEmpty { "Background shell · ended" } },
                            sh?.finished == false, sh?.failed == true,
                        ) { viewing = null }
                    }
                    state.subagents.any { !it.finished } || state.shells.any { !it.finished } ->
                        BackgroundStrip(state.subagents.filter { !it.finished }, state.shells.filter { !it.finished }) { viewing = it }
                }
                val shown = side ?: session
                key(shown) {
                    val shownState = if (side == null) state else side.state.collectAsStateWithLifecycle().value
                    if (shown.shellID.isNotEmpty()) ShellOutputView(shownState, Modifier.weight(1f).fillMaxWidth())
                    else Conversation(shown, shownState, Modifier.weight(1f).fillMaxWidth())
                }
                if (side == null) {
                    Column(
                        Modifier.fillMaxWidth().padding(horizontal = 12.dp).padding(bottom = 8.dp, top = 4.dp)
                            .widthIn(max = 884.dp).align(Alignment.CenterHorizontally),
                        verticalArrangement = Arrangement.spacedBy(10.dp),
                    ) {
                        SessionExtras(state)
                        when {
                            !canDrive -> Surface(shape = CircleShape, tonalElevation = 3.dp, modifier = Modifier.align(Alignment.CenterHorizontally)) {
                                Row(Modifier.padding(horizontal = 16.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                    Icon(Icons.Outlined.Visibility, null, tint = colors.secondary, modifier = Modifier.size(16.dp))
                                    Text("View only: its host can let you drive", style = MaterialTheme.typography.labelLarge, color = colors.secondary)
                                }
                            }
                            state.prompt.kind != AgentPrompt.Kind.KIND_NONE && state.prompt.kind != AgentPrompt.Kind.UNRECOGNIZED ->
                                PromptCard(session, state, maxHeight = maxOf(300.dp, height * 0.7f), onOpenTerminal = onOpenTerminal)
                            else -> Composer(session, state, imageSize)
                        }
                    }
                }
            }
        }
        viewed?.let { ImageViewer(it) { viewed = null } }
        asking?.let { url ->
            AlertDialog(
                onDismissRequest = { asking = null },
                title = { Text("Open this link?") },
                text = { Text(url) },
                confirmButton = { TextButton(onClick = { asking = null; runCatching { system.openUri(url) } }) { Text("Open") } },
                dismissButton = { TextButton(onClick = { asking = null }) { Text("Cancel") } },
            )
        }
    }
}

/**
 * The rows, newest at the bottom. The list is laid out from the bottom
 * (reverseLayout), with what follows the transcript (messages on their way,
 * Working…) as its first item, so:
 * - it opens at its end, with nothing to settle or jump to;
 * - at the end, rows arriving go in above that first item and the view stays
 *   anchored to it: the conversation follows without a scroll;
 * - a row changing height (a reply growing, a tool's result) moves what's above
 *   it, never what's at the bottom;
 * - earlier messages page in at the far end, so what you're reading stays put;
 * - scrolled up, the view stays anchored to the row you're on, by key.
 */
@Composable
private fun Conversation(session: AgentSession, state: AgentViewState, modifier: Modifier) {
    val c = LocalAgentColors.current
    val list = rememberLazyListState()
    val scope = rememberCoroutineScope()
    val items = state.items
    val n = items.size
    val density = LocalDensity.current
    val showAt = with(density) { 350.dp.toPx() }
    val hideAt = with(density) { 150.dp.toPx() }
    var latest by remember { mutableStateOf(false) }

    // Latest once well up from the end; gone again near it (a gap between the
    // two, so it doesn't flicker at the line).
    LaunchedEffect(list) {
        snapshotFlow { gapToEnd(list) }.distinctUntilChanged().collect { gap ->
            if (gap > showAt) latest = true else if (gap < hideAt) latest = false
        }
    }
    // Sent while scrolled up: down to see it go.
    var sent by remember { mutableStateOf(state.pendingSends.size) }
    LaunchedEffect(state.pendingSends.size) {
        if (state.pendingSends.size > sent && latest) list.scrollToItem(0)
        sent = state.pendingSends.size
    }
    // A reply arriving at the end taller than what shows: to its first line,
    // and no further (following it to its end left you at its last lines).
    // It stops following there; Latest is for the rest.
    val lastID = items.lastOrNull()?.id
    LaunchedEffect(lastID) {
        val last = items.lastOrNull() ?: return@LaunchedEffect
        val b = last.block ?: return@LaunchedEffect
        if (b.kind != AgentBlock.Kind.KIND_TEXT) return@LaunchedEffect
        val arrived = session.arrived[b.id] ?: return@LaunchedEffect
        if (System.currentTimeMillis() - arrived > 2000 || list.firstVisibleItemIndex > 1) return@LaunchedEffect
        val info = withTimeoutOrNull(500) {
            snapshotFlow { list.layoutInfo.visibleItemsInfo.firstOrNull { it.key == last.id } }.first { it != null }
        } ?: return@LaunchedEffect
        val layout = list.layoutInfo
        val viewport = layout.viewportSize.height - layout.beforeContentPadding - layout.afterContentPadding
        val over = info.size - viewport + with(density) { 24.dp.toPx() }.toInt()
        if (over > 0) list.animateScrollToItem(info.index, over)
    }

    // Dragging the conversation down puts the keyboard away, as on the iPhone
    // (there it follows the finger; here it goes once the drag does).
    val keyboard = androidx.compose.ui.platform.LocalSoftwareKeyboardController.current
    val focus = androidx.compose.ui.platform.LocalFocusManager.current
    val hideOnDrag = remember(keyboard, focus) {
        object : androidx.compose.ui.input.nestedscroll.NestedScrollConnection {
            override fun onPreScroll(available: androidx.compose.ui.geometry.Offset, source: androidx.compose.ui.input.nestedscroll.NestedScrollSource): androidx.compose.ui.geometry.Offset {
                if (source == androidx.compose.ui.input.nestedscroll.NestedScrollSource.UserInput && available.y > 4f) {
                    keyboard?.hide()
                    focus.clearFocus()
                }
                return androidx.compose.ui.geometry.Offset.Zero
            }
        }
    }

    Box(modifier) {
        LazyColumn(
            state = list,
            reverseLayout = true,
            modifier = Modifier.fillMaxSize().nestedScroll(hideOnDrag),
            contentPadding = PaddingValues(horizontal = 18.dp, vertical = 16.dp),
            // A short conversation starts at the top, under the bar, as on the iPhone.
            verticalArrangement = Arrangement.spacedBy(14.dp, Alignment.Top),
        ) {
            item(key = "tail", contentType = "tail") { Centered { Tail(session, state) } }
            items(n, key = { items[n - 1 - it].id }, contentType = { items[n - 1 - it].kind }) { i ->
                Centered(Modifier.animateItem(fadeInSpec = tween(220), placementSpec = null, fadeOutSpec = null)) {
                    AgentRow(items[n - 1 - i], session)
                }
            }
            if (state.hasMore) {
                // On tap only: loading as it appears could chain through the
                // whole history whenever the list is laid out from the top.
                item(key = "earlier", contentType = "earlier") {
                    Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                        TextButton(onClick = session::loadEarlier, enabled = !state.loadingOlder) {
                            Text(if (state.loadingOlder) "Loading…" else "Show earlier messages")
                        }
                    }
                }
            }
            if (state.blocks.isEmpty() && state.error.isNotEmpty()) {
                item(key = "empty", contentType = "empty") { Empty("No conversation yet", state.error) }
            } else if (state.blocks.isEmpty() && !state.heard) {
                item(key = "loading", contentType = "empty") {
                    Box(Modifier.fillMaxWidth().padding(top = 60.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator(Modifier.size(24.dp)) }
                }
            }
        }
        AnimatedVisibility(
            latest, enter = fadeIn() + scaleIn(initialScale = 0.6f), exit = fadeOut() + scaleOut(targetScale = 0.6f),
            modifier = Modifier.align(Alignment.BottomCenter).padding(bottom = 10.dp),
        ) {
            ExtendedFloatingActionButton(
                onClick = { scope.launch { list.animateScrollToItem(0) } },
                icon = { Icon(Icons.Outlined.ArrowDownward, null) },
                text = { Text("Latest") },
                containerColor = MaterialTheme.colorScheme.surfaceContainerHigh,
                contentColor = c.accent,
            )
        }
    }
}

/** Roughly how far the view is from the end of the conversation, in pixels. */
private fun gapToEnd(list: LazyListState): Float = when (list.firstVisibleItemIndex) {
    0 -> list.firstVisibleItemScrollOffset.toFloat()
    // The tail (often empty) is below: about what's scrolled into the row past it.
    1 -> list.firstVisibleItemScrollOffset.toFloat() + 60f
    else -> Float.MAX_VALUE
}

/** A row at the conversation's reading width, centred on a wide screen. */
@Composable
private fun Centered(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Box(modifier.fillMaxWidth(), contentAlignment = Alignment.TopCenter) {
        Box(Modifier.widthIn(max = 860.dp).fillMaxWidth()) { content() }
    }
}

/** What follows the transcript: messages on their way, and what the agent's doing. */
@Composable
private fun Tail(session: AgentSession, state: AgentViewState) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(14.dp)) {
        // A "!" command runs at once, and the transcript only has it when it's
        // done: shown running, as it then will be, with what it's printed so far
        // (the first one waiting is the one running).
        val running = state.pendingSends.firstOrNull { it.isBang }?.id
        for (p in state.pendingSends) {
            key(p.id) {
                val run = p.bangRun(if (p.id == running) state.bangOutput else "")
                if (run != null) ToolRow(run, session) else UserBubble(p.text, "", emptyList(), p.attachments, session, pending = true)
            }
        }
        AnimatedVisibility(state.working, enter = fadeIn(tween(250)), exit = fadeOut(tween(0))) { WorkingRow(state.status) }
    }
}
