import XCTest
@testable import HammockKit

final class GrantRequestTests: XCTestCase {
    func testAnAgentsRequestCanBeAllowedAlways() {
        let g = Hammock_V1_GrantRequest.with { $0.headline = "Claude in Web ▸ api wants to type into Web ▸ api"; $0.action = "send" }
        XCTAssertTrue(g.canAllowAlways)
        XCTAssertNil(g.originText)
        XCTAssertFalse(g.showsWholeHeadline)
    }

    func testWhatOnlyYouMayDoIsAllowedOnceAndSaysWhereItCameFrom() {
        let g = Hammock_V1_GrantRequest.with {
            $0.headline = "hmk wants to show a pairing code"; $0.action = "person"; $0.person = true
            $0.origin = "From the shell in Web ▸ api"
        }
        XCTAssertFalse(g.canAllowAlways)
        XCTAssertEqual(g.originText, "From the shell in Web ▸ api")
        XCTAssertTrue(g.showsWholeHeadline)
        XCTAssertNil(g.agentNote)
    }

    func testAnAutomationShowsItsTitleAndParts() {
        let g = Hammock_V1_GrantRequest.with {
            $0.headline = "Claude in Web ▸ api wants to add the automation “Nightly”: Claude Code, every day at 02:00, in ~/app: check"
            $0.title = "Claude in Web ▸ api wants to add the automation “Nightly”"
            $0.details = [.with { $0.label = "When"; $0.text = "Every day at 02:00" }]
            $0.action = "schedule"
        }
        XCTAssertTrue(g.hasDetails)
        XCTAssertEqual(g.titleText, g.title)
        XCTAssertFalse(g.showsWholeHeadline)
        // From an older daemon: all of it, in one line.
        let old = Hammock_V1_GrantRequest.with { $0.headline = "x wants to add the automation “Nightly”: …"; $0.action = "schedule" }
        XCTAssertFalse(old.hasDetails)
        XCTAssertEqual(old.titleText, old.headline)
        XCTAssertTrue(old.showsWholeHeadline)
    }
}
