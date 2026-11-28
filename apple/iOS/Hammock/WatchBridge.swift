import HammockKit
import os
import SwiftUI
import UIKit
import WatchConnectivity

/// The Apple Watch's way to your Macs: it asks this phone (WatchConnectivity),
/// and the phone answers from the Mac it shows, or a brief connection of its
/// own to another (as a notification's answer does). A message from the
/// watch wakes the app in the background long enough for that.
@MainActor
@Observable
final class WatchBridge: NSObject {
    static let shared = WatchBridge()
    @ObservationIgnored static let log = Logger(subsystem: "com.homegrownsw.hammock", category: "watch")

    /// The app's, set as it launches (HammockIOSApp).
    @ObservationIgnored var session: Session?

    /// Whether a watch with Hammock on it is paired (Settings shows its section).
    private(set) var watchAppInstalled = false

    /// Connections of their own to Macs not shown, kept a minute between
    /// the watch's asks (the list, then a conversation), then stopped.
    @ObservationIgnored private var brief: [String: (model: AppModel, used: Date)] = [:]
    /// Conversations the watch has open, kept watched a minute after it last asked.
    @ObservationIgnored private var watched: [String: (session: AgentSession, model: AppModel, pane: String, used: Date)] = [:]
    /// Each Mac's quick replies, as it last said.
    @ObservationIgnored private var quickReplies: [String: [String]] = [:]
    @ObservationIgnored private var sweeping = false
    @ObservationIgnored private var publishPending = false

    func start() {
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }

    // MARK: Answering the watch

    func handle(_ request: WatchRequest) async -> WatchReply {
        guard let session else { return .failed("Hammock on your iPhone isn't ready.") }
        if session.hosts.isEmpty && !session.isDemo { return .failed("Pair your Mac in Hammock on your iPhone first.") }
        let running = BackgroundTime("Answer the watch")
        defer {
            running.end()
            sweepSoon()
        }
        switch request {
        case .snapshot:
            return .snapshot(await snapshot())
        case let .conversation(host, pane, limit):
            guard let (model, h) = await model(for: host) else { return .failed(unreachable(host)) }
            let s = watch(model, host: host, pane: pane)
            // The conversation's first view, and older blocks if more are wanted.
            for _ in 0..<25 where !s.heard { try? await Task.sleep(for: .milliseconds(200)) }
            if WatchSnapshots.condense(s.blocks).count < limit, s.hasMore {
                s.loadOlder()
                for _ in 0..<15 where s.loadingOlder { try? await Task.sleep(for: .milliseconds(200)) }
            }
            let replies = await quickReplies(model, host: host)
            let header = WatchSnapshots.session(model, pane: pane, host: host, hostName: Self.name(h))
            return .conversation(Self.fitted(WatchSnapshots.conversation(s, session: header, limit: limit, quickReplies: replies)))
        case let .image(host, pane, ref):
            guard let (model, _) = await model(for: host) else { return .failed(unreachable(host)) }
            let s = watch(model, host: host, pane: pane)
            guard let data = await s.file(ref), let small = Self.shrink(data) else { return .failed("Couldn't load the picture.") }
            return .image(small)
        case let .send(host, pane, text, now):
            guard let (model, _) = await model(for: host) else { return .failed(unreachable(host)) }
            let s = watch(model, host: host, pane: pane)
            guard await s.send(text) else { return .failed(s.sendError ?? "Your message wasn't sent.") }
            if now, s.canSendNow, s.working { s.sendNow() }
            return .done
        case let .answer(host, pane, promptID, option, ticking, text):
            guard let (model, _) = await model(for: host) else { return .failed(unreachable(host)) }
            guard model.connected else { return .failed(unreachable(host)) }
            let s = watch(model, host: host, pane: pane)
            // The prompt it's for, on screen (it arrives with the conversation).
            for _ in 0..<25 where s.prompt.promptID != promptID { try? await Task.sleep(for: .milliseconds(200)) }
            guard s.prompt.promptID == promptID else { return .failed("That prompt isn't on the screen any more.") }
            if ticking.isEmpty {
                await s.answer(option: option, text: text)
            } else {
                await s.answer(ticking: ticking, text: text)
            }
            return s.answerError.map(WatchReply.failed) ?? .done
        case let .interrupt(host, pane):
            guard let (model, _) = await model(for: host) else { return .failed(unreachable(host)) }
            model.send(pane, Data([0x1B]))
            try? await Task.sleep(for: .milliseconds(300)) // let it go out
            return .done
        case let .notificationAction(action, sealed, text, delivered):
            return await Push.shared.answerFromWatch(action: action, sealed: sealed, text: text, delivered: delivered).map(WatchReply.failed) ?? .done
        case let .runSchedule(host, schedule):
            guard let (model, _) = await model(for: host) else { return .failed(unreachable(host)) }
            do {
                _ = try await model.runScheduleNow(schedule, reveal: false)
                return .done
            } catch {
                return .failed(error.localizedDescription)
            }
        }
    }

    /// Every paired Mac's sessions, usage and schedules (those that answer
    /// in a few seconds; the rest are named as unreachable).
    func snapshot() async -> WatchSnapshot {
        guard let session else { return WatchSnapshot() }
        let hosts = session.isDemo ? session.host.map { [$0] } ?? [] : session.hosts.filter { $0.deviceID != nil }
        // Asked afresh each time: they may have been edited on the Mac.
        quickReplies = [:]
        var out = WatchSnapshot(macs: hosts.map(Self.name))
        await withTaskGroup(of: (StoredHost, AppModel?).self) { group in
            for h in hosts {
                group.addTask { @MainActor in (h, await self.model(for: h.id)?.model) }
            }
            for await (h, model) in group {
                guard let model else {
                    out.unreachable.append(Self.name(h))
                    continue
                }
                out.sessions += WatchSnapshots.sessions(model, host: h.id, hostName: Self.name(h))
                out.schedules += WatchSnapshots.schedules(model, host: h.id, hostName: Self.name(h))
                // Usage is the agents' (the same account on each Mac): the shown Mac's, else the first.
                if out.usage.isEmpty || h.id == session.host?.id { out.usage = WatchSnapshots.usage(model) }
                if out.quickReplies.isEmpty || h.id == session.host?.id { out.quickReplies = await self.quickReplies(model, host: h.id) }
            }
        }
        out.sessions = WatchSnapshot.ordered(out.sessions)
        out.schedules.sort { $0.nextRun < $1.nextRun }
        out.schedules = Array(out.schedules.prefix(3))
        let theme = PhoneTheme.resolve(pref: UserDefaults.standard.string(forKey: PhoneTheme.prefKey) ?? "", host: session.model?.hostTheme)
        out.accent = Self.hex(theme.accent)
        out.bubble = Self.hex(theme.bubble)
        out.at = .now
        publish(out)
        return out
    }

    /// The latest snapshot, for the watch's complications and its list
    /// before it asks (WatchConnectivity keeps only the last).
    private func publish(_ s: WatchSnapshot) {
        guard WCSession.default.activationState == .activated, WCSession.default.isWatchAppInstalled,
              let data = try? WatchWire.encode(s) else { return }
        do {
            try WCSession.default.updateApplicationContext([WatchWire.snapshotKey: data])
        } catch {
            Self.log.error("Couldn't update the watch: \(error.localizedDescription, privacy: .public)")
        }
    }

    /// The shown Mac's agents changed state (RootView): the watch's
    /// complications follow, with the other Macs' as last seen. A couple
    /// of seconds on, once for a burst of changes.
    func shownMacChanged() {
        guard watchAppInstalled, !publishPending else { return }
        publishPending = true
        Task {
            try? await Task.sleep(for: .seconds(2))
            publishPending = false
            publishShownMac()
        }
    }

    private func publishShownMac() {
        guard let session, let model = session.model, let h = session.host, model.hasFullState,
              WCSession.default.activationState == .activated, WCSession.default.isWatchAppInstalled else { return }
        var s = (WCSession.default.applicationContext[WatchWire.snapshotKey] as? Data).flatMap { try? WatchWire.decode(WatchSnapshot.self, from: $0) }
            ?? WatchSnapshot(macs: session.hosts.map(Self.name))
        s.sessions = WatchSnapshot.ordered(s.sessions.filter { $0.host != h.id } + WatchSnapshots.sessions(model, host: h.id, hostName: Self.name(h)))
        s.at = .now
        publish(s)
    }

    // MARK: Connections

    /// A connected model for the Mac `host`, with its state: the one shown,
    /// else a brief connection (kept a minute). Nil if it can't be reached soon.
    private func model(for host: String) async -> (model: AppModel, host: StoredHost)? {
        guard let session else { return nil }
        if let m = session.model, let h = session.host, h.id == host {
            if !m.connected { m.checkConnection() }
            return await settled(m) ? (m, h) : nil
        }
        guard let h = session.hosts.first(where: { $0.id == host }) else { return nil }
        let m: AppModel
        if let b = brief[host] {
            m = b.model
        } else {
            guard let made = session.briefConnection(to: h) else { return nil }
            m = made
        }
        brief[host] = (m, .now)
        return await settled(m) ? (m, h) : nil
    }

    /// Waits (a few seconds at most) for a model to connect and have the Mac's state.
    private func settled(_ m: AppModel) async -> Bool {
        for _ in 0..<40 {
            if m.connected, m.hasFullState { return true }
            try? await Task.sleep(for: .milliseconds(200))
        }
        return m.connected && m.hasFullState
    }

    private func watch(_ model: AppModel, host: String, pane: String) -> AgentSession {
        let key = host + "/" + pane
        if let w = watched[key] {
            if w.model === model {
                watched[key]?.used = .now
                return w.session
            }
            w.model.releaseAgent(w.pane) // the Mac shown changed since
        }
        let s = model.acquireAgent(pane)
        watched[key] = (s, model, pane, .now)
        return s
    }

    private func quickReplies(_ model: AppModel, host: String) async -> [String] {
        if let q = quickReplies[host] { return q }
        // Not kept when it fails: asked again next time.
        guard let q = try? await model.quickReplies() else { return [] }
        quickReplies[host] = q
        return q
    }

    /// Lets go of conversations and connections the watch hasn't asked
    /// about for a minute.
    private func sweepSoon() {
        guard !sweeping else { return }
        sweeping = true
        Task {
            while !brief.isEmpty || !watched.isEmpty {
                try? await Task.sleep(for: .seconds(15))
                let stale = Date.now.addingTimeInterval(-60)
                for (k, w) in watched where w.used < stale {
                    w.model.releaseAgent(w.pane)
                    watched[k] = nil
                }
                for (k, b) in brief where b.used < stale && !watched.values.contains(where: { $0.model === b.model }) {
                    b.model.stop()
                    brief[k] = nil
                }
            }
            sweeping = false
        }
    }

    /// Quick replies changed (Settings): asked again next time.
    func forgetQuickReplies() { quickReplies = [:] }

    // MARK: Helpers

    /// The most a reply may hold: WatchConnectivity refuses a message over
    /// about 64 KB (and a binary plist keeps text with any non-ASCII in it
    /// as UTF-16, two bytes a character).
    static let replyBytes = 56 * 1024

    /// A conversation cut to fit one message: its oldest blocks go (and
    /// it says there are more) until it does.
    static func fitted(_ c: WatchConversation) -> WatchConversation {
        var c = c
        while c.blocks.count > 1, let data = try? WatchWire.encode(WatchReply.conversation(c)), data.count > replyBytes {
            c.blocks.removeFirst(max(1, c.blocks.count / 8))
            c.hasMore = true
        }
        return c
    }

    private func unreachable(_ host: String) -> String {
        "Couldn't reach \(session?.hosts.first { $0.id == host }.map(Self.name) ?? "your Mac")."
    }

    static func name(_ h: StoredHost) -> String {
        let n = h.hostName.replacingOccurrences(of: ".local", with: "")
        return n.isEmpty ? "Mac" : n
    }

    /// A colour as the watch draws it: dark (a watch face is black), 0xRRGGBB.
    static func hex(_ c: Color) -> UInt32 {
        let ui = UIColor(c).resolvedColor(with: UITraitCollection(userInterfaceStyle: .dark))
        var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
        ui.getRed(&r, green: &g, blue: &b, alpha: &a)
        func byte(_ v: CGFloat) -> UInt32 { UInt32(max(0, min(255, (v * 255).rounded()))) }
        return byte(r) << 16 | byte(g) << 8 | byte(b)
    }

    /// A picture made small enough for the watch and one message: its
    /// longest side WatchWire.imageSide, as a JPEG under WatchWire.imageBytes.
    nonisolated static func shrink(_ data: Data) -> Data? {
        guard let image = UIImage(data: data) else { return nil }
        let scale = min(1, WatchWire.imageSide / max(image.size.width * image.scale, image.size.height * image.scale))
        let size = CGSize(width: (image.size.width * image.scale * scale).rounded(), height: (image.size.height * image.scale * scale).rounded())
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        format.opaque = true
        let small = UIGraphicsImageRenderer(size: size, format: format).image { ctx in
            UIColor.black.setFill()
            ctx.fill(CGRect(origin: .zero, size: size))
            image.draw(in: CGRect(origin: .zero, size: size))
        }
        for q in [0.7, 0.5, 0.35, 0.2] {
            if let jpeg = small.jpegData(compressionQuality: q), jpeg.count <= WatchWire.imageBytes { return jpeg }
        }
        return nil
    }
}

extension WatchBridge: WCSessionDelegate {
    nonisolated func session(_ session: WCSession, activationDidCompleteWith state: WCSessionActivationState, error: Error?) {
        let installed = session.isWatchAppInstalled
        Task { @MainActor in self.watchAppInstalled = installed }
    }

    nonisolated func sessionWatchStateDidChange(_ session: WCSession) {
        let installed = session.isWatchAppInstalled
        Task { @MainActor in self.watchAppInstalled = installed }
    }

    nonisolated func sessionDidBecomeInactive(_ session: WCSession) {}

    /// Another watch was paired: activate again for it.
    nonisolated func sessionDidDeactivate(_ session: WCSession) { session.activate() }

    nonisolated func session(_ session: WCSession, didReceiveMessageData data: Data, replyHandler: @escaping (Data) -> Void) {
        nonisolated(unsafe) let reply = replyHandler
        guard let request = try? WatchWire.decode(WatchRequest.self, from: data) else {
            reply((try? WatchWire.encode(WatchReply.failed("Update Hammock on your iPhone."))) ?? Data())
            return
        }
        Task { @MainActor in
            let answer = await self.handle(request)
            reply((try? WatchWire.encode(answer)) ?? Data())
        }
    }
}

extension WatchBridge {
    /// The quick replies, as the Mac shown keeps them.
    func loadQuickReplies() async throws -> [String] {
        guard let session, let h = session.host, let (model, _) = await model(for: h.id) else {
            throw QuickRepliesError.unreachable(session?.hostShortName ?? "your Mac")
        }
        let q = try await model.quickReplies()
        quickReplies[h.id] = q
        return q
    }

    /// Saves the quick replies to every paired Mac (each keeps its own
    /// copy, in its config). The names of those that couldn't be reached.
    func saveQuickReplies(_ replies: [String]) async -> [String] {
        guard let session else { return [] }
        var failed: [String] = []
        await withTaskGroup(of: (String, Bool).self) { group in
            for h in session.hosts where h.deviceID != nil {
                group.addTask { @MainActor in
                    guard let (model, _) = await self.model(for: h.id),
                          let saved = try? await model.quickReplies(setting: replies) else { return (Self.name(h), false) }
                    self.quickReplies[h.id] = saved
                    return (Self.name(h), true)
                }
            }
            for await (name, ok) in group where !ok { failed.append(name) }
        }
        sweepSoon()
        return failed.sorted()
    }
}

enum QuickRepliesError: LocalizedError {
    case unreachable(String)
    var errorDescription: String? {
        switch self {
        case .unreachable(let mac): "Couldn't reach \(mac)."
        }
    }
}

/// Settings ▸ Apple Watch ▸ Quick Replies: the replies the watch offers
/// agents in one tap, kept on each of your Macs.
struct QuickRepliesScreen: View {
    @State private var replies: [String] = []
    @State private var loaded = false
    @State private var error: String?
    @State private var saving = false
    @State private var adding = ""
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        Form {
            if let error {
                Section { Text(error).foregroundStyle(.red) }
            }
            Section {
                ForEach(replies.indices, id: \.self) { i in
                    TextField("Reply", text: Binding(get: { replies.indices.contains(i) ? replies[i] : "" }, set: { replies[i] = $0 }))
                        .submitLabel(.done)
                        .onSubmit { save() }
                }
                .onDelete { replies.remove(atOffsets: $0); save() }
                .onMove { replies.move(fromOffsets: $0, toOffset: $1); save() }
                TextField("New Reply", text: $adding)
                    .submitLabel(.done)
                    .onSubmit {
                        let t = adding.trimmingCharacters(in: .whitespacesAndNewlines)
                        guard !t.isEmpty else { return }
                        replies.append(t)
                        adding = ""
                        save()
                    }
            }
            .disabled(!loaded)
        }
        .themedScreenBackground()
        .navigationTitle("Quick Replies")
        .toolbar {
            if saving { ToolbarItem(placement: .topBarTrailing) { ProgressView() } }
            ToolbarItem(placement: .topBarTrailing) { EditButton().tint(theme.accent) }
        }
        .task {
            do {
                replies = try await WatchBridge.shared.loadQuickReplies()
                loaded = true
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private func save() {
        let list = replies.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty }
        saving = true
        Task {
            let failed = await WatchBridge.shared.saveQuickReplies(list)
            saving = false
            error = failed.isEmpty ? nil : "Couldn't save to \(ListFormatter.localizedString(byJoining: failed)). Try again when it's on."
        }
    }
}
