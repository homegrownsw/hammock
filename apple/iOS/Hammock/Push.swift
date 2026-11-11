import HammockKit
import Foundation
import Security
import UIKit
import UserNotifications

/// Push notifications from your Macs while you're away from them: this
/// phone registers with the relay (which alone holds the APNs key) and
/// gives each Mac only its relay id and a key to encrypt with.
@MainActor
@Observable
final class Push: NSObject {
    static let shared = Push()

    /// The relay, set at build time (HMK_PUSH_RELAY); empty: no push.
    let relayURL = (Bundle.main.object(forInfoDictionaryKey: "HammockPushRelay") as? String ?? "").trimmingCharacters(in: .whitespaces)
    private(set) var status = "Off"
    weak var session: Session?
    /// A notification opened: the pane to show.
    var openPane: String?
    /// A notification about a permission or a join opened: Sessions' Needs
    /// You, where it's answered (not the agent's pane, which doesn't show it).
    var openNeedsYou = false
    /// A Hammock Key request whose notification's Approve was chosen: its
    /// sheet approves once straight away (Face ID first).
    var approveKey: String?
    /// Hammock Key requests whose sheet you closed without answering (the Mac
    /// still asks; it isn't shown again).
    var keyDismissed: Set<String> = []
    /// The Hammock Key request whose sheet is on screen.
    var keyShown: String?
    /// The pane on screen now (PaneScreen), and the Mac it's on: its own
    /// notifications aren't shown while it is.
    var visiblePane: (host: String, pane: String)?

    static let events = [("join", "Someone asks to join"), ("needs_input", "An agent needs you"), ("finished", "An agent finishes"), ("errored", "An agent hits an error")]
    static func prefKey(_ event: String) -> String { "push." + event }

    private var token: String? { UserDefaults.standard.string(forKey: "push.token") }
    private var relayID: String? { RelayCredentials.load()?.id }

    func start() {
        guard !relayURL.isEmpty else { status = "Not set up in this build"; return }
        let center = UNUserNotificationCenter.current()
        center.delegate = self
        setCategories()
        Task {
            let granted = (try? await center.requestAuthorization(options: [.alert, .sound, .badge])) ?? false
            status = granted ? "Registering…" : "Notifications are off for Hammock in iOS Settings"
            if granted { UIApplication.shared.registerForRemoteNotifications() }
        }
    }

    /// How often the phone registers again with the token it has: the relay
    /// forgets a phone nothing has sent to (or registered) for 60 days, and
    /// one always at its Mac gets no notifications to keep it.
    static let refreshEvery: TimeInterval = 7 * 24 * 60 * 60

    /// iOS gave us a device token (each launch): register it with the relay
    /// if it changed, or a week on (so the relay keeps it), then with the
    /// Mac being shown.
    func gotToken(_ data: Data) {
        let hex = data.map { String(format: "%02x", $0) }.joined()
        Task {
            let had = RelayCredentials.load()
            let registered = UserDefaults.standard.object(forKey: "push.registered") as? Date ?? .distantPast
            if hex != token || had == nil || Date().timeIntervalSince(registered) > Self.refreshEvery {
                do {
                    // With the id it has, given its secret, it keeps that id
                    // (a new token, or the app reinstalled).
                    var body: [String: Any] = ["token": hex, "sandbox": Self.sandbox]
                    if let had { body["id"] = had.id; body["secret"] = had.secret }
                    let r: [String: String] = try await relay("register", body)
                    guard let id = r["id"], let secret = r["secret"] else { throw URLError(.badServerResponse) }
                    RelayCredentials.save(.init(id: id, secret: secret))
                    UserDefaults.standard.set(hex, forKey: "push.token")
                    UserDefaults.standard.set(Date(), forKey: "push.registered")
                } catch {
                    // Only a week's refresh: the id it has still works (or
                    // will be refreshed next launch).
                    if hex != token || had == nil {
                        status = "Couldn't reach the push relay"
                        return
                    }
                }
            }
            status = "On"
            await forgetOldID()
            await registerWithMac()
        }
    }

    /// The id and secret this phone kept in UserDefaults before the Keychain:
    /// in backups, so another phone restored from one could take this one's
    /// id over. This phone registered afresh instead; the old id is
    /// unregistered (its secret proves it's ours to drop), so a Mac still
    /// sending to it is told it's gone and gets the new one when it next
    /// connects, instead of the old row living on. Kept until the relay has
    /// answered, to try again next launch if it couldn't be reached.
    private func forgetOldID() async {
        let d = UserDefaults.standard
        guard let id = d.string(forKey: "push.relayID") else { return }
        if let secret = d.string(forKey: "push.relaySecret"), id != relayID {
            var req = URLRequest(url: URL(string: relayURL)!.appendingPathComponent("unregister"))
            req.httpMethod = "POST"
            req.setValue("application/json", forHTTPHeaderField: "content-type")
            req.httpBody = try? JSONSerialization.data(withJSONObject: ["id": id, "secret": secret])
            // Done (200), or already gone (404); otherwise again next time.
            guard let (_, resp) = try? await URLSession.shared.data(for: req),
                  let code = (resp as? HTTPURLResponse)?.statusCode, code == 200 || code == 404 else { return }
        }
        d.removeObject(forKey: "push.relayID")
        d.removeObject(forKey: "push.relaySecret")
    }

    /// The answers notifications offer. Each one that answers needs the
    /// phone unlocked (whoever holds a locked phone mustn't let someone
    /// into a Group or allow an agent anything); with Settings ▸ Privacy ▸
    /// Require Face ID on, they open the app too, whose lock asks first
    /// (AppLock: set again when it's turned on or off).
    func setCategories() {
        let lock: UNNotificationActionOptions = UserDefaults.standard.bool(forKey: AppLock.key) ? [.authenticationRequired, .foreground] : .authenticationRequired
        // Driving is decided in the app, where the join card shows their key.
        let drive = UNNotificationAction(identifier: "join.drive", title: "Let Drive…", options: .foreground)
        let view = UNNotificationAction(identifier: "join.view", title: "Let View", options: lock)
        let decline = UNNotificationAction(identifier: "join.decline", title: "Decline", options: lock.union(.destructive))
        let allow = UNNotificationAction(identifier: "grant.allow", title: "Allow", options: lock)
        let deny = UNNotificationAction(identifier: "grant.deny", title: "Deny", options: lock.union(.destructive))
        // The Hammock Key: approving signs with this phone's approval
        // key, behind Face ID, so it opens the app.
        let keyApprove = UNNotificationAction(identifier: "key.approve", title: "Approve…", options: [.foreground, .authenticationRequired])
        let keyDeny = UNNotificationAction(identifier: "key.deny", title: "Don't Allow", options: lock.union(.destructive))
        UNUserNotificationCenter.current().setNotificationCategories([
            UNNotificationCategory(identifier: "join", actions: [drive, view, decline], intentIdentifiers: []),
            UNNotificationCategory(identifier: "grant", actions: [allow, deny], intentIdentifiers: []),
            UNNotificationCategory(identifier: "hammockkey", actions: [keyApprove, keyDeny], intentIdentifiers: []),
        ])
    }

    /// The app's own lock (Require Face ID) would ask before showing it now:
    /// a notification's answer waits for it, in the app. Its own state, not
    /// whether the app is active (AppLock.open).
    private var appLocked: Bool { AppLock.locked }

    func failed(_ error: Error) { status = "iOS refused: \(error.localizedDescription)" }

    /// Tells the Mac being shown where to push (or, with no events, not to).
    func registerWithMac() async {
        guard let model = session?.model, let host = session?.host, model.connected, let relayID else { return }
        let events = Self.events.map(\.0).filter { UserDefaults.standard.object(forKey: Self.prefKey($0)) as? Bool ?? true }
        try? await model.registerPush(relayURL: relayURL, relayID: relayID, key: PushKeys.key(for: host.id), events: events)
    }

    /// Debug and ad hoc builds use APNs' sandbox (their provisioning profile says so).
    private static var sandbox: Bool {
        guard let url = Bundle.main.url(forResource: "embedded", withExtension: "mobileprovision"),
              let data = try? Data(contentsOf: url), let s = String(data: data, encoding: .isoLatin1) else { return false }
        return s.range(of: #"<key>aps-environment</key>\s*<string>development</string>"#, options: .regularExpression) != nil
    }

    private func relay<T: Decodable>(_ path: String, _ body: [String: Any]) async throws -> T {
        var req = URLRequest(url: URL(string: relayURL)!.appendingPathComponent(path))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "content-type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
        return try JSONDecoder().decode(T.self, from: data)
    }
}

extension Push: UNUserNotificationCenterDelegate {
    // The completion-handler forms, called back on the main thread: iOS
    // asserts that (and aborts the app), and the async forms, nonisolated,
    // finish on a background thread.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                            withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        nonisolated(unsafe) let done = completionHandler
        let info = PushKeys.note(notification.request.content.userInfo)
        let pane = info?["pane"] as? String, host = info?["host"] as? String
        let answered = info?["grant"] != nil || info?["join"] != nil || info?["key"] != nil
        DispatchQueue.main.async {
            // About the pane you're looking at: you can see it already. A
            // permission or a join is answered elsewhere, so still show it.
            if !answered, let pane, let v = MainActor.assumeIsolated({ Push.shared.visiblePane }), v.pane == pane, host == nil || v.host.isEmpty || host == v.host {
                done([])
            } else {
                done([.banner, .sound, .list])
            }
        }
    }

    /// A notification tapped, or one of its actions chosen.
    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                            withCompletionHandler completionHandler: @escaping () -> Void) {
        nonisolated(unsafe) let done = completionHandler
        // Trust only the note that decrypts with a Mac's key and names that
        // Mac, and was fresh when it arrived (the extension drops a replayed
        // one's note, but iOS shows the original if the extension didn't
        // run). By the delivery date: one tapped hours later still opens.
        guard let info = PushKeys.note(response.notification.request.content.userInfo),
              PushKeys.fresh(info, now: response.notification.date) else {
            DispatchQueue.main.async { done() }
            return
        }
        let tapped = Tapped(action: response.actionIdentifier, join: info["join"] as? String, grant: info["grant"] as? String,
                            key: info["key"] as? String, pane: info["pane"] as? String, host: info["host"] as? String)
        Task { @MainActor in
            await self.handle(tapped)
            done()
        }
    }
}

/// What a tapped notification asks for.
private struct Tapped: Sendable {
    let action: String
    let join: String?, grant: String?, key: String?, pane: String?, host: String?
}

extension Push {
    fileprivate func handle(_ t: Tapped) async {
        guard let session else { return }
        if let host = t.host, host != session.host?.id, let h = session.hosts.first(where: { $0.id == host }) { session.show(h) }
        if let key = t.key {
            // Its sheet shows while it waits (RootView); Approve approves at once.
            keyDismissed.remove(key)
            if t.action == "key.approve" { approveKey = key }
        }
        if t.action == UNNotificationDefaultActionIdentifier, t.key == nil {
            if t.grant != nil || t.join != nil { openNeedsYou = true } else { openPane = t.pane }
        }
        guard t.action != UNNotificationDefaultActionIdentifier, t.action != UNNotificationDismissActionIdentifier,
              t.action != "join.drive", t.action != "key.approve" else { return }
        // Behind the app's own lock: answered in Needs You once it's unlocked
        // (the action opened the app, which asks).
        if appLocked {
            if t.grant != nil || t.join != nil { openNeedsYou = true }
            return
        }
        // Answer from the notification: wait (briefly) for the Mac.
        for _ in 0..<50 {
            if self.session?.model?.connected == true { break }
            try? await Task.sleep(for: .milliseconds(200))
        }
        guard let model = self.session?.model else { return }
        if let join = t.join, let req = model.joinRequests.first(where: { $0.id == join }) {
            model.respond(to: req, role: t.action == "join.view" ? .viewer : nil)
        }
        if let grant = t.grant, let g = model.grants.first(where: { $0.id == grant }) {
            model.respond(to: g, t.action == "grant.allow" ? .allowOnce : .deny)
        }
        if let key = t.key, t.action == "key.deny", let r = model.hammockKeyRequests.first(where: { $0.id == key }) {
            model.denyHammockKey(r)
        }
        try? await Task.sleep(for: .milliseconds(500)) // let the answer go out
    }
}

extension Push {
    /// What these were about (on the Mac `host`) is dealt with, as the Mac
    /// being shown says: their notifications go from Notification Center.
    func withdraw(_ ids: [String], host: String) {
        let ids = Set(ids)
        Task {
            let gone = await DeliveredNotes.from(host).filter { ids.contains($0.note.id) }
            DeliveredNotes.remove(gone)
        }
    }

    /// After connecting: notifications about what's no longer live on this
    /// Mac go, including ones whose clear never came (iOS holds silent
    /// pushes back, and doesn't wake an app that was swiped away).
    func reconcile(_ model: AppModel, host: String) {
        Task {
            let delivered = await DeliveredNotes.from(host)
            // Read after: something raised while iOS answered is live.
            let live = model.liveNoticeIDs
            DeliveredNotes.remove(delivered.filter { $0.note.live && !live.contains($0.note.id) })
        }
    }
}

/// Notifications in Notification Center, by what their note says.
enum DeliveredNotes {
    struct Note: Sendable {
        let id: String
        /// About something that's resolved (the Mac marks it), not a
        /// scheduled run's result, which stays until you clear it.
        let live: Bool
        /// When the Mac sent it (by its clock).
        let at: Double?
    }

    /// Those from one Mac (by its certificate fingerprint).
    static func from(_ host: String) async -> [(identifier: String, note: Note)] {
        await UNUserNotificationCenter.current().deliveredNotifications().compactMap { n in
            guard let note = PushKeys.note(n.request.content.userInfo), let id = note["id"] as? String, !id.isEmpty,
                  (note["host"] as? String)?.lowercased() == host.lowercased() else { return nil }
            return (n.request.identifier, Note(id: id, live: note["live"] as? Bool ?? false, at: (note["at"] as? NSNumber)?.doubleValue))
        }
    }

    static func remove(_ these: [(identifier: String, note: Note)]) {
        guard !these.isEmpty else { return }
        UNUserNotificationCenter.current().removeDeliveredNotifications(withIdentifiers: these.map(\.identifier))
    }

    /// A silent push from a Mac saying what a notification was about is
    /// dealt with (answered, seen or withdrawn, wherever): it goes. Only
    /// one sent before the clear: the same thing can need you again since.
    static func clear(_ userInfo: [AnyHashable: Any]) async -> Bool {
        guard let note = PushKeys.note(userInfo), note["clear"] as? Bool == true, PushKeys.fresh(note),
              let id = note["id"] as? String, !id.isEmpty, let host = note["host"] as? String else { return false }
        let at = (note["at"] as? NSNumber)?.doubleValue
        let gone = await from(host).filter { d in
            guard d.note.id == id else { return false }
            guard let at, let sent = d.note.at else { return true }
            return sent <= at
        }
        remove(gone)
        return !gone.isEmpty
    }
}

/// This phone's relay id and the secret that proves it, in the Keychain for
/// this device only: kept through a reinstall (so it keeps its id), never
/// restored onto another phone from a backup (which would take this one's
/// id, and its notifications, over).
private enum RelayCredentials {
    struct Value: Codable { var id: String; var secret: String }

    private static let query: [String: Any] = [
        kSecClass as String: kSecClassGenericPassword,
        kSecAttrService as String: "com.homegrownsw.hammock.push",
        kSecAttrAccount as String: "relay",
    ]

    static func load() -> Value? {
        var q = query
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else { return nil }
        return try? JSONDecoder().decode(Value.self, from: data)
    }

    static func save(_ v: Value) {
        guard let data = try? JSONEncoder().encode(v) else { return }
        SecItemDelete(query as CFDictionary)
        var q = query
        q[kSecValueData as String] = data
        q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(q as CFDictionary, nil)
    }
}

/// iOS's push callbacks.
final class AppDelegate: NSObject, UIApplicationDelegate {
    func application(_ application: UIApplication, didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil) -> Bool {
        MainActor.assumeIsolated { Push.shared.start() }
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        MainActor.assumeIsolated { Push.shared.gotToken(deviceToken) }
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: Error) {
        MainActor.assumeIsolated { Push.shared.failed(error) }
    }

    /// A silent push: a Mac clearing a notification (DeliveredNotes.clear).
    func application(_ application: UIApplication, didReceiveRemoteNotification userInfo: [AnyHashable: Any],
                     fetchCompletionHandler completionHandler: @escaping (UIBackgroundFetchResult) -> Void) {
        nonisolated(unsafe) let info = userInfo
        nonisolated(unsafe) let done = completionHandler
        Task { @MainActor in
            done(await DeliveredNotes.clear(info) ? .newData : .noData)
        }
    }
}
