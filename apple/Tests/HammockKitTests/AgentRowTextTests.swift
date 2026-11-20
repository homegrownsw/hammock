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

@Suite struct ContextActionsTests {
    private func commands(_ h: String, _ c: [String] = []) -> [String] { AgentStatusBar.contextActions(harness: h, commands: c).map(\.command) }

    @Test func offersEachAgentsOwn() {
        #expect(commands("claude") == ["/compact", "/clear"])
        #expect(commands("codex", ["/clear"]) == ["/compact", "/new"])
        #expect(commands("gemini", ["/compress", "/clear"]) == ["/clear"])
        #expect(commands("droid") == [])
    }
}

@Suite struct ToolWordsTests {
    @Test func saysHowLongItsRun() {
        #expect(ToolWords.running(12) == "Running, 12 seconds")
        #expect(ToolWords.running(1) == "Running, 1 second")
        #expect(ToolWords.running(65) == "Running, 1 minute, 5 seconds")
        #expect(ToolWords.running(120) == "Running, 2 minutes")
        #expect(ToolWords.running(3720) == "Running, 1 hour, 2 minutes")
        #expect(ToolWords.running(nil) == "Running")
    }

    @Test func saysARowAsOne() {
        #expect(ToolWords.row(tool: "Bash", title: "Run the tests", input: "npm test\nmore", bang: false, pending: false, error: true,
                              added: 0, removed: 0, open: false) == "Bash: npm test, failed, collapsed")
        #expect(ToolWords.row(tool: "Edit", title: "Edited a.go", input: "a.go", bang: false, pending: false, error: false,
                              added: 3, removed: 1, open: true) == "Edit: a.go, 3 added, 1 removed, succeeded, expanded")
        #expect(ToolWords.row(tool: "mcp__hammock__list_panes", title: "Listed `panes`", input: "{}", bang: false, pending: true, error: false,
                              added: 0, removed: 0, open: nil) == "hammock list_panes: Listed panes, running")
        #expect(ToolWords.row(tool: "Bash", title: "", input: "ls", bang: true, pending: false, error: false,
                              added: 0, removed: 0, open: nil) == "Shell command: ls, succeeded")
    }
}

@Suite struct TokensLeftTests {
    @Test func saysWhatsLeft() {
        #expect(AgentStatusBar.tokensLeft(120_000) == "~120k tokens left")
        #expect(AgentStatusBar.tokensLeft(640) == "640 tokens left")
        #expect(AgentStatusBar.tokensLeft(-5) == "0 tokens left")
    }
}

@Suite struct RunCommandTests {
    @MainActor @Test func aControlsCommandLeavesTheSendErrorAlone() async throws {
        let m = AppModel(socketPath: "/nonexistent", clientKind: "test")
        let s = AgentSession(paneID: "p", model: m)
        s.sendError = "Not connected. Your message wasn't sent."
        s.runCommand("/compact")
        try await Task.sleep(for: .milliseconds(50))
        #expect(s.sendError == "Not connected. Your message wasn't sent.")
        #expect(m.toast?.contains("/compact") == true)
    }
}

@Suite struct DiffFolderTests {
    @Test func looksInTheFilesFolderElseTheAgents() {
        let there: (String) -> Bool = { $0 == "/Users/me/app/src" }
        #expect(DiffFolder.repo(for: "src/a.go", cwd: "/Users/me/app", exists: there) == "/Users/me/app/src")
        #expect(DiffFolder.repo(for: "gone/a.go", cwd: "/Users/me/app", exists: there) == "/Users/me/app")
        #expect(DiffFolder.repo(for: "", cwd: "/Users/me/app", exists: there) == "/Users/me/app")
        #expect(DiffFolder.repo(for: "/Users/me/app/src/b.go", cwd: "/x", exists: there) == "/Users/me/app/src")
    }
}

@Suite struct DiffLinesEdgeTests {
    @Test func aNoteNeitherNumberedNorCounted() {
        let l = DiffLines.numbered("@@ -5 +5 @@\n-old\n\\ No newline at end of file\n+new\n")
        #expect(l.map(\.kind) == [.hunk, .removed, .note, .added])
        #expect(l[2].old == nil && l[2].new == nil)
        #expect(l[3].new == 5)
    }

    @Test func splitsWindowsLineBreaks() {
        let l = DiffLines.numbered("@@ -1 +1 @@\r\n-a\r\n+b\r\n")
        #expect(l.map(\.text) == ["@@ -1 +1 @@", "-a", "+b"])
        #expect(l[2].new == 1)
    }
}

@Suite struct OtherAgentsToolsTests {
    private func tool(_ name: String, _ input: String = "") -> AgentBlock { .with { $0.kind = .tool; $0.tool = name; $0.input = input } }

    @Test func knowsCodexsAndLowercaseNames() {
        #expect(ToolSummary.words([tool("exec_command"), tool("exec"), tool("web_search"), tool("apply_patch", "a.go")]) == "Ran 2 commands · searched once · edited 1 file")
        #expect(ToolSummary.words([tool("bash"), tool("read", "a"), tool("edit", "a")]) == "Ran 1 command · read 1 file · edited 1 file")
        #expect(ToolWords.row(tool: "exec_command", title: "Ran `ls`", input: "ls", bang: false, pending: false, error: false, added: 0, removed: 0, open: nil) == "exec_command: ls, succeeded")
    }

    @Test func namesTheDiffsFile() {
        #expect(ToolSummary.diffPath(tool: "edit", input: "a.go", diff: "-x\n+y\n") == "a.go")
        #expect(ToolSummary.diffPath(tool: "apply_patch", input: "a.go\nb.go", diff: "@@ a.go\n+x\n") == "")
        #expect(ToolSummary.diffPath(tool: "str_replace_editor", input: "src/a.go", diff: "-x\n+y\n") == "src/a.go")
        #expect(ToolSummary.diffPath(tool: "Bash", input: "sed -i s/a/b/ x", diff: "@@ x\n-a\n+b\n") == "")
    }
}
