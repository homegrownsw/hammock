import AppKit
import ScreenCaptureKit
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
///	drag [SECONDS]                resizes the window every frame and reports how it kept up (ResizeDrag)
///	finish-setup                  finishes onboarding, as its last button does
///	agentview PANE                that pane in its agent view, as the header's toggle does
///	agent show|toggle ID          that row at the top of the agent view; open or close it, as a click does
///	agent end | agent list FILE   to its end; its rows, one a line, to FILE (AgentDebug)
///	size W H                      the window's content this size
///	appearance light|dark|auto    the app's appearance (the config's, as Settings sets it)
///	capture PATH                  the window as drawn, to PATH.png (needs Screen Recording)
///	panel PATH                    β's floating panel (a tab's preview) as drawn, to PATH.png
///	screen PATH                   the whole display (floating panels too), to PATH.png
///	burst PATH SECONDS [COMMAND]  frames for SECONDS, to PATH-NN.png, running COMMAND after the first
///	responder PATH                the key window's first responder (its class), to PATH
///	pins on|off PANE              Pin Notes on or off there, as Pane ▸ Pin Notes does
///	pins point PANE X Y           a pointer at (X, Y) (fractions of the page), hovering as the mouse does; "pins point PANE" hides it
///	pins add PANE X Y NOTE        a pin there with that note, as a click and typing do
///	pins send PANE                Send, as the bar's button does
///	wait SECONDS
@MainActor enum DebugDirector {
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

    private static var mainWindow: NSWindow? { NSApp.windows.first { $0.isVisible && $0.contentView != nil && $0.styleMask.contains(.titled) } }

    private static func write(_ image: CGImage, to path: String) {
        try? NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: path))
    }

    private static func failed(_ line: String) {
        FileHandle.standardError.write(Data("HammockDirector: couldn't capture the window (\(line)): is the screen awake, and has β Screen Recording?\n".utf8))
    }

    private static func run(_ line: String) async {
        let parts = line.split(separator: " ", maxSplits: 1).map(String.init)
        guard let verb = parts.first, let m = AppModelRegistry.shared else { return }
        let arg = parts.count > 1 ? parts[1] : ""
        switch verb {
        case "collapse", "expand":
            guard let g = m.groups.first(where: { $0.name == arg }) else { return }
            GroupCollapse.set([g.id], collapsed: verb == "collapse")
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
        case "agentview":
            AgentModes.shared.set(arg, true)
        case "agent":
            let a = arg.split(separator: " ", maxSplits: 1).map(String.init)
            guard let cmd = a.first else { return }
            AgentDebug.send(cmd, a.count > 1 ? a[1] : "")
        case "size":
            let a = arg.split(separator: " ").compactMap { Double($0) }
            guard a.count == 2, let w = mainWindow else { return }
            w.setContentSize(NSSize(width: a[0], height: a[1]))
        case "appearance":
            // As Settings sets it (the config's appearance), which the window follows.
            AppConfig.shared.setAppearance(AppConfig.Appearance(rawValue: arg) ?? .auto)
        case "responder":
            let r = (NSApp.keyWindow ?? mainWindow)?.firstResponder
            try? (r.map { "\(type(of: $0))" } ?? "none").write(toFile: arg, atomically: true, encoding: .utf8)
        case "pins":
            let a = arg.split(separator: " ", maxSplits: 4).map(String.init)
            guard a.count >= 2 else { return }
            let pane = a[1]
            let point = a.count >= 4 ? Double(a[2]).flatMap { x in Double(a[3]).map { CGPoint(x: x, y: $0) } } : nil
            switch a[0] {
            case "on", "off": PinMode.shared.set(pane, a[0] == "on")
            case "point": PinPointer.shared.set(pane, point)
            case "add":
                guard let p = point else { return }
                let note = a.count > 4 ? a[4] : ""
                await m.pinCommand(pane) { $0.action = .add; $0.x = Double(p.x); $0.y = Double(p.y); $0.note = note }
            case "send": m.sendPins(pane)
            default: break
            }
        case "wait":
            try? await Task.sleep(for: .seconds(Double(arg) ?? 1))
        case "capture":
            guard let w = mainWindow, let image = await DebugSnapshot.grabWindow(w) else { return failed(line) }
            write(image, to: arg + ".png")
        case "panel":
            // β's floating panel (a tab's preview), and only it.
            guard let w = NSApp.windows.first(where: { $0.isVisible && $0 !== mainWindow && !$0.styleMask.contains(.titled) && $0.frame.width > 100 }),
                  let image = await DebugSnapshot.grabWindow(w) else { return failed(line) }
            write(image, to: arg + ".png")
        case "screen":
            guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true),
                  let d = content.displays.first else { return failed(line) }
            let config = SCStreamConfiguration()
            config.width = d.width * 2
            config.height = d.height * 2
            guard let image = try? await SCScreenshotManager.captureImage(contentFilter: SCContentFilter(display: d, excludingWindows: []), configuration: config)
            else { return failed(line) }
            write(image, to: arg + ".png")
        case "burst":
            let a = arg.split(separator: " ", maxSplits: 2).map(String.init)
            guard a.count >= 2, let seconds = Double(a[1]), let w = mainWindow else { return }
            let start = Date()
            var n = 0, times: [String] = []
            while Date().timeIntervalSince(start) < seconds {
                guard let image = await DebugSnapshot.grabWindow(w) else { return failed(line) }
                times.append(String(format: "%02d %.3f", n, Date().timeIntervalSince(start)))
                write(image, to: String(format: "%@-%02d.png", a[0], n))
                if n == 0, a.count > 2 { await run(a[2]) }
                n += 1
            }
            try? times.joined(separator: "\n").write(toFile: a[0] + "-times.txt", atomically: true, encoding: .utf8)
        case "finish-setup":
            Onboarding.shared.finish()
        case "drag":
            await ResizeDrag.run(seconds: Double(arg) ?? 4)
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
