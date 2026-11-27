import XCTest
@testable import HammockKit

/// Run Hammock Automation's result line, on the Mac and the iPhone.
final class TriggerOutcomeTests: XCTestCase {
    func testLines() {
        XCTAssertEqual(AppModel.TriggerOutcome.started.line(cut: false), "Started")
        XCTAssertEqual(AppModel.TriggerOutcome.queued("").line(cut: false), "Queued")
        XCTAssertEqual(AppModel.TriggerOutcome.queued("waiting for its cooldown").line(cut: false), "Queued: waiting for its cooldown")
        XCTAssertEqual(AppModel.TriggerOutcome.skipped("Only if: not plugged in").line(cut: false), "Skipped: Only if: not plugged in")
        XCTAssertEqual(AppModel.TriggerOutcome.started.line(cut: true), "Started (only the first 64 KB of the text went in)")
    }
}
