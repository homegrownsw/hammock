import CoreGraphics
import SwiftUI

/// Hammock's mark: a brush-stroke tilde, thick in the middle and tapering to
/// a point at each end, coloured left to right through six colours.
///
/// The outline is drawn on a 24-point grid with y pointing down. The app
/// icons (scripts/make-icon.swift) draw the same outline: change both.
public enum HammockMark {
    /// The part of the 24-point grid the stroke covers, with its softened
    /// tips (see `tipSoftening`).
    public static let bounds = CGRect(x: 2.2, y: 7.7, width: 19.6, height: 8.7)

    /// How wide a stroke of the stroke's own colour rounds off its sharp tips,
    /// in grid points.
    public static let tipSoftening: CGFloat = 0.6

    /// The six colours, left to right: green, yellow, orange, red, purple, blue.
    public static let spectrum: [(r: Double, g: Double, b: Double)] = {
        let hex: [UInt32] = [0x61D836, 0xFFD60A, 0xFF9F0A, 0xFF453A, 0xBF5AF2, 0x0A84FF]
        return hex.map { c -> (r: Double, g: Double, b: Double) in
            (Double(c >> 16 & 0xFF) / 255, Double(c >> 8 & 0xFF) / 255, Double(c & 0xFF) / 255)
        }
    }()

    public static var colors: [Color] { spectrum.map { Color(red: $0.r, green: $0.g, blue: $0.b) } }

    /// The outline, scaled by `scale` and moved by `origin` (y down, like the grid).
    public static func path(scale: CGFloat = 1, origin: CGPoint = .zero) -> CGPath {
        func p(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: origin.x + x * scale, y: origin.y + y * scale) }
        let path = CGMutablePath()
        path.move(to: p(2.5, 13.2))
        path.addCurve(to: p(12, 11), control1: p(5, 7.8), control2: p(9, 8.4))
        path.addCurve(to: p(21.5, 9.6), control1: p(15, 13.6), control2: p(18.4, 14))
        path.addCurve(to: p(11.6, 13.6), control1: p(19, 16), control2: p(14.8, 16.2))
        path.addCurve(to: p(2.5, 13.2), control1: p(8.6, 11.2), control2: p(5.4, 10.8))
        path.closeSubpath()
        return path
    }

    /// The outline fitted into `rect` (y down), keeping its proportions and
    /// centring the stroke itself, not the 24-point grid.
    public static func path(fitting rect: CGRect) -> CGPath {
        let scale = min(rect.width / bounds.width, rect.height / bounds.height)
        let origin = CGPoint(x: rect.midX - bounds.midX * scale, y: rect.midY - bounds.midY * scale)
        return path(scale: scale, origin: origin)
    }
}

/// The mark's outline as a SwiftUI shape, fitted to its frame.
public struct HammockMarkShape: Shape {
    public init() {}
    public func path(in rect: CGRect) -> Path { Path(HammockMark.path(fitting: rect)) }
}

/// The mark in colour, with the soft glow it has in the app icon.
public struct HammockMarkView: View {
    var glow: Bool

    public init(glow: Bool = true) { self.glow = glow }

    public var body: some View {
        GeometryReader { geo in
            let scale = min(geo.size.width / HammockMark.bounds.width, geo.size.height / HammockMark.bounds.height)
            let fill = LinearGradient(colors: HammockMark.colors, startPoint: .leading, endPoint: .trailing)
            ZStack {
                if glow {
                    stroke(fill, width: HammockMark.tipSoftening * 3 * scale)
                        .blur(radius: scale * 1.1)
                        .opacity(0.8)
                }
                stroke(fill, width: HammockMark.tipSoftening * scale)
            }
        }
        .aspectRatio(HammockMark.bounds.width / HammockMark.bounds.height, contentMode: .fit)
        .accessibilityHidden(true)
    }

    private func stroke(_ fill: LinearGradient, width: CGFloat) -> some View {
        HammockMarkShape().fill(fill)
            .overlay(HammockMarkShape().stroke(fill, style: StrokeStyle(lineWidth: width, lineJoin: .round)))
    }
}
