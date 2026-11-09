import SwiftUI
import XCTest
@testable import HammockKit

/// which icon a pane's program shows.
@MainActor
final class ProgramIconTests: XCTestCase {
    override func tearDown() {
        ProgramIcon.setOverrides([])
        ProgramIcon.setStyle(.plain)
    }

    func testTheFirstNameWithAnIconWins() {
        XCTAssertEqual(ProgramIcon.resolve(["vim"]), .path("vim"))
        XCTAssertEqual(ProgramIcon.resolve(["brew", "bash"]), .path("homebrew"))
        XCTAssertEqual(ProgramIcon.resolve(["vite", "npx", "node"]), .path("vite"))
        XCTAssertEqual(ProgramIcon.resolve(["manage", "python"]), .path("django"))
        XCTAssertEqual(ProgramIcon.resolve(["app", "python"]), .path("python"))
        XCTAssertEqual(ProgramIcon.resolve(["revdiff"]), .symbol("plus.forwardslash.minus"))
        XCTAssertEqual(ProgramIcon.resolve(["htop"]), .symbol("gauge.with.dots.needle.50percent"))
    }

    func testOffShowsTheTerminalButAgentsKeepTheirMark() {
        ProgramIcon.setStyle(.off)
        XCTAssertEqual(ProgramIcon.resolve(["brew", "bash"]), .symbol("apple.terminal"))
        XCTAssertEqual(ProgramIcon.resolve(["claude", "node"]), .agent("claude"))
    }

    func testStyleFromConfig() {
        XCTAssertEqual(ProgramIconStyle(config: nil), .plain)
        XCTAssertEqual(ProgramIconStyle(config: "false"), .off)
        XCTAssertEqual(ProgramIconStyle(config: "Colourful"), .colorful)
    }

    func testColorfulIconsKnowTheirColours() {
        XCTAssertEqual(ProgramIcon.resolveIcon(["brew", "bash"], style: .colorful).key, "homebrew")
        XCTAssertEqual(ProgramIcon.resolveIcon(["zsh"], style: .colorful).key, "terminal")
        ProgramIcon.setOverrides(["frobnicate=git"])
        XCTAssertEqual(ProgramIcon.resolveIcon(["frobnicate"], style: .colorful).key, "git")
    }

    func testEveryIconHasColours() {
        for key in ProgramIcon.builtInGlyphs.keys {
            XCTAssertNotNil(ProgramIcon.builtInColors[key], key)
        }
    }

    func testAgentsShowTheirMark() {
        XCTAssertEqual(ProgramIcon.resolve(["claude"]), .agent("claude"))
        XCTAssertEqual(ProgramIcon.resolve(["gemini"]), .agent("gemini"))
    }

    func testShellsAndUnknownProgramsShowTheTerminal() {
        XCTAssertEqual(ProgramIcon.resolve([]), .symbol("apple.terminal"))
        XCTAssertEqual(ProgramIcon.resolve(["zsh"]), .symbol("apple.terminal"))
        XCTAssertEqual(ProgramIcon.resolve(["frobnicate"]), .symbol("apple.terminal"))
    }

    func testOverridesTakeABuiltInIconOrASymbol() {
        ProgramIcon.setOverrides(["frobnicate=git, HTOP = cpu", "zsh=not-a-symbol-at-all"])
        XCTAssertEqual(ProgramIcon.resolve(["frobnicate"]), .path("git"))
        XCTAssertEqual(ProgramIcon.resolve(["htop"]), .symbol("cpu"))
        // Neither an icon nor a symbol: the built-in choice stands.
        XCTAssertEqual(ProgramIcon.resolve(["zsh"]), .symbol("apple.terminal"))
    }

    func testEveryBuiltInIconDraws() {
        for (key, glyph) in ProgramIcon.builtInGlyphs {
            switch glyph {
            case .path:
                let b = ProgramIcon.paths[key]?.boundingRect ?? .zero
                XCTAssertGreaterThan(b.width, 4, key)
                XCTAssertLessThanOrEqual(b.maxX, 24.5, key)
                XCTAssertLessThanOrEqual(b.maxY, 24.5, key)
            case .symbol(let name):
                XCTAssertNotNil(NSImage(systemSymbolName: name, accessibilityDescription: nil), name)
            case .agent:
                XCTFail("no built-in agent glyphs: \(key)")
            }
        }
        for (program, key) in ProgramIcon.builtInPrograms {
            XCTAssertNotNil(ProgramIcon.builtInGlyphs[key], program)
        }
    }
}
