import Testing
@testable import HammockKit

/// Pinching a phone terminal's font size.
struct TerminalFontSizeTests {
    @Test func pinchingPicksWholeSizesInRange() {
        #expect(TerminalFontSize.pinched(from: 11, scale: 1) == 11)
        #expect(TerminalFontSize.pinched(from: 11, scale: 1.3) == 14)
        #expect(TerminalFontSize.pinched(from: 11, scale: 0.8) == 9)
        #expect(TerminalFontSize.pinched(from: 11, scale: 5) == TerminalFontSize.range.upperBound)
        #expect(TerminalFontSize.pinched(from: 11, scale: 0.1) == TerminalFontSize.range.lowerBound)
        #expect(TerminalFontSize.pinched(from: 11, scale: .nan) == 11)
    }
}
