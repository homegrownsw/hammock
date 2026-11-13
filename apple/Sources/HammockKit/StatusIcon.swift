import SwiftUI
#if canImport(AppKit)
import AppKit
#else
import UIKit
#endif

/// An agent's state as an icon ("Orbit"). Rings while all is well:
/// grey at rest (a dot going slowly round it while shells still run), an
/// indigo arc going round while it works, green with a check when it has
/// finished. Filled when it needs you: amber with ! for input, red with ×
/// for an error. Those three arrive with one small pop (the check is drawn
/// in), then sit still. Asleep (its process ended to free memory, resumed
/// when it's woken), a grey moon where it would rest or show
/// its check. No agent: the program running, else the terminal glyph.
///
/// Drawn on a 20-point grid and scaled to `size`.
public struct StatusIcon: View {
    let state: AgentState?
    var resting: Bool
    /// At rest with shells still running (background tasks).
    var shells: Bool
    var size: CGFloat
    /// At rest without shells, draw nothing (and take no width), for a
    /// status shown beside something else.
    var quietAtRest: Bool
    /// With no agent state: the program to show, else the terminal glyph.
    var program: [String]
    /// The agent is asleep (Pane.asleep): a moon instead of its rest or check.
    var asleep: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    public init(state: AgentState?, resting: Bool = false, shells: Bool = false, size: CGFloat = 15, quietAtRest: Bool = false, program: [String] = [],
                asleep: Bool = false) {
        self.state = state
        self.resting = resting
        self.shells = shells
        self.size = size
        self.quietAtRest = quietAtRest
        self.program = program
        self.asleep = asleep
    }

    /// Asleep, and nothing more pressing to show (an error stays).
    private var sleeping: Bool {
        asleep && !(state.map { [AgentState.needsInput, .working, .starting, .errored].contains($0) } ?? false)
    }

    private var blank: Bool {
        !sleeping && quietAtRest && !shells && !(state.map { [AgentState.needsInput, .working, .starting, .idle, .errored].contains($0) } ?? false)
    }

    public var body: some View {
        // One square for every state, so a row never changes height.
        icon
            .frame(width: blank ? 0 : size, height: size)
            .keyframeAnimator(initialValue: StatusArrival(), trigger: state) { content, value in
                content.environment(\.statusArrival, reduceMotion ? StatusArrival() : value)
            } keyframes: { _ in
                KeyframeTrack(\.scale) {
                    MoveKeyframe(0.55)
                    CubicKeyframe(1.07, duration: 0.25)
                    CubicKeyframe(1, duration: 0.17)
                }
                KeyframeTrack(\.draw) {
                    MoveKeyframe(0)
                    LinearKeyframe(0, duration: 0.14)
                    CubicKeyframe(1, duration: 0.4)
                }
            }
    }

    @ViewBuilder
    private var icon: some View {
        if sleeping {
            SleepMark(size: size)
        } else {
            stateIcon
        }
    }

    @ViewBuilder
    private var stateIcon: some View {
        switch state {
        case .needsInput:
            StatusBadge(fill: StatusPalette.amber, glyph: .bang, size: size)
                .accessibilityLabel("Needs input")
        case .working:
            WorkingDot(size: size)
        case .starting:
            WorkingDot(size: size, starting: true)
        case .idle:
            FinishedMark(size: size)
                .accessibilityLabel("Finished")
        case .errored:
            StatusBadge(fill: StatusPalette.red, glyph: .cross, size: size)
                .accessibilityLabel("Errored")
        default:
            if blank {
                EmptyView()
            } else if resting {
                RestingRing(shells: shells, size: size)
                    .accessibilityLabel(shells ? "Agent idle, shells running" : "Agent idle")
            } else {
                ProgramIcon(program: program, size: size)
            }
        }
    }
}

/// The colours of the status icons, light and dark.
public enum StatusPalette {
    public static let indigo = dynamic(light: 0x5856D6, dark: 0x7D7AFF)
    public static let track = dynamic(light: 0x5856D6, dark: 0x7D7AFF, lightAlpha: 0.35, darkAlpha: 0.45)
    public static let green = dynamic(light: 0x248A3D, dark: 0x30D158)
    public static let amber = dynamic(light: 0xF59300, dark: 0xFF9F0A)
    public static let red = dynamic(light: 0xE5372C, dark: 0xFF453A)

    static func dynamic(light: UInt32, dark: UInt32, lightAlpha: CGFloat = 1, darkAlpha: CGFloat = 1) -> Color {
        func parts(_ c: UInt32) -> (CGFloat, CGFloat, CGFloat) {
            (CGFloat((c >> 16) & 0xFF) / 255, CGFloat((c >> 8) & 0xFF) / 255, CGFloat(c & 0xFF) / 255)
        }
        #if canImport(AppKit)
        return Color(nsColor: NSColor(name: nil) { appearance in
            let isDark = appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
            let (r, g, b) = parts(isDark ? dark : light)
            return NSColor(srgbRed: r, green: g, blue: b, alpha: isDark ? darkAlpha : lightAlpha)
        })
        #else
        return Color(uiColor: UIColor { traits in
            let isDark = traits.userInterfaceStyle == .dark
            let (r, g, b) = parts(isDark ? dark : light)
            return UIColor(red: r, green: g, blue: b, alpha: isDark ? darkAlpha : lightAlpha)
        })
        #endif
    }
}

/// How far an attention state's arrival has got: the pop's scale and how
/// much of the check is drawn. At rest both are 1.
struct StatusArrival {
    var scale: CGFloat = 1
    var draw: CGFloat = 1
}

private struct StatusArrivalKey: EnvironmentKey {
    static let defaultValue = StatusArrival()
}

extension EnvironmentValues {
    var statusArrival: StatusArrival {
        get { self[StatusArrivalKey.self] }
        set { self[StatusArrivalKey.self] = newValue }
    }
}

enum StatusGlyph {
    case check, cross, bang
}

/// A glyph on the 20-point grid, stroked by its user.
struct StatusGlyphShape: Shape {
    let glyph: StatusGlyph

    func path(in rect: CGRect) -> Path {
        let k = rect.width / 20
        func pt(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: rect.minX + x * k, y: rect.minY + y * k) }
        var p = Path()
        switch glyph {
        case .check:
            p.move(to: pt(6.3, 10.3))
            p.addLine(to: pt(8.9, 12.9))
            p.addLine(to: pt(13.9, 7.4))
        case .cross:
            p.move(to: pt(7.4, 7.4))
            p.addLine(to: pt(12.6, 12.6))
            p.move(to: pt(12.6, 7.4))
            p.addLine(to: pt(7.4, 12.6))
        case .bang:
            p.move(to: pt(10, 5.8))
            p.addLine(to: pt(10, 10.6))
            // A zero-length line: its round caps make the dot.
            p.move(to: pt(10, 14))
            p.addLine(to: pt(10, 14))
        }
        return p
    }
}

/// Needs input and errors: a filled disc with a white glyph, popping in.
struct StatusBadge: View {
    let fill: Color
    let glyph: StatusGlyph
    let size: CGFloat
    @Environment(\.statusArrival) private var arrival

    var body: some View {
        let k = size / 20
        ZStack {
            Circle().fill(fill).frame(width: 17.5 * k, height: 17.5 * k)
            StatusGlyphShape(glyph: glyph)
                .stroke(.white, style: StrokeStyle(lineWidth: max(1, (glyph == .bang ? 2.1 : 1.9) * k), lineCap: .round, lineJoin: .round))
        }
        .frame(width: size, height: size)
        .scaleEffect(arrival.scale)
        .opacity(min(1, Double(arrival.scale - 0.55) / 0.3))
        .accessibilityElement(children: .ignore)
    }
}

/// Finished: a green ring that pops, and a check drawn in.
struct FinishedMark: View {
    let size: CGFloat
    @Environment(\.statusArrival) private var arrival

    var body: some View {
        let k = size / 20
        let line = max(1, 1.75 * k)
        ZStack {
            Circle()
                .stroke(StatusPalette.green, lineWidth: line)
                .frame(width: 14.5 * k, height: 14.5 * k)
                .scaleEffect(arrival.scale)
            StatusGlyphShape(glyph: .check)
                .trim(from: 0, to: arrival.draw)
                .stroke(StatusPalette.green, style: StrokeStyle(lineWidth: max(1, 1.9 * k), lineCap: .round, lineJoin: .round))
        }
        .frame(width: size, height: size)
        .accessibilityElement(children: .ignore)
    }
}

/// An agent at rest: a grey ring; while its shells run, a dot goes slowly
/// round it (once every 10 s). The ring stays as strong as without shells.
/// An agent asleep: a grey moon, as quiet as the ring of one at rest.
public struct SleepMark: View {
    var size: CGFloat

    public init(size: CGFloat = 15) { self.size = size }

    public var body: some View {
        Image(systemName: "moon.zzz.fill")
            .font(.system(size: size * 0.78, weight: .medium))
            .foregroundStyle(Color.secondary) // not relative to a dimmed parent's style
            .frame(width: size, height: size)
            .accessibilityLabel("Asleep")
    }
}

struct RestingRing: View {
    let shells: Bool
    let size: CGFloat
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let k = size / 20
        ZStack {
            Circle()
                .stroke(.secondary, lineWidth: max(1, 1.5 * k))
                .frame(width: 14.5 * k, height: 14.5 * k)
            if shells {
                TimelineView(.animation(minimumInterval: 1.0 / 20, paused: reduceMotion)) { context in
                    let t = reduceMotion ? 0 : context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 10) / 10
                    Circle()
                        .fill(.secondary)
                        .frame(width: max(2.5, 3.4 * k), height: max(2.5, 3.4 * k))
                        .offset(y: -7.25 * k)
                        .rotationEffect(.degrees(t * 360))
                }
            }
        }
        .frame(width: size, height: size)
        .accessibilityElement(children: .ignore)
    }
}
