import SwiftUI
import XCTest
@testable import HammockKit

/// which agent a tab's row stands for, and the logos' paths.
final class AgentStatusTests: XCTestCase {
    private func pane(_ id: String, _ harness: String, _ state: AgentState, seen: Bool = false, exited: Bool = false) -> Pane {
        Pane.with {
            $0.id = id
            $0.exited = exited
            $0.agent = .with { $0.harness = harness; $0.state = state; $0.seen = seen }
        }
    }

    func testTheShownAgentIsTheOneWhoseStateShows() {
        let panes = [pane("a", "claude", .working), pane("b", "codex", .needsInput)]
        XCTAssertEqual(AppModel.shownAgentPane(in: panes, focused: "a")?.agent.harness, "codex")
    }

    func testATieGoesToTheFocusedPane() {
        let panes = [pane("a", "claude", .working), pane("b", "codex", .working)]
        XCTAssertEqual(AppModel.shownAgentPane(in: panes, focused: "b")?.agent.harness, "codex")
        XCTAssertEqual(AppModel.shownAgentPane(in: panes, focused: "a")?.agent.harness, "claude")
    }

    func testAnAgentAtRestStillNamesTheTab() {
        let panes = [Pane.with { $0.id = "shell" }, pane("a", "codex", .idle, seen: true)]
        XCTAssertEqual(AppModel.shownAgentPane(in: panes, focused: "shell")?.agent.harness, "codex")
    }

    func testAnAgentThatQuitDoesNot() {
        // Claude quit but its shell is still open: a plain shell again.
        XCTAssertNil(AppModel.shownAgentPane(in: [pane("a", "claude", .exited)], focused: "a"))
        // A closed pane whose agent was at rest.
        XCTAssertNil(AppModel.shownAgentPane(in: [pane("a", "claude", .idle, seen: true, exited: true)], focused: "a"))
    }

    func testTheMarksParse() {
        let claude = AgentMark.claude.boundingRect
        XCTAssertEqual(claude.minX, 0, accuracy: 0.01)
        XCTAssertEqual(claude.maxX, 24, accuracy: 0.01)
        XCTAssertEqual(claude.maxY, 24, accuracy: 0.01)
        // The cloud, with the prompt's > and _ as two more subpaths.
        var moves = 0
        AgentMark.codex.forEach { if case .move = $0 { moves += 1 } }
        XCTAssertEqual(moves, 3)
        let codex = AgentMark.codex.boundingRect
        XCTAssertEqual(codex.minY, 3, accuracy: 0.1)
        XCTAssertEqual(codex.maxY, 21, accuracy: 0.1)
    }

    func testHAndVAfterACloseStartFromTheSubpathsStart() {
        let p = SVGPath.parse("M2 2L10 2 10 10ZH6")
        var last = CGPoint.zero
        p.forEach { if case .line(let to) = $0 { last = to } }
        XCTAssertEqual(last, CGPoint(x: 6, y: 2))
    }
}
