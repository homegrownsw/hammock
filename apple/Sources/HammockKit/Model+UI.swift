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
        return title.isEmpty ? (command.first.map { ($0 as NSString).lastPathComponent } ?? "shell") : title
    }

    /// The cwd with the home directory abbreviated.
    public var shortCwd: String {
        let home = NSHomeDirectory()
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
