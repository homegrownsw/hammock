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

/// A diff's lines with their numbers in the old and new file, from its
/// "@@ -12 +14 @@" hunk headers (DiffView's gutter). Lines before any (an
/// edit not yet done, whose place isn't known) have none.
enum DiffLines {
    enum Kind: Equatable { case context, added, removed, hunk, file }

    struct Line: Equatable {
        var text: String
        var kind: Kind
        var old: Int?
        var new: Int?
    }

    static func numbered(_ diff: String) -> [Line] {
        var lines = diff.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        if lines.last?.isEmpty == true { lines.removeLast() }
        var o: Int?, n: Int?
        return lines.map { s in
            if s.hasPrefix("@@") {
                if let (a, b) = hunk(s) {
                    o = a
                    n = b
                    return Line(text: s, kind: .hunk)
                }
                // "@@ path": which file follows (a command's edits, Codex's patches).
                o = nil
                n = nil
                return Line(text: s, kind: .file)
            }
            if s.hasPrefix("+") {
                defer { n = n.map { $0 + 1 } }
                return Line(text: s, kind: .added, new: n)
            }
            if s.hasPrefix("-") {
                defer { o = o.map { $0 + 1 } }
                return Line(text: s, kind: .removed, old: o)
            }
            defer { o = o.map { $0 + 1 }; n = n.map { $0 + 1 } }
            return Line(text: s, kind: .context, old: o, new: n)
        }
    }

    /// "@@ -12,3 +14,4 @@ func x()" as (12, 14).
    static func hunk(_ s: String) -> (Int, Int)? {
        let parts = s.split(separator: " ")
        guard parts.count >= 3, parts[1].hasPrefix("-"), parts[2].hasPrefix("+"),
              let a = Int(parts[1].dropFirst().prefix { $0.isNumber }),
              let b = Int(parts[2].dropFirst().prefix { $0.isNumber }) else { return nil }
        return (a, b)
    }

    /// The files a diff's "@@ path" lines name, in order.
    static func files(_ lines: [Line]) -> [String] {
        lines.filter { $0.kind == .file && !$0.text.hasPrefix("@@ and ") }.map { String($0.text.dropFirst(3)) }
    }
}
