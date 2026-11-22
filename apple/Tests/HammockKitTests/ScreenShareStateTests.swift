import Testing
@testable import HammockKit

@Suite struct ScreenShareStateTests {
    @Test func opensMinimisesAndComesBack() {
        var s = ScreenShareState()
        #expect(!s.isShowing)
        s.open(computerID: "us_studio", name: "Studio")
        #expect(s.phase == .open)
        let session = s.session
        s.minimise()
        #expect(s.isMinimised && s.isShowing)
        // Back from the bar: the same session, so the same streams.
        s.expand()
        #expect(s.phase == .open && s.session == session)
        s.minimise()
        s.open(computerID: "us_studio", name: "Studio")
        #expect(s.phase == .open && s.session == session)
    }

    @Test func anotherMacReplacesIt() {
        var s = ScreenShareState()
        s.open(computerID: "us_studio", name: "Studio")
        s.minimise()
        let session = s.session
        s.open(computerID: "us_laptop", name: "Laptop")
        #expect(s.phase == .open && s.computerID == "us_laptop" && s.name == "Laptop" && s.session == session + 1)
    }

    @Test func endingLeavesNothing() {
        var s = ScreenShareState()
        s.open(computerID: "us_studio", name: "Studio")
        s.minimise()
        s.end()
        #expect(s.phase == .ended && s.computerID.isEmpty && !s.isShowing)
        // Nothing to minimise or open again from.
        s.minimise()
        s.expand()
        #expect(s.phase == .ended)
        // Opened again after ending: afresh.
        let session = s.session
        s.open(computerID: "us_studio", name: "Studio")
        #expect(s.session == session + 1)
    }

    @Test func onlyOpenMinimisesAndOnlyMinimisedExpands() {
        var s = ScreenShareState()
        s.expand()
        #expect(s.phase == .ended)
        s.open(computerID: "us_studio", name: "Studio")
        s.expand()
        #expect(s.phase == .open)
    }

    @Test func fewFramesWhileMinimised() {
        #expect(ScreenShareState.detailFPS(minimised: true) <= 2)
        #expect(ScreenShareState.baseFPS(minimised: true) <= 2)
        #expect(ScreenShareState.detailFPS(minimised: false) == 60)
        #expect(ScreenShareState.baseFPS(minimised: false) == 15)
    }
}
