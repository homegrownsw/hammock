import AppKit
import HammockKit
import SwiftUI

// Hammock Menu (ADR-0014): the menu bar item, a small app inside Hammock.app
// (Contents/Library/LoginItems), started at login with the server so it's
// there whether Hammock is open or not. It shows the server's state and
// controls, who's connected, who's watching the screen (and lights up
// while anyone is), and what needs you. It talks to the server over its
// socket, as Hammock does; its Info.plist names the same state dir and login
// item as the app it's in.

@main
struct HammockMenuApp: App {
    @NSApplicationDelegateAdaptor private var delegate: MenuAppDelegate

    init() {
        // One menu bar item: another copy already running (the login item
        // started one as the app did) keeps it.
        // Two started at once (the login item and the app) each see the
        // other: the newer one goes.
        let me = NSRunningApplication.current
        if let id = Bundle.main.bundleIdentifier,
           NSRunningApplication.runningApplications(withBundleIdentifier: id).contains(where: { other in
               other.processIdentifier != me.processIdentifier && !other.isTerminated
                   && (other.launchDate ?? .distantPast, other.processIdentifier) < (me.launchDate ?? .distantFuture, me.processIdentifier)
           }) {
            exit(0)
        }
    }

    // The item and its menu are AppKit's (StatusMenu, an NSMenu), not a
    // MenuBarExtra: its window kept the size and drawing of an earlier
    // menu when what's in it changed while it was closed.
    var body: some Scene {
        Settings { EmptyView() }
    }
}

final class MenuAppDelegate: NSObject, NSApplicationDelegate {
    private var menu: StatusMenu?

    func applicationDidFinishLaunching(_ notification: Notification) {
        MainActor.assumeIsolated { menu = StatusMenu(model: MenuModel()) }
    }
}

// MARK: The menu bar item

/// The menu bar item and its menu: a real NSMenu, so it opens, tracks a
/// press-drag-release, closes on a second click and takes the keyboard as
/// every menu does. The header and the screen sharing section are SwiftUI
/// views in it, updating as the server does; the rest are menu items,
/// rebuilt when what they'd show changes, even while the menu's open.
@MainActor
final class StatusMenu: NSObject, NSMenuDelegate {
    private let model: MenuModel
    private let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let menu = NSMenu()
    /// What the items were last built from: rebuilt only when it changes,
    /// so the item under the pointer stays highlighted between polls.
    private var built = ""

    init(model: MenuModel) {
        self.model = model
        super.init()
        item.autosaveName = "HammockMenu"
        item.button?.imagePosition = .imageLeading
        menu.delegate = self
        menu.autoenablesItems = false
        item.menu = menu
        render()
    }

    /// The item follows the model: its icon, whether it's there at all,
    /// and the menu's items.
    private func render() {
        withObservationTracking {
            item.isVisible = model.shown
            if !model.shown { menu.cancelTracking() }
            if let button = item.button {
                let viewers = model.status.screenViewers.count
                if model.server != .running {
                    button.image = Glyph.image(.warning)
                    button.title = ""
                    button.setAccessibilityLabel("Hammock: the server isn’t running")
                } else if viewers > 0 {
                    button.image = Glyph.image(.filled)
                    button.title = "\(viewers)"
                    button.setAccessibilityLabel(viewers == 1 ? "Hammock: 1 screen viewer" : "Hammock: \(viewers) screen viewers")
                } else {
                    button.image = Glyph.image(.plain)
                    button.title = ""
                    button.setAccessibilityLabel("Hammock")
                }
            }
            build()
        } onChange: { [weak self] in
            DispatchQueue.main.async { self?.render() }
        }
    }

    func menuWillOpen(_ menu: NSMenu) { model.tick() }

    private func build() {
        let s = model.status
        let running = model.server == .running
        let shape = [
            "\(model.server)", "\(s.screenViewers.isEmpty)",
            running ? "\(s.remoteEnabled) \(s.remoteAddresses.first ?? "")" : "",
            running ? s.connected.map { "\($0.name)/\($0.kind)" }.joined(separator: ",") : "",
            running ? s.needsYou.prefix(5).map { "\($0.id)/\($0.summary)" }.joined(separator: ",") + " \(s.grants)" : "",
        ].joined(separator: "|")
        guard shape != built else { return }
        built = shape

        menu.removeAllItems()
        menu.addItem(view(MenuHeader(model: model)))
        menu.addItem(.separator())
        if !s.screenViewers.isEmpty {
            menu.addItem(view(MenuSharing(model: model)))
            menu.addItem(.separator())
        }
        if running {
            menu.addItem(.sectionHeader(title: "Remote Access"))
            if s.remoteEnabled {
                menu.addItem(info("On", detail: s.remoteAddresses.first ?? ""))
                for c in s.connected {
                    menu.addItem(info(c.name.isEmpty ? "A device" : c.name,
                                      icon: c.kind == "phone" ? "iphone" : c.kind == "guest" ? "person" : "laptopcomputer",
                                      detail: c.kind == "guest" ? "guest" : "connected"))
                }
            } else {
                menu.addItem(action("Off", detail: "Settings ▸ Devices") { [model] in model.openHammock(settings: true) })
            }
            menu.addItem(.separator())
            let count = s.needsYou.count + Int(s.grants)
            if count > 0 {
                menu.addItem(.sectionHeader(title: "Needs You · \(count)"))
                for a in s.needsYou.prefix(5) {
                    menu.addItem(action(a.summary.isEmpty ? "An agent is waiting" : a.summary, icon: "exclamationmark.circle.fill", tint: .systemOrange) { [model] in model.openHammock() })
                }
                if s.grants > 0 {
                    let g = Int(s.grants)
                    menu.addItem(action(g == 1 ? "1 permission request" : "\(g) permission requests", icon: "hand.raised.fill", tint: .systemOrange) { [model] in model.openHammock() })
                }
                menu.addItem(.separator())
            }
        }
        menu.addItem(action("Open \(MenuModel.appName)") { [model] in model.openHammock() })
        menu.addItem(action("Settings…", key: ",") { [model] in model.openHammock(settings: true) })
        menu.addItem(.separator())
        switch model.server {
        case .running:
            menu.addItem(action("Restart Server") { [model] in model.restartServer() })
            menu.addItem(action("Stop Server…") { [model] in
                if let end = model.confirmStop(quitting: false) { model.stopServer(endPanes: end) }
            })
        case .notResponding:
            menu.addItem(action("Restart Server") { [model] in model.restartServer() })
        case .stopped:
            menu.addItem(action("Start Server") { [model] in model.startServer() })
        }
        menu.addItem(.separator())
        menu.addItem(action("Quit \(MenuModel.appName)", key: "q") { [model] in model.quitHammock() })
        menu.addItem(action("Quit \(MenuModel.appName) and Stop Server…") { [model] in
            if let end = model.confirmStop(quitting: true) { model.quitAll(endPanes: end) }
        })
    }

    /// A SwiftUI view as a menu item, as wide as the menu.
    private func view(_ content: some View) -> NSMenuItem {
        let host = NSHostingView(rootView: content.frame(width: 300, alignment: .leading).padding(.horizontal, 6).padding(.vertical, 2))
        host.frame.size = host.fittingSize
        host.autoresizingMask = [.width]
        let item = NSMenuItem()
        item.view = host
        return item
    }

    private func action(_ title: String, icon: String? = nil, tint: NSColor? = nil, detail: String = "",
                        key: String = "", run: @escaping @MainActor () -> Void) -> NSMenuItem {
        let item = ActionItem(title: title, run: run)
        item.keyEquivalent = key
        decorate(item, icon: icon, tint: tint, detail: detail)
        return item
    }

    /// A line that says how things are: not a command, so not chosen.
    private func info(_ title: String, icon: String? = nil, detail: String = "") -> NSMenuItem {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        decorate(item, icon: icon, tint: nil, detail: detail)
        return item
    }

    private func decorate(_ item: NSMenuItem, icon: String?, tint: NSColor?, detail: String) {
        if !detail.isEmpty { item.subtitle = detail }
        if let icon, var image = NSImage(systemSymbolName: icon, accessibilityDescription: nil) {
            if let tint, let tinted = image.withSymbolConfiguration(.init(paletteColors: [tint])) { image = tinted }
            item.image = image
        }
    }
}

/// A menu item that runs a closure when chosen (after the menu's closed).
private final class ActionItem: NSMenuItem {
    private let run: @MainActor () -> Void

    init(title: String, run: @escaping @MainActor () -> Void) {
        self.run = run
        super.init(title: title, action: #selector(choose), keyEquivalent: "")
        target = self
    }

    required init(coder: NSCoder) { fatalError("not from a nib") }

    @objc private func choose() { MainActor.assumeIsolated { run() } }
}

typealias ServerStatusReply = Hammock_V1_ServerStatusReply

@MainActor
@Observable
final class MenuModel {
    enum Server: Equatable { case running, notResponding, stopped }

    private(set) var server: Server = .stopped
    private(set) var status = ServerStatusReply()
    /// Settings ▸ General ▸ Show Hammock in the menu bar: always (the default),
    /// or only while something's happening (a screen shared, the server down).
    private(set) var onlyWhenActive = false
    var shown: Bool { !onlyWhenActive || server != .running || !status.screenViewers.isEmpty }

    @ObservationIgnored private var conn: DaemonConnection?
    @ObservationIgnored private var connected = false
    @ObservationIgnored private var failures = 0

    init() {
        connect()
        // In the common modes: it keeps ticking while the menu's open (tracking).
        let timer = Timer(timeInterval: 2, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.tick() }
        }
        RunLoop.main.add(timer, forMode: .common)
        tick()
    }

    // MARK: The server

    private func connect() {
        let c = DaemonConnection(socketPath: DaemonConnection.defaultSocketPath)
        c.onStateChange = { [weak self] ready, _ in
            DispatchQueue.main.async {
                guard let self, self.conn === c else { return }
                self.connected = ready
                if ready {
                    c.send { $0.hello = .with { $0.protocolVersion = 1; $0.clientKind = "menu"; $0.clientName = "Hammock Menu" } }
                    self.tick()
                } else {
                    self.conn = nil
                    self.server = .stopped
                }
            }
        }
        conn = c
        c.connect()
    }

    /// Asks the server how it is (every 2 s, and when the menu opens).
    func tick() {
        onlyWhenActive = Config.value("menu-bar") == "active"
        guard let conn, connected else {
            if conn == nil { connect() }
            return
        }
        conn.call({ $0.serverStatus = .init() }) { [weak self] result in
            DispatchQueue.main.async {
                guard let self else { return }
                switch result {
                case .success(let msg):
                    self.status = msg.serverStatusReply
                    self.server = .running
                    self.failures = 0
                case .failure:
                    self.failures += 1
                    // Connected but not answering: hung, or busy starting.
                    if self.failures >= 2 { self.server = .notResponding }
                }
            }
        }
    }

    var uptime: String {
        guard status.startedUnixMs > 0 else { return "" }
        let secs = Date().timeIntervalSince1970 - Double(status.startedUnixMs) / 1000
        let f = DateComponentsFormatter()
        f.unitsStyle = .full
        f.maximumUnitCount = 1
        f.allowedUnits = [.day, .hour, .minute]
        return f.string(from: max(secs, 60)) ?? ""
    }

    // MARK: The app it's in

    /// Hammock.app (or Hammock β.app): Contents/Library/LoginItems/Hammock Menu.app's.
    static let app: Bundle? = {
        let url = Bundle.main.bundleURL.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        return Bundle(url: url)
    }()
    static var appName: String { (app?.object(forInfoDictionaryKey: "CFBundleName") as? String) ?? "Hammock" }
    static var appVersion: String { (app?.object(forInfoDictionaryKey: "CFBundleVersion") as? String) ?? "?" }
    /// The version of the server this app ships (its hmkd's), asked again
    /// only when hmkd changes (an update in place). The app's own
    /// CFBundleVersion is a build number, not comparable.
    private static var bundled: (date: Date, version: String?)?
    static var bundledServerVersion: String? {
        guard let url = app?.bundleURL.appendingPathComponent("Contents/MacOS/hmkd"),
              let date = (try? FileManager.default.attributesOfItem(atPath: url.path))?[.modificationDate] as? Date
        else { return nil }
        if let b = bundled, b.date == date { return b.version }
        let p = Process(), out = Pipe()
        p.executableURL = url
        p.arguments = ["-version"]
        p.standardOutput = out
        p.standardError = FileHandle.nullDevice
        var version: String?
        if (try? p.run()) != nil {
            let data = out.fileHandleForReading.readDataToEndOfFile()
            p.waitUntilExit()
            let v = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
            if p.terminationStatus == 0, !v.isEmpty { version = v }
        }
        bundled = (date, version)
        return version
    }
    /// The running server isn't the one this app ships: restarting it
    /// starts the app's.
    var versionsDiffer: Bool {
        guard !status.version.isEmpty, let bundled = Self.bundledServerVersion else { return false }
        return status.version != bundled
    }

    // MARK: Actions

    func openHammock(settings: Bool = false) {
        guard let url = Self.app?.bundleURL, let id = Self.app?.bundleIdentifier else { return }
        let running = !NSRunningApplication.runningApplications(withBundleIdentifier: id).isEmpty
        let cfg = NSWorkspace.OpenConfiguration()
        cfg.activates = true
        // Not running: it opens Settings once it's up (told by argument);
        // running, it's told now (it listens for this).
        if settings, !running { cfg.arguments = ["--open-settings"] }
        NSWorkspace.shared.openApplication(at: url, configuration: cfg) { _, _ in
            guard settings, running else { return }
            DistributedNotificationCenter.default().postNotificationName(.init("com.homegrownsw.hammock.openSettings"), object: id,
                                                                        userInfo: nil, deliverImmediately: true)
        }
    }

    func endScreen(_ sessionID: String?) {
        conn?.send { $0.endScreenSession = .with { if let sessionID { $0.sessionID = sessionID } else { $0.all = true } } }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) { self.tick() }
    }

    private var agentLabel: String? { Self.app?.object(forInfoDictionaryKey: "HammockAgentLabel") as? String }

    func startServer() {
        guard let agentLabel else { return openHammock() }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        p.arguments = ["kickstart", "gui/\(getuid())/\(agentLabel)"]
        try? p.run()
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { if self.conn == nil { self.connect() } }
    }

    /// Stops the server; with endPanes, what's running in every pane ends
    /// too (the panes come back with fresh shells, agents resumed).
    func stopServer(endPanes: Bool = false) {
        conn?.send { $0.shutdownDaemon = .with { $0.endPanes = endPanes } }
    }

    func restartServer() {
        stopServer()
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) { self.startServer() }
    }

    func quitHammock() {
        guard let id = Self.app?.bundleIdentifier else { return }
        NSRunningApplication.runningApplications(withBundleIdentifier: id).forEach { $0.terminate() }
    }

    /// Quit Hammock and Stop Server: everything goes, the menu bar item too.
    /// Opening Hammock brings all three back (it starts the server and this).
    func quitAll(endPanes: Bool = false) {
        quitHammock()
        stopServer(endPanes: endPanes)
        // Once the shutdown's been written to the socket.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { NSApp.terminate(nil) }
    }

    /// What stopping the server means, asked first: nil if cancelled, else
    /// whether to end what's running in the panes too.
    func confirmStop(quitting: Bool) -> Bool? {
        let a = NSAlert()
        a.messageText = quitting ? "Quit \(Self.appName) and stop the Hammock Server?" : "Stop the Hammock Server?"
        var lost = ["your iPhone and other Macs can’t reach this Mac", "agents don’t notify you"]
        let viewers = status.screenViewers.count
        if viewers > 0 { lost.append(viewers == 1 ? "1 screen session ends" : "\(viewers) screen sessions end") }
        let back = quitting ? "when you next open \(Self.appName)" : "when it starts"
        a.informativeText = "Unless you end them, your panes keep running and come back \(back). Until then, " + lost.joined(separator: ", ") + "."
        a.addButton(withTitle: quitting ? "Quit and Stop" : "Stop Server")
        a.addButton(withTitle: "Cancel")
        a.buttons.first?.hasDestructiveAction = true
        // Off each time: ending them can't be undone. The panes themselves
        // stay, with fresh shells (and agents resumed) when it starts again.
        a.showsSuppressionButton = true
        a.suppressionButton?.title = "Also end what’s running in your panes"
        a.suppressionButton?.state = .off
        // This helper has no icon of its own: the app it's in lends its.
        if let app = Self.app { a.icon = NSWorkspace.shared.icon(forFile: app.bundlePath) }
        NSApp.activate()
        guard a.runModal() == .alertFirstButtonReturn else { return nil }
        return a.suppressionButton?.state == .on
    }
}

/// ~/.config/hammock/config (or $HMK_CONFIG_DIR's, or a β's in its state
/// dir): a key's value, as the app and the daemon read it.
enum Config {
    static var dir: String {
        if let d = ProcessInfo.processInfo.environment["HMK_CONFIG_DIR"] { return d }
        if let state = Bundle.main.object(forInfoDictionaryKey: "HammockStateDir") as? String, state != "Hammock" {
            return NSHomeDirectory() + "/Library/Application Support/" + state
        }
        return NSHomeDirectory() + "/.config/hammock"
    }

    static func value(_ key: String) -> String? {
        guard let text = try? String(contentsOfFile: dir + "/config", encoding: .utf8) else { return nil }
        for line in text.split(separator: "\n") {
            let parts = line.split(separator: "=", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
            if parts.count == 2, parts[0] == key { return parts[1].trimmingCharacters(in: CharacterSet(charactersIn: "\"")) }
        }
        return nil
    }
}

// MARK: The icon

/// Hammock's mark, the brush tilde, as a menu bar template image (it takes
/// the bar's colour): plain, filled (a screen's shared: the tilde cut out of
/// a rounded badge) or with a warning badge.
enum Glyph {
    enum Kind { case plain, filled, warning }

    static func image(_ kind: Kind) -> NSImage {
        let size = NSSize(width: 19, height: 16)
        let img = NSImage(size: size, flipped: true) { _ in
            guard let cg = NSGraphicsContext.current?.cgContext else { return false }
            cg.setFillColor(NSColor.black.cgColor)
            cg.setStrokeColor(NSColor.black.cgColor)
            cg.setLineJoin(.round)
            func mark(in rect: CGRect) {
                let path = HammockMark.path(fitting: rect)
                let scale = rect.width / HammockMark.bounds.width
                cg.addPath(path)
                cg.fillPath()
                cg.addPath(path)
                cg.setLineWidth(HammockMark.tipSoftening * scale)
                cg.strokePath()
            }
            switch kind {
            case .plain:
                mark(in: CGRect(x: 0.5, y: 3, width: 18, height: 10))
            case .filled:
                cg.addPath(CGPath(roundedRect: CGRect(x: 0.5, y: 1, width: 18, height: 14), cornerWidth: 4, cornerHeight: 4, transform: nil))
                cg.fillPath()
                cg.setBlendMode(.clear)
                mark(in: CGRect(x: 2.5, y: 4, width: 14, height: 8))
                cg.setBlendMode(.normal)
            case .warning:
                mark(in: CGRect(x: 0, y: 1, width: 16, height: 9))
                // A small "!" badge at the bottom right, cut out of the mark.
                cg.setBlendMode(.clear)
                cg.fillEllipse(in: CGRect(x: 10.5, y: 6.5, width: 9.5, height: 9.5))
                cg.setBlendMode(.normal)
                cg.fillEllipse(in: CGRect(x: 11.5, y: 7.5, width: 7.5, height: 7.5))
                cg.setBlendMode(.clear)
                cg.fill(CGRect(x: 14.6, y: 9, width: 1.3, height: 3))
                cg.fill(CGRect(x: 14.6, y: 12.8, width: 1.3, height: 1.2))
                cg.setBlendMode(.normal)
            }
            return true
        }
        img.isTemplate = true
        return img
    }
}

// MARK: The menu's views

/// The top of the menu: the server, how it is, and the versions.
struct MenuHeader: View {
    let model: MenuModel

    var body: some View {
        HStack(spacing: 12) {
            if let icon = MenuModel.app.map({ NSWorkspace.shared.icon(forFile: $0.bundlePath) }) {
                Image(nsImage: icon).resizable().frame(width: 40, height: 40)
            }
            VStack(alignment: .leading, spacing: 2) {
                Text("Hammock Server").font(.headline)
                HStack(spacing: 6) {
                    Circle().fill(dot).frame(width: 8, height: 8)
                    Text(stateLine).font(.callout).foregroundStyle(.secondary)
                }
            }
            Spacer()
        }
        .padding(.horizontal, 10).padding(.top, 8)
        .overlay(alignment: .bottomLeading) { EmptyView() }
        .safeAreaInset(edge: .bottom) {
            Grid(alignment: .leading, horizontalSpacing: 14, verticalSpacing: 2) {
                GridRow {
                    Text(MenuModel.appName).foregroundStyle(.secondary)
                    Text(MenuModel.appVersion)
                }
                if model.server == .running {
                    GridRow {
                        Text("Server").foregroundStyle(.secondary)
                        Text(model.status.version + (model.versionsDiffer ? "  (restart to update)" : ""))
                            .foregroundStyle(model.versionsDiffer ? Color.orange : .primary)
                    }
                }
            }
            .font(.caption)
            .lineLimit(1)
            .padding(.leading, 62).padding(.top, 4)
        }
    }

    private var dot: Color {
        switch model.server {
        case .running: .green
        case .notResponding: .orange
        case .stopped: .red
        }
    }

    private var stateLine: String {
        switch model.server {
        case .running:
            let panes = model.status.panes == 1 ? "1 pane" : "\(model.status.panes) panes"
            return "Running · up \(model.uptime) · \(panes)"
        case .notResponding: return "Not responding"
        case .stopped: return "Stopped"
        }
    }
}

/// Who's watching this Mac's screen, each with an End button.
struct MenuSharing: View {
    let model: MenuModel

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label("Sharing this Mac’s screen", systemImage: "display")
                .font(.callout.weight(.semibold))
                .foregroundStyle(Color.accentColor)
            ForEach(model.status.screenViewers, id: \.sessionID) { v in
                HStack(spacing: 10) {
                    Image(systemName: v.kind == "phone" ? "iphone" : "laptopcomputer").frame(width: 16)
                    VStack(alignment: .leading, spacing: 0) {
                        Text(v.name.isEmpty ? "A device" : v.name)
                        Text(viewerLine(v)).font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("End") { model.endScreen(v.sessionID) }
                        .controlSize(.small)
                }
            }
            if model.status.screenViewers.count > 1 {
                Button("End All Sessions", role: .destructive) { model.endScreen(nil) }
                    .controlSize(.small)
            }
        }
        .padding(10)
        .background(Color.accentColor.opacity(0.09), in: RoundedRectangle(cornerRadius: 10))
    }

    private func viewerLine(_ v: Hammock_V1_ScreenViewer) -> String {
        let mins = max(1, Int(Date().timeIntervalSince1970 - Double(v.sinceUnixMs) / 1000) / 60)
        return (v.controlling ? "controlling" : "watching") + " · \(mins) min"
    }
}
