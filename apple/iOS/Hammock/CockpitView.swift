import HammockKit
import SwiftUI

/// The phone's main screen: Sessions (your agents by what's happening),
/// Groups (everything, as you've organised it), Devices (your Macs) and
/// Settings.
struct CockpitView: View {
    let model: AppModel
    let session: Session
    // Not remembered: the app opens on Sessions (on Groups with the dev aid
    // HMK_DEBUG_OPEN_FIRST, which opens its first tab).
    // HMK_DEBUG_COCKPIT_TAB=groups (devices, settings) opens on that tab.
    @State private var tab = ProcessInfo.processInfo.environment["HMK_DEBUG_OPEN_FIRST"] == "1" ? "groups"
        : ProcessInfo.processInfo.environment["HMK_DEBUG_COCKPIT_TAB"] ?? "sessions"

    var body: some View {
        TabView(selection: $tab) {
            Tab("Sessions", systemImage: "bubble.left.and.text.bubble.right", value: "sessions") {
                SessionsView(model: model, session: session)
            }
            .badge(needsYou)
            Tab("Groups", systemImage: "square.stack.3d.up", value: "groups") {
                GroupsView(model: model, session: session)
            }
            Tab("Devices", systemImage: "desktopcomputer", value: "devices") {
                DevicesView(session: session)
            }
            Tab("Settings", systemImage: "gearshape", value: "settings") {
                SettingsScreen(model: model)
            }
        }
        .demoBar(session)
        #if DEBUG
        // Dev aid: the demo director's tab (DemoDirector, for the App Store's videos).
        .onReceive(NotificationCenter.default.publisher(for: DemoDirector.navigate)) { n in
            if let t = n.userInfo?["tab"] as? String { withAnimation { tab = t } }
        }
        #endif
        // Above the tab bar.
        .overlay(alignment: .bottom) { ToastOverlay(model: model).padding(.bottom, 64) }
        // A notification opened: its session.
        .onChange(of: Push.shared.openPane) { _, pane in if pane != nil { tab = "sessions" } }
        .onChange(of: Push.shared.openNeedsYou) { _, on in if on { tab = "sessions" } }
        .sheet(item: Binding(get: { AgentStarter.shared.request }, set: { AgentStarter.shared.request = $0 })) { r in
            let origin = tab
            StartAgentView(model: model, groupID: r.groupID, onDone: { _ in AgentStarter.shared.request = nil },
                           onStarted: { AgentStarter.shared.didOpen($0, origin: origin) })
        }
    }

    private var needsYou: Int {
        model.joinRequests.count + model.grants.count + model.attention.filter { $0.state == .needsInput || $0.state == .errored }.count
    }
}

/// Agent sessions, most pressing first: waiting for you, working, then
/// recently finished.
struct SessionsView: View {
    let model: AppModel
    let session: Session
    @Environment(\.phoneTheme) private var theme
    @State private var path: [String] = []
    /// The session finder is up.
    @State private var finding = false
    /// Recent shows sessions older than 48 hours too, until Show Less.
    @State private var showingAllRecent = false
    /// The agent pane a swipe asked to close, while it asks first.
    @State private var closing: Pane?

    var body: some View {
        CockpitSplit(model: model, session: session, path: $path, placeholder: "No Session Selected") {
            TimelineView(.periodic(from: .now, by: 30)) { ctx in
                List(selection: cockpitSelection($path)) {
                    let ready = readyRuns(model, now: ctx.date)
                    // A ready run shows once, as its schedule's.
                    let rows = sessionRows.without(panes: Set(ready.map(\.run.paneID)))
                    let waiting = model.attention.filter { $0.state == .needsInput || $0.state == .errored }
                    let others = rows.needsYou.filter { r in !waiting.contains { $0.paneID == r.pane.id } }
                    if !model.grants.isEmpty || !waiting.isEmpty || !others.isEmpty || !model.joinRequests.isEmpty {
                        Section("Needs You") {
                            ForEach(model.joinRequests, id: \.id) { r in JoinRow(model: model, request: r) }
                            ForEach(model.grants) { g in GrantRow(model: model, grant: g) }
                            // With Allow/Deny right here, for a permission prompt.
                            ForEach(waiting) { item in
                                AttentionRow(model: model, item: item) { paneID in path = [paneID] }
                                    .modifier(closeSwipe(paneID: item.paneID))
                            }
                            ForEach(others, id: \.tab.id) { r in sessionLink(r) }
                        }
                    }
                    if !ready.isEmpty {
                        ReadyRunsSection(model: model, runs: ready) { path = [$0] }
                    }
                    if !rows.working.isEmpty {
                        Section("Working") {
                            ForEach(rows.working, id: \.tab.id) { r in sessionLink(r) }
                        }
                    }
                    if !rows.recent.isEmpty {
                        Section("Recent") {
                            // The last 48 hours', then the rest on asking (all
                            // of them when none are that new).
                            let cutoff = Int64((ctx.date.timeIntervalSince1970 - 48 * 3600) * 1000)
                            let fresh = rows.recent.filter { $0.pane.agent.stateSinceUnixMs >= cutoff }
                            let shown = showingAllRecent || fresh.isEmpty ? rows.recent : fresh
                            ForEach(shown, id: \.tab.id) { r in sessionLink(r) }
                            if !fresh.isEmpty, fresh.count < rows.recent.count {
                                Button(showingAllRecent ? "Show Less" : "Show More…") {
                                    withAnimation { showingAllRecent.toggle() }
                                }
                                .tint(theme.accent)
                            }
                        }
                    }
                    if !model.schedules.isEmpty {
                        ComingUpSection(model: model, now: ctx.date) { path = [$0] }
                            .id("comingUp")
                    }
                    // The agents' usage limits, as chosen in Settings.
                    if case let usage = UsageLimitsPrefs.shown(model), !usage.isEmpty {
                        Section("Usage Limits") {
                            UsageLimitsView(usage: usage, now: ctx.date, tint: theme.accent, font: .footnote, labelWidth: 30)
                                .padding(.vertical, 4)
                        }
                    }
                }
                #if DEBUG
                // Dev aid: HMK_DEBUG_BOTTOM=1 starts scrolled to Coming Up.
                .debugScroll(to: "comingUp", if: "HMK_DEBUG_BOTTOM")
                #endif
                .overlay {
                    if model.connected, !model.groups.isEmpty, model.grants.isEmpty, sessionRows.isEmpty, model.schedules.isEmpty {
                        ContentUnavailableView {
                            Label("No Agent Sessions", systemImage: "bubble.left.and.text.bubble.right")
                        } description: {
                            Text("Agents running on your Mac show up here.")
                        } actions: {
                            Button("Start an Agent…") { AgentStarter.shared.start(groupID: nil) }
                        }
                    }
                }
            }
            .confirmationDialog(closing.map { "Close “\(model.tab(of: $0).map { model.title(of: $0) } ?? $0.displayName)”?" } ?? "",
                                isPresented: Binding(get: { closing != nil }, set: { if !$0 { closing = nil } }),
                                titleVisibility: .visible, presenting: closing) { p in
                Button("Close Session", role: .destructive) { close(p) }
            } message: { p in
                Text(closeWarning(p) ?? "")
            }
            .sheet(isPresented: $finding) {
                NavigationStack {
                    SessionFinderList(model: model) { id in
                        finding = false
                        AgentStarter.shared.didOpen(id, origin: "sessions")
                    }
                    .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Done") { finding = false } } }
                }
            }
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button { finding = true } label: { Image(systemName: "clock.arrow.circlepath") }
                        .accessibilityLabel("Find a Session")
                        .disabled(!model.connected)
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { AgentStarter.shared.start(groupID: nil) } label: { Image(systemName: "plus") }
                        .accessibilityLabel("Start an Agent")
                        .disabled(!model.connected || !model.groups.contains(where: \.canAddTabs))
                }
            }
            #if DEBUG
            // Dev aid (with HMK_DEBUG_SAMPLE): HMK_DEBUG_PATH=a,b pushes those ids
            // (ScheduleRoute's "schedules", "schedule:ID", tabs, panes).
            .onAppear {
                if path.isEmpty, let p = ProcessInfo.processInfo.environment["HMK_DEBUG_PATH"] {
                    path = p.split(separator: ",").map(String.init)
                }
            }
            // The demo director's path (DemoDirector, for the App Store's videos).
            .onReceive(NotificationCenter.default.publisher(for: DemoDirector.navigate)) { n in
                if let p = n.userInfo?["path"] as? [String] { path = p }
            }
            #endif
            .showsStartedAgents(origin: "sessions", path: $path)
            // A notification opened: its pane, once the Mac's state is here
            // (launched by the tap, or another Mac just shown); dropped if
            // the pane has gone.
            .onChange(of: model.connected ? Push.shared.openPane : nil, initial: true) { _, pane in
                guard let pane else { return }
                if model.panes[pane] != nil { path = [pane] }
                Push.shared.openPane = nil
            }
            // A permission or a join: answered here, at the top of Needs You.
            .onChange(of: Push.shared.openNeedsYou, initial: true) { _, on in
                guard on else { return }
                path = []
                Push.shared.openNeedsYou = false
            }
        }
    }

    private func sessionLink(_ r: SessionRow) -> some View {
        // Straight to the agent's pane, even in a tab of several.
        NavigationLink(value: r.pane.id) { SessionRowView(model: model, row: r) }
            .modifier(closeSwipe(paneID: r.pane.id))
    }

    /// Swipe left to close a session, asking first when that stops
    /// something (an agent at work, a program running beside it).
    private func closeSwipe(paneID: String) -> CloseSessionSwipe {
        let pane = model.panes[paneID]
        let drive = pane.flatMap { model.group(ofPane: $0.id) }.map { $0.canDrive && !$0.offline } ?? false
        return CloseSessionSwipe(enabled: drive) {
            guard let pane else { return }
            if closeWarning(pane) != nil { closing = pane } else { close(pane) }
        }
    }

    /// Closes a session: its tab, or only the agent's pane when the tab has
    /// others in it, so they keep running.
    private func close(_ pane: Pane) {
        if path.first == pane.id { path = [] }
        Task {
            if let tab = model.tab(of: pane), model.panes(in: tab).count == 1 {
                await model.closeTab(tab.id)
            } else {
                await model.closePane(pane.id)
            }
        }
    }

    /// A tab with an agent, represented by its most pressing agent pane.
    struct SessionRow {
        let tab: WorkspaceTab
        let pane: Pane
    }

    struct Rows {
        var needsYou: [SessionRow] = [], working: [SessionRow] = [], recent: [SessionRow] = []
        var isEmpty: Bool { needsYou.isEmpty && working.isEmpty && recent.isEmpty }

        /// Without these panes' rows.
        func without(panes: Set<String>) -> Rows {
            guard !panes.isEmpty else { return self }
            return Rows(needsYou: needsYou, working: working, recent: recent.filter { !panes.contains($0.pane.id) })
        }
    }

    private var sessionRows: Rows {
        var out = Rows()
        let rank: [AgentState: Int] = [.needsInput: 0, .errored: 1, .working: 2, .starting: 2, .idle: 3]
        for group in model.groups {
            for tab in model.tabs(in: group) {
                let agents = model.panes(in: tab).filter { $0.hasAgent && !$0.exited && rank[$0.agent.shownState] != nil }
                guard let p = agents.min(by: { rank[$0.agent.shownState]! < rank[$1.agent.shownState]! }) else { continue }
                let row = SessionRow(tab: tab, pane: p)
                switch p.agent.shownState {
                case .needsInput, .errored: out.needsYou.append(row)
                case .working, .starting: out.working.append(row)
                default: out.recent.append(row)
                }
            }
        }
        // Newest change first.
        for k in [\Rows.needsYou, \Rows.working, \Rows.recent] {
            out[keyPath: k].sort { $0.pane.agent.stateSinceUnixMs > $1.pane.agent.stateSinceUnixMs }
        }
        return out
    }
}

/// A session row's swipe to close it, there only if you can drive its Group.
private struct CloseSessionSwipe: ViewModifier {
    let enabled: Bool
    let close: () -> Void

    func body(content: Content) -> some View {
        if enabled {
            content.swipeActions(edge: .trailing, allowsFullSwipe: true) {
                // Red, but not .destructive: that role takes the row away at
                // once, before it's asked whether to.
                Button("Close", systemImage: "xmark", action: close).tint(.red)
            }
        } else {
            content
        }
    }
}

/// A session as Messages shows a conversation: its name and when, its
/// latest message (Claude's reply, what it's doing, or what it's asking),
/// then, quietly, its Group, as Ready for You's cards have it. Every row has
/// the same lines, whether its Group has a picture or not, and the state sits
/// beside the name it's about.
struct SessionRowView: View {
    let model: AppModel
    let row: SessionsView.SessionRow

    var body: some View {
        let a = row.pane.agent
        HStack(alignment: .sessionTitle, spacing: 12) {
            StateGlyph(state: a.state == .idle && a.seen ? nil : a.state, resting: a.state == .idle && a.seen)
                .symbolRenderingMode(.palette)
                .alignmentGuide(.sessionTitle) { $0[VerticalAlignment.center] }
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(model.title(of: row.tab)).font(.headline).lineLimit(1)
                    Spacer(minLength: 6)
                    TimelineView(ElapsedSchedule(sinceUnixMs: a.stateSinceUnixMs)) { ctx in
                        Text(elapsedString(sinceUnixMs: a.stateSinceUnixMs, now: ctx.date))
                            .font(.subheadline).foregroundStyle(.secondary).lineLimit(1).monospacedDigit()
                    }
                }
                .alignmentGuide(.sessionTitle) { $0[VerticalAlignment.center] }
                Text(Markdown.inlineText(a.summary.isEmpty ? a.state.label : a.summary))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                if let g = model.group(of: row.tab) {
                    HStack(spacing: 5) {
                        // Its picture or emoji, else a Group's symbol, so every footer lines up.
                        GroupIconView(model: model, group: g, size: 14) {
                            Image(systemName: "square.stack.3d.up").imageScale(.small)
                        }
                        .frame(width: 15)
                        Text(model.title(of: g)).lineLimit(1)
                    }
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .padding(.top, 1)
                }
            }
        }
        .padding(.vertical, 2)
    }
}

private extension VerticalAlignment {
    /// A session row's name: where its state glyph sits.
    enum SessionTitle: AlignmentID {
        static func defaultValue(in d: ViewDimensions) -> CGFloat { d[VerticalAlignment.center] }
    }
    static let sessionTitle = VerticalAlignment(SessionTitle.self)
}

/// Every Group and tab, as organised on the Mac, and organised from here:
/// new, renamed, moved and closed tabs and Groups.
struct GroupsView: View {
    let model: AppModel
    let session: Session
    @State private var path: [String] = []
    @State private var renaming: WorkspaceTab?
    @State private var editing: WorkspaceGroup?
    @State private var creatingGroup = false
    @State private var deleting: WorkspaceGroup?
    /// Collapsed Groups, by id: this phone's own, not the Mac's, kept
    /// between launches. Plain view state, saved as it changes, so toggling
    /// animates (@AppStorage's change arrives outside withAnimation).
    @State private var collapsed = Set(UserDefaults.standard.string(forKey: "collapsedGroups")?
        .split(separator: ",").map(String.init) ?? [])

    private func toggle(_ group: WorkspaceGroup) {
        var ids = collapsed
        if ids.remove(group.id) == nil { ids.insert(group.id) }
        // Forget Groups that have gone, so the list doesn't grow forever.
        ids.formIntersection(model.groups.map(\.id))
        withAnimation { collapsed = ids }
        UserDefaults.standard.set(ids.sorted().joined(separator: ","), forKey: "collapsedGroups")
    }

    var body: some View {
        CockpitSplit(model: model, session: session, path: $path, placeholder: "No Tab Selected") {
            List(selection: cockpitSelection($path)) {
                if model.hasLinkedComputers {
                    // Your computers: all of them, or one.
                    Section { ComputerChips(model: model) }
                        .listRowInsets(EdgeInsets(top: 4, leading: 0, bottom: 4, trailing: 0))
                        .listRowBackground(Color.clear)
                }
                ForEach(model.shownGroups) { group in
                    let isCollapsed = collapsed.contains(group.id)
                    Section {
                        let tabs = isCollapsed ? [] : model.tabs(in: group)
                        if group.tabIds.isEmpty, !isCollapsed {
                            EmptyGroupRowIOS(model: model, group: group, newTab: { newTab(in: group) })
                        }
                        ForEach(tabs) { tab in
                            NavigationLink(value: tab.id) {
                                TabRowIOS(model: model, tab: tab)
                            }
                            .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                                Button("Close", systemImage: "xmark", role: .destructive) { Task { await model.closeTab(tab.id) } }
                                Button("Rename", systemImage: "pencil") { renaming = tab }.tint(.orange)
                            }
                            .contextMenu {
                                TabActions(model: model, tab: tab, rename: { renaming = tab }, newTab: group.canAddTabs ? { newTab(in: group) } : nil)
                            }
                        }
                        .onMove { from, to in
                            guard let i = from.first, i < tabs.count else { return }
                            // SwiftUI's destination counts the moved row; the daemon's index doesn't.
                            model.moveTab(tabs[i].id, to: group.id, index: to > i ? to - 1 : to)
                        }
                    } header: {
                        GroupHeader(model: model, group: group, collapsed: isCollapsed, toggle: { toggle(group) },
                                    newTab: { newTab(in: group) }, edit: { editing = group }, delete: { deleting = group })
                    }
                }
            }
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    if !model.groups.isEmpty { EditButton() }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { creatingGroup = true } label: { Image(systemName: "rectangle.stack.badge.plus") }
                        .accessibilityLabel("New Group")
                        .disabled(!model.connected)
                }
            }
            .renameTabAlert(model: model, tab: $renaming)
            .sheet(item: $editing) { GroupEditor(model: model, group: $0) }
            .sheet(isPresented: $creatingGroup) {
                GroupEditor(model: model, group: nil) { tabID in path = [tabID] }
            }
            .confirmationDialog(deleting.map { "Delete “\(model.title(of: $0))”?" } ?? "",
                                isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }),
                                titleVisibility: .visible, presenting: deleting) { g in
                Button(g.tabIds.isEmpty ? "Delete Group" : "Delete Group and Close \(g.tabIds.count == 1 ? "1 Tab" : "\(g.tabIds.count) Tabs")", role: .destructive) {
                    Task { await model.closeGroup(g.id) }
                }
            } message: { _ in
                Text("Everything running in its tabs on your Mac is stopped.")
            }
            .onChange(of: model.groups.count) {
                // Dev aid: HMK_DEBUG_OPEN_FIRST=1 opens the first tab.
                if ProcessInfo.processInfo.environment["HMK_DEBUG_OPEN_FIRST"] == "1", path.isEmpty,
                   let first = model.groups.first?.tabIds.first {
                    path.append(first)
                }
            }
            .showsStartedAgents(origin: "groups", path: $path)
        }
    }

    private func newTab(in group: WorkspaceGroup) {
        Task { if let id = await model.createTab(in: group.id) { path = [id] } }
    }
}

/// What the Sessions and Groups tabs share: the Mac's name as the title,
/// the empty and connecting states, and their list beside (or under) what
/// it opens.
///
/// Narrow (an iPhone, iPhone Duo closed) it's the list, with what it opens
/// pushed over it. Wide (iPhone Duo open, an iPad) the list stays beside
/// what's open, a level more of the same hierarchy, and the split view
/// keeps both columns clear of the fold when it's half open. One path
/// either way, so what's open survives folding and unfolding: `path[0]` is
/// what the list opened (its selection), the rest pushed from there.
struct CockpitSplit<Sidebar: View>: View {
    let model: AppModel
    let session: Session
    @Binding var path: [String]
    /// Said beside the list while it has nothing open.
    let placeholder: String
    @ViewBuilder var sidebar: Sidebar
    /// What the list opened, once it's been seen in the state (a new tab
    /// can open before it arrives), so its going means it closed.
    @State private var seen: String?

    var body: some View {
        NavigationSplitView {
            sidebar
                // As it's always looked on the iPhone (a sidebar's own style
                // is the default in a split view).
                .listStyle(.insetGrouped)
                .scrollContentBackground(.hidden)
                .background(Color(.systemGroupedBackground))
                .navigationTitle(session.host?.hostName.replacingOccurrences(of: ".local", with: "") ?? "Hammock")
                .overlay {
                    if model.groups.isEmpty {
                        ContentUnavailableView(model.connected ? "No Groups" : "Connecting…", systemImage: "square.stack.3d.up",
                                               description: Text(model.connected ? "Create one in Groups, or on your Mac." : (model.lastError ?? "")))
                    }
                }
                // About half an iPhone Duo's inner display: the list on one
                // side of the fold, what it opened on the other.
                .navigationSplitViewColumnWidth(min: 320, ideal: 400, max: 480)
        } detail: {
            NavigationStack(path: Binding(get: { Array(path.dropFirst()) }, set: { path = Array(path.prefix(1)) + $0 })) {
                Group {
                    if let first = path.first {
                        CockpitDestination(model: model, id: first, path: $path).id(first)
                    } else if !model.groups.isEmpty {
                        ContentUnavailableView(placeholder, systemImage: "sidebar.left")
                    }
                }
                .navigationDestination(for: String.self) { CockpitDestination(model: model, id: $0, path: $path) }
            }
        }
        .navigationSplitViewStyle(.balanced)
        // What the list opened closed (here or on the Mac): nothing open.
        .onChange(of: path.first.map { [$0: exists($0)] }, initial: true) { _, open in
            guard let (id, there) = open?.first else { return }
            if there { seen = id } else if seen == id { path = [] }
        }
    }

    private func exists(_ id: String) -> Bool {
        id == ScheduleRoute.list || ScheduleRoute.scheduleID(id) != nil || model.panes[id] != nil || model.tabs[id] != nil
    }
}

/// The list's selection: what `path` opened first.
@MainActor
func cockpitSelection(_ path: Binding<[String]>) -> Binding<String?> {
    Binding(get: { path.wrappedValue.first }, set: { id in
        // Choosing what's already open keeps what was pushed from it.
        if id != path.wrappedValue.first { path.wrappedValue = id.map { [$0] } ?? [] }
    })
}

/// A tab (its one pane, or an overview of several), one pane, or a
/// schedule's screen.
struct CockpitDestination: View {
    let model: AppModel
    let id: String
    @Binding var path: [String]

    var body: some View {
        if id == ScheduleRoute.list {
            ScheduledTasksScreen(model: model, path: $path)
        } else if let s = ScheduleRoute.scheduleID(id) {
            ScheduleDetailScreen(model: model, scheduleID: s, path: $path)
        } else if model.panes[id] != nil {
            PaneScreen(model: model, paneID: id, path: $path)
        } else {
            TabScreen(model: model, tabID: id, path: $path)
        }
    }
}

struct StateGlyph: View {
    let state: AgentState?
    var resting = false
    /// At rest with shells still running: a dot going round the ring.
    var shells = false
    /// With no agent state: the program running.
    var program: [String] = []
    var body: some View {
        StatusIcon(state: state, resting: resting, shells: shells, size: 17, program: program)
    }
}

struct TabRowIOS: View {
    let model: AppModel
    let tab: WorkspaceTab
    var body: some View {
        HStack {
            StateGlyph(state: model.agentState(of: tab), resting: model.hasRestingAgent(tab), shells: model.hasRestingAgentWithWork(tab),
                       program: model.focusedProgram(of: tab))
            VStack(alignment: .leading) {
                HStack(spacing: 5) {
                    Text(model.title(of: tab))
                    // A scheduled task's run.
                    if let s = model.schedule(of: tab) {
                        Image(systemName: "clock")
                            .font(.caption.weight(.medium))
                            .foregroundStyle(.tertiary)
                            .accessibilityLabel("Scheduled run of \(s.name)")
                    }
                }
                if let p = model.panes(in: tab).first(where: { $0.hasAgent }), !p.agent.summary.isEmpty {
                    Text(p.agent.summary).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
            }
        }
    }
}

struct AttentionRow: View {
    let model: AppModel
    let item: AttentionItem
    let open: (String) -> Void

    /// The schedule this is a run of.
    private var schedule: Schedule? { model.panes[item.paneID].flatMap { model.schedule(for: $0) } }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                StateGlyph(state: item.state)
                VStack(alignment: .leading) {
                    Text(model.panes[item.paneID].flatMap { model.tab(of: $0) }.map { model.title(of: $0) } ?? "Agent").font(.headline)
                    if let s = schedule {
                        HStack(spacing: 4) {
                            Image(systemName: "clock")
                            Text("Scheduled · \(s.name)")
                        }
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    Text(item.summary.isEmpty ? item.state.label : item.summary).font(.subheadline).foregroundStyle(.secondary)
                }
            }
            if item.state == .needsInput, item.question {
                HStack {
                    Spacer()
                    Button("Answer") { openPane() }.prominentButton()
                }
            } else if item.state == .needsInput {
                HStack {
                    Button("Deny") { answer(false) }.buttonStyle(.bordered)
                    Button("Open") { openPane() }.buttonStyle(.bordered)
                    Spacer()
                    if let s = schedule, s.kind == "claude" {
                        // A scheduled Claude run: allow it in every run from now on.
                        Menu {
                            Button("Allow Once", systemImage: "checkmark") { answer(true) }
                            Button("Always Allow for This Schedule", systemImage: "clock.badge.checkmark") {
                                withAnimation { model.decide(item, .allowSchedule) }
                            }
                        } label: {
                            HStack(spacing: 4) {
                                Text("Allow")
                                Image(systemName: "chevron.down").font(.caption2.weight(.bold))
                            }
                        } primaryAction: {
                            answer(true)
                        }
                        .prominentButton()
                        .accessibilityHint("Hold for Always Allow for This Schedule")
                    } else {
                        Button("Allow") { answer(true) }.prominentButton()
                    }
                }
            }
        }
        .contentShape(Rectangle())
        .onTapGesture { openPane() }
        .padding(.vertical, 4)
    }

    private func openPane() { open(item.paneID) }

    /// Answers a permission prompt, if the pane still shows it.
    private func answer(_ allow: Bool) { withAnimation { model.decide(item, allow: allow) } }
}

/// Someone asking to join a Group you share (approve from the phone).
struct JoinRow: View {
    let model: AppModel
    let request: JoinRequest

    var body: some View {
        let who = model.people[request.userID]
        let group = model.groups.first { $0.id == request.groupID }
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 10) {
                if let who { AvatarView(person: who, size: 32) }
                VStack(alignment: .leading) {
                    Text("\(who?.name ?? "Someone") wants to join").font(.headline)
                    Text([group.map { model.title(of: $0) }, who?.deviceName].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                        .font(.subheadline).foregroundStyle(.secondary)
                    if !request.keyFingerprint.isEmpty {
                        Text("Key \(request.keyFingerprint)")
                            .font(.caption.monospaced()).foregroundStyle(.secondary)
                    }
                }
            }
            if let warning = request.keyWarning(name: who?.name) {
                Label(warning, systemImage: "exclamationmark.triangle.fill")
                    .font(.subheadline).foregroundStyle(.orange)
            }
            HStack {
                Button("Decline") { withAnimation { model.respond(to: request, role: nil) } }.buttonStyle(.bordered)
                Spacer()
                Button("Let View") { withAnimation { model.respond(to: request, role: .viewer) } }.buttonStyle(.bordered)
                Button("Let Drive") { withAnimation { model.respond(to: request, role: .driver) } }.prominentButton()
            }
        }
        .padding(.vertical, 4)
    }
}

struct GrantRow: View {
    let model: AppModel
    let grant: GrantRequest
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // The palette on the icon only: on the Label it made the text
            // white too, invisible on the card.
            Label {
                Text(grant.headlineText).font(.headline)
                    .lineLimit(grant.showsWholeHeadline ? nil : 4)
            } icon: {
                Image(systemName: "hand.raised.circle.fill")
                    .symbolRenderingMode(.palette)
                    .foregroundStyle(.white, .orange)
            }
            if let origin = grant.originText {
                Text(origin)
                    .font(.subheadline).foregroundStyle(.secondary)
                    .lineLimit(3)
            }
            if let note = grant.agentNote {
                Text("The agent says: “\(note)”")
                    .font(.subheadline).foregroundStyle(.secondary)
                    .lineLimit(3)
            }
            HStack {
                Button("Deny") { withAnimation { model.respond(to: grant, .deny) } }.buttonStyle(.bordered)
                Spacer()
                if grant.canAllowAlways {
                    Button("Always") { withAnimation { model.respond(to: grant, .allowGroup) } }.buttonStyle(.bordered)
                }
                Button("Allow") { withAnimation { model.respond(to: grant, .allowOnce) } }.prominentButton()
            }
        }
        .padding(.vertical, 4)
    }
}

/// A tab's panes, swipeable when there are several.
/// A tab: its one pane as a whole screen, or with several an overview of
/// them in their layout (tap one to open it).
struct TabScreen: View {
    let model: AppModel
    let tabID: String
    @Binding var path: [String]
    @State private var renaming: WorkspaceTab?
    @State private var shown = false
    @Environment(\.dismiss) private var dismiss
    @Environment(\.horizontalSizeClass) private var hSize

    var body: some View {
        let tab = model.tabs[tabID]
        let panes = tab.map { model.panes(in: $0) } ?? []
        Group {
            if let tab, panes.count > 1 {
                PaneOverview(model: model, tab: tab) { path.append($0) }
                    .hidesTabBarWhenPushed(hSize == .compact)
                    .toolbar {
                        ToolbarItem(placement: .topBarTrailing) {
                            Menu {
                                TabActions(model: model, tab: tab, rename: { renaming = tab },
                                           newTab: model.group(of: tab)?.canAddTabs == false ? nil : { newTab(in: tab.groupID, model: model, path: $path, replacing: tabID) })
                            } label: {
                                Image(systemName: "ellipsis.circle")
                            }
                            .accessibilityLabel("Tab Actions")
                        }
                    }
                    .renameTabAlert(model: model, tab: $renaming)
                    .navigationTitle(model.title(of: tab))
                    .navigationBarTitleDisplayMode(.inline)
            } else if let p = panes.first {
                PaneScreen(model: model, paneID: p.id, path: $path, asTab: tabID)
            }
        }
        // Closed (here or on the Mac): back to the list. A new tab can open
        // before its state arrives, so only once it's been seen.
        .onChange(of: tab == nil, initial: true) { _, gone in
            if !gone { shown = true } else if shown { dismiss() }
        }
    }
}

/// Opens a new tab in a tab's place (back goes to the list, not that tab).
@MainActor
private func newTab(in groupID: String, model: AppModel, path: Binding<[String]>, replacing: String) {
    Task {
        guard let id = await model.createTab(in: groupID) else { return }
        if path.wrappedValue.last == replacing { path.wrappedValue.removeLast() }
        path.wrappedValue.append(id)
    }
}

/// One pane, the whole screen: its terminal or agent view (with a switch),
/// a browser card, or a simulator. A tab's single pane, or one opened from
/// a tab's overview or a session.
struct PaneScreen: View {
    let model: AppModel
    let paneID: String
    @Binding var path: [String]
    /// Shown as its tab (one pane): titled and closed as the tab.
    var asTab: String? = nil
    @State private var showsAgent: Bool?
    /// Which views have been shown (each is kept once it has).
    @State private var shownAgent = false
    @State private var shownTerminal = false
    @State private var preview: FilePreview?
    @State private var renaming: WorkspaceTab?
    @State private var shown = false
    @Environment(\.dismiss) private var dismiss
    /// A log pane showing its terminal instead of the log.
    @State private var logRaw = false
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.horizontalSizeClass) private var hSize

    private var pane: Pane? { model.panes[paneID] }
    private var agentMode: Bool { showsAgent ?? AgentViewPrefs.showsAgent(paneID) }

    private func setMode(_ on: Bool) {
        // The composer's keyboard doesn't stay up over the terminal.
        if !on { UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil) }
        AgentViewPrefs.setShowsAgent(paneID, on)
        showsAgent = on
    }

    /// In a Group shared with you, input needs Drive (and its host online).
    private func canDrive(_ pane: Pane) -> Bool {
        guard let g = model.group(ofPane: pane.id), g.isRemote else { return true }
        return g.canDrive && !g.offline
    }

    /// The tab's name for its only pane, else the pane's own.
    private var title: String {
        guard let pane, let tab = model.tab(of: pane) else { return "" }
        if asTab != nil || model.panes(in: tab).count < 2 { return model.title(of: tab) }
        if pane.kind == .browser || pane.kind == .simulator || pane.kind == .editor || pane.kind == .diff { return pane.title.isEmpty ? model.title(of: tab) : pane.title }
        return pane.displayName
    }

    var body: some View {
        Group {
            if let pane {
                if pane.kind == .browser {
                    BrowserScreen(model: model, paneID: pane.id, active: scenePhase == .active, canDrive: canDrive(pane))
                } else if pane.kind == .simulator {
                    SimulatorScreen(model: model, paneID: pane.id, active: scenePhase == .active, canDrive: canDrive(pane))
                } else if pane.kind == .editor {
                    EditorScreen(model: model, paneID: pane.id, canDrive: canDrive(pane))
                } else if pane.kind == .diff {
                    DiffScreen(model: model, paneID: pane.id, canDrive: canDrive(pane))
                } else if pane.isLog {
                    if logRaw {
                        TerminalScreen(model: model, paneID: pane.id, active: true)
                    } else {
                        LogConsoleView(model: model, paneID: pane.id)
                    }
                } else if model.supportsAgentView(pane) {
                    // Both kept once shown, the other hidden: switching only
                    // changes which is visible, so nothing is rebuilt (the
                    // toolbar and its switch included, whose slide a rebuild
                    // cut short), and switching back is instant.
                    ZStack {
                        if shownAgent || agentMode {
                            AgentConversationView(model: model, paneID: pane.id, onShowTerminal: { setMode(false) },
                                                  onOpenFile: { preview = FilePreview(paneID: pane.id, path: $0) })
                                .opacity(agentMode ? 1 : 0)
                                .allowsHitTesting(agentMode)
                                .accessibilityHidden(!agentMode)
                                .onAppear { shownAgent = true }
                        }
                        if shownTerminal || !agentMode {
                            TerminalScreen(model: model, paneID: pane.id, active: !agentMode)
                                .opacity(agentMode ? 0 : 1)
                                .allowsHitTesting(!agentMode)
                                .accessibilityHidden(agentMode)
                                .onAppear { shownTerminal = true }
                        }
                    }
                } else {
                    TerminalScreen(model: model, paneID: pane.id, active: true)
                }
            }
        }
        // Still while swiped back (with the keyboard up, it scrolled down
        // as the swipe went).
        .steadyWhilePopped()
        // A session gets the whole screen, unless the list is beside it (a
        // wide screen: iPhone Duo open, an iPad), where the tabs stay.
        .hidesTabBarWhenPushed(hSize == .compact)
        .toolbar {
            if let pane, model.supportsAgentView(pane) {
                ToolbarItem(placement: .principal) {
                    Picker("View", selection: Binding(get: { agentMode }, set: { setMode($0) })) {
                        Image(systemName: "text.bubble").tag(true)
                        Image(systemName: "apple.terminal").tag(false)
                    }
                    .pickerStyle(.segmented)
                    .frame(width: 120)
                }
            } else if let pane, pane.isLog {
                ToolbarItem(placement: .principal) {
                    Picker("View", selection: $logRaw) {
                        Image(systemName: "list.bullet.rectangle").tag(false)
                        Image(systemName: "apple.terminal").tag(true)
                    }
                    .pickerStyle(.segmented)
                    .frame(width: 120)
                }
            }
        }
        .toolbar {
            if let tab = pane.flatMap({ model.tab(of: $0) }) {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        TabActions(model: model, tab: tab, rename: { renaming = tab },
                                   newTab: model.group(of: tab)?.canAddTabs == false ? nil : { newTab(in: tab.groupID, model: model, path: $path, replacing: asTab ?? paneID) })
                    } label: {
                        Image(systemName: "ellipsis.circle")
                    }
                    .accessibilityLabel("Tab Actions")
                }
            }
        }
        .renameTabAlert(model: model, tab: $renaming)
        .sheet(item: $preview) { FilePreviewSheet(model: model, preview: $0) }
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
        // Closed (here or on the Mac): back to where it was opened from.
        .onChange(of: pane == nil, initial: true) { _, gone in
            if !gone { shown = true } else if shown, asTab == nil { dismiss() }
        }
        // Looking at a finished agent acknowledges it, as on the Mac: when
        // you open it, and when it finishes while you're watching.
        .onAppear {
            model.focus(paneID)
            // Its own notifications aren't shown while it's on screen (Push).
            Push.shared.visiblePane = (Push.shared.session?.host?.id ?? "", paneID)
        }
        .onDisappear { if Push.shared.visiblePane?.pane == paneID { Push.shared.visiblePane = nil } }
        .onChange(of: unseenFinish) { _, unseen in if unseen, scenePhase == .active { model.focus(paneID) } }
        .onChange(of: scenePhase) { _, phase in if phase == .active, unseenFinish { model.focus(paneID) } }
    }

    /// The pane has an agent that finished and hasn't been looked at.
    private var unseenFinish: Bool {
        guard let a = pane.flatMap({ $0.hasAgent ? $0.agent : nil }) else { return false }
        return a.state == .idle && !a.seen
    }
}

struct FilePreview: Identifiable {
    let paneID: String
    let path: String
    var id: String { path }
}

/// A file the agent's session referenced, fetched from the Mac.
struct FilePreviewSheet: View {
    let model: AppModel
    let preview: FilePreview
    @State private var data: Data?
    @State private var failed = false
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Group {
                if let data, let img = UIImage(data: data) {
                    ScrollView { Image(uiImage: img).resizable().scaledToFit().padding() }
                } else if let data {
                    ScrollView([.vertical, .horizontal]) {
                        Text(String(decoding: data.prefix(400_000), as: UTF8.self))
                            .font(.system(.footnote, design: .monospaced))
                            .textSelection(.enabled)
                            .padding()
                    }
                } else if failed {
                    ContentUnavailableView(
                        model.group(ofPane: preview.paneID)?.canDrive == false ? "Only people who can drive can open files." : "Can’t open this file",
                        systemImage: "doc.questionmark", description: Text(preview.path))
                } else {
                    ProgressView()
                }
            }
            .navigationTitle((preview.path as NSString).lastPathComponent)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } } }
        }
        .task {
            let s = model.acquireAgent(preview.paneID)
            data = await s.file("file:" + preview.path)
            failed = data == nil
            model.releaseAgent(preview.paneID)
        }
    }
}

#if DEBUG
extension View {
    /// Dev aid: scrolls to the view with this id once shown, if env is "1".
    func debugScroll(to id: String, if env: String) -> some View {
        ScrollViewReader { proxy in
            self.task {
                guard ProcessInfo.processInfo.environment[env] == "1" else { return }
                try? await Task.sleep(for: .milliseconds(500))
                proxy.scrollTo(id, anchor: .top)
            }
        }
    }
}
#endif
