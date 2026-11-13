import AppKit
import HammockKit
import SwiftUI

/// Pane transitions (zooming, unzooming, switching the zoomed pane, a pane
/// floating out of the splits): one animation, and while it runs, terminals
/// resize once. SwiftUI moves a hosted NSView's frame on every frame of an
/// animation; a terminal that redrew its whole grid with CoreText and told
/// the daemon (and so its program) about a new size each time is what made
/// these stutter. Instead, in an animation run here, a terminal Metal draws
/// resizes to where it's going as it starts (SwiftUI's layout proposes the
/// final size from the first frame), and draws at that size pinned to its
/// top left, so the pane grows over (or shrinks onto) its new layout. Any
/// other terminal, or in a hold (the sidebar, where each frame proposes a
/// new size), keeps its last drawing pinned top left (the theme's
/// background fills what it uncovers) and resizes when the panes settle.
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

    /// Settings ▸ Appearance ▸ Pane animations: off, panes change at once.
    /// Reduce Motion (Accessibility) turns them off too.
    static var enabled: Bool {
        AppConfig.shared.paneAnimations && !NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
    }
    /// `animation`, unless pane animations are off.
    static func when(_ animation: Animation) -> Animation? { enabled ? animation : nil }

    /// Panes opening and closing, and the closing ones as they were, for
    /// their views to keep drawing as they go (the daemon's update deletes
    /// them as their split closes up).
    private(set) var departing: [String: Pane] = [:]
    /// A pane opening or closing is running (OpenCloseMotion), not a pane
    /// floating out or docking.
    private(set) var openingOrClosing = false
    /// Panes opening or closing with no neighbour to grow from or shrink
    /// onto: they fade (Fade).
    fileprivate(set) var fades: Set<String> = []

    private var running = 0
    private var holds = 0
    private var generation = 0
    var isMoving: Bool { running > 0 }
    /// Panes are moving in an animation run here, and nothing else: the
    /// size SwiftUI proposes a terminal is where it's going, so it can
    /// resize to it now.
    var resizesAhead: Bool { running > 0 && holds == 0 }

    /// Runs a pane change in the pane animation (or a quicker one), or at
    /// once with pane animations off.
    func animate(_ animation: Animation = PaneMotion.animation, _ change: () -> Void) {
        guard Self.enabled else {
            var t = Transaction()
            t.disablesAnimations = true
            withTransaction(t, change)
            return
        }
        begin()
        withAnimation(animation, completionCriteria: .logicallyComplete, change) { [weak self] in self?.end() }
    }

    /// Panes opening (a split, a new pane from hmk or an agent) or closing in
    /// a tab: they grow out of, or shrink onto, a neighbour's edge as the
    /// panes beside them make room or close up, over the pane animation
    /// (OpenCloseMotion). `apply` is the daemon's update that brings it.
    func openClose(closing: [Pane], _ apply: () -> Void) {
        guard Self.enabled else { return animate(Self.animation, apply) }
        for p in closing { departing[p.id] = p }
        openingOrClosing = true
        animate(Self.animation, apply)
    }

    /// Marks panes as moving for an animation run elsewhere (FloatMotion,
    /// the sidebar): terminals hold still until it's over.
    func hold(for seconds: Double) {
        holds += 1
        begin()
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds) { [weak self] in
            guard let self else { return }
            self.holds = max(self.holds - 1, 0)
            self.end()
        }
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
            self.holds = 0
            self.settle()
        }
    }

    private func end() {
        guard running > 0 else { return }
        running -= 1
        if running == 0 { settle() }
    }

    private func settle() {
        departing = [:]
        fades = []
        openingOrClosing = false
        NotificationCenter.default.post(name: Self.settled, object: nil)
    }
}

extension PaneMotion {
    /// A pane coming or going that has no neighbour to open from or close
    /// onto (OpenCloseMotion): it fades. Anything else comes and goes as it
    /// is: opening and closing ones as panes of no size at a neighbour's
    /// edge, a pane floated out or docked under its card.
    struct Fade: Transition {
        let id: String
        func body(content: Content, phase: TransitionPhase) -> some View {
            content.opacity(!phase.isIdentity && PaneMotion.shared.fades.contains(id) ? 0 : 1)
        }
    }

    func fade(_ id: String) { fades.insert(id) }
}

/// Panes opening and closing in a tab (LayoutView), as real panes whose
/// place grows out of, or shrinks onto, the edge of the neighbour they open
/// beside or close onto: border, header, terminal and hairlines all move
/// with it, as they do zooming.
///
/// Opening: the layout as it was is drawn first, with the new pane at no
/// size at its neighbour's edge; then the new layout, in the pane animation.
/// Closing: the new layout at once, in the update's animation, with the
/// closed pane kept at no size at its neighbour's edge until the panes
/// settle.
@MainActor
final class OpenCloseMotion {
    private var last: PaneLayout?
    /// Drawn in place of the tab's layout until an opening's first frame is up.
    private var from: PaneLayout?
    private var opening: [String: PanePlan.Tile] = [:]
    private var closing: [String: PanePlan.Tile] = [:]

    /// The plan to draw for layout, and the panes opening or closing that
    /// it doesn't place (at no size). redraw draws again (LayoutView's state).
    func plan(_ layout: PaneLayout, in area: CGRect, zoomed: String?, gap: CGFloat, drag: PanePlan.Drag?,
              redraw: @escaping () -> Void) -> (plan: PanePlan, ghosts: [PanePlan.Tile]) {
        if let last, layout != last { changed(from: last, to: layout, in: area, zoomed: zoomed, gap: gap, redraw: redraw) }
        last = layout
        let plan = PanePlan(from ?? layout, in: area, zoomed: zoomed, gap: gap, drag: drag)
        let placed = Set(plan.tiles.map(\.id))
        let ghosts = (Array(opening.values) + Array(closing.values)).filter { !placed.contains($0.id) }.sorted { $0.id < $1.id }
        return (plan, ghosts)
    }

    /// The panes have settled: closed ones go. Whether anything changed.
    func settled() -> Bool {
        guard !closing.isEmpty else { return false }
        closing = [:]
        return true
    }

    private func changed(from old: PaneLayout, to layout: PaneLayout, in area: CGRect, zoomed: String?, gap: CGFloat, redraw: @escaping () -> Void) {
        let motion = PaneMotion.shared
        guard motion.openingOrClosing, zoomed == nil else {
            from = nil
            opening = [:]
            closing = [:]
            return
        }
        let was = Set(AppModel.paneIDs(old)), now = Set(AppModel.paneIDs(layout))
        let before = PanePlan(from ?? old, in: area, zoomed: nil, gap: gap)
        let after = PanePlan(layout, in: area, zoomed: nil, gap: gap)
        var opened: [String: PanePlan.Tile] = [:]
        for id in now.subtracting(was) {
            if let g = Self.ghost(id, in: layout, placedBy: before) { opened[id] = g } else { motion.fade(id) }
        }
        for id in was.subtracting(now) where motion.departing[id] != nil {
            if let g = Self.ghost(id, in: old, placedBy: after) { closing[id] = g } else { motion.fade(id) }
        }
        guard !opened.isEmpty else { return }
        opening.merge(opened) { a, _ in a }
        if from == nil { from = old }
        // The first frame drawn as it was, the new pane at no size; then
        // everything moves to the new layout together.
        DispatchQueue.main.async { [weak self] in
            guard let self, self.from != nil else { return }
            // Still opening (should the update's own animation have ended).
            PaneMotion.shared.openClose(closing: []) {
                self.from = nil
                self.opening = [:]
                redraw()
            }
        }
    }

    /// A pane of no size where pane id meets its neighbour in layout (the
    /// next child of its split, else the one before), at that neighbour's
    /// edge as plan places it (flush with it: past it, a pane at the tab's
    /// edge would start out of sight).
    static func ghost(_ id: String, in layout: PaneLayout, placedBy plan: PanePlan) -> PanePlan.Tile? {
        guard let (sibling, after, horizontal) = neighbour(id, in: layout) else { return nil }
        let ids = Set(AppModel.paneIDs(sibling))
        let rects = plan.tiles.filter { ids.contains($0.id) && !$0.folded }.map(\.clip)
        guard var r = rects.first else { return nil }
        for x in rects.dropFirst() { r = r.union(x) }
        let g = horizontal ? CGRect(x: after ? r.maxX : r.minX, y: r.minY, width: 0, height: r.height)
            : CGRect(x: r.minX, y: after ? r.maxY : r.minY, width: r.width, height: 0)
        return PanePlan.Tile(id: id, rect: g, clip: g, folded: false, horizontal: horizontal)
    }

    /// Pane id's neighbour in its split, whether the pane comes after it,
    /// and which way the split runs.
    private static func neighbour(_ id: String, in layout: PaneLayout) -> (PaneLayout, Bool, Bool)? {
        guard case .split(let s) = layout.node else { return nil }
        if let i = s.children.firstIndex(where: { if case .paneID(let p) = $0.node { return p == id } else { return false } }) {
            let horizontal = s.direction != .vertical
            if i + 1 < s.children.count { return (s.children[i + 1], false, horizontal) }
            if i > 0 { return (s.children[i - 1], true, horizontal) }
            return nil
        }
        for c in s.children { if let n = neighbour(id, in: c) { return n } }
        return nil
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

    /// The window's own corner radius on macOS 26 (continuous), measured
    /// off its edge: AppKit doesn't say.
    static let windowCornerRadius: CGFloat = 16

    /// A split pane's shape at `rect` in its tab's `area`: spacious, a pane
    /// in one of the window's bottom corners rounds that corner with the
    /// window, concentric with it (the window's radius less the inset), the
    /// rest as any pane. The bottom left is the window's only with the
    /// sidebar hidden.
    static func shape(at rect: CGRect, in area: CGRect, sidebarHidden: Bool) -> UnevenRoundedRectangle {
        let bottom = rect.maxY >= area.maxY - 0.5
        let outer = spacious ? windowCornerRadius - inset : radius
        let bl = bottom && sidebarHidden && rect.minX <= area.minX + 0.5 ? outer : radius
        let br = bottom && rect.maxX >= area.maxX - 0.5 ? outer : radius
        return UnevenRoundedRectangle(topLeadingRadius: radius, bottomLeadingRadius: bl, bottomTrailingRadius: br, topTrailingRadius: radius, style: .continuous)
    }

    /// The terminal theme's background.
    static func background(dark: Bool) -> Color {
        let c = config.theme(isDark: dark).background
        return Color(red: Double(c.r) / 255, green: Double(c.g) / 255, blue: Double(c.b) / 255)
    }

    static var borderWidth: CGFloat { spacious ? 2 : 1.5 }

    /// The fade for focus moving: border and dimming.
    static let fade = Animation.easeInOut(duration: 0.16)

    static func borderColor(dark: Bool) -> Color {
        guard let c = config.activePaneBorderColor else { return Color.themeAccent }
        return Color(nsColor: dark ? c.dark : c.light)
    }
}

/// The others' dimming, as Settings asks, fading as focus moves. Over the
/// pane, never taking its clicks. (The focused one's border is
/// PaneFocusRing's.)
struct PaneFocusLook: ViewModifier {
    let isFocused: Bool
    /// Only panes that share their tab with another dim.
    let among: Bool
    /// The pane's shape (PaneLook.shape(at:in:sidebarHidden:)).
    var shape = UnevenRoundedRectangle(cornerRadii: .init(), style: .continuous)
    @Environment(\.colorScheme) private var colorScheme
    private var config: AppConfig { .shared }

    func body(content: Content) -> some View {
        let dim = among && config.dimInactivePanes && !isFocused
        content
            .overlay {
                shape
                    .fill(Color.black.opacity(colorScheme == .dark ? 0.22 : 0.07))
                    .animation(PaneMotion.shared.openingOrClosing ? nil : PaneLook.fade) { $0.opacity(dim ? 1 : 0) }
                    .allowsHitTesting(false)
            }
    }
}

/// The focused pane's border, as Settings asks: one over each pane in the
/// tab's layout (LayoutView), placed as the pane is, so it moves and resizes
/// with it in any animation. Drawn inside the pane's own view, it would be
/// laid out at the pane's final size from the start of an animation, and
/// show cut off by the pane's edges as the pane grew.
///
/// It shows or hides in a transaction of its own, just after focus moves:
/// fading, or at once when a pane opens or closes, so one pane has it all
/// through.
struct PaneFocusRing: View {
    let rect: CGRect
    let on: Bool
    /// The pane's shape (PaneLook.shape(at:in:sidebarHidden:)).
    var shape = UnevenRoundedRectangle(cornerRadii: .init(), style: .continuous)
    @Environment(\.colorScheme) private var colorScheme
    @State private var shown: Bool

    init(rect: CGRect, on: Bool, shape: UnevenRoundedRectangle) {
        self.rect = rect
        self.on = on
        self.shape = shape
        _shown = State(initialValue: on)
    }

    var body: some View {
        shape
            .strokeBorder(PaneLook.borderColor(dark: colorScheme == .dark), lineWidth: PaneLook.borderWidth)
            .opacity(shown ? 1 : 0)
            .frame(width: rect.width, height: rect.height)
            .offset(x: rect.minX, y: rect.minY)
            .allowsHitTesting(false)
            .onChange(of: on) { _, v in
                if PaneMotion.shared.openingOrClosing {
                    var t = Transaction()
                    t.disablesAnimations = true
                    withTransaction(t) { shown = v }
                } else {
                    withAnimation(PaneLook.fade) { shown = v }
                }
            }
    }
}
