package com.homegrownsw.hammock.ui

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.activity.enableEdgeToEdge
import hammock.v1.Hammock.HostTheme

/**
 * One of Hammock's themes as the phone uses it (the Mac's AppTheme): the
 * accent for buttons and links, your messages' bubble, and the tints the
 * Mac's sidebar and toolbar take, which colour the app's surfaces here.
 * Colours are a light and a dark value.
 */
data class HammockTheme(
    val name: String,
    val accent: Pair<Long, Long>,
    val bubble: Pair<Long, Long>,
    val sidebar: Pair<Long, Long>,
    val toolbar: Pair<Long, Long>,
) {
    companion object {
        private fun p(light: Long, dark: Long) = light to dark
        private fun p(both: Long) = both to both

        /** Hammock's own tints and the well-known themes' accents, as the Mac has them (AppTheme.swift). */
        val all = listOf(
            HammockTheme("Hammock", p(0x1F7A57, 0x3FB88A), p(0x1D6046, 0x1F6A4C), p(0xC5E2CF, 0x102D1C), p(0xE2F1E7, 0x173825)),
            HammockTheme("Graphite", p(0x3E3E46, 0xE2E2E8), p(0x5E5E68), p(0xE6E6EA, 0x1C1C1F), p(0xF3F3F5, 0x26262A)),
            HammockTheme("Ocean", p(0x2B6CC4, 0x72A8F0), p(0x2B6CC4), p(0xD5E6FA, 0x0F2238), p(0xEBF3FD, 0x162D47)),
            HammockTheme("Lagoon", p(0x0F828A, 0x3BCECE), p(0x107884), p(0xCDEAEA, 0x0E272A), p(0xE6F4F4, 0x153437)),
            HammockTheme("Rose", p(0xCC4A72, 0xEA7398), p(0xCC4A72), p(0xF9D9E3, 0x331821), p(0xFCEDF2, 0x40202B)),
            HammockTheme("Lavender", p(0x7352D1, 0xA68EF2), p(0x7352D1), p(0xE4DBFA, 0x221A3A), p(0xF3EFFD, 0x2C2349)),
            HammockTheme("Sand", p(0xA87A1F, 0xD9A743), p(0xA87A1F), p(0xF5E8C3, 0x2F2A1C), p(0xFBF5E2, 0x3B3524)),
            HammockTheme("Apricot", p(0xCD641D, 0xF19E55), p(0xB8571E), p(0xF8DCC3, 0x2C1B11), p(0xFCEEE1, 0x392618)),
            HammockTheme("Cocoa", p(0x845433, 0xD0A380), p(0x794A2F), p(0xE3CFBF, 0x2D1E16), p(0xF1E7DF, 0x38281E)),
            known("Catppuccin", 0xEFF1F5, 0x1E1E2E, p(0x8839EF, 0xCBA6F7), p(0x8839EF, 0x7A4FC0)),
            known("Tokyo Night", 0xE1E2E7, 0x1A1B26, p(0x2E7DE9, 0x7AA2F7), p(0x2E66C8, 0x3D59A1)),
            known("Gruvbox", 0xFBF1C7, 0x282828, p(0xAF3A03, 0xFE8019), p(0xAF3A03, 0xB4531A)),
            known("Nord", 0xE5E9F0, 0x2E3440, p(0x5E81AC, 0x88C0D0), p(0x5E81AC)),
            known("Rosé Pine", 0xFAF4ED, 0x191724, p(0x907AA9, 0xC4A7E7), p(0x7E6A99, 0x6E5A8F)),
            known("Solarized", 0xFDF6E3, 0x002B36, p(0x268BD2), p(0x1F72AD, 0x1D6A9E)),
        )

        val default = all.first()

        /**
         * A well-known terminal theme's: the toolbar in its terminal's
         * backgrounds, the sidebar a shade further from the text
         * (AppTheme.matchingTints).
         */
        private fun known(name: String, lightBg: Long, darkBg: Long, accent: Pair<Long, Long>, bubble: Pair<Long, Long>) =
            HammockTheme(name, accent, bubble, p(mix(lightBg, 0x000000, 0.06), mix(darkBg, 0xFFFFFF, 0.06)), p(lightBg, darkBg))

        private fun mix(a: Long, b: Long, t: Double): Long {
            fun ch(v: Long, s: Int) = (v shr s) and 0xFF
            fun one(s: Int) = Math.round(ch(a, s) + (ch(b, s) - ch(a, s)) * t).coerceIn(0, 255)
            return (one(16) shl 16) or (one(8) shl 8) or one(0)
        }

        fun named(name: String): HammockTheme? = all.firstOrNull { fold(it.name) == fold(name) }

        /** Ignoring case and accents ("rose pine" is Rosé Pine), as the Mac matches them. */
        private fun fold(s: String) = java.text.Normalizer.normalize(s.trim(), java.text.Normalizer.Form.NFD).replace(Regex("\\p{M}"), "").lowercase()

        /** What to show: the one picked in Settings, else the computer's (by its theme's name), else Hammock. */
        fun resolve(pref: String, host: HostTheme?): HammockTheme {
            if (pref.isNotEmpty() && pref != SAME_AS_COMPUTER) named(pref)?.let { return it }
            if (host != null) {
                named(host.windowTheme)?.let { return it }
                named(host.name)?.let { return it }
            }
            return default
        }

        const val SAME_AS_COMPUTER = ""
    }

    fun accent(dark: Boolean) = Color(0xFF000000 or (if (dark) accent.second else accent.first))
    fun bubble(dark: Boolean) = Color(0xFF000000 or (if (dark) bubble.second else bubble.first))
    fun sidebar(dark: Boolean) = Color(0xFF000000 or (if (dark) sidebar.second else sidebar.first))
    fun toolbar(dark: Boolean) = Color(0xFF000000 or (if (dark) toolbar.second else toolbar.first))

    fun colorScheme(dark: Boolean): ColorScheme {
        val a = accent(dark)
        val tint = sidebar(dark)
        val bar = toolbar(dark)
        return if (dark) {
            darkColorScheme(
                primary = a, onPrimary = Color(0xFF0B1A12), primaryContainer = bubble(true), onPrimaryContainer = Color.White,
                secondary = a, secondaryContainer = tint, onSecondaryContainer = Color(0xFFE6EEE9),
                tertiary = a, background = Color(0xFF111312), surface = Color(0xFF111312),
                surfaceContainerLowest = Color(0xFF0C0D0D), surfaceContainerLow = blend(Color(0xFF151716), tint, 0.25f),
                surfaceContainer = blend(Color(0xFF191B1A), tint, 0.35f), surfaceContainerHigh = blend(Color(0xFF202322), bar, 0.35f),
                surfaceContainerHighest = blend(Color(0xFF282B2A), bar, 0.4f),
            )
        } else {
            lightColorScheme(
                primary = a, onPrimary = Color.White, primaryContainer = bubble(false), onPrimaryContainer = Color.White,
                secondary = a, secondaryContainer = tint, onSecondaryContainer = Color(0xFF14241B),
                tertiary = a, background = Color(0xFFF9FAF9), surface = Color(0xFFF9FAF9),
                surfaceContainerLowest = Color.White, surfaceContainerLow = blend(Color(0xFFF4F5F4), bar, 0.35f),
                surfaceContainer = blend(Color(0xFFEFF1EF), bar, 0.5f), surfaceContainerHigh = blend(Color(0xFFE9EBE9), tint, 0.3f),
                surfaceContainerHighest = blend(Color(0xFFE3E5E3), tint, 0.4f),
            )
        }
    }

    private fun blend(base: Color, over: Color, t: Float) = Color(
        red = base.red + (over.red - base.red) * t, green = base.green + (over.green - base.green) * t,
        blue = base.blue + (over.blue - base.blue) * t, alpha = 1f,
    )
}

/** The theme in use, for colours Material's scheme doesn't name (your messages' bubble). */
val LocalHammockTheme = staticCompositionLocalOf { HammockTheme.default }

@Composable
fun HammockMaterialTheme(theme: HammockTheme, appearance: String, content: @Composable () -> Unit) {
    val dark = when (appearance) {
        "light" -> false
        "dark" -> true
        else -> isSystemInDarkTheme()
    }
    // The system bars' icons follow the app's appearance, not the phone's (Settings ▸ Appearance can differ).
    val activity = androidx.compose.ui.platform.LocalContext.current as? androidx.activity.ComponentActivity
    androidx.compose.runtime.LaunchedEffect(dark) {
        val style = if (dark) androidx.activity.SystemBarStyle.dark(android.graphics.Color.TRANSPARENT)
        else androidx.activity.SystemBarStyle.light(android.graphics.Color.TRANSPARENT, android.graphics.Color.TRANSPARENT)
        activity?.enableEdgeToEdge(style, style)
    }
    CompositionLocalProvider(LocalHammockTheme provides theme) {
        MaterialTheme(colorScheme = theme.colorScheme(dark), content = content)
    }
}
