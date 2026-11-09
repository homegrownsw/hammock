import SwiftUI
#if canImport(AppKit)
import AppKit
#endif

/// Marks a link's run in a Text, so its layout can tell where the links are.
struct LinkRun: TextAttribute {}

/// Text with clickable links that look clickable: on the Mac, the pointer
/// turns into the pointing hand over a link. SwiftUI's Text opens its
/// links but leaves the pointer an I-beam (selectable) or an arrow.
struct LinkedText: View {
    let text: AttributedString
    /// What `text` was made from, to cache its tagged Text by.
    let key: String

    /// Inline Markdown (Markdown.inline).
    init(markdown: String) {
        self.text = Markdown.inline(markdown)
        self.key = "in|" + markdown
    }

    init(_ text: AttributedString, key: String) {
        self.text = text
        self.key = key
    }

    var body: some View {
        #if os(macOS)
        if let tagged = RenderCache.get("lt|" + key, { Tagged(text: Self.tagged(text)) }).text {
            tagged.modifier(LinkPointer())
        } else {
            Text(text)
        }
        #else
        // An iPad's pointer could show the hand too, but its links aren't
        // worth the extra layout work on every phone.
        Text(text)
        #endif
    }

    /// Boxed: cached as a bare Optional, RenderCache.get handed back nil,
    /// so no Text ever got the pointer.
    struct Tagged { let text: Text? }

    /// The text with each link's run marked, as one Text (so a selection
    /// still runs across it); nil when it has no links.
    static func tagged(_ a: AttributedString) -> Text? {
        let parts = a.runs[\.link]
        guard parts.contains(where: { $0.0 != nil }) else { return nil }
        // One flat interpolation, not one nested Text per part.
        var s = LocalizedStringKey.StringInterpolation(literalCapacity: 0, interpolationCount: parts.count)
        for (url, range) in parts {
            let part = Text(AttributedString(a[range]))
            s.appendInterpolation(url == nil ? part : part.customAttribute(LinkRun()))
        }
        return Text(LocalizedStringKey(stringInterpolation: s))
    }
}

#if os(macOS)
/// Where a Text's link runs sit, in the Text's own coordinates.
private struct LinkRectsKey: PreferenceKey {
    static let defaultValue: [CGRect] = []
    static func reduce(value: inout [CGRect], nextValue: () -> [CGRect]) { value += nextValue() }
}

/// The pointing hand over a Text's marked link runs.
private struct LinkPointer: ViewModifier {
    @State private var rects: [CGRect] = []
    @State private var overLink = false

    func body(content: Content) -> some View {
        content
            .backgroundPreferenceValue(Text.LayoutKey.self) { layouts in
                GeometryReader { geo in
                    Color.clear.preference(key: LinkRectsKey.self, value: Self.linkRects(layouts, geo))
                }
            }
            .onPreferenceChange(LinkRectsKey.self) { rects = $0 }
            .onContinuousHover { phase in
                // Set by hand on every move: the selectable text's own
                // I-beam beats .pointerStyle(.link), and puts itself back as
                // the pointer moves.
                switch phase {
                case .active(let p):
                    let over = rects.contains { $0.contains(p) }
                    if over {
                        NSCursor.pointingHand.set()
                    } else if overLink {
                        NSCursor.iBeam.set()
                    }
                    overLink = over
                case .ended:
                    if overLink { NSCursor.arrow.set() }
                    overLink = false
                }
            }
            .onDisappear { if overLink { NSCursor.arrow.set() } }
    }

    static func linkRects(_ layouts: [Text.LayoutKey.AnchoredLayout], _ geo: GeometryProxy) -> [CGRect] {
        var out: [CGRect] = []
        for l in layouts {
            let origin = geo[l.origin]
            for line in l.layout {
                for run in line where run[LinkRun.self] != nil {
                    out.append(run.typographicBounds.rect.offsetBy(dx: origin.x, dy: origin.y))
                }
            }
        }
        return out
    }
}
#endif
