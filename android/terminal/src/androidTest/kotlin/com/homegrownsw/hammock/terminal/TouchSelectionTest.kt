package com.homegrownsw.hammock.terminal

import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/** The engine's touch selection, as TouchSelectionTests.swift checks the iPhone's. */
@RunWith(AndroidJUnit4::class)
class TouchSelectionTest {
    @Test
    fun touchAndHoldSelectsTheWord() = TerminalEngine(20, 4).use { e ->
        e.write("hello big world".toByteArray())
        assertTrue(e.selectWord(7, 0))
        assertEquals("big", e.selectionText())
        assertEquals(SelectionBounds(Cell(6, 0), Cell(8, 0)), e.selectionBounds())
        assertTrue(e.frame().flags(6, 0) and CellFlags.SELECTED != 0)
        // A blank selects nothing.
        assertFalse(e.selectWord(18, 2))
        assertNull(e.selectionText())
        assertNull(e.selectionBounds())
    }

    @Test
    fun draggingOnFromTheWordSelectsWholeWords() = TerminalEngine(30, 4).use { e ->
        e.write("one two three four".toByteArray())
        e.selectWord(9, 0) // "three"
        e.extendWordSelection(15, 0) // in "four"
        assertEquals("three four", e.selectionText())
        e.extendWordSelection(1, 0) // back into "one": the anchor word stays
        assertEquals("one two three", e.selectionText())
        e.extendWordSelection(10, 0)
        assertEquals("three", e.selectionText())
    }

    @Test
    fun handlesMoveOneEndAndSwapWhenCrossed() = TerminalEngine(20, 4).use { e ->
        e.write("abcdefghij\r\nklmnopqrst".toByteArray())
        e.selectWord(3, 0)
        e.moveSelection(SelectionEdge.START, 3, 0)
        e.moveSelection(SelectionEdge.END, 5, 0)
        assertEquals("def", e.selectionText())
        assertEquals(SelectionEdge.END, e.moveSelection(SelectionEdge.END, 2, 1))
        assertEquals("defghij\nklm", e.selectionText())
        // The start dragged past the end becomes the end; the old end stays put.
        assertEquals(SelectionEdge.END, e.moveSelection(SelectionEdge.START, 5, 1))
        assertEquals("mnop", e.selectionText())
    }

    @Test
    fun selectionFollowsOutputOffTheScreen() = TerminalEngine(20, 3).use { e ->
        e.write("first\r\nsecond\r\nthird".toByteArray())
        e.selectWord(1, 0)
        e.write("\r\nfourth\r\nfifth".toByteArray())
        assertEquals(Cell(0, -2), e.selectionBounds()?.start)
        assertEquals("first", e.selectionText())
        e.scroll(-2)
        assertEquals(Cell(0, 0), e.selectionBounds()?.start)
    }

    @Test
    fun copyJoinsWrappedLinesAndTrimsSpaces() = TerminalEngine(10, 4).use { e ->
        e.write("0123456789abcde   \r\nnext".toByteArray())
        e.selectScreen()
        assertEquals("0123456789abcde\nnext", e.selectionText())
        e.clearSelection()
        assertEquals("0123456789abcde\nnext", e.screenText())
        assertNull(e.selectionText())
    }

    @Test
    fun aSnapshotClearsTheSelection() = TerminalEngine(20, 4).use { e ->
        e.write("hello".toByteArray())
        e.selectWord(1, 0)
        e.replace(20, 4, "fresh".toByteArray())
        assertNull(e.selectionBounds())
        assertNull(e.selectionText())
    }
}
