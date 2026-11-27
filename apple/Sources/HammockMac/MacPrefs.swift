import HammockKit

/// The Mac's settings that live in the config file through Prefs (they
/// were in UserDefaults before 0.3.1; AppConfig moves them, once). Each is
/// in AppConfig's doc comment and the file's header too, but for retired
/// ones still moved (NotifyPrefs.sound).
extension Pref {
    /// Every one, for that move (with Pref.shared, both apps' settings).
    static var mac: [Pref] {
        [NotifyPrefs.needsInput, NotifyPrefs.grants, NotifyPrefs.finished, NotifyPrefs.errored, NotifyPrefs.reviewReplies, NotifyPrefs.sound,
         NotifyPrefs.whenVisible, NotifyPrefs.badge, NotifyPrefs.style,
         SidebarLayout.pref, FinishedPrefs.pref, CollapsedAgentsPrefs.pref, CloseGuard.pref, Pref.simulatorFrame]
    }

    /// Simulator panes draw the device around the screen.
    static let simulatorFrame = Pref("simulator-frame", defaults: "simulatorFrame", true)

    /// An Apple TV's pane shows its Siri Remote beside the screen.
    static let simulatorRemote = Pref("simulator-remote", defaults: "simulatorRemote", true)

    /// Agents acting in browser and simulator panes are shown there: a
    /// cursor or finger, a glow and a badge (AgentPresenceOverlay). hmkd
    /// reads it too: off, it sends no actions and doesn't wait for the
    /// cursor.
    static let agentPresence = Pref("agent-presence", defaults: "agentPresence", true)

    /// When automations' Free Time trigger may fire: "[DAYS ]HH:MM-HH:MM".
    /// hmkd reads it from the file itself.
    static let workingHours = Pref("working-hours", defaults: "workingHours", .string, "mon-fri 09:00-17:00")

    /// How long an agent rests before hmkd puts it to sleep (ends its
    /// process to free its memory; waking it resumes it): off, or
    /// one of AgentSleepChoice's. hmkd reads it from the file itself.
    static let agentSleep = Pref("agent-sleep", defaults: "agentSleep", .string, "off")

    /// Keep the Mac from idle sleep while an agent is working. hmkd reads
    /// it from the file itself and holds the power assertion.
    static let agentKeepAwake = Pref("agent-keep-awake", defaults: "agentKeepAwake", false)

    /// An agent stopped at its usage limit is sent "continue" when the
    /// limit resets. hmkd reads it from the file itself.
    static let agentResumeAfterLimit = Pref("agent-resume-after-limit", defaults: "agentResumeAfterLimit", true)

    /// The agent New Agent Tab starts on this Mac without asking (New Claude
    /// Code Tab, ⌥⌘N): an agent's id, or "" (none: the sheet asks). hmkd
    /// reads it, and tells the apps, while that agent is installed.
    static let defaultAgent = Pref("default-agent", defaults: "defaultAgent", .string, "")

    /// Sharing Groups with other people: off, the Share Group buttons and
    /// menu items are hidden (SharingUI.canShare). Settings stops every share
    /// when it's turned off.
    static let groupSharing = Pref("group-sharing", defaults: "groupSharing", false)
}
