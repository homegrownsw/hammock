import AppKit
import HammockKit

/// Offers to move Hammock to Applications when it's run from somewhere else:
/// a disk image, Downloads (where macOS runs it from a random read-only copy,
/// App Translocation) or any other folder. From there the daemon's login item
/// points at a path that goes away, and Sparkle can't update it.
///
/// Asked at launch, before the app connects to or starts a daemon, in release
/// builds only (local builds run from dist/; `HMK_MOVE_PROMPT=1` asks anyway).
/// Move to Applications copies it there (asking for an admin password if
/// /Applications needs one, else ~/Applications), opens the copy, ejects the
/// disk image and trashes a Downloads original. Not Now carries on from here
/// without registering login items, and asks again next launch; with Don't
/// ask again ticked, this place is where Hammock lives, and it never asks
/// again (login items register as usual).
@MainActor
enum AppMover {
    static let dontAskKey = "moveToApplications.dontAsk"

    /// Running from somewhere login items won't survive, for now (Not Now):
    /// nothing registers one from this copy.
    nonisolated(unsafe) private(set) static var provisional = false

    /// The app's start (connecting to its daemon, starting one), held until
    /// the question's answered so nothing runs from a copy about to move.
    static var deferredStart: (() -> Void)?

    static var shouldOffer: Bool {
        let env = ProcessInfo.processInfo.environment
        if env["HMK_DEBUG_SNAPSHOT"] != nil || UserDefaults.standard.bool(forKey: dontAskKey) { return false }
        if env["HMK_MOVE_PROMPT"] == "1" { return true }
        guard Bundle.main.object(forInfoDictionaryKey: "SUFeedURL") != nil else { return false }
        return !AppLocation.isInApplications(Bundle.main.bundlePath)
    }

    /// At launch: asks, then either starts the app here or moves it and quits
    /// (false: don't carry on launching).
    static func offerIfNeeded() -> Bool {
        guard let start = deferredStart else { return true }
        deferredStart = nil
        NSApp.activate()
        let alert = NSAlert()
        alert.messageText = "Move \(appName) to Applications?"
        alert.informativeText = "\(appName) is running from \(whereItIs). From there it can't keep your terminals running in the background or update itself. \(appName) can move itself to your Applications folder and open again from there."
        alert.addButton(withTitle: "Move to Applications")
        alert.addButton(withTitle: "Not Now")
        alert.showsSuppressionButton = true
        alert.suppressionButton?.title = "Don't ask again"
        if alert.runModal() == .alertFirstButtonReturn, moveAndRelaunch() { return false }
        if alert.suppressionButton?.state == .on {
            UserDefaults.standard.set(true, forKey: dontAskKey)
        } else {
            provisional = !AppLocation.isInApplications(Bundle.main.bundlePath)
        }
        start()
        return true
    }

    /// Moves the app to Applications and opens it from there, quitting this
    /// copy (false if it didn't: an error's been shown).
    @discardableResult
    static func moveAndRelaunch() -> Bool {
        do {
            let dest = try install()
            relaunch(dest)
            return true
        } catch Cancelled.cancelled {
            return false
        } catch {
            let alert = NSAlert()
            alert.alertStyle = .warning
            alert.messageText = "Couldn't move \(appName) to Applications"
            alert.informativeText = "\(error.localizedDescription)\n\nYou can drag it to the Applications folder in the Finder yourself."
            alert.runModal()
            return false
        }
    }

    // MARK: - Moving

    private enum Cancelled: Error { case cancelled }

    private struct Failed: LocalizedError {
        let errorDescription: String?
    }

    private static var appName: String { Bundle.main.object(forInfoDictionaryKey: "CFBundleName") as? String ?? "Hammock" }

    private static var whereItIs: String {
        let path = originalURL.path
        if diskImage != nil { return "its disk image" }
        if AppLocation.isInDownloads(path) { return "your Downloads folder" }
        return "“\((path as NSString).deletingLastPathComponent.replacingOccurrences(of: NSHomeDirectory(), with: "~"))”"
    }

    /// The disk image the app is on: a read-only volume under /Volumes.
    private static var diskImage: String? {
        guard let volume = AppLocation.volume(of: Bundle.main.bundlePath),
              (try? Bundle.main.bundleURL.resourceValues(forKeys: [.volumeIsReadOnlyKey]))?.volumeIsReadOnly == true else { return nil }
        return volume
    }

    /// Where the app really is: a translocated app's original (Security's
    /// SecTranslocateCreateOriginalPathForURL, not in its headers).
    private static var originalURL: URL {
        let url = Bundle.main.bundleURL
        guard AppLocation.isTranslocated(url.path),
              let security = dlopen("/System/Library/Frameworks/Security.framework/Security", RTLD_LAZY),
              let sym = dlsym(security, "SecTranslocateCreateOriginalPathForURL") else { return url }
        typealias Original = @convention(c) (CFURL, UnsafeMutablePointer<Unmanaged<CFError>?>?) -> Unmanaged<CFURL>?
        let original = unsafeBitCast(sym, to: Original.self)
        return original(url as CFURL, nil)?.takeRetainedValue() as URL? ?? url
    }

    /// Copies the app into Applications (or finds it there already) and
    /// returns that copy.
    private static func install() throws -> URL {
        let fm = FileManager.default
        let name = Bundle.main.bundleURL.lastPathComponent
        let system = URL(fileURLWithPath: "/Applications").appendingPathComponent(name)
        let user = URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Applications").appendingPathComponent(name)
        let running = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String

        // A copy there already: open it, or (an older one) ask first.
        var replacing: URL?
        for copy in [system, user] where fm.fileExists(atPath: copy.path) {
            let installed = NSDictionary(contentsOf: copy.appendingPathComponent("Contents/Info.plist"))?["CFBundleVersion"] as? String ?? ""
            switch AppLocation.existing(installed: installed, running: running) {
            case .none: break
            case .useIt: return copy
            case .older:
                let alert = NSAlert()
                alert.messageText = "An older \(appName) is in Applications"
                alert.informativeText = "Replace it with this version? Your Groups, terminals and settings are kept either way."
                alert.addButton(withTitle: "Replace")
                alert.addButton(withTitle: "Open the Older One")
                alert.addButton(withTitle: "Cancel")
                switch alert.runModal() {
                case .alertFirstButtonReturn: replacing = copy
                case .alertSecondButtonReturn: return copy
                default: throw Cancelled.cancelled
                }
            }
            break
        }

        if let replacing {
            try copy(to: replacing, privileged: !fm.isWritableFile(atPath: replacing.deletingLastPathComponent().path))
            trashOriginal()
            return replacing
        }
        if fm.isWritableFile(atPath: system.deletingLastPathComponent().path) {
            try copy(to: system, privileged: false)
        } else {
            do {
                try copy(to: system, privileged: true)
            } catch {
                // No admin password (or none given): the user's own Applications.
                try fm.createDirectory(at: user.deletingLastPathComponent(), withIntermediateDirectories: true)
                try copy(to: user, privileged: false)
                trashOriginal()
                return user
            }
        }
        trashOriginal()
        return system
    }

    /// Copies this bundle to `dest` (replacing what's there) without macOS's
    /// quarantine, so the copy doesn't run translocated too.
    private static func copy(to dest: URL, privileged: Bool) throws {
        let src = Bundle.main.bundleURL.path
        let script = "/bin/rm -rf \(shq(dest.path)) && /usr/bin/ditto \(shq(src)) \(shq(dest.path)) && /usr/bin/xattr -dr com.apple.quarantine \(shq(dest.path)); test -d \(shq(dest.path))"
        if privileged {
            let escaped = script.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
            var error: NSDictionary?
            NSAppleScript(source: "do shell script \"\(escaped)\" with administrator privileges")?.executeAndReturnError(&error)
            if let error { throw Failed(errorDescription: error[NSAppleScript.errorMessage] as? String ?? "The copy didn't finish.") }
            return
        }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", script]
        let err = Pipe()
        p.standardError = err
        try p.run()
        p.waitUntilExit()
        guard p.terminationStatus == 0 else {
            let message = String(decoding: err.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            throw Failed(errorDescription: message.isEmpty ? "Copying to \(dest.deletingLastPathComponent().path) failed." : message)
        }
    }

    /// A copy downloaded into Downloads goes to the Trash once it's installed.
    private static func trashOriginal() {
        let original = originalURL
        guard AppLocation.isInDownloads(original.path) else { return }
        try? FileManager.default.trashItem(at: original, resultingItemURL: nil)
    }

    /// Opens `dest` once this copy has quit, then ejects the disk image this
    /// one ran from, and quits.
    private static func relaunch(_ dest: URL) {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", """
            while /bin/kill -0 "$1" 2>/dev/null; do /bin/sleep 0.2; done
            /usr/bin/open "$2"
            [ -n "$3" ] && /usr/bin/hdiutil detach "$3" -quiet
            exit 0
            """, "sh", String(getpid()), dest.path, diskImage ?? ""]
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        try? p.run()
        NSApp.terminate(nil)
    }

    /// Single-quoted for the shell.
    private static func shq(_ s: String) -> String { "'" + s.replacingOccurrences(of: "'", with: "'\\''") + "'" }
}

/// Registering a login item from a copy that's about to move or vanish
/// (AppMover.provisional).
struct NotInApplications: LocalizedError {
    var errorDescription: String? {
        "Move Hammock to your Applications folder first: a login item for a copy that's run from somewhere else stops working once it moves or its disk image is ejected."
    }
}
