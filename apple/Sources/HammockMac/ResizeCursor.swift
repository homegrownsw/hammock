import AppKit

/// The resize cursor of a split's divider or a floating pane's edges: shown
/// while the pointer is over one and kept through a drag, though the pointer
/// outruns the handle (the layout follows a frame behind) and crosses panes
/// that set their own (a browser's page, the agent view's text).
///
/// Each handle area owns one. Views that set their own cursor leave it alone
/// while a drag is `holding` it.
@MainActor
final class ResizeCursor {
    static private var held: NSCursor?

    /// A drag is holding the resize cursor, anywhere in the app (the button
    /// still down, so a drag cancelled without ending doesn't hold it on).
    static var holding: Bool { held != nil && NSEvent.pressedMouseButtons & 1 != 0 }

    private var hovered: [AnyHashable: NSCursor] = [:]
    private var dragging: NSCursor?
    private var pushed = false

    /// The pointer came over (or left) a handle showing `cursor`.
    func hover(_ id: AnyHashable, _ inside: Bool, _ cursor: NSCursor) {
        hovered[id] = inside ? cursor : nil
        sync()
    }

    /// A drag moved: its cursor again, over anything that took it between
    /// events.
    func drag(_ cursor: NSCursor) {
        dragging = cursor
        Self.held = cursor
        sync()
        cursor.set()
    }

    /// The drag ended: the cursor goes back to what's under the pointer
    /// unless that's still a handle.
    func ended() {
        dragging = nil
        Self.held = nil
        sync()
    }

    /// The handles went away (the pane closed or unsplit).
    func gone() {
        hovered = [:]
        ended()
    }

    private func sync() {
        let want = dragging ?? hovered.values.first
        if let want {
            if !pushed { want.push(); pushed = true } else { want.set() }
        } else if pushed {
            NSCursor.pop()
            pushed = false
        }
    }
}
