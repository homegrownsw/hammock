import SwiftUI
import WidgetKit

/// Hammock's widgets: the Live Activity (the Lock Screen and the Dynamic
/// Island) and Needs You (the Home Screen and the Lock Screen). What they
/// show comes from your Macs: the Live Activity's content from the Mac's
/// pushes (or the app), Needs You's from the App Group (NeedsYouStore).
@main
struct HammockWidgets: WidgetBundle {
    var body: some Widget {
        NeedsYouWidget()
        HammockLiveActivity()
    }
}

/// The app theme's accent, as the Mac sent it, else Hammock's green.
func accent(_ c: LiveContent, dark: Bool) -> Color {
    Color(rgb: accentRGB(c, dark: dark))
}

private func accentRGB(_ c: LiveContent, dark: Bool) -> (Double, Double, Double) {
    if let rgb = c.accentRGB(dark: dark) { return rgb }
    let v = dark ? 0x3FB88A : 0x1F7A57
    return (Double(v >> 16 & 0xFF) / 255, Double(v >> 8 & 0xFF) / 255, Double(v & 0xFF) / 255)
}

/// The Live Activity's backdrop: the accent steeped into near-black (or
/// near-white), as the theme tints the Mac's sidebar.
func backdrop(_ c: LiveContent, dark: Bool) -> Color {
    let (r, g, b) = accentRGB(c, dark: dark)
    let base: Double = dark ? 0.07 : 0.97
    let share: Double = dark ? 0.2 : 0.12
    func mix(_ v: Double) -> Double { base + (v - base) * share }
    return Color(rgb: (mix(r), mix(g), mix(b)))
}

/// What's drawn on the accent: white, or near-black on a pale one (Graphite's).
func ink(_ c: LiveContent, dark: Bool) -> Color {
    let (r, g, b) = accentRGB(c, dark: dark)
    let luma: Double = 0.2126 * r + 0.7152 * g + 0.0722 * b
    return luma > 0.72 ? Color(white: 0.1) : .white
}

extension Color {
    init(rgb: (Double, Double, Double)) { self.init(red: rgb.0, green: rgb.1, blue: rgb.2) }
}

/// The agent's mark, white on a squircle of the theme's accent.
struct AgentTile: View {
    let content: LiveContent
    let tint: Color
    let ink: Color
    var size: CGFloat = 40

    var body: some View {
        RoundedRectangle(cornerRadius: size * 0.28, style: .continuous)
            .fill(LinearGradient(colors: [tint, tint.opacity(0.72)], startPoint: .topLeading, endPoint: .bottomTrailing))
            .overlay { AgentMark(harness: content.harness ?? content.agent.lowercased(), size: size * 0.56, tint: ink) }
            .frame(width: size, height: size)
    }
}

/// What state an agent is in, as a small mark: dots going round while it
/// works, a raised hand while it needs you, a tick or a cross once it's done.
struct StateMark: View {
    let content: LiveContent
    let tint: Color

    var body: some View {
        switch content.kind {
        case .working: Image(systemName: "circle.dotted").symbolEffect(.rotate, options: .repeating).foregroundStyle(tint)
        case .needsYou: Image(systemName: "hand.raised.fill").foregroundStyle(tint)
        case .finished: Image(systemName: "checkmark.circle.fill").foregroundStyle(tint)
        case .errored: Image(systemName: "xmark.octagon.fill").foregroundStyle(.red)
        }
    }
}

/// The state in words on a capsule: filled with the accent when it needs
/// you, else a pale one.
struct StatusPill: View {
    let content: LiveContent
    let tint: Color
    let ink: Color

    var body: some View {
        let loud = content.kind == .needsYou
        HStack(spacing: 4) {
            StateMark(content: content, tint: loud ? ink : tint).font(.caption.weight(.bold))
            Text(content.status).font(.caption.weight(.semibold)).lineLimit(1)
        }
        .foregroundStyle(loud ? ink : tint)
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(Capsule().fill(loud ? tint : tint.opacity(0.18)))
        .fixedSize()
    }
}
