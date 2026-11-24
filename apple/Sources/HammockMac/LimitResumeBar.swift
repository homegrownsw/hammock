import HammockKit
import SwiftUI

/// Along the top of a pane whose agent stopped at its usage limit and is
/// sent "continue" when the limit resets (Pane.limitResumeLabel):
/// "Continues at 15:01 · Cancel", over the terminal and the agent view
/// alike. The agent's own words about the limit are its tooltip.
struct LimitResumeBar: View {
    let model: AppModel
    let pane: Pane
    /// The Group lets you type here (not one you only watch): Cancel shows.
    let canCancel: Bool

    var body: some View {
        // Once a minute: "tomorrow at" becomes "at" past midnight.
        TimelineView(.periodic(from: .now, by: 60)) { context in
            HStack(spacing: 8) {
                Image(systemName: "clock")
                    .foregroundStyle(.secondary)
                Text(pane.limitResumeLabel(now: context.date) ?? "")
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .tooltip(pane.agent.summary)
                Spacer(minLength: 0)
                if canCancel {
                    Button { model.cancelLimitResume(pane.id) } label: {
                        Text("Cancel").fixedSize()
                    }
                    .buttonStyle(.bordered)
                    .tooltip(Keys.hint("Don't Continue When Limit Resets", "cancel_limit_resume"), label: "Cancel")
                }
            }
            .controlSize(.small)
            .font(.callout)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.bar)
        .overlay(alignment: .bottom) { Rectangle().fill(.separator).frame(height: 1) }
        .accessibilityElement(children: .contain)
    }
}
