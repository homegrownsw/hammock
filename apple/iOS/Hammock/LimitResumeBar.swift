import HammockKit
import SwiftUI

/// Along the top of a pane whose agent stopped at its usage limit and is
/// sent "continue" when the limit resets (Pane.limitResumeLabel):
/// "Continues at 15:01 · Cancel", over the terminal and the agent view
/// alike.
struct LimitResumeBar: View {
    let model: AppModel
    let pane: Pane
    /// You can type in its Group (not one you only watch): Cancel shows.
    let canCancel: Bool
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        // Once a minute: "tomorrow at" becomes "at" past midnight.
        TimelineView(.periodic(from: .now, by: 60)) { context in
            HStack(spacing: 10) {
                Image(systemName: "clock")
                    .foregroundStyle(.secondary)
                Text(pane.limitResumeLabel(now: context.date) ?? "")
                    .lineLimit(1)
                    .accessibilityHint(pane.agent.summary)
                Spacer(minLength: 0)
                if canCancel {
                    Button {
                        Haptics.success()
                        model.cancelLimitResume(pane.id)
                    } label: {
                        Text("Cancel").fixedSize()
                    }
                    .buttonStyle(.bordered)
                    .tint(theme.accent)
                    .accessibilityLabel("Don't Continue When Limit Resets")
                }
            }
        }
        .disabled(!model.connected)
        .font(.subheadline)
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.bar)
        .overlay(alignment: .bottom) { Rectangle().fill(.separator).frame(height: 1 / 3) }
        .accessibilityElement(children: .contain)
    }
}
