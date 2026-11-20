import HammockKit
import Network
import SwiftUI
import UIKit

// A tab of several panes: one pane at a time (TabScreen), a strip of
// pills along the bottom to switch between them, and Exposé, the tab laid
// out as on the Mac with each pane live, to pick one from.

extension EnvironmentValues {
    /// Shown as one of a tab's panes, with the pane strip under it: a
    /// screen keeps its own controls inside it (not in the system's bottom
    /// bar, which would go under the strip).
    @Entry var inPaneStrip = false
    /// The pane on screen, not the neighbour a swipe is bringing in: only
    /// it shows its title and toolbar.
    @Entry var paneChrome = true
    /// The tab's actions (Rename, New Tab…), for a screen that has its own
    /// … menu (the editor's, the simulator's): it puts them in that one,
    /// last, rather than the pane showing two.
    @Entry var paneTabMenu: AnyView? = nil
}

/// Where a pane's own content takes drags (a simulator's screen, a
/// browser's page), in global coordinates: a swipe starting there is
/// theirs, not a switch to the next pane.
struct PaneSwipeExclusion: PreferenceKey {
    static let defaultValue: [CGRect] = []
    static func reduce(value: inout [CGRect], nextValue: () -> [CGRect]) { value += nextValue() }
}

/// A pane with nothing to show yet (a simulator's or a browser's first
/// picture still coming): a tap on its pill waits a moment for it, so it
/// slides in showing something rather than filling in where it lands.
struct PaneLoading: PreferenceKey {
    static let defaultValue = false
    static func reduce(value: inout Bool, nextValue: () -> Bool) { value = value || nextValue() }
}

extension View {
    /// Marks this view's area as taking its own drags (PaneSwipeExclusion).
    func takesOwnDrags() -> some View {
        background {
            GeometryReader { geo in
                Color.clear.preference(key: PaneSwipeExclusion.self, value: [geo.frame(in: .global)])
            }
        }
    }
}

/// A screen's controls inside it, above the pane strip: the system's
/// bottom bar's buttons as a row of glass ones.
struct InlinePaneControls<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        HStack(spacing: 18) { content }
            .labelStyle(.iconOnly)
            .buttonStyle(.borderless)
            .font(.system(size: 18, weight: .medium))
            .controlSize(.large)
            .padding(.horizontal, 10)
            .frame(height: 48)
            .glassEffect(.regular.interactive(), in: Capsule())
            .padding(.bottom, 8)
    }
}

/// The order the strip and swiping go through a tab's panes: its splits'
/// in reading order, then its floats, front first.
@MainActor
func stripPaneIDs(_ tab: WorkspaceTab) -> [String] {
    AppModel.paneIDs(tab.layout) + tab.floating.reversed().map(\.paneID)
}

/// The pane each tab last showed on this phone, to open it there again.
enum TabPanes {
    private static let key = "tabLastPane"

    static func last(_ tabID: String) -> String? {
        UserDefaults.standard.dictionary(forKey: key)?[tabID] as? String
    }

    static func remember(_ paneID: String, in tabID: String) {
        var d = UserDefaults.standard.dictionary(forKey: key) ?? [:]
        guard d[tabID] as? String != paneID else { return }
        d[tabID] = paneID
        // Closed tabs' entries go once there are plenty.
        if d.count > 200 { d = [tabID: paneID] }
        UserDefaults.standard.set(d, forKey: key)
    }
}

/// What a pane is called in the strip and Exposé.
@MainActor
func stripName(_ p: Pane?) -> String {
    guard let p else { return "" }
    switch p.kind {
    case .browser: return p.title.isEmpty ? "Browser" : p.title
    case .simulator: return p.title.isEmpty ? "Simulator" : p.title
    case .editor: return p.editor.name.isEmpty ? p.displayName : p.editor.name
    case .diff: return p.diff.title.isEmpty ? "Diff" : p.diff.title
    default:
        // An agent by its name ("Claude", not the process's "claude"):
        // one Hammock doesn't know keeps its own.
        if p.hasAgent, !p.agent.harness.isEmpty { return AgentCatalog.shortName(of: p.agent.harness) }
        return p.displayName
    }
}

/// A pane's icon: its kind's, an agent's mark, or the program running.
struct PaneIcon: View {
    let pane: Pane?
    var size: CGFloat = 16

    var body: some View {
        switch pane?.kind {
        case .browser: symbol("globe")
        case .simulator: symbol(pane?.devicePlatform == "tvOS" ? "appletv" : "iphone")
        case .editor: symbol("doc.text")
        case .diff: symbol("plusminus")
        default:
            if let p = pane, p.isLog {
                symbol("list.bullet.rectangle")
            } else if let p = pane, p.hasAgent, !p.agent.harness.isEmpty {
                ProgramIcon(agent: p.agent.harness, size: size)
            } else {
                ProgramIcon(program: pane?.exited == false ? pane?.program ?? [] : [], size: size)
            }
        }
    }

    private func symbol(_ name: String) -> some View {
        Image(systemName: name)
            .font(.system(size: size * 0.85, weight: .medium))
            .frame(width: size, height: size)
    }
}

/// A pane's name and its tab's under it, as the bar's title: TabScreen
/// draws one per pane sliding, faded between them.
struct StripTitle: View {
    let model: AppModel
    let paneID: String

    var body: some View {
        let pane = model.panes[paneID]
        VStack(spacing: 1) {
            Text(stripName(pane)).font(.headline).lineLimit(1)
            if let tab = pane.flatMap({ model.tab(of: $0) }) {
                Text(model.title(of: tab)).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
    }
}

// MARK: The strip

/// The tab's panes as pills, the current one marked, after a button that
/// opens Exposé (its icon the tab's layout). Floats come after a divider.
struct PaneStrip: View {
    let model: AppModel
    let tab: WorkspaceTab
    /// The pane on screen, or the one a tap is going to: the strip scrolls
    /// to it, and Exposé's icon marks it.
    let current: String
    /// How selected each pill shows (PanePill.selected).
    let selected: (String) -> Double
    let namespace: Namespace.ID
    let select: (String) -> Void
    let expose: () -> Void
    let confirmClose: (Pane) -> Void

    var body: some View {
        let tiled = AppModel.paneIDs(tab.layout)
        let floats = Array(tab.floating.reversed())
        HStack(spacing: 8) {
            Button(action: expose) {
                LayoutGlyph(layout: tab.layout, current: current)
                    .frame(width: 20, height: 20)
                    .frame(width: 44, height: 44)
                    .background(Circle().fill(Color(.tertiarySystemFill)))
                    .contentShape(Circle())
            }
            .buttonStyle(.plain)
            .matchedTransitionSource(id: ExposeScreen.transitionID, in: namespace)
            .accessibilityLabel("Show All Panes")
            Divider().frame(height: 26)
            ScrollViewReader { proxy in
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        ForEach(tiled, id: \.self) { id in pill(id) }
                        if !floats.isEmpty {
                            Divider().frame(height: 26)
                            ForEach(floats, id: \.paneID) { f in pill(f.paneID, floating: f) }
                        }
                    }
                    .padding(.vertical, 2)
                }
                .onAppear { proxy.scrollTo(current, anchor: .center) }
                .onChange(of: current) { _, id in withAnimation { proxy.scrollTo(id, anchor: .center) } }
            }
        }
        .padding(.leading, 12)
        .padding(.top, 8)
        .padding(.bottom, 4)
        .background(.bar)
        .overlay(alignment: .top) { Divider() }
    }

    private func pill(_ id: String, floating: FloatingPane? = nil) -> some View {
        Button { select(id) } label: {
            PanePill(model: model, paneID: id, selected: selected(id), floating: floating != nil, minimised: floating?.minimised == true)
        }
        .buttonStyle(.plain)
        .id(id)
        .contentShape(.contextMenuPreview, Capsule())
        .contextMenu { PaneMenu(model: model, paneID: id, floating: floating, confirmClose: confirmClose) }
    }
}

/// One pane in the strip: its icon and name, and what it needs you to know
/// (an agent at work or waiting on you, finished and unseen, how far along).
struct PanePill: View {
    let model: AppModel
    let paneID: String
    /// How selected it shows, 0 to 1: the current pane 1, and during a
    /// swipe the mark fading from one pill to the next.
    let selected: Double
    var floating = false
    var minimised = false
    @Environment(\.phoneTheme) private var theme

    private var pane: Pane? { model.panes[paneID] }

    private var waiting: Bool { pane?.hasAgent == true && pane?.agent.shownState == .needsInput }

    var body: some View {
        // The plain pill fading into the selected one (never both whole:
        // their texts, regular and semibold, would show through each other).
        ZStack {
            face(current: false).opacity(1 - selected)
            face(current: true).opacity(selected)
        }
        .opacity(minimised && selected < 0.5 ? 0.6 : 1)
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected > 0.5 ? .isSelected : [])
        .accessibilityHint(minimised ? "Minimised on the Mac" : "")
    }

    private func face(current: Bool) -> some View {
        let p = pane
        return HStack(spacing: 6) {
            PaneIcon(pane: p, size: 16)
                .opacity(current ? 1 : 0.7)
            Text(stripName(p))
                .font(.subheadline.weight(current ? .semibold : .regular))
                .lineLimit(1)
                .frame(maxWidth: 140)
                .fixedSize(horizontal: true, vertical: false)
            status(p)
        }
        .padding(.horizontal, 14)
        .frame(height: 40)
        .foregroundStyle(waiting ? Color.orange : current ? theme.accent : Color.secondary)
        .background(Capsule().fill(fill(current)))
        .overlay(alignment: .bottom) { progressBar(p) }
        .clipShape(Capsule())
        .overlay(border(current))
    }

    private func fill(_ current: Bool) -> Color {
        if waiting { return Color.orange.opacity(current ? 0.22 : 0.12) }
        if current { return theme.accent.opacity(0.16) }
        return Color(.secondarySystemGroupedBackground)
    }

    @ViewBuilder private func border(_ current: Bool) -> some View {
        let color: Color = waiting ? .orange : current ? theme.accent : Color(.separator)
        if floating {
            Capsule().strokeBorder(color.opacity(current || waiting ? 1 : 0.8), style: StrokeStyle(lineWidth: 1, dash: [3, 3]))
        } else {
            Capsule().strokeBorder(color, lineWidth: current || waiting ? 1.5 : 0.5)
        }
    }

    /// An agent busy or waiting: its state; finished and not looked at: a dot.
    @ViewBuilder private func status(_ p: Pane?) -> some View {
        if let p, p.hasAgent, !p.exited {
            let s = p.agent.shownState
            if [.working, .needsInput, .starting, .errored].contains(s) {
                StatusIcon(state: s, size: 12, asleep: p.asleep)
            } else if s == .idle, !p.agent.seen {
                Circle().fill(theme.accent).frame(width: 7, height: 7)
                    .accessibilityLabel("Finished")
            }
        }
    }

    /// A program that says how far along it is: a bar along the bottom.
    @ViewBuilder private func progressBar(_ p: Pane?) -> some View {
        if let progress = p?.agentProgress {
            GeometryReader { geo in
                Rectangle()
                    .fill(waiting ? Color.orange : theme.accent)
                    .frame(width: geo.size.width * CGFloat(min(max(progress, 0), 100)) / 100, height: 3)
                    .frame(maxHeight: .infinity, alignment: .bottom)
            }
            .accessibilityLabel("\(progress)%")
        }
    }
}

/// The tab's split layout in miniature (Exposé's button): a box per pane,
/// the current one filled.
struct LayoutGlyph: View {
    let layout: PaneLayout
    let current: String

    var body: some View {
        Canvas { ctx, size in
            let line: CGFloat = 1.5
            for (id, r) in layout.rects(in: CGRect(origin: .zero, size: size).insetBy(dx: line / 2, dy: line / 2), gap: 3) {
                let path = Path(roundedRect: r, cornerRadius: min(3, r.width / 3, r.height / 3))
                if id == current { ctx.fill(path, with: .color(.primary)) }
                ctx.stroke(path, with: .color(.primary), lineWidth: line)
            }
        }
        .accessibilityHidden(true)
    }
}

// MARK: Pane actions

/// A pane's touch-and-hold menu (its pill, its Exposé tile): its agent's
/// actions, a float's place on the Mac, moving and closing it.
struct PaneMenu: View {
    let model: AppModel
    let paneID: String
    var floating: FloatingPane?
    let confirmClose: (Pane) -> Void

    var body: some View {
        let pane = model.panes[paneID]
        let drive = model.group(ofPane: paneID)?.canDrive != false
        if let pane, pane.hasAgent, !pane.exited {
            Section {
                if pane.agent.state == .working || pane.agent.state == .needsInput {
                    Button("Interrupt", systemImage: "stop.circle") { model.send(paneID, Data([0x1B])) }
                }
                if AgentCatalog.canSwitch(pane.agent.harness) {
                    ForEach(AgentCatalog.switchable, id: \.id) { k in
                        Button("Continue in \(AgentCatalog.shortName(of: k.id))", systemImage: "arrow.triangle.swap") { model.switchAgent(paneID, to: k.id) }
                            .disabled(pane.agent.harness == k.id)
                    }
                }
                if pane.asleep {
                    Button("Wake Agent", systemImage: "sun.max") { model.sleepPane(paneID, wake: true) }
                } else if pane.canSleep {
                    Button("Put Agent to Sleep", systemImage: "moon.zzz") { model.sleepPane(paneID) }
                }
            }
            .disabled(!drive)
            if let url = pane.sessionURL {
                Button("Copy Session Link", systemImage: "link") { UIPasteboard.general.url = url }
            }
        }
        // A terminal this phone has on screen (or kept): its text as it shows.
        if model.hasScreen(paneID) {
            Button("Copy Screen", systemImage: "doc.on.doc") { if let text = model.screenText(paneID) { UIPasteboard.general.string = text } }
        }
        Section {
            if let f = floating {
                if f.minimised {
                    Button("Restore on the Mac", systemImage: "macwindow") { model.arrange(paneID, .restore) }
                } else {
                    Button("Minimise on the Mac", systemImage: "minus.rectangle") { model.arrange(paneID, .minimise) }
                }
                Button("Dock in the Splits", systemImage: "rectangle.split.2x1") { model.arrange(paneID, .dock) }
            } else {
                Button("Move to New Tab", systemImage: "rectangle.badge.plus") { Task { await model.breakOut(paneID) } }
            }
            Button("Close Pane", systemImage: "xmark", role: .destructive) {
                if let pane, closeWarning(pane) != nil { confirmClose(pane) } else { Task { await model.closePane(paneID) } }
            }
        }
        .disabled(!drive)
    }
}

/// Why closing a pane needs a yes, if it does: an agent at work (unless
/// turned off), or programs other than an agent at rest still running.
@MainActor
func closeWarning(_ p: Pane) -> String? { closeWarning([p]) }

/// For several panes (a tab's): the same, of them all.
@MainActor
func closeWarning(_ panes: [Pane]) -> String? {
    let warnBusy = UserDefaults.standard.object(forKey: "warnCloseBusyAgent") as? Bool ?? true
    let busy = warnBusy ? panes.filter { p in
        p.hasAgent && !p.exited && ([.working, .needsInput, .starting].contains(p.agent.shownState) || p.agent.subagents > 0)
    } : []
    if busy.count > 1 { return "\(busy.count) agents are still at work. Closing stops them." }
    if let p = busy.first {
        let a = p.agent
        let doing = a.shownState == .needsInput ? "is waiting for you" : a.runningCommand ? "is running a command"
            : a.shownState == .idle ? "has \(a.subagents == 1 ? "a subagent" : "\(a.subagents) subagents") still running" : "is still working"
        return "\(p.displayName.capitalized) \(doing). Closing stops it."
    }
    var names: [String] = []
    for p in panes {
        for n in p.processes where !(p.hasAgent && n.lowercased() == p.agent.harness.lowercased()) && !names.contains(n) { names.append(n) }
    }
    guard !names.isEmpty else { return nil }
    return ListFormatter.localizedString(byJoining: names) + (names.count == 1 ? " is" : " are") + " still running in it and will be stopped."
}

// MARK: Exposé

/// The tab laid out as on the Mac, each pane live (only while this is
/// open): tap one to go to it. Its floats underneath, front first.
struct ExposeScreen: View {
    static let transitionID = "expose"

    let model: AppModel
    let tabID: String
    let current: String
    let open: (String) -> Void
    let confirmClose: (Pane) -> Void
    @Environment(\.dismiss) private var dismiss
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        let tab = model.tabs[tabID]
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(tab.map { model.title(of: $0) } ?? "").font(.title2.weight(.bold)).lineLimit(1)
                    if let tab { Text(summary(tab)).font(.subheadline).foregroundStyle(.secondary) }
                }
                Spacer()
                Button("Done") { dismiss() }
                    .buttonStyle(.bordered)
                    .buttonBorderShape(.capsule)
            }
            if let tab {
                ExposeLayout(model: model, layout: tab.layout, current: current, pick: pick, confirmClose: confirmClose)
                    .frame(maxHeight: .infinity)
                if !tab.floating.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Floating").font(.footnote.weight(.semibold)).foregroundStyle(.secondary)
                        ScrollView(.horizontal, showsIndicators: false) {
                            HStack(spacing: 10) {
                                ForEach(Array(tab.floating.reversed()), id: \.paneID) { f in
                                    tile(f.paneID, floating: f)
                                        .frame(width: 170, height: 110)
                                }
                            }
                        }
                    }
                }
            }
        }
        .padding(.horizontal, 12)
        .padding(.top, 8)
        .padding(.bottom, 4)
        // Over the tab, an overlay rather than another screen: the theme's
        // colour on glass, as the Mac's sidebar is (its tint at the Mac's
        // strength for a glassy sidebar; Native, the glass alone).
        .presentationBackground {
            ZStack {
                Rectangle().fill(.ultraThinMaterial)
                if let tint = theme.sidebar { tint.opacity(0.55) }
            }
        }
        // The tab closed meanwhile (here or on the Mac).
        .onChange(of: tab == nil) { _, gone in if gone { dismiss() } }
    }

    private func summary(_ tab: WorkspaceTab) -> String {
        let n = AppModel.paneIDs(tab.layout).count
        let panes = n == 1 ? "1 pane" : "\(n) panes"
        return tab.floating.isEmpty ? panes : "\(panes) · \(tab.floating.count) floating"
    }

    private func pick(_ id: String) {
        open(id)
        dismiss()
    }

    private func tile(_ id: String, floating: FloatingPane?) -> some View {
        LiveTile(model: model, paneID: id, current: id == current, minimised: floating?.minimised == true)
            .contentShape(.contextMenuPreview, RoundedRectangle(cornerRadius: 14))
            .contentShape(RoundedRectangle(cornerRadius: 14))
            .onTapGesture { pick(id) }
            .contextMenu { PaneMenu(model: model, paneID: id, floating: floating, confirmClose: confirmClose) }
    }
}

/// Exposé's splits: the Mac's layout, a live tile per pane.
private struct ExposeLayout: View {
    let model: AppModel
    let layout: PaneLayout
    let current: String
    let pick: (String) -> Void
    let confirmClose: (Pane) -> Void

    var body: some View {
        switch layout.node {
        case .paneID(let id):
            LiveTile(model: model, paneID: id, current: id == current)
                .contentShape(.contextMenuPreview, RoundedRectangle(cornerRadius: 14))
                .contentShape(RoundedRectangle(cornerRadius: 14))
                .onTapGesture { pick(id) }
                .contextMenu { PaneMenu(model: model, paneID: id, confirmClose: confirmClose) }
                .accessibilityAddTraits(.isButton)
        case .split(let s):
            let side = s.direction != .vertical
            let n = s.children.count
            let w = s.weights.count == n ? s.weights.map(CGFloat.init) : Array(repeating: 1 / CGFloat(n), count: n)
            GeometryReader { geo in
                let gap: CGFloat = 8
                let total = (side ? geo.size.width : geo.size.height) - gap * CGFloat(max(n - 1, 0))
                let stack = side ? AnyLayout(HStackLayout(spacing: gap)) : AnyLayout(VStackLayout(spacing: gap))
                stack {
                    ForEach(Array(s.children.enumerated()), id: \.element.stableKey) { i, child in
                        AnyView(ExposeLayout(model: model, layout: child, current: current, pick: pick, confirmClose: confirmClose))
                            .frame(width: side ? w[i] * total : nil, height: side ? nil : w[i] * total)
                    }
                }
            }
        case nil:
            Color.clear
        }
    }
}

/// A pane, live, small: its name and state over what it shows (its
/// terminal, its agent's latest, its video, its file or diff).
struct LiveTile: View {
    let model: AppModel
    let paneID: String
    let current: Bool
    var minimised = false
    @Environment(\.phoneTheme) private var theme

    private var pane: Pane? { model.panes[paneID] }

    var body: some View {
        let p = pane
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                PaneIcon(pane: p, size: 14)
                Text(stripName(p)).font(.footnote.weight(.semibold)).lineLimit(1)
                Spacer(minLength: 0)
                if minimised {
                    Text("Minimised").font(.caption2.weight(.semibold)).foregroundStyle(.secondary)
                } else if let p, p.hasAgent, !p.exited, p.agent.shownState != .unspecified {
                    StatusIcon(state: p.agent.shownState, size: 12, quietAtRest: true, asleep: p.asleep)
                }
            }
            content(p)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                .clipShape(RoundedRectangle(cornerRadius: 8))
        }
        .padding(8)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(current ? theme.accent : .clear, lineWidth: 2.5))
        .opacity(minimised ? 0.6 : 1)
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(current ? .isSelected : [])
    }

    @ViewBuilder private func content(_ p: Pane?) -> some View {
        if let p {
            switch p.kind {
            case .browser, .simulator:
                VideoTile(model: model, pane: p)
            case .editor:
                FileTile(model: model, pane: p)
            case .diff:
                DiffTile(pane: p)
            default:
                if !p.isLog, model.supportsAgentView(p), AgentViewPrefs.showsAgent(p.id) {
                    AgentTailTile(model: model, paneID: p.id)
                } else {
                    TerminalRepresentable(model: model, paneID: p.id, active: false, preview: true)
                }
            }
        }
    }
}

/// An agent pane's tile: the latest of its conversation, and the tool it's
/// running.
private struct AgentTailTile: View {
    let model: AppModel
    let paneID: String
    @State private var session: AgentSession?
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Spacer(minLength: 0)
            if let s = session {
                // Your last message and what's come since, oldest first, as
                // the conversation reads: its words, and only its latest
                // tools (a run of them would leave no room for the words).
                ForEach(AgentTail.tail(s.blocks), id: \.id) { b in
                    switch b.kind {
                    case .user:
                        Text(b.text)
                            .font(.caption)
                            .lineLimit(3)
                            .padding(.horizontal, 8)
                            .padding(.vertical, 5)
                            .background(theme.accent.opacity(0.18), in: RoundedRectangle(cornerRadius: 10))
                            .frame(maxWidth: .infinity, alignment: .trailing)
                    case .tool:
                        Label(b.title, systemImage: b.pending ? "gearshape" : b.error ? "exclamationmark.circle" : "checkmark")
                            .font(.caption2.monospaced())
                            .foregroundStyle(b.error ? Color.red : .secondary)
                            .lineLimit(1)
                    default:
                        Text(AgentTail.plain(b.text)).font(.caption).foregroundStyle(.primary)
                    }
                }
            }
            if session?.blocks.isEmpty != false, let summary = model.panes[paneID]?.agent.summary, !summary.isEmpty {
                Text(summary).font(.caption).foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomLeading)
        .onAppear { session = model.acquireAgent(paneID) }
        .onDisappear {
            model.releaseAgent(paneID)
            session = nil
        }
    }
}

/// A simulator or browser pane's tile: its video, small and a few frames a
/// second, while the tile shows. Not in Low Power or Low Data Mode, when
/// it's the pane's icon.
private struct VideoTile: View {
    let model: AppModel
    let pane: Pane
    @State private var stream: MediaStream?
    @State private var playing = false

    var body: some View {
        ZStack {
            Color.black.opacity(0.25)
            if let stream, playing {
                MediaView(stream: stream)
            } else {
                PaneIcon(pane: pane, size: 28).foregroundStyle(.secondary)
            }
        }
        .task(id: pane.id) { await watch() }
    }

    private func watch() async {
        guard !SavingData.shared.saving else { return }
        if pane.kind == .simulator, !pane.deviceBooted { return }
        while !Task.isCancelled {
            guard let s = model.videoStream(for: pane.id, maxPixels: 320 * 640, maxFPS: 3) else {
                try? await Task.sleep(for: .seconds(1))
                continue
            }
            let states = AsyncStream<MediaStream.State> { cont in s.onState = { cont.yield($0) } }
            stream = s
            s.start()
            for await st in states {
                if st == .playing { playing = true }
                if case .failed = st { break }
                if Task.isCancelled { break }
            }
            s.stop()
            playing = false
            stream = nil
            if Task.isCancelled { break }
            try? await Task.sleep(for: .seconds(2))
        }
        stream?.stop()
        stream = nil
        playing = false
    }
}

/// Low Power Mode or Low Data Mode: no video in Exposé's tiles.
@MainActor
final class SavingData {
    static let shared = SavingData()
    private let monitor = NWPathMonitor()
    private var constrained = false

    private init() {
        monitor.pathUpdateHandler = { path in
            let c = path.isConstrained
            Task { @MainActor in SavingData.shared.constrained = c }
        }
        monitor.start(queue: .global(qos: .utility))
    }

    var saving: Bool { constrained || ProcessInfo.processInfo.isLowPowerModeEnabled }
}

/// An editor pane's tile: the start of its text, live, and whether it has
/// unsaved edits.
private struct FileTile: View {
    let model: AppModel
    let pane: Pane
    @State private var session: EditorSession?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if let text = session?.text {
                // The first lines, as many as the tile has room for.
                Text(text.split(separator: "\n", omittingEmptySubsequences: false).prefix(60).joined(separator: "\n"))
                    .font(.system(size: 7, design: .monospaced))
                    .foregroundStyle(.primary.opacity(0.85))
                    .lineLimit(nil)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, minHeight: 0, maxHeight: .infinity, alignment: .topLeading)
                    .clipped()
            } else {
                Spacer(minLength: 0)
            }
            HStack(spacing: 6) {
                if pane.editor.dirty {
                    Circle().fill(Color.orange).frame(width: 6, height: 6).accessibilityLabel("Edited")
                }
                Text(pane.editor.path.isEmpty ? "Untitled" : (pane.editor.path as NSString).abbreviatingWithTildeInPath)
                    .font(.caption2.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.head)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomLeading)
        .onAppear { session = model.acquireEditor(pane.id) }
        .onDisappear {
            model.releaseEditor(pane.id)
            session = nil
        }
    }
}

/// A diff pane's tile: a picture of its changes (lines added and removed,
/// in proportion: the phone has the counts, not the diff), how much, and
/// its review threads.
private struct DiffTile: View {
    let pane: Pane

    /// Made-up lines in the diff's proportions: removed, added or context,
    /// each with a length, the same every time for the same counts.
    private var lines: [(kind: Int, width: CGFloat)] {
        DiffSketch.lines(added: pane.diff.added, removed: pane.diff.removed, files: pane.diff.files)
    }

    var body: some View {
        let d = pane.diff
        VStack(alignment: .leading, spacing: 4) {
            VStack(alignment: .leading, spacing: 3) {
                ForEach(Array(lines.enumerated()), id: \.offset) { _, l in
                    HStack(spacing: 4) {
                        Rectangle()
                            .fill(l.kind == 1 ? Color.green : l.kind == -1 ? Color.red : Color.clear)
                            .frame(width: 2)
                        GeometryReader { g in
                            Capsule()
                                .fill(l.kind == 1 ? Color.green.opacity(0.5) : l.kind == -1 ? Color.red.opacity(0.5) : Color.secondary.opacity(0.3))
                                .frame(width: g.size.width * l.width)
                        }
                    }
                    .frame(height: 4)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .clipped()
            .accessibilityHidden(true)
            HStack(spacing: 6) {
                Text(d.files == 1 ? "1 file" : "\(d.files) files")
                Text("+\(d.added)").foregroundStyle(.green)
                Text("−\(d.removed)").foregroundStyle(.red)
            }
            .font(.caption.monospacedDigit())
            if !d.branch.isEmpty, d.openThreads == 0, d.unread == 0 {
                Text(d.branch).font(.caption2.monospaced()).foregroundStyle(.secondary).lineLimit(1)
            }
            if d.openThreads > 0 || d.unread > 0 {
                Text([d.openThreads > 0 ? (d.openThreads == 1 ? "1 thread" : "\(d.openThreads) threads") : nil,
                      d.unread > 0 ? "\(d.unread) new" : nil].compactMap { $0 }.joined(separator: " · "))
                    .font(.caption2).foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomLeading)
    }
}
