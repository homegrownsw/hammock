import Foundation
import SwiftUI
import WebKit

/// What an editor page is set up with: the theme, font and settings.
public struct EditorConfig: Equatable {
    public var theme: TerminalTheme
    public var dark: Bool
    public var fontFamily: String
    public var fontSize: Double
    public var wrap: Bool
    public var lineNumbers: Bool
    public var tabWidth: Int
    public var indent: String
    public var vim: Bool
    public var readOnly: Bool

    public init(theme: TerminalTheme, dark: Bool, fontFamily: String, fontSize: Double, wrap: Bool, lineNumbers: Bool,
                tabWidth: Int, indent: String, vim: Bool, readOnly: Bool) {
        self.theme = theme
        self.dark = dark
        self.fontFamily = fontFamily
        self.fontSize = fontSize
        self.wrap = wrap
        self.lineNumbers = lineNumbers
        self.tabWidth = tabWidth
        self.indent = indent
        self.vim = vim
        self.readOnly = readOnly
    }

    /// From the shared settings (Prefs), with the platform's theme and font.
    @MainActor public static func from(theme: TerminalTheme, dark: Bool, fontFamily: String, fontSize: Double, readOnly: Bool) -> EditorConfig {
        let p = Prefs.shared
        let size = p.string(.editorFontSize).isEmpty ? fontSize : p.double(.editorFontSize)
        let family = p.string(.editorFontFamily).isEmpty ? fontFamily : p.string(.editorFontFamily)
        return EditorConfig(theme: theme, dark: dark, fontFamily: family, fontSize: size, wrap: p.bool(.editorWrap),
                            lineNumbers: p.bool(.editorLineNumbers), tabWidth: Int(p.double(.editorTabWidth)),
                            indent: p.string(.editorIndent), vim: p.bool(.editorVim), readOnly: readOnly)
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
            "wrap": wrap, "lineNumbers": lineNumbers, "tabWidth": tabWidth, "indent": indent, "vim": vim,
            "readOnly": readOnly, "platform": platform,
        ]
    }
}

extension Pref {
    public static let editorWrap = Pref("editor-wrap", defaults: "editorWrap", false)
    public static let editorLineNumbers = Pref("editor-line-numbers", defaults: "editorLineNumbers", true)
    public static let editorTabWidth = Pref("editor-tab-width", defaults: "editorTabWidth", 4.0)
    /// "spaces" or "tabs" (a file's .editorconfig, or its own indentation, wins).
    public static let editorIndent = Pref("editor-indent", defaults: "editorIndent", .string, "spaces")
    /// Editor and diff panes' font; "" follows the terminal's.
    public static let editorFontFamily = Pref("editor-font-family", defaults: "editorFontFamily", .string, "")
    /// "" follows the terminal's font size.
    public static let editorFontSize = Pref("editor-font-size", defaults: "editorFontSize", .string, "")
    public static let editorVim = Pref("editor-vim", defaults: "editorVim", false)

    public static var editor: [Pref] { [editorWrap, editorLineNumbers, editorTabWidth, editorIndent, editorFontFamily, editorFontSize, editorVim] }
}

/// The pane's CodeMirror page and its messages both ways.
@MainActor
public final class EditorBridge: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
    public let webView: WKWebView
    public let session: EditorSession
    private var ready = false
    private var config: EditorConfig?
    private var info: [String: Any] = [:]
    /// The bundled editor.html: the one page this web view may show, and
    /// the only one whose messages (which can change the file) count.
    private var page: URL?
    /// The page's languages (for a picker), once asked.
    public private(set) var languageNames: [String] = []
    /// The editor took focus (the app makes its pane the focused one).
    public var onFocus: (() -> Void)?
    public var onSave: ((_ saveAs: Bool) -> Void)?
    /// A link in Markdown's preview to another file (an absolute path).
    public var onOpenFile: ((String) -> Void)?

    public init(session: EditorSession, makeWebView: (WKWebViewConfiguration) -> WKWebView) {
        self.session = session
        let conf = WKWebViewConfiguration()
        // No Writing Tools button by a selection (Siri's rewrite and proofread):
        // what's shown here isn't prose to rewrite in place.
        if #available(macOS 15.0, iOS 18.0, *) { conf.writingToolsBehavior = .none }
        let ucc = WKUserContentController()
        conf.userContentController = ucc
        BundledFonts.addFontFaces(to: ucc, page: "editor")
        let images = PreviewImageHandler()
        conf.setURLSchemeHandler(images, forURLScheme: PreviewImageHandler.scheme)
        webView = makeWebView(conf)
        super.init()
        images.session = session
        ucc.add(WeakHandler(self), name: "hammock")
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
        if let url = Bundle.main.url(forResource: "editor", withExtension: "html") {
            page = url
            webView.loadFileURL(url, allowingReadAccessTo: url.deletingLastPathComponent())
        } else {
            session.error = "The editor is missing from this build (editor.html)"
        }
    }

    public func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        // Only the editor page itself, not a frame or a page it navigated to.
        guard message.frameInfo.isMainFrame, let page, Self.isPage(message.frameInfo.request.url, page) else { return }
        guard let m = message.body as? [String: Any], let type = m["type"] as? String else { return }
        switch type {
        case "ready":
            ready = true
            if let config { call("hammock.configure(c)", ["c": config.json]) }
            if !info.isEmpty { call("hammock.setInfo(i)", ["i": info]) }
            session.pageReady(self)
            webView.evaluateJavaScript("hammock.languageNames()") { [weak self] r, _ in
                MainActor.assumeIsolated { self?.languageNames = r as? [String] ?? [] }
            }
        case "push":
            session.push(from: self, version: (m["version"] as? NSNumber)?.uint64Value ?? 0, updates: m["updates"] as? [[String: Any]] ?? [])
        case "select":
            session.selected(m)
        case "language":
            session.languageFound(m["name"] as? String ?? "")
        case "resync":
            session.resync()
        case "focus":
            session.pageFocused(self)
            onFocus?()
        case "save":
            onSave?(false)
        case "saveAs":
            onSave?(true)
        case "shown":
            session.pageShows(m["view"] as? String ?? "source")
        case "openFile":
            if let path = m["path"] as? String, path.hasPrefix("/") { onOpenFile?(path) }
        case "resolveLinks":
            resolveLinks(m)
        case "openURL":
            if let s = m["url"] as? String, let url = URL(string: s), ["http", "https", "mailto"].contains(url.scheme?.lowercased() ?? "") {
                #if os(macOS)
                NSWorkspace.shared.open(url)
                #else
                UIApplication.shared.open(url)
                #endif
            }
        default:
            break
        }
    }

    // MARK: Navigation

    /// The web view shows editor.html and nothing else: a web link (should
    /// the page ever have one) opens in the browser, anything else is
    /// cancelled. The page's messages can write the file, so no other page
    /// may get to send them.
    public func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                        decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        let url = action.request.url
        if let page, Self.isPage(url, page) || url?.absoluteString == "about:blank" {
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
        DevLog.editor("editor page: blocked navigating to \(url?.absoluteString ?? "nothing")")
        decisionHandler(.cancel)
    }

    /// Whether url is the editor page (its own anchors included).
    nonisolated static func isPage(_ url: URL?, _ page: URL) -> Bool {
        guard let url, url.isFileURL, page.isFileURL else { return false }
        return url.standardizedFileURL.path == page.standardizedFileURL.path
    }

    public func configure(_ c: EditorConfig) {
        guard c != config else { return }
        config = c
        if ready { call("hammock.configure(c)", ["c": c.json]) }
    }

    /// The pane's file: its name (for the language) and path, a chosen language,
    /// indentation, how to show it.
    public func setInfo(_ e: EditorInfo, readOnly: Bool) {
        lastInfo = (e, readOnly)
        let i: [String: Any] = ["name": e.name, "language": e.language, "tabWidth": Int(e.tabWidth), "indent": e.indent, "readOnly": readOnly || e.readOnly,
                                "path": e.path, "view": Self.viewName(session.shownView(e))]
        guard !NSDictionary(dictionary: i).isEqual(to: info) else { return }
        info = i
        if ready { call("hammock.setInfo(i)", ["i": i]) }
    }

    private var lastInfo: (EditorInfo, Bool)?

    /// The session's own view changed (EditorSession.setView, local).
    func reapplyInfo() {
        if let (e, ro) = lastInfo { setInfo(e, readOnly: ro) }
    }

    nonisolated static func viewName(_ v: EditorInfo.View) -> String {
        switch v {
        case .preview: "preview"
        case .split: "split"
        default: "source"
        }
    }

    func load(_ u: Hammock_V1_EditorUpdate, git: [Hammock_V1_GitMark], discardLocal: Bool = false) {
        guard ready else { return }
        var m: [String: Any] = ["text": u.text, "version": NSNumber(value: u.version), "git": Self.marks(git), "discardLocal": discardLocal]
        if u.hasSelection { m["selection"] = ["anchor": Int(u.selection.anchor), "head": Int(u.selection.head)] }
        call("hammock.load(m)", ["m": m])
    }

    func receive(_ u: Hammock_V1_EditorUpdate) {
        guard ready else { return }
        var m: [String: Any] = ["version": NSNumber(value: u.version),
                                "updates": u.updates.map { ["changes": $0.changes, "clientID": $0.clientID] }]
        if u.gitKnown || !u.git.isEmpty || (u.updates.isEmpty && !u.hasSelection) { m["git"] = Self.marks(u.git) }
        if u.hasSelection { m["selection"] = ["anchor": Int(u.selection.anchor), "head": Int(u.selection.head)] }
        call("hammock.receive(m)", ["m": m])
    }

    /// The preview's Obsidian [[links]]: asked of the daemon, answered to the page (hammock.links).
    private func resolveLinks(_ m: [String: Any]) {
        guard let id = (m["id"] as? NSNumber)?.intValue else { return }
        let from = m["from"] as? String ?? ""
        let targets = (m["targets"] as? [Any] ?? []).compactMap { $0 as? String }
        let notes = (m["notes"] as? [Any] ?? []).compactMap { $0 as? String }
        Task { [weak self, session] in
            var r: [String: Any] = ["id": id]
            do {
                let links = try await session.resolveLinks(from: from, targets: targets, notes: notes)
                r["links"] = links.links.map { ["target": $0.target, "path": $0.path, "text": $0.text, "hasText": $0.hasText_p] as [String: Any] }
            } catch {
                r["error"] = error.localizedDescription
            }
            self?.call("hammock.links(r)", ["r": r])
        }
    }

    func pushResult(_ accepted: Bool) { call("hammock.pushResult(a)", ["a": accepted]) }
    func showDiff(_ disk: String) { call("hammock.showDiff(d)", ["d": disk]) }
    func hideDiff() { call("hammock.hideDiff()", [:]) }
    func command(_ name: String) { call("hammock.command(n)", ["n": name]) }
    func insert(_ text: String) { call("hammock.insert(t)", ["t": text]) }

    func selectionText() async -> String {
        await withCheckedContinuation { cont in
            webView.evaluateJavaScript("hammock.selection()") { r, _ in cont.resume(returning: r as? String ?? "") }
        }
    }

    private static func marks(_ git: [Hammock_V1_GitMark]) -> [[String: Any]] {
        git.map { m in
            let kind = switch m.kind { case .added: "added"; case .deleted: "deleted"; default: "modified" }
            return ["kind": kind, "from": Int(m.from), "to": Int(m.to)]
        }
    }

    /// Calls the page with arguments passed as values (no string building).
    private func call(_ js: String, _ args: [String: Any]) {
        webView.callAsyncJavaScript(js, arguments: args, in: nil, in: .page) { r in
            if case .failure(let e) = r { DevLog.editor("editor page: \(e.localizedDescription)") }
        }
    }
}

/// Images in Markdown's preview (hammock-file://local/PATH, PATH absolute):
/// fetched from the daemon (ReadEditorImage), so they show wherever the
/// pane does, not only on the file's own Mac. The page is the app's, so it
/// can't read them itself.
@MainActor
final class PreviewImageHandler: NSObject, WKURLSchemeHandler {
    nonisolated static let scheme = "hammock-file"
    weak var session: EditorSession?
    /// Tasks WebKit has stopped (a re-render dropped the image): not to be answered.
    private var stopped: Set<ObjectIdentifier> = []

    /// The path a request is for, if it's one to ask for.
    nonisolated static func path(for url: URL?) -> String? {
        guard let url, url.scheme == scheme, url.host == "local" else { return nil }
        let path = url.path
        guard path.hasPrefix("/"), !path.split(separator: "/").contains("..") else { return nil }
        return path
    }

    func webView(_ webView: WKWebView, start task: any WKURLSchemeTask) {
        let id = ObjectIdentifier(task)
        guard let url = task.request.url, let path = Self.path(for: url), let session else {
            task.didFailWithError(URLError(.fileDoesNotExist))
            return
        }
        Task {
            let image = try? await session.readImage(path)
            guard stopped.remove(id) == nil else { return }
            guard let image else {
                task.didFailWithError(URLError(.fileDoesNotExist))
                return
            }
            task.didReceive(URLResponse(url: url, mimeType: image.mediaType, expectedContentLength: image.data.count, textEncodingName: nil))
            task.didReceive(image.data)
            task.didFinish()
        }
    }

    func webView(_ webView: WKWebView, stop task: any WKURLSchemeTask) {
        stopped.insert(ObjectIdentifier(task))
    }
}

/// WKUserContentController keeps its handlers: this one lets the bridge go.
private final class WeakHandler: NSObject, WKScriptMessageHandler {
    weak var target: (any WKScriptMessageHandler)?
    init(_ t: any WKScriptMessageHandler) { target = t }
    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.userContentController(ucc, didReceive: message)
    }
}
