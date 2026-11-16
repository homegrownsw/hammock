import AppKit
import HammockKit
import SwiftUI

// A tab's preview: the pointer resting on a tab (the tab bar's, a sidebar
// row) brings up a card of it: its panes, live and small, in their places,
// its whole title, and what it's up to (its agent, who's in it, its last
// output). Settings ▸ Appearance ▸ Preview on hover (tab-preview); off, the
// tab's title as a tooltip.

/// Which side of its tab a preview stands: under a tab bar's tab, beside a
/// sidebar row.
enum TabPreviewEdge {
    case below, trailing
}

extension View {
    /// This tab's preview when the pointer rests here (or, with previews
    /// off, its title as a tooltip). `open` goes to a pane clicked in it
    /// (by default, the main window's way: `reveal`).
    func tabPreview(model: AppModel, tab: WorkspaceTab, edge: TabPreviewEdge, open: ((String) -> Void)? = nil) -> some View {
        background(TabPreviewAnchor(model: model, tabID: tab.id, title: model.title(of: tab), edge: edge, on: AppConfig.shared.tabPreview,
                                    open: open ?? { model.reveal(paneID: $0) }))
    }
}

/// Where a tab is on screen, and what the pointer does over it.
private struct TabPreviewAnchor: NSViewRepresentable {
    let model: AppModel
    let tabID: String
    let title: String
    let edge: TabPreviewEdge
    let on: Bool
    let open: (String) -> Void

    func makeNSView(context: Context) -> TabPreviewAnchorView {
        let v = TabPreviewAnchorView()
        updateNSView(v, context: context)
        return v
    }

    func updateNSView(_ v: TabPreviewAnchorView, context: Context) {
        v.model = model
        v.tabID = tabID
        v.edge = edge
        v.open = open
        let tip = on ? nil : title
        if v.toolTip != tip { v.toolTip = tip }
    }
}

final class TabPreviewAnchorView: NSView {
    weak var model: AppModel?
    var tabID = ""
    var edge = TabPreviewEdge.below
    /// Goes to a pane clicked in the preview, in this window's way.
    var open: (String) -> Void = { _ in }
    private var area: NSTrackingArea?

    /// Never takes a click: the tab in front of it does.
    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        if let area { removeTrackingArea(area) }
        let a = NSTrackingArea(rect: .zero, options: [.mouseEnteredAndExited, .mouseMoved, .activeInActiveApp, .inVisibleRect], owner: self)
        addTrackingArea(a)
        area = a
    }

    override func mouseEntered(with event: NSEvent) { TabPreview.shared.entered(self) }
    override func mouseMoved(with event: NSEvent) { TabPreview.shared.moved(self) }
    override func mouseExited(with event: NSEvent) { TabPreview.shared.exited(self) }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if window == nil { TabPreview.shared.gone(self) } else { TabPreview.shared.appeared(self) }
    }

    /// Where the tab is, in screen coordinates.
    var screenRect: CGRect? {
        guard let window else { return nil }
        return window.convertToScreen(convert(bounds, to: nil))
    }
}

/// The tab whose card the pointer's on: it stays lit as if hovered, in the
/// tab bar and the sidebar.
@MainActor
@Observable
final class TabPreviewHold {
    static let shared = TabPreviewHold()
    var tabID: String?
}

/// Shows and hides the preview: after the pointer rests on a tab a moment,
/// then at once from tab to tab while one shows; fading away a moment after
/// the pointer leaves, and at once on a click, a scroll or a key.
@MainActor
final class TabPreview {
    static let shared = TabPreview()

    /// The pointer's wait on a tab before its preview shows.
    static let delay: Duration = .milliseconds(500)
    /// How long it stays once the pointer's left the tab, and how soon
    /// after it went another tab's shows without the wait.
    static let linger: Duration = .milliseconds(200)
    /// Its fade in and out.
    static let fadeIn: TimeInterval = 0.12
    static let fadeOut: TimeInterval = 0.18

    /// The tab the card shows.
    private weak var anchor: TabPreviewAnchorView?
    /// The tab the pointer's on, if any.
    private weak var under: TabPreviewAnchorView?
    /// A tab whose card a click or key put away: none until the pointer
    /// leaves it.
    private weak var dismissed: TabPreviewAnchorView?
    private weak var waitingOn: TabPreviewAnchorView?
    private var waiting: Task<Void, Never>?
    private var closing: Task<Void, Never>?
    private var hiddenAt = ContinuousClock.now - .seconds(10)
    private var scrolledAt = ContinuousClock.now - .seconds(10)
    private var monitor: Any?
    private var panel: TabPreviewPanel?
    /// Bumped by each show and hide, so a fade that's been overtaken doesn't finish.
    private var fades = 0
    private var size = CGSize(width: TabPreviewCard.width, height: 300)
    /// HMK_DEBUG_TAB_PREVIEW: a tab's preview shown on its own, and kept.
    private let debugTab = ProcessInfo.processInfo.environment["HMK_DEBUG_TAB_PREVIEW"]
    private var pinned = false

    /// The card stays while the pointer's on it, and a click on a pane in
    /// it goes there (on trial: false makes it a picture the pointer goes
    /// through).
    static let clickable = true
    /// From tab to tab while the card shows, with it clickable: how long
    /// the pointer stays on another tab before the card goes to it.
    static let sweep: Duration = .milliseconds(100)
    private var overCard = false

    private init() {
        NotificationCenter.default.addObserver(forName: NSApplication.didResignActiveNotification, object: nil, queue: .main) { _ in
            MainActor.assumeIsolated { TabPreview.shared.hide() }
        }
    }

    private var showing: Bool { anchor != nil }

    func entered(_ v: TabPreviewAnchorView) {
        under = v
        guard AppConfig.shared.tabPreview, !pinned, dismissed !== v else { return }
        // Not mid drag, nor as a scroll carries tabs under the pointer.
        guard NSEvent.pressedMouseButtons == 0, ContinuousClock.now - scrolledAt > .milliseconds(300) else { return }
        if v === anchor {
            closing?.cancel()
            closing = nil
            return
        }
        waiting?.cancel()
        waitingOn = v
        if showing, Self.clickable {
            // On the way to the card, the pointer can cross other tabs:
            // only one it stays on takes the card.
            waiting = Task { [weak v] in
                try? await Task.sleep(for: Self.sweep)
                guard !Task.isCancelled, let v, !self.overCard else { return }
                self.show(v)
            }
            return
        }
        if showing || ContinuousClock.now - hiddenAt < Self.linger {
            show(v)
            return
        }
        waiting = Task { [weak v] in
            try? await Task.sleep(for: Self.delay)
            guard !Task.isCancelled, let v else { return }
            self.show(v)
        }
    }

    /// The pointer moving on a tab: its card, if none shows or is on its way
    /// (it went while the pointer stayed, or the pointer came to rest here
    /// as something else moved).
    func moved(_ v: TabPreviewAnchorView) {
        if v !== under || (anchor == nil && waitingOn !== v) { entered(v) }
    }

    func exited(_ v: TabPreviewAnchorView) {
        if under === v { under = nil }
        if dismissed === v { dismissed = nil }
        if waitingOn === v {
            waiting?.cancel()
            waiting = nil
            waitingOn = nil
        }
        guard v === anchor, !overCard else { return }
        closeSoon()
    }

    /// The pointer came onto the card or left it.
    func cardHover(_ inside: Bool) {
        guard Self.clickable, !pinned else { return }
        overCard = inside
        let held = inside ? anchor?.tabID : nil
        if TabPreviewHold.shared.tabID != held { TabPreviewHold.shared.tabID = held }
        if inside {
            closing?.cancel()
            closing = nil
            waiting?.cancel()
            waiting = nil
            waitingOn = nil
        } else if under !== anchor {
            closeSoon()
        }
    }

    /// Hidden after `linger`, unless by then the card's moved to another
    /// tab or the pointer's come back.
    private func closeSoon() {
        guard !pinned else { return }
        closing?.cancel()
        let from = anchor
        closing = Task {
            try? await Task.sleep(for: Self.linger)
            guard !Task.isCancelled, self.anchor === from, !self.overCard, self.under !== from else { return }
            self.hide()
        }
    }

    /// A pane clicked in the card: its tab selected, it focused.
    func open(_ paneID: String) {
        let v = anchor
        dismissed = under
        hide(force: true)
        v?.open(paneID)
    }

    func gone(_ v: TabPreviewAnchorView) {
        if waitingOn === v { waiting?.cancel() }
        if v === anchor { hide(force: true) }
    }

    func appeared(_ v: TabPreviewAnchorView) {
        guard let debugTab, !pinned, let model = v.model else { return }
        let tabs = model.orderedTabs
        let id = Int(debugTab).flatMap { tabs.indices.contains($0 - 1) ? tabs[$0 - 1].id : nil }
            ?? tabs.first { model.title(of: $0).localizedCaseInsensitiveContains(debugTab) }?.id
        guard v.tabID == id else { return }
        Task { [weak v] in
            try? await Task.sleep(for: .seconds(1.5))
            guard let v, v.window != nil else { return }
            self.show(v)
            self.pinned = true
        }
    }

    private func show(_ v: TabPreviewAnchorView) {
        guard let window = v.window, let model = v.model, model.tabs[v.tabID] != nil else { return }
        let panel = self.panel ?? TabPreviewPanel()
        self.panel = panel
        closing?.cancel()
        closing = nil
        waiting = nil
        waitingOn = nil
        fades += 1
        let appearing = !panel.isVisible || anchor == nil
        if v !== anchor {
            panel.host.rootView = TabPreviewContent(model: model, tabID: v.tabID) { [weak self] in self?.resized($0) }
            size = panel.host.fittingSize
        }
        anchor = v
        if panel.parent !== window {
            panel.parent?.removeChildWindow(panel)
            window.addChildWindow(panel, ordered: .above)
        }
        // From tab to tab it moves at once: sliding, its new tab's card
        // would show in a window still on its way.
        place()
        panel.ignoresMouseEvents = !Self.clickable
        if !panel.isVisible { panel.alphaValue = 0 }
        panel.orderFront(nil)
        // Its shadow follows what it's drawn: once the card is.
        DispatchQueue.main.async { panel.invalidateShadow() }
        if appearing || panel.alphaValue < 1 {
            NSAnimationContext.runAnimationGroup { c in
                c.duration = Self.fadeIn
                panel.animator().alphaValue = 1
            }
        }
        watchEvents()
    }

    /// The card grew or shrank (a line of what it's up to came or went).
    private func resized(_ s: CGSize) {
        guard s != size, s.width > 0, s.height > 0, anchor != nil else { return }
        size = s
        place()
    }

    private func place() {
        guard let panel, let v = anchor, let r = v.screenRect, let window = v.window else { return }
        let w = size.width, h = size.height
        let screen = window.screen?.visibleFrame ?? window.frame
        var x: CGFloat, top: CGFloat
        switch v.edge {
        case .below:
            // Centred under the tab, kept within the window.
            x = min(max(r.midX - w / 2, window.frame.minX + 8), window.frame.maxX - 8 - w)
            top = r.minY - 6
        case .trailing:
            // Beside the row, its picture level with it.
            x = r.maxX + 8
            top = r.maxY + 10
        }
        top = min(top, screen.maxY - 8)
        if top - h < screen.minY + 8 { top = screen.minY + 8 + h }
        panel.setFrame(CGRect(x: x, y: top - h, width: w, height: h), display: true)
        panel.invalidateShadow()
    }

    /// Fades it away; its panes let go once it's gone.
    func hide(force: Bool = false) {
        guard !pinned || force else { return }
        waiting?.cancel()
        closing?.cancel()
        waiting = nil
        waitingOn = nil
        closing = nil
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
        guard let panel, anchor != nil else { return }
        anchor = nil
        pinned = false
        overCard = false
        if TabPreviewHold.shared.tabID != nil { TabPreviewHold.shared.tabID = nil }
        hiddenAt = .now
        panel.ignoresMouseEvents = true
        fades += 1
        let fade = fades
        NSAnimationContext.runAnimationGroup { c in
            c.duration = Self.fadeOut
            panel.animator().alphaValue = 0
        } completionHandler: {
            MainActor.assumeIsolated {
                guard TabPreview.shared.fades == fade else { return }
                panel.parent?.removeChildWindow(panel)
                panel.orderOut(nil)
                // Streams detached, video stopped.
                panel.host.rootView = TabPreviewContent(model: nil, tabID: "") { _ in }
            }
        }
    }

    /// A click, scroll or key while it shows puts it away (not one on the
    /// card itself); after a click or key, it stays away from the tab the
    /// pointer's on until the pointer leaves it.
    private func watchEvents() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.leftMouseDown, .rightMouseDown, .otherMouseDown, .scrollWheel, .keyDown]) { e in
            MainActor.assumeIsolated {
                let p = TabPreview.shared
                if e.window != nil, e.window === p.panel { return }
                if e.type == .scrollWheel { p.scrolledAt = .now } else { p.dismissed = p.under }
                p.hide()
            }
            return e
        }
    }
}

/// The window the card floats in, over its tab's window: never key, the
/// card's size and shape (its shadow the window's own, around the card's
/// rounded corners, so the window needs no room for it, and never covers
/// its tab: the pointer over that, say on a row's close button, would be
/// in this window instead, and the tab would think it had left). Unless
/// it's clickable, the pointer goes through it.
private final class TabPreviewPanel: NSPanel {
    let host = FirstClickHostingView(rootView: TabPreviewContent(model: nil, tabID: "") { _ in })

    init() {
        super.init(contentRect: .zero, styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: true)
        isOpaque = false
        backgroundColor = .clear
        hasShadow = true
        ignoresMouseEvents = !TabPreview.clickable
        isReleasedWhenClosed = false
        animationBehavior = .none
        collectionBehavior = [.transient, .ignoresCycle, .fullScreenAuxiliary]
        host.sizingOptions = []
        contentView = host
    }

    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

/// Takes a click though its window is never key: the card's first click
/// opens a pane.
private final class FirstClickHostingView<Content: View>: NSHostingView<Content> {
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }
}

/// The card, saying when its size changes.
private struct TabPreviewContent: View {
    let model: AppModel?
    let tabID: String
    let resized: (CGSize) -> Void

    var body: some View {
        Group {
            if let model, model.tabs[tabID] != nil {
                TabPreviewCard(model: model, tabID: tabID, open: TabPreview.clickable ? { TabPreview.shared.open($0) } : nil)
                    .id(tabID)
                    .onHover { TabPreview.shared.cardHover($0) }
            }
        }
        .fixedSize()
        .onGeometryChange(for: CGSize.self) { $0.size } action: { resized($0) }
    }
}

// MARK: The card

struct TabPreviewCard: View {
    let model: AppModel
    let tabID: String
    /// Goes to a pane clicked in it (nil: not clickable).
    var open: ((String) -> Void)?
    @Environment(\.colorScheme) private var colorScheme

    static let width: CGFloat = 320
    static let picture = CGSize(width: 304, height: 190)

    var body: some View {
        if let tab = model.tabs[tabID] {
            VStack(alignment: .leading, spacing: 9) {
                TabPicture(model: model, tab: tab, open: open)
                    .frame(width: Self.picture.width, height: Self.picture.height)
                VStack(alignment: .leading, spacing: 6) {
                    Text(model.title(of: tab))
                        .font(.system(size: 13, weight: .semibold))
                        .lineLimit(3)
                        .fixedSize(horizontal: false, vertical: true)
                    TabPreviewDetails(model: model, tab: tab)
                }
                .padding(.horizontal, 4)
            }
            .padding(EdgeInsets(top: 8, leading: 8, bottom: 11, trailing: 8))
            .frame(width: Self.width, alignment: .leading)
            .background(Color(nsColor: .windowBackgroundColor), in: RoundedRectangle(cornerRadius: 14))
            .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(Color.primary.opacity(colorScheme == .dark ? 0.16 : 0.12), lineWidth: 0.5))
            // A click anywhere else on it goes to the tab as it is.
            .contentShape(RoundedRectangle(cornerRadius: 14))
            .onTapGesture { open?(tab.focusedPaneID) }
        }
    }
}

/// What a tab is up to, a line each, each only when it has something to
/// say: its agent, who's in it and where it runs, its panes and last output.
private struct TabPreviewDetails: View {
    let model: AppModel
    let tab: WorkspaceTab

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { context in
            VStack(alignment: .leading, spacing: 5) {
                if let p = model.shownAgentPane(of: tab), p.hasAgent, !p.exited {
                    agentLine(p, now: context.date)
                }
                people
                Text(panesLine(now: context.date))
                    .foregroundStyle(.secondary)
            }
            .font(.system(size: 11.5))
            .lineLimit(1)
        }
    }

    @ViewBuilder private func agentLine(_ p: Pane, now: Date) -> some View {
        let state = p.agent.shownState
        let name = p.isReportingProgram ? p.displayName : AgentMark.name(of: p.agent.harness)
        let since = p.agent.stateSinceUnixMs > 0 ? Self.ago(p.agent.stateSinceUnixMs, now: now) : nil
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 6) {
                StatusIcon(state: state, size: 12, asleep: p.asleep, progress: p.agentProgress)
                Text(Self.saying(state, name: name, asleep: p.asleep))
                if let progress = p.agentProgress {
                    Text("· \(progress)%").foregroundStyle(.secondary)
                } else if let since, since != "just now" {
                    Text("· \(since)").foregroundStyle(.secondary)
                }
            }
            if let progress = p.agentProgress {
                ProgressBar(value: Double(progress) / 100, waiting: state == .needsInput)
                    .padding(.leading, 18)
            }
        }
    }

    @ViewBuilder private var people: some View {
        let here = model.presence(in: tab)
        let group = model.group(of: tab)
        let elsewhere = group.map { $0.isRemote || model.computer(of: $0) != nil } ?? false
        if !here.isEmpty || elsewhere {
            HStack(spacing: 6) {
                if !here.isEmpty {
                    AvatarStack(here, size: 16)
                    let names = ListFormatter.localizedString(byJoining: here.map(\.1.name))
                    Text(names).foregroundStyle(.primary) + Text(here.count == 1 ? " is here" : " are here").foregroundStyle(.secondary)
                } else {
                    Image(systemName: "desktopcomputer").foregroundStyle(.secondary)
                }
                if elsewhere, let group {
                    Text(here.isEmpty ? "On \(model.hostLabel(of: group))" : "· on \(model.hostLabel(of: group))")
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func panesLine(now: Date) -> String {
        let split = AppModel.paneIDs(tab.layout).count
        var line = split == 1 ? "1 pane" : "\(split) panes"
        if !tab.floating.isEmpty { line += " + \(tab.floating.count) floating" }
        let last = AppModel.paneIDs(of: tab).compactMap { model.panes[$0]?.lastOutputUnixMs }.max() ?? 0
        if last > 0, let ago = Self.ago(last, now: now) {
            line += ago == "just now" ? " · output just now" : " · output \(ago) ago"
        }
        return line
    }

    static func saying(_ state: AgentState, name: String, asleep: Bool) -> String {
        if asleep { return "\(name) is asleep" }
        switch state {
        case .needsInput: return "\(name) is waiting on you"
        case .working, .starting: return "\(name) is working"
        case .errored: return "\(name) stopped on an error"
        case .idle: return "\(name) has finished"
        default: return name
        }
    }

    /// How long ago, to the minute ("just now", "4m", "2h", "3d").
    static func ago(_ ms: Int64, now: Date) -> String? {
        let s = now.timeIntervalSince1970 - Double(ms) / 1000
        guard s > -60 else { return nil }
        switch s {
        case ..<60: return "just now"
        case ..<3600: return "\(Int(s / 60))m"
        case ..<86400: return "\(Int(s / 3600))h"
        default: return "\(Int(s / 86400))d"
        }
    }
}

private struct ProgressBar: View {
    let value: Double
    let waiting: Bool

    var body: some View {
        GeometryReader { g in
            ZStack(alignment: .leading) {
                Capsule().fill(Color.primary.opacity(0.12))
                Capsule().fill(waiting ? Color.orange : Color.themeAccent)
                    .frame(width: g.size.width * min(max(value, 0), 1))
            }
        }
        .frame(height: 3)
    }
}

// MARK: The picture

/// The tab as laid out in its window, each pane live and small, its floats
/// over its splits where they float.
private struct TabPicture: View {
    let model: AppModel
    let tab: WorkspaceTab
    let open: ((String) -> Void)?

    private struct Placed: Identifiable {
        let id: String
        let rect: CGRect
        let floating: Bool
    }

    var body: some View {
        GeometryReader { geo in
            let area = CGRect(origin: .zero, size: geo.size).insetBy(dx: 3, dy: 3)
            ZStack(alignment: .topLeading) {
                ForEach(placed(in: area)) { p in
                    PaneTile(model: model, paneID: p.id, focused: p.id == tab.focusedPaneID, open: open)
                        .frame(width: max(p.rect.width, 1), height: max(p.rect.height, 1))
                        .shadow(color: .black.opacity(p.floating ? 0.3 : 0), radius: p.floating ? 6 : 0, y: p.floating ? 3 : 0)
                        .offset(x: p.rect.minX, y: p.rect.minY)
                }
            }
        }
        .background(Color.primary.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 8))
    }

    private func placed(in area: CGRect) -> [Placed] {
        var out = tab.layout.rects(in: area, gap: 3).map { Placed(id: $0.0, rect: $0.1, floating: false) }
        // Back to front, as the tab stacks them.
        for f in tab.floating where !f.minimised {
            let r = f.zoomed ? area : CGRect(x: area.minX + CGFloat(f.frame.x) * area.width, y: area.minY + CGFloat(f.frame.y) * area.height,
                                             width: CGFloat(f.frame.w) * area.width, height: CGFloat(f.frame.h) * area.height)
            out.append(Placed(id: f.paneID, rect: r, floating: true))
        }
        return out
    }
}

/// One pane, as it shows in its tab: its terminal or its agent's latest,
/// its page or simulator's video, its file, its diff.
private struct PaneTile: View {
    let model: AppModel
    let paneID: String
    let focused: Bool
    let open: ((String) -> Void)?
    @State private var hovered = false

    var body: some View {
        content
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .clipShape(RoundedRectangle(cornerRadius: 5))
            .overlay {
                // Clickable: the pane under the pointer lit, as it would be
                // once clicked.
                RoundedRectangle(cornerRadius: 5).fill(Color.themeAccent.opacity(hovered ? 0.12 : 0))
                RoundedRectangle(cornerRadius: 5)
                    .strokeBorder(focused || hovered ? Color.themeAccent : Color.primary.opacity(0.1), lineWidth: focused || hovered ? 1.5 : 0.5)
            }
            .contentShape(RoundedRectangle(cornerRadius: 5))
            .onHover { hovered = open != nil && $0 }
            .onTapGesture { open?(paneID) }
            .accessibilityAddTraits(open != nil ? .isButton : [])
    }

    @ViewBuilder private var content: some View {
        if let p = model.panes[paneID] {
            switch p.kind {
            case .browser, .simulator:
                VideoTile(model: model, pane: p)
            case .editor:
                FileTile(model: model, pane: p)
            case .diff:
                DiffTile(pane: p)
            default:
                if model.supportsAgentView(p), AgentModes.shared.showsAgent(p.id) {
                    AgentTile(model: model, paneID: p.id)
                } else {
                    TerminalTile(model: model, paneID: p.id)
                }
            }
        } else {
            Color.clear
        }
    }
}

/// A terminal, drawn as it is in its pane (its own size, its theme) by a
/// terminal view of its own out of sight, and shrunk to fit.
private struct TerminalTile: View {
    let model: AppModel
    let paneID: String
    @State private var thumb = TerminalThumb()
    @Environment(\.colorScheme) private var scheme

    /// The terminal theme's background: the tile's from its first frame,
    /// so its picture arriving doesn't flash the card's colour first.
    private var background: Color {
        let bg = AppConfig.shared.theme(isDark: scheme == .dark).background
        return Color(.sRGB, red: Double(bg.r) / 255, green: Double(bg.g) / 255, blue: Double(bg.b) / 255)
    }

    var body: some View {
        GeometryReader { g in
            if let image = thumb.image {
                // Filling the tile from its left edge, and, if the tile is
                // wider than the pane, from its bottom, where the latest
                // output is.
                let iw = CGFloat(image.width), ih = CGFloat(image.height)
                let s = max(g.size.width / iw, g.size.height / ih)
                Image(decorative: image, scale: 1)
                    .resizable()
                    .interpolation(.high)
                    .frame(width: iw * s, height: ih * s)
                    .frame(width: g.size.width, height: g.size.height, alignment: .bottomLeading)
                    .transition(.opacity)
            }
        }
        .background(background)
        .clipped()
        .onAppear { thumb.start(model, paneID) }
        .onDisappear { thumb.stop() }
    }
}

@MainActor
@Observable
private final class TerminalThumb {
    var image: CGImage?
    @ObservationIgnored private var view: TerminalNSView?
    @ObservationIgnored private weak var model: AppModel?
    @ObservationIgnored private var soon = false

    func start(_ model: AppModel, _ paneID: String) {
        guard view == nil else { return }
        self.model = model
        let v = TerminalNSView(frame: .zero)
        let session = model.acquireSession(paneID)
        v.session = session
        session.observe(self) { [weak self] in self?.drawSoon() }
        view = v
        draw(fade: false) // a pane already streaming: there from the first frame
    }

    func stop() {
        guard let v = view else { return }
        if let s = v.session {
            s.unobserve(self)
            v.session = nil
            model?.releaseSession(s.paneID)
        }
        view = nil
    }

    /// New output: drawn a few times a second at most.
    private func drawSoon() {
        guard !soon else { return }
        soon = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.12) { [weak self] in
            guard let self else { return }
            self.soon = false
            self.draw(fade: true)
        }
    }

    private func draw(fade: Bool) {
        guard let v = view, let s = v.session, s.ready, s.engine.cols > 0, s.engine.rows > 0 else { return }
        let size = CGSize(width: CGFloat(s.engine.cols) * v.cellWidth + v.padding * 2,
                          height: CGFloat(s.engine.rows) * v.cellHeight + v.padding * 2)
        if v.frame.size != size { v.setFrameSize(size) }
        guard let i = v.previewImage(scale: 1) else { return }
        // The first, if it comes after the card shows (the pane's screen
        // arriving from the daemon), fades in; otherwise it's simply there,
        // and new output replaces it at once.
        if fade, image == nil {
            withAnimation(.easeOut(duration: 0.15)) { image = i }
        } else {
            image = i
        }
    }
}

/// An agent pane shown as its conversation: the latest of it.
private struct AgentTile: View {
    let model: AppModel
    let paneID: String
    @State private var session: AgentSession?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Spacer(minLength: 0)
            if let s = session {
                ForEach(AgentTail.tail(s.blocks), id: \.id) { b in
                    switch b.kind {
                    case .user:
                        Text(b.text)
                            .lineLimit(3)
                            .padding(.horizontal, 5)
                            .padding(.vertical, 3)
                            .background(Color.themeAccent.opacity(0.16), in: RoundedRectangle(cornerRadius: 6))
                            .frame(maxWidth: .infinity, alignment: .trailing)
                    case .tool:
                        Label(b.title, systemImage: b.pending ? "gearshape" : b.error ? "exclamationmark.circle" : "checkmark")
                            .font(.system(size: 6.5, design: .monospaced))
                            .foregroundStyle(b.error ? Color.red : .secondary)
                            .lineLimit(1)
                    default:
                        Text(AgentTail.plain(b.text)).foregroundStyle(.primary)
                    }
                }
            }
            if session?.blocks.isEmpty != false, let summary = model.panes[paneID]?.agent.summary, !summary.isEmpty {
                Text(summary).foregroundStyle(.secondary)
            }
        }
        .font(.system(size: 7))
        .padding(6)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomLeading)
        .background(Color(nsColor: .textBackgroundColor))
        .onAppear { session = model.acquireAgent(paneID) }
        .onDisappear {
            model.releaseAgent(paneID)
            session = nil
        }
    }
}

/// A browser or simulator pane: its video, small and a few frames a second.
private struct VideoTile: View {
    let model: AppModel
    let pane: Pane
    @State private var stream: MediaStream?
    @State private var playing = false

    var body: some View {
        ZStack {
            Color.black.opacity(0.2)
            if let stream, playing {
                MediaView(stream: stream)
            } else if pane.kind == .browser {
                Favicon(url: pane.faviconURL, size: 18).opacity(0.7)
            } else {
                Image(systemName: "iphone").font(.system(size: 16)).foregroundStyle(.secondary)
            }
        }
        .task(id: pane.id) { await watch() }
    }

    private func watch() async {
        if pane.kind == .simulator, !pane.deviceBooted { return }
        while !Task.isCancelled {
            guard let s = model.videoStream(for: pane.id, maxPixels: 640 * 400, maxFPS: 3) else {
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

/// An editor pane: the start of its file.
private struct FileTile: View {
    let model: AppModel
    let pane: Pane
    @State private var session: EditorSession?

    var body: some View {
        ZStack(alignment: .topLeading) {
            Color(nsColor: .textBackgroundColor)
            if let text = session?.text {
                Text(text.split(separator: "\n", omittingEmptySubsequences: false).prefix(60).joined(separator: "\n"))
                    .font(.system(size: 6, design: .monospaced))
                    .foregroundStyle(.primary.opacity(0.85))
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(5)
            }
        }
        .clipped()
        .onAppear { session = model.acquireEditor(pane.id) }
        .onDisappear {
            model.releaseEditor(pane.id)
            session = nil
        }
    }
}

/// A diff pane: its changes in proportion, and how much.
private struct DiffTile: View {
    let pane: Pane

    var body: some View {
        let d = pane.diff
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 4) {
                Text(d.files == 1 ? "1 file" : "\(d.files) files")
                Text("+\(d.added)").foregroundStyle(.green)
                Text("−\(d.removed)").foregroundStyle(.red)
            }
            .font(.system(size: 6.5, design: .monospaced))
            VStack(alignment: .leading, spacing: 2) {
                ForEach(Array(DiffSketch.lines(added: d.added, removed: d.removed, files: d.files).enumerated()), id: \.offset) { _, l in
                    GeometryReader { g in
                        Capsule()
                            .fill(l.kind == 1 ? Color.green.opacity(0.5) : l.kind == -1 ? Color.red.opacity(0.5) : Color.secondary.opacity(0.3))
                            .frame(width: g.size.width * l.width)
                    }
                    .frame(height: 3)
                }
            }
            .frame(maxHeight: .infinity, alignment: .top)
            .clipped()
        }
        .padding(5)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Color(nsColor: .textBackgroundColor))
    }
}
