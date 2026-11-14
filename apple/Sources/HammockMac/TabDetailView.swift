import HammockKit
import SwiftUI

struct TabDetailView: View {
    let model: AppModel
    let tab: WorkspaceTab

    @AppStorage(SidebarHiding.key) private var storedSidebarHidden = false
    @Environment(\.windowScope) private var scope
    /// No sidebar beside the panes: hidden, or a window of its own without one.
    private var sidebarHidden: Bool { scope.map { !$0.showsSidebar } ?? storedSidebarHidden }

    var body: some View {
        VStack(spacing: 0) {
            LayoutView(model: model, layout: tab.layout, focusedPaneID: currentFocus, zoomed: PaneZoom.shared.zoomed(in: tab)) { newLayout in
                model.setLayout(tab.id, newLayout)
            }
            // Spacious spacing: the panes stand off the window's edges (right,
            // bottom, and left with the sidebar hidden), not the toolbar's or
            // the sidebar's: those meet them, as compact's do.
            .padding(EdgeInsets(top: 0, leading: sidebarHidden ? PaneLook.inset : 0, bottom: PaneLook.inset, trailing: PaneLook.inset))
            // A pane floating: the splits close up around it as its card grows out.
            // (Only then: an animation(nil) here would take the pane
            // animation off panes opening and closing.)
            .transaction(value: tab.layout) { t in
                if FloatMotion.shared.animatesLayout, let a = PaneMotion.when(.snappy(duration: FloatMotion.duration)) { t.animation = a }
            }
            // Floating panes over the splits, in the same space.
            .overlay { FloatLayer(model: model, tab: tab, focusedPaneID: currentFocus) }
            .overlay { PaneSwapChip(model: model) }
            .coordinateSpace(paneSpace)
            FloatShelf(model: model, tab: tab)
        }
        .coordinateSpace(tabSpace)
        .animation(PaneMotion.when(.snappy(duration: 0.2)), value: tab.floating.filter(\.minimised).count)
        // Moving focus to another pane (⌘]) zooms that one instead.
        .onChange(of: model.focusedPaneID) { _, id in if let id { PaneZoom.shared.follow(id, in: tab) } }
            // Compact: the hairlines between panes. Spacious: the panes sit
            // on the tab's background (HammockApp's detail: an empty Group's).
            // Not under the toolbar, which shows that background either way.
            .background(PaneLook.spacious ? Color.clear : Color(nsColor: .separatorColor), ignoresSafeAreaEdges: [])
            .navigationTitle(model.title(of: tab))
            .navigationSubtitle(subtitle)
            .toolbar {
                ToolbarItemGroup(placement: .primaryAction) {
                    // Each opens a menu of what to open: a terminal, browser or simulator.
                    Menu {
                        SplitMenuItems(model: model, paneID: currentFocus, direction: .horizontal)
                    } label: { Label("Split Right", systemImage: "rectangle.split.2x1") }
                        .help(Keys.hint("Split Right", "split_right", for: "a terminal"))
                    Menu {
                        SplitMenuItems(model: model, paneID: currentFocus, direction: .vertical)
                    } label: { Label("Split Down", systemImage: "rectangle.split.1x2") }
                        .help(Keys.hint("Split Down", "split_down", for: "a terminal"))
                    Menu {
                        NewTabMenuItems(model: model, groupID: tab.groupID)
                    } label: { Label("New Tab", systemImage: "plus") }
                        .help(Keys.hint("New Tab", "new_tab", for: "a terminal"))
                        .task { SimulatorList.shared.refresh(model) }
                }
                ToolbarItem(placement: .primaryAction) {
                    if let group = model.group(of: tab), let c = model.computer(of: group) {
                        // On another of your computers.
                        Menu {
                            Text("On \(c.name) · \(c.status)")
                            Divider()
                            Button("Show Only \(c.name)") { model.computerFilter = c.userID }
                        } label: { Label { Text("On \(c.name)") } icon: { ComputerIcon(c, size: 13) } }
                        .help("This tab runs on \(c.name)")
                    } else if let group = model.group(of: tab), group.isRemote {
                        Menu {
                            Text("Shared by \(model.hostLabel(of: group))")
                            Text(group.canDrive ? "You can drive" : "You can view")
                            Divider()
                            Button("Leave Group", role: .destructive) { Task { await model.leave(group.id) } }
                        } label: { Label("Shared Group", systemImage: "person.2.fill") }
                        .help("Shared with you")
                    } else if let group = model.group(of: tab), SharingUI.canShare(group) {
                        Button { SharingUI.shared.shareGroup = tab.groupID } label: {
                            Label("Share Group", systemImage: model.group(of: tab)?.isShared == true ? "person.2.fill" : "person.2")
                        }
                        .help("Share this Group…")
                    }
                }
            }
    }

    private var currentFocus: String {
        if let f = model.focusedPaneID, model.panes[f]?.tabID == tab.id { return f }
        return tab.focusedPaneID
    }

    private var subtitle: String {
        let group = model.group(of: tab).map { model.title(of: $0) } ?? ""
        let cwd = model.panes[currentFocus].map { $0.kind == .browser ? (URL(string: $0.url)?.host() ?? "") : $0.shortCwd } ?? ""
        return cwd.isEmpty ? group : "\(group) — \(cwd)"
    }
}

/// Renders a tab's split tree, flat: every pane is one view, keyed by its
/// id, put where the tree (`PanePlan`) places it. A pane keeps its view
/// whatever happens to the tree around it (split beside, its neighbour
/// closed, swapped, zoomed), so its place animates from where it was to where
/// it goes, and its terminal is never built again. `update` receives an
/// edited copy of the layout (new weights after a divider drag).
struct LayoutView: View {
    let model: AppModel
    let layout: PaneLayout
    let focusedPaneID: String
    /// The tab's zoomed pane, if any (see PanePlan).
    var zoomed: String? = nil
    let update: (PaneLayout) -> Void
    /// A divider being dragged: its split, and the weights then and now.
    @State private var drag: PanePlan.Drag?
    /// Panes opening and closing, and drawing again as they go.
    @State private var openClose = OpenCloseMotion()
    @State private var redraws = 0
    @Environment(\.colorScheme) private var colorScheme
    @AppStorage(SidebarHiding.key) private var storedSidebarHidden = false
    @Environment(\.windowScope) private var scope
    private var sidebarHidden: Bool { scope.map { !$0.showsSidebar } ?? storedSidebarHidden }

    var body: some View {
        GeometryReader { geo in
            let area = CGRect(origin: .zero, size: geo.size)
            let _ = redraws
            let (plan, ghosts) = openClose.plan(layout, in: area, zoomed: zoomed, gap: PaneLook.gap, drag: drag) { redraws &+= 1 }
            let tiles = plan.tiles + ghosts
            ZStack(alignment: .topLeading) {
                // Compact: the panes' own background under them, and the
                // hairlines between them drawn on it, each moving with its
                // pane. Spacious: the tab's background (an empty Group's).
                if PaneLook.spacious {
                    Color.clear.allowsHitTesting(false)
                } else {
                    PaneLook.background(dark: colorScheme == .dark).allowsHitTesting(false)
                    ForEach(PanePlan.lines(tiles: tiles, bars: plan.bars, in: area, gap: PaneLook.gap)) { line in
                        Rectangle().fill(Color(nsColor: .separatorColor))
                            .frame(width: line.rect.width, height: line.rect.height)
                            .offset(x: line.rect.minX, y: line.rect.minY)
                            .allowsHitTesting(false)
                    }
                }
                // In order, explicitly: a pane leaving stays over the background
                // (SwiftUI draws a removed view of a ZStack at the back otherwise).
                ForEach(tiles) { tile($0, in: area).zIndex(1) }
                ForEach(tiles.filter { !$0.folded }) { t in
                    PaneFocusRing(rect: t.clip, on: t.id == focusedPaneID && sharesTab(t.id) && AppConfig.shared.activePaneBorder,
                                  outlined: PaneLook.spacious, flushLeading: flushWithSidebar(t.clip, in: area),
                                  shape: shape(at: t.clip, in: area))
                        .zIndex(1.5)
                }
                ForEach(plan.bars) { bar($0, in: area).zIndex(2) }
                ForEach(plan.dividers) { DividerHandle(d: $0, drag: $drag, layout: layout, update: update).zIndex(3) }
            }
            // A hairline beside a pane of no size at the tab's edge stays in it.
            // Spacious, the border's outer half at the tab's edge shows.
            .clipShape(Rectangle().inset(by: PaneLook.spacious ? -PaneLook.borderWidth : 0))
        }
        .onReceive(NotificationCenter.default.publisher(for: PaneMotion.settled)) { _ in
            if openClose.settled() { redraws &+= 1 }
        }
    }

    /// A pane against the sidebar, which would cut off the outer half of a
    /// line on its edge.
    private func flushWithSidebar(_ rect: CGRect, in area: CGRect) -> Bool {
        PaneLook.spacious && !sidebarHidden && rect.minX <= area.minX + 0.5
    }

    /// A pane's shape here (PaneLook.shape(at:in:sidebarHidden:)).
    private func shape(at rect: CGRect, in area: CGRect) -> UnevenRoundedRectangle {
        PaneLook.shape(at: rect, in: area, sidebarHidden: sidebarHidden)
    }

    private func tile(_ t: PanePlan.Tile, in area: CGRect) -> some View {
        let shape = shape(at: t.clip, in: area)
        return PaneView(model: model, paneID: t.id, isFocused: t.id == focusedPaneID)
            .clipShape(shape)
            .modifier(PaneFocusLook(isFocused: t.id == focusedPaneID, among: sharesTab(t.id), shape: shape))
            .environment(\.paneFolded, t.folded)
            .allowsHitTesting(!t.folded)
            // Folded under a zoomed pane: at its own size, clipped to its
            // bar, so unzooming uncovers it and its terminal never shrinks
            // to a sliver.
            .frame(width: t.rect.width, height: t.rect.height)
            .offset(x: t.rect.minX - t.clip.minX, y: t.rect.minY - t.clip.minY)
            .frame(width: t.clip.width, height: t.clip.height, alignment: .topLeading)
            .clipShape(shape)
            .offset(x: t.clip.minX, y: t.clip.minY)
            // Opened or closed, it grows from or shrinks to no size at its
            // neighbour's edge (OpenCloseMotion); floated or docked, its card
            // covers where it goes or went.
            .transition(PaneMotion.Fade(id: t.id))
    }

    private func bar(_ b: PanePlan.Bar, in area: CGRect) -> some View {
        FoldedPanes(model: model, layout: b.layout, vertical: b.vertical, towardZoomed: b.toward)
            .frame(width: b.rect.width, height: b.rect.height)
            .clipShape(shape(at: b.rect, in: area))
            .offset(x: b.rect.minX, y: b.rect.minY)
            .transition(.opacity)
    }

    /// Whether the pane has company in its tab (split or floating), so
    /// which one's focused is worth showing.
    private func sharesTab(_ id: String) -> Bool {
        guard let tab = model.panes[id].flatMap({ model.tabs[$0.tabID] }) else { return false }
        return AppModel.paneIDs(tab.layout).count > 1 || !tab.floating.isEmpty
    }

}

/// An invisible, wider hit area over the hairline between two children of a
/// split, to drag.
private struct DividerHandle: View {
    let d: PanePlan.Divider
    @Binding var drag: PanePlan.Drag?
    let layout: PaneLayout
    let update: (PaneLayout) -> Void
    @State private var cursorPushed = false

    var body: some View {
        Rectangle()
            .fill(Color.clear)
            .frame(width: d.rect.width, height: d.rect.height)
            .contentShape(Rectangle())
            .onHover { inside in
                if inside, !cursorPushed {
                    (d.horizontal ? NSCursor.resizeLeftRight : NSCursor.resizeUpDown).push()
                    cursorPushed = true
                } else if !inside, cursorPushed {
                    NSCursor.pop()
                    cursorPushed = false
                }
            }
            .onDisappear {
                if cursorPushed { NSCursor.pop(); cursorPushed = false }
            }
            .gesture(
                DragGesture(minimumDistance: 1, coordinateSpace: .global)
                    .onChanged { g in
                        if drag?.path != d.path || drag?.base.count != d.weights.count {
                            drag = PanePlan.Drag(path: d.path, base: d.weights, weights: d.weights)
                        }
                        guard var current = drag, d.index + 1 < current.base.count else { return }
                        let base = current.base, i = d.index
                        let delta = (d.horizontal ? g.translation.width : g.translation.height) / max(d.total, 1)
                        let pair = base[i] + base[i + 1]
                        let minW = min(0.08, pair / 2)
                        current.weights[i] = min(max(base[i] + delta, minW), pair - minW)
                        current.weights[i + 1] = pair - current.weights[i]
                        drag = current
                    }
                    .onEnded { _ in
                        if let drag {
                            var l = layout
                            PanePlan.setWeights(&l, at: drag.path[...], drag.weights)
                            update(l)
                        }
                        drag = nil
                    }
            )
            .offset(x: d.rect.minX, y: d.rect.minY)
    }
}

/// Where a tab's panes go: each pane's place, the bars panes fold to under a
/// zoomed one, and the dividers between a split's children.
///
/// A pane zoomed: the child of each split holding it takes the space, and
/// the others fold to bars where they are (a strip beside it in a
/// side-by-side split, a bar above or below in a stacked one). Folded
/// children are laid out as they were, at their own size, against the far
/// side of their bar (as if the zoomed pane slid over them), and clipped to
/// it. Only the zoomed child resizes.
@MainActor
struct PanePlan {
    struct Tile: Identifiable {
        let id: String
        /// The pane's own place.
        let rect: CGRect
        /// What shows of it: its place, or less, folded under a bar.
        let clip: CGRect
        let folded: Bool
        /// Which way its split runs (nil: alone in its tab).
        var horizontal: Bool? = nil
    }
    struct Bar: Identifiable {
        let id: String
        let layout: PaneLayout
        let rect: CGRect
        let vertical: Bool
        let toward: Edge
    }
    struct Divider: Identifiable {
        var id: String { path.map(String.init).joined(separator: ".") + ":\(index)" }
        /// The split's place in the tree: child indexes from the root.
        let path: [Int]
        /// Between children index and index + 1.
        let index: Int
        let rect: CGRect
        let horizontal: Bool
        /// The split's length along it, less its gaps.
        let total: CGFloat
        let weights: [CGFloat]
    }
    /// The gap between two children of a split.
    struct Line: Identifiable {
        let id: String
        let rect: CGRect
    }
    struct Drag: Equatable {
        let path: [Int]
        let base: [CGFloat]
        var weights: [CGFloat]
    }

    private(set) var tiles: [Tile] = []
    private(set) var bars: [Bar] = []
    private(set) var dividers: [Divider] = []
    private let zoomed: String?
    private let gap: CGFloat
    private let drag: Drag?

    init(_ layout: PaneLayout, in rect: CGRect, zoomed: String?, gap: CGFloat, drag: Drag? = nil) {
        self.zoomed = zoomed
        self.gap = gap
        self.drag = drag
        place(layout, in: rect, clip: nil, path: [])
    }

    private mutating func place(_ layout: PaneLayout, in r: CGRect, clip: CGRect?, path: [Int], horizontal along: Bool? = nil) {
        switch layout.node {
        case .paneID(let id):
            let shown = clip.map { $0.intersection(r) }.map { $0.isNull ? CGRect(origin: r.origin, size: .zero) : $0 }
            tiles.append(Tile(id: id, rect: r, clip: shown ?? r, folded: clip != nil, horizontal: along))
        case .split(let s):
            let n = s.children.count
            guard n > 0 else { return }
            let horizontal = s.direction != .vertical
            let total = (horizontal ? r.width : r.height) - gap * CGFloat(n - 1)
            let w = weights(s, path)
            let zi = zoomed.flatMap { z in s.children.firstIndex { AppModel.paneIDs($0).contains(z) } }
            let barLength: CGFloat = horizontal ? 28 : 24
            var at = horizontal ? r.minX : r.minY
            for (i, child) in s.children.enumerated() {
                let natural = max(w[i] * total, 40)
                let length = zi.map { $0 == i ? total - barLength * CGFloat(n - 1) : barLength } ?? natural
                let slot = horizontal ? CGRect(x: at, y: r.minY, width: length, height: r.height)
                    : CGRect(x: r.minX, y: at, width: r.width, height: length)
                if let zi, zi != i {
                    // At its own size, against the side away from the zoomed child.
                    let before = i < zi
                    var content = slot
                    if horizontal {
                        content.size.width = natural
                        if !before { content.origin.x = slot.maxX - natural }
                    } else {
                        content.size.height = natural
                        if !before { content.origin.y = slot.maxY - natural }
                    }
                    place(child, in: content, clip: clip.map { $0.intersection(slot) } ?? slot, path: path + [i])
                    if clip == nil {
                        let toward: Edge = horizontal ? (before ? .trailing : .leading) : (before ? .bottom : .top)
                        bars.append(Bar(id: "fold." + child.stableKey, layout: child, rect: slot, vertical: horizontal, toward: toward))
                    }
                } else {
                    place(child, in: slot, clip: clip, path: path + [i], horizontal: horizontal)
                }
                if zi == nil, clip == nil, i < n - 1 {
                    // Centred on the gap after child i.
                    let hit = max(8, gap + 4)
                    let mid = at + length + gap / 2
                    let rect = horizontal ? CGRect(x: mid - hit / 2, y: r.minY, width: hit, height: r.height)
                        : CGRect(x: r.minX, y: mid - hit / 2, width: r.width, height: hit)
                    dividers.append(Divider(path: path, index: i, rect: rect, horizontal: horizontal, total: total, weights: w))
                }
                at += length + gap
            }
        case nil:
            break
        }
    }

    private func weights(_ s: Hammock_V1_Split, _ path: [Int]) -> [CGFloat] {
        let n = s.children.count
        if let drag, drag.path == path, drag.weights.count == n { return drag.weights }
        return s.weights.count == n ? s.weights.map(CGFloat.init) : Array(repeating: 1 / CGFloat(n), count: n)
    }

    /// The hairlines between panes (compact): each pane's and bar's along
    /// its leading and top edges, wherever another is beside it, so each
    /// moves with its own pane. A leading one reaches up over the gap above
    /// too, where it meets a top one.
    static func lines(tiles: [Tile], bars: [Bar], in area: CGRect, gap: CGFloat) -> [Line] {
        let shown = tiles.filter { !$0.folded }.map { ($0.id, $0.clip) } + bars.map { ($0.id, $0.rect) }
        var lines: [Line] = []
        for (id, r) in shown {
            let top = r.minY > area.minY + 0.5, leading = r.minX > area.minX + 0.5
            if leading {
                lines.append(Line(id: "l." + id, rect: CGRect(x: r.minX - gap, y: r.minY - (top ? gap : 0), width: gap, height: r.height + (top ? gap : 0))))
            }
            if top {
                lines.append(Line(id: "t." + id, rect: CGRect(x: r.minX, y: r.minY - gap, width: r.width, height: gap)))
            }
        }
        return lines
    }

    /// Sets the weights of the split at path.
    static func setWeights(_ layout: inout PaneLayout, at path: ArraySlice<Int>, _ weights: [CGFloat]) {
        guard case .split(var s) = layout.node else { return }
        if let i = path.first {
            guard s.children.indices.contains(i) else { return }
            setWeights(&s.children[i], at: path.dropFirst(), weights)
        } else {
            guard weights.count == s.children.count else { return }
            s.weights = weights.map(Float.init)
        }
        layout.split = s
    }
}

private extension View {
    /// A pane showing the agent view or log console has no terminal to take
    /// the keyboard, which is what tells the daemon a pane is focused (and
    /// so acknowledges a finished agent). Tell it here instead: when the pane
    /// becomes its tab's focused one, or appears as it (the tab selected).
    func focusesWhenTarget(_ isFocused: Bool, paneID: String, model: AppModel) -> some View {
        onChange(of: isFocused, initial: true) { _, focused in
            if focused { model.focus(paneID) }
        }
    }
}

struct PaneView: View {
    let model: AppModel
    let paneID: String
    let isFocused: Bool
    @Environment(\.colorScheme) private var colorScheme

    /// Closed and fading out of its place: as it was (PaneMotion.openClose).
    private var pane: Pane? { model.panes[paneID] ?? PaneMotion.shared.departing[paneID] }
    private var agentCapable: Bool { pane.map(model.supportsAgentView) ?? false }
    private var showsAgent: Bool { agentCapable && AgentModes.shared.showsAgent(paneID) }
    private var isLog: Bool { pane?.isLog == true }
    private var needsInput: Bool { pane?.hasAgent == true && pane?.agent.state == .needsInput }
    private var asleep: Bool { pane?.asleep == true }

    /// The terminal theme's background, so the header and gaps match the terminal.
    private var themeBackground: Color { PaneLook.background(dark: colorScheme == .dark) }

    var body: some View {
        Group {
            if pane?.kind == .browser {
                BrowserPaneView(model: model, paneID: paneID, isFocused: isFocused)
            } else if pane?.kind == .simulator {
                SimulatorPaneView(model: model, paneID: paneID, isFocused: isFocused)
            } else if pane?.kind == .editor {
                EditorPaneView(model: model, paneID: paneID, isFocused: isFocused)
            } else if pane?.kind == .diff {
                DiffPaneView(model: model, paneID: paneID, isFocused: isFocused)
            } else {
                VStack(spacing: 0) {
                    header
                        .contextMenu { PaneMenuItems(model: model, paneID: paneID) }
                        .paneSwapHandle(paneID, model: model)
                    Group {
                        if isLog, !LogUI.shared.showsRaw(paneID) {
                            LogConsoleView(model: model, paneID: paneID)
                                .background(Color(nsColor: .textBackgroundColor))
                                .simultaneousGesture(TapGesture().onEnded { model.focus(paneID) })
                                .focusesWhenTarget(isFocused, paneID: paneID, model: model)
                        } else if showsAgent {
                            AgentPaneView(model: model, paneID: paneID)
                                .simultaneousGesture(TapGesture().onEnded { model.focus(paneID) })
                                .focusesWhenTarget(isFocused, paneID: paneID, model: model)
                        } else {
                            TerminalView(paneID: paneID, model: model, isFocused: isFocused)
                        }
                    }
                    // Its agent asleep: faded, with a button to wake it
                    // (focusing it doesn't). Under the header, which still
                    // drags, zooms and has its menus.
                    .overlay {
                        if asleep {
                            SleepingPaneOverlay(backdrop: themeBackground, fill: AgentAutomatic.bubble, tint: AgentAutomatic.color,
                                                animated: PaneMotion.enabled) { model.sleepPane(paneID, wake: true) }
                                .transition(.opacity)
                        }
                    }
                    .animation(PaneMotion.when(.easeInOut(duration: 0.25)), value: asleep)
                }
            }
        }
        .background(themeBackground)
        .paneSwapTarget(paneID)
        .overlay {
            if needsInput {
                PaneLook.shape.strokeBorder(Color.orange.opacity(0.6), lineWidth: 2)
            }
        }
    }

    private var header: some View {
        HStack(spacing: 8) {
            // What runs here: an agent's logo, else the program's icon.
            if let pane, pane.hasAgent, !pane.exited, pane.agent.state != .exited {
                ProgramIcon(agent: pane.agent.harness, size: 14)
            } else if let pane {
                ProgramIcon(program: pane.exited ? [] : pane.program, size: 14)
            }
            PaneKindMenu(model: model, paneID: paneID) {
                Text(pane?.displayName ?? "").fontWeight(.semibold).lineLimit(1)
            }
            .layoutPriority(1) // the folder gives way first
            Text(pane?.shortCwd ?? "")
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.head)
            Spacer(minLength: 6)
            let here = model.presence(inPane: paneID)
            if let typist = here.first(where: { $0.0.typing }) {
                Text("\(typist.1.name.split(separator: " ").first.map(String.init) ?? typist.1.name) is typing…")
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            AvatarStack(here, size: 18)
            if model.group(ofPane: paneID)?.canDrive == false {
                Text("View only")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .background(.quaternary, in: Capsule())
                    .help("You can watch this Group; its host hasn't let you type")
            }
            // The pane's state comes before the buttons, so they stay put
            // as it comes and goes.
            if let pane, pane.exited {
                Text("Exited \(pane.exitStatus)")
                    .foregroundStyle(.secondary)
            } else if let pane, pane.asleep {
                SleepBadge(model: model, paneID: paneID)
            } else if let pane, pane.hasAgent, pane.agent.state != .unspecified, !(pane.agent.shownState == .idle && pane.agent.seen) {
                HStack(spacing: 5) {
                    StatusIcon(state: pane.agent.shownState, size: 12, progress: pane.agentProgress)
                    Text(pane.agent.runningCommand ? "Running a command" : pane.agent.state.label + (pane.agentProgress.map { " \($0)%" } ?? ""))
                }
                .foregroundStyle(pane.agent.state == .needsInput ? Color.orange : .secondary)
                .fontWeight(pane.agent.state == .needsInput ? .semibold : .regular)
            }
            if agentCapable {
                AgentViewToggle(paneID: paneID)
            } else if isLog {
                LogViewToggle(paneID: paneID)
            }
            ZoomButton(model: model, paneID: paneID)
            PaneCloseButton(model: model, paneID: paneID)
        }
        .font(.callout)
        // The header's icon lines up with the window title above.
        .padding(.leading, 15)
        .padding(.trailing, 12)
        .frame(height: 28)
        .background(isFocused ? Color.themeAccent.opacity(0.06) : Color.clear)
    }
}

/// A pane header's mark for an agent asleep (its process ended to free
/// memory): a moon and "Asleep". Clicking it wakes the agent, as the
/// pane's Wake Agent button does (SleepingPaneOverlay).
private struct SleepBadge: View {
    let model: AppModel
    let paneID: String
    @State private var hover = false

    var body: some View {
        Button { model.sleepPane(paneID, wake: true) } label: {
            HStack(spacing: 5) {
                SleepMark(size: 12)
                Text("Asleep")
            }
            .foregroundStyle(hover ? .primary : .secondary)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hover = $0 }
        .tooltip("Asleep, to free its memory: click to wake it")
        .accessibilityLabel("Asleep")
    }
}
