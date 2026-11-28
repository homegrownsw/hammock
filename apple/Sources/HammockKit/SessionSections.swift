import Foundation

/// A tab with an agent, represented by its most pressing agent pane: one
/// row of Sessions (the phones' tab, the Mac sidebar's view).
public struct SessionEntry: Identifiable {
    public let tab: WorkspaceTab
    public let pane: Pane
    public var id: String { tab.id }

    public init(tab: WorkspaceTab, pane: Pane) {
        self.tab = tab
        self.pane = pane
    }
}

/// Agent sessions, most pressing first: waiting for you (or failed),
/// working, then at rest. Each newest change first.
public struct SessionSections {
    public var needsYou: [SessionEntry] = []
    public var working: [SessionEntry] = []
    public var recent: [SessionEntry] = []

    public init(needsYou: [SessionEntry] = [], working: [SessionEntry] = [], recent: [SessionEntry] = []) {
        self.needsYou = needsYou
        self.working = working
        self.recent = recent
    }

    public var isEmpty: Bool { needsYou.isEmpty && working.isEmpty && recent.isEmpty }

    /// Without these panes' rows at rest (a scheduled run shown on its own).
    public func without(panes: Set<String>) -> SessionSections {
        guard !panes.isEmpty else { return self }
        return SessionSections(needsYou: needsYou, working: working, recent: recent.filter { !panes.contains($0.pane.id) })
    }
}

extension AppModel {
    /// The agent sessions in these Groups' tabs, one per tab, sorted into
    /// Needs You, Working and Recent.
    public func sessionSections(in groups: [WorkspaceGroup]) -> SessionSections {
        var out = SessionSections()
        let rank: [AgentState: Int] = [.needsInput: 0, .errored: 1, .working: 2, .starting: 2, .idle: 3]
        for group in groups {
            for tab in tabs(in: group) {
                let agents = panes(in: tab).filter { $0.hasAgent && !$0.exited && rank[$0.agent.shownState] != nil }
                guard let p = agents.min(by: { rank[$0.agent.shownState]! < rank[$1.agent.shownState]! }) else { continue }
                let row = SessionEntry(tab: tab, pane: p)
                switch p.agent.shownState {
                case .needsInput, .errored: out.needsYou.append(row)
                case .working, .starting: out.working.append(row)
                default: out.recent.append(row)
                }
            }
        }
        // Newest change first.
        for k in [\SessionSections.needsYou, \SessionSections.working, \SessionSections.recent] {
            out[keyPath: k].sort { $0.pane.agent.stateSinceUnixMs > $1.pane.agent.stateSinceUnixMs }
        }
        return out
    }
}
