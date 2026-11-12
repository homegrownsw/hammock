import AppKit
import HammockKit
import HammockUpdates
import SwiftUI
import UserNotifications

@main
struct HammockApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @AppStorage(SidebarHiding.key) private var sidebarHidden = false
    @State private var model: AppModel

    init() {
        let m = AppModel(clientKind: "mac")
        _model = State(initialValue: m)
        AppModelRegistry.shared = m
        m.onUnreachable = { DaemonLauncher.launchIfNeeded() }
        m.onDaemonVersion = { v in DaemonUpdater.check(running: v, model: m) }
        m.onAttention = { item, pane in
            // Finished in the pane you're in: you saw it, so acknowledge it as
            // clicking into it would (the daemon can miss that you were
            // watching), rather than leave its checkmark until you click out
            // and back.
            if item.state == .idle, NSApp.isActive, let tab = m.selectedTab, tab.focusedPaneID == item.paneID,
               AppModel.shownPaneIDs(of: tab).contains(item.paneID) {
                m.focus(item.paneID)
                return
            }
            Notifier.shared.notify(item, pane: pane, model: m)
        }
        m.onScheduleRun = { s, run in Notifier.shared.notifyRun(run, of: s, model: m) }
        m.onReviewReplies = { p, n in Notifier.shared.notifyReview(p, new: n, model: m) }
        m.onLinkRequest = { req in Notifier.shared.notifyLink(req) }
        m.onGrant = { grant in Notifier.shared.notifyGrant(grant, model: m) }
        m.onJoinRequest = { req in Notifier.shared.notifyJoin(req, model: m) }
        m.onShowPairing = {
            SharingUI.shared.pairing = true
            NSApp.activate()
        }
        m.onDevicePaired = { name in Notifier.shared.notifyPaired(name) }
        m.onResolved = { ids in Notifier.shared.withdraw(ids) }
        m.onFullState = { Notifier.shared.reconcile(m) }
        // Run from outside Applications, the app first asks to move there
        // (AppMover, at launch), so no daemon starts from a copy about to go.
        if AppMover.shouldOffer { AppMover.deferredStart = { m.start() } } else { m.start() }
        _ = Updater.shared // Sparkle's daily check (release builds only)
    }

    /// `hammock://pane/<id>` (`hmk focus`, desktop widgets): shows that pane,
    /// as clicking its notification does. When the link launched the app the
    /// daemon's state is still arriving, so it waits for the pane briefly.
    private func revealLinked(_ id: String) {
        NSApp.activate()
        Task { @MainActor in
            for _ in 0..<20 {
                if model.panes[id] != nil { model.reveal(paneID: id); return }
                try? await Task.sleep(for: .milliseconds(150))
            }
        }
    }

    var body: some Scene {
        WindowGroup("Hammock") {
            Group {
                if Onboarding.shared.isShowing {
                    OnboardingView(model: model)
                        .transition(.opacity)
                } else {
                    ContentView(model: model)
                }
            }
                .frame(minWidth: 900, minHeight: 560)
                // The sidebar material blended with what's behind the window, as in
                // Finder: glassy and neutral (a plain blur picks up the wallpaper's
                // colour). The panes stay opaque.
                .containerBackground(for: .window) { SidebarGlass() }
                .background(WorkspaceWindow())
                .background(SettingsOpener())
                .overlay {
                    if PaletteState.shared.isOpen {
                        if PaletteState.shared.mode == .sessions { SessionFinder(model: model) } else { CommandPalette(model: model) }
                    }
                }
                .sheet(item: Binding(get: { SharingUI.shared.shareGroup.map(SheetID.init) }, set: { SharingUI.shared.shareGroup = $0?.id })) {
                    ShareSheet(model: model, groupID: $0.id)
                }
                .sheet(item: Binding(get: { SharingUI.shared.joinLink.map(SheetID.init) }, set: { SharingUI.shared.joinLink = $0?.id })) {
                    JoinSheet(model: model, link: $0.id)
                }
                .sheet(isPresented: Binding(get: { SharingUI.shared.pairing }, set: { SharingUI.shared.pairing = $0 })) {
                    PairingSheet(model: model)
                }
                .sheet(isPresented: Binding(get: { LinkUI.shared.showing }, set: { LinkUI.shared.showing = $0 })) {
                    LinkComputerSheet(model: model)
                }
                .unlinkConfirmation(model)
                .sheet(item: Binding(get: { LinkUI.shared.customising.map(SheetID.init) }, set: { LinkUI.shared.customising = $0?.id })) { s in
                    ComputerLookPicker(model: model, userID: s.id) { LinkUI.shared.customising = nil }
                }
                .sheet(item: Binding(get: { AgentLauncher.shared.request }, set: { AgentLauncher.shared.request = $0 })) {
                    StartAgentSheet(model: model, request: $0)
                }
                .sheet(item: Binding(get: { LogUI.shared.request }, set: { LogUI.shared.request = $0 })) {
                    NewLogSheet(model: model, request: $0)
                }
                .sheet(item: Binding(get: { DiffLauncher.shared.request }, set: { DiffLauncher.shared.request = $0 })) {
                    OpenDiffSheet(model: model, request: $0)
                }
                .sheet(item: Binding(get: { EditorUI.shared.quickOpen }, set: { EditorUI.shared.quickOpen = $0 })) {
                    QuickOpenSheet(model: model, request: $0)
                }
                .sheet(item: Binding(get: { EditorUI.shared.saveAsPane.map(SheetID.init) }, set: { EditorUI.shared.saveAsPane = $0?.id })) {
                    EditorSaveAsSheet(model: model, paneID: $0.id)
                }
                .onOpenURL { url in
                    let link = url.absoluteString
                    if link.hasPrefix("hammock://join/") { SharingUI.shared.joinLink = link }
                    if link.hasPrefix("hammock://link/") { LinkUI.shared.open(link: link, fromOutside: true) }
                    if link.hasPrefix("hammock://pane/") { revealLinked(String(link.dropFirst("hammock://pane/".count))) }
                }
                // An open window takes hammock:// links itself. Without this the
                // Scene's matching: ["*"] opens a new window for every link
                // (each `hmk focus`, each invite); now one opens only when
                // there's none.
                .handlesExternalEvents(preferring: ["*"], allowing: ["*"])
                // Controls (and the sheets above) in the theme's colour.
                .themeTint()
        }
        .handlesExternalEvents(matching: ["*"])
        .windowToolbarStyle(.unified)
        // The toolbar's empty space drags the window (ToolbarBand lets clicks
        // through to it).
        .windowBackgroundDragBehavior(.enabled)
        .commands {
            // Shortcuts are Keys' (Shortcuts.swift), which the palette shows too.
            // Settings… (⌘,) is SwiftUI's own, from the Settings scene: on
            // macOS 27 it's added even when .appSettings is replaced, so a
            // replacement showed Settings twice. Its panes are in the palette.
            // About Hammock opens Settings ▸ About (version, updates,
            // quitting with the server, uninstalling), not AppKit's panel.
            CommandGroup(replacing: .appInfo) {
                Button("About Hammock") { SettingsTab.select(.about); SettingsOpener.show() }
                    .keyboardShortcut(Keys.about)
                if Updater.shared.isAvailable {
                    Button("Check for Updates…") { Updater.shared.checkForUpdates() }.keyboardShortcut(Keys.checkForUpdates)
                        .disabled(!Updater.shared.canCheck)
                }
            }
            CommandGroup(replacing: .newItem) {
                Button("New Tab") { newTab() }.keyboardShortcut(Keys.newTab)
                // Tools turned off in Settings ▸ Tools aren't offered (HammockTool).
                if HammockTool.browser.on {
                    Button("New Browser Tab") { newTab(browser: true) }.keyboardShortcut(Keys.newBrowserTab)
                }
                Button("New Agent Tab…") { AgentLauncher.shared.open(model) }.keyboardShortcut(Keys.newAgentTab)
                if HammockTool.log.on {
                    Button("New Log Tab…") { LogUI.shared.newTab(model) }.keyboardShortcut(Keys.newLogTab)
                }
                if HammockTool.editor.on {
                    Button("New Editor Tab") { EditorUI.shared.newTab(model) }.keyboardShortcut(Keys.newEditorTab)
                        .disabled(model.currentGroup?.canAddTabs == false)
                    Button("Quick Open…") { EditorUI.shared.showQuickOpen(model) }.keyboardShortcut(Keys.quickOpen)
                        .disabled(model.currentGroup == nil && model.groups.isEmpty)
                    Button("Open File…") { EditorUI.shared.openFile(model) }.keyboardShortcut(Keys.openFile)
                        .disabled(model.currentGroup == nil && model.groups.isEmpty)
                }
                if HammockTool.diff.on {
                    Button("New Diff Tab…") { DiffLauncher.shared.newTab(model) }.keyboardShortcut(Keys.newDiffTab)
                        .disabled(model.currentGroup?.canAddTabs == false || (model.currentGroup == nil && model.groups.isEmpty))
                }
                Button("New Scheduled Task…") { ScheduleUI.shared.new(model) }.keyboardShortcut(Keys.newScheduledTask)
                NewGroupMenuItems(model: model)
                Button("Link a Computer…") { LinkUI.shared.open() }.keyboardShortcut(Keys.linkComputer)
                Button("Pair a Device…") { SharingUI.shared.pairing = true }.keyboardShortcut(Keys.pairDevice)
                Button("Join Shared Group…") { SharingUI.shared.joinLink = NSPasteboard.general.string(forType: .string)?.trimmingCharacters(in: .whitespacesAndNewlines).hasPrefix("hammock://join/") == true ? NSPasteboard.general.string(forType: .string)!.trimmingCharacters(in: .whitespacesAndNewlines) : "" }
                    .keyboardShortcut(Keys.joinSharedGroup)
                Divider()
                Button("Rename Tab…") { if let tab = model.selectedTab { TabSheets.shared.rename(tab) } }
                    .keyboardShortcut(Keys.renameTab)
                    .disabled(model.selectedTab == nil)
                Button("Configure Group…") { if let g = currentGroup { TabSheets.shared.configuring = g } }
                    .keyboardShortcut(Keys.configureGroup)
                    .disabled(!(currentGroup.map(TabSheets.canConfigure) ?? false))
                Button("Change Group Folder…") { if let g = currentGroup { GroupFolder.choose(model, g) } }
                    .keyboardShortcut(Keys.changeGroupFolder)
                    .disabled(!(currentGroup.map(GroupFolder.canChange) ?? false))
                if Prefs.shared.bool(.groupSharing) {
                    Button("Share Group…") { if let g = currentGroup { SharingUI.shared.shareGroup = g.id } }
                        .keyboardShortcut(Keys.shareGroup)
                        .disabled(currentGroup?.isRemote != false)
                }
                Divider()
                Button("Command Palette…") { PaletteState.shared.toggle() }.keyboardShortcut(Keys.palette)
                Button("Find Session…") { PaletteState.shared.open(.sessions) }.keyboardShortcut(Keys.findSession)
                Button("Resume a Session from This Group…") { if let g = currentGroup { PaletteState.shared.open(.sessions, group: g.id, inGroup: true) } }
                    .keyboardShortcut(Keys.findSessionInGroup)
                    .disabled(currentGroup?.canResumeSessions != true)
                if model.hasLinkedComputers {
                    Menu("Find Session On") {
                        ForEach(model.computers.filter { !$0.thisComputer }, id: \.userID) { c in
                            Button(c.name) { PaletteState.shared.open(.sessions, computer: c.userID) }
                                .disabled(!c.online)
                        }
                    }
                }
            }
            CommandGroup(after: .pasteboard) {
                // Find in page, for the browser pane you're in.
                Divider()
                // (Or the editor pane you're in: its find bar.)
                Button(focusedIsEditor ? "Find…" : "Find in Page…") {
                    if focusedIsEditor { EditorUI.shared.run(model, focused, "find") } else { BrowserCommand.find.post() }
                }
                    .keyboardShortcut(Keys.findInPage)
                    .disabled(!focusedIsBrowser && !focusedIsEditor)
                Button("Find Next") {
                    if focusedIsEditor { EditorUI.shared.run(model, focused, "findNext") } else { BrowserCommand.findNext.post() }
                }
                    .keyboardShortcut(Keys.findNext)
                    .disabled(!focusedIsBrowser && !focusedIsEditor)
                Button("Find Previous") {
                    if focusedIsEditor { EditorUI.shared.run(model, focused, "findPrevious") } else { BrowserCommand.findPrevious.post() }
                }
                    .keyboardShortcut(Keys.findPrevious)
                    .disabled(!focusedIsBrowser && !focusedIsEditor)
                if focusedIsEditor {
                    Button("Go to Line…") { EditorUI.shared.run(model, focused, "gotoLine") }.keyboardShortcut(Keys.editorGoToLine)
                }
            }
            CommandGroup(replacing: .saveItem) {
                // Like a browser: ⌘W closes the pane, then the tab, then the
                // Group, and the window only once nothing is left.
                Button(closeLabel) { closeCurrent() }.keyboardShortcut(Keys.close)
                Button("Close Window") { NSApp.keyWindow?.performClose(nil) }.keyboardShortcut(Keys.closeWindow)
                // Only with an editor focused: ⌘S is the simulator's otherwise.
                if let id = focused, focusedIsEditor {
                    Divider()
                    Button("Save") { EditorUI.shared.save(model, id) }.keyboardShortcut(Keys.editorSave)
                        .disabled(model.group(ofPane: id)?.canDrive == false)
                    Button("Save As…") { EditorUI.shared.save(model, id, saveAs: true) }.keyboardShortcut(Keys.editorSaveAs)
                        .disabled(model.group(ofPane: id)?.canDrive == false)
                    if model.panes[id]?.editor.path.isEmpty == false {
                        Button(model.editorSession(id)?.showingDiff == true ? "Hide Comparison" : "Compare with File on Disk") { EditorUI.shared.showDiff(model, id) }
                            .keyboardShortcut(Keys.editorShowDiff)
                        Button("Reload from Disk") { EditorUI.shared.reload(model, id) }.keyboardShortcut(Keys.editorReload)
                            .disabled(model.group(ofPane: id)?.canDrive == false)
                    }
                }
                Button("Reopen Closed Tab or Pane") { Task { await model.reopenClosed() } }.keyboardShortcut(Keys.reopenClosed)
            }
            CommandGroup(after: .sidebar) {
                Button(sidebarHidden ? "Show Sidebar" : "Hide Sidebar") { sidebarHidden.toggle() }.keyboardShortcut(Keys.toggleSidebar)
                if HammockTool.editor.on {
                    Toggle("Wrap Lines in Editors", isOn: Prefs.shared.boolBinding(.editorWrap)).keyboardShortcut(Keys.editorWrap)
                }
                if model.hasLinkedComputers {
                    Menu("Show Computer") {
                        // ⌃⌘0 and ⌃⌘1…9: free in macOS (its desktops are ⌃1…9) and here (tabs are ⌘1…9).
                        Button("All Computers") { model.computerFilter = "" }
                            .keyboardShortcut(Keys.allComputers)
                        Divider()
                        ForEach(Array(model.computers.enumerated()), id: \.element.userID) { i, c in
                            Button(c.thisComputer ? "This Mac" : c.name) { model.computerFilter = c.thisComputer ? model.myUserID : c.userID }
                                .keyboardShortcut(Keys.showComputer(i))
                        }
                    }
                }
                if !model.computers.isEmpty {
                    Menu("Computer Icon") {
                        ForEach(model.computers, id: \.userID) { c in
                            Button(c.thisComputer ? "This Mac…" : "\(c.name)…") { LinkUI.shared.customise(c) }
                                .disabled(!c.online)
                        }
                    }
                }
                if !model.screenComputers.isEmpty {
                    Menu("Show Screen") {
                        ForEach(model.screenComputers, id: \.userID) { c in
                            Button(c.thisComputer ? "This Mac" : c.name) {
                                ScreenViewerUI.shared.open(model, computerID: c.userID, name: c.thisComputer ? "This Mac" : c.name)
                            }
                        }
                    }
                }
                Button("Scheduled Tasks") { ScheduleUI.shared.showList() }.keyboardShortcut(Keys.scheduledTasks)
                if HammockTool.diff.on {
                    Button("Review Changes") { DiffUI.review(model) }.keyboardShortcut(Keys.reviewChanges)
                        .disabled(model.currentGroup?.canAddTabs == false || (model.currentGroup == nil && model.groups.isEmpty))
                }
                Divider()
                Button("Next Tab") { model.cycleTab(by: 1) }.keyboardShortcut(Keys.nextTab)
                Button("Previous Tab") { model.cycleTab(by: -1) }.keyboardShortcut(Keys.previousTab)
                Button("Next Group") { model.cycleGroup(by: 1) }.keyboardShortcut(Keys.nextGroup)
                Button("Previous Group") { model.cycleGroup(by: -1) }.keyboardShortcut(Keys.previousGroup)
                Divider()
                ForEach(0..<9) { i in
                    Button("Tab \(i + 1)") { model.selectTab(at: i) }.keyboardShortcut(Keys.tab(i))
                }
                Divider()
                Menu("Theme") {
                    Picker("Theme", selection: Binding(get: { AppConfig.shared.appThemeName }, set: { AppConfig.shared.setAppTheme($0) })) {
                        ForEach(AppTheme.all) { Text($0.name).tag($0.name) }
                        Divider()
                        Text("Custom").tag(AppTheme.custom)
                    }
                    .pickerStyle(.inline)
                    .labelsHidden()
                    Divider()
                    Button("Appearance Settings…") {
                        SettingsTab.select(.appearance)
                        SettingsOpener.show()
                    }
                }
                Button("Bigger") { TerminalFont.zoom(by: 1) }.keyboardShortcut(Keys.bigger)
                Button("Smaller") { TerminalFont.zoom(by: -1) }.keyboardShortcut(Keys.smaller)
                Button("Actual Size") { TerminalFont.zoom(by: 0) }.keyboardShortcut(Keys.actualSize)
            }
            CommandGroup(after: .help) {
                Button("Welcome to Hammock…") { Onboarding.shared.show() }.keyboardShortcut(Keys.welcome)
                Button("Acknowledgements") { AcknowledgementsUI.shared.show() }.keyboardShortcut(Keys.acknowledgements)
            }
            CommandMenu("Pane") {
                Button("Next Pane") { model.cyclePane(by: 1) }.keyboardShortcut(Keys.nextPane)
                Button("Previous Pane") { model.cyclePane(by: -1) }.keyboardShortcut(Keys.previousPane)
                Divider()
                Button("Split Right") { split(.horizontal) }.keyboardShortcut(Keys.splitRight)
                Button("Split Down") { split(.vertical) }.keyboardShortcut(Keys.splitDown)
                Button("Split Left") { split(.horizontal, before: true) }.keyboardShortcut(Keys.splitLeft)
                Button("Split Up") { split(.vertical, before: true) }.keyboardShortcut(Keys.splitUp)
                Button(focusedIsLog ? "Toggle Log View" : "Toggle Agent View") {
                    guard let id = focused else { return }
                    if focusedIsLog { LogUI.shared.setRaw(id, !LogUI.shared.showsRaw(id)) } else { AgentModes.shared.toggle(id) }
                }
                    .keyboardShortcut(Keys.toggleAgentView)
                    .disabled(focused.flatMap { model.panes[$0] }.map { !model.supportsAgentView($0) } ?? true)
                Button("Copy Session Link") { SessionLink.copy(model, focused.flatMap { model.panes[$0] }) }
                    .keyboardShortcut(Keys.copySessionLink)
                    .disabled(focused.flatMap { model.panes[$0] }?.sessionURL == nil)
                if HammockTool.browser.on {
                    Button("Split Right with Browser") {
                        if let id = focused { Task { await model.splitBrowser(id, direction: .horizontal) } }
                    }.keyboardShortcut(Keys.splitBrowserRight)
                    Button("Split Down with Browser") {
                        if let id = focused { Task { await model.splitBrowser(id, direction: .vertical) } }
                    }.keyboardShortcut(Keys.splitBrowserDown)
                }
                Button("Show DevTools") { if let id = focused { Task { await model.showDevTools(id) } } }
                    .keyboardShortcut(Keys.devTools)
                    .disabled(!focusedIsBrowser || focused.flatMap { model.panes[$0] }?.url.hasPrefix("hmk-devtools:") == true)
                if HammockTool.log.on {
                    Button("Split Right with Log…") { if let id = focused { LogUI.shared.split(model, id, .horizontal) } }
                        .keyboardShortcut(Keys.splitLogRight)
                        .disabled(focused == nil)
                }
                if HammockTool.diff.on {
                    Button("Split Right with Diff…") { if let id = focused { DiffLauncher.shared.split(model, id, .horizontal) } }
                        .keyboardShortcut(Keys.splitDiffRight)
                        .disabled(focused == nil)
                }
                if HammockTool.editor.on {
                    Button("Split Right with Editor") { if let id = focused { EditorUI.shared.split(model, id, .horizontal) } }
                        .keyboardShortcut(Keys.splitEditorRight)
                        .disabled(focused == nil)
                    Button("Split Down with Editor") { if let id = focused { EditorUI.shared.split(model, id, .vertical) } }
                        .keyboardShortcut(Keys.splitEditorDown)
                        .disabled(focused == nil)
                }
                if HammockTool.log.on {
                    Button("Show Simulator Log") { if let id = focused { LogUI.shared.showSimulatorLog(model, id) } }
                        .keyboardShortcut(Keys.showSimulatorLog)
                        .disabled(focused.flatMap { model.panes[$0] }?.kind != .simulator)
                }
                if HammockTool.simulator.on {
                    Menu("Split Right with Simulator") {
                        SimulatorItems { udid in if let id = focused { Task { await model.splitSimulator(id, direction: .horizontal, udid: udid) } } }
                    }
                }
                // Only with a simulator focused: ⌘← and ⌘→ belong to the terminal otherwise.
                if let id = focused, model.panes[id]?.kind == .simulator {
                    Divider()
                    Button("Home") { model.pressSimulatorButton(id, "home") }.keyboardShortcut(Keys.simHome)
                    Button("Lock") { model.pressSimulatorButton(id, "lock") }.keyboardShortcut(Keys.simLock)
                    Button("Rotate Left") { model.rotateSimulator(id, "left") }.keyboardShortcut(Keys.simRotateLeft)
                    Button("Rotate Right") { model.rotateSimulator(id, "right") }.keyboardShortcut(Keys.simRotateRight)
                    Button("Toggle Software Keyboard") { model.pressSimulatorButton(id, "keyboard") }
                        .keyboardShortcut(Keys.simKeyboard)
                    Button("Screenshot to File") { SimulatorShot.saveToFile(model, id) }.keyboardShortcut(Keys.simScreenshotFile)
                    Button("Screenshot to Clipboard") { SimulatorShot.copyToClipboard(model, id) }
                        .keyboardShortcut(Keys.simScreenshotCopy)
                    if model.panes[id]?.deviceFoldable == true {
                        Button((model.panes[id]?.deviceHinge ?? 0) > 120 ? "Close Phone" : "Open Phone") { model.toggleFold(id) }
                            .keyboardShortcut(Keys.simFold)
                        Button("Half Open Phone") { model.foldSimulator(id, "half") }.keyboardShortcut(Keys.simHalfOpen)
                    }
                    Button("Shut Down Simulator") { model.shutDownSimulator(id) }.keyboardShortcut(Keys.simShutDown)
                }
                Divider()
                Button(focused.map { PaneZoom.shared.isZoomed($0, model: model) } == true ? "Unzoom Pane" : "Zoom Pane") {
                    if let id = focused { PaneZoom.shared.toggle(id, model: model) }
                }
                .keyboardShortcut(Keys.zoomPane)
                .disabled(focused.flatMap { model.floating($0) } == nil && (model.selectedTab.map { AppModel.paneIDs($0.layout).count < 2 } ?? true))
                Button("Move Pane to New Tab") { if let id = focused { Task { await model.breakOut(id) } } }
                    .keyboardShortcut(Keys.movePaneToNewTab)
                    .disabled(model.selectedTab.map { AppModel.paneIDs(of: $0).count < 2 } ?? true)
                Divider()
                FloatCommands(model: model, paneID: focused)
                Button("Close Pane") { if let id = focused { CloseGuard.confirm(model, panes: [id]) { Task { await model.closePane(id) } } } }
                    .keyboardShortcut(Keys.closePane)
            }
        }
        // The system's accent colour, not the theme's: switches, popups and
        // the selected pane look as they do in System Settings.
        Settings {
            SettingsView(model: model)
        }
    }

    /// The focused pane of the tab on screen, or none (an empty Group
    /// shown): the model's focus outlives the tab it was in, and pane
    /// commands mustn't reach a tab you can't see.
    private var focused: String? {
        guard let tab = model.selectedTab else { return nil }
        if let id = model.focusedPaneID, model.panes[id]?.tabID == tab.id { return id }
        return tab.focusedPaneID
    }

    private var focusedIsLog: Bool { focused.flatMap { model.panes[$0] }?.isLog == true }

    private var focusedIsEditor: Bool { focused.flatMap { model.panes[$0] }?.isEditor == true }

    /// The focused pane is a page on this Mac (find in page works there).
    private var focusedIsBrowser: Bool {
        guard let id = focused, let p = model.panes[id] else { return false }
        return p.kind == .browser && model.group(ofPane: id)?.isRemote != true
    }

    private var closeLabel: String {
        guard let tab = model.selectedTab else {
            if model.selectedEmptyGroup != nil { return "Close Group" }
            return model.groups.isEmpty || AppConfig.shared.keepEmptyGroups ? "Close Window" : "Close Group"
        }
        if AppModel.paneIDs(of: tab).count > 1 { return "Close Pane" }
        if (model.group(of: tab)?.tabIds.count ?? 0) > 1 || AppConfig.shared.keepEmptyGroups { return "Close Tab" }
        return "Close Group"
    }

    private func closeCurrent() {
        // Other windows (Settings) just close.
        guard let window = NSApp.keyWindow, window === WorkspaceWindow.window else {
            NSApp.keyWindow?.performClose(nil)
            return
        }
        // ⌘W closes the focused pane (or the tab it's the last of, and the
        // Group, unless Groups are kept), or the empty Group you're on.
        let keep = AppConfig.shared.keepEmptyGroups
        let close = { Task { if !(await model.closeCurrent(keepingGroups: keep)) { window.performClose(nil) } } }
        if let id = focused { CloseGuard.confirm(model, panes: [id]) { _ = close() } } else { _ = close() }
    }

    private func newTab(browser: Bool = false) {
        let group = model.currentGroup?.id ?? model.groups.first?.id
        Task {
            var id = group
            if id == nil { id = await model.createGroup(name: "Home") }
            guard let id else { return }
            if browser { await model.createBrowserTab(in: id) } else { await model.createTab(in: id) }
        }
    }

    private func split(_ d: SplitDirection, before: Bool = false) {
        if let id = focused { Task { await model.split(id, direction: d, before: before) } }
    }

    /// The Group of the tab you're on, or the empty one shown.
    private var currentGroup: WorkspaceGroup? { model.currentGroup }
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        RenderBench.runIfRequested() // measures, then quits
        RenderStats.startIfRequested()
        NSApp.setActivationPolicy(.regular)
        AppConfig.shared.applyAppearance()
        NSApp.activate()
        guard AppMover.offerIfNeeded() else { return } // moving: this copy quits
        AppMover.finishMove()
        Notifier.shared.setUp()
        AppConfig.shared.followUsageChoice()
        DebugSnapshot.scheduleIfRequested()
        DebugSnapshot.typeIfRequested()
        MenuHelper.start()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
}

struct ContentView: View {
    @Bindable var model: AppModel

    /// The + makes the Group on the computer the sidebar shows.
    private var newGroupHelp: String {
        guard model.hasLinkedComputers, let c = model.computer(model.computerFilter), !c.thisComputer else { return Keys.hint("New Group", "new_group") }
        return Keys.hint("New Group on \(c.name)", "new_group")
    }
    private var layout: SidebarLayout { .current }
    @AppStorage(SidebarHiding.key) private var sidebarHidden = false
    /// The split view's own state, so showing and hiding slide: it follows
    /// the setting (menu, palette) in an animation, and the setting follows
    /// it (the window's own sidebar button). Bound to the setting directly,
    /// every change snapped.
    @State private var columns: NavigationSplitViewVisibility = UserDefaults.standard.bool(forKey: SidebarHiding.key) ? .detailOnly : .all
    @Environment(\.scenePhase) private var phase
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        // Panes hold still while the sidebar slides (PaneMotion), whichever
        // way it's toggled: the window's button sets this binding.
        NavigationSplitView(columnVisibility: Binding(get: { columns }, set: { c in
            if c != columns { PaneMotion.shared.hold(for: PaneMotion.sidebarDuration) }
            columns = c
        })) {
            SidebarView(model: model)
                .navigationSplitViewColumnWidth(min: 220, ideal: 272, max: 380)
                // New Group, beside the button that hides the sidebar.
                .toolbar {
                    ToolbarItem {
                        Menu {
                            NewGroupMenuItems(model: model)
                        } label: { Label("New Group", systemImage: "plus") }
                            .help(newGroupHelp)
                    }
                }
                // The window theme's tint, over the column's own material.
                .background { SidebarTint().ignoresSafeArea() }
        } detail: {
            if let tab = model.selectedTab {
                VStack(spacing: 0) {
                    if layout == .horizontal, let group = model.group(of: tab) {
                        TabStrip(model: model, group: group)
                    }
                    TabDetailView(model: model, tab: tab)
                        .id(tab.id)
                }
                // Spacious spacing: the whole tab, toolbar and all, on an
                // empty Group's background (the sidebar's glass and tint),
                // the panes as cards on it. Compact: the panes fill the tab,
                // under a toolbar band.
                .modifier(TabBackground(spacious: PaneLook.spacious))
            } else if let group = model.selectedEmptyGroup {
                EmptyGroupView(model: model, group: group)
                    .id(group.id)
                    .emptyDetailBackground()
            } else if model.connected && model.groups.isEmpty {
                NoGroupsView(model: model)
                    .emptyDetailBackground()
            } else if !model.connected {
                ContentUnavailableView("Connecting to hmkd…", systemImage: "bolt.horizontal",
                                       description: Text(model.lastError ?? "Starting the Hammock daemon."))
                    .emptyDetailBackground()
            } else {
                ContentUnavailableView("Select a Tab", systemImage: "sidebar.left")
                    .emptyDetailBackground()
            }
        }
        // AppKit's toolbar background stays on: on macOS 26 it's what blurs the
        // sidebar's rows as they scroll under the window's buttons (hidden,
        // the whole window lost it). ToolbarBand draws over it above the panes.
        // Hidden, it comes in from the window's left edge.
        .overlay(alignment: .leading) {
            if sidebarHidden { SidebarPeek(model: model) }
        }
        .overlay(alignment: .bottom) {
            ToastOverlay(model: model, tint: AppConfig.shared.toolbarTint(dark: colorScheme == .dark).map { Color(nsColor: $0) })
                .padding(.bottom, 18)
        }
        .overlay(alignment: .bottomTrailing) { InAppNoticeStack() }
        .modifier(TabSheets.Host(model: model))
        .modifier(ScheduleUI.Host(model: model))
        // Hide Sidebar from the menu or palette: slide, as the window's button does.
        .onChange(of: sidebarHidden) { _, hidden in
            let want: NavigationSplitViewVisibility = hidden ? .detailOnly : .all
            guard columns != want else { return }
            PaneMotion.shared.hold(for: PaneMotion.sidebarDuration)
            withAnimation { columns = want }
        }
        // The window's button: remembered, and the menu's name follows.
        .onChange(of: columns) { _, c in
            if sidebarHidden != (c == .detailOnly) { sidebarHidden = c == .detailOnly }
        }
        .onChange(of: colorScheme, initial: true) { sendColors() }
        .onReceive(NotificationCenter.default.publisher(for: AppConfig.changed)) { _ in sendColors() }
        .onChange(of: model.selectedTabID) { reportPresence() }
        .onChange(of: phase) { reportPresence() }
        .onChange(of: model.connected) { reportPresence() }
        .onChange(of: model.selectedTab.map { AppModel.shownPaneIDs(of: $0) } ?? []) { reportPresence() }
        .modifier(SharingHooks(model: model, presence: reportPresence, seed: seedProfile))
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in reportPresence() }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didResignActiveNotification)) { _ in reportPresence() }
        // The Dock badge follows the live count (it also has to go down).
        .onChange(of: model.attention.filter { $0.state == .needsInput }.count + model.grants.count) { Notifier.shared.updateBadge(model) }
    }

    private func sendColors() {
        model.setColors(AppConfig.shared.theme(isDark: colorScheme == .dark), dark: colorScheme == .dark)
    }

    /// The first time, others see you with your Mac account's name and picture.
    private func seedProfile() {
        guard !model.myUserID.isEmpty, !UserDefaults.standard.bool(forKey: "profileSeeded") else { return }
        UserDefaults.standard.set(true, forKey: "profileSeeded")
        if model.me?.avatar.isEmpty ?? true, let picture = AccountPicture.png() {
            Task { await model.setProfile(name: model.me?.name ?? NSFullUserName(), avatar: picture) }
        }
    }

    private func reportPresence() {
        let visible = model.selectedTab.map { AppModel.shownPaneIDs(of: $0) } ?? []
        model.reportPresence(visible: visible, foreground: NSApp.isActive)
    }
}

/// Marks the workspace window so ⌘W runs the pane/tab/Group chain only there.
struct WorkspaceWindow: NSViewRepresentable {
    /// The workspace window. (Not an identifier: SwiftUI manages those itself and
    /// replaces anything set on its windows.)
    nonisolated(unsafe) static weak var window: NSWindow?

    final class Marker: NSView {
        /// The window's size and position, kept across launches. Not AppKit's
        /// frame autosave: naming the window saves its current frame at once,
        /// so SwiftUI's first, default sizing overwrote the one to restore.
        static let frameKey = "workspaceWindowFrame"
        private var observers: [NSObjectProtocol] = []
        private var restoring = true

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            guard let window, WorkspaceWindow.window !== window else { return }
            WorkspaceWindow.window = window
            window.setFrameAutosaveName("") // SwiftUI's own name saves but never restores
            // No hairline under the toolbar: it overhung the sidebar's divider,
            // and the pane headers already mark where the toolbar ends.
            window.titlebarSeparatorStyle = .none
            // Again after SwiftUI's sizing passes, which come after this; only
            // then do moves and resizes count as yours.
            restore(window)
            DispatchQueue.main.async { self.restore(window) }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
                self.restore(window)
                self.restoring = false
            }
            for name in [NSWindow.didMoveNotification, NSWindow.didResizeNotification, NSWindow.willCloseNotification] {
                observers.append(NotificationCenter.default.addObserver(forName: name, object: window, queue: .main) { [weak self, weak window] _ in
                    MainActor.assumeIsolated {
                        guard let self, let window, !self.restoring, !window.styleMask.contains(.fullScreen) else { return }
                        UserDefaults.standard.set(window.frameDescriptor, forKey: Self.frameKey)
                    }
                })
            }
        }

        private func restore(_ window: NSWindow) {
            if let saved = UserDefaults.standard.string(forKey: Self.frameKey) {
                window.setFrame(from: saved)
            } else if let screen = (window.screen ?? NSScreen.main)?.visibleFrame {
                // First launch: most of the screen, centred, rather than SwiftUI's small default.
                let size = CGSize(width: min(screen.width * 0.88, 1600), height: min(screen.height * 0.88, 1050))
                window.setFrame(CGRect(x: screen.midX - size.width / 2, y: screen.midY - size.height / 2, width: size.width, height: size.height), display: true)
            }
        }
    }

    func makeNSView(context: Context) -> Marker { Marker() }
    func updateNSView(_ v: Marker, context: Context) {}
}

/// With no tab selected, the detail is the sidebar's colour (its glass and
/// tint), not the window's, so the window reads as one surface until a tab opens.
extension View {
    func emptyDetailBackground() -> some View {
        frame(maxWidth: .infinity, maxHeight: .infinity)
            .background {
                ZStack { SidebarGlass(); SidebarTint() }.ignoresSafeArea()
            }
    }
}

/// A selected tab's backdrop: see the detail column above.
struct TabBackground: ViewModifier {
    let spacious: Bool

    func body(content: Content) -> some View {
        if spacious {
            content.emptyDetailBackground()
        } else {
            content.overlay(alignment: .top) { ToolbarBand() }
        }
    }
}

/// AppKit's sidebar material, blending with the desktop behind the window
/// (or, floating over the panes, with them: the peek sidebar).
struct SidebarGlass: NSViewRepresentable {
    var blending: NSVisualEffectView.BlendingMode = .behindWindow

    func makeNSView(context: Context) -> NSVisualEffectView {
        let v = NSVisualEffectView()
        v.material = .sidebar
        v.blendingMode = blending
        v.state = .followsWindowActiveState
        return v
    }

    func updateNSView(_ v: NSVisualEffectView, context: Context) {}
}

/// AppKit's own titlebar material, so the band matches a native toolbar.
struct TitlebarMaterial: NSViewRepresentable {
    /// Only drawn: clicks pass to the window's background, which drags it.
    final class Band: NSVisualEffectView {
        override func hitTest(_ point: NSPoint) -> NSView? { nil }
    }

    func makeNSView(context: Context) -> NSVisualEffectView {
        let v = Band()
        v.material = .titlebar
        v.blendingMode = .withinWindow
        v.state = .followsWindowActiveState
        return v
    }

    func updateNSView(_ v: NSVisualEffectView, context: Context) {}
}

/// A string as a sheet's identity.
struct SheetID: Identifiable {
    let id: String
}

/// Kept apart from ContentView's chain, which the type checker struggles with.
private struct SharingHooks: ViewModifier {
    let model: AppModel
    let presence: () -> Void
    let seed: () -> Void

    func body(content: Content) -> some View {
        content
            .onChange(of: model.focusedPaneID) { presence() }
            .onChange(of: model.myUserID, initial: true) { seed() }
    }
}

/// The toolbar's band over the detail column, drawn over AppKit's: its
/// titlebar material, the toolbar's height, with a hairline under it,
/// starting exactly where the panes do.
struct ToolbarBand: View {
    @Environment(\.displayScale) private var scale
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        GeometryReader { g in
            TitlebarMaterial()
                // The window theme's toolbar colour, nearly solid over it.
                .overlay { AppConfig.shared.toolbarTint(dark: scheme == .dark).map { Color(nsColor: $0).opacity(0.9) } }
                .frame(height: g.safeAreaInsets.top)
                .overlay(alignment: .bottom) {
                    Rectangle().fill(Color(nsColor: .separatorColor)).frame(height: 1 / scale)
                }
                .ignoresSafeArea(edges: .top)
        }
        .allowsHitTesting(false)
    }
}

/// The nth computer's shortcut: its digit (1…9), none past the ninth.