import AppKit
import HammockKit
import Metal
import QuartzCore
import SwiftUI

/// Draws a pane's TerminalEngine with CoreText and turns keyboard/mouse input
/// into PTY bytes. Purely a view: all terminal state comes from the daemon.
final class TerminalNSView: NSView {
    var session: PaneSession? {
        didSet {
            oldValue?.unobserve(self)
            session?.observe(self) { [weak self] in
                self?.blinkOn = true // solid while output arrives
                self?.redrawChanges()
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
    /// Told each draw's seconds spent in `frame()` and then drawing, while
    /// the renderer is being measured (HMK_DEBUG_RENDER_BENCH and _STATS).
    static var timing: ((_ frame: CFTimeInterval, _ draw: CFTimeInterval) -> Void)?
    private var thicken: Bool { AppConfig.shared.fontThicken }

    /// Draws the pane when terminal-renderer = metal (#18); nil: CoreGraphics
    /// draws it, in draw(_:). Chosen when the pane opens.
    let metal: TerminalMetalRenderer?

    override convenience init(frame: NSRect) {
        self.init(frame: frame, metal: AppConfig.shared.terminalRenderer == "metal")
    }

    init(frame: NSRect, metal useMetal: Bool) {
        (font, boldFont) = TerminalFont.resolve()
        metal = useMetal ? TerminalMetalRenderer() : nil
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

    /// After output: redraws only the rows it changed, and the rows the
    /// cursor left and reached (a keystroke at a full screen of colour costs
    /// a row, not the pane: docs/benchmarks.md). Anything else that changes
    /// the picture (selection, scrolling back, theme, focus) redraws it all.
    private func redrawChanges() {
        guard let session else { return }
        let changes = session.changes // asked even when put off, so they don't pile up
        if PaneMotion.shared.isMoving || folded {
            pendingDisplay = true
            return
        }
        if let rects = rectsToRedraw(changes) { rects.forEach(setNeedsDisplay) } else { needsDisplay = true }
    }

    /// The rows changes need redrawn, with the cursor's old and new ones;
    /// nil for the whole pane.
    func rectsToRedraw(_ changes: TerminalChanges) -> [NSRect]? {
        guard let session else { return nil }
        var rows = IndexSet()
        switch changes.dirty {
        case .full:
            return nil
        case .rows where !session.engine.images().isEmpty:
            return nil // Kitty images can span rows that didn't change
        case .rows(let changed):
            rows = changed
        case .none:
            break
        }
        if let y = drawnCursorRow { rows.insert(y) }
        if let y = changes.cursorRow { rows.insert(y) }
        return rows.map(rowRect)
    }

    /// Redraws the cursor's row (blinking), unless panes are moving or it's folded away.
    private func redrawCursor() {
        if PaneMotion.shared.isMoving || folded { pendingDisplay = true; return }
        if let y = drawnCursorRow { setNeedsDisplay(rowRect(y)) } else { needsDisplay = true }
    }

    /// Row y's band across the whole pane (the first and last take the padding too).
    private func rowRect(_ y: Int) -> NSRect {
        var top = padding + CGFloat(y) * cellHeight, bottom = top + cellHeight
        if y <= 0 { top = 0 }
        if y >= (session?.engine.rows ?? 0) - 1 { bottom = bounds.height }
        return NSRect(x: 0, y: top, width: bounds.width, height: max(bottom - top, 0))
    }

    /// What RenderBench draws, in place of AppKit's rects being drawn
    /// (it draws outside a display pass, where needsToDraw can't answer).
    var benchRects: [NSRect]?

    private func wants(_ r: NSRect) -> Bool {
        if let benchRects { return benchRects.contains { $0.intersects(r) } }
        return needsToDraw(r)
    }

    /// The row the cursor was last drawn on (shown or blinked off), to clear it once it moves.
    private var drawnCursorRow: Int?

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
        layoutCache?.setFonts(layoutFonts())
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
                if self.blinkWanted { self.blinkOn.toggle(); self.redrawCursor() } else if !self.blinkOn { self.blinkOn = true; self.redrawCursor() }
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
        // Off unless measuring (RenderBench, RenderStats): the time taken
        // copying the screen out of libghostty, then drawing it.
        let timing = Self.timing
        let start = timing == nil ? 0 : CACurrentMediaTime()
        var copied = start
        defer { if let timing { timing(copied - start, CACurrentMediaTime() - copied) } }
        let f = session.engine.frame()
        if timing != nil { copied = CACurrentMediaTime() }
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
        // Only the rows asked for (redrawChanges): dirtyRect is the bounds of them all.
        let layout = lineLayout(for: f, theme: theme)
        var lines: [(top: CGFloat, line: TerminalLine)] = []
        for (y, row) in f.cells.enumerated() where wants(rowRect(y)) {
            let top = padding + CGFloat(y) * cellHeight
            let line = layout.line(row)
            drawBackgrounds(line, top: top)
            lines.append((top, line))
        }
        drawImages(images, .belowText)
        for (top, line) in lines { drawText(line, top: top) }
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
        drawnCursorRow = f.cursorVisible && f.cursorY < f.cells.count ? f.cursorY : nil
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
                        drawText(layout.layout([(f.cursorX, c)], row: cells), top: rect.minY)
                    }
                }
            }
        }
    }

    // MARK: Metal

    override var wantsUpdateLayer: Bool { metal != nil }



    /// Metal draws into this, a sublayer filling the view: AppKit never asks
    /// a view backed by a CAMetalLayer itself to update, so the view keeps
    /// an ordinary layer (updateLayer is called as for any other view) and
    /// draws into this one from there.
    private lazy var metalLayer: CAMetalLayer? = metal.map { metal in
        let l = CAMetalLayer()
        l.device = metal.device
        l.pixelFormat = TerminalMetalRenderer.pixelFormat
        l.colorspace = CGColorSpace(name: CGColorSpace.sRGB)
        l.framebufferOnly = true
        l.isOpaque = true
        // Shown in step with Core Animation's transaction, so a pane being
        // resized never shows a frame of the wrong size.
        l.presentsWithTransaction = true
        l.contentsGravity = .topLeft
        // Moved and resized with the view, never animated.
        l.actions = ["bounds": NSNull(), "position": NSNull(), "frame": NSNull(), "contents": NSNull()]
        layer?.addSublayer(l)
        return l
    }

    override func layout() {
        super.layout()
        if let metalLayer, metalLayer.frame != bounds { metalLayer.frame = bounds }
    }

    override func updateLayer() {
        guard metal != nil, let layer = metalLayer else { return }
        if layer.frame != bounds { layer.frame = bounds }
        let scale = window?.backingScaleFactor ?? 2
        let size = CGSize(width: (bounds.width * scale).rounded(), height: (bounds.height * scale).rounded())
        guard size.width >= 1, size.height >= 1 else { return }
        layer.contentsScale = scale
        if layer.drawableSize != size { layer.drawableSize = size }
        let timing = Self.timing
        let start = timing == nil ? 0 : CACurrentMediaTime()
        guard let (scene, frameTime) = metalScene(scale: scale), let drawable = layer.nextDrawable() else { return }
        let cb = metal?.render(scene, to: drawable.texture)
        cb?.waitUntilScheduled()
        drawable.present()
        if let timing { timing(frameTime, CACurrentMediaTime() - start - frameTime) }
    }

    /// Draws the pane into `texture` with Metal (RenderBench). The command
    /// buffer is committed; wait on it before reading the texture.
    func renderMetal(into texture: MTLTexture, scale: CGFloat) -> MTLCommandBuffer? {
        guard let metal else { return nil }
        let timing = Self.timing
        let start = timing == nil ? 0 : CACurrentMediaTime()
        guard let (scene, frameTime) = metalScene(scale: scale) else { return nil }
        let cb = metal.render(scene, to: texture)
        if let timing { timing(frameTime, CACurrentMediaTime() - start - frameTime) }
        return cb
    }

    /// What Metal draws: the same rows, cursor and link underline as
    /// draw(_:). Not yet: Kitty images, and IME text being composed (#18,
    /// phase 3). Also returns the time spent in frame().
    private func metalScene(scale: CGFloat) -> (TerminalMetalRenderer.Scene, CFTimeInterval)? {
        guard let session else { return nil }
        let theme = session.engine.theme
        guard session.ready else {
            return (TerminalMetalRenderer.Scene(size: bounds.size, scale: scale, thicken: thicken, background: theme.background,
                                                padding: padding, cellHeight: cellHeight), 0)
        }
        let start = CACurrentMediaTime()
        let f = session.engine.frame()
        let frameTime = CACurrentMediaTime() - start
        var scene = TerminalMetalRenderer.Scene(size: bounds.size, scale: scale, thicken: thicken, background: f.background,
                                                padding: padding, cellHeight: cellHeight)
        let layout = lineLayout(for: f, theme: theme)
        for (y, row) in f.cells.enumerated() {
            scene.rows.append((padding + CGFloat(y) * cellHeight, layout.line(row)))
        }
        if let link = hoverLink {
            for span in link.spans where span.row < f.rows {
                scene.overlay.append(.init(rect: CGRect(x: padding + CGFloat(span.cols.lowerBound) * cellWidth, y: padding + CGFloat(span.row + 1) * cellHeight - 2,
                                                        width: CGFloat(span.cols.count) * cellWidth, height: 1),
                                           color: TerminalColor(theme.foreground), copy: true))
            }
        }
        let focused = window?.isKeyWindow == true && window?.firstResponder === self
        blinkWanted = focused && f.cursorBlinking
        drawnCursorRow = f.cursorVisible && f.cursorY < f.cells.count ? f.cursorY : nil
        if f.cursorVisible, f.cursorY < f.cells.count, blinkOn || !blinkWanted {
            let rect = CGRect(x: padding + CGFloat(f.cursorX) * cellWidth, y: padding + CGFloat(f.cursorY) * cellHeight,
                              width: cellWidth, height: cellHeight)
            let cc = TerminalColor(f.cursorColor ?? theme.cursor)
            func add(_ r: CGRect) { scene.overlay.append(.init(rect: r, color: cc, copy: true)) }
            if !focused || f.cursorStyle == .hollow {
                // A 1-point outline inside the cell, as the stroke draws it.
                add(CGRect(x: rect.minX, y: rect.minY, width: rect.width, height: 1))
                add(CGRect(x: rect.minX, y: rect.maxY - 1, width: rect.width, height: 1))
                add(CGRect(x: rect.minX, y: rect.minY + 1, width: 1, height: rect.height - 2))
                add(CGRect(x: rect.maxX - 1, y: rect.minY + 1, width: 1, height: rect.height - 2))
            } else {
                switch f.cursorStyle {
                case .bar: add(CGRect(x: rect.minX, y: rect.minY, width: AppConfig.shared.cursorThin ? 1 : 2, height: rect.height))
                case .underline: add(CGRect(x: rect.minX, y: rect.maxY - 2, width: rect.width, height: 2))
                default:
                    add(rect)
                    let cells = f.cells[f.cursorY]
                    if f.cursorX < cells.count, !cells[f.cursorX].text.isEmpty {
                        var c = cells[f.cursorX]
                        c.fg = f.background
                        c.inverse = false
                        c.selected = false
                        scene.overlayText.append((rect.minY, layout.layout([(f.cursorX, c)], row: cells)))
                    }
                }
            }
        }
        return (scene, frameTime)
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

    // Rows are laid out by TerminalLineLayout (shared with the Metal
    // renderer to come, docs/design/metal-renderer.md), which keeps the
    // layouts of rows it has seen; here they're drawn with CoreGraphics.
    private var layoutCache: TerminalLineLayout?
    private var cgColors: [TerminalColor: CGColor] = [:]
    /// Rows laid out afresh and kept, so far (RenderBench).
    var layoutCounts: (hits: Int, misses: Int) { (layoutCache?.hits ?? 0, layoutCache?.misses ?? 0) }

    private func lineLayout(for f: TerminalFrame, theme: TerminalTheme) -> TerminalLineLayout {
        let palette = TerminalPalette(foreground: f.foreground, background: f.background,
                                      selection: theme.selection, selectionForeground: theme.selectionForeground)
        let l = layoutCache ?? TerminalLineLayout(fonts: layoutFonts(), palette: palette)
        layoutCache = l
        l.setPalette(palette)
        // Each generation holds twice the screen, so up to four screens are
        // kept: a dense 400-column row and its layout are ~40 KB, about 16 MB
        // for a full 400×100 pane.
        l.capacity = max(f.rows * 2, 48)
        return l
    }

    private func layoutFonts() -> TerminalFonts {
        let italic = NSFontManager.shared.convert(font, toHaveTrait: .italicFontMask)
        let boldItalic = NSFontManager.shared.convert(boldFont, toHaveTrait: .italicFontMask)
        return TerminalFonts(regular: font, bold: boldFont, italic: italic, boldItalic: boldItalic,
                             cellWidth: cellWidth, cellHeight: cellHeight, baseline: ascent + 1,
                             scale: window?.backingScaleFactor ?? 2)
    }

    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        layoutCache?.setFonts(layoutFonts())
        needsDisplay = true
    }

    private func cg(_ c: TerminalColor) -> CGColor {
        if let x = cgColors[c] { return x }
        let x = CGColor(srgbRed: CGFloat(c.rgb.r) / 255, green: CGFloat(c.rgb.g) / 255, blue: CGFloat(c.rgb.b) / 255, alpha: c.alpha)
        if cgColors.count > 4096 { cgColors = [:] }
        cgColors[c] = x
        return x
    }

    private func drawBackgrounds(_ line: TerminalLine, top: CGFloat) {
        guard !line.backgrounds.isEmpty, let ctx = NSGraphicsContext.current?.cgContext else { return }
        ctx.saveGState()
        defer { ctx.restoreGState() }
        ctx.setBlendMode(.copy) // as NSRectFill
        for b in line.backgrounds {
            ctx.setFillColor(cg(TerminalColor(b.color)))
            ctx.fill(CGRect(x: padding + b.x, y: top, width: b.width, height: cellHeight))
        }
    }

    /// A row's rectangles (blocks, underlines), then its glyphs.
    private func drawText(_ line: TerminalLine, top: CGFloat) {
        guard let ctx = NSGraphicsContext.current?.cgContext else { return }
        ctx.saveGState()
        defer { ctx.restoreGState() }
        ctx.translateBy(x: padding, y: top)
        for r in line.rects {
            ctx.setBlendMode(r.copy ? .copy : .normal)
            ctx.setFillColor(cg(r.color))
            ctx.fill(r.rect)
        }
        ctx.setBlendMode(.normal)
        ctx.textMatrix = .identity
        for item in line.text {
            ctx.saveGState()
            switch item {
            case .glyphs(let g):
                ctx.setFillColor(cg(g.color))
                ctx.scaleBy(x: 1, y: -1)
                CTFontDrawGlyphs(g.font, g.glyphs, g.positions, g.glyphs.count, ctx)
            case .fitted(let f):
                if let clip = f.clip { ctx.clip(to: clip) }
                ctx.setFillColor(cg(f.color))
                ctx.concatenate(f.transform)
                ctx.scaleBy(x: 1, y: -1)
                var glyph = f.glyph
                var origin = CGPoint.zero
                CTFontDrawGlyphs(f.font, &glyph, &origin, 1, ctx)
            }
            ctx.restoreGState()
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

    @MainActor
    /// Opens a link from a terminal: in the default browser, or in a Hammock
    /// browser pane beside the terminal. Anything a program prints can be a
    /// link, so only web and mail links open straight away: a file that
    /// would run something is shown in the Finder instead, and any other
    /// scheme (which could launch any app, or mount a server) asks first.
    static func open(_ url: URL, inHammock: Bool, beside paneID: String, model: AppModel) {
        switch AgentLink.opening(url) {
        case .web:
            if inHammock && HammockTool.browser.on { // else (turned off in Settings ▸ Tools) your browser
                Task { await model.splitBrowser(paneID, direction: .horizontal, url: url.absoluteString) }
            } else {
                NSWorkspace.shared.open(url)
            }
        case .direct:
            NSWorkspace.shared.open(url)
        case .file(let path):
            FileLink.open(path)
        case .ask:
            askToOpen(url)
        }
    }

    /// "Open this link?", showing where it really goes. Cancel comes first,
    /// so NSAlert gives it Escape; Open has no key, so Return opens nothing.
    @MainActor private static func askToOpen(_ url: URL) {
        let alert = NSAlert()
        alert.messageText = "Open this link?"
        alert.informativeText = "It isn’t a web page: it could open another app, or connect to a server.\n\n\(url.absoluteString)"
        alert.addButton(withTitle: "Cancel")
        alert.addButton(withTitle: "Open").keyEquivalent = ""
        let opened: (NSApplication.ModalResponse) -> Void = { if $0 == .alertSecondButtonReturn { NSWorkspace.shared.open(url) } }
        if let window = NSApp.keyWindow {
            alert.beginSheetModal(for: window, completionHandler: opened)
        } else {
            opened(alert.runModal())
        }
    }

    /// A pane in a Group shared with you runs on another Mac: dropped files
    /// are copied there (up to 20 MB each) and their paths typed.
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
            // Where it really goes: a program's link can say one thing and go elsewhere.
            if link.hyperlink {
                // Percent-encoded, as it goes: the raw text could carry
                // right-to-left or other invisible marks to disguise it.
                let dest = NSMenuItem(title: Self.shortened(url.absoluteString), action: nil, keyEquivalent: "")
                dest.isEnabled = false
                dest.toolTip = url.absoluteString
                menu.addItem(dest)
                menu.addItem(.separator())
            }
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

    /// A long link cut in the middle, for a menu.
    static func shortened(_ url: String, to max: Int = 80) -> String {
        guard url.count > max else { return url }
        let half = (max - 1) / 2
        return String(url.prefix(half)) + "…" + String(url.suffix(half))
    }

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
        // A program's link (OSC 8) shows where it goes (its text needn't),
        // percent-encoded as in its menu (none for one that isn't a URL at
        // all: it doesn't open either).
        toolTip = link?.hyperlink == true ? link.flatMap { URL(string: $0.url)?.absoluteString } : nil
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
