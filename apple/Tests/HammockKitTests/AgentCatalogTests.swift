import XCTest
@testable import HammockKit

final class AgentCatalogTests: XCTestCase {
    private func kind(_ id: String, name: String = "", short: String = "", canSwitch: Bool = false) -> Hammock_V1_AgentKind {
        .with { $0.id = id; $0.name = name; $0.shortName = short; $0.canSwitch = canSwitch }
    }

    func testAnOlderDaemonKnowsClaudeAndCodex() {
        let k = AgentCatalog.resolve([])
        XCTAssertEqual(k.map(\.id), ["claude", "codex"])
        XCTAssertEqual(k.map(\.name), ["Claude Code", "Codex"])
        XCTAssertEqual(k.map(\.shortName), ["Claude", "Codex"])
        for a in k {
            XCTAssertTrue(a.installed && a.canResume && a.agentView && a.canSwitch && a.schedulable && a.usage && a.hooks && a.sessions)
        }
    }

    func testTheDaemonsListReplacesIt() {
        let sent = [kind("gemini", name: "Gemini CLI", short: "Gemini")]
        XCTAssertEqual(AgentCatalog.resolve(sent).map(\.id), ["gemini"])
    }

    func testNames() {
        let kinds = [kind("claude", name: "Claude Code", short: "Claude"), kind("new", name: "Newcomer")]
        XCTAssertEqual(AgentCatalog.name(of: "claude", in: kinds), "Claude Code")
        XCTAssertEqual(AgentCatalog.shortName(of: "claude", in: kinds), "Claude")
        // Not in the catalogue, but known by name.
        XCTAssertEqual(AgentCatalog.name(of: "gemini", in: kinds), "Gemini CLI")
        XCTAssertEqual(AgentCatalog.shortName(of: "gemini", in: kinds), "Gemini")
        XCTAssertEqual(AgentCatalog.shortName(of: "copilot", in: []), "Copilot")
        XCTAssertEqual(AgentCatalog.name(of: "droid", in: []), "Factory Droid")
        // No short name sent: its name.
        XCTAssertEqual(AgentCatalog.shortName(of: "new", in: kinds), "Newcomer")
        // Unknown altogether: its id; none at all: "Agent".
        XCTAssertEqual(AgentCatalog.name(of: "zed", in: kinds), "zed")
        XCTAssertEqual(AgentCatalog.shortName(of: "", in: kinds), "Agent")
    }

    func testOther() {
        let kinds = AgentCatalog.resolve([]) + [kind("gemini", canSwitch: true), kind("aider")]
        XCTAssertEqual(AgentCatalog.other(of: "claude", in: kinds), "codex")
        XCTAssertEqual(AgentCatalog.other(of: "codex", in: kinds), "claude")
        XCTAssertEqual(AgentCatalog.other(of: "gemini", in: kinds), "claude")
        XCTAssertNil(AgentCatalog.other(of: "aider", in: kinds)) // can't switch
        XCTAssertNil(AgentCatalog.other(of: "unknown", in: kinds))
        XCTAssertNil(AgentCatalog.other(of: "claude", in: [kind("claude", canSwitch: true)])) // nowhere to go
    }

    @MainActor func testSwitchMenusKeepClaudeLast() {
        defer { AgentCatalog.shared.received = [] }
        XCTAssertEqual(AgentCatalog.switchable.map(\.id), ["codex", "claude"])
        AgentCatalog.shared.received = AgentCatalog.resolve([]) + [kind("gemini", canSwitch: true)]
        XCTAssertEqual(AgentCatalog.switchable.map(\.id), ["codex", "gemini", "claude"])
    }
}
