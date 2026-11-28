package com.homegrownsw.hammock.agentview

import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import hammock.v1.Hammock.GrantDetail

private const val PROMPT_LINES = 4
private const val TOOL_COUNT = 6

/**
 * The parts of what a permission request would allow (an automation's: what
 * runs, when, where, its allowed tools and its prompt), beneath its title:
 * each labelled, the tools as chips, the prompt in a box with its lines.
 * [compact] (a card in a list) shows a few lines of the long ones, with Show
 * all for the rest.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun GrantDetails(details: List<GrantDetail>, compact: Boolean = false, modifier: Modifier = Modifier) {
    var expanded by rememberSaveable { mutableStateOf(false) }
    val clipped = compact && !expanded
    val label = MaterialTheme.colorScheme.onSurfaceVariant
    val fill = MaterialTheme.colorScheme.surfaceVariant
    val hasMore = compact && details.any { d ->
        val n = d.text.split('\n').size
        if (d.code) n > TOOL_COUNT else d.label == "Prompt" && (n > PROMPT_LINES || d.text.length > 240)
    }
    Column(modifier.animateContentSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        details.forEach { d ->
            Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
                Text(d.label, style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.SemiBold, color = label)
                when {
                    d.code -> {
                        val all = d.text.split('\n')
                        val shown = if (clipped) all.take(TOOL_COUNT) else all
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            shown.forEach { t ->
                                Text(
                                    t, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                    modifier = Modifier.clip(RoundedCornerShape(5.dp)).background(fill).padding(horizontal = 6.dp, vertical = 2.dp),
                                )
                            }
                            if (shown.size < all.size) Text("${all.size - shown.size} more", style = MaterialTheme.typography.bodySmall, color = label,
                                modifier = Modifier.padding(horizontal = 4.dp, vertical = 2.dp))
                        }
                    }
                    d.label == "Prompt" || d.text.contains('\n') -> SelectionContainer {
                        Text(
                            d.text, style = MaterialTheme.typography.bodyMedium,
                            maxLines = if (clipped) PROMPT_LINES else Int.MAX_VALUE, overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(fill.copy(alpha = 0.6f)).padding(10.dp),
                        )
                    }
                    else -> SelectionContainer { Text(d.text, style = MaterialTheme.typography.bodyMedium) }
                }
            }
        }
        if (hasMore) TextButton(onClick = { expanded = !expanded }) { Text(if (expanded) "Show less" else "Show all") }
    }
}
