import SwiftUI
import XCTest
@testable import HammockKit

final class ElapsedScheduleTests: XCTestCase {
    /// The first `n` ticks, as seconds since the start, from `from` seconds in.
    private func ticks(from: Double, _ n: Int, mode: TimelineScheduleMode = .normal) -> [Double] {
        let start = Date(timeIntervalSince1970: 1000)
        let schedule = ElapsedSchedule(sinceUnixMs: 1_000_000)
        return schedule.entries(from: start.addingTimeInterval(from), mode: mode)
            .prefix(n).map { $0.timeIntervalSince(start) }
    }

    func testEachSecondForTheFirstMinute() {
        XCTAssertEqual(ticks(from: 0.5, 4), [0.5, 1, 2, 3])
        XCTAssertEqual(ticks(from: 58.5, 4), [58.5, 59, 60, 120])
    }

    func testEachMinuteAfter() {
        XCTAssertEqual(ticks(from: 61, 3), [61, 120, 180])
        XCTAssertEqual(ticks(from: 3600 + 125, 2), [3725, 3780])
    }

    func testMinutesOnlyInLowFrequencyMode() {
        XCTAssertEqual(ticks(from: 5, 3, mode: .lowFrequency), [5, 60, 120])
    }

    func testEveryTickChangesTheString() {
        let start = Date(timeIntervalSince1970: 1000)
        let shown = ticks(from: 0.5, 70).map { elapsedString(sinceUnixMs: 1_000_000, now: start.addingTimeInterval($0)) }
        XCTAssertEqual(shown.count, Set(shown).count)
        XCTAssertEqual(shown.prefix(3), ["0s", "1s", "2s"])
    }

    func testNoTimeTicksOnce() {
        let entries = ElapsedSchedule(sinceUnixMs: 0).entries(from: Date(), mode: .normal)
        XCTAssertEqual(Array(entries.prefix(3)).count, 1)
    }
}
