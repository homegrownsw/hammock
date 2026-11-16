import SwiftUI

extension EnvironmentValues {
    /// The theme's wash for screens of grouped rows (the iPhone sets it from
    /// its theme; nil: the system's own background).
    @Entry public var themedScreenWash: AnyShapeStyle?
}

extension View {
    /// A screen of grouped rows on the theme's colour (`themedScreenWash`),
    /// over the grouped background, as the Mac's sidebar is its tint over
    /// glass. Each screen takes it itself: a navigation stack draws its own
    /// background under what it pushes. The Mac leaves its own as they are.
    public func themedScreenBackground() -> some View {
        modifier(ThemedScreenBackground())
    }
}

private struct ThemedScreenBackground: ViewModifier {
    @Environment(\.themedScreenWash) private var wash

    func body(content: Content) -> some View {
        #if os(iOS)
        content
            .scrollContentBackground(.hidden)
            .background {
                ZStack {
                    Color(.systemGroupedBackground)
                    if let wash { Rectangle().fill(wash) }
                }
                .ignoresSafeArea()
            }
        #else
        content
        #endif
    }
}
