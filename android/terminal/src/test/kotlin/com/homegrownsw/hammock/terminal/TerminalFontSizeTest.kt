package com.homegrownsw.hammock.terminal

import org.junit.Assert.assertEquals
import org.junit.Test

/** Pinching the terminal's font size, as TerminalFontSizeTests.swift checks the iPhone's. */
class TerminalFontSizeTest {
    @Test
    fun pinchingPicksWholeSizesInRange() {
        assertEquals(12f, TerminalFontSize.pinched(12f, 1f))
        assertEquals(16f, TerminalFontSize.pinched(12f, 1.3f))
        assertEquals(10f, TerminalFontSize.pinched(12f, 0.8f))
        assertEquals(TerminalFontSize.range.last.toFloat(), TerminalFontSize.pinched(12f, 5f))
        assertEquals(TerminalFontSize.range.first.toFloat(), TerminalFontSize.pinched(12f, 0.1f))
        assertEquals(12f, TerminalFontSize.pinched(12f, Float.NaN))
    }
}
