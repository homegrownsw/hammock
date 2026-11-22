package com.homegrownsw.hammock.ui

import androidx.activity.compose.BackHandler
import com.homegrownsw.hammock.LocalAppLocked
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.filled.Delete
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
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
import com.homegrownsw.hammock.media.ScreenShare

/** Your paired computers: which is shown, switching, forgetting, adding; and this phone's name. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun DevicesScreen(session: Session, model: AppModel, share: ScreenShare) {
    val hosts by session.hosts.collectAsStateWithLifecycle()
    val host by session.host.collectAsStateWithLifecycle()
    val link by model.link.collectAsStateWithLifecycle()
    val w by model.workspace.collectAsStateWithLifecycle()
    var adding by remember { mutableStateOf(false) }
    var forgetting by remember { mutableStateOf<StoredHost?>(null) }
    var name by remember { mutableStateOf(session.deviceName) }

    // The computer shown says what kind it is: kept for its icon.
    w.computers.firstOrNull { it.thisComputer }?.kind?.let { if (it.isNotEmpty()) session.noteKind(it) }

    if (adding) {
        BackHandler { adding = false }
        DetailScaffold("Add Computer", onBack = { adding = false }) { padding ->
            Box(Modifier.padding(padding)) { PairingScreen(session, firstRun = false) { adding = false } }
        }
        return
    }

    DetailScaffold("Devices", onBack = null, large = true) { padding ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(top = padding.calculateTopPadding(), bottom = 24.dp)) {
            item { SectionTitle("Computers") }
            val n = hosts.size + 1 + if (session.isDemo) 1 else 0
            var i = 0
            if (session.isDemo) { val at = i++; item(key = "demo") {
                CardRow(at, n) {
                    ListItem(colors = cardListItemColors(), headlineContent = { Text("Demo") }, supportingContent = { Text("A made-up Mac: nothing is paired or sent.") },
                        trailingContent = { TextButton(onClick = session::endDemo) { Text("Pair Your Mac") } })
                }
            } }
            for (h in hosts) { val at = i++; item(key = h.id) {
                val current = h.id == host?.id
                var menu by remember { mutableStateOf(false) }
                CardRow(at, n) { Box {
                    ListItem(
                        colors = cardListItemColors(),
                        modifier = Modifier.combinedClickable(onClick = { session.show(h) }, onLongClick = { menu = true }),
                        leadingContent = {
                            val c = if (current) w.computers.firstOrNull { it.thisComputer } else null
                            val tint = if (current) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant
                            Box(Modifier.width(34.dp), contentAlignment = Alignment.Center) {
                                if (c != null) ComputerIcon(c, 26.dp, color = tint) else Icon(computerIcon(h.kind), null, Modifier.size(28.dp), tint = tint)
                            }
                        },
                        headlineContent = { Text(h.shortName, fontWeight = FontWeight.SemiBold) },
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
                        DropdownMenuItem(text = { Text("Forget This Computer", color = ErrorColor) }, leadingIcon = { Icon(Icons.Default.Delete, null, tint = ErrorColor) },
                            onClick = { menu = false; forgetting = h })
                    }
                } }
            } }
            val addAt = i
            item(key = "add") {
                CardRow(addAt, n) {
                    ListItem(
                        colors = cardListItemColors(),
                        modifier = Modifier.combinedClickable(onClick = { adding = true }),
                        leadingContent = { Box(Modifier.width(34.dp), contentAlignment = Alignment.Center) { Icon(Icons.Default.Add, null, tint = MaterialTheme.colorScheme.primary) } },
                        headlineContent = { Text("Add Computer") },
                        trailingContent = { Chevron() },
                    )
                }
                Footer("The phone shows one computer at a time. Tap one to switch; touch and hold to forget it.")
            }
            if (!session.isDemo && link == Link.Connected) item {
                // Shown over the whole app (Workspace), and minimised to a bar on every screen.
                ScreenCards(model) { id, title -> share.open(id, title) }
            }
            item {
                SectionTitle("This Phone")
                GroupedCard {
                    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text("Name", style = MaterialTheme.typography.bodyLarge)
                        Spacer(Modifier.width(16.dp))
                        Box(Modifier.weight(1f), contentAlignment = Alignment.CenterEnd) {
                            if (name.isEmpty()) Text(android.os.Build.MODEL, style = MaterialTheme.typography.bodyLarge,
                                color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f))
                            BasicTextField(
                                name, { name = it }, singleLine = true,
                                textStyle = MaterialTheme.typography.bodyLarge.copy(color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.End),
                                cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                                modifier = Modifier.fillMaxWidth().semantics { contentDescription = "Name" }.onFocusChanged { f ->
                                    if (!f.isFocused && name.trim() != session.deviceName) {
                                        session.deviceName = name
                                        session.renamed()
                                    }
                                },
                            )
                        }
                    }
                }
                Footer("What your computers call this phone.")
            }
        }
    }
    forgetting?.let { h ->
        if (!LocalAppLocked.current) AlertDialog(
            onDismissRequest = { forgetting = null },
            title = { Text("Forget ${h.shortName}?") },
            text = { Text("This phone stops connecting to it and getting its notifications. You can pair again with a new code.") },
            confirmButton = { TextButton(onClick = { session.forget(h); forgetting = null }) { Text("Forget", color = ErrorColor) } },
            dismissButton = { TextButton(onClick = { forgetting = null }) { Text("Cancel") } },
        )
    }
}

@Composable
fun SectionTitle(title: String) = SectionHeader(title)

@Composable
fun Footer(text: String) = Text(
    text, Modifier.padding(horizontal = CardInset + 16.dp, vertical = 8.dp),
    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
)
