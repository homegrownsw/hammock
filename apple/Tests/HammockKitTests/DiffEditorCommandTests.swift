import XCTest
@testable import HammockKit

final class DiffEditorCommandTests: XCTestCase {
    private let file = "/Users/me/Developer/app/Sources/a b.swift"

    func testTerminalEditorsGetTheLineTheirWay() {
        XCTAssertEqual(DiffEditorCommand.commandLine("nvim", file: file, line: 12).line, "nvim '+12' '\(file)'")
        XCTAssertEqual(DiffEditorCommand.commandLine("/opt/homebrew/bin/hx", file: file, line: 3).line, "/opt/homebrew/bin/hx '\(file):3'")
        XCTAssertFalse(DiffEditorCommand.commandLine("vim", file: file, line: 1).windowed)
    }

    func testAnEditorNotKnownGetsTheFileAlone() {
        // +N could be taken for a file to make.
        XCTAssertEqual(DiffEditorCommand.commandLine("ed", file: file, line: 9).line, "ed '\(file)'")
    }

    func testWindowedEditorsWaitAndRunWithoutAPane() {
        let code = DiffEditorCommand.commandLine("code", file: file, line: 7)
        XCTAssertTrue(code.windowed)
        XCTAssertEqual(code.line, "code --wait '--goto' '\(file):7'")
        // Their own wait flag, once.
        XCTAssertEqual(DiffEditorCommand.commandLine("zed --wait", file: file, line: 2).line, "zed --wait '\(file):2'")
        XCTAssertEqual(DiffEditorCommand.commandLine("mate", file: file, line: 4).line, "mate -w '-l' '4' '\(file)'")
    }

    func testACommandWithPlaceholders() {
        let c = DiffEditorCommand.commandLine("emacsclient -t +{line} {file}", file: file, line: 40)
        XCTAssertEqual(c.line, "emacsclient -t +40 '\(file)'")
        XCTAssertFalse(c.windowed)
    }

    func testQuotingAFileWithAQuote() {
        XCTAssertEqual(DiffEditorCommand.quote("it's"), "'it'\\''s'")
    }

    func testTheLineIsNeverBelowOne() {
        XCTAssertEqual(DiffEditorCommand.commandLine("vim", file: "f", line: 0).line, "vim '+1' 'f'")
    }
}
