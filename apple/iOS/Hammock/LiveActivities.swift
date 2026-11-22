import ActivityKit
import Foundation
import HammockKit
import os
import UIKit

/// Live Activities (Settings ▸ Notifications): what your agents are doing,
/// on the Lock Screen and in the Dynamic Island, while you're away from
/// your Mac. The Mac decides when one shows and what it says
/// (internal/daemon/live.go): it starts one by push with this phone's
/// push-to-start token and updates it with the activity's own token, both
/// told to it here (RegisterPush); with Hammock open, the app starts and
/// updates its own from what the Mac says (AppModel.liveActivity).
/// Off unless turned on: the pushes go through the relay in plain text.
@MainActor
@Observable
final class LiveActivities {
    static let shared = LiveActivities()
    static let prefKey = "live.enabled"
    static let log = Logger(subsystem: "com.homegrownsw.hammock", category: "live")

    private(set) var enabled = UserDefaults.standard.bool(forKey: prefKey)
    /// Activities whose tokens are being watched, by id.
    private var watched: Set<String> = []

    /// This phone's push-to-start token (hex), once iOS has given one.
    var startToken: String? { UserDefaults.standard.string(forKey: "live.startToken") }

    /// The update token of the activity a Mac started, while it runs.
    func token(for host: String) -> String? { UserDefaults.standard.string(forKey: "live.token." + host.lowercased()) }

    private func setToken(_ token: String?, for host: String) {
        UserDefaults.standard.set(token, forKey: "live.token." + host.lowercased())
    }

    /// As the app launches: follow iOS's tokens and the activities running.
    func start() {
        LiveAnswerIntent.answer = { host, pane, item, prompt, allow in
            await LiveActivities.shared.answer(host: host, pane: pane, item: item, prompt: prompt, allow: allow)
        }
        Task {
            for await data in Activity<HammockActivityAttributes>.pushToStartTokenUpdates {
                let hex = data.map { String(format: "%02x", $0) }.joined()
                guard hex != startToken else { continue }
                UserDefaults.standard.set(hex, forKey: "live.startToken")
                if enabled { await Push.shared.registerWithMac() }
            }
        }
        for a in Activity<HammockActivityAttributes>.activities { watch(a) }
        #if DEBUG
        showSample()
        #endif
        Task {
            for await a in Activity<HammockActivityAttributes>.activityUpdates { watch(a) }
        }
    }

    func setEnabled(_ on: Bool) {
        enabled = on
        UserDefaults.standard.set(on, forKey: Self.prefKey)
        if !on {
            Task {
                for a in Activity<HammockActivityAttributes>.activities {
                    setToken(nil, for: a.attributes.host)
                    await a.end(nil, dismissalPolicy: .immediate)
                }
            }
        }
        Task { await Push.shared.registerWithMac() }
    }

    /// Follows an activity: its update token goes to its Mac, and when it
    /// ends (or you dismiss it), the Mac's told it's gone.
    private func watch(_ a: Activity<HammockActivityAttributes>) {
        guard watched.insert(a.id).inserted else { return }
        let host = a.attributes.host
        Task {
            for await data in a.pushTokenUpdates {
                let hex = data.map { String(format: "%02x", $0) }.joined()
                guard hex != token(for: host) else { continue }
                setToken(hex, for: host)
                await tell(host)
            }
        }
        Task {
            for await state in a.activityStateUpdates where state == .ended || state == .dismissed {
                watched.remove(a.id)
                // Another from the same Mac may run now (one started since).
                let others = Activity<HammockActivityAttributes>.activities.filter { $0.id != a.id && $0.attributes.host == host && $0.activityState == .active }
                if others.isEmpty {
                    setToken(nil, for: host)
                    await tell(host)
                }
                break
            }
        }
    }

    /// Tells a Mac this phone's tokens: over the app's connection if it's
    /// the Mac shown, else a brief one of its own (the app may have been
    /// woken in the background as the Mac started an activity by push).
    private func tell(_ host: String) async {
        guard let session = Push.shared.session else { return }
        if session.host?.id.lowercased() == host.lowercased(), let m = session.model, m.connected {
            await Push.shared.register(m, host: host)
            return
        }
        guard let h = session.hosts.first(where: { $0.id.lowercased() == host.lowercased() }) else { return }
        let running = BackgroundTime("Tell the Mac about a Live Activity")
        defer { running.end() }
        guard let m = session.briefConnection(to: h) else { return }
        defer { m.stop() }
        for _ in 0..<50 where !m.connected { try? await Task.sleep(for: .milliseconds(200)) }
        if m.connected { await Push.shared.register(m, host: h.id) }
    }

    /// With Hammock open: starts, updates or ends this Mac's activity as it
    /// says (AppModel.liveActivity).
    func apply(_ content: LiveContent?, host: String, mac: String) {
        guard enabled, ActivityAuthorizationInfo().areActivitiesEnabled || content == nil else { return }
        let running = Activity<HammockActivityAttributes>.activities.filter { $0.attributes.host.lowercased() == host.lowercased() && $0.activityState == .active }
        Task {
            guard let content else {
                for a in running { await a.end(nil, dismissalPolicy: .immediate) }
                return
            }
            if let a = running.first {
                for extra in running.dropFirst() { await extra.end(nil, dismissalPolicy: .immediate) }
                if a.content.state != content { await a.update(ActivityContent(state: content, staleDate: nil)) }
                return
            }
            // Started only from the front (iOS's rule); the Mac starts it by push otherwise.
            guard UIApplication.shared.applicationState == .active else { return }
            do {
                let a = try Activity.request(attributes: HammockActivityAttributes(host: host, mac: mac),
                                             content: ActivityContent(state: content, staleDate: nil), pushType: .token)
                watch(a)
            } catch {
                Self.log.error("Couldn't start a Live Activity: \(error.localizedDescription, privacy: .public)")
            }
        }
    }

    /// Allow or Deny on the activity (LiveAnswerIntent, once unlocked): as
    /// a notification's, through the Mac it's from. If the Mac pressed
    /// nothing (the prompt changed), the activity says so instead of
    /// offering the buttons; if it never got there, they stay to try again.
    func answer(host: String, pane: String, item: String, prompt: String, allow: Bool) async {
        guard let session = Push.shared.session else { return }
        let running = BackgroundTime("Answer from a Live Activity")
        defer { running.end() }
        var brief: AppModel?
        defer { brief?.stop() }
        var model = session.host?.id.lowercased() == host.lowercased() ? session.model : nil
        if model?.connected != true, let h = session.hosts.first(where: { $0.id.lowercased() == host.lowercased() }) {
            brief = session.briefConnection(to: h)
            model = brief
        }
        guard let model else { return }
        for _ in 0..<50 where !model.connected { try? await Task.sleep(for: .milliseconds(200)) }
        let result = await model.answerPrompt(pane: pane, item: item, prompt: prompt, allow: allow)
        guard let a = Activity<HammockActivityAttributes>.activities.first(where: { $0.attributes.host.lowercased() == host.lowercased() }) else { return }
        var c = a.content.state
        guard c.prompt == prompt else { return } // moved on meanwhile
        switch result {
        case .answered:
            c.prompt = nil
            c.state = "working"
            c.status = "Working"
            c.doing = allow ? "Allowed" : "Denied"
        case .refused:
            c.prompt = nil
            c.doing = AgentPromptNotice.couldntAnswer
        case .unsent:
            Self.log.error("Couldn't reach the Mac to answer from a Live Activity")
            return
        }
        await a.update(ActivityContent(state: c, staleDate: nil))
        try? await Task.sleep(for: .milliseconds(500)) // let the answer go out
    }

    #if DEBUG
    /// Dev aid: HMK_DEBUG_LIVE_ACTIVITY=working|needs_you|finished starts a
    /// Live Activity with made-up content (and fills the Needs You widget),
    /// to look at on the Lock Screen and in the Dynamic Island.
    private func showSample() {
        guard let kind = ProcessInfo.processInfo.environment["HMK_DEBUG_LIVE_ACTIVITY"] else { return }
        let now = Int(Date().timeIntervalSince1970)
        var c = LiveContent(agent: "Claude", harness: "claude", tab: "Map Judder", group: "Atlas", groupIcon: "🗺", place: "🗺 Atlas · Map Judder",
                            state: "working", status: "Working", doing: "Editing AppModel.swift", since: now - 754, ctx: 42, pane: "pn_demo",
                            more: "+2 more working", others: 2, waiting: 1)
        switch kind {
        case "needs_you":
            c.state = "needs_you"
            c.status = "Needs you"
            c.doing = "Allow Bash: npm test?"
            c.prompt = "pr_demo"
            c.item = "at_pn_demo"
            c.since = now - 95
        case "finished":
            c.state = "finished"
            c.status = "Finished"
            c.doing = "Done: the map no longer judders when you zoom."
            c.more = nil
            c.others = 0
        default: break
        }
        NeedsYouStore.update { _ in
            NeedsYouSnapshot(items: [
                NeedsYouItem(id: "at_1", host: "demo", pane: "pn_1", title: "Map Judder", place: "🗺 Atlas", ask: "Allow Bash: npm test?", at: Double(now - 95)),
                NeedsYouItem(id: "at_2", host: "demo", pane: "pn_2", title: "api", place: "Web", ask: "Claude asks: Which database should I use?", at: Double(now - 30)),
            ], badge: 2)
        }
        // Once the app is in front: iOS starts one only from there.
        Task {
            try? await Task.sleep(for: .seconds(2))
            do {
                _ = try Activity.request(attributes: HammockActivityAttributes(host: "demo", mac: "Studio"),
                                         content: ActivityContent(state: c, staleDate: nil), pushType: nil)
            } catch {
                Self.log.error("Sample Live Activity: \(error.localizedDescription, privacy: .public)")
            }
        }
    }
    #endif
}
