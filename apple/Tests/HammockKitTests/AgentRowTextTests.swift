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

@Suite struct DiffLinesTests {
    @Test func numbersLinesFromHunks() {
        let l = DiffLines.numbered("@@ -12 +14 @@\n keep\n-old\n+new\n+more\n tail\n")
        #expect(l.map(\.kind) == [.hunk, .context, .removed, .added, .added, .context])
        #expect(l.map(\.old) == [nil, 12, 13, nil, nil, 14])
        #expect(l.map(\.new) == [nil, 14, nil, 15, 16, 17])
    }

    @Test func readsCountsAndHeaders() {
        #expect(DiffLines.hunk("@@ -3,2 +3,4 @@ func x()")! == (3, 3))
        #expect(DiffLines.hunk("@@ a.go") == nil)
        let l = DiffLines.numbered("@@ a.go\n@@ -1 +1 @@\n-x\n+y\n@@ b.go\n+z\n@@ and 2 more files\n")
        #expect(DiffLines.files(l) == ["a.go", "b.go"])
        #expect(l[5].new == nil)  // a new file's header: its numbers aren't known
        #expect(l[3].new == 1)
    }

    @Test func noHunksNoNumbers() {
        let l = DiffLines.numbered("-a\n+b\n")
        #expect(l.allSatisfy { $0.old == nil && $0.new == nil })
    }
}
