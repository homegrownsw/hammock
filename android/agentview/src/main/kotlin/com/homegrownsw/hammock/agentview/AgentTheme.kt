package com.homegrownsw.hammock.agentview

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance

/** The agent view's colours: the app theme's accent, and Material's scheme for the rest. */
@Immutable
data class AgentColors(
    val accent: Color,
    val onAccent: Color,
    /** Your messages: the theme's deeper shade, for white text in light and dark alike. */
    val bubble: Color,
    val secondary: Color,
    val tertiary: Color,
    /** Cards, code and quiet fills (the iPhone's quaternary). */
    val fill: Color,
    val added: Color,
    val removed: Color,
    val success: Color,
    val warning: Color,
    val plan: Color,
    val subagent: Color,
    /** Claude Code's shell-mode pink: a "!" message runs as a command. */
    val shell: Color,
    val md: MdStyle,
)

internal val LocalAgentColors = staticCompositionLocalOf<AgentColors> { error("AgentColors not provided") }

/** Opens a picture in the viewer (the conversation's, which outlives the row). */
internal val LocalOpenImage = compositionLocalOf<(androidx.compose.ui.graphics.ImageBitmap) -> Unit> { {} }

@Composable
internal fun rememberAgentColors(accent: Color, bubble: Color = accent): AgentColors {
    val cs = MaterialTheme.colorScheme
    val body = MaterialTheme.typography.bodyLarge.fontSize
    val dark = cs.surface.luminance() < 0.4f
    return remember(accent, bubble, cs, body) {
        val secondary = cs.onSurfaceVariant
        AgentColors(
            accent = accent,
            onAccent = if (accent.luminance() > 0.6f) Color.Black else Color.White,
            bubble = bubble,
            secondary = secondary,
            tertiary = secondary.copy(alpha = 0.6f),
            fill = cs.onSurface.copy(alpha = 0.07f),
            added = Color(0xFF34C759),
            removed = Color(0xFFFF3B30),
            success = Color(0xFF34C759).copy(alpha = 0.85f),
            warning = Color(0xFFFF9500),
            plan = Color(0xFFAF52DE),
            subagent = Color(0xFF30B0C7),
            shell = Color(0xFFFC5CB0),
            md = MdStyle(
                body = body,
                secondary = secondary,
                link = accent,
                codeBackground = cs.onSurface.copy(alpha = 0.08f),
                string = if (dark) Color(0xFFFF8170) else Color(0xFFC4543D),
                number = if (dark) Color(0xFF6BC9F0) else Color(0xFF1C85AD),
                keyword = if (dark) Color(0xFFFF7AB2) else Color(0xFF9E40B3),
            ),
        )
    }
}
