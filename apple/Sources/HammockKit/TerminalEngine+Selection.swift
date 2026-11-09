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
        guard let terminal = handle else { return }
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_SELECTION, nil)
    }

    /// The selected text, or nil when nothing is selected.
    public func selectionText() -> String? {
        guard let terminal = handle else { return nil }
        var opts = GhosttyTerminalSelectionFormatOptions()
        opts.size = MemoryLayout<GhosttyTerminalSelectionFormatOptions>.size
        opts.emit = GHOSTTY_FORMATTER_FORMAT_PLAIN
        opts.unwrap = true
        opts.trim = true
        opts.selection = nil
        var ptr: UnsafeMutablePointer<UInt8>?
        var len = 0
        guard ghostty_terminal_selection_format_alloc(terminal, nil, opts, &ptr, &len) == GHOSTTY_SUCCESS, let ptr else { return nil }
        defer { ghostty_free(nil, ptr, len) }
        let s = String(decoding: UnsafeBufferPointer(start: ptr, count: len), as: UTF8.self)
        return s.isEmpty ? nil : s
    }
}
