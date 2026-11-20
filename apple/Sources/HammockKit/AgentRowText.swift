import Foundation

/// What a run of tool calls did, in words: "Read 4 files · ran 2 commands ·
/// edited 1 file" (ToolGroup's line).
enum ToolSummary {
    enum Kind: Equatable {
        case read, edit, write, run, search, fetch, tasks, skill, mcp, other

        /// By name, whoever's: Claude's ("Bash", "Read"), Codex's
        /// ("exec_command", "apply_patch", "web_search"), and the other
        /// agents' lowercase or snake_case spellings, ignoring case, "_" and "-".
        init(tool: String) {
            if tool.hasPrefix("mcp__") { self = .mcp; return }
            switch tool.lowercased().replacingOccurrences(of: "_", with: "").replacingOccurrences(of: "-", with: "") {
            case "read", "readfile", "view", "viewimage", "cat": self = .read
            case "edit", "multiedit", "notebookedit", "editnotebook", "strreplace", "searchreplace", "editfile", "applypatch", "patch": self = .edit
            case "write", "createfile", "writefile": self = .write
            case "bash", "shell", "exec", "execcommand", "localshell", "shellcommand", "runterminalcmd", "terminal", "writestdin": self = .run
            case "grep", "glob", "websearch", "search", "find", "rg", "ripgrep", "grepsearch", "globfilesearch", "codebasesearch", "semanticsearch": self = .search
            case "webfetch", "fetch": self = .fetch
            case "todowrite", "taskcreate", "taskupdate", "updateplan", "plan": self = .tasks
            case "skill": self = .skill
            default: self = .other
            }
        }

        /// Its input says best what it did (a command, a path, a pattern).
        var saysInput: Bool { [.read, .edit, .write, .run, .search, .fetch].contains(self) }

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
    enum Kind: Equatable { case context, added, removed, hunk, file, note }

    struct Line: Equatable {
        var text: String
        var kind: Kind
        var old: Int?
        var new: Int?
    }

    static func numbered(_ diff: String) -> [Line] {
        // "\n", "\r\n" (one Character) or a lone "\r": not the other
        // characters isNewline counts (form feed, U+2028…), which are text.
        var lines = diff.split(omittingEmptySubsequences: false) { $0 == "\n" || $0 == "\r\n" || $0 == "\r" }.map(String.init)
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
            // "\ No newline at end of file": about the line before, not one of the file's.
            if s.hasPrefix("\\") { return Line(text: s, kind: .note) }
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

/// What VoiceOver says for a tool call's row and how it went.
enum ToolWords {
    static let succeeded = "Succeeded"
    static let failed = "Failed"

    /// "Running, 12 seconds" (just "Running" without a start).
    static func running(_ seconds: TimeInterval?) -> String {
        guard let seconds else { return "Running" }
        return "Running, " + duration(seconds)
    }

    /// "12 seconds", "1 minute, 5 seconds", "2 hours, 3 minutes".
    static func duration(_ seconds: TimeInterval) -> String {
        let s = max(Int(seconds), 0)
        func unit(_ n: Int, _ name: String) -> String { "\(n) \(name)\(n == 1 ? "" : "s")" }
        if s < 60 { return unit(s, "second") }
        if s < 3600 { return unit(s / 60, "minute") + (s % 60 == 0 ? "" : ", " + unit(s % 60, "second")) }
        return unit(s / 3600, "hour") + ((s % 3600) / 60 == 0 ? "" : ", " + unit((s % 3600) / 60, "minute"))
    }

    /// The tool as said: Claude's names as they are, an MCP tool's without
    /// its "mcp__" ("hammock list_panes").
    static func toolName(_ tool: String) -> String {
        guard tool.hasPrefix("mcp__") else { return tool }
        return tool.dropFirst(5).replacingOccurrences(of: "__", with: " ")
    }


    /// A tool row as one: "Bash: npm test, failed, collapsed".
    static func row(tool: String, title: String, input: String, bang: Bool, pending: Bool, error: Bool,
                    added: Int, removed: Int, open: Bool?) -> String {
        let first = input.split(separator: "\n", maxSplits: 1).first.map(String.init) ?? ""
        var what: String
        if bang {
            what = "Shell command: " + first
        } else if ToolSummary.Kind(tool: tool).saysInput, !first.isEmpty {
            what = toolName(tool) + ": " + first
        } else {
            let plain = title.replacingOccurrences(of: "`", with: "").replacingOccurrences(of: "**", with: "")
            what = plain.isEmpty ? toolName(tool) : toolName(tool) + ": " + plain
        }
        var parts = [what]
        if added > 0 || removed > 0 { parts.append("\(added) added, \(removed) removed") }
        parts.append(pending ? "running" : error ? "failed" : "succeeded")
        if let open { parts.append(open ? "expanded" : "collapsed") }
        return parts.joined(separator: ", ")
    }
}

/// Where Open in Diff Pane looks for a repository: the changed file's
/// folder (relative to the agent's, or in full), or the agent's own folder
/// when there's no file or its folder's gone.
public enum DiffFolder {
    public static func repo(for path: String, cwd: String, exists: (String) -> Bool) -> String {
        guard !path.isEmpty else { return cwd }
        let full = path.hasPrefix("/") || cwd.isEmpty ? path : (cwd as NSString).appendingPathComponent(path)
        let folder = (full as NSString).deletingLastPathComponent
        return !folder.isEmpty && exists(folder) ? folder : cwd
    }
}

extension ToolSummary {
    /// The file an edit's diff is of, for its header: an edit's or a write's
    /// input; another tool's when its diff names no file of its own ("@@
    /// path") and its input is one path. "" when there's none to name.
    static func diffPath(tool: String, input: String, diff: String) -> String {
        let single = !input.isEmpty && !input.contains("\n")
        let k = Kind(tool: tool)
        if k == .edit || k == .write, single { return input }
        guard single, !input.contains(" "), DiffLines.files(DiffLines.numbered(diff)).isEmpty else { return "" }
        return input
    }
}
