import SwiftUI
import UserNotifications

@main
struct HammockWatchApp: App {
    @WKApplicationDelegateAdaptor(WatchAppDelegate.self) private var delegate

    var body: some Scene {
        WindowGroup {
            NavigationStack {
                SessionsView()
            }
            .environment(\.watchTheme, WatchTheme(PhoneLink.shared.snapshot))
            .tint(WatchTheme(PhoneLink.shared.snapshot).accent)
        }
        // Your iPhone's notifications, drawn here: an agent's with its
        // mark and your quick replies, a permission's with its command.
        WKNotificationScene(controller: AgentNotificationController.self, category: "agentreply")
        WKNotificationScene(controller: AgentNotificationController.self, category: "agentprompt")
    }
}

final class WatchAppDelegate: NSObject, WKApplicationDelegate, UNUserNotificationCenterDelegate {
    func applicationDidFinishLaunching() {
        MainActor.assumeIsolated { PhoneLink.shared.start() }
        UNUserNotificationCenter.current().delegate = self
        AgentNotices.register()
    }

    /// A notification's button (Allow, Deny, Reply): the phone answers it.
    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        await AgentNotices.handle(response)
    }

    func applicationDidBecomeActive() {
        Task { @MainActor in await PhoneLink.shared.refresh() }
    }

    /// A notification while the app's open: shown, and the list asked for again.
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        await PhoneLink.shared.refresh()
        return [.banner, .sound]
    }
}
