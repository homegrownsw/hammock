import AppKit
import HammockKit
import SwiftUI

/// Draws a pane's TerminalEngine with CoreText and turns keyboard/mouse input
/// into PTY bytes. Purely a view: all terminal state comes from the daemon.
final class TerminalNSView: NSView {
    var session: PaneSession? {
        didSet {
            oldValue?.unobserve(self)
            session?.observe(self) { [weak self] in
                self?.blinkOn = true // solid while output arrives
                self?.redraw()
            }
            applyTheme()
            needsDisplay = true
        }
    }
    var onInput: ((Data) -> Void)?
    /// Files dropped on a pane whose Mac is elsewhere: sent there (returns
    /// true when it took them).
    var onDropFiles: (([URL]) -> Bool)?
    /// Opens a link: in the browser, or (true) a browser pane beside this one.
    var onOpenLink: ((URL, Bool) -> Void)?
    /// The link under the pointer while ⌘ is held (underlined, pointing hand).
    fileprivate(set) var hoverLink: TerminalLink?
    var onResize: ((Int, Int) -> Void)?
    /// The cell size in device pixels, for programs that ask (Kitty graphics).
    var onCellSize: ((Int, Int) -> Void)?
    var onFocus: (() -> Void)?

    private var font: NSFont
    private var boldFont: NSFont
    var cellWidth: CGFloat = 8
    var cellHeight: CGFloat = 16
    private var ascent: CGFloat = 12
    let padding: CGFloat = 6
    private var lastGrid = (cols: 0, rows: 0)
    private var scrollAccumulator: CGFloat = 0
    private var fontObserver: NSObjectProtocol?
    private var configObserver: NSObjectProtocol?
    private var refocusObserver: NSObjectProtocol?
    private var motionObserver: NSObjectProtocol?
    /// A redraw or grid report put off while panes moved (or while folded).
    private var pendingDisplay = false
    private var pendingGrid = false
    /// Folded under a zoomed pane: out of sight, so not drawn until it's back.
    var folded = false {
        didSet {
            guard folded != oldValue, !folded else { return }
            // Drawn now, before it's uncovered, even mid-animation.
            pendingDisplay = false
            needsDisplay = true
        }
    }
    /// Posted when an overlay (the command palette) closes: the pane that
    /// should have the keyboard takes it back.
    static let refocus = Notification.Name("HammockTerminalRefocus")
    private var thicken: Bool { AppConfig.shared.fontThicken }

    override init(frame: NSRect) {
        (font, boldFont) = TerminalFont.resolve()
        super.init(frame: frame)
        fontObserver = NotificationCenter.default.addObserver(forName: TerminalFont.changed, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.reloadFont() }
        }
        refocusObserver = NotificationCenter.default.addObserver(forName: TerminalNSView.refocus, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { if self?.isFocusTarget == true { self?.focusSoon() } }
        }
        configObserver = NotificationCenter.default.addObserver(forName: AppConfig.changed, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.reloadFont()
                self?.applyTheme()
            }
        }
        motionObserver = NotificationCenter.default.addObserver(forName: PaneMotion.settled, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.settle() }
        }
        measureFont()
        wantsLayer = true
        layerContentsRedrawPolicy = .onSetNeedsDisplay
        // While it's resized mid-animation, the last drawing stays put at the
        // top left, uncovering the layer's background (the theme's), rather
        // than stretching.
        layerContentsPlacement = .topLeft
        applyTheme()
        // Views don't clip to their bounds by default (macOS 14+), and draw's
        // dirty rect can reach past them: without this the background fill
        // painted over neighbouring panes and the pane headers.
        clipsToBounds = true
    }

    required init?(coder: NSCoder) { fatalError() }

    deinit {
        if let fontObserver { NotificationCenter.default.removeObserver(fontObserver) }
        if let configObserver { NotificationCenter.default.removeObserver(configObserver) }
        if let refocusObserver { NotificationCenter.default.removeObserver(refocusObserver) }
        if let motionObserver { NotificationCenter.default.removeObserver(motionObserver) }
    }

    /// Asks for a redraw, unless panes are moving or it's folded away: then
    /// once they've settled (or it's back).
    private func redraw() {
        if PaneMotion.shared.isMoving || folded { pendingDisplay = true } else { needsDisplay = true }
    }

    private func settle() {
        if pendingGrid { pendingGrid = false; reportGrid() }
        if pendingDisplay, !folded { pendingDisplay = false; needsDisplay = true }
    }

    override var isFlipped: Bool { true }
    override var acceptsFirstResponder: Bool { true }
    override var isOpaque: Bool { true }

    private func reloadFont() {
        (font, boldFont) = TerminalFont.resolve()
        measureFont()
        lastGrid = (0, 0)
        reportGrid()
        needsDisplay = true
    }

    private func measureFont() {
        let ctFont = font as CTFont
        var glyph = CGGlyph()
        var ch: UniChar = 0x4D // "M"
        CTFontGetGlyphsForCharacters(ctFont, &ch, &glyph, 1)
        var advance = CGSize.zero
        CTFontGetAdvancesForGlyphs(ctFont, .horizontal, &glyph, &advance, 1)
        cellWidth = ceil(advance.width * 2) / 2
        let natural = ceil(CTFontGetAscent(ctFont) + CTFontGetDescent(ctFont) + CTFontGetLeading(ctFont)) + 2
        // Line spacing (adjust-cell-height) is shared above and below the
        // text, so it stays centred in its row, as in Ghostty.
        let extra = (AppConfig.shared.cellHeightExtra(base: natural)).rounded()
        cellHeight = max(natural + extra, ceil(font.pointSize * 0.5))
        ascent = ceil(CTFontGetAscent(ctFont)) + ((cellHeight - natural) / 2).rounded(.down)
    }

    override func viewDidChangeEffectiveAppearance() {
        super.viewDidChangeEffectiveAppearance()
        applyTheme()
    }

    private var isDark: Bool { effectiveAppearance.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua }

    private func applyTheme() {
        let theme = AppConfig.shared.theme(isDark: isDark)
        layer?.backgroundColor = color(theme.background).cgColor
        session?.engine.theme = theme
        session?.engine.setDefaultCursor(AppConfig.shared.cursorStyle, blink: AppConfig.shared.cursorBlink)
        needsDisplay = true
    }

    // MARK: PaneLayout

    override func setFrameSize(_ newSize: NSSize) {
        let changed = newSize != frame.size
        super.setFrameSize(newSize)
        guard changed else { return }
        // Panes animating: the grid (and the program's idea of its size)
        // changes once, when they settle, not on every frame (PaneMotion).
        if PaneMotion.shared.isMoving {
            pendingGrid = true
            pendingDisplay = true
            return
        }
        redraw() // the layer only redraws on request (.onSetNeedsDisplay)
        reportGrid()
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        registerForDraggedTypes(Self.dropTypes)
        reportGrid()
        if isFocusTarget { focusSoon() }
    }

    // Blinking: the cursor blinks while focused if the terminal says so
    // (cursor-style-blink, or a program's request), and stays solid while
    // typing or output is arriving.
    private var blinkOn = true
    private var blinkWanted = false
    private var blinkTimer: Timer?

    override func viewWillMove(toWindow newWindow: NSWindow?) {
        super.viewWillMove(toWindow: newWindow)
        blinkTimer?.invalidate()
        blinkTimer = nil
        guard newWindow != nil else { return }
        blinkTimer = Timer.scheduledTimer(withTimeInterval: 0.6, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated {
                guard let self else { return }
                if self.blinkWanted { self.blinkOn.toggle(); self.redraw() } else if !self.blinkOn { self.blinkOn = true; self.redraw() }
            }
        }
    }

    /// Whether this is the pane that should have the keyboard. Focus is taken
    /// when this becomes true or the view lands in a window (a newly shown
    /// tab), never on unrelated updates, so the sidebar search box keeps it.
    var isFocusTarget = false {
        didSet { if isFocusTarget, !oldValue { focusSoon() } }
    }

    func focusSoon() {
        // After the current event: a sidebar click hands focus to the list first.
        DispatchQueue.main.async { [weak self] in
            // Not while the palette is open: it has the keyboard.
            guard let self, self.isFocusTarget, !PaletteState.shared.isOpen, let w = self.window, w.firstResponder !== self else { return }
            w.makeFirstResponder(self)
        }
    }

    /// Re-sends the grid size even if unchanged (new pane or reconnect).
    func forceGridReport() {
        lastGrid = (0, 0)
        reportGrid()
    }

    private func reportGrid() {
        guard let window else { return }
        let scale = window.backingScaleFactor
        let cw = Int((cellWidth * scale).rounded()), ch = Int((cellHeight * scale).rounded())
        session?.engine.setCellPixels(width: cw, height: ch)
        onCellSize?(cw, ch)
        let cols = max(Int((bounds.width - padding * 2) / cellWidth), 2)
        let rows = max(Int((bounds.height - padding * 2) / cellHeight), 1)
        if cols != lastGrid.cols || rows != lastGrid.rows {
            lastGrid = (cols, rows)
            onResize?(cols, rows)
        }
    }

    // MARK: Drawing

    private func color(_ c: RGB, alpha: CGFloat = 1) -> NSColor {
        NSColor(srgbRed: CGFloat(c.r) / 255, green: CGFloat(c.g) / 255, blue: CGFloat(c.b) / 255, alpha: alpha)
    }

    override func draw(_ dirtyRect: NSRect) {
        guard let session else {
            NSColor.textBackgroundColor.setFill()
            dirtyRect.fill()
            return
        }
        let theme = session.engine.theme
        guard session.ready else {
            // No snapshot yet: just the background, never an empty grid and cursor.
            color(theme.background).setFill()
            dirtyRect.fill()
            return
        }
        let f = session.engine.frame()
        color(f.background).setFill()
        dirtyRect.fill()

        // Ghostty draws without CoreGraphics font smoothing (stem darkening)
        // unless font-thicken is set; matching it keeps text as crisp.
        NSGraphicsContext.current?.cgContext.setShouldSmoothFonts(thicken)
        let focused = window?.isKeyWindow == true && window?.firstResponder === self
        // Kitty graphics, in their layers: below the background, between the
        // background and the text, above the text.
        let images = session.engine.images()
        drawImages(images, .belowBackground)
        for (y, row) in f.cells.enumerated() {
            let top = padding + CGFloat(y) * cellHeight
            if top > dirtyRect.maxY || top + cellHeight < dirtyRect.minY { continue }

            // Backgrounds, merged into runs.
            var x = 0
            while x < row.count {
                let bg = background(of: row[x], frame: f, theme: theme)
                var end = x + 1
                while end < row.count, background(of: row[end], frame: f, theme: theme) == bg { end += 1 }
                if let bg {
                    color(bg).setFill()
                    NSRect(x: padding + CGFloat(x) * cellWidth, y: top, width: CGFloat(end - x) * cellWidth, height: cellHeight).fill()
                }
                x = end
            }
        }
        drawImages(images, .belowText)
        for (y, row) in f.cells.enumerated() {
            let top = padding + CGFloat(y) * cellHeight
            if top > dirtyRect.maxY || top + cellHeight < dirtyRect.minY { continue }
            drawText(row.indices.map { ($0, row[$0]) }, row: row, top: top, frame: f)
        }
        drawImages(images, .aboveText)

        // The link under the pointer while ⌘ is held: underlined.
        if let link = hoverLink {
            color(theme.foreground).setFill()
            for span in link.spans where span.row < f.rows {
                let y = padding + CGFloat(span.row + 1) * cellHeight - 2
                NSRect(x: padding + CGFloat(span.cols.lowerBound) * cellWidth, y: y,
                       width: CGFloat(span.cols.count) * cellWidth, height: 1).fill()
            }
        }

        drawMarkedText(frame: f, theme: theme)

        // Cursor.
        blinkWanted = focused && f.cursorBlinking
        if f.cursorVisible, f.cursorY < f.cells.count, blinkOn || !blinkWanted {
            let rect = NSRect(x: padding + CGFloat(f.cursorX) * cellWidth, y: padding + CGFloat(f.cursorY) * cellHeight,
                              width: cellWidth, height: cellHeight)
            let cc = color(f.cursorColor ?? theme.cursor)
            cc.setFill()
            cc.setStroke()
            if !focused || f.cursorStyle == .hollow {
                let p = NSBezierPath(rect: rect.insetBy(dx: 0.5, dy: 0.5))
                p.lineWidth = 1
                p.stroke()
            } else {
                switch f.cursorStyle {
                case .bar: NSRect(x: rect.minX, y: rect.minY, width: AppConfig.shared.cursorThin ? 1 : 2, height: rect.height).fill()
                case .underline: NSRect(x: rect.minX, y: rect.maxY - 2, width: rect.width, height: 2).fill()
                default:
                    rect.fill()
                    let cells = f.cells[f.cursorY]
                    if f.cursorX < cells.count, !cells[f.cursorX].text.isEmpty {
                        var c = cells[f.cursorX]
                        c.fg = f.background
                        c.inverse = false
                        c.selected = false
                        drawText([(f.cursorX, c)], row: cells, top: rect.minY, frame: f)
                    }
                }
            }
        }
    }

    /// Draws Kitty graphics images in one layer. Sizes are in device pixels
    /// (the cell size the engine reports to programs is too).
    private func drawImages(_ images: [TerminalImage], _ layer: TerminalImage.Layer) {
        guard let ctx = NSGraphicsContext.current?.cgContext else { return }
        let scale = window?.backingScaleFactor ?? 2
        for img in images where img.layer == layer {
            let rect = CGRect(x: padding + CGFloat(img.col) * cellWidth + img.offsetX / scale,
                              y: padding + CGFloat(img.row) * cellHeight + img.offsetY / scale,
                              width: img.width / scale, height: img.height / scale)
            // The view is flipped; CGImages draw bottom-up.
            ctx.saveGState()
            ctx.translateBy(x: rect.minX, y: rect.maxY)
            ctx.scaleBy(x: 1, y: -1)
            ctx.interpolationQuality = .high
            ctx.draw(img.image, in: CGRect(origin: .zero, size: rect.size))
            ctx.restoreGState()
        }
    }

    /// Draws in-progress IME composition at the cursor, underlined.
    private func drawMarkedText(frame f: TerminalFrame, theme: TerminalTheme) {
        guard markedText.length > 0 else { return }
        let origin = CGPoint(x: padding + CGFloat(f.cursorX) * cellWidth, y: padding + CGFloat(f.cursorY) * cellHeight)
        let s = NSMutableAttributedString(attributedString: markedText)
        let range = NSRange(location: 0, length: s.length)
        s.addAttributes([.font: font, .foregroundColor: color(theme.foreground), .underlineStyle: NSUnderlineStyle.single.rawValue], range: range)
        let width = s.size().width
        color(theme.background).setFill()
        NSRect(x: origin.x, y: origin.y, width: width, height: cellHeight).fill()
        s.draw(with: NSRect(x: origin.x, y: origin.y + ascent + 1, width: width + cellWidth, height: cellHeight), options: [], context: nil)
    }

    private func background(of c: TerminalCell, frame f: TerminalFrame, theme: TerminalTheme) -> RGB? {
        if c.selected { return theme.selection }
        if c.inverse { return c.fg ?? f.foreground }
        return c.bg
    }

    private func sameStyle(_ a: TerminalCell, _ b: TerminalCell) -> Bool {
        a.fg == b.fg && a.bg == b.bg && a.bold == b.bold && a.italic == b.italic && a.faint == b.faint &&
            a.underline == b.underline && a.strikethrough == b.strikethrough && a.inverse == b.inverse && a.selected == b.selected
    }

    private func attributes(for cell: TerminalCell, frame f: TerminalFrame) -> [NSAttributedString.Key: Any] {
        var fg = cell.inverse ? (cell.bg ?? f.background) : (cell.fg ?? f.foreground)
        if cell.selected { fg = session?.engine.theme.selectionForeground ?? (cell.fg == nil ? f.foreground : fg) }
        var font = cell.bold ? boldFont : self.font
        if cell.italic { font = NSFontManager.shared.convert(font, toHaveTrait: .italicFontMask) }
        var attrs: [NSAttributedString.Key: Any] = [.font: font, .foregroundColor: color(fg, alpha: cell.faint ? 0.6 : 1), .ligature: 0]
        if cell.invisible { attrs[.foregroundColor] = NSColor.clear }
        return attrs
    }

    /// Draws cells' text with every glyph pinned to its cell. CoreText lays the
    /// cells out as one line (so it still picks fallback fonts for symbols and
    /// emoji), but glyphs are placed by column rather than by CoreText's
    /// advances, which drift off the grid with fallback fonts and fractional
    /// widths: enough to hide powerline separators under the next segment.
    private func drawText(_ cells: [(col: Int, cell: TerminalCell)], row: [TerminalCell], top: CGFloat, frame f: TerminalFrame) {
        guard let ctx = NSGraphicsContext.current?.cgContext else { return }
        let baseline = top + ascent + 1
        let line = NSMutableAttributedString()
        var colAt: [Int] = []  // UTF-16 offset -> column
        // Blank cells aren't drawn, but a space still goes where they were:
        // shaped without it, "about this" becomes "aboutthis", and a font's
        // contextual alternates (Maple Mono's italic "tt") join glyphs across
        // the gap and draw the second letter in the blank cell.
        var gap: Int?  // column of the first skipped cell since the last text
        var i = 0
        while i < cells.count {
            let (col, cell) = cells[i]
            if cell.underline || cell.strikethrough { drawDecorations(cell, col: col, baseline: baseline, frame: f) }
            if cell.spacer || cell.invisible || cell.text.isEmpty || cell.text == " " || drawBlock(cell, col: col, top: top, frame: f) {
                if !cell.spacer, gap == nil { gap = col }
                i += 1; continue
            }
            // Coalesce same-style cells into one attributed run.
            var text = "", cols: [Int] = []
            if let g = gap, line.length > 0 { text = " "; cols = [g] }
            gap = nil
            text += cell.text
            cols += [Int](repeating: col, count: cell.text.utf16.count)
            var j = i + 1
            while j < cells.count, sameStyle(cells[j].cell, cell) {
                let (c2, cell2) = cells[j]
                if cell2.underline || cell2.strikethrough { drawDecorations(cell2, col: c2, baseline: baseline, frame: f) }
                if cell2.spacer || cell2.invisible || cell2.text.isEmpty || cell2.text == " " || drawBlock(cell2, col: c2, top: top, frame: f) {
                    if !cell2.spacer, gap == nil { gap = c2 }
                } else {
                    if let g = gap { text += " "; cols.append(g); gap = nil }
                    text += cell2.text
                    cols += [Int](repeating: c2, count: cell2.text.utf16.count)
                }
                j += 1
            }
            line.append(NSAttributedString(string: text, attributes: attributes(for: cell, frame: f)))
            colAt += cols
            i = j
        }
        guard line.length > 0 else { return }

        ctx.saveGState()
        defer { ctx.restoreGState() }
        ctx.textMatrix = .identity
        let ctLine = CTLineCreateWithAttributedString(line)
        for run in CTLineGetGlyphRuns(ctLine) as! [CTRun] {
            let n = CTRunGetGlyphCount(run)
            guard n > 0 else { continue }
            let attrs = CTRunGetAttributes(run) as NSDictionary
            let runFont = attrs[kCTFontAttributeName] as! CTFont
            let fg = (attrs[NSAttributedString.Key.foregroundColor] as? NSColor) ?? .textColor
            ctx.setFillColor(fg.cgColor)
            var glyphs = [CGGlyph](repeating: 0, count: n)
            var positions = [CGPoint](repeating: .zero, count: n)
            var indices = [CFIndex](repeating: 0, count: n)
            CTRunGetGlyphs(run, CFRange(), &glyphs)
            CTRunGetPositions(run, CFRange(), &positions)
            CTRunGetStringIndices(run, CFRange(), &indices)
            // The view is flipped; glyphs are drawn in an unflipped space, so
            // a baseline at y lands at -y there.
            var plain: [CGGlyph] = [], at: [CGPoint] = []
            // Combining marks keep their offset from their cluster's first glyph.
            var clusterStart: [Int: CGFloat] = [:]
            for g in 0..<n {
                let col = colAt[min(indices[g], colAt.count - 1)]
                let startX = clusterStart[col] ?? positions[g].x
                clusterStart[col] = startX
                let x = padding + CGFloat(col) * cellWidth + (positions[g].x - startX)
                if let fit = symbolFit(glyph: glyphs[g], font: runFont, col: col, row: row, top: top, x: x, baseline: baseline) {
                    ctx.saveGState()
                    ctx.concatenate(fit)
                    ctx.scaleBy(x: 1, y: -1)
                    var glyph = glyphs[g]
                    var origin = CGPoint.zero
                    CTFontDrawGlyphs(runFont, &glyph, &origin, 1, ctx)
                    ctx.restoreGState()
                } else {
                    plain.append(glyphs[g])
                    at.append(CGPoint(x: x, y: -baseline))
                }
            }
            if !plain.isEmpty {
                ctx.saveGState()
                ctx.scaleBy(x: 1, y: -1)
                CTFontDrawGlyphs(runFont, plain, at, plain.count, ctx)
                ctx.restoreGState()
            }
        }
    }

    /// Nerd Font symbols (Private Use Area) are often wider than a cell.
    /// Powerline separators are stretched to fill their cell exactly so
    /// segments join seamlessly; other icons shrink to the space they have
    /// (two cells when followed by a blank, as Ghostty does). nil = draw as is.
    private func symbolFit(glyph: CGGlyph, font: CTFont, col: Int, row: [TerminalCell], top: CGFloat, x: CGFloat, baseline: CGFloat) -> CGAffineTransform? {
        guard col < row.count, let scalar = row[col].text.unicodeScalars.first else { return nil }
        if (0x2500...0x257F).contains(scalar.value) || (0x1FB00...0x1FBFF).contains(scalar.value) {
            // Box drawing and block elements are designed to span the font's
            // line height; stretch that to the cell so lines join across rows
            // (the cell is taller than the font by the line spacing).
            let a = CTFontGetAscent(font), d = CTFontGetDescent(font)
            let sy = cellHeight / (a + d)
            return CGAffineTransform(translationX: x, y: top + a * sy).scaledBy(x: 1, y: sy)
        }
        guard (0xE000...0xF8FF).contains(scalar.value) || scalar.value >= 0xF0000 else { return nil }
        var g = glyph
        var b = CGRect.zero
        CTFontGetBoundingRectsForGlyphs(font, .horizontal, &g, &b, 1)
        guard b.width > 0, b.height > 0 else { return nil }
        let left = padding + CGFloat(col) * cellWidth
        if (0xE0B0...0xE0D7).contains(scalar.value) {
            // Map the glyph's bounds onto the cell.
            let sx = cellWidth / b.width, sy = cellHeight / b.height
            return CGAffineTransform(translationX: left - b.minX * sx, y: top + b.maxY * sy).scaledBy(x: sx, y: sy)
        }
        let nextBlank = col + 1 < row.count && (row[col + 1].text.isEmpty || row[col + 1].text == " ")
        let room = cellWidth * (row[col].wide || nextBlank ? 2 : 1)
        guard b.maxX > room + 0.5 else { return nil }
        let s = room / b.maxX
        return CGAffineTransform(translationX: x, y: baseline + (b.midY * s - b.midY)).scaledBy(x: s, y: s)
    }

    /// Block elements (U+2580–259F) as rectangles snapped to device pixels, as
    /// Ghostty draws them: glyphs leave anti-aliased seams where cells meet
    /// (the cell width isn't a whole number of pixels), striping block art.
    private func drawBlock(_ cell: TerminalCell, col: Int, top: CGFloat, frame f: TerminalFrame) -> Bool {
        guard cell.text.unicodeScalars.count == 1, let v = cell.text.unicodeScalars.first?.value, (0x2580...0x259F).contains(v) else { return false }
        let scale = window?.backingScaleFactor ?? 2
        let x0 = padding + CGFloat(col) * cellWidth
        let fg = (attributes(for: cell, frame: f)[.foregroundColor] as? NSColor) ?? .textColor
        func snap(_ x: CGFloat) -> CGFloat { (x * scale).rounded() / scale }
        // A rectangle in fractions of the cell, y from the top.
        func fill(_ fx: CGFloat, _ fy: CGFloat, _ fw: CGFloat, _ fh: CGFloat, alpha: CGFloat = 1) {
            let l = snap(x0 + fx * cellWidth), r = snap(x0 + (fx + fw) * cellWidth)
            let t = snap(top + fy * cellHeight), b = snap(top + (fy + fh) * cellHeight)
            fg.withAlphaComponent(fg.alphaComponent * alpha).setFill()
            NSRect(x: l, y: t, width: r - l, height: b - t).fill(using: .sourceOver)
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

    private func drawDecorations(_ cell: TerminalCell, col: Int, baseline: CGFloat, frame f: TerminalFrame) {
        let font = cell.bold ? boldFont : self.font
        let fg = (attributes(for: cell, frame: f)[.foregroundColor] as? NSColor) ?? .textColor
        fg.setFill()
        let x = padding + CGFloat(col) * cellWidth
        let w = cellWidth * (cell.wide ? 2 : 1)
        let thickness = max(1, CTFontGetUnderlineThickness(font as CTFont))
        if cell.underline {
            NSRect(x: x, y: baseline - CTFontGetUnderlinePosition(font as CTFont), width: w, height: thickness).fill()
        }
        if cell.strikethrough {
            NSRect(x: x, y: baseline - CTFontGetXHeight(font as CTFont) / 2, width: w, height: thickness).fill()
        }
    }

    // MARK: Input

    override func becomeFirstResponder() -> Bool {
        needsDisplay = true
        onFocus?()
        return true
    }

    override func resignFirstResponder() -> Bool {
        needsDisplay = true
        return true
    }

    /// Hands a pointer event to Ghostty's selection gesture, in backing
    /// pixels (cell widths can be half points).
    private func selectionGesture(_ phase: TerminalEngine.SelectionPhase, _ event: NSEvent) {
        guard let engine = session?.engine else { return }
        let s = window?.backingScaleFactor ?? 2
        let p = convert(event.locationInWindow, from: nil)
        let g = TerminalEngine.SelectionGeometry(cellWidth: cellWidth * s, cellHeight: cellHeight * s,
                                                 padding: padding * s, height: bounds.height * s)
        engine.selectionGesture(phase, at: CGPoint(x: p.x * s, y: p.y * s), time: event.timestamp,
                                geometry: g, repeatInterval: NSEvent.doubleClickInterval)
        needsDisplay = true
    }

    override func mouseDown(with event: NSEvent) {
        window?.makeFirstResponder(self)
        guard session?.engine != nil else { return }
        // ⌘-click a link: open it (⌘⇧: in a browser pane beside this one).
        if event.modifierFlags.contains(.command), let link = link(at: event), let url = URL(string: link.url) {
            onOpenLink?(url, event.modifierFlags.contains(.shift))
            return
        }
        if reportMouse(.press, event) { return }
        selectionGesture(.press, event)
    }

    override func mouseDragged(with event: NSEvent) {
        if reportMouse(.motion, event, button: .left) { return }
        selectionGesture(.drag, event)
    }

    // Hover: programs that track every movement (mode 1003: Claude Code,
    // neovim, htop…) highlight what's under the pointer, as in Ghostty. The
    // engine only encodes a move when the program asked for it; sent when
    // the pointer reaches another cell, not on every pixel.
    private var hoverArea: NSTrackingArea?
    private var lastHoverCell: (Int, Int)?

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let hoverArea { removeTrackingArea(hoverArea) }
        let area = NSTrackingArea(rect: .zero, options: [.mouseMoved, .activeInKeyWindow, .inVisibleRect], owner: self)
        addTrackingArea(area)
        hoverArea = area
    }

    override func mouseMoved(with event: NSEvent) {
        updateHoverLink(event)
        guard let engine = session?.engine, engine.mouseTracking else { return }
        let p = convert(event.locationInWindow, from: nil)
        let cell = (Int((p.x - padding) / cellWidth), Int((p.y - padding) / cellHeight))
        guard lastHoverCell.map({ $0 != cell }) ?? true else { return }
        lastHoverCell = cell
        _ = reportMouse(.motion, event, button: nil)
    }

    override func scrollWheel(with event: NSEvent) {
        guard let engine = session?.engine else { return }
        var dy = event.scrollingDeltaY
        if !event.hasPreciseScrollingDeltas { dy *= cellHeight }
        scrollAccumulator += dy
        let lines = Int(scrollAccumulator / cellHeight)
        if lines != 0 {
            scrollAccumulator -= CGFloat(lines) * cellHeight
            if engine.mouseTracking, !event.modifierFlags.contains(.shift) {
                // The program wants wheel events.
                for _ in 0..<abs(lines) { _ = reportMouse(.press, event, button: lines > 0 ? .wheelUp : .wheelDown) }
            } else if engine.isAlternateScreen {
                // Full-screen apps without mouse support: scroll with arrow keys.
                let key: UInt16 = lines > 0 ? 0x7E : 0x7D
                for _ in 0..<abs(lines) {
                    if let d = engine.encodeKey(macKeyCode: key, mods: [], text: nil, unshifted: 0) { onInput?(d) }
                }
            } else {
                engine.scroll(by: -lines)
                needsDisplay = true
            }
        }
    }

    // Keys that never produce text go straight to the encoder; everything else
    // goes through the input system so dead keys and IMEs work.
    private static let directKeys: Set<UInt16> = [
        0x24, 0x4C, 0x30, 0x33, 0x35, 0x75, 0x73, 0x77, 0x74, 0x79, 0x72,
        0x7B, 0x7C, 0x7D, 0x7E, 0x7A, 0x78, 0x63, 0x76, 0x60, 0x61, 0x62, 0x64, 0x65, 0x6D, 0x67, 0x6F,
    ]
    private var pendingKeyEvent: NSEvent?
    var markedText = NSMutableAttributedString()

    override func keyDown(with event: NSEvent) {
        blinkOn = true
        guard session != nil else { return }
        let flags = event.modifierFlags
        if flags.contains(.command) {
            super.keyDown(with: event)
            return
        }
        // While composing (IME, dead keys) every key belongs to the input system,
        // so Return commits, Backspace edits the composition, and so on.
        if !hasMarkedText(), flags.contains(.control) || Self.directKeys.contains(event.keyCode) {
            sendKey(event, text: flags.contains(.control) ? event.charactersIgnoringModifiers : nil)
            return
        }
        pendingKeyEvent = event
        defer { pendingKeyEvent = nil }
        if inputContext?.handleEvent(event) != true {
            sendKey(event, text: event.characters)
        }
    }

    /// Encodes a key event (with the text it produced, if any) for the PTY.
    private func sendKey(_ event: NSEvent, text: String?) {
        guard let engine = session?.engine else { return }
        let flags = event.modifierFlags
        var mods: KeyMods = []
        if flags.contains(.shift) { mods.insert(.shift) }
        if flags.contains(.control) { mods.insert(.ctrl) }
        // Option that composed a character (e.g. ⌥3 → £) is consumed, not Alt.
        if flags.contains(.option), text == nil || text == event.charactersIgnoringModifiers { mods.insert(.alt) }
        var text = text
        // Drop control characters and AppKit's private-use function-key codes;
        // the encoder derives those from the key itself.
        if let t = text, let s = t.unicodeScalars.first, s.value < 0x20 || (0xF700...0xF8FF).contains(s.value) || s.value == 0x7F {
            text = nil
        }
        // The key's base character with no modifiers at all (charactersIgnoringModifiers
        // still applies Shift), for the kitty keyboard protocol.
        var unshifted = event.characters(byApplyingModifiers: [])?.unicodeScalars.first?.value ?? 0
        if (0xF700...0xF8FF).contains(unshifted) || unshifted < 0x20 { unshifted = 0 }
        if let data = engine.encodeKey(macKeyCode: event.keyCode, mods: mods, text: text, unshifted: unshifted, isRepeat: event.isARepeat) {
            engine.scrollToBottom()
            engine.clearSelection()
            onInput?(data)
        }
    }

    // MARK: Drag and drop

    // Dropping files types their paths, shell-escaped, as a paste (as
    // Ghostty does): Claude Code turns a pasted image path into [Image #n].
    // URLs and text drop as themselves.
    private static let dropTypes: [NSPasteboard.PasteboardType] = [.fileURL, .URL, .string]

    override func draggingEntered(_ sender: NSDraggingInfo) -> NSDragOperation {
        sender.draggingPasteboard.availableType(from: Self.dropTypes) == nil ? [] : .copy
    }

    override func draggingUpdated(_ sender: NSDraggingInfo) -> NSDragOperation { draggingEntered(sender) }

    override func performDragOperation(_ sender: NSDraggingInfo) -> Bool {
        let pb = sender.draggingPasteboard
        var text = ""
        if let urls = pb.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL], !urls.isEmpty {
            if onDropFiles?(urls) == true {
                window?.makeFirstResponder(self)
                return true
            }
            text = urls.map { Self.shellEscape($0.path) }.joined(separator: " ")
        } else if let url = pb.readObjects(forClasses: [NSURL.self]) as? [URL], let first = url.first {
            text = first.absoluteString
        } else if let s = pb.string(forType: .string) {
            text = s
        }
        guard !text.isEmpty, session != nil else { return false }
        window?.makeFirstResponder(self)
        pasteChecked(text)
        return true
    }

    /// Backslash-escapes characters a shell would treat specially.
    static func shellEscape(_ path: String) -> String {
        var out = ""
        for c in path {
            if " \t'\"\\()[]{}&;|<>*?$`!#~".contains(c) { out.append("\\") }
            out.append(c)
        }
        return out
    }

    @objc func paste(_ sender: Any?) {
        guard let text = NSPasteboard.general.string(forType: .string) else { return }
        pasteChecked(text)
    }

    /// Pastes text, first asking (as Ghostty does; clipboard-paste-protection)
    /// when it could run commands as it lands: a line break or the
    /// end-of-paste sequence, unless the program takes bracketed pastes.
    private func pasteChecked(_ text: String) {
        guard let engine = session?.engine else { return }
        let config = AppConfig.shared
        let risky = config.pasteProtection && !(engine.bracketedPaste && config.pasteBracketedSafe) && !TerminalEngine.pasteIsSafe(text)
        guard risky, let window else {
            onInput?(engine.encodePaste(text))
            return
        }
        let alert = NSAlert()
        alert.messageText = "Paste text that could run commands?"
        alert.informativeText = "It has a line break or control sequence, so the terminal may run it as soon as it’s pasted."
        alert.addButton(withTitle: "Paste")
        alert.addButton(withTitle: "Cancel")
        alert.accessoryView = Self.pastePreview(text)
        alert.beginSheetModal(for: window) { [weak self] r in
            MainActor.assumeIsolated {
                guard let self else { return }
                // Encoded now, for the terminal's modes as they are now.
                if r == .alertFirstButtonReturn, let engine = self.session?.engine {
                    self.onInput?(engine.encodePaste(text))
                }
                self.window?.makeFirstResponder(self)
            }
        }
    }

    /// The text about to be pasted, scrollable and in the terminal's font.
    private static func pastePreview(_ text: String) -> NSView {
        let scroll = NSScrollView(frame: NSRect(x: 0, y: 0, width: 440, height: 180))
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = true
        scroll.autohidesScrollers = true
        scroll.borderType = .bezelBorder
        let view = NSTextView(frame: scroll.bounds)
        view.isEditable = false
        view.isSelectable = true
        view.isRichText = false
        view.font = .monospacedSystemFont(ofSize: 11, weight: .regular)
        view.textContainerInset = NSSize(width: 4, height: 4)
        // No wrapping, so each line reads as the shell will see it.
        view.isHorizontallyResizable = true
        view.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        view.textContainer?.widthTracksTextView = false
        view.textContainer?.containerSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
        view.autoresizingMask = [.height]
        // Control characters made visible, as `cat -v` would.
        view.string = String(text.unicodeScalars.map { s -> String in
            switch s.value {
            case 0x0A, 0x09: return String(s)
            case 0x0D: return "⏎"
            case 0..<0x20: return "^" + String(UnicodeScalar(s.value + 0x40)!)
            case 0x7F: return "^?"
            default: return String(s)
            }
        }.joined())
        scroll.documentView = view
        return scroll
    }

    @objc func copy(_ sender: Any?) {
        guard let text = session?.engine.selectionText() else { return NSSound.beep() }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
    }

    @objc override func selectAll(_ sender: Any?) {
        guard let engine = session?.engine else { return }
        engine.select(fromX: 0, fromY: 0, toX: engine.cols - 1, toY: engine.rows - 1)
        needsDisplay = true
    }
}

/// SwiftUI wrapper.
struct TerminalView: NSViewRepresentable {
    let paneID: String
    let model: AppModel
    var isFocused: Bool

    func makeNSView(context: Context) -> TerminalNSView {
        let v = TerminalNSView(frame: .zero)
        let session = model.acquireSession(paneID)
        v.session = session
        v.onInput = { [paneID, model] data in model.send(paneID, data) }
        v.onDropFiles = { [paneID, model] urls in TerminalView.upload(urls, to: paneID, model: model) }
        v.onResize = { [paneID, model] cols, rows in model.resize(paneID, cols: cols, rows: rows) }
        v.onCellSize = { [paneID, model] w, h in model.setCellPixels(paneID, width: w, height: h) }
        v.onFocus = { [paneID, model] in model.viewFocused(paneID) }
        v.onOpenLink = { [paneID, model] url, inHammock in TerminalView.open(url, inHammock: inHammock, beside: paneID, model: model) }
        v.isFocusTarget = isFocused
        v.folded = context.environment.paneFolded
        return v
    }

    func updateNSView(_ v: TerminalNSView, context: Context) {
        // SwiftUI may reuse this view for a different pane (e.g. after a split
        // reorders the layout): switch sessions and input targets.
        if v.session?.paneID != paneID {
            if let old = v.session { model.releaseSession(old.paneID) }
            v.session = model.acquireSession(paneID)
            v.onInput = { [paneID, model] data in model.send(paneID, data) }
            v.onDropFiles = { [paneID, model] urls in TerminalView.upload(urls, to: paneID, model: model) }
            v.onResize = { [paneID, model] cols, rows in model.resize(paneID, cols: cols, rows: rows) }
            v.onFocus = { [paneID, model] in model.viewFocused(paneID) }
            v.onOpenLink = { [paneID, model] url, inHammock in TerminalView.open(url, inHammock: inHammock, beside: paneID, model: model) }
            v.forceGridReport()
        }
        v.isFocusTarget = isFocused
        v.folded = context.environment.paneFolded
    }

    /// A pane in a Group shared with you runs on another Mac: dropped files
    /// are copied there (up to 20 MB each) and their paths typed.
    @MainActor
    /// Opens a link from a terminal: in the default browser (or the app
    /// for its scheme), or in a Hammock browser pane beside the terminal.
    static func open(_ url: URL, inHammock: Bool, beside paneID: String, model: AppModel) {
        if inHammock, url.scheme == "http" || url.scheme == "https" {
            Task { await model.splitBrowser(paneID, direction: .horizontal, url: url.absoluteString) }
        } else {
            NSWorkspace.shared.open(url)
        }
    }

    static func upload(_ urls: [URL], to paneID: String, model: AppModel) -> Bool {
        guard let group = model.group(ofPane: paneID), group.isRemote else { return false }
        guard group.canDrive else { return true }
        Task {
            for url in urls {
                guard let data = try? Data(contentsOf: url), data.count <= 20 << 20 else { continue }
                await model.upload(data, name: url.lastPathComponent, to: paneID)
            }
        }
        return true
    }

    static func dismantleNSView(_ v: TerminalNSView, coordinator: ()) {
        if let s = v.session {
            v.session = nil
            MainActor.assumeIsolated { AppModelRegistry.shared?.releaseSession(s.paneID) }
        }
    }
}

/// Lets static SwiftUI teardown reach the model.
@MainActor
enum AppModelRegistry {
    static var shared: AppModel?
}

extension Array {
    subscript(safe i: Int) -> Element? { indices.contains(i) ? self[i] : nil }
}

// MARK: Text input (dead keys, IMEs)

extension TerminalNSView: NSTextInputClient {
    func insertText(_ string: Any, replacementRange: NSRange) {
        let text = (string as? String) ?? (string as? NSAttributedString)?.string ?? ""
        let wasComposing = hasMarkedText()
        markedText = NSMutableAttributedString()
        guard !text.isEmpty else { return }
        if let ev = pendingKeyEvent, text.count == 1, !wasComposing {
            sendKey(ev, text: text)
        } else {
            // Composed or multi-character input (IME commit): send as text.
            session?.engine.scrollToBottom()
            onInput?(Data(text.utf8))
        }
        needsDisplay = true
    }

    override func doCommand(by selector: Selector) {
        if let ev = pendingKeyEvent { sendKey(ev, text: nil) }
    }

    func setMarkedText(_ string: Any, selectedRange: NSRange, replacementRange: NSRange) {
        if let s = string as? NSAttributedString {
            markedText = NSMutableAttributedString(attributedString: s)
        } else if let s = string as? String {
            markedText = NSMutableAttributedString(string: s)
        }
        needsDisplay = true
    }

    func unmarkText() {
        markedText = NSMutableAttributedString()
        needsDisplay = true
    }

    func selectedRange() -> NSRange { NSRange(location: NSNotFound, length: 0) }
    func markedRange() -> NSRange { markedText.length > 0 ? NSRange(location: 0, length: markedText.length) : NSRange(location: NSNotFound, length: 0) }
    func hasMarkedText() -> Bool { markedText.length > 0 }
    func attributedSubstring(forProposedRange range: NSRange, actualRange: NSRangePointer?) -> NSAttributedString? { nil }
    func validAttributesForMarkedText() -> [NSAttributedString.Key] { [] }
    func characterIndex(for point: NSPoint) -> Int { 0 }

    func firstRect(forCharacterRange range: NSRange, actualRange: NSRangePointer?) -> NSRect {
        // Put IME candidate windows at the cursor.
        guard let engine = session?.engine, let window else { return .zero }
        let f = engine.frame()
        let local = NSRect(x: padding + CGFloat(f.cursorX) * cellWidth, y: padding + CGFloat(f.cursorY) * cellHeight, width: cellWidth, height: cellHeight)
        return window.convertToScreen(convert(local, to: nil))
    }
}

// MARK: Mouse reporting

extension TerminalNSView {
    override func mouseUp(with event: NSEvent) {
        if reportMouse(.release, event) {
            // A program doing its own selection (Claude Code, a TUI with
            // mouse on) copies it itself, with pbcopy: when the clipboard
            // changes just after the release, the same "Copied" shows.
            pillOnClipboardChange(at: convert(event.locationInWindow, from: nil))
            return
        }
        selectionGesture(.release, event)
        // copy-on-select: finishing a selection puts it on the clipboard.
        if AppConfig.shared.copyOnSelect, let text = session?.engine.selectionText(), !text.isEmpty {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            showCopiedPill(at: convert(event.locationInWindow, from: nil))
        }
    }

    /// Watches the clipboard for a moment after a release the program got.
    private func pillOnClipboardChange(at point: NSPoint) {
        let start = NSPasteboard.general.changeCount
        var checks = 0
        Timer.scheduledTimer(withTimeInterval: 0.05, repeats: true) { [weak self] timer in
            MainActor.assumeIsolated {
                checks += 1
                guard let self, self.window != nil else { return timer.invalidate() }
                if NSPasteboard.general.changeCount != start {
                    timer.invalidate()
                    self.showCopiedPill(at: point)
                } else if checks >= 16 { timer.invalidate() } // 0.8s
            }
        }
    }

    /// copy-on-select's quiet receipt: a small "Copied" by the pointer that
    /// fades by itself, smaller than the window's toast and in this pane only.
    private func showCopiedPill(at point: NSPoint) {
        subviews.filter { $0 is CopiedPillView }.forEach { $0.removeFromSuperview() }
        // The app theme's accent, with white or black on it, whichever reads.
        let accent = AppConfig.shared.agentColors.color
        var resolved = NSColor.controlAccentColor
        effectiveAppearance.performAsCurrentDrawingAppearance { resolved = NSColor(accent).usingColorSpace(.sRGB) ?? resolved }
        let luminance = 0.2126 * resolved.redComponent + 0.7152 * resolved.greenComponent + 0.0722 * resolved.blueComponent
        let pill = CopiedPillView(CopiedPill(background: Color(nsColor: resolved), foreground: luminance > 0.6 ? .black : .white))
        pill.frame.size = pill.fittingSize
        // Just above and right of the pointer, kept inside the pane.
        let size = pill.frame.size
        pill.frame.origin = NSPoint(
            x: min(max(point.x + 4, 4), bounds.width - size.width - 4),
            y: min(max(point.y - size.height - 4, 4), bounds.height - size.height - 4))
        pill.alphaValue = 0
        addSubview(pill)
        let still = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        NSAnimationContext.runAnimationGroup { $0.duration = 0.12; pill.animator().alphaValue = 1 }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.9) { [weak pill] in
            guard let pill, pill.superview != nil else { return }
            NSAnimationContext.runAnimationGroup({ ctx in
                ctx.duration = 0.3
                pill.animator().alphaValue = 0
                if !still { pill.animator().frame.origin.y -= 4 }
            }, completionHandler: { pill.removeFromSuperview() })
        }
    }

    override func rightMouseDown(with event: NSEvent) {
        // On a link: what to do with it (before the program's own right-click).
        if let link = link(at: event), let url = URL(string: link.url) {
            let menu = NSMenu()
            menu.addItem(LinkMenuItem("Open Link") { [weak self] in self?.onOpenLink?(url, false) })
            menu.addItem(LinkMenuItem("Open in Hammock Browser") { [weak self] in self?.onOpenLink?(url, true) })
            menu.addItem(LinkMenuItem("Copy Link") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(link.url, forType: .string)
            })
            NSMenu.popUpContextMenu(menu, with: event, for: self)
            return
        }
        if !reportMouse(.press, event, button: .right) { super.rightMouseDown(with: event) }
    }

    // MARK: Links

    private func cell(at p: NSPoint) -> (x: Int, y: Int) {
        (Int((p.x - padding) / cellWidth), Int((p.y - padding) / cellHeight))
    }

    private func link(at event: NSEvent) -> TerminalLink? {
        guard let engine = session?.engine else { return nil }
        let c = cell(at: convert(event.locationInWindow, from: nil))
        return engine.link(atX: c.x, y: c.y)
    }

    private func updateHoverLink(_ event: NSEvent) {
        var link: TerminalLink?
        if event.modifierFlags.contains(.command), let engine = session?.engine, let window {
            let c = cell(at: convert(window.mouseLocationOutsideOfEventStream, from: nil))
            // Still on the one found: no need to look again.
            link = hoverLink?.contains(x: c.x, y: c.y) == true ? hoverLink : engine.link(atX: c.x, y: c.y)
        }
        guard link != hoverLink else { return }
        hoverLink = link
        (link == nil ? NSCursor.iBeam : NSCursor.pointingHand).set()
        needsDisplay = true
    }

    override func flagsChanged(with event: NSEvent) {
        updateHoverLink(event) // ⌘ pressed or let go over a link
        super.flagsChanged(with: event)
    }

    override func rightMouseUp(with event: NSEvent) {
        _ = reportMouse(.release, event, button: .right)
    }

    /// Sends a mouse event to the program if it enabled mouse reporting
    /// (hold Shift to select text instead). Returns whether it was sent.
    func reportMouse(_ action: MouseAction, _ event: NSEvent, button: MouseButton? = .left) -> Bool {
        guard let engine = session?.engine, engine.mouseTracking, !event.modifierFlags.contains(.shift) else { return false }
        let p = convert(event.locationInWindow, from: nil)
        var mods: KeyMods = []
        if event.modifierFlags.contains(.control) { mods.insert(.ctrl) }
        if event.modifierFlags.contains(.option) { mods.insert(.alt) }
        let g = MouseGeometry(width: Float(bounds.width), height: Float(bounds.height), cellWidth: Float(cellWidth),
                              cellHeight: Float(cellHeight), padding: Float(padding), scale: Float(window?.backingScaleFactor ?? 2))
        if let data = engine.encodeMouse(action, button: button, x: Float(p.x), y: Float(p.y), mods: mods, geometry: g) {
            onInput?(data)
        }
        return true
    }
}

/// A menu item that runs a closure.
final class LinkMenuItem: NSMenuItem {
    private let run: () -> Void

    init(_ title: String, run: @escaping () -> Void) {
        self.run = run
        super.init(title: title, action: #selector(fire), keyEquivalent: "")
        target = self
    }

    required init(coder: NSCoder) { fatalError() }

    @objc private func fire() { run() }
}

/// The "Copied" pill copy-on-select shows by the pointer.
private final class CopiedPillView: NSHostingView<CopiedPill> {
    init(_ pill: CopiedPill) { super.init(rootView: pill) }
    required init(rootView: CopiedPill) { super.init(rootView: rootView) }
    @MainActor required dynamic init?(coder: NSCoder) { nil }
    // Never in the way of the selection or the program's mouse.
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

private struct CopiedPill: View {
    /// The theme's accent, and white or black text on it.
    let background: Color
    let foreground: Color

    var body: some View {
        HStack(spacing: 5) {
            Image(systemName: "doc.on.doc")
            Text("Copied")
        }
        .font(.callout.weight(.semibold))
        .foregroundStyle(foreground)
        .padding(.horizontal, 11)
        .padding(.vertical, 5)
        .background(background, in: Capsule())
        .shadow(color: .black.opacity(0.2), radius: 4, y: 1)
            .fixedSize()
            .accessibilityHidden(true)
    }
}
