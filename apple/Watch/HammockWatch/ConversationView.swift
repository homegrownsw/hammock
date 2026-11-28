import SwiftUI
import WatchKit

/// One agent's conversation, cut down for a wrist: your messages, its
/// text and pictures, a line for each run of tools, and where each turn
/// finished. Its prompt, when it's asking something, then replies.
struct ConversationView: View {
    let session: WatchSession
    @State private var conversation: WatchConversation?
    @State private var limit = 30
    @State private var problem: String?
    @State private var stopping = false
    @State private var sending = false
    @Environment(\.watchTheme) private var theme

    private var shown: WatchSession { conversation?.session ?? session }

    var body: some View {
        ScrollViewReader { scroll in
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    header
                    if conversation?.hasMore == true {
                        Button("Earlier Messages") { limit += 30; Task { await load() } }
                            .font(.footnote)
                    }
                    ForEach(conversation?.blocks ?? []) { BlockView(block: $0, session: session) }
                    if let problem {
                        Label(problem, systemImage: "exclamationmark.triangle").font(.footnote).foregroundStyle(.secondary)
                    }
                    if let c = conversation {
                        if let p = c.prompt {
                            PromptView(session: shown, prompt: p) { await load() }
                        } else {
                            actions(c)
                        }
                    } else if problem == nil {
                        ProgressView().frame(maxWidth: .infinity)
                    }
                    Color.clear.frame(height: 1).id("end")
                }
            }
            .onChange(of: conversation?.blocks.last?.id) { scroll.scrollTo("end", anchor: .bottom) }
            .onChange(of: conversation?.prompt?.promptID) { scroll.scrollTo("end", anchor: .bottom) }
        }
        .navigationTitle(session.title)
        .confirmationDialog("Stop \(session.agentName) in \(session.title)?", isPresented: $stopping, titleVisibility: .visible) {
            Button("Stop", role: .destructive) { Task { await stop() } }
            Button("Cancel", role: .cancel) {}
        }
        // While it's open: again every few seconds.
        .task {
            while !Task.isCancelled {
                await load()
                try? await Task.sleep(for: .seconds(conversation?.working == true || conversation?.prompt != nil ? 3 : 6))
            }
        }
    }

    private var header: some View {
        HStack(spacing: 6) {
            AgentMark(harness: session.harness, size: 13)
            Text(conversation?.working == true ? "Working" : shown.agentName)
            if session.showsMacName { Text("· " + session.hostName).lineLimit(1) }
        }
        .font(.footnote)
        .foregroundStyle(conversation?.working == true ? StatusPalette.indigo : .secondary)
    }

    @ViewBuilder
    private func actions(_ c: WatchConversation) -> some View {
        VStack(spacing: 6) {
            ForEach(c.suggestions.prefix(3), id: \.self) { s in
                SendButton(title: s, canSendNow: c.canSendNow && c.working) { now in await send(s, now: now) }
            }
            NavigationLink {
                ReplyView(session: shown, conversation: c) { text, now in await send(text, now: now) }
            } label: {
                Label("Reply", systemImage: "mic.fill").frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .tint(theme.accent)
            .foregroundStyle(.black)
            if c.working {
                Button(role: .destructive) { stopping = true } label: {
                    Label("Stop", systemImage: "stop.fill").frame(maxWidth: .infinity)
                }
            }
        }
        .disabled(sending)
        .padding(.top, 4)
    }

    private func load() async {
        do {
            if case let .conversation(c) = try await PhoneLink.shared.ask(.conversation(host: session.host, pane: session.paneID, limit: limit)) {
                conversation = c
                problem = nil
            }
        } catch {
            problem = error.localizedDescription
        }
    }

    private func send(_ text: String, now: Bool) async {
        sending = true
        defer { sending = false }
        do {
            _ = try await PhoneLink.shared.ask(.send(host: session.host, pane: session.paneID, text: text, now: now))
            WKInterfaceDevice.current().play(.success)
            await load()
        } catch {
            WKInterfaceDevice.current().play(.failure)
            problem = error.localizedDescription
        }
    }

    private func stop() async {
        do {
            _ = try await PhoneLink.shared.ask(.interrupt(host: session.host, pane: session.paneID))
            WKInterfaceDevice.current().play(.stop)
            await load()
        } catch {
            problem = error.localizedDescription
        }
    }
}

extension WatchSession {
    /// Several Macs are paired: say which this is.
    @MainActor var showsMacName: Bool { PhoneLink.shared.snapshot?.showsMacNames ?? false }
}

/// A reply in one tap: sent when the agent's turn ends; touched and held,
/// at once (when it can be: Claude Code, while it works).
struct SendButton: View {
    let title: String
    let canSendNow: Bool
    let send: (_ now: Bool) async -> Void
    @State private var held = false

    var body: some View {
        Text(title)
            .font(.body)
            .multilineTextAlignment(.leading)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(RoundedRectangle(cornerRadius: 18).fill(.white.opacity(held ? 0.28 : 0.14)))
            .contentShape(RoundedRectangle(cornerRadius: 18))
            .onTapGesture { Task { await send(false) } }
            .onLongPressGesture(minimumDuration: 0.5) {
                guard canSendNow else { return }
                WKInterfaceDevice.current().play(.click)
                Task { await send(true) }
            } onPressingChanged: { held = $0 }
            .accessibilityAddTraits(.isButton)
            .accessibilityAction(named: "Send Now") { Task { await send(true) } }
            .accessibilityHint(canSendNow ? "Touch and hold to send it now." : "")
    }
}

/// One block of the conversation.
struct BlockView: View {
    let block: WatchBlock
    let session: WatchSession
    @Environment(\.watchTheme) private var theme

    var body: some View {
        switch block.kind {
        case .user:
            HStack {
                Spacer(minLength: 24)
                Text(block.text)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 6)
                    .background(UnevenRoundedRectangle(topLeadingRadius: 16, bottomLeadingRadius: 16, bottomTrailingRadius: 4, topTrailingRadius: 16)
                        .fill(theme.bubble))
            }
        case .text:
            Text(SessionRow.markdown(block.text))
        case .image:
            BlockImage(session: session, ref: block.ref)
        case .tools:
            Label(block.text, systemImage: "list.bullet")
                .font(.footnote)
                .foregroundStyle(.secondary)
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .background(Capsule().fill(.white.opacity(0.1)))
        case .question:
            VStack(alignment: .leading, spacing: 2) {
                Text(block.text).font(.footnote).foregroundStyle(.secondary)
                Label(block.answer, systemImage: "arrow.turn.down.right").font(.footnote.weight(.semibold))
            }
        case .done:
            Label("Finished in \(WatchBlock.duration(block.durationMs))", systemImage: "checkmark.circle")
                .font(.footnote)
                .foregroundStyle(StatusPalette.green)
        }
    }
}

/// A picture in the conversation, fetched (small) from the phone.
struct BlockImage: View {
    let session: WatchSession
    let ref: String
    @State private var image: UIImage?
    @State private var failed = false
    @State private var zoomed = false

    var body: some View {
        Group {
            if let image {
                Image(uiImage: image).resizable().scaledToFit()
                    .clipShape(RoundedRectangle(cornerRadius: 10))
                    .onTapGesture { zoomed = true }
                    .accessibilityLabel("Picture")
            } else {
                RoundedRectangle(cornerRadius: 10).fill(.white.opacity(0.1))
                    .frame(height: 90)
                    .overlay { failed ? AnyView(Image(systemName: "photo").foregroundStyle(.secondary)) : AnyView(ProgressView()) }
            }
        }
        .sheet(isPresented: $zoomed) {
            if let image { ZoomedImage(image: image) }
        }
        .task {
            guard image == nil, let d = await PhoneLink.shared.image(host: session.host, pane: session.paneID, ref: ref) else {
                failed = image == nil
                return
            }
            image = UIImage(data: d)
            failed = image == nil
        }
    }
}

/// A picture full screen: turn the Digital Crown to zoom.
struct ZoomedImage: View {
    let image: UIImage
    @State private var zoom = 1.0

    var body: some View {
        Image(uiImage: image).resizable().scaledToFit()
            .scaleEffect(zoom)
            .focusable()
            .digitalCrownRotation($zoom, from: 1, through: 4, by: 0.1, sensitivity: .low, isContinuous: false)
    }
}

extension SessionRow {
    /// The agent's markdown, its inline marks (bold, code, links) kept.
    static func markdown(_ s: String) -> AttributedString {
        (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(s)
    }
}
