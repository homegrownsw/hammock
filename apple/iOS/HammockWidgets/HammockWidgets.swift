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
    if let (r, g, b) = c.accentRGB(dark: dark) { return Color(red: r, green: g, blue: b) }
    return dark ? Color(red: 0x3F / 255.0, green: 0xB8 / 255.0, blue: 0x8A / 255.0) : Color(red: 0x1F / 255.0, green: 0x7A / 255.0, blue: 0x57 / 255.0)
}

/// An agent's mark: a symbol for the agents Hammock knows best, else its
/// name's first letter.
struct AgentGlyph: View {
    let harness: String?
    let agent: String
    var size: CGFloat = 16

    var body: some View {
        switch harness {
        case "claude": Image(systemName: "sparkle").font(.system(size: size * 0.9, weight: .semibold))
        case "codex": Image(systemName: "cloud.fill").font(.system(size: size * 0.8, weight: .semibold))
        default:
            Text(String((agent.first ?? "A").uppercased()))
                .font(.system(size: size * 0.75, weight: .bold, design: .rounded))
                .frame(width: size, height: size)
                .background(Circle().fill(.quaternary))
        }
    }
}

/// What state an agent is in, as a small mark: a spinner-like dot while it
/// works, an exclamation while it needs you, a tick or a cross once it's done.
struct StateMark: View {
    let content: LiveContent
    let tint: Color

    var body: some View {
        switch content.kind {
        case .working: Image(systemName: "circle.dotted").symbolEffect(.rotate, options: .repeating).foregroundStyle(tint)
        case .needsYou: Image(systemName: "exclamationmark.circle.fill").foregroundStyle(.orange)
        case .finished: Image(systemName: "checkmark.circle.fill").foregroundStyle(tint)
        case .errored: Image(systemName: "xmark.octagon.fill").foregroundStyle(.red)
        }
    }
}
