import SwiftUI

/// The session finder on a phone: search every agent's
/// session on the Mac, read one, resume it. Choosing a session or resuming
/// reports the tab (or pane) it opened through onOpen.
public struct SessionFinderList: View {
    let model: AppModel
    let onOpen: (String) -> Void
    @State private var query = ""
    @State private var results: [SessionInfo] = []
    @State private var indexing = false
    @State private var loaded = false

    public init(model: AppModel, onOpen: @escaping (String) -> Void) {
        self.model = model
        self.onOpen = onOpen
    }

    public var body: some View {
        List {
            if results.isEmpty {
                Text(!loaded || indexing ? "Indexing your sessions…" : query.isEmpty ? "No sessions yet." : "No sessions match “\(query)”.")
                    .foregroundStyle(.secondary)
            }
            ForEach(results, id: \.key) { s in
                NavigationLink {
                    PastSessionView(model: model, session: s, onOpen: onOpen)
                } label: {
                    SessionSummaryRow(session: s)
                }
            }
        }
        .searchable(text: $query, prompt: "Titles, folders, anything said")
        .navigationTitle("Find a Session")
        .task(id: query) {
            if loaded { try? await Task.sleep(for: .milliseconds(200)) }
            while !Task.isCancelled {
                guard let r = await model.listSessions(query) else { return }
                if Task.isCancelled { return }
                results = r.sessions
                indexing = r.indexing
                loaded = true
                guard r.indexing else { return }
                try? await Task.sleep(for: .milliseconds(600))
            }
        }
    }
}

extension SessionInfo {
    /// Unique across agents (their IDs are their own).
    public var key: String { harness + ":" + id }
}

/// A session in a list: its agent, title, folder, when, and the match.
public struct SessionSummaryRow: View {
    let session: SessionInfo

    public init(session: SessionInfo) { self.session = session }

    public var body: some View {
        HStack(alignment: .top, spacing: 10) {
            SessionGlyph(harness: session.harness, size: 15, font: 13)
                .frame(width: 20)
                .padding(.top, 2)
            VStack(alignment: .leading, spacing: 2) {
                HStack {
                    Text(session.title).font(.headline).lineLimit(1)
                    if !session.openPaneID.isEmpty {
                        Text("Open")
                            .font(.caption2.weight(.semibold))
                            .padding(.horizontal, 5).padding(.vertical, 1)
                            .background(Capsule().fill(Color.green.opacity(0.18)))
                            .foregroundStyle(.green)
                    }
                }
                Text(details).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                if let line = excerpt {
                    Text(line).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                }
            }
        }
    }

    private var details: String {
        let when = Date(timeIntervalSince1970: Double(session.updatedUnixMs) / 1000)
        return [shortFolder(session.cwd), when.formatted(.relative(presentation: .named)),
                session.messages == 1 ? "1 message" : "\(session.messages) messages"].filter { !$0.isEmpty }.joined(separator: " · ")
    }

    private var excerpt: AttributedString? {
        if !session.snippet.isEmpty {
            var out = AttributedString()
            for (i, part) in session.snippet.split(separator: "«", omittingEmptySubsequences: false).enumerated() {
                if i == 0 { out += AttributedString(String(part)); continue }
                let pieces = part.split(separator: "»", maxSplits: 1, omittingEmptySubsequences: false)
                var hit = AttributedString(String(pieces[0]))
                hit.inlinePresentationIntent = .stronglyEmphasized
                hit.foregroundColor = .primary
                out += hit
                if pieces.count > 1 { out += AttributedString(String(pieces[1])) }
            }
            return out
        }
        let last = session.lastPrompt.isEmpty ? session.firstPrompt : session.lastPrompt
        return last.isEmpty ? nil : AttributedString("❯ " + last)
    }
}

/// A path with the Mac's home folder as ~ (the usual /Users/NAME shape).
func shortFolder(_ p: String) -> String {
    let parts = p.split(separator: "/", omittingEmptySubsequences: false)
    if parts.count >= 3, parts[1] == "Users" {
        return "~" + (parts.count > 3 ? "/" + parts[3...].joined(separator: "/") : "")
    }
    return p
}

/// A past session, read only, with Resume (and continue in the other
/// agent) at the bottom.
public struct PastSessionView: View {
    let model: AppModel
    let session: SessionInfo
    let onOpen: (String) -> Void
    @State private var agent: AgentSession?
    @State private var failed = ""
    @State private var resuming = false

    public init(model: AppModel, session: SessionInfo, onOpen: @escaping (String) -> Void) {
        self.model = model
        self.session = session
        self.onOpen = onOpen
    }

    private var other: String? { AgentCatalog.other(of: session.harness) }

    public var body: some View {
        VStack(spacing: 0) {
            if let agent {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 14) {
                        if agent.hasMore {
                            Button(agent.loadingOlder ? "Loading…" : "Show earlier messages") { agent.loadOlder() }
                                .buttonStyle(.borderless)
                                .frame(maxWidth: .infinity)
                        }
                        ForEach(agent.items) { item in
                            AgentItemView(item: item, session: agent).equatable().id(item.id)
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 14)
                }
                .defaultScrollAnchor(.bottom, for: .initialOffset)
            } else if !failed.isEmpty {
                ContentUnavailableView("Can’t read this session", systemImage: "exclamationmark.bubble", description: Text(failed))
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            Divider()
            HStack(spacing: 10) {
                if !session.openPaneID.isEmpty {
                    Button { onOpen(session.openPaneID) } label: {
                        Label("Go to It", systemImage: "arrow.right.circle.fill").frame(maxWidth: .infinity)
                    }
                    .prominentButton()
                } else {
                    Button { resume(nil) } label: {
                        Label("Resume", systemImage: "play.fill").frame(maxWidth: .infinity)
                    }
                    .prominentButton()
                }
                if let other {
                    Button { resume(other) } label: {
                        Text("In \(AgentCatalog.shortName(of: other))").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                }
            }
            .disabled(resuming)
            .controlSize(.large)
            .padding(12)
        }
        .environment(\.agentFonts, AgentFonts.current)
        .navigationTitle(session.title)
        #if os(iOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
        .task {
            guard agent == nil else { return }
            do {
                guard let r = try await model.connection?.call({ $0.readSession = .with { $0.harness = session.harness; $0.id = session.id } }) else { return }
                let a = AgentSession(paneID: "", model: model)
                a.pastSession = (session.harness, session.id)
                a.apply(r.agentView)
                agent = a
            } catch {
                failed = error.localizedDescription
            }
        }
    }

    private func resume(_ target: String?) {
        resuming = true
        Task {
            let r = await model.resumeSession(session, in: target, groupID: nil)
            resuming = false
            if let r { onOpen(r.tabID.isEmpty ? r.paneID : r.tabID) }
        }
    }
}

/// A session's agent in the session lists: Claude's sparkle and Codex's
/// chevrons as they've always been, any other agent's mark.
public struct SessionGlyph: View {
    let harness: String
    let size: CGFloat
    let font: CGFloat

    public init(harness: String, size: CGFloat, font: CGFloat) {
        self.harness = harness
        self.size = size
        self.font = font
    }

    public var body: some View {
        switch harness {
        case "claude", "codex", "":
            Image(systemName: harness == "codex" ? "chevron.left.forwardslash.chevron.right" : "sparkle")
                .font(.system(size: font, weight: .semibold))
                .foregroundStyle(harness == "codex" ? Color.teal : Color.orange)
        default:
            AgentMark(harness: harness, size: size)
        }
    }
}
