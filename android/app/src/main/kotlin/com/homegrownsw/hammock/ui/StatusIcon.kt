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
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import hammock.v1.Hammock.AgentState

/** Waiting on you: amber, as on the Mac and iPhone. */
val NeedsYouColor = Color(0xFFF59300)
val ErrorColor = Color(0xFFE5372C)

/** The status icons' colours (the iPhone's StatusPalette), light and dark. */
object StatusPalette {
    @Composable fun indigo() = pick(Color(0xFF5856D6), Color(0xFF7D7AFF))
    @Composable fun green() = pick(Color(0xFF248A3D), Color(0xFF30D158))
    @Composable fun amber() = pick(Color(0xFFF59300), Color(0xFFFF9F0A))
    @Composable fun red() = pick(Color(0xFFE5372C), Color(0xFFFF453A))
    /** The dot going round a resting agent's ring while shells run: an opaque grey, stronger than the ring. */
    @Composable fun grey() = pick(Color(0xFF6E6E73), Color(0xFFAEAEB2))

    @Composable
    private fun pick(light: Color, dark: Color) = if (MaterialTheme.colorScheme.surface.luminance() < 0.5f) dark else light
}

/**
 * An agent's state as an icon (the iPhone's "Orbit"): rings while all is
 * well (grey at rest, with a dot going slowly round it while work still runs:
 * grey for shells, indigo for subagents; an indigo arc going round while it works, green with a
 * check once it's finished); filled when it needs you (amber with ! for input,
 * red with × for an error). Asleep, a grey moon. No agent: the terminal glyph.
 * Drawn on the iPhone's 20-point grid, scaled to [size].
 */
@Composable
fun StatusIcon(
    state: AgentState?,
    resting: Boolean = false,
    shells: Boolean = false,
    subagents: Boolean = false,
    asleep: Boolean = false,
    progress: Int? = null,
    size: Dp = 18.dp,
    modifier: Modifier = Modifier,
) {
    val quiet = MaterialTheme.colorScheme.onSurfaceVariant
    val sleeping = asleep && state !in listOf(
        AgentState.AGENT_STATE_NEEDS_INPUT, AgentState.AGENT_STATE_WORKING, AgentState.AGENT_STATE_STARTING, AgentState.AGENT_STATE_ERRORED,
    )
    Box(modifier.size(size), contentAlignment = Alignment.Center) {
        when {
            sleeping -> Icon(Icons.Default.Bedtime, "Asleep", Modifier.size(size * 0.78f), tint = quiet)
            state == AgentState.AGENT_STATE_NEEDS_INPUT -> Badge(StatusPalette.amber(), Glyph.Bang, size, "Needs input")
            state == AgentState.AGENT_STATE_ERRORED -> Badge(StatusPalette.red(), Glyph.Cross, size, "Errored")
            state == AgentState.AGENT_STATE_WORKING -> Working(StatusPalette.indigo(), size, progress, starting = false)
            state == AgentState.AGENT_STATE_STARTING -> Working(StatusPalette.indigo(), size, null, starting = true)
            state == AgentState.AGENT_STATE_IDLE -> {
                val green = StatusPalette.green()
                Canvas(Modifier.size(size).semantics { contentDescription = "Finished" }) {
                    val k = this.size.width / 20
                    drawCircle(green, radius = 7.25f * k, style = Stroke(maxOf(1f, 1.75f * k)))
                    drawGlyph(Glyph.Check, green, maxOf(1f, 1.9f * k))
                }
            }
            resting -> Resting(quiet, size, shells, subagents)
            else -> ProgramIcon(emptyList(), size)
        }
    }
}

private enum class Glyph { Check, Cross, Bang }

/** A glyph on the 20-point grid, stroked round. */
private fun DrawScope.drawGlyph(glyph: Glyph, color: Color, width: Float) {
    val k = size.width / 20
    val p = Path()
    when (glyph) {
        Glyph.Check -> { p.moveTo(6.3f * k, 10.3f * k); p.lineTo(8.9f * k, 12.9f * k); p.lineTo(13.9f * k, 7.4f * k) }
        Glyph.Cross -> {
            p.moveTo(7.4f * k, 7.4f * k); p.lineTo(12.6f * k, 12.6f * k)
            p.moveTo(12.6f * k, 7.4f * k); p.lineTo(7.4f * k, 12.6f * k)
        }
        Glyph.Bang -> {
            p.moveTo(10f * k, 5.8f * k); p.lineTo(10f * k, 10.6f * k)
            drawCircle(color, radius = width / 2, center = Offset(10f * k, 14f * k))
        }
    }
    drawPath(p, color, style = Stroke(width, cap = StrokeCap.Round, join = StrokeJoin.Round))
}

@Composable
private fun Badge(fill: Color, glyph: Glyph, size: Dp, label: String) {
    Canvas(Modifier.size(size).semantics { contentDescription = label }) {
        val k = this.size.width / 20
        drawCircle(fill, radius = 8.75f * k)
        drawGlyph(glyph, Color.White, maxOf(1f, (if (glyph == Glyph.Bang) 2.1f else 1.9f) * k))
    }
}

/** At work: a short indigo arc going round a faint track (or filling it, when it says how far along). */
@Composable
private fun Working(color: Color, size: Dp, progress: Int?, starting: Boolean) {
    val t = rememberInfiniteTransition(label = "working")
    val angle by t.animateFloat(0f, 360f, infiniteRepeatable(tween(if (starting) 4800 else 2400, easing = LinearEasing), RepeatMode.Restart), label = "spin")
    Canvas(Modifier.size(size).semantics { contentDescription = if (starting) "Starting" else progress?.let { "Working, $it%" } ?: "Working" }) {
        val k = this.size.width / 20
        val w = maxOf(1.5f, 2.5f * k)
        val d = 14.5f * k
        val topLeft = Offset((this.size.width - d) / 2, (this.size.height - d) / 2)
        val arc = Size(d, d)
        drawArc(color.copy(alpha = 0.35f), 0f, 360f, false, topLeft, arc, style = Stroke(w))
        if (progress != null) {
            drawArc(color, -90f, 360f * progress.coerceIn(2, 100) / 100f, false, topLeft, arc, style = Stroke(w, cap = StrokeCap.Round))
        } else {
            drawArc(color.copy(alpha = if (starting) 0.55f else 1f), angle - 90f, 0.28f * 360f, false, topLeft, arc, style = Stroke(w, cap = StrokeCap.Round))
        }
    }
}

/**
 * At rest: a grey ring. While work still runs, a solid dot goes slowly round
 * it (once every 10 s): grey for shells, the working indigo for subagents.
 */
@Composable
private fun Resting(ring: Color, size: Dp, shells: Boolean, subagents: Boolean) {
    val dot = if (subagents) StatusPalette.indigo() else StatusPalette.grey()
    val label = when {
        subagents -> "Agent idle, subagents running"
        shells -> "Agent idle, shells running"
        else -> "Agent idle"
    }
    val t = rememberInfiniteTransition(label = "resting")
    val angle by t.animateFloat(0f, 360f, infiniteRepeatable(tween(10_000, easing = LinearEasing), RepeatMode.Restart), label = "orbit")
    Canvas(Modifier.size(size).semantics { contentDescription = label }) {
        val k = this.size.width / 20
        drawCircle(ring, radius = 7.25f * k, style = Stroke(maxOf(1f, 1.5f * k)))
        if (shells || subagents) {
            val r = maxOf(3.5f, 5f * k) / 2
            val a = Math.toRadians((angle - 90f).toDouble())
            drawCircle(dot, radius = r, center = Offset(center.x + 7.25f * k * Math.cos(a).toFloat(), center.y + 7.25f * k * Math.sin(a).toFloat()))
        }
    }
}
