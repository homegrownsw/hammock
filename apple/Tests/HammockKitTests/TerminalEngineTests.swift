import Foundation
import Testing
@testable import HammockKit

@Test func writesAndRendersStyledText() {
    let t = TerminalEngine(cols: 20, rows: 4)
    t.write(Data("hi \u{1b}[1;31mred\u{1b}[0m\r\n日本".utf8))
    let f = t.frame()
    #expect(f.cols == 20 && f.rows == 4)
    #expect(t.text().hasPrefix("hi red\n日本"))
    let r = f.cells[0][3]
    #expect(r.text == "r" && r.bold && r.fg != nil)
    #expect(f.cells[1][0].wide && f.cells[1][1].spacer)
    #expect(f.cursorY == 1 && f.cursorX == 4)
}

@Test func encodesKeysWithModes() {
    let t = TerminalEngine(cols: 20, rows: 4)
    #expect(t.encodeKey(macKeyCode: 0x7E, mods: [], text: nil, unshifted: 0) == Data("\u{1b}[A".utf8))
    t.write(Data("\u{1b}[?1h".utf8)) // application cursor keys
    #expect(t.encodeKey(macKeyCode: 0x7E, mods: [], text: nil, unshifted: 0) == Data("\u{1b}OA".utf8))
    #expect(t.encodeKey(macKeyCode: 0x08, mods: .ctrl, text: "c", unshifted: 99) == Data([3]))
    #expect(t.encodeKey(macKeyCode: 0x00, mods: [], text: "a", unshifted: 97) == Data("a".utf8))
}

@Test func bracketsPasteWhenRequested() {
    let t = TerminalEngine(cols: 20, rows: 4)
    #expect(t.encodePaste("x") == Data("x".utf8))
    t.write(Data("\u{1b}[?2004h".utf8))
    #expect(t.encodePaste("x") == Data("\u{1b}[200~x\u{1b}[201~".utf8))
}

@Test func selectsAndCopiesText() {
    let t = TerminalEngine(cols: 20, rows: 4)
    t.write(Data("hello world\r\nsecond".utf8))
    #expect(t.selectionText() == nil)
    t.select(fromX: 6, fromY: 0, toX: 2, toY: 1)
    #expect(t.selectionText() == "world\nsec")
    #expect(t.frame().cells[0][6].selected)
    t.clearSelection()
    #expect(t.selectionText() == nil)
}

@Test func encodesMouseOnlyWhenTracking() {
    let t = TerminalEngine(cols: 20, rows: 4)
    let g = MouseGeometry(width: 200, height: 80, cellWidth: 10, cellHeight: 20, padding: 0)
    #expect(!t.mouseTracking)
    #expect(t.encodeMouse(.press, button: .wheelUp, x: 15, y: 25, mods: [], geometry: g) == nil)
    t.write(Data("\u{1b}[?1000h\u{1b}[?1006h".utf8)) // normal tracking, SGR format
    #expect(t.mouseTracking)
    let d = t.encodeMouse(.press, button: .left, x: 15, y: 25, mods: [], geometry: g)
    #expect(d == Data("\u{1b}[<0;2;2M".utf8))
    t.write(Data("\u{1b}[?1049h".utf8))
    #expect(t.isAlternateScreen)
}

@Test func reportsDragMotionInButtonMode() {
    let t = TerminalEngine(cols: 20, rows: 4)
    let g = MouseGeometry(width: 200, height: 80, cellWidth: 10, cellHeight: 20, padding: 0)
    t.write(Data("\u{1b}[?1002h\u{1b}[?1006h".utf8)) // button-event tracking, SGR
    #expect(t.encodeMouse(.press, button: .left, x: 5, y: 5, mods: [], geometry: g) != nil)
    let drag = t.encodeMouse(.motion, button: .left, x: 55, y: 25, mods: [], geometry: g)
    #expect(drag == Data("\u{1b}[<32;6;2M".utf8))
    #expect(t.encodeMouse(.release, button: .left, x: 55, y: 25, mods: [], geometry: g) != nil)
}

// Hover: plain movement (no button) goes to a program tracking every
// movement (1003), and not to one tracking only clicks (1000).
@Test func encodesHoverOnlyWhenAsked() {
    let g = MouseGeometry(width: 200, height: 80, cellWidth: 10, cellHeight: 20, padding: 0)
    let any = TerminalEngine(cols: 20, rows: 4)
    any.write(Data("\u{1b}[?1003h\u{1b}[?1006h".utf8))
    #expect(any.encodeMouse(.motion, button: nil, x: 55, y: 25, mods: [], geometry: g) == Data("\u{1b}[<35;6;2M".utf8))
    let clicks = TerminalEngine(cols: 20, rows: 4)
    clicks.write(Data("\u{1b}[?1000h\u{1b}[?1006h".utf8))
    #expect(clicks.encodeMouse(.motion, button: nil, x: 55, y: 25, mods: [], geometry: g) == nil)
}

// Fractional cells (7.5pt, as a 13pt font gives): a click far along a long
// line reports the cell under it, not one several to the right.
@Test func encodesMouseOnFractionalCells() {
    let t = TerminalEngine(cols: 120, rows: 4)
    t.write(Data("\u{1b}[?1000h\u{1b}[?1006h".utf8))
    let g = MouseGeometry(width: 6 * 2 + 120 * 7.5, height: 6 * 2 + 4 * 17, cellWidth: 7.5, cellHeight: 17, padding: 6, scale: 2)
    // Column 100 (1-based 101) spans 6 + 100 × 7.5 = 756 to 763.5; row 2 spans 40 to 57.
    let d = t.encodeMouse(.press, button: .left, x: 760, y: 45, mods: [], geometry: g)
    #expect(d == Data("\u{1b}[<0;101;3M".utf8))
}

// Cells 10 px wide and 20 px tall, no padding.
private let selGeom = TerminalEngine.SelectionGeometry(cellWidth: 10, cellHeight: 20, padding: 0, height: 80)

private func drag(_ t: TerminalEngine, from a: CGPoint, to b: CGPoint, at time: TimeInterval = 1) {
    t.selectionGesture(.press, at: a, time: time, geometry: selGeom, repeatInterval: 0.5)
    t.selectionGesture(.drag, at: b, time: time, geometry: selGeom, repeatInterval: 0.5)
    t.selectionGesture(.release, at: b, time: time, geometry: selGeom, repeatInterval: 0.5)
}

@Test func dragSelectsCellsPastTheirMiddle() {
    let t = TerminalEngine(cols: 20, rows: 4)
    t.write(Data("abcdefgh".utf8))
    // From the right half of "c" to the right half of "f": "c" is left out
    // (the pointer was past its middle), "f" is in.
    drag(t, from: CGPoint(x: 27, y: 5), to: CGPoint(x: 57, y: 5))
    #expect(t.selectionText() == "def")
    // From the left half of "c" to the left half of "f": the reverse.
    drag(t, from: CGPoint(x: 22, y: 5), to: CGPoint(x: 52, y: 5), at: 5)
    #expect(t.selectionText() == "cde")
    // A click, or a wiggle inside one half-cell, selects nothing.
    drag(t, from: CGPoint(x: 22, y: 5), to: CGPoint(x: 24, y: 5), at: 9)
    #expect(t.selectionText() == nil)
}

@Test func dragAnchorFollowsScrollingOutput() {
    let t = TerminalEngine(cols: 20, rows: 4)
    t.write(Data("one\r\ntwo\r\nthree\r\nfour".utf8))
    t.selectionGesture(.press, at: CGPoint(x: 1, y: 25), time: 1, geometry: selGeom, repeatInterval: 0.5) // "two"
    // Output arrives mid-drag and scrolls everything up two rows.
    t.write(Data("\r\nfive\r\nsix".utf8))
    // Drag to the end of what is now the second row ("four").
    t.selectionGesture(.drag, at: CGPoint(x: 39, y: 25), time: 1, geometry: selGeom, repeatInterval: 0.5)
    #expect(t.selectionText() == "two\nthree\nfour")
}

@Test func doubleAndTripleClickSelectWordAndLine() {
    let t = TerminalEngine(cols: 20, rows: 4)
    t.write(Data("hello big world".utf8))
    let p = CGPoint(x: 75, y: 5) // in "big"
    for (i, want) in ["", "big", "hello big world"].enumerated() {
        t.selectionGesture(.press, at: p, time: 1 + Double(i) * 0.1, geometry: selGeom, repeatInterval: 0.5)
        t.selectionGesture(.release, at: p, time: 1 + Double(i) * 0.1, geometry: selGeom, repeatInterval: 0.5)
        #expect(t.selectionText() == (want.isEmpty ? nil : want))
    }
}

@Test func shiftedTextStaysTextUnderKittyDisambiguate() {
    let t = TerminalEngine(cols: 20, rows: 4)
    t.write(Data("\u{1b}[>1u".utf8)) // what neovim asks for
    // ⇧; is `:`, not ESC[59;2u, or neovim's command mode never opens.
    #expect(t.encodeKey(macKeyCode: 0x29, mods: .shift, text: ":", unshifted: 59) == Data(":".utf8))
    #expect(t.encodeKey(macKeyCode: 0x00, mods: .shift, text: "A", unshifted: 97) == Data("A".utf8))
    // A real chord still reports its Shift.
    #expect(t.encodeKey(macKeyCode: 0x00, mods: [.ctrl, .shift], text: "A", unshifted: 97) == Data("\u{1b}[97;6u".utf8))
}

@Test func pasteSafety() {
    #expect(TerminalEngine.pasteIsSafe(""))
    #expect(TerminalEngine.pasteIsSafe("ls -la"))
    #expect(!TerminalEngine.pasteIsSafe("ls\nrm -rf ~"))
    #expect(!TerminalEngine.pasteIsSafe("ls\r"))
    #expect(!TerminalEngine.pasteIsSafe("a\u{1b}[201~b"))
    let t = TerminalEngine(cols: 20, rows: 4)
    #expect(!t.bracketedPaste)
    t.write(Data("\u{1b}[?2004h".utf8))
    #expect(t.bracketedPaste)
}

// Links: a URL in the text (without the full stop after it), one wrapping
// onto the next row, and a program's hyperlink (OSC 8).
@Test func findsLinks() {
    let t = TerminalEngine(cols: 40, rows: 4)
    t.write(Data("see https://hammock.dev/docs. ok\r\n".utf8))
    let a = t.link(atX: 8, y: 0)
    #expect(a?.url == "https://hammock.dev/docs")
    #expect(a?.spans == [.init(row: 0, cols: 4..<28)])
    #expect(t.link(atX: 1, y: 0) == nil)

    let w = TerminalEngine(cols: 20, rows: 4)
    w.write(Data("x https://example.com/a/very/long/path end".utf8))
    let b = w.link(atX: 3, y: 1)
    #expect(b?.url == "https://example.com/a/very/long/path")
    #expect(b?.spans.map(\.row) == [0, 1])

    let h = TerminalEngine(cols: 40, rows: 4)
    h.write(Data("\u{1b}]8;;https://hammock.dev\u{1b}\\Hammock site\u{1b}]8;;\u{1b}\\ after".utf8))
    let c = h.link(atX: 2, y: 0)
    #expect(c?.url == "https://hammock.dev")
    #expect(c?.spans == [.init(row: 0, cols: 0..<12)])
}

// The Mac pane redraws only the rows output changed (#18).
@Test func reportsChangedRows() {
    let t = TerminalEngine(cols: 20, rows: 6)
    t.write(Data("one\r\ntwo\r\nthree".utf8))
    #expect(t.takeChanges().dirty == .full) // nothing drawn yet
    #expect(t.takeChanges() == TerminalChanges(dirty: .none, cursorRow: 2))

    t.write(Data("!".utf8)) // a key typed at the end of row 2
    let typed = t.takeChanges()
    #expect(typed.cursorRow == 2)
    guard case .rows(let rows) = typed.dirty else { Issue.record("expected rows, got \(typed.dirty)"); return }
    #expect(rows.contains(2) && !rows.contains(0) && !rows.contains(5))

    t.write(Data("\u{1b}[1;1H".utf8)) // the cursor moves, nothing else
    #expect(t.takeChanges().cursorRow == 0)

    t.write(Data("\r\n\r\n\r\n\r\n\r\n\r\n\r\n".utf8)) // scrolled: every row moved
    if case .rows(let rows) = t.takeChanges().dirty { #expect(rows.count == 6) }
}
