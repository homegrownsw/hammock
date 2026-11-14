import HammockKit
import SwiftUI

// An Apple TV simulator's Siri Remote, drawn beside its screen
// (SimulatorPaneView): the current aluminium remote, without Power (there's
// no TV for it to switch). Each button is sent as it's pressed and let go, as
// the remote's own HID usage (helpers/simulator/host.m), so holding one is a
// long press.

/// The remote, drawn at any size at its own proportions. press(name, phase)
/// is called with "down" and "up", the names sim_remote takes.
struct SiriRemote: View {
    var press: (_ name: String, _ phase: String) -> Void

    /// Its width to its height (with the Siri button on its side).
    static let aspect: CGFloat = 0.3

    var body: some View {
        GeometryReader { geo in
            // Laid out on a 100-wide grid, scaled to fit.
            let w = min(geo.size.width, geo.size.height * Self.aspect)
            let u = w / 100
            ZStack(alignment: .top) {
                RoundedRectangle(cornerRadius: 19 * u, style: .continuous)
                    .fill(LinearGradient(colors: [Color(white: 0.86), Color(white: 0.7), Color(white: 0.78)],
                                         startPoint: .leading, endPoint: .trailing))
                    .overlay(RoundedRectangle(cornerRadius: 19 * u, style: .continuous).strokeBorder(Color.black.opacity(0.18), lineWidth: max(0.5, u * 0.6)))
                    .frame(width: 88 * u)
                VStack(spacing: 7 * u) {
                    Clickpad(size: 76 * u, press: press)
                        .padding(.top, 15 * u)
                    HStack(alignment: .top, spacing: 10 * u) {
                        VStack(spacing: 7 * u) {
                            RoundButton(name: "menu", symbol: "chevron.left", help: "Back (Esc)", size: 30 * u, press: press)
                            RoundButton(name: "play_pause", symbol: "playpause", help: Keys.hint("Play/Pause (Space)", "sim_play_pause"), size: 30 * u, press: press)
                            RoundButton(name: "mute", symbol: "speaker.slash", help: Keys.hint("Mute", "sim_mute"), size: 30 * u, press: press)
                        }
                        VStack(spacing: 7 * u) {
                            RoundButton(name: "home", symbol: "tv", help: Keys.hint("TV", "sim_home"), size: 30 * u, press: press)
                            VolumeRocker(width: 30 * u, height: 67 * u, press: press)
                        }
                    }
                }
                .frame(width: 88 * u)
                // Siri, on the right side.
                SideButton(width: 5 * u, height: 22 * u, press: press)
                    .offset(x: 46.5 * u, y: 63 * u)
            }
            .frame(width: w, height: w / Self.aspect)
            .position(x: geo.size.width / 2, y: geo.size.height / 2)
        }
        .aspectRatio(Self.aspect, contentMode: .fit)
    }
}

/// Tracks a press from mouse down to mouse up, wherever it goes meanwhile.
private struct Press: ViewModifier {
    let begin: (CGPoint) -> String?
    let press: (String, String) -> Void
    @Binding var held: String?

    func body(content: Content) -> some View {
        content.gesture(
            DragGesture(minimumDistance: 0, coordinateSpace: .local)
                .onChanged { g in
                    guard held == nil, let name = begin(g.startLocation) else { return }
                    held = name
                    press(name, "down")
                }
                .onEnded { _ in
                    if let name = held { press(name, "up") }
                    held = nil
                }
        )
    }
}

/// The clickpad: its ring presses up, down, left or right, its centre select.
private struct Clickpad: View {
    let size: CGFloat
    let press: (String, String) -> Void
    @State private var held: String?
    @State private var hover: String?

    private func region(_ p: CGPoint) -> String? {
        let dx = p.x - size / 2, dy = p.y - size / 2, r = (dx * dx + dy * dy).squareRoot()
        if r > size / 2 { return nil }
        if r < size * 0.27 { return "select" }
        return abs(dx) > abs(dy) ? (dx > 0 ? "right" : "left") : (dy > 0 ? "down" : "up")
    }

    var body: some View {
        let lit = held ?? hover
        ZStack {
            Circle().fill(LinearGradient(colors: [Color(white: 0.2), Color(white: 0.08)], startPoint: .top, endPoint: .bottom))
            // The pressed or hovered quarter of the ring.
            if let lit, lit != "select" {
                let angle: Double = ["up": -90, "right": 0, "down": 90, "left": 180][lit] ?? 0
                Circle()
                    .trim(from: 0, to: 0.25)
                    .stroke(Color.white.opacity(held == nil ? 0.08 : 0.18), lineWidth: size * 0.23)
                    .rotationEffect(.degrees(angle - 45))
                    .padding(size * 0.115)
            }
            Circle().strokeBorder(Color(white: 0.32), lineWidth: max(0.5, size * 0.012))
            // The ring's four marks.
            ForEach(0..<4) { i in
                Circle().fill(Color(white: 0.55)).frame(width: size * 0.04, height: size * 0.04)
                    .offset(y: -size * 0.385)
                    .rotationEffect(.degrees(Double(i) * 90))
            }
            Circle()
                .fill(Color(white: lit == "select" ? (held == nil ? 0.17 : 0.24) : 0.12))
                .overlay(Circle().strokeBorder(Color(white: 0.3), lineWidth: max(0.5, size * 0.01)))
                .frame(width: size * 0.54, height: size * 0.54)
        }
        .frame(width: size, height: size)
        .contentShape(Circle())
        .onContinuousHover { phase in
            if case .active(let p) = phase { hover = region(p) } else { hover = nil }
        }
        .modifier(Press(begin: region, press: press, held: $held))
        .tooltip(tip(lit))
        .accessibilityElement()
        .accessibilityLabel("Clickpad")
    }

    private func tip(_ region: String?) -> String {
        switch region {
        case "select": "Select (Return)"
        case "up", "down", "left", "right": "\(region!.capitalized) (arrow keys)"
        default: "Clickpad"
        }
    }
}

/// A round black button with its symbol.
private struct RoundButton: View {
    let name: String
    let symbol: String
    let help: String
    let size: CGFloat
    let press: (String, String) -> Void
    @State private var held: String?

    var body: some View {
        Circle()
            .fill(Color(white: held == nil ? 0.1 : 0.22))
            .overlay(Image(systemName: symbol).font(.system(size: size * 0.36, weight: .semibold)).foregroundStyle(Color(white: 0.92)))
            .frame(width: size, height: size)
            .contentShape(Circle())
            .modifier(Press(begin: { _ in name }, press: press, held: $held))
            .tooltip(help)
            .accessibilityElement()
            .accessibilityLabel(help)
            .accessibilityAddTraits(.isButton)
    }
}

/// Volume up and down: one capsule, its top half louder, its bottom quieter.
private struct VolumeRocker: View {
    let width: CGFloat
    let height: CGFloat
    let press: (String, String) -> Void
    @State private var held: String?

    var body: some View {
        ZStack {
            Capsule().fill(Color(white: 0.1))
            VStack(spacing: 0) {
                half("volume_up", "plus")
                half("volume_down", "minus")
            }
        }
        .frame(width: width, height: height)
        .clipShape(Capsule())
        .contentShape(Capsule())
        .modifier(Press(begin: { p in p.y < height / 2 ? "volume_up" : "volume_down" }, press: press, held: $held))
        .tooltip(Keys.hint("Volume", "sim_volume_up"))
    }

    private func half(_ name: String, _ symbol: String) -> some View {
        Rectangle().fill(Color.white.opacity(held == name ? 0.12 : 0))
            .overlay(Image(systemName: symbol).font(.system(size: width * 0.36, weight: .semibold)).foregroundStyle(Color(white: 0.92)))
    }
}

/// Siri, on the remote's side.
private struct SideButton: View {
    let width: CGFloat
    let height: CGFloat
    let press: (String, String) -> Void
    @State private var held: String?

    var body: some View {
        Capsule()
            .fill(Color(white: held == nil ? 0.62 : 0.45))
            .frame(width: width, height: height)
            .padding(.horizontal, width) // easier to hit
            .contentShape(Rectangle())
            .modifier(Press(begin: { _ in "siri" }, press: press, held: $held))
            .tooltip(Keys.hint("Siri (hold to talk)", "sim_siri"))
    }
}
