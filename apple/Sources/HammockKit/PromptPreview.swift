import SwiftUI

/// What a Needs You card shows of the prompt an agent is waiting on (its
/// whole text as the pane shows it: the command, every line), cut to a few
/// lines. Allow from the card only when nothing was cut: otherwise the card
/// offers Review, which opens the pane, so what's allowed is always seen.
public struct PromptPreview: Equatable {
    /// The most lines a card shows, and characters in all.
    public static let maxLines = 10
    public static let maxCharacters = 1200

    public let text: String
    /// Lines left out (0: none, though the last may still be cut short).
    public let hiddenLines: Int
    /// The whole prompt is shown.
    public let complete: Bool

    public init(_ detail: String) {
        var lines = detail.components(separatedBy: "\n")
        // "…" first: the prompt ran off the top of its pane's screen.
        var cut = detail.hasPrefix("…\n")
        var hidden = 0
        if lines.count > Self.maxLines {
            hidden = lines.count - Self.maxLines
            lines = Array(lines.prefix(Self.maxLines))
        }
        var shown = lines.joined(separator: "\n")
        if shown.count > Self.maxCharacters {
            shown = String(shown.prefix(Self.maxCharacters)) + "…"
            cut = true
        }
        text = shown
        hiddenLines = hidden
        complete = hidden == 0 && !cut
    }

    /// "+3 more lines", when any are left out.
    public var moreLabel: String? {
        switch hiddenLines {
        case 0: return complete ? nil : "More not shown"
        case 1: return "+1 more line"
        default: return "+\(hiddenLines) more lines"
        }
    }
}

extension AttentionItem {
    /// The prompt it's waiting on, as its card shows it: nil without one
    /// (an older daemon, a question, or the pane not showing it yet).
    public var promptPreview: PromptPreview? {
        guard state == .needsInput, !question, !detail.isEmpty else { return nil }
        return PromptPreview(detail)
    }

    /// Allow can go straight from its card: the card shows the whole of
    /// what's asked, or it has nothing more to show (an older daemon).
    public var allowsFromCard: Bool { promptPreview?.complete ?? true }

    /// The answers a session's touch-and-hold menu offers it, top first, as
    /// its card does: none for a question or nothing asked, Deny alone when
    /// the card can't show all of it (open the pane to see it first), and
    /// Always Allow for This Schedule too for a scheduled Claude run.
    public func promptAnswers(scheduledClaude: Bool) -> [PromptAnswer] {
        guard state == .needsInput, !question else { return [] }
        guard allowsFromCard else { return [.deny] }
        return scheduledClaude ? [.allow, .allowForSchedule, .deny] : [.allow, .deny]
    }
}

/// An answer to an agent's permission prompt, from outside its pane.
public enum PromptAnswer: Equatable, Sendable {
    case allow, allowForSchedule, deny

    public var decision: Hammock_V1_AgentAnswer.Decision {
        switch self {
        case .allow: .allow
        case .allowForSchedule: .allowSchedule
        case .deny: .deny
        }
    }
}

extension AppModel {
    /// The permission prompt a pane's agent waits on, with the answers its
    /// touch-and-hold menu offers (`promptAnswers`): nil when there's none.
    public func promptAnswers(forPane paneID: String) -> (item: AttentionItem, answers: [PromptAnswer])? {
        guard let item = attention.first(where: { $0.paneID == paneID && $0.state == .needsInput && !$0.question }) else { return nil }
        let scheduledClaude = panes[paneID].flatMap { schedule(for: $0) }?.kind == "claude"
        let answers = item.promptAnswers(scheduledClaude: scheduledClaude)
        return answers.isEmpty ? nil : (item, answers)
    }
}

/// A Needs You card's prompt: its text in a monospaced block, and how much
/// was left out.
public struct PromptPreviewView: View {
    let preview: PromptPreview
    let font: Font

    public init(_ preview: PromptPreview, font: Font = .caption) {
        self.preview = preview
        self.font = font
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(preview.text)
                .font(font.monospaced())
                .foregroundStyle(.primary)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
            if let more = preview.moreLabel {
                Text(more)
                    .font(font.weight(.semibold))
                    .foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(8)
        .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 6))
    }
}
