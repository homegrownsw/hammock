import AppKit
import HammockKit
import QuartzCore
import SwiftUI
import UniformTypeIdentifiers

// Browser panes. The page runs in the browser host ("Hammock Browser",
// helpers/browser/host.mm), which the daemon starts; this file shows it and
// feeds it input. Pixels arrive as Chromium's own GPU-process CAContext,
// shown here through CALayerHost (no copies); input goes back as raw CGEvent
// bytes so trackpad phases, momentum and gestures survive. Native UI the host
// can't keep open as a background app (<select> menus, context menus, JS
// dialogs, file pickers) is shown here instead. See docs/adr/0011.

/// The app's one connection to the browser host. Each visible browser pane
/// registers its view; messages for a pane go to its view. A pane shown in
/// two windows (DetachedWindows) has its page in one of them, the one you
/// were in last; the other view waits on standby, saying where it is.
@MainActor
final class BrowserHost {
    static let shared = BrowserHost()

    private var fd: Int32 = -1
    private var readSource: DispatchSourceRead?
    private var writeSource: DispatchSourceWrite?
    private var inbuf = Data()
    private var outbuf = Data()
    private var views: [String: BrowserPaneNSView] = [:]
    /// Every view of a page, the one showing it and any on standby.
    private let all = NSHashTable<BrowserPaneNSView>.weakObjects()
    private var retrying = false

    private var socketPath: String {
        ((DaemonConnection.defaultSocketPath as NSString).deletingLastPathComponent as NSString)
            .appendingPathComponent("browser-host.sock")
    }

    func register(_ v: BrowserPaneNSView) {
        all.add(v)
        // Already showing in another window, and this one isn't where you are: it waits.
        if let shown = views[v.paneID], shown !== v, shown.window?.isVisible == true, v.window?.isKeyWindow != true {
            v.standby = true
            return
        }
        activate(v)
    }

    /// Shows the page in v (from standby: a click on it, or its window
    /// coming to the front), the view that had it going on standby.
    func activate(_ v: BrowserPaneNSView) {
        if let shown = views[v.paneID], shown !== v { shown.standby = true }
        views[v.paneID] = v
        v.standby = false
        if fd < 0 { connect() } else { v.hostConnected() }
    }

    func unregister(_ v: BrowserPaneNSView) {
        all.remove(v)
        guard views[v.paneID] === v else { return }
        views[v.paneID] = nil
        // Another window shows it: the page goes there.
        if let next = all.allObjects.first(where: { $0.paneID == v.paneID && $0.window != nil }) {
            activate(next)
            return
        }
        send(["t": "detach", "id": v.paneID])
    }

    private func connect() {
        guard fd < 0, !views.isEmpty else { return }
        let s = socket(AF_UNIX, SOCK_STREAM, 0)
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let path = socketPath
        withUnsafeMutableBytes(of: &addr.sun_path) { buf in
            _ = path.withCString { strlcpy(buf.baseAddress!.assumingMemoryBound(to: CChar.self), $0, buf.count) }
        }
        let ok = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(s, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
        }
        guard ok == 0 else {
            close(s)
            retry() // the daemon is still starting the host
            return
        }
        var on: Int32 = 1
        setsockopt(s, SOL_SOCKET, SO_NOSIGPIPE, &on, socklen_t(MemoryLayout<Int32>.size))
        _ = fcntl(s, F_SETFL, fcntl(s, F_GETFL) | O_NONBLOCK)
        fd = s
        inbuf = Data()
        outbuf = Data()
        let src = DispatchSource.makeReadSource(fileDescriptor: s, queue: .main)
        src.setEventHandler { [weak self] in MainActor.assumeIsolated { self?.readable() } }
        src.resume()
        readSource = src
        for v in views.values { v.hostConnected() }
    }

    private func retry() {
        guard !retrying else { return }
        retrying = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) { [weak self] in
            MainActor.assumeIsolated {
                self?.retrying = false
                self?.connect()
            }
        }
    }

    private func disconnect() {
        readSource?.cancel()
        writeSource?.cancel()
        readSource = nil
        writeSource = nil
        if fd >= 0 { close(fd) }
        fd = -1
        for v in views.values { v.hostLost() }
        retry() // the host restarts (the daemon replaces it), and pages come back
    }

    private func readable() {
        var buf = [UInt8](repeating: 0, count: 65536)
        let n = read(fd, &buf, buf.count)
        if n < 0, errno == EAGAIN || errno == EINTR { return }
        guard n > 0 else { disconnect(); return }
        inbuf.append(buf, count: n)
        while let nl = inbuf.firstIndex(of: 0x0A) {
            let line = inbuf[inbuf.startIndex..<nl]
            inbuf = Data(inbuf[(nl + 1)...])
            if let m = try? JSONSerialization.jsonObject(with: line) as? [String: Any] { handle(m) }
        }
    }

    private func handle(_ m: [String: Any]) {
        guard let id = m["id"] as? String, let v = views[id] else { return }
        v.handle(m)
    }

    func send(_ m: [String: Any]) {
        guard fd >= 0, var d = try? JSONSerialization.data(withJSONObject: m) else { return }
        d.append(0x0A)
        outbuf.append(d)
        flush()
    }

    /// Never blocks the main thread: what the host can't take yet waits.
    private func flush() {
        while !outbuf.isEmpty {
            let n = outbuf.withUnsafeBytes { write(fd, $0.baseAddress, $0.count) }
            if n < 0, errno == EINTR { continue }
            if n < 0, errno == EAGAIN { break }
            guard n > 0 else { disconnect(); return }
            outbuf.removeFirst(n)
        }
        if !outbuf.isEmpty, writeSource == nil {
            let src = DispatchSource.makeWriteSource(fileDescriptor: fd, queue: .main)
            src.setEventHandler { [weak self] in MainActor.assumeIsolated { self?.flush() } }
            src.resume()
            writeSource = src
        } else if outbuf.isEmpty, let src = writeSource {
            src.cancel()
            writeSource = nil
        }
    }
}

// MARK: - Pane view

/// Shows one browser pane's page and forwards input to it.
final class BrowserPaneNSView: NSView, NSTextInputClient {
    let paneID: String
    var onFocus: (() -> Void)?
    var onCommand: ((BrowserCommand) -> Void)?
    /// Find in page's results: how many matches, and which is selected (1-based).
    var onFound: ((Int, Int) -> Void)?
    var isFocusTarget = false {
        didSet { if isFocusTarget, !oldValue { focusSoon() } }
    }
    /// The page is showing in another window (BrowserHost): this one says
    /// so, and takes it back when clicked or its window comes to the front.
    var standby = false {
        didSet {
            guard standby != oldValue else { return }
            if standby {
                let c = StandbyCover(frame: bounds)
                c.autoresizingMask = [.width, .height]
                addSubview(c)
                cover = c
            } else {
                cover?.removeFromSuperview()
                cover = nil
            }
        }
    }
    private var cover: NSView?

    private let container = CALayer()
    private var remote: CALayer?
    private var cursor = NSCursor.arrow
    private var lastGeometry: [String: Any] = [:]
    private var observers: [NSObjectProtocol] = []
    private var host: BrowserHost { .shared }

    // Text input: NSTextInputClient calls made during a keyDown are recorded
    // and sent with the key; the host replays them inside Chromium's own
    // keyDown (the host is never the active app, so its text system can't run).
    private var ops: [[String: Any]]?
    private var hostSel = NSRange(location: 0, length: 0)
    private var hostMarked = NSRange(location: NSNotFound, length: 0)
    private var hostCaret = NSRect.zero

    // Drags the page starts: the latest left-button event (a drag session
    // has to start from one), and what the page allows done with its data.
    // Cleared with focus: a menu's tracking can eat the mouse-up.
    private var dragEvent: NSEvent?
    private var dragMask: NSDragOperation = []
    private var dragSeq = 0 // the host's, for the drag running now

    init(paneID: String) {
        self.paneID = paneID
        super.init(frame: .zero)
        wantsLayer = true
        layer?.backgroundColor = NSColor.textBackgroundColor.cgColor
        // Mirror Chromium's own structure: a geometry-flipped container holding
        // the CALayerHost at the origin (the remote tree is in points).
        container.isGeometryFlipped = true
        container.anchorPoint = .zero
        container.frame = bounds
        // Kept to the view's bounds by fitContainer, not autoresizing (which
        // works from size deltas and can drift: the page then drew over the
        // address bar, leaving a strip under it), and clipped, so the page
        // never covers the pane's own controls.
        container.masksToBounds = true
        layer?.addSublayer(container)
        addTrackingArea(NSTrackingArea(rect: .zero, options: [.mouseMoved, .mouseEnteredAndExited, .cursorUpdate, .activeInKeyWindow, .inVisibleRect], owner: self))
        observers.append(NotificationCenter.default.addObserver(forName: TerminalNSView.refocus, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { if self?.isFocusTarget == true { self?.focusSoon() } }
        })
    }

    required init?(coder: NSCoder) { fatalError() }

    deinit {
        for o in observers { NotificationCenter.default.removeObserver(o) }
    }

    override var acceptsFirstResponder: Bool { true }
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    func focusSoon() {
        DispatchQueue.main.async { [weak self] in
            guard let self, self.isFocusTarget, !PaletteState.shared.isOpen, let w = self.window, w.firstResponder !== self else { return }
            w.makeFirstResponder(self)
        }
    }

    // MARK: Host connection

    func hostConnected() {
        lastGeometry = [:]
        host.send(["t": "attach", "id": paneID])
        sendGeometry()
        keyChanged()
    }

    override var debugDescription: String {
        "pane=\(paneID) layer=\((remote?.value(forKey: "contextId") as? NSNumber)?.uint32Value ?? 0) firstResponder=\(window?.firstResponder === self)"
    }

    func hostLost() {
        remote?.removeFromSuperlayer()
        remote = nil
    }

    func handle(_ m: [String: Any]) {
        switch m["t"] as? String {
        case "layer":
            if let ctx = (m["ctx"] as? NSNumber)?.uint32Value { show(context: ctx) }
        case "cursor":
            cursor = Self.cursor((m["type"] as? Int) ?? 0)
            if let w = window, w.isKeyWindow, let p = w.mouseLocationOutsideOfEventStream as NSPoint?, bounds.contains(convert(p, from: nil)) {
                cursor.set()
            }
        case "ti":
            hostTextState(m)
        case "unhandled":
            // A ⌘-key the page didn't want: ⌘← and ⌘→ go back and forward, as
            // in Chrome (a text field on the page keeps them), and our menus
            // get the rest.
            if let s = m["cg"] as? String, let d = Data(base64Encoded: s), let cg = CGEvent(withDataAllocator: nil, data: d as CFData),
               let e = NSEvent(cgEvent: cg) {
                if let history = Self.historyCommand(e) {
                    onCommand?(history)
                } else {
                    NSApp.mainMenu?.performKeyEquivalent(with: e)
                }
            }
        case "menu":
            // After this message: the popup tracks in a nested run loop.
            DispatchQueue.main.async { [weak self] in self?.showSelectMenu(m) }
        case "context":
            DispatchQueue.main.async { [weak self] in self?.showContextMenu(m) }
        case "dialog":
            showDialog(m)
        case "external":
            openExternal(m)
        case "file":
            showFilePanel(m)
        case "found":
            onFound?((m["count"] as? Int) ?? 0, (m["active"] as? Int) ?? 0)
        case "drag":
            startDrag(m)
        default:
            break
        }
    }

    /// Chromium may move to a new GPU context (surface recreated, GPU process
    /// restart). The new host goes on top, transparent until Chromium draws
    /// into it, and the old one stays underneath briefly, so the swap never
    /// shows a blank frame.
    private func show(context ctx: UInt32) {
        if let r = remote, (r.value(forKey: "contextId") as? NSNumber)?.uint32Value == ctx { return }
        guard let cls = NSClassFromString("CALayerHost") as? CALayer.Type else { return }
        let old = remote
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        let l = cls.init()
        l.setValue(NSNumber(value: ctx), forKey: "contextId")
        l.anchorPoint = .zero
        l.position = .zero
        container.addSublayer(l)
        remote = l
        CATransaction.commit()
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.25) {
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            old?.removeFromSuperlayer()
            CATransaction.commit()
        }
    }

    // MARK: Geometry and focus

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        for o in observers.dropFirst() { NotificationCenter.default.removeObserver(o) }
        observers = Array(observers.prefix(1))
        guard let w = window else {
            host.unregister(self)
            return
        }
        let nc = NotificationCenter.default
        for n in [NSWindow.didMoveNotification, NSWindow.didResizeNotification, NSWindow.didChangeScreenNotification, NSWindow.didEndLiveResizeNotification] {
            observers.append(nc.addObserver(forName: n, object: w, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.sendGeometry() }
            })
        }
        for n in [NSWindow.didBecomeKeyNotification, NSWindow.didResignKeyNotification] {
            observers.append(nc.addObserver(forName: n, object: w, queue: .main) { [weak self] note in
                MainActor.assumeIsolated {
                    guard let self else { return }
                    // On standby, its window coming to the front brings the page here.
                    if self.standby {
                        if note.name == NSWindow.didBecomeKeyNotification { self.host.activate(self) }
                        return
                    }
                    self.keyChanged()
                }
            })
        }
        host.register(self)
        if isFocusTarget { focusSoon() }
    }

    override func setFrameSize(_ s: NSSize) {
        super.setFrameSize(s)
        fitContainer()
        sendGeometry()
    }

    /// The page's layer exactly over the view.
    private func fitContainer() {
        guard container.frame != bounds else { return }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        container.frame = bounds
        CATransaction.commit()
    }

    override func setFrameOrigin(_ p: NSPoint) {
        super.setFrameOrigin(p)
        sendGeometry()
    }

    override func layout() {
        super.layout()
        fitContainer()
        sendGeometry()
    }

    /// The host keeps its transparent window exactly behind this pane, so
    /// anything Chromium places on screen (IME candidates) lands in the right
    /// place, at the pane's size.
    func sendGeometry() {
        guard !standby, let w = window, bounds.width > 0, bounds.height > 0 else { return }
        let r = w.convertToScreen(convert(bounds, to: nil))
        let g: [String: Any] = ["t": "geom", "id": paneID, "x": r.origin.x, "y": r.origin.y, "w": r.width, "h": r.height, "wid": w.windowNumber]
        guard !NSDictionary(dictionary: g).isEqual(to: lastGeometry) else { return }
        lastGeometry = g
        host.send(g)
    }

    private func sendFocus(_ on: Bool) {
        guard !standby else { return }
        host.send(["t": "focus", "id": paneID, "on": on && window?.isKeyWindow == true])
    }

    private func keyChanged() { sendFocus(window?.firstResponder === self) }

    override func becomeFirstResponder() -> Bool {
        sendFocus(true)
        onFocus?()
        return true
    }

    override func resignFirstResponder() -> Bool {
        dragEvent = nil
        sendFocus(false)
        return true
    }

    // MARK: Input

    private func forward(_ e: NSEvent, at p: NSPoint, ops: [[String: Any]] = []) {
        guard !standby, let cg = e.cgEvent, let d = cg.data as Data? else { return }
        host.send(["t": "ev", "id": paneID, "cg": d.base64EncodedString(), "x": p.x, "y": bounds.height - p.y, "ops": ops])
    }

    private func fwd(_ e: NSEvent) { forward(e, at: convert(e.locationInWindow, from: nil)) }

    override func mouseDown(with e: NSEvent) {
        window?.makeFirstResponder(self)
        if standby { host.activate(self); return } // the page comes here
        dragEvent = e
        fwd(e)
    }
    override func mouseUp(with e: NSEvent) { dragEvent = nil; fwd(e) }
    override func mouseDragged(with e: NSEvent) { dragEvent = e; fwd(e) }
    override func mouseMoved(with e: NSEvent) { fwd(e); cursor.set() }
    override func mouseEntered(with e: NSEvent) { fwd(e) }
    override func mouseExited(with e: NSEvent) { fwd(e) }
    override func rightMouseDown(with e: NSEvent) { window?.makeFirstResponder(self); fwd(e) }
    override func rightMouseUp(with e: NSEvent) { fwd(e) }
    override func rightMouseDragged(with e: NSEvent) { fwd(e) }
    override func otherMouseDown(with e: NSEvent) { fwd(e) }
    override func otherMouseUp(with e: NSEvent) { fwd(e) }
    override func otherMouseDragged(with e: NSEvent) { fwd(e) }
    override func scrollWheel(with e: NSEvent) { fwd(e) }
    override func magnify(with e: NSEvent) { fwd(e) }
    override func smartMagnify(with e: NSEvent) { fwd(e) }
    override func rotate(with e: NSEvent) { fwd(e) }
    override func cursorUpdate(with e: NSEvent) { cursor.set() }
    override func keyUp(with e: NSEvent) { fwd(e) }
    override func flagsChanged(with e: NSEvent) { fwd(e) }

    /// Back or forward for ⌘← or ⌘→ (and nothing else held).
    private static func historyCommand(_ e: NSEvent) -> BrowserCommand? {
        let flags = e.modifierFlags.intersection([.command, .shift, .option, .control])
        guard e.type == .keyDown, flags == .command else { return nil }
        switch e.keyCode {
        case 123: return .back
        case 124: return .forward
        default: return nil
        }
    }

    /// Text input runs here, in the active app, where dead keys and IMEs work.
    override func keyDown(with e: NSEvent) {
        ops = []
        interpretKeyEvents([e])
        let recorded = ops ?? []
        ops = nil
        forward(e, at: .zero, ops: recorded)
    }

    /// ⌘-keys go to the page first, as in Chrome; the host bounces back any
    /// it didn't use, and those reach our menus. The browser's own keys
    /// (address bar, reload, back/forward) and Hammock's shortcuts (`Keys`, as
    /// the person has them: tabs, Groups, panes, the palette, find) never
    /// reach the page; the simulator's do (they only apply to a simulator), and
    /// so do Bigger, Smaller and Actual Size, which zoom the page there.
    /// ⌘[ and ⌘] are back and forward only while no shortcut has them: by
    /// default they're Previous and Next Pane, which must not stop here.
    override func performKeyEquivalent(with e: NSEvent) -> Bool {
        guard !standby, window?.firstResponder === self, e.type == .keyDown else { return false }
        let flags = e.modifierFlags.intersection(.deviceIndependentFlagsMask)
        let key = e.charactersIgnoringModifiers?.lowercased() ?? ""
        let action = Keys.action(for: e)
        if flags == .command {
            switch key {
            case "l": onCommand?(.focusAddress); return true
            case "r": onCommand?(.reload); return true
            case "[" where action == nil: onCommand?(.back); return true
            case "]" where action == nil: onCommand?(.forward); return true
            default: break
            }
        }
        switch action {
        case "find_in_page": onCommand?(.find); return true
        case "find_next": onCommand?(.findNext); return true
        case "find_previous": onCommand?(.findPrevious); return true
        case "dev_tools": onCommand?(.devTools); return true
        default: break
        }
        // The zoom keys zoom the page (the host bounces back what it doesn't use).
        let toPage = ["bigger", "smaller", "actual_size"]
        if let action, Keybinds.byID[action]?.group != "Simulator", !toPage.contains(action) { return false }
        if flags.contains(.control) || (flags.contains(.command) && key == "q") { return false }
        guard flags.contains(.command) else { return false }
        fwd(e)
        return true
    }

    // NSTextInputClient

    private static func json(_ r: NSRange) -> [Int] { [r.location == NSNotFound ? -1 : r.location, r.length] }
    // Copied: the text system passes a mutable string it reuses and clears.
    private static func plain(_ s: Any) -> String {
        if let a = s as? NSAttributedString { return String(a.string) }
        return String((s as? String) ?? "")
    }

    private func record(_ op: [String: Any]) {
        if ops != nil {
            ops!.append(op)
        } else {
            host.send(["t": "ti", "id": paneID, "ops": [op]]) // outside a key press
        }
    }

    func insertText(_ string: Any, replacementRange: NSRange) {
        hostMarked = NSRange(location: NSNotFound, length: 0)
        record(["op": "insert", "s": Self.plain(string), "r": Self.json(replacementRange)])
    }

    func setMarkedText(_ string: Any, selectedRange: NSRange, replacementRange: NSRange) {
        let p = Self.plain(string)
        let base = hostMarked.location != NSNotFound ? hostMarked.location : hostSel.location
        hostMarked = p.isEmpty ? NSRange(location: NSNotFound, length: 0) : NSRange(location: base, length: (p as NSString).length)
        record(["op": "mark", "s": p, "sel": Self.json(selectedRange), "r": Self.json(replacementRange)])
    }

    func unmarkText() {
        hostMarked = NSRange(location: NSNotFound, length: 0)
        record(["op": "unmark"])
    }

    override func doCommand(by selector: Selector) { record(["op": "cmd", "sel": NSStringFromSelector(selector)]) }
    func hasMarkedText() -> Bool { hostMarked.location != NSNotFound && hostMarked.length > 0 }
    func markedRange() -> NSRange { hasMarkedText() ? hostMarked : NSRange(location: NSNotFound, length: 0) }
    func selectedRange() -> NSRange { hostSel }
    func validAttributesForMarkedText() -> [NSAttributedString.Key] { [] }
    func attributedSubstring(forProposedRange range: NSRange, actualRange: NSRangePointer?) -> NSAttributedString? { nil }
    func characterIndex(for point: NSPoint) -> Int { NSNotFound }

    func firstRect(forCharacterRange range: NSRange, actualRange: NSRangePointer?) -> NSRect {
        if hostCaret.height == 0, let w = window {
            return w.convertToScreen(convert(NSRect(x: 0, y: bounds.height - 20, width: 1, height: 20), to: nil))
        }
        return hostCaret
    }

    private func hostTextState(_ m: [String: Any]) {
        func range(_ a: Any?) -> NSRange {
            guard let a = a as? [NSNumber], a.count == 2 else { return NSRange(location: NSNotFound, length: 0) }
            return NSRange(location: a[0].intValue < 0 ? NSNotFound : a[0].intValue, length: a[1].intValue)
        }
        hostSel = range(m["sel"])
        hostMarked = range(m["marked"])
        if let r = m["rect"] as? [NSNumber], r.count == 4 {
            hostCaret = NSRect(x: r[0].doubleValue, y: r[1].doubleValue, width: r[2].doubleValue, height: r[3].doubleValue)
        }
    }

    // MARK: Native UI for the page

    /// <select>: an NSPopUpButtonCell over the element, with the current item
    /// over it, as Chrome shows them.
    private func showSelectMenu(_ m: [String: Any]) {
        let seq = m["seq"] ?? 0
        let menu = NSMenu()
        menu.autoenablesItems = false
        menu.font = .menuFont(ofSize: (m["font"] as? Double) ?? 13)
        let picker = MenuPick()
        for (i, it) in ((m["items"] as? [[String: Any]]) ?? []).enumerated() {
            let item = (it["sep"] as? Bool) == true ? NSMenuItem.separator()
                : NSMenuItem(title: (it["title"] as? String) ?? "", action: #selector(MenuPick.pick(_:)), keyEquivalent: "")
            item.target = picker
            item.tag = i
            item.isEnabled = (it["enabled"] as? Bool) ?? true
            item.isHidden = (it["hidden"] as? Bool) ?? false
            item.indentationLevel = (it["indent"] as? Int) ?? 0
            menu.addItem(item)
        }
        let cell = NSPopUpButtonCell(textCell: "", pullsDown: false)
        cell.menu = menu
        cell.font = menu.font
        cell.selectItem(withTag: (m["index"] as? Int) ?? 0)
        let x = (m["x"] as? Double) ?? 0, y = (m["y"] as? Double) ?? 0, w = (m["w"] as? Double) ?? 0, h = (m["h"] as? Double) ?? 0
        let frame = NSRect(x: x, y: bounds.height - y - h, width: w, height: h)
        cell.attachPopUp(withFrame: bounds, in: self)
        cell.performClick(withFrame: frame, in: self) // tracks until dismissed
        cell.dismissPopUp()
        host.send(["t": "menuResult", "seq": seq, "index": picker.chosen])
    }

    private func showContextMenu(_ m: [String: Any]) {
        let menu = NSMenu()
        menu.autoenablesItems = false
        let picker = MenuPick()
        for it in (m["items"] as? [[String: Any]]) ?? [] {
            if (it["sep"] as? Bool) == true {
                if menu.items.last.map({ !$0.isSeparatorItem }) ?? false { menu.addItem(.separator()) }
                continue
            }
            let item = NSMenuItem(title: (it["title"] as? String) ?? "", action: #selector(MenuPick.pick(_:)), keyEquivalent: "")
            item.target = picker
            item.tag = (it["cmd"] as? Int) ?? -1
            item.isEnabled = (it["enabled"] as? Bool) ?? true
            item.state = (it["checked"] as? Bool) == true ? .on : .off
            menu.addItem(item)
        }
        let p = NSPoint(x: (m["x"] as? Double) ?? 0, y: bounds.height - ((m["y"] as? Double) ?? 0))
        menu.popUp(positioning: nil, at: p, in: self) // tracks until dismissed
        host.send(["t": "contextResult", "seq": m["seq"] ?? 0, "cmd": picker.chosen])
    }

    /// alert(), confirm() and prompt(), as a sheet over the window.
    private func showDialog(_ m: [String: Any]) {
        let seq = m["seq"] ?? 0
        guard let w = window else {
            host.send(["t": "dialogResult", "seq": seq, "ok": false])
            return
        }
        let kind = m["kind"] as? String ?? "alert"
        let alert = NSAlert()
        var origin = (m["origin"] as? String).flatMap { URL(string: $0)?.host } ?? ""
        if origin.isEmpty { origin = "This page" }
        alert.messageText = "\(origin) says"
        alert.informativeText = m["message"] as? String ?? ""
        alert.addButton(withTitle: "OK")
        if kind != "alert" { alert.addButton(withTitle: "Cancel") }
        var field: NSTextField?
        if kind == "prompt" {
            let f = NSTextField(string: m["default"] as? String ?? "")
            f.frame = NSRect(x: 0, y: 0, width: 260, height: 22)
            alert.accessoryView = f
            field = f
        }
        alert.beginSheetModal(for: w) { [weak self] r in
            MainActor.assumeIsolated {
                self?.host.send(["t": "dialogResult", "seq": seq, "ok": r == .alertFirstButtonReturn, "text": field?.stringValue ?? ""])
                self?.focusSoon()
            }
        }
        if let field { alert.window.initialFirstResponder = field }
    }

    /// A link for another app (obsidian:, zoommtg:, …), which Chromium can't
    /// load: opened in that app once the person says so, as Chrome asks.
    /// "Always allow" remembers the scheme (browser-open-schemes in the
    /// config; Settings ▸ General lists them).
    private func openExternal(_ m: [String: Any]) {
        guard let s = m["url"] as? String, let url = URL(string: s), let scheme = url.scheme?.lowercased(),
              let appPath = m["app"] as? String else { return }
        let app = URL(fileURLWithPath: appPath)
        if AppConfig.shared.browserOpenSchemes.contains(scheme) {
            NSWorkspace.shared.open([url], withApplicationAt: app, configuration: NSWorkspace.OpenConfiguration())
            return
        }
        // One question at a time: a page can't stack them up.
        guard let w = window, w.attachedSheet == nil else { return }
        let name = FileManager.default.displayName(atPath: appPath).replacingOccurrences(of: ".app", with: "")
        let from = (m["from"] as? String).flatMap { $0.isEmpty ? nil : $0 } ?? "This page"
        let alert = NSAlert()
        alert.icon = NSWorkspace.shared.icon(forFile: appPath)
        alert.messageText = "Open “\(name)”?"
        let shown = s.count > 300 ? String(s.prefix(300)) + "…" : s
        alert.informativeText = "\(from) wants to open this link in \(name).\n\n\(shown)"
        alert.addButton(withTitle: "Open \(name)")
        alert.addButton(withTitle: "Cancel")
        alert.showsSuppressionButton = true
        alert.suppressionButton?.title = "Always open \(scheme): links in \(name)"
        alert.beginSheetModal(for: w) { [weak self] r in
            MainActor.assumeIsolated {
                if r == .alertFirstButtonReturn {
                    if alert.suppressionButton?.state == .on {
                        AppConfig.shared.setBrowserOpenSchemes(AppConfig.shared.browserOpenSchemes + [scheme])
                    }
                    NSWorkspace.shared.open([url], withApplicationAt: app, configuration: NSWorkspace.OpenConfiguration())
                } else {
                    self?.focusSoon()
                }
            }
        }
    }

    /// <input type=file>, showSaveFilePicker and downloads, as a sheet.
    private func showFilePanel(_ m: [String: Any]) {
        let seq = m["seq"] ?? 0
        let mode = m["mode"] as? String ?? "open"
        guard let w = window else {
            host.send(["t": "fileResult", "seq": seq, "paths": [String]()])
            return
        }
        let types = ((m["exts"] as? [String]) ?? []).compactMap { UTType(filenameExtension: $0) }
        let done: (NSSavePanel, NSApplication.ModalResponse) -> Void = { [weak self] panel, r in
            var paths: [String] = []
            if r == .OK {
                if let open = panel as? NSOpenPanel { paths = open.urls.map(\.path) } else if let u = panel.url { paths = [u.path] }
            }
            self?.host.send(["t": "fileResult", "seq": seq, "paths": paths])
            self?.focusSoon()
        }
        if mode == "save" || mode == "download" {
            let panel = NSSavePanel()
            let def = m["default"] as? String ?? ""
            if !def.isEmpty { panel.nameFieldStringValue = (def as NSString).lastPathComponent }
            if !types.isEmpty { panel.allowedContentTypes = types }
            if mode == "download" {
                // Every download asks where it goes, starting in ~/Downloads.
                if !def.isEmpty { panel.directoryURL = URL(fileURLWithPath: (def as NSString).deletingLastPathComponent, isDirectory: true) }
                let from = m["from"] as? String ?? ""
                panel.message = from.isEmpty ? "Save this download?" : "Save this download from \(from)?"
                panel.isExtensionHidden = false
            }
            panel.beginSheetModal(for: w) { r in MainActor.assumeIsolated { done(panel, r) } }
            return
        }
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = mode == "multiple"
        panel.canChooseDirectories = mode == "folder"
        panel.canChooseFiles = mode != "folder"
        if !types.isEmpty, mode != "folder" { panel.allowedContentTypes = types }
        if let t = m["title"] as? String, !t.isEmpty { panel.message = t }
        panel.beginSheetModal(for: w) { r in MainActor.assumeIsolated { done(panel, r) } }
    }

    private static func cursor(_ type: Int) -> NSCursor {
        switch type {
        case 1: return .crosshair
        case 2: return .pointingHand
        case 3: return .iBeam
        case 6, 13, 15, 18: return .resizeLeftRight
        case 7, 10, 14, 19: return .resizeUpDown
        case 30: return .iBeamCursorForVerticalLayout
        case 32: return .contextualMenu
        case 33: return .dragLink
        case 35, 38, 46: return .operationNotAllowed
        case 36, 48: return .dragCopy
        case 41: return .openHand
        case 42: return .closedHand
        default: return .arrow
        }
    }
}

// MARK: - Drags from the page

/// A drag the page starts (selected text, a link, an image) runs here, from
/// the pane, with the real mouse: begun in the host, a background app, macOS
/// never ended it and the whole window stayed stuck in it. Its data and
/// image come from the host, which hears back how it ended. The pane isn't a
/// drop target, so dropping back onto the page does nothing.
extension BrowserPaneNSView: NSDraggingSource {
    fileprivate func startDrag(_ m: [String: Any]) {
        let items = (m["items"] as? [[String: String]] ?? []).compactMap { types -> NSPasteboardItem? in
            let item = NSPasteboardItem()
            for (type, b64) in types {
                if let d = Data(base64Encoded: b64) { item.setData(d, forType: NSPasteboard.PasteboardType(type)) }
            }
            return item.types.isEmpty ? nil : item
        }
        dragSeq = m["seq"] as? Int ?? 0
        // The button may be up already (a quick flick): nothing to drag then.
        guard let e = dragEvent, e.window === window, NSEvent.pressedMouseButtons & 1 != 0, !items.isEmpty else {
            endDrag(at: NSEvent.mouseLocation, operation: [])
            return
        }
        dragMask = NSDragOperation(rawValue: (m["mask"] as? UInt) ?? NSDragOperation.copy.rawValue)
        var image: NSImage?
        var frame = NSRect(origin: convert(e.locationInWindow, from: nil), size: .zero)
        if let png = (m["png"] as? String).flatMap({ Data(base64Encoded: $0) }), let img = NSImage(data: png) {
            let w = (m["w"] as? Double) ?? img.size.width, h = (m["h"] as? Double) ?? img.size.height
            img.size = NSSize(width: w, height: h)
            image = img
            frame = NSRect(x: (m["x"] as? Double) ?? 0, y: bounds.height - ((m["y"] as? Double) ?? 0) - h, width: w, height: h)
        }
        let dragging = items.enumerated().map { i, item in
            let d = NSDraggingItem(pasteboardWriter: item)
            d.setDraggingFrame(frame, contents: i == 0 ? image : nil)
            return d
        }
        beginDraggingSession(with: dragging, event: e, source: self)
        dragEvent = nil
    }

    func draggingSession(_ session: NSDraggingSession, sourceOperationMaskFor context: NSDraggingContext) -> NSDragOperation {
        dragMask
    }

    func draggingSession(_ session: NSDraggingSession, endedAt screenPoint: NSPoint, operation: NSDragOperation) {
        endDrag(at: screenPoint, operation: operation)
    }

    private func endDrag(at p: NSPoint, operation: NSDragOperation) {
        host.send(["t": "dragEnd", "id": paneID, "seq": dragSeq, "x": p.x, "y": p.y, "op": operation.rawValue])
    }
}

/// Records which item of a menu shown for the page was chosen (-1: none).
private final class MenuPick: NSObject {
    var chosen = -1
    @objc func pick(_ item: NSMenuItem) { chosen = item.tag }
}

enum BrowserCommand: String { case focusAddress, reload, back, forward, find, findNext, findPrevious, devTools }

extension BrowserCommand {
    /// The menu bar and palette's way to the focused browser pane (its
    /// command in userInfo["command"]).
    static let notification = Notification.Name("HammockBrowserCommand")
    /// For the focused pane in the window you're in (one of its own, or the main one).
    @MainActor func post() {
        NotificationCenter.default.post(name: Self.notification, object: nil,
                                        userInfo: ["command": rawValue, "window": DetachedWindows.shared.keyScope?.id.uuidString ?? ""])
    }
}

/// Over a browser pane whose page is in another window (BrowserHost).
private final class StandbyCover: NSView {
    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        layer?.backgroundColor = NSColor.windowBackgroundColor.withAlphaComponent(0.85).cgColor
        let title = NSTextField(labelWithString: "Showing in another window")
        title.font = .systemFont(ofSize: 15, weight: .semibold)
        let hint = NSTextField(labelWithString: "Click to show it here")
        hint.font = .systemFont(ofSize: 13)
        hint.textColor = .secondaryLabelColor
        let stack = NSStackView(views: [title, hint])
        stack.orientation = .vertical
        stack.spacing = 4
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([stack.centerXAnchor.constraint(equalTo: centerXAnchor), stack.centerYAnchor.constraint(equalTo: centerYAnchor)])
    }

    required init?(coder: NSCoder) { fatalError() }

    // Clicks reach the pane under it, which takes the page back.
    override func hitTest(_ point: NSPoint) -> NSView? { nil }
}

// MARK: - SwiftUI

struct BrowserView: NSViewRepresentable {
    let paneID: String
    let model: AppModel
    var isFocused: Bool
    var onCommand: (BrowserCommand) -> Void
    var onFound: (Int, Int) -> Void = { _, _ in }

    func makeNSView(context: Context) -> BrowserPaneNSView {
        let v = BrowserPaneNSView(paneID: paneID)
        v.onFocus = { [paneID, model] in model.viewFocused(paneID) }
        v.onCommand = onCommand
        v.onFound = onFound
        v.isFocusTarget = isFocused
        return v
    }

    func updateNSView(_ v: BrowserPaneNSView, context: Context) {
        v.onCommand = onCommand
        v.onFound = onFound
        v.isFocusTarget = isFocused
    }
}

/// A browser pane: address bar over the page.
struct BrowserPaneView: View {
    let model: AppModel
    let paneID: String
    let isFocused: Bool
    @State private var address = ""
    @State private var editing = false
    @FocusState private var addressFocused: Bool
    // Find in page (⌘F): its bar, what's searched for, and the results.
    @State private var finding = false
    @State private var findText = ""
    @State private var matches = 0
    @State private var activeMatch = 0
    @FocusState private var findFocused: Bool
    @Environment(\.windowScope) private var windowScope

    private var pane: Pane? { model.panes[paneID] }
    private var pageURL: String { pane.map { $0.url == "about:blank" ? "" : $0.url } ?? "" }

    /// In a Group shared with you, the page runs in its host's browser.
    private var remote: WorkspaceGroup? { model.group(ofPane: paneID).flatMap { $0.isRemote ? $0 : nil } }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
                .disabled(remote.map { !$0.canDrive || $0.offline } ?? false)
                .contextMenu { PaneMenuItems(model: model, paneID: paneID) }
                .paneSwapHandle(paneID, model: model)
            if finding, remote == nil {
                findBar
                    .transition(.move(edge: .top).combined(with: .opacity))
            }
            if let remote {
                RemoteBrowserPane(model: model, group: remote, pane: pane, paneID: paneID)
            } else {
                BrowserView(paneID: paneID, model: model, isFocused: isFocused && !addressFocused && !findFocused,
                            onCommand: { run($0) }, onFound: { matches = $0; activeMatch = $1 })
                    .overlay { AgentPresenceOverlay(model: model, paneID: paneID, isSimulator: false) }
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: BrowserCommand.notification)) { n in
            // Only in the window it was meant for (the pane may show in two).
            guard (n.userInfo?["window"] as? String ?? "") == (windowScope?.id.uuidString ?? "") else { return }
            guard isFocused, let raw = n.userInfo?["command"] as? String, let c = BrowserCommand(rawValue: raw) else { return }
            run(c)
        }
        .onChange(of: findText) { _, t in search(t, next: false) }
        .onAppear {
            address = pageURL
            // A new, empty browser tab starts in its address bar.
            if pageURL.isEmpty, isFocused { DispatchQueue.main.async { addressFocused = true } }
        }
        .onChange(of: pageURL) { _, u in if !addressFocused { address = u } }
        .onChange(of: addressFocused) { _, f in
            if f { model.focus(paneID) } else { address = pageURL }
        }
    }

    private var toolbar: some View {
        HStack(spacing: 2) {
            PaneKindMenu(model: model, paneID: paneID) {
                Image(systemName: "globe")
            }
            .fixedSize() // only an icon: never squeezed away
            .padding(.trailing, 4)
            button("chevron.left", "Back (⌘←)", enabled: pane?.canGoBack == true) { run(.back) }
            button("chevron.right", "Forward (⌘→)", enabled: pane?.canGoForward == true) { run(.forward) }
            if pane?.loading == true {
                button("xmark", "Stop") { model.navigate(paneID, "stop") }
            } else {
                button("arrow.clockwise", "Reload (⌘R)") { run(.reload) }
            }
            TextField("Search or enter address", text: $address)
                .textFieldStyle(.plain)
                .font(.callout)
                .focused($addressFocused)
                .padding(.horizontal, 8)
                .frame(height: 22)
                .background(RoundedRectangle(cornerRadius: 6).fill(Color.primary.opacity(addressFocused ? 0.09 : 0.05)))
                .onSubmit {
                    model.navigate(paneID, "load", url: address)
                    addressFocused = false
                    NotificationCenter.default.post(name: TerminalNSView.refocus, object: nil)
                }
                .onKeyPress(.escape) {
                    address = pageURL
                    addressFocused = false
                    NotificationCenter.default.post(name: TerminalNSView.refocus, object: nil)
                    return .handled
                }
                .padding(.leading, 4)
            FloatButtons(model: model, paneID: paneID)
            PaneCloseButton(model: model, paneID: paneID)
        }
        .padding(.leading, 15) // lined up with the window title, as the terminal's header
        .padding(.trailing, 6)
        .frame(height: 28)
        .background(isFocused ? Color.themeAccent.opacity(0.06) : Color.clear)
    }

    private func button(_ symbol: String, _ help: String, enabled: Bool = true, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 12, weight: .medium))
                .frame(width: 24, height: 22)
                .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .disabled(!enabled)
        .tooltip(help)
    }

    private func run(_ c: BrowserCommand) {
        switch c {
        case .focusAddress: addressFocused = true
        case .reload: model.navigate(paneID, "reload")
        case .back: model.navigate(paneID, "back")
        case .forward: model.navigate(paneID, "forward")
        case .find:
            guard remote == nil else { return }
            withAnimation(.snappy(duration: 0.18)) { finding = true }
            DispatchQueue.main.async { findFocused = true }
            if !findText.isEmpty { search(findText, next: false) }
        case .findNext: if finding { search(findText, next: true) } else { run(.find) }
        case .findPrevious: if finding { search(findText, next: true, backward: true) } else { run(.find) }
        case .devTools:
            guard remote == nil, !(pane?.url.hasPrefix("hmk-devtools:") ?? false) else { return }
            Task { await model.showDevTools(paneID) }
        }
    }

    // MARK: Find in page

    /// Searches the page: a new search as the text changes, else the next
    /// (or previous) match.
    private func search(_ text: String, next: Bool, backward: Bool = false) {
        guard finding else { return }
        if text.isEmpty {
            BrowserHost.shared.send(["t": "stopFind", "id": paneID, "clear": true])
            matches = 0
            activeMatch = 0
            return
        }
        BrowserHost.shared.send(["t": "find", "id": paneID, "text": text, "next": next, "backward": backward])
    }

    private func endFind() {
        // The match stays selected, as in Safari and Chrome.
        BrowserHost.shared.send(["t": "stopFind", "id": paneID, "clear": false])
        withAnimation(.snappy(duration: 0.18)) { finding = false }
        findFocused = false
        NotificationCenter.default.post(name: TerminalNSView.refocus, object: nil)
    }

    private var findBar: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass").font(.system(size: 11, weight: .medium)).foregroundStyle(.secondary)
            TextField("Find in page", text: $findText)
                .textFieldStyle(.plain)
                .font(.callout)
                .focused($findFocused)
                .onSubmit { search(findText, next: true, backward: NSEvent.modifierFlags.contains(.shift)) }
                .onKeyPress(.escape) { endFind(); return .handled }
            if !findText.isEmpty {
                Text(matches == 0 ? "Not found" : "\(activeMatch) of \(matches)")
                    .font(.caption)
                    .monospacedDigit()
                    .foregroundStyle(matches == 0 ? Color.red.opacity(0.8) : .secondary)
            }
            button("chevron.up", Keys.hint("Previous", "find_previous"), enabled: matches > 0) { search(findText, next: true, backward: true) }
            button("chevron.down", Keys.hint("Next", "find_next"), enabled: matches > 0) { search(findText, next: true) }
            Button("Done") { endFind() }
                .buttonStyle(.borderless)
                .font(.callout)
                .padding(.leading, 2)
        }
        .padding(.leading, 15)
        .padding(.trailing, 8)
        .frame(height: 30)
        .background(Color.primary.opacity(0.04))
        .overlay(alignment: .bottom) { Divider() }
    }
}

/// A page open in the host's browser, in a Group shared with you: what it
/// is and where it's got to, live. Drivers steer it from the toolbar above;
/// anyone can open it in their own browser.
struct RemotePageCard: View {
    let model: AppModel
    let group: WorkspaceGroup
    let pane: Pane?

    var body: some View {
        let host = model.people[group.hostUserID]
        VStack(spacing: 14) {
            Favicon(url: pane?.faviconURL ?? "", size: 40)
            VStack(spacing: 4) {
                Text(pane.map { $0.title.isEmpty ? "Untitled page" : $0.title } ?? "")
                    .font(.title3.weight(.semibold))
                    .multilineTextAlignment(.center)
                    .lineLimit(2)
                Text(pane?.url ?? "")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
                if pane?.loading == true {
                    ProgressView().controlSize(.small).padding(.top, 4)
                }
            }
            Text(host.map { "Open in \($0.name)'s browser on \($0.deviceName)." } ?? "Open in its host's browser.")
                + Text(group.canDrive ? " The controls above steer it." : "")
            if let url = pane.flatMap({ URL(string: $0.url) }), url.scheme?.hasPrefix("http") == true {
                Button("Open in Your Browser") { NSWorkspace.shared.open(url) }
                    .buttonStyle(.borderedProminent)
            }
        }
        .foregroundStyle(.primary)
        .font(.callout)
        .multilineTextAlignment(.center)
        .padding(32)
        .frame(maxWidth: 460)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .opacity(group.offline ? 0.5 : 1)
    }
}
