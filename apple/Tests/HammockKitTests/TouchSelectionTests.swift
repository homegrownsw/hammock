import CoreGraphics
import Foundation
import Testing
@testable import HammockKit

// Touch selection on the phones: the engine's word, handle and screen
// selections, and TerminalTouchGrid's touch-to-cell geometry.

@Test func touchAndHoldSelectsTheWord() {
    let t = TerminalEngine(cols: 20, rows: 4)
    t.write(Data("hello big world".utf8))
    #expect(t.selectWord(x: 7, y: 0))
    #expect(t.selectionText() == "big")
    #expect(t.selectionBounds()?.start == TerminalCellPoint(x: 6, y: 0))
    #expect(t.selectionBounds()?.end == TerminalCellPoint(x: 8, y: 0))
    // A blank selects nothing.
    #expect(!t.selectWord(x: 18, y: 2))
    #expect(t.selectionText() == nil)
    #expect(t.selectionBounds() == nil)
}

@Test func draggingOnFromTheWordSelectsWholeWords() {
    let t = TerminalEngine(cols: 30, rows: 4)
    t.write(Data("one two three four".utf8))
    t.selectWord(x: 9, y: 0) // "three"
    t.extendWordSelection(toX: 15, y: 0) // in "four"
    #expect(t.selectionText() == "three four")
    t.extendWordSelection(toX: 1, y: 0) // back into "one": the anchor word stays
    #expect(t.selectionText() == "one two three")
    t.extendWordSelection(toX: 10, y: 0) // inside the anchor again
    #expect(t.selectionText() == "three")
}

@Test func handlesMoveOneEndAndSwapWhenCrossed() {
    let t = TerminalEngine(cols: 20, rows: 4)
    t.write(Data("abcdefghij\r\nklmnopqrst".utf8))
    t.select(fromX: 3, fromY: 0, toX: 5, toY: 0) // "def"
    #expect(t.moveSelection(.end, toX: 2, y: 1) == .end)
    #expect(t.selectionText() == "defghij\nklm")
    #expect(t.moveSelection(.start, toX: 0, y: 0) == .start)
    #expect(t.selectionText() == "abcdefghij\nklm")
    // The start dragged past the end becomes the end; the old end stays put.
    #expect(t.moveSelection(.start, toX: 5, y: 1) == .end)
    #expect(t.selectionText() == "mnop")
    #expect(t.selectionBounds()?.start == TerminalCellPoint(x: 2, y: 1))
}

@Test func selectionBoundsGoOffTheScreenWithScrollback() {
    let t = TerminalEngine(cols: 20, rows: 3)
    t.write(Data("first\r\nsecond\r\nthird".utf8))
    t.selectWord(x: 1, y: 0) // "first"
    t.write(Data("\r\nfourth\r\nfifth".utf8)) // "first" scrolls two rows up, out of sight
    let b = t.selectionBounds()
    #expect(b?.start == TerminalCellPoint(x: 0, y: -2))
    #expect(t.selectionText() == "first")
    // Scrolled back to it, it's on the screen again.
    t.scroll(by: -2)
    #expect(t.selectionBounds()?.start == TerminalCellPoint(x: 0, y: 0))
    #expect(t.frame().cells[0][0].selected)
}

@Test func copyJoinsWrappedLinesAndTrimsSpaces() {
    let t = TerminalEngine(cols: 10, rows: 4)
    t.write(Data("0123456789abcde   \r\nnext".utf8)) // wraps after "9"
    t.selectScreen()
    #expect(t.selectionText() == "0123456789abcde\nnext")
    #expect(t.screenText() == "0123456789abcde\nnext")
}

@Test func screenTextIsWhatShows() {
    let t = TerminalEngine(cols: 10, rows: 2)
    t.write(Data("one\r\ntwo\r\nthree".utf8))
    #expect(t.screenText() == "two\nthree")
    t.scroll(by: -1)
    #expect(t.screenText() == "one\ntwo")
    // Copying the screen leaves the selection alone.
    #expect(t.selectionText() == nil)
}

// Cells 10 pt wide and 20 tall, the grid 4 pt in from the view's corner.
private let grid = TerminalTouchGrid(origin: CGPoint(x: 4, y: 4), cellWidth: 10, cellHeight: 20, cols: 8, rows: 3)

@Test func touchesMapToCells() {
    #expect(grid.cell(at: CGPoint(x: 4, y: 4)) == TerminalCellPoint(x: 0, y: 0))
    #expect(grid.cell(at: CGPoint(x: 23.9, y: 24)) == TerminalCellPoint(x: 1, y: 1))
    // Held to the grid.
    #expect(grid.cell(at: CGPoint(x: -50, y: -50)) == TerminalCellPoint(x: 0, y: 0))
    #expect(grid.cell(at: CGPoint(x: 500, y: 500)) == TerminalCellPoint(x: 7, y: 2))
    #expect(grid.row(at: -20) == -2)
    #expect(grid.row(at: 64) == 3)
}

@Test func handlesGoToTheNearestGap() {
    // x = 4 + 10 × 2 = 24 is the gap between cells 1 and 2.
    #expect(grid.handleCell(at: CGPoint(x: 27, y: 10), edge: .start) == TerminalCellPoint(x: 2, y: 0))
    #expect(grid.handleCell(at: CGPoint(x: 27, y: 10), edge: .end) == TerminalCellPoint(x: 1, y: 0))
    #expect(grid.handleCell(at: CGPoint(x: 30, y: 10), edge: .end) == TerminalCellPoint(x: 2, y: 0))
    // An end before a row's first cell is the last of the row above; a
    // start past a row's end, the first of the row below.
    #expect(grid.handleCell(at: CGPoint(x: 4, y: 30), edge: .end) == TerminalCellPoint(x: 7, y: 0))
    #expect(grid.handleCell(at: CGPoint(x: 84, y: 30), edge: .start) == TerminalCellPoint(x: 0, y: 2))
    // At the grid's corners they stay on it.
    #expect(grid.handleCell(at: CGPoint(x: 0, y: 0), edge: .end) == TerminalCellPoint(x: 0, y: 0))
    #expect(grid.handleCell(at: CGPoint(x: 200, y: 200), edge: .start) == TerminalCellPoint(x: 7, y: 2))
}

@Test func caretsAndMenuRect() {
    #expect(grid.caret(TerminalCellPoint(x: 2, y: 1), edge: .start) == CGRect(x: 24, y: 24, width: 0, height: 20))
    #expect(grid.caret(TerminalCellPoint(x: 2, y: 1), edge: .end) == CGRect(x: 34, y: 24, width: 0, height: 20))
    #expect(grid.rect(from: TerminalCellPoint(x: 2, y: 1), to: TerminalCellPoint(x: 4, y: 1)) == CGRect(x: 24, y: 24, width: 30, height: 20))
    // Over several rows it takes them whole, and only what shows.
    #expect(grid.rect(from: TerminalCellPoint(x: 5, y: -4), to: TerminalCellPoint(x: 1, y: 1)) == CGRect(x: 4, y: 4, width: 80, height: 40))
    #expect(grid.rect(from: TerminalCellPoint(x: 0, y: -4), to: TerminalCellPoint(x: 1, y: -1)) == nil)
    #expect(!grid.shows(TerminalCellPoint(x: 0, y: 3)))
    #expect(grid.overscroll(at: -30) == -2)
    #expect(grid.overscroll(at: 30) == 0)
    #expect(grid.overscroll(at: 70) == 1)
}
