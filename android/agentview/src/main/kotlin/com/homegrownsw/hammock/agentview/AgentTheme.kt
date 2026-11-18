package com.homegrownsw.hammock.agentview

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.DeviceFontFamilyName
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

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

/**
 * How the conversation reads, from Settings ▸ Text (the iPhone's
 * AgentFonts): the text's font, its size and line spacing, code's size, and
 * which code blocks wrap. Sizes in sp; [lineSpacing] is extra space between
 * lines.
 */
@Immutable
data class AgentFonts(
    /** A system family's name ([families]); "" the system's own. */
    val family: String = "",
    val size: Float = DEFAULT_SIZE,
    val codeSize: Float = DEFAULT_SIZE - 1,
    val lineSpacing: Float = DEFAULT_SPACING,
    /** Which code blocks wrap: "prose" (Markdown, text and tools), "always" or "never". */
    val codeWrap: String = "prose",
) {
    /** The text's font, or null for the system's. */
    val fontFamily: FontFamily? get() = fontFamily(family)

    companion object {
        const val DEFAULT_SIZE = 16f
        const val DEFAULT_SPACING = 4f

        /**
         * The fonts Settings offers, as name and label: the system's, then
         * Android's generic families, which every phone has (fonts.xml).
         */
        val families = listOf(
            "" to "System", "serif" to "Serif", "sans-serif-condensed" to "Condensed", "monospace" to "Monospace",
            "serif-monospace" to "Serif Monospace", "casual" to "Casual", "cursive" to "Cursive",
        )

        /** A family by its name ("" the system's: null). */
        fun fontFamily(name: String): FontFamily? = when (name) {
            "" -> null
            "serif" -> FontFamily.Serif
            "monospace" -> FontFamily.Monospace
            "cursive" -> FontFamily.Cursive
            "sans-serif" -> FontFamily.SansSerif
            else -> FontFamily(listOf(FontWeight.Normal, FontWeight.Medium, FontWeight.SemiBold, FontWeight.Bold).map { Font(DeviceFontFamilyName(name), it) })
        }
    }
}

internal val LocalAgentFonts = staticCompositionLocalOf { AgentFonts() }

/**
 * The app's type scale with the body and title styles at [fonts]' size (as
 * a share of Material's 16 sp body), in its font, and the body's lines
 * [AgentFonts.lineSpacing] apart: everything the conversation writes in
 * body styles follows Settings, as the iPhone's fonts.body() does. Labels
 * (captions, chips) keep the system's sizes.
 */
internal fun Typography.forAgent(fonts: AgentFonts): Typography {
    val scale = fonts.size / 16f
    val family = fonts.fontFamily
    fun TextStyle.scaled(extra: Float = 0f) = copy(
        fontSize = fontSize * scale,
        lineHeight = (fontSize.value * scale * 1.25f + extra).sp,
        fontFamily = family ?: fontFamily,
    )
    fun TextStyle.title() = copy(fontSize = fontSize * scale, lineHeight = lineHeight * scale, fontFamily = family ?: fontFamily)
    // At the defaults (16 sp, 4 apart) the body styles' lines are Material's own height.
    return copy(
        bodyLarge = bodyLarge.scaled(fonts.lineSpacing),
        bodyMedium = bodyMedium.scaled(fonts.lineSpacing * 0.75f),
        bodySmall = bodySmall.scaled(fonts.lineSpacing * 0.25f),
        titleLarge = titleLarge.title(),
        titleMedium = titleMedium.title(),
        titleSmall = titleSmall.title(),
    )
}
