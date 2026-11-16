import AppKit
import SwiftUI

/// Alerts' and panels' default buttons in the theme's colour. AppKit draws
/// them in the app's own accent (the asset catalog's: Hammock's green,
/// whatever the theme), and SwiftUI's tint reaches neither NSAlert nor
/// SwiftUI's own alerts (`.alert`, `.confirmationDialog`, which are
/// NSAlerts). So when a panel comes up, its default button takes the
/// theme's colour as its bezel.
@MainActor
enum ThemedAlerts {
    private static var observer: NSObjectProtocol?

    static func start() {
        guard observer == nil else { return }
        // Posted on the main thread; handled there and then (not queued), so
        // it's done before a modal alert's run loop draws the button.
        observer = NotificationCenter.default.addObserver(forName: NSWindow.didBecomeKeyNotification, object: nil, queue: nil) { note in
            MainActor.assumeIsolated {
                guard let panel = note.object as? NSPanel, let root = panel.contentView else { return }
                tint(root, NSColor(Color.themeAccent))
            }
        }
    }

    private static func tint(_ view: NSView, _ color: NSColor) {
        if let button = view as? NSButton, button.keyEquivalent == "\r", button.bezelColor != color {
            button.bezelColor = color
        }
        for v in view.subviews { tint(v, color) }
    }
}
