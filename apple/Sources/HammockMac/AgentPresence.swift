import HammockKit
import SwiftUI

/// An agent at work in a browser or simulator pane, drawn over it: a
/// cursor (a finger on a simulator) that glides to where it acts and
/// ripples when it clicks or taps, a soft glow around the page or screen,
/// and a badge saying an agent is working there, with Take over. The pointer fades a
/// couple of seconds after the agent's last action; the glow and badge stay
/// while the agent is still working (its turn, as its hooks tell Hammock),
/// so they don't blink out while it thinks between actions, and go when it
/// finishes, asks something or exits, or a minute after its last action
/// here (it's moved on to other work). An agent Hammock doesn't know the
/// state of gets the pointer's couple of seconds. The daemon sends the
/// actions (AgentAction, agentpresence.go) and holds the agent's pointer
/// back while the cursor glides (agentGlide there; `glide` here).
///
/// Only the badge takes clicks: the pane still works under the rest.
/// Taken over (HoldPane), the badge stays to hand it back.
struct AgentPresenceOverlay: View {
    let model: AppModel
    let paneID: String
    let isSimulator: Bool
    /// Where a simulator's screen is in this view, and its shape there
    /// (nil: a page, the whole view).
    var screen: (rect: CGRect, shape: Path)?
    /// A simulator's whole device, frame and all, as its outline in this
    /// view: its glow rings that, and spills out around it.
    var device: Path?

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var active = false
    @State private var agentPane = ""
    @State private var cursor: CGPoint? // a fraction of the page or screen
    @State private var finger: CGPoint?
    @State private var pinch: (center: CGPoint, spread: CGFloat)?
    @State private var ripples: [Ripple] = []
    @State private var sweep: CGFloat?
    @State private var sweptAt = Date.distantPast
    @State private var pulse = false
    @State private var fade: Task<Void, Never>?
    @State private var lastAction = Date.distantPast

    /// HMK_DEBUG_AGENT_CURSOR=1: a made-up click (or tap) in every pane,
    /// kept showing, for snapshots.
    static let debug = ProcessInfo.processInfo.environment["HMK_DEBUG_AGENT_CURSOR"] != nil
    /// The cursor's glide: as long as the daemon holds the pointer back.
    static let glide = 0.22
    /// How long the pointer shows after the agent's last action (and
    /// everything, for an agent whose state Hammock doesn't know).
    static let linger: Duration = .seconds(2)
    /// How long the glow and badge stay after the agent's last action here
    /// while it's still working.
    static let staysWorking: Duration = .seconds(60)

    private var pane: Pane? { model.panes[paneID] }
    private var held: Bool { pane?.agentHeld == true }
    private var on: Bool { Prefs.shared.bool(.agentPresence) }
    private var accent: Color { AgentAutomatic.color }

    /// Whether the acting agent is mid-turn: the glow stays between its actions.
    private var agentWorking: Bool {
        guard let p = model.panes[agentPane], p.hasAgent, !p.exited else { return false }
        return p.agent.shownState == .working
    }

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
        .onChange(of: agentWorking) { _, working in
            // Its turn over: gone, unless it acted just now (the pointer's
            // linger ends it then).
            if !working, active, !Self.debug, Date().timeIntervalSince(lastAction) >= 2 { hide() }
        }
        .onDisappear { fade?.cancel() }
    }

    // MARK: Drawing

    @ViewBuilder
    private func decorations(in area: CGRect) -> some View {
        let at = { (f: CGPoint) in CGPoint(x: area.minX + f.x * area.width, y: area.minY + f.y * area.height) }
        ZStack(alignment: .topLeading) {
            glow(in: area)
            if let s = sweep {
                // Looking: one soft band across the page or screen.
                LinearGradient(colors: [accent.opacity(0), accent.opacity(0.2), accent.opacity(0)], startPoint: .leading, endPoint: .trailing)
                    .frame(width: area.width * 0.45, height: area.height)
                    .offset(x: area.width * (s * 1.45 - 0.45))
                    .frame(width: area.width, height: area.height, alignment: .leading)
                    .offset(x: area.minX, y: area.minY)
                    .frame(width: area.maxX, height: area.maxY, alignment: .topLeading)
                    .clipShape(screen.map { AnyShape(PathShape(path: $0.shape)) } ?? AnyShape(Rectangle().path(in: area)))
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

    /// A page glows in from its edges; a device glows out around its
    /// frame, and its screen a little in from its edges.
    private func glow(in area: CGRect) -> some View {
        ZStack(alignment: .topLeading) {
            if let device, let screen {
                OuterGlow(color: accent, shape: PathShape(path: device))
                InnerGlow(color: accent, shape: PathShape(path: screen.shape), ring: false, strength: 0.75)
            } else {
                InnerGlow(color: accent, shape: Rectangle(), ring: true, strength: 1)
                    .frame(width: area.width, height: area.height)
                    .offset(x: area.minX, y: area.minY)
            }
        }
        .opacity(pulse ? 1 : 0.75)
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
                        // The same throughout: what it does shows on the pane.
                        Text("\(agentName) is working")
                            .fontWeight(.medium)
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
        lastAction = Date()
        fade?.cancel()
        guard !Self.debug else { return }
        fade = Task { @MainActor in
            try? await Task.sleep(for: Self.linger)
            guard !Task.isCancelled else { return }
            guard agentWorking else { return hide() }
            // Between actions: the pointer goes, the glow and badge stay.
            withAnimation(.easeOut(duration: 0.6)) {
                cursor = nil
                finger = nil
            }
            try? await Task.sleep(for: Self.staysWorking - Self.linger)
            guard !Task.isCancelled else { return }
            hide()
        }
    }

    private func hide() {
        fade?.cancel()
        withAnimation(.easeOut(duration: 0.6)) {
            active = false
            cursor = nil
            finger = nil
        }
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

/// A glow in from a shape's edges: a ring, then softer layers fading
/// inwards (the inset shadows of the design).
private struct InnerGlow<S: Shape>: View {
    let color: Color
    let shape: S
    let ring: Bool
    let strength: Double

    var body: some View {
        // Centred on the edge (half of each is clipped away), so each layer
        // is brightest there and fades inwards.
        ZStack {
            shape.stroke(color, lineWidth: 36).blur(radius: 34).opacity(0.38 * strength)
            shape.stroke(color, lineWidth: 14).blur(radius: 11).opacity(0.95 * strength)
            if ring {
                shape.stroke(color.opacity(0.85), lineWidth: 4) // 2 inside
            }
        }
        .clipShape(shape)
    }
}

/// A ring around a shape with a glow spilling out from it, none of it
/// drawn inside (over a simulator's screen).
private struct OuterGlow<S: Shape>: View {
    let color: Color
    let shape: S

    var body: some View {
        ZStack {
            shape.stroke(color, lineWidth: 20).blur(radius: 22).opacity(0.4)
            shape.stroke(color, lineWidth: 8).blur(radius: 7).opacity(0.9)
            shape.stroke(color.opacity(0.85), lineWidth: 4) // 2 outside
        }
        .mask {
            Rectangle().padding(-160)
                .overlay(shape.fill(.black).blendMode(.destinationOut))
                .compositingGroup()
        }
    }
}

/// A path already placed in the view, as a shape.
private struct PathShape: Shape {
    let path: Path
    func path(in rect: CGRect) -> Path { path }
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
