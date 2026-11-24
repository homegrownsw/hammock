import HammockKit
import SwiftUI

// Menus shared by the toolbar, pane headers and tab rows: what to open in a
// new split, and moving tabs and panes about.

/// This Mac's simulators, loaded once and refreshed when a menu of them is
/// about to show (a context menu can't load them itself while open).
@MainActor @Observable
final class SimulatorList {
    static let shared = SimulatorList()
    private(set) var devices: [AppModel.SimulatorDevice] = []

    /// Running ones first.
    var sorted: [AppModel.SimulatorDevice] { devices.sorted { $0.booted && !$1.booted } }
    /// iPhones and iPads, Apple TVs, Android phones and tablets, then Android TVs, each running ones first.
    var phones: [AppModel.SimulatorDevice] { sorted.filter { ($0.platform ?? "iOS") == "iOS" } }
    var tvs: [AppModel.SimulatorDevice] { sorted.filter { $0.platform == "tvOS" } }
    var androids: [AppModel.SimulatorDevice] { sorted.filter { $0.platform == "Android" } }
    var androidTVs: [AppModel.SimulatorDevice] { sorted.filter { $0.platform == "Android TV" } }

    private var refreshing = false

    func refresh(_ model: AppModel) {
        // The README's demo (HMK_DEBUG_DIRECTOR) shows none: they're this Mac's.
        guard ProcessInfo.processInfo.environment["HMK_DEBUG_DIRECTOR"] == nil, !refreshing else { return }
        refreshing = true
        Task {
            defer { refreshing = false }
            // An empty answer is often the simulator host not running yet (just
            // after launch): asked again for half a minute, not left empty.
            for attempt in 0..<10 {
                devices = await model.simulators()
                if !devices.isEmpty || attempt == 9 { return }
                try? await Task.sleep(for: .seconds(3))
            }
        }
    }

    static func label(_ d: AppModel.SimulatorDevice) -> String {
        d.booted ? "\(d.name) — \(d.runtime) (running)" : "\(d.name) — \(d.runtime)"
    }
}

/// Terminal, browser or simulator (or a log, editor or diff, right or below), split off a
/// pane: after it, or before it (left, or above) with before.
struct SplitMenuItems: View {
    let model: AppModel
    /// Read when an item runs, not when the menu was built: the toolbar's
    /// menus keep their items (and their ⌘D) from a while ago, and the
    /// window finds that ⌘D before the menu bar's.
    private let pane: () -> String
    var direction: SplitDirection = .horizontal
    var before = false
    /// A new pane floating over the tab instead of a split.
    var floating = false

    private var paneID: String { pane() }

    init(model: AppModel, paneID: String, direction: SplitDirection = .horizontal, before: Bool = false, floating: Bool = false) {
        self.init(model: model, pane: { paneID }, direction: direction, before: before, floating: floating)
    }

    /// For the pane focused in a tab when the item runs (the toolbar's).
    init(model: AppModel, focusedIn tabID: String, direction: SplitDirection) {
        self.init(model: model, pane: { [weak model] in
            guard let model, let tab = model.tabs[tabID] else { return "" }
            if let f = model.focusedPaneID, model.panes[f]?.tabID == tabID { return f }
            return tab.focusedPaneID
        }, direction: direction)
    }

    private init(model: AppModel, pane: @escaping () -> String, direction: SplitDirection, before: Bool = false, floating: Bool = false) {
        self.model = model
        self.pane = pane
        self.direction = direction
        self.before = before
        self.floating = floating
    }

    /// The default agent of the pane's computer, straight away (when it has
    /// one), and Agent…: the Start an Agent sheet, for a pane here.
    @ViewBuilder private func agentItems(_ split: AgentSplit) -> some View {
        if let h = model.defaultAgent(ofPane: paneID) {
            Button { Task { await model.startAgent(nil, beside: split) } } label: {
                Label { Text(AgentCatalog.name(of: h)) } icon: { AgentMenuIcon.image(h) }
            }
        }
        Button("Agent…", systemImage: PaneKindSymbol.agent) { AgentLauncher.shared.open(model, beside: split) }
    }

    // In PaneKindSymbol's sections: terminal and agents; browser and
    // simulator; editor, files and diff; log.
    // Symbols in a menu only show when the label asks for them.
    var body: some View { Group { items }.labelStyle(.titleAndIcon) }

    @ViewBuilder private var items: some View {
        if floating {
            Button("Terminal", systemImage: PaneKindSymbol.terminal) { Task { await model.newFloating(over: paneID) } }.keyboardShortcut(Keys.newFloatingPane)
            agentItems(AgentSplit(paneID: paneID, floating: true))
            if HammockTool.browser.on || HammockTool.simulator.on { Divider() }
            if HammockTool.browser.on {
                Button("Browser", systemImage: PaneKindSymbol.browser) { Task { await model.newFloating(over: paneID, spec: .with { $0.url = "about:blank" }) } }
            }
            if HammockTool.simulator.on {
                Menu("Simulator", systemImage: PaneKindSymbol.simulator) {
                    SimulatorItems { udid in Task { await model.newFloating(over: paneID, spec: .with { $0.simulator = udid }) } }
                }
            }
        } else {
            let editor = !before && HammockTool.editor.on, diff = !before && HammockTool.diff.on, log = !before && HammockTool.log.on
            Button("Terminal", systemImage: PaneKindSymbol.terminal) { Task { await model.split(paneID, direction: direction, before: before) } }
                .keyboardShortcut(before ? (direction == .horizontal ? Keys.splitLeft : Keys.splitUp) : (direction == .horizontal ? Keys.splitRight : Keys.splitDown))
            agentItems(AgentSplit(paneID: paneID, direction: direction, before: before))
            if HammockTool.browser.on || HammockTool.simulator.on { Divider() }
            if HammockTool.browser.on {
                Button("Browser", systemImage: PaneKindSymbol.browser) { Task { await model.splitBrowser(paneID, direction: direction, before: before) } }
                    .keyboardShortcut(before ? nil : direction == .horizontal ? Keys.splitBrowserRight : Keys.splitBrowserDown)
            }
            if HammockTool.simulator.on {
                Menu("Simulator", systemImage: PaneKindSymbol.simulator) {
                    SimulatorItems { udid in Task { await model.splitSimulator(paneID, direction: direction, udid: udid, before: before) } }
                }
            }
            if editor || HammockTool.files.on || diff { Divider() }
            if editor {
                // Asks which file (or a new one); ⌥: a new, empty one straight away.
                let keys = direction == .horizontal ? Keys.splitEditorRight : Keys.splitEditorDown
                Button("Editor…", systemImage: PaneKindSymbol.editor) { EditorUI.shared.showQuickOpen(model, splitPane: paneID, direction: direction) }
                    .keyboardShortcut(keys)
                    .emptyEditorAlternate("Empty Editor", keys) { EditorUI.shared.split(model, paneID, direction) }
            }
            if HammockTool.files.on {
                Button("Files", systemImage: PaneKindSymbol.files) { FilesUI.split(model, paneID, direction: direction, before: before) }
                    .keyboardShortcut(before && direction == .horizontal ? Keys.splitFilesLeft : nil)
            }
            if diff {
                Button("Diff…", systemImage: PaneKindSymbol.diff) { DiffLauncher.shared.split(model, paneID, direction) }
                    .keyboardShortcut(direction == .horizontal ? Keys.splitDiffRight : nil)
            }
            if log {
                Divider()
                Button("Log…", systemImage: PaneKindSymbol.log) { LogUI.shared.split(model, paneID, direction) }
                    .keyboardShortcut(direction == .horizontal ? Keys.splitLogRight : nil)
            }
        }
    }
}

/// The symbol each kind of pane shows (its header, a folded or minimised
/// pane), for the menus that list them. Those menus share one order, in
/// sections: Terminal and the agents; Browser and Simulator; Editor, Files
/// and Diff; Log.
enum PaneKindSymbol {
    static let terminal = "apple.terminal"
    static let agent = "sparkles"
    static let browser = "globe"
    static let simulator = "iphone"
    static let editor = "doc.text"
    static let files = "folder"
    static let diff = "plusminus"
    static let log = "list.bullet.rectangle"
}

/// An agent's mark as a menu item's icon, drawn like the menu's symbols
/// beside it: in the theme's colour (a menu tints symbols, not images), and
/// no bigger than they are.
@MainActor
enum AgentMenuIcon {
    private static var cache: [String: Image] = [:]

    static func image(_ harness: String) -> Image {
        let accent = Color.themeAccent
        let key = "\(harness) \(accent.description)"
        if let i = cache[key] { return i }
        let r = ImageRenderer(content: AgentMark(harness: harness, size: 12, tint: accent).padding(2))
        r.scale = 2
        guard let ns = r.nsImage else { return Image(systemName: PaneKindSymbol.agent) }
        let i = Image(nsImage: ns)
        cache[key] = i
        return i
    }
}

extension View {
    /// With ⌥ held, a new, empty editor (`title`) in place of an item that
    /// asks which file. Not when the item's shortcut has ⌥ already: the
    /// menu tells them apart by it, so the two would be one. With the
    /// editor's icon unless `icon` is false (the item has none).
    @ViewBuilder
    func emptyEditorAlternate(_ title: String, _ keys: KeyboardShortcut?, icon: Bool = true, action: @escaping () -> Void) -> some View {
        if keys?.modifiers.contains(.option) == true {
            self
        } else if icon {
            modifierKeyAlternate(.option) { Button(title, systemImage: PaneKindSymbol.editor, action: action) }
        } else {
            modifierKeyAlternate(.option) { Button(title, action: action) }
        }
    }
}

/// New Group, New Group in Directory… and New Group On: the File menu's and
/// the sidebar's + button's.
struct NewGroupMenuItems: View {
    let model: AppModel

    var body: some View {
        Button("New Group") { Task { await model.newGroup() } }.keyboardShortcut(Keys.newGroup)
        Button("New Group in Directory…") { GroupFolder.newGroup(model) }.keyboardShortcut(Keys.newGroupInDirectory)
        if model.hasLinkedComputers {
            Menu("New Group On") {
                // ⌃⌥⌘1…9: the computers in the footer's order (as Show Computer's ⌃⌘1…9).
                ForEach(Array(model.computers.enumerated()), id: \.element.userID) { i, c in
                    Button(c.thisComputer ? "This Mac" : c.name) { Task { await model.newGroup(on: c.thisComputer ? model.myUserID : c.userID) } }
                        .disabled(!c.online)
                        .keyboardShortcut(Keys.newGroupOn(i))
                }
            }
        }
    }
}

/// One item per simulator.
struct SimulatorItems: View {
    /// The one a pane shows already, ticked (Show ▸ Simulator).
    var current: String?
    let open: (String) -> Void
    private var list: SimulatorList { .shared }

    init(current: String? = nil, open: @escaping (String) -> Void) {
        self.current = current
        self.open = open
    }

    var body: some View {
        if list.devices.isEmpty {
            Text("No simulators (Xcode ▸ Settings ▸ Components)")
        }
        ForEach(list.phones) { d in item(d) }
        section("Apple TV", list.tvs)
        section("Android", list.androids)
        section("Android TV", list.androidTVs)
    }

    @ViewBuilder private func item(_ d: AppModel.SimulatorDevice) -> some View {
        if d.udid == current {
            Toggle(SimulatorList.label(d), isOn: Binding(get: { true }, set: { _ in open(d.udid) }))
        } else {
            Button(SimulatorList.label(d)) { open(d.udid) }
        }
    }

    @ViewBuilder private func section(_ title: String, _ devices: [AppModel.SimulatorDevice]) -> some View {
        if !devices.isEmpty {
            Section(title) {
                ForEach(devices) { d in item(d) }
            }
        }
    }
}

/// A pane header's right-click menu.
struct PaneMenuItems: View {
    let model: AppModel
    let paneID: String

    private var tabPaneCount: Int {
        guard let t = model.panes[paneID].flatMap({ model.tabs[$0.tabID] }) else { return 0 }
        return AppModel.paneIDs(of: t).count
    }

    var body: some View {
        Menu("Split Right") { SplitMenuItems(model: model, paneID: paneID, direction: .horizontal) }
        Menu("Split Down") { SplitMenuItems(model: model, paneID: paneID, direction: .vertical) }
        Menu("Split Left") { SplitMenuItems(model: model, paneID: paneID, direction: .horizontal, before: true) }
        Menu("Split Up") { SplitMenuItems(model: model, paneID: paneID, direction: .vertical, before: true) }
        Menu("New Floating Pane") { SplitMenuItems(model: model, paneID: paneID, floating: true) }
        Divider()
        FloatPaneItems(model: model, paneID: paneID)
        Button("Rotate Split") { withAnimation(PaneMotion.when(.snappy(duration: 0.22))) { model.rotateSplit(paneID) } }
            .disabled(!model.canRotateSplit(paneID))
        Button("Move to New Tab") { Task { await model.breakOut(paneID) } }
            .disabled(tabPaneCount < 2)
        OpenInNewWindowButton(model: model, subject: .pane(paneID))
        if model.isTVSimulator(paneID) {
            Button(Prefs.shared.bool(.simulatorRemote) ? "Hide Remote" : "Show Remote") { Prefs.shared.toggle(.simulatorRemote) }
        } else if model.panes[paneID]?.kind == .simulator {
            if model.isAndroidPhone(paneID) {
                Button("Back") { model.pressSimulatorButton(paneID, "back") }
                Button("Home") { model.pressSimulatorButton(paneID, "home") }
                Button("Recents") { model.pressSimulatorButton(paneID, "recents") }
            }
            Button("Rotate Left") { model.rotateSimulator(paneID, "left") }
            Button("Rotate Right") { model.rotateSimulator(paneID, "right") }
            if !model.isAndroid(paneID) {
                Button("Toggle Software Keyboard") { model.pressSimulatorButton(paneID, "keyboard") }
            }
        }
        if let start = model.emulatorStartLabel(paneID) {
            Button(start) { EmulatorTakeover.confirm(model, paneID) }
        }
        if model.panes[paneID]?.kind == .simulator {
            Button("Screenshot to File") { SimulatorShot.saveToFile(model, paneID) }
            Button("Screenshot to Clipboard") { SimulatorShot.copyToClipboard(model, paneID) }
            if model.simulatorHasClipboard(paneID) {
                Button("Send Clipboard to Device") { model.sendClipboardToSimulator(paneID) }
                Button("Copy Device Clipboard") { model.copySimulatorClipboard(paneID) }
            }
            if model.panes[paneID]?.deviceFoldable == true {
                Button("Open Phone") { model.foldSimulator(paneID, "open") }
                Button("Half Open Phone") { model.foldSimulator(paneID, "half") }
                Button("Close Phone") { model.foldSimulator(paneID, "closed") }
            }
            Button("Shut Down Simulator") { model.shutDownSimulator(paneID) }
            if HammockTool.log.on {
                Button("Show Simulator Log") { LogUI.shared.showSimulatorLog(model, paneID) }
            }
        }
        if BrowserPage.url(model, paneID) != nil {
            Button("Open in Default Browser") { BrowserPage.openInDefaultBrowser(model, paneID) }
                .keyboardShortcut(Keys.openInDefaultBrowser)
            Button("Copy Address") { BrowserPage.copyAddress(model, paneID) }
                .keyboardShortcut(Keys.copyPageAddress)
        }
        if let p = model.panes[paneID], p.kind == .browser, !p.url.hasPrefix("hmk-devtools:"), model.group(ofPane: paneID)?.isRemote != true {
            Button("Show DevTools") { Task { await model.showDevTools(paneID) } }
        }
        // Agents acting here (AgentPresenceOverlay's badge has the same).
        if let p = model.panes[paneID], p.kind == .browser || p.kind == .simulator, model.group(ofPane: paneID)?.isRemote != true {
            if p.agentHeld {
                Button("Hand Back to Agents") { model.holdPane(paneID, false) }
            } else {
                Button("Take Over from Agents") { model.holdPane(paneID, true) }
            }
        }
        if let p = model.panes[paneID], p.asleep || p.canSleep {
            SleepAgentButton(model: model, pane: p)
        }
        if let p = model.panes[paneID], p.canRunAgain {
            RunAgainButton(model: model, pane: p)
        }
        Button("Close Pane", role: .destructive) { CloseGuard.confirm(model, panes: [paneID]) { Task { await model.closePane(paneID) } } }
    }
}

/// Put Agent to Sleep, or on one asleep, Wake Agent: the Pane menu's
/// command (Keys.sleepAgent), in a pane's menus.
struct SleepAgentButton: View {
    let model: AppModel
    let pane: Pane

    var body: some View {
        Button(pane.asleep ? "Wake Agent" : "Put Agent to Sleep") { model.sleepPane(pane.id, wake: pane.asleep) }
            .keyboardShortcut(Keys.sleepAgent)
    }
}

/// Run Again, or Resume Agent on an agent's pane: an ended pane's program
/// started again in it (ExitedPaneBar), in the Pane menu and a pane's menu.
struct RunAgainButton: View {
    let model: AppModel
    let pane: Pane?

    var body: some View {
        Button(pane?.resumesWhenRunAgain == true ? "Resume Agent" : "Run Again") { if let pane { model.restartPane(pane.id) } }
            .keyboardShortcut(Keys.runAgain)
            .disabled(pane?.canRunAgain != true)
    }
}

/// A tab's right-click menu, in the sidebar and the tab bar.
struct TabMenuItems: View {
    let model: AppModel
    let tab: WorkspaceTab

    private var group: WorkspaceGroup? { model.group(of: tab) }

    /// Groups the tab can go to: your own, unless it's in a shared Group
    /// (those tabs stay with their host).
    private var otherGroups: [WorkspaceGroup] {
        guard group?.isRemote != true else { return [] }
        return model.groups.filter { $0.id != tab.groupID && !$0.isRemote }
    }

    var body: some View {
        Button("Rename Tab…") { TabSheets.shared.rename(tab) }
        Button("Move Tab Up") { model.moveTab(tab.id, by: -1) }
            .keyboardShortcut(Keys.moveTabUp)
            .disabled(model.tabMoveIndex(tab.id, by: -1) == nil)
        Button("Move Tab Down") { model.moveTab(tab.id, by: 1) }
            .keyboardShortcut(Keys.moveTabDown)
            .disabled(model.tabMoveIndex(tab.id, by: 1) == nil)
        if group?.isRemote != true {
            Menu("Move to Group") {
                ForEach(otherGroups) { g in
                    Button { model.moveTab(tab.id, to: g.id, index: g.tabIds.count) } label: {
                        // Its emoji, picture or the default, as in the sidebar.
                        Label { Text(model.title(of: g)) } icon: { model.groupMenuIcon(g) }
                    }
                }
                if !otherGroups.isEmpty { Divider() }
                Button("New Group") {
                    Task {
                        guard let g = await model.createGroup(name: "") else { return }
                        model.moveTab(tab.id, to: g, index: 0)
                    }
                }
            }
        }
        if ScheduleUI.canSchedule(model, tab) {
            Button("Schedule This…") { ScheduleUI.shared.scheduleThis(model, tab) }
        }
        if let p = linkedPane {
            Button("Copy Session Link") { SessionLink.copy(model, p) }
        }
        // The agent the row stands for, at rest or asleep.
        if let p = model.shownAgentPane(of: tab), p.asleep || p.canSleep {
            SleepAgentButton(model: model, pane: p)
        }
        Menu("Merge into Tab") {
            mergeTargets(in: group, side: true)
            Divider()
            Menu("Below") { mergeTargets(in: group, side: false) }
        }
        .disabled((group?.tabIds.count ?? 0) < 2 && otherGroups.isEmpty)
        OpenInNewWindowButton(model: model, subject: .tab(tab.id))
        Divider()
        if model.canRemoveWorktree(tab) {
            Button("Remove Worktree…", role: .destructive) { WorktreeUI.remove(model, tab) }
        }
        Button("Close Tab", role: .destructive) { CloseGuard.confirm(model, tab: tab) { Task { await model.closeTab(tab.id) } } }
    }

    /// The tab's agent with a session link: its focused pane's, else the first.
    private var linkedPane: Hammock_V1_Pane? {
        let panes = ([tab.focusedPaneID] + AppModel.paneIDs(of: tab)).compactMap { model.panes[$0] }
        return panes.first { $0.sessionURL != nil }
    }

    /// The tabs this one can merge into: its Group's, then other Groups' in submenus.
    @ViewBuilder
    private func mergeTargets(in group: WorkspaceGroup?, side: Bool) -> some View {
        if let group {
            targets(model.tabs(in: group), side: side)
        }
        ForEach(otherGroups) { g in
            let tabs = model.tabs(in: g)
            if !tabs.isEmpty {
                Menu(model.title(of: g)) { targets(tabs, side: side) }
            }
        }
    }

    private func targets(_ tabs: [WorkspaceTab], side: Bool) -> some View {
        ForEach(tabs.filter { $0.id != tab.id }) { t in
            Button(model.title(of: t)) {
                Task { await model.mergeTab(tab.id, into: t.focusedPaneID, direction: side ? .horizontal : .vertical) }
            }
        }
    }
}

/// What a new tab can hold.
struct NewTabMenuItems: View {
    let model: AppModel
    let groupID: String

    // In PaneKindSymbol's sections.
    // Symbols in a menu only show when the label asks for them.
    var body: some View { Group { items }.labelStyle(.titleAndIcon) }

    @ViewBuilder private var items: some View {
        Button("Terminal Tab", systemImage: PaneKindSymbol.terminal) { Task { await model.createTab(in: groupID) } }.keyboardShortcut(Keys.newTab)
        NewAgentTabItems(model: model, groupID: groupID, short: true, icons: true)
        if HammockTool.browser.on || HammockTool.simulator.on { Divider() }
        if HammockTool.browser.on {
            Button("Browser Tab", systemImage: PaneKindSymbol.browser) { Task { await model.createBrowserTab(in: groupID) } }.keyboardShortcut(Keys.newBrowserTab)
        }
        if HammockTool.simulator.on {
            Menu("Simulator Tab", systemImage: PaneKindSymbol.simulator) {
                SimulatorItems { udid in Task { await model.createSimulatorTab(in: groupID, udid: udid) } }
            }
        }
        if HammockTool.editor.on || HammockTool.files.on || HammockTool.diff.on { Divider() }
        if HammockTool.editor.on {
            // Asks which file (or a new one); ⌥: a new, empty one straight away.
            Button("Editor Tab…", systemImage: PaneKindSymbol.editor) { EditorUI.shared.quickOpen = QuickOpenRequest(groupID: groupID) }
                .keyboardShortcut(Keys.newEditorTab)
                .emptyEditorAlternate("Empty Editor Tab", Keys.newEditorTab) { EditorUI.shared.open(model, "", groupID: groupID) }
        }
        if HammockTool.files.on {
            Button("Files Tab", systemImage: PaneKindSymbol.files) { FilesUI.newTab(model, groupID: groupID) }.keyboardShortcut(Keys.newFilesTab)
        }
        if HammockTool.diff.on {
            Button("Diff Tab…", systemImage: PaneKindSymbol.diff) { DiffLauncher.shared.newTab(model, groupID: groupID) }.keyboardShortcut(Keys.newDiffTab)
        }
        if HammockTool.log.on {
            Divider()
            Button("Log Tab…", systemImage: PaneKindSymbol.log) { LogUI.shared.request = LogRequest(groupID: groupID) }.keyboardShortcut(Keys.newLogTab)
        }
    }
}

/// The menu at the start of every pane's header: what the pane shows
/// (Show ▸: something else in its place), its agent's
/// actions, and moving or closing it.
/// Close, at the far right of a pane's header (after its other controls):
/// only where there's another pane to keep, as a tab's last pane closes
/// with the tab.
struct PaneCloseButton: View {
    let model: AppModel
    let paneID: String

    var body: some View {
        if let t = model.panes[paneID].flatMap({ model.tabs[$0.tabID] }), AppModel.paneIDs(of: t).count > 1 {
            Button {
                CloseGuard.confirm(model, panes: [paneID]) { Task { await model.closePane(paneID) } }
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .semibold))
                    .frame(width: 22, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.borderless)
            .foregroundStyle(.secondary)
            .tooltip("Close Pane", label: "Close Pane")
        }
    }
}

struct PaneKindMenu<Label: View>: View {
    let model: AppModel
    let paneID: String
    @ViewBuilder let label: () -> Label

    private var pane: Pane? { model.panes[paneID] }
    private var tabPaneCount: Int {
        guard let t = pane.flatMap({ model.tabs[$0.tabID] }) else { return 0 }
        return AppModel.paneIDs(of: t).count
    }

    var body: some View { menu }

    private var menu: some View {
        Menu {
            Menu("Show") { kinds.labelStyle(.titleAndIcon) }
            if let pane, pane.isEditor, model.group(ofPane: paneID)?.canDrive != false {
                Section("Open in This Pane") {
                    Button("Quick Open…") { EditorUI.shared.showQuickOpen(model, replacePane: paneID) }
                    Button("Open File…") { EditorUI.shared.openFile(model, replacePane: paneID) }
                }
            }
            if let pane, pane.isEditor, !pane.editor.path.isEmpty {
                Section(pane.editor.name) {
                    if model.group(ofPane: paneID)?.canDrive != false {
                        Button("Rename…") { EditorUI.shared.rename(model, paneID) }.keyboardShortcut(Keys.editorRename)
                    }
                    if EditorUI.shared.canReveal(model, paneID) {
                        Button("Reveal in Finder") { EditorUI.shared.revealInFinder(model, paneID) }.keyboardShortcut(Keys.editorRevealInFinder)
                    }
                    Button("Copy Path") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(pane.editor.path, forType: .string)
                    }
                }
            }
            if let pane, pane.isEditor {
                Toggle("Wrap Lines", isOn: Prefs.shared.boolBinding(.editorWrap)).keyboardShortcut(Keys.editorWrap)
            }
            if let pane, pane.hasAgent {
                Section(pane.agent.harness.isEmpty ? pane.displayName : AgentCatalog.shortName(of: pane.agent.harness)) {
                    if AgentCatalog.canSwitch(pane.agent.harness) {
                        ForEach(AgentCatalog.switchable, id: \.id) { k in
                            Button("Continue in \(AgentCatalog.shortName(of: k.id))") { model.switchAgent(paneID, to: k.id) }
                                .disabled(pane.agent.harness == k.id)
                        }
                    }
                    if pane.sessionURL != nil {
                        Button("Copy Session Link") { SessionLink.copy(model, pane) }
                    }
                    Button("Interrupt (Esc)") { model.send(paneID, Data([0x1B])) }
                    if pane.asleep || pane.canSleep {
                        SleepAgentButton(model: model, pane: pane)
                    }
                }
            }
            Divider()
            FloatPaneItems(model: model, paneID: paneID)
            Button("Rotate Split") { withAnimation(PaneMotion.when(.snappy(duration: 0.22))) { model.rotateSplit(paneID) } }
                .disabled(!model.canRotateSplit(paneID))
            Button("Move to New Tab") { Task { await model.breakOut(paneID) } }
                .disabled(tabPaneCount < 2)
            Button("Close Pane", role: .destructive) {
                CloseGuard.confirm(model, panes: [paneID]) { Task { await model.closePane(paneID) } }
            }
        } label: {
            label()
        }
        .menuStyle(.borderlessButton)
        // As wide as its label, but shrinking (the label truncating) when
        // the header is short of room: a long title, such as a command's,
        // never makes the header wider than its pane.
        .fixedSize(horizontal: false, vertical: true)
        .tooltip("What this pane shows, and its actions")
        .task { SimulatorList.shared.refresh(model) }
    }

    /// Show ▸: what else the pane could show in its place, in
    /// PaneKindSymbol's sections, the current one ticked. A sheet's
    /// (Editor…, Diff…, Log…) asks before it opens, a file's once it's chosen.
    @ViewBuilder private var kinds: some View {
        let kind = pane?.kind
        let isLog = pane?.isLog == true
        let canDrive = model.group(ofPane: paneID)?.canDrive != false
        let browser = HammockTool.browser.on || kind == .browser, simulator = HammockTool.simulator.on || kind == .simulator
        let editor = (HammockTool.editor.on || kind == .editor) && canDrive
        let files = HammockTool.files.on || kind == .files, diff = HammockTool.diff.on || kind == .diff
        item("Terminal", PaneKindSymbol.terminal, current: kind == .terminal && !isLog) { change("a terminal", Hammock_V1_PaneSpec()) }
        if browser || simulator { Divider() }
        if browser {
            item("Browser", PaneKindSymbol.browser, current: kind == .browser) { change("a browser", .with { $0.url = "about:blank" }) }
        }
        if simulator {
            // A submenu can't be ticked: the current one's tick takes its
            // icon's place, and its device is ticked inside.
            Menu("Simulator", systemImage: kind == .simulator ? "checkmark" : PaneKindSymbol.simulator) {
                SimulatorItems(current: kind == .simulator ? pane?.deviceUdid : nil) { udid in
                    guard kind != .simulator || pane?.deviceUdid != udid else { return }
                    change("a simulator", .with { $0.simulator = udid })
                }
            }
        }
        if editor || files || diff { Divider() }
        if editor {
            // Asks which file (or a new one); ⌥: a new, empty one.
            item("Editor…", PaneKindSymbol.editor, current: kind == .editor, again: true) { EditorUI.shared.showQuickOpen(model, replacePane: paneID) }
                .emptyEditorAlternate("Empty Editor", nil) { EditorUI.shared.open(model, "", groupID: nil, replacePane: paneID) }
        }
        if files {
            item("Files", PaneKindSymbol.files, current: kind == .files) { change("files", .with { $0.files = .init() }) }
        }
        if diff {
            item("Diff…", PaneKindSymbol.diff, current: kind == .diff, again: true) { ask("a diff") { DiffLauncher.shared.replace(model, paneID) } }
        }
        if HammockTool.log.on || isLog {
            Divider()
            item("Log…", PaneKindSymbol.log, current: isLog, again: true) { ask("a log") { LogUI.shared.replace(model, paneID) } }
        }
    }

    /// A kind to show, with its icon, ticked when it's the current one
    /// (which does nothing again, unless again: another file, diff or log).
    private func item(_ title: String, _ symbol: String, current: Bool, again: Bool = false, action: @escaping () -> Void) -> some View {
        Toggle(isOn: Binding(get: { current }, set: { _ in
            guard !current || again else { return }
            action()
        })) {
            SwiftUI.Label(title, systemImage: symbol)
        }
    }

    /// Shows spec in the pane's place, asking first unless nothing can be lost.
    private func change(_ what: String, _ spec: Hammock_V1_PaneSpec) {
        ask(what) { Task { await model.replacePane(paneID, with: spec) } }
    }

    private func ask(_ what: String, then: @escaping () -> Void) {
        CloseGuard.confirmReplace(model, paneID, title: "Show \(what) here instead?", then: then)
    }
}

/// Which pane each tab has zoomed (fills the tab, the others folded to their
/// bars above it). The app's own state: the layout itself doesn't change.
@MainActor @Observable
final class PaneZoom {
    static let shared = PaneZoom()
    private var byTab: [String: String] = [:]

    /// The tab's zoomed split pane, if it still has it and another to fold.
    func zoomed(in tab: WorkspaceTab) -> String? {
        guard let id = byTab[tab.id] else { return nil }
        let ids = AppModel.paneIDs(tab.layout)
        return ids.count > 1 && ids.contains(id) ? id : nil
    }

    func isZoomed(_ paneID: String, model: AppModel) -> Bool {
        if let f = model.floating(paneID) { return f.zoomed }
        return model.panes[paneID].flatMap { model.tabs[$0.tabID] }.flatMap { zoomed(in: $0) } == paneID
    }

    /// Zooms a pane, or unzooms it if it's the one zoomed. A floating pane
    /// fills its tab, still a card (the daemon's, so every client sees it).
    func toggle(_ paneID: String, model: AppModel) {
        if model.floating(paneID) != nil {
            // The same motion as a split pane zooming, terminals held still.
            PaneMotion.shared.animate { model.arrange(paneID, .zoom) }
            model.focus(paneID)
            return
        }
        guard let tab = model.panes[paneID].flatMap({ model.tabs[$0.tabID] }) else { return }
        let next = zoomed(in: tab) == paneID ? nil : paneID
        PaneMotion.shared.animate { byTab[tab.id] = next }
        model.focus(paneID)
    }

    /// Zooms another pane of a zoomed tab (focus moved to it).
    func follow(_ paneID: String, in tab: WorkspaceTab) {
        guard let z = zoomed(in: tab), z != paneID, AppModel.paneIDs(tab.layout).contains(paneID) else { return }
        PaneMotion.shared.animate(PaneMotion.switchAnimation) { byTab[tab.id] = paneID }
    }
}

/// Zoom in a pane's header: shown when its tab has another pane to fold away.
struct ZoomButton: View {
    let model: AppModel
    let paneID: String
    private var zoom: PaneZoom { .shared }

    var body: some View {
        FloatButtons(model: model, paneID: paneID)
        if let tab = model.panes[paneID].flatMap({ model.tabs[$0.tabID] }), model.floating(paneID) != nil || AppModel.paneIDs(tab.layout).count > 1 {
            let on = zoom.isZoomed(paneID, model: model)
            Button { zoom.toggle(paneID, model: model) } label: {
                Image(systemName: on ? "arrow.down.right.and.arrow.up.left" : "arrow.up.left.and.arrow.down.right")
                    .font(.system(size: 11, weight: .medium))
                    .frame(width: 22, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.borderless)
            .foregroundStyle(on ? Color.themeAccent : .secondary)
            .tooltip(Keys.hint(on ? "Unzoom Pane" : "Zoom Pane", "zoom_pane"), label: on ? "Unzoom Pane" : "Zoom Pane")
        }
    }
}

/// Panes folded away while another is zoomed, where they were in the
/// layout: a strip (vertical) beside the zoomed pane in a side-by-side split,
/// a bar above or below it in a stacked one. Shaded on the side facing the
/// zoomed pane, as if it lies over them; click one to zoom it instead.
struct FoldedPanes: View {
    let model: AppModel
    let layout: PaneLayout
    let vertical: Bool
    let towardZoomed: Edge

    var body: some View {
        FoldedLayout(model: model, layout: layout, vertical: vertical)
            .frame(width: vertical ? 28 : nil, height: vertical ? nil : 24)
            .frame(maxWidth: vertical ? nil : .infinity, maxHeight: vertical ? .infinity : nil)
        .background(Color.primary.opacity(0.04))
        .background(Color(nsColor: .windowBackgroundColor))
        .overlay(alignment: Alignment(horizontal: towardZoomed == .leading ? .leading : towardZoomed == .trailing ? .trailing : .center,
                                      vertical: towardZoomed == .top ? .top : towardZoomed == .bottom ? .bottom : .center)) {
            shade.allowsHitTesting(false)
        }
    }

    /// Darkest at the edge by the zoomed pane.
    private var shade: some View {
        let (start, end): (UnitPoint, UnitPoint) = switch towardZoomed {
        case .leading: (.trailing, .leading)
        case .trailing: (.leading, .trailing)
        case .top: (.bottom, .top)
        case .bottom: (.top, .bottom)
        }
        return LinearGradient(colors: [.clear, .black.opacity(0.14)], startPoint: start, endPoint: end)
            .frame(width: vertical ? 6 : nil, height: vertical ? nil : 6)
    }
}

/// A folded part of the layout, kept in its own shape along the bar: a
/// split that runs the bar's way keeps its proportions (a bar above two
/// side-by-side panes is two halves); one across it is laid out along it in
/// equal parts.
private struct FoldedLayout: View {
    let model: AppModel
    let layout: PaneLayout
    let vertical: Bool

    var body: some View {
        switch layout.node {
        case .paneID(let id):
            FoldedPane(model: model, paneID: id, label: !vertical)
        case .split(let s):
            let along = (s.direction == .vertical) == vertical
            let n = s.children.count
            let w = along && s.weights.count == n ? s.weights.map(CGFloat.init) : Array(repeating: 1 / CGFloat(n), count: n)
            GeometryReader { geo in
                let total = (vertical ? geo.size.height : geo.size.width) - CGFloat(max(n - 1, 0))
                let stack = vertical ? AnyLayout(VStackLayout(spacing: 0)) : AnyLayout(HStackLayout(spacing: 0))
                stack {
                    ForEach(Array(s.children.enumerated()), id: \.element.stableKey) { i, child in
                        if i > 0 { Rectangle().fill(Color(nsColor: .separatorColor)).frame(width: vertical ? nil : 1, height: vertical ? 1 : nil) }
                        AnyView(FoldedLayout(model: model, layout: child, vertical: vertical))
                            .frame(width: vertical ? nil : w[i] * total, height: vertical ? w[i] * total : nil)
                    }
                }
            }
        case nil:
            Color.clear
        }
    }
}

/// One folded pane: its kind or agent state (and name, in a bar).
private struct FoldedPane: View {
    let model: AppModel
    let paneID: String
    let label: Bool
    @State private var hover = false

    private var pane: Pane? { model.panes[paneID] }
    private var name: String {
        guard let p = pane else { return "" }
        if p.kind == .browser || p.kind == .simulator { return p.title.isEmpty ? p.url : p.title }
        return p.displayName
    }

    var body: some View {
        // Its whole part of the bar: a click anywhere in it zooms it.
        HStack(spacing: 6) {
            icon.frame(width: 16)
            if label { Text(name).lineLimit(1) }
        }
        .font(.callout)
        .foregroundStyle(hover ? .primary : .secondary)
        .padding(label ? .leading : .top, label ? 15 : 8)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: label ? .leading : .top)
        .background(hover ? Color.primary.opacity(0.06) : .clear)
        .contentShape(Rectangle())
        .onHover { hover = $0 }
        .onTapGesture { PaneZoom.shared.toggle(paneID, model: model) }
        .tooltip("\(name): zoom this pane instead")
    }

    @ViewBuilder private var icon: some View {
        switch pane?.kind {
        case .browser: Image(systemName: "globe")
        case .simulator: Image(systemName: "iphone")
        case .editor: Image(systemName: "doc.text")
        case .diff: Image(systemName: "plusminus")
        case .files: Image(systemName: "folder")
        default:
            if let p = pane, p.hasAgent, p.agent.state != .unspecified {
                StatusIcon(state: p.agent.shownState, size: 13, asleep: p.asleep, progress: p.agentProgress)
            } else {
                ProgramIcon(program: pane?.exited == false ? pane?.program ?? [] : [], size: 13)
            }
        }
    }
}

/// Swapping two panes of a tab: drag one's header onto the other. The panes'
/// places in the window, the one being dragged, and where the pointer is.
/// (Not the system's drag and drop: terminals take drops of their own.)
@MainActor @Observable
final class PaneSwap {
    static let shared = PaneSwap()
    var frames: [String: CGRect] = [:]
    private(set) var dragging: String?
    private(set) var point: CGPoint?
    /// The panes it could swap with: its tab's splits (not its floats).
    private var among: Set<String> = []

    /// The pane the dragged one would swap with: the one under the pointer.
    var target: String? {
        guard let dragging, let point else { return nil }
        return frames.first { $0.key != dragging && among.contains($0.key) && $0.value.contains(point) }?.key
    }

    func drag(_ paneID: String, to p: CGPoint, among splits: [String]) {
        dragging = paneID
        point = p
        among = Set(splits)
    }

    /// Swaps the dragged pane with the one it's over, in the same tab.
    func drop(model: AppModel) {
        defer { dragging = nil; point = nil }
        guard let a = dragging, let b = target, let tab = model.panes[a].flatMap({ model.tabs[$0.tabID] }),
              model.panes[b]?.tabID == tab.id else { return }
        let splits = AppModel.paneIDs(tab.layout)
        guard splits.contains(a), splits.contains(b) else { return }
        withAnimation(PaneMotion.when(.snappy(duration: 0.22))) { model.setLayout(tab.id, Self.swapping(tab.layout, a, b)) }
    }

    /// A layout with two panes' places exchanged.
    static func swapping(_ l: PaneLayout, _ a: String, _ b: String) -> PaneLayout {
        var l = l
        switch l.node {
        case .paneID(let id): l.paneID = id == a ? b : id == b ? a : id
        case .split(var s):
            s.children = s.children.map { swapping($0, a, b) }
            l.split = s
        case nil: break
        }
        return l
    }
}

/// The tab's pane area: where swap drags, the panes' places and the chip are
/// measured, so they agree.
let paneSpace = NamedCoordinateSpace.named("hammock.panes")

extension View {
    /// Makes a pane's header a handle for dragging it onto another pane (a
    /// floating pane's moves it, see FloatDrag), and a click on it focuses the pane.
    func paneSwapHandle(_ paneID: String, model: AppModel) -> some View {
        // All of it: an unfocused header's background is clear, and SwiftUI
        // only hit-tests what's drawn, so only its text took clicks.
        contentShape(Rectangle())
        .onTapGesture { model.focus(paneID) }
        .simultaneousGesture(
            DragGesture(minimumDistance: model.floating(paneID) == nil ? 8 : 4, coordinateSpace: paneSpace)
                .onChanged { g in
                    guard let tab = model.panes[paneID].flatMap({ model.tabs[$0.tabID] }) else { return }
                    if model.floating(paneID) != nil {
                        FloatDrag.shared.drag(paneID, g, model: model)
                    } else {
                        PaneSwap.shared.drag(paneID, to: g.location, among: AppModel.paneIDs(tab.layout))
                    }
                }
                .onEnded { _ in
                    if FloatDrag.shared.moving == paneID { FloatDrag.shared.end(model: model) } else { PaneSwap.shared.drop(model: model) }
                }
        )
    }

    /// A pane's place for swapping: where it is, dimmed while dragged, and
    /// marked while it's the one a drag would swap with.
    func paneSwapTarget(_ paneID: String) -> some View {
        modifier(PaneSwapTarget(paneID: paneID))
    }
}

private struct PaneSwapTarget: ViewModifier {
    let paneID: String
    @Environment(\.paneFolded) private var folded
    private var swap: PaneSwap { .shared }

    func body(content: Content) -> some View {
        content
            // Not cleared when the view goes: after a swap the pane's new
            // view records its place before its old one disappears. (A
            // closed pane's leftover place is harmless: a drop checks both
            // panes are in the tab.) A pane folded under a zoomed one has no
            // place you could drop on: its view runs on under the others.
            .onGeometryChange(for: CGRect.self) { $0.frame(in: paneSpace) } action: { if !folded { swap.frames[paneID] = $0 } }
            .onChange(of: folded) { _, f in if f { swap.frames[paneID] = nil } }
            .opacity(swap.dragging == paneID ? 0.5 : 1)
            .overlay {
                if swap.target == paneID {
                    ZStack {
                        Color.themeAccent.opacity(0.10)
                        RoundedRectangle(cornerRadius: 4).strokeBorder(Color.themeAccent, lineWidth: 2)
                        Label("Swap", systemImage: "arrow.left.arrow.right")
                            .font(.callout.weight(.semibold))
                            .padding(.horizontal, 12).padding(.vertical, 6)
                            .background(.regularMaterial, in: Capsule())
                    }
                    .allowsHitTesting(false)
                }
            }
    }
}

/// The dragged pane's name, following the pointer (over the tab's panes,
/// in their space).
struct PaneSwapChip: View {
    let model: AppModel
    private var swap: PaneSwap { .shared }

    var body: some View {
        ZStack(alignment: .topLeading) {
            Color.clear
            if let id = swap.dragging, let p = swap.point, let pane = model.panes[id] {
                Label(pane.kind == .browser || pane.kind == .simulator ? (pane.title.isEmpty ? "Pane" : pane.title) : pane.displayName,
                      systemImage: pane.kind == .browser ? "globe" : pane.kind == .simulator ? "iphone" : "apple.terminal")
                    .font(.callout.weight(.medium))
                    .padding(.horizontal, 10).padding(.vertical, 6)
                    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
                    .shadow(color: .black.opacity(0.2), radius: 8, y: 3)
                    .fixedSize()
                    .offset(x: p.x + 12, y: p.y + 12)
            }
        }
        .allowsHitTesting(false)
    }
}

/// Asks before closing panes that would stop something: an agent at work
/// (working, waiting for you, running a command; unless that's turned off in
/// Settings ▸ Agents) or other programs running in a terminal. An agent at
/// rest on its own isn't asked about, unless there's `always` to say (a
/// Group of several tabs). A sheet on the window, so menus and shortcuts can
/// use it as well as views.
@MainActor
enum CloseGuard {
    nonisolated static let pref = Pref("confirm-close-busy-agent", defaults: "warnCloseBusyAgent", true)
    static var warnBusy: Bool { Prefs.shared.bool(pref) }

    static func confirm(_ model: AppModel, panes ids: [String], title: String? = nil, button: String = "Close", always: String? = nil,
                        askUnsent: Bool = true, then: @escaping () -> Void) {
        let panes = ids.compactMap { model.panes[$0] }
        // A review with comments not yet sent: finish it, or close (they're
        // kept with the review, for the same diff opened again).
        let unsent = panes.filter { p in
            p.isDiff && p.diff.unsent > 0 && model.group(ofPane: p.id).map { !$0.isRemote || $0.myRole == .owner } ?? true
        }
        if askUnsent, !unsent.isEmpty {
            let n = unsent.reduce(0) { $0 + Int($1.diff.unsent) }
            let a = NSAlert()
            a.messageText = "Finish your review?"
            a.informativeText = (n == 1 ? "A comment hasn't" : "\(n) comments haven't") + " been sent to the agent. "
                + "Closing keeps \(n == 1 ? "it" : "them"): open the same diff again to send \(n == 1 ? "it" : "them")."
            if unsent.count == 1 { a.addButton(withTitle: "Finish Review…") }
            a.addButton(withTitle: "Close Anyway")
            a.addButton(withTitle: "Cancel")
            let done: (NSApplication.ModalResponse) -> Void = { r in
                let finish = unsent.count == 1 && r == .alertFirstButtonReturn
                let close = unsent.count == 1 ? r == .alertSecondButtonReturn : r == .alertFirstButtonReturn
                if finish {
                    model.viewFocused(unsent[0].id)
                    model.diffSession(unsent[0].id)?.command("finish")
                } else if close {
                    confirm(model, panes: ids, title: title, button: button, always: always, askUnsent: false, then: then)
                }
            }
            // On the diff's own window (it may be one of its own).
            if let w = PaneNotices.shared.window(for: unsent[0].id) { a.beginSheetModal(for: w, completionHandler: done) } else { done(a.runModal()) }
            return
        }
        // Files with unsaved edits, in their last pane (others share the buffer).
        let closing = Set(ids)
        let unsaved = panes.filter { p in
            p.isEditor && p.editor.dirty && (p.editor.path.isEmpty || !model.panes.values.contains { $0.isEditor && !closing.contains($0.id) && $0.editor.path == p.editor.path })
        }
        if !unsaved.isEmpty {
            let a = NSAlert()
            a.messageText = unsaved.count == 1 ? "Close \(unsaved[0].editor.name) without saving?" : "Close \(unsaved.count) files without saving?"
            a.informativeText = "Your changes will be lost."
            a.addButton(withTitle: "Close Without Saving")
            a.addButton(withTitle: "Cancel")
            a.buttons[0].hasDestructiveAction = true
            let done: (NSApplication.ModalResponse) -> Void = { r in if r == .alertFirstButtonReturn { then() } }
            // On the file's pane's window.
            if let w = PaneNotices.shared.window(for: unsaved[0].id) { a.beginSheetModal(for: w, completionHandler: done) } else { done(a.runModal()) }
            return
        }
        let busy = panes.filter { $0.hasAgent && !$0.exited && ([.working, .needsInput, .starting].contains($0.agent.shownState) || $0.agent.subagents > 0) }
        let alert = NSAlert()
        if warnBusy, !busy.isEmpty {
            let who = busy.count == 1 ? busy[0].displayName.capitalized : "\(busy.count) agents"
            let a = busy[0].agent
            let doing = busy.count > 1 ? "are still at work"
                : a.shownState == .needsInput ? "is waiting for you"
                : a.runningCommand ? "is running a command"
                : a.shownState == .idle ? "has \(a.subagents == 1 ? "a subagent" : "\(a.subagents) subagents") still running"
                : "is still working"
            alert.messageText = title ?? "\(who) \(doing)"
            alert.informativeText = title == nil ? "Closing stops \(busy.count == 1 ? "it" : "them")." : "\(who) \(doing), and will be stopped."
            alert.showsSuppressionButton = true
            alert.suppressionButton?.title = "Don't warn about busy agents"
        } else {
            // Programs other than an agent at rest.
            var names: [String] = []
            for p in panes {
                for n in p.processes where !(p.hasAgent && n.lowercased() == p.agent.harness.lowercased()) && !names.contains(n) { names.append(n) }
            }
            guard !names.isEmpty || always != nil else { then(); return }
            alert.messageText = title ?? (panes.count == 1 ? "Close this pane?" : "Close these panes?")
            let running = names.isEmpty ? nil : ListFormatter.localizedString(byJoining: names) + (names.count == 1 ? " is" : " are") + " still running and will be stopped."
            alert.informativeText = [always, running].compactMap { $0 }.joined(separator: " ")
        }
        alert.addButton(withTitle: button)
        alert.addButton(withTitle: "Cancel")
        alert.buttons[0].hasDestructiveAction = true
        // A destructive button loses Return; this one is what Close asked for.
        alert.buttons[0].keyEquivalent = "\r"
        let done: (NSApplication.ModalResponse) -> Void = { r in
            if alert.suppressionButton?.state == .on { Prefs.shared.set(pref, false) }
            if r == .alertFirstButtonReturn { then() }
        }
        if let w = WorkspaceWindow.current { alert.beginSheetModal(for: w, completionHandler: done) } else { done(alert.runModal()) }
    }

    /// Before something else is shown in a pane's place (its menu's Show):
    /// goes ahead only when nothing can be lost, else asks. Stricter than
    /// closing: an agent asks even at rest (its prompt may hold a draft), and
    /// whatever the busy-agent setting says.
    static func confirmReplace(_ model: AppModel, _ paneID: String, title: String, then: @escaping () -> Void) {
        guard let p = model.panes[paneID] else { return }
        // Unsaved edits: closing's question, which says so.
        if p.isEditor {
            confirm(model, panes: [paneID], title: title, button: "Replace", then: then)
            return
        }
        guard let lost = replaceLoses(p, model) else { then(); return }
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = lost
        alert.addButton(withTitle: "Replace")
        alert.addButton(withTitle: "Cancel")
        alert.buttons[0].hasDestructiveAction = true
        alert.buttons[0].keyEquivalent = "\r"
        let done: (NSApplication.ModalResponse) -> Void = { r in if r == .alertFirstButtonReturn { then() } }
        if let w = WorkspaceWindow.current { alert.beginSheetModal(for: w, completionHandler: done) } else { done(alert.runModal()) }
    }

    /// What replacing a pane would lose, or nil when it's known to be
    /// nothing: a terminal that's exited, a log, or a shell with nothing
    /// running; a blank page; a simulator another pane still shows; a diff
    /// (its open threads are kept).
    static func replaceLoses(_ p: Pane, _ model: AppModel) -> String? {
        switch p.kind {
        case .simulator:
            // Its device shuts down soon after its last pane closes.
            let shown = model.panes.values.contains { $0.id != p.id && $0.kind == .simulator && $0.deviceUdid == p.deviceUdid }
            return shown ? nil : "\(p.title.isEmpty ? "The simulator" : p.title) may shut down, and what's running on it with it."
        case .browser:
            return p.url.isEmpty || p.url == "about:blank" ? nil : "This page will close, and anything typed into it will be lost."
        case .terminal:
            if p.exited || p.isLog { return nil }
            if p.hasAgent, !p.isReportingProgram, p.agent.state != .exited {
                let name = AgentCatalog.shortName(of: p.agent.harness)
                return p.asleep ? "\(name)'s session here will close." : "\(name) will be stopped, and anything typed into it will be lost."
            }
            let names = p.processes.reduce(into: [String]()) { if !$0.contains($1) { $0.append($1) } }
            guard !names.isEmpty else { return nil }
            return ListFormatter.localizedString(byJoining: names) + (names.count == 1 ? " is" : " are") + " still running and will be stopped."
        default:
            return nil
        }
    }

    /// For a tab: all its panes.
    static func confirm(_ model: AppModel, tab: WorkspaceTab, then: @escaping () -> Void) {
        confirm(model, panes: AppModel.paneIDs(of: tab), title: nil, button: "Close Tab", then: then)
    }

    /// Deleting a Group: every pane in its tabs. One of several tabs always
    /// asks, busy or not: there's no getting them back.
    static func confirm(_ model: AppModel, group: WorkspaceGroup, then: @escaping () -> Void) {
        let ids = group.tabIds.compactMap { model.tabs[$0] }.flatMap { AppModel.paneIDs(of: $0) }
        let tabs = group.tabIds.count
        confirm(model, panes: ids, title: "Delete “\(model.title(of: group))”?", button: "Delete Group",
                always: tabs > 1 ? "Its \(tabs) tabs will be closed." : nil, then: then)
    }
}
