import ObjectiveC
import HammockKit
import SwiftUI

/// Which Groups are collapsed in the sidebar (kept in defaults, to survive
/// launches), and collapsing or expanding them from the Group's menu and the
/// palette, not only by clicking its header. The sidebar keeps its own copy
/// as view state, so toggling animates; `set` tells it.
@MainActor
enum GroupCollapse {
    static let changed = Notification.Name("HammockGroupCollapse")
    private static let key = "collapsedGroups"

    static var collapsed: Set<String> {
        get { Set(UserDefaults.standard.string(forKey: key)?.split(separator: ",").map(String.init) ?? []) }
        set { UserDefaults.standard.set(newValue.sorted().joined(separator: ","), forKey: key) }
    }

    static func isCollapsed(_ id: String) -> Bool { collapsed.contains(id) }

    static func set(_ ids: [String], collapsed on: Bool) {
        var c = collapsed
        if on { c.formUnion(ids) } else { c.subtract(ids) }
        guard c != collapsed else { return }
        collapsed = c
        NotificationCenter.default.post(name: changed, object: nil)
    }

    /// Whether Groups collapse in this layout (with the tabs along the top, they don't).
    static var applies: Bool { SidebarLayout.current != .horizontal }

    /// The Groups to collapse for Collapse Others: every other one with tabs.
    static func others(_ model: AppModel, than id: String) -> [String] {
        model.groups.filter { $0.id != id && !$0.tabIds.isEmpty && !isCollapsed($0.id) }.map(\.id)
    }
}

/// Collapse or Expand a Group, and Collapse Others: its right-click menu's.
struct GroupCollapseButtons: View {
    let model: AppModel
    let group: WorkspaceGroup

    var body: some View {
        if GroupCollapse.applies, !group.tabIds.isEmpty {
            if GroupCollapse.isCollapsed(group.id) {
                Button("Expand") { GroupCollapse.set([group.id], collapsed: false) }
            } else {
                Button("Collapse") { GroupCollapse.set([group.id], collapsed: true) }
            }
            Button("Collapse Others") { GroupCollapse.set(GroupCollapse.others(model, than: group.id), collapsed: true) }
                .disabled(GroupCollapse.others(model, than: group.id).isEmpty)
            Divider()
        }
    }
}

struct SidebarView: View {
    @Bindable var model: AppModel
    /// Collapsed Groups. Plain view state, so toggling animates (an
    /// @AppStorage change doesn't), mirrored to defaults to survive launches.
    @State private var collapsed = GroupCollapse.collapsed
    // Dragging a Group by its header (List rows don't take SwiftUI drags):
    // the headers' places in the window, the Group being dragged, where the
    // pointer is, and the Group order while it moves through the list.
    @State private var headerFrames: [String: CGRect] = [:]
    @State private var listFrame: CGRect = .zero
    @State private var draggingGroup: String?
    @State private var dragPoint: CGPoint?
    @State private var previewOrder: [String]?
    // Dragging a tab the same way: the tabs' places, the tab being dragged,
    // and every Group's tab order while it moves (kept after the drop until
    // the daemon's order matches, so it doesn't flick back).
    @State private var tabFrames: [String: CGRect] = [:]
    @State private var draggingTab: String?
    @State private var tabPreview: [String: [String]]?
    @State private var hoveredGroup: String?
    @State private var hoverClear: DispatchWorkItem?
    @State private var hoveredRows = Set<String>()
    @State private var hoveredTab: String?
    /// Where folders dragged from the Finder would land: on a Group (its
    /// rows light up) or between Groups (a line there).
    @State private var folderDrop: FolderDrop.Target?
    @State private var tracker = SidebarDragTracker()
    @State private var scroller = SidebarScroller()
    /// The bottom bar's height: the List scrolls under it.
    @State private var barHeight: CGFloat = 0
    private var layout: SidebarLayout { .current }
    private var showFinished: Bool { Prefs.shared.bool(FinishedPrefs.pref) }
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    private var showAgentsCollapsed: Bool { Prefs.shared.bool(CollapsedAgentsPrefs.pref) }
    @Environment(\.colorScheme) private var colorScheme

    private var horizontal: Bool { layout == .horizontal }

    /// Whether a collapsed Group shows this tab: the selected tab (as in
    /// Dia), one being dragged into it, and, if Settings asks, one whose
    /// agent is active (working, waiting, failed, or finished and not yet
    /// seen: `agentState(of:)` is nil once it's at rest and seen).
    private func showsCollapsed(_ tab: WorkspaceTab) -> Bool {
        tab.id == model.selectedTabID || tab.id == draggingTab || showAgentsCollapsed && model.agentState(of: tab) != nil
    }

    /// The tabs each collapsed Group is showing.
    private var collapsedShown: [[String]] {
        model.groups.filter { collapsed.contains($0.id) }.map { $0.tabIds.filter { model.tabs[$0].map(showsCollapsed) ?? false } }
    }

    /// The Groups as shown: in the order a drag is trying out, else the model's.
    private var shownGroups: [WorkspaceGroup] {
        // All your computers' Groups, or the one chosen in the footer.
        let shown = model.shownGroups
        guard let order = previewOrder else { return shown }
        let byID = Dictionary(uniqueKeysWithValues: shown.map { ($0.id, $0) })
        return order.compactMap { byID[$0] }
    }

    /// One row of the sidebar: a Group's header or one of its tabs. One flat
    /// list (not a ForEach per Group), so the List sees rows move, and
    /// animates them.
    private enum Row: Identifiable {
        case header(WorkspaceGroup, collapsed: Bool, tabsShown: Bool)
        case tab(WorkspaceTab, group: WorkspaceGroup, last: Bool, collapsed: Bool)
        var id: String {
            switch self {
            case .header(let g, _, _): "group:" + g.id
            case .tab(let t, _, _, _): t.id
            }
        }
    }

    private var rows: [Row] {
        var out: [Row] = []
        for group in shownGroups {
            // Horizontal Tabs: the Group's tabs are in the tab bar, not here.
            let isCollapsed = horizontal || collapsed.contains(group.id)
            // Collapsed, a Group still shows some of its tabs (showsCollapsed).
            let all = (tabPreview?[group.id] ?? group.tabIds).compactMap { model.tabs[$0] }
            let tabs = horizontal ? [] : isCollapsed ? all.filter(showsCollapsed) : all
            out.append(.header(group, collapsed: isCollapsed, tabsShown: !tabs.isEmpty))
            for t in tabs { out.append(.tab(t, group: group, last: t.id == tabs.last?.id, collapsed: isCollapsed)) }
        }
        return out
    }

    /// Moves a dragged Group to where the pointer is among the others:
    /// before the first whose header's middle is below it, else last. (The
    /// others' headers are where the order being tried puts them, so a Group
    /// doesn't flip back and forth over one.)
    private func dragGroup(_ id: String, to p: CGPoint) {
        if draggingGroup == nil {
            hoveredGroup = nil
            tracker.begin(at: p, onMove: { dragGroup(id, to: $0) }, onEnd: { dropGroup() })
        }
        draggingGroup = id
        dragPoint = p
        let current = previewOrder ?? model.shownGroups.map(\.id)
        var order = current.filter { $0 != id }
        let i = order.firstIndex { headerFrames[$0].map { p.y < $0.midY } ?? false } ?? order.count
        order.insert(id, at: i)
        if order != current {
            bump()
            withAnimation(.snappy(duration: 0.22)) { previewOrder = order }
        }
    }

    /// A small trackpad bump as a dragged Group or tab passes another.
    private func bump() {
        NSHapticFeedbackManager.defaultPerformer.perform(.alignment, performanceTime: .now)
    }

    /// Moves a dragged tab to where the pointer is: before the first row
    /// (other than its own) whose middle is below it. Above a header, that's
    /// the end of the Group before. Your tabs move between your Groups; a
    /// shared Group's tabs stay in it.
    private func dragTab(_ id: String, to p: CGPoint) {
        if draggingTab == nil {
            hoveredGroup = nil
            tracker.begin(at: p, onMove: { dragTab(id, to: $0) }, onEnd: { dropTab() })
            tabPreview = Dictionary(uniqueKeysWithValues: model.groups.map { ($0.id, $0.tabIds) })
        }
        draggingTab = id
        dragPoint = p
        guard var preview = tabPreview, let src = model.groups.first(where: { $0.tabIds.contains(id) }) else { return }
        for k in preview.keys { preview[k]?.removeAll { $0 == id } }
        let groups = shownGroups
        var target: (group: WorkspaceGroup, index: Int)?
        for row in rows where row.id != id {
            switch row {
            case .header(let g, _, _):
                guard let f = headerFrames[g.id], p.y < f.midY else { continue }
                if let gi = groups.firstIndex(where: { $0.id == g.id }), gi > 0 {
                    let prev = groups[gi - 1]
                    target = (prev, preview[prev.id]?.count ?? 0)
                } else {
                    target = (g, 0)
                }
            case .tab(let t, let g, _, _):
                guard let f = tabFrames[t.id], p.y < f.midY else { continue }
                target = (g, preview[g.id]?.firstIndex(of: t.id) ?? 0)
            }
            break
        }
        if target == nil, let last = groups.last { target = (last, preview[last.id]?.count ?? 0) }
        guard let (g, i) = target, g.id == src.id || (!g.isRemote && !src.isRemote) else { return }
        preview[g.id, default: []].insert(id, at: min(i, preview[g.id]?.count ?? 0))
        if preview != tabPreview {
            bump()
            withAnimation(.snappy(duration: 0.22)) { tabPreview = preview }
        }
    }

    private func dropTab() {
        tracker.end()
        guard draggingTab != nil else { return } // dropped already (the tracker or the gesture)
        defer { draggingTab = nil; dragPoint = nil }
        guard let id = draggingTab, let preview = tabPreview,
              let (g, i) = preview.lazy.compactMap({ k, v in v.firstIndex(of: id).map { (k, $0) } }).first,
              let group = model.groups.first(where: { $0.id == g }), group.tabIds.firstIndex(of: id) != i || !group.tabIds.contains(id)
        else { tabPreview = nil; return }
        model.moveTab(id, to: g, index: i)
        // In case the daemon's order never matches (it refused the move).
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { if draggingTab == nil { tabPreview = nil } }
    }

    private func dropGroup() {
        tracker.end()
        guard draggingGroup != nil else { return }
        defer { draggingGroup = nil; dragPoint = nil; previewOrder = nil }
        guard let id = draggingGroup, let preview = previewOrder else { return }
        // With one computer's Groups shown, the others keep their places:
        // the shown ones take the shown slots in the new order.
        var slots = preview.makeIterator()
        let shown = Set(preview)
        let full = model.groups.map { shown.contains($0.id) ? (slots.next() ?? $0.id) : $0.id }
        guard let to = full.firstIndex(of: id), to != model.groups.firstIndex(where: { $0.id == id }) else { return }
        model.moveGroup(id, to: to) // reorders here at once, as previewed
    }

    /// The shown Groups' rows, top to bottom in the window, for placing a
    /// folder dragged over the sidebar.
    private var dropBlocks: [FolderDrop.Block] {
        var out: [FolderDrop.Block] = []
        for row in rows {
            switch row {
            case .header(let g, _, _):
                guard let f = headerFrames[g.id] else { continue }
                out.append(.init(id: g.id, top: f.minY, bottom: f.maxY, takesTabs: FolderDrop.takesTabs(g)))
            case .tab(let t, let g, _, _):
                guard let f = tabFrames[t.id], let i = out.indices.last, out[i].id == g.id else { continue }
                out[i].bottom = max(out[i].bottom, f.maxY)
            }
        }
        return out
    }

    /// The folder drag shown: the one under way, else HMK_DEBUG_FOLDER_DROP's
    /// (`group:<n>`, the nth shown Group lit; `line:<n>`, the line before it).
    private var shownFolderDrop: FolderDrop.Target? {
        if let folderDrop { return folderDrop }
        guard let v = ProcessInfo.processInfo.environment["HMK_DEBUG_FOLDER_DROP"] else { return nil }
        let parts = v.split(separator: ":")
        guard parts.count == 2, let n = Int(parts[1]), n >= 1 else { return nil }
        if parts[0] == "group" { return shownGroups.indices.contains(n - 1) ? .group(shownGroups[n - 1].id) : nil }
        return .insert(n - 1)
    }

    /// Group headings drawn subtly (group-headings = subtle): only over
    /// tabs, so not with Horizontal Tabs, where the Groups are the rows.
    private var subtleGroups: Bool { AppConfig.shared.groupHeadings == .subtle && !horizontal }

    /// A Group's header. The whole row toggles the Group (no selection of
    /// its own); its chevron sits after the name, as in Dia. With Horizontal
    /// Tabs it selects the Group instead, and so does a Group without tabs
    /// (its zero state), which has no chevron.
    private func headerRow(_ group: WorkspaceGroup, collapsed isCollapsed: Bool, tabsShown: Bool) -> some View {
        let empty = group.tabIds.isEmpty && !tabsShown
        // With active agents shown, the header's status covers only the tabs
        // it hides (as the TUI's does).
        let shown = showAgentsCollapsed && !horizontal ? Set(model.tabs(in: group).filter(showsCollapsed).map(\.id)) : []
        return GroupRow(model: model, group: group, collapsed: isCollapsed, chevron: !horizontal && !empty, shown: shown, subtle: subtleGroups, icon: AppConfig.shared.groupIcons)
            .modifier(FullRowHover())
            .background {
                if horizontal || empty {
                    let all = model.tabs(in: group)
                    tabPill(all.contains { $0.id == model.selectedTabID } ? model.selectedTabID ?? "" : "group:" + group.id)
                }
            }
            .onTapGesture {
                if empty {
                    model.selectGroup(group.id)
                    return
                }
                if horizontal {
                    if let t = currentTab(of: group, in: model) { model.selectedTabID = t.id } else { model.selectGroup(group.id) }
                    return
                }
                withAnimation(.snappy(duration: 0.2)) {
                    if isCollapsed { collapsed.remove(group.id) } else { collapsed.insert(group.id) }
                }
            }
            .onHover { inside in
                hover(group.id, row: group.id, inside)
                if horizontal || empty { if inside, !PaletteState.shared.isOpen { hoveredTab = "group:" + group.id } else if hoveredTab == "group:" + group.id { hoveredTab = nil } }
            }
            // Drag a Group by its header: it moves through the list as you
            // go (its place dimmed), its header following the pointer.
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { headerFrames[group.id] = $0 }
            .opacity(draggingGroup == group.id ? 0.3 : 1)
            .gesture(
                DragGesture(minimumDistance: 6, coordinateSpace: .global)
                    .onChanged { g in dragGroup(group.id, to: g.location) }
                    .onEnded { _ in dropGroup() }
            )
            // Outermost, so the drag's wrappers don't lose them.
            .listRowBackground(groupBackground(group.id, tabsShown ? .top : .single))
            .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
            .contextMenu {
                if group.isOnLinkedComputer {
                    // On another of your computers: yours to change, from here.
                    Button("Configure Group…") { TabSheets.shared.configuring = group }
                        .disabled(group.offline)
                    OpenInNewWindowButton(model: model, subject: .group(group.id))
                    Divider()
                    Button("New Tab") { Task { await model.createTab(in: group.id) } }
                        .disabled(group.offline)
                    NewAgentTabItems(model: model, groupID: group.id, shortcuts: false)
                        .disabled(!group.canAddTabs)
                    Button("Resume a Session…") { PaletteState.shared.open(.sessions, group: group.id, inGroup: true) }
                        .disabled(group.offline)
                    moveButtons(group)
                    if let c = model.computer(of: group) {
                        Button("Show Only \(c.name)") { withAnimation(.snappy(duration: 0.25)) { model.computerFilter = c.userID } }
                    }
                    Button("Delete Group", role: .destructive) { CloseGuard.confirm(model, group: group) { Task { await model.deleteGroup(group.id) } } }
                        .disabled(group.offline)
                } else if group.isRemote {
                    OpenInNewWindowButton(model: model, subject: .group(group.id))
                    Divider()
                    Button("New Tab") { Task { await model.createTab(in: group.id) } }
                        .disabled(!group.canDrive || group.offline)
                    NewAgentTabItems(model: model, groupID: group.id, shortcuts: false)
                        .disabled(!group.canAddTabs)
                    moveButtons(group)
                    Button("Leave Group", role: .destructive) { Task { await model.leave(group.id) } }
                } else {
                    Button("Configure Group…") { TabSheets.shared.configuring = group }
                    if SharingUI.canShare(group) {
                        Button("Share…") { SharingUI.shared.shareGroup = group.id }
                    }
                    Button("Change Folder…") { GroupFolder.choose(model, group) }
                    Button("Reveal in Finder") { GroupFolder.reveal(model, group) }
                        .keyboardShortcut(Keys.revealGroupInFinder)
                    OpenInNewWindowButton(model: model, subject: .group(group.id))
                    Divider()
                    Button("New Tab") { Task { await model.createTab(in: group.id) } }
                    NewAgentTabItems(model: model, groupID: group.id, shortcuts: false)
                    if HammockTool.log.on {
                        Button("New Log Tab…") { LogUI.shared.request = LogRequest(groupID: group.id) }
                    }
                    if HammockTool.schedules.on {
                        Button("New Scheduled Task…") { ScheduleUI.shared.new(model, groupID: group.id) }
                    }
                    Button("Resume a Session…") { PaletteState.shared.open(.sessions, group: group.id, inGroup: true) }
                    moveButtons(group)
                    Button("Delete Group", role: .destructive) { CloseGuard.confirm(model, group: group) { Task { await model.deleteGroup(group.id) } } }
                }
            }
    }

    private func tabRow(_ tab: WorkspaceTab, group: WorkspaceGroup, last: Bool) -> some View {
        TabRow(model: model, tab: tab, hovered: hoveredTab == tab.id || TabPreviewHold.shared.tabID == tab.id)
            .padding(.leading, layout == .compact ? 12 : 14)
            .opacity(draggingGroup == group.id || draggingTab == tab.id ? 0.3 : 1)
            .modifier(FullRowHover())
            .background { tabPill(tab.id) }
            .tabPreview(model: model, tab: tab, edge: .trailing)
            .onHover { inside in
                hover(group.id, row: tab.id, inside)
                if inside, !PaletteState.shared.isOpen, draggingTab == nil { hoveredTab = tab.id } else if hoveredTab == tab.id { hoveredTab = nil }
            }
            // Drag a tab, as a Group: it moves through the list (between
            // your Groups too) as you go, a copy following the pointer.
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { tabFrames[tab.id] = $0 }
            // A click selects the tab: the drag handling below kept the
            // List's own click from doing it.
            .onTapGesture { model.selectedTabID = tab.id }
            .gesture(
                DragGesture(minimumDistance: 6, coordinateSpace: .global)
                    .onChanged { g in dragTab(tab.id, to: g.location) }
                    .onEnded { _ in dropTab() }
            )
            // Outermost, so the drag's wrappers don't lose them.
            .listRowBackground(groupBackground(group.id, last ? .bottom : .middle))
            .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
            .contextMenu {
                TabMenuItems(model: model, tab: tab)
            }
    }

    @ViewBuilder
    private func moveButtons(_ group: WorkspaceGroup) -> some View {
        let i = model.groups.firstIndex(where: { $0.id == group.id }) ?? 0
        Divider()
        GroupCollapseButtons(model: model, group: group)
        Button("Move Up") { withAnimation(.snappy(duration: 0.25)) { model.moveGroup(group.id, to: i - 1) } }
            .disabled(i == 0)
        Button("Move Down") { withAnimation(.snappy(duration: 0.25)) { model.moveGroup(group.id, to: i + 1) } }
            .disabled(i >= model.groups.count - 1)
        Divider()
    }

    var body: some View {
        // No selection of the list's own: a tab's tap selects it and its pill
        // shows it (the list never has the keyboard). On macOS 26 the list
        // moved its selection when collapsing a Group took rows away, and
        // selectable rows wouldn't drag.
        ScrollViewReader { proxy in
        List {
            Group {
                ForEach(rows) { row in
                    switch row {
                    case .header(let group, let isCollapsed, let tabsShown):
                        headerRow(group, collapsed: isCollapsed, tabsShown: tabsShown)
                    case .tab(let tab, let group, let last, _):
                        tabRow(tab, group: group, last: last)
                    }
                }
            }
        }
        .listStyle(.sidebar)
        .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { listFrame = $0 }
        .overlay(alignment: .top) {
            if let id = draggingGroup, let p = dragPoint, let group = model.groups.first(where: { $0.id == id }) {
                GroupRow(model: model, group: group, collapsed: true, chevron: false, subtle: subtleGroups, icon: AppConfig.shared.groupIcons)
                    .padding(.horizontal, 10).padding(.vertical, 6)
                    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
                    .shadow(color: .black.opacity(0.18), radius: 8, y: 3)
                    .padding(.horizontal, 10)
                    .offset(y: p.y - listFrame.minY - 18)
                    .allowsHitTesting(false)
            } else if let id = draggingTab, let p = dragPoint, let tab = model.tabs[id] {
                TabRow(model: model, tab: tab)
                    .padding(.horizontal, 10).padding(.vertical, 6)
                    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
                    .shadow(color: .black.opacity(0.18), radius: 8, y: 3)
                    .padding(.horizontal, 10)
                    .offset(y: p.y - listFrame.minY - 16)
                    .allowsHitTesting(false)
            }
        }
        // Folders dragged from the Finder: a new Group where the line is,
        // or new tabs in the Group they're over (FolderDrop).
        .overlay(alignment: .top) {
            if case .insert(let i)? = shownFolderDrop, let y = FolderDrop.lineY(i, blocks: dropBlocks) {
                FolderDropLine()
                    .offset(y: y - listFrame.minY - 4)
            }
        }
        .onDrop(of: [.fileURL], delegate: SidebarFolderDrop(
            locate: { p in FolderDrop.target(y: p.y + listFrame.minY, blocks: dropBlocks) },
            show: { t in if t != folderDrop { withAnimation(.snappy(duration: 0.15)) { folderDrop = t } } },
            perform: { folders, target in FolderDropUI.shared.drop(folders, on: target, shown: shownGroups, model: model) }
        ))
        .accessibilityDropPoint(.bottom, description: "New Group for the folder", isEnabled: true)
        .onChange(of: collapsed) { _, c in GroupCollapse.collapsed = c }
        // The Group menus' and palette's Collapse, Expand and Collapse Others.
        .onReceive(NotificationCenter.default.publisher(for: GroupCollapse.changed)) { _ in
            withAnimation(.snappy(duration: 0.2)) { collapsed = GroupCollapse.collapsed }
        }
        .onChange(of: PaletteState.shared.isOpen) { _, open in
            if open {
                hoveredGroup = nil
                hoveredTab = nil
                hoveredRows = []
            }
        }
        // A dragged Group's neighbours slide out of its way.
        .animation(.snappy(duration: 0.22), value: previewOrder)
        .onChange(of: model.groups.map(\.tabIds)) {
            guard draggingTab == nil, let preview = tabPreview else { return }
            if model.groups.allSatisfy({ preview[$0.id] == $0.tabIds }) { tabPreview = nil }
        }
        // Groups and tabs slide in and out as they're created and closed:
        // AppModel applies those changes in an animation.
        // A collapsed Group's selected tab slides away when you select a tab
        // elsewhere (and in when one of its tabs is selected), as do its
        // agents' tabs as they start up and come to rest.
        .animation(.snappy(duration: 0.25), value: collapsedShown)
        // Finder-sized rows (larger text and icons than the default);
        // Compact Vertical Tabs: rows sized by their content too, with less
        // room around nearly full-size text (medium rows have a minimum
        // height that undid the tighter padding).
        .environment(\.sidebarRowSize, .small) // SidebarMetrics: the large ones grew in macOS 26
        .environment(\.sidebarCompact, layout == .compact)
        // Clicking a tab focuses its pane; if the list took focus first, the
        // selection would flash blue (focused) then grey. So the list never
        // takes keyboard focus; clicks still select.
        .background(SidebarRefusesFocus(scroller: scroller))
        .scrollEdgeEffectStyle(.hard, for: .bottom)
        // A bar (macOS 26): the list scrolling beneath it fades under, not shows through.
        .safeAreaBar(edge: .bottom, spacing: 0) {
            VStack(spacing: 0) {
                NeedsYouView(model: model)
                ScheduleFooter(model: model)
                UsageFooter(model: model)
                HostFooter(model: model)
            }
            .modifier(SlidingBarHeight())
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { barHeight = $0 }
            // Needs You's cards come and go over the same curve as the bar's
            // height. Outside SlidingBarHeight, so the whole update moves
            // together: where the content sits in the sliding frame as well
            // as the cards within it (animating only the cards moved them
            // twice, the content having jumped to its new place). Under
            // Reduce Motion, at once, as before.
            .animation(reduceMotion ? nil : .snappy(duration: 0.25), value: NeedsYouView.animationKey(model, finished: showFinished))
        }
        // A new Group (⌘N), or a tab picked from the keyboard or a
        // notification, may be scrolled out of sight: bring it into view.
        .onChange(of: model.selectedTabID ?? model.selectedGroupID.map { "group:" + $0 }) { _, id in
            if let id { reveal(id, proxy) }
        }
        }
    }

    /// Scrolls the list just far enough to show the row `id` (a tab, or
    /// "group:" and a Group's id), or its Group's header when its tabs
    /// aren't listed (Horizontal Tabs), clear of the bottom bar, which
    /// SwiftUI's scrollTo puts it under. After this update, once the List
    /// has the row (a new tab is selected as it's inserted), and again once
    /// the insertion has settled.
    private func reveal(_ id: String, _ proxy: ScrollViewProxy) {
        func go(_ animated: Bool) {
            let shown = rows.map(\.id)
            let target = shown.contains(id) ? id : model.groups.first(where: { $0.tabIds.contains(id) }).map { "group:" + $0.id }
            guard let target, let i = shown.firstIndex(of: target) else { return }
            if !scroller.reveal(row: i, of: shown.count, bottomInset: barHeight, animated: animated && !reduceMotion) {
                proxy.scrollTo(target)
            }
        }
        DispatchQueue.main.async { go(true) }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.35) { go(false) }
    }

    // MARK: Group hover, as in Dia: hovering a Group or any of its tabs puts the
    // whole group on one rounded background. A List draws each row's
    // background separately, so each row gets its slice of the shape.

    enum GroupPosition { case single, top, middle, bottom }

    /// Dia-style pills: a bright one (with a soft shadow) for the selected
    /// tab, which replaces the system highlight, and a dim one on hover.
    @ViewBuilder
    private func tabPill(_ id: String) -> some View {
        let selected = model.selectedTabID == id || (id.hasPrefix("group:") && model.selectedEmptyGroup.map { "group:" + $0.id } == id)
        SidebarPill(selected: selected, hovered: hoveredTab == id || TabPreviewHold.shared.tabID == id)
            .padding(.leading, -8)
            .padding(.trailing, -8)
            .padding(.vertical, pillInset)
    }

    /// How far a pill sits inside its row. Compact rows are tight around
    /// their text, so their pills fill more of the row (taller pills, 2pt
    /// between them rather than 4); the rows themselves keep their height.
    private var pillInset: CGFloat { layout == .compact ? 1 : 2 }

    /// Tracks hovered rows (not just the Group): events between rows can
    /// arrive exit-then-enter or enter-then-exit, with a gap in between.
    private func hover(_ groupID: String, row: String, _ inside: Bool) {
        hoverClear?.cancel()
        // Nothing under the command palette reacts to the pointer.
        if inside, PaletteState.shared.isOpen { return }
        // Nor while something's dragged: the pointer crosses rows as it goes.
        if draggingTab != nil || draggingGroup != nil { return }
        if inside {
            hoveredRows.insert(row)
            hoveredGroup = groupID
            return
        }
        hoveredRows.remove(row)
        guard hoveredRows.isEmpty else { return }
        let clear = DispatchWorkItem { if hoveredRows.isEmpty { hoveredGroup = nil } }
        hoverClear = clear
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.12, execute: clear)
    }

    @ViewBuilder
    private func groupBackground(_ groupID: String, _ position: GroupPosition) -> some View {
        // In the theme's colour: folders dragged over it would open tabs
        // here, or a folder dropped just now is this Group's.
        let lit = shownFolderDrop == .group(groupID) || FolderDropUI.shared.flashing == groupID
        if lit || draggingGroup == nil && draggingTab == nil && (hoveredGroup == groupID || ProcessInfo.processInfo.environment["HMK_DEBUG_HOVER"] != nil) {
            let r: CGFloat = 10
            let top = position == .single || position == .top ? r : 0
            let bottom = position == .single || position == .bottom ? r : 0
            UnevenRoundedRectangle(topLeadingRadius: top, bottomLeadingRadius: bottom, bottomTrailingRadius: bottom, topTrailingRadius: top)
                .fill(lit ? Color.themeAccent.opacity(0.16) : Color.primary.opacity(0.035))
                // A hairline round the whole group, as in Dia: each slice
                // strokes only its outer edges, so they join into one.
                .overlay(GroupSliceOutline(top: top, bottom: bottom).stroke(lit ? Color.themeAccent.opacity(0.6) : Color.primary.opacity(0.08), lineWidth: 1))
                .padding(.horizontal, 4)
                .padding(.top, top > 0 ? 2 : 0)
                // The last tab's slice ends at its row's edge: the List clips
                // row backgrounds to the row, so drawn past it the rounded
                // corners and the outline's bottom were cut off.
                .padding(.bottom, position == .single ? 2 : 0)
        }
    }
}

/// One row's slice of a hovered Group's outline: its sides, plus its top or
/// bottom (rounded) when it's the Group's first or last row. Left open where
/// it meets the next slice, so the slices draw one border. Drawn half a
/// point in, so the 1pt stroke stays inside the fill.
private struct GroupSliceOutline: Shape {
    var top: CGFloat
    var bottom: CGFloat

    func path(in rect: CGRect) -> Path {
        let r = rect.insetBy(dx: 0.5, dy: 0)
        let minY = top > 0 ? r.minY + 0.5 : r.minY
        let maxY = bottom > 0 ? r.maxY - 0.5 : r.maxY
        let t = max(0, top - 0.5), b = max(0, bottom - 0.5)
        var p = Path()
        // Down the left, along the bottom (if any), up the right, along the top (if any).
        if top > 0 {
            p.move(to: CGPoint(x: r.maxX - t, y: minY))
            p.addLine(to: CGPoint(x: r.minX + t, y: minY))
            p.addArc(center: CGPoint(x: r.minX + t, y: minY + t), radius: t, startAngle: .degrees(270), endAngle: .degrees(180), clockwise: true)
        } else {
            p.move(to: CGPoint(x: r.minX, y: minY))
        }
        p.addLine(to: CGPoint(x: r.minX, y: maxY - b))
        if bottom > 0 {
            p.addArc(center: CGPoint(x: r.minX + b, y: maxY - b), radius: b, startAngle: .degrees(180), endAngle: .degrees(90), clockwise: true)
            p.addLine(to: CGPoint(x: r.maxX - b, y: maxY))
            p.addArc(center: CGPoint(x: r.maxX - b, y: maxY - b), radius: b, startAngle: .degrees(90), endAngle: .degrees(0), clockwise: true)
        } else {
            p.move(to: CGPoint(x: r.maxX, y: maxY))
        }
        p.addLine(to: CGPoint(x: r.maxX, y: minY + t))
        if top > 0 {
            p.addArc(center: CGPoint(x: r.maxX - t, y: minY + t), radius: t, startAngle: .degrees(0), endAngle: .degrees(270), clockwise: true)
        }
        return p
    }
}

enum RenameTarget: Identifiable {
    case group(String, String), tab(String, String)
    var id: String {
        switch self {
        case .group(let id, _), .tab(let id, _): return id
        }
    }
    var current: String {
        switch self {
        case .group(_, let n), .tab(_, let n): return n
        }
    }
}

/// Renaming a tab and configuring a Group, asked for from anywhere (the
/// sidebar, the tab bar, the menu bar, the command palette): the window
/// shows the sheet (TabSheets.Host), so it works with the sidebar hidden too.
@MainActor @Observable
final class TabSheets {
    static let shared = TabSheets()
    var renaming: RenameTarget?
    var configuring: WorkspaceGroup?

    func rename(_ tab: WorkspaceTab) { renaming = .tab(tab.id, tab.name) }

    /// Your own Groups, and those on your other computers while they're
    /// online; not a Group someone shared with you.
    static func canConfigure(_ g: WorkspaceGroup) -> Bool { g.isOnLinkedComputer ? !g.offline : !g.isRemote }

    struct Host: ViewModifier {
        let model: AppModel
        @Bindable private var sheets = TabSheets.shared

        func body(content: Content) -> some View {
            content
                .sheet(item: $sheets.configuring) { group in
                    GroupSheet(model: model, group: group, automaticName: model.title(of: WorkspaceGroup.with { $0.id = group.id; $0.lastFocusedPaneID = group.lastFocusedPaneID; $0.folder = group.folder })) { name, icon, picture, folder in
                        Task {
                            if name != group.name { await model.renameGroup(group.id, name) }
                            if icon != group.icon { await model.setGroupIcon(group.id, icon) }
                            if let picture { await model.setGroupPicture(group.id, picture) }
                            if let folder, folder != group.folder { await model.setGroupFolder(group.id, folder) }
                        }
                    }
                }
                .sheet(item: $sheets.renaming) { target in
                    RenameSheet(target: target) { name in
                        Task {
                            switch target {
                            case .group(let id, _): await model.renameGroup(id, name)
                            case .tab(let id, _): await model.renameTab(id, name)
                            }
                        }
                    }
                }
        }
    }
}

struct RenameSheet: View {
    let target: RenameTarget
    let onSave: (String) -> Void
    @State private var name = ""
    @FocusState private var focused: Bool
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        Form {
            // Empty restores the automatic name ("Tab 2", or the directory).
            // No label: a grouped form's labelled field right-aligns its text.
            TextField("Name", text: $name, prompt: Text("Automatic"))
                .labelsHidden()
                .multilineTextAlignment(.leading)
                .focused($focused)
                .onSubmit(save)
        }
        .formStyle(.grouped)
        .frame(width: 320)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) { Button("Rename", action: save) }
        }
        .onAppear {
            name = target.current
            focused = true
        }
    }

    private func save() {
        onSave(name)
        dismiss()
    }
}

/// Configure Group: its emoji icon and name. An empty name keeps the
/// automatic one (the directory), shown as the placeholder.
struct GroupSheet: View {
    let model: AppModel
    let group: WorkspaceGroup
    let automaticName: String
    /// The name, the emoji, a picture change (nil none, empty data to
    /// remove it), and a new folder (nil: unchanged).
    let onSave: (String, String, Data?, String?) -> Void
    @State private var picture: Data?
    @State private var folder: String?
    @State private var name = ""
    @State private var icon = ""
    /// Which field has focus: the name, or the icon only while the emoji
    /// picker is choosing into it (never by clicking or tabbing).
    private enum Field { case name, icon }
    @FocusState private var focus: Field?
    @State private var choosing = false
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        Form {
            HStack(spacing: 12) {
                TextField("Icon", text: $icon, prompt: Text("🙂"))
                    .labelsHidden()
                    .font(.system(size: 22))
                    .multilineTextAlignment(.center)
                    .frame(width: 48)
                    .focused($focus, equals: .icon)
                    .allowsHitTesting(false)
                    .overlay { Color.clear.contentShape(Rectangle()).onTapGesture(perform: chooseEmoji) }
                    .onChange(of: icon) { _, v in
                        if v.count > 1, let last = v.last { icon = String(last) }
                        // Picked: back to the name.
                        if choosing, !v.isEmpty {
                            choosing = false
                            focus = .name
                        }
                    }
                    .tooltip("Choose an emoji")
                TextField("Name", text: $name, prompt: Text(automaticName))
                    .labelsHidden()
                    .multilineTextAlignment(.leading)
                    .focused($focus, equals: .name)
                    .onSubmit(save)
            }
            // Tabbing into the icon: on to the name instead.
            .onChange(of: focus) { _, f in
                if f == .icon, !choosing { focus = .name }
                if f == .name { choosing = false } // the picker closed without one
            }
            HStack {
                Button("Choose Emoji…", action: chooseEmoji)
                if !icon.isEmpty { Button("Remove Icon") { icon = "" } }
            }
            .buttonStyle(.link)
            Section {
                HStack(spacing: 12) {
                    if let shown = shownPicture, let img = NSImage(data: shown) {
                        Image(nsImage: img).resizable().scaledToFit().frame(width: 40, height: 40)
                            .clipShape(RoundedRectangle(cornerRadius: 9, style: .continuous))
                    } else {
                        RoundedRectangle(cornerRadius: 9, style: .continuous)
                            .strokeBorder(.tertiary, style: StrokeStyle(lineWidth: 1, dash: [3]))
                            .frame(width: 40, height: 40)
                            .overlay { Image(systemName: "photo").foregroundStyle(.tertiary) }
                    }
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Button("Choose Picture…", action: choosePicture)
                            if shownPicture != nil { Button("Remove Picture") { picture = Data() } }
                        }
                        .buttonStyle(.link)
                        Text(icon.isEmpty ? "Kept in Hammock on this Mac. A project's own .hammock/icon.png shows when none is set here." : "Kept in Hammock on this Mac. Choosing one replaces the emoji.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                .dropDestination(for: URL.self) { urls, _ in
                    guard let u = urls.first, let d = try? Data(contentsOf: u), NSImage(data: d) != nil else { return false }
                    usePicture(d)
                    return true
                }
            } header: {
                Text("Picture")
            }
            if GroupFolder.canChange(group) {
                Section {
                    HStack {
                        Text(shownFolder.isEmpty ? "Home" : GroupFolder.tilde(shownFolder))
                            .lineLimit(1).truncationMode(.middle)
                            .foregroundStyle(.secondary)
                        Spacer()
                        Button("Change…") { if let f = GroupFolder.pick(startingAt: shownFolder) { folder = f } }
                            .buttonStyle(.link)
                    }
                } header: {
                    Text("New tabs open in")
                } footer: {
                    Text("Follows the folder you last worked in here.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .formStyle(.grouped)
        .frame(width: 360)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            ToolbarItem(placement: .confirmationAction) { Button("Save", action: save) }
        }
        .onAppear {
            name = group.name
            icon = group.icon
            focus = .name
        }
    }

    /// The picture as it will be: one chosen here, else the Group's.
    private var shownPicture: Data? {
        if let picture { return picture.isEmpty ? nil : picture }
        return model.groupPicture(group)
    }

    /// Starts in the Group's folder, where a project's logo usually is.
    private func choosePicture() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.png, .jpeg]
        panel.allowsMultipleSelection = false
        panel.directoryURL = URL(fileURLWithPath: shownFolder.isEmpty ? NSHomeDirectory() : shownFolder, isDirectory: true)
        if panel.runModal() == .OK, let u = panel.url, let d = try? Data(contentsOf: u) { usePicture(d) }
    }

    /// A picture chosen: it replaces the emoji, which would show instead.
    private func usePicture(_ d: Data) {
        picture = d
        icon = ""
    }

    private func chooseEmoji() {
        choosing = true
        focus = .icon
        DispatchQueue.main.async { NSApp.orderFrontCharacterPalette(nil) }
    }

    private var shownFolder: String { folder ?? model.groupFolder(group) }

    private func save() {
        onSave(name, icon, picture, folder)
        dismiss()
    }
}

/// The sidebar's rows as they were before macOS 26 made the large rows 40 pt:
/// small rows sized by their content, plus FullRowHover's 18 pt (35 pt tabs
/// with a 31 pt pill, 37.5 pt Group rows).
enum SidebarMetrics {
    static let tabContent: CGFloat = 17
    static let groupContent: CGFloat = 19.5
}

struct GroupRow: View {
    let model: AppModel
    let group: WorkspaceGroup
    let collapsed: Bool
    var chevron = true
    /// Tabs shown under the collapsed header, left out of its status.
    var shown: Set<String> = []
    /// group-headings = subtle: smaller and dimmer than the tabs.
    var subtle = false
    /// The Group's icon before its name (group-icons).
    var icon = true
    @Environment(\.sidebarCompact) private var compact

    var body: some View {
        HStack(spacing: 6) {
            if icon {
                // A fixed slot, so names line up whether or not a Group has an icon.
                // Subtle: smaller, like the name.
                GroupIconView(model: model, group: group, size: subtle ? (compact ? 14 : 15) : (compact ? 19 : 21))
                .frame(width: subtle ? (compact ? 15 : 16) : (compact ? 20 : 22))
                .opacity(subtle ? 0.75 : 1)
            }
            VStack(alignment: .leading, spacing: 0) {
                let name = Text(model.title(of: group))
                    .fontWeight(.semibold)
                    // Subtle: under the tabs' 13.5–14 pt, in the secondary colour.
                    .font(.system(size: subtle ? (compact ? 11.5 : 12) : (compact ? 14 : 15)))
                    .lineLimit(1)
                // Otherwise the List's own colour, like the tabs', so it dims
                // with them when the window isn't key (an explicit .primary doesn't).
                if subtle { name.foregroundStyle(.secondary) } else { name }
                if group.isRemote, !group.isOnLinkedComputer {
                    // Whose Mac it runs on.
                    Text(group.offline ? "\(model.hostLabel(of: group)) · offline" : model.hostLabel(of: group))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            if chevron {
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary.opacity(0.7))
                    .rotationEffect(.degrees(collapsed ? 0 : 90))
                    .padding(.leading, 2)
            }
            Spacer(minLength: 4)
            if let c = model.computer(of: group) {
                // On another of your computers: its badge (none for this one's).
                if group.offline {
                    Text("asleep").font(.caption).foregroundStyle(.secondary)
                }
                ComputerBadge(c, size: compact ? 14 : 15)
            } else if group.isRemote, let host = model.people[group.hostUserID] {
                AvatarView(person: host, size: 20)
            } else if group.isShared {
                Image(systemName: "person.2.fill")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .tooltip("Shared", label: "Shared")
            }
            if collapsed {
                AvatarStack(model.members(of: group).filter { $0.0.online && $0.0.userID != model.myUserID && !$0.0.tabID.isEmpty }, size: 16)
            }
            if collapsed, let state = summaryState {
                StatusIcon(state: state, size: compact ? 15 : 16)
            }
        }
        // Compact: nearly the full size text, with less room around it.
        .font(.system(size: compact ? 14 : 15))
        .frame(height: compact || (group.isRemote && !group.isOnLinkedComputer) ? nil : SidebarMetrics.groupContent)
        .opacity(group.offline ? 0.55 : 1)
    }

    private var summaryState: AgentState? {
        let states = model.tabs(in: group).filter { !shown.contains($0.id) }.compactMap { model.agentState(of: $0) }
        for s in [AgentState.needsInput, .errored, .working, .idle] where states.contains(s) { return s }
        return nil
    }
}

struct TabRow: View {
    let model: AppModel
    let tab: WorkspaceTab
    /// Under the pointer: shows the close button.
    var hovered = false
    @Environment(\.sidebarCompact) private var compact

    private var trailing: Bool { AppConfig.shared.tabStatus == .trailing }

    var body: some View {
        HStack(spacing: compact ? 7 : 8) {
            // The focused pane decides the icon; with status in the
            // icon, an agent's state comes first.
            if trailing, let harness = focusedAgent {
                // Its logo, on a squircle in the colourful style.
                ProgramIcon(agent: harness, size: compact ? 16 : 17)
                    .frame(width: compact ? 16 : 17, height: compact ? 16 : 17)
            } else if let page = browserPane, trailing || model.agentState(of: tab) == nil {
                Favicon(url: page.faviconURL, size: compact ? 16 : 17)
            } else if trailing {
                StatusIcon(state: nil, size: compact ? 16 : 17, program: model.focusedProgram(of: tab))
            } else {
                StatusIcon(state: model.agentState(of: tab), resting: model.hasRestingAgent(tab), shells: model.hasRestingAgentWithWork(tab), subagents: model.hasRestingAgentWithSubagents(tab), size: compact ? 16 : 17,
                           program: model.focusedProgram(of: tab), asleep: model.hasSleepingAgent(tab), progress: model.agentProgress(of: tab), waiting: model.hasAgentWaitingOnLimit(tab))
            }
            Text(model.title(of: tab))
                .font(.system(size: compact ? 13.5 : 14)) // 1pt under the large sidebar row size; compact, a little smaller
                .lineLimit(1)
            Spacer(minLength: 4)
            // Hovered, a close button takes the place of what's at the end
            // of the row (Settings ▸ Appearance ▸ Close button on hover).
            if hovered, AppConfig.shared.tabCloseButton {
                closeButton
            } else {
                AvatarStack(model.presence(in: tab), size: compact ? 15 : 16)
                gitMark
                // A scheduled task's run: at the trailing edge, before the time
                // if there is one (so it sits where the time would).
                if let s = model.schedule(of: tab) {
                    Image(systemName: "clock")
                        .font(.system(size: compact ? 10 : 10.5, weight: .medium))
                        .foregroundStyle(.tertiary)
                        .help("A run of “\(s.name)”")
                        .accessibilityLabel("Scheduled run")
                }
                detail
                    .font(compact ? .system(size: 11.5) : .callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                if trailing { trailingStatus }
            }
        }
        // The rows of before macOS 26 (SidebarMetrics).
        .frame(height: compact ? nil : SidebarMetrics.tabContent)
        // Compact: a colourful icon's squircle 20 pt, not 22, clear of the pill's edges.
        .environment(\.programSquircleScale, compact ? 1.25 : ProgramIcon.squircleScale)
    }

    private var closeButton: some View {
        Button { CloseGuard.confirm(model, tab: tab) { Task { await model.closeTab(tab.id) } } } label: {
            Image(systemName: "xmark")
                .font(.system(size: compact ? 8.5 : 9, weight: .bold))
                .frame(width: compact ? 15 : 16, height: compact ? 15 : 16)
                .background(Circle().fill(Color.primary.opacity(0.08)))
                .contentShape(Circle())
        }
        .buttonStyle(.plain)
        .foregroundStyle(.secondary)
        .tooltip("Close Tab", label: "Close Tab")
        .accessibilityLabel("Close Tab")
    }

    private var browserPane: Pane? {
        model.panes[tab.focusedPaneID].flatMap { $0.kind == .browser ? $0 : nil }
    }

    /// The branch's pull request, or a branch for a worktree; once its
    /// branch is merged (a while ago), Remove Worktree in its place.
    @ViewBuilder
    private var gitMark: some View {
        if model.canRemoveWorktree(tab) {
            Button { WorktreeUI.remove(model, tab) } label: {
                Image(systemName: "trash")
                    .font(.system(size: compact ? 10 : 10.5, weight: .medium))
                    .frame(width: compact ? 15 : 16, height: compact ? 15 : 16)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundStyle(.secondary)
            .tooltip("Remove Worktree: \(tab.git.summary)", label: "Remove Worktree")
            .accessibilityLabel("Remove Worktree")
        } else if tab.hasGit, tab.git.shown {
            GitMark(tab.git, size: compact ? 10 : 10.5)
                .help(tab.git.summary)
        }
    }

    /// The harness of an agent running in the focused pane.
    private var focusedAgent: String? {
        model.panes[tab.focusedPaneID].flatMap { $0.hasAgent && !$0.exited && $0.agent.state != .exited ? $0.agent.harness : nil }
    }

    /// With `tab-status = trailing`: the agent's state after the time; an
    /// agent at rest shows nothing, unless its shells still run (or it's
    /// asleep: a moon). The icon
    /// stays in the row while there's an agent, so a state arriving pops in.
    @ViewBuilder
    private var trailingStatus: some View {
        if model.shownAgentPane(of: tab) != nil {
            StatusIcon(state: model.agentState(of: tab), resting: true, shells: model.hasRestingAgentWithWork(tab), subagents: model.hasRestingAgentWithSubagents(tab),
                       size: compact ? 13 : 14, quietAtRest: true, asleep: model.hasSleepingAgent(tab), progress: model.agentProgress(of: tab), waiting: model.hasAgentWaitingOnLimit(tab))
        }
    }

    /// How long an agent has been at it (nothing while it's idle), else the
    /// pane count. With `tab-status = trailing`, the agent whose state shows.
    @ViewBuilder
    private var detail: some View {
        let panes = model.panes(in: tab)
        if trailing, let p = model.shownAgentPane(of: tab) {
            if p.agent.shownState != .idle, p.agent.state != .exited { ElapsedText(sinceUnixMs: p.agent.stateSinceUnixMs) }
        } else if let agentPane = panes.first(where: { $0.hasAgent && $0.agent.state != .exited }) {
            let a = agentPane.agent
            if a.state != .idle { ElapsedText(sinceUnixMs: a.stateSinceUnixMs) }
        } else if panes.count > 1 {
            Text("\(panes.count)")
        }
    }
}

/// A page's favicon, or a globe until there is one.
struct Favicon: View {
    let url: String
    var size: CGFloat = 16

    var body: some View {
        AsyncImage(url: URL(string: url)) { phase in
            if let image = phase.image {
                image.resizable().interpolation(.high).scaledToFit()
                    .frame(width: size - 2, height: size - 2)
                    .clipShape(RoundedRectangle(cornerRadius: 3))
            } else {
                Image(systemName: "globe").foregroundStyle(.secondary).font(.system(size: size - 2))
            }
        }
        .frame(width: size, height: size)
        .accessibilityHidden(true)
    }
}

struct HostFooter: View {
    let model: AppModel
    @State private var confirmStop = false

    private var status: String {
        guard model.connected else { return "Connecting to hmkd…" }
        let s = model.sharing
        guard s.groups > 0 else { return "This Mac" }
        let groups = s.groups == 1 ? "1 Group" : "\(s.groups) Groups"
        return s.online > 0 ? "Sharing \(groups) · \(s.online) here" : "Sharing \(groups)"
    }

    var body: some View {
        if model.hasLinkedComputers {
            // Your computers: the switcher (LinkComputers.swift).
            ComputerSwitcher(model: model)
        } else {
            single
        }
    }

    private var single: some View {
        HStack(spacing: 8) {
            Group {
                if let me = model.thisComputer {
                    ComputerIcon(me, size: 13, weight: .regular)
                } else {
                    Image(systemName: "desktopcomputer").foregroundStyle(.secondary)
                }
            }
            VStack(alignment: .leading, spacing: 0) {
                Text(model.hostName.isEmpty ? "This Mac" : model.hostName.replacingOccurrences(of: ".local", with: ""))
                    .font(.callout.weight(.medium))
                Text(status)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if model.sharing.groups > 0 {
                Button("Stop") { confirmStop = true }
                    .controlSize(.small)
                    .tooltip("Stop all sharing: everyone is disconnected")
                    .confirmationDialog("Stop sharing everything?", isPresented: $confirmStop) {
                        Button("Stop All Sharing", role: .destructive) { Task { await model.stopSharing(nil) } }
                    } message: {
                        Text("Everyone is disconnected and every invite link stops working.")
                    }
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .contentShape(Rectangle())
        .contextMenu {
            if let me = model.thisComputer {
                Button("Icon…") { LinkUI.shared.customise(me) }
            }
            Button("Link a Computer…") { LinkUI.shared.open() }
        }
    }
}

/// Scrolls the sidebar's table view (found by SidebarRefusesFocus) to show
/// a row clear of what covers its edges: the toolbar at the top (the
/// scroll view's inset) and the bottom bar, whose height the List doesn't
/// count when SwiftUI's scrollTo shows a row.
@MainActor
final class SidebarScroller {
    weak var table: NSTableView?

    /// Shows row `i` with the least scrolling, or false when the table
    /// isn't the List's rows one for one (the caller falls back to scrollTo).
    @discardableResult
    func reveal(row i: Int, of count: Int, bottomInset: CGFloat, animated: Bool) -> Bool {
        guard let t = table, let scroll = t.enclosingScrollView, t.numberOfRows == count, i < count else { return false }
        let clip = scroll.contentView
        let r = clip.convert(t.rect(ofRow: i), from: t)
        let b = clip.bounds
        let insets = scroll.contentInsets
        let top = b.minY + insets.top, bottom = b.maxY - max(insets.bottom, bottomInset)
        var y = b.minY
        if r.minY < top { y -= top - r.minY } else if r.maxY > bottom { y += r.maxY - bottom } else { return true }
        let docHeight = scroll.documentView?.frame.height ?? 0
        y = max(-insets.top, min(y, docHeight - b.height + max(insets.bottom, bottomInset)))
        let to = NSPoint(x: b.minX, y: y)
        if animated {
            NSAnimationContext.runAnimationGroup { ctx in
                ctx.duration = 0.25
                ctx.allowsImplicitAnimation = true
                clip.animator().setBoundsOrigin(to)
            } completionHandler: { scroll.reflectScrolledClipView(clip) }
        } else {
            clip.setBoundsOrigin(to)
        }
        scroll.reflectScrolledClipView(clip)
        return true
    }
}

/// Makes the sidebar's table view refuse first responder and drop its own
/// selection highlight (see SidebarView).
struct SidebarRefusesFocus: NSViewRepresentable {
    var scroller: SidebarScroller?

    final class Probe: NSView {
        weak var scroller: SidebarScroller?

        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            // The table is created alongside this view; look once it exists.
            DispatchQueue.main.async { [weak self] in self?.apply() }
        }

        func apply() {
            var v: NSView? = self
            while let s = v?.superview, !(s is NSScrollView) { v = s }
            let root = v?.superview?.superview ?? window?.contentView
            func walk(_ view: NSView) {
                if let t = view as? NSTableView {
                    scroller?.table = t
                    t.refusesFirstResponder = true
                    t.selectionHighlightStyle = .none // tabs draw their own (see tabPill)
                    SidebarRefusesFocus.dropContextMenuRing(type(of: t))
                    SidebarRefusesFocus.ignoreMouseDown(t)
                }
                view.subviews.forEach(walk)
            }
            if let root { walk(root) }
            // HMK_DEBUG_SIDEBAR_SCROLL=<pt>: start scrolled, to check what
            // shows under the window's buttons.
            if let s = ProcessInfo.processInfo.environment["HMK_DEBUG_SIDEBAR_SCROLL"].flatMap(Double.init),
               let scroll = v?.superview as? NSScrollView ?? root.flatMap({ Self.firstScroll(in: $0) }) {
                DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                    scroll.contentView.scroll(to: NSPoint(x: 0, y: scroll.contentView.bounds.minY + s))
                    scroll.reflectScrolledClipView(scroll.contentView)
                }
            }
        }

        private static func firstScroll(in view: NSView) -> NSScrollView? {
            if let s = view as? NSScrollView, s.documentView is NSTableView { return s }
            for v in view.subviews { if let s = firstScroll(in: v) { return s } }
            return nil
        }
    }

    func makeNSView(context: Context) -> Probe {
        let p = Probe()
        p.scroller = scroller
        return p
    }
    func updateNSView(_ v: Probe, context: Context) { v.scroller = scroller }

    /// The table outlines a right-clicked row in blue, ignoring our own pills;
    /// there's no setting for it, so its drawing methods become no-ops (on
    /// SwiftUI's table class, which only lists use).
    @MainActor private static var ringDropped = Set<ObjectIdentifier>()

    /// The sidebar's table takes no clicks of its own: rows select and toggle
    /// by their own taps, and drag by their own gestures. On macOS 26 the
    /// table's mouseDown runs a tracking loop that swallows the drag that
    /// follows, so rows' drags froze (Groups) or never started (tabs). Other
    /// lists share the class, so only tables marked here skip it.
    @MainActor private static var mouseDownPatched = Set<ObjectIdentifier>()
    nonisolated(unsafe) private static var sidebarKey: UInt8 = 0

    @MainActor static func ignoreMouseDown(_ table: NSTableView) {
        objc_setAssociatedObject(table, &sidebarKey, true, .OBJC_ASSOCIATION_RETAIN_NONATOMIC)
        let cls: AnyClass = type(of: table)
        guard mouseDownPatched.insert(ObjectIdentifier(cls)).inserted else { return }
        let sel = #selector(NSResponder.mouseDown(with:))
        guard let m = class_getInstanceMethod(cls, sel) else { return }
        typealias MouseDown = @convention(c) (AnyObject, Selector, NSEvent) -> Void
        let original = unsafeBitCast(method_getImplementation(m), to: MouseDown.self)
        let block: @convention(block) (AnyObject, NSEvent) -> Void = { obj, event in
            if objc_getAssociatedObject(obj, &sidebarKey) as? Bool == true { return }
            original(obj, sel, event)
        }
        class_replaceMethod(cls, sel, imp_implementationWithBlock(block), method_getTypeEncoding(m))
    }

    @MainActor static func dropContextMenuRing(_ cls: AnyClass) {
        guard ringDropped.insert(ObjectIdentifier(cls)).inserted else { return }
        let noRow: @convention(block) (AnyObject, Int) -> Void = { _, _ in }
        let noRect: @convention(block) (AnyObject, NSRect) -> Void = { _, _ in }
        for (name, block) in [("drawContextMenuHighlightForRow:", noRow as Any), ("_drawContextualMenuHighlightInClipRect:", noRect as Any)] {
            let sel = NSSelectorFromString(name)
            guard let m = class_getInstanceMethod(cls, sel) else { continue }
            class_replaceMethod(cls, sel, imp_implementationWithBlock(block), method_getTypeEncoding(m))
        }
    }
}

/// A sidebar row's pill, Dia-style: a bright one (with a soft shadow) when
/// it's selected, which replaces the system highlight, and a dim one on
/// hover. The workspace sidebar's tabs and Settings' panes.
struct SidebarPill: View {
    let selected: Bool
    var hovered = false
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        let dark = colorScheme == .dark
        if selected {
            RoundedRectangle(cornerRadius: 10)
                .fill(dark ? Color.white.opacity(0.16) : Color.white.opacity(0.92))
                .shadow(color: .black.opacity(dark ? 0.3 : 0.08), radius: 2, y: 1)
        } else if hovered {
            RoundedRectangle(cornerRadius: 10)
                .fill(dark ? Color.white.opacity(0.06) : Color.white.opacity(0.4))
        }
    }
}

/// Makes a sidebar row's whole height (including the list's usual vertical
/// insets) its hover and click area, so the Group hover group has no dead
/// strips between rows.
struct FullRowHover: ViewModifier {
    @Environment(\.sidebarCompact) private var compact

    func body(content: Content) -> some View {
        content
            // Compact: room for a colourful program icon's squircle (20 pt) inside the pill, with some to spare.
            .padding(.vertical, compact ? 6 : 9)
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
            .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
    }
}

/// Follows a sidebar drag once a row's gesture has started it, from the
/// app's own mouse events: on macOS 26 the List rebuilds rows as a drag
/// reorders them, which cancels the row's gesture without ending it, so the
/// drag stopped moving (tabs) or never dropped (Groups).
@MainActor
final class SidebarDragTracker {
    private var monitor: Any?
    private var offset = CGPoint.zero
    private var onMove: ((CGPoint) -> Void)?
    private var onEnd: (() -> Void)?

    var active: Bool { monitor != nil }

    /// Starts following at p (in the gesture's global space).
    func begin(at p: CGPoint, onMove: @escaping (CGPoint) -> Void, onEnd: @escaping () -> Void) {
        end()
        self.onMove = onMove
        self.onEnd = onEnd
        // The gesture's space against the window's (y down from the content's top).
        if let e = NSApp.currentEvent, let q = Self.point(e) { offset = CGPoint(x: p.x - q.x, y: p.y - q.y) }
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.leftMouseDragged, .leftMouseUp]) { [weak self] e in
            guard let self else { return e }
            if e.type == .leftMouseUp {
                let done = self.onEnd
                self.end()
                done?()
            } else if let q = Self.point(e) {
                self.onMove?(CGPoint(x: q.x + self.offset.x, y: q.y + self.offset.y))
            }
            return e
        }
    }

    /// Stops following (the drag dropped another way, or ended).
    func end() {
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
        onMove = nil
        onEnd = nil
    }

    private static func point(_ e: NSEvent) -> CGPoint? {
        guard let view = e.window?.contentView else { return nil }
        return CGPoint(x: e.locationInWindow.x, y: view.bounds.height - e.locationInWindow.y)
    }
}

/// Above the Mac in the sidebar's footer: the usage limits of the agents
/// chosen in Settings ▸ Agents (UsageLimitsView), in the window theme's colour.
struct UsageFooter: View {
    let model: AppModel

    var body: some View {
        // Observed: follows Settings' switches (and reads no usage while none is on).
        let shown = UsageLimitsPrefs.shown(model)
        if !shown.isEmpty {
            // Each minute, so the time to each reset counts down.
            TimelineView(.periodic(from: .now, by: 60)) { ctx in
                UsageLimitsView(usage: shown, now: ctx.date, tint: themeColor)
                    .padding(.horizontal, 16)
                    .padding(.top, 10)
            }
        }
    }

    /// The theme's colour (the agent view's); Native has the system's accent.
    private var themeColor: Color { AppConfig.shared.agentColors.color }
}

/// The sidebar's bottom bar (Needs You, Finished and the footers) changes
/// height over an animation rather than in one frame. The bar's height is the
/// List's bottom inset, so when it shrank at once the List, scrolled to its
/// end, jumped to its new end (or its top, when it now fit) in one frame;
/// stepping the height every frame makes the rows slide down with the bar
/// instead. Its content changes at once, laid out at its own height, pinned
/// to the bottom and clipped while the bar is taller or shorter than it: the
/// frame is the only thing that moves (animating the content's own layout too
/// would move it twice).
struct SlidingBarHeight: ViewModifier {
    @State private var height: CGFloat?
    @State private var width: CGFloat?

    func body(content: Content) -> some View {
        content
            .fixedSize(horizontal: false, vertical: true)
            .onGeometryChange(for: CGSize.self) { $0.size } action: { size in
                // The first measurement, and a change of width (the sidebar
                // resized: text rewraps), take effect at once.
                if height == nil || width != size.width {
                    var t = Transaction()
                    t.disablesAnimations = true
                    withTransaction(t) { height = size.height }
                } else if height != size.height {
                    withAnimation(.snappy(duration: 0.25)) { height = size.height }
                }
                width = size.width
            }
            .modifier(AnimatedHeight(height: height))
    }

    /// A frame whose height is laid out anew on every frame of an animation
    /// (an Animatable modifier), so the inset it gives the List follows it.
    private struct AnimatedHeight: ViewModifier, Animatable {
        var height: CGFloat?
        var animatableData: CGFloat {
            get { height ?? 0 }
            set { height = newValue }
        }

        func body(content: Content) -> some View {
            content
                .frame(height: height, alignment: .bottom)
                .clipped()
                .contentShape(Rectangle()) // clipped-off content takes no clicks
        }
    }
}
