package com.homegrownsw.hammock.terminal

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Test

/** Touch selection's geometry, as TouchSelectionTests.swift checks the iPhone's. */
class TouchGridTest {
    // Cells 10 px wide and 20 tall, the grid 4 px in from the view's corner.
    private val grid = TouchGrid(4f, 4f, 10f, 20f, cols = 8, rows = 3)

    @Test
    fun touchesMapToCells() {
        assertEquals(Cell(0, 0), grid.cell(4f, 4f))
        assertEquals(Cell(1, 1), grid.cell(23.9f, 24f))
        // Held to the grid.
        assertEquals(Cell(0, 0), grid.cell(-50f, -50f))
        assertEquals(Cell(7, 2), grid.cell(500f, 500f))
        assertEquals(-2, grid.row(-20f))
        assertEquals(3, grid.row(64f))
    }

    @Test
    fun handlesGoToTheNearestGap() {
        // x = 4 + 10 × 2 = 24 is the gap between cells 1 and 2.
        assertEquals(Cell(2, 0), grid.handleCell(27f, 10f, SelectionEdge.START))
        assertEquals(Cell(1, 0), grid.handleCell(27f, 10f, SelectionEdge.END))
        assertEquals(Cell(2, 0), grid.handleCell(30f, 10f, SelectionEdge.END))
        // An end before a row's first cell is the last of the row above; a
        // start past a row's end, the first of the row below.
        assertEquals(Cell(7, 0), grid.handleCell(4f, 30f, SelectionEdge.END))
        assertEquals(Cell(0, 2), grid.handleCell(84f, 30f, SelectionEdge.START))
        // At the grid's corners they stay on it.
        assertEquals(Cell(0, 0), grid.handleCell(0f, 0f, SelectionEdge.END))
        assertEquals(Cell(7, 2), grid.handleCell(200f, 200f, SelectionEdge.START))
    }

    @Test
    fun caretsAndMenuArea() {
        assertEquals(24f, grid.caretX(Cell(2, 1), SelectionEdge.START))
        assertEquals(34f, grid.caretX(Cell(2, 1), SelectionEdge.END))
        assertEquals(24f, grid.rowTop(1))
        assertEquals(44f, grid.rowBottom(1))
        assertEquals(Area(24f, 24f, 54f, 44f), grid.rect(Cell(2, 1), Cell(4, 1)))
        // Over several rows it takes them whole, and only what shows.
        assertEquals(Area(4f, 4f, 84f, 44f), grid.rect(Cell(5, -4), Cell(1, 1)))
        assertNull(grid.rect(Cell(0, -4), Cell(1, -1)))
        assertFalse(grid.shows(Cell(0, 3)))
        assertEquals(-2, grid.overscroll(-30f))
        assertEquals(0, grid.overscroll(30f))
        assertEquals(1, grid.overscroll(70f))
    }

    @Test
    fun handleKnobsHangUnderTheirEnds() {
        val start = SelectionHandle(SelectionEdge.START, x = 100f, top = 20f, bottom = 40f)
        val end = SelectionHandle(SelectionEdge.END, x = 100f, top = 20f, bottom = 40f)
        // At density 2, the knob's radius is 22 px.
        assertEquals(78f, start.knobX(2f))
        assertEquals(122f, end.knobX(2f))
        assertEquals(62f, start.knobY(2f))
    }
}
