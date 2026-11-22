import HammockKit
import SwiftUI

/// Along the bottom of a terminal whose program ended (Pane.canRunAgain),
/// where the key bar is while it runs, its last output still above it:
/// "Exited 1 · Run Again · Close", as on the Mac. An agent's pane says
/// Resume, and carries on its session.
struct ExitedPaneBar: View {
    let model: AppModel
    let pane: Pane
    @State private var closing = false
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        HStack(spacing: 10) {
            Text(pane.exitedStatusLabel)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .accessibilityHint(pane.startError)
            Spacer(minLength: 0)
            Button {
                Haptics.success()
                model.restartPane(pane.id)
            } label: {
                Text(pane.runAgainTitle).fixedSize()
            }
            .buttonStyle(.borderedProminent)
            .tint(theme.accent)
            Button {
                if closeWarning(pane) != nil { closing = true } else { close() }
            } label: {
                Text("Close").fixedSize()
            }
            .buttonStyle(.bordered)
            .tint(theme.accent)
            .accessibilityLabel("Close Pane")
        }
        .disabled(!model.connected)
        .font(.subheadline)
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.bar)
        .overlay(alignment: .top) { Rectangle().fill(.separator).frame(height: 1 / 3) }
        .accessibilityElement(children: .contain)
        .confirmationDialog("Close \(pane.displayName)?", isPresented: $closing, titleVisibility: .visible) {
            Button("Close Pane", role: .destructive) { close() }
        } message: {
            Text(closeWarning(pane) ?? "")
        }
    }

    private func close() {
        let id = pane.id
        Task { await model.closePane(id) }
    }
}
