import SwiftUI

/// Every paired Mac's agent sessions: those that need you, then those
/// working, then those finished, as the iPhone's Sessions list has them.
struct SessionsView: View {
    @State private var link = PhoneLink.shared
    @Environment(\.watchTheme) private var theme
    #if DEBUG
    @State private var debugPath: [WatchSession] = WatchSample.openConversation.map { [$0] } ?? []
    #endif

    private var snapshot: WatchSnapshot? { link.snapshot }

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { ctx in
            List {
                if let s = snapshot {
                    let groups = Dictionary(grouping: s.sessions, by: \.section)
                    section("Needs You", groups[.needsYou], color: StatusPalette.amber, now: ctx.date, macs: s.showsMacNames)
                    section("Working", groups[.working], color: .secondary, now: ctx.date, macs: s.showsMacNames)
                    section("Finished", groups[.recent].map { Array($0.prefix(8)) }, color: .secondary, now: ctx.date, macs: s.showsMacNames)
                    if !s.unreachable.isEmpty {
                        Text("Couldn't reach \(ListFormatter.localizedString(byJoining: s.unreachable)).")
                            .font(.footnote).foregroundStyle(.secondary).listRowBackground(Color.clear)
                    }
                }
                if let problem = link.problem {
                    Label(problem, systemImage: "iphone.slash")
                        .font(.footnote).foregroundStyle(.secondary).listRowBackground(Color.clear)
                }
            }
            .overlay {
                if let s = snapshot, s.sessions.isEmpty, link.problem == nil {
                    ContentUnavailableView("No Agents", systemImage: "bubble.left.and.text.bubble.right",
                                           description: Text("Agents running on your Mac show up here."))
                } else if snapshot == nil {
                    if link.loading {
                        ProgressView()
                    } else {
                        ContentUnavailableView("Not Connected", systemImage: "iphone",
                                               description: Text(link.problem ?? "Open Hammock on your iPhone and pair your Mac."))
                    }
                }
            }
        }
        .navigationTitle("Hammock")
        .navigationDestination(for: WatchSession.self) { ConversationView(session: $0) }
        #if DEBUG
        .navigationDestination(isPresented: Binding(get: { !debugPath.isEmpty }, set: { if !$0 { debugPath = [] } })) {
            if let s = debugPath.first { ConversationView(session: s) }
        }
        #endif
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                NavigationLink {
                    UsageView()
                } label: {
                    Image(systemName: "gauge.with.dots.needle.33percent")
                }
                .accessibilityLabel("Usage and Schedules")
            }
        }
        .refreshable { await link.refresh() }
        .task { await link.refresh() }
    }

    @ViewBuilder
    private func section(_ title: String, _ rows: [WatchSession]?, color: Color, now: Date, macs: Bool) -> some View {
        if let rows, !rows.isEmpty {
            Section {
                ForEach(rows) { s in
                    NavigationLink(value: s) { SessionRow(session: s, now: now, showsMac: macs) }
                }
            } header: {
                Text(title).foregroundStyle(color)
            }
        }
    }
}

/// A session in the list: its state, its tab's name, how long, and what
/// it's doing or asking.
struct SessionRow: View {
    let session: WatchSession
    let now: Date
    let showsMac: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            ZStack(alignment: .bottomTrailing) {
                AgentMark(harness: session.harness, size: 16)
                    .frame(width: 26, height: 26)
                    .background(Circle().fill(.white.opacity(0.12)))
                session.icon
                    .background(Circle().fill(.black).padding(-1.5))
                    .offset(x: 4, y: 4)
            }
            .padding(.top, 1)
            VStack(alignment: .leading, spacing: 1) {
                HStack(alignment: .firstTextBaseline) {
                    Text(session.title).font(.headline).lineLimit(1)
                    Spacer(minLength: 4)
                    Text(session.elapsed(now: now)).font(.caption2).foregroundStyle(.secondary).monospacedDigit()
                }
                Text(Self.inline(session.summary)).font(.footnote).foregroundStyle(.secondary).lineLimit(2)
                if showsMac {
                    Text(session.hostName).font(.caption2).foregroundStyle(.tertiary).lineLimit(1)
                }
            }
        }
        .accessibilityElement(children: .combine)
    }

    /// Markdown's inline marks (bold, code) without its blocks.
    static func inline(_ s: String) -> AttributedString {
        (try? AttributedString(markdown: s, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(s)
    }
}
