import Foundation
import Testing
@testable import HammockKit

/// what an update clears or resolves reaches `onResolved`, so its
/// notification goes, and a full update says what's still live.
@MainActor
@Suite(.serialized) struct ResolvedNoticeTests {
    private func model() -> AppModel {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        m.apply(.with {
            $0.full = true
            $0.attention = [.with { $0.id = "at_pn_a"; $0.paneID = "pn_a"; $0.state = .idle }]
            $0.grants = [.with { $0.id = "gr_1"; $0.requesterPaneID = "pn_a" }]
            $0.joinRequests = [.with { $0.id = "jr_1" }]
            $0.linkRequests = [.with { $0.id = "lr_1"; $0.incoming = true }]
        })
        return m
    }

    @Test func clearedAndResolvedIDsAreReported() {
        let m = model()
        var resolved: [[String]] = []
        m.onResolved = { resolved.append($0) }
        m.apply(.with {
            $0.clearedAttentionIds = ["at_pn_a"]
            $0.resolvedGrantIds = ["gr_1"]
            $0.resolvedJoinIds = ["jr_1"]
            $0.resolvedLinkIds = ["lr_1"]
        })
        #expect(resolved == [["at_pn_a", "gr_1", "jr_1", "lr_1"]])
        #expect(m.liveNoticeIDs.isEmpty)
    }

    @Test func updatesThatResolveNothingStayQuiet() {
        let m = model()
        var calls = 0
        m.onResolved = { _ in calls += 1 }
        m.apply(.with { $0.attention = [.with { $0.id = "at_pn_b"; $0.paneID = "pn_b"; $0.state = .needsInput }] })
        #expect(calls == 0)
    }

    @Test func onlyAFullUpdateReconciles() {
        let m = model()
        var calls = 0
        m.onFullState = { calls += 1 }
        m.apply(.with { $0.clearedAttentionIds = ["at_pn_a"] })
        #expect(calls == 0)
    }

    @Test func listsArePrunedBeforeOnResolved() {
        let m = model()
        var live: Set<String>?
        m.onResolved = { _ in live = m.liveNoticeIDs }
        m.apply(.with { $0.clearedAttentionIds = ["at_pn_a"]; $0.resolvedGrantIds = ["gr_1"] })
        #expect(live == ["jr_1", "lr_1"])
    }

    @Test func fullStateListsWhatsLive() {
        let m = model()
        var live: Set<String>?
        m.onFullState = { live = m.liveNoticeIDs }
        m.apply(.with {
            $0.full = true
            $0.attention = [.with { $0.id = "at_pn_b"; $0.paneID = "pn_b"; $0.state = .needsInput }]
        })
        #expect(live == ["at_pn_b"])
    }
}
