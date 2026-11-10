import HammockKit
import SwiftUI

struct TabDetailView: View {
    let model: AppModel
    let tab: WorkspaceTab

    var body: some View {
        VStack(spacing: 0) {
            LayoutView(model: model, layout: tab.layout, focusedPaneID: currentFocus, zoomed: PaneZoom.shared.zoomed(in: tab)) { newLayout in
                model.setLayout(tab.id, newLayout)
            }
            // Spacious spacing: the panes stand off the tab's edges too.
            .padding(PaneLook.inset)
            // A pane floating: the splits close up around it as its card grows out.
            .animation(FloatMotion.shared.animatesLayout ? .snappy(duration: FloatMotion.duration) : nil, value: tab.layout)
            // Floating panes over the splits, in the same space.
            .overlay { FloatLayer(model: model, tab: tab, focusedPaneID: currentFocus) }
            .overlay { PaneSwapChip(model: model) }
            .coordinateSpace(paneSpace)
            FloatShelf(model: model, tab: tab)
        }
        .coordinateSpace(tabSpace)
        .animation(.snappy(duration: 0.2), value: tab.floating.filter(\.minimised).count)
        // Moving focus to another pane (⌘]) zooms that one instead.
        .onChange(of: model.focusedPaneID) { _, id in if let id { PaneZoom.shared.follow(id, in: tab) } }
            // Compact: the hairlines between panes. Spacious: the panes sit
            // on the tab's background (HammockApp's detail: an empty Group's).
            .background(PaneLook.spacious ? Color.clear : Color(nsColor: .separatorColor))
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

/// Renders a tab's split tree. `update` receives an edited copy of this
/// subtree (e.g. new weights after a divider drag).
struct LayoutView: View {
    let model: AppModel
    let layout: PaneLayout
    let focusedPaneID: String
    /// The tab's zoomed pane, if any (see SplitView).
    var zoomed: String? = nil
    let update: (PaneLayout) -> Void

    var body: some View {
        switch layout.node {
        case .paneID(let id):
            PaneView(model: model, paneID: id, isFocused: id == focusedPaneID)
                .clipShape(PaneLook.shape)
                .modifier(PaneFocusLook(isFocused: id == focusedPaneID, among: sharesTab(id)))
                .id(id) // a view per pane, never reused for another
                // Floated or docked, its card covers where it goes or went:
                // no fade.
                .transition(.identity)
        case .split(let s):
            SplitView(model: model, split: s, focusedPaneID: focusedPaneID, zoomed: zoomed) { newSplit in
                var l = layout
                l.split = newSplit
                update(l)
            }
        case nil:
            Color.clear
        }
    }

    /// Whether the pane has company in its tab (split or floating), so
    /// which one's focused is worth showing.
    private func sharesTab(_ id: String) -> Bool {
        guard let tab = model.panes[id].flatMap({ model.tabs[$0.tabID] }) else { return false }
        return AppModel.paneIDs(tab.layout).count > 1 || !tab.floating.isEmpty
    }
}

struct SplitView: View {
    let model: AppModel
    let split: Hammock_V1_Split
    let focusedPaneID: String
    var zoomed: String? = nil
    let update: (Hammock_V1_Split) -> Void
    @State private var dragBase: [CGFloat]?   // weights when the drag began
    @State private var dragWeights: [CGFloat]? // live weights while dragging
    @State private var cursorPushed = false

    private var weights: [CGFloat] {
        if let dragWeights, dragWeights.count == split.children.count { return dragWeights }
        let n = split.children.count
        return split.weights.count == n ? split.weights.map(CGFloat.init) : Array(repeating: 1 / CGFloat(n), count: n)
    }

    var body: some View {
        GeometryReader { geo in
            let horizontal = split.direction != .vertical
            let n = split.children.count
            let gap = PaneLook.gap
            let total = (horizontal ? geo.size.width : geo.size.height) - gap * CGFloat(max(n - 1, 0))
            let w = weights
            // A pane zoomed: the child holding it takes the space, and the
            // others fold to bars where they are (a strip beside it in a
            // side-by-side split, a bar above or below in a stacked one).
            // Folded children stay as they were, at their own size, clipped
            // under their bar: unzooming (or zooming another) uncovers them
            // rather than building them again, and their terminals never
            // shrink to a sliver. Only the zoomed child resizes.
            let zi = zoomed.flatMap { z in split.children.firstIndex { AppModel.paneIDs($0).contains(z) } }
            let bar: CGFloat = horizontal ? 28 : 24
            let stack = horizontal ? AnyLayout(HStackLayout(spacing: gap)) : AnyLayout(VStackLayout(spacing: gap))
            stack {
                ForEach(Array(split.children.enumerated()), id: \.element.stableKey) { i, child in
                    let natural = max(w[i] * total, 40)
                    let length = zi.map { $0 == i ? total - bar * CGFloat(n - 1) : bar } ?? natural
                    let folded = zi != nil && zi != i
                    // A folded child sits against the far side of its bar,
                    // as if the zoomed pane slid over it.
                    let toward: Edge = horizontal ? (i < (zi ?? 0) ? .trailing : .leading) : (i < (zi ?? 0) ? .bottom : .top)
                    let pin: Alignment = switch toward {
                    case .trailing: .leading
                    case .leading: .trailing
                    case .bottom: .top
                    case .top: .bottom
                    }
                    let content = folded ? natural : length
                    AnyView(LayoutView(model: model, layout: child, focusedPaneID: focusedPaneID, zoomed: zoomed) { newChild in
                        var s = split
                        s.children[i] = newChild
                        update(s)
                    })
                    .environment(\.paneFolded, folded)
                    .allowsHitTesting(!folded)
                    .frame(width: horizontal ? content : nil, height: horizontal ? nil : content)
                    .frame(width: horizontal ? length : nil, height: horizontal ? nil : length, alignment: folded ? pin : .center)
                    .overlay {
                        if folded {
                            FoldedPanes(model: model, layout: child, vertical: horizontal, towardZoomed: toward)
                                .transition(.opacity)
                        }
                    }
                    .clipShape(PaneLook.shape)
                    .overlay(alignment: horizontal ? .trailing : .bottom) {
                        if zi == nil, i < n - 1 {
                            divider(index: i, horizontal: horizontal, total: total)
                        }
                    }
                }
            }
        }
    }

    /// An invisible, wider hit area over the hairline between children i and i+1.
    private func divider(index i: Int, horizontal: Bool, total: CGFloat) -> some View {
        // Centred on the gap after child i.
        let hit = max(8, PaneLook.gap + 4)
        let shift = hit / 2 + PaneLook.gap / 2
        return Rectangle()
            .fill(Color.clear)
            .frame(width: horizontal ? hit : nil, height: horizontal ? nil : hit)
            .offset(x: horizontal ? shift : 0, y: horizontal ? 0 : shift)
            .contentShape(Rectangle())
            .onHover { inside in
                if inside, !cursorPushed {
                    (horizontal ? NSCursor.resizeLeftRight : NSCursor.resizeUpDown).push()
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
                        if dragBase?.count != split.children.count { dragBase = weights }
                        let base = dragBase ?? weights
                        guard i + 1 < base.count else { return }
                        var w = base
                        let delta = (horizontal ? g.translation.width : g.translation.height) / max(total, 1)
                        let pair = base[i] + base[i + 1]
                        let minW = min(0.08, pair / 2)
                        w[i] = min(max(base[i] + delta, minW), pair - minW)
                        w[i + 1] = pair - w[i]
                        dragWeights = w
                    }
                    .onEnded { _ in
                        if let w = dragWeights {
                            var s = split
                            s.weights = w.map(Float.init)
                            update(s)
                        }
                        dragWeights = nil
                        dragBase = nil
                    }
            )
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

    private var pane: Pane? { model.panes[paneID] }
    private var agentCapable: Bool { pane.map(model.supportsAgentView) ?? false }
    private var showsAgent: Bool { agentCapable && AgentModes.shared.showsAgent(paneID) }
    private var isLog: Bool { pane?.isLog == true }
    private var needsInput: Bool { pane?.hasAgent == true && pane?.agent.state == .needsInput }

    /// The terminal theme's background, so the header and gaps match the terminal.
    private var themeBackground: Color {
        let c = AppConfig.shared.theme(isDark: colorScheme == .dark).background
        return Color(red: Double(c.r) / 255, green: Double(c.g) / 255, blue: Double(c.b) / 255)
    }

    var body: some View {
        Group {
            if pane?.kind == .browser {
                BrowserPaneView(model: model, paneID: paneID, isFocused: isFocused)
            } else if pane?.kind == .simulator {
                SimulatorPaneView(model: model, paneID: paneID, isFocused: isFocused)
            } else if pane?.kind == .editor {
                EditorPaneView(model: model, paneID: paneID, isFocused: isFocused)
            } else {
                VStack(spacing: 0) {
                    header
                        .contextMenu { PaneMenuItems(model: model, paneID: paneID) }
                        .paneSwapHandle(paneID, model: model)
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
                Text(pane?.displayName ?? "").fontWeight(.semibold)
            }
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
            if agentCapable {
                AgentViewToggle(paneID: paneID)
            } else if isLog {
                LogViewToggle(paneID: paneID)
            }
            ZoomButton(model: model, paneID: paneID)
            if let pane, pane.exited {
                Text("Exited \(pane.exitStatus)")
                    .foregroundStyle(.secondary)
            } else if let pane, pane.hasAgent, pane.agent.state != .unspecified, !(pane.agent.shownState == .idle && pane.agent.seen) {
                HStack(spacing: 5) {
                    StatusIcon(state: pane.agent.shownState, size: 12)
                    Text(pane.agent.runningCommand ? "Running a command" : pane.agent.state.label)
                }
                .foregroundStyle(pane.agent.state == .needsInput ? Color.orange : .secondary)
                .fontWeight(pane.agent.state == .needsInput ? .semibold : .regular)
            }
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
