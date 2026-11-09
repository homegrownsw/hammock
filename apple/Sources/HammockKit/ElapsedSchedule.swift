import SwiftUI

/// When `elapsedString` changes: each second for the first minute after
/// `start`, then each whole minute after it. A `.periodic` schedule left
/// "44s" standing for up to 30 seconds and flipped minutes late, since its
/// ticks weren't lined up with the start.
public struct ElapsedSchedule: TimelineSchedule {
    /// What the time counts from; nil for no time (`elapsedString`'s ""),
    /// which needs no ticks after the first.
    public let start: Date?

    public init(sinceUnixMs ms: Int64) {
        start = ms > 0 ? Date(timeIntervalSince1970: Double(ms) / 1000) : nil
    }

    public func entries(from startDate: Date, mode: TimelineScheduleMode) -> Entries {
        Entries(upcoming: startDate, start: start, seconds: mode != .lowFrequency)
    }

    public struct Entries: Sequence, IteratorProtocol {
        var upcoming: Date?
        let start: Date?
        /// Seconds are shown ticking (not in low-frequency mode, where
        /// SwiftUI asks for updates at most each minute or so).
        let seconds: Bool

        public mutating func next() -> Date? {
            guard let date = upcoming else { return nil }
            upcoming = start.map { ElapsedSchedule.following(date, start: $0, seconds: seconds) }
            return date
        }
    }

    /// The first date after `date` at which the shown time changes.
    static func following(_ date: Date, start: Date, seconds: Bool) -> Date {
        let elapsed = date.timeIntervalSince(start)
        if seconds, elapsed < 60 { return start.addingTimeInterval(elapsed.rounded(.down) + 1) }
        return start.addingTimeInterval(((elapsed / 60).rounded(.down) + 1) * 60)
    }
}

/// `elapsedString` since `ms`, redrawn each time it changes (`ElapsedSchedule`).
public struct ElapsedText: View {
    let ms: Int64
    public init(sinceUnixMs ms: Int64) { self.ms = ms }

    public var body: some View {
        TimelineView(ElapsedSchedule(sinceUnixMs: ms)) { ctx in
            Text(elapsedString(sinceUnixMs: ms, now: ctx.date)).monospacedDigit()
        }
    }
}
