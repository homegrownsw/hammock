import Foundation
import SwiftUI

/// Git worktrees (the daemon's worktrees.go): a tab knows its branch and
/// the branch's pull request, and once that's merged, Remove Worktree is
/// offered for a tab in a linked worktree.
extension AppModel {
    /// Remove Worktree is offered for the tab: its worktree's branch was
    /// merged a while ago, in a Group on one of your computers (a Group
    /// shared with you is its host's to clean up).
    public func canRemoveWorktree(_ tab: WorkspaceTab) -> Bool {
        guard tab.hasGit, tab.git.removable, let g = group(of: tab) else { return false }
        return !g.isRemote || g.isOnLinkedComputer
    }

    /// Removes the tab's worktree the repository's own way; the tabs in it
    /// close. force: even with uncommitted changes (asked first).
    @discardableResult
    public func removeWorktree(_ tabID: String, force: Bool) async -> Bool {
        if await run({ $0.removeWorktree = .with { $0.tabID = tabID; $0.force = force } }) != nil { return true }
        showToast("Couldn't remove the worktree: \(lastError ?? "unknown error")")
        return false
    }

    /// The panes of the tabs in the tab's worktree (they close with it).
    public func panesInWorktree(of tab: WorkspaceTab) -> [Pane] {
        tabs.values.filter { $0.hasGit && $0.git.worktree == tab.git.worktree }.flatMap { panes(in: $0) }
    }

    /// What Remove Worktree's confirmation says: the command that runs,
    /// what closes and what's lost (warnBusy: as closing a tab would warn of
    /// agents at work). canRemove false: it has uncommitted changes the
    /// repository's own command won't drop, so there's nothing to confirm.
    public func removeWorktreeQuestion(_ tab: WorkspaceTab, warnBusy: Bool = true) -> (title: String, message: String, canRemove: Bool) {
        let g = tab.git
        if g.needsClean {
            return ("“\(g.worktreeName)” has uncommitted changes",
                    "Commit or discard them first: \(g.removeWith) won't remove it with them.", false)
        }
        var lines = [(g.pr.state == "closed" ? "Its pull request was closed." : "Its branch is merged.") + " The tabs in it close."]
        if g.dirty { lines.append("It has uncommitted changes, which are lost.") }
        if let busy = closeWarning(panesInWorktree(of: tab), warnBusy: warnBusy) { lines.append(busy) }
        lines.append("Runs: \(g.removeWith)")
        return ("Remove the worktree “\(g.worktreeName)”?", lines.joined(separator: "\n\n"), true)
    }
}

extension Hammock_V1_TabGit {
    /// The worktree's folder's name ("pricing").
    public var worktreeName: String { (worktree as NSString).lastPathComponent }

    /// The tab's git state in words, for a tooltip or VoiceOver:
    /// "Worktree on pricing · Pull request #42, merged".
    public var summary: String {
        var parts: [String] = []
        if !branch.isEmpty { parts.append((worktree.isEmpty ? "On " : "Worktree on ") + branch) }
        if hasPr, pr.number > 0 {
            parts.append("Pull request #\(pr.number), \(pr.state)" + (pr.title.isEmpty ? "" : ": \(pr.title)"))
        }
        return parts.joined(separator: " · ")
    }

    /// Whether the tab shows a git mark: a linked worktree, or a pull request.
    public var shown: Bool { !worktree.isEmpty || (hasPr && pr.number > 0) }

    /// Its mark's symbol: the pull request's state, else a branch.
    public var symbol: String {
        guard hasPr, pr.number > 0 else { return "arrow.triangle.branch" }
        return pr.state == "merged" ? "arrow.triangle.merge" : "arrow.triangle.pull"
    }

    /// Its mark's colour: a pull request's state as GitHub shows it (open
    /// green, merged purple, closed red); a draft or a bare branch quiet.
    /// Status colours, not the theme's accent: they mean the same as on
    /// GitHub whatever the theme.
    public var tint: Color? {
        guard hasPr, pr.number > 0 else { return nil }
        switch pr.state {
        case "open": return .green
        case "merged": return .purple
        case "closed": return .red
        default: return nil
        }
    }
}

/// A tab's git mark: its pull request's state, or a branch for a worktree.
public struct GitMark: View {
    let git: Hammock_V1_TabGit
    var size: CGFloat

    public init(_ git: Hammock_V1_TabGit, size: CGFloat = 11) {
        self.git = git
        self.size = size
    }

    public var body: some View {
        Image(systemName: git.symbol)
            .font(.system(size: size, weight: .medium))
            .foregroundStyle(git.tint.map { AnyShapeStyle($0) } ?? AnyShapeStyle(.tertiary))
            .accessibilityLabel(git.summary)
    }
}

/// Why closing panes needs a yes, if it does: an agent at work (unless
/// warnBusy is off), or programs other than an agent at rest still running.
@MainActor
public func closeWarning(_ panes: [Pane], warnBusy: Bool) -> String? {
    let busy = warnBusy ? panes.filter { p in
        p.hasAgent && !p.exited && ([.working, .needsInput, .starting].contains(p.agent.shownState) || p.agent.subagents > 0)
    } : []
    if busy.count > 1 { return "\(busy.count) agents are still at work. Closing stops them." }
    if let p = busy.first {
        let a = p.agent
        let doing = a.shownState == .needsInput ? "is waiting for you" : a.runningCommand ? "is running a command"
            : a.shownState == .idle ? "has \(a.subagents == 1 ? "a subagent" : "\(a.subagents) subagents") still running" : "is still working"
        return "\(p.displayName.capitalized) \(doing). Closing stops it."
    }
    var names: [String] = []
    for p in panes {
        for n in p.processes where !(p.hasAgent && n.lowercased() == p.agent.harness.lowercased()) && !names.contains(n) { names.append(n) }
    }
    guard !names.isEmpty else { return nil }
    return ListFormatter.localizedString(byJoining: names) + (names.count == 1 ? " is" : " are") + " still running in it and will be stopped."
}
