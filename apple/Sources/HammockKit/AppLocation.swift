import Foundation

#if os(macOS)
/// Where the Mac app is running from, and what offering to move it to
/// Applications should do (AppMover in the Mac app does the moving). Pure
/// path and version logic, here so it's tested without a disk image (the
/// Mac's only: the iPhone app never moves).
public enum AppLocation {
    /// In /Applications or ~/Applications (or a folder inside either), where
    /// login items and Sparkle's updates keep working.
    public static func isInApplications(_ path: String, home: String = NSHomeDirectory()) -> Bool {
        let p = (path as NSString).standardizingPath
        return ["/Applications", (home as NSString).appendingPathComponent("Applications")]
            .contains { p.hasPrefix($0 + "/") }
    }

    /// macOS's App Translocation: a quarantined app opened where it was
    /// downloaded runs from a random, read-only mount of itself.
    public static func isTranslocated(_ path: String) -> Bool {
        path.contains("/AppTranslocation/")
    }

    /// In ~/Downloads (or a folder inside it): moved, the original is trashed.
    public static func isInDownloads(_ path: String, home: String = NSHomeDirectory()) -> Bool {
        (path as NSString).standardizingPath.hasPrefix((home as NSString).appendingPathComponent("Downloads") + "/")
    }

    /// The disk image a path is on: the volume's mount point, "/Volumes/<name>".
    public static func volume(of path: String) -> String? {
        let parts = (path as NSString).standardizingPath.split(separator: "/", omittingEmptySubsequences: true)
        guard parts.count >= 2, parts[0] == "Volumes" else { return nil }
        return "/Volumes/\(parts[1])"
    }

    /// What to do about a copy already in Applications, by build number
    /// (CFBundleVersion, which only goes up).
    public enum Existing: Equatable {
        /// Nothing there: copy this one.
        case none
        /// The same build or a newer one: open that instead.
        case useIt
        /// An older build: ask before replacing it.
        case older
    }

    public static func existing(installed: String?, running: String?) -> Existing {
        guard let installed else { return .none }
        guard let i = Int(installed), let r = Int(running ?? "") else { return .useIt }
        return i < r ? .older : .useIt
    }
}
#endif
