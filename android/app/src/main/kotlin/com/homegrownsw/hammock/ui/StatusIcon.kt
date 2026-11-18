package com.homegrownsw.hammock.ui

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Bedtime
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.PanTool
import androidx.compose.material.icons.filled.PriorityHigh
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import hammock.v1.Hammock.AgentState

/** Waiting on you: orange, as on the Mac and iPhone. */
val NeedsYouColor = Color(0xFFF2994A)
val ErrorColor = Color(0xFFE5484D)

/**
 * An agent's state as one small glyph (the iPhone's StatusIcon): a ring
 * going round while it works (filling, when it says how far along), a hand
 * when it needs you, a tick when it's finished and you haven't looked, a
 * grey ring at rest, a moon asleep, a terminal for a plain shell.
 */
@Composable
fun StatusIcon(
    state: AgentState?,
    resting: Boolean = false,
    asleep: Boolean = false,
    progress: Int? = null,
    size: Dp = 18.dp,
    modifier: Modifier = Modifier,
) {
    val accent = MaterialTheme.colorScheme.primary
    val quiet = MaterialTheme.colorScheme.onSurfaceVariant
    Box(modifier.size(size), contentAlignment = Alignment.Center) {
        when {
            asleep -> Icon(Icons.Default.Bedtime, "Asleep", Modifier.size(size), tint = quiet)
            state == AgentState.AGENT_STATE_NEEDS_INPUT -> Badge(NeedsYouColor, size) {
                Icon(Icons.Default.PanTool, "Waiting for you", Modifier.size(size * 0.55f), tint = Color.White)
            }
            state == AgentState.AGENT_STATE_ERRORED -> Badge(ErrorColor, size) {
                Icon(Icons.Default.PriorityHigh, "Errored", Modifier.size(size * 0.6f), tint = Color.White)
            }
            state == AgentState.AGENT_STATE_WORKING || state == AgentState.AGENT_STATE_STARTING -> Working(accent, size, progress)
            state == AgentState.AGENT_STATE_IDLE -> Badge(accent, size) {
                Icon(Icons.Default.Check, "Finished", Modifier.size(size * 0.62f), tint = MaterialTheme.colorScheme.onPrimary)
            }
            resting -> Canvas(Modifier.size(size)) {
                drawCircle(quiet.copy(alpha = 0.6f), radius = this.size.minDimension / 2 - 1.5.dp.toPx(), style = Stroke(1.6.dp.toPx()))
            }
            else -> Icon(Icons.Default.Terminal, null, Modifier.size(size), tint = quiet)
        }
    }
}

@Composable
private fun Badge(color: Color, size: Dp, content: @Composable () -> Unit) {
    Box(Modifier.size(size), contentAlignment = Alignment.Center) {
        Canvas(Modifier.size(size)) { drawCircle(color) }
        content()
    }
}

@Composable
private fun Working(color: Color, size: Dp, progress: Int?) {
    val t = rememberInfiniteTransition(label = "working")
    val angle by t.animateFloat(0f, 360f, infiniteRepeatable(tween(1100, easing = LinearEasing), RepeatMode.Restart), label = "spin")
    Canvas(Modifier.size(size)) {
        val w = 2.dp.toPx()
        val inset = w / 2 + 1.dp.toPx()
        val arcSize = androidx.compose.ui.geometry.Size(this.size.width - inset * 2, this.size.height - inset * 2)
        val topLeft = androidx.compose.ui.geometry.Offset(inset, inset)
        drawArc(color.copy(alpha = 0.22f), 0f, 360f, false, topLeft, arcSize, style = Stroke(w))
        if (progress != null) {
            drawArc(color, -90f, 360f * progress.coerceIn(0, 100) / 100f, false, topLeft, arcSize, style = Stroke(w, cap = StrokeCap.Round))
        } else {
            drawArc(color, angle, 100f, false, topLeft, arcSize, style = Stroke(w, cap = StrokeCap.Round))
        }
    }
}
