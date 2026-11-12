import CoreGraphics
import Foundation
import GhosttyVt

/// An RGB colour from the terminal.
public struct RGB: Hashable, Sendable {
    public var r, g, b: UInt8
    public init(_ c: GhosttyColorRgb) { r = c.r; g = c.g; b = c.b }
    public init(r: UInt8, g: UInt8, b: UInt8) { self.r = r; self.g = g; self.b = b }
}

/// One cell ready to draw.
public struct TerminalCell: Hashable, Sendable {
    public var text: String          // grapheme cluster; "" for empty
    public var fg: RGB?              // nil = default foreground
    public var bg: RGB?              // nil = default background
    public var bold = false
    public var italic = false
    public var faint = false
    public var underline = false
    public var strikethrough = false
    public var inverse = false
    public var invisible = false
    public var wide = false          // occupies this and the next column
    public var spacer = false        // trailing half of a wide cell
    public var selected = false

    public init(text: String) { self.text = text }
}

public enum CursorStyle: Sendable, Hashable { case block, bar, underline, hollow }

/// A snapshot of what to draw.
/// What changed on a terminal's screen (TerminalEngine.takeChanges).
public struct TerminalChanges: Equatable, Sendable {
    public enum Dirty: Equatable, Sendable {
        case none
        /// Rows of the viewport, from the top.
        case rows(IndexSet)
        case full
    }
    public var dirty: Dirty
    /// The cursor's row in the viewport, if it's showing.
    public var cursorRow: Int?

    public init(dirty: Dirty, cursorRow: Int? = nil) {
        self.dirty = dirty
        self.cursorRow = cursorRow
    }
}

public struct TerminalFrame: Sendable {
    public var cols = 0
    public var rows = 0
    public var background = RGB(r: 0, g: 0, b: 0)
    public var foreground = RGB(r: 255, g: 255, b: 255)
    public var cursorColor: RGB?
    public var cursorVisible = false
    public var cursorX = 0
    public var cursorY = 0
    public var cursorStyle = CursorStyle.block
    public var cursorBlinking = false
    public var cells: [[TerminalCell]] = []
}

/// Modifier keys, mirroring GHOSTTY_MODS_*.
public struct KeyMods: OptionSet, Sendable {
    public let rawValue: UInt16
    public init(rawValue: UInt16) { self.rawValue = rawValue }
    public static let shift = KeyMods(rawValue: 1 << 0)
    public static let ctrl = KeyMods(rawValue: 1 << 1)
    public static let alt = KeyMods(rawValue: 1 << 2)
    public static let superKey = KeyMods(rawValue: 1 << 3)
}

/// A client-side terminal fed with a pane's byte stream (snapshot, then live
/// output) using libghostty-vt. It never answers terminal queries — the
/// daemon's mirror does that — so it is purely a renderer's model.
/// Not thread-safe: use from one thread (the main thread in the apps).
public final class TerminalEngine {
    var terminal: GhosttyTerminal?
    var handle: GhosttyTerminal? { terminal }
    var renderState: GhosttyRenderState?
    var imageIter: GhosttyRenderStateImageIterator?
    var imageCache: [String: CGImage] = [:]
    /// Cell size in pixels, reported to programs (Kitty graphics, XTWINOPS).
    private var cellPixels = (w: 0, h: 0)
    private var rowIter: GhosttyRenderStateRowIterator?
    private var rowCells: GhosttyRenderStateRowCells?
    private var keyEncoder: GhosttyKeyEncoder?
    private var keyEvent: GhosttyKeyEvent?
    var mouseEncoder: GhosttyMouseEncoder?
    var mouseEvent: GhosttyMouseEvent?
    var mouseButtonDown = false
    var selectionGesture: GhosttySelectionGesture?
    var selectionEvents: [Int32: GhosttySelectionGestureEvent] = [:]
    public private(set) var cols: Int
    public private(set) var rows: Int
    public var theme: TerminalTheme = .light {
        didSet { if let terminal, theme != oldValue { applyTheme(theme, to: terminal) } }
    }

    public init(cols: Int, rows: Int) {
        self.cols = max(cols, 1)
        self.rows = max(rows, 1)
        ghostty_terminal_new(nil, &terminal, UInt16(self.cols), UInt16(self.rows))
        if let terminal {
            // Grapheme clustering (mode 2027), as in Ghostty: emoji sequences and
            // flags take one cluster's width. Must match the daemon's mirror.
            var cluster = GhosttyTerminalModeConfig(mode: ghostty_mode_new(2027, false), value: true)
            ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_MODE_DEFAULT, &cluster)
            // As much scrollback as the daemon's mirror keeps (10,000 lines,
            // vt.newMirror), with no limit in bytes: libghostty's default
            // kept only a few hundred lines of coloured output.
            var lines = 10_000
            ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_LINES, &lines)
            ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, nil)
            KittyGraphicsSetup.enable(terminal)
        }
        if let terminal { applyTheme(theme, to: terminal) }
        ghostty_render_state_new(nil, &renderState)
        ghostty_render_state_row_iterator_new(nil, &rowIter)
        ghostty_render_state_row_cells_new(nil, &rowCells)
        ghostty_key_encoder_new(nil, &keyEncoder)
        ghostty_key_event_new(nil, &keyEvent)
    }

    deinit {
        if let selectionGesture { ghostty_selection_gesture_free(selectionGesture, terminal) }
        for e in selectionEvents.values { ghostty_selection_gesture_event_free(e) }
        if let keyEvent { ghostty_key_event_free(keyEvent) }
        if let mouseEvent { ghostty_mouse_event_free(mouseEvent) }
        if let mouseEncoder { ghostty_mouse_encoder_free(mouseEncoder) }
        if let keyEncoder { ghostty_key_encoder_free(keyEncoder) }
        if let imageIter { ghostty_render_state_image_iterator_free(imageIter) }
        if let rowCells { ghostty_render_state_row_cells_free(rowCells) }
        if let rowIter { ghostty_render_state_row_iterator_free(rowIter) }
        if let renderState { ghostty_render_state_free(renderState) }
        if let terminal { ghostty_terminal_free(terminal) }
    }

    /// Feeds PTY output.
    public func write(_ data: Data) {
        guard let terminal, !data.isEmpty else { return }
        data.withUnsafeBytes { buf in
            if let base = buf.bindMemory(to: UInt8.self).baseAddress {
                ghostty_terminal_vt_write(terminal, base, buf.count)
            }
        }
    }

    /// Resets the terminal (before replaying a snapshot).
    /// The cursor shape and blinking used until a program asks for another.
    public func setDefaultCursor(_ style: CursorStyle, blink: Bool) {
        guard let terminal else { return }
        var s: GhosttyTerminalCursorStyle
        switch style {
        case .bar: s = GHOSTTY_TERMINAL_CURSOR_STYLE_BAR
        case .underline: s = GHOSTTY_TERMINAL_CURSOR_STYLE_UNDERLINE
        case .hollow: s = GHOSTTY_TERMINAL_CURSOR_STYLE_BLOCK_HOLLOW
        case .block: s = GHOSTTY_TERMINAL_CURSOR_STYLE_BLOCK
        }
        var b = blink
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_DEFAULT_CURSOR_STYLE, &s)
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_DEFAULT_CURSOR_BLINK, &b)
    }

    public func reset(cols: Int, rows: Int) {
        guard let terminal else { return }
        ghostty_terminal_reset(terminal)
        applyTheme(theme, to: terminal)
        resize(cols: cols, rows: rows)
    }

    public func resize(cols: Int, rows: Int, cellWidth: Int = 0, cellHeight: Int = 0) {
        guard let terminal, cols > 0, rows > 0 else { return }
        self.cols = cols
        self.rows = rows
        if cellWidth > 0, cellHeight > 0 { cellPixels = (cellWidth, cellHeight) }
        ghostty_terminal_resize(terminal, UInt16(cols), UInt16(rows), UInt32(cellPixels.w), UInt32(cellPixels.h))
    }

    /// Sets the cell size in pixels (as drawn), keeping the grid. Returns
    /// whether it changed: Kitty images are placed by it, so a frame read
    /// before it changed has none, or has them at the old size.
    @discardableResult
    public func setCellPixels(width: Int, height: Int) -> Bool {
        guard width > 0, height > 0, (width, height) != cellPixels else { return false }
        resize(cols: cols, rows: rows, cellWidth: width, cellHeight: height)
        return true
    }

    /// Scrolls the viewport by `delta` rows (negative = back into scrollback).
    public func scroll(by delta: Int) {
        guard let terminal else { return }
        var b = GhosttyTerminalScrollViewport()
        b.tag = GHOSTTY_SCROLL_VIEWPORT_DELTA
        b.value.delta = delta
        ghostty_terminal_scroll_viewport(terminal, b)
    }

    public func scrollToBottom() {
        guard let terminal else { return }
        var b = GhosttyTerminalScrollViewport()
        b.tag = GHOSTTY_SCROLL_VIEWPORT_BOTTOM
        ghostty_terminal_scroll_viewport(terminal, b)
    }

    /// Whether DEC private mode `mode` is set (e.g. 2004 bracketed paste).
    public func decMode(_ mode: UInt16) -> Bool {
        guard let terminal else { return false }
        var cfg = GhosttyTerminalModeConfig(mode: ghostty_mode_new(mode, false), value: false)
        guard ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_MODE, &cfg) == GHOSTTY_SUCCESS else { return false }
        return cfg.value
    }

    /// Whether the running program has enabled mouse reporting.
    public var mouseTracking: Bool {
        guard let terminal else { return false }
        var v = false
        _ = ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &v)
        return v
    }

    /// Encodes a key press for the PTY, honouring the program's keyboard modes
    /// (application cursor keys, kitty keyboard protocol, …).
    public func encodeKey(key: GhosttyKey, mods: KeyMods, text: String?, unshifted: UInt32, action: GhosttyKeyAction = GHOSTTY_KEY_ACTION_PRESS) -> Data? {
        guard let terminal, let keyEncoder, let keyEvent else { return nil }
        ghostty_key_encoder_setopt_from_terminal(keyEncoder, terminal)
        ghostty_key_event_set_action(keyEvent, action)
        ghostty_key_event_set_key(keyEvent, key)
        ghostty_key_event_set_mods(keyEvent, GhosttyMods(mods.rawValue))
        // Shift that produced the text (`:` from ⇧;) is consumed, as Ghostty's
        // own app reports it: otherwise the kitty protocol sends ⇧; as
        // ESC[59;2u and neovim never sees a `:`.
        let consumed: KeyMods = (text?.isEmpty == false) ? mods.intersection(.shift) : []
        ghostty_key_event_set_consumed_mods(keyEvent, GhosttyMods(consumed.rawValue))
        ghostty_key_event_set_unshifted_codepoint(keyEvent, unshifted)
        ghostty_key_event_set_composing(keyEvent, false)
        var out = [CChar](repeating: 0, count: 128)
        var len = 0
        let result: GhosttyResult
        if let text, !text.isEmpty {
            result = text.withCString { ptr in
                ghostty_key_event_set_utf8(keyEvent, ptr, strlen(ptr))
                return ghostty_key_encoder_encode(keyEncoder, keyEvent, &out, out.count, &len)
            }
        } else {
            ghostty_key_event_set_utf8(keyEvent, nil, 0)
            result = ghostty_key_encoder_encode(keyEncoder, keyEvent, &out, out.count, &len)
        }
        guard result == GHOSTTY_SUCCESS, len > 0 else { return nil }
        return Data(bytes: out, count: len)
    }

    /// The program asked for bracketed paste (mode 2004).
    public var bracketedPaste: Bool { decMode(2004) }

    /// libghostty's conservative check that pasting text can't run anything
    /// by itself: no line breaks and no end-of-paste sequence.
    public static func pasteIsSafe(_ text: String) -> Bool {
        let bytes = Array(text.utf8CString.dropLast())
        guard !bytes.isEmpty else { return true }
        return bytes.withUnsafeBufferPointer { ghostty_paste_is_safe($0.baseAddress, $0.count) }
    }

    /// Encodes pasted text (bracketed if the program asked for it).
    public func encodePaste(_ text: String) -> Data {
        var bytes = Array(text.utf8CString.dropLast())
        let bracketed = decMode(2004)
        var out = [CChar](repeating: 0, count: bytes.count + 32)
        var written = 0
        let r = bytes.withUnsafeMutableBufferPointer { buf in
            ghostty_paste_encode(buf.baseAddress, buf.count, bracketed, &out, out.count, &written)
        }
        guard r == GHOSTTY_SUCCESS else { return Data(text.utf8) }
        return Data(bytes: out, count: written)
    }

    /// What changed on screen since the last call, from libghostty's dirty
    /// tracking (conservative: a row may be dirty and look the same), and
    /// where the cursor is now. A renderer that draws only these rows must
    /// also redraw the rows the cursor left and reached. Calling it marks
    /// everything clean, so one caller asks for each change (PaneSession).
    public func takeChanges() -> TerminalChanges {
        guard let terminal, let renderState, rowIter != nil,
              ghostty_render_state_update(renderState, terminal) == GHOSTTY_SUCCESS else { return TerminalChanges(dirty: .full) }
        defer { ghostty_render_state_clean(renderState) }
        var changes = TerminalChanges(dirty: .none)
        var cursor = GhosttyRenderStateCursor()
        cursor.size = MemoryLayout<GhosttyRenderStateCursor>.size
        if ghostty_render_state_get(renderState, GHOSTTY_RENDER_STATE_DATA_CURSOR, &cursor) == GHOSTTY_SUCCESS,
           cursor.visible, cursor.viewport_has_value {
            changes.cursorRow = Int(cursor.viewport_y)
        }
        var dirty = GHOSTTY_RENDER_STATE_DIRTY_FULL
        ghostty_render_state_get(renderState, GHOSTTY_RENDER_STATE_DATA_DIRTY, &dirty)
        switch dirty {
        case GHOSTTY_RENDER_STATE_DIRTY_FALSE:
            break
        case GHOSTTY_RENDER_STATE_DIRTY_PARTIAL:
            guard ghostty_render_state_get(renderState, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &rowIter) == GHOSTTY_SUCCESS else {
                changes.dirty = .full
                break
            }
            var rows = IndexSet()
            var y: UInt16 = 0
            while ghostty_render_state_row_iterator_next_dirty(rowIter, &y) { rows.insert(Int(y)) }
            changes.dirty = rows.isEmpty ? .none : .rows(rows)
        default:
            changes.dirty = .full
        }
        return changes
    }

    /// Captures everything needed to draw the current viewport.
    public func frame() -> TerminalFrame {
        var f = TerminalFrame()
        guard let terminal, let renderState, rowIter != nil, rowCells != nil else { return f }
        guard ghostty_render_state_update(renderState, terminal) == GHOSTTY_SUCCESS else { return f }

        var colors = GhosttyRenderStateColors()
        colors.size = MemoryLayout<GhosttyRenderStateColors>.size
        if ghostty_render_state_get(renderState, GHOSTTY_RENDER_STATE_DATA_COLORS, &colors) == GHOSTTY_SUCCESS {
            f.background = RGB(colors.background)
            f.foreground = RGB(colors.foreground)
            if colors.cursor_has_value { f.cursorColor = RGB(colors.cursor) }
        }
        var c16: UInt16 = 0
        ghostty_render_state_get(renderState, GHOSTTY_RENDER_STATE_DATA_COLS, &c16); f.cols = Int(c16)
        ghostty_render_state_get(renderState, GHOSTTY_RENDER_STATE_DATA_ROWS, &c16); f.rows = Int(c16)

        var cursor = GhosttyRenderStateCursor()
        cursor.size = MemoryLayout<GhosttyRenderStateCursor>.size
        if ghostty_render_state_get(renderState, GHOSTTY_RENDER_STATE_DATA_CURSOR, &cursor) == GHOSTTY_SUCCESS {
            f.cursorVisible = cursor.visible && cursor.viewport_has_value
            f.cursorX = Int(cursor.viewport_x)
            f.cursorBlinking = cursor.blinking
            f.cursorY = Int(cursor.viewport_y)
            switch cursor.visual_style {
            case GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BAR: f.cursorStyle = .bar
            case GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_UNDERLINE: f.cursorStyle = .underline
            case GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BLOCK_HOLLOW: f.cursorStyle = .hollow
            default: f.cursorStyle = .block
            }
        }

        guard ghostty_render_state_get(renderState, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &rowIter) == GHOSTTY_SUCCESS else { return f }
        var cps = [UInt32](repeating: 0, count: 16)
        f.cells.reserveCapacity(f.rows)
        while ghostty_render_state_row_iterator_next(rowIter) {
            guard ghostty_render_state_row_get(rowIter, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &rowCells) == GHOSTTY_SUCCESS else {
                f.cells.append([])
                continue
            }
            var row: [TerminalCell] = []
            row.reserveCapacity(f.cols)
            while ghostty_render_state_row_cells_next(rowCells) {
                var cell = TerminalCell(text: "")
                var raw = GhosttyCell()
                if ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &raw) == GHOSTTY_SUCCESS {
                    var wide = GHOSTTY_CELL_WIDE_NARROW
                    ghostty_cell_get(raw, GHOSTTY_CELL_DATA_WIDE, &wide)
                    cell.wide = wide == GHOSTTY_CELL_WIDE_WIDE
                    cell.spacer = wide == GHOSTTY_CELL_WIDE_SPACER_TAIL || wide == GHOSTTY_CELL_WIDE_SPACER_HEAD
                }
                var glen: UInt32 = 0
                ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_LEN, &glen)
                if glen > 0 {
                    if Int(glen) > cps.count { cps = [UInt32](repeating: 0, count: Int(glen)) }
                    ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_BUF, &cps)
                    // Kitty graphics Unicode placeholders (U+10EEEE plus
                    // diacritics for row and column) mark where an image goes;
                    // the image is drawn there, the placeholder never is.
                    if cps[0] != kittyPlaceholder {
                        var s = String.UnicodeScalarView()
                        for i in 0..<Int(glen) { if let u = Unicode.Scalar(cps[i]) { s.append(u) } }
                        cell.text = String(s)
                    }
                }
                var hasStyle = false
                ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_HAS_STYLING, &hasStyle)
                if hasStyle {
                    var style = GhosttyStyle()
                    style.size = MemoryLayout<GhosttyStyle>.size
                    if ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style) == GHOSTTY_SUCCESS {
                        cell.bold = style.bold
                        cell.italic = style.italic
                        cell.faint = style.faint
                        cell.inverse = style.inverse
                        cell.invisible = style.invisible
                        cell.strikethrough = style.strikethrough
                        cell.underline = style.underline != Int32(GHOSTTY_SGR_UNDERLINE_NONE.rawValue)
                    }
                    var rgb = GhosttyColorRgb()
                    if ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &rgb) == GHOSTTY_SUCCESS {
                        cell.fg = RGB(rgb)
                    }
                    if ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &rgb) == GHOSTTY_SUCCESS {
                        cell.bg = RGB(rgb)
                    }
                }
                var selected = false
                ghostty_render_state_row_cells_get(rowCells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_SELECTED, &selected)
                cell.selected = selected
                row.append(cell)
            }
            f.cells.append(row)
        }
        return f
    }

    /// Plain text of the viewport (for tests and accessibility).
    public func text() -> String {
        frame().cells.map { row in
            row.map { $0.spacer ? "" : ($0.text.isEmpty ? " " : $0.text) }.joined()
                .replacingOccurrences(of: "\\s+$", with: "", options: .regularExpression)
        }.joined(separator: "\n")
    }
}

/// The Kitty graphics protocol's Unicode placeholder character.
let kittyPlaceholder: UInt32 = 0x10EEEE
