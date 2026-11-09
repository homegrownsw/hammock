import AppKit
import HammockKit
import SwiftUI

// Screen sharing (ADR-0014): another of your Macs' screens in a window of
// its own, like Screen Sharing.app. Clicks, scrolling and keys (shortcuts
// too, while the window is in front) go to that Mac as its own events.

typealias ScreenDisplay = Hammock_V1_Display

/// The screen windows, one per computer.
@MainActor
final class ScreenViewerUI {
    static let shared = ScreenViewerUI()
    private var windows: [String: NSWindow] = [:]
    private var delegates: [String: WindowCloser] = [:]

    /// Shows a computer's screen (computerID: a linked computer's, or this
    /// Mac's own: "" or your user ID).
    func open(_ model: AppModel, computerID: String, name: String) {
        let key = computerID == model.myUserID ? "" : computerID
        if let w = windows[key] {
            w.makeKeyAndOrderFront(nil)
            NSApp.activate()
            return
        }
        let w = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1200, height: 780),
                         styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView], backing: .buffered, defer: false)
        w.title = name
        w.subtitle = "Screen"
        w.titlebarAppearsTransparent = true
        w.titleVisibility = .hidden
        w.isReleasedWhenClosed = false
        w.collectionBehavior.insert(.fullScreenPrimary)
        w.contentMinSize = NSSize(width: 480, height: 320)
        w.contentView = NSHostingView(rootView: ScreenViewer(model: model, computerID: key, name: name) { [weak w] in w?.performClose(nil) }.themeTint())
        w.center()
        w.setFrameAutosaveName("HammockScreen-\(key.isEmpty ? "this" : key)")
        let closer = WindowCloser { [weak self] in
            self?.windows[key] = nil
            self?.delegates[key] = nil
        }
        w.delegate = closer
        windows[key] = w
        delegates[key] = closer
        w.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }
}

private final class WindowCloser: NSObject, NSWindowDelegate {
    let onClose: () -> Void
    init(_ onClose: @escaping () -> Void) { self.onClose = onClose }
    func windowWillClose(_ notification: Notification) {
        // The video stops with its view (ScreenVideo.dismantleNSView).
        (notification.object as? NSWindow)?.contentView = nil
        onClose()
    }
}

/// The window's content: the bar (display, fit, state, End) over the screen.
struct ScreenViewer: View {
    let model: AppModel
    let computerID: String
    let name: String
    let close: () -> Void

    @State private var displays: [ScreenDisplay] = []
    @State private var display: UInt32 = 0
    @State private var actualSize = false
    @State private var state: MediaStream.State = .connecting
    @State private var unavailable: String?
    @State private var pixels = CGSize.zero
    @State private var session = 0 // bumped to start the video again
    @State private var locked = false

    var body: some View {
        VStack(spacing: 0) {
            bar
            ZStack {
                Color.black
                if let unavailable {
                    ContentUnavailableView("Can’t Share \(name)’s Screen", systemImage: "rectangle.on.rectangle.slash", description: Text(unavailable))
                        .foregroundStyle(.white)
                } else {
                    screen
                }
                if case .failed(let why) = state, unavailable == nil {
                    VStack(spacing: 10) {
                        Text("The screen stopped").font(.headline)
                        Text(why).font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
                        Button("Try Again") { reconnect() }
                    }
                    .padding(20)
                    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14))
                }
            }
        }
        .ignoresSafeArea(.container, edges: .top)
        .task { await load() }
    }

    @ViewBuilder private var screen: some View {
        let video = ScreenVideo(model: model, computerID: computerID, display: display, session: session, state: $state, pixels: $pixels, locked: $locked)
            .id("\(display)-\(session)")
        if actualSize, pixels.width > 0 {
            let scale = NSScreen.main?.backingScaleFactor ?? 2
            ScrollView([.horizontal, .vertical]) {
                video.frame(width: pixels.width / scale, height: pixels.height / scale)
            }
        } else {
            video
        }
    }

    private var bar: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 1) {
                Text(name).font(.headline)
                Text(computerID.isEmpty && state == .playing ? "Screen · view only" : subtitle).font(.caption).foregroundStyle(.secondary)
            }
            .padding(.leading, 78) // the traffic lights
            Spacer()
            if displays.count > 1 {
                Picker(selection: $display) {
                    ForEach(displays, id: \.id) { d in
                        Text(d.name).tag(d.main ? UInt32(0) : d.id)
                    }
                } label: {
                    Label("Display", systemImage: "display")
                }
                .pickerStyle(.menu)
                .fixedSize()
                .help("Which display to show")
            }
            Picker("Size", selection: $actualSize) {
                Text("Fit").tag(false)
                Text("1:1").tag(true)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .fixedSize()
            .help("Fit the screen to the window, or show it at its own size")
            stateChip
            Button(role: .destructive) { close() } label: { Text("End") }
                .buttonStyle(.borderedProminent)
                .tint(.red)
                .help("Stop showing this screen")
        }
        .padding(.horizontal, 14)
        .frame(height: 52)
        .background(.bar)
    }

    private var subtitle: String {
        switch state {
        case .connecting: return "Connecting…"
        case .playing: return locked ? "Locked · type the password to unlock it" : "Screen · you’re in control"
        case .failed: return "Screen · stopped"
        }
    }

    private var stateChip: some View {
        let (color, text): (Color, String) = switch state {
        case .playing: (.green, "Live")
        case .connecting: (.orange, "Connecting")
        case .failed: (.red, "Stopped")
        }
        return HStack(spacing: 6) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(text).font(.callout)
        }
        .padding(.horizontal, 10)
        .frame(height: 26)
        .background(.quaternary, in: Capsule())
        .accessibilityElement(children: .combine)
    }

    private func load() async {
        guard let r = await model.listDisplays(computerID: computerID) else {
            unavailable = "\(name) didn’t answer."
            return
        }
        displays = r.displays
        unavailable = r.unavailable.isEmpty ? nil : r.unavailable
    }

    private func reconnect() {
        state = .connecting
        session += 1
        Task { await load() }
    }
}

/// The screen's video, taking your mouse and keys.
struct ScreenVideo: NSViewRepresentable {
    let model: AppModel
    let computerID: String
    let display: UInt32
    let session: Int
    @Binding var state: MediaStream.State
    @Binding var pixels: CGSize
    @Binding var locked: Bool

    func makeNSView(context: Context) -> ScreenNSView {
        let v = ScreenNSView(model: model, computerID: computerID, display: display)
        v.onState = { st in DispatchQueue.main.async { state = st } }
        v.onLocked = { on in DispatchQueue.main.async { locked = on } }
        v.onPixels = { px in DispatchQueue.main.async { pixels = px } }
        return v
    }

    func updateNSView(_ v: ScreenNSView, context: Context) {}

    static func dismantleNSView(_ v: ScreenNSView, coordinator: ()) { v.stopVideo() }
}

final class ScreenNSView: NSView {
    let model: AppModel
    let computerID: String
    let display: UInt32
    var onState: ((MediaStream.State) -> Void)?
    var onPixels: ((CGSize) -> Void)?
    var onLocked: ((Bool) -> Void)?
    private var stream: MediaStream?
    private var pixels = CGSize(width: 16, height: 10)
    private var pendingMove: [String: Any]?
    private var moveScheduled = false
    private var downKeys = Set<UInt16>()
    private var lastClick = (at: Date.distantPast, point: NSPoint.zero, count: 0)
    /// The other Mac's pointer shape (it isn't in the picture): this Mac's
    /// pointer takes it over the screen, so it's never behind your hand.
    private var remoteCursor: NSCursor?
    private var remoteShape: CGImage?

    init(model: AppModel, computerID: String, display: UInt32) {
        self.model = model
        self.computerID = computerID
        self.display = display
        super.init(frame: .zero)
        wantsLayer = true
        layer?.backgroundColor = NSColor.black.cgColor
    }

    required init?(coder: NSCoder) { fatalError() }

    override var isFlipped: Bool { true }
    override var acceptsFirstResponder: Bool { !viewOnly }
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if window != nil {
            startVideo()
            window?.makeFirstResponder(self)
        } else {
            stopVideo()
        }
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
        // About a window's size (more would only be scaled down here), so frames
        // are quick to encode and send while things move.
        guard stream == nil, let s = model.screenStream(computerID: computerID, display: display, maxPixels: 2560 * 1440) else { return }
        stream = s
        s.onSize = { [weak self] px in
            self?.pixels = px
            self?.onPixels?(px)
        }
        s.onState = { [weak self] st in self?.onState?(st) }
        s.onCursor = { [weak self] p in self?.pointerChanged(p) }
        s.onLocked = { [weak self] on in self?.onLocked?(on) }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        s.layer.videoGravity = .resizeAspect
        s.layer.frame = bounds
        layer?.addSublayer(s.layer)
        CATransaction.commit()
        s.start()
    }

    private func pointerChanged(_ p: ScreenPointer) {
        guard !viewOnly, let image = p.image, image !== remoteShape, p.size.width > 0 else { return }
        remoteShape = image
        remoteCursor = NSCursor(image: NSImage(cgImage: image, size: p.size), hotSpot: p.hotSpot)
        window?.invalidateCursorRects(for: self)
        if let w = window, bounds.contains(convert(w.mouseLocationOutsideOfEventStream, from: nil)) { remoteCursor?.set() }
    }

    override func resetCursorRects() {
        if let remoteCursor { addCursorRect(bounds, cursor: remoteCursor) }
    }

    func stopVideo() {
        stream?.onCursor = nil
        stream?.onState = nil
        stream?.onSize = nil
        stream?.stop()
        stream?.layer.removeFromSuperlayer()
        stream = nil
    }

    // MARK: Input

    /// This Mac's own screen (the dev build's test): watched only. Its
    /// pointer is yours already, and keys would come back to this window.
    var viewOnly: Bool { computerID.isEmpty }

    private func send(_ op: String, _ args: [String: Any]) {
        guard !viewOnly else { return }
        model.screenInput(computerID: computerID, display: display, op, args)
    }

    /// Where the screen is drawn (aspect-fit in the view).
    private var screenRect: CGRect {
        guard pixels.width > 0, pixels.height > 0, bounds.width > 0, bounds.height > 0 else { return bounds }
        let s = min(bounds.width / pixels.width, bounds.height / pixels.height)
        let w = pixels.width * s, h = pixels.height * s
        return CGRect(x: (bounds.width - w) / 2, y: (bounds.height - h) / 2, width: w, height: h)
    }

    /// An event's position as fractions of the screen (clamped to it).
    private func position(_ e: NSEvent) -> [String: Any] {
        let p = convert(e.locationInWindow, from: nil), r = screenRect
        let x = min(max((p.x - r.minX) / r.width, 0), 1), y = min(max((p.y - r.minY) / r.height, 0), 1)
        return ["x": x, "y": y]
    }

    private func mods(_ f: NSEvent.ModifierFlags) -> [String] {
        var m: [String] = []
        if f.contains(.shift) { m.append("shift") }
        if f.contains(.control) { m.append("ctrl") }
        if f.contains(.option) { m.append("alt") }
        if f.contains(.command) { m.append("meta") }
        if f.contains(.function) { m.append("fn") }
        return m
    }

    private func mouse(_ type: String, _ e: NSEvent, button: String) {
        flushMove()
        var a = position(e)
        a["type"] = type
        a["button"] = button
        a["clicks"] = e.clickCount
        a["mods"] = mods(e.modifierFlags)
        send("mouse", a)
    }

    /// Moves go at most once a display frame, the newest winning.
    private func queueMove(_ e: NSEvent, pressed: Bool) {
        var a = position(e)
        a["type"] = "move"
        a["pressed"] = pressed
        a["mods"] = mods(e.modifierFlags)
        pendingMove = a
        guard !moveScheduled else { return }
        moveScheduled = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0 / 120) { [weak self] in self?.flushMove() }
    }

    private func flushMove() {
        moveScheduled = false
        guard let a = pendingMove else { return }
        pendingMove = nil
        send("mouse", a)
    }

    override func mouseDown(with e: NSEvent) {
        window?.makeFirstResponder(self)
        mouse("down", e, button: "left")
    }
    override func mouseUp(with e: NSEvent) { mouse("up", e, button: "left") }
    override func mouseDragged(with e: NSEvent) { queueMove(e, pressed: true) }
    override func mouseMoved(with e: NSEvent) { queueMove(e, pressed: false) }
    override func rightMouseDown(with e: NSEvent) { mouse("down", e, button: "right") }
    override func rightMouseUp(with e: NSEvent) { mouse("up", e, button: "right") }
    override func rightMouseDragged(with e: NSEvent) { queueMove(e, pressed: true) }
    override func otherMouseDown(with e: NSEvent) { mouse("down", e, button: "other") }
    override func otherMouseUp(with e: NSEvent) { mouse("up", e, button: "other") }
    override func otherMouseDragged(with e: NSEvent) { queueMove(e, pressed: true) }

    override func scrollWheel(with e: NSEvent) {
        flushMove()
        var a = position(e)
        // Pixels on the other Mac: a line is about 10 there, as in its own wheel events.
        let k: CGFloat = e.hasPreciseScrollingDeltas ? 1 : 10
        a["dx"] = e.scrollingDeltaX * k
        a["dy"] = e.scrollingDeltaY * k
        a["mods"] = mods(e.modifierFlags)
        if e.momentumPhase != [] {
            a["phase"] = "momentum"
        } else if e.phase.contains(.began) {
            a["phase"] = "began"
        } else if e.phase.contains(.changed) {
            a["phase"] = "changed"
        } else if e.phase.contains(.ended) || e.phase.contains(.cancelled) {
            a["phase"] = "ended"
        }
        send("wheel", a)
    }

    // MARK: Keys

    private func key(_ type: String, _ e: NSEvent) {
        send("key", ["type": type, "vk": Int(e.keyCode), "mods": mods(e.modifierFlags)])
    }

    override func keyDown(with e: NSEvent) {
        key("down", e)
        if e.modifierFlags.contains(.command) {
            // macOS sends no keyUp for a key pressed with ⌘ held: up now.
            key("up", e)
        } else {
            downKeys.insert(e.keyCode)
        }
    }

    override func keyUp(with e: NSEvent) {
        guard downKeys.remove(e.keyCode) != nil else { return }
        key("up", e)
    }

    /// Shortcuts go to the other Mac while its screen is in front, as in
    /// Screen Sharing (close the window with End, or from the Window menu).
    override func performKeyEquivalent(with e: NSEvent) -> Bool {
        guard !viewOnly, window?.isKeyWindow == true, window?.firstResponder === self, e.type == .keyDown else { return super.performKeyEquivalent(with: e) }
        keyDown(with: e)
        return true
    }
}
