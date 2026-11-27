package com.homegrownsw.hammock.ui

import android.content.ClipData
import android.content.ClipDescription
import android.content.ClipboardManager
import android.os.PersistableBundle
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.homegrownsw.hammock.AutomationIntent
import com.homegrownsw.hammock.LocalAppLocked
import hammock.v1.Hammock.Schedule

/**
 * Run from Tasker: what to fill in Tasker's Send Intent to run [s] on computer [host], its
 * secret among it (made the first time this opens), each with Copy, and a new secret.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TaskerSheet(host: String, s: Schedule, onDone: () -> Unit) {
    val ctx = LocalContext.current
    var secret by remember(host, s.id) { mutableStateOf(AutomationIntent.secret(ctx, host, s.id, s.name)) }
    var renewing by remember { mutableStateOf(false) }
    fun copy(label: String, text: String, sensitive: Boolean = false) {
        val clip = ClipData.newPlainText(label, text)
        if (sensitive) clip.description.extras = PersistableBundle().apply { putBoolean(ClipDescription.EXTRA_IS_SENSITIVE, true) }
        ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(clip)
    }
    if (LocalAppLocked.current) return
    ModalBottomSheet(onDismissRequest = onDone, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("Run from Tasker", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.SemiBold)
            Text("In Tasker, add a Send Intent action with these.",
                style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Field("Action", AutomationIntent.ACTION) { copy("Action", AutomationIntent.ACTION) }
            Field("Package", ctx.packageName) { copy("Package", ctx.packageName) }
            Field("Target", "Broadcast Receiver")
            Field("Extra", "${AutomationIntent.EXTRA_AUTOMATION}:${s.id}", "Copy Automation") { copy("Automation", "${AutomationIntent.EXTRA_AUTOMATION}:${s.id}") }
            Field("Extra", "${AutomationIntent.EXTRA_SECRET}:$secret", "Copy Secret") { copy("Secret", "${AutomationIntent.EXTRA_SECRET}:$secret", sensitive = true) }
            Field("Extra (optional)", "${AutomationIntent.EXTRA_TEXT}:what happened, for the run")
            TextButton(onClick = { renewing = true }) { Text("Make New Secret") }
        }
    }
    if (renewing) AlertDialog(
        onDismissRequest = { renewing = false },
        title = { Text("Make a New Secret?") },
        text = { Text("The old one stops working: Tasker needs the new one.") },
        confirmButton = {
            TextButton(onClick = { secret = AutomationIntent.renew(ctx, host, s.id, s.name); renewing = false }) { Text("Make New Secret") }
        },
        dismissButton = { TextButton(onClick = { renewing = false }) { Text("Cancel") } },
    )
}

/** A field to fill in: its name, and its value with Copy (when it's one to paste). */
@Composable
private fun Field(name: String, value: String, copyLabel: String = "Copy $name", onCopy: (() -> Unit)? = null) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(name, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(value, style = MaterialTheme.typography.bodyMedium, fontFamily = if (onCopy != null) FontFamily.Monospace else null)
        }
        if (onCopy != null) IconButton(onClick = onCopy) { Icon(Icons.Default.ContentCopy, copyLabel) }
    }
}
