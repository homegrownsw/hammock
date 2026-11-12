import AppKit
import HammockKit

/// Dev aid: HMK_DEBUG_DIRECTOR=/path/file lets a script direct the window,
/// for the README's demo (`scripts/readme-demo`), without real clicks or
/// keys. The app reads lines appended to the file, one command each, for
/// what `hmk` can't do (it makes the panes; `hmk focus` goes by a
/// hammock:// link, which another copy of Hammock might take):
///
///	collapse NAME | expand NAME   a Group, by name, as clicking its header does
///	focus PANE                    shows that pane, as clicking its notification does
///	zoom N                        zooms (or unzooms) the selected tab's Nth pane
///	palette [TEXT]                opens the command palette, typing TEXT into it
///	palette close
///	profile NAME|DEVICE           your name and this Mac's, as Settings ▸ Sharing sets them
@MainActor enum DebugDirector {
    static let collapse = Notification.Name("HammockDebugCollapse")
    /// What the palette's search field should hold, set as it's typed.
    static var paletteQuery: ((String) -> Void)?

    static func startIfRequested() {
        guard let path = ProcessInfo.processInfo.environment["HMK_DEBUG_DIRECTOR"], !path.isEmpty else { return }
        // Lines written before launch count too: it waits for the daemon's
        // state, then reads the file from its start.
        if !FileManager.default.fileExists(atPath: path) { FileManager.default.createFile(atPath: path, contents: nil) }
        var offset = 0
        Task { @MainActor in
            while !(AppModelRegistry.shared?.connected ?? false) || AppModelRegistry.shared?.groups.isEmpty != false {
                try? await Task.sleep(for: .milliseconds(100))
            }
            while true {
                try? await Task.sleep(for: .milliseconds(100))
                guard let data = FileManager.default.contents(atPath: path), data.count > offset else { continue }
                let text = String(decoding: data[offset...], as: UTF8.self)
                guard let end = text.lastIndex(of: "\n") else { continue }
                offset += text[..<end].utf8.count + 1
                for line in text[..<end].split(separator: "\n") { await run(String(line)) }
            }
        }
    }

    private static func run(_ line: String) async {
        let parts = line.split(separator: " ", maxSplits: 1).map(String.init)
        guard let verb = parts.first, let m = AppModelRegistry.shared else { return }
        let arg = parts.count > 1 ? parts[1] : ""
        switch verb {
        case "collapse", "expand":
            guard let g = m.groups.first(where: { $0.name == arg }) else { return }
            NotificationCenter.default.post(name: collapse, object: g.id, userInfo: ["collapsed": verb == "collapse"])
        case "focus":
            m.reveal(paneID: arg)
        case "profile":
            let p = arg.split(separator: "|", maxSplits: 1).map(String.init)
            guard p.count == 2 else { return }
            await m.setProfile(name: p[0], avatar: nil, deviceName: p[1])
        case "zoom":
            guard let n = Int(arg), let tab = m.selectedTabID.flatMap({ m.tabs[$0] }) else { return }
            let ids = AppModel.paneIDs(tab.layout)
            if n >= 1, n <= ids.count { PaneZoom.shared.toggle(ids[n - 1], model: m) }
        case "palette" where arg == "close":
            PaletteState.shared.isOpen = false
        case "palette":
            if !PaletteState.shared.isOpen { PaletteState.shared.open(.commands) }
            try? await Task.sleep(for: .milliseconds(400))
            var typed = ""
            for c in arg {
                typed.append(c)
                paletteQuery?(typed)
                try? await Task.sleep(for: .milliseconds(90))
            }
        default:
            FileHandle.standardError.write(Data("HammockDirector: unknown command \(line)\n".utf8))
        }
    }
}
