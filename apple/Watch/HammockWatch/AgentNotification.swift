import SwiftUI
import UserNotifications
import WatchKit

/// An agent's notification, drawn on the watch: its mark and session, what
/// it says (a permission's command in monospace), and your quick replies
/// offered for Reply.
final class AgentNotificationController: WKUserNotificationHostingController<AgentNotificationView> {
    private var content = UNNotificationContent()
    private var session: WatchSession?

    override var body: AgentNotificationView {
        AgentNotificationView(title: content.title, subtitle: content.subtitle, message: content.body,
                              session: session, permission: content.categoryIdentifier == AgentNotices.promptCategory)
    }

    override func didReceive(_ notification: UNNotification) {
        content = notification.request.content
        let info = content.userInfo
        if let host = info["host"] as? String, let pane = info["pane"] as? String {
            session = PhoneLink.stored()?.session(host: host, pane: pane)
        }
    }

    /// Reply's suggestions: your quick replies.
    override func suggestionsForResponseToAction(withIdentifier identifier: String, for notification: UNNotification, inputLanguage: String) -> [String] {
        let saved = PhoneLink.stored()?.quickReplies ?? []
        return WatchSuggestions.make(lastText: notification.request.content.body, quickReplies: saved)
    }
}

struct AgentNotificationView: View {
    let title: String
    let subtitle: String
    let message: String
    let session: WatchSession?
    let permission: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                if let session {
                    ZStack(alignment: .bottomTrailing) {
                        AgentMark(harness: session.harness, size: 15)
                            .frame(width: 24, height: 24)
                            .background(Circle().fill(.white.opacity(0.12)))
                        session.icon.background(Circle().fill(.black).padding(-1.5)).offset(x: 4, y: 4)
                    }
                }
                VStack(alignment: .leading, spacing: 0) {
                    Text(title).font(.headline).lineLimit(2)
                    if !subtitle.isEmpty { Text(subtitle).font(.footnote).foregroundStyle(.secondary).lineLimit(1) }
                }
            }
            if permission {
                Text(message)
                    .font(.system(.footnote, design: .monospaced))
                    .padding(8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(RoundedRectangle(cornerRadius: 10).fill(.white.opacity(0.1)))
            } else {
                Text(SessionRow.markdown(message)).font(.body)
            }
            if let s = session, PhoneLink.stored()?.showsMacNames == true {
                Text(s.hostName).font(.caption2).foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// The iPhone's notification categories and actions, registered here too:
/// with Hammock on the watch, its buttons are handled here, and passed to
/// the phone, which alone can open the note they're about.
enum AgentNotices {
    static let replyCategory = "agentreply"
    static let replyAction = "agentreply.reply"
    static let promptCategory = "agentprompt"
    static let allowAction = "agentprompt.allow"
    static let denyAction = "agentprompt.deny"

    /// Each opens the watch app (foreground), so the phone is reachable to answer it.
    static func register() {
        let reply = UNTextInputNotificationAction(identifier: replyAction, title: "Reply", options: .foreground,
                                                  textInputButtonTitle: "Send", textInputPlaceholder: "Message")
        let allow = UNNotificationAction(identifier: allowAction, title: "Allow", options: .foreground)
        let deny = UNNotificationAction(identifier: denyAction, title: "Deny", options: [.foreground, .destructive])
        let grantAllow = UNNotificationAction(identifier: "grant.allow", title: "Allow", options: .foreground)
        let grantDeny = UNNotificationAction(identifier: "grant.deny", title: "Deny", options: [.foreground, .destructive])
        // A join's Let Drive… and the Hammock Key's Approve… need the phone (its card, Face ID): not here.
        let joinView = UNNotificationAction(identifier: "join.view", title: "Let View", options: .foreground)
        let joinDecline = UNNotificationAction(identifier: "join.decline", title: "Decline", options: [.foreground, .destructive])
        let keyDeny = UNNotificationAction(identifier: "key.deny", title: "Don't Allow", options: [.foreground, .destructive])
        UNUserNotificationCenter.current().setNotificationCategories([
            UNNotificationCategory(identifier: replyCategory, actions: [reply], intentIdentifiers: []),
            UNNotificationCategory(identifier: promptCategory, actions: [allow, deny], intentIdentifiers: []),
            UNNotificationCategory(identifier: "grant", actions: [grantAllow, grantDeny], intentIdentifiers: []),
            UNNotificationCategory(identifier: "join", actions: [joinView, joinDecline], intentIdentifiers: []),
            UNNotificationCategory(identifier: "hammockkey", actions: [keyDeny], intentIdentifiers: []),
        ])
    }

    /// A button chosen: the phone answers it. If it couldn't, a
    /// notification here says why.
    static func handle(_ response: UNNotificationResponse) async {
        let action = response.actionIdentifier
        guard action != UNNotificationDefaultActionIdentifier, action != UNNotificationDismissActionIdentifier,
              let sealed = response.notification.request.content.userInfo["e"] as? String else { return }
        let text = (response as? UNTextInputNotificationResponse)?.userText ?? ""
        do {
            _ = try await PhoneLink.shared.ask(.notificationAction(action: action, sealed: sealed, text: text, delivered: response.notification.date))
            WKInterfaceDevice.current().play(.success)
            await PhoneLink.shared.refresh()
        } catch {
            let c = UNMutableNotificationContent()
            c.title = response.notification.request.content.title
            c.body = error.localizedDescription
            c.userInfo = response.notification.request.content.userInfo
            try? await UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: response.notification.request.identifier, content: c, trigger: nil))
        }
    }
}
