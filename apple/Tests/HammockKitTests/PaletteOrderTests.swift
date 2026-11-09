import XCTest
@testable import HammockKit

/// the palette lists the tabs that need you first, then unseen
/// finished ones, then the rest by when you last opened them.
final class PaletteOrderTests: XCTestCase {
    private func tab(_ id: String, _ state: AgentState? = nil, since: Int64 = 0, seen: Bool = false) -> (tab: WorkspaceTab, panes: [Pane]) {
        let pane = Pane.with { p in
            p.id = id + "-pane"
            if let state { p.agent = .with { $0.harness = "claude"; $0.state = state; $0.stateSinceUnixMs = since; $0.seen = seen } }
        }
        return (WorkspaceTab.with { $0.id = id }, [pane])
    }

    private func order(_ tabs: [(tab: WorkspaceTab, panes: [Pane])], opened: [String: Double] = [:], current: String? = nil) -> [String] {
        AppModel.paletteOrder(tabs, openedAt: opened, current: current).map(\.id)
    }

    func testTheTabYoureOnComesLast() {
        XCTAssertEqual(order([tab("a"), tab("b"), tab("c")], opened: ["a": 10, "b": 20], current: "b"), ["a", "c", "b"])
    }

    func testAFailureYouveOpenedSinceIsJustATab() {
        let tabs = [tab("x"), tab("failed", .errored, since: 9000)]
        XCTAssertEqual(order(tabs, opened: ["failed": 8, "x": 5]), ["failed", "x"])
        XCTAssertEqual(order(tabs, opened: ["failed": 10, "x": 11]), ["x", "failed"])
        XCTAssertEqual(order(tabs, opened: ["x": 11], current: "failed"), ["x", "failed"])
    }

    func testTheMostUrgentPaneSpeaksForItsTab() {
        let split = Pane.with { $0.id = "s2"; $0.agent = .with { $0.harness = "codex"; $0.state = .needsInput } }
        let tabs: [(tab: WorkspaceTab, panes: [Pane])] = [
            tab("x"),
            (WorkspaceTab.with { $0.id = "split" }, tab("s", .working).panes + [split]),
            (WorkspaceTab.with { $0.id = "closed" }, [Pane.with { $0.exited = true; $0.agent = .with { $0.harness = "claude"; $0.state = .needsInput } }]),
            // A command you ran while it was idle: at work, not finished.
            (WorkspaceTab.with { $0.id = "cmd" }, [Pane.with { $0.agent = .with { $0.harness = "claude"; $0.state = .idle; $0.runningCommand = true } }]),
        ]
        XCTAssertEqual(order(tabs), ["split", "x", "closed", "cmd"])
    }

    func testNeedsYouThenFinishedThenTheRest() {
        let tabs = [tab("shell"), tab("working", .working), tab("done", .idle, since: 5), tab("ask", .needsInput, since: 1), tab("failed", .errored, since: 9)]
        XCTAssertEqual(order(tabs), ["ask", "failed", "done", "shell", "working"])
    }

    func testFinishedAndLookedAtIsJustATab() {
        let tabs = [tab("a"), tab("seen", .idle, since: 5, seen: true)]
        XCTAssertEqual(order(tabs, opened: ["a": 2, "seen": 1]), ["a", "seen"])
    }

    func testTheNewestWaitingComesFirst() {
        XCTAssertEqual(order([tab("old", .needsInput, since: 1), tab("new", .needsInput, since: 2)]), ["new", "old"])
    }

    func testTheRestByWhenYouOpenedThem() {
        let tabs = [tab("a"), tab("b"), tab("c"), tab("d")]
        // Never opened here: sidebar order, after the ones you have.
        XCTAssertEqual(order(tabs, opened: ["c": 20, "a": 10]), ["c", "a", "b", "d"])
    }
}
