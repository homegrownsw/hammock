import Foundation
import Observation
import SwiftUI
import WebKit

// Files panes: a folder's tree, like an editor's file explorer. The daemon
// keeps what the pane shows (its folder, the folders open, the selection)
// and lists a folder at a time; the page (files.html, built from
// apple/Editor/src/files.js) draws the tree. This is the go-between, shared
// by the Mac and iOS apps: FilesSession talks to the daemon, FilesBridge to
// the page. Menus and what opening a file does are each app's own.

public typealias FilesInfo = Hammock_V1_FilesInfo

extension Hammock_V1_Pane {
    public var isFiles: Bool { kind == .files }
}

/// What a files page shows: the theme and font, and what you may do there.
public struct FilesConfig: Equatable {
    public var theme: TerminalTheme
    public var dark: Bool
    public var fontSize: Double
    public var canDrive: Bool
    /// The daemon renames, creates, moves and trashes files.
    public var ops = true
    /// The app's accent (the theme's), for the selection ("" its own).
    public var accent = ""
    /// The home folder on the pane's computer, shown as ~ ("" none known).
    public var home = ""

    @MainActor public init(theme: TerminalTheme, dark: Bool, fontSize: Double, canDrive: Bool, accent: Color? = nil, home: String = "") {
        self.theme = theme
        self.dark = dark
        self.fontSize = fontSize
        self.canDrive = canDrive
        self.accent = accent.map(DiffConfig.hex) ?? ""
        self.home = home
    }

    var json: [String: Any] {
        func hex(_ c: RGB) -> String { String(format: "#%02x%02x%02x", c.r, c.g, c.b) }
        #if os(iOS)
        let platform = "ios"
        #else
        let platform = "mac"
        #endif
        return [
            "theme": ["background": hex(theme.background), "foreground": hex(theme.foreground), "palette": theme.ansi.map(hex), "dark": dark],
            "font": ["size": fontSize], "canDrive": canDrive, "ops": ops, "platform": platform, "accent": accent, "home": home,
        ]
    }
}

public enum FilesError: LocalizedError {
    case offline
    public var errorDescription: String? { "Not connected to Hammock" }
}

// MARK: - The session

/// One files pane as this app shows it: lists folders for its pages, and
/// passes on what's done there.
@MainActor
@Observable
public final class FilesSession {
    public let paneID: String
    public var error = ""

    weak var model: AppModel?
    var refs = 0
    /// Every page showing the pane (two windows can show one tab).
    @ObservationIgnored private let pages = NSHashTable<FilesBridge>.weakObjects()
    /// The page commands go to: the newest (or last focused).
    @ObservationIgnored weak var bridge: FilesBridge?

    init(paneID: String, model: AppModel) {
        self.paneID = paneID
        self.model = model
    }

    /// Asks the daemon to watch what the pane shows while it's showing.
    func attach() { model?.connection?.send { $0.attachPane = .with { $0.paneID = paneID } } }
    func detach() { model?.connection?.send { $0.detachPane = .with { $0.paneID = paneID } } }

    func pageReady(_ b: FilesBridge) {
        pages.add(b)
        bridge = b
    }

    func pageFocused(_ b: FilesBridge) { bridge = b }

    /// Folders whose entries changed on disk: each page lists the ones it has again.
    func apply(_ c: Hammock_V1_FilesChanged) {
        for p in pages.allObjects { p.changed(c.paths) }
    }

    /// After reconnecting: every page lists what it shows again.
    func relist() {
        for p in pages.allObjects { p.relist() }
    }

    /// One of the page's commands (files.js `command`): the menus'.
    public func command(_ name: String) { bridge?.command(name) }

    func list(_ path: String, for page: FilesBridge) {
        guard let conn = model?.connection else {
            page.listing(["path": path, "entries": [], "error": FilesError.offline.localizedDescription])
            return
        }
        conn.call({ $0.listDir = .with { $0.paneID = self.paneID; $0.path = path } }) { [weak page] r in
            Task { @MainActor in
                switch r {
                case .success(let m): page?.listing(Self.json(m.dirListing))
                case .failure(let e): page?.listing(["path": path, "entries": [], "error": e.localizedDescription])
                }
            }
        }
    }

    /// Changes what the pane shows (FilesCommand).
    public func send(_ build: @escaping (inout Hammock_V1_FilesCommand) -> Void) {
        guard let conn = model?.connection else {
            error = FilesError.offline.localizedDescription
            return
        }
        conn.call({ m in m.filesCommand = .with { $0.paneID = self.paneID; build(&$0) } }) { [weak self] r in
            Task { @MainActor in
                if case .failure(let e) = r { self?.error = e.localizedDescription }
            }
        }
    }

    public func reveal(_ path: String) { send { $0.action = .reveal; $0.path = path } }
    public func setRoot(_ path: String) { send { $0.action = .setRoot; $0.path = path } }
    public func setHidden(_ on: Bool) { send { $0.action = .showHidden; $0.on = on } }

    static func json(_ l: Hammock_V1_DirListing) -> [String: Any] {
        [
            "path": l.path, "error": l.error, "truncated": l.truncated,
            "entries": l.entries.map { e -> [String: Any] in
                ["name": e.name, "dir": e.dir, "symlink": e.symlink, "size": NSNumber(value: e.size),
                 "mtime": NSNumber(value: e.mtimeUnixMs), "git": e.git, "ignored": e.ignored]
            },
        ]
    }

    static func infoJSON(_ f: FilesInfo) -> [String: Any] {
        [
            "root": f.root, "followGroup": f.followGroup, "expanded": f.expanded, "showHidden": f.showHidden,
            "selected": f.selected, "error": f.error, "repo": f.repo, "reveal": f.reveal, "revealSeq": NSNumber(value: f.revealSeq),
        ]
    }
}

extension AppModel {
    /// The pane's files session, attached on first use.
    public func acquireFiles(_ paneID: String) -> FilesSession {
        let s = filesSessions[paneID] ?? FilesSession(paneID: paneID, model: self)
        filesSessions[paneID] = s
        s.refs += 1
        if s.refs == 1 { s.attach() }
        return s
    }

    public func releaseFiles(_ paneID: String) {
        guard let s = filesSessions[paneID] else { return }
        s.refs -= 1
        if s.refs <= 0 {
            filesSessions[paneID] = nil
            s.detach()
        }
    }

    public func filesSession(_ paneID: String) -> FilesSession? { filesSessions[paneID] }

    /// The pane's files session, held until the lease is let go.
    public func leaseFiles(_ paneID: String) -> PaneLease<FilesSession> {
        PaneLease(acquireFiles(paneID)) { [self] in releaseFiles(paneID) }
    }

    /// After reconnecting: watched again, and listed again.
    func reattachFiles() {
        for s in filesSessions.values {
            s.attach()
            s.relist()
        }
    }

    /// Opens a files pane on root ("" the Group's folder, followed): a new
    /// tab, a split beside splitPane, or in replacePane's place. reveal: a
    /// file or folder in it to show.
    @discardableResult
    public func openFiles(root: String = "", reveal: String = "", groupID: String?, splitPane: String? = nil,
                          direction: SplitDirection = .horizontal, before: Bool = false, replacePane: String? = nil) async -> String? {
        let spec = Hammock_V1_PaneSpec.with { $0.files = .with { $0.root = root; $0.reveal = reveal } }
        var id: String?
        if let replacePane {
            id = await run { $0.replacePane = .with { $0.paneID = replacePane; $0.spec = spec } }?.created.id
        } else if let splitPane {
            id = await run {
                $0.createPane = .with {
                    $0.splitPaneID = splitPane
                    $0.direction = direction
                    $0.before = before
                    $0.spec = spec
                }
            }?.created.id
        } else if let groupID {
            id = await run { $0.createTab = .with { $0.groupID = groupID; $0.pane = spec } }?.created.id
            if let id { selectedTabID = id }
            return id
        }
        if let id { focus(id) }
        return id
    }

    /// Shows path in files pane pane: selected, its folders opened, scrolled to.
    public func revealInFiles(pane: String, path: String) {
        connection?.send { $0.filesCommand = .with { $0.paneID = pane; $0.action = .reveal; $0.path = path } }
    }

    /// A terminal beside paneID, in folder.
    @discardableResult
    public func splitTerminal(_ paneID: String, cwd: String, direction: SplitDirection = .horizontal) async -> String? {
        let size = initialSize(splitting: paneID, direction: direction)
        let id = await run {
            $0.createPane = .with {
                $0.splitPaneID = paneID
                $0.direction = direction
                $0.spec = .with {
                    $0.cwd = cwd
                    if let s = size { $0.cols = s.cols; $0.rows = s.rows }
                }
            }
        }?.created.id
        if let id { focus(id) }
        return id
    }

    /// The files pane in paneID's tab, if there is one.
    public func filesPane(besides paneID: String) -> String? {
        guard let p = panes[paneID], let tab = tab(of: p) else { return nil }
        return self.panes(in: tab).first { $0.isFiles }?.id
    }
}

// MARK: - The page

/// The pane's files page and its messages both ways.
@MainActor
public final class FilesBridge: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
    public let webView: WKWebView
    public let session: FilesSession
    private var ready = false
    private var config: FilesConfig?
    private var info: [String: Any] = [:]
    private var page: URL?
    public var onFocus: (() -> Void)?
    /// A file or folder opened: double-click or Return (a tap on the
    /// iPhone); how: "" its default.
    public var onOpen: ((_ path: String, _ dir: Bool, _ how: String) -> Void)?
    /// A right-click (touch and hold): the selection, which are folders,
    /// and where, in the page's coordinates. No paths: the folder's background.
    public var onMenu: ((_ paths: [String], _ dirs: [Bool], _ point: CGPoint) -> Void)?
    /// A drag of the selection started (the Mac takes it over).
    public var onDrag: ((_ paths: [String], _ point: CGPoint) -> Void)?
    /// Renaming, creating, trashing, moving: the page's in-place edits.
    public var onOp: ((_ message: [String: Any]) -> Void)?

    public init(session: FilesSession, makeWebView: (WKWebViewConfiguration) -> WKWebView) {
        self.session = session
        let conf = WKWebViewConfiguration()
        let ucc = WKUserContentController()
        conf.userContentController = ucc
        webView = makeWebView(conf)
        super.init()
        ucc.add(FilesWeakHandler(self), name: "hammock")
        webView.navigationDelegate = self
        #if os(macOS)
        webView.setValue(false, forKey: "drawsBackground")
        #else
        webView.isOpaque = false
        webView.backgroundColor = .clear
        webView.scrollView.backgroundColor = .clear
        webView.scrollView.bounces = false
        #endif
        #if DEBUG
        webView.isInspectable = true
        #endif
        if let url = Bundle.main.url(forResource: "files", withExtension: "html") {
            page = url
            webView.loadFileURL(url, allowingReadAccessTo: url.deletingLastPathComponent())
        } else {
            session.error = "The files view is missing from this build (files.html)"
        }
    }

    public func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame, let page, EditorBridge.isPage(message.frameInfo.request.url, page) else { return }
        guard let m = message.body as? [String: Any], let type = m["type"] as? String else { return }
        let paths = m["paths"] as? [String] ?? []
        func point() -> CGPoint { CGPoint(x: (m["x"] as? NSNumber)?.doubleValue ?? 0, y: (m["y"] as? NSNumber)?.doubleValue ?? 0) }
        switch type {
        case "ready":
            ready = true
            if let config { call("hammock.configure(c)", ["c": config.json]) }
            if !info.isEmpty { call("hammock.setInfo(i)", ["i": info]) }
            session.pageReady(self)
        case "list":
            if let p = m["path"] as? String { session.list(p, for: self) }
        case "expand":
            session.send { $0.action = .expand; $0.paths = paths }
        case "collapse":
            session.send { $0.action = .collapse; $0.paths = paths }
        case "select":
            session.send { $0.action = .select; $0.paths = paths }
        case "setRoot":
            session.setRoot(m["path"] as? String ?? "")
        case "hidden":
            session.setHidden(m["on"] as? Bool ?? false)
        case "open":
            if let p = m["path"] as? String { onOpen?(p, m["dir"] as? Bool ?? false, m["how"] as? String ?? "") }
        case "menu":
            onMenu?(paths, m["dirs"] as? [Bool] ?? [], point())
        case "drag":
            onDrag?(paths, point())
        case "rename", "create", "trash", "move":
            onOp?(m)
        case "focus":
            session.pageFocused(self)
            onFocus?()
        default:
            break
        }
    }

    /// The page shows files.html and nothing else.
    public func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                        decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        let url = action.request.url
        decisionHandler(page.map { EditorBridge.isPage(url, $0) } == true || url?.absoluteString == "about:blank" ? .allow : .cancel)
    }

    public func configure(_ c: FilesConfig) {
        guard c != config else { return }
        config = c
        if ready { call("hammock.configure(c)", ["c": c.json]) }
    }

    public func setInfo(_ f: FilesInfo) {
        let i = FilesSession.infoJSON(f)
        guard !NSDictionary(dictionary: i).isEqual(to: info) else { return }
        info = i
        if ready { call("hammock.setInfo(i)", ["i": i]) }
    }

    func listing(_ l: [String: Any]) { call("hammock.listing(l)", ["l": l]) }
    func changed(_ paths: [String]) { call("hammock.changed(p)", ["p": paths]) }
    func relist() {
        guard ready else { return }
        call("hammock.relist && hammock.relist()", [:])
    }
    public func command(_ name: String) { call("hammock.command(n)", ["n": name]) }
    public func focus() { call("hammock.focus()", [:]) }

    /// A drag over the page (the Mac's, back over its own tree): the
    /// folder it would drop into, highlighted (nil point: none).
    public func dragOver(_ p: CGPoint?) async -> String? {
        let r = try? await webView.callAsyncJavaScript("return hammock.dragOver(x, y)",
                                                       arguments: ["x": p.map { $0.x } ?? NSNull(), "y": p.map { $0.y } ?? NSNull()],
                                                       in: nil, contentWorld: .page)
        return r as? String
    }

    private func call(_ js: String, _ args: [String: Any]) {
        webView.callAsyncJavaScript(js, arguments: args, in: nil, in: .page) { r in
            if case .failure(let e) = r { DevLog.editor("files page: \(e.localizedDescription)") }
        }
    }
}

/// WKUserContentController keeps its handlers: this one lets the bridge go.
private final class FilesWeakHandler: NSObject, WKScriptMessageHandler {
    weak var target: (any WKScriptMessageHandler)?
    init(_ t: any WKScriptMessageHandler) { target = t }
    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.userContentController(ucc, didReceive: message)
    }
}
