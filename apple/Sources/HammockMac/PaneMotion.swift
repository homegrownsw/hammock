import AppKit
import SwiftUI

/// Pane transitions (zooming, unzooming, switching the zoomed pane, a pane
/// floating out of the splits): one animation, and while it runs, terminals
/// hold still. SwiftUI moves a hosted NSView's frame on every frame of an
/// animation; a terminal that redrew its whole grid with CoreText and told
/// the daemon (and so its program) about a new size each time is what made
/// these stutter. Instead a terminal keeps its last drawing pinned to its
/// top left (the theme's background fills what it uncovers) and resizes
/// once, when the panes settle.
@MainActor
final class PaneMotion {
    static let shared = PaneMotion()
    /// Posted when the last running pane transition ends.
    static let settled = Notification.Name("HammockPaneMotionSettled")
    nonisolated static let duration = 0.28
    /// No bounce: panes are big, and overshooting reads as wobble.
    nonisolated static let animation = Animation.smooth(duration: duration)
    /// Switching which pane is zoomed: you're moving to the other pane, so
    /// it should be there almost at once, and its terminal free to draw and
    /// resize, not held still through a full zoom.
    nonisolated static let switchDuration = 0.14
    nonisolated static let switchAnimation = Animation.smooth(duration: switchDuration)
    /// The sidebar sliding in or out: AppKit's split view animates it (about
    /// 0.2s), resizing every pane in the tab as it goes; a little longer, so
    /// the last frames don't resize terminals either.
    nonisolated static let sidebarDuration = 0.35

    private var running = 0
    private var generation = 0
    var isMoving: Bool { running > 0 }

    /// Runs a pane change in the pane animation (or a quicker one).
    func animate(_ animation: Animation = PaneMotion.animation, _ change: () -> Void) {
        begin()
        withAnimation(animation, completionCriteria: .logicallyComplete, change) { [weak self] in self?.end() }
    }

    /// Marks panes as moving for an animation run elsewhere (FloatMotion).
    func hold(for seconds: Double) {
        begin()
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds) { [weak self] in self?.end() }
    }

    private func begin() {
        running += 1
        generation += 1
        // Never stuck holding still: a completion that doesn't come (the
        // view gone mid-animation) is let go of after a while.
        let g = generation
        DispatchQueue.main.asyncAfter(deadline: .now() + Self.duration + 0.6) { [weak self] in
            guard let self, self.generation == g, self.running > 0 else { return }
            self.running = 0
            NotificationCenter.default.post(name: Self.settled, object: nil)
        }
    }

    private func end() {
        guard running > 0 else { return }
        running -= 1
        if running == 0 { NotificationCenter.default.post(name: Self.settled, object: nil) }
    }
}

private struct PaneFoldedKey: EnvironmentKey { static let defaultValue = false }

extension EnvironmentValues {
    /// The pane is folded under another one zoomed in its tab: still there
    /// (so unzooming reveals it, not rebuilds it), but out of sight.
    var paneFolded: Bool {
        get { self[PaneFoldedKey.self] }
        set { self[PaneFoldedKey.self] = newValue }
    }
}

/// How a tab's split panes look (Settings ▸ Appearance ▸ Panes).
@MainActor
enum PaneLook {
    private static var config: AppConfig { .shared }
    static var spacious: Bool { config.paneSpacing == .spacious }
    /// Between panes: a hairline, or a gap.
    static var gap: CGFloat { spacious ? 6 : 1 }
    /// Around the panes, against the tab's edges.
    static var inset: CGFloat { spacious ? 6 : 0 }
    static var radius: CGFloat { spacious ? 8 : 0 }
    static var shape: RoundedRectangle { RoundedRectangle(cornerRadius: radius, style: .continuous) }

    /// The fade for focus moving: border and dimming.
    static let fade = Animation.easeInOut(duration: 0.16)

    static func borderColor(dark: Bool) -> Color {
        guard let c = config.activePaneBorderColor else { return Color.themeAccent }
        return Color(nsColor: dark ? c.dark : c.light)
    }
}

/// The focused pane's border and the others' dimming, as Settings asks,
/// fading as focus moves. Over the pane, never taking its clicks.
struct PaneFocusLook: ViewModifier {
    let isFocused: Bool
    /// Only panes that share their tab with another dim or get a border.
    let among: Bool
    @Environment(\.colorScheme) private var colorScheme
    private var config: AppConfig { .shared }

    func body(content: Content) -> some View {
        let dim = among && config.dimInactivePanes && !isFocused
        let border = among && config.activePaneBorder && isFocused
        content
            .overlay {
                PaneLook.shape
                    .fill(Color.black.opacity(colorScheme == .dark ? 0.22 : 0.07))
                    .opacity(dim ? 1 : 0)
                    .animation(PaneLook.fade, value: dim)
                    .allowsHitTesting(false)
            }
            .overlay {
                PaneLook.shape
                    .strokeBorder(PaneLook.borderColor(dark: colorScheme == .dark), lineWidth: PaneLook.spacious ? 2 : 1.5)
                    .opacity(border ? 1 : 0)
                    .animation(PaneLook.fade, value: border)
                    .allowsHitTesting(false)
            }
    }
}
