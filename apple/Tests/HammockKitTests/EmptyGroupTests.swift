import Foundation
import Testing
@testable import HammockKit

/// a Group whose tabs have all closed stays selectable, shown in place
/// of a tab, and the Group you're in follows it.
@MainActor
@Suite(.serialized) struct EmptyGroupTests {
    private func model() -> AppModel {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        m.apply(.with {
            $0.full = true
            $0.groups = [
                .with { $0.id = "gp_a"; $0.name = "A"; $0.order = 0; $0.tabIds = ["tb_a1"] },
                .with { $0.id = "gp_b"; $0.name = "B"; $0.order = 1; $0.tabIds = ["tb_b1"]; $0.folder = "/tmp/b" },
            ]
            $0.tabs = [.with { $0.id = "tb_a1"; $0.groupID = "gp_a" }, .with { $0.id = "tb_b1"; $0.groupID = "gp_b" }]
        })
        m.selectedTabID = "tb_b1"
        return m
    }

    @Test func lastTabClosingShowsItsGroup() {
        let m = model()
        m.apply(.with {
            $0.deletedTabIds = ["tb_b1"]
            $0.groups = [.with { $0.id = "gp_b"; $0.name = "B"; $0.order = 1; $0.folder = "/tmp/b" }]
        })
        #expect(m.selectedTabID == nil)
        #expect(m.selectedEmptyGroup?.id == "gp_b")
        #expect(m.currentGroup?.id == "gp_b")
        #expect(m.groupFolder(m.currentGroup!) == "/tmp/b")
    }

    @Test func aTabArrivingInItIsShown() {
        let m = model()
        m.apply(.with {
            $0.deletedTabIds = ["tb_b1"]
            $0.groups = [.with { $0.id = "gp_b"; $0.name = "B"; $0.order = 1 }]
        })
        m.apply(.with {
            $0.groups = [.with { $0.id = "gp_b"; $0.name = "B"; $0.order = 1; $0.tabIds = ["tb_b2"] }]
            $0.tabs = [.with { $0.id = "tb_b2"; $0.groupID = "gp_b" }]
        })
        #expect(m.selectedTabID == "tb_b2")
        #expect(m.selectedEmptyGroup == nil)
    }

    @Test func selectingATabLeavesTheEmptyGroup() {
        let m = model()
        m.apply(.with {
            $0.deletedTabIds = ["tb_b1"]
            $0.groups = [.with { $0.id = "gp_b"; $0.name = "B"; $0.order = 1 }]
        })
        m.selectedTabID = "tb_a1"
        #expect(m.selectedEmptyGroup == nil)
        #expect(m.currentGroup?.id == "gp_a")
        // Cycling Groups stops at the empty one too.
        m.cycleGroup(by: 1)
        #expect(m.selectedEmptyGroup?.id == "gp_b")
        m.cycleGroup(by: 1)
        #expect(m.selectedTabID == "tb_a1")
    }

    @Test func itsGoingSelectsAnother() {
        let m = model()
        m.apply(.with {
            $0.deletedTabIds = ["tb_b1"]
            $0.groups = [.with { $0.id = "gp_b"; $0.name = "B"; $0.order = 1 }]
        })
        m.apply(.with { $0.deletedGroupIds = ["gp_b"] })
        #expect(m.selectedEmptyGroup == nil)
        #expect(m.selectedTabID == "tb_a1")
    }

    @Test func unnamedEmptyGroupIsNamedAfterItsFolder() {
        let m = model()
        let g = WorkspaceGroup.with { $0.id = "gp_x"; $0.folder = "/Users/someone/Developer/proj" }
        #expect(m.title(of: g) == "proj")
    }
}
