import AppKit
import HammockKit
import HammockUpdates
import SwiftUI

/// ⌘K: a Spotlight-style palette over the whole window to jump to any tab or
/// Group and run any Hammock action. Deliberately playful: it springs in,
/// its sparkle icon bounces, and the chosen row flashes before it springs
/// away.
@MainActor
@Observable
final class PaletteState {
    static let shared = PaletteState()
    /// What it's showing: ⌘K's commands and tabs, or ⌘⇧O's session finder
    /// (SessionFinder.swift). Both are "the palette" to the views that stay
    /// out of its way while it's open.
    enum Mode { case commands, sessions }
    /// HMK_DEBUG_PALETTE opens it at launch (for screenshots), and
    /// HMK_DEBUG_SESSIONS the finder.
    var isOpen = ProcessInfo.processInfo.environment["HMK_DEBUG_PALETTE"] != nil || ProcessInfo.processInfo.environment["HMK_DEBUG_SESSIONS"] != nil
    var mode: Mode = ProcessInfo.processInfo.environment["HMK_DEBUG_SESSIONS"] != nil ? .sessions : .commands
    /// The Group a session resumes into when it has none in its folder (a
    /// Group's "Resume a Session…"); else the selected tab's.
    var sessionGroupID: String?
    /// A linked computer whose sessions the finder shows (Find Session on…).
    var sessionComputerID = ""
    /// The finder shows only the sessions in the folders open in
    /// sessionGroupID (a Group's "Resume a Session…"), until it's cleared.
    var sessionsInGroup = false
    /// The window of its own it opened in (DetachedWindows), or nil: the main window's.
    var window: UUID?
    func toggle() { open(.commands) }
    /// Opens in mode (for group or computer), or closes if it's already
    /// showing just that; asked for another computer's or Group's sessions,
    /// it switches to them. inGroup: only the Group's folders' sessions.
    func open(_ m: Mode, group: String? = nil, computer: String = "", inGroup: Bool = false) {
        if isOpen && mode == m && sessionGroupID == group && sessionComputerID == computer && sessionsInGroup == inGroup {
            isOpen = false
            return
        }
        mode = m
        sessionGroupID = group
        sessionComputerID = computer
        sessionsInGroup = inGroup && group != nil
        window = DetachedWindows.shared.keyScope?.id
        isOpen = true
    }
}

struct PaletteItem: Identifiable {
    enum Icon { case symbol(String, Color), emoji(String), picture(NSImage), program([String]), agent(String) }
    let id: String
    let title: String
    var subtitle = ""
    let icon: Icon
    var badge: AgentState?
    var keywords = ""
    /// Its keyboard shortcut, shown on the right so it can be learnt here.
    var shortcut: KeyboardShortcut?
    let run: () -> Void
}

struct CommandPalette: View {
    let model: AppModel
    /// HMK_DEBUG_PALETTE=<text> (not 1) opens it searched for text, for screenshots.
    @State private var query: String = {
        let v = ProcessInfo.processInfo.environment["HMK_DEBUG_PALETTE"] ?? ""
        return v == "1" ? "" : v
    }()
    @State private var selection = 0
    @State private var appeared = false
    @State private var burst = 0
    @State private var confirming: String?
    @FocusState private var fieldFocused: Bool
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.openSettings) private var openSettings

    private var state: PaletteState { .shared }

    var body: some View {
        let results = filtered
        ZStack(alignment: .top) {
            // Dim the window; a click outside closes.
            Color.black.opacity(appeared ? 0.18 : 0)
                .ignoresSafeArea()
                .onTapGesture { close() }

            VStack(spacing: 0) {
                HStack(spacing: 10) {
                    Image(systemName: "sparkles")
                        .font(.title2)
                        .foregroundStyle(.linearGradient(colors: [.pink, .orange, .yellow], startPoint: .topLeading, endPoint: .bottomTrailing))
                        .symbolEffect(.bounce, value: burst)
                    // A darker prompt than the default placeholder, which washes out
                    // over the translucent background.
                    TextField("Search", text: $query, prompt: Text("Jump to a tab, or run a command…").foregroundStyle(.secondary))
                        .textFieldStyle(.plain)
                        .font(.system(size: 20))
                        .focused($fieldFocused)
                        .onSubmit { confirm(results) }
                        .onKeyPress(.downArrow) { move(1, results.count); return .handled }
                        .onKeyPress(.upArrow) { move(-1, results.count); return .handled }
                        .onKeyPress(.escape) { close(); return .handled }
                        .onChange(of: query) { selection = 0 }
                }
                .padding(.horizontal, 18)
                .frame(height: 56)

                let noMatches = results.isEmpty && !query.trimmingCharacters(in: .whitespaces).isEmpty
                Divider().opacity(results.isEmpty && !noMatches ? 0 : 1)

                if noMatches {
                    Text("No matches")
                        .font(.system(size: 13))
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity)
                        .frame(height: 46)
                        .padding(.vertical, 4)
                } else {
                    KeyboardList(items: results, id: \.id, selection: $selection, query: query, maxHeight: 360,
                                 choose: { selection = $0; confirm(results) }) { item, selected in
                        PaletteRow(item: item, selected: selected, flashing: confirming == item.id)
                    }
                    .opacity(results.isEmpty ? 0 : 1)
                }
            }
            .frame(width: 620)
            .background {
                // Light mode: nearly white over the material, for contrast
                // with what's behind (the material alone reads grey).
                RoundedRectangle(cornerRadius: 18).fill(.regularMaterial)
                    .overlay(RoundedRectangle(cornerRadius: 18).fill(Color.white.opacity(colorScheme == .light ? 0.75 : 0)))
            }
            .overlay(RoundedRectangle(cornerRadius: 18).strokeBorder(Color.primary.opacity(0.1), lineWidth: 0.5))
            .shadow(color: .black.opacity(0.25), radius: 30, y: 14)
            .scaleEffect(appeared ? 1 : 0.97)
            .opacity(appeared ? 1 : 0)
            .blur(radius: appeared ? 0 : 2)
            .padding(.top, 90)
        }
        .onAppear {
            DebugDirector.paletteQuery = { query = $0 }
            // The terminal holds the keyboard as an AppKit first responder,
            // which a SwiftUI focus request alone doesn't take over: let it go,
            // then focus the field, again once the palette is laid out.
            NSApp.keyWindow?.makeFirstResponder(nil)
            fieldFocused = true
            DispatchQueue.main.async { fieldFocused = true }
            withAnimation(.snappy(duration: 0.18)) { appeared = true }
            burst += 1
        }
    }

    // MARK: Items

    private var items: [PaletteItem] {
        var out: [PaletteItem] = []
        // Agents waiting on you, then finished ones you haven't seen, then
        // the tabs you opened most recently.
        let tabs = model.paletteOrderedTabs
        // ⌘1…⌘9 pick the tabs of the Group you're in: shown on those rows.
        let here = (model.currentGroup ?? model.groups.first).map { model.tabs(in: $0).map(\.id) } ?? []
        for tab in tabs {
            let group = model.group(of: tab)
            let cwd = model.panes(in: tab).first?.shortCwd ?? ""
            let groupTitle = group.map { ($0.icon.isEmpty ? "" : $0.icon + " ") + model.title(of: $0) } ?? ""
            let browser = model.panes[tab.focusedPaneID].flatMap { $0.kind == .browser ? $0 : nil }
            let detail = browser?.url ?? cwd
            out.append(PaletteItem(id: "tab:" + tab.id, title: model.title(of: tab), subtitle: [groupTitle, detail].filter { !$0.isEmpty }.joined(separator: " · "),
                                   icon: browser != nil ? .symbol("globe", .secondary) : model.hasRestingAgent(tab) ? .symbol("circle", .secondary) : .program(model.focusedProgram(of: tab)), badge: model.agentState(of: tab),
                                   keywords: groupTitle + " " + detail + " " + model.panes(in: tab).map(\.displayName).joined(separator: " "),
                                   shortcut: here.firstIndex(of: tab.id).flatMap(Keys.tab)) {
                model.selectedTabID = tab.id
            })
        }
        for group in model.groups {
            out.append(PaletteItem(id: "group:" + group.id, title: model.title(of: group), subtitle: "Group · \(group.tabIds.count) tab\(group.tabIds.count == 1 ? "" : "s")",
                                   icon: !group.icon.isEmpty ? .emoji(group.icon) : model.groupPicture(group).flatMap(NSImage.init(data:)).map { .picture($0) } ?? .symbol("square.stack.3d.up", Color.themeAccent)) {
                model.selectGroup(group.id) // its tab you were last on, or the empty Group itself
            })
        }
        let focused = model.focusedPaneID ?? model.selectedTab?.focusedPaneID
        let group = model.currentGroup?.id ?? model.groups.first?.id
        let toolsOff = Set(HammockTool.allCases.filter { !$0.on })
        func action(_ id: String, _ title: String, _ symbol: String, _ color: Color, _ keywords: String = "", keys: KeyboardShortcut? = nil, emoji: String? = nil, run: @escaping () -> Void) {
            if let t = Self.tool(ofAction: id), toolsOff.contains(t) { return } // turned off in Settings ▸ Tools
            out.append(PaletteItem(id: "action:" + id, title: title, subtitle: "Command", icon: emoji.map { .emoji($0) } ?? .symbol(symbol, color), keywords: keywords, shortcut: keys, run: run))
        }
        action("new-tab", "New Tab", "plus.square", .blue, "create", keys: Keys.newTab) { if let group { Task { await model.createTab(in: group) } } }
        action("new-browser-tab", "New Browser Tab", "globe", .blue, "create web", keys: Keys.newBrowserTab) { if let group { Task { await model.createBrowserTab(in: group) } } }
        action("reopen-closed", "Reopen Closed Tab or Pane", "arrow.uturn.backward", .blue, "undo close restore", keys: Keys.reopenClosed) { Task { await model.reopenClosed() } }
        if let tab = model.selectedTab {
            action("open-tab-window", "Open Tab in New Window", "macwindow.badge.plus", .blue, "detach pop out separate screen share present mirror window", keys: Keys.openTabInNewWindow) {
                DetachedWindows.shared.open(.tab(tab.id), model: model)
            }
        }
        if let group {
            action("open-group-window", "Open Group in New Window", "macwindow.badge.plus", .blue, "detach pop out separate screen share present mirror window", keys: Keys.openGroupInNewWindow) {
                DetachedWindows.shared.open(.group(group), model: model)
            }
        }
        action("toggle-sidebar", UserDefaults.standard.bool(forKey: SidebarHiding.key) ? "Show Sidebar" : "Hide Sidebar", "sidebar.left", .gray, "hide show toggle peek", keys: Keys.toggleSidebar) { SidebarHiding.toggle() }
        action("editor-wrap", Prefs.shared.bool(.editorWrap) ? "Stop Wrapping Lines in Editors" : "Wrap Lines in Editors", "text.word.spacing", .indigo, "text word soft wrap long lines editor", keys: Keys.editorWrap) { Prefs.shared.toggle(.editorWrap) }
        let defaultAgent = AgentLauncher.shared.defaultAgent(model)
        if let defaultAgent {
            action("new-default-agent-tab", AppModel.defaultAgentTitle(defaultAgent), "sparkles", .orange, "start agent session launch default quick \(defaultAgent)", keys: Keys.newDefaultAgentTab) {
                AgentLauncher.shared.newDefault(model)
            }
        }
        action("new-agent-tab", "New Agent Tab…", "sparkles", .orange, "start claude codex agent session launch", keys: Keys.newAgentTab(defaultAgent != nil)) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { AgentLauncher.shared.open(model) }
        }
        action("new-claude-tab", "New Claude Code Tab…", "sparkle", .orange, "start agent session launch") {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { AgentLauncher.shared.open(model, harness: "claude") }
        }
        action("new-codex-tab", "New Codex Tab…", "chevron.left.forwardslash.chevron.right", .teal, "start agent session launch openai") {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { AgentLauncher.shared.open(model, harness: "codex") }
        }
        // Any other agent the daemon knows and finds installed.
        for k in AgentCatalog.installed where !AgentCatalog.builtIn.contains(k.id) {
            out.append(PaletteItem(id: "action:new-\(k.id)-tab", title: "New \(AgentCatalog.name(of: k.id)) Tab…", subtitle: "Command", icon: .agent(k.id),
                                   keywords: "start agent session launch \(k.id) \(k.shortName)", shortcut: nil) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { AgentLauncher.shared.open(model, harness: k.id) }
            })
        }
        action("new-log-tab", "New Log Tab…", "list.bullet.rectangle", .blue, "create console logs logcat tail file stream", keys: Keys.newLogTab) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { LogUI.shared.request = LogRequest(groupID: group) }
        }
        action("new-editor-tab", "New Editor Tab…", "doc.text", .indigo, "create text file note write code choose", keys: Keys.newEditorTab) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { EditorUI.shared.showQuickOpen(model) }
        }
        action("new-empty-editor-tab", "New Empty Editor Tab", "doc.badge.plus", .indigo, "create text file untitled blank note write code") { EditorUI.shared.newTab(model) }
        action("review-changes", "Review Changes", "plusminus", .orange, "diff git review pull request branch", keys: Keys.reviewChanges) { DiffUI.review(model) }
        action("review-uncommitted", "Review Uncommitted Changes", "plusminus", .orange, "diff git review working tree staged") { DiffUI.review(model, source: DiffUI.uncommitted) }
        action("review-branch", "Review This Branch", "plusminus", .orange, "diff git review pull request base main") { DiffUI.review(model, source: DiffUI.branch) }
        action("new-diff-tab", "New Diff Tab…", "plusminus", .orange, "diff git review compare branch commit range uncommitted choose", keys: Keys.newDiffTab) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { DiffLauncher.shared.newTab(model) }
        }
        action("new-files-tab", "New Files Tab", "folder", .cyan, "file browser explorer tree folder finder", keys: Keys.newFilesTab) { FilesUI.newTab(model) }
        action("review-last-commit", "Review the Last Commit", "plusminus", .orange, "diff git review head commit") { DiffUI.review(model, source: DiffUI.lastCommit) }
        action("quick-open", "Quick Open…", "doc.text.magnifyingglass", .indigo, "find file fuzzy go to open editor", keys: Keys.quickOpen) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { EditorUI.shared.showQuickOpen(model) }
        }
        action("open-file", "Open File…", "folder", .indigo, "editor text code browse finder", keys: Keys.openFile) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { EditorUI.shared.openFile(model) }
        }
        action("new-group", "New Group", "square.stack.3d.up.badge.plus", .purple, "create", keys: Keys.newGroup) { Task { await model.newGroup() } }
        action("new-group-in-directory", "New Group in Directory…", "folder.badge.plus", .purple, "create folder project open path", keys: Keys.newGroupInDirectory) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { GroupFolder.newGroup(model) }
        }
        // Scheduled tasks: the list, a new one, and each one's run, pause and edit.
        action("scheduled-tasks", "Automations", "clock", .indigo, "automations schedule scheduled tasks cron timer recurring runs triggers", keys: Keys.scheduledTasks) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ScheduleUI.shared.showList() }
        }
        action("new-scheduled-task", "New Automation…", "clock.badge.plus", .indigo, "create automation schedule scheduled task cron timer recurring trigger", keys: Keys.newScheduledTask) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ScheduleUI.shared.new(model) }
        }
        if let tab = model.selectedTab, ScheduleUI.canSchedule(model, tab) {
            action("schedule-this", "Automate This Tab…", "clock.arrow.circlepath", .indigo, "automate automation schedule this scheduled task cron timer recurring") {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ScheduleUI.shared.scheduleThis(model, tab) }
            }
        }
        for sc in model.sortedSchedules {
            action("schedule-run:" + sc.id, "Run “\(sc.name)” Now", "play.circle", .indigo, "automation scheduled task start") { ScheduleActions.runNow(model, sc) }
            if sc.canRunEarly {
                action("schedule-run-early:" + sc.id, "Run “\(sc.name)” Now Instead of \(sc.nextRunShort())", "play.circle", .indigo, "automation scheduled task start early bring forward skip next") {
                    ScheduleActions.runNow(model, sc, early: true)
                }
            }
            action("schedule-pause:" + sc.id, sc.paused ? "Resume “\(sc.name)”" : "Pause “\(sc.name)”", sc.paused ? "play" : "pause", .indigo, "automation scheduled task") {
                ScheduleActions.setPaused(model, sc, !sc.paused)
            }
            if sc.hasWebhook {
                action("schedule-webhook:" + sc.id, "Copy “\(sc.name)” Webhook URL", "link", .indigo, "automation scheduled task trigger hook url") {
                    ScheduleActions.copyWebhook(model, sc)
                }
            }
            action("schedule-edit:" + sc.id, "Edit “\(sc.name)”…", "pencil", .indigo, "automation scheduled task change") {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ScheduleUI.shared.edit(sc) }
            }
        }
        // Getting about.
        action("next-tab", "Next Tab", "arrow.right.square", .gray, "switch cycle go", keys: Keys.nextTab) { model.cycleTab(by: 1) }
        action("previous-tab", "Previous Tab", "arrow.left.square", .gray, "switch cycle go back", keys: Keys.previousTab) { model.cycleTab(by: -1) }
        if !model.needsYou.isEmpty {
            action("next-needs-you", "Next Agent That Needs You", "exclamationmark.bubble", .orange, "needs you waiting attention question permission jump go agent queue inbox", keys: Keys.nextNeedsYou) { model.revealNeedingYou(by: 1) }
            action("previous-needs-you", "Previous Agent That Needs You", "exclamationmark.bubble", .orange, "needs you waiting attention question permission jump go back agent queue", keys: Keys.previousNeedsYou) { model.revealNeedingYou(by: -1) }
        }
        if model.groups.count > 1 {
            action("next-group", "Next Group", "arrow.down.square", .gray, "switch cycle go", keys: Keys.nextGroup) { model.cycleGroup(by: 1) }
            action("previous-group", "Previous Group", "arrow.up.square", .gray, "switch cycle go back", keys: Keys.previousGroup) { model.cycleGroup(by: -1) }
        }
        // The tab you're on, and its Group (as their right-click menus); or
        // the empty Group you're in.
        if let g = model.currentGroup {
            let name = model.title(of: g)
            let tab = model.selectedTab
            if let tab {
                action("rename-tab", "Rename Tab…", "pencil", .blue, "name title label", keys: Keys.renameTab) {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { TabSheets.shared.rename(tab) }
                }
                if model.tabMoveIndex(tab.id, by: -1) != nil {
                    action("move-tab-up", "Move Tab Up", "arrow.up", .blue, "reorder order earlier left before sidebar tab bar", keys: Keys.moveTabUp) { model.moveTab(tab.id, by: -1) }
                }
                if model.tabMoveIndex(tab.id, by: 1) != nil {
                    action("move-tab-down", "Move Tab Down", "arrow.down", .blue, "reorder order later right after sidebar tab bar", keys: Keys.moveTabDown) { model.moveTab(tab.id, by: 1) }
                }
            }
            if TabSheets.canConfigure(g) {
                action("configure-group", "Configure Group…", "slider.horizontal.3", .purple, "rename name icon emoji picture edit settings", keys: Keys.configureGroup) {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { TabSheets.shared.configuring = g }
                }
            }
            if GroupFolder.canChange(g) {
                action("group-folder", "Change Group Folder…", "folder", .blue, "directory cwd where new tabs open project", keys: Keys.changeGroupFolder) {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { GroupFolder.choose(model, g) }
                }
            }
            if GroupFolder.canReveal(model, g) {
                action("reveal-group", "Reveal Group in Finder", "folder", .blue, "show folder directory open finder", keys: Keys.revealGroupInFinder) { GroupFolder.reveal(model, g) }
            }
            if SharingUI.canShare(g) {
                action("share-group", "Share Group…", "person.2", .blue, "invite collaborate multiplayer link people", keys: Keys.shareGroup) {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { SharingUI.shared.shareGroup = g.id }
                }
            }
            if !g.isRemote {
                // Your own Groups take your tabs; a shared Group's stay with its host.
                if let tab {
                    for other in model.groups where other.id != g.id && !other.isRemote {
                        action("move-tab:" + other.id, "Move Tab to “\(model.title(of: other))”", "arrow.right.doc.on.clipboard", .purple, "group move") {
                            model.moveTab(tab.id, to: other.id, index: other.tabIds.count)
                        }
                    }
                }
            }
            if GroupCollapse.applies, !g.tabIds.isEmpty {
                if GroupCollapse.isCollapsed(g.id) {
                    action("group-expand", "Expand “\(name)”", "chevron.down", .purple, "group open show tabs sidebar uncollapse") { GroupCollapse.set([g.id], collapsed: false) }
                } else {
                    action("group-collapse", "Collapse “\(name)”", "chevron.right", .purple, "group close hide tabs sidebar fold") { GroupCollapse.set([g.id], collapsed: true) }
                }
            }
            if GroupCollapse.applies, !GroupCollapse.others(model, than: g.id).isEmpty {
                action("group-collapse-others", "Collapse Other Groups", "rectangle.compress.vertical", .purple, "group close hide tabs sidebar fold others all") {
                    GroupCollapse.set(GroupCollapse.others(model, than: g.id), collapsed: true)
                }
            }
            if let i = model.groups.firstIndex(where: { $0.id == g.id }) {
                if i > 0 {
                    action("group-up", "Move “\(name)” Up", "arrow.up", .purple, "group order reorder sidebar") {
                        withAnimation(.snappy(duration: 0.25)) { model.moveGroup(g.id, to: i - 1) }
                    }
                }
                if i < model.groups.count - 1 {
                    action("group-down", "Move “\(name)” Down", "arrow.down", .purple, "group order reorder sidebar") {
                        withAnimation(.snappy(duration: 0.25)) { model.moveGroup(g.id, to: i + 1) }
                    }
                }
            }
            if g.isOnLinkedComputer ? !g.offline : !g.isRemote {
                action("delete-group", "Delete “\(name)”", "trash", .red, "group close remove") { CloseGuard.confirm(model, group: g) { Task { await model.deleteGroup(g.id) } } }
            } else if g.isRemote && !g.isOnLinkedComputer {
                action("leave-group", "Leave “\(name)”", "rectangle.portrait.and.arrow.right", .red, "shared group exit") { Task { await model.leave(g.id) } }
            }
        }
        // Your other computers (linked): show one, make things there, link another.
        action("link-computer", "Link a Computer…", "link", .blue, "pair mac another computer machine linux tailscale local network vpn", keys: Keys.linkComputer) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { LinkUI.shared.open() }
        }
        for c in model.screenComputers {
            let name = c.thisComputer ? "This Mac" : c.name
            action("screen:" + c.userID, "Show Screen of \(name)", "display", .blue, "screen sharing remote control view desktop \(name)") {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ScreenViewerUI.shared.open(model, computerID: c.userID, name: name) }
            }
        }
        // Those that need you, whether or not any other is linked.
        for c in model.computers where !c.thisComputer && c.needsAttention {
            if let addr = c.addresses.first {
                action("link-again:" + c.userID, "Link \(c.name) Again…", "link", .blue, "link again code check certificate \(c.name)") {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { LinkUI.shared.open(link: addr) }
                }
            }
            if c.needsLinking {
                action("remove:" + c.userID, "Remove \(c.name) from This Mac", "minus.circle", .secondary, "forget remove computer \(c.name)") {
                    Task { await model.unlinkComputer(c.userID, forget: true) }
                }
                action("unlink:" + c.userID, "Unlink \(c.name) from All Your Computers…", "xmark.circle", .red, "unlink lost stolen computer \(c.name)") {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { LinkUI.shared.askUnlink(c) }
                }
            }
        }
        if !model.hasLinkedComputers, let me = model.thisComputer {
            // Before anything's linked: this Mac's icon (the footer shows it).
            action("look:" + me.userID, "Icon of This Mac…", "paintpalette", me.color, "computer icon emoji symbol picture badge color colour tint \(me.name)") {
                LinkUI.shared.customise(me)
            }
        }
        if model.hasLinkedComputers {
            action("show-all-computers", "Show All Computers", "square.grid.2x2", .secondary, "computers filter sidebar every", keys: Keys.allComputers) { model.computerFilter = "" }
            for (i, c) in model.computers.enumerated() {
                let name = c.thisComputer ? "This Mac" : c.name
                let id = c.thisComputer ? model.myUserID : c.userID
                action("show-computer:" + c.userID, "Show Only \(name)", c.symbol, c.color, "computer filter sidebar \(c.name)", keys: Keys.showComputer(i), emoji: c.emoji) { model.computerFilter = id }
                if c.online {
                    action("look:" + c.userID, "Icon of \(name)…", "paintpalette", c.color, "computer icon emoji symbol picture badge color colour tint \(c.name)") {
                        LinkUI.shared.customise(c)
                    }
                }
                // Each computer's colour is its own: not one another has.
                for t in computerTints where t.caseInsensitiveCompare(c.tint) != .orderedSame && model.tintOwner(t, except: c.userID) == nil {
                    action("tint:\(c.userID):\(t)", "Colour \(name) \(tintName(t))", "circle.fill", Color(hex: t) ?? .primary, "computer color colour badge tint \(c.name)") {
                        Task { await model.setTint(t, of: c.userID) }
                    }
                }
                if !c.needsLinking {
                    action("unlink:" + c.userID, c.thisComputer ? "Unlink This Mac…" : "Unlink \(c.name)…", "xmark.circle", .red, "remove forget computer \(c.name)") {
                        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { LinkUI.shared.askUnlink(c) }
                    }
                }
                if c.online {
                    action("new-group-on:" + c.userID, "New Group on \(name)", "square.stack.3d.up.badge.plus", .purple, "create computer \(c.name)", keys: Keys.newGroupOn(i)) { Task { await model.newGroup(on: id) } }
                    if !c.thisComputer {
                        action("find-session-on:" + c.userID, "Find Session on \(c.name)…", "clock.arrow.circlepath", .orange, "resume history claude codex computer \(c.name)") {
                            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { PaletteState.shared.open(.sessions, computer: c.userID) }
                        }
                    }
                    if let h = model.defaultAgent(onComputer: c.thisComputer ? "" : c.userID) {
                        action("new-default-agent-on:" + c.userID, AppModel.defaultAgentTitle(h, on: name), "sparkles", .orange, "start agent default quick computer \(c.name)") {
                            AgentLauncher.shared.newDefault(model, computerID: c.thisComputer ? "" : c.userID)
                        }
                    }
                    action("new-agent-on:" + c.userID, "New Agent Tab on \(name)…", "sparkles", .orange, "create claude codex computer \(c.name)") {
                        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { AgentLauncher.shared.open(model, computerID: c.thisComputer ? "" : c.userID) }
                    }
                }
            }
        }
        if let f = focused, let p = model.panes[f], p.kind == .browser, model.group(ofPane: f).map({ $0.canDrive && !$0.offline }) ?? true {
            if p.canGoBack {
                action("browser-back", "Back", "chevron.left", .blue, "web page history previous browser go", keys: Keys.browserBack) { model.navigate(f, "back") }
            }
            if p.canGoForward {
                action("browser-forward", "Forward", "chevron.right", .blue, "web page history next browser go", keys: Keys.browserForward) { model.navigate(f, "forward") }
            }
            action("browser-reload", "Reload Page", "arrow.clockwise", .blue, "web page refresh load again browser", keys: Keys.browserReload) { model.navigate(f, "reload") }
        }
        if let f = focused, model.panes[f]?.kind == .browser, model.group(ofPane: f)?.isRemote != true {
            action("find-in-page", "Find in Page…", "magnifyingglass", .blue, "search text page browser", keys: Keys.findInPage) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { BrowserCommand.find.post() }
            }
            action("find-next", "Find Next", "chevron.down", .blue, "search again text page browser", keys: Keys.findNext) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { BrowserCommand.findNext.post() }
            }
            action("find-previous", "Find Previous", "chevron.up", .blue, "search again back text page browser", keys: Keys.findPrevious) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { BrowserCommand.findPrevious.post() }
            }
            if model.panes[f]?.url.hasPrefix("hmk-devtools:") == false {
                action("devtools", "Show DevTools", "hammer", .blue, "inspect console network elements web inspector", keys: Keys.devTools) {
                    Task { await model.showDevTools(f) }
                }
            }
        }
        if let f = focused, model.canPin(f) {
            let on = PinMode.shared.isOn(f)
            action("pin-notes", on ? "Stop Pinning Notes" : "Pin Notes", "mappin.and.ellipse", .pink,
                   "design notes annotate comment feedback mark point element screenshot agent browser simulator page screen", keys: Keys.pinNotes) {
                PinMode.shared.set(f, !on)
            }
            if !model.pins(f).isEmpty {
                action("send-notes", "Send Notes", "paperplane", .pink, "design notes pins agent message screenshot") { model.sendPins(f) }
            }
        }
        if BrowserPage.url(model, focused) != nil {
            action("open-in-default-browser", "Open in Default Browser", "safari", .blue, "web page external safari chrome firefox system browser url link", keys: Keys.openInDefaultBrowser) {
                BrowserPage.openInDefaultBrowser(model, focused)
            }
            action("copy-page-address", "Copy Address", "link", .blue, "web page url link copy clipboard browser", keys: Keys.copyPageAddress) {
                BrowserPage.copyAddress(model, focused)
            }
        }
        action("find-session", "Find Session…", "clock.arrow.circlepath", .orange, "resume history claude codex thread conversation", keys: Keys.findSession) {
            // Once the palette has closed.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { PaletteState.shared.open(.sessions) }
        }
        // Only the sessions in the folders open in the Group you're in.
        if let g = model.selectedTab.flatMap({ model.group(of: $0) }), g.canResumeSessions {
            action("find-session-in-group", "Resume a Session from This Group…", "clock.arrow.circlepath", .orange,
                   "find resume history claude codex thread conversation group folder \(g.name)", keys: Keys.findSessionInGroup) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { PaletteState.shared.open(.sessions, group: g.id, inGroup: true) }
            }
        }
        // What ⌘W closes here (the pane, or the tab it's alone in), to show
        // it beside that command.
        let closeKey: String = {
            guard let t = model.selectedTab else { return "" }
            if AppModel.paneIDs(of: t).count > 1 { return "pane" }
            return (model.group(of: t)?.tabIds.count ?? 0) > 1 ? "tab" : ""
        }()
        if let focused {
            // A pane with an agent (or a log): its view, or its terminal.
            if let p = model.panes[focused], !p.isLog, model.supportsAgentView(p) {
                let agent = AgentModes.shared.showsAgent(focused)
                action("agent-view", agent ? "Show Terminal" : "Show Agent View", agent ? "apple.terminal" : "sparkles", .orange,
                       "toggle agent view terminal switch transcript claude codex", keys: Keys.toggleAgentView) {
                    AgentModes.shared.toggle(focused)
                }
            }
            if DictateAction.available(model, focused) {
                let on = DictationRequests.shared.listening.contains(focused)
                action("dictate", on ? "Stop Dictating" : "Dictate", on ? "mic.slash" : "mic", .orange,
                       "dictation voice speech microphone mic talk speak transcribe", keys: Keys.dictate) {
                    DictateAction.run(focused)
                }
            }
            if let p = model.panes[focused], p.sessionURL != nil {
                action("copy-session-link", "Copy Session Link", "link", .orange,
                       "claude code remote control web url share agent conversation", keys: Keys.copySessionLink) {
                    SessionLink.copy(model, p)
                }
            }
            if let p = model.panes[focused], p.canRunAgain {
                action("run-again", p.resumesWhenRunAgain ? "Resume Agent" : "Run Again", p.resumesWhenRunAgain ? "play.fill" : "arrow.clockwise", .green,
                       "restart rerun resume exited ended failed command program agent", keys: Keys.runAgain) {
                    model.restartPane(focused)
                }
            }
            if let p = model.panes[focused], p.limitResumeLabel() != nil {
                action("cancel-limit-resume", "Don't Continue When Limit Resets", "clock", .orange,
                       "usage limit reset rate continue resume cancel claude codex agent", keys: Keys.cancelLimitResume) {
                    model.cancelLimitResume(focused)
                }
            }
            if let p = model.panes[focused], p.asleep || p.canSleep {
                action("sleep-agent", p.asleep ? "Wake Agent" : "Put Agent to Sleep", p.asleep ? "sun.max" : "moon.zzz", .indigo,
                       "sleep wake resume suspend idle memory ram free claude codex agent", keys: Keys.sleepAgent) {
                    model.sleepPane(focused, wake: p.asleep)
                }
            }
            action("split-right", "Split Right", "rectangle.split.2x1", .teal, "pane", keys: Keys.splitRight) { Task { await model.split(focused, direction: .horizontal) } }
            action("split-browser", "Split Right with Browser", "rectangle.split.2x1", .blue, "pane web", keys: Keys.splitBrowserRight) { Task { await model.splitBrowser(focused, direction: .horizontal) } }
            action("split-down", "Split Down", "rectangle.split.1x2", .teal, "pane", keys: Keys.splitDown) { Task { await model.split(focused, direction: .vertical) } }
            action("split-browser-down", "Split Down with Browser", "rectangle.split.1x2", .blue, "pane web", keys: Keys.splitBrowserDown) { Task { await model.splitBrowser(focused, direction: .vertical) } }
            for (dir, word, symbol) in [(SplitDirection.horizontal, "Right", "rectangle.split.2x1"), (.vertical, "Down", "rectangle.split.1x2")] {
                let split = AgentSplit(paneID: focused, direction: dir)
                if let h = model.defaultAgent(ofPane: focused) {
                    action("split-default-agent-\(word.lowercased())", "Split \(word) with \(AgentCatalog.name(of: h))", symbol, .orange, "pane agent default quick \(h)") {
                        Task { await model.startAgent(nil, beside: split) }
                    }
                }
                action("split-agent-\(word.lowercased())", "Split \(word) with Agent…", symbol, .orange, "pane agent claude codex start") {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { AgentLauncher.shared.open(model, beside: split) }
                }
            }
            action("split-left", "Split Left","rectangle.split.2x1", .teal, "pane", keys: Keys.splitLeft) { Task { await model.split(focused, direction: .horizontal, before: true) } }
            action("split-up", "Split Up", "rectangle.split.1x2", .teal, "pane above", keys: Keys.splitUp) { Task { await model.split(focused, direction: .vertical, before: true) } }
            action("split-browser-left", "Split Left with Browser", "rectangle.split.2x1", .blue, "pane web") { Task { await model.splitBrowser(focused, direction: .horizontal, before: true) } }
            action("split-browser-up", "Split Up with Browser", "rectangle.split.1x2", .blue, "pane web above") { Task { await model.splitBrowser(focused, direction: .vertical, before: true) } }
            action("split-log", "Split Right with Log…", "list.bullet.rectangle", .blue, "pane console logs logcat tail file stream", keys: Keys.splitLogRight) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { LogUI.shared.split(model, focused, .horizontal) }
            }
            action("split-diff", "Split Right with Diff…", "plusminus", .orange, "pane git review changes compare branch commit", keys: Keys.splitDiffRight) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { DiffLauncher.shared.split(model, focused, .horizontal) }
            }
            action("split-files-left", "Split Left with Files", "folder", .cyan, "pane file browser explorer tree folder sidebar", keys: Keys.splitFilesLeft) { FilesUI.split(model, focused) }
            if let path = FilesUI.revealPath(model, focused) {
                action("reveal-in-files", "Reveal in Files", "folder", .cyan, "show file browser explorer tree locate current", keys: Keys.revealInFiles) {
                    FilesUI.reveal(model, path: path, besides: focused)
                }
            }
            action("split-editor", "Split Right with Editor…", "doc.text", .indigo, "pane text file code choose", keys: Keys.splitEditorRight) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { EditorUI.shared.showQuickOpen(model, splitPane: focused, direction: .horizontal) }
            }
            action("split-editor-down", "Split Down with Editor…", "doc.text", .indigo, "pane text file code choose", keys: Keys.splitEditorDown) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { EditorUI.shared.showQuickOpen(model, splitPane: focused, direction: .vertical) }
            }
            action("split-empty-editor", "Split Right with Empty Editor", "doc.badge.plus", .indigo, "pane text file code untitled blank new") { EditorUI.shared.split(model, focused, .horizontal) }
            if let p = model.panes[focused], p.isDiff {
                for c in DiffUI.commands {
                    action("diff-" + c.id, c.title, c.symbol, .orange, (Keybinds.byID["diff_" + c.id]?.keywords ?? "") + " diff", keys: Keys.diff(c.id)) {
                        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { DiffUI.run(model, focused, c) }
                    }
                }
            }
            if let p = model.panes[focused], p.isEditor {
                let ui = EditorUI.shared
                action("editor-save", "Save", "square.and.arrow.down", .indigo, "write file disk", keys: Keys.editorSave) { ui.save(model, focused) }
                action("editor-save-as", "Save As…", "square.and.arrow.down.on.square", .indigo, "write file rename new name", keys: Keys.editorSaveAs) {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ui.save(model, focused, saveAs: true) }
                }
                if !ui.previewOnly(model, focused) {
                    action("editor-go-to-line", "Go to Line…", "arrow.right.to.line", .indigo, "jump line number", keys: Keys.editorGoToLine) {
                        DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ui.run(model, focused, "gotoLine") }
                    }
                }
                action("editor-find", "Find…", "magnifyingglass", .indigo, "search replace", keys: Keys.findInPage) {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ui.run(model, focused, "find") }
                }
                action("editor-find-next", "Find Next", "chevron.down", .indigo, "search again", keys: Keys.findNext) {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ui.run(model, focused, "findNext") }
                }
                action("editor-find-previous", "Find Previous", "chevron.up", .indigo, "search again back", keys: Keys.findPrevious) {
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ui.run(model, focused, "findPrevious") }
                }
                if ui.canPreview(model, focused) {
                    action("editor-preview", ui.previewLabel(model, focused), "doc.richtext", .indigo, "markdown rendered read view source", keys: Keys.editorTogglePreview) { ui.togglePreview(model, focused) }
                    action("editor-split-preview", ui.previewLabel(model, focused, split: true), "rectangle.split.2x1", .indigo, "markdown rendered side by side split view", keys: Keys.editorToggleSplitPreview) { ui.togglePreview(model, focused, split: true) }
                }
                if !p.editor.path.isEmpty {
                    action("editor-diff", "Compare with File on Disk", "arrow.left.arrow.right", .indigo, "diff changes conflict merge", keys: Keys.editorShowDiff) { ui.showDiff(model, focused) }
                    action("editor-reload", "Reload from Disk", "arrow.clockwise", .indigo, "revert discard changes file", keys: Keys.editorReload) { ui.reload(model, focused) }
                    if model.group(ofPane: focused)?.canDrive != false {
                        action("editor-rename", "Rename File…", "pencil", .indigo, "file name move", keys: Keys.editorRename) {
                            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { ui.rename(model, focused) }
                        }
                    }
                    if ui.canReveal(model, focused) {
                        action("editor-reveal", "Reveal in Finder", "folder", .indigo, "show file folder finder", keys: Keys.editorRevealInFinder) { ui.revealInFinder(model, focused) }
                    }
                }
            }
            if model.panes[focused]?.kind == .simulator {
                action("sim-log", "Show Simulator Log", "list.bullet.rectangle", .blue, "console logs os_log", keys: Keys.showSimulatorLog) { LogUI.shared.showSimulatorLog(model, focused) }
                if let start = model.emulatorStartLabel(focused) {
                    action("sim-start", start, "play.circle", .blue, "android emulator studio take over restart boot", keys: nil) { EmulatorTakeover.confirm(model, focused) }
                }
                action("sim-home", "Home", "house", .blue, "simulator button springboard", keys: Keys.simHome) { model.pressSimulatorButton(focused, "home") }
                if model.isTVSimulator(focused) {
                    action("sim-play-pause", "Play/Pause", "playpause", .blue, "apple tv remote media", keys: Keys.simPlayPause) { model.pressRemote(focused, "play_pause") }
                    action("sim-volume-up", "Volume Up", "speaker.plus", .blue, "apple tv remote louder", keys: Keys.simVolumeUp) { model.pressRemote(focused, "volume_up") }
                    action("sim-volume-down", "Volume Down", "speaker.minus", .blue, "apple tv remote quieter", keys: Keys.simVolumeDown) { model.pressRemote(focused, "volume_down") }
                    action("sim-mute", "Mute", "speaker.slash", .blue, "apple tv remote sound silence", keys: Keys.simMute) { model.pressRemote(focused, "mute") }
                    if model.isAppleTV(focused) {
                        action("sim-siri", "Siri", "mic", .blue, "apple tv remote voice dictation", keys: Keys.simSiri) { model.pressRemote(focused, "siri") }
                    }
                    let shown = Prefs.shared.bool(.simulatorRemote)
                    action("sim-remote", shown ? "Hide Remote" : "Show Remote", "appletvremote.gen4", .blue, "apple tv siri remote control toggle", keys: Keys.simRemoteToggle) { Prefs.shared.toggle(.simulatorRemote) }
                } else {
                    if model.isAndroidPhone(focused) {
                        action("sim-back", "Back", "chevron.backward", .blue, "android navigation return", keys: Keys.simBack) { model.pressSimulatorButton(focused, "back") }
                        action("sim-recents", "Recents", "square.on.square", .blue, "android overview app switcher", keys: Keys.simRecents) { model.pressSimulatorButton(focused, "recents") }
                        action("sim-volume-up", "Volume Up", "speaker.plus", .blue, "android louder", keys: Keys.simVolumeUp) { model.pressSimulatorButton(focused, "volume_up") }
                        action("sim-volume-down", "Volume Down", "speaker.minus", .blue, "android quieter", keys: Keys.simVolumeDown) { model.pressSimulatorButton(focused, "volume_down") }
                    }
                    action("sim-lock", model.isAndroidPhone(focused) ? "Power" : "Lock Simulator", model.isAndroidPhone(focused) ? "power" : "lock", .blue, "simulator button sleep power", keys: Keys.simLock) { model.pressSimulatorButton(focused, "lock") }
                    action("sim-rotate-left", "Rotate Left", "rotate.left", .blue, "simulator orientation landscape portrait turn", keys: Keys.simRotateLeft) { model.rotateSimulator(focused, "left") }
                    action("sim-rotate-right", "Rotate Right", "rotate.right", .blue, "simulator orientation landscape portrait turn", keys: Keys.simRotateRight) { model.rotateSimulator(focused, "right") }
                    if !model.isAndroid(focused) {
                        action("sim-keyboard", "Toggle Software Keyboard", "keyboard", .blue, "simulator show hide keyboard", keys: Keys.simKeyboard) { model.pressSimulatorButton(focused, "keyboard") }
                    }
                }
                action("sim-shot-file", "Screenshot to File", "camera", .blue, "simulator screen capture save png picture image", keys: Keys.simScreenshotFile) { SimulatorShot.saveToFile(model, focused) }
                action("sim-shot-copy", "Screenshot to Clipboard", "doc.on.clipboard", .blue, "simulator screen capture copy png picture image", keys: Keys.simScreenshotCopy) { SimulatorShot.copyToClipboard(model, focused) }
                if model.simulatorHasClipboard(focused) {
                    action("sim-clipboard-send", "Send Clipboard to Device", "arrow.up.doc.on.clipboard", .blue, "simulator emulator android paste pasteboard text", keys: Keys.simClipboardSend) { model.sendClipboardToSimulator(focused) }
                    action("sim-clipboard-get", "Copy Device Clipboard", "arrow.down.doc", .blue, "simulator emulator android get pasteboard text", keys: Keys.simClipboardGet) { model.copySimulatorClipboard(focused) }
                }
                if model.panes[focused]?.deviceFoldable == true {
                    action("sim-open", "Open Phone", "book", .blue, "simulator fold unfold foldable duo hinge", keys: Keys.simFold) { model.foldSimulator(focused, "open") }
                    action("sim-close", "Close Phone", "book.closed", .blue, "simulator fold foldable duo hinge cover", keys: Keys.simFold) { model.foldSimulator(focused, "closed") }
                    action("sim-half", "Half Open Phone", "laptopcomputer", .blue, "simulator fold foldable duo hinge tabletop", keys: Keys.simHalfOpen) { model.foldSimulator(focused, "half") }
                }
                action("sim-shutdown", "Shut Down Simulator", "power", .red, "simulator stop quit turn off", keys: Keys.simShutDown) { model.shutDownSimulator(focused) }
            }
            if model.panes[focused]?.isLog == true {
                let raw = LogUI.shared.showsRaw(focused)
                action("log-raw", raw ? "Show as Log" : "Show Log's Terminal", raw ? "list.bullet.rectangle" : "apple.terminal", .teal, "raw output view toggle", keys: Keys.toggleAgentView) {
                    LogUI.shared.setRaw(focused, !raw)
                }
            }
            for d in SimulatorList.shared.sorted {
                action("split-sim:" + d.udid, "Split Right with \(d.name)", "iphone", .blue, "pane simulator ios \(d.runtime)") {
                    Task { await model.splitSimulator(focused, direction: .horizontal, udid: d.udid) }
                }
            }
            if let tab = model.selectedTab, AppModel.shownPaneIDs(of: tab).count > 1 {
                action("next-pane", "Next Pane", "arrow.right.circle", .teal, "switch cycle go focus", keys: Keys.nextPane) { model.cyclePane(by: 1) }
                action("previous-pane", "Previous Pane", "arrow.left.circle", .teal, "switch cycle go back focus", keys: Keys.previousPane) { model.cyclePane(by: -1) }
                let zoomed = PaneZoom.shared.isZoomed(focused, model: model)
                action("zoom", zoomed ? "Unzoom Pane" : "Zoom Pane", "arrow.up.left.and.arrow.down.right", .teal, "pane maximize fill focus", keys: Keys.zoomPane) {
                    PaneZoom.shared.toggle(focused, model: model)
                }
                if model.canRotateSplit(focused) {
                    action("rotate-split", "Rotate Split", "rectangle.2.swap", .teal, "pane turn flip orientation direction horizontal vertical side by side stacked layout", keys: Keys.rotateSplit) {
                        withAnimation(PaneMotion.when(.snappy(duration: 0.22))) { model.rotateSplit(focused) }
                    }
                }
                action("break-out", "Move Pane to New Tab", "rectangle.badge.plus", .teal, "pane tab break out", keys: Keys.movePaneToNewTab) { Task { await model.breakOut(focused) } }
            }
            // Floating panes.
            action("new-floating", "New Floating Pane", "macwindow.on.rectangle", .teal, "float floating popup overlay scratch terminal pane", keys: Keys.newFloatingPane) {
                Task { await model.newFloating(over: focused) }
            }
            if model.floating(focused) != nil {
                action("dock-pane", "Dock Pane", "rectangle.split.2x1", .teal, "float floating dock split", keys: Keys.floatOrDock) { FloatMotion.shared.dock(model, focused) }
                action("minimise-pane", "Minimise Floating Pane", "minus.rectangle", .teal, "float floating minimize hide shelf", keys: Keys.minimiseFloat) {
                    FloatMotion.shared.minimise(model, focused)
                }
            } else if model.canFloat(focused) {
                action("float-pane", "Float Pane", "macwindow.on.rectangle", .teal, "float floating popup overlay lift", keys: Keys.floatOrDock) { FloatMotion.shared.float(model, focused) }
            }
            if let tab = model.selectedTab, !tab.floating.isEmpty {
                let hidden = tab.floating.allSatisfy(\.minimised)
                action("toggle-floats", hidden ? "Show Floating Panes" : "Hide Floating Panes", "square.stack", .teal, "float floating minimize restore all shelf", keys: Keys.toggleFloats) {
                    FloatMotion.shared.toggleAll(model, tab)
                }
            }
            action("open-pane-window", "Open Pane in New Window", "macwindow.badge.plus", .teal, "detach pop out separate screen share present mirror window", keys: Keys.openPaneInNewWindow) {
                DetachedWindows.shared.open(.pane(focused), model: model)
            }
            action("close-pane", "Close Pane", "xmark.square", .red, "close", keys: Keys.closePane ?? (closeKey == "pane" ? Keys.close : nil)) { CloseGuard.confirm(model, panes: [focused]) { Task { await model.closePane(focused) } } }
        }
        if let tab = model.selectedTab {
            // Merge this tab into another of its Group's (side by side).
            for other in model.group(of: tab).map(model.tabs(in:)) ?? [] where other.id != tab.id {
                action("merge:" + other.id, "Merge Tab into “\(model.title(of: other))”", "rectangle.split.2x1", .teal, "combine join split pane") {
                    Task { await model.mergeTab(tab.id, into: other.focusedPaneID) }
                }
            }
            action("close-tab", "Close Tab", "xmark.rectangle", .red, "close", keys: closeKey == "tab" ? Keys.close : nil) { CloseGuard.confirm(model, tab: tab) { Task { await model.closeTab(tab.id) } } }
            if model.canRemoveWorktree(tab) {
                action("remove-worktree", "Remove Worktree…", "trash", .red, "git worktree branch merged pull request delete clean up", keys: Keys.removeWorktree) {
                    WorktreeUI.remove(model, tab)
                }
            }
        }
        action("light", "Appearance: Light", "sun.max", .orange, "theme mode") { AppConfig.shared.setAppearance(.light) }
        action("dark", "Appearance: Dark", "moon.stars", .indigo, "theme mode") { AppConfig.shared.setAppearance(.dark) }
        action("auto", "Appearance: Automatic", "circle.lefthalf.filled", .gray, "theme mode system") { AppConfig.shared.setAppearance(.auto) }
        // Every theme but the one in use; Custom keeps your own colours.
        for t in AppTheme.all where t.name != AppConfig.shared.appThemeName {
            action("theme:" + t.name, "Theme: \(t.name)", "paintpalette", .pink, "theme colours colors appearance \(t.lightTerminalName) \(t.darkTerminalName)") {
                AppConfig.shared.setAppTheme(t.name)
            }
        }
        if !AppConfig.shared.isCustomTheme {
            action("theme:custom", "Theme: Custom…", "slider.horizontal.3", .pink, "theme colours colors appearance own") {
                AppConfig.shared.setAppTheme(AppTheme.custom)
                SettingsTab.select(.appearance)
                SettingsOpener.show()
            }
        }
        // How the sidebar shows tabs, and whether it lists finished agents.
        let layout = SidebarLayout.current
        for l in SidebarLayout.allCases where l != layout {
            action("layout:" + l.rawValue, "Use \(l.label)", l == .horizontal ? "rectangle.topthird.inset.filled" : "sidebar.left", .gray, "sidebar layout tabs compact vertical horizontal tab bar") {
                SidebarLayout.set(l)
            }
        }
        let finished = Prefs.shared.bool(FinishedPrefs.pref)
        action("finished", finished ? "Hide Finished Agents" : "Show Finished Agents", "checkmark.circle", .green, "sidebar done needs you attention list") {
            Prefs.shared.set(FinishedPrefs.pref, !finished)
        }
        // Horizontal Tabs has no tab rows in the sidebar to keep.
        let agentsCollapsed = Prefs.shared.bool(CollapsedAgentsPrefs.pref)
        if layout != .horizontal {
            action("collapsed-agents", agentsCollapsed ? "Hide Active Agents in Collapsed Groups" : "Show Active Agents in Collapsed Groups", "sidebar.squares.left", .indigo, "sidebar collapse working agent tabs") {
                Prefs.shared.set(CollapsedAgentsPrefs.pref, !agentsCollapsed)
            }
        }
        action("bigger", "Bigger Text", "textformat.size.larger", .gray, "font size zoom in terminal", keys: Keys.bigger) { TerminalFont.zoom(by: 1) }
        action("smaller", "Smaller Text", "textformat.size.smaller", .gray, "font size zoom out terminal", keys: Keys.smaller) { TerminalFont.zoom(by: -1) }
        action("actual-size", "Actual Size Text", "textformat.size", .gray, "font size zoom reset terminal", keys: Keys.actualSize) { TerminalFont.zoom(by: 0) }
        action("join", "Join Shared Group…", "person.2.badge.plus", .blue, "invite link multiplayer", keys: Keys.joinSharedGroup) {
            let s = NSPasteboard.general.string(forType: .string)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { SharingUI.shared.joinLink = s.hasPrefix("hammock://join/") ? s : "" }
        }
        action("close-window", "Close Window", "xmark.square", .red, "close", keys: Keys.closeWindow) { WorkspaceWindow.current?.performClose(nil) }
        action("welcome", "Welcome to Hammock…", "hand.wave", .orange, "onboarding help tour introduction", keys: Keys.welcome) {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.15) { Onboarding.shared.show() }
        }
        action("documentation", "Hammock Documentation", "book", .blue, "docs help manual guide reference website", keys: Keys.documentation) {
            NSWorkspace.shared.open(HammockLinks.docs)
        }
        action("hmk", "Install the hmk Command", "terminal", .green, "cli shell") { _ = try? CommandLineTool.install() }
        action("pair", "Pair a Device…", "iphone", .blue, "iphone android phone ios remote qr pairing code", keys: Keys.pairDevice) { SharingUI.shared.pairing = true }
        if Updater.shared.isAvailable {
            action("updates", "Check for Updates…", "arrow.down.circle", .blue, "update upgrade version release new", keys: Keys.checkForUpdates) { Updater.shared.checkForUpdates() }
        }
        action("settings", "Settings…", "gearshape", .gray, "preferences", keys: Keys.settings) { openSettings() }
        action("about", "About Hammock", "info.circle", .blue, "version uninstall quit stop server", keys: Keys.about) { SettingsTab.select(.about); openSettings() }
        action("acknowledgements", "Acknowledgements", "doc.text.magnifyingglass", .gray, "licences licenses third party open source credits notices copyright chromium", keys: Keys.acknowledgements) { AcknowledgementsUI.shared.show() }
        action("keyboard-shortcuts", "Keyboard Shortcuts…", "keyboard", .gray, "keybindings keybinds hotkeys keys customize rebind settings") { SettingsTab.select(.keyboard); openSettings() }
        action("ssh-settings", "SSH and Hammock Key…", "key", .gray, "ssh key hmk key agent 1password git signing secure enclave settings") { SettingsTab.select(.ssh); openSettings() }
        action("full-disk-access", "Full Disk Access…", "lock.open", .blue, "privacy permission files 1password op tcc security system settings") { FullDiskAccess.openSettingsWithGuide() }
        action("remote-networks", "Remote Access Networks…", "network", .blue, "remote access network wifi lan local vpn wireguard tailscale listen address iphone settings") { DevicesSettingsFocus.shared.showRemoteAccess(openSettings) }
        if model.hammockKey.exists {
            let key = model.hammockKey.publicKey
            action("copy-hmk-key", "Copy Hammock Key", "key.fill", .orange, "ssh public key authorized_keys github") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(key, forType: .string)
            }
        }
        return out
    }

    private var filtered: [PaletteItem] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return items }
        var out = items.compactMap { item -> (PaletteItem, Int)? in
            guard let s = Self.score(q, item.title.lowercased(), item.keywords.lowercased() + " " + item.subtitle.lowercased()) else { return nil }
            return (item, s)
        }
        .sorted { $0.1 > $1.1 }
        .map(\.0)
        // Anything typed can be opened: first if it looks like an address,
        // else after the matches, as a search.
        let typed = query.trimmingCharacters(in: .whitespaces)
        let looksLikeURL = !typed.contains(" ") && (typed.contains(".") || typed.contains("://") || typed.hasPrefix("localhost"))
        if HammockTool.browser.on, let group = model.currentGroup?.id ?? model.groups.first?.id {
            let open = PaletteItem(id: "open:" + typed, title: looksLikeURL ? "Open \(typed)" : "Search the web for “\(typed)”",
                                   subtitle: "New browser tab", icon: .symbol(looksLikeURL ? "globe" : "magnifyingglass", .blue)) {
                Task { await model.createBrowserTab(in: group, url: typed) }
            }
            if looksLikeURL { out.insert(open, at: 0) } else { out.append(open) }
        }
        return out
    }

    /// Fuzzy match: every query character in order. Prefix and word-start hits
    /// score highest; matches in the title beat matches elsewhere.
    static func score(_ q: String, _ title: String, _ extra: String) -> Int? {
        func subsequence(_ text: String) -> Int? {
            var score = 0, last = text.startIndex, first = true
            var idx = text.startIndex
            for ch in q {
                guard let found = text[idx...].firstIndex(of: ch) else { return nil }
                let atWordStart = found == text.startIndex || text[text.index(before: found)] == " "
                score += atWordStart ? 8 : (found == last && !first ? 5 : 1)
                last = text.index(after: found)
                idx = last
                first = false
            }
            return score
        }
        if title.hasPrefix(q) { return 1000 - title.count }
        if let s = subsequence(title) { return 200 + s }
        if let s = subsequence(extra) { return s }
        return nil
    }

    // MARK: Actions

    private func move(_ delta: Int, _ count: Int) {
        guard count > 0 else { return }
        selection = (selection + delta + count) % count
    }

    private func confirm(_ results: [PaletteItem]) {
        guard selection < results.count else { return }
        let item = results[selection]
        // At once: the row's tint and the fade are only what you see as it happens.
        confirming = item.id
        item.run()
        close()
    }

    private func close() {
        withAnimation(.easeOut(duration: 0.12)) { appeared = false }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.12) {
            state.isOpen = false
            NotificationCenter.default.post(name: TerminalNSView.refocus, object: nil)
        }
    }
}

private struct PaletteRow: View {
    let item: PaletteItem
    let selected: Bool
    let flashing: Bool

    var body: some View {
        HStack(spacing: 12) {
            Group {
                switch item.icon {
                case .symbol(let name, let color): Image(systemName: name).foregroundStyle(color)
                case .emoji(let e): Text(e)
                case .program(let names): ProgramIcon(program: names, size: 18)
                case .agent(let harness): AgentMark(harness: harness, size: 16)
                case .picture(let img): Image(nsImage: img).resizable().scaledToFit().frame(width: 18, height: 18).clipShape(RoundedRectangle(cornerRadius: 4, style: .continuous))
                }
            }
            .font(.system(size: 16))
            .frame(width: 24)
            VStack(alignment: .leading, spacing: 1) {
                Text(item.title).font(.system(size: 14, weight: .medium)).lineLimit(1)
                if !item.subtitle.isEmpty {
                    Text(item.subtitle).font(.system(size: 12)).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            Spacer(minLength: 8)
            if let badge = item.badge, badge != .unspecified {
                StatusIcon(state: badge, size: 14)
            }
            // As the menu bar writes it, to learn it by.
            if let keys = item.shortcut {
                Text(keys.label)
                    .font(.system(size: 13))
                    .monospacedDigit()
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(RoundedRectangle(cornerRadius: 5).fill(Color.primary.opacity(0.06)))
                    .accessibilityLabel("Shortcut \(keys.label)")
            }
            if selected {
                Image(systemName: "return").font(.caption).foregroundStyle(.tertiary)
            }
        }
        .padding(.horizontal, 12)
        .frame(height: 46)
        .background {
            RoundedRectangle(cornerRadius: 10)
                .fill(flashing ? Color.themeAccent.opacity(0.3) : selected ? Color.themeAccent.opacity(0.16) : .clear)
        }
        .contentShape(Rectangle())
    }
}

extension CommandPalette {
    /// The tool (Settings ▸ Tools) a palette action opens or is for, so a
    /// tool turned off has none.
    static func tool(ofAction id: String) -> HammockTool? {
        switch id {
        case "new-browser-tab", "split-browser", "split-browser-down", "split-browser-left", "split-browser-up": .browser
        case "new-log-tab", "split-log", "sim-log": .log
        case "new-editor-tab", "new-empty-editor-tab", "editor-wrap", "quick-open", "open-file", "split-editor", "split-editor-down", "split-empty-editor": .editor
        case _ where id.hasPrefix("review-"): .diff
        case "new-files-tab", "split-files-left", "reveal-in-files": .files
        case _ where id.hasPrefix("split-sim:"): .simulator
        case "scheduled-tasks", "new-scheduled-task", "schedule-this": .schedules
        case _ where id.hasPrefix("schedule-"): .schedules
        default: nil
        }
    }
}
