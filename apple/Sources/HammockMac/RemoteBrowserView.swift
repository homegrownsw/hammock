import AppKit
import HammockKit
import SwiftUI

/// A browser pane in a Group shared with you: its host's page as live video
/// (MediaStream, relayed by your daemon), at the size it has on the host's
/// Mac, scaled to fit. With Drive, your clicks, scrolling and typing go to
/// the page (BrowserInput); with View, it's only shown. Until the video
/// plays, the page's card stands in.
struct RemoteBrowserPane: View {
    let model: AppModel
    let group: WorkspaceGroup
    let pane: Pane?
    let paneID: String
    @State private var playing = false

    var body: some View {
        ZStack {
            if !playing { RemotePageCard(model: model, group: group, pane: pane) }
            RemoteBrowserView(model: model, paneID: paneID, canDrive: group.canDrive && !group.offline,
                              menu: model.browserMenus[paneID]) { playing = $0 }
                .opacity(playing ? 1 : 0)
                .allowsHitTesting(playing) // until then, the card's buttons take clicks
        }
    }
}

struct RemoteBrowserView: NSViewRepresentable {
    let model: AppModel
    let paneID: String
    let canDrive: Bool
    var menu: BrowserMenu?
    let onPlaying: (Bool) -> Void

    func makeNSView(context: Context) -> RemoteBrowserNSView {
        let v = RemoteBrowserNSView(model: model, paneID: paneID)
        v.canDrive = canDrive
        v.onPlaying = onPlaying
        return v
    }

    func updateNSView(_ v: RemoteBrowserNSView, context: Context) {
        v.canDrive = canDrive
        v.onPlaying = onPlaying
        if let menu { v.show(menu) }
    }

    static func dismantleNSView(_ v: RemoteBrowserNSView, coordinator: ()) { v.stopVideo() }
}

final class RemoteBrowserNSView: NSView {
    let model: AppModel
    let paneID: String
    var canDrive = false
    var onPlaying: ((Bool) -> Void)?
    private var stream: MediaStream?
    private var pixels = CGSize(width: 16, height: 10)
    private var buttonDown: String?
    var shownMenu: Int32 = -1
    // Text input: the key being interpreted, keys sent down as keys, and
    // the composition (IME marked text) in progress.
    var typing: NSEvent?
    var downKeys = Set<UInt16>()
    var marked = ""
    var lastClick = NSPoint.zero

    init(model: AppModel, paneID: String) {
        self.model = model
        self.paneID = paneID
        super.init(frame: .zero)
        wantsLayer = true
    }

    required init?(coder: NSCoder) { fatalError() }

    override var isFlipped: Bool { true }
    override var acceptsFirstResponder: Bool { canDrive }
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if window != nil { startVideo() } else { stopVideo() }
    }

    override func layout() {
        super.layout()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        stream?.layer.frame = bounds
        CATransaction.commit()
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        trackingAreas.forEach(removeTrackingArea)
        addTrackingArea(NSTrackingArea(rect: .zero, options: [.mouseMoved, .activeInKeyWindow, .inVisibleRect], owner: self))
    }

    // MARK: Video

    private func startVideo() {
        guard stream == nil else { return }
        guard let s = model.videoStream(for: paneID) else {
            retryVideo()
            return
        }
        stream = s
        s.onSize = { [weak self] px in
            self?.pixels = px
        }
        s.onState = { [weak self, weak s] st in
            guard let self, let s, self.stream === s else { return }
            switch st {
            case .playing: self.onPlaying?(true)
            case .failed:
                self.stopVideo()
                self.retryVideo()
            default: break
            }
        }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        s.layer.videoGravity = .resizeAspect
        s.layer.frame = bounds
        layer?.addSublayer(s.layer)
        CATransaction.commit()
        s.start()
    }

    func stopVideo() {
        stream?.onState = nil
        stream?.onSize = nil
        stream?.stop()
        stream?.layer.removeFromSuperlayer()
        stream = nil
        onPlaying?(false)
    }

    private func retryVideo() {
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
            guard let self, self.window != nil, self.stream == nil else { return }
            self.startVideo()
        }
    }

    // MARK: Input

    /// Where the page is drawn (the video, aspect-fit in the view).
    var pageRect: CGRect {
        guard pixels.width > 0, pixels.height > 0, bounds.width > 0, bounds.height > 0 else { return bounds }
        let s = min(bounds.width / pixels.width, bounds.height / pixels.height)
        let w = pixels.width * s, h = pixels.height * s
        return CGRect(x: (bounds.width - w) / 2, y: (bounds.height - h) / 2, width: w, height: h)
    }

    /// An event's position as fractions of the page, or nil outside it.
    private func position(_ e: NSEvent, clamped: Bool = false) -> [String: Any]? {
        let p = convert(e.locationInWindow, from: nil), r = pageRect
        var x = (p.x - r.minX) / r.width, y = (p.y - r.minY) / r.height
        if clamped {
            x = min(max(x, 0), 1)
            y = min(max(y, 0), 1)
        } else if !(0...1).contains(x) || !(0...1).contains(y) {
            return nil
        }
        return ["x": x, "y": y]
    }

    private func mods(_ e: NSEvent) -> [String] {
        var m: [String] = []
        if e.modifierFlags.contains(.option) { m.append("alt") }
        if e.modifierFlags.contains(.control) { m.append("ctrl") }
        if e.modifierFlags.contains(.command) { m.append("meta") }
        if e.modifierFlags.contains(.shift) { m.append("shift") }
        return m
    }

    private func send(_ op: String, _ args: [String: Any]) {
        guard canDrive else { return }
        model.browserInput(paneID, op, args)
    }

    @discardableResult
    private func mouse(_ type: String, _ e: NSEvent, button: String, clamped: Bool = false, extra: [String: Any] = [:]) -> Bool {
        guard var a = position(e, clamped: clamped) else { return false }
        a["type"] = type
        a["button"] = button
        a["mods"] = mods(e)
        a.merge(extra) { _, b in b }
        if type == "move" {
            queueMove(a)
            return true
        }
        flushMove() // a press or release comes after the moves before it
        send("mouse", a)
        return true
    }

    /// Moves go at most once a frame (the latest), as the page would see them.
    private var pendingMove: [String: Any]?
    private var lastMoveAt = Date.distantPast

    private func queueMove(_ a: [String: Any]) {
        let first = pendingMove == nil
        pendingMove = a
        let wait = 1.0 / 60 - Date().timeIntervalSince(lastMoveAt)
        if wait <= 0 {
            flushMove()
        } else if first {
            DispatchQueue.main.asyncAfter(deadline: .now() + wait) { [weak self] in self?.flushMove() }
        }
    }

    private func flushMove() {
        guard let a = pendingMove else { return }
        pendingMove = nil
        lastMoveAt = Date()
        send("mouse", a)
    }

    override func mouseDown(with e: NSEvent) {
        lastClick = convert(e.locationInWindow, from: nil)
        model.focus(paneID)
        guard canDrive else { return }
        window?.makeFirstResponder(self)
        if mouse("down", e, button: "left", extra: ["clicks": e.clickCount]) { buttonDown = "left" }
    }

    override func mouseDragged(with e: NSEvent) {
        mouse("move", e, button: "left", clamped: true, extra: ["pressed": true])
    }

    override func mouseUp(with e: NSEvent) {
        guard buttonDown == "left" else { return }
        buttonDown = nil
        mouse("up", e, button: "left", clamped: true, extra: ["clicks": e.clickCount])
    }

    override func rightMouseDown(with e: NSEvent) {
        model.focus(paneID)
        guard canDrive else { return }
        if mouse("down", e, button: "right", extra: ["clicks": 1]) { buttonDown = "right" }
    }

    override func rightMouseUp(with e: NSEvent) {
        guard buttonDown == "right" else { return }
        buttonDown = nil
        mouse("up", e, button: "right", clamped: true, extra: ["clicks": 1])
    }

    override func mouseMoved(with e: NSEvent) {
        mouse("move", e, button: "none")
    }

    override func scrollWheel(with e: NSEvent) {
        guard canDrive, var a = position(e), pageRect.width > 0 else { return }
        // In the page's terms: a line is about 40 points.
        let k: CGFloat = e.hasPreciseScrollingDeltas ? 1 : 40
        a["dx"] = -e.scrollingDeltaX * k / pageRect.width
        a["dy"] = -e.scrollingDeltaY * k / pageRect.height
        a["mods"] = mods(e)
        send("wheel", a)
    }

    // MARK: Keys

    /// Keys with no text, as the page knows them: macOS key code to the
    /// DOM's key (and code) and the Windows virtual key code.
    private static let special: [UInt16: (String, Int)] = [
        36: ("Enter", 13), 76: ("Enter", 13), 48: ("Tab", 9), 51: ("Backspace", 8), 117: ("Delete", 46),
        53: ("Escape", 27), 123: ("ArrowLeft", 37), 124: ("ArrowRight", 39), 125: ("ArrowDown", 40),
        126: ("ArrowUp", 38), 115: ("Home", 36), 119: ("End", 35), 116: ("PageUp", 33), 121: ("PageDown", 34),
        122: ("F1", 112), 120: ("F2", 113), 99: ("F3", 114), 118: ("F4", 115), 96: ("F5", 116), 97: ("F6", 117),
        98: ("F7", 118), 100: ("F8", 119), 101: ("F9", 120), 109: ("F10", 121), 103: ("F11", 122), 111: ("F12", 123),
    ]

    /// The other keys' DOM codes and Windows virtual key codes (by position,
    /// as a US keyboard has them, as the DOM's code is).
    private static let codes: [UInt16: (String, Int)] = {
        var m: [UInt16: (String, Int)] = [49: ("Space", 32), 27: ("Minus", 189), 24: ("Equal", 187), 33: ("BracketLeft", 219),
            30: ("BracketRight", 221), 42: ("Backslash", 220), 41: ("Semicolon", 186), 39: ("Quote", 222), 43: ("Comma", 188),
            47: ("Period", 190), 44: ("Slash", 191), 50: ("Backquote", 192)]
        let letters: [(UInt16, Character)] = [(0, "A"), (11, "B"), (8, "C"), (2, "D"), (14, "E"), (3, "F"), (5, "G"), (4, "H"), (34, "I"),
            (38, "J"), (40, "K"), (37, "L"), (46, "M"), (45, "N"), (31, "O"), (35, "P"), (12, "Q"), (15, "R"), (1, "S"), (17, "T"),
            (32, "U"), (9, "V"), (13, "W"), (7, "X"), (16, "Y"), (6, "Z")]
        for (k, c) in letters { m[k] = ("Key\(c)", Int(c.asciiValue!)) }
        let digits: [(UInt16, Character)] = [(29, "0"), (18, "1"), (19, "2"), (20, "3"), (21, "4"), (23, "5"), (22, "6"), (26, "7"), (28, "8"), (25, "9")]
        for (k, c) in digits { m[k] = ("Digit\(c)", Int(c.asciiValue!)) }
        return m
    }()

    private func key(_ type: String, _ e: NSEvent) {
        var a: [String: Any] = ["type": type, "mods": mods(e)]
        if let (name, vk) = Self.special[e.keyCode] {
            a["key"] = name
            a["code"] = name
            a["vk"] = vk
            if name == "Enter", type == "down" { a["text"] = "\r" } // submits forms, breaks lines
        } else {
            // With ⌃ or ⌘ held, the key itself (⌃A is "a", not a control character).
            let shortcut = e.modifierFlags.contains(.command) || e.modifierFlags.contains(.control)
            let chars = (shortcut ? e.charactersIgnoringModifiers : e.characters) ?? ""
            a["key"] = chars
            if let (code, vk) = Self.codes[e.keyCode] {
                a["code"] = code
                a["vk"] = vk
            }
            // Text, unless it's a shortcut (⌘ or ⌃ held).
            if type == "down", !e.modifierFlags.contains(.command), !e.modifierFlags.contains(.control),
               let u = chars.unicodeScalars.first, u.value >= 0x20, u.value != 0x7F, !(0xF700...0xF8FF).contains(u.value) {
                a["text"] = chars
            }
        }
        send("key", a)
    }

    override func keyDown(with e: NSEvent) {
        guard canDrive else { return super.keyDown(with: e) }
        // Keys without text, and shortcuts, go as they are; the rest through
        // this Mac's text input system (dead keys, IME, Press-and-Hold),
        // which answers through NSTextInputClient below.
        let shortcut = e.modifierFlags.contains(.command) || e.modifierFlags.contains(.control)
        if !marked.isEmpty || (Self.special[e.keyCode] == nil && !shortcut) {
            typing = e
            interpretKeyEvents([e])
            typing = nil
            return
        }
        key("down", e)
        if e.modifierFlags.contains(.command) {
            // macOS sends no keyUp for a key pressed with ⌘ held: up now, or
            // the page sees it held until the next key.
            key("up", e)
        } else {
            downKeys.insert(e.keyCode)
        }
    }

    override func keyUp(with e: NSEvent) {
        guard canDrive else { return super.keyUp(with: e) }
        guard downKeys.remove(e.keyCode) != nil else { return }
        key("up", e)
    }

    /// An editing shortcut: down (with its command) and straight up again.
    private func shortcut(_ key: String, _ code: String, _ vk: Int, _ mods: [String], _ command: String) {
        send("key", ["type": "down", "key": key, "code": code, "vk": vk, "mods": mods, "commands": [command]])
        send("key", ["type": "up", "key": key, "code": code, "vk": vk, "mods": mods])
    }

    /// The page's own editing shortcuts: ⌘C and ⌘X bring the selection back
    /// to your clipboard (BrowserClipboard); ⌘V sends yours.
    override func performKeyEquivalent(with e: NSEvent) -> Bool {
        guard canDrive, window?.firstResponder === self, e.type == .keyDown else { return super.performKeyEquivalent(with: e) }
        let flags = e.modifierFlags.intersection([.command, .shift, .option, .control])
        let ch = e.charactersIgnoringModifiers?.lowercased() ?? ""
        switch (flags, ch) {
        case ([.command], "a"): shortcut("a", "KeyA", 65, ["meta"], "selectAll")
        case ([.command], "z"): shortcut("z", "KeyZ", 90, ["meta"], "undo")
        case ([.command, .shift], "z"): shortcut("z", "KeyZ", 90, ["meta", "shift"], "redo")
        case ([.command], "v"):
            if let s = NSPasteboard.general.string(forType: .string), !s.isEmpty { send("text", ["text": s]) }
        case ([.command], "c"): send("copy", [:])
        case ([.command], "x"): send("cut", [:])
        default: return super.performKeyEquivalent(with: e)
        }
        return true
    }
}

// MARK: Page menus

extension RemoteBrowserNSView {
    /// Shows a menu the page opened after your input (a <select>'s options
    /// or a right-click) where it opened on the page, and sends the choice.
    func show(_ m: BrowserMenu) {
        guard m.seq != shownMenu else { return }
        shownMenu = m.seq
        // After this update pass: popUp tracks the menu until it closes.
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            let menu = NSMenu()
            menu.autoenablesItems = false
            let picker = MenuPicker()
            for (i, it) in m.items.enumerated() {
                if it.separator {
                    menu.addItem(.separator())
                    continue
                }
                let item = NSMenuItem(title: it.title, action: #selector(MenuPicker.pick(_:)), keyEquivalent: "")
                item.target = picker
                item.tag = i
                item.isEnabled = it.enabled
                item.indentationLevel = Int(it.indent)
                item.state = it.checked ? .on : .off
                menu.addItem(item)
            }
            let r = self.pageRect
            let at = NSPoint(x: r.minX + m.x * r.width, y: r.minY + (m.y + (m.kind == "select" ? m.h : 0)) * r.height)
            if m.kind == "select" {
                menu.minimumWidth = m.w * r.width
                let selected = m.selected >= 0 && Int(m.selected) < menu.items.count ? menu.items[Int(m.selected)] : nil
                // Over the <select>, the current option where the control is, as a native popup.
                let top = NSPoint(x: r.minX + m.x * r.width, y: r.minY + m.y * r.height)
                menu.popUp(positioning: selected, at: selected == nil ? at : top, in: self)
            } else {
                menu.popUp(positioning: nil, at: at, in: self)
            }
            self.model.answerBrowserMenu(m, index: picker.chosen)
        }
    }
}

/// Records which item of a page's menu was picked (-1: none).
private final class MenuPicker: NSObject {
    var chosen = -1
    @objc func pick(_ item: NSMenuItem) { chosen = item.tag }
}

// MARK: Text input

extension RemoteBrowserNSView: NSTextInputClient {
    func insertText(_ string: Any, replacementRange: NSRange) {
        let s = (string as? NSAttributedString)?.string ?? (string as? String) ?? ""
        let wasComposing = !marked.isEmpty
        marked = ""
        guard !s.isEmpty else { return }
        // Plain typing: a key with its text, so the page sees keydown and
        // keypress. Composed text (a dead key, an IME) is inserted as text.
        if let e = typing, !wasComposing, e.characters == s {
            downKeys.insert(e.keyCode)
            key("down", e)
        } else {
            send("text", ["text": s])
        }
    }

    func setMarkedText(_ string: Any, selectedRange: NSRange, replacementRange: NSRange) {
        marked = (string as? NSAttributedString)?.string ?? (string as? String) ?? ""
        send("compose", ["text": marked, "start": selectedRange.location, "end": selectedRange.location + selectedRange.length])
    }

    func unmarkText() {
        if !marked.isEmpty { send("text", ["text": marked]) }
        marked = ""
    }

    override func doCommand(by selector: Selector) {
        // Text system commands for keys that reached it (e.g. a dead key's
        // fallback): replay the key itself.
        if let e = typing {
            downKeys.insert(e.keyCode)
            key("down", e)
        }
    }

    func selectedRange() -> NSRange { NSRange(location: marked.count, length: 0) }
    func markedRange() -> NSRange { marked.isEmpty ? NSRange(location: NSNotFound, length: 0) : NSRange(location: 0, length: marked.count) }
    func hasMarkedText() -> Bool { !marked.isEmpty }
    func attributedSubstring(forProposedRange range: NSRange, actualRange: NSRangePointer?) -> NSAttributedString? { nil }
    func validAttributesForMarkedText() -> [NSAttributedString.Key] { [] }
    func characterIndex(for point: NSPoint) -> Int { 0 }

    /// Where IME candidates go: by the last click (the page's caret isn't known here).
    func firstRect(forCharacterRange range: NSRange, actualRange: NSRangePointer?) -> NSRect {
        let r = NSRect(origin: lastClick, size: NSSize(width: 1, height: 18))
        return window?.convertToScreen(convert(r, to: nil)) ?? .zero
    }
}
