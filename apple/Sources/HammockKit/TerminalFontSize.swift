import Foundation

/// The terminal's font size on the phones (Settings ▸ Terminal ▸ Size),
/// which pinching a terminal changes too.
public enum TerminalFontSize {
    public static let range: ClosedRange<Double> = 7...18

    /// The size a pinch picks: where it started, scaled by the pinch, in
    /// whole points within the setting's range.
    public static func pinched(from start: Double, scale: Double) -> Double {
        guard scale.isFinite, scale > 0 else { return min(max(start, range.lowerBound), range.upperBound) }
        return min(max((start * scale).rounded(), range.lowerBound), range.upperBound)
    }
}
