package com.homegrownsw.hammock.panes

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock

/** Opens a file in an editor pane beside paneID (a link in the preview, a diff's file) and shows it. */
internal suspend fun openEditorBeside(model: AppModel, paneID: String, path: String, line: Int = 0, view: Hammock.EditorInfo.View = Hammock.EditorInfo.View.VIEW_SOURCE) {
    val open = Hammock.EditorOpen.newBuilder().setPath(path).setLine(maxOf(line, 0)).setView(view).build()
    val r = model.call {
        setCreatePane(Hammock.CreatePane.newBuilder().setSplitPaneId(paneID).setSpec(Hammock.PaneSpec.newBuilder().setEditor(open)).build())
    }
    if (r.hasCreated() && r.created.id.isNotEmpty()) model.focus(r.created.id)
}

/** A strip across the pane saying something about it, with what to do about it. */
@Composable
internal fun Banner(text: String, tint: Color, foreground: Color, buttons: @Composable () -> Unit) {
    Column(
        Modifier.fillMaxWidth().background(tint.copy(alpha = 0.16f)).padding(start = 12.dp, end = 4.dp, top = 8.dp, bottom = 2.dp),
    ) {
        Text(text, color = foreground, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(end = 8.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(0.dp), verticalAlignment = Alignment.CenterVertically) { buttons() }
    }
}

@Composable
internal fun BannerButton(label: String, color: Color, onClick: () -> Unit) {
    TextButton(onClick = onClick) { Text(label, color = color, style = MaterialTheme.typography.labelLarge) }
}

/** A small rounded chip (log levels, filters). */
@Composable
internal fun Chip(label: String, fill: Color, content: Color, onClick: () -> Unit, modifier: Modifier = Modifier) {
    Text(
        label,
        color = content,
        style = MaterialTheme.typography.labelMedium,
        modifier = modifier
            .background(fill, RoundedCornerShape(50))
            .clickable(onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 5.dp),
    )
}

/** A colour as Compose has it, from the theme's ARGB (opaque). */
internal fun color(argb: Int) = Color(argb or (0xFF shl 24))
