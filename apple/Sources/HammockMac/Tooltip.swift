import AppKit
import SwiftUI

extension View {
    /// A tooltip that shows even while the view keeps updating. SwiftUI's
    /// `.help` is reinstalled on every update, which restarts the wait before
    /// it shows; pane headers update whenever any pane changes (an agent at
    /// work retitles its pane several times a second), so theirs never
    /// appeared. This one is AppKit's own, set only when the text changes.
    func tooltip(_ text: String) -> some View {
        background(TooltipView(text: text))
            .accessibilityHint(text)
    }
}

private struct TooltipView: NSViewRepresentable {
    let text: String

    func makeNSView(context: Context) -> NSView {
        let v = PassThroughView()
        v.toolTip = text
        return v
    }

    func updateNSView(_ v: NSView, context: Context) {
        if v.toolTip != text { v.toolTip = text }
    }

    /// Never takes a click: the control in front of it does.
    private final class PassThroughView: NSView {
        override func hitTest(_ point: NSPoint) -> NSView? { nil }
    }
}
