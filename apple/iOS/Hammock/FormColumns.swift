import SwiftUI

/// A form's sections in one column on a narrow screen (an iPhone, iPhone
/// Duo closed), and in two side by side on a wide one (iPhone Duo open, an
/// iPad), each about a phone's width, rather than one form stretched across
/// the screen with each control far from its label. On iPhone Duo the
/// columns meet at the fold (flat too, so nothing moves as it bends), each
/// clear of its curve; elsewhere they're halves.
struct FormColumns<Leading: View, Trailing: View>: View {
    /// Two columns when there's room; false keeps one (nothing worth a
    /// second column).
    var enabled = true
    @ViewBuilder var leading: Leading
    @ViewBuilder var trailing: Trailing
    @Environment(\.horizontalSizeClass) private var hSize
    /// A fold running down the screen: where it starts and ends, across.
    @State private var fold: ClosedRange<CGFloat>?

    var body: some View {
        if enabled, hSize == .regular {
            GeometryReader { geo in
                let split = fold ?? (geo.size.width / 2)...(geo.size.width / 2)
                HStack(spacing: 0) {
                    Form { leading }.frame(width: max(split.lowerBound, 0))
                    Spacer(minLength: 0).frame(width: split.upperBound - split.lowerBound)
                    Form { trailing }
                }
            }
            .onGeometryChange(for: ClosedRange<CGFloat>?.self) { proxy in
                guard #available(iOS 27.1, *) else { return nil }
                let regions = proxy.reservedRegions(kind: .division, options: .includeInactive)
                guard let f = regions.first(where: { $0.frame.height > $0.frame.width })?.frame,
                      f.minX > 0, f.maxX < proxy.size.width else { return nil }
                return f.minX...f.maxX
            } action: { fold = $0 }
        } else {
            Form {
                leading
                trailing
            }
        }
    }
}
