import CoreGraphics
import CoreText
import Foundation

/// The fonts and cell size a pane draws with.
public struct TerminalFonts {
    public let regular: CTFont, bold: CTFont, italic: CTFont, boldItalic: CTFont
    public let cellWidth: CGFloat, cellHeight: CGFloat
    /// From a row's top to its baseline.
    public let baseline: CGFloat
    /// The backing scale, to snap block elements to device pixels.
    public let scale: CGFloat

    public init(regular: CTFont, bold: CTFont, italic: CTFont, boldItalic: CTFont,
                cellWidth: CGFloat, cellHeight: CGFloat, baseline: CGFloat, scale: CGFloat) {
        self.regular = regular
        self.bold = bold
        self.italic = italic
        self.boldItalic = boldItalic
        self.cellWidth = cellWidth
        self.cellHeight = cellHeight
        self.baseline = baseline
        self.scale = scale
    }

    func font(_ i: Int) -> CTFont { [regular, bold, italic, boldItalic][i] }

    static func index(_ cell: TerminalCell) -> Int { (cell.bold ? 1 : 0) + (cell.italic ? 2 : 0) }
}

/// What a row's colours resolve against: the screen's default colours and
/// the selection's.
public struct TerminalPalette: Equatable {
    public var foreground: RGB, background: RGB
    public var selection: RGB, selectionForeground: RGB?

    public init(foreground: RGB, background: RGB, selection: RGB, selectionForeground: RGB?) {
        self.foreground = foreground
        self.background = background
        self.selection = selection
        self.selectionForeground = selectionForeground
    }

    /// A cell's background, nil for the screen's own.
    public func background(of c: TerminalCell) -> RGB? {
        if c.selected { return selection }
        if c.inverse { return c.fg ?? foreground }
        return c.bg
    }

    /// A cell's text colour.
    public func foreground(of c: TerminalCell) -> TerminalColor {
        var fg = c.inverse ? (c.bg ?? background) : (c.fg ?? foreground)
        if c.selected { fg = selectionForeground ?? (c.fg == nil ? foreground : fg) }
        return TerminalColor(fg, alpha: c.faint ? 0.6 : 1)
    }
}

public struct TerminalColor: Hashable, Sendable {
    public var rgb: RGB
    public var alpha: CGFloat
    public init(_ rgb: RGB, alpha: CGFloat = 1) { self.rgb = rgb; self.alpha = alpha }
}

/// A row laid out to draw: everything in it placed, in the row's own space
/// (origin at its top left, inside the pane's padding; y down). Both
/// renderers draw from it, so what text looks like is decided once.
public struct TerminalLine {
    public struct Background { public var x: CGFloat, width: CGFloat, color: RGB }
    /// Glyphs drawn as they are: positions in a space flipped about the
    /// baseline (y up), as CTFontDrawGlyphs wants after scaleBy(1, -1).
    public struct Glyphs { public var font: CTFont, color: TerminalColor, glyphs: [CGGlyph], positions: [CGPoint] }
    /// One glyph stretched or shrunk to its cell (box drawing, powerline,
    /// Nerd Font icons): draw it at the origin after concatenating
    /// `transform` and then scaleBy(1, -1).
    public struct Fitted { public var font: CTFont, color: TerminalColor, glyph: CGGlyph, transform: CGAffineTransform }
    /// A filled rectangle: block elements (blended), underlines and
    /// strikethroughs (copied, as NSRectFill does).
    public struct Rect { public var rect: CGRect, color: TerminalColor, copy: Bool }
    public enum Text { case glyphs(Glyphs), fitted(Fitted) }

    public var backgrounds: [Background] = []
    /// Drawn before the text.
    public var rects: [Rect] = []
    /// In order.
    public var text: [Text] = []
}

/// Lays out rows of cells with CoreText, and keeps the layouts of rows it
/// has seen (by their content, so a line that scrolls keeps its layout).
/// Shaping a row once with only its fonts as attributes, colours looked up
/// per glyph afterwards, is most of what makes it faster than drawing an
/// attributed string per colour run (docs/design/metal-renderer.md).
public final class TerminalLineLayout {
    public private(set) var fonts: TerminalFonts
    public private(set) var palette: TerminalPalette
    private var fontAttributes: [CFDictionary] = []
    /// Keyed by a cheap hash of the row (`key`), checked against the row
    /// itself on a hit: Swift's Hasher over every cell's String cost a flood
    /// (every row new) as much as shaping. Two generations: a hit in the old
    /// one moves to the new one, and when the new one is full it becomes the
    /// old one.
    private struct Entry { var row: [TerminalCell]; var line: TerminalLine }
    private var cache: [UInt64: Entry] = [:]
    private var older: [UInt64: Entry] = [:]
    public var capacity = 512
    public private(set) var hits = 0, misses = 0

    public init(fonts: TerminalFonts, palette: TerminalPalette) {
        self.fonts = fonts
        self.palette = palette
        setFonts(fonts)
    }

    /// Fonts or cell size changed: everything is laid out again.
    public func setFonts(_ f: TerminalFonts) {
        fonts = f
        fontAttributes = (0..<4).map {
            [kCTFontAttributeName as String: f.font($0), kCTLigatureAttributeName as String: 0] as NSDictionary as CFDictionary
        }
        clear()
    }

    public func setPalette(_ p: TerminalPalette) {
        guard p != palette else { return }
        palette = p
        clear()
    }

    public func clear() {
        cache = Dictionary(minimumCapacity: capacity)
        older = [:]
    }

    /// A whole row, from the cache if it's been seen.
    public func line(_ row: [TerminalCell]) -> TerminalLine {
        let k = Self.key(row)
        if let e = cache[k], e.row == row { hits += 1; return e.line }
        if let e = older.removeValue(forKey: k), e.row == row {
            hits += 1
            store(k, e)
            return e.line
        }
        misses += 1
        let l = layout(row.indices.map { ($0, row[$0]) }, row: row)
        store(k, Entry(row: row, line: l))
        return l
    }

    private func store(_ k: UInt64, _ e: Entry) {
        if cache.count >= capacity {
            older = cache
            cache = Dictionary(minimumCapacity: capacity)
        }
        cache[k] = e
    }

    /// FNV-1a over the row's text and styles.
    static func key(_ row: [TerminalCell]) -> UInt64 {
        var h: UInt64 = 0xCBF29CE484222325
        @inline(__always) func mix(_ v: UInt64) { h = (h ^ v) &* 0x100000001B3 }
        for c in row {
            for u in c.text.utf8 { mix(UInt64(u)) }
            var bits: UInt64 = 0x80 // ends the text, so "ab","c" isn't "a","bc"
            if c.bold { bits |= 1 << 8 }
            if c.italic { bits |= 1 << 9 }
            if c.faint { bits |= 1 << 10 }
            if c.underline { bits |= 1 << 11 }
            if c.strikethrough { bits |= 1 << 12 }
            if c.inverse { bits |= 1 << 13 }
            if c.invisible { bits |= 1 << 14 }
            if c.wide { bits |= 1 << 15 }
            if c.spacer { bits |= 1 << 16 }
            if c.selected { bits |= 1 << 17 }
            if let fg = c.fg { bits |= 1 << 18 | UInt64(fg.r) << 24 | UInt64(fg.g) << 32 | UInt64(fg.b) << 40 }
            mix(bits)
            if let bg = c.bg { mix(1 << 63 | UInt64(bg.r) | UInt64(bg.g) << 8 | UInt64(bg.b) << 16) }
        }
        return h
    }

    /// Some cells of a row (the one under a block cursor), not cached.
    public func layout(_ cells: [(col: Int, cell: TerminalCell)], row: [TerminalCell]) -> TerminalLine {
        var line = TerminalLine()
        let f = fonts, cw = f.cellWidth

        // Backgrounds, merged into runs.
        var x = 0
        while x < cells.count {
            let bg = palette.background(of: cells[x].cell)
            var end = x + 1
            while end < cells.count, cells[end].col == cells[end - 1].col + 1, palette.background(of: cells[end].cell) == bg { end += 1 }
            if let bg {
                line.backgrounds.append(.init(x: CGFloat(cells[x].col) * cw, width: CGFloat(cells[end - 1].col + 1 - cells[x].col) * cw, color: bg))
            }
            x = end
        }

        // The row's text as one string: blank cells aren't drawn, but a
        // space goes where they were, between text (shaped without it,
        // "about this" becomes "aboutthis", and a font's contextual
        // alternates join glyphs across the gap). Spacers (a wide cell's
        // second half) add nothing.
        var text = ""
        var cellAt: [Int32] = []  // UTF-16 offset -> index into cells, -1 for a gap's space
        var fontRuns: [(font: Int, length: Int)] = []
        func add(_ s: String, cell: Int32, font: Int) {
            let n = s.utf16.count
            text += s
            cellAt += [Int32](repeating: cell, count: n)
            if let last = fontRuns.last, last.font == font { fontRuns[fontRuns.count - 1].length += n } else { fontRuns.append((font, n)) }
        }
        var gap = false
        for (i, (col, cell)) in cells.enumerated() {
            if cell.underline || cell.strikethrough { decorations(cell, col: col, into: &line) }
            if cell.spacer || cell.invisible || cell.text.isEmpty || cell.text == " " || block(cell, col: col, into: &line) {
                if !cell.spacer { gap = true }
                continue
            }
            let font = TerminalFonts.index(cell)
            if gap, !text.isEmpty { add(" ", cell: -1, font: font) }
            gap = false
            add(cell.text, cell: Int32(i), font: font)
        }
        guard !text.isEmpty else { return line }

        let s = CFAttributedStringCreateMutable(nil, 0)!
        CFAttributedStringReplaceString(s, CFRange(), text as CFString)
        CFAttributedStringBeginEditing(s)
        var at = 0
        for r in fontRuns {
            CFAttributedStringSetAttributes(s, CFRange(location: at, length: r.length), fontAttributes[r.font], true)
            at += r.length
        }
        CFAttributedStringEndEditing(s)
        let ctLine = CTLineCreateWithAttributedString(s)
        let baseline = f.baseline
        for run in CTLineGetGlyphRuns(ctLine) as! [CTRun] {
            let n = CTRunGetGlyphCount(run)
            guard n > 0 else { continue }
            let runFont = (CTRunGetAttributes(run) as NSDictionary)[kCTFontAttributeName] as! CTFont
            var glyphs = [CGGlyph](repeating: 0, count: n)
            var positions = [CGPoint](repeating: .zero, count: n)
            var indices = [CFIndex](repeating: 0, count: n)
            CTRunGetGlyphs(run, CFRange(), &glyphs)
            CTRunGetPositions(run, CFRange(), &positions)
            CTRunGetStringIndices(run, CFRange(), &indices)
            // Glyphs are placed by column rather than by CoreText's
            // advances, which drift off the grid with fallback fonts and
            // fractional widths. Combining marks keep their offset from
            // their cluster's first glyph.
            var clusterStart: [Int: CGFloat] = [:]
            var plain: [TerminalLine.Glyphs] = []
            for g in 0..<n {
                let ci = Int(cellAt[min(indices[g], cellAt.count - 1)])
                guard ci >= 0 else { continue } // a gap's space: nothing to draw
                let (col, cell) = cells[ci]
                let startX = clusterStart[col] ?? positions[g].x
                clusterStart[col] = startX
                let x = CGFloat(col) * cw + (positions[g].x - startX)
                let color = palette.foreground(of: cell)
                if let fit = symbolFit(glyph: glyphs[g], font: runFont, col: col, row: row, x: x) {
                    line.text.append(.fitted(.init(font: runFont, color: color, glyph: glyphs[g], transform: fit)))
                } else if let last = plain.last, last.color == color {
                    plain[plain.count - 1].glyphs.append(glyphs[g])
                    plain[plain.count - 1].positions.append(CGPoint(x: x, y: -baseline))
                } else {
                    plain.append(.init(font: runFont, color: color, glyphs: [glyphs[g]], positions: [CGPoint(x: x, y: -baseline)]))
                }
            }
            line.text += plain.map { .glyphs($0) }
        }
        return line
    }

    /// Nerd Font symbols (Private Use Area) are often wider than a cell.
    /// Powerline separators are stretched to fill their cell exactly so
    /// segments join seamlessly; other icons shrink to the space they have
    /// (two cells when followed by a blank, as Ghostty does). Box drawing
    /// spans the cell's height. nil = draw as is.
    private func symbolFit(glyph: CGGlyph, font: CTFont, col: Int, row: [TerminalCell], x: CGFloat) -> CGAffineTransform? {
        guard col < row.count, let scalar = row[col].text.unicodeScalars.first else { return nil }
        let cw = fonts.cellWidth, ch = fonts.cellHeight
        if (0x2500...0x257F).contains(scalar.value) || (0x1FB00...0x1FBFF).contains(scalar.value) {
            // Designed to span the font's line height; stretched to the cell
            // so lines join across rows (the cell is taller than the font by
            // the line spacing).
            let a = CTFontGetAscent(font), d = CTFontGetDescent(font)
            let sy = ch / (a + d)
            return CGAffineTransform(translationX: x, y: a * sy).scaledBy(x: 1, y: sy)
        }
        guard (0xE000...0xF8FF).contains(scalar.value) || scalar.value >= 0xF0000 else { return nil }
        var g = glyph
        var b = CGRect.zero
        CTFontGetBoundingRectsForGlyphs(font, .horizontal, &g, &b, 1)
        guard b.width > 0, b.height > 0 else { return nil }
        let left = CGFloat(col) * cw
        if (0xE0B0...0xE0D7).contains(scalar.value) {
            // Map the glyph's bounds onto the cell.
            let sx = cw / b.width, sy = ch / b.height
            return CGAffineTransform(translationX: left - b.minX * sx, y: b.maxY * sy).scaledBy(x: sx, y: sy)
        }
        let nextBlank = col + 1 < row.count && (row[col + 1].text.isEmpty || row[col + 1].text == " ")
        let room = cw * (row[col].wide || nextBlank ? 2 : 1)
        guard b.maxX > room + 0.5 else { return nil }
        let s = room / b.maxX
        return CGAffineTransform(translationX: x, y: fonts.baseline + (b.midY * s - b.midY)).scaledBy(x: s, y: s)
    }

    /// Block elements (U+2580–259F) as rectangles snapped to device pixels, as
    /// Ghostty draws them: glyphs leave anti-aliased seams where cells meet
    /// (the cell width isn't a whole number of pixels), striping block art.
    /// Rows start on whole points, so snapping in the row's space is snapping
    /// on screen.
    private func block(_ cell: TerminalCell, col: Int, into line: inout TerminalLine) -> Bool {
        guard cell.text.unicodeScalars.count == 1, let v = cell.text.unicodeScalars.first?.value, (0x2580...0x259F).contains(v) else { return false }
        let cw = fonts.cellWidth, ch = fonts.cellHeight, scale = fonts.scale
        let x0 = CGFloat(col) * cw
        let fg = palette.foreground(of: cell)
        func snap(_ x: CGFloat) -> CGFloat { (x * scale).rounded() / scale }
        // A rectangle in fractions of the cell, y from the top.
        func fill(_ fx: CGFloat, _ fy: CGFloat, _ fw: CGFloat, _ fh: CGFloat, alpha: CGFloat = 1) {
            let l = snap(x0 + fx * cw), r = snap(x0 + (fx + fw) * cw)
            let t = snap(fy * ch), b = snap((fy + fh) * ch)
            line.rects.append(.init(rect: CGRect(x: l, y: t, width: r - l, height: b - t),
                                    color: TerminalColor(fg.rgb, alpha: fg.alpha * alpha), copy: false))
        }
        func quadrants(_ q: Int) { // 1 upper left, 2 upper right, 4 lower left, 8 lower right
            if q & 1 != 0 { fill(0, 0, 0.5, 0.5) }
            if q & 2 != 0 { fill(0.5, 0, 0.5, 0.5) }
            if q & 4 != 0 { fill(0, 0.5, 0.5, 0.5) }
            if q & 8 != 0 { fill(0.5, 0.5, 0.5, 0.5) }
        }
        switch v {
        case 0x2580: fill(0, 0, 1, 0.5)
        case 0x2581...0x2587: let n = CGFloat(v - 0x2580) / 8; fill(0, 1 - n, 1, n) // lower eighths
        case 0x2588: fill(0, 0, 1, 1)
        case 0x2589...0x258F: fill(0, 0, CGFloat(0x2590 - v) / 8, 1) // left eighths
        case 0x2590: fill(0.5, 0, 0.5, 1)
        case 0x2591: fill(0, 0, 1, 1, alpha: 0.25)
        case 0x2592: fill(0, 0, 1, 1, alpha: 0.5)
        case 0x2593: fill(0, 0, 1, 1, alpha: 0.75)
        case 0x2594: fill(0, 0, 1, 1.0 / 8)
        case 0x2595: fill(7.0 / 8, 0, 1.0 / 8, 1)
        default:
            let q: [UInt32: Int] = [0x2596: 4, 0x2597: 8, 0x2598: 1, 0x2599: 13, 0x259A: 9, 0x259B: 7, 0x259C: 11, 0x259D: 2, 0x259E: 6, 0x259F: 14]
            quadrants(q[v] ?? 0)
        }
        return true
    }

    private func decorations(_ cell: TerminalCell, col: Int, into line: inout TerminalLine) {
        guard !cell.invisible else { return }
        let font = cell.bold ? fonts.bold : fonts.regular
        let fg = palette.foreground(of: cell)
        let x = CGFloat(col) * fonts.cellWidth
        let w = fonts.cellWidth * (cell.wide ? 2 : 1)
        let thickness = max(1, CTFontGetUnderlineThickness(font))
        if cell.underline {
            line.rects.append(.init(rect: CGRect(x: x, y: fonts.baseline - CTFontGetUnderlinePosition(font), width: w, height: thickness), color: fg, copy: true))
        }
        if cell.strikethrough {
            line.rects.append(.init(rect: CGRect(x: x, y: fonts.baseline - CTFontGetXHeight(font) / 2, width: w, height: thickness), color: fg, copy: true))
        }
    }
}
