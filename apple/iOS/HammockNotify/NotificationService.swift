import UserNotifications

/// Decrypts a notification from one of your Macs: the relay and Apple only
/// ever carry ciphertext.
final class NotificationService: UNNotificationServiceExtension {
    /// What the note says, decrypted here and copied into userInfo for the app.
    private static let noteKeys = ["pane", "join", "grant", "key", "host", "id"]

    override func didReceive(_ request: UNNotificationRequest, withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        let content = (request.content.mutableCopy() as? UNMutableNotificationContent) ?? UNMutableNotificationContent()
        // Only what decrypts counts: drop anything the relay put alongside,
        // its category (actions, Time Sensitive) and thread included.
        var clean = content.userInfo
        for k in Self.noteKeys { clean[k] = nil }
        content.userInfo = clean
        content.categoryIdentifier = ""
        content.threadIdentifier = ""
        content.interruptionLevel = .active
        // Hammock's own sound, a soft kalimba note (NeedsYou.caf, in the app's
        // bundle), for every notification from your Macs.
        content.sound = UNNotificationSound(named: UNNotificationSoundName("NeedsYou.caf"))
        guard let note = PushKeys.note(request.content.userInfo), PushKeys.fresh(note) else {
            // Not from one of your Macs, or sent long ago (replayed): nothing
            // of it is shown, and the app won't act on it.
            content.title = "Hammock"
            content.subtitle = ""
            content.body = "Something needs you"
            var info = content.userInfo
            info["e"] = nil
            content.userInfo = info
            contentHandler(content)
            return
        }
        content.title = note["t"] as? String ?? "Hammock"
        content.subtitle = note["s"] as? String ?? ""
        content.body = note["b"] as? String ?? ""
        let category = note["c"] as? String ?? ""
        content.categoryIdentifier = category
        // A Hammock Key request waits on you for a couple of minutes:
        // through Focus, as Time Sensitive.
        if category == "hammockkey" { content.interruptionLevel = .timeSensitive }
        // Grouped by the pane (or the request) it's about.
        content.threadIdentifier = ["pane", "join", "grant", "key"].lazy.compactMap { note[$0] as? String }.first { !$0.isEmpty } ?? ""
        // "e" stays, so the app can check the note again when it's tapped.
        var info = content.userInfo
        for k in Self.noteKeys { if let v = note[k] { info[k] = v } }
        content.userInfo = info

        // The same thing from another Mac (a linked computer's agent, told by
        // each Mac this phone is paired with) replaces the one shown: the
        // relay's collapse id is per Mac, so Apple can't tell they're one.
        guard let id = note["id"] as? String, !id.isEmpty else {
            contentHandler(content)
            return
        }
        nonisolated(unsafe) let done = contentHandler
        nonisolated(unsafe) let shown = content
        let center = UNUserNotificationCenter.current()
        center.getDeliveredNotifications { delivered in
            let same = delivered.filter { $0.request.identifier != request.identifier && $0.request.content.userInfo["id"] as? String == id }
            if !same.isEmpty { center.removeDeliveredNotifications(withIdentifiers: same.map(\.request.identifier)) }
            done(shown)
        }
    }
}
