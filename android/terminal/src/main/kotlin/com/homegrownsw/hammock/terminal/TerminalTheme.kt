package com.homegrownsw.hammock.terminal

/**
 * Default colours for a terminal, as 0xRRGGBB, matched to the app's
 * appearance (apple/Sources/HammockKit/TerminalTheme.swift). Programs can
 * still set their own; these are what they start from.
 */
data class TerminalTheme(
    val background: Int,
    val foreground: Int,
    val cursor: Int,
    val selection: Int,
    /** null keeps the text's own colour. */
    val selectionForeground: Int? = null,
    /** The 16 ANSI colours. */
    val ansi: List<Int>,
) {
    init {
        require(ansi.size == 16) { "a theme has 16 ANSI colours" }
    }

    companion object {
        val Light = TerminalTheme(
            background = 0xFFFFFF, foreground = 0x1D1D1F, cursor = 0x1D1D1F, selection = 0xB3D7FF,
            ansi = listOf(
                0x1D1D1F, 0xC42B21, 0x1E7B34, 0x946300, 0x0058CC, 0x8944AB, 0x16707F, 0x8E8E93,
                0x6B6B70, 0xFF3B30, 0x34C759, 0xB58A00, 0x007AFF, 0xAF52DE, 0x32ADE6, 0xC7C7CC,
            ),
        )

        val Dark = TerminalTheme(
            background = 0x1E1E20, foreground = 0xE5E5EA, cursor = 0xE5E5EA, selection = 0x3F638B,
            ansi = listOf(
                0x48484A, 0xFF6961, 0x4CD964, 0xFFD60A, 0x64A8FF, 0xC589F2, 0x5AC8DA, 0xD1D1D6,
                0x8E8E93, 0xFF453A, 0x30D158, 0xFFE55C, 0x0A84FF, 0xBF5AF2, 0x64D2FF, 0xFFFFFF,
            ),
        )

        /**
         * Parses a Ghostty theme file (`background = #rrggbb`, `palette = 3=#rrggbb`,
         * …). Anything the file doesn't set comes from [base].
         */
        fun parseGhostty(text: String, base: TerminalTheme = Dark): TerminalTheme {
            var t = base
            val ansi = base.ansi.toMutableList()
            for (raw in text.lineSequence()) {
                val line = raw.trim()
                if (line.startsWith("#")) continue
                val eq = line.indexOf('=').takeIf { it > 0 } ?: continue
                val key = line.substring(0, eq).trim()
                val value = line.substring(eq + 1).trim()
                when (key) {
                    "background" -> t = t.copy(background = hex(value) ?: t.background)
                    "foreground" -> t = t.copy(foreground = hex(value) ?: t.foreground)
                    "cursor-color" -> t = t.copy(cursor = hex(value) ?: t.cursor)
                    "selection-background" -> t = t.copy(selection = hex(value) ?: t.selection)
                    "selection-foreground" -> t = t.copy(selectionForeground = hex(value))
                    "palette" -> {
                        val parts = value.split('=', limit = 2).map { it.trim() }
                        val i = parts.getOrNull(0)?.toIntOrNull()
                        val c = parts.getOrNull(1)?.let(::hex)
                        if (i != null && i in 0..15 && c != null) ansi[i] = c
                    }
                }
            }
            return t.copy(ansi = ansi)
        }

        private fun hex(s: String): Int? {
            val h = s.removePrefix("#")
            return if (h.length == 6) h.toIntOrNull(16) else null
        }
    }
}
