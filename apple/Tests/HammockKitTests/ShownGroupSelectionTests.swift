import Foundation
import Testing
@testable import HammockKit

/// Where the selection lands when it has to move: on a Group the sidebar
/// shows, not one on a computer it's hiding.
@MainActor
@Suite(.serialized) struct ShownGroupSelectionTests {
    private func state() -> Hammock_V1_StateUpdate {
        .with {
            $0.full = true
            $0.computers = .with { $0.list = [.with { $0.userID = "u_studio" }] }
            $0.groups = [
                .with { $0.id = "gp_here"; $0.name = "Here"; $0.order = 0; $0.tabIds = ["tb_here"] },
                .with {
                    $0.id = "gp_studio"; $0.name = "Studio"; $0.order = 1; $0.tabIds = ["tb_studio"]
                    $0.hostUserID = "u_studio"; $0.myRole = .owner
                },
            ]
            $0.tabs = [.with { $0.id = "tb_here"; $0.groupID = "gp_here" }, .with { $0.id = "tb_studio"; $0.groupID = "gp_studio" }]
        }
    }

    @Test func nothingSelectedPicksAShownGroup() {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        let was = m.computerFilter
        defer { m.computerFilter = was }
        m.computerFilter = "u_studio"
        m.apply(state())
        #expect(m.selectedTabID == "tb_studio")
    }

    @Test func noneShownFallsBackToAny() {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        let was = m.computerFilter
        defer { m.computerFilter = was }
        m.computerFilter = "u_studio"
        var s = state()
        s.groups.removeLast()
        s.tabs.removeLast()
        m.apply(s)
        #expect(m.selectedTabID == "tb_here")
    }
}
