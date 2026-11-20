import Foundation
import Testing
@testable import HammockKit

@Suite struct ToolSummaryTests {
    private func tool(_ name: String, _ input: String = "", error: Bool = false, pending: Bool = false) -> AgentBlock {
        .with { $0.kind = .tool; $0.tool = name; $0.input = input; $0.error = error; $0.pending = pending }
    }

    @Test func saysWhatTheToolsDid() {
        let tools = [tool("Read", "a.go"), tool("Read", "b.go"), tool("Bash", "go test"), tool("Read", "a.go"),
                     tool("Edit", "a.go"), tool("Bash", "go vet")]
        #expect(ToolSummary.words(tools) == "Read 2 files · ran 2 commands · edited 1 file")
    }

    @Test func keepsItShort() {
        let tools = [tool("Read", "a"), tool("Bash"), tool("Edit", "a"), tool("Grep"), tool("Grep"), tool("WebFetch")]
        #expect(ToolSummary.words(tools) == "Read 1 file · ran 1 command · edited 1 file · 3 more")
    }

    @Test func namesOtherTools() {
        #expect(ToolSummary.words([tool("Foo"), tool("Bar")]) == "Used 2 tools")
        #expect(ToolSummary.words([tool("Bash"), tool("Foo")]) == "Ran 1 command · used 1 other tool")
        #expect(ToolSummary.words([tool("mcp__hammock__list_panes"), tool("Grep")]) == "Used 1 MCP tool · searched once")
        #expect(ToolSummary.words([tool("TodoWrite"), tool("TodoWrite")]) == "Updated the tasks")
    }

    @Test func countsFailuresInWords() {
        #expect(ToolSummary.failed([tool("Bash"), tool("Bash", error: true)]) == "1 failed")
        #expect(ToolSummary.failed([tool("Bash", error: true), tool("Read", error: true)]) == "2 failed")
        #expect(ToolSummary.failed([tool("Bash")]) == nil)
    }
}
