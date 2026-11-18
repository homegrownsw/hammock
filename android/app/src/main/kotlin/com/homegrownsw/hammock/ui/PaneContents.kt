package com.homegrownsw.hammock.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import com.homegrownsw.hammock.Prefs
import com.homegrownsw.hammock.agentview.AgentConversation
import com.homegrownsw.hammock.agentview.AgentSession
import com.homegrownsw.hammock.agentview.AttachmentSize
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.rememberPref
import hammock.v1.Hammock.Pane

// What each kind of pane shows. Filled in as each view lands.

@Composable
fun AgentContent(model: AppModel, pane: Pane, drive: Boolean, openTerminal: () -> Unit) {
    val session = remember(model, pane.id) { AgentSession(model, pane.id) }
    val size by rememberPref(Prefs.ATTACHMENT_SIZE, "medium")
    AgentConversation(
        session, canDrive = drive, onOpenTerminal = openTerminal,
        imageSize = when (size) {
            "small" -> AttachmentSize.SMALL; "large" -> AttachmentSize.LARGE; "original" -> AttachmentSize.ORIGINAL; else -> AttachmentSize.MEDIUM
        },
    )
}

@Composable
fun TerminalContent(model: AppModel, pane: Pane, drive: Boolean) = Placeholder("Terminal")

@Composable
fun LogContent(model: AppModel, pane: Pane) = Placeholder("Log")

@Composable
fun EditorContent(model: AppModel, pane: Pane, drive: Boolean) = Placeholder("Editor")

@Composable
fun DiffContent(model: AppModel, pane: Pane, drive: Boolean) = Placeholder("Diff")

@Composable
fun BrowserContent(model: AppModel, pane: Pane, drive: Boolean) = Placeholder("Browser")

@Composable
fun SimulatorContent(model: AppModel, pane: Pane, drive: Boolean) = Placeholder("Simulator")
