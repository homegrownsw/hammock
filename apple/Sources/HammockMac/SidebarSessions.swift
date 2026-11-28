import HammockKit
import SwiftUI

/// What the sidebar lists: the Groups and their tabs, or every agent
/// session (the phones' Sessions tab).
enum SidebarMode: String {
    case groups, sessions
}

/// The Groups | Sessions switch is on (Settings ▸ Appearance); off, the
/// sidebar lists only Groups.
enum SessionsSwitch {
    static let pref = Pref("sidebar-sessions", defaults: "sidebarSessions", true)
}

/// The sidebar's mode, remembered between launches (state, not a setting).
@MainActor @Observable
final class SidebarModeState {
    static let shared = SidebarModeState()
    private static let key = "sidebarMode"

    var mode: SidebarMode = SidebarMode(rawValue: UserDefaults.standard.string(forKey: key) ?? "") ?? .groups {
        didSet { UserDefaults.standard.set(mode.rawValue, forKey: Self.key) }
    }

    /// Sessions is the vertical sidebar's, while its switch is on: with
    /// Horizontal Tabs, or the switch off, the sidebar lists only Groups.
    var applies: Bool { SidebarLayout.current != .horizontal && Prefs.shared.bool(SessionsSwitch.pref) }
    var showsSessions: Bool { applies && mode == .sessions }

    /// Where the sidebar's two pages sit: 0 Groups, 1 Sessions, between
    /// them while sliding or swiped (a little past either end, stretching).
    /// Each page is its own List, so each keeps where it was scrolled to.
    var position: CGFloat = 0
    /// The pages' width: how far a swipe has to go.
    var width: CGFloat = 0

    init() { position = Self.page(mode) }

    private static func page(_ m: SidebarMode) -> CGFloat { m == .sessions ? 1 : 0 }

    /// Shows Groups or Sessions, sliding to it, and the sidebar if it's hidden.
    func show(_ m: SidebarMode) {
        UserDefaults.standard.set(false, forKey: SidebarHiding.key)
        settle(on: m)
    }

    /// Between pages: swiped, or sliding to one. Tab previews wait.
    private(set) var moving = false
    private var settling = 0

    private func settle(on m: SidebarMode, duration: Double = 0.3) {
        let reduce = NSWorkspace.shared.accessibilityDisplayShouldReduceMotion
        guard !reduce, Self.page(m) != position || m != mode else {
            mode = m
            position = Self.page(m)
            moving = false
            return
        }
        startMoving()
        settling += 1
        let n = settling
        withAnimation(.snappy(duration: duration)) {
            mode = m
            position = Self.page(m)
        } completion: { [weak self] in
            // Only the last slide's end, if another began meanwhile.
            guard let self, self.settling == n else { return }
            self.moving = false
        }
    }

    private func startMoving() {
        moving = true
        TabPreview.shared.hide(force: true)
    }

    // A two-finger swipe across the sidebar (SidebarSwipe): AppKit's swipe
    // tracking, as Safari's back and forward, says how far it's gone and,
    // let go, which way it turns; our spring finishes it.
    private var swipeStart: CGFloat = 0
    private var swipeCommits = false

    /// The page a swipe from here would turn to: the other one.
    var swipeTarget: SidebarMode { mode == .groups ? .sessions : .groups }

    func beginSwipe() {
        swipeStart = Self.page(mode)
        swipeCommits = false
        startMoving()
    }

    /// `amount`: AppKit's gesture amount, positive with the fingers moving
    /// right (towards Groups), 1 a whole page.
    func swipe(amount: CGFloat) {
        position = swipeStart - amount
        // A tap of the trackpad as it passes halfway, where letting go turns
        // the page.
        let commits = abs(amount) > 0.5
        if commits != swipeCommits {
            swipeCommits = commits
            NSHapticFeedbackManager.defaultPerformer.perform(.alignment, performanceTime: .now)
        }
    }

    /// The swipe's over: on to the other page (`turned`) or back, springing
    /// there (`animated`) or already there.
    func endSwipe(turned: Bool, animated: Bool) {
        let to = turned ? swipeTarget : mode
        guard animated else { mode = to; position = Self.page(to); moving = false; return }
        // Quicker than the switch's slide: the page is already moving.
        settle(on: to, duration: 0.22)
    }
}

/// Two-finger swipes left and right over the sidebar turn its page between
/// Groups and Sessions (when System Settings' Swipe between pages is on);
/// vertical ones still scroll. Watches the window's scroll events: a
/// gesture's first ones are held back from the List until it's clear which
/// way it goes, then a sideways one is AppKit's swipe tracking's.
@MainActor
final class SidebarSwipe {
    /// The sidebar's window, and its pages' frame in it (SwiftUI's global
    /// space: from the top left).
    var window: () -> NSWindow? = { nil }
    var frame: CGRect = .zero

    private func isOver(_ e: NSEvent) -> Bool {
        guard let w = window(), e.window === w, let content = w.contentView else { return false }
        let p = e.locationInWindow
        return frame.contains(CGPoint(x: p.x, y: content.bounds.height - p.y))
    }
    private var monitor: Any?
    /// nil: undecided; then sideways (ours) or not.
    private var sideways: Bool?
    private var dx: CGFloat = 0, dy: CGFloat = 0
    /// The gesture's first events, until it's clear which way it goes.
    private var held: [NSEvent] = []
    /// AppKit's swipe tracking is running (it takes the gesture's events).
    private var tracking = false

    func start() {
        guard monitor == nil else { return }
        monitor = NSEvent.addLocalMonitorForEvents(matching: .scrollWheel) { [weak self] e in
            self?.handle(e) ?? e
        }
    }

    func stop() {
        if let monitor { NSEvent.removeMonitor(monitor) }
        monitor = nil
    }

    private func handle(_ e: NSEvent) -> NSEvent? {
        // A mouse's wheel (no phases), or momentum: not a swipe.
        guard !e.phase.isEmpty, !e.phase.contains(.mayBegin) else { return e }
        if e.phase.contains(.began) {
            held = []
            dx = 0; dy = 0
            sideways = !tracking && SidebarModeState.shared.applies && NSEvent.isSwipeTrackingFromScrollEventsEnabled && isOver(e) ? nil : false
        }
        guard sideways == nil else { return e }
        if e.phase.contains(.ended) || e.phase.contains(.cancelled) {
            sideways = false
            release()
            return e
        }
        // Held back from the List until it's clear which way it goes.
        held.append(e)
        dx += e.scrollingDeltaX; dy += e.scrollingDeltaY
        guard max(abs(dx), abs(dy)) > 6 else { return nil }
        guard abs(dx) > abs(dy) * 1.5 else {
            sideways = false
            held.removeLast()
            release()
            return e
        }
        sideways = true
        held = []
        track(e)
        return nil
    }

    private func track(_ e: NSEvent) {
        let state = SidebarModeState.shared
        state.beginSwipe()
        tracking = true
        // Only towards the other page; past it, or back past this one, it
        // stretches.
        let toSessions = state.swipeTarget == .sessions
        // Where the fingers let go: AppKit's next amount says which way its
        // spring goes, and ours (quicker) takes it from there.
        var released: CGFloat?
        var done = false
        e.trackSwipeEvent(options: [.lockDirection], dampenAmountThresholdMin: toSessions ? -1 : 0, max: toSessions ? 0 : 1) { [weak self] amount, phase, complete, stop in
            MainActor.assumeIsolated {
                guard !done else { return }
                if let released {
                    stop.pointee = true
                    done = true
                    state.endSwipe(turned: abs(amount) > abs(released) || abs(amount) > 0.99, animated: true)
                    self?.tracking = false
                    return
                }
                state.swipe(amount: amount)
                if phase == .ended || phase == .cancelled { released = amount }
                if complete {
                    done = true
                    state.endSwipe(turned: abs(amount) > 0.5, animated: false)
                    self?.tracking = false
                }
            }
        }
    }

    /// Not a swipe after all: the events held back go to the List, in order.
    private func release() {
        let events = held
        held = []
        for ev in events { ev.window?.sendEvent(ev) }
    }
}

/// One of the sidebar's two pages (0 Groups, 1 Sessions), placed by where
/// they sit: the one off to the side neither drawn nor clickable.
struct SidebarPage: ViewModifier {
    let index: CGFloat

    func body(content: Content) -> some View {
        let s = SidebarModeState.shared
        let at = s.applies ? s.position : 0
        let current = s.applies ? (s.mode == .sessions ? 1 : 0) == index : index == 0
        content
            .offset(x: (index - at) * s.width)
            .opacity(abs(index - at) < 1 ? 1 : 0)
            .allowsHitTesting(current)
            .accessibilityHidden(!current)
    }
}

/// Groups | Sessions, at the left of the sidebar's footer, as their symbols.
struct SidebarModePicker: View {
    private var state: SidebarModeState { .shared }

    var body: some View {
        // Read here, not only in the binding, so the control follows the
        // mode however it changes (the View menu, the palette).
        let mode = state.mode
        Picker("Sidebar", selection: Binding(get: { mode }, set: { state.show($0) })) {
            segment("Groups", "square.stack.3d.up").tag(SidebarMode.groups)
            segment("Sessions", "bubble.left.and.text.bubble.right").tag(SidebarMode.sessions)
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .fixedSize()
        .help("\(Keys.hint("Groups", "show_groups")) or \(Keys.hint("Sessions", "show_sessions"))")
    }

    private func segment(_ title: String, _ symbol: String) -> some View {
        Image(systemName: symbol).accessibilityLabel(title)
    }
}

/// The sidebar's Sessions: the agents in the shown Groups' tabs, one row a
/// tab, in Needs You (with the queue's cards: permission prompts,
/// questions, requests), Working and Recent, as on the phones. Rows of the
/// sidebar's List.
struct SidebarSessionsList: View {
    let model: AppModel
    /// Recent shows sessions older than 48 hours too, until Show Less.
    @State private var showingAllRecent = false
    @State private var hovered: String?
    @Environment(\.sidebarCompact) private var compact

    var body: some View {
        let groups = model.shownGroups
        let sections = model.sessionSections(in: groups)
        let shownPanes = Set(groups.flatMap { g in model.tabs(in: g).flatMap { model.panes(in: $0).map(\.id) } })
        // The computer chosen in the footer: its agents' cards. Requests to
        // link or join aren't any one Group's.
        let shown: (String) -> Bool = { $0.isEmpty || shownPanes.contains($0) }
        let cards = model.needsYou.filter { shown($0.paneID) }
        let grants = model.grants.filter { shown($0.requesterPaneID) }
        // A session the queue has a card for shows as its card.
        let others = sections.needsYou.filter { s in !cards.contains { $0.paneID == s.pane.id } }
        let links = NeedsYouView.incomingLinks(model)
        let needsYou = !links.isEmpty || !model.joinRequests.isEmpty || !grants.isEmpty || !cards.isEmpty || !others.isEmpty

        if needsYou {
            heading("Needs You")
            ForEach(links, id: \.id) { card(LinkRequestCard(model: model, request: $0)) }
            ForEach(model.joinRequests, id: \.id) { card(JoinCard(model: model, request: $0)) }
            ForEach(grants) { card(GrantCard(model: model, grant: $0)) }
            ForEach(cards) { card(AttentionCard(model: model, item: $0)) }
            ForEach(others) { row($0) }
        }
        if !sections.working.isEmpty {
            heading("Working")
            ForEach(sections.working) { row($0) }
        }
        if !sections.finished.isEmpty {
            heading("Finished")
            ForEach(sections.finished) { row($0) }
        }
        if !sections.recent.isEmpty {
            heading("Recent")
            // The last 48 hours', then the rest on asking. (As of this
            // update: the list updates with every state change.)
            let recent = sections.shownRecent(all: showingAllRecent, now: Date())
            ForEach(recent.rows) { row($0) }
            if recent.more {
                Button(showingAllRecent ? "Show Less" : "Show More…") {
                    withAnimation(.snappy(duration: 0.25)) { showingAllRecent.toggle() }
                }
                .buttonStyle(.plain)
                .foregroundStyle(Color.themeAccent)
                .font(.callout)
                .padding(.leading, 10)
                .padding(.vertical, 4)
                .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
            }
        }
        if !needsYou, sections.isEmpty, model.connected {
            Text("No Agent Sessions")
                .font(.callout)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity)
                .padding(.top, 24)
                .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
        }
    }

    private func heading(_ title: String) -> some View {
        Text(title)
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(.secondary)
            .padding(.leading, 10)
            .padding(.top, 10)
            .padding(.bottom, 2)
            .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
    }

    private func card(_ content: some View) -> some View {
        content
            .padding(.vertical, 3)
            .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
    }

    private func row(_ s: SessionEntry) -> some View {
        SidebarSessionRow(model: model, session: s)
            .padding(.leading, 10)
            .padding(.trailing, 8)
            .padding(.vertical, compact ? 5 : 7)
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
            .background {
                SidebarPill(selected: model.selectedTabID == s.tab.id, hovered: hovered == s.tab.id)
                    .padding(.vertical, compact ? 1 : 2)
            }
            .tabPreview(model: model, tab: s.tab, edge: .trailing)
            .onHover { inside in
                if inside, !PaletteState.shared.isOpen { hovered = s.tab.id } else if hovered == s.tab.id { hovered = nil }
            }
            // Straight to the agent's pane, even in a tab of several.
            .onTapGesture { model.reveal(paneID: s.pane.id) }
            .contextMenu { TabMenuItems(model: model, tab: s.tab) }
            .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 0, trailing: 0))
    }
}

/// A session as the phones show it: its state and name, when, its latest
/// message (Claude's reply, what it's doing, or what it's asking), then,
/// quietly, its Group.
struct SidebarSessionRow: View {
    let model: AppModel
    let session: SessionEntry
    @Environment(\.sidebarCompact) private var compact

    var body: some View {
        let a = session.pane.agent
        HStack(alignment: .top, spacing: compact ? 7 : 8) {
            StatusIcon(state: a.shownState == .idle && a.seen ? nil : a.shownState, resting: a.shownState == .idle && a.seen, size: compact ? 16 : 17,
                       asleep: session.pane.asleep, progress: session.pane.agentProgress, waiting: a.waitingOnLimit)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 2) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(model.title(of: session.tab))
                        .font(.system(size: compact ? 13.5 : 14, weight: .medium))
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    TimelineView(ElapsedSchedule(sinceUnixMs: a.stateSinceUnixMs)) { ctx in
                        Text(elapsedString(sinceUnixMs: a.stateSinceUnixMs, now: ctx.date))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .monospacedDigit()
                            .lineLimit(1)
                    }
                }
                let line = a.summary.isEmpty ? session.pane.agentStatusLabel : a.summary
                if !line.isEmpty {
                    Text(Markdown.inlineText(line))
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                if let resume = session.pane.limitResumeLabel() {
                    Label(resume, systemImage: "hourglass")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                if let g = model.group(of: session.tab) {
                    HStack(spacing: 5) {
                        GroupIconView(model: model, group: g, size: 12) {
                            Image(systemName: "square.stack.3d.up").imageScale(.small)
                        }
                        .frame(width: 13)
                        Text(model.title(of: g)).lineLimit(1)
                    }
                    .font(.caption)
                    .foregroundStyle(.tertiary)
                    .padding(.top, 1)
                }
            }
        }
        .accessibilityElement(children: .combine)
    }
}
