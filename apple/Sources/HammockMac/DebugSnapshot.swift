import AppKit
import HammockKit
import ScreenCaptureKit
import SwiftUI

/// Dev aid: with HMK_DEBUG_SNAPSHOT=/path/prefix the app renders its
/// main window to PNGs (light and dark) a few seconds after launch, so UI
/// changes can be checked without screen-recording permission.
enum DebugSnapshot {
    @MainActor static func scheduleIfRequested() {
        let env = ProcessInfo.processInfo.environment
        // HMK_DEBUG_NOTICES=1: a few of the window's notification cards, for the snapshot.
        if env["HMK_DEBUG_NOTICES"] == "1" {
            DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
                InAppNotices.shared.post(InAppNotice(id: "debug-1", icon: .agent(.needsInput), title: "Fix the login flow", subtitle: "🚀 Hammock",
                                                     body: "Claude asks: Should I also update the tests for the new session API?"))
                InAppNotices.shared.post(InAppNotice(id: "debug-2", icon: .agent(.idle), title: "Release notes", subtitle: "", body: "Codex finished"))
                InAppNotices.shared.post(InAppNotice(id: "debug-3", icon: .agent(.errored), title: "Migrate settings", subtitle: "🚀 Hammock", body: "Claude hit an error"))
            }
        }
        // HMK_DEBUG_SETTINGS=notifications|about|keyboard|ssh|appearance|agentview|workspace (onboarding's drawing)|devices (with HMK_DEBUG_SNAPSHOT):
        // that Settings pane rendered to <prefix>-settings-<light|dark>.png;
        // window: the whole Settings window, sidebar and all, on the pane last
        // shown (pick one with --args -settingsTab <name>);
        // HMK_DEBUG_SETTINGS_QUERY searches the keyboard one, or the window's sidebar;
        // HMK_DEBUG_SSH=key|none shows the SSH one with a made-up Hammock Key
        // (and 1Password not yet routed), or none.
        if let pane = env["HMK_DEBUG_SETTINGS"], ["notifications", "keyboard", "ssh", "appearance", "agentview", "workspace", "devices", "editor", "tools", "window", "about"].contains(pane), let prefix = env["HMK_DEBUG_SNAPSHOT"] {
            // Devices asks the daemon for its networks first, once it's connected
            // (its own .task can't run while this renders).
            Task { @MainActor in
                try? await Task.sleep(for: .seconds(pane == "devices" ? 6 : 2))
                guard let model = AppModelRegistry.shared else { return }
                var devices: Hammock_V1_Devices?
                if pane == "devices" {
                    do { devices = try await model.deviceStatus() } catch { NSLog("Hammock: debug snapshot: devices: %@", "\(error)") }
                }
                // Rendered off this task (it spins the run loop, which an async context mustn't).
                DispatchQueue.main.async { MainActor.assumeIsolated {
                let content = pane == "window"
                    ? AnyView(SettingsView(model: model, initialQuery: env["HMK_DEBUG_SETTINGS_QUERY"] ?? ""))
                    : pane == "keyboard"
                    ? AnyView(KeyboardSettings(initialQuery: env["HMK_DEBUG_SETTINGS_QUERY"] ?? ""))
                    : pane == "ssh"
                    ? AnyView(SSHSettings(model: model, sample: SSHSettings.Sample(env["HMK_DEBUG_SSH"] ?? "key")))
                    : pane == "appearance"
                    ? AnyView(AppearanceSettings())
                    : pane == "editor"
                    ? AnyView(EditorSettings())
                    : pane == "tools"
                    ? AnyView(ToolsSettings())
                    : pane == "agentview"
                    ? AnyView(AgentViewSettings())
                    : pane == "workspace"
                    ? AnyView(MiniWorkspaceDebug())
                    : pane == "devices"
                    ? AnyView(DevicesSettings(model: model, sample: devices))
                    : pane == "about"
                    ? AnyView(AboutSettings(model: model))
                    : AnyView(NotificationSettings(model: model))
                // Appearance is long: all of it, to see its middle sections.
                let height: CGFloat = pane == "appearance" ? (AppConfig.shared.isCustomTheme ? 2900 : 2000) : pane == "workspace" ? 480 : pane == "devices" || pane == "ssh" ? 1400 : pane == "about" ? 1150 : pane == "window" ? 720 : 640
                let width: CGFloat = pane == "workspace" ? 1200 : pane == "window" ? 920 : 680
                let host = NSHostingView(rootView: content.frame(width: width, height: height).background(Color(nsColor: .windowBackgroundColor)))
                let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: width, height: height), styleMask: [.titled], backing: .buffered, defer: false)
                window.isReleasedWhenClosed = false // ARC owns it; AppKit releasing it too crashes
                window.contentView = host
                window.orderFront(nil)
                for (name, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
                    window.appearance = NSAppearance(named: appearance)
                    host.layoutSubtreeIfNeeded()
                    host.displayIfNeeded()
                    RunLoop.main.run(until: Date().addingTimeInterval(1))
                    guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { continue }
                    host.cacheDisplay(in: host.bounds, to: rep)
                    try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: "\(prefix)-settings-\(name).png"))
                }
                window.close()
                } }
            }
        }
        // HMK_DEBUG_ISLAND=1 (with HMK_DEBUG_SNAPSHOT): Hammock Island in a
        // window of its own, captured as it's drawn (RealityKit isn't in a
        // layer render) every half second, to <prefix>-island-NN.png; with
        // HMK_DEBUG_ISLAND_PLAY=1 it swings and drops coconuts by itself.
        // (HMK_DEBUG_ISLAND also opens it in Settings ▸ About.)
        if env["HMK_DEBUG_ISLAND"] != nil, let prefix = env["HMK_DEBUG_SNAPSHOT"] {
            Task { @MainActor in
                try? await Task.sleep(for: .seconds(1))
                let size = NSSize(width: 640, height: 400)
                let window = NSWindow(contentRect: NSRect(origin: NSPoint(x: 200, y: 200), size: size), styleMask: [.titled], backing: .buffered, defer: false)
                window.isReleasedWhenClosed = false
                window.contentView = NSHostingView(rootView: HammockIslandView(icon: NSApp.applicationIconImage.cgImage(forProposedRect: nil, context: nil, hints: nil)).frame(width: size.width, height: size.height))
                window.orderFrontRegardless()
                try? await Task.sleep(for: .seconds(1))
                for n in 0..<12 {
                    if let image = await grabWindow(window) {
                        try? NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:])?
                            .write(to: URL(fileURLWithPath: String(format: "%@-island-%02d.png", prefix, n)))
                    }
                    try? await Task.sleep(for: .milliseconds(500))
                }
                // Open a while longer, to capture from outside (screencapture -l)
                // when this copy has no Screen Recording permission.
                try? await Task.sleep(for: .seconds(30))
                window.close()
            }
        }
        // HMK_DEBUG_ACKNOWLEDGEMENTS=1, or a component's name, or chromium-credits:
        // the Acknowledgements window opened (on that one); with
        // HMK_DEBUG_SNAPSHOT also rendered to <prefix>-acknowledgements-<light|dark>.png.
        if let which = env["HMK_DEBUG_ACKNOWLEDGEMENTS"] {
            Task { @MainActor in
                try? await Task.sleep(for: .seconds(1))
                AcknowledgementsUI.shared.show()
                guard let prefix = env["HMK_DEBUG_SNAPSHOT"] else { return }
                render(AcknowledgementsView(initial: which == "1" ? nil : which), size: NSSize(width: 860, height: 600), to: "\(prefix)-acknowledgements")
            }
        }
        // HMK_DEBUG_SCHEDULES=list or edit: the Scheduled Tasks sheet (open
        // from launch, ScheduleUI) or the editor (the first schedule, else a
        // new one) opened; with HMK_DEBUG_SNAPSHOT also rendered on their own
        // to <prefix>-schedules-<light|dark>.png (sheets aren't in the
        // window's snapshot).
        if let which = env["HMK_DEBUG_SCHEDULES"] {
            Task { @MainActor in
                // Once the daemon's state (and its schedules) is in.
                for _ in 0..<40 {
                    try? await Task.sleep(for: .milliseconds(100))
                    if AppModelRegistry.shared?.schedules.isEmpty == false { break }
                }
                guard let model = AppModelRegistry.shared else { return }
                if which == "edit" { ScheduleUI.shared.openDebugEditor(model) }
                guard let prefix = env["HMK_DEBUG_SNAPSHOT"] else { return }
                if which == "edit" {
                    let s = ScheduleUI.shared.editing?.schedule ?? Schedule()
                    render(ScheduleEditor(model: model, original: s), size: NSSize(width: 580, height: 720), to: "\(prefix)-schedules")
                } else {
                    render(ScheduledTasksSheet(model: model), size: NSSize(width: 900, height: 600), to: "\(prefix)-schedules")
                }
            }
        }
        // HMK_DEBUG_START_AGENT=1: the Start an Agent sheet opened on the
        // current Group; with HMK_DEBUG_SNAPSHOT also rendered on its own to
        // <prefix>-start-agent-<light|dark>.png.
        if env["HMK_DEBUG_START_AGENT"] != nil {
            Task { @MainActor in
                // Once the daemon's state (and its Groups) is in.
                for _ in 0..<40 {
                    try? await Task.sleep(for: .milliseconds(100))
                    if AppModelRegistry.shared?.groups.isEmpty == false { break }
                }
                guard let model = AppModelRegistry.shared else { return }
                AgentLauncher.shared.open(model)
                guard let prefix = env["HMK_DEBUG_SNAPSHOT"], let request = AgentLauncher.shared.request else { return }
                // Its Groups' pictures, fetched as the sheet shows them.
                try? await Task.sleep(for: .seconds(3))
                render(StartAgentSheet(model: model, request: request), size: NSSize(width: 520, height: 600), to: "\(prefix)-start-agent")
            }
        }
        // HMK_DEBUG_OPEN_DIFF=1: the Open a Diff sheet beside the focused
        // pane; with HMK_DEBUG_SNAPSHOT also rendered on its own to
        // <prefix>-open-diff-<light|dark>.png.
        if env["HMK_DEBUG_OPEN_DIFF"] != nil {
            Task { @MainActor in
                for _ in 0..<40 {
                    try? await Task.sleep(for: .milliseconds(100))
                    if AppModelRegistry.shared?.selectedTab != nil { break }
                }
                guard let model = AppModelRegistry.shared, let pane = model.selectedTab?.focusedPaneID else { return }
                DiffLauncher.shared.split(model, pane, .horizontal)
                guard let prefix = env["HMK_DEBUG_SNAPSHOT"], let request = DiffLauncher.shared.request else { return }
                try? await Task.sleep(for: .seconds(3))
                render(OpenDiffSheet(model: model, request: request), size: NSSize(width: 520, height: 560), to: "\(prefix)-open-diff")
            }
        }
        // HMK_DEBUG_PROMPT=plain|other|preview|tasks: the agent view's question
        // card with a made-up question (PromptCardSample), rendered to
        // <prefix>-prompt-<light|dark>.png.
        if let kind = env["HMK_DEBUG_PROMPT"], let prefix = env["HMK_DEBUG_SNAPSHOT"] {
            Task { @MainActor in
                try? await Task.sleep(for: .seconds(1))
                guard let model = AppModelRegistry.shared else { return }
                render(PromptCardSample(model: model, kind: kind), size: NSSize(width: 520, height: kind == "preview" ? 640 : kind == "tasks" ? 640 : 460), to: "\(prefix)-prompt")
            }
        }
        // HMK_DEBUG_CODEBLOCKS=1: code blocks with long lines, wrapped and
        // not, and a Markdown one as source and rendered (CodeBlockSample),
        // rendered to <prefix>-code-<light|dark>.png.
        if env["HMK_DEBUG_CODEBLOCKS"] != nil, let prefix = env["HMK_DEBUG_SNAPSHOT"] {
            Task { @MainActor in
                try? await Task.sleep(for: .seconds(1))
                render(CodeBlockSample(), size: NSSize(width: 560, height: 1300), to: "\(prefix)-code")
            }
        }
        // HMK_DEBUG_COMPOSER=<text>: "!" commands as the agent view shows
        // them, over the composer with <text> typed (ComposerSample: start it
        // with "!" for a shell command), to <prefix>-composer-<light|dark>.png.
        if let typed = env["HMK_DEBUG_COMPOSER"], let prefix = env["HMK_DEBUG_SNAPSHOT"] {
            Task { @MainActor in
                try? await Task.sleep(for: .seconds(1))
                guard let model = AppModelRegistry.shared else { return }
                render(ComposerSample(model: model, text: typed), size: NSSize(width: 560, height: 420), to: "\(prefix)-composer")
            }
        }
        // HMK_DEBUG_COMPUTER_LOOK=symbols|emoji[:search]: the icon and colour
        // picker for this computer (the first linked one, if any), on that
        // tab, rendered to <prefix>-look-<light|dark>.png.
        if let which = env["HMK_DEBUG_COMPUTER_LOOK"], let prefix = env["HMK_DEBUG_SNAPSHOT"] {
            Task { @MainActor in
                for _ in 0..<40 {
                    try? await Task.sleep(for: .milliseconds(100))
                    if AppModelRegistry.shared?.computers.isEmpty == false { break }
                }
                guard let model = AppModelRegistry.shared, let c = model.linkedComputers.first ?? model.thisComputer else { return }
                let parts = which.split(separator: ":", maxSplits: 1).map(String.init)
                render(ComputerLookPicker(model: model, userID: c.userID, done: {}, kind: parts[0] == "emoji" ? .emoji : .symbols, query: parts.count > 1 ? parts[1] : ""),
                       size: NSSize(width: 400, height: 580), to: "\(prefix)-look")
            }
        }
        if let name = ProcessInfo.processInfo.environment["HMK_DEBUG_TAB"] {
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
                if let m = AppModelRegistry.shared, let t = m.orderedTabs.first(where: { $0.name == name || m.title(of: $0) == name }) {
                    m.selectedTabID = t.id
                    // HMK_DEBUG_ZOOM=N: zoom the tab's Nth pane (from 1).
                    if let n = ProcessInfo.processInfo.environment["HMK_DEBUG_ZOOM"].flatMap(Int.init) {
                        let ids = AppModel.paneIDs(t.layout)
                        if n >= 1, n <= ids.count { PaneZoom.shared.toggle(ids[n - 1], model: m) }
                    }
                }
            }
        }
        guard let prefix = ProcessInfo.processInfo.environment["HMK_DEBUG_SNAPSHOT"] else { return }
        let delay = Double(ProcessInfo.processInfo.environment["HMK_DEBUG_DELAY"] ?? "") ?? 4
        DispatchQueue.main.asyncAfter(deadline: .now() + delay) {
            guard let window = NSApp.windows.first(where: { $0.isVisible && $0.contentView != nil }) else { return }
            window.setContentSize(NSSize(width: 1280, height: 800))
            for (name, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
                NSApp.appearance = NSAppearance(named: appearance)
                window.displayIfNeeded()
                RunLoop.main.run(until: Date().addingTimeInterval(1))
                guard let view = window.contentView?.superview ?? window.contentView else { continue }
                view.wantsLayer = true
                guard let layer = view.layer else { continue }
                let scale = window.backingScaleFactor
                let size = view.bounds.size
                guard let ctx = CGContext(data: nil, width: Int(size.width * scale), height: Int(size.height * scale), bitsPerComponent: 8, bytesPerRow: 0,
                                          space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { continue }
                ctx.scaleBy(x: scale, y: scale)
                if !view.isFlipped { } 
                // Panes Metal draws show their CoreGraphics drawing instead
                // while the layers render: a layer render can't see Metal.
                var panes: [TerminalNSView] = []
                @MainActor func find(_ v: NSView) {
                    if let t = v as? TerminalNSView, t.metal != nil, !t.isHiddenOrHasHiddenAncestor { panes.append(t) }
                    v.subviews.forEach(find)
                }
                find(view)
                panes.forEach { $0.beginSnapshot(scale: scale) }
                layer.render(in: ctx)
                panes.forEach { $0.endSnapshot() }
                // The same through AppKit's drawing, which gets the sidebar's
                // list (inside its glass) that the layer render leaves out.
                if ProcessInfo.processInfo.environment["HMK_DEBUG_CACHED"] == "1", let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) {
                    view.cacheDisplay(in: view.bounds, to: rep)
                    try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: "\(prefix)-cached-\(name).png"))
                }
                guard let image = ctx.makeImage() else { continue }
                let rep = NSBitmapImageRep(cgImage: image)
                try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: "\(prefix)-\(name).png"))
            }
            NSApp.appearance = nil
            // HMK_DEBUG_CAPTURE=1: the window as it's really drawn (glass,
            // the sidebar, live layers), captured by the app itself with its
            // own Screen Recording permission, to <prefix>-window-<light|dark>.png.
            // HMK_DEBUG_CAPTURE_ZOOM=N then zooms the tab's Nth pane (and
            // back) and captures frames as it animates, <prefix>-zoom-NN.png.
            if ProcessInfo.processInfo.environment["HMK_DEBUG_CAPTURE"] == "1" {
                Task { @MainActor in await captureWindow(window, prefix: prefix) }
            }
            var lines: [String] = []
            @MainActor func walk(_ v: NSView, _ depth: Int) {
                let r = v.convert(v.bounds, to: nil)
                if let b = v as? BrowserPaneNSView {
                    lines.append(String(repeating: "  ", count: depth) + "BrowserPaneNSView \(Int(r.minX)),\(Int(r.minY)) \(Int(r.width))x\(Int(r.height)) \(b.debugDescription)")
                }
                // The window's frame around the panes: split columns, divider, titlebar.
                let cls = "\(type(of: v))"
                if v is NSSplitView || v.superview is NSSplitView || cls.contains("Titlebar") || cls.contains("Toolbar") {
                    let extra = (v as? NSSplitView).map { " divider=\($0.dividerThickness)" } ?? ""
                    lines.append(String(repeating: "  ", count: depth) + "\(cls) x=\(r.minX) w=\(r.width) y=\(r.minY) h=\(r.height)\(extra)")
                }
                if v is TerminalNSView || depth < 3 {
                    let renderer = (v as? TerminalNSView).map { $0.metal == nil ? " renderer=coretext" : " renderer=metal" } ?? ""
                    lines.append(String(repeating: "  ", count: depth) + "\(type(of: v)) \(Int(r.minX)),\(Int(r.minY)) \(Int(r.width))x\(Int(r.height)) hidden=\(v.isHidden)\(renderer)")
                    if let t = v as? TerminalNSView, let s = t.session { lines.append(s.engine.text().split(separator: "\n").suffix(4).joined(separator: "\n")) }
                }
                v.subviews.forEach { walk($0, depth + 1) }
            }
            if let root = window.contentView?.superview ?? window.contentView { walk(root, 0) }
            // What a click in the toolbar's empty middle lands on, and whether
            // it drags the window from there.
            if let frame = window.contentView?.superview {
                for x in [frame.bounds.midX, frame.bounds.maxX - 300] {
                    let p = NSPoint(x: x, y: frame.bounds.maxY - 20)
                    let hit = frame.hitTest(p)
                    lines.append("toolbar click at \(p): \(hit.map { "\(type(of: $0))" } ?? "nil") movesWindow=\(hit?.mouseDownCanMoveWindow ?? false)")
                }
            }
            try? lines.joined(separator: "\n").write(toFile: prefix + "-views.txt", atomically: true, encoding: .utf8)
        }
    }
}

extension DebugSnapshot {
    /// Renders a view in a window of its own, light then dark, to <path>-<light|dark>.png.
    @MainActor static func render(_ view: some View, size: NSSize, to path: String) {
        // A sheet's own background (a hosting view alone paints none).
        let host = NSHostingView(rootView: view.frame(width: size.width, height: size.height).background(Color(nsColor: .windowBackgroundColor)))
        let window = NSWindow(contentRect: NSRect(origin: .zero, size: size), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false // ARC owns it; AppKit releasing it too crashes
        window.contentView = host
        window.orderFront(nil)
        for (name, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
            window.appearance = NSAppearance(named: appearance)
            host.layoutSubtreeIfNeeded()
            host.displayIfNeeded()
            RunLoop.main.run(until: Date().addingTimeInterval(1))
            guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { continue }
            host.cacheDisplay(in: host.bounds, to: rep)
            try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: "\(path)-\(name).png"))
        }
        window.close()
    }

    /// Dev aid: with HMK_DEBUG_KEYS=1, types a few synthetic key
    /// events into the first terminal view (x, ⌥3, ⌃C) to exercise input.
    @MainActor static func typeIfRequested() {
        guard ProcessInfo.processInfo.environment["HMK_DEBUG_KEYS"] == "1" else { return }
        DispatchQueue.main.asyncAfter(deadline: .now() + 3) {
            guard let window = NSApp.windows.first(where: { $0.isVisible }),
                  let view = findTerminal(window.contentView) else { return }
            window.makeKeyAndOrderFront(nil)
            window.makeFirstResponder(view)
            let keys: [(String, String, UInt16, NSEvent.ModifierFlags)] = [
                ("e", "e", 0x0E, []), ("c", "c", 0x08, []), ("h", "h", 0x04, []), ("o", "o", 0x1F, []), (" ", " ", 0x31, []),
                ("x", "x", 0x07, []), ("£", "3", 0x14, .option), ("\r", "\r", 0x24, []),
            ]
            for (chars, raw, code, mods) in keys {
                if let ev = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: mods, timestamp: 0, windowNumber: window.windowNumber,
                                             context: nil, characters: chars, charactersIgnoringModifiers: raw, isARepeat: false, keyCode: code) {
                    view.keyDown(with: ev)
                }
            }
        }
    }

    @MainActor private static func findTerminal(_ v: NSView?) -> TerminalNSView? {
        guard let v else { return nil }
        if let t = v as? TerminalNSView { return t }
        for s in v.subviews { if let t = findTerminal(s) { return t } }
        return nil
    }

    /// One window as it's drawn on screen, with the app's own Screen Recording permission.
    @MainActor private static func grabWindow(_ window: NSWindow) async -> CGImage? {
        guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true),
              let w = content.windows.first(where: { $0.windowID == CGWindowID(window.windowNumber) }) else { return nil }
        let config = SCStreamConfiguration()
        config.width = Int(w.frame.width * window.backingScaleFactor)
        config.height = Int(w.frame.height * window.backingScaleFactor)
        config.showsCursor = false
        return try? await SCScreenshotManager.captureImage(contentFilter: SCContentFilter(desktopIndependentWindow: w), configuration: config)
    }

    @MainActor private static func captureWindow(_ window: NSWindow, prefix: String) async {
        func grab() async -> CGImage? {
            guard let content = try? await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true),
                  let w = content.windows.first(where: { $0.windowID == CGWindowID(window.windowNumber) }) else { return nil }
            let config = SCStreamConfiguration()
            config.width = Int(w.frame.width * window.backingScaleFactor)
            config.height = Int(w.frame.height * window.backingScaleFactor)
            config.showsCursor = false
            return try? await SCScreenshotManager.captureImage(contentFilter: SCContentFilter(desktopIndependentWindow: w), configuration: config)
        }
        func write(_ image: CGImage?, _ name: String) {
            guard let image else { return }
            try? NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: "\(prefix)-\(name).png"))
        }
        let env = ProcessInfo.processInfo.environment
        for (name, appearance) in [("light", NSAppearance.Name.aqua), ("dark", .darkAqua)] {
            NSApp.appearance = NSAppearance(named: appearance)
            try? await Task.sleep(for: .seconds(1))
            write(await grab(), "window-\(name)")
        }
        guard let n = env["HMK_DEBUG_CAPTURE_ZOOM"].flatMap(Int.init), let m = AppModelRegistry.shared,
              let tab = m.selectedTabID.flatMap({ m.tabs[$0] }) else { return }
        let ids = AppModel.paneIDs(tab.layout)
        guard n >= 1, n <= ids.count else { return }
        var frame = 0
        for target in [ids[n - 1], ids[n - 1]] { // zoom, then unzoom
            PaneZoom.shared.toggle(target, model: m)
            let start = Date()
            while Date().timeIntervalSince(start) < PaneMotion.duration + 1.2 {
                write(await grab(), String(format: "zoom-%02d", frame))
                frame += 1
            }
        }
    }
}
