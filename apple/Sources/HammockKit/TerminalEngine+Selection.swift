import CoreGraphics
import Foundation
import GhosttyVt

extension TerminalEngine {
    private func gridRef(x: Int, y: Int) -> GhosttyGridRef? {
        guard let terminal = handle else { return nil }
        var ref = GhosttyGridRef()
        ref.size = MemoryLayout<GhosttyGridRef>.size
        var pt = GhosttyPoint()
        pt.tag = GHOSTTY_POINT_TAG_VIEWPORT
        pt.value.coordinate = GhosttyPointCoordinate(x: UInt16(clamping: max(0, min(x, cols - 1))), y: UInt32(max(0, min(y, rows - 1))))
        guard ghostty_terminal_grid_ref(terminal, pt, &ref) == GHOSTTY_SUCCESS else { return nil }
        return ref
    }

    /// Selects from one viewport cell to another (inclusive).
    public func select(fromX: Int, fromY: Int, toX: Int, toY: Int) {
        guard let terminal = handle, let a = gridRef(x: fromX, y: fromY), let b = gridRef(x: toX, y: toY) else { return }
        var sel = GhosttySelection()
        sel.size = MemoryLayout<GhosttySelection>.size
        sel.start = a
        sel.end = b
        sel.rectangle = false
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_SELECTION, &sel)
    }

    // MARK: Mouse selection

    /// Where the grid sits in the view, in pixels (the gesture's surface space).
    public struct SelectionGeometry {
        public var cellWidth: Double
        public var cellHeight: Double
        public var padding: Double
        public var height: Double
        public init(cellWidth: Double, cellHeight: Double, padding: Double, height: Double) {
            self.cellWidth = cellWidth
            self.cellHeight = cellHeight
            self.padding = padding
            self.height = height
        }
    }

    public enum SelectionPhase { case press, drag, release }

    /// Feeds a pointer event to Ghostty's own selection gesture and installs
    /// the resulting selection. Ghostty decides which cells a drag covers
    /// (a cell only counts once the pointer is past its middle), follows the
    /// press point as output scrolls the screen, and turns double and triple
    /// clicks into word and line selections.
    ///
    /// `point` is in pixels with a top-left origin; `time` is the event's
    /// timestamp in seconds (for counting repeat clicks).
    public func selectionGesture(_ phase: SelectionPhase, at point: CGPoint, time: TimeInterval,
                                 geometry g: SelectionGeometry, repeatInterval: TimeInterval) {
        guard let terminal else { return }
        if selectionGesture == nil { ghostty_selection_gesture_new(nil, &selectionGesture) }
        let type: GhosttySelectionGestureEventType = switch phase {
        case .press: GHOSTTY_SELECTION_GESTURE_EVENT_TYPE_PRESS
        case .drag: GHOSTTY_SELECTION_GESTURE_EVENT_TYPE_DRAG
        case .release: GHOSTTY_SELECTION_GESTURE_EVENT_TYPE_RELEASE
        }
        guard let gesture = selectionGesture, let event = selectionEvent(type) else { return }

        let col = Int(floor((point.x - g.padding) / g.cellWidth))
        let row = Int(floor((point.y - g.padding) / g.cellHeight))
        guard var ref = gridRef(x: col, y: row) else { return }
        ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_REF, &ref)
        if phase != .release {
            var pos = GhosttySurfacePosition(x: point.x, y: point.y)
            ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_POSITION, &pos)
        }
        switch phase {
        case .press:
            var ns = UInt64(max(time, 0) * 1e9)
            var interval = UInt64(repeatInterval * 1e9)
            var distance = g.cellWidth
            ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_TIME_NS, &ns)
            ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_REPEAT_INTERVAL_NS, &interval)
            ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_REPEAT_DISTANCE, &distance)
        case .drag:
            var geom = GhosttySelectionGestureGeometry(
                columns: UInt32(cols), cell_width: UInt32(max(g.cellWidth.rounded(), 1)),
                padding_left: UInt32(max(g.padding.rounded(), 0)), screen_height: UInt32(max(g.height.rounded(), 1)))
            ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_GEOMETRY, &geom)
        case .release:
            break
        }

        var sel = GhosttySelection()
        sel.size = MemoryLayout<GhosttySelection>.size
        let r = ghostty_selection_gesture_event(gesture, terminal, event, &sel)
        guard phase != .release else { return }
        if r == GHOSTTY_SUCCESS {
            ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_SELECTION, &sel)
        } else if r == GHOSTTY_NO_VALUE {
            // A plain click, or a drag that hasn't crossed a cell's middle yet.
            ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_SELECTION, nil)
        }
    }

    private func selectionEvent(_ type: GhosttySelectionGestureEventType) -> GhosttySelectionGestureEvent? {
        if let e = selectionEvents[type.rawValue] { return e }
        var e: GhosttySelectionGestureEvent?
        guard ghostty_selection_gesture_event_new(nil, &e, type) == GHOSTTY_SUCCESS, let e else { return nil }
        selectionEvents[type.rawValue] = e
        return e
    }

    public func clearSelection() {
        wordAnchor = nil
        guard let terminal = handle else { return }
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_SELECTION, nil)
    }

    /// The selected text, or nil when nothing is selected: plain text,
    /// trailing spaces trimmed and soft-wrapped lines joined.
    public func selectionText() -> String? {
        format(nil)
    }

    /// The screen as it shows now (scrolled back or not), as plain text
    /// the way a selection of it copies: Copy Screen.
    public func screenText() -> String? {
        guard handle != nil, let a = gridRef(x: 0, y: 0), let b = gridRef(x: cols - 1, y: rows - 1) else { return nil }
        var sel = GhosttySelection()
        sel.size = MemoryLayout<GhosttySelection>.size
        sel.start = a
        sel.end = b
        return withUnsafePointer(to: &sel) { format($0) }
    }

    private func format(_ selection: UnsafePointer<GhosttySelection>?) -> String? {
        guard let terminal = handle else { return nil }
        var opts = GhosttyTerminalSelectionFormatOptions()
        opts.size = MemoryLayout<GhosttyTerminalSelectionFormatOptions>.size
        opts.emit = GHOSTTY_FORMATTER_FORMAT_PLAIN
        opts.unwrap = true
        opts.trim = true
        opts.selection = selection
        var ptr: UnsafeMutablePointer<UInt8>?
        var len = 0
        guard ghostty_terminal_selection_format_alloc(terminal, nil, opts, &ptr, &len) == GHOSTTY_SUCCESS, let ptr else { return nil }
        defer { ghostty_free(nil, ptr, len) }
        let s = String(decoding: UnsafeBufferPointer(start: ptr, count: len), as: UTF8.self)
        return s.isEmpty ? nil : s
    }

    // MARK: Touch selection (the phones)

    private func install(_ sel: inout GhosttySelection) {
        guard let terminal = handle else { return }
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_SELECTION, &sel)
    }

    private func selection(start: GhosttyGridRef, end: GhosttyGridRef) -> GhosttySelection {
        var sel = GhosttySelection()
        sel.size = MemoryLayout<GhosttySelection>.size
        sel.start = start
        sel.end = end
        sel.rectangle = false
        return sel
    }

    /// The current selection, its ends in reading order.
    private func orderedSelection() -> GhosttySelection? {
        guard let terminal = handle else { return nil }
        var sel = GhosttySelection()
        sel.size = MemoryLayout<GhosttySelection>.size
        guard ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_SELECTION, &sel) == GHOSTTY_SUCCESS else { return nil }
        var out = GhosttySelection()
        out.size = MemoryLayout<GhosttySelection>.size
        guard ghostty_terminal_selection_ordered(terminal, &sel, GHOSTTY_SELECTION_ORDER_FORWARD, &out) == GHOSTTY_SUCCESS else { return nil }
        return out
    }

    private func screenPoint(_ ref: GhosttyGridRef) -> GhosttyPointCoordinate? {
        guard let terminal = handle else { return nil }
        var ref = ref
        var p = GhosttyPointCoordinate()
        guard ghostty_terminal_point_from_grid_ref(terminal, &ref, GHOSTTY_POINT_TAG_SCREEN, &p) == GHOSTTY_SUCCESS else { return nil }
        return p
    }

    private func screenRef(_ p: GhosttyPointCoordinate) -> GhosttyGridRef? {
        guard let terminal = handle else { return nil }
        var ref = GhosttyGridRef()
        ref.size = MemoryLayout<GhosttyGridRef>.size
        var pt = GhosttyPoint()
        pt.tag = GHOSTTY_POINT_TAG_SCREEN
        pt.value.coordinate = p
        guard ghostty_terminal_grid_ref(terminal, pt, &ref) == GHOSTTY_SUCCESS else { return nil }
        return ref
    }

    private static func before(_ a: GhosttyPointCoordinate, _ b: GhosttyPointCoordinate) -> Bool {
        a.y != b.y ? a.y < b.y : a.x < b.x
    }

    /// The word at a viewport cell, as its first and last cells; nil on a blank.
    private func word(at ref: GhosttyGridRef) -> GhosttySelection? {
        guard let terminal = handle else { return nil }
        var opts = GhosttyTerminalSelectWordOptions()
        opts.size = MemoryLayout<GhosttyTerminalSelectWordOptions>.size
        opts.ref = ref
        var sel = GhosttySelection()
        sel.size = MemoryLayout<GhosttySelection>.size
        guard ghostty_terminal_select_word(terminal, &opts, &sel) == GHOSTTY_SUCCESS else { return nil }
        return sel
    }

    /// Selects the word at a viewport cell (Ghostty's rules, as a double
    /// click on the Mac). Returns false, selecting nothing, on a blank.
    @discardableResult
    public func selectWord(x: Int, y: Int) -> Bool {
        wordAnchor = nil
        guard let ref = gridRef(x: x, y: y), var sel = word(at: ref) else {
            clearSelection()
            return false
        }
        if let a = screenPoint(sel.start), let b = screenPoint(sel.end) {
            wordAnchor = Self.before(b, a) ? (b, a) : (a, b)
        }
        install(&sel)
        return true
    }

    /// Dragging on from a touch and hold: from the word it selected to the
    /// word at a viewport cell (or the cell itself, between words).
    public func extendWordSelection(toX x: Int, y: Int) {
        guard let anchor = wordAnchor, let a = screenRef(anchor.start), let b = screenRef(anchor.end),
              let ref = gridRef(x: x, y: y), let here = screenPoint(ref) else { return }
        // The word here, first cell to last (or the cell alone, between words).
        var lo = here, hi = here
        if let w = word(at: ref), let p = screenPoint(w.start), let q = screenPoint(w.end) {
            (lo, hi) = Self.before(q, p) ? (q, p) : (p, q)
        }
        var sel: GhosttySelection
        if Self.before(lo, anchor.start) {
            sel = selection(start: screenRef(lo) ?? ref, end: b)
        } else if Self.before(anchor.end, hi) {
            sel = selection(start: a, end: screenRef(hi) ?? ref)
        } else {
            sel = selection(start: a, end: b)
        }
        install(&sel)
    }

    /// Selects the whole screen as it shows now (the Mac's Select All).
    public func selectScreen() {
        wordAnchor = nil
        select(fromX: 0, fromY: 0, toX: cols - 1, toY: rows - 1)
    }

    /// The selection's first and last cells, in viewport cells: a row
    /// above the screen is negative, one below it `rows` or more. Nil
    /// when nothing is selected.
    public func selectionBounds() -> (start: TerminalCellPoint, end: TerminalCellPoint)? {
        guard let sel = orderedSelection(), let top = gridRef(x: 0, y: 0).flatMap(screenPoint),
              let a = screenPoint(sel.start), let b = screenPoint(sel.end) else { return nil }
        func cell(_ p: GhosttyPointCoordinate) -> TerminalCellPoint { TerminalCellPoint(x: Int(p.x), y: Int(p.y) - Int(top.y)) }
        return (cell(a), cell(b))
    }

    /// Moves one end of the selection to a viewport cell, keeping the
    /// other where it is (a selection handle dragged). Returns which end
    /// the moved one is now: dragged past the other, the start becomes
    /// the end.
    @discardableResult
    public func moveSelection(_ edge: SelectionEdge, toX x: Int, y: Int) -> SelectionEdge {
        wordAnchor = nil
        guard let sel = orderedSelection(), let ref = gridRef(x: x, y: y) else { return edge }
        let fixed = edge == .start ? sel.end : sel.start
        guard let fixedAt = screenPoint(fixed), let here = screenPoint(ref) else { return edge }
        let moved: SelectionEdge = Self.before(here, fixedAt) ? .start : .end
        var new = moved == .start ? selection(start: ref, end: fixed) : selection(start: fixed, end: ref)
        install(&new)
        return moved
    }
}

/// A cell, by column and row: in the viewport for touch selection, where
/// a row can be off the screen (negative above it).
public struct TerminalCellPoint: Equatable, Hashable, Sendable {
    public var x: Int
    public var y: Int
    public init(x: Int, y: Int) {
        self.x = x
        self.y = y
    }
}

/// A selection's two ends, and its two handles on the phones.
public enum SelectionEdge: Sendable { case start, end }
