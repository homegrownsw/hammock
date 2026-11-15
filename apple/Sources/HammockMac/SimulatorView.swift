import AppKit
import HammockKit
import QuartzCore
import SwiftUI

// Simulator panes. The device runs in CoreSimulator; the simulator host
// ("Hammock Simulator", helpers/simulator/host.m), which the daemon starts,
// publishes its screen as a CAContext whose layer holds the framebuffer
// IOSurface. This file shows it through CALayerHost, scaled to fit, and
// sends the mouse and keyboard back as touches and typing.

/// The app's one connection to the simulator host. A pane can show in
/// two windows at once (DetachedWindows): what the host says about it
/// goes to each, and it's let go when the last one goes.
@MainActor
final class SimulatorHost {
    static let shared = SimulatorHost()

    private var fd: Int32 = -1
    private var readSource: DispatchSourceRead?
    private var inbuf = Data()
    private var views: [String: NSHashTable<SimulatorPaneNSView>] = [:]
    private var retrying = false

    private func shown(_ id: String) -> [SimulatorPaneNSView] { views[id]?.allObjects ?? [] }

    private var socketPath: String {
        ((DaemonConnection.defaultSocketPath as NSString).deletingLastPathComponent as NSString)
            .appendingPathComponent("simulator-host.sock")
    }

    func register(_ v: SimulatorPaneNSView) {
        let set = views[v.paneID] ?? .weakObjects()
        set.add(v)
        views[v.paneID] = set
        // Again for a second view: the host tells it the screen and state.
        if fd < 0 { connect() } else { send(["t": "attach", "id": v.paneID]) }
    }

    func unregister(_ v: SimulatorPaneNSView) {
        guard let set = views[v.paneID], set.contains(v) else { return }
        set.remove(v)
        guard set.allObjects.isEmpty else { return }
        views[v.paneID] = nil
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
        fd = s
        inbuf = Data()
        let src = DispatchSource.makeReadSource(fileDescriptor: s, queue: .main)
        src.setEventHandler { [weak self] in MainActor.assumeIsolated { self?.readable() } }
        src.resume()
        readSource = src
        for (id, set) in views where !set.allObjects.isEmpty { send(["t": "attach", "id": id]) }
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

    private func readable() {
        var buf = [UInt8](repeating: 0, count: 65536)
        let n = read(fd, &buf, buf.count)
        if n < 0, errno == EAGAIN || errno == EINTR { return }
        guard n > 0 else {
            readSource?.cancel()
            readSource = nil
            close(fd)
            fd = -1
            for set in views.values { for v in set.allObjects { v.hostLost() } }
            retry()
            return
        }
        inbuf.append(buf, count: n)
        while let nl = inbuf.firstIndex(of: 0x0A) {
            let line = inbuf[inbuf.startIndex..<nl]
            inbuf = Data(inbuf[(nl + 1)...])
            guard let m = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else { continue }
            if m["t"] as? String == "copyResult", let seq = (m["seq"] as? NSNumber)?.intValue {
                copies.removeValue(forKey: seq)?(m["text"] as? String ?? "")
            } else if let id = m["id"] as? String {
                for v in shown(id) { v.handle(m) }
            }
        }
    }

    private var copies: [Int: (String) -> Void] = [:]
    private var seq = 0

    /// Copies the device's selection (⌘C there), then hands back its pasteboard.
    func copy(_ paneID: String, done: @escaping (String) -> Void) {
        seq += 1
        copies[seq] = done
        send(["t": "copy", "id": paneID, "seq": seq])
    }

    /// Small messages (touches, keys): written straight out.
    func send(_ m: [String: Any]) {
        guard fd >= 0, var d = try? JSONSerialization.data(withJSONObject: m) else { return }
        d.append(0x0A)
        _ = d.withUnsafeBytes { write(fd, $0.baseAddress, $0.count) }
    }
}

/// Shows one simulator's screen, scaled to fit inside the device's frame,
/// and turns clicks and drags into touches and key presses into typing.
/// In a Group shared with you the device runs on its host's Mac: the screen
/// is video from there (MediaStream, relayed by your daemon) and input goes
/// through the daemon.
final class SimulatorPaneNSView: NSView {
    let paneID: String
    var onFocus: (() -> Void)?
    var isFocusTarget = false {
        didSet { if isFocusTarget, !oldValue { focusSoon() } }
    }
    /// Draw the device's frame (DeviceHub's artwork) around the screen.
    var showsFrame = true {
        didSet { if showsFrame != oldValue { needsLayout = true } }
    }
    /// An Apple TV: no touch screen; the keyboard is its remote's, and its
    /// frame is a TV's thin bezel.
    /// An Apple TV's remote is shown beside it: set when it's toggled, so the
    /// device animates to its new size (SimulatorPaneView fades the remote).
    var remoteShown = false {
        didSet { if remoteShown != oldValue, window != nil { animatesNextLayout = true; needsLayout = true } }
    }
    private var animatesNextLayout = false
    /// How long the TV and its remote take to make room for each other.
    static let remoteToggleDuration = 0.3

    var isTV = false {
        didSet {
            guard isTV != oldValue else { return }
            for (l, c) in [(rim, frameColours.rim), (band, frameColours.band)] { l.fillColor = c.cgColor }
            sizeFromVideo()
            needsLayout = true
        }
    }
    /// An Android emulator: its input goes through the daemon (which drives
    /// it), and its frame is drawn, as Xcode has no artwork for it.
    var isAndroid = false {
        didSet { if isAndroid != oldValue { sizeFromVideo(); needsLayout = true } }
    }
    private var frameColours: (rim: NSColor, band: NSColor) {
        isTV ? (NSColor(white: 0.24, alpha: 1), NSColor(white: 0.06, alpha: 1)) : (NSColor(white: 0.62, alpha: 1), NSColor(white: 0.17, alpha: 1))
    }

    private let device = CALayer()   // the whole device, in points, scaled to fit
    private let screen = CALayer()   // the device's screen: its shape, clipping
    // The frame: the screen's own outline (traced from the device's mask)
    // grown outward, so every band follows the display's curve exactly
    // (DeviceHub's nine-slice artwork has its own, rounder corners), in that
    // artwork's colours: a light rim, a dark band, then black glass.
    private let bezel = CALayer()
    private let rim = CAShapeLayer(), band = CAShapeLayer(), glass = CAShapeLayer()
    private let screenShape = CAShapeLayer()
    private var outline: CGPath? // the screen's shape, in its points (y down)
    private var buttons: [(name: String, layer: CALayer)] = []
    private var remote: CALayer?
    private var size = CGSize(width: 402, height: 874) // the screen, in points, from the host
    private var chrome: DeviceChrome?
    private var chromeDir = "", maskPath = ""
    private var host: SimulatorHost { .shared }
    /// Set for a pane in a Group shared with you.
    private let shared: AppModel?
    private var stream: MediaStream?
    private var video: CALayer?

    init(paneID: String, shared: AppModel? = nil) {
        self.paneID = paneID
        self.shared = shared
        super.init(frame: .zero)
        wantsLayer = true
        // The device may draw past the view while it animates to a smaller
        // place (an Apple TV making room for its remote, which it covers).
        clipsToBounds = false
        device.anchorPoint = .zero
        screen.masksToBounds = true
        screen.anchorPoint = .zero
        screen.isGeometryFlipped = true
        screen.backgroundColor = NSColor.black.cgColor
        bezel.anchorPoint = .zero
        for (l, c) in [(rim, NSColor(white: 0.62, alpha: 1)), (band, NSColor(white: 0.17, alpha: 1)), (glass, NSColor.black)] {
            l.anchorPoint = .zero
            l.fillColor = c.cgColor
            bezel.addSublayer(l)
        }
        screenShape.anchorPoint = .zero
        device.addSublayer(bezel)
        device.addSublayer(screen)
        layer?.addSublayer(device)
    }

    required init?(coder: NSCoder) { fatalError() }

    override var acceptsFirstResponder: Bool { true }
    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    func focusSoon() {
        DispatchQueue.main.async { [weak self] in
            guard let self, self.isFocusTarget, !PaletteState.shared.isOpen, let w = self.window, w.firstResponder !== self else { return }
            w.makeFirstResponder(self)
        }
    }

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if shared != nil {
            if window == nil { stopVideo() } else { startVideo() }
        } else if window == nil { host.unregister(self) } else { host.register(self) }
        if isFocusTarget { focusSoon() }
    }

    override func viewDidChangeBackingProperties() {
        super.viewDidChangeBackingProperties()
        loadArtwork()
    }

    func hostLost() {
        remote?.removeFromSuperlayer()
        remote = nil
    }

    func handle(_ m: [String: Any]) {
        switch m["t"] as? String {
        case "layer":
            if let ctx = (m["ctx"] as? NSNumber)?.uint32Value { show(context: ctx) }
        case "state":
            if let w = (m["w"] as? NSNumber)?.doubleValue, let h = (m["h"] as? NSNumber)?.doubleValue, w > 0, h > 0 {
                let turned = (w > h) != landscape
                size = CGSize(width: w, height: h)
                if turned { loadArtwork() } // rotated: the screen's outline turns with it
            }
            applyArtwork(dir: m["chrome"] as? String ?? "", mask: m["mask"] as? String ?? "")
            needsLayout = true
        default:
            break
        }
    }

    private func applyArtwork(dir: String, mask: String) {
        guard dir != chromeDir || mask != maskPath else { return }
        chromeDir = dir
        maskPath = mask
        chrome = DeviceChrome(dir)
        loadArtwork()
    }

    // MARK: Shared (video)
    /// A shared pane's frame: its host's artwork, if this Mac has the same
    /// (it's installed with Xcode); a plain screen otherwise.
    private func sharedArtwork() {
        guard let pane = shared?.panes[paneID] else { return }
        let fm = FileManager.default
        let dir = fm.fileExists(atPath: pane.deviceChrome + "/chrome.json") ? pane.deviceChrome : ""
        let mask = fm.fileExists(atPath: pane.deviceMask) ? pane.deviceMask : ""
        applyArtwork(dir: dir, mask: mask)
    }

    private func startVideo() {
        sharedArtwork()
        guard stream == nil, let model = shared, let s = model.videoStream(for: paneID) else {
            if stream == nil, shared != nil { retryVideo() }
            return
        }
        stream = s
        s.onSize = { [weak self] px in
            guard let self else { return }
            self.videoPixels = px
            self.sizeFromVideo()
        }
        s.onState = { [weak self, weak s] st in
            guard let self, let s, self.stream === s, case .failed = st else { return }
            self.stopVideo()
            self.retryVideo()
        }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        video?.removeFromSuperlayer()
        s.layer.anchorPoint = .zero
        s.layer.videoGravity = .resize
        screen.addSublayer(s.layer)
        video = s.layer
        CATransaction.commit()
        s.start()
        needsLayout = true
    }

    /// A shared pane's video, in pixels, as last sized.
    private var videoPixels: CGSize?

    /// The screen's size in points, from the video's pixels: as the simulator
    /// host decides, and again once the pane says it's an Apple TV (1920
    /// points wide: its scale is 2 at 4K, 1 at 1080p), which can come after
    /// the first frame.
    private func sizeFromVideo() {
        guard let px = videoPixels, px.width > 0 else { return }
        var scale: CGFloat = isTV ? max(px.width, px.height) / 1920 : px.width >= 1000 ? 3 : 2
        // The host says it (an emulator's is its density, which can't be guessed).
        if let s = (shared ?? model)?.panes[paneID]?.deviceScale, s > 0 { scale = s }
        size = CGSize(width: px.width / scale, height: px.height / scale)
        needsLayout = true
    }

    private func stopVideo() {
        stream?.onState = nil
        stream?.onSize = nil
        stream?.stop()
        stream = nil
        video?.removeFromSuperlayer()
        video = nil
    }

    private func retryVideo() {
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
            guard let self, self.window != nil, self.stream == nil else { return }
            self.startVideo()
        }
    }

    /// Input for a shared pane, through the daemon to its host; or an
    /// emulator's, which the daemon drives (the simulator host only shows it).
    private func remote(_ op: String, _ args: [String: Any] = [:]) {
        (shared ?? model)?.simulatorInput(paneID, op, args)
    }

    /// Input goes through the daemon: a shared pane's, or an emulator's.
    private var viaDaemon: Bool { shared != nil || isAndroid }

    private func show(context ctx: UInt32) {
        if let r = remote, (r.value(forKey: "contextId") as? NSNumber)?.uint32Value == ctx { return }
        guard let cls = NSClassFromString("CALayerHost") as? CALayer.Type else { return }
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        remote?.removeFromSuperlayer()
        let l = cls.init()
        l.setValue(NSNumber(value: ctx), forKey: "contextId")
        l.anchorPoint = .zero
        l.position = .zero
        screen.addSublayer(l)
        remote = l
        CATransaction.commit()
        needsLayout = true
    }

    // MARK: Frame

    /// Rasterises the frame's PDFs for this screen's scale.
    private func loadArtwork() {
        let px = (window?.backingScaleFactor ?? 2) * 1.5 // headroom for larger panes
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        for b in buttons { b.layer.removeFromSuperlayer() }
        buttons = []
        outline = nil
        if let chrome {
            for input in chrome.buttons {
                guard let img = DeviceChrome.image(input.image, scale: px) else { continue }
                let l = CALayer()
                l.anchorPoint = .zero
                l.contents = img
                l.contentsScale = px
                device.insertSublayer(l, below: bezel) // under the frame's edge, as on the device
                buttons.append((input.name, l))
            }
        }
        if !maskPath.isEmpty, let img = DeviceChrome.image(maskPath, scale: 2) {
            // The mask is the portrait screen; turned a quarter for landscape.
            let portrait = CGSize(width: min(size.width, size.height), height: max(size.width, size.height))
            outline = DeviceChrome.outline(of: img, size: portrait)
            if landscape, let o = outline {
                var turn = CGAffineTransform(translationX: size.width, y: 0).rotated(by: .pi / 2)
                outline = o.copy(using: &turn)
            }
        }
        CATransaction.commit()
        needsLayout = true
    }

    // MARK: Layout

    private var framed: Bool { showsFrame && (chrome != nil || isTV || isAndroid) }
    /// Turned on its side: the frame's side buttons (drawn for portrait) are hidden.
    private var landscape: Bool { size.width > size.height }
    /// The frame's width around the screen, in points: a TV's is a thin
    /// bezel (its artwork's 8 points would vanish at a pane's scale).
    private var edge: CGFloat { framed ? (isTV ? 22 : chrome?.edge ?? 14) : 0 }
    /// The screen's shape without a mask: a TV's square corners, rounded a
    /// little; else a continuous corner close to a modern iPhone's.
    private var plainOutline: CGPath {
        // An Android phone's: a little tighter than an iPhone's.
        let r = isTV ? 6 : size.width * (isAndroid ? 0.09 : 0.12)
        return CGPath(roundedRect: CGRect(origin: .zero, size: size), cornerWidth: r, cornerHeight: r, transform: nil)
    }
    /// Room beside the frame for the side buttons.
    private var sides: CGFloat { framed ? 10 : 0 }
    private var deviceSize: CGSize { CGSize(width: size.width + 2 * edge, height: size.height + 2 * edge) }

    /// The scale that fits the device (and its buttons) in the view, with a margin.
    private var fit: CGFloat {
        let avail = bounds.insetBy(dx: 16, dy: 16)
        guard avail.width > 0, avail.height > 0 else { return 0 }
        return min(avail.width / (deviceSize.width + 2 * sides), avail.height / deviceSize.height)
    }

    /// Where the device sits in the view.
    private var deviceRect: CGRect {
        let s = fit, d = deviceSize
        return CGRect(x: bounds.midX - d.width * s / 2, y: bounds.midY - d.height * s / 2, width: d.width * s, height: d.height * s)
    }

    /// Where the screen sits in the view.
    private var screenRect: CGRect {
        let s = fit
        return deviceRect.insetBy(dx: edge * s, dy: edge * s)
    }

    /// Told where the screen and the device are (in the view's flipped
    /// coordinates, as SwiftUI has them), for the agent's overlay
    /// (AgentPresenceOverlay).
    var onPlacement: ((SimulatorPlacement) -> Void)?
    private var reported: SimulatorPlacement?

    override func layout() {
        super.layout()
        let s = fit
        guard s > 0 else { return }
        // The screen's and the frame's real outlines (as the layers below
        // draw them), placed in the view: the screen's shape is in its own
        // points, y down, from its top left.
        let r = screenRect
        let screenTopLeft = CGPoint(x: r.minX, y: bounds.height - r.maxY)
        let outlineShape = outline ?? plainOutline
        var place = CGAffineTransform(translationX: screenTopLeft.x, y: screenTopLeft.y).scaledBy(x: s, y: s)
        let placed = SimulatorPlacement(screen: CGRect(origin: screenTopLeft, size: r.size),
                                        screenPath: outlineShape.copy(using: &place) ?? outlineShape,
                                        devicePath: DeviceChrome.grow(outlineShape, by: edge).copy(using: &place) ?? outlineShape)
        if placed != reported, let report = onPlacement {
            reported = placed
            DispatchQueue.main.async { report(placed) }
        }
        // The device moves and scales to its new place: animated when the
        // remote beside it was shown or hidden, else straight there.
        let animate = animatesNextLayout
        animatesNextLayout = false
        CATransaction.begin()
        if animate {
            CATransaction.setAnimationDuration(SimulatorPaneNSView.remoteToggleDuration)
            CATransaction.setAnimationTimingFunction(CAMediaTimingFunction(name: .easeInEaseOut))
        } else {
            CATransaction.setDisableActions(true)
        }
        let d = deviceSize
        device.bounds = CGRect(origin: .zero, size: d)
        device.anchorPoint = .zero
        device.position = deviceRect.origin
        device.transform = CATransform3DMakeScale(s, s, 1)
        CATransaction.commit()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        bezel.isHidden = !framed
        bezel.frame = CGRect(origin: .zero, size: d)
        let shape = outline ?? plainOutline
        for (l, inset) in isTV ? [(rim, 0.0), (band, 1.0), (glass, edge)] : [(rim, 0.0), (band, 1.5), (glass, 6.0)] {
            l.frame = CGRect(x: edge, y: edge, width: size.width, height: size.height)
            l.path = DeviceChrome.grow(shape, by: edge - inset)
        }
        screen.frame = CGRect(x: edge, y: edge, width: size.width, height: size.height)
        video?.frame = screen.bounds
        screenShape.frame = screen.bounds
        screenShape.path = shape
        screen.mask = screenShape
        if let chrome {
            for (i, b) in buttons.enumerated() {
                let input = chrome.buttons[i]
                b.layer.isHidden = !framed || landscape
                b.layer.frame = input.frame(in: d)
            }
        }
        CATransaction.commit()
    }

    // MARK: Input

    private func ratio(_ e: NSEvent) -> CGPoint {
        let p = convert(e.locationInWindow, from: nil)
        let r = screenRect
        return CGPoint(x: (p.x - r.minX) / r.width, y: (r.maxY - p.y) / r.height) // the device's y runs down
    }

    private func touch(_ phase: String, _ e: NSEvent) {
        let p = ratio(e)
        if viaDaemon {
            remote("touch", ["phase": phase, "x": p.x * size.width, "y": p.y * size.height]) // in points
            return
        }
        host.send(["t": "touch", "id": paneID, "phase": phase, "x": p.x, "y": p.y])
    }

    private var touching = false

    override func mouseDown(with e: NSEvent) {
        onFocus?()
        window?.makeFirstResponder(self)
        let p = convert(e.locationInWindow, from: nil)
        if let name = buttonAt(p) {
            if name == "power" { button("lock") }
            return
        }
        guard !isTV, screenRect.contains(p) else { return } // an Apple TV has no touch screen
        touching = true
        touch("down", e)
    }

    override func mouseDragged(with e: NSEvent) { if touching { touch("move", e) } }
    override func mouseUp(with e: NSEvent) {
        if touching { touch("up", e) }
        touching = false
    }

    // A trackpad pinch becomes two fingers on the device, played once it ends
    // (XCTest synthesizes whole gestures), around where it started.
    private var pinch: (center: CGPoint, start: TimeInterval, scale: CGFloat, samples: [(scale: CGFloat, t: Double)])?
    /// For pinches (through the daemon and the device's XCTest runner).
    var model: AppModel?

    override func magnify(with e: NSEvent) {
        guard !isTV else { return }
        switch e.phase {
        case .began:
            let p = ratio(e)
            guard (0...1).contains(p.x), (0...1).contains(p.y) else { return }
            pinch = (CGPoint(x: p.x * size.width, y: p.y * size.height), e.timestamp, 1, [(1, 0)])
        case .changed:
            guard var g = pinch else { return }
            g.scale = max(0.1, g.scale * (1 + e.magnification))
            g.samples.append((g.scale, (e.timestamp - g.start) * 1000))
            pinch = g
        case .ended, .cancelled:
            defer { pinch = nil }
            guard e.phase == .ended, let g = pinch, let model, abs(g.scale - 1) > 0.05 else { return }
            // Half the fingers' distance: at least 20pt apart, at most within the screen.
            let lo = g.samples.map(\.scale).min() ?? 1, hi = g.samples.map(\.scale).max() ?? 1
            let room = min(size.width, size.height) / 2 * 0.9
            let base = min(20 / lo, room / hi)
            // Played a little faster than it was made, but never in a blink.
            let span = max(g.samples.last?.t ?? 0, 1), length = min(max(span, 250), 800)
            let d = 1 / 2.squareRoot()
            let fingers = [-1.0, 1.0].map { side in
                g.samples.map { s in
                    let r = base * s.scale * side * d
                    return (x: Double(g.center.x + r), y: Double(g.center.y + r), t: s.t / span * length)
                }
            }
            model.simulatorGesture(paneID, paths: fingers)
        default:
            break
        }
    }

    /// The frame's side button under a point in the view, if any.
    private func buttonAt(_ p: CGPoint) -> String? {
        guard framed, !landscape, let chrome else { return nil }
        let s = fit, r = deviceRect
        let q = CGPoint(x: (p.x - r.minX) / s, y: (p.y - r.minY) / s)
        return chrome.buttons.first { $0.frame(in: deviceSize).insetBy(dx: -4, dy: 0).contains(q) }?.name
    }

    override func keyDown(with e: NSEvent) {
        let mods = e.modifierFlags.intersection(.deviceIndependentFlagsMask)
        if mods.contains(.command) {
            super.keyDown(with: e) // menus (Pane ▸ Home, Lock, Rotate…)
            return
        }
        // An Apple TV: the arrows, Return, Escape and Space are its remote's
        // buttons, sent as the remote sends them (Space as Play/Pause, not a
        // space), held while the key is. Other keys type, for its text fields.
        if isTV, let name = Self.remoteKeys[e.keyCode] {
            if !e.isARepeat {
                heldKeys.insert(e.keyCode)
                remotePress(name, "down")
            }
            return
        }
        // Keys without text: HID usages.
        let usages: [UInt16: UInt32] = [
            0x24: 40, 0x4C: 40, 0x35: 41, 0x33: 42, 0x30: 43, 0x75: 76,
            0x7C: 79, 0x7B: 80, 0x7D: 81, 0x7E: 82,
        ]
        if viaDaemon {
            // Through the daemon to its host: keys without text by usage, the rest as text.
            if let u = usages[e.keyCode] {
                remote("key", ["usage": u, "shift": mods.contains(.shift)])
            } else if let s = e.characters, !s.isEmpty {
                remote("text", ["text": s])
            }
            return
        }
        if let u = usages[e.keyCode] {
            host.send(["t": "key", "id": paneID, "usage": u, "shift": mods.contains(.shift)])
        } else if let s = e.characters, !s.isEmpty {
            host.send(["t": "text", "id": paneID, "text": s])
        }
    }

    /// The keys that are an Apple TV remote's buttons, by key code.
    private static let remoteKeys: [UInt16: String] = [
        0x7E: "up", 0x7D: "down", 0x7B: "left", 0x7C: "right",
        0x24: "select", 0x4C: "select", 0x35: "menu", 0x31: "play_pause",
    ]
    private var heldKeys: Set<UInt16> = []

    override func keyUp(with e: NSEvent) {
        guard isTV, heldKeys.remove(e.keyCode) != nil, let name = Self.remoteKeys[e.keyCode] else {
            super.keyUp(with: e)
            return
        }
        remotePress(name, "up")
    }

    override func resignFirstResponder() -> Bool {
        releaseKeys()
        return super.resignFirstResponder()
    }

    /// Lets go of the remote's buttons held by keys: a key let go elsewhere
    /// (another view, another app, or after the pane went) never reaches keyUp.
    private func releaseKeys() {
        for code in heldKeys { if let name = Self.remoteKeys[code] { remotePress(name, "up") } }
        heldKeys = []
    }

    private var resignObserver: NSObjectProtocol?

    override func viewWillMove(toWindow newWindow: NSWindow?) {
        super.viewWillMove(toWindow: newWindow)
        releaseKeys()
        if let o = resignObserver { NotificationCenter.default.removeObserver(o) }
        resignObserver = newWindow.map { w in
            NotificationCenter.default.addObserver(forName: NSWindow.didResignKeyNotification, object: w, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.releaseKeys() }
            }
        }
    }

    /// Half a press on an Apple TV's remote ("down" or "up"): straight to the
    /// simulator host, or for a shared pane through the daemon to its host.
    func remotePress(_ name: String, _ phase: String) {
        if viaDaemon {
            remote("remote", ["name": name, "phase": phase])
            return
        }
        host.send(["t": "remote", "id": paneID, "name": name, "phase": phase])
    }

    // Edit menu: through the device's pasteboard.
    @objc func paste(_ sender: Any?) {
        guard let s = NSPasteboard.general.string(forType: .string), !s.isEmpty else { return }
        if isAndroid {
            remote("paste", ["text": s]) // its clipboard, then Paste
            return
        }
        if shared != nil {
            remote("text", ["text": s])
            return
        }
        host.send(["t": "paste", "id": paneID, "text": s])
    }

    @objc func copy(_ sender: Any?) {
        if viaDaemon, let m = shared ?? model {
            // The device's pasteboard, from its host (an emulator's clipboard), onto yours.
            Task { [paneID] in
                guard let r = await m.simulatorCall(paneID, "copy"), let text = r.json["text"] as? String, !text.isEmpty else { return }
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(text, forType: .string)
            }
            return
        }
        host.copy(paneID) { text in
            guard !text.isEmpty else { return }
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
        }
    }

    @objc override func selectAll(_ sender: Any?) {
        if isAndroid { return } // no ⌘A on Android
        if shared != nil {
            remote("key", ["usage": 4, "cmd": true])
            return
        }
        host.send(["t": "key", "id": paneID, "usage": 4, "cmd": true])
    }

    func button(_ name: String) {
        if viaDaemon {
            remote("button", ["name": name])
            return
        }
        host.send(["t": "button", "id": paneID, "name": name])
    }
}

/// A device's frame: DeviceHub's artwork (a .devicechrome bundle), drawn as
/// nine slices around the screen, and the side buttons that sit under it.
struct DeviceChrome {
    struct Button {
        let name: String
        let image: String
        let size: CGSize
        let anchorRight: Bool
        let offset: CGPoint

        /// Where it sits in the device (points, y up): its offset is from the
        /// frame's top and from the side it's on, half tucked under the edge.
        func frame(in device: CGSize) -> CGRect {
            let x = anchorRight ? device.width + offset.x : offset.x - size.width
            return CGRect(x: x, y: device.height - offset.y - size.height, width: size.width, height: size.height)
        }
    }

    let edge: CGFloat     // the frame's width around the screen
    let buttons: [Button]

    init?(_ dir: String) {
        guard !dir.isEmpty,
              let data = FileManager.default.contents(atPath: (dir as NSString).appendingPathComponent("chrome.json")),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let images = json["images"] as? [String: Any]
        else { return nil }
        func pdf(_ n: String) -> String { (dir as NSString).appendingPathComponent(n + ".pdf") }
        edge = ((images["sizing"] as? [String: Any])?["leftWidth"] as? NSNumber).map { CGFloat($0.doubleValue) } ?? 18
        buttons = (json["inputs"] as? [[String: Any]] ?? []).compactMap { input in
            guard let name = input["name"] as? String, let image = input["image"] as? String,
                  let anchor = input["anchor"] as? String, anchor == "left" || anchor == "right",
                  let size = NSImage(contentsOfFile: pdf(image))?.size,
                  let normal = (input["offsets"] as? [String: Any])?["normal"] as? [String: Any],
                  let x = normal["x"] as? NSNumber, let y = normal["y"] as? NSNumber
            else { return nil }
            return Button(name: name, image: pdf(image), size: size, anchorRight: anchor == "right",
                          offset: CGPoint(x: x.doubleValue, y: y.doubleValue))
        }
    }

    /// The screen's outline, traced from its mask (a shape at the screen's
    /// aspect) and scaled to size: straight sides, and corners followed edge
    /// pixel by edge pixel, to a fraction of a pixel from their antialiasing.
    /// The four corners are the same curve mirrored.
    static func outline(of mask: CGImage, size: CGSize) -> CGPath? {
        let w = mask.width, h = mask.height
        guard w > 2, h > 2,
              let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w, space: CGColorSpaceCreateDeviceGray(),
                                  bitmapInfo: CGImageAlphaInfo.alphaOnly.rawValue)
        else { return nil }
        ctx.draw(mask, in: CGRect(x: 0, y: 0, width: w, height: h))
        guard let data = ctx.data?.assumingMemoryBound(to: UInt8.self) else { return nil }
        // Rows from the top of the bitmap (its memory runs top down).
        func alpha(_ x: Int, _ y: Int) -> CGFloat { CGFloat(data[y * w + x]) / 255 }
        // How far the corner reaches along the top edge.
        var reach = 0
        while reach < w / 2, alpha(reach, 0) < 0.5 { reach += 1 }
        guard reach > 1 else { return CGPath(rect: CGRect(origin: .zero, size: size), transform: nil) }
        // Where the edge crosses a row (or column): the corner's width less the coverage.
        func rowEdge(_ y: Int) -> CGFloat { CGFloat(reach) - (0..<reach).reduce(0) { $0 + alpha($1, y) } }
        func columnEdge(_ x: Int) -> CGFloat { CGFloat(reach) - (0..<reach).reduce(0) { $0 + alpha(x, $1) } }
        // Columns where the edge is flatter than 45°, rows where it's steeper.
        var diagonal = 0
        while diagonal < reach, alpha(diagonal, diagonal) < 0.5 { diagonal += 1 }
        var corner: [CGPoint] = [CGPoint(x: CGFloat(reach), y: 0)]
        for x in stride(from: reach - 1, through: diagonal, by: -1) { corner.append(CGPoint(x: CGFloat(x) + 0.5, y: columnEdge(x))) }
        for y in diagonal..<reach { corner.append(CGPoint(x: rowEdge(y), y: CGFloat(y) + 0.5)) }
        corner.append(CGPoint(x: 0, y: CGFloat(reach)))
        // To points.
        let kx = size.width / CGFloat(w), ky = size.height / CGFloat(h)
        let tl = corner.map { CGPoint(x: $0.x * kx, y: $0.y * ky) } // from the top edge round to the left edge
        let W = size.width, H = size.height
        let points = tl.map { CGPoint(x: W - $0.x, y: $0.y) } // across the top, round the top right
            + tl.reversed().map { CGPoint(x: W - $0.x, y: H - $0.y) } // down, round the bottom right
            + tl.map { CGPoint(x: $0.x, y: H - $0.y) } // across the bottom, round the bottom left
            + tl.reversed() // up, round the top left
        let path = CGMutablePath()
        path.move(to: tl[0])
        for p in points { path.addLine(to: p) }
        path.closeSubpath()
        return path
    }

    /// A shape grown outward by a distance all round (its outline swept by a
    /// circle), so a frame's bands keep the screen's curve.
    static func grow(_ shape: CGPath, by d: CGFloat) -> CGPath {
        guard d > 0 else { return shape }
        return shape.copy(strokingWithWidth: 2 * d, lineCap: .round, lineJoin: .round, miterLimit: 1).union(shape)
    }

    /// A PDF rasterised at a scale.
    static func image(_ path: String, scale: CGFloat) -> CGImage? {
        guard let img = NSImage(contentsOfFile: path) else { return nil }
        var r = CGRect(origin: .zero, size: CGSize(width: img.size.width * scale, height: img.size.height * scale))
        return img.cgImage(forProposedRect: &r, context: nil, hints: nil)
    }
}

struct SimulatorView: NSViewRepresentable {
    let paneID: String
    let model: AppModel
    var isFocused: Bool
    var showsFrame: Bool
    var isTV = false
    var isAndroid = false
    var remoteShown = false
    var view: SimulatorRef
    var onPlacement: ((SimulatorPlacement) -> Void)? = nil

    func makeNSView(context: Context) -> SimulatorPaneNSView {
        let isShared = model.group(ofPane: paneID)?.isRemote ?? false
        let v = SimulatorPaneNSView(paneID: paneID, shared: isShared ? model : nil)
        v.model = model
        v.onFocus = { [paneID, model] in model.viewFocused(paneID) }
        v.showsFrame = showsFrame
        v.isTV = isTV
        v.isAndroid = isAndroid
        v.remoteShown = remoteShown
        v.isFocusTarget = isFocused
        v.onPlacement = onPlacement
        view.view = v
        return v
    }

    func updateNSView(_ v: SimulatorPaneNSView, context: Context) {
        v.isFocusTarget = isFocused
        v.showsFrame = showsFrame
        v.isTV = isTV
        v.isAndroid = isAndroid
        v.remoteShown = remoteShown
        v.onPlacement = onPlacement
    }
}

/// Where a simulator pane draws the device and its screen, and their
/// shapes, in SwiftUI's coordinates.
struct SimulatorPlacement: Equatable {
    var screen: CGRect
    var screenPath: CGPath // the screen's shape
    var devicePath: CGPath // the frame's outline (the screen's, without one)
}

/// Lets the pane's toolbar reach its view (for the hardware buttons).
final class SimulatorRef {
    weak var view: SimulatorPaneNSView?
}

/// A simulator pane: the device's name and hardware buttons over its screen.
struct SimulatorPaneView: View {
    let model: AppModel
    let paneID: String
    let isFocused: Bool
    @State private var ref = SimulatorRef()
    @State private var placement: SimulatorPlacement? // where the device and its screen are, for the agent's overlay
    private var showsFrame: Bool { Prefs.shared.bool(.simulatorFrame) }
    private var showsRemote: Bool { Prefs.shared.bool(.simulatorRemote) }

    private var pane: Pane? { model.panes[paneID] }
    private var isTV: Bool { model.isTVSimulator(paneID) }
    private var isAndroid: Bool { model.isAndroid(paneID) }
    private var deviceSymbol: String {
        model.isAppleTV(paneID) ? "appletv" : isTV ? "tv" : isAndroid ? "smartphone" : "iphone"
    }
    /// The remote's own symbol: a Siri Remote or an Android TV's.
    private func remoteSymbol(_ shown: Bool) -> String {
        model.isAppleTV(paneID) ? (shown ? "appletvremote.gen4.fill" : "appletvremote.gen4") : (shown ? "av.remote.fill" : "av.remote")
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 6) {
                PaneKindMenu(model: model, paneID: paneID) {
                    Label(pane?.title ?? "Simulator", systemImage: deviceSymbol).font(.callout.weight(.medium))
                }
                if let note = pane?.deviceNote, !note.isEmpty {
                    Text(note).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                } else if pane?.deviceBooted == false {
                    Text("Booting…").font(.caption).foregroundStyle(.secondary)
                    ProgressView().controlSize(.mini)
                } else if model.simulatorsBusy.contains(paneID) {
                    ProgressView().controlSize(.mini).tooltip("Waiting for the simulator (the first rotation or pinch starts its XCTest runner)")
                }
                Spacer()
                if isTV {
                    button(remoteSymbol(showsRemote), Keys.hint(showsRemote ? "Hide Remote" : "Show Remote", "sim_remote_toggle")) {
                        Prefs.shared.toggle(.simulatorRemote)
                    }
                } else if isAndroid {
                    button("chevron.backward", Keys.hint("Back", "sim_back")) { model.pressSimulatorButton(paneID, "back") }
                    button("circle", Keys.hint("Home", "sim_home")) { model.pressSimulatorButton(paneID, "home") }
                    button("square", Keys.hint("Recents", "sim_recents")) { model.pressSimulatorButton(paneID, "recents") }
                    button("rotate.left", Keys.hint("Rotate Left", "sim_rotate_left")) { model.rotateSimulator(paneID, "left") }
                    button("rotate.right", Keys.hint("Rotate Right", "sim_rotate_right")) { model.rotateSimulator(paneID, "right") }
                } else {
                    button("house", Keys.hint("Home", "sim_home")) { ref.view?.button("home") }
                    button("lock", Keys.hint("Lock", "sim_lock")) { ref.view?.button("lock") }
                    button("rotate.left", Keys.hint("Rotate Left", "sim_rotate_left")) { model.rotateSimulator(paneID, "left") }
                    button("rotate.right", Keys.hint("Rotate Right", "sim_rotate_right")) { model.rotateSimulator(paneID, "right") }
                }
                if pane?.deviceFoldable == true {
                    let open = (pane?.deviceHinge ?? 0) > 120
                    button(open ? "book.closed" : "book", Keys.hint(open ? "Close Phone" : "Open Phone", "sim_fold")) { model.toggleFold(paneID) }
                }
                button("camera", Keys.hint("Screenshot to File", "sim_screenshot_file")) { SimulatorShot.saveToFile(model, paneID) }
                button("doc.on.clipboard", Keys.hint("Screenshot to Clipboard", "sim_screenshot_copy")) { SimulatorShot.copyToClipboard(model, paneID) }
                button("power", "Shut Down Simulator (closes the pane)") { model.shutDownSimulator(paneID) }
                button(isTV ? (showsFrame ? "tv" : "rectangle") : isAndroid ? (showsFrame ? "smartphone" : "rectangle.portrait") : (showsFrame ? "iphone.gen3" : "rectangle.portrait"),
                       showsFrame ? "Hide Device Frame" : "Show Device Frame") { Prefs.shared.toggle(.simulatorFrame) }
                ZoomButton(model: model, paneID: paneID)
                PaneCloseButton(model: model, paneID: paneID)
            }
            .padding(.leading, 15) // lined up with the window title, as the terminal's header
            .padding(.trailing, 10)
            .frame(height: 28)
            .background(isFocused ? Color.themeAccent.opacity(0.06) : Color.clear)
            .contextMenu { PaneMenuItems(model: model, paneID: paneID) }
            .paneSwapHandle(paneID, model: model)
            GeometryReader { geo in
                // An Apple TV's remote goes beside it, or under it in a tall pane.
                let wide = geo.size.width > geo.size.height * 1.15
                let layout = wide ? AnyLayout(HStackLayout(spacing: 0)) : AnyLayout(VStackLayout(spacing: 0))
                layout {
                    // Over the remote: as the TV scales down to make room, it
                    // uncovers the remote fading in behind it, and covers it again
                    // as it grows back.
                    screen.zIndex(1)
                    if isTV && showsRemote {
                        Group {
                            if model.isAppleTV(paneID) {
                                SiriRemote { name, phase in press(name, phase) }
                            } else {
                                GoogleTVRemote { name, phase in press(name, phase) }
                            }
                        }
                        .padding(wide ? EdgeInsets(top: 20, leading: 0, bottom: 20, trailing: 24) : EdgeInsets(top: 0, leading: 20, bottom: 16, trailing: 20))
                        .frame(maxWidth: wide ? min(150, geo.size.width * 0.22) : .infinity,
                               maxHeight: wide ? .infinity : min(380, geo.size.height * 0.45))
                        // Fades while the TV scales to make room (SimulatorPaneNSView.layout).
                        .transition(.opacity)
                    }
                }
                .animation(.easeInOut(duration: SimulatorPaneNSView.remoteToggleDuration), value: isTV && showsRemote)
            }
            .background(Color(nsColor: .underPageBackgroundColor))
        }
    }

    private var screen: some View {
        SimulatorView(paneID: paneID, model: model, isFocused: isFocused, showsFrame: showsFrame, isTV: isTV, isAndroid: isAndroid, remoteShown: isTV && showsRemote, view: ref,
                      onPlacement: { placement = $0 })
            .overlay {
                if let p = placement, !p.screen.isEmpty {
                    AgentPresenceOverlay(model: model, paneID: paneID, isSimulator: true,
                                         screen: (p.screen, Path(p.screenPath)), device: Path(p.devicePath))
                }
            }
            .overlay {
                if pane?.deviceElsewhere == true {
                    // Android Studio runs it, where Hammock can't drive it: offer to take it over.
                    VStack(spacing: 10) {
                        Image(systemName: "smartphone").font(.system(size: 28)).foregroundStyle(.secondary)
                        Text("\(pane?.title ?? "This emulator") is running in Android Studio").font(.headline)
                        Button("Restart in Hammock…") { EmulatorTakeover.confirm(model, paneID) }
                    }
                    .padding(24)
                }
            }
    }

    /// A press on the remote beside a TV.
    private func press(_ name: String, _ phase: String) {
        model.viewFocused(paneID)
        ref.view?.remotePress(name, phase)
    }

    private func button(_ symbol: String, _ help: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .font(.system(size: 12, weight: .medium))
                .frame(width: 24, height: 22)
                .contentShape(Rectangle())
        }
        .buttonStyle(.borderless)
        .tooltip(help)
    }
}
