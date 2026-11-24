import XCTest
@testable import HammockKit

/// The message box as your other devices have it: taken from each replace
/// (rev 0: the daemon has none) and from updates that carry one, and left
/// alone by updates that don't.
@MainActor
final class SharedDraftTests: XCTestCase {
    func testTakenFromReplacesAndChanges() {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        let s = AgentSession(paneID: "p1", model: m)
        s.apply(.with { $0.replace = true; $0.composerDraft = "fix the"; $0.composerRev = 3; $0.composerWriter = "mac" })
        XCTAssertEqual(s.sharedDraft, SharedDraft(text: "fix the", rev: 3, writer: "mac"))
        // An update about something else: unchanged.
        s.apply(.with { $0.working = true })
        XCTAssertEqual(s.sharedDraft.rev, 3)
        s.apply(.with { $0.composerDraft = "fix the test"; $0.composerRev = 4; $0.composerWriter = "phone" })
        XCTAssertEqual(s.sharedDraft, SharedDraft(text: "fix the test", rev: 4, writer: "phone"))
        // The daemon restarted: it has none.
        s.apply(.with { $0.replace = true })
        XCTAssertEqual(s.sharedDraft, SharedDraft())
    }

    func testASubagentsViewHasNone() {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        let s = AgentSession(paneID: "p1", subagentID: "a1", model: m)
        s.apply(.with { $0.replace = true; $0.composerDraft = "hi"; $0.composerRev = 1 })
        XCTAssertEqual(s.sharedDraft, SharedDraft())
    }

    func testNotSharedWhileDisconnected() {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        let s = AgentSession(paneID: "p1", model: m)
        XCTAssertFalse(s.shareDraft("hi", writer: "box"))
    }
}
