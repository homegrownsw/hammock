import SwiftUI

/// Over a pane whose agent is asleep (Pane.asleep: its process ended to
/// free memory, its screen kept): the pane faded towards its own
/// background, and a card in the middle with a big Wake Agent button, in
/// the theme's colour. Focusing the pane doesn't wake it; typing in it,
/// sending its agent a message, or this button does.
///
/// Only the card takes clicks: the faded pane under it can still be
/// scrolled, selected and typed in (which wakes it too). Too short for the
/// card, it's the button alone; too narrow for its words, the button's icon.
/// It comes in with a small rise of the card unless `animated` is false
/// (pane animations off, Reduce Motion): the caller fades the whole of it.
public struct SleepingPaneOverlay: View {
    /// The pane's background (the terminal theme's), which the pane fades to
    /// and the card is made of.
    let backdrop: Color
    /// The button's fill: the theme's colour, deep enough for white text.
    let fill: Color
    /// The moon's colour: the theme's colour.
    let tint: Color
    let animated: Bool
    let wake: () -> Void
    @State private var shown = false

    public init(backdrop: Color, fill: Color, tint: Color, animated: Bool = true, wake: @escaping () -> Void) {
        self.backdrop = backdrop
        self.fill = fill
        self.tint = tint
        self.animated = animated
        self.wake = wake
    }

    public var body: some View {
        ZStack {
            backdrop.opacity(0.68)
                .allowsHitTesting(false)
            ViewThatFits(in: .vertical) {
                card
                ViewThatFits(in: .horizontal) {
                    WakeButton(fill: fill, iconOnly: false, action: wake)
                    WakeButton(fill: fill, iconOnly: true, action: wake)
                }
            }
            .padding(12)
            .scaleEffect(shown ? 1 : 0.94)
            .offset(y: shown ? 0 : 6)
        }
        .onAppear {
            guard animated else { shown = true; return }
            withAnimation(.spring(response: 0.42, dampingFraction: 0.78)) { shown = true }
        }
        .accessibilityElement(children: .contain)
    }

    private var card: some View {
        VStack(spacing: 14) {
            ZStack {
                Circle().fill(tint.opacity(0.16))
                Circle().strokeBorder(tint.opacity(0.28), lineWidth: 1)
                Image(systemName: "moon.zzz.fill")
                    .font(.system(size: 23, weight: .medium))
                    .foregroundStyle(tint)
                    .symbolRenderingMode(.hierarchical)
            }
            .frame(width: 54, height: 54)
            .accessibilityHidden(true)
            VStack(spacing: 4) {
                Text("Asleep")
                    .font(.headline)
                Text("Stopped to save memory.\nTyping here wakes it too.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: 260)
            WakeButton(fill: fill, iconOnly: false, action: wake)
                .padding(.top, 2)
        }
        .padding(.horizontal, 26)
        .padding(.top, 22)
        .padding(.bottom, 20)
        .background {
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .fill(backdrop)
                .shadow(color: .black.opacity(0.18), radius: 18, y: 6)
        }
        .overlay {
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .strokeBorder(Color.primary.opacity(0.09), lineWidth: 1)
        }
    }
}

/// The overlay's button: a capsule in the theme's colour.
private struct WakeButton: View {
    let fill: Color
    let iconOnly: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Group {
                if iconOnly {
                    Image(systemName: "sun.max.fill")
                } else {
                    Label("Wake Agent", systemImage: "sun.max.fill")
                        .lineLimit(1)
                        .fixedSize()
                }
            }
            .font(.body.weight(.semibold))
            .padding(.horizontal, iconOnly ? 11 : 20)
            .padding(.vertical, 9)
        }
        .buttonStyle(WakeButtonStyle(fill: fill))
        .accessibilityLabel("Wake Agent")
    }
}

private struct WakeButtonStyle: ButtonStyle {
    let fill: Color

    func makeBody(configuration: Configuration) -> some View {
        WakeButtonLook(label: configuration.label, pressed: configuration.isPressed, fill: fill)
    }
}

/// The capsule, a little lighter under the pointer, darker and smaller pressed.
private struct WakeButtonLook<Content: View>: View {
    let label: Content
    let pressed: Bool
    let fill: Color
    @State private var hover = false

    var body: some View {
        label
            .foregroundStyle(.white)
            .background {
                Capsule()
                    .fill(fill.gradient)
                    .overlay(Capsule().fill(Color.white.opacity(hover && !pressed ? 0.1 : 0)))
                    .overlay(Capsule().fill(Color.black.opacity(pressed ? 0.12 : 0)))
                    .shadow(color: fill.opacity(0.35), radius: 8, y: 3)
            }
            .contentShape(Capsule())
            .scaleEffect(pressed ? 0.96 : 1)
            .animation(.easeOut(duration: 0.12), value: pressed)
            .animation(.easeOut(duration: 0.12), value: hover)
            #if os(macOS)
            .onHover { hover = $0 }
            #endif
    }
}
