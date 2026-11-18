package com.homegrownsw.hammock.panes

import android.content.res.Configuration
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.isRemote
import hammock.v1.Hammock
import kotlinx.coroutines.launch

/**
 * A diff pane: the same page as the Mac's and the iPhone's (diff.html),
 * narrow: one column, the files' tree a sheet over it (its button in the
 * page's bar). Comments, replies and sending them to the agent work as they
 * do on the Mac; a file's open button opens it in an editor pane beside.
 * There's no menu of its own: Refresh and the source are in the page.
 */
@Composable
fun DiffPane(
    model: AppModel,
    pane: Hammock.Pane,
    canDrive: Boolean,
    theme: PaneTheme,
    modifier: Modifier = Modifier,
    settings: DiffSettings = DiffSettings(),
) {
    val scope = rememberCoroutineScope()
    val session = remember(model, pane.id) { DiffSession(model, pane.id) }
    DisposableEffect(session) {
        session.start()
        onDispose { session.stop() }
    }
    val ws by model.workspace.collectAsState()
    val info = (ws.panes[pane.id] ?: pane).diff
    val error by session.error.collectAsState()

    // A keyboard connected: the page's shortcuts work, so its button shows.
    val conf = LocalConfiguration.current
    val keyboard = conf.keyboard == Configuration.KEYBOARD_QWERTY && conf.hardKeyboardHidden == Configuration.HARDKEYBOARDHIDDEN_NO
    val (target, agents) = remember(ws, pane.id) { DiffSession.agents(ws, pane.id) }
    // A guest can't send comments to the host's agents.
    val canSend = ws.groupOfPane(pane.id)?.let { !it.isRemote || it.myRole == Hammock.Role.ROLE_OWNER } ?: true
    val config = DiffJson.config(theme, settings, info.view, canDrive, target, agents, canSend, keyboard)
    LaunchedEffect(session, config.toString()) { session.configure(config) }
    val infoJSON = DiffJson.info(info)
    LaunchedEffect(session, infoJSON.toString()) { session.setInfo(infoJSON) }

    SideEffect {
        session.onOpenFile = { path, line ->
            if (canDrive && info.repo.isNotEmpty()) {
                val full = if (path.startsWith("/")) path else info.repo.trimEnd('/') + "/" + path
                scope.launch {
                    try { openEditorBeside(model, pane.id, full, line) } catch (e: Exception) { session.error.value = e.message ?: "Couldn't open it" }
                }
            }
        }
    }

    Box(modifier.fillMaxSize().background(color(theme.background)).imePadding()) {
        DiffWebView(session, theme.background)
        if (error.isNotEmpty()) ErrorPill(error, Modifier.align(Alignment.BottomCenter)) { session.error.value = "" }
    }
}

@Composable
private fun DiffWebView(session: DiffSession, background: Int) {
    val context = LocalContext.current
    val bridge = remember(session) {
        lateinit var b: PageBridge
        b = PageBridge(context, "diff.html", background, { type, m -> session.onPageMessage(b, type, m) })
        b
    }
    DisposableEffect(bridge) {
        session.attachPage(bridge)
        onDispose {
            session.detachPage(bridge)
            bridge.destroy()
        }
    }
    val problem = bridge.problem
    if (problem != null) {
        Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
            Text(problem, color = MaterialTheme.colorScheme.error)
        }
        return
    }
    AndroidView(factory = { bridge.webView }, modifier = Modifier.fillMaxSize(), update = { it.setBackgroundColor(background) })
}
