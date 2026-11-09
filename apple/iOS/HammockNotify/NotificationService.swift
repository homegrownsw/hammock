import UserNotifications

/// Decrypts a notification from one of your Macs: the relay and Apple only
/// ever carry ciphertext.
final class NotificationService: UNNotificationServiceExtension {
    override func didReceive(_ request: UNNotificationRequest, withContentHandler contentHandler: @escaping (UNNotificationContent) -> Void) {
        let content = (request.content.mutableCopy() as? UNMutableNotificationContent) ?? UNMutableNotificationContent()
        // Only what decrypts counts: drop anything the relay put alongside.
        var clean = content.userInfo
        for k in ["pane", "join", "grant", "key", "host"] { clean[k] = nil }
        content.userInfo = clean
        // Hammock's own sound, a soft kalimba note (NeedsYou.caf, in the app's
        // bundle), for every notification from your Macs.
        content.sound = UNNotificationSound(named: UNNotificationSoundName("NeedsYou.caf"))
        if let note = PushKeys.note(request.content.userInfo) {
            content.title = note["t"] as? String ?? content.title
            content.subtitle = note["s"] as? String ?? ""
            content.body = note["b"] as? String ?? content.body
            if let c = note["c"] as? String, !c.isEmpty { content.categoryIdentifier = c }
            // A Hammock Key request waits on you for a couple of minutes:
            // through Focus, as Time Sensitive.
            if note["c"] as? String == "hammockkey" { content.interruptionLevel = .timeSensitive }
            if let pane = note["pane"] as? String { content.threadIdentifier = pane }
            // "e" stays, so the app can check the note again when it's tapped.
            var info = content.userInfo
            for k in ["pane", "join", "grant", "key", "host"] { if let v = note[k] { info[k] = v } }
            content.userInfo = info
        }
        contentHandler(content)
    }
}
