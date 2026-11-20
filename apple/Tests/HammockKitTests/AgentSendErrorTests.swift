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

    /// A Mac that's still being dialled (a connection, no link yet, as a
    /// phone has): the send fails at once, not after waiting on a reply.
    func testASendWhileDiallingFailsAtOnce() async throws {
        let m = AppModel(standIn: Silent(), clientKind: "test")
        m.start()
        try await Task.sleep(for: .milliseconds(100))
        XCTAssertNotNil(m.connection)
        XCTAssertFalse(m.connected)
        let s = AgentSession(paneID: "p1", model: m)
        let started = Date()
        let went = await s.send("hello")
        XCTAssertFalse(went)
        XCTAssertLessThan(Date().timeIntervalSince(started), 0.5)
        XCTAssertEqual(s.sendError, "Not connected. Your message wasn't sent.")
        m.stop()
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

/// A pretend daemon that never answers: connected to, never subscribed.
private final class Silent: DaemonStandIn, @unchecked Sendable {
    func receive(_ msg: ClientMessage, reply: @escaping @Sendable (ServerMessage) -> Void) {}
}
