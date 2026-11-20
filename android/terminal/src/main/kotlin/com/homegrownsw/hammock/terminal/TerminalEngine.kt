package com.homegrownsw.hammock.terminal

import android.view.KeyEvent
import java.lang.ref.Cleaner

/** Modifier keys, as libghostty's GHOSTTY_MODS_* bits. */
object KeyMods {
    const val SHIFT = 1 shl 0
    const val CTRL = 1 shl 1
    const val ALT = 1 shl 2
    const val SUPER = 1 shl 3

    /** The modifiers held in a KeyEvent's meta state. */
    fun fromMetaState(meta: Int): Int {
        var m = 0
        if (meta and KeyEvent.META_SHIFT_ON != 0) m = m or SHIFT
        if (meta and KeyEvent.META_CTRL_ON != 0) m = m or CTRL
        if (meta and KeyEvent.META_ALT_ON != 0) m = m or ALT
        if (meta and KeyEvent.META_META_ON != 0) m = m or SUPER
        return m
    }
}

enum class KeyAction(internal val raw: Int) { RELEASE(0), PRESS(1), REPEAT(2) }

enum class MouseAction(internal val raw: Int) { PRESS(0), RELEASE(1), MOTION(2) }

/** Mouse buttons; the wheel is 4 (up) and 5 (down). */
enum class MouseButton(internal val raw: Int) { LEFT(1), RIGHT(2), MIDDLE(3), WHEEL_UP(4), WHEEL_DOWN(5) }

/**
 * Where the grid is drawn, in pixels, to turn a touch into a cell: the
 * view's size, the cell's (fractional) size, and the padding around the grid.
 */
data class MouseGeometry(val width: Float, val height: Float, val cellWidth: Float, val cellHeight: Float, val padding: Float)

/** The scrollback: [total] rows, the viewport [offset] rows down it, [length] rows tall. */
data class Scrollbar(val total: Long, val offset: Long, val length: Long) {
    val atBottom get() = offset + length >= total
}

/**
 * A client-side terminal fed with a pane's byte stream (snapshot, then live
 * output) using libghostty-vt, as TerminalEngine.swift is on the iPhone. It
 * never answers terminal queries (the daemon's mirror does), so it's purely
 * a renderer's model.
 *
 * Thread-safe: output is written on the model's thread while frames are
 * read on the GL thread, so every call takes the engine's lock. [frame]
 * copies the screen out, so drawing doesn't hold it.
 */
class TerminalEngine(cols: Int = 80, rows: Int = 24) : AutoCloseable {
    private val lock = Any()
    private var handle: Long = NativeVt.create(cols.coerceAtLeast(1), rows.coerceAtLeast(1))
    private val cleanup: Cleaner.Cleanable

    init {
        check(handle != 0L) { "libghostty-vt couldn't make a terminal" }
        val h = handle
        // Freed by close(), or by the cleaner if the engine is dropped
        // without it (a Cleanable runs once). It mustn't capture the engine.
        cleanup = cleaner.register(this) { NativeVt.free(h) }
        NativeVt.setTheme(h, TerminalTheme.Dark.background, TerminalTheme.Dark.foreground, TerminalTheme.Dark.cursor, TerminalTheme.Dark.ansi.toIntArray())
    }

    var cols = cols.coerceAtLeast(1); private set
    var rows = rows.coerceAtLeast(1); private set

    /** Cell size in pixels, reported to programs (Kitty graphics, XTWINOPS). */
    private var cellPixels = 0 to 0

    /** The default colours; a reset keeps them. */
    var theme: TerminalTheme = TerminalTheme.Dark
        set(value) {
            if (value == field) return
            field = value
            locked { NativeVt.setTheme(it, value.background, value.foreground, value.cursor, value.ansi.toIntArray()) }
        }

    private inline fun <T> locked(default: T, fn: (Long) -> T): T = synchronized(lock) {
        if (handle == 0L) default else fn(handle)
    }

    private inline fun locked(fn: (Long) -> Unit) = synchronized(lock) { if (handle != 0L) fn(handle) }

    /** Feeds PTY output. */
    fun write(data: ByteArray, offset: Int = 0, length: Int = data.size - offset) {
        if (length <= 0) return
        locked { NativeVt.write(it, data, offset, length) }
    }

    /** Resets the terminal (before replaying a snapshot) at the pane's size. */
    fun reset(cols: Int, rows: Int) = locked {
        if (cols > 0 && rows > 0) {
            this.cols = cols
            this.rows = rows
        }
        NativeVt.reset(it, cols, rows)
    }

    /** Resets the terminal at the pane's size and replays a snapshot into it, at once: no frame is drawn between the two. */
    fun replace(cols: Int, rows: Int, data: ByteArray) = locked {
        if (cols > 0 && rows > 0) {
            this.cols = cols
            this.rows = rows
        }
        NativeVt.reset(it, cols, rows)
        if (data.isNotEmpty()) NativeVt.write(it, data, 0, data.size)
    }

    fun resize(cols: Int, rows: Int, cellWidthPx: Int = 0, cellHeightPx: Int = 0) = locked {
        if (cols <= 0 || rows <= 0) return@locked
        this.cols = cols
        this.rows = rows
        if (cellWidthPx > 0 && cellHeightPx > 0) cellPixels = cellWidthPx to cellHeightPx
        NativeVt.resize(it, cols, rows, cellPixels.first, cellPixels.second)
    }

    /** Sets the cell size in pixels (as drawn), keeping the grid. Returns whether it changed. */
    fun setCellPixels(width: Int, height: Int): Boolean = locked(false) {
        if (width <= 0 || height <= 0 || (width to height) == cellPixels) return@locked false
        cellPixels = width to height
        NativeVt.resize(it, cols, rows, width, height)
        true
    }

    /** The cursor's shape and blinking until a program asks for another. */
    fun setDefaultCursor(style: CursorStyle, blink: Boolean) = locked {
        NativeVt.setDefaultCursor(it, when (style) {
            CursorStyle.BLOCK -> 0
            CursorStyle.BAR -> 1
            CursorStyle.UNDERLINE -> 2
            CursorStyle.HOLLOW -> 3
        }, blink)
    }

    /** Scrolls the viewport by [delta] rows (negative: back into scrollback). */
    fun scroll(delta: Int) {
        if (delta != 0) locked { NativeVt.scroll(it, 0, delta) }
    }

    fun scrollToBottom() = locked { NativeVt.scroll(it, 1, 0) }

    fun scrollToTop() = locked { NativeVt.scroll(it, 2, 0) }

    val scrollbar: Scrollbar
        get() = locked(Scrollbar(0, 0, 0)) {
            val out = LongArray(3)
            NativeVt.scrollbar(it, out)
            Scrollbar(out[0], out[1], out[2])
        }

    /** Whether DEC private mode [mode] is set (e.g. 2004, bracketed paste; 1, application cursor keys). */
    fun decMode(mode: Int): Boolean = locked(false) { NativeVt.mode(it, mode, false) }

    /** Whether ANSI mode [mode] is set. */
    fun ansiMode(mode: Int): Boolean = locked(false) { NativeVt.mode(it, mode, true) }

    private val state get() = locked(0) { NativeVt.state(it) }

    /** Whether the program is showing the alternate screen (full-screen apps). */
    val isAlternateScreen get() = state and 1 != 0

    /** Whether the running program has enabled mouse reporting. */
    val mouseTracking get() = state and 2 != 0

    /** The program asked for bracketed paste (mode 2004). */
    val bracketedPaste get() = state and 4 != 0

    /** The viewport is following output (not scrolled back). */
    val isAtBottom get() = state and 8 != 0

    /** The window title a program set (OSC 0/2), or "". */
    val title: String get() = locked("") { String(NativeVt.title(it), Charsets.UTF_8) }

    /**
     * Copies the viewport into [into] (reused frame after frame). With
     * [clean], what's reported dirty is cleared: one reader (the renderer)
     * should clean, so it sees every change.
     */
    fun frame(into: TerminalFrame = TerminalFrame(), clean: Boolean = true): TerminalFrame = locked(into) { h ->
        while (true) {
            val r = NativeVt.frame(h, into.header, into.cells, into.extra, into.rowFlags, clean)
            if (r == 1) {
                val needCells = into.header[12]
                val needExtra = into.header[13]
                if (into.cells.size < needCells) into.cells = IntArray(needCells)
                if (into.extra.size < needExtra) into.extra = IntArray(needExtra * 2)
                val rows = into.header[1]
                if (into.rowFlags.size < rows) into.rowFlags = IntArray(rows)
                continue
            }
            if (r == 0) into.load()
            break
        }
        into
    }

    /** The viewport as plain text (tests and accessibility); leaves the dirty state alone. */
    fun text(): String = frame(TerminalFrame(), clean = false).text()

    /**
     * Encodes a key for the PTY, honouring the program's keyboard modes
     * (application cursor keys, the kitty keyboard protocol, …).
     * [keyCode] is Android's (KeyEvent.KEYCODE_*), [mods] [KeyMods] bits,
     * [text] what the key types, if anything, and [unshifted] the codepoint
     * it types without modifiers.
     */
    fun encodeKey(keyCode: Int, mods: Int = 0, text: String? = null, unshifted: Int = 0, action: KeyAction = KeyAction.PRESS): ByteArray? =
        locked(null) { NativeVt.encodeKey(it, keyCode, mods, text?.takeIf { t -> t.isNotEmpty() }?.toByteArray(), unshifted, action.raw) }

    /** Encodes pasted text (bracketed if the program asked for it). */
    fun encodePaste(text: String): ByteArray = locked(text.toByteArray()) { NativeVt.encodePaste(it, text.toByteArray()) }

    /**
     * Encodes a mouse event at ([x], [y]) in the view, in pixels, for a
     * program that enabled mouse reporting; null when it isn't tracking
     * this kind of event.
     */
    fun encodeMouse(action: MouseAction, button: MouseButton?, x: Float, y: Float, mods: Int, geometry: MouseGeometry): ByteArray? =
        locked(null) {
            val g = floatArrayOf(geometry.width, geometry.height, geometry.cellWidth, geometry.cellHeight, geometry.padding)
            NativeVt.encodeMouse(it, action.raw, button?.raw ?: 0, x, y, mods, g)
        }

    // MARK: Selection (touch and hold, and its handles)

    /** Selects the word at a viewport cell (Ghostty's rules); false, selecting nothing, on a blank. */
    fun selectWord(x: Int, y: Int): Boolean = locked(false) { NativeVt.selectWord(it, x, y) }

    /** Dragging on from a touch and hold: from the word it selected to the word at a cell (or the cell, between words). */
    fun extendWordSelection(x: Int, y: Int) = locked { NativeVt.extendWord(it, x, y) }

    /** Selects the screen as it shows now. */
    fun selectScreen() = locked { NativeVt.selectScreen(it) }

    fun clearSelection() = locked { NativeVt.clearSelection(it) }

    /**
     * Moves one end of the selection to a viewport cell, keeping the other
     * (a handle dragged). Returns which end the moved one is now: dragged
     * past the other, the start becomes the end.
     */
    fun moveSelection(edge: SelectionEdge, x: Int, y: Int): SelectionEdge = locked(edge) {
        if (NativeVt.moveSelection(it, edge.ordinal, x, y) == 0) SelectionEdge.START else SelectionEdge.END
    }

    /** The selection's first and last cells in the viewport (rows off the screen are negative, or [rows] or more), or null. */
    fun selectionBounds(): SelectionBounds? = locked(null) {
        val out = IntArray(4)
        if (NativeVt.selectionBounds(it, out)) SelectionBounds(Cell(out[0], out[1]), Cell(out[2], out[3])) else null
    }

    /** The selected text, plain, with trailing spaces trimmed and wrapped lines joined (as the Mac copies), or null. */
    fun selectionText(): String? = locked(null) { NativeVt.selectionText(it)?.toString(Charsets.UTF_8) }

    /** The screen as it shows now, as text the way a selection of it copies (Copy Screen), or null. */
    fun screenText(): String? = locked(null) { NativeVt.screenText(it)?.toString(Charsets.UTF_8) }

    override fun close() {
        synchronized(lock) {
            if (handle == 0L) return
            handle = 0
        }
        cleanup.clean()
    }

    companion object {
        private val cleaner: Cleaner = Cleaner.create()

        /**
         * libghostty's conservative check that pasting text can't run anything
         * by itself: no line breaks and no end-of-paste sequence.
         */
        fun pasteIsSafe(text: String): Boolean = text.isEmpty() || NativeVt.pasteIsSafe(text.toByteArray())
    }
}
