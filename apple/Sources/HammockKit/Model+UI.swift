import Foundation

extension Hammock_V1_Group: Identifiable {}
extension Hammock_V1_Tab: Identifiable {}
extension Hammock_V1_Pane: Identifiable {}
extension Hammock_V1_AttentionItem: Identifiable {}
extension Hammock_V1_GrantRequest: Identifiable {}

extension Hammock_V1_Pane {
    /// Display name for the pane's process: the agent harness if known, else the title.
    public var displayName: String {
        if kind == .editor { return editor.name.isEmpty ? "Untitled" : editor.name }
        if hasAgent, !agent.harness.isEmpty { return agent.harness }
        // A program reporting its status (OSC 7501): its label for itself,
        // else its own name, else what's running.
        if isReportingProgram, let name = [agent.title, agent.app, program.first ?? ""].first(where: { !$0.isEmpty }) {
            return name
        }
        return title.isEmpty ? (command.first.map { ($0 as NSString).lastPathComponent } ?? "shell") : title
    }

    /// How far along its agent (or program) is, 0–100, while working or
    /// waiting on you, when it says (OSC 7501).
    public var agentProgress: Int? {
        hasAgent && agent.hasProgress && [.working, .needsInput].contains(agent.state) ? Int(agent.progress) : nil
    }

    /// Its "agent" is another program (brew, a deploy script) reporting what
    /// it's doing with the Program Status Protocol (OSC 7501), not an agent.
    public var isReportingProgram: Bool { hasAgent && agent.reported && agent.harness.isEmpty }

    /// Where the pane's agent session opens on the web (Claude Code's
    /// Remote Control link), when it has one.
    public var sessionURL: URL? {
        guard hasAgent, agent.sessionURL.hasPrefix("https://") else { return nil }
        return URL(string: agent.sessionURL)
    }

    /// Its agent could be put to sleep (SleepPane): a terminal's, at rest
    /// (finished, or ended its turn on an error), not asleep already. The
    /// daemon has the last word (no shells or subagents, a known session),
    /// and says why not.
    public var canSleep: Bool {
        (kind == .terminal || kind == .unspecified) && hasAgent && !exited && !asleep
            && (agent.state == .idle || agent.state == .errored) && !agent.runningCommand
    }

    /// Its agent's state in words: "Asleep" for one put to sleep.
    public var agentStatusLabel: String {
        asleep ? "Asleep" : agent.state.label
    }

    /// The cwd with the home directory abbreviated. HMK_DEBUG_HOME stands in
    /// for the home directory, for the README's demo (`scripts/readme-demo`),
    /// whose made-up projects live in a scratch dir.
    public var shortCwd: String {
        let home = ProcessInfo.processInfo.environment["HMK_DEBUG_HOME"] ?? NSHomeDirectory()
        return cwd.hasPrefix(home) ? "~" + cwd.dropFirst(home.count) : cwd
    }
}

extension Hammock_V1_GrantRequest {
    /// What's asked, in Hammock's words (from the action and the panes); an
    /// older daemon sends only the agent's own description.
    public var headlineText: String {
        headline.isEmpty ? "An agent wants to \(description_p)" : headline
    }

    /// The agent's own words about it, to show quoted beneath the headline
    /// (nil when there are none, or they're all an older daemon sent).
    public var agentNote: String? {
        headline.isEmpty || description_p.isEmpty ? nil : description_p
    }

    /// Where a request to do something only you may do came from ("From
    /// the shell in Web ▸ api", "From hmk run by zsh on ttys004, outside
    /// Hammock"), to show beneath the headline; nil for an agent's.
    public var originText: String? {
        person && !origin.isEmpty ? origin : nil
    }

    /// Whether "Always" may be offered: an agent's requests can be
    /// remembered for its Group; what only you may do is allowed once.
    public var canAllowAlways: Bool { !person }

    /// Whether to show all of the headline: a schedule (all of what you'd
    /// allow), and what only you may do.
    public var showsWholeHeadline: Bool { action == "schedule" || person }
}

/// A user key's short fingerprint, as a join card shows it: the user id's
/// hex in fours ("3f2a 91c0 7be4 1d05").
public func keyFingerprint(userID: String) -> String {
    var hex = Substring(userID.hasPrefix("us_") ? String(userID.dropFirst(3)) : userID)
    var parts: [Substring] = []
    while hex.count > 4 { parts.append(hex.prefix(4)); hex = hex.dropFirst(4) }
    return (parts + [hex]).joined(separator: " ")
}

extension Hammock_V1_JoinRequest {
    /// What to beware of in the guest's key, for the join card: a name
    /// that's joined before with another key, or a key never seen.
    public func keyWarning(name: String?) -> String? {
        if unlinkedUnixMs > 0 {
            let on = Date(timeIntervalSince1970: Double(unlinkedUnixMs) / 1000).formatted(date: .abbreviated, time: .omitted)
            return "A computer you unlinked from your computers, on \(on)"
        }
        if nameHasOtherKey { return "\(name ?? "This person") usually joins with a different key" }
        if newKey { return "First time this key has asked to join" }
        return nil
    }
}

extension Hammock_V1_AgentState {
    public var label: String {
        switch self {
        case .needsInput: return "Waiting for you"
        case .working: return "Working"
        case .idle: return "Finished"
        case .errored: return "Errored"
        case .starting: return "Starting"
        case .exited: return "Exited"
        default: return ""
        }
    }
}

/// "3m", "1h 4m" style elapsed time.
public func elapsedString(sinceUnixMs ms: Int64, now: Date = Date()) -> String {
    guard ms > 0 else { return "" }
    let s = max(0, Int(now.timeIntervalSince1970 - Double(ms) / 1000))
    if s < 60 { return "\(s)s" }
    if s < 3600 { return "\(s / 60)m" }
    return "\(s / 3600)h \(s % 3600 / 60)m"
}

extension Hammock_V1_Layout {
    /// A stable identity for a split child: its first pane's id.
    public var stableKey: String {
        switch node {
        case .paneID(let id): return id
        case .split(let s): return s.children.first?.stableKey ?? "empty"
        case nil: return "empty"
        }
    }
}

extension Hammock_V1_Agent {
    /// The state to show: a command you ran with ! (after the agent went
    /// idle) shows as work, as it is.
    public var shownState: AgentState { runningCommand && state == .idle ? .working : state }

    /// At rest with work still going on (shells: background tasks, a
    /// server; subagents that run on after its turn): shown a little
    /// differently from plain rest.
    public var restingWithWork: Bool { state == .idle && !runningCommand && (shells > 0 || subagents > 0) }
}
