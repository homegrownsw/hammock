import HammockKit
import SwiftUI

/// How the window lays out Groups and tabs (Settings ▸ Appearance).
enum SidebarLayout: String, CaseIterable {
    /// Groups with their tabs listed under them in the sidebar.
    case vertical
    /// The same, tighter: smaller rows and type, for more tabs on screen.
    case compact
    /// Groups in the sidebar; the selected Group's tabs in a tab bar.
    case horizontal

    static let pref = Pref("sidebar-layout", defaults: "sidebarLayout", .string, SidebarLayout.vertical.rawValue)
    @MainActor static var current: SidebarLayout { SidebarLayout(rawValue: Prefs.shared.string(pref)) ?? .vertical }
    @MainActor static func set(_ l: SidebarLayout) { Prefs.shared.set(pref, l.rawValue) }

    var label: String {
        switch self {
        case .vertical: "Vertical Tabs"
        case .compact: "Compact Vertical Tabs"
        case .horizontal: "Horizontal Tabs"
        }
    }

    /// Under its picture in Settings.
    var shortLabel: String {
        switch self {
        case .vertical: "Vertical"
        case .compact: "Compact"
        case .horizontal: "Horizontal"
        }
    }
}

private struct SidebarCompactKey: EnvironmentKey { static let defaultValue = false }

extension EnvironmentValues {
    /// Compact Vertical Tabs: sidebar rows draw tighter.
    var sidebarCompact: Bool {
        get { self[SidebarCompactKey.self] }
        set { self[SidebarCompactKey.self] = newValue }
    }
}

/// The tab to show when a Group is picked in Horizontal Tabs: the one you
/// were last in, else its first.
@MainActor
func currentTab(of group: WorkspaceGroup, in model: AppModel) -> WorkspaceTab? {
    let tabs = model.tabs(in: group)
    return tabs.first { AppModel.paneIDs(of: $0).contains(group.lastFocusedPaneID) } ?? tabs.first
}

/// Horizontal Tabs: the selected Group's tabs across the top of the
/// window's content, in the style of Terminal's tab bar on macOS 26: a row
/// of pills in a capsule of their own, standing off the panes below, equal
/// widths, the selected one raised in the window's colour, a close button on
/// hover, and a round + beside it.
struct TabStrip: View {
    let model: AppModel
    let group: WorkspaceGroup
    @State private var hovered: String?
    // Dragging a tab, as in Safari: it follows the pointer along the bar and
    // the others make way. order is the order shown meanwhile (and after the
    // drop, until the daemon's matches); dragX is where the dragged tab's
    // leading edge is.
    @State private var dragging: String?
    @State private var dragStart = 0
    @State private var dragX: CGFloat = 0
    @State private var order: [String]?
    @Environment(\.colorScheme) private var colorScheme
    @AppStorage(SidebarHiding.key) private var sidebarHidden = false

    /// The capsule the tabs sit in, and the + beside it.
    private let height: CGFloat = 28
    /// Between the capsule's edge and the tabs in it.
    private let inset: CGFloat = 3
    /// Between the capsule and the +.
    private let gap: CGFloat = 8
    /// Around the strip: as far off the window's edges as spacious panes
    /// are, and that far above them, compact or spacious.
    private let margin: CGFloat = 6

    var body: some View {
        let tabs = shown(model.tabs(in: group))
        GeometryReader { geo in
            let track = max(0, geo.size.width - height - gap)
            // The tabs share the capsule equally, one filling it; at 130pt
            // each it scrolls instead.
            let width = max(130, (track - inset * 2) / CGFloat(max(tabs.count, 1)))
            HStack(spacing: gap) {
                ScrollViewReader { proxy in
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 0) {
                            ForEach(Array(tabs.enumerated()), id: \.element.id) { i, tab in
                                tabView(tab, width: width, divider: divides(after: i, in: tabs))
                                    .offset(x: dragging == tab.id ? dragX - CGFloat(i) * width : 0)
                                    .zIndex(dragging == tab.id ? 1 : 0)
                                    // The dragged tab stays under the pointer; the rest slide.
                                    .transaction { if dragging == tab.id { $0.animation = nil } }
                                    .gesture(
                                        DragGesture(minimumDistance: 4, coordinateSpace: .named("tabStrip"))
                                            .onChanged { g in drag(tab.id, by: g.translation.width, in: tabs.map(\.id), width: width) }
                                            .onEnded { _ in drop() }
                                    )
                                    .id(tab.id)
                            }
                        }
                        .padding(inset)
                        // Drags measured here, not on the tab: it moves as it
                        // takes each new place, which threw the pointer off it.
                        .coordinateSpace(.named("tabStrip"))
                    }
                    .frame(width: track, height: height)
                    .background(Capsule().fill(trackColor))
                    .clipShape(Capsule())
                    // The selected tab kept in view: another in this Group,
                    // a glide to it; a Group opened (or the strip shown),
                    // straight there, once its tabs are laid out.
                    .onChange(of: [group.id, model.selectedTabID ?? ""], initial: true) { old, new in
                        let id = new[1]
                        guard !id.isEmpty else { return }
                        if old[0] == new[0] && old != new {
                            withAnimation(.snappy(duration: 0.2)) { proxy.scrollTo(id) }
                        } else {
                            DispatchQueue.main.async { proxy.scrollTo(id) }
                        }
                    }
                }
                Button { Task { await model.createTab(in: group.id) } } label: {
                    Image(systemName: "plus")
                        .font(.system(size: 13, weight: .medium))
                        .frame(width: height, height: height)
                        .contentShape(Circle())
                }
                .buttonStyle(.plain)
                .glassEffect(.regular.interactive(), in: Circle())
                .disabled(group.isRemote && (!group.canDrive || group.offline))
                .tooltip(Keys.hint("New Tab", "new_tab"))
            }
        }
        .frame(height: height)
        // On the tab's own backdrop (an empty Group's), as the toolbar is:
        // in line with the panes below, and as far above them as they are
        // apart. The sidebar meets it, as it does them.
        .padding(EdgeInsets(top: 0, leading: sidebarHidden ? margin : 0, bottom: margin, trailing: margin))
        .animation(.snappy(duration: 0.2), value: tabs.map(\.id))
        .onChange(of: model.tabs(in: group).map(\.id)) { _, ids in
            if dragging == nil, ids == order { order = nil }
        }
    }

    private var dark: Bool { colorScheme == .dark }
    private var trackColor: Color { Color.primary.opacity(dark ? 0.09 : 0.07) }
    private var pillColor: Color { dark ? Color.white.opacity(0.14) : Color(nsColor: .windowBackgroundColor) }

    @ViewBuilder
    private func tabView(_ tab: WorkspaceTab, width: CGFloat, divider: Bool) -> some View {
        let selected = model.selectedTabID == tab.id
        let hover = hovered == tab.id && dragging == nil
        ZStack {
            HStack(spacing: 6) {
                if let page = model.panes[tab.focusedPaneID], page.kind == .browser, model.agentState(of: tab) == nil {
                    Favicon(url: page.faviconURL, size: 14)
                } else {
                    StatusIcon(state: model.agentState(of: tab), resting: model.hasRestingAgent(tab), shells: model.hasRestingAgentWithWork(tab), size: 14,
                               program: model.focusedProgram(of: tab), asleep: model.hasSleepingAgent(tab))
                }
                Text(model.title(of: tab))
                    .font(.system(size: 12, weight: selected ? .medium : .regular))
                    .lineLimit(1)
                    .truncationMode(.middle)
                AvatarStack(model.presence(in: tab), size: 13)
            }
            .padding(.horizontal, 26)
            .foregroundStyle(selected ? .primary : .secondary)
            HStack {
                // Close on the leading edge, on hover, as in Finder.
                Button { CloseGuard.confirm(model, tab: tab) { Task { await model.closeTab(tab.id) } } } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 9, weight: .bold))
                        .frame(width: 16, height: 16)
                        .background(Circle().fill(Color.primary.opacity(0.08)))
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .opacity(hover && AppConfig.shared.tabCloseButton ? 1 : 0)
                .tooltip("Close Tab")
                Spacer()
            }
            .padding(.leading, 4)
        }
        .frame(width: width, height: height - inset * 2)
        .background {
            if selected {
                Capsule().fill(pillColor)
                    .shadow(color: .black.opacity(dark ? 0.3 : 0.12), radius: 1, y: 0.5)
            } else if hover {
                Capsule().fill(Color.primary.opacity(0.05))
            }
        }
        // A short line before the next tab, between two unselected ones.
        .overlay(alignment: .trailing) {
            Capsule().fill(Color.primary.opacity(dark ? 0.2 : 0.16))
                .frame(width: 1, height: 14)
                .offset(x: 0.5)
                .opacity(divider ? 1 : 0)
        }
        .contentShape(Capsule())
        .onTapGesture { model.selectedTabID = tab.id }
        .onHover { hovered = $0 && dragging == nil ? tab.id : (hovered == tab.id ? nil : hovered) }
        .tooltip(model.title(of: tab))
        .contextMenu {
            TabMenuItems(model: model, tab: tab)
        }
    }

    /// Whether a line separates the tab at `i` from the next: as in
    /// Terminal, only between two tabs that aren't selected, nor hovered or
    /// being dragged, whose pills would show the gap anyway.
    private func divides(after i: Int, in tabs: [WorkspaceTab]) -> Bool {
        guard i + 1 < tabs.count else { return false }
        return [tabs[i].id, tabs[i + 1].id].allSatisfy { id in
            id != model.selectedTabID && id != dragging && (id != hovered || dragging != nil)
        }
    }

    /// The tabs in the order shown: while dragging (and until the daemon
    /// agrees), the dragged one where it's going.
    private func shown(_ tabs: [WorkspaceTab]) -> [WorkspaceTab] {
        guard let order, Set(order) == Set(tabs.map(\.id)) else { return tabs }
        let byID = Dictionary(uniqueKeysWithValues: tabs.map { ($0.id, $0) })
        return order.compactMap { byID[$0] }
    }

    private func drag(_ id: String, by dx: CGFloat, in ids: [String], width: CGFloat) {
        if dragging == nil {
            dragging = id
            hovered = nil
            dragStart = ids.firstIndex(of: id) ?? 0
            order = ids
        }
        guard dragging == id, var o = order, let from = o.firstIndex(of: id) else { return }
        // Kept within the bar.
        dragX = min(max(CGFloat(dragStart) * width + dx, 0), CGFloat(o.count - 1) * width)
        // Past halfway over a neighbour, it takes that place.
        let to = min(max(Int((dragX / width).rounded()), 0), o.count - 1)
        if to != from {
            o.remove(at: from)
            o.insert(id, at: to)
            withAnimation(.snappy(duration: 0.2)) { order = o }
        }
    }

    private func drop() {
        guard let id = dragging, let o = order, let to = o.firstIndex(of: id) else { dragging = nil; return }
        // Into its place.
        withAnimation(.snappy(duration: 0.2)) { dragging = nil }
        if model.tabs(in: group).map(\.id) == o {
            order = nil
            return
        }
        model.moveTab(id, to: group.id, index: to)
        // In case the daemon's order never matches (it refused the move).
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { if dragging == nil, order == o { order = nil } }
    }
}
