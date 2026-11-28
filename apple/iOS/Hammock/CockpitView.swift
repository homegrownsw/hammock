import HammockKit
import SwiftUI

/// The phone's main screen: Sessions (your agents by what's happening),
/// Groups (everything, as you've organised it), Devices (your Macs) and
/// Settings.
struct CockpitView: View {
    let model: AppModel
    let session: Session
    /// Settings ▸ Default Tab: Sessions or Groups, first in the tab bar.
    @AppStorage(CockpitView.defaultTabKey) private var defaultTab = "sessions"
    static let defaultTabKey = "defaultTab"
    // Not remembered: the app opens on the default tab (on Groups with the
    // dev aid HMK_DEBUG_OPEN_FIRST, which opens its first tab).
    // HMK_DEBUG_COCKPIT_TAB=groups (devices, settings) opens on that tab.
    @State private var tab = ProcessInfo.processInfo.environment["HMK_DEBUG_OPEN_FIRST"] == "1" ? "groups"
        : ProcessInfo.processInfo.environment["HMK_DEBUG_COCKPIT_TAB"]
        ?? (UserDefaults.standard.string(forKey: CockpitView.defaultTabKey) == "groups" ? "groups" : "sessions")

    var body: some View {
        TabView(selection: $tab) {
            if defaultTab == "groups" {
                groupsTab
                sessionsTab
            } else {
                sessionsTab
                groupsTab
            }
            Tab("Devices", systemImage: "desktopcomputer", value: "devices") {
                DevicesView(session: session)
            }
            Tab("Settings", systemImage: "gearshape", value: "settings") {
                SettingsScreen(model: model)
            }
        }
        // A Mac's screen minimised: a bar above the tab bar (and at the top
        // of a screen that hides it), and the viewer over everything while
        // it's open, kept while minimised so its stream goes on.
        .screenShareAccessory()
        .overlay { ScreenShareViewerHost(model: model) }
        #if DEBUG
        // Dev aid: HMK_DEBUG_SCREEN_BAR=<name> shows a made-up minimised screen.
        .task {
            if let name = ProcessInfo.processInfo.environment["HMK_DEBUG_SCREEN_BAR"], !name.isEmpty {
                ScreenShare.shared.showSample(name: name == "1" ? "Studio" : name)
            }
        }
        #endif
        // The link to the Mac, for the connection bar on every screen.
        .watchesLink(model, host: session.hostShortName)
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
        .onChange(of: tab, initial: true) {
            AgentStarter.shared.shownTab = tab
            ScreenShare.shared.shownTab = tab
        }
        // The Home Screen icon's count, as this Mac's pushes set it.
        .onChange(of: model.connected && !session.isDemo ? model.badgeCount : nil, initial: true) { _, n in
            if let n { Push.setBadge(n) }
        }
        // A notification opened over the Mac's screen: the screen minimises.
        .onChange(of: Push.shared.openPane) { _, pane in if pane != nil { tab = "sessions"; ScreenShare.shared.minimise() } }
        .onChange(of: Push.shared.openNeedsYou) { _, on in if on { tab = "sessions" } }
        .sheet(item: Binding(get: { AgentStarter.shared.request }, set: { AgentStarter.shared.request = $0 })) { r in
            let origin = tab
            StartAgentView(model: model, groupID: r.groupID, onDone: { _ in AgentStarter.shared.request = nil },
                           onStarted: { AgentStarter.shared.didOpen($0, origin: origin) })
        }
    }

    private var sessionsTab: some TabContent<String> {
        Tab("Sessions", systemImage: "bubble.left.and.text.bubble.right", value: "sessions") {
            SessionsView(model: model, session: session)
                .environment(\.cockpitTab, "sessions")
        }
        .badge(model.badgeCount)
    }

    private var groupsTab: some TabContent<String> {
        Tab("Groups", systemImage: "square.stack.3d.up", value: "groups") {
            GroupsView(model: model, session: session)
                .environment(\.cockpitTab, "groups")
        }
    }
}

/// Agent sessions, most pressing first: waiting for you, working, then
/// recently finished.
struct SessionsView: View {
    let model: AppModel
    let session: Session
    @Environment(\.phoneTheme) private var theme
    /// Allow and Deny wait for the Mac (the connection bar says why).
    @Environment(\.linkDown) private var linkDown
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
                            ForEach(model.grants) { g in
                                // The pane shows the whole request, in either view.
                                GrantRow(model: model, grant: g) { path = [$0] }
                            }
                            // With Allow/Deny right here, for a permission prompt.
                            ForEach(waiting) { item in
                                AttentionRow(model: model, item: item) { paneID in path = [paneID] }
                                    .modifier(closeSwipe(paneID: item.paneID))
                                    .modifier(holdMenu(paneID: item.paneID))
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
                    if !rows.finished.isEmpty {
                        Section("Finished") {
                            ForEach(rows.finished, id: \.tab.id) { r in sessionLink(r) }
                        }
                    }
                    if !rows.recent.isEmpty {
                        Section("Recent") {
                            // The last 48 hours', then the rest on asking (all
                            // of them when none are that new).
                            let shown = rows.shownRecent(all: showingAllRecent, now: ctx.date)
                            ForEach(shown.rows, id: \.tab.id) { r in sessionLink(r) }
                            if shown.more {
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
                .refreshable { await model.refresh() }
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
                    .themedScreenBackground()
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
            // Dev aid: HMK_DEBUG_HOLD_PREVIEW=<pane id> shows its touch-and-hold preview.
            .overlay {
                if let id = ProcessInfo.processInfo.environment["HMK_DEBUG_HOLD_PREVIEW"], model.panes[id] != nil {
                    HoldPreviewDebug(model: model, paneID: id)
                }
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

    private func sessionLink(_ r: SessionEntry) -> some View {
        // Straight to the agent's pane, even in a tab of several.
        NavigationLink(value: r.pane.id) { SessionRowView(model: model, row: r) }
            .modifier(closeSwipe(paneID: r.pane.id))
            .modifier(holdMenu(paneID: r.pane.id))
    }

    /// Touch and hold: the session's pane, large and live (tap it to open
    /// it), over Allow and Deny for a permission prompt and the pane's menu.
    private func holdMenu(paneID: String) -> HoldMenu {
        HoldMenu(model: model, paneID: paneID, open: { path = [paneID] }) { [model, linkDown] in
            sessionHoldMenu(model: model, paneID: paneID, linkDown: linkDown) { closing = $0 }
        }
    }

    /// Swipe left to close a session, asking first when that stops
    /// something (an agent at work, a program running beside it).
    private func closeSwipe(paneID: String) -> CloseSessionSwipe {
        let pane = model.panes[paneID]
        let drive = pane.flatMap { model.group(ofPane: $0.id) }.map { $0.canDrive && !$0.offline } ?? false
        return CloseSessionSwipe(enabled: drive && model.connected) {
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

    private var sessionRows: SessionSections { model.sessionSections(in: model.groups) }
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
    let row: SessionEntry

    var body: some View {
        let a = row.pane.agent
        HStack(alignment: .sessionTitle, spacing: 12) {
            StateGlyph(state: a.shownState == .idle && a.seen ? nil : a.shownState, resting: a.shownState == .idle && a.seen, asleep: row.pane.asleep,
                       progress: row.pane.agentProgress, waiting: a.waitingOnLimit)
                .symbolRenderingMode(.palette)
                .staleWhenLinkDown()
                .alignmentGuide(.sessionTitle) { $0[VerticalAlignment.center] }
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(model.title(of: row.tab)).font(.headline).lineLimit(1)
                    Spacer(minLength: 6)
                    TimelineView(ElapsedSchedule(sinceUnixMs: a.stateSinceUnixMs)) { ctx in
                        Text(elapsedString(sinceUnixMs: a.stateSinceUnixMs, now: ctx.date))
                            .font(.subheadline).foregroundStyle(.secondary).lineLimit(1).monospacedDigit()
                    }
                    .staleWhenLinkDown()
                }
                .alignmentGuide(.sessionTitle) { $0[VerticalAlignment.center] }
                Text(Markdown.inlineText(a.summary.isEmpty ? row.pane.agentStatusLabel : a.summary))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                // Stopped at its usage limit: when it carries on by itself.
                if let resume = row.pane.limitResumeLabel() {
                    Label(resume, systemImage: "hourglass")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
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
    @State private var closingTab: WorkspaceTab?
    @State private var removingWorktree: WorkspaceTab?
    @State private var editing: WorkspaceGroup?
    @State private var creatingGroup = false
    @State private var deleting: WorkspaceGroup?
    @State private var leaving: WorkspaceGroup?
    /// Collapsed Groups, by id: this phone's own, not the Mac's, kept
    /// between launches. Plain view state, saved as it changes, so toggling
    /// animates (@AppStorage's change arrives outside withAnimation).
    @State private var collapsed = Set(UserDefaults.standard.string(forKey: "collapsedGroups")?
        .split(separator: ",").map(String.init) ?? [])

    /// The pane a tab's touch and hold shows: the agent its row stands for,
    /// else the one focused on the Mac, else its first.
    private func previewPane(of tab: WorkspaceTab) -> String? {
        model.shownAgentPane(of: tab)?.id ?? (model.panes[tab.focusedPaneID] != nil ? tab.focusedPaneID : model.panes(in: tab).first?.id)
    }

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
                                // Red, but not .destructive: that role takes the row away at
                                // once, before it's asked whether to.
                                Button("Close", systemImage: "xmark") { requestCloseTab(model, tab) { closingTab = $0 } }.tint(.red)
                                Button("Rename", systemImage: "pencil") { renaming = tab }.tint(.orange)
                            }
                            // Touch and hold: the tab's pane, large and live (tap it to open the tab).
                            .modifier(HoldMenu(model: model, paneID: previewPane(of: tab), open: { path = [tab.id] }) {
                                TabActions.entries(model: model, tab: tab, rename: { renaming = tab }, newTab: group.canAddTabs ? { newTab(in: group) } : nil,
                                                   newFilesTab: group.canAddTabs ? { newTab(in: group, files: true) } : nil,
                                                   askRemove: { removingWorktree = $0 }, askClose: { closingTab = $0 })
                            })
                        }
                        .onMove { from, to in
                            guard let i = from.first, i < tabs.count else { return }
                            // SwiftUI's destination counts the moved row; the daemon's index doesn't.
                            model.moveTab(tabs[i].id, to: group.id, index: to > i ? to - 1 : to)
                        }
                    } header: {
                        GroupHeader(model: model, group: group, collapsed: isCollapsed, toggle: { toggle(group) },
                                    newTab: { newTab(in: group) }, edit: { editing = group },
                                    // An empty Group has nothing to lose: no asking.
                                    delete: { if group.tabIds.isEmpty { Task { await model.closeGroup(group.id) } } else { deleting = group } },
                                    leave: { leaving = group })
                    }
                }
            }
            .refreshable { await model.refresh() }
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
            .closeTabConfirmation(model: model, tab: $closingTab)
            .removeWorktreeConfirmation(model: model, tab: $removingWorktree)
            .sheet(item: $editing) { GroupEditor(model: model, group: $0) }
            .sheet(isPresented: $creatingGroup) {
                GroupEditor(model: model, group: nil) { tabID in path = [tabID] }
            }
            .confirmationDialog(deleting.map { "Delete “\(model.title(of: $0))”?" } ?? "",
                                isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }),
                                titleVisibility: .visible, presenting: deleting) { g in
                Button("Delete Group and Close \(g.tabIds.count == 1 ? "1 Tab" : "\(g.tabIds.count) Tabs")", role: .destructive) {
                    Task { await model.closeGroup(g.id) }
                }
            } message: { _ in
                Text("Everything running in its tabs on your Mac is stopped.")
            }
            .confirmationDialog(leaving.map { "Leave “\(model.title(of: $0))”?" } ?? "",
                                isPresented: Binding(get: { leaving != nil }, set: { if !$0 { leaving = nil } }),
                                titleVisibility: .visible, presenting: leaving) { g in
                Button("Leave Group", role: .destructive) { Task { await model.leave(g.id) } }
            } message: { _ in
                Text("To come back, join it again from its invite link.")
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

    private func newTab(in group: WorkspaceGroup, files: Bool = false) {
        Task { if let id = files ? await model.openFiles(groupID: group.id) : await model.createTab(in: group.id) { path = [id] } }
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
    /// What the list had open, while it slides away after going back (on
    /// an iPhone, where the split view is one column).
    @State private var leaving: String?
    @Environment(\.horizontalSizeClass) private var hSize

    var body: some View {
        NavigationSplitView {
            sidebar
                // As it's always looked on the iPhone (a sidebar's own style
                // is the default in a split view).
                .listStyle(.insetGrouped)
                .themedScreenBackground()
                .navigationTitle(session.host?.hostName.replacingOccurrences(of: ".local", with: "") ?? "Hammock")
                .connectionBar()
                .overlay {
                    if model.groups.isEmpty {
                        if model.everConnected {
                            ContentUnavailableView("No Groups", systemImage: "square.stack.3d.up",
                                                   description: Text("Create one in Groups, or on your Mac."))
                        } else {
                            // Still loading: the connection bar says from where, once it's slow.
                            ProgressView()
                                .controlSize(.large)
                                .accessibilityLabel("Loading")
                        }
                    }
                }
                // About half an iPhone Duo's inner display: the list on one
                // side of the fold, what it opened on the other.
                .navigationSplitViewColumnWidth(min: 320, ideal: 400, max: 480)
        } detail: {
            NavigationStack(path: Binding(get: { Array(path.dropFirst()) }, set: { path = Array(path.prefix(1)) + $0 })) {
                Group {
                    // Going back, what was open stays as it slides away: the
                    // placeholder came in its place, said on the way out.
                    if let shown = path.first ?? (hSize == .compact ? leaving : nil) {
                        CockpitDestination(model: model, id: shown, path: $path).id(shown)
                            .environment(\.screenLeaving, path.first == nil)
                    } else if !model.groups.isEmpty {
                        ContentUnavailableView(placeholder, systemImage: "sidebar.left")
                    }
                }
                .onChange(of: path.first) { old, new in
                    guard new == nil, let old else { leaving = nil; return }
                    leaving = old
                    Task { @MainActor in
                        try? await Task.sleep(for: .milliseconds(700))
                        if path.first == nil, leaving == old { leaving = nil }
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
        screen.connectionBar()
    }

    @ViewBuilder private var screen: some View {
        if id == ScheduleRoute.list {
            ScheduledTasksScreen(model: model, path: $path)
        } else if let s = ScheduleRoute.scheduleID(id) {
            ScheduleDetailScreen(model: model, scheduleID: s, path: $path)
        } else if let pane = model.panes[id] {
            // A pane of a tab of several opens in its tab, with the strip.
            if let tab = model.tab(of: pane), model.panes(in: tab).count > 1 {
                TabScreen(model: model, tabID: tab.id, path: $path, start: id)
            } else {
                PaneScreen(model: model, paneID: id, path: $path)
            }
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
    /// At rest with subagents still running: the dot is indigo.
    var subagents = false
    /// With no agent state: the program running.
    var program: [String] = []
    /// Its agent asleep: a moon.
    var asleep = false
    /// Working, how far along (OSC 7501): a ring filling.
    var progress: Int?
    /// Waiting out its usage limit: a clock.
    var waiting = false
    var body: some View {
        StatusIcon(state: state, resting: resting, shells: shells, subagents: subagents, size: 17, program: program, asleep: asleep, progress: progress, waiting: waiting)
    }
}

struct TabRowIOS: View {
    let model: AppModel
    let tab: WorkspaceTab
    var body: some View {
        HStack {
            StateGlyph(state: model.agentState(of: tab), resting: model.hasRestingAgent(tab), shells: model.hasRestingAgentWithWork(tab), subagents: model.hasRestingAgentWithSubagents(tab),
                       program: model.focusedProgram(of: tab), asleep: model.hasSleepingAgent(tab), progress: model.agentProgress(of: tab),
                       waiting: model.hasAgentWaitingOnLimit(tab))
                .staleWhenLinkDown()
            VStack(alignment: .leading) {
                HStack(spacing: 5) {
                    Text(model.title(of: tab))
                    // Its branch's pull request, or a worktree's branch.
                    if tab.hasGit, tab.git.shown {
                        GitMark(tab.git, size: 11)
                    }
                    // A scheduled task's run.
                    if let s = model.schedule(of: tab) {
                        Image(systemName: "clock")
                            .font(.caption.weight(.medium))
                            .foregroundStyle(.tertiary)
                            .accessibilityLabel("Automation run of \(s.name)")
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
    /// Allow and Deny wait for the Mac (the connection bar says why).
    @Environment(\.linkDown) private var linkDown

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
                            Text("Automation · \(s.name)")
                        }
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    Text(item.summary.isEmpty ? item.state.label : item.summary).font(.subheadline).foregroundStyle(.secondary)
                }
            }
            if let preview = item.promptPreview {
                PromptPreviewView(preview, font: .footnote)
            }
            if item.state == .needsInput, item.question {
                HStack {
                    Spacer()
                    Button("Answer") { openPane() }.prominentButton()
                }
            } else if item.state == .needsInput {
                HStack {
                    Button("Deny") { answer(false) }.buttonStyle(.bordered).disabled(linkDown)
                    if item.allowsFromCard {
                        Button("Open") { openPane() }.buttonStyle(.bordered)
                    }
                    Spacer()
                    if !item.allowsFromCard {
                        // More than the card shows: see all of it in the pane first.
                        Button("Review") { openPane() }
                            .prominentButton()
                            .accessibilityHint("Opens the pane to see the whole request")
                    } else if let s = schedule, s.kind == "claude" {
                        // A scheduled Claude run: allow it in every run from now on.
                        Menu {
                            Button("Allow Once", systemImage: "checkmark") { answer(true) }
                            Button("Always Allow for This Automation", systemImage: "clock.badge.checkmark") {
                                Haptics.success()
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
                        .accessibilityHint("Hold for Always Allow for This Automation")
                        .disabled(linkDown)
                    } else {
                        Button("Allow") { answer(true) }.prominentButton().disabled(linkDown)
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
    private func answer(_ allow: Bool) {
        allow ? Haptics.success() : Haptics.refuse()
        withAnimation { model.decide(item, allow: allow) }
    }
}

/// Someone asking to join a Group you share (approve from the phone).
struct JoinRow: View {
    let model: AppModel
    let request: JoinRequest
    @Environment(\.linkDown) private var linkDown

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
                Button("Decline") { Haptics.refuse(); withAnimation { model.respond(to: request, role: nil) } }.buttonStyle(.bordered)
                Spacer()
                Button("Let View") { Haptics.success(); withAnimation { model.respond(to: request, role: .viewer) } }.buttonStyle(.bordered)
                Button("Let Drive") { Haptics.success(); withAnimation { model.respond(to: request, role: .driver) } }.prominentButton()
            }
            .disabled(linkDown)
        }
        .padding(.vertical, 4)
    }
}

struct GrantRow: View {
    let model: AppModel
    let grant: GrantRequest
    /// Opens the pane that asked (tapping the row).
    var open: ((String) -> Void)?
    @Environment(\.linkDown) private var linkDown
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // The palette on the icon only: on the Label it made the text
            // white too, invisible on the card.
            Label {
                Text(grant.titleText).font(.headline)
                    .lineLimit(grant.showsWholeHeadline ? nil : 4)
            } icon: {
                Image(systemName: "hand.raised.circle.fill")
                    .symbolRenderingMode(.palette)
                    .foregroundStyle(.white, .orange)
            }
            if grant.hasDetails {
                GrantDetailsView(grant.details, compact: true, font: .subheadline, codeFont: .system(.caption, design: .monospaced))
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
                Button("Deny") { Haptics.refuse(); withAnimation { model.respond(to: grant, .deny) } }.buttonStyle(.bordered)
                Spacer()
                if grant.canAllowAlways {
                    Button("Always") { Haptics.success(); withAnimation { model.respond(to: grant, .allowGroup) } }.buttonStyle(.bordered)
                }
                Button("Allow") { Haptics.success(); withAnimation { model.respond(to: grant, .allowOnce) } }.prominentButton()
            }
            .disabled(linkDown)
        }
        .contentShape(Rectangle())
        .onTapGesture {
            if let open, model.panes[grant.requesterPaneID] != nil { open(grant.requesterPaneID) }
        }
        .padding(.vertical, 4)
    }
}

/// A tab: its one pane as a whole screen, or with several one of them at a
/// time (the one last shown here, else the first), a strip of pills along
/// the bottom to switch, and Exposé to pick one from the tab's layout.
/// Swiping sideways goes to the next or previous pane, in panes that
/// don't use sideways drags themselves (not a simulator, a browser or the
/// editor). Only the pane showing is loaded: the others stream nothing.
struct TabScreen: View {
    let model: AppModel
    let tabID: String
    @Binding var path: [String]
    /// The pane to show first (opened from a notification or a session).
    var start: String? = nil
    @State private var current = ""
    /// The pane a swipe (or a tap on a pill) is bringing in, and how far
    /// the panes have moved: the finger's drag, then the settling.
    @State private var incoming: String?
    @State private var offset: CGFloat = 0
    @State private var pageWidth: CGFloat = 390
    /// The swipe under way is sideways (nil: not decided yet).
    @State private var sideways: Bool?
    /// Where panes take their own drags (PaneSwipeExclusion).
    @State private var ownDrags: [CGRect] = []
    /// A pane showing (or coming in) has nothing to show yet (PaneLoading).
    @State private var loading = false
    /// The pane a tap on its pill is going to: marked in the strip at once.
    @State private var tapped: String?
    /// The pane settling in, which has the bar (its buttons, its … menu)
    /// from the moment it starts to: changed in the settling animation, so
    /// iOS morphs the bar's buttons from one pane's to the other's as they
    /// slide (changed after it, with no animation, they jumped).
    @State private var barPane: String?

    /// How far the slide to the incoming pane has got, 0 to 1.
    private var progress: Double {
        pageWidth > 0 && incoming != nil ? min(abs(offset) / pageWidth, 1) : 0
    }

    /// How selected a pill shows: the pane a tap is going to at once; during
    /// a swipe, fading from the pane on screen to the one coming in.
    private func selection(_ id: String, showing: String) -> Double {
        if let tapped { return id == tapped ? 1 : 0 }
        if id == showing { return 1 - progress }
        return id == incoming ? progress : 0
    }
    @State private var exposing = false
    @State private var closing: Pane?
    @State private var keyboardUp = false
    @State private var shown = false
    @Namespace private var namespace
    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let tab = model.tabs[tabID]
        let ids = tab.map(stripPaneIDs) ?? []
        Group {
            if let tab, ids.count > 1 {
                let showing = ids.contains(current) ? current : ids[0]
                // The pane on screen, and while a swipe brings one in, that
                // one beside it, following the finger. Only the panes slide:
                // the strip stays put and moves its mark.
                GeometryReader { geo in
                    let width = geo.size.width
                    ZStack {
                        ForEach([showing] + (incoming.map { [$0] } ?? []).filter { $0 != showing && ids.contains($0) }, id: \.self) { id in
                            PaneScreen(model: model, paneID: id, path: $path, asTab: tabID, inStrip: true, chrome: id == (barPane ?? showing))
                                .offset(x: side(of: id, from: showing, in: ids) * width + offset)
                                .allowsHitTesting(id == showing)
                        }
                    }
                    .frame(width: width, height: geo.size.height)
                    .clipped()
                    .contentShape(Rectangle())
                    .simultaneousGesture(swipe(ids, from: showing, width: width))
                    .onAppear { pageWidth = width }
                    .onChange(of: width) { _, w in pageWidth = w }
                }
                    // The title fades from one pane's to the next's as they slide.
                    .toolbar {
                        ToolbarItem(placement: .principal) {
                            ZStack {
                                StripTitle(model: model, paneID: showing).opacity(1 - progress)
                                if let incoming, incoming != showing { StripTitle(model: model, paneID: incoming).opacity(progress) }
                            }
                        }
                    }
                    .onPreferenceChange(PaneSwipeExclusion.self) { ownDrags = $0 }
                    .onPreferenceChange(PaneLoading.self) { loading = $0 }
                    .backFromEdgeOnly()
                    // The keyboard covers the strip rather than lifting it.
                    .safeAreaInset(edge: .bottom, spacing: 0) {
                        if !keyboardUp {
                            PaneStrip(model: model, tab: tab, current: tapped ?? showing, selected: { selection($0, showing: showing) },
                                      namespace: namespace, select: { go(to: $0, in: ids) }, expose: { exposing = true }, confirmClose: { closing = $0 })
                        }
                    }
                    .fullScreenCover(isPresented: $exposing) {
                        ExposeScreen(model: model, tabID: tabID, current: showing, open: { go(to: $0, in: ids, animated: false) },
                                     confirmClose: { closing = $0 })
                            .navigationTransition(.zoom(sourceID: ExposeScreen.transitionID, in: namespace))
                    }
            } else if let p = ids.first {
                PaneScreen(model: model, paneID: p, path: $path, asTab: tabID)
            }
        }
        .confirmationDialog(closing.map { "Close \($0.displayName)?" } ?? "", isPresented: Binding(get: { closing != nil }, set: { if !$0 { closing = nil } }),
                            titleVisibility: .visible, presenting: closing) { p in
            Button("Close Pane", role: .destructive) { Task { await model.closePane(p.id) } }
        } message: { p in
            Text(closeWarning(p) ?? "")
        }
        .onAppear {
            guard current.isEmpty else { return }
            let first = [start, TabPanes.last(tabID)].compactMap { $0 }.first { ids.contains($0) }
            current = first ?? ids.first ?? ""
            #if DEBUG
            // Dev aid: HMK_DEBUG_EXPOSE=1 opens Exposé over a tab of several panes as it opens.
            if ProcessInfo.processInfo.environment["HMK_DEBUG_EXPOSE"] == "1", ids.count > 1 {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.6) { exposing = true }
            }
            #endif
        }
        .onChange(of: current) { _, id in if !id.isEmpty { TabPanes.remember(id, in: tabID) } }
        .onChange(of: PaneShowRequest.shared.paneID) { showRequested(ids) }
        .onChange(of: ids) { showRequested(ids) }
        .demoStripSteps { verb, id in
            switch verb {
            case "pane": go(to: id, in: ids)
            case "expose": exposing = true
            case "pick":
                go(to: id, in: ids, animated: false)
                exposing = false
            default: break
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillShowNotification)) { _ in keyboardUp = true }
        .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillHideNotification)) { _ in keyboardUp = false }
        // Closed (here or on the Mac): back to the list. A new tab can open
        // before its state arrives, so only once it's been seen.
        .onChange(of: tab == nil, initial: true) { _, gone in
            if !gone { shown = true } else if shown { dismiss() }
        }
    }

    /// Which side of the pane on screen a pane sits: after it in the
    /// strip's order to the right (1), before it to the left (-1).
    private func side(of id: String, from showing: String, in ids: [String]) -> CGFloat {
        guard id != showing, let a = ids.firstIndex(of: id), let b = ids.firstIndex(of: showing) else { return 0 }
        return a > b ? 1 : -1
    }

    /// A sideways drag moves the panes with the finger, the next one (or
    /// the previous) coming in beside; let go past a third of the way, or
    /// flicked, it settles there, else it goes back. Drags that start at
    /// the left edge (the system's Back) or where a pane takes its own
    /// (a simulator's screen, a browser's page), or go mostly up and down,
    /// are left to them.
    private func swipe(_ ids: [String], from id: String, width: CGFloat) -> some Gesture {
        DragGesture(minimumDistance: 10, coordinateSpace: .global)
            .onChanged { v in
                let dx = v.translation.width, dy = v.translation.height
                if sideways == nil {
                    guard abs(dx) > 8 || abs(dy) > 8 else { return }
                    sideways = abs(dx) > abs(dy) * 1.2 && v.startLocation.x > 24
                        && !ownDrags.contains { $0.contains(v.startLocation) }
                    if sideways == true {
                        tapped = nil
                        // The keyboard belongs to the pane it was typing into.
                        UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
                    }
                }
                guard sideways == true, let i = ids.firstIndex(of: id) else { return }
                let next = dx < 0 ? i + 1 : i - 1
                if ids.indices.contains(next) {
                    incoming = ids[next]
                    offset = dx
                } else {
                    // No pane that way: it gives, and comes back.
                    incoming = nil
                    offset = dx / 4
                }
            }
            .onEnded { v in
                defer { sideways = nil }
                guard sideways == true else { return }
                let dx = v.translation.width, flick = v.predictedEndTranslation.width
                if let target = incoming, abs(dx) > width / 3 || (abs(flick) > width / 2 && (flick < 0) == (dx < 0)) {
                    settle(on: target, width: width, direction: dx < 0 ? -1 : 1)
                } else {
                    withAnimation(.snappy(duration: 0.25)) { offset = 0 } completion: { if sideways == nil { incoming = nil } }
                }
            }
    }

    /// Slides the incoming pane the rest of the way in, then makes it the
    /// one on screen (in place: the view moves over as it is).
    private func settle(on target: String, width: CGFloat, direction: CGFloat) {
        withAnimation(.snappy(duration: 0.28)) {
            incoming = target
            offset = direction * width
            barPane = target
        } completion: {
            var t = Transaction()
            t.disablesAnimations = true
            withTransaction(t) {
                current = target
                incoming = nil
                offset = 0
                tapped = nil
                barPane = nil
            }
        }
    }

    /// Slides to the pane asked for (PaneShowRequest), once it's here.
    private func showRequested(_ ids: [String]) {
        guard let want = PaneShowRequest.shared.paneID, ids.contains(want) else { return }
        PaneShowRequest.shared.paneID = nil
        go(to: want, in: ids)
    }

    /// To a pane from its pill (or a take): slid in from its side.
    private func go(to id: String, in ids: [String], animated: Bool = true) {
        guard id != current, ids.contains(id), let to = ids.firstIndex(of: id) else { return }
        UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
        guard animated, !reduceMotion, sideways == nil else {
            current = id
            incoming = nil
            offset = 0
            tapped = nil
            return
        }
        let from = ids.firstIndex(of: current) ?? 0
        // Its pill marked at once, not when it's slid in.
        withAnimation(.easeOut(duration: 0.15)) { tapped = id }
        // As a swipe does it: the pane in place off screen on its side
        // first, without an animation, then both slid. (Animating its
        // arrival instead, it showed in place at once: its glass, the
        // agent view's message box, doesn't follow a transition.) A moment
        // between lets it draw before it moves, and one still waiting on
        // its first picture (a simulator's, a browser's) gets up to half a
        // second for it, so it slides in showing something.
        incoming = id
        Task { @MainActor in
            try? await Task.sleep(for: .milliseconds(50))
            let until = Date.now.addingTimeInterval(0.5)
            while loading, Date.now < until, incoming == id {
                try? await Task.sleep(for: .milliseconds(25))
            }
            guard incoming == id, sideways == nil else {
                if tapped == id { tapped = nil }
                return
            }
            settle(on: id, width: pageWidth, direction: to > from ? -1 : 1)
        }
    }
}

extension View {
    /// The demo's takes switching panes and opening Exposé (DemoDirector's
    /// pane, expose and pick steps). Nothing outside a debug build.
    @ViewBuilder func demoStripSteps(_ act: @escaping (String, String) -> Void) -> some View {
        #if DEBUG
        onReceive(NotificationCenter.default.publisher(for: DemoDirector.strip)) { n in
            act(n.userInfo?["do"] as? String ?? "", n.userInfo?["pane"] as? String ?? "")
        }
        #else
        self
        #endif
    }
}

/// Opens a new tab in a tab's place (back goes to the list, not that tab).
@MainActor
private func newTab(in groupID: String, model: AppModel, path: Binding<[String]>, replacing: String, files: Bool = false) {
    Task {
        guard let id = files ? await model.openFiles(groupID: groupID) : await model.createTab(in: groupID) else { return }
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
    /// One of its tab's several, with the pane strip (TabScreen): titled
    /// with its own name, the tab's under it.
    var inStrip = false
    /// On screen, not a neighbour a swipe is bringing in: titles the bar,
    /// shows the toolbar, and counts as looked at.
    var chrome = true
    @State private var showsAgent: Bool?
    /// Which views have been shown (each is kept once it has).
    @State private var shownAgent = false
    @State private var shownTerminal = false
    @State private var preview: FilePreview?
    @State private var renaming: WorkspaceTab?
    @State private var closingTab: WorkspaceTab?
    @State private var removingWorktree: WorkspaceTab?
    @State private var shown = false
    @Environment(\.dismiss) private var dismiss
    /// A log pane showing its terminal instead of the log.
    @State private var logRaw = false
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.horizontalSizeClass) private var hSize
    @Environment(\.phoneTheme) private var theme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var pane: Pane? { model.panes[paneID] }
    private var asleep: Bool { pane?.asleep == true }
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
        if inStrip || switchesView { return stripName(pane) }
        if asTab != nil || model.panes(in: tab).count < 2 { return model.title(of: tab) }
        if pane.kind == .browser || pane.kind == .simulator || pane.kind == .editor || pane.kind == .diff || pane.kind == .files { return pane.title.isEmpty ? model.title(of: tab) : pane.title }
        return pane.displayName
    }

    var body: some View {
        Group {
            if let pane {
                PaneContent(model: model, pane: pane, active: VideoGrace.shared.live, canDrive: canDrive(pane), logRaw: logRaw,
                            agentMode: agentMode, agentView: model.supportsAgentView(pane),
                            shownAgent: $shownAgent, shownTerminal: $shownTerminal,
                            showTerminal: { setMode(false) }, openFile: { preview = FilePreview(paneID: pane.id, path: $0) })
                    .equatable()
            }
        }
        // Its agent stopped at its usage limit: when it continues, with Cancel.
        .safeAreaInset(edge: .top, spacing: 0) {
            if let pane, pane.limitResumeLabel() != nil {
                LimitResumeBar(model: model, pane: pane, canCancel: canDrive(pane))
                    .transition(.move(edge: .top).combined(with: .opacity))
            }
        }
        // Its agent asleep: faded, with a button to wake it (opening it
        // doesn't; typing in it or sending its agent a message does).
        .overlay {
            if asleep {
                SleepingPaneOverlay(backdrop: theme.terminalBackground, fill: theme.bubble, tint: theme.accent) {
                    model.sleepPane(paneID, wake: true)
                }
                .transition(.opacity)
            }
        }
        .animation(reduceMotion ? nil : .easeInOut(duration: 0.25), value: asleep)
        // Still while swiped back (with the keyboard up, it scrolled down
        // as the swipe went).
        .steadyWhilePopped()
        // A session gets the whole screen, unless the list is beside it (a
        // wide screen: iPhone Duo open, an iPad), where the tabs stay.
        .hidesTabBarWhenPushed(hSize == .compact)
        .toolbar {
            // One … menu, at the right, with which view an agent's or a
            // log's pane shows (viewSwitch): the editor and the simulator
            // draw their own, with the tab's actions in it (paneTabMenu).
            if chrome, !ownsMenu, let tab = pane.flatMap({ model.tab(of: $0) }) {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        viewSwitch
                        tabActions(tab)
                    } label: {
                        Image(systemName: "ellipsis.circle")
                    }
                    .accessibilityLabel("Tab Actions")
                }
            }
        }
        .environment(\.paneTabMenu, tabMenu)
        .renameTabAlert(model: model, tab: $renaming)
        .closeTabConfirmation(model: model, tab: $closingTab)
        .removeWorktreeConfirmation(model: model, tab: $removingWorktree)
        .sheet(item: $preview) { FilePreviewSheet(model: model, preview: $0) }
        // Only the pane on screen titles the bar: not a neighbour a swipe
        // is bringing in (from a view of its own, so that changing doesn't
        // rebuild the pane).
        .background {
            if chrome {
                // With the strip, TabScreen draws the title and the tab's
                // name under it (faded between panes as they slide): this
                // one is for the Back button's menu and VoiceOver. On its
                // own, an agent's or a log's pane says so the same way.
                Color.clear
                    .navigationTitle(title)
                    .navigationSubtitle(!inStrip && switchesView ? pane.flatMap { model.tab(of: $0) }.map { model.title(of: $0) } ?? "" : "")
            }
        }
        .navigationBarTitleDisplayMode(.inline)
        .environment(\.inPaneStrip, inStrip)
        .environment(\.paneChrome, chrome)
        // Closed (here or on the Mac): back to where it was opened from.
        .onChange(of: pane == nil, initial: true) { _, gone in
            if !gone { shown = true } else if shown, asTab == nil { dismiss() }
        }
        // Looking at a finished agent acknowledges it, as on the Mac: when
        // you open it (or swipe to it), and when it finishes while you're
        // watching. A neighbour peeking in during a swipe isn't looked at.
        .onAppear { if chrome, !inStrip { cameOnScreen() } }
        // Swiped to: once it's slid in, so what looking at it changes (the
        // Mac's focus, its finished state) doesn't update the app mid-slide.
        .task(id: chrome) {
            guard chrome, inStrip else { return }
            try? await Task.sleep(for: .milliseconds(350))
            if !Task.isCancelled { cameOnScreen() }
        }
        .onChange(of: chrome) { _, on in if on, !inStrip { cameOnScreen() } }
        .onDisappear { if Push.shared.visiblePane?.pane == paneID { Push.shared.visiblePane = nil } }
        .onChange(of: unseenFinish) { _, unseen in if unseen, chrome, scenePhase == .active { model.focus(paneID) } }
        .onChange(of: scenePhase) { _, phase in if phase == .active, chrome, unseenFinish { model.focus(paneID) } }
    }

    /// An agent's pane (conversation or terminal) or a log's (log or
    /// terminal): titled with its own name, the tab's under it, and which
    /// view shows chosen in the … menu, however many panes its tab has.
    private var switchesView: Bool {
        guard let pane else { return false }
        return pane.isLog || model.supportsAgentView(pane)
    }

    /// The editor and the simulator have a … menu of their own.
    private var ownsMenu: Bool { pane?.kind == .editor || pane?.kind == .simulator }

    /// The tab's actions for their menu (paneTabMenu).
    private var tabMenu: AnyView? {
        guard let tab = pane.flatMap({ model.tab(of: $0) }) else { return nil }
        return AnyView(Section("Tab") { tabActions(tab) })
    }

    @ViewBuilder private func tabActions(_ tab: WorkspaceTab) -> some View {
        TabActions(model: model, tab: tab, rename: { renaming = tab },
                   newTab: model.group(of: tab)?.canAddTabs == false ? nil : { newTab(in: tab.groupID, model: model, path: $path, replacing: asTab ?? paneID) },
                   newFilesTab: model.group(of: tab)?.canAddTabs == false ? nil : {
                       newTab(in: tab.groupID, model: model, path: $path, replacing: asTab ?? paneID, files: true)
                   },
                   closing: $closingTab, removing: $removingWorktree)
    }

    /// With the strip: which view an agent's (or a log's) pane shows, as a
    /// choice in the … menu, where the bar's switch is otherwise.
    @ViewBuilder private var viewSwitch: some View {
        if let pane, model.supportsAgentView(pane), !pane.isLog {
            Picker("View", selection: Binding(get: { agentMode }, set: { setMode($0) })) {
                Label("Conversation", systemImage: "text.bubble").tag(true)
                Label("Terminal", systemImage: "apple.terminal").tag(false)
            }
            .pickerStyle(.inline)
        } else if let pane, pane.isLog {
            Picker("View", selection: $logRaw) {
                Label("Log", systemImage: "list.bullet.rectangle").tag(false)
                Label("Terminal", systemImage: "apple.terminal").tag(true)
            }
            .pickerStyle(.inline)
        }
    }

    private func cameOnScreen() {
        model.focus(paneID)
        // Its own notifications aren't shown while it's on screen (Push).
        Push.shared.visiblePane = (Push.shared.session?.host?.id ?? "", paneID)
    }

    /// The pane has an agent that finished and hasn't been looked at.
    private var unseenFinish: Bool {
        guard let a = pane.flatMap({ $0.hasAgent ? $0.agent : nil }) else { return false }
        return a.state == .idle && !a.seen
    }
}

/// What a pane shows: its terminal or agent view, a browser, a simulator,
/// the editor or a diff. Compared by what decides that, so PaneScreen's
/// bar changing hands as a swipe settles (`chrome`) doesn't rebuild it: a
/// long conversation took longer than the slide, which then jumped to its
/// end. Its views follow the pane themselves, through the model.
private struct PaneContent: View, Equatable {
    let model: AppModel
    let pane: Pane
    let active: Bool
    let canDrive: Bool
    let logRaw: Bool
    let agentMode: Bool
    let agentView: Bool
    @Binding var shownAgent: Bool
    @Binding var shownTerminal: Bool
    let showTerminal: () -> Void
    let openFile: (String) -> Void

    nonisolated static func == (a: PaneContent, b: PaneContent) -> Bool {
        MainActor.assumeIsolated {
            a.pane.id == b.pane.id && a.pane.kind == b.pane.kind && a.pane.isLog == b.pane.isLog && a.active == b.active
                && a.canDrive == b.canDrive && a.logRaw == b.logRaw && a.agentMode == b.agentMode && a.agentView == b.agentView
                && a.shownAgent == b.shownAgent && a.shownTerminal == b.shownTerminal
        }
    }

    var body: some View {
        if pane.kind == .browser {
            BrowserScreen(model: model, paneID: pane.id, active: active, canDrive: canDrive)
        } else if pane.kind == .simulator {
            SimulatorScreen(model: model, paneID: pane.id, active: active, canDrive: canDrive)
        } else if pane.kind == .editor {
            EditorScreen(model: model, paneID: pane.id, canDrive: canDrive)
        } else if pane.kind == .diff {
            DiffScreen(model: model, paneID: pane.id, canDrive: canDrive)
        } else if pane.kind == .files {
            FilesScreen(model: model, paneID: pane.id, canDrive: canDrive)
        } else if pane.isLog {
            if logRaw {
                TerminalScreen(model: model, paneID: pane.id, active: true)
            } else {
                LogConsoleView(model: model, paneID: pane.id)
            }
        } else if agentView {
            // Both kept once shown, the other hidden: switching only
            // changes which is visible, so nothing is rebuilt (the
            // toolbar and its switch included, whose slide a rebuild
            // cut short), and switching back is instant.
            ZStack {
                if shownAgent || agentMode {
                    AgentConversationView(model: model, paneID: pane.id, onShowTerminal: showTerminal, onOpenFile: openFile)
                        .opacity(agentMode ? 1 : 0)
                        .allowsHitTesting(agentMode)
                        .accessibilityHidden(!agentMode)
                        .onAppear { shownAgent = true }
                }
                if shownTerminal || !agentMode {
                    TerminalScreen(model: model, paneID: pane.id, active: !agentMode)
                        // Hammock asking you on its agent's behalf: the whole request.
                        .overlay { GrantPromptOverlay(model: model, paneID: pane.id) }
                        .opacity(agentMode ? 0 : 1)
                        .allowsHitTesting(!agentMode)
                        .accessibilityHidden(agentMode)
                        .onAppear { shownTerminal = true }
                }
            }
        } else {
            TerminalScreen(model: model, paneID: pane.id, active: true)
                // hmk run in its shell, asking you.
                .overlay { GrantPromptOverlay(model: model, paneID: pane.id) }
        }
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
