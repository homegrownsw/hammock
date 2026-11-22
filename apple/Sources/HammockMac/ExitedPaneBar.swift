import HammockKit
import SwiftUI

/// Along the bottom of a terminal pane whose program ended (Pane.canRunAgain),
/// its last output still above it: "Exited 1 · Run Again ↩ · Close". An
/// agent's pane says Resume, and carries on its session. Return in the pane
/// does the same (TerminalNSView.onReturn). Narrower, the status goes, then
/// the buttons' words.
struct ExitedPaneBar: View {
    let model: AppModel
    let pane: Pane

    private var status: String { pane.exitedStatusLabel }

    private var runTitle: String { pane.runAgainTitle }

    var body: some View {
        ViewThatFits(in: .horizontal) {
            row(showsStatus: true, words: true)
            row(showsStatus: false, words: true)
            row(showsStatus: false, words: false)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.bar)
        .overlay(alignment: .top) { Rectangle().fill(.separator).frame(height: 1) }
        .accessibilityElement(children: .contain)
    }

    private func row(showsStatus: Bool, words: Bool) -> some View {
        HStack(spacing: 8) {
            if showsStatus {
                Text(status)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .fixedSize()
                    .tooltip(pane.startError)
            }
            Spacer(minLength: 0)
            Button { model.restartPane(pane.id) } label: {
                if words {
                    HStack(spacing: 5) {
                        Text(runTitle)
                        Text("↩").opacity(0.7)
                    }
                    .fixedSize()
                } else {
                    Image(systemName: pane.resumesWhenRunAgain ? "play.fill" : "arrow.clockwise")
                }
            }
            .buttonStyle(.borderedProminent)
            .tint(Color.themeAccent)
            .tooltip(Keys.hint(runTitle, "run_again"), label: runTitle)
            Button { close() } label: {
                if words { Text("Close").fixedSize() } else { Image(systemName: "xmark") }
            }
            .buttonStyle(.bordered)
            .tooltip("Close Pane", label: "Close")
        }
        .controlSize(.small)
        .font(.callout)
    }

    private func close() {
        let id = pane.id
        CloseGuard.confirm(model, panes: [id]) { Task { await model.closePane(id) } }
    }
}
