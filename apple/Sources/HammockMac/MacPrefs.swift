import HammockKit

/// The Mac's settings that live in the config file through Prefs (they
/// were in UserDefaults before 0.3.1; AppConfig moves them, once). Each is
/// in AppConfig's doc comment and the file's header too.
extension Pref {
    /// Every one, for that move (with Pref.shared, both apps' settings).
    static var mac: [Pref] {
        [NotifyPrefs.needsInput, NotifyPrefs.grants, NotifyPrefs.finished, NotifyPrefs.errored, NotifyPrefs.sound,
         NotifyPrefs.whenVisible, NotifyPrefs.badge, NotifyPrefs.style,
         SidebarLayout.pref, FinishedPrefs.pref, CollapsedAgentsPrefs.pref, CloseGuard.pref, Pref.simulatorFrame]
    }

    /// Simulator panes draw the device around the screen.
    static let simulatorFrame = Pref("simulator-frame", defaults: "simulatorFrame", true)
}
