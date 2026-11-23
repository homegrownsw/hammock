import SwiftUI
import WebKit
#if canImport(AppKit)
import AppKit
#else
import UIKit
#endif

// The agent view's conversation drawn by a web page (agent.html, built from
// apple/Editor/src/agent.js): an experiment beside the native one, chosen by
// Pref.agentRenderer. The session (AgentSession) is still the model: this
// sends the page the rows that changed since it last did, with what the
// native rows would show worked out here (tool summaries, diffs' lines), and
// the platform's own colours, text sizes and SF Symbols, so the page draws
// what the native view draws. The page owns the scrolling; the composer and
// prompt cards stay native, floating over it (its bottom inset).

/// What the page is drawn with, from the conversation view.
struct AgentWebStyle: Equatable {
    var fonts: AgentFonts
    var accent: Color
    var bubble: Color
    var canOpenDiff: Bool
    var agentName: String
}

@MainActor
final class AgentWebBridge: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
    let webView: WKWebView
    private(set) var session: AgentSession?
    private var ready = false
    private var page: URL?
    private var style: AgentWebStyle?
    /// What the page has: each row as it was sent, in order, and the rest.
    private var sentItems: [String: AgentItem] = [:]
    private var sentOrder: [String] = []
    private var sentTail: NSDictionary?
    private var sentMeta: NSDictionary?
    private var sentPending = 0
    /// Replies already shown arriving (revealed once, not again when redrawn).
    private var revealed: Set<String> = []
    private var insets: (top: CGFloat, bottom: CGFloat) = (-1, -1)
    private var scheduled = false

    var onLink: ((URL) -> Void)?
    var onOpenDiff: ((String) -> Void)?
    var onOpenImage: ((AgentSession, String) -> Void)?

    override init() {
        let conf = WKWebViewConfiguration()
        let ucc = WKUserContentController()
        conf.userContentController = ucc
        BundledFonts.addFontFaces(to: ucc, page: "agent")
        #if os(macOS)
        webView = AgentPageView(frame: .zero, configuration: conf)
        #else
        webView = WKWebView(frame: .zero, configuration: conf)
        #endif
        super.init()
        ucc.add(AgentWeakHandler(self), name: "hammock")
        webView.navigationDelegate = self
        #if os(macOS)
        webView.setValue(false, forKey: "drawsBackground")
        #else
        webView.isOpaque = false
        webView.backgroundColor = .clear
        webView.scrollView.backgroundColor = .clear
        // The page pads itself for the bars (setInsets): none of UIKit's too.
        webView.scrollView.contentInsetAdjustmentBehavior = .never
        // The page doesn't scroll: its conversation does, in an element of its own (agent.js).
        webView.scrollView.isScrollEnabled = false
        webView.scrollView.bounces = false
        webView.scrollView.showsHorizontalScrollIndicator = false
        webView.allowsLinkPreview = false
        // Soft edges under the bars, as the native conversation has (agentSoftEdges).
        if #available(iOS 26.0, *) {
            webView.scrollView.topEdgeEffect.style = .soft
            webView.scrollView.bottomEdgeEffect.style = .soft
        }
        #endif
        #if DEBUG
        webView.isInspectable = true
        #endif
        if let url = Bundle.main.url(forResource: "agent", withExtension: "html") {
            page = url
            webView.loadFileURL(url, allowingReadAccessTo: url.deletingLastPathComponent())
        }
    }

    // MARK: From the view

    func attach(_ s: AgentSession) {
        guard s !== session else { return }
        session = s
        resetSent()
        if ready { push(replace: true) }
        observe()
    }

    func configure(_ st: AgentWebStyle) {
        guard st != style else { return }
        style = st
        if ready { call("hammock.configure(c)", ["c": Self.config(st)]) }
    }

    /// How much of the page the bars cover, at the top (the navigation bar)
    /// and the bottom (the message box, the keyboard).
    func setInsets(top: CGFloat, bottom: CGFloat) {
        let t = max(0, top.rounded()), b = max(0, bottom.rounded())
        guard t != insets.top || b != insets.bottom else { return }
        insets = (t, b)
        if ready { call("hammock.setInsets(i)", ["i": ["top": t, "bottom": b]]) }
    }

    #if DEBUG
    /// The demo's takes scrolling the conversation (HammockDebugScroll).
    func debugScroll(_ to: String) { if ready { call("hammock.debugScroll(t)", ["t": to]) } }
    #endif

    // MARK: To the page

    private func resetSent() {
        sentItems = [:]
        sentOrder = []
        sentTail = nil
        sentMeta = nil
        sentPending = session?.pendingSends.count ?? 0
    }

    /// Watches what the page shows; changes are sent together, once per turn of the run loop.
    private func observe() {
        guard let s = session else { return }
        withObservationTracking {
            _ = s.items
            _ = s.pendingSends.count
            _ = s.working
            _ = s.status
            _ = s.bangOutput
            _ = s.hasMore
            _ = s.loadingOlder
            _ = s.error
            _ = s.heard
            _ = s.prompt.kind
        } onChange: { [weak self] in
            Task { @MainActor in self?.schedule() }
        }
    }

    private func schedule() {
        guard !scheduled else { return }
        scheduled = true
        Task { @MainActor in
            scheduled = false
            push()
            observe()
        }
    }

    private func push(replace: Bool = false) {
        guard ready, let s = session else { return }
        var u: [String: Any] = [:]
        if replace { u["replace"] = true; resetSent() }
        let order = s.items.map(\.id)
        var rows: [[String: Any]] = []
        for it in s.items where sentItems[it.id] != it { rows.append(row(it, s)) }
        if replace || order != sentOrder || !rows.isEmpty {
            u["order"] = order
            u["rows"] = rows
        }
        sentItems = Dictionary(s.items.map { ($0.id, $0) }, uniquingKeysWith: { $1 })
        sentOrder = order
        let meta: NSDictionary = [
            "hasMore": s.hasMore, "loadingOlder": s.loadingOlder,
            "error": !s.error.isEmpty && s.blocks.isEmpty ? s.error : "",
            "working": s.working, "prompt": s.prompt.kind != .none,
        ]
        if meta != sentMeta { u["meta"] = meta; sentMeta = meta }
        let tail = tailJSON(s)
        if tail != sentTail { u["tail"] = tail; sentTail = tail }
        // Sent from here: down to see it go, as the native view does.
        if s.pendingSends.count > sentPending { u["sent"] = true }
        sentPending = s.pendingSends.count
        if !u.isEmpty { call("hammock.update(u)", ["u": u]) }
    }

    private func row(_ it: AgentItem, _ s: AgentSession) -> [String: Any] {
        var r: [String: Any] = ["id": it.id]
        guard let b = it.block else {
            if it.tools.count == 1, let only = it.tools.first {
                r["kind"] = "tool"
                r["b"] = block(only)
            } else {
                r["kind"] = "group"
                r["tools"] = it.tools.map(block)
                r["summary"] = ToolSummary.words(it.tools)
                r["failed"] = ToolSummary.failed(it.tools) ?? ""
            }
            return r
        }
        switch b.kind {
        case .user:
            r["kind"] = "user"
            r["text"] = b.text
            r["images"] = b.images.map(\.ref)
        case .text:
            r["kind"] = "text"
            r["text"] = b.text
            // Arrived while watched: shown from the top down (RevealsReply).
            if let at = s.arrived[b.id], Date.now.timeIntervalSince(at) < 2, !revealed.contains(b.id) {
                revealed.insert(b.id)
                r["live"] = true
            }
        case .thinking:
            r["kind"] = "thinking"
            r["text"] = b.text
            if let secs = it.thoughtSeconds { r["seconds"] = secs }
        case .tool where b.tool == "SendUserFile":
            r["kind"] = "files"
            r["b"] = block(b)
        case .tool:
            r["kind"] = "tool"
            r["b"] = block(b)
        case .question:
            r["kind"] = "question"
            r["b"] = block(b)
        case .plan:
            r["kind"] = "plan"
            r["b"] = block(b)
        case .subagent:
            r["kind"] = "subagent"
            r["b"] = block(b)
        case .note:
            r["kind"] = "note"
            r["b"] = block(b)
        case .done:
            r["kind"] = "done"
            r["words"] = DoneRow.words(durationMs: b.durationMs, doneUnixMs: b.timeUnixMs)
        default:
            r["kind"] = ""
        }
        return r
    }

    private func block(_ b: AgentBlock) -> [String: Any] {
        var d: [String: Any] = [
            "id": b.id, "tool": b.tool, "title": b.title, "input": b.input, "output": b.output, "text": b.text,
            "error": b.error, "pending": b.pending, "time": NSNumber(value: b.timeUnixMs),
            "added": Int(b.added), "removed": Int(b.removed), "images": b.images.map(\.ref),
            "bang": b.tool == "Bash" && (b.turnID == b.id || b.id.hasPrefix(PendingSend.runPrefix)),
        ]
        if !b.diff.isEmpty { d["diff"] = Self.diff(b) }
        if !b.questions.isEmpty {
            d["questions"] = b.questions.map { ["question": $0.question, "answer": $0.answer, "notes": $0.notes] }
        }
        return d
    }

    /// An edit's diff as DiffView shows it: its file, and its lines numbered.
    private static func diff(_ b: AgentBlock) -> [String: Any] {
        let lines = RenderCache.get("difflines\u{0}" + b.diff) { DiffLines.numbered(b.diff) }
        let path = ToolSummary.diffPath(tool: b.tool, input: b.input, diff: b.diff)
        let files = DiffLines.files(lines)
        var name = "Changes", folder = ""
        if !path.isEmpty {
            name = (path as NSString).lastPathComponent
            folder = (path as NSString).deletingLastPathComponent
        } else if let first = files.first {
            name = (first as NSString).lastPathComponent
            if files.count == 1 { folder = (first as NSString).deletingLastPathComponent } else { name += " and \(files.count - 1) more" }
        }
        let kinds: [DiffLines.Kind: String] = [.context: "c", .added: "a", .removed: "r", .hunk: "h", .file: "f", .note: "n"]
        return [
            "name": name, "folder": folder, "path": path.isEmpty ? (files.first ?? "") : path, "raw": b.diff,
            "lines": lines.map { [kinds[$0.kind] ?? "c", $0.old ?? 0, $0.new ?? 0, $0.text] as [Any] },
        ]
    }

    private func tailJSON(_ s: AgentSession) -> NSDictionary {
        let pending: [[String: Any]] = s.pendingSends.map { p in
            if let run = p.bangRun(output: s.runningBang == p.id ? s.bangOutput : "") {
                return ["id": p.id.uuidString, "kind": "tool", "b": block(run)]
            }
            return ["id": p.id.uuidString, "kind": "user", "text": p.text]
        }
        return ["pending": pending, "working": s.working, "status": s.status]
    }

    private func call(_ js: String, _ args: [String: Any]) {
        webView.callAsyncJavaScript(js, arguments: args, in: nil, in: .page) { r in
            if case .failure(let e) = r { DevLog.agent("agent page: \(e.localizedDescription)") }
        }
    }

    // MARK: From the page

    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.isMainFrame, let page, EditorBridge.isPage(message.frameInfo.request.url, page) else { return }
        guard let m = message.body as? [String: Any], let type = m["type"] as? String else { return }
        switch type {
        case "ready":
            // Loaded (or loaded again): it has nothing, so everything goes.
            ready = true
            if let style { call("hammock.configure(c)", ["c": Self.config(style)]) }
            if insets.top >= 0 { call("hammock.setInsets(i)", ["i": ["top": insets.top, "bottom": insets.bottom]]) }
            push(replace: true)
        case "log":
            DevLog.agent("agent page: \(m["text"] as? String ?? "")")
        case "dismissKeyboard":
            #if os(iOS)
            UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
            #endif
        case "loadOlder":
            session?.loadOlder()
        case "link":
            if let href = m["href"] as? String, let url = URL(string: href) { onLink?(url) }
        case "copy":
            if let text = m["text"] as? String { HammockKit.copy(text) }
        case "image":
            guard let ref = m["ref"] as? String, let s = session else { return }
            Task { @MainActor in
                guard let data = await s.file(ref) else { return }
                call("hammock.image(r, u)", ["r": ref, "u": Self.dataURL(data)])
            }
        case "openImage":
            if let ref = m["ref"] as? String, let s = session { onOpenImage?(s, ref) }
        case "openDiff":
            if let path = m["path"] as? String { onOpenDiff?(path) }
        case "pendingMenu":
            #if os(macOS)
            let p = CGPoint(x: (m["x"] as? NSNumber)?.doubleValue ?? 0, y: (m["y"] as? NSNumber)?.doubleValue ?? 0)
            pendingMenu(at: p)
            #endif
        default:
            break
        }
    }

    /// The page shows agent.html and nothing else.
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void) {
        let url = action.request.url
        decisionHandler(page.map { EditorBridge.isPage(url, $0) } == true || url?.absoluteString == "about:blank" ? .allow : .cancel)
    }

    /// The page's process went away (memory, a crash): loaded again, it asks for everything.
    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        ready = false
        if let page { webView.loadFileURL(page, allowingReadAccessTo: page.deletingLastPathComponent()) }
    }

    #if os(macOS)
    /// A waiting message's menu: Send Now, while Claude Code holds it.
    private func pendingMenu(at p: CGPoint) {
        guard let s = session else { return }
        let menu = NSMenu()
        let item = NSMenuItem(title: "Send Now", action: #selector(sendNow), keyEquivalent: "")
        item.target = self
        item.image = NSImage(systemSymbolName: "paperplane", accessibilityDescription: nil)
        item.isEnabled = s.canSendNow
        menu.autoenablesItems = false
        menu.addItem(item)
        menu.popUp(positioning: nil, at: NSPoint(x: p.x, y: webView.isFlipped ? p.y : webView.bounds.height - p.y), in: webView)
    }

    @objc private func sendNow() { session?.sendNow() }
    #endif

    private static func dataURL(_ d: Data) -> String {
        let m = [UInt8](d.prefix(12))
        let type = m.starts(with: [0x89, 0x50, 0x4E, 0x47]) ? "image/png"
            : m.starts(with: [0xFF, 0xD8, 0xFF]) ? "image/jpeg"
            : m.starts(with: [0x47, 0x49, 0x46]) ? "image/gif"
            : m.count == 12 && m[8...11] == [0x57, 0x45, 0x42, 0x50] ? "image/webp"
            : m.count >= 12 && m[4...7] == [0x66, 0x74, 0x79, 0x70] ? "image/heic"
            : "application/octet-stream"
        return "data:\(type);base64," + d.base64EncodedString()
    }
}

// MARK: - What the page is drawn with

extension AgentWebBridge {
    static func config(_ st: AgentWebStyle) -> [String: Any] {
        #if os(macOS)
        let platform = "mac"
        #else
        let platform = "ios"
        #endif
        let f = st.fonts
        let body = Self.font(f.name, f.size, mono: false)
        let code = Self.font(f.codeName, f.codeSize, mono: true)
        let bodyLine = Self.lineHeight(body, f.body())
        return [
            "platform": platform,
            "codeWrap": f.codeWrap,
            "canOpenDiff": st.canOpenDiff,
            "agentName": st.agentName,
            "fonts": [
                "family": f.name.isEmpty ? "-apple-system, system-ui, sans-serif" : "\"\(f.name)\", -apple-system, system-ui, sans-serif",
                "size": f.size,
                // SwiftUI's line spacing goes between lines, on top of the font's own height.
                "lineHeight": bodyLine + f.spacing,
                // Between prose blocks: a line at 0.45 of the size, as MarkdownView puts there.
                "gap": bodyLine * 0.45 + f.spacing,
                "code": f.codeName.isEmpty ? "ui-monospace, \"SF Mono\", Menlo, monospace" : "\"\(f.codeName)\", ui-monospace, Menlo, monospace",
                "codeSize": f.codeSize,
                "codeLineHeight": Self.lineHeight(code, f.code()),
                "spacing": f.spacing,
            ],
            // A sample's width in the native fonts: the page matches its tracking to it.
            "calibrate": [
                "body": ["text": Self.sample, "width": Self.width(Self.sample, body)],
                "code": ["text": Self.sample, "width": Self.width(Self.sample, code)],
            ],
            "metrics": Self.metrics,
            "colors": ["light": Self.colors(st, dark: false), "dark": Self.colors(st, dark: true)],
            "symbols": Self.symbols,
        ]
    }

    private static let sample = "let value0 = compute(0) // The quick brown fox jumps over the lazy dog"

    #if os(macOS)
    private static func width(_ s: String, _ f: NSFont) -> Double { Double((s as NSString).size(withAttributes: [.font: f]).width) }
    #else
    private static func width(_ s: String, _ f: UIFont) -> Double { Double((s as NSString).size(withAttributes: [.font: f]).width) }
    #endif

    #if os(macOS)
    private static func font(_ name: String, _ size: Double, mono: Bool) -> NSFont {
        if !name.isEmpty, let f = NSFont(name: name, size: size) { return f }
        return mono ? .monospacedSystemFont(ofSize: size, weight: .regular) : .systemFont(ofSize: size)
    }
    /// A line as SwiftUI lays it out, which on the Mac is rounded to a whole point (19 for 15pt SF, not 17.7).
    private static func lineHeight(_ f: NSFont, _ font: Font) -> Double {
        Double(NSHostingView(rootView: Text("Hg").font(font).fixedSize()).fittingSize.height)
    }
    private static let metrics: [String: Double] = [
        "caption": Double(NSFont.preferredFont(forTextStyle: .caption1).pointSize),
        "caption2": Double(NSFont.preferredFont(forTextStyle: .caption2).pointSize),
        "callout": Double(NSFont.preferredFont(forTextStyle: .callout).pointSize),
        "button": Double(NSFont.systemFontSize),
    ]
    #else
    private static func font(_ name: String, _ size: Double, mono: Bool) -> UIFont {
        if !name.isEmpty, let f = UIFont(name: name, size: size) { return f }
        return mono ? .monospacedSystemFont(ofSize: size, weight: .regular) : .systemFont(ofSize: size)
    }
    private static func lineHeight(_ f: UIFont, _ font: Font) -> Double { Double(f.lineHeight) }
    private static var metrics: [String: Double] {
        [
            "caption": Double(UIFont.preferredFont(forTextStyle: .caption1).pointSize),
            "caption2": Double(UIFont.preferredFont(forTextStyle: .caption2).pointSize),
            "callout": Double(UIFont.preferredFont(forTextStyle: .callout).pointSize),
            "button": Double(UIFont.preferredFont(forTextStyle: .body).pointSize),
        ]
    }
    #endif

    /// The colours the native rows use, resolved for light or dark.
    private static func colors(_ st: AgentWebStyle, dark: Bool) -> [String: String] {
        #if os(macOS)
        let named: [(String, NSColor)] = [
            ("fg", .labelColor), ("fg2", .secondaryLabelColor), ("fg3", .tertiaryLabelColor), ("fg4", .quaternaryLabelColor),
            ("sep", .separatorColor), ("green", .systemGreen), ("red", .systemRed), ("orange", .systemOrange),
            ("purple", .systemPurple), ("teal", .systemTeal), ("accent", NSColor(st.accent)), ("bubble", NSColor(st.bubble)),
        ]
        var out: [String: String] = [:]
        NSAppearance(named: dark ? .darkAqua : .aqua)?.performAsCurrentDrawingAppearance {
            for (k, c) in named { out[k] = css(c) }
        }
        return out
        #else
        let traits = UITraitCollection(userInterfaceStyle: dark ? .dark : .light)
        let named: [(String, UIColor)] = [
            ("fg", .label), ("fg2", .secondaryLabel), ("fg3", .tertiaryLabel), ("fg4", .quaternaryLabel),
            ("sep", .separator), ("green", .systemGreen), ("red", .systemRed), ("orange", .systemOrange),
            ("purple", .systemPurple), ("teal", .systemTeal), ("accent", UIColor(st.accent)), ("bubble", UIColor(st.bubble)),
        ]
        return Dictionary(uniqueKeysWithValues: named.map { ($0.0, css($0.1.resolvedColor(with: traits))) })
        #endif
    }

    #if os(macOS)
    private static func css(_ c: NSColor) -> String {
        guard let s = c.usingColorSpace(.sRGB) else { return "transparent" }
        return rgba(s.redComponent, s.greenComponent, s.blueComponent, s.alphaComponent)
    }
    #else
    private static func css(_ c: UIColor) -> String {
        var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
        guard c.getRed(&r, green: &g, blue: &b, alpha: &a) else { return "transparent" }
        return rgba(r, g, b, a)
    }
    #endif

    private static func rgba(_ r: CGFloat, _ g: CGFloat, _ b: CGFloat, _ a: CGFloat) -> String {
        func v(_ x: CGFloat) -> Int { Int((min(max(x, 0), 1) * 255).rounded()) }
        return String(format: "rgba(%d, %d, %d, %.3f)", v(r), v(g), v(b), min(max(a, 0), 1))
    }

    /// The SF Symbols the rows use, rendered once as images the page masks
    /// in its text's colour: w and h are their size at a point size of 1.
    static let symbols: [String: [String: Any]] = {
        let names = [
            "arrow.turn.down.left", "doc.on.doc", "checkmark", "terminal", "doc.text", "doc.badge.plus", "pencil", "magnifyingglass",
            "globe", "checklist", "wand.and.stars", "puzzlepiece.extension", "wrench.and.screwdriver", "xmark.circle.fill",
            "checkmark.circle.fill", "chevron.down", "chevron.right", "rectangle.split.2x1", "square.stack.3d.up", "brain",
            "questionmark.bubble", "arrowshape.turn.up.left.fill", "text.bubble", "arrow.uturn.backward", "list.bullet.clipboard",
            "person.2.wave.2", "paperclip", "sparkle", "arrow.down", "bubble.left.and.text.bubble.right",
        ]
        let pt: CGFloat = 64
        var out: [String: [String: Any]] = [:]
        for name in names {
            #if os(macOS)
            guard let img = NSImage(systemSymbolName: name, accessibilityDescription: nil)?
                .withSymbolConfiguration(.init(pointSize: pt, weight: .regular)) else { continue }
            let size = img.size
            guard let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int((size.width * 2).rounded(.up)),
                                             pixelsHigh: Int((size.height * 2).rounded(.up)), bitsPerSample: 8, samplesPerPixel: 4,
                                             hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)
            else { continue }
            rep.size = size
            NSGraphicsContext.saveGraphicsState()
            NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: rep)
            img.draw(in: NSRect(origin: .zero, size: size))
            NSGraphicsContext.restoreGraphicsState()
            guard let png = rep.representation(using: .png, properties: [:]) else { continue }
            #else
            guard let img = UIImage(systemName: name, withConfiguration: UIImage.SymbolConfiguration(pointSize: pt, weight: .regular))?
                .withTintColor(.black, renderingMode: .alwaysOriginal) else { continue }
            let size = img.size
            let format = UIGraphicsImageRendererFormat()
            format.scale = 2
            format.opaque = false
            let png = UIGraphicsImageRenderer(size: size, format: format).pngData { _ in img.draw(at: .zero) }
            #endif
            out[name] = ["url": "data:image/png;base64," + png.base64EncodedString(), "w": size.width / pt, "h": size.height / pt]
        }
        return out
    }()
}

// MARK: - The view

/// Holds the page for a conversation view: made the first time it's needed,
/// kept for the view's life, with where the bars are over it.
@MainActor
final class AgentWebHost {
    lazy var bridge = AgentWebBridge()
    var page = CGRect.zero, stack = CGRect.zero, bar = CGRect.zero
    /// The stack's own safe area at the top: the navigation bar, when the
    /// stack runs under it already (the iPhone's).
    var stackTop: CGFloat = 0

    /// The navigation bar over the page's top (it runs under it), and the
    /// message box (and the keyboard) over its bottom.
    func pushInsets() {
        // The web view's own frame: SwiftUI's leaves out what it runs into
        // under the bars (ignoresSafeArea).
        guard let f = webFrame(), f.height > 0 else { return }
        // The bar's height, whether the stack starts under it (and has it as
        // its safe area) or below it.
        bridge.setInsets(top: max(0, stack.minY - f.minY, stackTop),
                         bottom: bar.height > 0 ? max(0, f.maxY - bar.minY) : max(0, f.maxY - stack.maxY))
    }

    /// The web view's frame in the window, top-left first, as SwiftUI's global frames are.
    private func webFrame() -> CGRect? {
        let v = bridge.webView
        guard v.window != nil else { return nil }
        #if os(macOS)
        guard let content = v.window?.contentView else { return nil }
        let r = v.convert(v.bounds, to: content)
        return content.isFlipped ? r : CGRect(x: r.minX, y: content.bounds.height - r.maxY, width: r.width, height: r.height)
        #else
        return v.convert(v.bounds, to: nil)
        #endif
    }
}

#if os(macOS)
struct AgentWebTranscript: NSViewRepresentable {
    let bridge: AgentWebBridge
    func makeNSView(context: Context) -> WKWebView { bridge.webView }
    func updateNSView(_ v: WKWebView, context: Context) {}
}

/// The page's web view, without the menu's Reload (the page is the app's, not a site).
final class AgentPageView: WKWebView {
    override func willOpenMenu(_ menu: NSMenu, with event: NSEvent) {
        menu.items.removeAll { $0.identifier?.rawValue == "WKMenuItemIdentifierReload" }
        super.willOpenMenu(menu, with: event)
    }
}
#else
struct AgentWebTranscript: UIViewRepresentable {
    let bridge: AgentWebBridge
    func makeUIView(context: Context) -> WKWebView { bridge.webView }
    func updateUIView(_ v: WKWebView, context: Context) {}
}
#endif

/// WKUserContentController keeps its handlers: this one lets the bridge go.
private final class AgentWeakHandler: NSObject, WKScriptMessageHandler {
    weak var target: (any WKScriptMessageHandler)?
    init(_ t: any WKScriptMessageHandler) { target = t }
    func userContentController(_ ucc: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.userContentController(ucc, didReceive: message)
    }
}
