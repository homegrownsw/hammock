import XCTest
@testable import HammockKit

/// A message that didn't go says so by the composer (sendError), not on
/// the next prompt card (answerError).
@MainActor
final class AgentSendErrorTests: XCTestCase {
    func testAFailedSendIsTheComposersErrorNotThePromptCards() async {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        let s = AgentSession(paneID: "p1", model: m)
        let went = await s.send("hello")
        XCTAssertFalse(went)
        XCTAssertEqual(s.sendError, "Not connected. Your message wasn't sent.")
        XCTAssertNil(s.answerError)
        XCTAssertTrue(s.pendingSends.isEmpty)
    }

    func testANewPromptClearsTheLastOnesAnswerError() {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        let s = AgentSession(paneID: "p1", model: m)
        s.apply(.with { $0.prompt = .with { $0.promptID = "a" } })
        s.answerError = "Couldn't answer"
        s.apply(.with { $0.prompt = .with { $0.promptID = "a" } })
        XCTAssertEqual(s.answerError, "Couldn't answer", "the same prompt keeps it")
        s.apply(.with { $0.prompt = .with { $0.promptID = "b" } })
        XCTAssertNil(s.answerError)
    }
}
