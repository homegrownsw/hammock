import Foundation
import Observation
import Testing
@testable import HammockKit

/// A pane's size changing (each step of resizing a window) is kept, but
/// doesn't wake the views that read `panes`: nothing on screen shows it.
@MainActor
@Suite(.serialized) struct PaneResizeTests {
    private func model() -> AppModel {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        m.apply(.with {
            $0.full = true
            $0.groups = [.with { $0.id = "gp_a"; $0.name = "A"; $0.tabIds = ["tb_a"] }]
            $0.tabs = [.with { $0.id = "tb_a"; $0.groupID = "gp_a" }]
            $0.panes = [pane(cols: 80, rows: 24, title: "zsh")]
        })
        return m
    }

    private func pane(cols: UInt32, rows: UInt32, title: String) -> Pane {
        .with { $0.id = "pn_a"; $0.tabID = "tb_a"; $0.cols = cols; $0.rows = rows; $0.title = title }
    }

    /// Whether applying `update` told observers of `panes`.
    private func notifies(_ m: AppModel, _ update: Hammock_V1_StateUpdate) -> Bool {
        nonisolated(unsafe) var told = false
        withObservationTracking { _ = m.panes } onChange: { told = true }
        m.apply(update)
        return told
    }

    @Test func aSizeAloneDoesNotWakeViews() {
        let m = model()
        #expect(!notifies(m, .with { $0.panes = [pane(cols: 100, rows: 30, title: "zsh")] }))
        #expect(m.panes["pn_a"]?.cols == 100 && m.panes["pn_a"]?.rows == 30)
    }

    @Test func anythingElseDoes() {
        let m = model()
        #expect(notifies(m, .with { $0.panes = [pane(cols: 100, rows: 30, title: "vim")] }))
        #expect(m.panes["pn_a"]?.title == "vim" && m.panes["pn_a"]?.cols == 100)
    }
}
