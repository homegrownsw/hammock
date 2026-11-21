import Foundation
import Testing
@testable import HammockKit

/// Move Tab Up and Move Tab Down: one place within the tab's Group, not past either end.
@MainActor
@Suite struct TabMoveTests {
    private func model() -> AppModel {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        m.apply(.with {
            $0.full = true
            $0.groups = [.with { $0.id = "gp"; $0.name = "G"; $0.tabIds = ["tb_a", "tb_b", "tb_c"] }]
            $0.tabs = ["a", "b", "c"].map { n in .with { $0.id = "tb_" + n; $0.groupID = "gp" } }
        })
        return m
    }

    @Test func movesOnePlaceWithinTheGroup() {
        let m = model()
        #expect(m.tabMoveIndex("tb_b", by: -1) == 0)
        #expect(m.tabMoveIndex("tb_b", by: 1) == 2)
    }

    @Test func stopsAtEitherEnd() {
        let m = model()
        #expect(m.tabMoveIndex("tb_a", by: -1) == nil)
        #expect(m.tabMoveIndex("tb_c", by: 1) == nil)
        #expect(m.tabMoveIndex("tb_missing", by: 1) == nil)
    }
}
