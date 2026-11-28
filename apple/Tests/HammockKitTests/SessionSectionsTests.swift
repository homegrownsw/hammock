import Foundation
import Testing
@testable import HammockKit

/// Sessions (the phones' tab, the Mac sidebar's view): one row a tab, for
/// its most pressing agent, in Needs You, Working and Recent, newest first.
@MainActor
@Suite(.serialized) struct SessionSectionsTests {
    private func agent(_ state: Hammock_V1_AgentState, since: Int64) -> Hammock_V1_Agent {
        .with { $0.harness = "claude"; $0.state = state; $0.stateSinceUnixMs = since }
    }

    private func model() -> AppModel {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        m.apply(.with {
            $0.full = true
            $0.groups = [
                .with { $0.id = "gp_a"; $0.name = "A"; $0.order = 0; $0.tabIds = ["tb_ask", "tb_old", "tb_two", "tb_shell"] },
                .with { $0.id = "gp_b"; $0.name = "B"; $0.order = 1; $0.tabIds = ["tb_new", "tb_work"] },
            ]
            $0.tabs = [("tb_ask", "gp_a"), ("tb_old", "gp_a"), ("tb_shell", "gp_a"), ("tb_new", "gp_b"), ("tb_work", "gp_b")]
                .map { t in .with { $0.id = t.0; $0.groupID = t.1; $0.layout = .with { $0.paneID = "pn_" + t.0.dropFirst(3) } } }
                + [.with {
                    $0.id = "tb_two"; $0.groupID = "gp_a"
                    $0.layout = .with { $0.split = .with { $0.children = [.with { $0.paneID = "pn_two_rest" }, .with { $0.paneID = "pn_two_err" }] } }
                }]
            $0.panes = [
                .with { $0.id = "pn_ask"; $0.tabID = "tb_ask"; $0.agent = agent(.needsInput, since: 100) },
                .with { $0.id = "pn_old"; $0.tabID = "tb_old"; $0.agent = agent(.idle, since: 10) },
                // A tab of two agents is one session: the one that needs you.
                .with { $0.id = "pn_two_rest"; $0.tabID = "tb_two"; $0.agent = agent(.idle, since: 50) },
                .with { $0.id = "pn_two_err"; $0.tabID = "tb_two"; $0.agent = agent(.errored, since: 40) },
                // No agent: not a session.
                .with { $0.id = "pn_shell"; $0.tabID = "tb_shell" },
                .with { $0.id = "pn_new"; $0.tabID = "tb_new"; $0.agent = agent(.idle, since: 90) },
                .with { $0.id = "pn_work"; $0.tabID = "tb_work"; $0.agent = agent(.working, since: 70) },
            ]
        })
        return m
    }

    @Test func sortsTabsBySectionNewestFirst() {
        let m = model()
        let s = m.sessionSections(in: m.groups)
        #expect(s.needsYou.map(\.pane.id) == ["pn_ask", "pn_two_err"])
        #expect(s.working.map(\.pane.id) == ["pn_work"])
        #expect(s.recent.map(\.pane.id) == ["pn_new", "pn_old"])
    }

    @Test func onlyTheGroupsGiven() {
        let m = model()
        let s = m.sessionSections(in: m.groups.filter { $0.id == "gp_b" })
        #expect(s.needsYou.isEmpty)
        #expect(s.working.map(\.tab.id) == ["tb_work"])
        #expect(s.recent.map(\.tab.id) == ["tb_new"])
    }

    @Test func withoutDropsOnlyRecentRows() {
        let m = model()
        let s = m.sessionSections(in: m.groups).without(panes: ["pn_new", "pn_ask"])
        #expect(s.recent.map(\.pane.id) == ["pn_old"])
        #expect(s.needsYou.count == 2)
    }
}
