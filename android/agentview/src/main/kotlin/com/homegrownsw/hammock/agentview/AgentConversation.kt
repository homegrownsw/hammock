package com.homegrownsw.hammock.agentview

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.togetherWith
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
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
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
import androidx.compose.runtime.collectAsState
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
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.client.AppCover
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
    /** The pane on screen (not a neighbouring page): its prompt card may take a hardware keyboard's keys by itself. */
    active: Boolean = true,
) {
    val base = MaterialTheme.typography
    val typography = remember(base, fonts) { base.forAgent(fonts) }
    MaterialTheme(colorScheme = MaterialTheme.colorScheme, shapes = MaterialTheme.shapes, typography = typography) {
        CompositionLocalProvider(LocalAgentFonts provides fonts) {
            Conversation(session, canDrive, onOpenTerminal, modifier, agentAccent, bubble, imageSize, onOpenFile, active)
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
    active: Boolean,
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
                    // The rows are drawn by the page the Mac and the iPhone draw them with (WebConversation).
                    if (shown.shellID.isNotEmpty()) ShellOutputView(shownState, Modifier.weight(1f).fillMaxWidth())
                    else WebConversation(shown, shownState, Modifier.weight(1f).fillMaxWidth())
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
                                PromptCard(session, state, maxHeight = maxOf(300.dp, height * 0.7f), onOpenTerminal = onOpenTerminal, active = active)
                            else -> Composer(session, state, imageSize, active)
                        }
                    }
                }
            }
        }
        viewed?.let { ImageViewer(it) { viewed = null } }
        val locked by AppCover.locked.collectAsState()
        if (!locked) asking?.let { url ->
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
