import XCTest
@testable import HammockKit

final class WatchWireTests: XCTestCase {
    func testAQuestionOffersYesAndNoThenQuickReplies() {
        let s = WatchSuggestions.make(lastText: "Tests pass. Is that enough for now?", quickReplies: ["Continue", "Looks good"])
        XCTAssertEqual(s, ["Yes", "No", "Continue", "Looks good"])
    }

    func testAnOfferIsOfferedBack() {
        let s = WatchSuggestions.make(lastText: "Sign-in works in Safari now.\n\nWant me to open a PR?", quickReplies: ["Continue"])
        XCTAssertEqual(s, ["Yes, open a PR", "No", "Continue"])
        XCTAssertEqual(WatchSuggestions.offer(in: "Should I run the full suite?"), "run the full suite")
        // A choice isn't a yes/no offer.
        XCTAssertNil(WatchSuggestions.offer(in: "Should I use Postgres or SQLite?"))
    }

    func testNoQuestionOnlyQuickRepliesWithoutRepeats() {
        let s = WatchSuggestions.make(lastText: "Done: the flaky test is fixed.", quickReplies: ["Continue", "continue", "Run the tests"])
        XCTAssertEqual(s, ["Continue", "Run the tests"])
    }

    func testAtMostSix() {
        let s = WatchSuggestions.make(lastText: "OK?", quickReplies: (1...10).map { "Reply \($0)" })
        XCTAssertEqual(s.count, WatchSuggestions.max)
    }

    private func block(_ id: String, _ kind: AgentBlock.Kind, text: String = "", tool: String = "", images: [String] = [], ms: Int64 = 0) -> AgentBlock {
        .with {
            $0.id = id
            $0.kind = kind
            $0.text = text
            $0.tool = tool
            $0.images = images.map { r in .with { $0.ref = r } }
            $0.durationMs = ms
        }
    }

    func testCondenseFoldsToolRunsAndDropsThinking() {
        let blocks: [AgentBlock] = [
            block("u", .user, text: "Fix the login button"),
            block("t0", .thinking, text: "Hmm"),
            block("a", .tool, tool: "Bash"),
            block("b", .tool, tool: "Bash"),
            block("c", .tool, tool: "Edit"),
            block("d", .tool, tool: "Read", images: ["shot"]),
            block("x", .text, text: "Fixed it."),
            block("done", .done, ms: 252_000),
        ]
        let c = WatchSnapshots.condense(blocks)
        XCTAssertEqual(c.map(\.kind), [.user, .tools, .image, .text, .done])
        XCTAssertEqual(c[1].text, "Ran 2 commands, edited 1 file, read 1 file")
        XCTAssertEqual(c[2].ref, "shot")
        XCTAssertEqual(c[4].durationMs, 252_000)
        XCTAssertEqual(WatchBlock.duration(252_000), "4m 12s")
    }

    func testCondenseKeepsAnsweredQuestions() {
        let q = AgentBlock.with {
            $0.id = "q"
            $0.kind = .question
            $0.questions = [.with { $0.question = "Which database?"; $0.answer = "PostgreSQL" }, .with { $0.question = "Unanswered?" }]
        }
        let c = WatchSnapshots.condense([q])
        XCTAssertEqual(c.count, 1)
        XCTAssertEqual(c[0].text, "Which database?")
        XCTAssertEqual(c[0].answer, "PostgreSQL")
    }

    func testRequestsAndRepliesRoundTrip() throws {
        let r = WatchRequest.answer(host: "h", pane: "p", promptID: "x", option: 2, ticking: [], text: "")
        XCTAssertEqual(try WatchWire.decode(WatchRequest.self, from: WatchWire.encode(r)), r)
        let snap = WatchSnapshot(sessions: [WatchSession(host: "h", hostName: "Studio", paneID: "p", title: "login-fix", harness: "claude", state: .working)],
                                 macs: ["Studio"], at: Date(timeIntervalSince1970: 1000))
        XCTAssertEqual(try WatchWire.decode(WatchReply.self, from: WatchWire.encode(WatchReply.snapshot(snap))), .snapshot(snap))
    }

    func testListOrder() {
        func s(_ id: String, _ st: WatchAgentState, _ since: Int64) -> WatchSession {
            WatchSession(host: "h", hostName: "", paneID: id, title: id, harness: "claude", state: st, sinceMs: since)
        }
        let o = WatchSnapshot.ordered([s("done", .idle, 9), s("work", .working, 5), s("ask-old", .needsInput, 1), s("ask-new", .needsInput, 2)])
        XCTAssertEqual(o.map(\.paneID), ["ask-new", "ask-old", "work", "done"])
    }
}
