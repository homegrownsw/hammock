import HammockKit
import SwiftUI

/// What the sidebar lists: the Groups and their tabs, or every agent
/// session (the phones' Sessions tab).
enum SidebarMode: String {
    case groups, sessions
}

/// The sidebar's mode, remembered between launches (state, not a setting).
@MainActor @Observable
final class SidebarModeState {
    static let shared = SidebarModeState()
    private static let key = "sidebarMode"

    var mode: SidebarMode = SidebarMode(rawValue: UserDefaults.standard.string(forKey: key) ?? "") ?? .groups {
        didSet { UserDefaults.standard.set(mode.rawValue, forKey: Self.key) }
    }

    /// Sessions is the vertical sidebar's: with Horizontal Tabs the sidebar
    /// lists only Groups.
    var applies: Bool { SidebarLayout.current != .horizontal }
    var showsSessions: Bool { applies && mode == .sessions }

    func show(_ m: SidebarMode) {
        withAnimation(.snappy(duration: 0.2)) { mode = m }
    }
}

/// Groups | Sessions, at the left of the sidebar's footer.
struct SidebarModePicker: View {
    private var state: SidebarModeState { .shared }

    var body: some View {
        Picker("Sidebar", selection: Binding(get: { state.mode }, set: { state.show($0) })) {
            Text("Groups").tag(SidebarMode.groups)
                .help(Keys.hint("Groups", "show_groups"))
            Text("Sessions").tag(SidebarMode.sessions)
                .help(Keys.hint("Sessions", "show_sessions"))
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .fixedSize()
    }
}

/// The sidebar's Sessions: the agents in the shown Groups' tabs, one row a
/// tab, in Needs You (with the queue's cards: permission prompts,
/// questions, requests), Working and Recent, as on the phones. Rows of the
/// sidebar's List.
struct SidebarSessionsList: View {
    let model: AppModel
    /// Recent shows sessions older than 48 hours too, until Show Less.
    @State private var showingAllRecent = false
    @State private var hovered: String?
    @Environment(\.sidebarCompact) private var compact

    var body: some View {
        let groups = model.shownGroups
        let sections = model.sessionSections(in: groups)
        let shownPanes = Set(groups.flatMap { g in model.tabs(in: g).flatMap { model.panes(in: $0).map(\.id) } })
        let cards = model.needsYou.filter { $0.paneID.isEmpty || shownPanes.contains($0.paneID) }
        // A session the queue has a card for shows as its card.
        let others = sections.needsYou.filter { s in !cards.contains { $0.paneID == s.pane.id } }
        let links = NeedsYouView.incomingLinks(model)
        let needsYou = !links.isEmpty || !model.joinRequests.isEmpty || !model.grants.isEmpty || !cards.isEmpty || !others.isEmpty

        if needsYou {
            heading("Needs You")
            ForEach(links, id: \.id) { card(LinkRequestCard(model: model, request: $0)) }
            ForEach(model.joinRequests, id: \.id) { card(JoinCard(model: model, request: $0)) }
            ForEach(model.grants) { card(GrantCard(model: model, grant: $0)) }
            ForEach(cards) { card(AttentionCard(model: model, item: $0)) }
            ForEach(others) { row($0) }
        }
        if !sections.working.isEmpty {
            heading("Working")
            ForEach(sections.working) { row($0) }
        }
        if !sections.recent.isEmpty {
            heading("Recent")
            // The last 48 hours', then the rest on asking (all of them when
            // none are that new).
            let cutoff = Int64((Date().timeIntervalSince1970 - 48 * 3600) * 1000)
            let fresh = sections.recent.filter { $0.pane.agent.stateSinceUnixMs >= cutoff }
            let shown = showingAllRecent || fresh.isEmpty ? sections.recent : fresh
            ForEach(shown) { row($0) }
            if !fresh.isEmpty, fresh.count < sections.recent.count {
                Button(showingAllRecent ? "Show Less" : "Show More…") {
                    withAnimation(.snappy(duration: 0.25)) { showingAllRecent.toggle() }
                }
                .buttonStyle(.link)
                .font(.callout)
                .padding(.leading, 10)
                .padding(.vertical, 4)
                .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
            }
        }
        if !needsYou, sections.isEmpty {
            Text("No Agent Sessions")
                .font(.callout)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity)
                .padding(.top, 24)
                .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
        }
    }

    private func heading(_ title: String) -> some View {
        Text(title)
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(.secondary)
            .padding(.leading, 10)
            .padding(.top, 10)
            .padding(.bottom, 2)
            .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
    }

    private func card(_ content: some View) -> some View {
        content
            .padding(.vertical, 3)
            .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
    }

    private func row(_ s: SessionEntry) -> some View {
        SidebarSessionRow(model: model, session: s)
            .padding(.leading, 10)
            .padding(.trailing, 8)
            .padding(.vertical, compact ? 5 : 7)
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
            .background {
                SidebarPill(selected: model.selectedTabID == s.tab.id, hovered: hovered == s.tab.id)
                    .padding(.vertical, compact ? 1 : 2)
            }
            .tabPreview(model: model, tab: s.tab, edge: .trailing)
            .onHover { inside in
                if inside, !PaletteState.shared.isOpen { hovered = s.tab.id } else if hovered == s.tab.id { hovered = nil }
            }
            // Straight to the agent's pane, even in a tab of several.
            .onTapGesture { model.reveal(paneID: s.pane.id) }
            .contextMenu { TabMenuItems(model: model, tab: s.tab) }
            .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
    }
}

/// A session as the phones show it: its state and name, when, its latest
/// message (Claude's reply, what it's doing, or what it's asking), then,
/// quietly, its Group.
struct SidebarSessionRow: View {
    let model: AppModel
    let session: SessionEntry
    @Environment(\.sidebarCompact) private var compact

    var body: some View {
        let a = session.pane.agent
        HStack(alignment: .top, spacing: compact ? 7 : 8) {
            StatusIcon(state: a.shownState == .idle && a.seen ? nil : a.shownState, resting: a.shownState == .idle && a.seen, size: compact ? 16 : 17,
                       asleep: session.pane.asleep, progress: session.pane.agentProgress, waiting: a.waitingOnLimit)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 2) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(model.title(of: session.tab))
                        .font(.system(size: compact ? 13.5 : 14, weight: .medium))
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    TimelineView(ElapsedSchedule(sinceUnixMs: a.stateSinceUnixMs)) { ctx in
                        Text(elapsedString(sinceUnixMs: a.stateSinceUnixMs, now: ctx.date))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .monospacedDigit()
                            .lineLimit(1)
                    }
                }
                let line = a.summary.isEmpty ? session.pane.agentStatusLabel : a.summary
                if !line.isEmpty {
                    Text(Markdown.inlineText(line))
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                if let resume = session.pane.limitResumeLabel() {
                    Label(resume, systemImage: "hourglass")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                if let g = model.group(of: session.tab) {
                    HStack(spacing: 5) {
                        GroupIconView(model: model, group: g, size: 12) {
                            Image(systemName: "square.stack.3d.up").imageScale(.small)
                        }
                        .frame(width: 13)
                        Text(model.title(of: g)).lineLimit(1)
                    }
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .padding(.top, 1)
                }
            }
        }
        .accessibilityElement(children: .combine)
    }
}
