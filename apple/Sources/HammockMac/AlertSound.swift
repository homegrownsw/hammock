import AppKit
import UserNotifications

/// The sounds a notification can play, by name as the config file holds
/// them: Hammock's own ("Hammock Chime": in the app's Resources, made by
/// scripts/alert-sounds.py), macOS's and any of yours ("Glass": a file in a
/// Sounds folder), or "none".
enum AlertSound {
    static let none = "none"

    struct Own {
        /// As the config file and the pickers know it.
        let name: String
        /// Its file in the app's Resources, a .caf.
        let resource: String
        /// Its file in ~/Library/Sounds.
        let installedAs: String
        /// As the pickers show it, under Hammock.
        var title: String { String(name.dropFirst("Hammock ".count)) }
    }

    static let kalimba = "Hammock Kalimba"
    static let chime = "Hammock Chime"
    static let low = "Hammock Low"

    /// Hammock's own, as the pickers list them. The kalimba keeps the name
    /// it was installed under before there were others (versions before
    /// these still use it).
    static let own: [Own] = [
        Own(name: kalimba, resource: "NeedsYou", installedAs: "Hammock Needs You.caf"),
        Own(name: chime, resource: "Chime", installedAs: "Hammock Chime.caf"),
        Own(name: low, resource: "Low", installedAs: "Hammock Low.caf"),
        Own(name: "Hammock Marimba", resource: "Marimba", installedAs: "Hammock Marimba.caf"),
        Own(name: "Hammock Bell", resource: "Bell", installedAs: "Hammock Bell.caf"),
    ]

    /// The Sounds folders, the first one's file winning for a name, as
    /// macOS looks.
    private static let folders: [URL] = {
        let fm = FileManager.default
        return fm.urls(for: .libraryDirectory, in: .userDomainMask).map { $0.appendingPathComponent("Sounds") }
            + [URL(fileURLWithPath: "/Library/Sounds"), URL(fileURLWithPath: "/System/Library/Sounds")]
    }()

    private static let soundTypes: Set<String> = ["aiff", "aif", "aifc", "caf", "wav", "m4a", "mp3"]

    /// macOS's sounds and yours, by name: each one's file. Not Hammock's own
    /// copies in ~/Library/Sounds, which are listed as Hammock's.
    static func system() -> [String: URL] {
        var found: [String: URL] = [:]
        for dir in folders {
            let files = (try? FileManager.default.contentsOfDirectory(at: dir, includingPropertiesForKeys: nil)) ?? []
            for f in files where soundTypes.contains(f.pathExtension.lowercased()) {
                let name = f.deletingPathExtension().lastPathComponent
                if name.hasPrefix("Hammock ") || found[name] != nil { continue }
                found[name] = f
            }
        }
        return found
    }

    /// Whether a name is one to play: "none", Hammock's own, or a sound in
    /// a Sounds folder still.
    static func exists(_ name: String) -> Bool {
        name == none || own.contains { $0.name == name } || system()[name] != nil
    }

    /// For a notification posted to Notification Center: nil for "none".
    @MainActor static func notification(_ name: String) -> UNNotificationSound? {
        if name == none { return nil }
        if let o = own.first(where: { $0.name == name }) {
            return UNNotificationSound(named: UNNotificationSoundName(install(o) ?? o.resource + ".caf"))
        }
        guard let file = system()[name] else { return nil }
        return UNNotificationSound(named: UNNotificationSoundName(file.lastPathComponent))
    }

    /// Notification Center didn't find Hammock's sounds in its Resources (it
    /// played the default instead), so each is also put in ~/Library/Sounds,
    /// where it always looks, under a name of its own: only those someone
    /// has picked, so the rest don't crowd the Mac's other sound lists.
    /// Its file name there, or nil if that couldn't be written.
    @MainActor @discardableResult
    static func install(_ o: Own) -> String? {
        if let done = installed[o.name] { return done }
        guard let src = Bundle.main.url(forResource: o.resource, withExtension: "caf"), let dir = folders.first else { return nil }
        let dst = dir.appendingPathComponent(o.installedAs)
        let fm = FileManager.default
        var result: String? = o.installedAs
        if !fm.contentsEqual(atPath: src.path, andPath: dst.path) {
            do {
                try fm.createDirectory(at: dir, withIntermediateDirectories: true)
                if fm.fileExists(atPath: dst.path) { try fm.removeItem(at: dst) }
                try fm.copyItem(at: src, to: dst)
            } catch {
                NSLog("Hammock: couldn't put the notification sound in %@: %@", dir.path, error.localizedDescription)
                result = nil
            }
        }
        installed[o.name] = result
        return result
    }

    @MainActor private static var installed: [String: String?] = [:]

    /// Puts a picked sound of Hammock's in place before its first notification.
    @MainActor static func prepare(_ name: String) {
        if let o = own.first(where: { $0.name == name }) { install(o) }
    }

    /// Plays one here: for notifications shown in the window, as Notification
    /// Center would, and to hear one picked in Settings.
    @MainActor static func play(_ name: String) {
        guard name != none, let s = sound(name) ?? NSSound(named: "Glass") else { return }
        playing?.stop()
        s.stop()
        s.play()
        playing = s
    }

    @MainActor private static var loaded: [String: NSSound] = [:]
    @MainActor private static var playing: NSSound?

    @MainActor private static func sound(_ name: String) -> NSSound? {
        if let s = loaded[name] { return s }
        let url = own.first { $0.name == name }.flatMap { Bundle.main.url(forResource: $0.resource, withExtension: "caf") }
            ?? system()[name]
        let s = url.flatMap { NSSound(contentsOf: $0, byReference: true) }
        loaded[name] = s
        return s
    }
}
