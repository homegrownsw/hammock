import CoreText
import Foundation
import ImageIO
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

// TerminalLineLayout (#18 phase 1): what both renderers draw from.
private func testLayout() -> TerminalLineLayout {
    let font = CTFontCreateWithName("Menlo" as CFString, 14, nil)
    let bold = CTFontCreateCopyWithSymbolicTraits(font, 0, nil, .traitBold, .traitBold) ?? font
    let fonts = TerminalFonts(regular: font, bold: bold, italic: font, boldItalic: bold,
                              cellWidth: 8.5, cellHeight: 18, baseline: 14, scale: 2)
    return TerminalLineLayout(fonts: fonts, palette: TerminalPalette(foreground: RGB(r: 0, g: 0, b: 0), background: RGB(r: 255, g: 255, b: 255),
                                                                     selection: RGB(r: 0, g: 0, b: 255), selectionForeground: nil))
}

private func cells(_ s: String) -> [TerminalCell] { s.map { TerminalCell(text: $0 == " " ? "" : String($0)) } }

@Test func laysGlyphsOnTheGrid() {
    let l = testLayout()
    var row = cells("ab  c")
    row[4].fg = RGB(r: 255, g: 0, b: 0)
    let line = l.line(row)
    var placed: [(x: CGFloat, color: TerminalColor)] = []
    for case .glyphs(let g) in line.text { for p in g.positions { placed.append((p.x, g.color)) } }
    // Three glyphs, each at its column; no glyph for the gap's space.
    #expect(placed.map(\.x) == [0, 8.5, 34])
    #expect(placed.last?.color == TerminalColor(RGB(r: 255, g: 0, b: 0)))
}

@Test func cachesRowsByContent() {
    let l = testLayout()
    _ = l.line(cells("hello"))
    _ = l.line(cells("world"))
    _ = l.line(cells("hello"))
    #expect(l.hits == 1 && l.misses == 2)
    var bold = cells("hello")
    bold[0].bold = true
    _ = l.line(bold) // same text, another style: not the same row
    #expect(l.misses == 3)
    // A palette change lays everything out again.
    l.setPalette(TerminalPalette(foreground: RGB(r: 1, g: 1, b: 1), background: RGB(r: 255, g: 255, b: 255), selection: RGB(r: 0, g: 0, b: 255), selectionForeground: nil))
    _ = l.line(cells("hello"))
    #expect(l.misses == 4)
}

@Test func drawsBlocksAndBackgroundsAsRects() {
    let l = testLayout()
    var row = cells("█x ")
    row[1].bg = RGB(r: 9, g: 9, b: 9)
    row[2].bg = RGB(r: 9, g: 9, b: 9)
    row[1].underline = true
    let line = l.line(row)
    #expect(line.backgrounds.count == 1 && line.backgrounds[0].x == 8.5 && line.backgrounds[0].width == 17)
    #expect(line.rects.contains { !$0.copy && $0.rect == CGRect(x: 0, y: 0, width: 8.5, height: 18) }) // the full block
    #expect(line.rects.contains { $0.copy && $0.rect.minX == 8.5 }) // the underline
}

// Kitty images placed with Unicode placeholders (as Claude Code shows them)
// when the placeholder cells arrive before the image: a screen sent on
// connecting, say. The rows didn't change when the image came, but it must
// still be drawn.
@Test func placesImagesWhosePlaceholdersCameFirst() throws {
    let ctx = CGContext(data: nil, width: 8, height: 4, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.setFillColor(CGColor(srgbRed: 1, green: 0, blue: 0, alpha: 1))
    ctx.fill(CGRect(x: 0, y: 0, width: 8, height: 4))
    let png = NSMutableData()
    let dest = try #require(CGImageDestinationCreateWithData(png, "public.png" as CFString, 1, nil))
    CGImageDestinationAddImage(dest, ctx.makeImage()!, nil)
    #expect(CGImageDestinationFinalize(dest))
    let transmit = "\u{1b}_Ga=T,U=1,i=42,f=100,c=2,r=1,q=2;\((png as Data).base64EncodedString())\u{1b}\\"
    // Image 42 (in the foreground colour), row 0, columns 0 and 1.
    let cells = "\u{1b}[38;5;42m\u{10EEEE}\u{0305}\u{0305}\u{10EEEE}\u{0305}\u{030D}\u{1b}[39m\r\n"

    let first = TerminalEngine(cols: 20, rows: 4)
    first.setCellPixels(width: 10, height: 20)
    first.write(Data((transmit + cells).utf8))
    _ = first.frame()
    #expect(first.images().count == 1) // the usual order

    let late = TerminalEngine(cols: 20, rows: 4)
    late.setCellPixels(width: 10, height: 20)
    late.write(Data(cells.utf8))
    _ = late.frame()
    #expect(late.images().isEmpty)
    late.write(Data(transmit.utf8))
    _ = late.frame()
    #expect(late.images().count == 1)

    // The cell size told after the screen came (the iPhone learns it as it
    // first draws): the image is still drawn, at its size.
    let unsized = TerminalEngine(cols: 20, rows: 4)
    unsized.write(Data((transmit + cells).utf8))
    _ = unsized.frame()
    unsized.setCellPixels(width: 10, height: 20)
    _ = unsized.frame()
    // Before any cell size, nothing's placed; setting it says so, and the
    // next frame places the image (the iPhone reads the screen again then).
    let fresh = TerminalEngine(cols: 20, rows: 4)
    fresh.write(Data((transmit + cells).utf8))
    _ = fresh.frame()
    #expect(fresh.images().isEmpty)
    #expect(fresh.setCellPixels(width: 10, height: 20))
    #expect(!fresh.setCellPixels(width: 10, height: 20))
    _ = fresh.frame()
    #expect(fresh.images().count == 1)

    let placed = unsized.images(), sizedFirst = first.images()
    #expect(placed.count == 1 && placed.first?.width == sizedFirst.first?.width && placed.first?.height == sizedFirst.first?.height)
}
