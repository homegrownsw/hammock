import Foundation
import Testing
@testable import HammockKit

/// A full update (after a reconnect) without the selected tab moves the
/// selection to the tab beside it, as a deleted tab does.
@MainActor
@Suite(.serialized) struct ReconnectSelectionTests {
    private func state(tabs ids: [String]) -> Hammock_V1_StateUpdate {
        .with {
            $0.full = true
            $0.groups = [
                .with { $0.id = "gp_a"; $0.name = "A"; $0.order = 0; $0.tabIds = ["tb_a1"] },
                .with { $0.id = "gp_b"; $0.name = "B"; $0.order = 1; $0.tabIds = ids },
            ]
            $0.tabs = (["tb_a1"] + ids).map { id in .with { $0.id = id; $0.groupID = id == "tb_a1" ? "gp_a" : "gp_b" } }
        }
    }

    @Test func selectedTabGoneSelectsItsNeighbour() {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        m.apply(state(tabs: ["tb_b1", "tb_b2", "tb_b3"]))
        m.selectedTabID = "tb_b2"
        m.apply(state(tabs: ["tb_b1", "tb_b3"]))
        #expect(m.selectedTabID == "tb_b3")
    }

    @Test func selectedTabStaysWhenStillThere() {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        m.apply(state(tabs: ["tb_b1", "tb_b2"]))
        m.selectedTabID = "tb_b2"
        m.apply(state(tabs: ["tb_b1", "tb_b2"]))
        #expect(m.selectedTabID == "tb_b2")
    }
}
