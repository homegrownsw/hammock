import SwiftUI
#if canImport(AppKit)
import AppKit
#else
import UIKit
#endif

/// An agent at work ("Orbit"): a short indigo arc going round a faint
/// track, once every 2.4 s. Starting is the same, dimmer and at half the
/// speed. Under Reduce Motion the arc stands still at the top.
public struct WorkingDot: View {
    var size: CGFloat
    var starting: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    public init(size: CGFloat = 15, starting: Bool = false) {
        self.size = size
        self.starting = starting
    }

    public var body: some View {
        let k = size / 20
        let line = max(1.5, 2.5 * k)
        let period = starting ? 4.8 : 2.4
        ZStack {
            Circle().stroke(StatusPalette.track, lineWidth: line)
            TimelineView(.animation(minimumInterval: 1.0 / 30, paused: reduceMotion)) { context in
                let t = reduceMotion ? 0 : context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: period) / period
                Circle()
                    .trim(from: 0, to: 0.28)
                    .stroke(StatusPalette.indigo, style: StrokeStyle(lineWidth: line, lineCap: .round))
                    .rotationEffect(.degrees(t * 360 - 90))
                    .opacity(starting ? 0.55 : 1)
            }
        }
        .frame(width: 14.5 * k, height: 14.5 * k)
        .frame(width: size, height: size)
        .accessibilityElement()
        .accessibilityLabel(starting ? "Starting" : "Working")
    }
}
