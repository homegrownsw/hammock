import Foundation
import Testing
@testable import HammockKit

/// Next Agent That Needs You: through Needs You's agents in its order (the
/// newest first), skipping finished ones, wrapping both ways.
@MainActor
@Suite(.serialized) struct NeedsYouCycleTests {
    private func model() -> AppModel {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        m.apply(.with {
            $0.full = true
            $0.groups = [.with { $0.id = "gp"; $0.name = "G"; $0.tabIds = ["tb_a", "tb_b", "tb_c", "tb_d"] }]
            $0.tabs = ["a", "b", "c", "d"].map { n in .with { $0.id = "tb_" + n; $0.groupID = "gp"; $0.focusedPaneID = "pn_" + n } }
            $0.panes = ["a", "b", "c", "d"].map { n in .with { $0.id = "pn_" + n; $0.tabID = "tb_" + n } }
        })
        // Each arrives first in the list: c, then b (finished), then a, so
        // Needs You shows a, c.
        for (id, state) in [("c", Hammock_V1_AgentState.errored), ("b", .idle), ("a", .needsInput)] {
            m.apply(.with { $0.attention = [.with { $0.id = "at_" + id; $0.paneID = "pn_" + id; $0.state = state }] })
        }
        return m
    }

    @Test func cyclesNeedsYouInOrderAndWraps() {
        let m = model()
        #expect(m.needsYou.map(\.paneID) == ["pn_a", "pn_c"])
        m.reveal(paneID: "pn_d") // not waiting: starts from the first
        #expect(m.revealNeedingYou(by: 1))
        #expect(m.selectedTabID == "tb_a")
        m.revealNeedingYou(by: 1)
        #expect(m.selectedTabID == "tb_c")
        m.revealNeedingYou(by: 1)
        #expect(m.selectedTabID == "tb_a")
        m.revealNeedingYou(by: -1)
        #expect(m.selectedTabID == "tb_c")
    }

    @Test func previousFromElsewhereStartsAtTheLast() {
        let m = model()
        m.reveal(paneID: "pn_b")
        m.revealNeedingYou(by: -1)
        #expect(m.selectedTabID == "tb_c")
    }

    @Test func nothingWaitingDoesNothing() {
        let m = model()
        m.apply(.with { $0.clearedAttentionIds = ["at_a", "at_c"] })
        m.reveal(paneID: "pn_d")
        #expect(!m.revealNeedingYou(by: 1))
        #expect(m.selectedTabID == "tb_d")
    }
}
