import HammockKit
import SwiftUI

/// An agent at work in a browser or simulator pane, drawn over it: a
/// cursor (a finger on a simulator) that glides to where it acts and
/// ripples when it clicks or taps, a soft glow around the page or screen,
/// and a badge saying what it's doing, with Take over. It all fades a
/// couple of seconds after the agent's last action. The daemon sends the
/// actions (AgentAction, agentpresence.go) and holds the agent's pointer
/// back while the cursor glides (agentGlide there; `glide` here).
///
/// Only the badge takes clicks: the pane still works under the rest.
/// Taken over (HoldPane), the badge stays to hand it back.
struct AgentPresenceOverlay: View {
    let model: AppModel
    let paneID: String
    let isSimulator: Bool
    /// Where the page or screen is in this view, and its corners' radius
    /// (nil: the whole view, square).
    var screen: (rect: CGRect, radius: CGFloat)?

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var active = false
    @State private var agentPane = ""
    @State private var status = Status.using
    @State private var statusAt = Date.distantPast
    @State private var cursor: CGPoint? // a fraction of the page or screen
    @State private var finger: CGPoint?
    @State private var pinch: (center: CGPoint, spread: CGFloat)?
    @State private var ripples: [Ripple] = []
    @State private var sweep: CGFloat?
    @State private var sweptAt = Date.distantPast
    @State private var pulse = false
    @State private var fade: Task<Void, Never>?

    /// HMK_DEBUG_AGENT_CURSOR=1: a made-up click (or tap) in every pane,
    /// kept showing, for snapshots.
    static let debug = ProcessInfo.processInfo.environment["HMK_DEBUG_AGENT_CURSOR"] != nil
    /// The cursor's glide: as long as the daemon holds the pointer back.
    static let glide = 0.22
    /// How long it shows after the agent's last action.
    static let linger: Duration = .seconds(2)

    private var pane: Pane? { model.panes[paneID] }
    private var held: Bool { pane?.agentHeld == true }
    private var on: Bool { Prefs.shared.bool(.agentPresence) }
    private var accent: Color { AgentAutomatic.color }

    var body: some View {
        GeometryReader { geo in
            let area = screen?.rect ?? CGRect(origin: .zero, size: geo.size)
            ZStack(alignment: .topLeading) {
                if active, on {
                    decorations(in: area)
                        .frame(width: geo.size.width, height: geo.size.height, alignment: .topLeading)
                        .allowsHitTesting(false)
                        .transition(.opacity)
                }
                if on, active || held {
                    badge
                        .fixedSize()
                        .position(x: area.midX, y: max(area.minY, 0) + 26)
                        .transition(.opacity.combined(with: .move(edge: .top)))
                }
            }
        }
        .onChange(of: model.agentActions[paneID]) { _, e in
            if let e { apply(e.action) }
        }
        .onAppear {
            guard Self.debug else { return }
            var a = AgentAction()
            a.paneID = paneID
            a.kind = isSimulator ? "tap" : "click"
            a.x = isSimulator ? 0.5 : 0.62
            a.y = isSimulator ? 0.42 : 0.55
            apply(a)
        }
        .onDisappear { fade?.cancel() }
    }

    // MARK: Drawing

    @ViewBuilder
    private func decorations(in area: CGRect) -> some View {
        let at = { (f: CGPoint) in CGPoint(x: area.minX + f.x * area.width, y: area.minY + f.y * area.height) }
        ZStack(alignment: .topLeading) {
            glow
                .frame(width: area.width, height: area.height)
                .offset(x: area.minX, y: area.minY)
            if let s = sweep {
                // Looking: one soft band across the page or screen.
                LinearGradient(colors: [accent.opacity(0), accent.opacity(0.2), accent.opacity(0)], startPoint: .leading, endPoint: .trailing)
                    .frame(width: area.width * 0.45, height: area.height)
                    .offset(x: area.width * (s * 1.45 - 0.45))
                    .frame(width: area.width, height: area.height, alignment: .leading)
                    .clipShape(RoundedRectangle(cornerRadius: screen?.radius ?? 0, style: .continuous))
                    .offset(x: area.minX, y: area.minY)
            }
            ForEach(ripples) { r in
                RippleView(color: accent, size: isSimulator ? 60 : 52, still: reduceMotion)
                    .position(at(r.at))
            }
            if let p = pinch {
                let c = at(p.center), d = p.spread / 2 / 1.414
                FingerDot(color: accent, size: 30).position(x: c.x - d, y: c.y + d)
                FingerDot(color: accent, size: 30).position(x: c.x + d, y: c.y - d)
            }
            if let f = finger {
                FingerDot(color: accent, size: 36).position(at(f))
            }
            if let c = cursor, !isSimulator {
                let p = at(c)
                AgentCursor(color: accent)
                    .frame(width: 22, height: 26)
                    // The arrow's tip (3, 2) on the point.
                    .position(x: p.x + 8, y: p.y + 11)
            }
        }
    }

    private var glow: some View {
        let shape = RoundedRectangle(cornerRadius: screen?.radius ?? 0, style: .continuous)
        return ZStack {
            shape.strokeBorder(accent, lineWidth: 14).blur(radius: 14).opacity(0.45)
            shape.strokeBorder(accent.opacity(0.85), lineWidth: 2)
        }
        // A page's glow stays inside it; a device's spills onto its frame.
        .clipShape(screen == nil ? AnyShape(shape) : AnyShape(Rectangle().inset(by: -40)))
        .opacity(pulse ? 1 : 0.72)
        .onAppear {
            guard !reduceMotion else { pulse = true; return }
            withAnimation(.easeInOut(duration: 1.2).repeatForever(autoreverses: true)) { pulse = true }
        }
        .onDisappear { pulse = false }
    }

    private var badge: some View {
        HStack(spacing: 8) {
            if held {
                Image(systemName: "hand.raised.fill")
                    .foregroundStyle(.secondary)
                Text("You've taken over")
                    .fontWeight(.medium)
                Button("Hand Back") { model.holdPane(paneID, false) }
                    .buttonStyle(BadgeButtonStyle())
                    .tooltip("Let agents act here again")
            } else {
                Button { if !agentPane.isEmpty { model.reveal(paneID: agentPane) } } label: {
                    HStack(spacing: 7) {
                        if let harness = harness {
                            AgentMark(harness: harness, size: 14)
                        } else {
                            Image(systemName: "sparkle").foregroundStyle(accent)
                        }
                        Text(status.text(agent: agentName, simulator: isSimulator))
                            .fontWeight(.medium)
                            .contentTransition(.opacity)
                    }
                }
                .buttonStyle(.plain)
                .tooltip("Show the agent")
                Button("Take Over") { model.holdPane(paneID, true) }
                    .buttonStyle(BadgeButtonStyle())
                    .tooltip("Stop agents acting here until you hand it back")
            }
        }
        .font(.callout)
        .foregroundStyle(.white)
        // Dark whatever the page or the window's appearance: it has to
        // read over a white page and a dark one alike.
        .environment(\.colorScheme, .dark)
        .padding(.leading, 12)
        .padding(.trailing, 5)
        .frame(height: 32)
        .background(Capsule().fill(Color(white: 0.11).opacity(0.92)))
        .overlay(Capsule().strokeBorder(held ? Color.white.opacity(0.15) : accent.opacity(0.6), lineWidth: 1))
        .shadow(color: .black.opacity(0.2), radius: 10, y: 4)
        .animation(.easeOut(duration: 0.2), value: status)
    }

    private var harness: String? {
        guard let p = model.panes[agentPane], p.hasAgent, !p.agent.harness.isEmpty else { return nil }
        return p.agent.harness
    }

    private var agentName: String { harness.map { AgentMark.name(of: $0) } ?? "An agent" }

    // MARK: Actions

    private func apply(_ a: AgentAction) {
        guard on else { return }
        if !a.agentPaneID.isEmpty { agentPane = a.agentPaneID }
        let point: CGPoint? = a.x >= 0 && a.y >= 0 ? CGPoint(x: CGFloat(a.x), y: CGFloat(a.y)) : nil
        let end: CGPoint? = a.x2 >= 0 && a.y2 >= 0 ? CGPoint(x: CGFloat(a.x2), y: CGFloat(a.y2)) : nil
        // Eased, not a spring: it arrives when the daemon lets the click go.
        let glide: Animation? = reduceMotion ? nil : .easeInOut(duration: Self.glide)
        set(Status(a))
        withAnimation(.easeOut(duration: 0.2)) { active = true }
        switch a.kind {
        case "move", "scroll":
            if let point { withAnimation(cursor == nil ? nil : glide) { cursor = point } }
        case "click":
            if let point {
                // Its move may have come in the same update (the app was
                // busy): glide there first, then ripple.
                let far = cursor.map { hypot($0.x - point.x, $0.y - point.y) > 0.004 } ?? false
                withAnimation(cursor == nil ? nil : glide) { cursor = point }
                ripple(at: point, after: far && glide != nil ? Self.glide : 0)
            }
        case "tap":
            if let point {
                finger = point
                ripple(at: point)
                lift(after: 0.35)
            }
        case "swipe":
            if let point, let end {
                finger = point
                let ms = Double(max(a.ms, 1)) / 1000
                withAnimation(reduceMotion ? nil : .linear(duration: ms)) { finger = end }
                lift(after: ms + 0.25)
            }
        case "touch":
            if let point {
                switch a.label {
                case "down": finger = point
                case "up": lift(after: 0.2)
                default: finger = point
                }
            }
        case "pinch":
            if let point {
                let from: CGFloat = 44, to = min(max(from * CGFloat(a.scale > 0 ? a.scale : 1), 14), 160)
                pinch = (point, from)
                withAnimation(reduceMotion ? nil : .easeInOut(duration: Double(max(a.ms, 200)) / 1000)) { pinch = (point, to) }
                Task { @MainActor in
                    try? await Task.sleep(for: .milliseconds(Int(max(a.ms, 200)) + 300))
                    withAnimation(.easeOut(duration: 0.2)) { pinch = nil }
                }
            }
        case "look":
            if !reduceMotion, Date().timeIntervalSince(sweptAt) > 2.5 {
                sweptAt = Date()
                sweep = 0
                withAnimation(.easeInOut(duration: 0.9)) { sweep = 1 }
                Task { @MainActor in
                    try? await Task.sleep(for: .seconds(0.95))
                    sweep = nil
                }
            }
        default:
            break
        }
        fade?.cancel()
        guard !Self.debug else { return }
        fade = Task { @MainActor in
            try? await Task.sleep(for: Self.linger)
            guard !Task.isCancelled else { return }
            withAnimation(.easeOut(duration: 0.6)) {
                active = false
                cursor = nil
                finger = nil
            }
        }
    }

    /// What the badge says: a look or a pointer's move (part of finding
    /// what to click) doesn't replace what it said a moment ago.
    private func set(_ s: Status) {
        let now = Date()
        if s == .looking || s == .using, status != s, now.timeIntervalSince(statusAt) < 1.5 { return }
        status = s
        statusAt = now
    }

    private func ripple(at p: CGPoint, after delay: Double = 0) {
        let r = Ripple(at: p)
        Task { @MainActor in
            if delay > 0 { try? await Task.sleep(for: .seconds(delay)) }
            ripples.append(r)
            try? await Task.sleep(for: .seconds(0.7))
            ripples.removeAll { $0.id == r.id }
        }
    }

    private func lift(after seconds: Double) {
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(seconds))
            withAnimation(.easeOut(duration: 0.25)) { finger = nil }
        }
    }

    // MARK: Pieces

    enum Status: Equatable {
        case using, clicking, scrolling, tapping, swiping, pinching, typing, looking
        case pressed(String)

        init(_ a: AgentAction) {
            switch a.kind {
            case "click": self = .clicking
            case "scroll": self = .scrolling
            case "tap": self = .tapping
            case "swipe", "touch": self = .swiping
            case "pinch": self = .pinching
            case "type": self = .typing
            case "look": self = .looking
            case "key": self = .pressed(Self.keyName(a.label))
            default: self = .using
            }
        }

        func text(agent: String, simulator: Bool) -> String {
            switch self {
            case .using: "\(agent) is using this \(simulator ? "simulator" : "browser")"
            case .clicking: "\(agent) is clicking"
            case .scrolling: "\(agent) is scrolling"
            case .tapping: "\(agent) is tapping"
            case .swiping: "\(agent) is swiping"
            case .pinching: "\(agent) is pinching"
            case .typing: "\(agent) is typing"
            case .looking: "\(agent) is looking"
            case .pressed(let key): "\(agent) pressed \(key)"
            }
        }

        /// A key or button as the Mac names it.
        static func keyName(_ key: String) -> String {
            switch key.lowercased() {
            case "": "a key"
            case "enter": "Return"
            case "escape": "Escape"
            case "backspace": "Delete"
            case "delete": "Forward Delete"
            case "arrowup": "↑"
            case "arrowdown": "↓"
            case "arrowleft": "←"
            case "arrowright": "→"
            case "home": "Home"
            case "lock": "Lock"
            case "side": "the side button"
            case "siri": "Siri"
            case "keyboard": "the keyboard button"
            default: key
            }
        }
    }

    struct Ripple: Identifiable {
        let id = UUID()
        let at: CGPoint
    }
}

/// The agent's pointer: an arrow in its colour, outlined in white.
struct AgentCursor: View {
    let color: Color

    var body: some View {
        ArrowShape()
            .fill(color)
            .overlay(ArrowShape().stroke(.white, style: StrokeStyle(lineWidth: 1.8, lineJoin: .round)))
            .shadow(color: .black.opacity(0.35), radius: 2, y: 1.5)
    }

    private struct ArrowShape: Shape {
        func path(in r: CGRect) -> Path {
            // Drawn in a 22 × 26 box, tip at (3, 2).
            let sx = r.width / 22, sy = r.height / 26
            var p = Path()
            for (i, pt) in [(3.0, 2.0), (3, 21), (8, 16.5), (11.5, 24), (15, 22.4), (11.6, 15), (18.5, 15)].enumerated() {
                let q = CGPoint(x: r.minX + pt.0 * sx, y: r.minY + pt.1 * sy)
                if i == 0 { p.move(to: q) } else { p.addLine(to: q) }
            }
            p.closeSubpath()
            return p
        }
    }
}

/// A fingertip on a simulator's screen.
private struct FingerDot: View {
    let color: Color
    let size: CGFloat

    var body: some View {
        Circle()
            .fill(color.opacity(0.55))
            .overlay(Circle().strokeBorder(.white, lineWidth: 2))
            .frame(width: size, height: size)
            .shadow(color: .black.opacity(0.25), radius: 5, y: 2)
            .transition(.opacity.combined(with: .scale(scale: 0.7)))
    }
}

/// A click's or tap's ring, spreading out and fading (still with Reduce Motion).
private struct RippleView: View {
    let color: Color
    let size: CGFloat
    let still: Bool
    @State private var spread = false

    var body: some View {
        Circle()
            .strokeBorder(color, lineWidth: 2)
            .frame(width: size, height: size)
            .scaleEffect(still ? 1 : (spread ? 1.8 : 0.4))
            .opacity(still ? 0.8 : (spread ? 0 : 0.9))
            .onAppear {
                guard !still else { return }
                withAnimation(.timingCurve(0.2, 0.7, 0.3, 1, duration: 0.65)) { spread = true }
            }
    }
}

private struct BadgeButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout)
            .padding(.horizontal, 10)
            .frame(height: 22)
            .background(Capsule().fill(Color.primary.opacity(configuration.isPressed ? 0.2 : 0.1)))
            .contentShape(Capsule())
    }
}
