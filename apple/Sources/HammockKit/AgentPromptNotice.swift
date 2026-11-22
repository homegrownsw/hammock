import Foundation

/// A notification about an agent's permission prompt (Claude Code's or
/// Codex's menu): Allow and Deny on it answer that prompt, as Needs You's
/// do, without opening Hammock. The daemon's pushes mark one with its
/// category (agentPromptCategory, in the sealed note only); the Mac its own.
public enum AgentPromptNotice {
    public static let category = "agentprompt"
    public static let allowAction = "agentprompt.allow"
    public static let denyAction = "agentprompt.deny"
    /// Said in place of the request when nothing could be pressed (the
    /// prompt changed, it has no plain Yes, or the pane's gone).
    public static let couldntAnswer = "Couldn't answer: open it to choose"

    /// What a pushed note's Allow and Deny answer: the pane, the Needs You
    /// item and the prompt. Nil for any other note (a question, a finished
    /// agent, a grant).
    public static func answers(_ note: [AnyHashable: Any]) -> (pane: String, item: String, prompt: String)? {
        guard note["c"] as? String == category, let pane = note["pane"] as? String, !pane.isEmpty,
              let prompt = note["prompt"] as? String, !prompt.isEmpty else { return nil }
        return (pane, note["id"] as? String ?? "", prompt)
    }

    /// Whether an action is Allow (true), Deny (false), or neither (nil).
    public static func allows(_ action: String) -> Bool? {
        switch action {
        case allowAction: true
        case denyAction: false
        default: nil
        }
    }

    /// Whether an item gets Allow and Deny: an agent waiting on a
    /// permission prompt its screen shows (not a question, nor another program).
    public static func offers(_ item: AttentionItem) -> Bool {
        item.state == .needsInput && !item.question && !item.program && !item.promptID.isEmpty
    }
}

/// What answering an agent's prompt from its notification came to.
public enum NotificationAnswer: Sendable, Equatable {
    /// Pressed.
    case answered
    /// The computer pressed nothing: the prompt changed, has no plain Yes,
    /// or the pane's gone. Its notification says so.
    case refused
    /// It never got there (not connected, or the connection dropped): the
    /// notification comes back as it was, to try again.
    case unsent
}

extension AppModel {
    /// Allow or Deny from an agent's permission-prompt notification: as
    /// Needs You's (typed only while the pane shows that prompt; Deny is
    /// Esc), then its Needs You item cleared, as Needs You's does.
    public func answerPrompt(pane: String, item: String, prompt: String, allow: Bool) async -> NotificationAnswer {
        guard connected, let conn else { return .unsent }
        do {
            _ = try await conn.call {
                $0.agentAnswer = .with {
                    $0.paneID = pane
                    $0.decision = allow ? .allow : .deny
                    $0.promptID = prompt
                    $0.bound = true
                }
            }
        } catch DaemonError.server {
            return .refused
        } catch {
            return .unsent
        }
        if !item.isEmpty { clearAttention(item) }
        return .answered
    }
}
