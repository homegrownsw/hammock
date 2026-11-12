import AppKit
import ServiceManagement
import SwiftUI

/// SwiftUI's own way to open Settings, kept from a window for code outside
/// the views (the menu bar item's Settings…): AppKit's showSettingsWindow:
/// isn't honoured from there.
struct SettingsOpener: View {
    @Environment(\.openSettings) private var openSettings
    @MainActor static var open: (() -> Void)?

    var body: some View {
        Color.clear.frame(width: 0, height: 0)
            .onAppear { Self.open = { openSettings() } }
    }

    @MainActor static func show() {
        NSApp.activate()
        if let open { open() } else { NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil) }
    }
}

/// The menu bar item (Hammock Menu.app, in Contents/Library/LoginItems,
/// ADR-0014): registered as a login item so it's there whether Hammock is open
/// or not, and started now if it isn't running. It asks this app to open
/// Settings with a distributed notification.
@MainActor
enum MenuHelper {
    static var bundleID: String { (Bundle.main.bundleIdentifier ?? "com.homegrownsw.hammock") + ".menu" }
    static var url: URL { Bundle.main.bundleURL.appendingPathComponent("Contents/Library/LoginItems/Hammock Menu.app") }

    static func start() {
        // Not for a snapshot run (HMK_DEBUG_SNAPSHOT: a second copy, briefly).
        guard FileManager.default.fileExists(atPath: url.path), ProcessInfo.processInfo.environment["HMK_DEBUG_SNAPSHOT"] == nil else { return }
        // A copy from before this build (it outlives updates): replaced.
        let built = (try? FileManager.default.attributesOfItem(atPath: url.appendingPathComponent("Contents/MacOS/Hammock Menu").path)[.modificationDate]) as? Date
        var replacing = false
        for old in NSRunningApplication.runningApplications(withBundleIdentifier: bundleID) where (old.launchDate ?? .distantPast) < (built ?? .distantPast) {
            old.terminate()
            replacing = true
        }
        if replacing {
            DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                let cfg = NSWorkspace.OpenConfiguration()
                cfg.activates = false
                cfg.addsToRecentItems = false
                NSWorkspace.shared.openApplication(at: url, configuration: cfg) { _, _ in }
            }
        }
        let item = SMAppService.loginItem(identifier: bundleID)
        // Registering starts it; started here only if it was registered
        // already (and isn't running: it quit, or was quit). Not at all from
        // a copy run from outside Applications (AppMover): its login item
        // would go with it, and running, it would hold the disk image open.
        if !AppMover.provisional, item.status != .enabled {
            try? item.register()
        } else if !AppMover.provisional, NSRunningApplication.runningApplications(withBundleIdentifier: bundleID).isEmpty {
            let cfg = NSWorkspace.OpenConfiguration()
            cfg.activates = false
            cfg.addsToRecentItems = false
            NSWorkspace.shared.openApplication(at: url, configuration: cfg) { _, _ in }
        }
        // Opened from the menu bar's Settings… while it wasn't running.
        if CommandLine.arguments.contains("--open-settings") {
            // Once the window's up (it holds the way to open Settings).
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) { SettingsOpener.show() }
        }
        DistributedNotificationCenter.default().addObserver(forName: .init("com.homegrownsw.hammock.openSettings"), object: Bundle.main.bundleIdentifier,
                                                            queue: .main) { _ in
            MainActor.assumeIsolated {
                NSLog("Hammock: the menu bar asked for Settings (%@)", SettingsOpener.open == nil ? "no window: showSettingsWindow:" : "openSettings")
                SettingsOpener.show()
            }
        }
    }
}
