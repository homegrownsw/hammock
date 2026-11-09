import Foundation
import Testing
@testable import HammockKit

@Test func framePerformance() {
    let t = TerminalEngine(cols: 220, rows: 60)
    var s = ""
    for row in 0..<60 {
        for col in 0..<220 { s += "\u{1b}[38;5;\((row + col) % 256)m\(Character(UnicodeScalar(65 + (col % 26))!))" }
        s += "\r\n"
    }
    t.write(Data(s.utf8))
    let start = Date()
    let n = 50
    for _ in 0..<n { _ = t.frame() }
    let ms = Date().timeIntervalSince(start) * 1000 / Double(n)
    print("frame() 220x60 styled: \(String(format: "%.2f", ms)) ms")
    #expect(ms < 16)
}
