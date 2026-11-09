import Foundation
import GhosttyVt

public enum MouseAction: Sendable { case press, release, motion }

/// Mouse buttons; 4/5 are the wheel (up/down).
public enum MouseButton: Int, Sendable {
    case left = 1, right = 2, middle = 3, wheelUp = 4, wheelDown = 5
}

/// Geometry needed to turn view positions into cells.
public struct MouseGeometry: Sendable {
    public var width, height, cellWidth, cellHeight, padding: Float
    /// Device pixels per point, for programs that ask for pixel positions.
    public var scale: Float
    public init(width: Float, height: Float, cellWidth: Float, cellHeight: Float, padding: Float, scale: Float = 1) {
        self.width = width; self.height = height; self.cellWidth = cellWidth; self.cellHeight = cellHeight; self.padding = padding
        self.scale = scale
    }
}

extension TerminalEngine {
    /// Whether the program is showing the alternate screen (full-screen apps).
    public var isAlternateScreen: Bool {
        guard let terminal = handle else { return false }
        var screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY
        _ = ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &screen)
        return screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE
    }

    /// Encodes a mouse event for a program that enabled mouse reporting.
    /// Returns nil when the program isn't tracking this kind of event.
    public func encodeMouse(_ action: MouseAction, button: MouseButton?, x: Float, y: Float, mods: KeyMods, geometry g: MouseGeometry) -> Data? {
        guard let terminal = handle else { return nil }
        if mouseEncoder == nil {
            var enc: GhosttyMouseEncoder?
            ghostty_mouse_encoder_new(nil, &enc)
            var ev: GhosttyMouseEvent?
            ghostty_mouse_event_new(nil, &ev)
            mouseEncoder = enc
            mouseEvent = ev
        }
        guard let enc = mouseEncoder, let ev = mouseEvent else { return nil }
        ghostty_mouse_encoder_setopt_from_terminal(enc, terminal)
        // The encoder works in whole pixels, but cells are often fractional
        // (7.5pt wide): truncated, every column came out a little narrow and
        // a click far along a line landed several cells right of it. Find the
        // cell with the real size, then give the encoder the same spot on a
        // grid of whole device pixels.
        let k = max(g.scale, 1)
        let cw = max(g.cellWidth, 1), ch = max(g.cellHeight, 1)
        let cwPx = max((cw * k).rounded(), 1), chPx = max((ch * k).rounded(), 1), padPx = (g.padding * k).rounded()
        let cols = ((g.width - 2 * g.padding) / cw).rounded(.down), rows = ((g.height - 2 * g.padding) / ch).rounded(.down)
        let px = padPx + (x - g.padding) / cw * cwPx, py = padPx + (y - g.padding) / ch * chPx
        var size = GhosttyMouseEncoderSize()
        size.size = MemoryLayout<GhosttyMouseEncoderSize>.size
        size.screen_width = UInt32(max(2 * padPx + cols * cwPx, 1))
        size.screen_height = UInt32(max(2 * padPx + rows * chPx, 1))
        size.cell_width = UInt32(cwPx)
        size.cell_height = UInt32(chPx)
        size.padding_top = UInt32(padPx)
        size.padding_left = UInt32(padPx)
        ghostty_mouse_encoder_setopt(enc, GHOSTTY_MOUSE_ENCODER_OPT_SIZE, &size)
        // Button-event tracking (mode 1002) only reports motion while a button is
        // held; setopt_from_terminal leaves that state to us.
        if let button, button != .wheelUp, button != .wheelDown {
            if action == .press { mouseButtonDown = true }
            if action == .release { mouseButtonDown = false }
        }
        var down = mouseButtonDown
        ghostty_mouse_encoder_setopt(enc, GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED, &down)

        switch action {
        case .press: ghostty_mouse_event_set_action(ev, GHOSTTY_MOUSE_ACTION_PRESS)
        case .release: ghostty_mouse_event_set_action(ev, GHOSTTY_MOUSE_ACTION_RELEASE)
        case .motion: ghostty_mouse_event_set_action(ev, GHOSTTY_MOUSE_ACTION_MOTION)
        }
        if let button {
            ghostty_mouse_event_set_button(ev, GhosttyMouseButton(rawValue: Int32(button.rawValue)))
        } else {
            ghostty_mouse_event_clear_button(ev)
        }
        ghostty_mouse_event_set_mods(ev, GhosttyMods(mods.rawValue))
        ghostty_mouse_event_set_position(ev, GhosttyMousePosition(x: px, y: py))
        var out = [CChar](repeating: 0, count: 64)
        var len = 0
        guard ghostty_mouse_encoder_encode(enc, ev, &out, out.count, &len) == GHOSTTY_SUCCESS, len > 0 else { return nil }
        return Data(bytes: out, count: len)
    }
}
