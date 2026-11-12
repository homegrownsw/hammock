import Foundation
import Observation
import SwiftUI
import WebKit

// Diff panes: a git diff to review, laid out like a pull request's Files
// Changed. The daemon works the diff out; the page (diff.html, built from
// apple/Editor/src/diff.js) shows it with CodeMirror's merge views. This is
// the go-between, shared by the Mac and iOS apps: DiffSession talks to the
// daemon, DiffBridge to the page.

public typealias DiffInfo = Hammock_V1_DiffInfo

extension Pref {
    /// "unified" (one column) or "split" (old and new side by side).
    public static let diffStyle = Pref("diff-style", defaults: "diffStyle", .string, "unified")
    /// "show" or "ignore" (lines that differ only in their spaces count as the same).
    public static let diffWhitespace = Pref("diff-whitespace", defaults: "diffWhitespace", .string, "show")
    public static let diffWrap = Pref("diff-wrap", defaults: "diffWrap", false)

    public static var diff: [Pref] { [diffStyle, diffWhitespace, diffWrap] }
}

extension Hammock_V1_Pane {
    public var isDiff: Bool { kind == .diff }
}

/// What a diff page shows: the theme and font, and the view, the
/// settings with the pane's own choices over them.
public struct DiffConfig: Equatable {
    public var theme: TerminalTheme
    public var dark: Bool
    public var fontFamily: String
    public var fontSize: Double
    public var style: String
    public var whitespace: String
    public var wrap: Bool
    /// The pane has choices of its own (Use My Settings clears them).
    public var overridden: Bool
    public var canDrive: Bool
    /// Reviews: the agent comments go to, the agents that could take them
    /// (this Group's), and whether you can send (a guest can't).
    public var target: DiffAgent?
    public var agents: [DiffAgent] = []
    public var canSend = true
    /// The app's accent (the theme's), for the page's buttons ("" its own).
    public var accent = ""

    /// From the settings (Prefs), the pane's choices and the platform's theme and font.
    @MainActor public static func from(theme: TerminalTheme, dark: Bool, fontFamily: String, fontSize: Double,
                                       view: Hammock_V1_DiffView, canDrive: Bool, target: DiffAgent? = nil,
                                       agents: [DiffAgent] = [], canSend: Bool = true, accent: Color? = nil) -> DiffConfig {
        let p = Prefs.shared
        let size = p.string(.editorFontSize).isEmpty ? fontSize : p.double(.editorFontSize)
        let style = view.style.isEmpty ? p.string(.diffStyle) : view.style
        let ws = view.whitespace.isEmpty ? p.string(.diffWhitespace) : view.whitespace
        let wrap = view.wrap.isEmpty ? p.bool(.diffWrap) : view.wrap == "on"
        return DiffConfig(theme: theme, dark: dark, fontFamily: fontFamily, fontSize: size,
                          style: style == "split" ? "split" : "unified", whitespace: ws == "ignore" ? "ignore" : "show", wrap: wrap,
                          overridden: !(view.style.isEmpty && view.whitespace.isEmpty && view.wrap.isEmpty), canDrive: canDrive,
                          target: target, agents: agents, canSend: canSend, accent: accent.map(Self.hex) ?? "")
    }

    /// A colour as #rrggbb (sRGB).
    @MainActor static func hex(_ c: Color) -> String {
        #if os(macOS)
        guard let n = NSColor(c).usingColorSpace(.sRGB) else { return "" }
        let (r, g, b) = (n.redComponent, n.greenComponent, n.blueComponent)
        #else
        var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
        guard UIColor(c).getRed(&r, green: &g, blue: &b, alpha: &a) else { return "" }
        #endif
        func v(_ x: CGFloat) -> Int { Int((min(max(x, 0), 1) * 255).rounded()) }
        return String(format: "#%02x%02x%02x", v(r), v(g), v(b))
    }

    var json: [String: Any] {
        func hex(_ c: RGB) -> String { String(format: "#%02x%02x%02x", c.r, c.g, c.b) }
        #if os(iOS)
        let platform = "ios"
        #else
        let platform = "mac"
        #endif
        return [
            "theme": ["background": hex(theme.background), "foreground": hex(theme.foreground), "cursor": hex(theme.cursor),
                      "selection": hex(theme.selection), "palette": theme.ansi.map(hex), "dark": dark],
            "font": ["family": fontFamily, "size": fontSize],
            "view": ["style": style, "whitespace": whitespace, "wrap": wrap],
            "overridden": overridden, "canDrive": canDrive, "platform": platform,
            "target": target.map { $0.json } ?? NSNull(), "agents": agents.map(\.json), "canSend": canSend,
            "accent": accent,
        ]
    }
}

/// An agent review comments can go to.
public struct DiffAgent: Equatable, Sendable {
    public var id: String
    public var name: String
    public var detail: String

    public init(id: String, name: String, detail: String) {
        self.id = id
        self.name = name
        self.detail = detail
    }

    var json: [String: Any] { ["id": id, "name": name, "detail": detail] }
}

extension AppModel {
    /// The agents a diff pane's comments can go to: those running in its
    /// Group (the pane's target first, even if it's elsewhere).
    public func diffAgents(for paneID: String) -> (target: DiffAgent?, agents: [DiffAgent]) {
        guard let pane = panes[paneID], pane.isDiff else { return (nil, []) }
        let group = self.group(ofPane: paneID)?.id
        func agent(_ p: Pane) -> DiffAgent {
            let tab = self.tab(of: p).map { title(of: $0) } ?? ""
            return DiffAgent(id: p.id, name: AgentCatalog.shortName(of: p.agent.harness), detail: tab)
        }
        var list: [DiffAgent] = []
        for p in panes.values where p.hasAgent && p.agent.state != .exited && p.id != paneID && self.group(ofPane: p.id)?.id == group {
            list.append(agent(p))
        }
        list.sort { ($0.detail, $0.id) < ($1.detail, $1.id) }
        let target = panes[pane.diff.targetPaneID].map(agent)
        return (target, list)
    }
}

// MARK: - The session

/// One diff pane as this app shows it: reads the diff from the daemon for
/// its page, and passes on what's asked of it there (another source, the
/// pane's view, a refresh).
@MainActor
@Observable
public final class DiffSession {
    public let paneID: String
    public var error = ""
    /// The generation the page shows.
    public private(set) var loadedGeneration: UInt64 = 0
    public private(set) var loading = false

    weak var model: AppModel?
    var refs = 0
    /// The page commands go to: the newest (or last focused).
    @ObservationIgnored weak var bridge: DiffBridge?
    /// Every page showing the pane (two windows can show one tab): each
    /// gets the diff and its review, and its own files.
    @ObservationIgnored private let pages = NSHashTable<DiffBridge>.weakObjects()
    /// What the page was last given, for a page that loads after (a second window).
    @ObservationIgnored private var content: [String: Any]?
    @ObservationIgnored private var reviewJSON: [String: Any]?

    @ObservationIgnored private var texts: [String: [String: Any]] = [:]

    init(paneID: String, model: AppModel) {
        self.paneID = paneID
        self.model = model
    }

    /// Asks the daemon to watch the repository while the pane's showing.
    func attach() { model?.connection?.send { $0.attachPane = .with { $0.paneID = paneID } } }
    func detach() { model?.connection?.send { $0.detachPane = .with { $0.paneID = paneID } } }

    func pageReady(_ b: DiffBridge) {
        pages.add(b)
        bridge = b
        if let content {
            b.load(content)
            if let reviewJSON { b.setReview(reviewJSON) }
        } else {
            load(refresh: false)
        }
    }

    /// The review, as the daemon sends it (on reading, and as it changes).
    func apply(_ r: Hammock_V1_Review) {
        let j = Self.json(r)
        reviewJSON = j
        for p in pages.allObjects { p.setReview(j) }
    }

    func pageFocused(_ b: DiffBridge) { bridge = b }

    /// Shows the next reply you haven't seen (and marks it seen).
    public func showNextUnread() { bridge?.nextUnread() }

    func readReview() {
        model?.connection?.call({ $0.readReview = .with { $0.paneID = self.paneID } }) { [weak self] r in
            Task { @MainActor in
                if case .success(let m) = r { self?.apply(m.review) }
            }
        }
    }

    /// A review command from the page (add a thread, reply, resolve, send…).
    func reviewCommand(_ c: [String: Any]) {
        guard let conn = model?.connection else {
            error = DiffError.offline.localizedDescription
            return
        }
        var m = Hammock_V1_ReviewCommand()
        m.paneID = paneID
        m.action = switch c["action"] as? String {
        case "addThread": .addThread
        case "reply": .reply
        case "setState": .setState
        case "send": .send
        case "deleteComment": .deleteComment
        case "setTarget": .setTarget
        case "markRead": .markRead
        case "editComment": .editComment
        default: .unspecified
        }
        m.threadID = c["threadId"] as? String ?? ""
        m.commentID = c["commentId"] as? String ?? ""
        m.path = c["path"] as? String ?? ""
        m.side = c["side"] as? String == "old" ? .old : .new
        m.line = UInt32(c["line"] as? Int ?? 0)
        m.endLine = UInt32(c["endLine"] as? Int ?? 0)
        m.text = c["text"] as? String ?? ""
        m.send = c["send"] as? Bool ?? false
        m.state = switch c["state"] as? String {
        case "addressed": .addressed
        case "resolved": .resolved
        default: .open
        }
        m.commentIds = c["commentIds"] as? [String] ?? []
        m.targetPaneID = c["targetPaneId"] as? String ?? ""
        m.quote = c["quote"] as? String ?? ""
        m.before = c["before"] as? String ?? ""
        m.after = c["after"] as? String ?? ""
        let msg = m
        conn.call({ $0.reviewCommand = msg }) { [weak self] r in
            Task { @MainActor in
                guard let self else { return }
                switch r {
                case .success(let reply):
                    if reply.reviewReply.hasReview { self.apply(reply.reviewReply.review) }
                    if reply.reviewReply.queued > 0 {
                        self.error = "Held until the agent has your answer: it's waiting on you"
                    }
                case .failure(let e):
                    self.error = e.localizedDescription
                    self.readReview() // what the daemon has, comments saved but not sent included
                }
            }
        }
    }

    /// The diff's files, afresh from the daemon (refresh: worked out again).
    public func load(refresh: Bool) {
        guard let conn = model?.connection else {
            error = DiffError.offline.localizedDescription
            return
        }
        loading = true
        conn.call({ $0.readDiff = .with { $0.paneID = self.paneID; $0.refresh = refresh } }) { [weak self] r in
            Task { @MainActor in
                guard let self else { return }
                self.loading = false
                switch r {
                case .success(let m):
                    let c = m.diffContent
                    self.loadedGeneration = c.generation
                    let json = Self.json(c)
                    if self.content?["generation"] as? UInt64 != c.generation { self.texts = [:] }
                    self.content = json
                    self.error = ""
                    for p in self.pages.allObjects { p.load(json) }
                    self.readReview()
                case .failure(let e):
                    self.error = e.localizedDescription
                }
            }
        }
    }

    /// A page asking for a file's two sides: they go to that page.
    func needFile(_ path: String, generation: UInt64, for page: DiffBridge) {
        if let t = texts[path], (t["generation"] as? NSNumber)?.uint64Value == generation {
            page.fileText(t)
            return
        }
        guard let conn = model?.connection else {
            page.fileError(path, generation: generation, DiffError.offline.localizedDescription)
            return
        }
        conn.call({ $0.readDiffFile = .with { $0.paneID = self.paneID; $0.path = path; $0.generation = generation } }) { [weak self, weak page] r in
            Task { @MainActor in
                guard let self else { return }
                switch r {
                case .success(let m):
                    let t = m.diffFileText
                    var j: [String: Any] = [
                        "path": t.path, "generation": NSNumber(value: generation), "oldText": t.oldText, "newText": t.newText,
                        "mime": t.mime, "oldSize": NSNumber(value: t.oldSize), "newSize": NSNumber(value: t.newSize), "tooLarge": t.tooLarge,
                    ]
                    if !t.oldData.isEmpty { j["oldData"] = t.oldData.base64EncodedString() }
                    if !t.newData.isEmpty { j["newData"] = t.newData.base64EncodedString() }
                    // Kept for another page showing the pane (small ones: big ones are read again).
                    if t.oldText.utf8.count + t.newText.utf8.count + t.oldData.count + t.newData.count < 2_000_000 { self.texts[path] = j }
                    page?.fileText(j)
                case .failure(let e):
                    page?.fileError(path, generation: generation, e.localizedDescription)
                }
            }
        }
    }

    /// Compares something else (the page's source menu): kind is
    /// "uncommitted", "branch", "commit" or "range".
    public func setSource(_ s: Hammock_V1_DiffSource) {
        Task {
            do {
                try await command { $0.action = .setSource; $0.source = s }
                load(refresh: false)
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    /// The pane's own view (nil: back to the settings').
    public func setView(_ v: Hammock_V1_DiffView?) {
        Task {
            do {
                try await command { $0.action = .setView; $0.view = v ?? .init() }
            } catch {
                self.error = error.localizedDescription
            }
        }
    }

    private func command(_ build: @escaping (inout Hammock_V1_DiffCommand) -> Void) async throws {
        guard let conn = model?.connection else { throw DiffError.offline }
        _ = try await conn.call { m in
            m.diffCommand = .with {
                $0.paneID = self.paneID
                build(&$0)
            }
        }
    }

    // MARK: To the page

    static func json(_ c: Hammock_V1_DiffContent) -> [String: Any] {
        [
            "generation": NSNumber(value: c.generation),
            "files": c.files.map { f -> [String: Any] in
                let status = switch f.status {
                case .added: "added"
                case .deleted: "deleted"
                case .renamed: "renamed"
                case .copied: "copied"
                case .typeChanged: "typeChanged"
                default: "modified"
                }
                return ["path": f.path, "oldPath": f.oldPath, "status": status, "added": Int(f.added), "removed": Int(f.removed),
                        "binary": f.binary, "generated": f.generated, "untracked": f.untracked]
            },
            "commits": c.commits.map { ["sha": $0.sha, "subject": $0.subject, "author": $0.author, "unix": NSNumber(value: $0.unix)] },
            "branches": c.branches,
            "defaultBranch": c.defaultBranch,
        ]
    }

    static func json(_ r: Hammock_V1_Review) -> [String: Any] {
        [
            "id": r.id,
            "threads": r.threads.map { t -> [String: Any] in
                let state = switch t.state {
                case .addressed: "addressed"
                case .resolved: "resolved"
                default: "open"
                }
                return [
                    "id": t.id, "path": t.path, "side": t.side == .old ? "old" : "new", "line": Int(t.line),
                    "endLine": Int(max(t.endLine, t.line)), "state": state, "outdated": t.outdated, "quote": t.quote,
                    "comments": t.comments.map { c -> [String: Any] in
                        let delivery = switch c.delivery {
                        case .unsent: "unsent"
                        case .queued: "queued"
                        case .sent: "sent"
                        default: "none"
                        }
                        return ["id": c.id, "author": c.author, "agent": c.agent, "text": c.text, "unixMs": NSNumber(value: c.unixMs),
                                "delivery": delivery, "unread": c.unread]
                    },
                ]
            },
        ]
    }

    static func infoJSON(_ d: DiffInfo) -> [String: Any] {
        [
            "title": d.title, "stale": d.stale, "error": d.error, "generation": NSNumber(value: d.generation),
            "files": Int(d.files), "added": Int(d.added), "removed": Int(d.removed), "branch": d.branch, "repo": d.repo,
            "source": sourceJSON(d.source),
        ]
    }

    static func sourceJSON(_ s: Hammock_V1_DiffSource) -> [String: Any] {
        let kind = switch s.kind {
        case .branch: "branch"
        case .commit: "commit"
        case .range: "range"
        default: "uncommitted"
        }
        return ["kind": kind, "base": s.base, "rev": s.rev, "withUncommitted": s.withUncommitted, "mergeBase": s.mergeBase]
    }

    static func source(_ j: [String: Any]) -> Hammock_V1_DiffSource {
        .with {
            $0.kind = switch j["kind"] as? String {
            case "branch": .branch
            case "commit": .commit
            case "range": .range
            default: .uncommitted
            }
            $0.base = j["base"] as? String ?? ""
            $0.rev = j["rev"] as? String ?? ""
            $0.withUncommitted = j["withUncommitted"] as? Bool ?? false
            $0.mergeBase = j["mergeBase"] as? Bool ?? false
        }
    }
}

public enum DiffError: LocalizedError {
    case offline
    public var errorDescription: String? { "Not connected to Hammock" }
}

extension AppModel {
    /// The pane's diff session, attached on first use.
    public func acquireDiff(_ paneID: String) -> DiffSession {
        let s = diffSessions[paneID] ?? DiffSession(paneID: paneID, model: self)
        diffSessions[paneID] = s
        s.refs += 1
        if s.refs == 1 { s.attach() }
        return s
    }

    public func releaseDiff(_ paneID: String) {
        guard let s = diffSessions[paneID] else { return }
        s.refs -= 1
        if s.refs <= 0 {
            diffSessions[paneID] = nil
            s.detach()
        }
    }

    public func diffSession(_ paneID: String) -> DiffSession? { diffSessions[paneID] }

    /// After reconnecting: watched again, and read again.
    func reattachDiffs() {
        for s in diffSessions.values {
            s.attach()
            s.load(refresh: false)
        }
    }

    /// The repository path is in (empty: the Group's folder), on the
    /// Group's computer: its branches and commits, to choose what a diff
    /// compares before opening one. nil: the daemon couldn't be asked.
    public func readRepo(_ path: String, groupID: String) async -> Hammock_V1_RepoInfo? {
        guard let conn = connection else { return nil }
        return try? await conn.call {
            $0.readRepo = .with {
                $0.path = path
                $0.groupID = groupID
            }
        }.repoInfo
    }

    /// Opens a diff of the repository dir is in: a new tab just after
    /// afterTab (or a split beside splitPane). source nil: the branch
    /// against its base, else what's uncommitted.
    public func openDiff(repo: String, groupID: String?, afterTab: String? = nil, splitPane: String? = nil,
                         direction: SplitDirection = .horizontal, source: Hammock_V1_DiffSource? = nil) async throws {
        guard let conn = connection else { throw DiffError.offline }
        let open = Hammock_V1_DiffOpen.with {
            $0.repo = repo
            if let source { $0.source = source }
        }
        if let splitPane {
            let r = try await conn.call {
                $0.createPane = .with {
                    $0.splitPaneID = splitPane
                    $0.direction = direction
                    $0.spec = .with { $0.diff = open }
                }
            }
            focus(r.created.id)
        } else if let groupID {
            let r = try await conn.call {
                $0.createTab = .with {
                    $0.groupID = groupID
                    $0.afterTabID = afterTab ?? ""
                    $0.pane = .with { $0.diff = open }
                }
            }
            selectedTabID = r.created.id
        }
    }
}

// MARK: - The page

/// The pane's diff page and its messages both ways.
@MainActor
public final class DiffBridge: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
    public let webView: WKWebView
    public let session: DiffSession
    private var ready = false
    private var config: DiffConfig?
    private var info: [String: Any] = [:]
    /// The bundled diff.html: the one page this web view may show, and the
    /// only one whose messages count.
    private var page: URL?
    public var onFocus: (() -> Void)?
    /// Open a file (relative to the repository) in an editor pane.
    public var onOpenFile: ((String) -> Void)?

    public init(session: DiffSession, makeWebView: (WKWebViewConfiguration) -> WKWebView) {
        self.session = session
        let conf = WKWebViewConfiguration()
        let ucc = WKUserContentController()
        conf.userContentController = ucc
        webView = makeWebView(conf)
        super.init()
        ucc.add(DiffWeakHandler(self), name: "hammock")
        webView.navigationDelegate = self
        #if os(macOS)
        webView.setValue(false, forKey: "drawsBackground")
        #else
        webView.isOpaque = false
        webView.backgroundColor = .clear
        webView.scrollView.backgroundColor = .clear
        webView.scrollView.bounces = false
        #endif
        if #available(macOS 13.3, iOS 16.4, *) {
            #if DEBUG
            webView.isInspectable = true
            #endif
        }
        if let url = Bundle.main.url(forResource: "diff", withExtension: "html") {
            page = url
            webView.loadFileURL(url, allowingReadAccessTo: url.deletingLastPathComponent())
        } else {
            session.error = "The diff view is missing from this build (diff.html)"
        }
    }

    public func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame, let page, EditorBridge.isPage(message.frameInfo.request.url, page) else { return }
        guard let m = message.body as? [String: Any], let type = m["type"] as? String else { return }
        switch type {
        case "ready":
            ready = true
            if let config { call("hammock.configure(c)", ["c": config.json]) }
            if !info.isEmpty { call("hammock.setInfo(i)", ["i": info]) }
            session.pageReady(self)
        case "needFile":
            if let path = m["path"] as? String {
                session.needFile(path, generation: (m["generation"] as? NSNumber)?.uint64Value ?? 0, for: self)
            }
        case "refresh":
            session.load(refresh: true)
        case "setSource":
            if let s = m["source"] as? [String: Any] { session.setSource(DiffSession.source(s)) }
        case "setView":
            if let v = m["view"] as? [String: Any] {
                session.setView(.with {
                    $0.style = v["style"] as? String ?? ""
                    $0.whitespace = v["whitespace"] as? String ?? ""
                    if let w = v["wrap"] as? Bool { $0.wrap = w ? "on" : "off" }
                })
            } else {
                session.setView(nil)
            }
        case "openFile":
            if let p = m["path"] as? String { onOpenFile?(p) }
        case "review":
            if let c = m["command"] as? [String: Any] { session.reviewCommand(c) }

        case "focus":
            session.pageFocused(self)
            onFocus?()
        default:
            break
        }
    }

    /// The page shows diff.html and nothing else: a web link opens in the
    /// browser, anything else is cancelled.
    public func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                        decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        let url = action.request.url
        if let page, EditorBridge.isPage(url, page) || url?.absoluteString == "about:blank" {
            decisionHandler(.allow)
            return
        }
        if action.navigationType == .linkActivated, let url, ["http", "https"].contains(url.scheme?.lowercased() ?? "") {
            #if os(macOS)
            NSWorkspace.shared.open(url)
            #else
            UIApplication.shared.open(url)
            #endif
        }
        decisionHandler(.cancel)
    }

    public func configure(_ c: DiffConfig) {
        guard c != config else { return }
        config = c
        if ready { call("hammock.configure(c)", ["c": c.json]) }
    }

    public func setInfo(_ d: DiffInfo) {
        let i = DiffSession.infoJSON(d)
        guard !NSDictionary(dictionary: i).isEqual(to: info) else { return }
        info = i
        if ready { call("hammock.setInfo(i)", ["i": i]) }
    }

    func load(_ content: [String: Any]) {
        guard ready else { return }
        call("hammock.load(c)", ["c": content])
    }

    func fileText(_ t: [String: Any]) { call("hammock.fileText(t)", ["t": t]) }

    func fileError(_ path: String, generation: UInt64, _ message: String) {
        call("hammock.fileError(p, g, m)", ["p": path, "g": NSNumber(value: generation), "m": message])
    }

    public func reveal(_ path: String) { call("hammock.reveal(p)", ["p": path]) }

    func nextUnread() { call("hammock.nextUnread()", [:]) }

    func setReview(_ r: [String: Any]) {
        guard ready else { return }
        call("hammock.setReview(r)", ["r": r])
    }

    private func call(_ js: String, _ args: [String: Any]) {
        webView.callAsyncJavaScript(js, arguments: args, in: nil, in: .page) { r in
            if case .failure(let e) = r { DevLog.editor("diff page: \(e.localizedDescription)") }
        }
    }
}

/// WKUserContentController keeps its handlers: this one lets the bridge go.
private final class DiffWeakHandler: NSObject, WKScriptMessageHandler {
    weak var target: (any WKScriptMessageHandler)?
    init(_ t: any WKScriptMessageHandler) { target = t }
    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.userContentController(ucc, didReceive: message)
    }
}
