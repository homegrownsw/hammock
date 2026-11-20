import Foundation

/// What a run of tool calls did, in words: "Read 4 files · ran 2 commands ·
/// edited 1 file" (ToolGroup's line).
enum ToolSummary {
    enum Kind: Equatable {
        case read, edit, write, run, search, fetch, tasks, skill, mcp, other

        init(tool: String) {
            switch tool {
            case "Read": self = .read
            case "Edit", "MultiEdit", "NotebookEdit": self = .edit
            case "Write": self = .write
            case "Bash": self = .run
            case "Grep", "Glob", "WebSearch": self = .search
            case "WebFetch": self = .fetch
            case "TodoWrite", "TaskCreate", "TaskUpdate": self = .tasks
            case "Skill": self = .skill
            default: self = tool.hasPrefix("mcp__") ? .mcp : .other
            }
        }

        /// Files are counted once each (read twice, it's one file).
        var byFile: Bool { [.read, .edit, .write].contains(self) }

        func words(_ n: Int, alone: Bool) -> String {
            func n_(_ one: String, _ many: String) -> String { "\(n) " + (n == 1 ? one : many) }
            switch self {
            case .read: return "read " + n_("file", "files")
            case .edit: return "edited " + n_("file", "files")
            case .write: return "wrote " + n_("file", "files")
            case .run: return "ran " + n_("command", "commands")
            case .search: return n == 1 ? "searched once" : "searched \(n) times"
            case .fetch: return "fetched " + n_("page", "pages")
            case .tasks: return "updated the tasks"
            case .skill: return "used " + n_("skill", "skills")
            case .mcp: return "used " + n_("MCP tool", "MCP tools")
            case .other: return "used " + n_(alone ? "tool" : "other tool", alone ? "tools" : "other tools")
            }
        }
    }

    /// The line, with at most `parts` kinds (the rest as "2 more"), each in
    /// the order it first came, the first word capitalised.
    static func words(_ tools: [AgentBlock], parts: Int = 3) -> String {
        var order: [Kind] = []
        var counts: [Kind: Int] = [:]
        var files: [Kind: Set<String>] = [:]
        for t in tools {
            let k = Kind(tool: t.tool)
            if counts[k] == nil { order.append(k) }
            if k.byFile, !t.input.isEmpty {
                if files[k, default: []].insert(t.input).inserted { counts[k, default: 0] += 1 }
            } else {
                counts[k, default: 0] += 1
            }
        }
        var said = order.prefix(parts).map { $0.words(counts[$0] ?? 0, alone: order.count == 1) }
        let rest = order.dropFirst(parts).reduce(0) { $0 + (counts[$1] ?? 0) }
        if rest > 0 { said.append("\(rest) more") }
        guard let first = said.first else { return "Used no tools" }
        said[0] = first.prefix(1).uppercased() + first.dropFirst()
        return said.joined(separator: " · ")
    }

    /// "1 failed", "2 failed", or nil when none did.
    static func failed(_ tools: [AgentBlock]) -> String? {
        let n = tools.filter { $0.error && !$0.pending }.count
        return n == 0 ? nil : "\(n) failed"
    }
}
