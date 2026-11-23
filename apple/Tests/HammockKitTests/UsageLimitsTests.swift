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
