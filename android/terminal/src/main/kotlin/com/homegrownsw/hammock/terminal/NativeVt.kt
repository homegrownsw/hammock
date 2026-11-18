package com.homegrownsw.hammock.terminal

/**
 * libhammockterm's entry points (src/main/cpp/hammockterm.cpp), over
 * libghostty-vt. Not thread-safe: TerminalEngine locks around each call.
 */
internal object NativeVt {
    init {
        System.loadLibrary("hammockterm")
    }

    external fun create(cols: Int, rows: Int): Long
    external fun free(h: Long)
    external fun write(h: Long, data: ByteArray, off: Int, len: Int)
    external fun resize(h: Long, cols: Int, rows: Int, cellWidth: Int, cellHeight: Int)
    external fun reset(h: Long, cols: Int, rows: Int)
    external fun setTheme(h: Long, bg: Int, fg: Int, cursor: Int, ansi: IntArray)
    external fun setDefaultCursor(h: Long, style: Int, blink: Boolean)
    external fun scroll(h: Long, mode: Int, delta: Int)
    external fun scrollbar(h: Long, out: LongArray)
    external fun mode(h: Long, mode: Int, ansi: Boolean): Boolean
    external fun state(h: Long): Int
    external fun title(h: Long): ByteArray
    external fun frame(h: Long, header: IntArray, cells: IntArray, extra: IntArray, rowFlags: IntArray, clean: Boolean): Int
    external fun encodeKey(h: Long, keyCode: Int, mods: Int, text: ByteArray?, unshifted: Int, action: Int): ByteArray?
    external fun pasteIsSafe(text: ByteArray): Boolean
    external fun encodePaste(h: Long, text: ByteArray): ByteArray
    external fun encodeMouse(h: Long, action: Int, button: Int, x: Float, y: Float, mods: Int, geometry: FloatArray): ByteArray?
}
