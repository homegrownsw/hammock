package com.homegrownsw.hammock.terminal

import kotlin.math.roundToInt

/** The terminal's font size (Settings ▸ Terminal ▸ Size), which pinching a terminal changes too (TerminalFontSize.swift on the iPhone). */
object TerminalFontSize {
    val range = 7..18

    /** The size a pinch picks: where it started, scaled by the pinch, in whole sizes within the setting's range. */
    fun pinched(start: Float, scale: Float): Float {
        val s = if (scale.isFinite() && scale > 0f) (start * scale).roundToInt() else start.roundToInt()
        return s.coerceIn(range).toFloat()
    }
}
