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

    /// What Remove Worktree's confirmation says.
    public func removeWorktreeQuestion(_ tab: WorkspaceTab) -> (title: String, message: String) {
        let g = tab.git
        var message = (g.pr.state == "closed" ? "Its pull request was closed." : "Its branch is merged.") + " The tabs in it close."
        if g.dirty { message += " It has uncommitted changes, which are lost." }
        return ("Remove the worktree “\(g.worktreeName)”?", message)
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
