package com.homegrownsw.hammock.ui

import androidx.compose.runtime.Composable
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock.Pane

// What each kind of pane shows. Filled in as each view lands.

@Composable
fun AgentContent(model: AppModel, pane: Pane, drive: Boolean, openTerminal: () -> Unit) = Placeholder("Agent view")

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
