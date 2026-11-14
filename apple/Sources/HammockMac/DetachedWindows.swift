import AppKit
import HammockKit
import SwiftUI

// Open in New Window: a tab, a Group or a pane shown in a window of its own,
// e.g. to share just that on a call. It's the workspace's own tab, Group or
// pane, not a copy: what you do there happens in the workspace, and closing
// the window closes nothing. A tab or pane fills its window (no sidebar, no
// tab bar); a Group's window has its tabs, in a sidebar or a tab bar as the
// main window does, and a selected tab of its own.
//
// Its panes are the main window's too when both show them: one focus (the
// daemon's), and the window you were in last sets a terminal's size (the
// other shows it at that size) and has a browser page (the other shows it's
// there: BrowserHost). Windows follow what they show (a pane moved to another
// tab, a tab to another Group), close when it closes, and open again at
// launch.

/// What a window of its own shows.
enum WindowSubject: Hashable, Codable {
    case pane(String)
    case tab(String)
    case group(String)

    init?(_ w: Hammock_V1_OpenWindow) {
        switch w.kind {
        case .pane: self = .pane(w.id)
        case .tab: self = .tab(w.id)
        case .group: self = .group(w.id)
        default: return nil
        }
    }
}

/// One window of its own: what it shows, and (a Group's) its selected tab.
@MainActor @Observable
final class WindowScope: Identifiable {
    let id = UUID()
    let subject: WindowSubject
    /// A Group's window: the tab it shows (nil, or gone from the Group: the
    /// one last focused there, else its first).
    var selectedTabID: String?
    @ObservationIgnored weak var window: NSWindow?

    init(_ subject: WindowSubject, selectedTabID: String? = nil) {
        self.subject = subject
        self.selectedTabID = selectedTabID
    }

    /// Whether what it shows is still there.
    func exists(in model: AppModel) -> Bool {
        switch subject {
        case .pane(let id): model.panes[id] != nil
        case .tab(let id): model.tabs[id] != nil
        case .group(let id): model.groups.contains { $0.id == id }
        }
    }

    func group(in model: AppModel) -> WorkspaceGroup? {
        switch subject {
        case .pane(let id): model.group(ofPane: id)
        case .tab(let id): model.tabs[id].flatMap { model.group(of: $0) }
        case .group(let id): model.groups.first { $0.id == id }
        }
    }

    /// The tab it shows: a pane's, the tab, or a Group's selected one.
    func tab(in model: AppModel) -> WorkspaceTab? {
        switch subject {
        case .pane(let id): return model.panes[id].flatMap { model.tabs[$0.tabID] }
        case .tab(let id): return model.tabs[id]
        case .group(let id):
            guard let g = model.groups.first(where: { $0.id == id }) else { return nil }
            if let t = selectedTabID.flatMap({ model.tabs[$0] }), t.groupID == id { return t }
            return currentTab(of: g, in: model)
        }
    }

    /// The pane that has the keyboard here: the one focus, when it's in
    /// what this shows, else the tab's own focused pane.
    func focused(in model: AppModel) -> String? {
        if case .pane(let id) = subject { return id }
        guard let tab = tab(in: model) else { return nil }
        if let f = model.focusedPaneID, model.panes[f]?.tabID == tab.id { return f }
        return tab.focusedPaneID
    }

    /// The panes on screen here (for presence: agents you're watching).
    func shownPaneIDs(in model: AppModel) -> [String] {
        if case .pane(let id) = subject { return [id] }
        return tab(in: model).map { AppModel.shownPaneIDs(of: $0) } ?? []
    }

    /// A Group's window with Vertical Tabs: its tabs in a sidebar.
    var showsSidebar: Bool {
        if case .group = subject { return SidebarLayout.current != .horizontal }
        return false
    }

    var isGroup: Bool { if case .group = subject { return true } else { return false } }

    /// The window's title: the pane's or tab's name, or the Group's.
    func title(in model: AppModel) -> String {
        switch subject {
        case .pane(let id): return model.panes[id]?.displayName ?? "Pane"
        case .tab: return tab(in: model).map { model.title(of: $0) } ?? "Tab"
        case .group: return group(in: model).map { model.title(of: $0) } ?? "Group"
        }
    }

    /// Under the title: the Group it's in (a tab's or pane's window).
    func subtitle(in model: AppModel) -> String {
        if case .group = subject { return tab(in: model).map { model.title(of: $0) } ?? "" }
        return group(in: model).map { model.title(of: $0) } ?? ""
    }
}

private struct WindowScopeKey: EnvironmentKey {
    static let defaultValue: WindowScope? = nil
}

extension EnvironmentValues {
    /// The window of its own this is in (nil: the main window).
    var windowScope: WindowScope? {
        get { self[WindowScopeKey.self] }
        set { self[WindowScopeKey.self] = newValue }
    }
}

/// The windows of their own: opening them, which is in front (menus act on
/// it), and keeping them across launches.
@MainActor @Observable
final class DetachedWindows: NSObject, NSWindowDelegate {
    static let shared = DetachedWindows()

    private(set) var scopes: [WindowScope] = []
    /// The window of its own you're in, if you're in one: the menu bar's
    /// commands act on what it shows. (Not cleared by another app coming
    /// forward: only by one of Hammock's other windows becoming key.)
    private(set) var keyScope: WindowScope?
    @ObservationIgnored private weak var model: AppModel?
    @ObservationIgnored private var restored = false
    /// Saved frames aren't overwritten while windows open at launch.
    @ObservationIgnored private var restoring = false
    @ObservationIgnored private var observers: [NSObjectProtocol] = []

    private static let savedKey = "detachedWindows"

    private struct Saved: Codable {
        let subject: WindowSubject
        let frame: String
        var tab: String?
    }

    /// Wires up the model: hmk window / open_window, tabs made in a Group's
    /// window, and which window is in front.
    func attach(_ model: AppModel) {
        self.model = model
        model.onOpenWindow = { [weak self, weak model] w in
            guard let self, let model, let s = WindowSubject(w) else { return }
            NSApp.activate()
            self.open(s, model: model)
        }
        model.showsTabElsewhere = { [weak self] id, groupID in
            guard let s = self?.keyScope, case .group(let g) = s.subject, g == groupID else { return false }
            s.selectedTabID = id
            return true
        }
        observers.append(NotificationCenter.default.addObserver(forName: NSApplication.willTerminateNotification, object: nil, queue: .main) { _ in
            MainActor.assumeIsolated { DetachedWindows.terminating = true }
        })
        observers.append(NotificationCenter.default.addObserver(forName: NSWindow.didBecomeKeyNotification, object: nil, queue: .main) { [weak self] n in
            MainActor.assumeIsolated {
                guard let self, let w = n.object as? NSWindow else { return }
                // Panels (the palette's, alerts, menus) aren't windows you're "in".
                guard !(w is NSPanel) else { return }
                self.keyScope = self.scopes.first { $0.window === w }
            }
        })
    }

    /// Opens what it's given in a new window, in front.
    func open(_ subject: WindowSubject, model: AppModel, frame: String? = nil, selectedTab: String? = nil) {
        guard WindowScope(subject).exists(in: model) else { return }
        let scope = WindowScope(subject, selectedTabID: selectedTab)
        if case .group = subject, selectedTab == nil {
            // The Group's tab you're on in the main window, or the one last focused there.
            if let t = model.selectedTab, case .group(let g) = subject, t.groupID == g { scope.selectedTabID = t.id }
        }
        let window = DetachedWindow(contentRect: NSRect(x: 0, y: 0, width: 900, height: 600),
                                    styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView],
                                    backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.titlebarAppearsTransparent = true
        window.titleVisibility = .hidden
        window.titlebarSeparatorStyle = .none
        window.tabbingMode = .disallowed
        window.collectionBehavior.insert(.fullScreenPrimary)
        // The sidebar's glass blends with what's behind the window, as the main window's does.
        window.isOpaque = false
        window.backgroundColor = .clear
        window.minSize = NSSize(width: 360, height: 240)
        let host = NSHostingView(rootView: DetachedWindowView(model: model, scope: scope)
            .environment(\.windowScope, scope)
            .themeTint())
        host.sceneBridgingOptions = []
        window.contentView = host
        window.delegate = self
        scope.window = window
        window.title = scope.title(in: model)
        scopes.append(scope)
        if let frame {
            window.setFrame(from: frame)
        } else {
            placeNew(window, for: subject, model: model)
        }
        window.makeKeyAndOrderFront(nil)
        save()
    }

    /// A new window: the size of the main window's panes for a tab or Group
    /// (a pane: its own size), cascaded off the main window.
    private func placeNew(_ window: NSWindow, for subject: WindowSubject, model: AppModel) {
        let main = WorkspaceWindow.window
        let screen = (main?.screen ?? NSScreen.main)?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        var size = CGSize(width: 1000, height: 680)
        if case .pane = subject { size = CGSize(width: 820, height: 560) }
        if let m = main?.frame { size = CGSize(width: min(size.width, m.width), height: min(size.height, m.height)) }
        let origin = main.map { CGPoint(x: $0.frame.minX + 60 + CGFloat(scopes.count % 6) * 26, y: $0.frame.maxY - size.height - 60 - CGFloat(scopes.count % 6) * 26) }
            ?? CGPoint(x: screen.midX - size.width / 2, y: screen.midY - size.height / 2)
        var r = CGRect(origin: origin, size: size)
        r.origin.x = min(max(r.minX, screen.minX), screen.maxX - r.width)
        r.origin.y = min(max(r.minY, screen.minY), screen.maxY - r.height)
        window.setFrame(r, display: false)
    }

    /// Closes a window whose tab, Group or pane is gone.
    func close(_ scope: WindowScope) {
        scope.window?.close()
    }

    /// The panes on screen in these windows (presence).
    func shownPaneIDs(in model: AppModel) -> [String] {
        scopes.filter { $0.window?.isVisible == true && $0.window?.isMiniaturized == false }.flatMap { $0.shownPaneIDs(in: model) }
    }

    /// The windows open when Hammock last quit, once the workspace has
    /// arrived (those whose tab, Group or pane has gone stay closed).
    func restoreOnce(_ model: AppModel) {
        guard !restored else { return }
        restored = true
        guard let data = UserDefaults.standard.data(forKey: Self.savedKey),
              let saved = try? JSONDecoder().decode([Saved].self, from: data) else { return }
        restoring = true
        let key = NSApp.keyWindow
        for s in saved { open(s.subject, model: model, frame: s.frame, selectedTab: s.tab) }
        // The main window stays in front, as it was.
        key?.makeKeyAndOrderFront(nil)
        restoring = false
        save()
    }

    /// Whether the workspace has arrived since launch: until it has, a
    /// window's tab looks gone when it's only not here yet.
    var workspaceArrived: Bool { restored }

    func save() {
        guard !restoring else { return }
        let saved = scopes.compactMap { s in s.window.map { Saved(subject: s.subject, frame: $0.frameDescriptor, tab: s.selectedTabID) } }
        if let data = try? JSONEncoder().encode(saved) { UserDefaults.standard.set(data, forKey: Self.savedKey) }
    }

    // MARK: NSWindowDelegate

    func windowWillClose(_ n: Notification) {
        guard let w = n.object as? NSWindow, let i = scopes.firstIndex(where: { $0.window === w }) else { return }
        let scope = scopes.remove(at: i)
        if keyScope === scope { keyScope = nil }
        if PaletteState.shared.window == scope.id { PaletteState.shared.isOpen = false }
        // Quitting closes every window: they're to come back next time.
        guard !Self.terminating else { return }
        save()
    }

    func windowDidMove(_ n: Notification) { save() }
    func windowDidEndLiveResize(_ n: Notification) { save() }

    /// Set as the app quits, so the windows it closes are kept for next time.
    static var terminating = false
}

/// A window of its own: ⌘W and the close button close it, and nothing in it.
final class DetachedWindow: NSWindow {
    override var canBecomeKey: Bool { true }
    override var canBecomeMain: Bool { true }
}

// MARK: - Views

/// What a window of its own shows: the pane, the tab, or the Group (its
/// tabs beside or above the selected one), under a title bar of the
/// window's own surface.
struct DetachedWindowView: View {
    let model: AppModel
    let scope: WindowScope
    @Environment(\.colorScheme) private var colorScheme
    @State private var titlebar: CGFloat = 28

    private var horizontal: Bool { SidebarLayout.current == .horizontal }

    var body: some View {
        HStack(spacing: 0) {
            if scope.showsSidebar, let group = scope.group(in: model) {
                DetachedGroupSidebar(model: model, scope: scope, group: group)
                    .frame(width: 240)
            }
            detail
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        // One surface, title bar and all: the sidebar's material and tint, as
        // the main window has behind everything (ColumnGlass, its detail's,
        // only matches inside a split view).
        .background { ZStack { SidebarGlass(); SidebarTint() }.ignoresSafeArea() }
        // The title, in the title bar (which the content sits under).
        .overlay(alignment: .top) {
            title
                .frame(height: titlebar)
                .frame(maxWidth: .infinity)
                .padding(.leading, scope.showsSidebar ? 240 : 0)
                .offset(y: -titlebar)
                .allowsHitTesting(false)
        }
        .onGeometryChange(for: CGFloat.self) { $0.safeAreaInsets.top } action: { if $0 > 0 { titlebar = $0 } }
        // The palette, opened from this window (PaletteState.window).
        .overlay {
            if PaletteState.shared.isOpen, PaletteState.shared.window == scope.id {
                if PaletteState.shared.mode == .sessions { SessionFinder(model: model) } else { CommandPalette(model: model) }
            }
        }
        .frame(minWidth: 360, minHeight: 240)
        .onChange(of: scope.title(in: model), initial: true) { _, t in scope.window?.title = t }
        // Gone from the workspace: the window goes too.
        .onChange(of: model.connected && DetachedWindows.shared.workspaceArrived && !scope.exists(in: model)) { _, gone in
            if gone { DetachedWindows.shared.close(scope) }
        }
        .onChange(of: scope.selectedTabID) { DetachedWindows.shared.save() }
        .onChange(of: scope.shownPaneIDs(in: model)) { WorkspacePresence.report(model) }
        .onAppear { WorkspacePresence.report(model) }
        .onDisappear { WorkspacePresence.report(model) }
    }

    @ViewBuilder
    private var detail: some View {
        switch scope.subject {
        case .pane(let id):
            DetachedPane(model: model, paneID: id)
        case .tab, .group:
            if let tab = scope.tab(in: model) {
                VStack(spacing: 0) {
                    if scope.isGroup, horizontal, let group = scope.group(in: model) {
                        TabStrip(model: model, group: group)
                    }
                    TabDetailView(model: model, tab: tab)
                        .id(tab.id)
                }
            } else if case .group(let gid) = scope.subject, let group = model.groups.first(where: { $0.id == gid }) {
                EmptyGroupView(model: model, group: group)
            } else {
                Color.clear
            }
        }
    }

    private var title: some View {
        let sub = scope.subtitle(in: model)
        return HStack(spacing: 6) {
            Text(scope.title(in: model))
                .fontWeight(.semibold)
            if !sub.isEmpty {
                Text(sub).foregroundStyle(.secondary)
            }
        }
        .font(.system(size: 13))
        .lineLimit(1)
        .truncationMode(.middle)
        .padding(.horizontal, 80) // clear of the window's buttons
    }
}

/// A pane alone in its window: as in its tab, filling the window, its
/// corners rounding with the window's.
private struct DetachedPane: View {
    let model: AppModel
    let paneID: String
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        GeometryReader { geo in
            let area = CGRect(origin: .zero, size: geo.size)
            let shape = PaneLook.shape(at: area, in: area, sidebarHidden: true)
            PaneView(model: model, paneID: paneID, isFocused: true)
                .clipShape(shape)
                .overlay {
                    if PaneLook.spacious {
                        shape.strokeBorder(PaneLook.outlineColor(dark: colorScheme == .dark), lineWidth: PaneLook.outlineWidth)
                            .allowsHitTesting(false)
                    }
                }
        }
        .padding(EdgeInsets(top: 0, leading: PaneLook.inset, bottom: PaneLook.inset, trailing: PaneLook.inset))
        .background(PaneLook.spacious ? Color.clear : Color(nsColor: .separatorColor))
    }
}

/// A Group's window with Vertical Tabs: the Group's name, and its tabs.
/// Selecting one shows it in this window, not the main one.
private struct DetachedGroupSidebar: View {
    let model: AppModel
    let scope: WindowScope
    let group: WorkspaceGroup
    @State private var hovered: String?
    @Environment(\.colorScheme) private var colorScheme

    private var compact: Bool { SidebarLayout.current == .compact }

    var body: some View {
        let selected = scope.tab(in: model)?.id
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                GroupRow(model: model, group: group, collapsed: false, chevron: false, icon: AppConfig.shared.groupIcons)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 6)
                    .contextMenu { GroupWindowMenuItems(model: model, group: group) }
                ForEach(model.tabs(in: group)) { tab in
                    TabRow(model: model, tab: tab, hovered: hovered == tab.id)
                        .padding(.horizontal, 14)
                        .padding(.leading, compact ? 12 : 14)
                        .frame(height: compact ? 30 : 36)
                        .background { pill(selected: tab.id == selected, hovered: hovered == tab.id) }
                        .contentShape(Rectangle())
                        .onTapGesture { select(tab) }
                        .onHover { inside in hovered = inside ? tab.id : (hovered == tab.id ? nil : hovered) }
                        .contextMenu { TabMenuItems(model: model, tab: tab) }
                }
                Button { Task { await model.createTab(in: group.id) } } label: {
                    Label("New Tab", systemImage: "plus")
                        .font(.system(size: compact ? 13 : 13.5))
                        .foregroundStyle(.secondary)
                        .padding(.leading, compact ? 26 : 28)
                        .frame(maxWidth: .infinity, minHeight: compact ? 28 : 32, alignment: .leading)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .disabled(!group.canAddTabs)
                .tooltip(Keys.hint("New Tab", "new_tab"))
            }
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
        }
        .environment(\.sidebarCompact, compact)
    }

    private func select(_ tab: WorkspaceTab) {
        scope.selectedTabID = tab.id
        // Its focused pane has the keyboard, as selecting a tab gives it.
        model.focus(tab.focusedPaneID)
    }

    @ViewBuilder
    private func pill(selected: Bool, hovered: Bool) -> some View {
        let dark = colorScheme == .dark
        if selected {
            RoundedRectangle(cornerRadius: 10)
                .fill(dark ? Color.white.opacity(0.16) : Color.white.opacity(0.92))
                .shadow(color: .black.opacity(dark ? 0.3 : 0.08), radius: 2, y: 1)
                .padding(.vertical, compact ? 1 : 2)
        } else if hovered {
            RoundedRectangle(cornerRadius: 10)
                .fill(dark ? Color.white.opacity(0.06) : Color.white.opacity(0.4))
                .padding(.vertical, compact ? 1 : 2)
        }
    }
}

/// A Group's window's right-click menu on its name.
private struct GroupWindowMenuItems: View {
    let model: AppModel
    let group: WorkspaceGroup

    var body: some View {
        Button("New Tab") { Task { await model.createTab(in: group.id) } }
            .disabled(!group.canAddTabs)
        if !group.isRemote {
            Button("Configure Group…") { TabSheets.shared.configuring = group }
        }
    }
}

// MARK: - Menu items

/// "Open in New Window", for the menus of a pane, a tab or a Group.
struct OpenInNewWindowButton: View {
    let model: AppModel
    let subject: WindowSubject

    var body: some View {
        Button("Open in New Window") { DetachedWindows.shared.open(subject, model: model) }
    }
}

// MARK: - Presence

/// The panes on screen, in the main window and windows of their own, told
/// to the daemon: an agent you can see finishing is one you've seen.
@MainActor
enum WorkspacePresence {
    static func report(_ model: AppModel) {
        var visible = model.selectedTab.map { AppModel.shownPaneIDs(of: $0) } ?? []
        for id in DetachedWindows.shared.shownPaneIDs(in: model) where !visible.contains(id) { visible.append(id) }
        model.reportPresence(visible: visible, foreground: NSApp.isActive)
    }
}
