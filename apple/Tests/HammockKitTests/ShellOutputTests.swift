import XCTest
@testable import HammockKit

final class ShellOutputTests: XCTestCase {
    func testRedrawnLinesKeepTheirLast() {
        XCTAssertEqual(AgentSession.appendOutput("build\n", "10%\r50%\r100%\ndone\n"), "build\n100%\ndone\n")
        // Redrawn across reads: the next part replaces the line, not joins it.
        let partway = AgentSession.appendOutput("", "10%\r")
        XCTAssertEqual(AgentSession.appendOutput(partway, "20%\n"), "20%\n")
        XCTAssertEqual(AgentSession.appendOutput("a\r", "\nb"), "a\nb")
    }

    func testKeepsTheEndFromALine() {
        let big = String(repeating: "x", count: 100) + "\n"
        let s = AgentSession.appendOutput(String(repeating: big, count: AgentSession.shellKept / big.count + 10), "last\n")
        XCTAssertLessThanOrEqual(s.utf8.count, AgentSession.shellKept)
        XCTAssertTrue(s.hasPrefix("x"))
        XCTAssertTrue(s.hasSuffix("last\n"))
    }
}
