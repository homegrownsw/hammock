import Foundation
import ServiceManagement

/// Registers hmkd as a login item (a LaunchAgent inside the app
/// bundle) so it starts at login and survives the app quitting.
enum DaemonService {
    /// Each build variant bundles its own agent (see scripts/build-mac-app.sh).
    static let plistName = (Bundle.main.object(forInfoDictionaryKey: "HammockAgentLabel") as? String ?? "com.homegrownsw.hammock.daemon") + ".plist"
    static var service: SMAppService { SMAppService.agent(plistName: plistName) }

    static var isEnabled: Bool { service.status == .enabled }
    /// Registered but waiting for the user to allow it in Login Items.
    static var needsApproval: Bool { service.status == .requiresApproval }

    static func openLoginItems() { SMAppService.openSystemSettingsLoginItems() }

    static func setEnabled(_ on: Bool) throws {
        if on {
            if AppMover.provisional { throw NotInApplications() }
            try service.register()
            if service.status == .requiresApproval { SMAppService.openSystemSettingsLoginItems() }
        } else {
            try service.unregister()
        }
    }
}
