import AppKit
import HammockKit
import SwiftUI

// An Android TV emulator's remote, drawn beside its screen (SimulatorPaneView)
// as the Siri Remote is beside an Apple TV's: in the current Google TV style,
// a white pill with a D-pad ring and its centre, Back and Home, Play/Pause and
// Mute, and the volume rocker on its side. Presses go to the emulator as its
// keys (internal/daemon/emulator.go), down and up, so holding one is a long
// press.

/// The remote, drawn at any size at its own proportions. press(name, phase)
/// is called with "down" and "up", the names sim_remote takes.
struct GoogleTVRemote: View {
    var press: (_ name: String, _ phase: String) -> Void

    /// Its width to its height (with the volume rocker on its side).
    static let aspect: CGFloat = 0.3

    var body: some View {
        GeometryReader { geo in
            // Laid out on a 100-wide grid, scaled to fit.
            let w = min(geo.size.width, geo.size.height * Self.aspect)
            let u = w / 100
            ZStack(alignment: .top) {
                Capsule(style: .continuous)
                    .fill(LinearGradient(colors: [Color(white: 0.97), Color(white: 0.9)], startPoint: .leading, endPoint: .trailing))
                    .overlay(Capsule(style: .continuous).strokeBorder(Color.black.opacity(0.12), lineWidth: max(0.5, u * 0.6)))
                    .frame(width: 88 * u)
                VStack(spacing: 9 * u) {
                    DPad(size: 74 * u, press: press)
                        .padding(.top, 14 * u)
                    HStack(spacing: 16 * u) {
                        VStack(spacing: 9 * u) {
                            Key(name: "menu", symbol: "arrow.uturn.backward", help: "Back (Esc)", size: 28 * u, press: press)
                            Key(name: "play_pause", symbol: "playpause", help: Keys.hint("Play/Pause (Space)", "sim_play_pause"), size: 28 * u, press: press)
                        }
                        VStack(spacing: 9 * u) {
                            Key(name: "home", symbol: "house", help: Keys.hint("Home", "sim_home"), size: 28 * u, press: press)
                            Key(name: "mute", symbol: "speaker.slash", help: Keys.hint("Mute", "sim_mute"), size: 28 * u, press: press)
                        }
                    }
                }
                .frame(width: 88 * u)
                // The volume rocker, on the right side.
                Rocker(width: 5 * u, height: 34 * u, press: press)
                    .offset(x: 46.5 * u, y: 112 * u)
            }
            .frame(width: w, height: w / Self.aspect)
            .position(x: geo.size.width / 2, y: geo.size.height / 2)
        }
        .aspectRatio(Self.aspect, contentMode: .fit)
    }
}

/// The D-pad: its ring presses up, down, left or right, its centre select.
private struct DPad: View {
    let size: CGFloat
    let press: (String, String) -> Void
    @State private var held: String?
    @State private var hover: String?

    private func region(_ p: CGPoint) -> String? {
        let dx = p.x - size / 2, dy = p.y - size / 2, r = (dx * dx + dy * dy).squareRoot()
        if r > size / 2 { return nil }
        if r < size * 0.25 { return "select" }
        return abs(dx) > abs(dy) ? (dx > 0 ? "right" : "left") : (dy > 0 ? "down" : "up")
    }

    var body: some View {
        let lit = held ?? hover
        ZStack {
            Circle().fill(Color(white: 0.84))
            if let lit, lit != "select" {
                let angle: Double = ["up": -90, "right": 0, "down": 90, "left": 180][lit] ?? 0
                Circle()
                    .trim(from: 0, to: 0.25)
                    .stroke(Color.black.opacity(held == nil ? 0.05 : 0.12), lineWidth: size * 0.24)
                    .rotationEffect(.degrees(angle - 45))
                    .padding(size * 0.12)
            }
            ForEach(0..<4) { i in
                Circle().fill(Color(white: 0.55)).frame(width: size * 0.045, height: size * 0.045)
                    .offset(y: -size * 0.39)
                    .rotationEffect(.degrees(Double(i) * 90))
            }
            Circle()
                .fill(Color(white: lit == "select" ? (held == nil ? 0.9 : 0.8) : 0.95))
                .overlay(Circle().strokeBorder(Color.black.opacity(0.1), lineWidth: max(0.5, size * 0.01)))
                .frame(width: size * 0.5, height: size * 0.5)
        }
        .frame(width: size, height: size)
        .contentShape(Circle())
        .onContinuousHover { phase in
            if case .active(let p) = phase { hover = region(p) } else { hover = nil }
        }
        .modifier(RemotePress(begin: region, press: press, held: $held))
        .tooltip(lit == "select" ? "Select (Return)" : lit.map { "\($0.capitalized) (arrow keys)" } ?? "D-pad")
        .accessibilityElement()
        .accessibilityLabel("D-pad")
    }
}

/// A round button with its symbol, dark on the remote's white.
private struct Key: View {
    let name: String
    let symbol: String
    let help: String
    let size: CGFloat
    let press: (String, String) -> Void
    @State private var held: String?

    var body: some View {
        Circle()
            .fill(Color(white: held == nil ? 0.86 : 0.76))
            .overlay(Image(systemName: symbol).font(.system(size: size * 0.38, weight: .medium)).foregroundStyle(Color(white: 0.22)))
            .frame(width: size, height: size)
            .contentShape(Circle())
            .modifier(RemotePress(begin: { _ in name }, press: press, held: $held))
            .tooltip(help)
            .accessibilityElement()
            .accessibilityLabel(help)
            .accessibilityAddTraits(.isButton)
    }
}

/// Volume up (its top half) and down, on the remote's side.
private struct Rocker: View {
    let width: CGFloat
    let height: CGFloat
    let press: (String, String) -> Void
    @State private var held: String?

    var body: some View {
        VStack(spacing: height * 0.08) {
            Capsule().fill(Color(white: held == "volume_up" ? 0.66 : 0.8))
            Capsule().fill(Color(white: held == "volume_down" ? 0.66 : 0.8))
        }
        .frame(width: width, height: height)
        .padding(.horizontal, width) // easier to hit
        .contentShape(Rectangle())
        .modifier(RemotePress(begin: { p in p.y < height / 2 ? "volume_up" : "volume_down" }, press: press, held: $held))
        .tooltip(Keys.hint("Volume", "sim_volume_up"))
    }
}

/// Taking an emulator over from Android Studio: stopping it there (after you
/// say so) and booting it in Hammock, which can then drive it.
@MainActor
enum EmulatorTakeover {
    static func confirm(_ model: AppModel, _ paneID: String) {
        // A stopped one just boots; one in Android Studio is stopped there first.
        guard model.emulatorInStudio(paneID) else {
            model.takeOverEmulator(paneID)
            return
        }
        let name = model.panes[paneID]?.title ?? "this emulator"
        let alert = NSAlert()
        alert.messageText = "Restart \(name) in Hammock?"
        alert.informativeText = "Hammock stops it in Android Studio, then boots it here. Whatever's open in it now is closed."
        alert.addButton(withTitle: "Restart")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        model.takeOverEmulator(paneID)
    }
}
