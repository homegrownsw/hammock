package com.homegrownsw.hammock.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Computer
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material.icons.filled.LaptopMac
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.Session
import com.homegrownsw.hammock.StoredHost
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Link
import com.homegrownsw.hammock.media.ScreenCards
import com.homegrownsw.hammock.media.ScreenViewer

fun computerIcon(kind: String): ImageVector = when (kind) {
    "server", "linux", "unraid" -> Icons.Default.Dns
    "mac", "macbook", "laptop" -> Icons.Default.LaptopMac
    else -> Icons.Default.Computer
}

/** Your paired computers: which is shown, switching, forgetting, adding; and this phone's name. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun DevicesScreen(session: Session, model: AppModel) {
    val hosts by session.hosts.collectAsStateWithLifecycle()
    val host by session.host.collectAsStateWithLifecycle()
    val link by model.link.collectAsStateWithLifecycle()
    val w by model.workspace.collectAsStateWithLifecycle()
    var adding by remember { mutableStateOf(false) }
    var forgetting by remember { mutableStateOf<StoredHost?>(null) }
    var name by remember { mutableStateOf(session.deviceName) }
    var screen by remember { mutableStateOf<Pair<String, String>?>(null) }

    // The computer shown says what kind it is: kept for its icon.
    w.computers.firstOrNull { it.thisComputer }?.kind?.let { if (it.isNotEmpty()) session.noteKind(it) }

    screen?.let { (id, title) ->
        ScreenViewer(model, id, title) { screen = null }
        return
    }

    if (adding) {
        BackHandler { adding = false }
        DetailScaffold("Add Computer", onBack = { adding = false }) { padding ->
            Box(Modifier.padding(padding)) { PairingScreen(session, firstRun = false) { adding = false } }
        }
        return
    }

    DetailScaffold("Devices", onBack = null) { padding ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = padding) {
            item { SectionTitle("Computers") }
            if (session.isDemo) item {
                ListItem(headlineContent = { Text("Demo") }, supportingContent = { Text("A made-up Mac: nothing is paired or sent.") },
                    trailingContent = { TextButton(onClick = session::endDemo) { Text("Pair Your Mac") } })
            }
            items(hosts, key = { it.id }) { h ->
                val current = h.id == host?.id
                var menu by remember { mutableStateOf(false) }
                Box {
                    ListItem(
                        modifier = Modifier.combinedClickable(onClick = { session.show(h) }, onLongClick = { menu = true }),
                        leadingContent = {
                            Icon(computerIcon(h.kind), null, tint = if (current) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant)
                        },
                        headlineContent = { Text(h.shortName) },
                        supportingContent = {
                            Text(
                                if (!current) h.address else when (val l = link) {
                                    Link.Connected -> "Connected"
                                    is Link.Failed -> l.message
                                    Link.Connecting -> "Connecting…"
                                },
                            )
                        },
                        trailingContent = { if (current) Icon(Icons.Default.Check, "Shown", tint = MaterialTheme.colorScheme.primary) },
                    )
                    DropdownMenu(menu, { menu = false }) {
                        DropdownMenuItem(text = { Text("Forget This Computer", color = ErrorColor) }, onClick = { menu = false; forgetting = h })
                    }
                }
            }
            item {
                ListItem(
                    modifier = Modifier.combinedClickable(onClick = { adding = true }),
                    leadingContent = { Icon(Icons.Default.Add, null, tint = MaterialTheme.colorScheme.primary) },
                    headlineContent = { Text("Add Computer", color = MaterialTheme.colorScheme.primary) },
                )
                Footer("The phone shows one computer at a time. Tap one to switch; touch and hold to forget it.")
            }
            if (!session.isDemo && link == Link.Connected) item {
                ScreenCards(model) { id, title -> screen = id to title }
            }
            item {
                SectionTitle("This Phone")
                OutlinedTextField(
                    name, { name = it }, label = { Text("Name") }, singleLine = true,
                    modifier = Modifier.padding(horizontal = 20.dp).onFocusChanged { f ->
                        if (!f.isFocused && name.trim() != session.deviceName) {
                            session.deviceName = name
                            session.renamed()
                        }
                    },
                )
                Footer("What your computers call this phone.")
            }
        }
    }
    forgetting?.let { h ->
        AlertDialog(
            onDismissRequest = { forgetting = null },
            title = { Text("Forget ${h.shortName}?") },
            text = { Text("This phone stops connecting to it and getting its notifications. You can pair again with a new code.") },
            confirmButton = { TextButton(onClick = { session.forget(h); forgetting = null }) { Text("Forget", color = ErrorColor) } },
            dismissButton = { TextButton(onClick = { forgetting = null }) { Text("Cancel") } },
        )
    }
}

@Composable
fun SectionTitle(title: String) = Text(
    title, Modifier.padding(start = 20.dp, end = 20.dp, top = 20.dp, bottom = 6.dp),
    style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary,
)

@Composable
fun Footer(text: String) = Text(
    text, Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
)
