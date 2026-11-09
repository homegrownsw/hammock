import Foundation

/// Links the bundled `hmk` into a directory on PATH so it can be used from
/// any terminal. The β build installs `hmk-beta`, so the two never clash.
enum CommandLineTool {
    static var linkName: String {
        (Bundle.main.object(forInfoDictionaryKey: "HammockStateDir") as? String ?? "Hammock") == "Hammock" ? "hmk" : "hmk-beta"
    }

    static var bundled: URL? {
        Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/hmk") as URL?
    }

    /// Where it's linked from, in order of preference.
    static var candidates: [String] {
        ["/opt/homebrew/bin", "/usr/local/bin", (NSHomeDirectory() as NSString).appendingPathComponent(".local/bin")]
    }

    /// The installed link, if one points at this app's hammock.
    static var installedPath: String? {
        guard let target = bundled?.resolvingSymlinksInPath().path else { return nil }
        return candidates.map { ($0 as NSString).appendingPathComponent(linkName) }.first { path in
            (try? FileManager.default.destinationOfSymbolicLink(atPath: path)).map { URL(fileURLWithPath: $0).resolvingSymlinksInPath().path == target } ?? false
        }
    }

    /// Links hmk into the first writable candidate (creating ~/.local/bin if
    /// need be); returns the path. Only ever replaces a symlink, not a file.
    @discardableResult
    static func install() throws -> String {
        guard let target = bundled else { throw CocoaError(.fileNoSuchFile) }
        let fm = FileManager.default
        let dir = candidates.first { fm.isWritableFile(atPath: $0) } ?? candidates.last!
        try fm.createDirectory(atPath: dir, withIntermediateDirectories: true)
        let link = (dir as NSString).appendingPathComponent(linkName)
        if (try? fm.destinationOfSymbolicLink(atPath: link)) != nil { try fm.removeItem(atPath: link) }
        try fm.createSymbolicLink(atPath: link, withDestinationPath: target.path)
        return link
    }

    static func uninstall() {
        if let p = installedPath { try? FileManager.default.removeItem(atPath: p) }
    }
}
