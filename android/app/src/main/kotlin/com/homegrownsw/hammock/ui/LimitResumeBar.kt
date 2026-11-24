package com.homegrownsw.hammock.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Schedule
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.limitResumeLabel
import hammock.v1.Hammock.Pane
import java.time.ZonedDateTime
import kotlinx.coroutines.delay

/**
 * Along the top of a pane whose agent stopped at its usage limit and is sent "continue" when the limit
 * resets: "Continues at 15:01 · Cancel", over the terminal and the conversation alike, as on the Mac and
 * the iPhone. Cancel shows where you can type ([drive]).
 */
@Composable
fun LimitResumeBar(model: AppModel, pane: Pane, drive: Boolean) {
    val view = LocalView.current
    // Once a minute: "tomorrow at" becomes "at" past midnight.
    val now by produceState(ZonedDateTime.now()) {
        while (true) {
            delay(60_000)
            value = ZonedDateTime.now()
        }
    }
    val label = pane.limitResumeLabel(now) ?: return
    Surface(color = MaterialTheme.colorScheme.surfaceContainer, modifier = Modifier.fillMaxWidth()) {
        Row(
            Modifier.padding(horizontal = 12.dp, vertical = 6.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.Outlined.Schedule, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(
                label, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.weight(1f).semantics { stateDescription = pane.agent.summary },
            )
            if (drive) {
                OutlinedButton(
                    onClick = { view.hapticConfirm(); model.cancelLimitResume(pane.id) },
                    modifier = Modifier.semantics { contentDescription = "Don't Continue When Limit Resets" },
                ) { Text("Cancel") }
            }
        }
    }
}
