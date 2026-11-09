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
        return EditorConfig(theme: theme, dark: dark, fontFamily: fontFamily, fontSize: size, wrap: p.bool(.editorWrap),
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
    /// "" follows the terminal's font size.
    public static let editorFontSize = Pref("editor-font-size", defaults: "editorFontSize", .string, "")
    public static let editorVim = Pref("editor-vim", defaults: "editorVim", false)

    public static var editor: [Pref] { [editorWrap, editorLineNumbers, editorTabWidth, editorIndent, editorFontSize, editorVim] }
}

/// The pane's CodeMirror page and its messages both ways.
@MainActor
public final class EditorBridge: NSObject, WKScriptMessageHandler {
    public let webView: WKWebView
    public let session: EditorSession
    private var ready = false
    private var config: EditorConfig?
    private var info: [String: Any] = [:]
    /// The page's languages (for a picker), once asked.
    public private(set) var languageNames: [String] = []
    /// The editor took focus (the app makes its pane the focused one).
    public var onFocus: (() -> Void)?
    public var onSave: ((_ saveAs: Bool) -> Void)?

    public init(session: EditorSession, makeWebView: (WKWebViewConfiguration) -> WKWebView) {
        self.session = session
        let conf = WKWebViewConfiguration()
        let ucc = WKUserContentController()
        conf.userContentController = ucc
        webView = makeWebView(conf)
        super.init()
        ucc.add(WeakHandler(self), name: "hammock")
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
            webView.loadFileURL(url, allowingReadAccessTo: url.deletingLastPathComponent())
        } else {
            session.error = "The editor is missing from this build (editor.html)"
        }
    }

    public func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
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
        default:
            break
        }
    }

    public func configure(_ c: EditorConfig) {
        guard c != config else { return }
        config = c
        if ready { call("hammock.configure(c)", ["c": c.json]) }
    }

    /// The pane's file: its name (for the language), a chosen language, indentation.
    public func setInfo(_ e: EditorInfo, readOnly: Bool) {
        let i: [String: Any] = ["name": e.name, "language": e.language, "tabWidth": Int(e.tabWidth), "indent": e.indent, "readOnly": readOnly || e.readOnly]
        guard !NSDictionary(dictionary: i).isEqual(to: info) else { return }
        info = i
        if ready { call("hammock.setInfo(i)", ["i": i]) }
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

/// WKUserContentController keeps its handlers: this one lets the bridge go.
private final class WeakHandler: NSObject, WKScriptMessageHandler {
    weak var target: (any WKScriptMessageHandler)?
    init(_ t: any WKScriptMessageHandler) { target = t }
    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.userContentController(ucc, didReceive: message)
    }
}
