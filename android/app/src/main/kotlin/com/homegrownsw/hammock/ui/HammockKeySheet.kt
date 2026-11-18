package com.homegrownsw.hammock.ui

import com.homegrownsw.hammock.LocalAppLocked
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Key
import androidx.compose.material.icons.filled.Fingerprint
import androidx.compose.material.icons.filled.Tab
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.filled.Schedule
import androidx.compose.material.icons.filled.Warning
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import com.google.protobuf.ByteString
import com.homegrownsw.hammock.ApprovalKey
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Workspace
import hammock.v1.Hammock
import hammock.v1.Hammock.HammockKeyRequest
import kotlinx.coroutines.launch

/**
 * A request to sign with the computer's Hammock Key, shown on this phone
 * when it's one the computer lets approve: what's asking, from where, and
 * Approve (with your fingerprint, face or screen lock), Approve for a few
 * minutes, or Don't Allow.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun HammockKeySheet(model: AppModel, w: Workspace, request: HammockKeyRequest, hostName: String, onDone: () -> Unit) {
    val activity = LocalContext.current as FragmentActivity
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val minutes = request.windowMs / 60000
    val pane = w.panes[request.paneId]
    val tab = pane?.let { w.tabs[it.tabId] }
    val group = tab?.let { w.groupOf(it) }
    val agent = pane?.agent?.harness?.takeIf { it.isNotEmpty() }?.let { AgentCatalog.shortName(it, w.kinds) } ?: "An agent"
    val label = request.paneLabel.split(" ▸ ")
    val groupName = group?.let { w.title(it) } ?: label.getOrNull(0).orEmpty()
    val tabName = tab?.let { w.title(it) } ?: label.getOrNull(1).orEmpty()
    val inPane = groupName.isNotEmpty() || tabName.isNotEmpty()

    fun approve(window: Boolean) {
        busy = true
        error = null
        scope.launch {
            try {
                val expires = ApprovalKey.expiry()
                val sig = ApprovalKey.sign(activity, ApprovalKey.message(request, window, expires), "Approve: ${request.what}",
                    // A key Android invalidated is renewed: the computer has the new one before you approve again.
                    onReplaced = { key -> model.send { setSetApprovalKey(Hammock.SetApprovalKey.newBuilder().setPublicKey(ByteString.copyFrom(key)).build()) } })
                model.call {
                    setRespondHammockKey(Hammock.RespondHammockKey.newBuilder().setId(request.id).setAllow(true).setWindow(window)
                        .setSignature(ByteString.copyFrom(sig)).setExpiresUnixMs(expires).build())
                }
                onDone()
            } catch (e: Exception) {
                error = e.message
            }
            busy = false
        }
    }

    if (!LocalAppLocked.current) ModalBottomSheet(onDismissRequest = onDone, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                Box(Modifier.size(56.dp).background(NeedsYouColor, RoundedCornerShape(14.dp)), contentAlignment = Alignment.Center) {
                    Icon(Icons.Default.Key, null, tint = Color.White, modifier = Modifier.size(28.dp))
                }
                Column {
                    Text(hostName, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
                    Text(request.what.replaceFirstChar { it.uppercase() }, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
                }
            }
            Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerHigh, RoundedCornerShape(16.dp)).padding(14.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    (if (!inPane) "Asked by" else if (request.agent) "$agent is asking from" else "Asked from").uppercase(),
                    style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (!inPane) Text(if (request.agent) "$agent outside Hammock" else "A program outside Hammock", style = MaterialTheme.typography.titleMedium)
                if (groupName.isNotEmpty()) Text((group?.icon?.takeIf { it.isNotEmpty() }?.plus(" ") ?: "") + groupName, style = MaterialTheme.typography.titleMedium)
                if (tabName.isNotEmpty()) Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    Icon(Icons.Default.Tab, null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                    Text(tabName, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                if (request.command.isNotEmpty()) Text(
                    request.command, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodyMedium, maxLines = 4, overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.onSurface.copy(alpha = 0.05f), RoundedCornerShape(8.dp)).padding(10.dp),
                )
            }
            if (minutes > 0) Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Icon(Icons.Default.Schedule, null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(18.dp))
                val who = if (request.agent) (if (agent == "An agent") "the agent" else agent) else "programs"
                Text("Approving for $minutes minutes also lets $who in this pane sign without asking again.",
                    color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodyMedium)
            }
            error?.let {
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Icon(Icons.Default.Warning, null, tint = ErrorColor, modifier = Modifier.size(18.dp))
                    Text(it, color = ErrorColor)
                }
            }
            Button(onClick = { approve(false) }, enabled = !busy, modifier = Modifier.fillMaxWidth()) {
                Icon(Icons.Default.Fingerprint, null, Modifier.size(18.dp))
                Spacer(Modifier.width(8.dp))
                Text("Approve")
            }
            if (minutes > 0) OutlinedButton(onClick = { approve(true) }, enabled = !busy, modifier = Modifier.fillMaxWidth()) {
                Text("Approve for $minutes Minutes")
            }
            TextButton(onClick = {
                model.send { setRespondHammockKey(Hammock.RespondHammockKey.newBuilder().setId(request.id).setAllow(false).build()) }
                onDone()
            }, enabled = !busy, modifier = Modifier.fillMaxWidth()) { Text("Don't Allow", color = ErrorColor) }
        }
    }
}
