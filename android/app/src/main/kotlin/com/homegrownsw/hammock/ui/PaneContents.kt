package com.homegrownsw.hammock.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Button
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.unit.dp
import com.homegrownsw.hammock.client.Workspace
import com.homegrownsw.hammock.client.canRunAgain
import com.homegrownsw.hammock.client.exitedStatusLabel
import com.homegrownsw.hammock.client.runAgainTitle
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.toArgb
import com.homegrownsw.hammock.media.BrowserPane
import com.homegrownsw.hammock.media.SimulatorPane
import com.homegrownsw.hammock.panes.DiffPane
import com.homegrownsw.hammock.panes.DiffSettings
import com.homegrownsw.hammock.panes.EditorPane
import com.homegrownsw.hammock.panes.EditorSettings
import com.homegrownsw.hammock.panes.LogPane
import com.homegrownsw.hammock.panes.PaneTheme
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.terminal.TerminalFonts
import com.homegrownsw.hammock.terminal.TerminalPane
import com.homegrownsw.hammock.terminal.TerminalSession
import com.homegrownsw.hammock.terminal.TerminalTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.runtime.remember
import com.homegrownsw.hammock.Prefs
import com.homegrownsw.hammock.agentview.AgentConversation
import com.homegrownsw.hammock.agentview.AgentFonts
import com.homegrownsw.hammock.agentview.AgentSession
import com.homegrownsw.hammock.agentview.AttachmentSize
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Link
import com.homegrownsw.hammock.rememberPref
import hammock.v1.Hammock.Pane

// What each kind of pane shows. Filled in as each view lands.

@Composable
fun AgentContent(model: AppModel, pane: Pane, drive: Boolean, active: Boolean = true, openTerminal: () -> Unit) {
    val session = remember(model, pane.id) { AgentSession.of(model, pane.id) }
    val size by rememberPref(Prefs.ATTACHMENT_SIZE, "medium")
    val theme = LocalHammockTheme.current
    val dark = MaterialTheme.colorScheme.surface.luminance() < 0.5f
    AgentConversation(
        session, canDrive = drive, onOpenTerminal = openTerminal, bubble = theme.bubble(dark),
        imageSize = when (size) {
            "small" -> AttachmentSize.SMALL; "large" -> AttachmentSize.LARGE; "original" -> AttachmentSize.ORIGINAL; else -> AttachmentSize.MEDIUM
        },
        fonts = rememberAgentFonts(),
        active = active,
    )
}

/** Settings ▸ Text: how agent conversations read. */
@Composable
fun rememberAgentFonts(): AgentFonts {
    val family by rememberPref(Prefs.TEXT_FONT, "")
    val size by rememberPref(Prefs.TEXT_SIZE, DefaultSizes.TEXT.toString())
    val spacing by rememberPref(Prefs.LINE_SPACING, DefaultSizes.LINE_SPACING.toString())
    val code by rememberPref(Prefs.CODE_SIZE, DefaultSizes.CODE.toString())
    val wrap by rememberPref(Prefs.CODE_WRAP, "prose")
    return remember(family, size, spacing, code, wrap) {
        AgentFonts(
            family = family,
            size = size.toFloatOrNull() ?: DefaultSizes.TEXT.toFloat(),
            codeSize = code.toFloatOrNull() ?: DefaultSizes.CODE.toFloat(),
            lineSpacing = spacing.toFloatOrNull() ?: DefaultSizes.LINE_SPACING.toFloat(),
            codeWrap = wrap,
        )
    }
}

/** Settings' sizes until they're changed (the text's are AgentFonts'; the terminal's in sp). */
object DefaultSizes {
    const val TEXT = AgentFonts.DEFAULT_SIZE.toInt()
    const val CODE = TEXT - 1
    const val LINE_SPACING = AgentFonts.DEFAULT_SPACING.toInt()
    const val TERMINAL = 12
}

/** Settings ▸ Terminal: the font family ("" the bundled one), size in sp, and extra row height as a fraction. */
data class TerminalFont(val family: String, val size: Float, val lineSpacing: Float)

@Composable
fun rememberTerminalFont(): TerminalFont {
    val family by rememberPref(Prefs.TERMINAL_FONT, "")
    val size by rememberPref(Prefs.TERMINAL_FONT_SIZE, DefaultSizes.TERMINAL.toString())
    val spacing by rememberPref(Prefs.TERMINAL_LINE_SPACING, "0")
    return remember(family, size, spacing) {
        TerminalFont(family, size.toFloatOrNull() ?: DefaultSizes.TERMINAL.toFloat(), (spacing.toFloatOrNull() ?: 0f) / 100f)
    }
}

/** Settings ▸ Editors and Diffs' font: its own, else the terminal's, as a family the pages know. */
@Composable
fun rememberEditorFontFamily(): String {
    val editor by rememberPref(Prefs.EDITOR_FONT, "")
    val terminal by rememberPref(Prefs.TERMINAL_FONT, "")
    return editor.ifEmpty { terminal }.ifEmpty { TerminalFonts.BUNDLED }
}

/**
 * A terminal pane: the computer's screen, kept by libghostty-vt here and
 * drawn with GLES. Opening it makes this phone the pane's driver, as on the
 * iPhone: the pane takes the grid that fits the screen at the font size
 * chosen in Settings.
 */
@Composable
fun TerminalContent(model: AppModel, pane: Pane, drive: Boolean, onSession: (TerminalSession?) -> Unit = {}) {
    val session = remember(model, pane.id) { TerminalSession(pane.id, pane.cols.coerceAtLeast(1), pane.rows.coerceAtLeast(1)) }
    DisposableEffect(session) {
        model.attach(pane.id, session)
        onSession(session)
        onDispose {
            onSession(null)
            model.detach(pane.id, session)
            session.close()
        }
    }
    val theme = rememberTerminalTheme(model)
    LaunchedEffect(theme) { model.setColors(theme.toSetColors(isSystemInDarkThemeOr(theme))) }
    val font = rememberTerminalFont()
    var fontSize by rememberPref(Prefs.TERMINAL_FONT_SIZE, DefaultSizes.TERMINAL.toString())
    val link by model.link.collectAsStateWithLifecycle()
    val w by model.workspace.collectAsStateWithLifecycle()
    // Its program ended: the bar saying so takes the key bar's place (no keyboard: nothing to type to).
    val ended = drive && pane.canRunAgain
    Column {
        TerminalPane(
            session,
            modifier = Modifier.weight(1f),
            onInput = { model.input(pane.id, it) },
            onResize = { cols, rows, w, h -> if (drive) model.resize(pane.id, cols, rows, w, h) },
            readOnly = !drive || ended,
            theme = theme,
            fontSize = font.size.sp,
            fontFamily = font.family,
            lineSpacing = font.lineSpacing,
            onFocus = { model.focus(pane.id) },
            linkUp = link == Link.Connected,
            offline = linkDown(),
            onReconnect = { model.reconnectNow() },
            onFontSize = { fontSize = it.toInt().toString() },
        )
        AnimatedVisibility(ended, enter = expandVertically() + fadeIn(), exit = shrinkVertically() + fadeOut()) {
            ExitedPaneBar(model, w, pane, connected = link == Link.Connected)
        }
    }
}

/**
 * Along the bottom of a terminal whose program ended (canRunAgain), where the key bar is while it
 * runs, its last output still above it: "Exited 1 · Run Again · Close", as on the Mac and the iPhone.
 * An agent's pane says Resume, and carries on its session.
 */
@Composable
private fun ExitedPaneBar(model: AppModel, w: Workspace, pane: Pane, connected: Boolean) {
    val view = LocalView.current
    val closer = LocalCloser.current
    Surface(color = MaterialTheme.colorScheme.surfaceContainer, modifier = Modifier.fillMaxWidth()) {
        Row(
            Modifier.padding(horizontal = 12.dp, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                pane.exitedStatusLabel, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1,
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.weight(1f).semantics { if (pane.startError.isNotEmpty()) stateDescription = pane.startError },
            )
            Button(onClick = { view.hapticConfirm(); model.restartPane(pane.id) }, enabled = connected) {
                Text(pane.runAgainTitle(w.kinds))
            }
            OutlinedButton(
                onClick = { closer.closePane(model, w, pane) }, enabled = connected,
                modifier = Modifier.semantics { contentDescription = "Close Pane" },
            ) { Text("Close") }
        }
    }
}

/** The terminal's colours: the computer's theme files when it sends them, else Hammock's own, light or dark as the app is. */
@Composable
fun rememberTerminalTheme(model: AppModel): TerminalTheme {
    val w by model.workspace.collectAsStateWithLifecycle()
    val dark = MaterialTheme.colorScheme.surface.luminance() < 0.5f
    val host = w.hostTheme
    return remember(host, dark) {
        val base = if (dark) TerminalTheme.Dark else TerminalTheme.Light
        val file = if (dark) host?.darkTerminalFile else host?.lightTerminalFile
        if (!file.isNullOrBlank()) runCatching { TerminalTheme.parseGhostty(file, base) }.getOrDefault(base) else base
    }
}

private fun isSystemInDarkThemeOr(t: TerminalTheme) = ((t.background shr 16 and 0xFF) + (t.background shr 8 and 0xFF) + (t.background and 0xFF)) < 384

/** For the daemon, so programs asking the terminal's colours (OSC 10/11) get the truth. */
fun TerminalTheme.toSetColors(dark: Boolean): hammock.v1.Hammock.SetColors = hammock.v1.Hammock.SetColors.newBuilder()
    .setForeground(foreground and 0xFFFFFF).setBackground(background and 0xFFFFFF).setCursor(cursor and 0xFFFFFF)
    .addAllPalette(ansi.map { it and 0xFFFFFF }).setDark(dark).build()

@Composable
fun LogContent(model: AppModel, pane: Pane) = LogPane(model, pane, rememberPaneTheme(model))

@Composable
fun EditorContent(model: AppModel, pane: Pane, drive: Boolean, paneActions: @Composable (close: () -> Unit) -> Unit) {
    val font = rememberTerminalFont()
    EditorPane(
        model, pane, drive, rememberPaneTheme(model), settings = EditorSettings(fontFamily = rememberEditorFontFamily(), fontSize = font.size + 1.0),
        extraMenu = { close -> HorizontalDivider(); paneActions(close) },
    )
}

@Composable
fun DiffContent(model: AppModel, pane: Pane, drive: Boolean) {
    val font = rememberTerminalFont()
    DiffPane(model, pane, drive, rememberPaneTheme(model), settings = DiffSettings(fontFamily = rememberEditorFontFamily(), fontSize = font.size.toDouble()))
}

/** Editor, diff and log panes in the terminal's colours, with the app's accent. */
@Composable
fun rememberPaneTheme(model: AppModel): PaneTheme {
    val t = rememberTerminalTheme(model)
    val accent = MaterialTheme.colorScheme.primary.toArgb()
    return remember(t, accent) { PaneTheme(t.background, t.foreground, t.cursor, t.selection, t.ansi, isSystemInDarkThemeOr(t), accent) }
}

@Composable
fun BrowserContent(model: AppModel, pane: Pane, drive: Boolean) = BrowserPane(model, pane, drive)

@Composable
fun SimulatorContent(model: AppModel, pane: Pane, drive: Boolean) = SimulatorPane(model, pane, drive)
