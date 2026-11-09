import SwiftUI

// The agent view's floating controls in Liquid Glass: the message box, the
// status pill, prompt cards, jump to latest. The conversation scrolls
// beneath them; content itself stays plain, as Apple's apps do.

extension View {
    /// Glass in shape, tinted and answering touches if asked (a material on a
    /// Mac before macOS 26: the Mac app still runs on 15, the phone needs 26).
    @ViewBuilder
    func agentGlass<S: Shape>(in shape: S, tint: Color? = nil, interactive: Bool = false) -> some View {
        if #available(macOS 26.0, iOS 26.0, *) {
            glassEffect(Self.glass(tint: tint, interactive: interactive), in: shape)
        } else {
            background(tint.map { AnyShapeStyle($0) } ?? AnyShapeStyle(.regularMaterial), in: shape)
                .overlay { shape.stroke(Color.primary.opacity(0.08), lineWidth: 0.5) }
        }
    }

    @available(macOS 26.0, iOS 26.0, *)
    private static func glass(tint: Color?, interactive: Bool) -> Glass {
        var g = Glass.regular
        if let tint { g = g.tint(tint) }
        if interactive { g = g.interactive() }
        return g
    }

    /// A glass button (prominent: tinted), or a bordered one before 26.
    @ViewBuilder
    func agentGlassButton(prominent: Bool = false) -> some View {
        if #available(macOS 26.0, iOS 26.0, *) {
            if prominent { buttonStyle(.glassProminent) } else { buttonStyle(.glass) }
        } else {
            if prominent { buttonStyle(.borderedProminent) } else { buttonStyle(.bordered) }
        }
    }

    /// Filled with the deeper shade of the colour (agentBubble), so its white
    /// label reads.
    public func prominentButton() -> some View { modifier(ProminentButton()) }

    /// Soft edges under floating controls (26 and later).
    @ViewBuilder
    func agentSoftEdges() -> some View {
        if #available(macOS 26.0, iOS 26.0, *) {
            scrollEdgeEffectStyle(.soft, for: .all)
        } else {
            self
        }
    }

    /// A light sweeping across, for text about work in progress.
    func shimmering(_ on: Bool = true) -> some View { modifier(Shimmer(on: on)) }
}

private struct Shimmer: ViewModifier {
    let on: Bool
    @State private var phase: CGFloat = -1.3
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func body(content: Content) -> some View {
        if !on || reduceMotion {
            content
        } else {
            // The light drawn over the text and kept to its glyphs by its
            // blend, in one group, rather than masked by a copy of the text:
            // a mask of text was drawn again on the CPU every frame.
            content
                .overlay {
                    GeometryReader { g in
                        LinearGradient(colors: [.clear, .white.opacity(0.75), .clear], startPoint: .leading, endPoint: .trailing)
                            .frame(width: max(40, g.size.width * 0.5))
                            .offset(x: phase * g.size.width)
                    }
                    .blendMode(.sourceAtop)
                    .allowsHitTesting(false)
                }
                .compositingGroup()
                .onAppear {
                    withAnimation(.linear(duration: 1.8).repeatForever(autoreverses: false)) { phase = 1.3 }
                }
        }
    }
}

private struct ProminentButton: ViewModifier {
    @Environment(\.agentBubble) private var fill
    func body(content: Content) -> some View { content.buttonStyle(.borderedProminent).tint(fill) }
}
