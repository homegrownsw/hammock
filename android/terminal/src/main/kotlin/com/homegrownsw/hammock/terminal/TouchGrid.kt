package com.homegrownsw.hammock.terminal

import kotlin.math.floor
import kotlin.math.roundToInt

/** A cell by column and row; in the viewport, a row can be off the screen (negative above it). */
data class Cell(val x: Int, val y: Int)

/** A selection's two ends, and its two handles. */
enum class SelectionEdge { START, END }

/** A selection's first and last cells. */
data class SelectionBounds(val start: Cell, val end: Cell)

/** A rectangle in the view, in pixels. */
data class Area(val left: Float, val top: Float, val right: Float, val bottom: Float)

/**
 * Where the grid was drawn in the view, in pixels: turns a touch into a
 * cell, and a selection into the places its handles and its menu go.
 * TerminalTouchGrid.swift is the iPhone's.
 */
data class TouchGrid(val originX: Float, val originY: Float, val cellWidth: Float, val cellHeight: Float, val cols: Int, val rows: Int) {
    private val cw get() = cellWidth.coerceAtLeast(1f)
    private val ch get() = cellHeight.coerceAtLeast(1f)
    private val lastCol get() = cols.coerceAtLeast(1) - 1
    private val lastRow get() = rows.coerceAtLeast(1) - 1

    /** The row a point is on, unclamped: negative above the grid, [rows] or more below it. */
    fun row(y: Float): Int = floor((y - originY) / ch).toInt()

    /** The cell under a point, held to the grid. */
    fun cell(x: Float, y: Float): Cell = Cell(floor((x - originX) / cw).toInt().coerceIn(0, lastCol), row(y).coerceIn(0, lastRow))

    /**
     * The cell a handle's caret at a point takes in: the start's caret is
     * on a cell's left edge, the end's on its right, so each goes to the
     * nearest gap between cells. Past a row's end, the start goes on to the
     * next row's first cell; before its first, the end back to the last
     * row's last. Held to the grid.
     */
    fun handleCell(x: Float, y: Float, edge: SelectionEdge): Cell {
        val gap = ((x - originX) / cw).roundToInt()
        var r = row(y).coerceIn(0, lastRow)
        var c = if (edge == SelectionEdge.START) gap else gap - 1
        if (c > lastCol) {
            if (r < lastRow) { c = 0; r++ } else c = lastCol
        } else if (c < 0) {
            if (r > 0) { c = lastCol; r-- } else c = 0
        }
        return Cell(c, r)
    }

    /** Where a handle's caret goes: x, and the row's top and bottom (left of the first cell, right of the last). */
    fun caretX(cell: Cell, edge: SelectionEdge): Float = originX + (if (edge == SelectionEdge.START) cell.x else cell.x + 1) * cw
    fun rowTop(row: Int): Float = originY + row * ch
    fun rowBottom(row: Int): Float = originY + (row + 1) * ch

    /** Whether a cell's row is on the screen. */
    fun shows(cell: Cell) = cell.y in 0..lastRow

    /** What a selection covers on the screen (whole rows wide over several), or null when none of it shows. */
    fun rect(start: Cell, end: Cell): Area? {
        val top = maxOf(start.y, 0)
        val bottom = minOf(end.y, lastRow)
        if (top > bottom) return null
        val oneRow = start.y == end.y
        val left = if (oneRow) start.x else 0
        val right = if (oneRow) end.x + 1 else cols
        return Area(originX + left * cw, rowTop(top), originX + right * cw, rowBottom(bottom))
    }

    /** How far past the grid's top or bottom a point is, in rows (negative above, 0 on it): selecting there scrolls. */
    fun overscroll(y: Float): Int {
        val r = row(y)
        return if (r < 0) r else if (r > lastRow) r - lastRow else 0
    }
}
