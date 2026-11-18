package com.homegrownsw.hammock.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Article
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.automirrored.filled.ListAlt
import androidx.compose.material.icons.filled.AutoAwesome
import androidx.compose.material.icons.filled.Build
import androidx.compose.material.icons.filled.Cloud
import androidx.compose.material.icons.filled.DataObject
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.Email
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Hub
import androidx.compose.material.icons.filled.Language
import androidx.compose.material.icons.filled.MusicNote
import androidx.compose.material.icons.filled.PhoneIphone
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.SettingsInputAntenna
import androidx.compose.material.icons.filled.Speed
import androidx.compose.material.icons.filled.Storage
import androidx.compose.material.icons.filled.SwapVert
import androidx.compose.material.icons.filled.TheaterComedy
import androidx.compose.material.icons.filled.Tv
import androidx.compose.material.icons.filled.ViewQuilt
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.FindInPage
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.Bedtime
import androidx.compose.material.icons.outlined.SdStorage
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Matrix
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathFillType
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathParser
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.isLog
import hammock.v1.Hammock.Pane
import hammock.v1.Hammock.PaneKind

// The iPhone's ProgramIcon, AgentMark and PaneIcon: the program running in a
// pane as a monochrome glyph (Simple Icons' logos where there's one, Material
// symbols for the rest), an agent as its own logo.

/** The agents' harnesses that draw their mark as a program (the daemon's knownAgents). */
private val harnesses = setOf("claude", "codex", "opencode", "gemini")

private val parsed = HashMap<String, Path>()

/** A path, parsed once. */
private fun pathOf(d: String): Path = synchronized(parsed) {
    parsed.getOrPut(d) { PathParser().parsePathString(d).toPath() }
}

/** SVG path data fitted (aspect kept, centred) into the canvas, as the iPhone's SVGShape. */
@Composable
private fun FittedPath(d: String, size: Dp, brush: Brush, evenOdd: Boolean = false, modifier: Modifier = Modifier) {
    Canvas(modifier.size(size)) {
        val src = pathOf(d)
        val b = src.getBounds()
        if (b.width <= 0f || b.height <= 0f) return@Canvas
        val s = minOf(this.size.width / b.width, this.size.height / b.height)
        val p = Path().apply {
            addPath(src)
            transform(Matrix().apply {
                translate(this@Canvas.size.width / 2 - b.center.x * s, this@Canvas.size.height / 2 - b.center.y * s)
                scale(s, s)
            })
            fillType = if (evenOdd) PathFillType.EvenOdd else PathFillType.NonZero
        }
        drawPath(p, brush)
    }
}

private val claudeOrange = Color(0xFFD97757)
private val codexGradient = listOf(Color(0xFFB1A7FF), Color(0xFF7A9DFF), Color(0xFF3941FF))
private val geminiGradient = listOf(Color(0xFF3186FF), Color(0xFF9B72CB))

/** Which agent a pane runs, as its logo: Claude's spark, Codex's cloud, others' marks, else a monogram. */
@Composable
fun AgentMark(harness: String, size: Dp = 16.dp, modifier: Modifier = Modifier) {
    val ink = LocalContentColor.current
    val d = agentMarkPaths[harness]
    when {
        harness == "claude" -> FittedPath(d!!, size, SolidColor(claudeOrange), modifier = modifier)
        harness == "codex" -> FittedPath(d!!, size, Brush.verticalGradient(codexGradient), evenOdd = true, modifier = modifier)
        harness == "gemini" -> FittedPath(d!!, size, Brush.linearGradient(geminiGradient, Offset.Zero, Offset.Infinite), evenOdd = true, modifier = modifier)
        d != null -> {
            val own = when (harness) {
                "qwen" -> Color(0xFF615CED)
                "kiro" -> Color(0xFF9046FF)
                "antigravity" -> Color(0xFF3186FF)
                else -> ink
            }
            FittedPath(d, size, SolidColor(own), evenOdd = true, modifier = modifier)
        }
        else -> {
            val tint = monogramTint(harness)
            Box(
                modifier.size(size).padding(size * 0.06f).clip(RoundedCornerShape(size * 0.28f)).background(tint.copy(alpha = 0.18f)),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    AgentCatalog.shortName(harness, emptyList()).take(1).uppercase(), color = tint, fontWeight = FontWeight.Bold,
                    fontSize = (size.value * 0.62f).sp, lineHeight = (size.value * 0.62f).sp,
                )
            }
        }
    }
}

/** A monogram's colour, picked by a hash of the id that's the same on every device (as the iPhone's). */
private fun monogramTint(harness: String): Color {
    val palette = listOf(
        Color(0xFF007AFF), Color(0xFF34C759), Color(0xFF5856D6), Color(0xFFFF2D55), Color(0xFFAF52DE),
        Color(0xFF30B0C7), Color(0xFF00C7BE), Color(0xFF32ADE6), Color(0xFFA2845E), Color(0xFFFF3B30),
    )
    var h = 5381L
    for (c in harness) h = (h * 33 + c.code) and 0xFFFFFFFFL
    return palette[(h % palette.size).toInt()]
}

/** The Material stand-in for each SF Symbol the program icons use. */
private fun programSymbol(name: String): ImageVector = when (name) {
    "sparkles" -> Icons.Default.AutoAwesome
    "hammer" -> Icons.Default.Build
    "bubble.left.and.bubble.right" -> Icons.AutoMirrored.Filled.Chat
    "cloud" -> Icons.Default.Cloud
    "cylinder.split.1x2" -> Icons.Default.Storage
    "plus.forwardslash.minus" -> PlusMinus
    "internaldrive" -> Icons.Outlined.SdStorage
    "doc.text" -> Icons.Outlined.Description
    "pencil" -> Icons.Default.Edit
    "folder" -> Icons.Default.Folder
    "curlybraces" -> Icons.Default.DataObject
    "doc.text.magnifyingglass" -> Icons.Outlined.FindInPage
    "envelope" -> Icons.Default.Email
    "gauge.with.dots.needle.50percent" -> Icons.Default.Speed
    "rectangle.split.2x2" -> Icons.Default.ViewQuilt
    "music.note" -> Icons.Default.MusicNote
    "antenna.radiowaves.left.and.right" -> Icons.Default.SettingsInputAntenna
    "theatermasks" -> Icons.Default.TheaterComedy
    "magnifyingglass" -> Icons.Default.Search
    "zzz" -> Icons.Outlined.Bedtime
    "network" -> Icons.Default.Hub
    "arrow.up.arrow.down" -> Icons.Default.SwapVert
    "eye" -> Icons.Default.Visibility
    "globe" -> Icons.Default.Language
    else -> Icons.Outlined.Terminal
}

/** ± (the iPhone's plusminus): Material has no such symbol. */
val PlusMinus: ImageVector by lazy {
    ImageVector.Builder("PlusMinus", 24.dp, 24.dp, 24f, 24f).apply {
        addPath(PathParser().parsePathString("M11 4h2v4h4v2h-4v4h-2v-4H7V8h4zM6 17h12v2H6z").toNodes(), fill = SolidColor(Color.Black))
    }.build()
}

/**
 * The program running in a terminal pane, as an icon: the first of [program]'s
 * names with one (an agent's mark for an agent), else the terminal glyph.
 * Monochrome in the content colour, as the iPhone's plain style.
 */
@Composable
fun ProgramIcon(program: List<String>, size: Dp = 15.dp, modifier: Modifier = Modifier) {
    val tint = LocalContentColor.current
    for (name in program) {
        if (name in harnesses) return AgentMark(name, size - 1.dp, modifier)
        val key = builtInPrograms[name] ?: continue
        builtInPaths[key]?.let { d ->
            return FittedPath(d, size, SolidColor(tint), modifier = modifier.padding(size * 0.1f))
        }
        builtInSymbols[key]?.let { s -> return Icon(programSymbol(s), null, modifier.size(size), tint = tint) }
    }
    Icon(Icons.Outlined.Terminal, null, modifier.size(size), tint = tint)
}

/** A pane's icon: its kind's, an agent's mark, or the program running (the iPhone's PaneIcon). */
@Composable
fun PaneIcon(pane: Pane?, size: Dp = 16.dp, modifier: Modifier = Modifier) {
    val tint = LocalContentColor.current
    when (pane?.kind) {
        PaneKind.PANE_KIND_BROWSER -> Icon(Icons.Default.Language, null, modifier.size(size), tint = tint)
        PaneKind.PANE_KIND_SIMULATOR -> Icon(if (pane.devicePlatform == "tvOS") Icons.Default.Tv else Icons.Default.PhoneIphone, null, modifier.size(size), tint = tint)
        PaneKind.PANE_KIND_EDITOR -> Icon(Icons.AutoMirrored.Filled.Article, null, modifier.size(size), tint = tint)
        PaneKind.PANE_KIND_DIFF -> Icon(PlusMinus, null, modifier.size(size), tint = tint)
        else -> when {
            pane != null && pane.isLog -> Icon(Icons.AutoMirrored.Filled.ListAlt, null, modifier.size(size), tint = tint)
            pane != null && pane.hasAgent() && pane.agent.harness.isNotEmpty() -> AgentMark(pane.agent.harness, size, modifier)
            else -> ProgramIcon(if (pane?.exited == false) pane.programList else emptyList(), size, modifier)
        }
    }
}
