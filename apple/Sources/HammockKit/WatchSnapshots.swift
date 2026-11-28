import Foundation

/// The watch's values (WatchWire.swift) made from a Mac's state: what the
/// iPhone sends its watch.
@MainActor
public enum WatchSnapshots {
    /// The Mac's agent sessions: each tab with an agent, by its most
    /// pressing pane (as the iPhone's Sessions list has them).
    public static func sessions(_ model: AppModel, host: String, hostName: String) -> [WatchSession] {
        let rank: [AgentState: Int] = [.needsInput: 0, .errored: 1, .working: 2, .starting: 2, .idle: 3]
        var out: [WatchSession] = []
        for group in model.groups {
            for tab in model.tabs(in: group) {
                let agents = model.panes(in: tab).filter { $0.hasAgent && !$0.exited && rank[$0.agent.shownState] != nil }
                guard let p = agents.min(by: { rank[$0.agent.shownState]! < rank[$1.agent.shownState]! }) else { continue }
                let a = p.agent
                let state = a.shownState
                out.append(WatchSession(
                    host: host, hostName: hostName, paneID: p.id, title: model.title(of: tab), harness: a.harness,
                    state: watchState(state), resting: state == .idle && a.seen,
                    summary: a.summary.isEmpty ? p.agentStatusLabel : a.summary, sinceMs: a.stateSinceUnixMs,
                    progress: p.agentProgress, asleep: p.asleep, waitingOnLimit: a.waitingOnLimit,
                    group: model.title(of: group)))
            }
        }
        return out
    }

    /// One pane's agent as a session (a conversation's header).
    public static func session(_ model: AppModel, pane: String, host: String, hostName: String) -> WatchSession? {
        guard let p = model.panes[pane], p.hasAgent else { return nil }
        let tab = model.tab(of: p)
        let a = p.agent
        return WatchSession(host: host, hostName: hostName, paneID: p.id, title: tab.map { model.title(of: $0) } ?? p.displayName,
                            harness: a.harness, state: watchState(a.shownState), resting: a.shownState == .idle && a.seen,
                            summary: a.summary.isEmpty ? p.agentStatusLabel : a.summary, sinceMs: a.stateSinceUnixMs,
                            progress: p.agentProgress, asleep: p.asleep, waitingOnLimit: a.waitingOnLimit,
                            group: tab.flatMap { model.group(of: $0) }.map { model.title(of: $0) } ?? "")
    }

    /// The agents' usage limits, as chosen in the phone's Settings.
    public static func usage(_ model: AppModel) -> [WatchUsage] {
        UsageLimitsPrefs.shown(model).map { u in
            WatchUsage(harness: u.harness, name: AgentCatalog.shortName(of: u.harness), windows: u.windows.map {
                .init(label: $0.label, usedPercent: $0.usedPercent, resetsAt: $0.resetsUnix > 0 ? Date(timeIntervalSince1970: TimeInterval($0.resetsUnix)) : nil)
            })
        }
    }

    /// The next few scheduled tasks.
    public static func schedules(_ model: AppModel, host: String, hostName: String, limit: Int = 3) -> [WatchSchedule] {
        model.sortedSchedules.filter(\.upcoming).prefix(limit).map {
            WatchSchedule(host: host, hostName: hostName, scheduleID: $0.id, name: $0.name,
                          nextRun: Date(timeIntervalSince1970: TimeInterval($0.nextRunUnixMs) / 1000))
        }
    }

    public static func watchState(_ s: AgentState) -> WatchAgentState {
        switch s {
        case .starting: .starting
        case .working: .working
        case .needsInput: .needsInput
        case .idle: .idle
        case .errored: .errored
        case .exited: .exited
        default: .unspecified
        }
    }

    /// The prompt on the agent's screen, if it's waiting on one.
    public static func prompt(_ p: AgentPrompt) -> WatchPrompt? {
        let kind: WatchPrompt.Kind
        switch p.kind {
        case .question: kind = .question
        case .permission: kind = .permission
        case .plan: kind = .plan
        default: return nil
        }
        guard !p.options.isEmpty else { return nil }
        return WatchPrompt(kind: kind, promptID: p.promptID, title: p.title, body: p.body, options: p.options,
                           descriptions: p.descriptions, multiSelect: p.multiSelect, checkboxes: Int(p.checkboxes), other: Int(p.other))
    }

    /// An agent's conversation for the watch: its last `limit` blocks.
    public static func conversation(_ s: AgentSession, session: WatchSession?, limit: Int, quickReplies: [String]) -> WatchConversation {
        let all = condense(s.blocks)
        let shown = Array(all.suffix(limit))
        var prompt = prompt(s.prompt)
        // The question tool's header ("Database"), from its call.
        if var p = prompt, p.kind == .question, let q = s.blocks.last(where: { $0.kind == .question })?.questions.first(where: { $0.question == p.title }) {
            p.header = q.header
            prompt = p
        }
        let lastText = s.blocks.last(where: { $0.kind == .text || $0.kind == .user }).flatMap { $0.kind == .text ? $0.text : nil } ?? ""
        return WatchConversation(session: session, blocks: shown, prompt: prompt, working: s.working,
                                 hasMore: all.count > shown.count || s.hasMore, canSendNow: s.canSendNow,
                                 suggestions: prompt == nil ? WatchSuggestions.make(lastText: lastText, quickReplies: quickReplies) : [])
    }

    /// The blocks a wrist reads: your messages, the agent's text, pictures,
    /// each run of tool calls as one line, questions with your answers, and
    /// where each turn finished. Thinking, plans, subagents and notes go.
    nonisolated public static func condense(_ blocks: [AgentBlock]) -> [WatchBlock] {
        var out: [WatchBlock] = []
        var run = ToolRun()
        func flush() {
            if let line = run.line, let id = run.firstID {
                out.append(WatchBlock(id: "tools-" + id, kind: .tools, text: line))
            }
            out += run.images
            run = ToolRun()
        }
        for b in blocks {
            switch b.kind {
            case .tool, .subagent:
                run.add(b)
            case .user:
                flush()
                if !b.text.isEmpty { out.append(WatchBlock(id: b.id, kind: .user, text: clip(b.text))) }
                out += images(b)
            case .text:
                flush()
                if !b.text.isEmpty { out.append(WatchBlock(id: b.id, kind: .text, text: clip(b.text))) }
                out += images(b)
            case .question:
                flush()
                for (i, q) in b.questions.enumerated() where !q.answer.isEmpty {
                    out.append(WatchBlock(id: "\(b.id)-q\(i)", kind: .question, text: q.question, answer: q.answer))
                }
            case .done:
                flush()
                out.append(WatchBlock(id: b.id, kind: .done, durationMs: b.durationMs))
            default:
                continue // thinking, plans, notes
            }
        }
        flush()
        return out
    }

    nonisolated static func images(_ b: AgentBlock) -> [WatchBlock] {
        b.images.enumerated().map { i, img in WatchBlock(id: "\(b.id)-img\(i)", kind: .image, ref: img.ref) }
    }

    /// Long text is cut, and says so: the watch is for glancing.
    nonisolated static func clip(_ s: String, max: Int = 1500) -> String {
        s.count <= max ? s : String(s.prefix(max)).trimmingCharacters(in: .whitespacesAndNewlines) + "…"
    }

    /// A run of tool calls, counted for its one line.
    struct ToolRun {
        var firstID: String?
        var commands = 0, edits = 0, reads = 0, searches = 0, subagents = 0, other = 0
        var images: [WatchBlock] = []

        mutating func add(_ b: AgentBlock) {
            if firstID == nil { firstID = b.id }
            images += WatchSnapshots.images(b)
            if b.kind == .subagent { subagents += 1; return }
            switch Self.kind(of: b.tool) {
            case .command: commands += 1
            case .edit: edits += 1
            case .read: reads += 1
            case .search: searches += 1
            case .other: other += 1
            }
        }

        enum Kind { case command, edit, read, search, other }

        static func kind(of tool: String) -> Kind {
            let t = tool.lowercased()
            if ["bash", "shell", "exec_command", "local_shell", "run_shell_command", "command"].contains(t) || t.hasSuffix("bash") { return .command }
            if t.contains("edit") || t.contains("write") || t.contains("patch") || t == "replace" { return .edit }
            if t == "read" || t.hasPrefix("read") || t == "view" || t == "cat" { return .read }
            if t.contains("grep") || t.contains("glob") || t.contains("search") || t == "ls" || t.hasPrefix("list") || t == "find" { return .search }
            return .other
        }

        /// "Ran 4 commands, edited 2 files"; nil for an empty run.
        var line: String? {
            func n(_ c: Int, _ one: String, _ many: String) -> String? { c == 0 ? nil : "\(c) \(c == 1 ? one : many)" }
            var parts: [String] = []
            if let s = n(commands, "command", "commands") { parts.append("ran " + s) }
            if let s = n(edits, "file", "files") { parts.append("edited " + s) }
            if let s = n(reads, "file", "files") { parts.append("read " + s) }
            if let s = n(searches, "search", "searches") { parts.append("ran " + s) }
            if let s = n(subagents, "subagent", "subagents") { parts.append("ran " + s) }
            if let s = n(other, "tool", "tools") { parts.append("used " + s) }
            guard !parts.isEmpty else { return nil }
            let s = parts.joined(separator: ", ")
            return s.prefix(1).uppercased() + s.dropFirst()
        }
    }
}

extension AppModel {
    /// The quick replies the watch offers (the Mac's quick-reply key, else
    /// its defaults); set: replaces them all (none: back to the defaults).
    public func quickReplies(setting replies: [String]? = nil) async throws -> [String] {
        guard let conn = connection else { throw DaemonError.closed }
        let r = try await conn.call {
            $0.quickReplyCommand = .with {
                if let replies {
                    $0.set = true
                    $0.replies = replies
                }
            }
        }
        return r.quickReplies.replies
    }
}
