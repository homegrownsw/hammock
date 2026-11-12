import Foundation
import Observation
import SwiftUI
#if os(iOS)
import UIKit
#else
import AppKit
#endif

public typealias WorkspaceGroup = Hammock_V1_Group
public typealias WorkspaceTab = Hammock_V1_Tab
public typealias FloatingPane = Hammock_V1_FloatingPane
public typealias FloatAction = Hammock_V1_FloatPane.Action
public typealias Pane = Hammock_V1_Pane
public typealias PaneLayout = Hammock_V1_Layout
public typealias SessionInfo = Hammock_V1_SessionInfo
public typealias AttentionItem = Hammock_V1_AttentionItem
public typealias GrantRequest = Hammock_V1_GrantRequest
public typealias GrantDecision = Hammock_V1_RespondGrant.Decision
public typealias AgentState = Hammock_V1_AgentState
public typealias SplitDirection = Hammock_V1_Split.Direction
public typealias Person = Hammock_V1_Person
public typealias Member = Hammock_V1_Member
public typealias Role = Hammock_V1_Role
public typealias JoinRequest = Hammock_V1_JoinRequest
public typealias Computer = Hammock_V1_Computer
public typealias LinkRequest = Hammock_V1_LinkRequest

/// A pane's client-side terminal plus the views drawing it.
@MainActor
public final class PaneSession {
    public let paneID: String
    public let engine: TerminalEngine
    var refs = 0
    /// Whether the first snapshot has arrived (until then there's nothing to draw).
    public internal(set) var ready = false
    /// The grid the client wants (re-sent after reconnecting).
    var desired: (cols: Int, rows: Int)?
    private var observers: [ObjectIdentifier: () -> Void] = [:]

    /// Registers a view to redraw after new output (one entry per owner).
    public func observe(_ owner: AnyObject, _ fn: @escaping () -> Void) { observers[ObjectIdentifier(owner)] = fn }

    /// Removes an owner's observer (other views of the pane keep theirs).
    public func unobserve(_ owner: AnyObject) { observers[ObjectIdentifier(owner)] = nil }

    func notify() {
        latestChanges = nil
        for fn in observers.values { fn() }
    }

    private var latestChanges: TerminalChanges?

    /// What the output that just arrived changed on screen, for an observer
    /// that redraws only that: asked of the engine once per notify, so every
    /// view of the pane sees the same answer.
    public var changes: TerminalChanges {
        if let latestChanges { return latestChanges }
        let c = engine.takeChanges()
        latestChanges = c
        return c
    }

    init(paneID: String, cols: Int, rows: Int) {
        self.paneID = paneID
        self.engine = TerminalEngine(cols: cols, rows: rows)
    }

    /// A session fed by hand rather than by a daemon, ready to draw at once
    /// (the renderer benchmark, HMK_DEBUG_RENDER_BENCH).
    public static func standalone(paneID: String, cols: Int, rows: Int) -> PaneSession {
        let s = PaneSession(paneID: paneID, cols: cols, rows: rows)
        s.ready = true
        return s
    }

    /// Told the bytes and seconds of each write of a pane's output (or a
    /// snapshot) into its engine, while the Mac's renderer is measured
    /// (HMK_DEBUG_RENDER_STATS): parsing shares the main thread with drawing.
    @MainActor public static var writeTiming: ((_ bytes: Int, _ seconds: CFTimeInterval, _ snapshot: Bool) -> Void)?

    @MainActor static func timed(_ bytes: Int, snapshot: Bool, _ write: () -> Void) {
        guard let writeTiming else { return write() }
        let start = CACurrentMediaTime()
        write()
        writeTiming(bytes, CACurrentMediaTime() - start, snapshot)
    }
}

/// The client's view of a daemon: state, actions and pane streams. Shared by
/// the macOS and iOS apps.
@MainActor
@Observable
public final class AppModel {
    public private(set) var connected = false
    public internal(set) var hostName = ""
    /// Whether the daemon accepts paired devices (local clients only).
    public internal(set) var remoteEnabled = false
    public internal(set) var groups: [WorkspaceGroup] = []
    public private(set) var tabs: [String: WorkspaceTab] = [:]
    public private(set) var panes: [String: Pane] = [:]
    public private(set) var attention: [AttentionItem] = []
    public internal(set) var grants: [GrantRequest] = []
    /// This Mac's Hammock Key, and requests to sign with it waiting on you.
    public internal(set) var hammockKey = Hammock_V1_HammockKeyStatus()
    public internal(set) var hammockKeyRequests: [Hammock_V1_HammockKeyRequest] = []
    /// Multiplayer: people you share Groups with (and you), and who's asking to join.
    public private(set) var people: [String: Person] = [:]
    public internal(set) var joinRequests: [JoinRequest] = []
    /// Your computers: this one first, then those linked with it (Computers.swift).
    public internal(set) var computers: [Computer] = []
    /// The computer you used last: it's the one that notifies.
    public internal(set) var activeComputerID = ""
    /// Something to tell you about your computers (after an upgrade: ones to link again).
    public internal(set) var computersNote = ""
    /// Links waiting on their code, asked from here or by another computer.
    public internal(set) var linkRequests: [LinkRequest] = []
    /// The computer the sidebar shows ("" for all of them), remembered.
    public var computerFilter: String = UserDefaults.standard.string(forKey: "computerFilter") ?? "" {
        didSet { UserDefaults.standard.set(computerFilter, forKey: "computerFilter") }
    }
    /// This Mac's agents' usage limits (Claude's and Codex's 5-hour and
    /// weekly windows), as their CLIs last had them.
    public internal(set) var usage: [Hammock_V1_AgentUsage] = []
    /// This Mac's theme (Settings ▸ Appearance): the iPhone follows it
    /// unless it picks its own. nil until the Mac says.
    public internal(set) var hostTheme: Hammock_V1_HostTheme?
    /// The agents the daemon knows (AgentCatalog, which also has them for
    /// static helpers); empty from a daemon too old to send them.
    public internal(set) var agentKinds: [Hammock_V1_AgentKind] = [] {
        didSet { AgentCatalog.shared.received = agentKinds }
    }
    /// Scheduled tasks (Schedules.swift): this computer's and linked
    /// computers', by id.
    public internal(set) var schedules: [String: Schedule] = [:]
    /// This daemon's user (see Person).
    public private(set) var myUserID = ""
    /// Remembered across launches (per device), so the app reopens where you were.
    public var selectedTabID: String? = UserDefaults.standard.string(forKey: "selectedTab") {
        didSet {
            UserDefaults.standard.set(selectedTabID, forKey: "selectedTab")
            if selectedTabID != nil, selectedGroupID != nil { selectedGroupID = nil }
            if let id = selectedTabID, id != oldValue { noteOpened(id) }
        }
    }
    /// When this device last showed each tab (seconds since 1970), kept
    /// across launches: the palette lists the ones you opened most recently
    /// first. Not observed: nothing redraws when it changes.
    @ObservationIgnored public private(set) var tabOpenedAt: [String: Double] =
        UserDefaults.standard.dictionary(forKey: "tabOpenedAt") as? [String: Double] ?? [:]

    private func noteOpened(_ id: String) {
        tabOpenedAt[id] = Date().timeIntervalSince1970
        UserDefaults.standard.set(tabOpenedAt, forKey: "tabOpenedAt")
    }

    /// A Group without tabs, shown in place of a tab (what to open in
    /// it next). Only while no tab is selected: selecting one clears it.
    public private(set) var selectedGroupID: String? = UserDefaults.standard.string(forKey: "selectedEmptyGroup") {
        didSet { UserDefaults.standard.set(selectedGroupID, forKey: "selectedEmptyGroup") }
    }
    var lastColors: Hammock_V1_SetColors?
    public var focusedPaneID: String?
    /// A pane this app is splitting: the new pane takes focus as it arrives,
    /// before the split's rebuilt views can hand it back to the old one.
    private var splitting: String?
    public var lastError: String?
    /// A short note shown for a few seconds over the window (see showToast).
    public private(set) var toast: String?
    @ObservationIgnored private var toastTask: Task<Void, Never>?
    /// Tabs shown before the daemon has them, and gone before it has closed
    /// them: the sidebar follows what you did at once, the daemon catches up
    /// (and if it refuses, the tab comes back or goes again). Updates from
    /// the daemon meanwhile don't undo them.
    @ObservationIgnored private var openingTabs: [String: WorkspaceTab] = [:]
    @ObservationIgnored private var closingTabs: [String: (tab: WorkspaceTab, index: Int)] = [:]

    /// Called when the daemon can't be reached, before retrying (e.g. to launch it).
    @ObservationIgnored public var onUnreachable: (() -> Void)?
    /// Called with the daemon's version after each connect (the Mac app uses
    /// it to replace an outdated daemon with the one it bundles).
    @ObservationIgnored public var onDaemonVersion: ((String) -> Void)?
    /// Called when a new attention item arrives (for notifications).
    @ObservationIgnored public var onAttention: ((AttentionItem, Pane?) -> Void)?
    /// A scheduled run ended (ready, failed, gave up) while connected: the
    /// daemon raises no "finished" item for it, so clients tell you
    /// (Schedules.swift). Not for runs that ended while disconnected.
    @ObservationIgnored public var onScheduleRun: ((Schedule, ScheduleRun) -> Void)?
    /// An agent answered review comments on a diff pane: how many new.
    @ObservationIgnored public var onReviewReplies: ((Pane, Int) -> Void)?
    /// Applies an update that opens or closes panes in a tab already showing,
    /// given the closing panes: the Mac animates it (PaneMotion).
    @ObservationIgnored public var onPanesOpenClose: ((_ closing: [Pane], _ apply: () -> Void) -> Void)?
    /// Called when an agent asks for permission.
    @ObservationIgnored public var onGrant: ((GrantRequest) -> Void)?
    /// Called when someone asks to join a Group you share.
    @ObservationIgnored public var onJoinRequest: ((JoinRequest) -> Void)?
    /// Called when `hmk pair` (or the TUI) asks the Mac app to show its pairing code.
    @ObservationIgnored public var onShowPairing: (() -> Void)?
    /// Called when another computer asks to link (its code to check).
    @ObservationIgnored public var onLinkRequest: ((LinkRequest) -> Void)?
    /// Called with a device's name when it pairs with this Mac.
    @ObservationIgnored public var onDevicePaired: ((String) -> Void)?
    /// Called with the ids of attention items, grants, join and link
    /// requests an update clears or resolves, so their notifications go too.
    @ObservationIgnored public var onResolved: (([String]) -> Void)?
    /// Called after a full state update (connecting, reconnecting): what's
    /// still live is in `liveNoticeIDs`.
    @ObservationIgnored public var onFullState: (() -> Void)?

    @ObservationIgnored private let socketPath: String
    /// Set for remote clients (iOS); nil for the local Mac app.
    @ObservationIgnored public var remoteIdentity: RemoteIdentity?
    /// Called with the new device id after pairing succeeds.
    @ObservationIgnored public var onPaired: ((String) -> Void)?
    /// The Mac's name, as it says on each connection (it can be renamed).
    @ObservationIgnored public var onHostName: ((String) -> Void)?
    /// Remotely, once signed in: the Mac's addresses as it gives them now,
    /// and the one this connection reached it at (`address`), to keep for
    /// next time.
    @ObservationIgnored public var onRemoteAddresses: ((RemoteIdentity) -> Void)?
    @ObservationIgnored private var challenge: Data?
    @ObservationIgnored var stopped = false
    @ObservationIgnored private var challengeWaiters: [CheckedContinuation<Data, Never>] = []
    @ObservationIgnored var conn: DaemonConnection?
    var connection: DaemonConnection? { conn }
    @ObservationIgnored private var sessions: [String: PaneSession] = [:]
    @ObservationIgnored var agentSessions: [String: AgentSession] = [:]
    @ObservationIgnored var logSessions: [String: LogSession] = [:]
    @ObservationIgnored var editorSessions: [String: EditorSession] = [:]
    @ObservationIgnored var diffSessions: [String: DiffSession] = [:]
    /// Groups' pictures (PNG) by hash, and the ones being fetched.
    public internal(set) var groupPictures: [String: Data] = [:]
    @ObservationIgnored var pictureLoads: [String: Task<Void, Never>] = [:]
    @ObservationIgnored var decodedPictures: [String: PlatformImage] = [:]
    @ObservationIgnored var cellPixels = (w: 0, h: 0)
    @ObservationIgnored private var retryDelay: Double = 0.2
    @ObservationIgnored private var knownAttention = Set<String>()
    @ObservationIgnored private let clientKind: String

    public init(socketPath: String = DaemonConnection.defaultSocketPath, clientKind: String) {
        self.socketPath = socketPath
        self.clientKind = clientKind
    }

    /// A remote client connecting to a paired daemon.
    public init(remote: RemoteIdentity, clientKind: String) {
        self.socketPath = ""
        self.clientKind = clientKind
        self.remoteIdentity = remote
    }

    /// A client of `standIn`, a pretend daemon (the iPhone's demo).
    public init(standIn: DaemonStandIn, clientKind: String) {
        self.socketPath = ""
        self.clientKind = clientKind
        self.standIn = standIn
    }

    /// Set: this model's daemon is a stand-in (DaemonStandIn), not a Mac.
    @ObservationIgnored public private(set) var standIn: DaemonStandIn?

    private func nextChallenge() async -> Data {
        if let c = challenge { challenge = nil; return c }
        return await withCheckedContinuation { challengeWaiters.append($0) }
    }

    // MARK: Connection

    public func start() {
        guard !stopped else { return }
        let c = standIn.map { DaemonConnection(standIn: $0) } ?? remoteIdentity.map {
            DaemonConnection(remoteAddresses: $0.addresses, lanSubnets: $0.lanSubnets, lastWorked: $0.address, certSHA256: $0.certSHA256)
        } ?? DaemonConnection(socketPath: socketPath)
        challenge = nil
        conn = c
        let inbox = inbox
        c.onEvent = { [weak self] msg in
            // The first message waiting asks the main thread for them all:
            // under a flood, one hop takes many messages, not one each.
            guard inbox.add(msg) else { return }
            DispatchQueue.main.async { self?.drain(inbox) }
        }
        c.onStateChange = { [weak self] ready, err in
            DispatchQueue.main.async { self?.connectionChanged(ready: ready, error: err) }
        }
        c.connect()
    }

    private func connectionChanged(ready: Bool, error: Error?) {
        if stopped { return }
        if ready {
            Task { await self.handshake() }
            return
        }
        guard conn != nil else { return }
        connected = false
        conn = nil
        browserMenus.removeAll() // their pages were answered when the link dropped
        onUnreachable?()
        let delay = retryDelay
        retryDelay = min(retryDelay * 2, 5)
        DispatchQueue.main.asyncAfter(deadline: .now() + delay) { [weak self] in self?.start() }
    }

    private func handshake() async {
        guard let conn else { return }
        do {
            let kind = clientKind
            // Remotely, sign in first: the daemon only says who it is after.
            if let id = remoteIdentity {
                try await authenticateRemote(id, nonce: await nextChallenge())
            }
            let hello = try await conn.call {
                $0.hello = .with { $0.protocolVersion = 1; $0.clientKind = kind; $0.clientName = kind }
            }
            hostName = hello.helloReply.hostName
            if !hostName.isEmpty { onHostName?(hostName) }
            onDaemonVersion?(hello.helloReply.daemonVersion)
            remoteEnabled = hello.helloReply.remoteEnabled
            myUserID = hello.helloReply.userID
            if var id = remoteIdentity {
                // Where the Mac can be reached now (its local network's
                // address changes), and the address that worked: tried first next time.
                if !hello.helloReply.addresses.isEmpty {
                    id.addresses = hello.helloReply.addresses
                    id.lanSubnets = hello.helloReply.lanSubnets
                }
                if let a = conn.connectedAddress { id.address = a }
                remoteIdentity = id
                onRemoteAddresses?(id)
            }
            let state = try await conn.call { $0.subscribe = .init() }
            knownAttention = []
            apply(state.state)
            connected = true
            retryDelay = 0.2 // only once fully connected, so a daemon that drops us backs off
            if let c = lastColors { conn.send { $0.setColors = c } }
            // Re-attach panes that views are showing, re-sending their sizes: the
            // daemon forgets a connection's sizes when it goes away.
            // Lingering (unviewed) sessions went stale with the old connection.
            sessions = sessions.filter { $0.value.refs > 0 }
            for (id, s) in sessions where s.refs > 0 {
                guard panes[id] != nil else { continue }
                if let d = s.desired {
                    conn.send { $0.resize = .with { $0.paneID = id; $0.cols = UInt32(d.cols); $0.rows = UInt32(d.rows) } }
                }
                conn.send { $0.attachPane = .with { $0.paneID = id } }
            }
            rewatchAgents()
            rewatchLogs()
            reattachEditors()
            reattachDiffs()
        } catch {
            lastError = error.localizedDescription
        }
    }

    /// Messages from the daemon waiting for the main thread (`start`).
    @ObservationIgnored private let inbox = Inbox()

    final class Inbox: @unchecked Sendable {
        /// How much output one hop to the main thread takes at most, so a
        /// backlog (the main thread was busy) is parsed a piece at a time,
        /// with drawing and input in between, not all at once.
        static let maxBytes = 4 << 20

        private let lock = NSLock()
        private var waiting: [ServerMessage] = []
        private var start = 0
        private var asked = false

        /// Adds msg; true if the main thread must be asked to take what's
        /// waiting (it hasn't been already).
        func add(_ msg: ServerMessage) -> Bool {
            lock.lock()
            defer { lock.unlock() }
            waiting.append(msg)
            if asked { return false }
            asked = true
            return true
        }

        /// The messages waiting, up to about `maxBytes` of output, and
        /// whether more are left (then the main thread is asked again).
        func take() -> (batch: ArraySlice<ServerMessage>, more: Bool) {
            lock.lock()
            defer { lock.unlock() }
            var end = start, bytes = 0
            while end < waiting.count, bytes < Self.maxBytes {
                if case .paneOutput(let o) = waiting[end].msg { bytes += o.data.count }
                end += 1
            }
            let batch = waiting[start ..< end]
            if end == waiting.count {
                waiting = []
                start = 0
                asked = false
                return (batch, false)
            }
            start = end
            return (batch, true)
        }
    }

    /// Handles what's waiting in `inbox`, a batch per turn of the main thread.
    private func drain(_ inbox: Inbox) {
        let (batch, more) = inbox.take()
        handle(batch)
        if more { DispatchQueue.main.async { [weak self] in self?.drain(inbox) } }
    }

    // MARK: State

    /// Handles messages in order, except that each pane's output is gathered
    /// up to the next other message and written into its engine at once:
    /// one write and one redraw request per pane, not one per message.
    private func handle(_ batch: ArraySlice<ServerMessage>) {
        var output: [(id: String, data: Data)] = []
        func writeOutput() {
            for (id, data) in output {
                guard let session = sessions[id] else { continue }
                PaneSession.timed(data.count, snapshot: false) { session.engine.write(data) }
                session.notify()
            }
            output.removeAll()
        }
        for msg in batch {
            if case .paneOutput(let o) = msg.msg {
                if let i = output.firstIndex(where: { $0.id == o.paneID }) {
                    output[i].data.append(o.data)
                } else {
                    output.append((o.paneID, o.data))
                }
            } else {
                writeOutput()
                handle(msg)
            }
        }
        writeOutput()
    }

    private func handle(_ msg: ServerMessage) {
        switch msg.msg {
        case .state(let u):
            apply(u)
        case .paneSnapshot(let s):
            guard let session = sessions[s.paneID] else { return }
            session.engine.reset(cols: Int(s.cols), rows: Int(s.rows))
            PaneSession.timed(s.data.count, snapshot: true) { session.engine.write(s.data) }
            session.ready = true
            session.notify()
        case .authChallenge(let c):
            if challengeWaiters.isEmpty { challenge = c.nonce } else { challengeWaiters.removeFirst().resume(returning: c.nonce) }
        case .agentView(let v):
            agentSessions[AgentSession.key(v.paneID, v.subagentID, v.shellID)]?.apply(v)
        case .logView(let v):
            logSessions[v.paneID]?.apply(v)
        case .editorUpdate(let u):
            editorSessions[u.paneID]?.apply(u)
        case .review(let r):
            diffSessions[r.paneID]?.apply(r)
        case .browserMenu(let m):
            browserMenus[m.paneID] = m
        case .agentAction(let a):
            showAgentAction(a)
        case .browserClipboard(let c):
            // Only ever in answer to your own copy on a page you watch.
            #if os(iOS)
            UIPasteboard.general.string = c.text
            #else
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(c.text, forType: .string)
            #endif
        case .showPairing:
            onShowPairing?()
        case .devicePaired(let p):
            onDevicePaired?(p.name)
        default:
            break
        }
    }

    func apply(_ u: Hammock_V1_StateUpdate) {
        // If the selected tab goes, the tabs beside it (after it first) are
        // where the selection moves, not the sidebar's first tab. Worked out
        // before a full update (after a reconnect) empties the Groups.
        var besideSelected: [String] = []
        // …and if it was its Group's last, that Group.
        var selectedTabGroup: String?
        if let sel = selectedTabID, u.full || u.deletedTabIds.contains(sel), let g = groups.first(where: { $0.tabIds.contains(sel) }),
           let i = g.tabIds.firstIndex(of: sel) {
            besideSelected = Array(g.tabIds[(i + 1)...]) + g.tabIds[..<i].reversed()
            selectedTabGroup = g.id
        }
        if u.full {
            groups = []
            tabs = [:]
            panes = [:]
            attention = []
            grants = []
            hammockKeyRequests = []
            people = [:]
            joinRequests = []
            linkRequests = []
        }
        if u.hasComputers {
            let list = u.computers.list // (only linked computers come)
            if list != computers { computers = list }
            if u.computers.activeID != activeComputerID { activeComputerID = u.computers.activeID }
            if u.computers.note != computersNote { computersNote = u.computers.note }
            // The computer shown went away: show them all.
            if !computerFilter.isEmpty, !computers.contains(where: { $0.userID == computerFilter }) { computerFilter = "" }
        }
        for r in u.linkRequests {
            if let i = linkRequests.firstIndex(where: { $0.id == r.id }) {
                if linkRequests[i] != r { linkRequests[i] = r }
            } else {
                linkRequests.append(r)
                if r.incoming, !u.full { onLinkRequest?(r) }
            }
        }
        if linkRequests.contains(where: { u.resolvedLinkIds.contains($0.id) }) {
            linkRequests.removeAll { u.resolvedLinkIds.contains($0.id) }
        }
        // Every write to an observed property re-renders whatever reads it,
        // even when nothing changed, and most updates are one pane: only
        // touch what the update does change.
        applySchedules(u)
        for p in u.people where people[p.userID] != p { people[p.userID] = p }
        if u.hasUsage, u.usage.agents != usage { usage = u.usage.agents }
        if u.hasTheme, u.theme != hostTheme { hostTheme = u.theme }
        // Sent whole when it changes; a full update without it is an older daemon's.
        if (u.full || !u.agentKinds.isEmpty), u.agentKinds != agentKinds { agentKinds = u.agentKinds }
        for j in u.joinRequests where !joinRequests.contains(where: { $0.id == j.id }) {
            joinRequests.append(j)
            if !u.full { onJoinRequest?(j) }
        }
        if joinRequests.contains(where: { u.resolvedJoinIds.contains($0.id) }) {
            joinRequests.removeAll { u.resolvedJoinIds.contains($0.id) }
        }
        for id in u.deletedTabIds {
            closingTabs[id] = nil
            openingTabs[id] = nil
        }
        var gs = groups
        if !u.groups.isEmpty || !u.deletedGroupIds.isEmpty {
            for var s in u.groups {
                // Tabs you've closed stay gone, and ones you've opened stay
                // (at the end) until the daemon's list has them.
                s.tabIds.removeAll { closingTabs[$0] != nil }
                for (id, t) in openingTabs where t.groupID == s.id {
                    if s.tabIds.contains(id) { openingTabs[id] = nil } else { s.tabIds.append(id) }
                }
                if let i = gs.firstIndex(where: { $0.id == s.id }) { gs[i] = s } else { gs.append(s) }
            }
            gs.removeAll { u.deletedGroupIds.contains($0.id) }
            gs.sort { $0.order < $1.order }
        }
        // Groups and tabs coming, going or moving slide into place, whichever
        // update brings the change (a tab and its Group's list of tabs can
        // arrive separately); everything else changes at once.
        let structural = !u.full && (gs.map(\.id) != groups.map(\.id) || gs.map(\.tabIds) != groups.map(\.tabIds)
            || u.tabs.contains { tabs[$0.id] == nil } || u.deletedTabIds.contains { tabs[$0] != nil })
        let applyGroupsAndTabs = {
            if gs != self.groups { self.groups = gs }
            for t in u.tabs where self.tabs[t.id] != t && self.closingTabs[t.id] == nil { self.tabs[t.id] = t }
            for id in u.deletedTabIds where self.tabs[id] != nil { self.tabs[id] = nil }
            for (id, t) in self.openingTabs where self.tabs[id] == nil { self.tabs[id] = t } // after a full update
        }
        // Panes opening or closing in the tab showing (not floated out or
        // docked: their pane stays in the tab; not a new tab's first).
        var opened: [String] = [], closed: [String] = []
        if !u.full, onPanesOpenClose != nil {
            for t in u.tabs where t.id == selectedTabID {
                guard let old = tabs[t.id], old.layout != t.layout, closingTabs[t.id] == nil else { continue }
                let was = Set(Self.paneIDs(old.layout)), now = Set(Self.paneIDs(t.layout))
                guard !was.isEmpty else { continue }
                let floats = Set(old.floating.map(\.paneID)).union(t.floating.map(\.paneID))
                opened += now.subtracting(was).subtracting(floats)
                closed += was.subtracting(now).subtracting(floats)
            }
        }
        if structural {
            withAnimation(.snappy(duration: 0.25)) { applyGroupsAndTabs() }
        } else if !opened.isEmpty || !closed.isEmpty, let animate = onPanesOpenClose {
            animate(closed.compactMap { panes[$0] }) {
                applyGroupsAndTabs()
                // The closed panes go in the same transaction, so their views
                // leave with the animation rather than at once.
                for id in u.deletedPaneIds where self.panes[id] != nil || self.sessions[id] != nil {
                    self.panes[id] = nil
                    self.sessions[id] = nil
                }
                if let f = self.focusedPaneID, self.panes[f] == nil { self.focusedPaneID = nil }
            }
        } else {
            applyGroupsAndTabs()
        }
        for p in u.panes {
            let old = panes[p.id]
            if old == p { continue }
            panes[p.id] = p
            if old == nil, let from = splitting, panes[from]?.tabID == p.tabID {
                splitting = nil
                focusedPaneID = p.id
            }
            // A link the focused page opened in a new tab: go there, as a
            // browser does.
            if old == nil, !u.full, p.kind == .browser, !p.createdByPaneID.isEmpty, p.createdByPaneID == focusedPaneID {
                selectedTabID = p.tabID
            }
            if p.isDiff, let old, !u.full, p.diff.unread > old.diff.unread {
                onReviewReplies?(p, Int(p.diff.unread - old.diff.unread))
            }
            if let session = sessions[p.id], old?.cols != p.cols || old?.rows != p.rows {
                session.engine.resize(cols: Int(p.cols), rows: Int(p.rows))
                session.notify()
            }
        }
        for id in u.deletedPaneIds where panes[id] != nil || sessions[id] != nil {
            panes[id] = nil
            sessions[id] = nil
        }
        // The focused pane closed: follow its tab's focus, which the daemon
        // hands to the pane focused before it.
        if let f = focusedPaneID, panes[f] == nil { focusedPaneID = nil }
        if u.full {
            // Panes deleted while we were disconnected.
            for id in sessions.keys where panes[id] == nil { sessions[id] = nil }
            // Forget when you opened tabs that have since closed.
            if tabOpenedAt.keys.contains(where: { tabs[$0] == nil }) {
                tabOpenedAt = tabOpenedAt.filter { tabs[$0.key] != nil }
                UserDefaults.standard.set(tabOpenedAt, forKey: "tabOpenedAt")
            }
        }
        for a in u.attention {
            attention.removeAll { $0.id == a.id }
            attention.insert(a, at: 0)
            if !u.full, !knownAttention.contains(a.id + String(a.createdUnixMs)) {
                onAttention?(a, panes[a.paneID])
            }
            knownAttention.insert(a.id + String(a.createdUnixMs))
        }
        if attention.contains(where: { u.clearedAttentionIds.contains($0.id) }) {
            attention.removeAll { u.clearedAttentionIds.contains($0.id) }
        }
        for g in u.grants where !grants.contains(where: { $0.id == g.id }) {
            grants.insert(g, at: 0)
            if !u.full { onGrant?(g) }
        }
        if grants.contains(where: { u.resolvedGrantIds.contains($0.id) }) {
            grants.removeAll { u.resolvedGrantIds.contains($0.id) }
        }
        if u.hasHammockKey { hammockKey = u.hammockKey }
        for r in u.hammockKeyRequests where !hammockKeyRequests.contains(where: { $0.id == r.id }) {
            hammockKeyRequests.append(r) // oldest first: the one on screen stays
        }
        if hammockKeyRequests.contains(where: { u.resolvedHammockKeyIds.contains($0.id) }) {
            hammockKeyRequests.removeAll { u.resolvedHammockKeyIds.contains($0.id) }
        }

        if u.full || !u.deletedTabIds.isEmpty, let sel = selectedTabID, tabs[sel] == nil {
            selectedTabID = besideSelected.first { tabs[$0] != nil }
            if selectedTabID == nil, let g = selectedTabGroup { selectGroup(g) }
        }
        if selectedTabID == nil, let id = selectedGroupID {
            if let g = groups.first(where: { $0.id == id }) {
                // A tab arrived in the empty Group you're on (opened elsewhere): show it.
                if let first = g.tabIds.first(where: { tabs[$0] != nil }) { selectedTabID = first }
            } else if u.full || u.deletedGroupIds.contains(id) {
                selectedGroupID = nil
            }
        }
        if selectedTabID == nil, selectedGroupID == nil {
            // The sidebar's first tab (on the computer shown), else any.
            selectedTabID = (shownGroups + groups).lazy.compactMap { $0.tabIds.first }.first
        }
        let resolved = u.clearedAttentionIds + u.resolvedGrantIds + u.resolvedJoinIds + u.resolvedLinkIds + u.resolvedHammockKeyIds
        if !resolved.isEmpty { onResolved?(resolved) }
        if u.full { onFullState?() }
    }

    /// The ids of what can still need you (attention items, grants, join and
    /// link requests): a notification about anything else is stale.
    public var liveNoticeIDs: Set<String> {
        Set(attention.map(\.id) + grants.map(\.id) + joinRequests.map(\.id) + linkRequests.map(\.id) + hammockKeyRequests.map(\.id))
    }

    /// Shows a Group with no tabs (its zero state). A Group with tabs
    /// selects the tab you were last on there instead.
    public func selectGroup(_ id: String) {
        guard let g = groups.first(where: { $0.id == id }) else { return }
        if !g.tabIds.isEmpty {
            let last = panes[g.lastFocusedPaneID]?.tabID
            selectedTabID = last.flatMap { g.tabIds.contains($0) ? $0 : nil } ?? g.tabIds.first
            return
        }
        selectedTabID = nil
        selectedGroupID = id
    }

    /// The empty Group being shown, if that's what's selected.
    public var selectedEmptyGroup: WorkspaceGroup? {
        guard selectedTab == nil, let id = selectedGroupID else { return nil }
        return groups.first { $0.id == id && $0.tabIds.isEmpty }
    }

    /// Where a Group works, and its new tabs open: the folder the daemon
    /// keeps for it (its last focused terminal's, or one set by hand), else,
    /// from an older daemon, its last focused terminal's. "" when it has had
    /// neither (new tabs open at home).
    public func groupFolder(_ group: WorkspaceGroup) -> String {
        if !group.folder.isEmpty { return group.folder }
        return panes[group.lastFocusedPaneID]?.cwd ?? ""
    }

    /// The Group you're in: the selected tab's, or the empty one shown.
    /// New tabs, agents and logs go here.
    public var currentGroup: WorkspaceGroup? { selectedTab.flatMap { group(of: $0) } ?? selectedEmptyGroup }

    // MARK: Queries

    public func tabs(in group: WorkspaceGroup) -> [WorkspaceTab] { group.tabIds.compactMap { tabs[$0] } }

    public func group(of tab: WorkspaceTab) -> WorkspaceGroup? { groups.first { $0.id == tab.groupID } }

    /// The tab's name, or "Tab N" by its position in its Group when unnamed, so
    /// the number stays current as tabs move or close.
    public func title(of tab: WorkspaceTab) -> String {
        if !tab.name.isEmpty { return tab.name }
        // What the tab's work looks like (an agent's session title, the
        // branch), even with a browser or simulator beside it focused.
        if !tab.autoName.isEmpty { return tab.autoName }
        // Otherwise a browser tab is named after its page.
        if let p = panes[tab.focusedPaneID], p.kind == .browser {
            if !p.title.isEmpty, p.title != p.url { return p.title }
            if let host = URL(string: p.url)?.host() { return host }
            return "New Tab"
        }
        // A simulator tab is named after its device.
        if let p = panes[tab.focusedPaneID], p.kind == .simulator { return p.title }
        if let i = group(of: tab)?.tabIds.firstIndex(of: tab.id) { return "Tab \(i + 1)" }
        return "Tab"
    }

    /// The Group's name. Unnamed, it's named after the directory its
    /// last-focused pane is in ("Home" for the home directory), following it
    /// as it changes, else the folder it remembers (its tabs have closed),
    /// else "Group N" by position.
    public func title(of group: WorkspaceGroup) -> String {
        if !group.name.isEmpty { return group.name }
        if let cwd = panes[group.lastFocusedPaneID]?.cwd, !cwd.isEmpty {
            return cwd == NSHomeDirectory() ? "Home" : (cwd as NSString).lastPathComponent
        }
        if !group.folder.isEmpty {
            return group.folder == NSHomeDirectory() ? "Home" : (group.folder as NSString).lastPathComponent
        }
        if let i = groups.firstIndex(where: { $0.id == group.id }) { return "Group \(i + 1)" }
        return "Group"
    }

    public func panes(in tab: WorkspaceTab) -> [Pane] { Self.paneIDs(of: tab).compactMap { panes[$0] } }

    /// All of a tab's panes: its splits', then its floating ones (back to front).
    public static func paneIDs(of tab: WorkspaceTab) -> [String] {
        paneIDs(tab.layout) + tab.floating.map(\.paneID)
    }

    /// The panes a tab shows: its splits', then the floats not on its shelf
    /// (back to front), the order ⌘] goes through them.
    public static func shownPaneIDs(of tab: WorkspaceTab) -> [String] {
        paneIDs(tab.layout) + tab.floating.filter { !$0.minimised }.map(\.paneID)
    }

    /// A pane's place floating over its tab, if it floats.
    public func floating(_ paneID: String) -> FloatingPane? {
        guard let t = panes[paneID].flatMap({ tabs[$0.tabID] }) else { return nil }
        return t.floating.first { $0.paneID == paneID }
    }

    public func tab(of pane: Pane) -> WorkspaceTab? { tabs[pane.tabID] }

    public static func paneIDs(_ l: PaneLayout) -> [String] {
        switch l.node {
        case .paneID(let id): return [id]
        case .split(let s): return s.children.flatMap(paneIDs)
        case nil: return []
        }
    }

    /// The most urgent agent state among a tab's panes.
    public func agentState(of tab: WorkspaceTab) -> AgentState? {
        // A finished agent the user has since looked at shows no status.
        let states = panes(in: tab).compactMap { $0.hasAgent && !($0.agent.shownState == .idle && $0.agent.seen) ? $0.agent.shownState : nil }
        for s in [AgentState.needsInput, .errored, .working, .idle, .starting] where states.contains(s) { return s }
        return nil
    }

    /// The agent pane a tab's row stands for: the one whose state
    /// `agentState(of:)` shows (the focused pane on a tie), else an agent at
    /// rest there. Nil for a tab without one.
    public func shownAgentPane(of tab: WorkspaceTab) -> Pane? {
        Self.shownAgentPane(in: panes(in: tab), focused: tab.focusedPaneID)
    }

    nonisolated static func shownAgentPane(in panes: [Pane], focused: String) -> Pane? {
        let order: [AgentState] = [.needsInput, .errored, .working, .idle, .starting]
        func focusRank(_ p: Pane) -> Int { p.id == focused ? 0 : 1 }
        let agents = panes.filter(\.hasAgent)
        let shown = agents.compactMap { p -> (rank: Int, pane: Pane)? in
            guard !(p.agent.shownState == .idle && p.agent.seen), let r = order.firstIndex(of: p.agent.shownState) else { return nil }
            return (r, p)
        }
        if let best = shown.min(by: { ($0.rank, focusRank($0.pane)) < ($1.rank, focusRank($1.pane)) }) { return best.pane }
        return agents.filter { !$0.exited && $0.agent.shownState == .idle && $0.agent.seen }.min { focusRank($0) < focusRank($1) }
    }

    /// The harness ("claude", "codex", …) of `shownAgentPane(of:)`.
    public func agentHarness(of tab: WorkspaceTab) -> String? {
        shownAgentPane(of: tab)?.agent.harness
    }

    /// A tab whose agent is at rest: there, finished, and looked at (a grey
    /// ring, where a plain terminal shows its prompt glyph).
    public func hasRestingAgent(_ tab: WorkspaceTab) -> Bool {
        panes(in: tab).contains { $0.hasAgent && !$0.exited && $0.agent.shownState == .idle && $0.agent.seen }
    }

    /// Tabs in the order the palette offers them: agents waiting on
    /// you, then ones that finished (or failed) since you last looked, the
    /// newest first in each; then the rest, the ones you opened most
    /// recently first, and the one you're on last (⌘K ↩ takes you back to
    /// the previous tab); then sidebar order.
    public var paletteOrderedTabs: [WorkspaceTab] {
        Self.paletteOrder(orderedTabs.map { ($0, panes(in: $0)) }, openedAt: tabOpenedAt, current: selectedTabID)
    }

    /// Its own ranking, not `shownAgentPane`'s: finished and failed rank
    /// together, and only until you've looked.
    nonisolated static func paletteOrder(_ tabs: [(tab: WorkspaceTab, panes: [Pane])], openedAt: [String: Double], current: String? = nil) -> [WorkspaceTab] {
        func rank(_ p: Pane, _ tab: String) -> Int? {
            guard p.hasAgent, !p.exited else { return nil }
            switch p.agent.shownState {
            case .needsInput: return 0
            case .idle: return p.agent.seen ? nil : 1
            // The daemon doesn't mark a failure seen: it is, once you've
            // opened its tab since.
            case .errored: return tab == current || (openedAt[tab] ?? 0) * 1000 >= Double(p.agent.stateSinceUnixMs) ? nil : 1
            default: return nil
            }
        }
        let keyed = tabs.enumerated().map { i, t -> (tab: WorkspaceTab, rank: Int, when: Double, index: Int) in
            let ranked = t.panes.compactMap { p in rank(p, t.tab.id).map { ($0, p.agent.stateSinceUnixMs) } }
            if let best = ranked.map(\.0).min() {
                let since = ranked.filter { $0.0 == best }.map(\.1).max() ?? 0
                return (t.tab, best, Double(since) / 1000, i)
            }
            return (t.tab, 2, t.tab.id == current ? -1 : openedAt[t.tab.id] ?? 0, i)
        }
        return keyed.sorted { a, b in
            if a.rank != b.rank { return a.rank < b.rank }
            if a.when != b.when { return a.when > b.when }
            return a.index < b.index
        }.map(\.tab)
    }

    /// A resting agent in the tab still has work going on (shells,
    /// subagents): a dot goes round its ring.
    public func hasRestingAgentWithWork(_ tab: WorkspaceTab) -> Bool {
        panes(in: tab).contains { $0.hasAgent && !$0.exited && $0.agent.seen && $0.agent.restingWithWork }
    }

    public var selectedTab: WorkspaceTab? { selectedTabID.flatMap { tabs[$0] } }

    // MARK: Pane sessions

    /// Returns the pane's terminal session, attaching on first use.
    public func acquireSession(_ paneID: String) -> PaneSession {
        let pane = panes[paneID]
        let s = sessions[paneID] ?? PaneSession(paneID: paneID, cols: Int(pane?.cols ?? 80), rows: Int(pane?.rows ?? 24))
        sessions[paneID] = s
        s.refs += 1
        if s.refs == 1 {
            conn?.send { $0.attachPane = .with { $0.paneID = paneID } }
        }
        return s
    }

    public func releaseSession(_ paneID: String) {
        guard let s = sessions[paneID] else { return }
        s.refs -= 1
        guard s.refs <= 0 else { return }
        s.refs = 0
        // Keep it attached for a while: switching back to a tab then reuses an
        // up-to-date terminal instead of drawing an empty one while a fresh
        // snapshot arrives.
        Task { @MainActor [weak self, weak s] in
            try? await Task.sleep(for: .seconds(30))
            guard let self, let s, s.refs == 0, self.sessions[paneID] === s else { return }
            self.conn?.send { $0.detachPane = .with { $0.paneID = paneID } }
            self.sessions[paneID] = nil
        }
    }

    // MARK: Actions

    public func send(_ paneID: String, _ data: Data) {
        // In a Group shared with you as a viewer, typing goes nowhere.
        if group(ofPane: paneID)?.canDrive == false { return }
        conn?.send { $0.input = .with { $0.paneID = paneID; $0.data = data } }
    }

    public func resize(_ paneID: String, cols: Int, rows: Int) {
        sessions[paneID]?.desired = (cols, rows)
        let px = cellPixels
        conn?.send {
            $0.resize = .with {
                $0.paneID = paneID; $0.cols = UInt32(cols); $0.rows = UInt32(rows)
                $0.cellWidthPx = UInt32(px.w); $0.cellHeightPx = UInt32(px.h)
            }
        }
    }

    /// The terminal's cell size in device pixels, sent with resizes so
    /// programs that ask (Kitty graphics tools) know it.
    public func setCellPixels(_ paneID: String, width: Int, height: Int) {
        guard (width, height) != cellPixels else { return }
        cellPixels = (width, height)
        if let d = sessions[paneID]?.desired { resize(paneID, cols: d.cols, rows: d.rows) }
    }

    public func focus(_ paneID: String) {
        focusedPaneID = paneID
        // A float comes to the front (off the shelf), as the daemon will have it.
        if let tid = panes[paneID]?.tabID, var t = tabs[tid], let i = t.floating.firstIndex(where: { $0.paneID == paneID }),
           i != t.floating.count - 1 || t.floating[i].minimised {
            var f = t.floating.remove(at: i)
            f.minimised = false
            f.minimisedByAll = false
            t.floating.append(f)
            tabs[tid] = t
        }
        conn?.send { $0.focus = .with { $0.paneID = paneID } }
    }

    public func reportPresence(visible: [String], foreground: Bool) {
        let focused = focusedPaneID ?? selectedTab?.focusedPaneID ?? ""
        conn?.send { $0.presence = .with { $0.visiblePaneIds = visible; $0.foreground = foreground; $0.focusedPaneID = focused } }
    }

    func run(_ build: @escaping (inout ClientMessage) -> Void) async -> ServerMessage? {
        guard let conn else { lastError = "Not connected"; return nil }
        do { return try await conn.call(build) } catch {
            lastError = error.localizedDescription
            return nil
        }
    }

    @discardableResult
    public func createGroup(name: String, on computer: String = "") async -> String? {
        let id = await run {
            $0.createGroup = .with {
                $0.name = name
                if !computer.isEmpty, computer != self.myUserID { $0.computerID = computer }
            }
        }?.created.id
        // One made on another computer comes back from it a moment later.
        if let id, !computer.isEmpty, computer != myUserID {
            for _ in 0..<50 where !groups.contains(where: { $0.id == id }) {
                try? await Task.sleep(for: .milliseconds(40))
            }
        }
        return id
    }

    @discardableResult
    public func createTab(in groupID: String, name: String = "", command: [String] = [], cwd: String = "") async -> String? {
        let size = initialSize()
        return await openTab(in: groupID, name: name, pane: .with {
            $0.command = command
            $0.cwd = cwd
            if let s = size { $0.cols = s.cols; $0.rows = s.rows }
        })
    }

    private static let idAlphabet = Array("abcdefghijkmnpqrstuvwxyz23456789")

    /// Shows a new tab (selected) at once, then has the daemon create it
    /// with the same ID (CreateTab.tab_id): its row never swaps from a
    /// stand-in to the real one. If the daemon refuses, it goes again.
    private func openTab(in groupID: String, name: String, pane: Hammock_V1_PaneSpec) async -> String? {
        await openTab(in: groupID, name: name) { id in
            await self.run {
                $0.createTab = .with {
                    $0.groupID = groupID
                    $0.name = name
                    $0.pane = pane
                    $0.tabID = id
                }
            }?.created.id
        }
    }

    /// Starts an agent (claude or codex) in a new tab in folder cwd, shown
    /// at once as a new tab is.
    @discardableResult
    public func startAgent(_ harness: String, cwd: String, groupID: String, prompt: String = "", computerID: String = "") async -> String? {
        if groupID.isEmpty {
            // No Group chosen: the computer's own (by folder), or a new one there.
            return await run {
                $0.startAgent = .with {
                    $0.harness = harness
                    $0.cwd = cwd
                    $0.prompt = prompt
                    if computerID != self.myUserID { $0.computerID = computerID }
                }
            }?.created.id
        }
        return await openTab(in: groupID, name: "") { id in
            await self.run {
                $0.startAgent = .with {
                    $0.harness = harness
                    $0.cwd = cwd
                    $0.groupID = groupID
                    $0.prompt = prompt
                    $0.tabID = id
                }
            }?.created.id
        }
    }

    /// The folders to start an agent in: suggestions, and path's subfolders.
    public func listFolders(_ path: String = "", groupID: String = "", computerID: String = "") async -> Hammock_V1_FolderList? {
        await run {
            $0.listFolders = .with {
                $0.path = path
                $0.groupID = groupID
                if groupID.isEmpty, computerID != self.myUserID { $0.computerID = computerID }
            }
        }?.folderList
    }

    /// openTab, with create sending what makes it (given the tab's ID).
    private func openTab(in groupID: String, name: String, create: @escaping (String) async -> String?) async -> String? {
        let id = "tb_" + String((0..<8).map { _ in Self.idAlphabet.randomElement()! })
        let previous = selectedTabID
        let previousGroup = selectedEmptyGroup?.id // opened from an empty Group's page
        if let gi = groups.firstIndex(where: { $0.id == groupID }) {
            var stub = WorkspaceTab()
            stub.id = id
            stub.groupID = groupID
            stub.name = name
            openingTabs[id] = stub
            withAnimation(.snappy(duration: 0.25)) {
                tabs[id] = stub
                groups[gi].tabIds.append(id)
            }
            selectedTabID = id
        }
        let created = await create(id)
        if created == id { return id }
        // Refused, or an older host that picked its own ID: the stand-in goes.
        openingTabs[id] = nil
        withAnimation(.snappy(duration: 0.25)) {
            tabs[id] = nil
            if let gi = groups.firstIndex(where: { $0.id == groupID }) { groups[gi].tabIds.removeAll { $0 == id } }
        }
        if let created {
            selectedTabID = created
            return created
        }
        if selectedTabID == id {
            selectedTabID = previous
            if previous == nil, let g = previousGroup { selectGroup(g) }
        }
        showToast("Couldn't open a tab: \(lastError ?? "unknown error")")
        return nil
    }

    /// Opens a log pane (LogSource): a new tab in the Group, or split beside a pane.
    @discardableResult
    public func openLog(_ source: LogSource, in groupID: String?, splitting paneID: String? = nil, direction: SplitDirection = .horizontal, cwd: String = "") async -> String? {
        let size = initialSize(splitting: paneID, direction: direction)
        let spec = Hammock_V1_PaneSpec.with {
            $0.log = source
            $0.cwd = cwd
            if let s = size { $0.cols = s.cols; $0.rows = s.rows }
        }
        if let paneID { return await splitPane(paneID, direction: direction, spec: spec) }
        guard let groupID else { return nil }
        return await openTab(in: groupID, name: "", pane: spec)
    }

    /// Opens a browser tab (an empty one, in its address bar, without a URL).
    @discardableResult
    public func createBrowserTab(in groupID: String, url: String = "") async -> String? {
        await openTab(in: groupID, name: "", pane: .with { $0.url = url.isEmpty ? "about:blank" : url })
    }

    /// A simulator this Mac has (Xcode's devices).
    public struct SimulatorDevice: Decodable, Identifiable, Hashable {
        public let udid: String
        public let name: String
        public let runtime: String
        public let booted: Bool
        public var id: String { udid }
    }

    /// The simulators on this Mac, from the simulator host.
    public func simulators() async -> [SimulatorDevice] {
        guard let r = await run({ $0.simulatorCall = .with { $0.op = "devices" } }) else { return [] }
        return (try? JSONDecoder().decode([SimulatorDevice].self, from: Data(r.simulatorResult.json.utf8))) ?? []
    }

    /// Drives a simulator pane (the daemon's ops: screenshot, tap, swipe,
    /// text, button; coordinates in the device's points). Returns the reply's
    /// JSON and, for a screenshot, the picture.
    public func simulatorCall(_ paneID: String, _ op: String, _ args: [String: Any] = [:]) async -> (json: [String: Any], data: Data)? {
        let argsJSON = (try? JSONSerialization.data(withJSONObject: args)).flatMap { String(data: $0, encoding: .utf8) } ?? ""
        guard let r = await run({ $0.simulatorCall = .with { $0.paneID = paneID; $0.op = op; $0.argsJson = argsJSON } }) else { return nil }
        let json = (try? JSONSerialization.jsonObject(with: Data(r.simulatorResult.json.utf8))) as? [String: Any] ?? [:]
        return (json, r.simulatorResult.png)
    }

    /// A simulator pane's screen as a PNG in the device's pixels (agents'
    /// screenshots are in points), for someone to save or copy.
    public func simulatorScreenshot(_ paneID: String) async -> Data? {
        guard let r = await simulatorCall(paneID, "screenshot", ["pixels": true]), !r.data.isEmpty else { return nil }
        return r.data
    }

    /// Sends a simulator op without waiting (touch phases, typing), in the
    /// order given: for driving a pane shared with you.
    public func simulatorInput(_ paneID: String, _ op: String, _ args: [String: Any] = [:]) {
        guard group(ofPane: paneID)?.canDrive != false else { return } // View only: the host would refuse it
        let argsJSON = (try? JSONSerialization.data(withJSONObject: args)).flatMap { String(data: $0, encoding: .utf8) } ?? ""
        connection?.send { $0.simulatorCall = .with { $0.paneID = paneID; $0.op = op; $0.argsJson = argsJSON } }
    }

    /// Menus pages opened in answer to your input on panes you watch as
    /// video (a <select>, a right-click), by pane: show it, then answer.
    public private(set) var browserMenus: [String: BrowserMenu] = [:]

    /// Answers a page's menu: the index of the item picked, or -1 for none.
    public func answerBrowserMenu(_ menu: BrowserMenu, index: Int) {
        guard browserMenus[menu.paneID]?.seq == menu.seq else { return }
        browserMenus[menu.paneID] = nil
        connection?.send { $0.browserMenuChoice = .with { $0.paneID = menu.paneID; $0.seq = menu.seq; $0.kind = menu.kind; $0.index = Int32(index) } }
    }

    /// Input to a browser pane you watch as video (BrowserInput): op is
    /// mouse, wheel, key or text, with positions as fractions of the page.
    /// Sent in order, without waiting.
    public func browserInput(_ paneID: String, _ op: String, _ args: [String: Any] = [:]) {
        let argsJSON = (try? JSONSerialization.data(withJSONObject: args)).flatMap { String(data: $0, encoding: .utf8) } ?? ""
        connection?.send { $0.browserInput = .with { $0.paneID = paneID; $0.op = op; $0.argsJson = argsJSON } }
    }

    // MARK: Screen sharing (ADR-0014)

    /// A computer's displays, and whether its screen can be shared now
    /// (computerID: a linked computer's, or "" / your own for this one).
    /// requestAccess ("screen" or "input", this Mac only) asks macOS for
    /// Hammock Screen Sharing's permission first.
    public func listDisplays(computerID: String = "", requestAccess: String = "") async -> Hammock_V1_Displays? {
        await run {
            $0.listDisplays = .with {
                if computerID != self.myUserID { $0.computerID = computerID }
                $0.requestAccess = requestAccess
            }
        }?.displays
    }

    /// A display (0: the main one) as a small picture: what's on it now, or
    /// what it last showed if it's asleep (taken says when).
    public func screenSnapshot(computerID: String = "", display: UInt32 = 0, maxWidth: UInt32 = 960) async -> Hammock_V1_ScreenSnapshotReply? {
        await run {
            $0.screenSnapshot = .with {
                if computerID != self.myUserID { $0.computerID = computerID }
                $0.displayID = display
                $0.maxWidth = maxWidth
            }
        }?.screenSnapshotReply
    }

    /// Live video of a display (0: the main one) of this computer, or of a
    /// linked one (relayed by your Mac). Call start().
    /// background: a layer under a zoomed view (a small share of the link).
    public func screenStream(computerID: String = "", display: UInt32 = 0, maxPixels: UInt32 = 0, maxFPS: UInt32 = 0, background: Bool = false) -> MediaStream? {
        guard let conn = connection else { return nil }
        return MediaStream(connection: conn, display: display, computerID: computerID == myUserID ? "" : computerID, maxPixels: maxPixels, maxFPS: maxFPS,
                           background: background)
    }

    /// Your input to a display you're watching (ScreenInput's ops).
    public func screenInput(computerID: String = "", display: UInt32 = 0, _ op: String, _ args: [String: Any] = [:]) {
        let argsJSON = (try? JSONSerialization.data(withJSONObject: args)).flatMap { String(data: $0, encoding: .utf8) } ?? ""
        let cid = computerID == myUserID ? "" : computerID
        connection?.send { $0.screenInput = .with { $0.computerID = cid; $0.displayID = display; $0.op = op; $0.argsJson = argsJSON } }
    }

    /// Live video of a simulator or browser pane (MediaStream): over QUIC from
    /// a paired device, in band locally. maxPixels caps its size (0: the
    /// pane's own). Nil while disconnected. Call start().
    public func videoStream(for paneID: String, maxPixels: UInt32 = 0) -> MediaStream? {
        guard let conn = connection else { return nil }
        // A shared Group's pane comes through your Mac, which relays its host's video.
        let shared = group(ofPane: paneID)?.isRemote == true
        return MediaStream(connection: conn, paneID: paneID, maxPixels: maxPixels, inBand: shared)
    }

    /// Simulator panes waiting on their XCTest runner (rotations and pinches:
    /// the first one on a device starts it, which takes a while).
    public private(set) var simulatorsBusy: Set<String> = []

    /// Rotates a simulator: "left" or "right" (a quarter turn), or an orientation.
    public func rotateSimulator(_ paneID: String, _ value: String) {
        runnerCall(paneID, "rotate", ["value": value])
    }

    /// A foldable simulator (the iPhone Duo): "open", "closed" or "half".
    public func foldSimulator(_ paneID: String, _ value: String) {
        runnerCall(paneID, "fold", ["value": value])
    }

    /// Opens a closed (or half-open) foldable, or closes an open one.
    public func toggleFold(_ paneID: String) {
        foldSimulator(paneID, (panes[paneID]?.deviceHinge ?? 0) > 120 ? "closed" : "open")
    }

    /// Two fingers on a simulator's screen (points, ms), e.g. a pinch.
    public func simulatorGesture(_ paneID: String, paths: [[(x: Double, y: Double, t: Double)]]) {
        runnerCall(paneID, "gesture", ["paths": paths.map { $0.map { ["x": $0.x, "y": $0.y, "t": $0.t] } }])
    }

    private func runnerCall(_ paneID: String, _ op: String, _ args: [String: Any]) {
        guard group(ofPane: paneID)?.canDrive != false else { return }
        simulatorsBusy.insert(paneID)
        Task {
            _ = await simulatorCall(paneID, op, args)
            simulatorsBusy.remove(paneID)
        }
    }

    /// Presses a simulator's button: home, lock, side, siri, or keyboard (the software keyboard).
    public func pressSimulatorButton(_ paneID: String, _ name: String) {
        simulatorInput(paneID, "button", ["name": name])
    }

    /// Closes a simulator pane and shuts its simulator down now.
    public func shutDownSimulator(_ paneID: String) {
        Task { _ = await simulatorCall(paneID, "shutdown") }
    }

    /// Opens a tab showing a simulator.
    @discardableResult
    public func createSimulatorTab(in groupID: String, udid: String) async -> String? {
        let id = await run {
            $0.createTab = .with {
                $0.groupID = groupID
                $0.pane = .with { $0.simulator = udid }
            }
        }?.created.id
        if let id { selectedTabID = id }
        return id
    }

    @discardableResult
    public func splitBrowser(_ paneID: String, direction: SplitDirection, url: String = "", before: Bool = false) async -> String? {
        await splitPane(paneID, direction: direction, before: before, spec: .with { $0.url = url.isEmpty ? "about:blank" : url })
    }

    @discardableResult
    /// Shows a browser pane's DevTools: in a pane beside it (split right),
    /// or the one already showing them.
    public func showDevTools(_ paneID: String) async {
        let url = "hmk-devtools:" + paneID
        if let open = panes.values.first(where: { $0.kind == .browser && $0.url == url }) {
            if let tab = tabs[open.tabID] { selectedTabID = tab.id }
            focus(open.id)
            return
        }
        if let id = await splitBrowser(paneID, direction: .horizontal, url: url) { focus(id) }
    }

    public func splitSimulator(_ paneID: String, direction: SplitDirection, udid: String, before: Bool = false) async -> String? {
        await splitPane(paneID, direction: direction, before: before, spec: .with { $0.simulator = udid })
    }

    private func splitPane(_ paneID: String, direction: SplitDirection, before: Bool = false, spec: Hammock_V1_PaneSpec) async -> String? {
        splitting = paneID
        defer { splitting = nil }
        let id = await run {
            $0.createPane = .with {
                $0.splitPaneID = paneID
                $0.direction = direction
                $0.before = before
                $0.spec = spec
            }
        }?.created.id
        if let id { focus(id) } // a new split is where you're working
        return id
    }

    /// Moves a tab's panes, layout and all, next to a pane in another tab
    /// (side by side, or below), and shows that tab.
    public func mergeTab(_ tabID: String, into paneID: String, direction: SplitDirection = .horizontal) async {
        let target = panes[paneID]?.tabID
        guard await run({ $0.mergeTab = .with { $0.tabID = tabID; $0.intoPaneID = paneID; $0.direction = direction } }) != nil else { return }
        if let target { selectedTabID = target }
    }

    /// Turns a pane into another (a terminal, browser or simulator) in its
    /// place; the old one closes.
    @discardableResult
    public func replacePane(_ paneID: String, with spec: Hammock_V1_PaneSpec) async -> String? {
        let id = await run { $0.replacePane = .with { $0.paneID = paneID; $0.spec = spec } }?.created.id
        if let id { focus(id) }
        return id
    }

    /// Moves a pane out of its tab into a new tab of its own, and shows it.
    @discardableResult
    public func breakOut(_ paneID: String) async -> String? {
        let id = await run { $0.breakOutPane = .with { $0.paneID = paneID } }?.created.id
        if let id { selectedTabID = id }
        return id
    }

    /// Drives a browser pane: "load" (with url, as typed), "back", "forward",
    /// "reload" or "stop".
    public func navigate(_ paneID: String, _ action: String, url: String = "") {
        conn?.send { $0.browserNavigate = .with { $0.paneID = paneID; $0.action = action; $0.url = url } }
    }

    /// Each browser and simulator pane's agent's latest action there
    /// (AgentAction), for the pane to show it: a cursor or finger, a glow
    /// and a badge. Numbered, so the same action twice shows twice.
    public private(set) var agentActions: [String: AgentPresenceEvent] = [:]
    private var agentActionCount = 0

    /// Shows an agent's action on its pane (the daemon's, or a made-up one
    /// for HMK_DEBUG_AGENT_CURSOR).
    public func showAgentAction(_ a: AgentAction) {
        agentActionCount += 1
        agentActions[a.paneID] = AgentPresenceEvent(action: a, seq: agentActionCount)
    }

    /// Takes a browser or simulator pane over from agents, or hands it
    /// back: taken over, agents may look at it but not act on it.
    public func holdPane(_ paneID: String, _ held: Bool) {
        conn?.send { $0.holdPane = .with { $0.paneID = paneID; $0.held = held } }
    }

    /// Splits a pane: the new one after it (right, or below), or before it
    /// (left, or above) with before.
    @discardableResult
    public func split(_ paneID: String, direction: SplitDirection, before: Bool = false) async -> String? {
        let size = initialSize(splitting: paneID, direction: direction)
        splitting = paneID
        defer { splitting = nil }
        let id = await run {
            $0.createPane = .with {
                $0.splitPaneID = paneID
                $0.direction = direction
                $0.before = before
                if let s = size { $0.spec = .with { $0.cols = s.cols; $0.rows = s.rows } }
            }
        }?.created.id
        if let id { focus(id) } // a new split is where you're working
        return id
    }

    /// A new pane's likely grid, so its shell starts at the right size instead
    /// of being resized (and redrawing its prompt) once the view measures
    /// itself: the focused pane's size for a new tab, half of it for a split.
    func initialSize(splitting paneID: String? = nil, direction: SplitDirection = .horizontal) -> (cols: UInt32, rows: UInt32)? {
        guard let id = paneID ?? focusedPaneID, let d = sessions[id]?.desired else { return nil }
        var (cols, rows) = d
        if paneID != nil {
            if direction == .vertical { rows = max((rows - 1) / 2, 1) } else { cols = max((cols - 1) / 2, 2) }
        }
        return (UInt32(cols), UInt32(rows))
    }

    public func closePane(_ id: String) async { _ = await run { $0.closePane = .with { $0.paneID = id } } }
    /// Reopen Closed: the last tab or pane you closed (an agent in it
    /// resuming its session), selected.
    public func reopenClosed() async {
        if let id = await run({ $0.reopenClosed = .init() })?.created.id, !id.isEmpty {
            selectedTabID = id
        }
    }

    /// Closes a tab: it goes from the sidebar at once (the tab beside it
    /// selected, if it was), and comes back where it was if the daemon
    /// refuses.
    public func closeTab(_ id: String) async {
        guard let tab = tabs[id], let gi = groups.firstIndex(where: { $0.id == tab.groupID }),
              let i = groups[gi].tabIds.firstIndex(of: id)
        else {
            _ = await run { $0.closeTab = .with { $0.tabID = id } }
            return
        }
        closingTabs[id] = (tab, i)
        openingTabs[id] = nil
        let ids = groups[gi].tabIds
        let wasSelected = selectedTabID == id
        if wasSelected {
            selectedTabID = i + 1 < ids.count ? ids[i + 1] : i > 0 ? ids[i - 1] : nil
        }
        withAnimation(.snappy(duration: 0.25)) {
            groups[gi].tabIds.remove(at: i)
            tabs[id] = nil
        }
        // Its Group's last: stay in the Group, now empty.
        if wasSelected, selectedTabID == nil { selectGroup(tab.groupID) }
        if selectedTabID == nil, selectedEmptyGroup == nil { selectedTabID = groups.lazy.compactMap { $0.tabIds.first }.first }
        if await run({ $0.closeTab = .with { $0.tabID = id } }) != nil { return }
        // Refused (or the link dropped): back where it was.
        guard let closing = closingTabs.removeValue(forKey: id) else { return }
        withAnimation(.snappy(duration: 0.25)) {
            if tabs[id] == nil { tabs[id] = closing.tab }
            if let gi = groups.firstIndex(where: { $0.id == closing.tab.groupID }), !groups[gi].tabIds.contains(id) {
                groups[gi].tabIds.insert(id, at: min(closing.index, groups[gi].tabIds.count))
            }
        }
        if wasSelected, selectedTab == nil { selectedTabID = id }
        showToast("Couldn't close the tab: \(lastError ?? "unknown error")")
    }
    @discardableResult
    public func closeGroup(_ id: String) async -> Bool { await run { $0.closeGroup = .with { $0.groupID = id } } != nil }

    public func renameTab(_ id: String, _ name: String) async {
        _ = await run { $0.renameTab = .with { $0.tabID = id; $0.name = name } }
    }

    /// Sets a Group's emoji icon ("" restores the default).
    public func setGroupIcon(_ id: String, _ icon: String) async {
        _ = await run { $0.setGroupIcon = .with { $0.groupID = id; $0.icon = icon } }
    }

    public func renameGroup(_ id: String, _ name: String) async {
        _ = await run { $0.renameGroup = .with { $0.groupID = id; $0.name = name } }
    }

    /// Sets where a Group's new tabs open (a folder on its computer).
    public func setGroupFolder(_ id: String, _ folder: String) async {
        if await run({ $0.setGroupFolder = .with { $0.groupID = id; $0.folder = folder } }) == nil {
            showToast("Couldn't change the folder: \(lastError ?? "unknown error")")
        }
    }

    public func clearAttention(_ id: String) {
        conn?.send { $0.clearAttention_p = .with { $0.id = id } }
    }

    /// Tells the daemon these config keys were just written in Settings (each
    /// with its lines' values; none: removed), so it doesn't take a security
    /// setting you changed there for one changed behind your back.
    public func noteConfigWritten(_ entries: [(key: String, values: [String])]) {
        conn?.send { m in
            m.configWritten = .with { $0.entries = entries.map { e in .with { $0.key = e.key; $0.values = e.values } } }
        }
    }

    /// Selects the tab containing pane and focuses it.
    public func reveal(paneID: String) {
        guard let p = panes[paneID] else { return }
        selectedTabID = p.tabID
        focus(paneID)
    }
}

// MARK: Navigation

extension AppModel {
    /// All tabs in sidebar order.
    public var orderedTabs: [WorkspaceTab] { groups.flatMap { tabs(in: $0) } }

    /// Selects the nth tab (0-based) of the current Group.
    public func selectTab(at index: Int) {
        guard let group = currentGroup ?? groups.first else { return }
        let ts = tabs(in: group)
        guard index < ts.count else { return }
        selectedTabID = ts[index].id
    }

    /// Moves the selection through the tabs the sidebar shows (one
    /// computer's, when it shows one), wrapping.
    public func cycleTab(by delta: Int) {
        let all = shownGroups.flatMap { tabs(in: $0) }
        guard !all.isEmpty else { return }
        guard let i = all.firstIndex(where: { $0.id == selectedTabID }) else {
            // None of these is selected (it's on a computer not shown): the
            // first, going on; the last, going back.
            selectedTabID = (delta > 0 ? all.first : all.last)?.id
            return
        }
        selectedTabID = all[(i + delta + all.count) % all.count].id
    }

    /// Moves to the next (or previous) Group the sidebar shows, wrapping: to
    /// the tab it was last on, else its first; a Group without tabs, itself.
    public func cycleGroup(by delta: Int) {
        let all = shownGroups
        guard !all.isEmpty else { return }
        let current = currentGroup.flatMap { c in all.firstIndex { $0.id == c.id } }
        let next = current.map { all[($0 + delta + all.count) % all.count] } ?? (delta > 0 ? all.first : all.last)
        guard let g = next else { return }
        selectGroup(g.id)
    }

    /// Moves focus through the current tab's panes, wrapping.
    public func cyclePane(by delta: Int) {
        guard let tab = selectedTab else { return }
        let ids = Self.shownPaneIDs(of: tab)
        guard !ids.isEmpty else { return }
        let current = focusedPaneID.flatMap { ids.firstIndex(of: $0) } ?? ids.firstIndex(of: tab.focusedPaneID) ?? 0
        focus(ids[(current + delta + ids.count) % ids.count])
    }

    /// Creates an unnamed Group ("Group N") with one tab, and selects it.
    /// A new Group with a tab: on the computer the sidebar shows (this one
    /// when it shows them all), or on `computer`.
    public func newGroup(on computer: String? = nil) async {
        guard let id = await createGroup(name: "", on: computer ?? computerFilter) else { return }
        await createTab(in: id)
    }

    /// A Group on this Mac named after `folder`, its tabs opening there,
    /// with no tabs yet: selected, on its zero state.
    public func newGroup(inFolder folder: String) async {
        guard let id = await createGroup(name: (folder as NSString).lastPathComponent) else { return }
        await setGroupFolder(id, folder)
        for _ in 0..<50 where !groups.contains(where: { $0.id == id }) {
            try? await Task.sleep(for: .milliseconds(40))
        }
        selectGroup(id)
    }

    /// ⌘W, as in a browser: closes the focused pane; if it's the tab's only
    /// pane, the tab; if that's the Group's only tab, the tab and, unless
    /// `keepingGroups` (the Group stays, empty and selected), the Group.
    /// On an empty Group, closes it. Selection moves to the neighbouring tab.
    /// Returns false when there's nothing left to close, so the caller
    /// closes the window instead.
    public func closeCurrent(keepingGroups: Bool = true) async -> Bool {
        guard let tab = selectedTab, let group = group(of: tab) else {
            if let empty = selectedEmptyGroup {
                if empty.isRemote, !empty.isOnLinkedComputer {
                    showToast("“\(title(of: empty))” is shared with you: leave it from its menu.")
                } else if empty.offline {
                    showToast("\(title(of: empty))’s computer can't be reached right now.")
                } else {
                    await closeGroupSelectingNeighbour(empty.id)
                }
                return true
            }
            // Nothing selected: an empty Group is the next thing to close.
            if !keepingGroups, let empty = groups.first(where: { $0.tabIds.isEmpty }) {
                await closeGroup(empty.id)
                return true
            }
            return false
        }
        let paneIDs = Self.paneIDs(of: tab)
        if paneIDs.count > 1 {
            let focused = focusedPaneID.flatMap { paneIDs.contains($0) ? $0 : nil } ?? tab.focusedPaneID
            await closePane(focused)
            return true
        }
        if group.tabIds.count > 1 || keepingGroups {
            await closeTab(tab.id) // selects the tab beside it, or the Group it leaves empty
            return true
        }
        await closeGroupSelectingNeighbour(group.id)
        return true
    }

    /// Deletes a Group from a menu: the one you're on hands the selection to
    /// its neighbour first, as ⌘W does; another leaves it where it is.
    public func deleteGroup(_ id: String) async {
        if currentGroup?.id == id { await closeGroupSelectingNeighbour(id) } else { _ = await closeGroup(id) }
    }

    /// Closes a Group, first selecting the next Group's tab (or, with none,
    /// a Group without tabs): one the sidebar shows, else one on a computer
    /// it doesn't.
    func closeGroupSelectingNeighbour(_ id: String) async {
        let (wasTab, wasGroup) = (selectedTabID, selectedGroupID)
        let shown = shownGroups.contains { $0.id == id } ? shownGroups : groups
        if let j = shown.firstIndex(where: { $0.id == id }) {
            let shownIDs = Set(shown.map(\.id))
            let neighbours = shown[(j + 1)...].map(\.self) + shown[..<j].reversed() + groups.filter { !shownIDs.contains($0.id) }
            if let t = neighbours.lazy.compactMap({ $0.tabIds.first }).first {
                selectedTabID = t
            } else if let g = neighbours.first {
                selectGroup(g.id)
            } else {
                selectedTabID = nil
                selectedGroupID = nil
            }
        }
        if await closeGroup(id) { return }
        // Refused: back where you were.
        if let wasTab { selectedTabID = wasTab } else if let wasGroup { selectGroup(wasGroup) }
        showToast("Couldn't close the Group: \(lastError ?? "unknown error")")
    }
}

// Floating panes: over a tab's splits (Tab.floating; the daemon's floating.go).
extension AppModel {
    /// Opens a pane (a terminal, or from spec) floating over paneID's tab, and focuses it.
    @discardableResult
    public func newFloating(over paneID: String, spec: Hammock_V1_PaneSpec? = nil) async -> String? {
        let id = await run {
            $0.createPane = .with {
                $0.splitPaneID = paneID
                $0.floating = true
                if let spec { $0.spec = spec }
            }
        }?.created.id
        if let id { focus(id) }
        return id
    }

    /// Floats a split pane, or docks a floating one back where it came from.
    public func toggleFloating(_ paneID: String) {
        arrange(paneID, floating(paneID) == nil ? .float : .dock)
    }

    /// Whether a pane can float: it isn't its tab's only pane in the splits.
    public func canFloat(_ paneID: String) -> Bool {
        guard let t = panes[paneID].flatMap({ tabs[$0.tabID] }) else { return false }
        return floating(paneID) == nil && Self.paneIDs(t.layout).count > 1
    }

    /// Docks a floating pane beside target (direction, before).
    public func dock(_ paneID: String, beside target: String, direction: SplitDirection, before: Bool) {
        arrange(paneID, .dock) { $0.targetPaneID = target; $0.direction = direction; $0.before = before }
    }

    /// Moves or resizes a floating pane (fractions of its tab).
    public func setFloatFrame(_ paneID: String, _ frame: Hammock_V1_Frame) {
        updateFloat(paneID) { $0.frame = frame; $0.zoomed = false } // optimistic: no snap back
        arrange(paneID, .frame) { $0.frame = frame }
    }

    /// Minimises every float showing in a tab, or restores those that minimised.
    public func toggleFloats(_ tabID: String) {
        Task { _ = await run { $0.floatPane = .with { $0.action = .toggleAll; $0.tabID = tabID } } }
    }

    /// Floats, docks, minimises, restores, raises or zooms a pane.
    public func arrange(_ paneID: String, _ action: FloatAction, _ more: ((inout Hammock_V1_FloatPane) -> Void)? = nil) {
        switch action {
        case .minimise:
            updateFloat(paneID) { $0.minimised = true }
            if focusedPaneID == paneID, let t = panes[paneID].flatMap({ tabs[$0.tabID] }) {
                focusedPaneID = Self.shownPaneIDs(of: t).last { $0 != paneID }
            }
        case .restore, .raise:
            focusedPaneID = paneID
        case .zoom:
            // Here first, so the card grows (or shrinks) in the caller's
            // animation; the daemon's update confirms it.
            updateFloat(paneID) { $0.zoomed.toggle() }
        default: break
        }
        Task {
            _ = await run {
                $0.floatPane = .with {
                    $0.action = action
                    $0.paneID = paneID
                    more?(&$0)
                }
            }
        }
    }

    /// A pane's view took the keyboard. Not a pane on its tab's shelf: its
    /// card's view, on its way out, can take it as it's moved for the
    /// transition, and focusing it would bring it back off the shelf (a
    /// notification or Needs You reveals one with focus(_:) instead).
    public func viewFocused(_ paneID: String) {
        if floating(paneID)?.minimised == true { return }
        focus(paneID)
    }

    /// Moves focus off a pane (about to be minimised) to the pane its tab
    /// shows last, so its view gives up the keyboard before it goes.
    public func focusAway(from paneIDs: Set<String>) {
        guard let f = focusedPaneID ?? selectedTab?.focusedPaneID, paneIDs.contains(f),
              let t = panes[f].flatMap({ tabs[$0.tabID] }),
              let other = Self.shownPaneIDs(of: t).last(where: { !paneIDs.contains($0) }) else { return }
        focus(other)
    }

    /// Changes a float here ahead of the daemon's update (which confirms it).
    private func updateFloat(_ paneID: String, _ change: (inout FloatingPane) -> Void) {
        guard let tid = panes[paneID]?.tabID, var t = tabs[tid],
              let i = t.floating.firstIndex(where: { $0.paneID == paneID }) else { return }
        change(&t.floating[i])
        tabs[tid] = t
    }
}

extension AppModel {
    /// Replaces a tab's layout (same panes, new weights/arrangement).
    public func setLayout(_ tabID: String, _ layout: PaneLayout) {
        if var t = tabs[tabID] {
            t.layout = layout // optimistic; the daemon's update confirms it
            tabs[tabID] = t
        }
        Task { _ = await run { $0.setLayout = .with { $0.tabID = tabID; $0.layout = layout } } }
    }
}

extension AppModel {
    /// Answers an agent's permission request.
    public func respond(to grant: GrantRequest, _ decision: GrantDecision) {
        grants.removeAll { $0.id == grant.id }
        Task { _ = await run { $0.respondGrant = .with { $0.id = grant.id; $0.decision = decision } } }
    }

    /// Needs You's Allow / Deny. The daemon types it only while the pane
    /// still shows the agent's prompt, so a stale card types nothing; the
    /// card clears either way, and says so if nothing was asking.
    public func decide(_ item: AttentionItem, allow: Bool) {
        decide(item, allow ? .allow : .deny)
    }

    /// As decide(_:allow:), with any decision: .allowSchedule allows and
    /// remembers the tool for the pane's schedule (a scheduled Claude run).
    public func decide(_ item: AttentionItem, _ decision: Hammock_V1_AgentAnswer.Decision) {
        clearAttention(item.id)
        guard group(ofPane: item.paneID)?.canDrive != false, let conn else { return }
        let name = panes[item.paneID].map { $0.displayName.capitalized } ?? "The agent"
        Task {
            do {
                _ = try await conn.call { $0.agentAnswer = .with { $0.paneID = item.paneID; $0.decision = decision } }
            } catch DaemonError.server {
                // No prompt on screen, or it changed before the key went in.
                showToast("\(name) isn’t asking any more")
            } catch {
                showToast(error.localizedDescription)
            }
        }
    }

    /// Shows a short note for a few seconds.
    public func showToast(_ text: String) {
        toast = text
        toastTask?.cancel()
        toastTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(3))
            guard !Task.isCancelled else { return }
            self?.toast = nil
        }
    }
}

extension AppModel {
    /// Moves a tab to `index` within `groupID`.
    public func moveTab(_ tabID: String, to groupID: String, index: Int) {
        Task { _ = await run { $0.moveTab = .with { $0.tabID = tabID; $0.groupID = groupID; $0.index = Int32(index) } } }
    }
}

// MARK: Remote access & devices (local clients only)

public typealias Pairing = Hammock_V1_Pairing
public typealias Device = Hammock_V1_Device

extension AppModel {
    public func setRemoteAccess(_ on: Bool) async throws {
        guard let conn = connection else { throw DaemonError.notConnected }
        try await conn.call { $0.setRemoteAccess = .with { $0.enabled = on } }
        remoteEnabled = on
    }

    public func createPairing() async throws -> Pairing {
        guard let conn = connection else { throw DaemonError.notConnected }
        return try await conn.call { $0.createPairing = .init() }.pairing
    }

    public func devices() async throws -> [Device] {
        try await deviceStatus().devices
    }

    /// Paired devices, and what remote access found on this Mac's networks.
    public func deviceStatus() async throws -> Hammock_V1_Devices {
        guard let conn = connection else { throw DaemonError.notConnected }
        return try await conn.call { $0.listDevices = .init() }.devices
    }

    public func revoke(_ deviceID: String) async throws {
        guard let conn = connection else { throw DaemonError.notConnected }
        try await conn.call { $0.revokeDevice = .with { $0.deviceID = deviceID } }
    }
}

extension Hammock_V1_Pairing {
    /// The `hammock://pair/…` URL a device scans or opens.
    public var url: URL? {
        guard let data = try? serializedData() else { return nil }
        let b64 = data.base64EncodedString().replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "=", with: "")
        return URL(string: "hammock://pair/" + b64)
    }

    /// Parses a pairing URL or bare code.
    public init?(code: String) {
        var s = code.trimmingCharacters(in: .whitespacesAndNewlines)
        if let r = s.range(of: "pair/") { s = String(s[r.upperBound...]) }
        s = s.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        while s.count % 4 != 0 { s += "=" }
        guard let data = Data(base64Encoded: s), let p = try? Hammock_V1_Pairing(serializedBytes: data) else { return nil }
        self = p
    }

    /// The start of the Mac's certificate fingerprint, to check by eye:
    /// "AB12 CD34 EF56 7890".
    public var shortFingerprint: String { Self.shortFingerprint(certSha256) }

    public static func shortFingerprint(_ certSHA256: String) -> String {
        let hex = Array(certSHA256.uppercased().prefix(16))
        return stride(from: 0, to: hex.count, by: 4).map { String(hex[$0..<min($0 + 4, hex.count)]) }.joined(separator: " ")
    }
}

// MARK: Remote identity

import CryptoKit

/// How a remote client proves who it is: a device id plus its Ed25519 key,
/// or (first run) a pairing secret to register that key.
public struct RemoteIdentity: Sendable {
    /// The address that worked last, tried first.
    public var address: String
    /// Every address the Mac gave ("host:port"), and its local networks'
    /// subnets, which scope them (RemoteCandidates).
    public var addresses: [String]
    public var lanSubnets: [String]
    public var certSHA256: String
    public var deviceID: String?
    public var privateKey: Data            // Curve25519.Signing raw representation
    public var pairingSecret: Data?
    public var deviceName: String
    public var platform: String
    public init(address: String, addresses: [String] = [], lanSubnets: [String] = [], certSHA256: String, deviceID: String?,
                privateKey: Data, pairingSecret: Data?, deviceName: String, platform: String) {
        self.address = address; self.addresses = addresses; self.lanSubnets = lanSubnets
        self.certSHA256 = certSHA256; self.deviceID = deviceID
        self.privateKey = privateKey; self.pairingSecret = pairingSecret; self.deviceName = deviceName; self.platform = platform
    }
}

extension AppModel {
    /// Proves the device's identity after connecting remotely.
    func authenticateRemote(_ id: RemoteIdentity, nonce: Data) async throws {
        guard let conn = connection else { throw DaemonError.notConnected }
        let key = try Curve25519.Signing.PrivateKey(rawRepresentation: id.privateKey)
        if let deviceID = id.deviceID {
            guard nonce.count == 32 else { throw DaemonError.server("Bad challenge") }
            // Bound to its purpose and to this host's certificate, so the
            // signature can't be replayed anywhere else.
            var message = Data("hmk-device-auth-v1".utf8)
            message.append(0)
            message.append(Data(id.certSHA256.lowercased().utf8))
            message.append(0)
            message.append(nonce)
            let sig = try key.signature(for: message)
            try await conn.call { $0.authenticate = .with { $0.deviceID = deviceID; $0.signature = sig; $0.deviceName = id.deviceName } }
        } else if let secret = id.pairingSecret {
            let r = try await conn.call {
                $0.pair = .with {
                    $0.secret = secret
                    $0.deviceName = id.deviceName
                    $0.platform = id.platform
                    $0.publicKey = key.publicKey.rawRepresentation
                }
            }
            remoteIdentity?.deviceID = r.paired.deviceID
            remoteIdentity?.pairingSecret = nil
            onPaired?(r.paired.deviceID)
        } else {
            throw DaemonError.server("This device isn't paired")
        }
    }
}

extension AppModel {
    /// Checks a paired Mac's link still carries, and dials again if not
    /// (DaemonConnection.checkAlive): the iPhone, back in the foreground.
    public func checkConnection() {
        if connected { conn?.checkAlive() }
    }

    /// Disconnects for good (no reconnect).
    public func stop() {
        stopped = true
        connected = false
        connection?.disconnect()
    }
}

extension AppModel {
    /// Continues a pane's agent session in another harness (e.g. Claude → Codex).
    public func switchAgent(_ paneID: String, to harness: String) {
        Task {
            let name = AgentCatalog.shortName(of: harness)
            showToast("Switching to \(name)…")
            guard let r = await run({ $0.switchAgent = .with { $0.paneID = paneID; $0.targetHarness = harness } }) else {
                showToast("Couldn't switch to \(name): \(lastError ?? "unknown error")")
                return
            }
            // What came across (the carry-over report).
            if !r.agentSwitched.report.isEmpty { showToast(r.agentSwitched.report) }
        }
    }

    /// Agents' sessions on this Mac matching query (the
    /// daemon's index), best first; the most recent with no query. indexing:
    /// the first index is still being built, so ask again shortly.
    /// computerID: a linked computer's instead ("" for this one).
    /// groupID: only the sessions in the folders open in that Group.
    public func listSessions(_ query: String, limit: Int = 50, computerID: String = "", groupID: String = "") async -> (sessions: [SessionInfo], indexing: Bool)? {
        guard let r = await run({ $0.listSessions = .with { $0.query = query; $0.limit = Int32(limit); $0.computerID = computerID; $0.groupID = groupID } }) else { return nil }
        return (r.sessionList.sessions, r.sessionList.indexing)
    }

    /// A session's title (nil when it's only its first prompt) and the first
    /// prompt you sent in it, whole: what Schedule This starts from.
    public func sessionStart(harness: String, id: String) async -> (title: String?, prompt: String)? {
        guard !id.isEmpty, let r = await run({ $0.sessionExcerpts = .with { $0.harness = harness; $0.id = id; $0.firstPrompt = true } }),
              case .sessionExcerpt(let ex)? = r.msg else { return nil }
        let title = ex.session.title
        let named = title.isEmpty || title == ex.session.firstPrompt ? nil : title
        return (named, ex.messages.first?.text ?? "")
    }

    /// Resumes a session in a new tab (in its folder's Group, else groupID),
    /// in target if that's the other agent (converted), and goes to it; if
    /// it's already running in a pane, goes there.
    @discardableResult
    /// computerID: the linked computer the session is on ("" for this one).
    public func resumeSession(_ s: SessionInfo, in target: String? = nil, groupID: String?, computerID: String = "") async -> Hammock_V1_SessionResumed? {
        let name = AgentCatalog.shortName(of: target ?? s.harness)
        if target != nil { showToast("Converting to \(name)…") }
        guard let r = await run({
            $0.resumeSession = .with {
                $0.harness = s.harness
                $0.id = s.id
                $0.targetHarness = target ?? ""
                $0.groupID = groupID ?? ""
                $0.computerID = computerID
            }
        }) else {
            showToast("Couldn't resume it: \(lastError ?? "unknown error")")
            return nil
        }
        let res = r.sessionResumed
        if !res.tabID.isEmpty { selectedTabID = res.tabID }
        if !res.paneID.isEmpty { focusedPaneID = res.paneID }
        if !res.report.isEmpty { showToast(res.report) }
        return res
    }
}

extension AppModel {
    /// Tells the daemon the colours the terminal is drawn with, so programs
    /// asking for them (OSC 10/11, light/dark) get the truth. Re-sent on
    /// reconnect.
    public func setColors(_ t: TerminalTheme, dark: Bool) {
        let pack = { (c: RGB) in UInt32(c.r) << 16 | UInt32(c.g) << 8 | UInt32(c.b) }
        let msg = Hammock_V1_SetColors.with {
            $0.foreground = pack(t.foreground)
            $0.background = pack(t.background)
            $0.cursor = pack(t.cursor)
            $0.palette = t.ansi.map(pack)
            $0.dark = dark
        }
        guard msg != lastColors else { return }
        lastColors = msg
        conn?.send { $0.setColors = msg }
    }
}

extension AppModel {
    /// Asks the local daemon to exit (pane holders keep running unless
    /// endPanes: then what's running in every pane ends too).
    public func shutdownDaemon(endPanes: Bool = false) {
        conn?.send { $0.shutdownDaemon = .with { $0.endPanes = endPanes } }
    }
}

extension AppModel {
    /// Recent audit events (agent actions, permission decisions, remote
    /// access), newest first.
    public func listAudit(limit: Int = 200) async -> [Hammock_V1_AuditEvent] {
        await run { $0.listAudit = .with { $0.limit = Int32(limit) } }?.auditLog.events ?? []
    }
}

