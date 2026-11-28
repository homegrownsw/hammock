import SwiftUI
import WatchKit

/// What the agent's screen asks: a permission, a question or a plan, with
/// its options to pick (and tick, for several), as the phone's prompt
/// card has them. The Mac only answers the prompt it was for.
struct PromptView: View {
    let session: WatchSession
    let prompt: WatchPrompt
    let answered: () async -> Void
    @State private var ticked: Set<Int> = []
    @State private var answering = false
    @State private var problem: String?
    @Environment(\.watchTheme) private var theme

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if !prompt.header.isEmpty {
                Text(prompt.header.uppercased()).font(.caption2.weight(.semibold)).foregroundStyle(StatusPalette.amber)
            } else if prompt.kind == .permission {
                Label("\(session.agentName) wants to", systemImage: "exclamationmark.circle.fill")
                    .font(.footnote.weight(.semibold)).foregroundStyle(StatusPalette.amber)
            }
            Text(prompt.title).font(.headline)
            if prompt.kind == .permission, !prompt.body.isEmpty {
                Text(prompt.body)
                    .font(.system(.footnote, design: .monospaced))
                    .lineLimit(8)
                    .padding(8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(RoundedRectangle(cornerRadius: 10).fill(.white.opacity(0.1)))
            }
            ForEach(Array(prompt.options.enumerated()), id: \.offset) { i, option in
                optionRow(i + 1, option)
            }
            if prompt.multiSelect {
                Button("Submit") { Task { await answer(option: 0, ticking: ticked.sorted()) } }
                    .buttonStyle(.borderedProminent)
                    .tint(theme.accent)
                    .disabled(ticked.isEmpty)
            }
            if let problem {
                Text(problem).font(.footnote).foregroundStyle(StatusPalette.red)
            }
        }
        .disabled(answering)
        .onChange(of: prompt.promptID) { ticked = [] }
    }

    @ViewBuilder
    private func optionRow(_ n: Int, _ option: String) -> some View {
        let description = prompt.descriptions.indices.contains(n - 1) ? prompt.descriptions[n - 1] : ""
        // Options 1 to checkboxes tick (none said: all); those after ("Chat about this") answer at once.
        let checkbox = prompt.multiSelect && (prompt.checkboxes == 0 || n <= prompt.checkboxes)
        if n == prompt.other {
            // Typed (or said) into the option that takes text.
            TextFieldLink(prompt: Text(option)) {
                label(option, description, systemImage: "mic.fill")
            } onSubmit: { text in
                let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
                guard !t.isEmpty else { return }
                Task { await answer(option: n, ticking: prompt.multiSelect ? ticked.sorted() + [n] : [], text: t) }
            }
            .buttonStyle(.plain)
        } else if checkbox {
            Button {
                if ticked.contains(n) { ticked.remove(n) } else { ticked.insert(n) }
            } label: {
                label(option, description, systemImage: ticked.contains(n) ? "checkmark.circle.fill" : "circle")
            }
            .buttonStyle(.plain)
        } else {
            Button { Task { await answer(option: n, ticking: []) } } label: {
                label(option, description, systemImage: nil, prominent: n == 1, destructive: Self.isNo(option))
            }
            .buttonStyle(.plain)
        }
    }

    private func label(_ title: String, _ description: String, systemImage: String?, prominent: Bool = false, destructive: Bool = false) -> some View {
        HStack(spacing: 8) {
            if let systemImage { Image(systemName: systemImage).foregroundStyle(theme.accent) }
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(.body.weight(.semibold))
                    .foregroundStyle(prominent ? .black : destructive ? StatusPalette.red : .white)
                if !description.isEmpty {
                    Text(description).font(.caption2).foregroundStyle(prominent ? .black.opacity(0.7) : .secondary)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .background(RoundedRectangle(cornerRadius: 18).fill(prominent ? theme.accent : destructive ? StatusPalette.red.opacity(0.2) : .white.opacity(0.14)))
        .contentShape(RoundedRectangle(cornerRadius: 18))
    }

    /// A refusal ("No", "No, and tell Claude…"), drawn in red.
    static func isNo(_ option: String) -> Bool {
        let o = option.lowercased()
        return o == "no" || o.hasPrefix("no,") || o.hasPrefix("no ") || o.hasPrefix("deny")
    }

    private func answer(option: Int, ticking: [Int], text: String = "") async {
        answering = true
        defer { answering = false }
        do {
            _ = try await PhoneLink.shared.ask(.answer(host: session.host, pane: session.paneID, promptID: prompt.promptID,
                                                       option: option, ticking: ticking, text: text))
            WKInterfaceDevice.current().play(.success)
            problem = nil
            await answered()
        } catch {
            WKInterfaceDevice.current().play(.failure)
            problem = error.localizedDescription
        }
    }
}
