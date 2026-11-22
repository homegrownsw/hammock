import XCTest
@testable import HammockKit

/// The bar over a pane whose program ended (the Mac's and the iPhone's
/// ExitedPaneBar): when it shows, and what it says.
@MainActor
final class RunAgainTests: XCTestCase {
    private func ended(_ edit: (inout Pane) -> Void = { _ in }) -> Pane {
        var p = Pane.with {
            $0.id = "p"
            $0.kind = .terminal
            $0.exited = true
            $0.exitStatus = 1
        }
        edit(&p)
        return p
    }

    func testAnEndedTerminalCanRunAgain() {
        XCTAssertTrue(ended().canRunAgain)
        XCTAssertFalse(ended { $0.exited = false }.canRunAgain)
        XCTAssertFalse(ended { $0.asleep = true }.canRunAgain)
        XCTAssertFalse(ended { $0.kind = .browser }.canRunAgain)
        // A scheduled run's task runs again instead.
        XCTAssertFalse(ended { $0.scheduleRunID = "r" }.canRunAgain)
    }

    func testItSaysHowItEnded() {
        XCTAssertEqual(ended().exitedStatusLabel, "Exited 1")
        XCTAssertEqual(ended { $0.startError = "no such file" }.exitedStatusLabel, "Couldn't start")
    }

    func testAnAgentWithASessionResumes() {
        XCTAssertEqual(ended().runAgainTitle, "Run Again")
        let agent = ended { $0.agent = .with { $0.harness = "claude"; $0.sessionID = "s" } }
        XCTAssertEqual(agent.runAgainTitle, "Resume")
        // Nothing said in it yet: its own command again.
        XCTAssertEqual(ended { $0.agent = .with { $0.harness = "claude" } }.runAgainTitle, "Run Again")
    }
}
