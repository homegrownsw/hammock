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
}
