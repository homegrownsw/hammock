import XCTest
@testable import HammockKit

final class UsageLimitsTests: XCTestCase {
    func testTimeUntilReset() {
        let now = Date(timeIntervalSince1970: 0)
        func at(_ minutes: Int) -> String { UsageLimitsPrefs.until(now.addingTimeInterval(TimeInterval(minutes * 60)), now: now) }
        XCTAssertEqual(at(45), "45m")
        XCTAssertEqual(at(3 * 60 + 30), "3h30m")
        XCTAssertEqual(at(5 * 60), "5h")
        XCTAssertEqual(at(29 * 60), "1d5h")
        XCTAssertEqual(at(2 * 1440 + 22 * 60), "3d") // rounded, not floored
        XCTAssertEqual(at(2 * 1440 + 5 * 60), "2d")
    }
}

final class RunningTimeTests: XCTestCase {
    func testFormat() {
        XCTAssertEqual(RunningTime.format(0), "0s")
        XCTAssertEqual(RunningTime.format(12.7), "12s")
        XCTAssertEqual(RunningTime.format(65), "1m 05s")
        XCTAssertEqual(RunningTime.format(3720), "1h 02m")
        XCTAssertEqual(RunningTime.format(-3), "0s")
    }
}
