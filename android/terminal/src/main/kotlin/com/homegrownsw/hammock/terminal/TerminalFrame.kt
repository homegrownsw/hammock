package com.homegrownsw.hammock.terminal

enum class CursorStyle { BLOCK, BAR, UNDERLINE, HOLLOW }

/**
 * Everything needed to draw the viewport, copied out of the engine
 * ([TerminalEngine.frame]) so drawing needn't hold its lock. Kept and
 * refilled frame after frame: its arrays grow but are never reallocated
 * for the same size, so a frame costs no garbage.
 *
 * Cells are four ints each, row by row: see [codepoint], [fg], [bg], [flags]
 * and [text].
 */
class TerminalFrame {
    var cols = 0; internal set
    var rows = 0; internal set
    /** Default colours (0xRRGGBB), with any a program set (OSC 10/11). */
    var background = 0; internal set
    var foreground = 0xFFFFFF; internal set
    /** The cursor's colour, if a program set one (else the theme's). */
    var cursorColor: Int? = null; internal set
    var cursorVisible = false; internal set
    var cursorX = 0; internal set
    var cursorY = 0; internal set
    var cursorStyle = CursorStyle.BLOCK; internal set
    var cursorBlinking = false; internal set
    /** What changed since the last frame that cleaned: [Dirty]. */
    var dirty = Dirty.FULL; internal set

    internal var cells = IntArray(0)
    internal var extra = IntArray(64)
    internal var rowFlags = IntArray(0)
    internal val header = IntArray(14)

    enum class Dirty { NONE, PARTIAL, FULL }

    /** Whether row [y] changed (libghostty's tracking: conservative). */
    fun rowDirty(y: Int): Boolean = dirty == Dirty.FULL || (dirty == Dirty.PARTIAL && rowFlags[y] != 0)

    private fun i(x: Int, y: Int) = (y * cols + x) * 4

    /**
     * The cell's codepoint: 0 when empty, negative for a cluster of several
     * (read it with [text]).
     */
    fun codepoint(x: Int, y: Int): Int = cells[i(x, y)]

    /** The cell's foreground, 0xRRGGBB, or -1 for the default. */
    fun fg(x: Int, y: Int): Int = color(cells[i(x, y) + 1])

    /** The cell's background, 0xRRGGBB, or -1 for the default. */
    fun bg(x: Int, y: Int): Int = color(cells[i(x, y) + 2])

    /** The cell's [CellFlags]. */
    fun flags(x: Int, y: Int): Int = cells[i(x, y) + 3]

    /** The cell's grapheme cluster ("" when empty). */
    fun text(x: Int, y: Int): String {
        val cp = codepoint(x, y)
        return when {
            cp == 0 -> ""
            cp > 0 -> String(Character.toChars(cp))
            else -> cluster(-cp - 1)
        }
    }

    internal fun cluster(offset: Int): String {
        val n = extra[offset]
        val sb = StringBuilder(n * 2)
        for (k in 1..n) sb.appendCodePoint(extra[offset + k])
        return sb.toString()
    }

    /** One row as plain text, trailing blanks trimmed. */
    fun rowText(y: Int): String {
        val sb = StringBuilder(cols)
        for (x in 0 until cols) {
            if (flags(x, y) and CellFlags.SPACER != 0) continue
            val t = text(x, y)
            sb.append(t.ifEmpty { " " })
        }
        return sb.trimEnd().toString()
    }

    /** The viewport as plain text (tests and accessibility). */
    fun text(): String = (0 until rows).joinToString("\n") { rowText(it) }

    internal fun load(): Boolean {
        val h = header
        cols = h[0]; rows = h[1]
        background = h[2]; foreground = h[3]
        cursorColor = if (h[4] and HAS != 0) h[4] and 0xFFFFFF else null
        cursorVisible = h[5] != 0
        cursorX = h[6]; cursorY = h[7]
        cursorStyle = when (h[8]) { 1 -> CursorStyle.BAR; 2 -> CursorStyle.UNDERLINE; 3 -> CursorStyle.HOLLOW; else -> CursorStyle.BLOCK }
        cursorBlinking = h[9] != 0
        dirty = when (h[10]) { 0 -> Dirty.NONE; 1 -> Dirty.PARTIAL; else -> Dirty.FULL }
        return true
    }

    internal companion object {
        const val HAS = 1 shl 24
        fun color(v: Int) = if (v and HAS != 0) v and 0xFFFFFF else -1
    }
}

/** Bits of [TerminalFrame.flags]. */
object CellFlags {
    const val BOLD = 1 shl 0
    const val ITALIC = 1 shl 1
    const val FAINT = 1 shl 2
    const val UNDERLINE = 1 shl 3
    const val STRIKETHROUGH = 1 shl 4
    const val INVERSE = 1 shl 5
    const val INVISIBLE = 1 shl 6
    /** Takes this column and the next. */
    const val WIDE = 1 shl 7
    /** The trailing half of a wide cell. */
    const val SPACER = 1 shl 8
    const val SELECTED = 1 shl 9
    const val BLINK = 1 shl 10
    const val OVERLINE = 1 shl 11

    /** The underline's style (1 single, 2 double, 3 curly, 4 dotted, 5 dashed). */
    fun underlineStyle(flags: Int) = (flags shr 12) and 7
}
