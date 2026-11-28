import SwiftUI
import WatchKit

/// Replying: say it, scribble or type it, or pick a reply; and stopping
/// the agent.
struct ReplyView: View {
    let session: WatchSession
    let conversation: WatchConversation
    let send: (_ text: String, _ now: Bool) async -> Void
    @State private var draft: String?
    @State private var stopping = false
    @Environment(\.dismiss) private var dismiss
    @Environment(\.watchTheme) private var theme

    private var canSendNow: Bool { conversation.canSendNow && conversation.working }

    var body: some View {
        ScrollView {
            VStack(spacing: 8) {
                HStack(spacing: 10) {
                    TextFieldLink(prompt: Text("Message \(session.agentName)")) {
                        Image(systemName: "mic.fill").font(.title3).foregroundStyle(.black)
                            .frame(width: 44, height: 44).background(Circle().fill(theme.accent))
                    } onSubmit: { text in
                        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
                        if !t.isEmpty { draft = t }
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Dictate or Type")
                    if conversation.working {
                        Button { stopping = true } label: {
                            Image(systemName: "stop.fill").font(.title3).foregroundStyle(StatusPalette.red)
                                .frame(width: 44, height: 44).background(Circle().fill(StatusPalette.red.opacity(0.2)))
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Stop")
                    }
                }
                .padding(.bottom, 4)
                if !conversation.suggestions.isEmpty {
                    Text("Suggested").font(.footnote.weight(.semibold)).foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    ForEach(conversation.suggestions, id: \.self) { s in
                        SendButton(title: s, canSendNow: canSendNow) { now in await done(s, now: now) }
                    }
                }
                if conversation.working {
                    Text(canSendNow ? "Sent when its turn ends. Touch and hold to send now." : "Sent when its turn ends.")
                        .font(.caption2).foregroundStyle(.secondary).multilineTextAlignment(.center)
                }
            }
        }
        .navigationTitle("Reply")
        // What you said, to check before it goes (dictation mishears).
        .sheet(item: Binding(get: { draft.map(Draft.init) }, set: { if $0 == nil { draft = nil } })) { d in
            ScrollView {
                VStack(spacing: 10) {
                    Text(d.text).frame(maxWidth: .infinity, alignment: .leading)
                    SendButton(title: "Send", canSendNow: canSendNow) { now in
                        draft = nil
                        await done(d.text, now: now)
                    }
                }
            }
        }
        .confirmationDialog("Stop \(session.agentName) in \(session.title)?", isPresented: $stopping, titleVisibility: .visible) {
            Button("Stop", role: .destructive) {
                Task {
                    _ = try? await PhoneLink.shared.ask(.interrupt(host: session.host, pane: session.paneID))
                    WKInterfaceDevice.current().play(.stop)
                    dismiss()
                }
            }
            Button("Cancel", role: .cancel) {}
        }
    }

    private func done(_ text: String, now: Bool) async {
        await send(text, now)
        dismiss()
    }

    private struct Draft: Identifiable {
        let text: String
        var id: String { text }
    }
}
