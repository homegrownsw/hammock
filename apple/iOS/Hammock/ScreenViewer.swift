import HammockKit
import SwiftUI
import UIKit

// Screen sharing on the phone (ADR-0014): a Mac's whole screen, to approve a
// prompt or do something only its UI does while you're away, best in
// landscape. (Face ID, if you want it, guards the whole app: AppLock.swift.) Trackpad mode by default (precise on
// small targets: drag moves the pointer, tap clicks), Touch mode to tap
// where you mean; pinch zooms either way.

/// Which screen is up (the Devices tab's Show Screen).
struct ScreenRequest: Identifiable {
    let computerID: String
    let name: String
    var id: String { computerID }
}

/// The whole-screen viewer.
struct PhoneScreenViewer: View {
    let model: AppModel
    let request: ScreenRequest
    @Environment(\.dismiss) private var dismiss
    @State private var displays: [Hammock_V1_Display] = []
    @State private var display: UInt32 = 0
    @State private var trackpad = true
    @State private var state: MediaStream.State = .connecting
    @State private var unavailable: String?
    @State private var session = 0
    @State private var keyboard = false
    @State private var hint = true
    @State private var locked = false
    @Environment(\.scenePhase) private var phase
    @State private var grace = BackgroundGrace()
    /// When the app last came back to the front: a stream that ends just
    /// after (it died while the app was away) starts again by itself.
    @State private var returnedAt = Date.distantPast
    @State private var resume = false
    /// How much of the width is beside a fold running down the screen, to
    /// its trailing side (iPhone Duo open): the controls stay there, out of
    /// the curve when it's half open, and there already when it's flat, so
    /// nothing moves as it bends. Nil without such a fold.
    @State private var besideFold: CGFloat?

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let unavailable {
                ContentUnavailableView("Can’t Share \(request.name)’s Screen", systemImage: "rectangle.on.rectangle.slash", description: Text(unavailable))
                    .foregroundStyle(.white)
            } else {
                ScreenTouch(model: model, computerID: request.computerID, display: display, trackpad: trackpad,
                            macWidth: CGFloat((displays.first { display == 0 ? $0.main : $0.id == display } ?? displays.first)?.width ?? 0),
                            keyboard: $keyboard, state: $state, locked: $locked, onEnd: { dismiss() })
                    .id("\(display)-\(session)")
                    .ignoresSafeArea()
            }
            if case .failed(let why) = state, unavailable == nil {
                VStack(spacing: 10) {
                    Text("The screen stopped").font(.headline)
                    Text(why).font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    Button("Try Again") { state = .connecting; session += 1 }
                        .prominentButton()
                }
                .padding(20)
                .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16))
                .padding()
            }
        }
        .onGeometryChange(for: CGFloat?.self) { proxy in
            guard #available(iOS 27.1, *) else { return nil }
            // Inactive too: there whether it's flat or half open.
            let folds = proxy.reservedRegions(kind: .division, options: .includeInactive)
            guard let fold = folds.first(where: { $0.frame.height > $0.frame.width }) else { return nil }
            let width = proxy.size.width - fold.frame.maxX
            return width > 0 ? width : nil
        } action: { besideFold = $0 }
        .overlay(alignment: besideFold == nil ? .top : .topTrailing) {
            if locked, state == .playing, !keyboard {
                // At the lock screen: type the password (macOS lets the
                // keys through to its field).
                HStack(spacing: 12) {
                    Image(systemName: "lock.fill")
                    Text("\(request.name) is locked. Tap the password field and type your password.")
                        .font(.footnote)
                    Button { keyboard = true } label: { Label("Keyboard", systemImage: "keyboard") }
                        .buttonStyle(.borderedProminent)
                        .tint(.white)
                        .foregroundStyle(.black)
                }
                .foregroundStyle(.white)
                .padding(.leading, 16).padding(.trailing, 6).padding(.vertical, 6)
                .background(.black.opacity(0.78), in: RoundedRectangle(cornerRadius: 22, style: .continuous))
                .padding(.top, 8)
                .padding(.horizontal, 12)
                .frame(maxWidth: besideFold ?? .infinity)
                .accessibilityElement(children: .contain)
            } else if hint, state == .playing, trackpad {
                Text("Drag to move · tap to click · two fingers to scroll or right-click · pinch to zoom")
                    .font(.footnote)
                    .foregroundStyle(.white)
                    .padding(.horizontal, 14).padding(.vertical, 7)
                    .multilineTextAlignment(.center)
                    .background(.black.opacity(0.7), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                    .padding(.top, 8)
                    .padding(.horizontal, 12)
                    .frame(maxWidth: besideFold ?? .infinity)
                    .transition(.opacity)
                    .task {
                        try? await Task.sleep(for: .seconds(5))
                        withAnimation { hint = false }
                    }
            }
        }
        .overlay(alignment: besideFold == nil ? .bottom : .bottomTrailing) {
            if !keyboard {
                // Tighter when that's all there's room for (iPhone Duo
                // closed, beside its fold).
                ViewThatFits(in: .horizontal) {
                    controls(tight: false)
                    controls(tight: true)
                }
                .padding(.bottom, 8)
                .padding(.horizontal, 12)
                .frame(maxWidth: besideFold ?? .infinity)
            }
        }
        .statusBarHidden()
        .persistentSystemOverlays(.hidden)
        .task { await load() }
        // Leaving the app for a moment (a code from Messages, another app's
        // password) keeps the screen: iOS gives the app half a minute or so
        // before it suspends it. Away longer, it picks up again on return.
        .onChange(of: phase) { _, p in
            switch p {
            case .background: grace.begin()
            case .active:
                grace.end()
                returnedAt = .now
                if case .failed = state { resume = true }
            default: break
            }
        }
        .onChange(of: state) { _, st in
            if case .failed = st, phase != .active || Date.now.timeIntervalSince(returnedAt) < 15 { resume = true }
        }
        // Once the Mac's connection is back (it dropped too, if the app was
        // suspended): a new stream, not "The screen stopped".
        .task(id: resume && phase == .active && model.connected) {
            guard resume, phase == .active, model.connected else { return }
            resume = false
            state = .connecting
            session += 1
        }
        .onDisappear { grace.end() }
    }

    private func controls(tight: Bool) -> some View {
        HStack(spacing: tight ? 0 : 4) {
            HStack(spacing: 2) {
                modeButton("Trackpad", on: trackpad, tight: tight) { trackpad = true }
                modeButton("Touch", on: !trackpad, tight: tight) { trackpad = false }
            }
            .padding(2)
            .background(.white.opacity(0.14), in: Capsule())
            Button { keyboard = true } label: { Image(systemName: "keyboard").frame(width: 44, height: 44) }
                .accessibilityLabel("Keyboard")
            if displays.count > 1 {
                Menu {
                    Picker("Display", selection: $display) {
                        ForEach(displays, id: \.id) { d in Text(d.name).tag(d.main ? UInt32(0) : d.id) }
                    }
                } label: { Image(systemName: "display").frame(width: 44, height: 44) }
                    .accessibilityLabel("Display")
            }
            Button { paste() } label: { Image(systemName: "doc.on.clipboard").frame(width: 44, height: 44) }
                .accessibilityLabel("Paste to the Mac")
            // The state, in words when there's room (else just its colour).
            stateChip(words: !tight)
            Button(role: .destructive) { dismiss() } label: {
                Image(systemName: "xmark").fontWeight(.semibold).frame(width: 20, height: 20)
            }
            .buttonStyle(.borderedProminent)
            .buttonBorderShape(.circle)
            .tint(.red)
            .accessibilityLabel("End screen sharing")
        }
        .foregroundStyle(.white)
        .padding(6)
        .background(.black.opacity(0.72), in: Capsule())
        .overlay(Capsule().strokeBorder(.white.opacity(0.18)))
    }

    private func stateChip(words: Bool) -> some View {
        HStack(spacing: 6) {
            Circle().fill(state == .playing ? Color.green : .orange).frame(width: 8, height: 8)
            if words { Text(state == .playing ? "Live" : "Connecting").font(.footnote).lineLimit(1).fixedSize() }
        }
        .padding(.horizontal, words ? 8 : 6)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(state == .playing ? "Live" : "Connecting")
    }

    private func modeButton(_ title: String, on: Bool, tight: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title)
                .font(.subheadline.weight(on ? .semibold : .regular))
                .foregroundStyle(on ? Color.black : Color.white)
                .lineLimit(1)
                .fixedSize()
                .padding(.horizontal, tight ? 10 : 14)
                .frame(minHeight: 40)
                .background(on ? Color.white : Color.clear, in: Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    private func paste() {
        guard let s = UIPasteboard.general.string, !s.isEmpty else { return }
        model.screenInput(computerID: request.computerID, display: display, "text", ["text": s])
    }

    private func load() async {
        guard let r = await model.listDisplays(computerID: request.computerID) else {
            unavailable = "\(request.name) didn’t answer."
            return
        }
        displays = r.displays
        unavailable = r.unavailable.isEmpty ? nil : r.unavailable
    }
}

/// Background time asked of iOS while the screen's up and the app's away,
/// so the stream (and the connection under it) outlives a quick switch.
@MainActor
final class BackgroundGrace {
    private var task = UIBackgroundTaskIdentifier.invalid

    func begin() {
        guard task == .invalid else { return }
        task = UIApplication.shared.beginBackgroundTask(withName: "Screen sharing") { [weak self] in
            // Out of time: the app suspends, and the stream with it.
            MainActor.assumeIsolated { self?.end() }
        }
    }

    func end() {
        guard task != .invalid else { return }
        UIApplication.shared.endBackgroundTask(task)
        task = .invalid
    }
}

/// The video, taking touches as a trackpad or directly, and the keyboard.
struct ScreenTouch: UIViewRepresentable {
    /// The floating bar's height with its margin: kept clear at the bottom.
    static let barHeight: CGFloat = 76
    let model: AppModel
    let computerID: String
    let display: UInt32
    let trackpad: Bool
    let macWidth: CGFloat // the display's width in points (scrolling's scale)
    @Binding var keyboard: Bool
    @Binding var state: MediaStream.State
    @Binding var locked: Bool
    let onEnd: () -> Void

    func makeUIView(context: Context) -> ScreenTouchView {
        let v = ScreenTouchView()
        v.send = { [model, computerID, display] op, args in model.screenInput(computerID: computerID, display: display, op, args) }
        v.onState = { st in DispatchQueue.main.async { state = st } }
        v.onKeyboard = { on in DispatchQueue.main.async { keyboard = on } }
        v.onLocked = { on in locked = on }
        v.onEnd = { DispatchQueue.main.async { onEnd() } }
        // Two layers. Detail: about this screen's size; zoomed out that's
        // all it can show, zoomed in the Mac sends just the part shown
        // (SetMediaView), cut from the display at full resolution. Small
        // frames, quick to encode and send while things move. Base: the
        // whole display, small and cheap, always live underneath, so a pan
        // shows what's there at once while the detail catches up.
        if let detail = model.screenStream(computerID: computerID, display: display, maxPixels: 3072 * 1728),
           // Background only: 15 frames a second leaves the encoder to the detail.
           let base = model.screenStream(computerID: computerID, display: display, maxPixels: 1280 * 720, maxFPS: 15, background: true) {
            v.start(detail, base: base)
        }
        return v
    }

    func updateUIView(_ v: ScreenTouchView, context: Context) {
        v.trackpad = trackpad
        v.barInset = ScreenTouch.barHeight
        v.macWidth = macWidth
        // After this update: taking or giving up first responder tells
        // SwiftUI (onKeyboard), and doing that inside its update is a cycle
        // it kills the app for.
        if keyboard != v.isFirstResponder {
            DispatchQueue.main.async {
                if keyboard, !v.isFirstResponder { v.becomeFirstResponder() }
                if !keyboard, v.isFirstResponder { v.resignFirstResponder() }
            }
        }
    }

    static func dismantleUIView(_ v: ScreenTouchView, coordinator: ()) { v.stop() }
}

final class ScreenTouchView: UIView, UIGestureRecognizerDelegate, UITextInput {
    var send: ((String, [String: Any]) -> Void)?
    var onState: ((MediaStream.State) -> Void)?
    var onKeyboard: ((Bool) -> Void)?
    var onEnd: (() -> Void)?
    var onLocked: ((Bool) -> Void)?
    /// The Mac's at its lock screen: typing goes as keys (its password field
    /// may take only those), not as text.
    private var macLocked = false
    var macWidth: CGFloat = 0
    var trackpad = true {
        didSet { if trackpad != oldValue { placeCursor() } }
    }

    /// The whole display, zoomed and panned; the video sits in it where its
    /// frames say they're from (all of it, or the part zoomed into).
    private let page = UIView()
    /// The detail: pictures decoded by its stream, each shown with its place
    /// in one screen update (the base is a plain video layer).
    private let video = UIView()
    private var shown: [CVPixelBuffer] = [] // the picture up, and the one before (not reused while shown)
    private let baseVideo = MediaView.LayerView()
    private var baseStream: MediaStream?
    private var crop = CGRect(x: 0, y: 0, width: 1, height: 1)
    private var sentView = CGRect.null
    private var sentAspect: CGFloat = 0
    private var detailPixels = CGSize.zero // the detail's frames' size: how much it can spare round the view
    private var viewPending = false
    /// Fingers are down (panning, pinching): the detail isn't asked to move
    /// unless the view leaves what it has, so it glides with the page and
    /// the base fills in; it's asked once they lift.
    /// The view lately (fractions of the display) and when, for its velocity.
    private var lastView: (CGRect, CFTimeInterval)?
    private var viewVelocity = CGPoint.zero // fractions of the display a second
    private var lastAsked: CFTimeInterval = 0
    private var touching = 0 {
        didSet { if touching == 0, oldValue > 0 { viewChanged() } }
    }
    /// The pointer: the Mac's own shape (it isn't in the picture), drawn
    /// here at once. Where this view's input put it while that's recent,
    /// else where the Mac says it is (someone at the Mac, or Touch mode).
    private let cursorView = PointerView()
    private var pointer: ScreenPointer?
    private var lastLocalInput: CFTimeInterval = 0
    private var stream: MediaStream?
    private var pixels = CGSize.zero
    private var zoom: CGFloat = 1
    private var pan = CGPoint.zero      // the zoomed page's offset from its fitted place
    private var cursor = CGPoint(x: 0.5, y: 0.5) // fractions of the screen
    private var dragging = false        // a button held (trackpad: press and hold, then drag)
    private var lastTap = (at: Date.distantPast, count: 0)
    private var pendingMove = false
    private var holdAt = CGPoint.zero // where a held drag's finger last was
    private weak var movePan: UIPanGestureRecognizer?
    private var momentum: CADisplayLink?
    private var velocity = CGPoint.zero
    private var scrollAt = CGPoint.zero
    private var scrolling = false
    /// What two fingers are doing: settled once they've moved enough to
    /// tell (apart or together: zoom; the same way: scroll), then kept till
    /// they lift, so a scroll doesn't turn into a zoom halfway (two fingers
    /// never keep quite the same distance apart) or a zoom into a scroll.
    private enum TwoFingers { case undecided, scroll, zoom }
    private var twoFingerMode = TwoFingers.undecided
    private var twoFingerStart: (centre: CGPoint, spread: CGFloat)?
    private var twoFingerGestures = 0 // the pan and the pinch, while they're going

    override init(frame: CGRect) {
        super.init(frame: frame)
        backgroundColor = .black
        clipsToBounds = true
        addSubview(page)
        page.addSubview(baseVideo)
        page.addSubview(video)
        page.backgroundColor = UIColor(white: 0.08, alpha: 1)
        page.clipsToBounds = true
        addSubview(cursorView)
        keyboardBacking.isUserInteractionEnabled = false
        keyboardBacking.alpha = 0
        addSubview(keyboardBacking)
        cursorView.isUserInteractionEnabled = false

        let move = UIPanGestureRecognizer(target: self, action: #selector(oneFinger(_:)))
        move.maximumNumberOfTouches = 1
        movePan = move
        let two = UIPanGestureRecognizer(target: self, action: #selector(twoFingers(_:)))
        two.minimumNumberOfTouches = 2
        two.maximumNumberOfTouches = 2
        let pinch = UIPinchGestureRecognizer(target: self, action: #selector(pinch(_:)))
        let tap = UITapGestureRecognizer(target: self, action: #selector(tap(_:)))
        let twoTap = UITapGestureRecognizer(target: self, action: #selector(twoFingerTap(_:)))
        twoTap.numberOfTouchesRequired = 2
        let hold = UILongPressGestureRecognizer(target: self, action: #selector(hold(_:)))
        hold.minimumPressDuration = 0.35
        hold.allowableMovement = 12
        // A held drag can go on as long as the finger's down.
        hold.cancelsTouchesInView = false
        for g in [move, two, pinch, tap, twoTap, hold] as [UIGestureRecognizer] {
            g.delegate = self
            addGestureRecognizer(g)
        }
        tap.require(toFail: hold)
        NotificationCenter.default.addObserver(self, selector: #selector(keyboardChanged(_:)), name: UIResponder.keyboardWillChangeFrameNotification, object: nil)
        NotificationCenter.default.addObserver(self, selector: #selector(keyboardChanged(_:)), name: UIResponder.keyboardWillHideNotification, object: nil)
    }

    required init?(coder: NSCoder) { fatalError() }

    /// A held drag moves the pointer itself (hold), so the one-finger pan
    /// doesn't start then: it would compete with the hold, and cancelled the
    /// drag (a mouse-up straight after the down).
    override func gestureRecognizerShouldBegin(_ g: UIGestureRecognizer) -> Bool {
        if g === movePan, dragging { return false }
        return super.gestureRecognizerShouldBegin(g)
    }

    func gestureRecognizer(_ g: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool {
        // Two fingers both pinch and pan (zoom while moving the view).
        (g is UIPinchGestureRecognizer && other is UIPanGestureRecognizer) || (g is UIPanGestureRecognizer && other is UIPinchGestureRecognizer)
            // Held, then dragged: the hold carries the drag.
            || (g is UILongPressGestureRecognizer && other is UIPanGestureRecognizer)
    }

    // MARK: Video

    func start(_ s: MediaStream, base: MediaStream) {
        baseStream = base
        baseVideo.hosted = base.layer
        base.layer.videoGravity = .resize
        // The display's shape, from the base: the detail's frames come in
        // this screen's shape (SetMediaView's aspect), whatever they show.
        base.onSize = { [weak self] px in
            guard let self, px != self.pixels else { return }
            self.pixels = px
            self.setNeedsLayout()
        }
        base.start()
        stream = s
        video.isUserInteractionEnabled = false
        video.layer.contentsGravity = .resize // the picture is exactly the part it shows
        s.onCursor = { [weak self] p in self?.pointerMoved(p) }
        s.onLocked = { [weak self] on in
            self?.macLocked = on
            self?.onLocked?(on)
        }
        s.onPicture = { [weak self] picture, c in
            guard let self else { return }
            CATransaction.begin()
            CATransaction.setDisableActions(true)
            if c != self.crop {
                self.crop = c
                self.placeVideo()
            }
            self.video.layer.contents = CVPixelBufferGetIOSurface(picture)?.takeUnretainedValue()
            CATransaction.commit()
            self.shown.append(picture)
            if self.shown.count > 2 { self.shown.removeFirst() }
        }
        s.onSize = { [weak self] px in self?.detailPixels = px }
        s.onState = { [weak self] st in self?.onState?(st) }
        s.start()
    }

    func stop() {
        stopMomentum()
        stream?.onState = nil
        baseStream?.onSize = nil
        stream?.onSize = nil
        stream?.onPicture = nil
        stream?.onCursor = nil
        stream?.onLocked = nil
        stream?.stop()
        video.layer.contents = nil
        shown.removeAll()
        stream = nil
        baseStream?.stop()
        baseVideo.hosted = nil
        baseStream = nil
    }

    override func willMove(toWindow w: UIWindow?) {
        if w == nil { stopMomentum() }
    }

    // MARK: Layout: the page fitted, zoomed and panned

    /// What's covered at the bottom: the floating bar (always reserved, so
    /// zoomed out everything fits above it), and the keyboard while it's up
    /// (the screen slides up above it, at the same zoom).
    var barInset: CGFloat = 0 { didSet { if barInset != oldValue { setNeedsLayout() } } }
    private var keyboardInset: CGFloat = 0

    /// Where the screen is fitted (above the bar), and where it can be seen
    /// now (above the bar, or the keyboard).
    /// The bar's height, and the home indicator's area under it (this view
    /// goes to the screen's edges).
    private var barCovers: CGFloat { barInset + (window?.safeAreaInsets.bottom ?? 0) }
    /// Clear of the Dynamic Island or notch, and the rounded corners' safe
    /// area at the sides (this view goes to the screen's edges).
    private var safe: UIEdgeInsets { window?.safeAreaInsets ?? .zero }
    private func area(bottom: CGFloat) -> CGRect {
        let s = safe
        return CGRect(x: s.left, y: s.top, width: max(bounds.width - s.left - s.right, 1), height: max(bounds.height - s.top - bottom, 1))
    }
    private var fitArea: CGRect { area(bottom: barCovers) }
    private var seenArea: CGRect { area(bottom: max(barCovers, keyboardInset)) }
    /// Behind the keyboard, as the key bar is (the keyboard's rounded corners
    /// otherwise show the screen through beside them).
    private let keyboardBacking = UIVisualEffectView(effect: UIBlurEffect(style: .systemChromeMaterial))

    override func didMoveToWindow() {
        super.didMoveToWindow()
        setNeedsLayout()  // the safe areas are known now
    }

    override func safeAreaInsetsDidChange() {
        super.safeAreaInsetsDidChange()
        setNeedsLayout()  // turned: the notch moved
    }

    private func placeKeyboardBacking() {
        keyboardBacking.frame = CGRect(x: 0, y: bounds.height - keyboardInset, width: bounds.width, height: keyboardInset)
        keyboardBacking.alpha = keyboardInset > 0 ? 1 : 0
    }

    private var fitted: CGRect {
        let a = fitArea
        guard pixels.width > 0, pixels.height > 0, a.width > 0, a.height > 0 else { return a }
        let s = min(a.width / pixels.width, a.height / pixels.height)
        let w = pixels.width * s, h = pixels.height * s
        return CGRect(x: a.midX - w / 2, y: a.midY - h / 2, width: w, height: h)
    }

    @objc private func keyboardChanged(_ n: Notification) {
        guard let end = (n.userInfo?[UIResponder.keyboardFrameEndUserInfoKey] as? NSValue)?.cgRectValue, let w = window else { return }
        let k = w.screen.coordinateSpace.convert(end, to: self)  // the frame's in the screen's coordinates
        let covered = n.name == UIResponder.keyboardWillHideNotification ? 0 : max(0, bounds.maxY - k.minY)
        guard covered != keyboardInset else { return }
        keyboardInset = covered
        let d = (n.userInfo?[UIResponder.keyboardAnimationDurationUserInfoKey] as? Double) ?? 0.25
        UIView.animate(withDuration: d) {
            self.placeKeyboardBacking()
            self.place()
            self.follow(force: true)  // what you tapped (the password field) stays in sight
            self.layoutIfNeeded()
        }
    }

    override func layoutSubviews() {
        super.layoutSubviews()
        placeKeyboardBacking()
        place()
    }

    private func place() {
        let f = fitted
        let w = f.width * zoom, h = f.height * zoom
        // Keep the zoomed page covering what can be seen (or centred in it
        // while smaller), so its every edge can be brought into view.
        let seen = seenArea
        var x = f.midX - w / 2 + pan.x, y = f.midY - h / 2 + pan.y
        x = w > seen.width ? min(seen.minX, max(seen.maxX - w, x)) : seen.midX - w / 2
        y = h > seen.height ? min(seen.minY, max(seen.maxY - h, y)) : seen.midY - h / 2
        pan = CGPoint(x: x - (f.midX - w / 2), y: y - (f.midY - h / 2))
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        page.frame = CGRect(x: x, y: y, width: w, height: h)
        CATransaction.commit()
        placeVideo()
        placeCursor()
        viewChanged()
    }

    private func placeVideo() {
        let b = page.bounds
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        baseVideo.frame = b
        video.frame = CGRect(x: crop.minX * b.width, y: crop.minY * b.height, width: crop.width * b.width, height: crop.height * b.height)
        CATransaction.commit()
    }

    /// Tells the Mac what part of its display is in view (zoomed in), at
    /// most every 0.1 s, so it sends that part sharp.
    private func viewChanged() {
        guard !viewPending else { return }
        viewPending = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) { [weak self] in
            guard let self else { return }
            self.viewPending = false
            let r = self.page.frame
            guard r.width > 0, r.height > 0 else { return }
            var v = self.seenArea.intersection(r)
            v = CGRect(x: (v.minX - r.minX) / r.width, y: (v.minY - r.minY) / r.height, width: v.width / r.width, height: v.height / r.height)
            if self.zoom <= 1.02 || v.width >= 0.98 { v = CGRect(x: 0, y: 0, width: 1, height: 1) }
            let now = CACurrentMediaTime()
            if let (last, at) = self.lastView, now - at > 0.01 {
                let k = 1 / (now - at)
                let vel = CGPoint(x: (v.midX - last.midX) * k, y: (v.midY - last.midY) * k)
                self.viewVelocity = CGPoint(x: self.viewVelocity.x * 0.5 + vel.x * 0.5, y: self.viewVelocity.y * 0.5 + vel.y * 0.5)
            }
            self.lastView = (v, now)
            if self.touching > 0 {
                // Mid-gesture: ask ahead, not all the time (each new area is a
                // frame of all-new pixels). Once the view nears the detail's
                // edge (or zooms well into it, or out of it), at most four times
                // a second, for where it's heading: a quarter second along.
                let inner = self.crop.insetBy(dx: self.crop.width * 0.15, dy: self.crop.height * 0.15)
                let near = !inner.contains(v) || v.width < self.crop.width * 0.5
                if !near || now - self.lastAsked < 0.25 {
                    self.viewChanged()  // look again while the gesture goes on
                    return
                }
                self.lastAsked = now
                v = v.offsetBy(dx: self.viewVelocity.x * 0.25, dy: self.viewVelocity.y * 0.25)
                v.origin.x = min(max(v.minX, 0), 1 - v.width)
                v.origin.y = min(max(v.minY, 0), 1 - v.height)
            }
            // Some to spare all round, so a pan stays sharp: as much as the
            // detail's frames cover with this screen's pixels one to one, a
            // tenth to 15% a side (a tenth once the link has made them
            // smaller: more would soften what's seen).
            if v.width < 1 {
                let screen = self.bounds.width * self.bounds.height * pow(self.window?.screen.scale ?? 3, 2)
                let frame = self.detailPixels.width * self.detailPixels.height
                let fits = screen > 0 && frame > 0 ? (sqrt(frame / screen) - 1) / 2 : 0
                let spare = min(max(fits, 0.1), 0.15)
                v = v.insetBy(dx: -v.width * spare, dy: -v.height * spare)
            }
            // Frames in this screen's shape, not the display's: upright, a
            // display's whole height filling it is sharp, not stretched
            // from a landscape frame's.
            let aspect = self.bounds.height > 0 ? self.bounds.width / self.bounds.height : 0
            if !self.sentView.isNull, abs(v.minX - self.sentView.minX) < 0.01, abs(v.minY - self.sentView.minY) < 0.01, abs(v.width - self.sentView.width) < 0.01,
               abs(aspect - self.sentAspect) < 0.01 { return }
            self.sentView = v
            self.sentAspect = aspect
            self.stream?.setView(v.width >= 1 ? .zero : v, aspect: aspect)
        }
    }

    private func placeCursor() {
        let r = page.frame
        let tip = CGPoint(x: r.minX + cursor.x * r.width, y: r.minY + cursor.y * r.height)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        // The pointer grows with the zoom, but less than the screen does: as
        // big as the Mac's own when shown about 1:1, a little smaller zoomed
        // right out (still easy to see), and not so big zoomed right in that
        // it hides what it points at.
        let scale = macWidth > 0 ? r.width / macWidth : 1  // phone points per Mac point
        let k = min(max(pow(scale, 0.6), 0.75), 3)
        if let p = pointer, let image = p.image, p.size.width > 0 {
            // The Mac's shape, its hot spot on the spot.
            cursorView.shape = image
            cursorView.frame = CGRect(x: tip.x - p.hotSpot.x * k, y: tip.y - p.hotSpot.y * k, width: p.size.width * k, height: p.size.height * k)
        } else {
            cursorView.shape = nil
            cursorView.frame = CGRect(x: tip.x - k, y: tip.y - k, width: 16 * k, height: 22 * k)
        }
        // Hidden when it's on another display, or (Touch mode) not known yet.
        cursorView.isHidden = pointer?.visible == false || (!trackpad && pointer == nil)
        CATransaction.commit()
    }

    /// Where the Mac's pointer is and what it looks like.
    func pointerMoved(_ p: ScreenPointer) {
        pointer = p
        // Its position, unless this view's own input moved it just now (then
        // the Mac's report is behind it).
        if CACurrentMediaTime() - lastLocalInput > 0.5 { cursor = p.position }
        placeCursor()
    }

    /// A point in this view as fractions of the screen.
    private func fraction(_ p: CGPoint) -> CGPoint {
        let r = page.frame
        guard r.width > 0, r.height > 0 else { return CGPoint(x: 0.5, y: 0.5) }
        return CGPoint(x: min(max((p.x - r.minX) / r.width, 0), 1), y: min(max((p.y - r.minY) / r.height, 0), 1))
    }

    /// Zoomed in, the view follows the pointer near its edges.
    private func follow(force: Bool = false) {
        let seen = seenArea, r = page.frame
        guard force || zoom > 1.01 || r.height > seen.height + 1 else { return }
        let p = CGPoint(x: r.minX + cursor.x * r.width, y: r.minY + cursor.y * r.height)
        let margin: CGFloat = min(60, seen.height / 4)
        if p.x < seen.minX + margin { pan.x += seen.minX + margin - p.x }
        if p.x > seen.maxX - margin { pan.x -= p.x - (seen.maxX - margin) }
        if p.y < seen.minY + margin { pan.y += seen.minY + margin - p.y }
        if p.y > seen.maxY - margin { pan.y -= p.y - (seen.maxY - margin) }
        place()
    }

    /// Trackpad, zoomed in: a pointer outside the part you see (left there
    /// as you zoomed, or moved on the Mac) comes in to its nearest point
    /// there, rather than the view leaping off after it on the next move.
    private func keepPointerInView() {
        guard trackpad, zoom > 1.01 else { return }
        let seen = seenArea, r = page.frame
        guard r.width > 0, seen.width > 0, seen.height > 0 else { return }
        let p = CGPoint(x: r.minX + cursor.x * r.width, y: r.minY + cursor.y * r.height)
        let margin: CGFloat = min(60, seen.height / 4) + 1  // inside follow()'s margin, so it doesn't pan
        let inner = seen.insetBy(dx: min(margin, seen.width / 2 - 1), dy: min(margin, seen.height / 2 - 1))
        guard !inner.contains(p) else { return }
        cursor = fraction(CGPoint(x: min(max(p.x, inner.minX), inner.maxX), y: min(max(p.y, inner.minY), inner.maxY)))
        lastLocalInput = CACurrentMediaTime()
        placeCursor()
        queueMove()
    }

    // MARK: Pointer

    private func mouse(_ type: String, at p: CGPoint, button: String = "left", clicks: Int = 1, pressed: Bool = false) {
        var a: [String: Any] = ["type": type, "x": p.x, "y": p.y, "button": button, "clicks": clicks]
        if pressed { a["pressed"] = true }
        if !mods.isEmpty { a["mods"] = Array(mods) }
        if type == "down" { forgetTyped() }  // a click may move the Mac's caret
        send?("mouse", a)
    }

    /// Moves go at most once a display frame.
    private func queueMove() {
        guard !pendingMove else { return }
        pendingMove = true
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.0 / 120) { [weak self] in
            guard let self else { return }
            self.pendingMove = false
            self.mouse("move", at: self.cursor, pressed: self.dragging)
        }
    }

    private func click(_ button: String) {
        stopMomentum()
        let now = Date()
        let count = button == "left" && now.timeIntervalSince(lastTap.at) < 0.4 ? min(lastTap.count + 1, 3) : 1
        lastTap = (now, count)
        mouse("down", at: cursor, button: button, clicks: count)
        mouse("up", at: cursor, button: button, clicks: count)
        clearMods()
    }

    @objc private func tap(_ g: UITapGestureRecognizer) {
        if !trackpad {
            cursor = fraction(g.location(in: self))
            lastLocalInput = CACurrentMediaTime()
            placeCursor()
        }
        click("left")
    }

    @objc private func twoFingerTap(_ g: UITapGestureRecognizer) {
        if !trackpad { cursor = fraction(g.location(in: self)) }
        click("right")
    }

    @objc private func hold(_ g: UILongPressGestureRecognizer) {
        if !trackpad {
            // Touch mode: press and hold right-clicks where you hold.
            if g.state == .began {
                cursor = fraction(g.location(in: self))
                click("right")
            }
            return
        }
        // Trackpad: press and hold, then drag, holds the button down; the
        // hold itself moves the pointer as the finger moves.
        switch g.state {
        case .began:
            keepPointerInView()  // the drag starts where you can see it
            dragging = true
            holdAt = g.location(in: self)
            movePan?.isEnabled = false  // ends a pan already going (it's the hold's now)
            movePan?.isEnabled = true
            mouse("down", at: cursor)
            UIImpactFeedbackGenerator(style: .medium).impactOccurred()
        case .changed:
            let p = g.location(in: self), r = page.frame
            defer { holdAt = p }
            guard dragging, r.width > 0 else { return }
            cursor.x = min(max(cursor.x + (p.x - holdAt.x) / r.width, 0), 1)
            cursor.y = min(max(cursor.y + (p.y - holdAt.y) / r.height, 0), 1)
            lastLocalInput = CACurrentMediaTime()
            placeCursor()
            follow()
            queueMove()
        case .ended, .cancelled, .failed:
            if dragging {
                dragging = false
                mouse("up", at: cursor)
                clearMods()
            }
        default: break
        }
    }

    @objc private func oneFinger(_ g: UIPanGestureRecognizer) {
        track(g)
        if trackpad {
            if g.state == .began { keepPointerInView() }
            // Relative, as on a trackpad, a little faster when quick.
            let t = g.translation(in: self)
            g.setTranslation(.zero, in: self)
            let r = page.frame
            guard r.width > 0 else { return }
            let speed = hypot(g.velocity(in: self).x, g.velocity(in: self).y)
            let k: CGFloat = 1 + min(speed / 1500, 1.5)
            cursor.x = min(max(cursor.x + t.x * k / r.width, 0), 1)
            cursor.y = min(max(cursor.y + t.y * k / r.height, 0), 1)
            lastLocalInput = CACurrentMediaTime()
            placeCursor()
            follow()
            queueMove()
        } else {
            scroll(g, at: g.location(in: self))
        }
    }

    @objc private func twoFingers(_ g: UIPanGestureRecognizer) {
        track(g)
        defer { twoFingersDone(g) }
        // Touch mode, zoomed: two fingers move the view (as they do while zooming).
        let movesView = !trackpad && zoom > 1.01
        switch settleTwoFingers(g) {
        case .undecided:
            g.setTranslation(.zero, in: self)
            // A quick flick, over before it could be told: a scroll.
            if g.state == .ended, !movesView, hypot(g.velocity(in: self).x, g.velocity(in: self).y) > 300 {
                scrollAt = trackpad ? cursor : fraction(g.location(in: self))
                wheel(0, 0, phase: "began")
                wheel(0, 0, phase: "ended")
                velocity = g.velocity(in: self)
                startMomentum()
            }
            return
        case .zoom where !movesView:
            g.setTranslation(.zero, in: self)
            return
        default: break
        }
        if movesView {
            let t = g.translation(in: self)
            g.setTranslation(.zero, in: self)
            pan.x += t.x
            pan.y += t.y
            place()
        } else {
            scroll(g, at: trackpad ? nil : g.location(in: self))
        }
    }

    /// Two fingers' centre and how far apart they are.
    private func twoFingerSpot(_ g: UIGestureRecognizer) -> (centre: CGPoint, spread: CGFloat)? {
        guard g.numberOfTouches >= 2 else { return nil }
        let a = g.location(ofTouch: 0, in: self), b = g.location(ofTouch: 1, in: self)
        return (CGPoint(x: (a.x + b.x) / 2, y: (a.y + b.y) / 2), hypot(a.x - b.x, a.y - b.y))
    }

    /// Decides, from either two-finger gesture, whether the fingers are
    /// zooming or scrolling: by whichever they've done more of once they've
    /// moved 10 points from where they were when the first gesture began.
    private func settleTwoFingers(_ g: UIGestureRecognizer) -> TwoFingers {
        if g.state == .began { twoFingerGestures += 1 }
        guard twoFingerMode == .undecided, let now = twoFingerSpot(g) else { return twoFingerMode }
        guard let start = twoFingerStart else {
            twoFingerStart = now
            return .undecided
        }
        let travel = hypot(now.centre.x - start.centre.x, now.centre.y - start.centre.y)
        let spread = abs(now.spread - start.spread)
        if max(travel, spread) >= 10 { twoFingerMode = spread > travel ? .zoom : .scroll }
        return twoFingerMode
    }

    /// Once both two-finger gestures are over, the next two fingers decide afresh.
    private func twoFingersDone(_ g: UIGestureRecognizer) {
        guard [.ended, .cancelled, .failed].contains(g.state) else { return }
        twoFingerGestures = max(0, twoFingerGestures - 1)
        if twoFingerGestures == 0 {
            twoFingerMode = .undecided
            twoFingerStart = nil
        }
    }

    @objc private func pinch(_ g: UIPinchGestureRecognizer) {
        track(g)
        defer { twoFingersDone(g) }
        let mode = settleTwoFingers(g)
        if [.ended, .cancelled, .failed].contains(g.state) {
            if mode == .zoom { keepPointerInView() }
            return
        }
        // Undecided, the scale builds up, to be zoomed by at once if it's a zoom.
        guard mode == .zoom else {
            if mode == .scroll { g.scale = 1 }
            return
        }
        guard g.state == .changed || g.state == .began else { return }
        let before = zoom
        zoom = min(max(zoom * g.scale, 1), 16)
        g.scale = 1
        // Zoom about the fingers: the point under them stays put.
        let c = g.location(in: self), r = page.frame
        let k = zoom / before
        let nx = c.x - (c.x - r.minX) * k, ny = c.y - (c.y - r.minY) * k
        let f = fitted
        pan = CGPoint(x: nx - (f.midX - f.width * zoom / 2), y: ny - (f.midY - f.height * zoom / 2))
        place()
    }

    // MARK: Scrolling, with momentum (the Mac's own phases)

    private func wheel(_ dx: CGFloat, _ dy: CGFloat, phase: String) {
        // In the Mac's points: the page is drawn smaller here than there, so
        // a finger's travel scrolls the Mac as far as it moved over it.
        let r = page.frame
        guard r.width > 0 else { return }
        let k = macWidth > 0 ? macWidth / r.width : 1
        send?("wheel", ["x": scrollAt.x, "y": scrollAt.y, "dx": dx * k, "dy": dy * k, "phase": phase])
    }

    private func scroll(_ g: UIPanGestureRecognizer, at p: CGPoint?) {
        switch g.state {
        case .began:
            stopMomentum()
            scrollAt = p.map(fraction) ?? cursor
            scrolling = true
            wheel(0, 0, phase: "began")
        case .changed:
            let t = g.translation(in: self)
            g.setTranslation(.zero, in: self)
            if !scrolling {
                // Two fingers only just settled on scrolling: a scroll from here.
                scrollAt = p.map(fraction) ?? cursor
                scrolling = true
                wheel(0, 0, phase: "began")
            }
            wheel(t.x, t.y, phase: "changed")
        case .ended, .cancelled:
            guard scrolling else { return }
            scrolling = false
            wheel(0, 0, phase: "ended")
            velocity = g.velocity(in: self)
            startMomentum()
        default: break
        }
    }

    private func startMomentum() {
        guard hypot(velocity.x, velocity.y) > 80 else { return }
        let link = CADisplayLink(target: self, selector: #selector(glide(_:)))
        link.add(to: .main, forMode: .common)
        momentum = link
    }

    @objc private func glide(_ link: CADisplayLink) {
        let dt = link.targetTimestamp - link.timestamp
        wheel(velocity.x * dt, velocity.y * dt, phase: "momentum")
        let k = pow(0.997, dt * 1000)
        velocity = CGPoint(x: velocity.x * k, y: velocity.y * k)
        if hypot(velocity.x, velocity.y) < 25 { stopMomentum() }
    }

    private func stopMomentum() {
        momentum?.invalidate()
        momentum = nil
    }

    override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent?) {
        stopMomentum()
        super.touchesBegan(touches, with: event)
    }

    /// Counts the gestures going on (their touches are cancelled for the
    /// view, so they're counted here instead).
    private func track(_ g: UIGestureRecognizer) {
        switch g.state {
        case .began: touching += 1
        case .ended, .cancelled, .failed: touching = max(0, touching - 1)
        default: break
        }
    }

    // MARK: Keyboard

    override var canBecomeFirstResponder: Bool { true }
    override var inputAccessoryView: UIView? { modifierBar }
    var hasText: Bool { true }
    var autocorrectionType: UITextAutocorrectionType { get { .no } set {} }
    var autocapitalizationType: UITextAutocapitalizationType { get { .none } set {} }
    var smartQuotesType: UITextSmartQuotesType { get { .no } set {} }
    var smartDashesType: UITextSmartDashesType { get { .no } set {} }
    var spellCheckingType: UITextSpellCheckingType { get { .no } set {} }
    var smartInsertDeleteType: UITextSmartInsertDeleteType { get { .no } set {} }
    // For UITextInput (below): what was typed lately, as plain text, and the
    // composition in progress (IME, dictation).
    fileprivate var typed: [Character] = []
    fileprivate var marked = ""
    var markedTextStyle: [NSAttributedString.Key: Any]?
    weak var inputDelegate: UITextInputDelegate?
    lazy var tokenizer: UITextInputTokenizer = UITextInputStringTokenizer(textInput: self)

    /// Forget what was typed (the Mac's caret may have moved), and tell the
    /// keyboard its document changed.
    fileprivate func forgetTyped() {
        guard !typed.isEmpty || !marked.isEmpty else { return }
        inputDelegate?.textWillChange(self)
        typed = []
        marked = ""
        inputDelegate?.textDidChange(self)
    }

    override func becomeFirstResponder() -> Bool {
        let ok = super.becomeFirstResponder()
        if ok { onKeyboard?(true) }
        return ok
    }

    override func resignFirstResponder() -> Bool {
        let ok = super.resignFirstResponder()
        if ok { onKeyboard?(false) }
        return ok
    }

    /// Modifiers for the next key (or click); locked ones stay on.
    private var mods: Set<String> = []
    private var locked: Set<String> = []

    private func clearMods() {
        mods = locked
        modifierBar.show(mods: mods, locked: locked)
    }

    private func key(_ vk: Int) {
        if vk != 0x33 { forgetTyped() }
        send?("key", ["type": "down", "vk": vk, "mods": Array(mods)])
        send?("key", ["type": "up", "vk": vk, "mods": Array(mods)])
        clearMods()
    }

    func insertText(_ text: String) {
        if text == "\n" { return key(0x24) }
        // With a modifier, the key itself (⌘C is C's key); else the text,
        // whatever the Mac's keyboard layout.
        if !mods.isEmpty, text.count == 1, let vk = Self.keyCodes[Character(text.lowercased())] {
            var m = mods
            if text != text.lowercased() { m.insert("shift") }
            forgetTyped()
            send?("key", ["type": "down", "vk": vk, "mods": Array(m)])
            send?("key", ["type": "up", "vk": vk, "mods": Array(m)])
            clearMods()
            return
        }
        if macLocked {
            forgetTyped()
            // Key by key, with shift where the character needs it (a US
            // layout), for a password field that takes only keys.
            for ch in text {
                let lower = Character(ch.lowercased())
                if let vk = Self.keyCodes[lower] {
                    key(vk, shift: ch != lower)
                } else if let (vk, _) = Self.shifted[ch] {
                    key(vk, shift: true)
                } else {
                    send?("text", ["text": String(ch)])
                }
            }
            clearMods()
            return
        }
        send?("text", ["text": text])
        typed.append(contentsOf: text)
        if typed.count > 400 { typed.removeFirst(typed.count - 400) }
        clearMods()
    }

    private func key(_ vk: Int, shift: Bool) {
        var m = mods
        if shift { m.insert("shift") }
        send?("key", ["type": "down", "vk": vk, "mods": Array(m)])
        send?("key", ["type": "up", "vk": vk, "mods": Array(m)])
    }

    /// Characters typed with shift on a US keyboard, by the key they're on.
    static let shifted: [Character: (Int, Character)] = {
        let pairs: [(Character, Character)] = [("!", "1"), ("@", "2"), ("#", "3"), ("$", "4"), ("%", "5"), ("^", "6"), ("&", "7"), ("*", "8"), ("(", "9"), (")", "0"),
            ("_", "-"), ("+", "="), ("{", "["), ("}", "]"), ("|", "\\"), (":", ";"), ("\"", "'"), ("<", ","), (">", "."), ("?", "/"), ("~", "`")]
        var m: [Character: (Int, Character)] = [:]
        for (s, base) in pairs { if let vk = keyCodes[base] { m[s] = (vk, base) } }
        return m
    }()

    func deleteBackward() {
        if !typed.isEmpty { typed.removeLast() }
        key(0x33)
    }

    private lazy var modifierBar: ModifierBar = {
        let v = ModifierBar()
        v.onKey = { [weak self] vk in self?.key(vk) }
        v.onMod = { [weak self] m, lock in
            guard let self else { return }
            if lock {
                if self.locked.contains(m) { self.locked.remove(m); self.mods.remove(m) } else { self.locked.insert(m); self.mods.insert(m) }
            } else if self.mods.contains(m) {
                self.mods.remove(m)
                self.locked.remove(m)
            } else {
                self.mods.insert(m)
            }
            v.show(mods: self.mods, locked: self.locked)
        }
        v.onHide = { [weak self] in _ = self?.resignFirstResponder() }
        v.onEnd = { [weak self] in self?.onEnd?() }
        return v
    }()

    /// Mac key codes (kVK_*) for characters, as on a US keyboard.
    static let keyCodes: [Character: Int] = {
        var m: [Character: Int] = [" ": 49, "-": 27, "=": 24, "[": 33, "]": 30, "\\": 42, ";": 41, "'": 39, ",": 43, ".": 47, "/": 44, "`": 50]
        let letters: [(Character, Int)] = [("a", 0), ("b", 11), ("c", 8), ("d", 2), ("e", 14), ("f", 3), ("g", 5), ("h", 4), ("i", 34),
            ("j", 38), ("k", 40), ("l", 37), ("m", 46), ("n", 45), ("o", 31), ("p", 35), ("q", 12), ("r", 15), ("s", 1), ("t", 17),
            ("u", 32), ("v", 9), ("w", 13), ("x", 7), ("y", 16), ("z", 6)]
        for (c, k) in letters { m[c] = k }
        let digits: [(Character, Int)] = [("0", 29), ("1", 18), ("2", 19), ("3", 20), ("4", 21), ("5", 23), ("6", 22), ("7", 26), ("8", 28), ("9", 25)]
        for (c, k) in digits { m[c] = k }
        return m
    }()

    // MARK: Hardware keyboard: keys go as the Mac's own

    override func pressesBegan(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        var rest = Set<UIPress>()
        for p in presses {
            guard let k = p.key, let vk = Self.hidToMac[k.keyCode.rawValue] else { rest.insert(p); continue }
            send?("key", ["type": "down", "vk": vk, "mods": Self.mods(k.modifierFlags)])
        }
        if !rest.isEmpty { super.pressesBegan(rest, with: event) }
    }

    override func pressesEnded(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        var rest = Set<UIPress>()
        for p in presses {
            guard let k = p.key, let vk = Self.hidToMac[k.keyCode.rawValue] else { rest.insert(p); continue }
            send?("key", ["type": "up", "vk": vk, "mods": Self.mods(k.modifierFlags)])
        }
        if !rest.isEmpty { super.pressesEnded(rest, with: event) }
    }

    /// A press iOS takes back (the app going to the background, the view
    /// losing the keyboard) is let go on the Mac too, or it'd stay down there
    /// and repeat.
    override func pressesCancelled(_ presses: Set<UIPress>, with event: UIPressesEvent?) {
        var rest = Set<UIPress>()
        for p in presses {
            guard let k = p.key, let vk = Self.hidToMac[k.keyCode.rawValue] else { rest.insert(p); continue }
            send?("key", ["type": "up", "vk": vk, "mods": Self.mods(k.modifierFlags)])
        }
        if !rest.isEmpty { super.pressesCancelled(rest, with: event) }
    }

    private static func mods(_ f: UIKeyModifierFlags) -> [String] {
        var m: [String] = []
        if f.contains(.shift) { m.append("shift") }
        if f.contains(.control) { m.append("ctrl") }
        if f.contains(.alternate) { m.append("alt") }
        if f.contains(.command) { m.append("meta") }
        return m
    }

    /// USB HID usages to Mac key codes (letters, digits, punctuation and the
    /// editing and arrow keys).
    static let hidToMac: [Int: Int] = {
        var m: [Int: Int] = [0x28: 36, 0x29: 53, 0x2A: 51, 0x2B: 48, 0x2C: 49, 0x2D: 27, 0x2E: 24, 0x2F: 33, 0x30: 30, 0x31: 42,
            0x33: 41, 0x34: 39, 0x35: 50, 0x36: 43, 0x37: 47, 0x38: 44, 0x4F: 124, 0x50: 123, 0x51: 125, 0x52: 126,
            0x4A: 115, 0x4D: 119, 0x4B: 116, 0x4E: 121, 0x4C: 117]
        let letters = [0, 11, 8, 2, 14, 3, 5, 4, 34, 38, 40, 37, 46, 45, 31, 35, 12, 15, 1, 17, 32, 9, 13, 7, 16, 6]
        for (i, k) in letters.enumerated() { m[0x04 + i] = k }
        let digits = [18, 19, 20, 21, 23, 22, 26, 28, 25, 29] // 1…9, 0
        for (i, k) in digits.enumerated() { m[0x1E + i] = k }
        return m
    }()
}

/// The pointer drawn here at once, ahead of the video (trackpad mode).
private final class PointerView: UIView {
    /// The Mac's own pointer image, once it's come (else an arrow is drawn).
    /// In a layer of its own: a view that draws (the arrow) replaces its own
    /// layer's contents with what it draws, which blanked the image.
    var shape: CGImage? {
        didSet {
            guard shape !== oldValue else { return }
            shapeLayer.contents = shape
            setNeedsDisplay()
        }
    }
    private let shapeLayer = CALayer()

    override func layoutSubviews() {
        super.layoutSubviews()
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        shapeLayer.frame = bounds
        CATransaction.commit()
    }

    init() {
        super.init(frame: CGRect(x: 0, y: 0, width: 16, height: 22))
        backgroundColor = .clear
        isAccessibilityElement = false
        shapeLayer.contentsGravity = .resize
        shapeLayer.actions = ["contents": NSNull(), "bounds": NSNull(), "position": NSNull()]
        layer.addSublayer(shapeLayer)
    }

    required init?(coder: NSCoder) { fatalError() }

    override func draw(_ rect: CGRect) {
        guard shape == nil else { return }
        let p = UIBezierPath()
        p.move(to: CGPoint(x: 1, y: 1))
        p.addLine(to: CGPoint(x: 1, y: 17))
        p.addLine(to: CGPoint(x: 5.5, y: 13))
        p.addLine(to: CGPoint(x: 8.5, y: 20))
        p.addLine(to: CGPoint(x: 11, y: 19))
        p.addLine(to: CGPoint(x: 8, y: 12))
        p.addLine(to: CGPoint(x: 14, y: 12))
        p.close()
        UIColor.black.setFill()
        p.fill()
        UIColor.white.setStroke()
        p.lineWidth = 1.2
        p.stroke()
    }
}

/// esc, tab, ⌃ ⌥ ⌘ ⇧, arrows, more keys, and hide, above the keyboard.
/// A modifier holds for the next key; double-tap locks it.
final class ModifierBar: UIInputView {
    var onKey: ((Int) -> Void)?
    var onMod: ((String, Bool) -> Void)?
    var onHide: (() -> Void)?
    var onEnd: (() -> Void)?
    private var modButtons: [String: UIButton] = [:]
    private var lastModTap: (String, Date) = ("", .distantPast)

    init() {
        super.init(frame: CGRect(x: 0, y: 0, width: 320, height: 52), inputViewStyle: .default)
        allowsSelfSizing = true
        // Over the video, it needs a background of its own.
        let bg = UIVisualEffectView(effect: UIBlurEffect(style: .systemChromeMaterial))
        bg.translatesAutoresizingMaskIntoConstraints = false
        addSubview(bg)
        NSLayoutConstraint.activate([
            bg.leadingAnchor.constraint(equalTo: leadingAnchor), bg.trailingAnchor.constraint(equalTo: trailingAnchor),
            bg.topAnchor.constraint(equalTo: topAnchor), bg.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
        let stack = UIStackView()
        stack.axis = .horizontal
        stack.spacing = 5
        stack.distribution = .fillEqually
        stack.translatesAutoresizingMaskIntoConstraints = false
        stack.addArrangedSubview(keyButton("esc", vk: 53))
        stack.addArrangedSubview(keyButton("tab", vk: 48))
        for (m, symbol, label) in [("ctrl", "⌃", "Control"), ("alt", "⌥", "Option"), ("meta", "⌘", "Command"), ("shift", "⇧", "Shift")] {
            let b = button(symbol)
            b.accessibilityLabel = label
            b.addAction(UIAction { [weak self] _ in self?.modTapped(m) }, for: .touchUpInside)
            modButtons[m] = b
            stack.addArrangedSubview(b)
        }
        let arrows = button("")
        arrows.setImage(UIImage(systemName: "arrow.up.and.down.and.arrow.left.and.right"), for: .normal)
        arrows.accessibilityLabel = "Arrow keys"
        arrows.showsMenuAsPrimaryAction = true
        arrows.menu = UIMenu(children: [("Up", 126, "arrow.up"), ("Down", 125, "arrow.down"), ("Left", 123, "arrow.left"), ("Right", 124, "arrow.right")].map { t, vk, img in
            UIAction(title: t, image: UIImage(systemName: img)) { [weak self] _ in self?.onKey?(vk) }
        })
        stack.addArrangedSubview(arrows)
        let more = button("…")
        more.accessibilityLabel = "More keys"
        more.showsMenuAsPrimaryAction = true
        var items: [UIMenuElement] = [("Delete Forward", 117), ("Home", 115), ("End", 119), ("Page Up", 116), ("Page Down", 121)].map { t, vk in
            UIAction(title: t) { [weak self] _ in self?.onKey?(vk) }
        }
        let fkeys = [122, 120, 99, 118, 96, 97, 98, 100, 101, 109, 103, 111]
        items.append(UIMenu(title: "Function Keys", children: fkeys.enumerated().map { i, vk in
            UIAction(title: "F\(i + 1)") { [weak self] _ in self?.onKey?(vk) }
        }))
        more.menu = UIMenu(children: items)
        stack.addArrangedSubview(more)
        let hide = button("")
        hide.setImage(UIImage(systemName: "keyboard.chevron.compact.down"), for: .normal)
        hide.accessibilityLabel = "Hide keyboard"
        hide.addAction(UIAction { [weak self] _ in self?.onHide?() }, for: .touchUpInside)
        stack.addArrangedSubview(hide)
        // End the session without putting the keyboard away first (the
        // controls' End is under the keyboard).
        let end = button("")
        end.setImage(UIImage(systemName: "xmark"), for: .normal)
        end.accessibilityLabel = "End screen sharing"
        end.tintColor = .systemRed
        end.configurationUpdateHandler = { btn in
            var c = btn.configuration
            c?.baseBackgroundColor = .systemRed
            c?.baseForegroundColor = .white
            btn.configuration = c
        }
        end.addAction(UIAction { [weak self] _ in self?.onEnd?() }, for: .touchUpInside)
        stack.addArrangedSubview(end)
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: layoutMarginsGuide.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: layoutMarginsGuide.trailingAnchor),
            stack.topAnchor.constraint(equalTo: topAnchor, constant: 6),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -6),
        ])
    }

    required init?(coder: NSCoder) { fatalError() }

    private func button(_ title: String) -> UIButton {
        var config = UIButton.Configuration.filled()
        config.cornerStyle = .medium
        config.contentInsets = NSDirectionalEdgeInsets(top: 6, leading: 0, bottom: 6, trailing: 0)
        config.titleLineBreakMode = .byClipping
        config.preferredSymbolConfigurationForImage = UIImage.SymbolConfiguration(pointSize: 15, weight: .medium)
        let b = UIButton(configuration: config)
        if !title.isEmpty {
            b.configuration?.attributedTitle = AttributedString(title, attributes: AttributeContainer([.font: UIFont.systemFont(ofSize: 16, weight: .medium)]))
        }
        b.configurationUpdateHandler = { btn in
            var c = btn.configuration
            // Keys as the keyboard draws them; a held modifier in the tint.
            c?.baseBackgroundColor = btn.isSelected ? .tintColor : .secondarySystemFill
            c?.baseForegroundColor = btn.isSelected ? .white : .label
            btn.configuration = c
        }
        return b
    }

    private func keyButton(_ title: String, vk: Int) -> UIButton {
        let b = button(title)
        b.addAction(UIAction { [weak self] _ in self?.onKey?(vk) }, for: .touchUpInside)
        return b
    }

    private func modTapped(_ m: String) {
        let now = Date()
        let double = lastModTap.0 == m && now.timeIntervalSince(lastModTap.1) < 0.35
        lastModTap = (m, now)
        UIImpactFeedbackGenerator(style: .light).impactOccurred()
        onMod?(m, double)
    }

    func show(mods: Set<String>, locked: Set<String>) {
        for (m, b) in modButtons {
            b.isSelected = mods.contains(m)
            b.configuration?.subtitle = locked.contains(m) ? "locked" : nil
        }
    }
}

// MARK: UITextInput

// The software keyboard repeats its delete key (and offers dictation and
// IME) only for views that are UITextInput, not just UIKeyInput. So, as the
// terminal does (TerminalScreen.swift), the screen is one, over a stand-in
// document: a run of underscores, then what was typed lately, with the caret
// at its end. The keyboard deletes from it as from any text, and each
// delete goes to the Mac as its Delete key.
extension ScreenTouchView {
    private static let standIn = String(repeating: "_", count: 64)
    private var docLength: Int { Self.standIn.count + typed.count + marked.count }
    private var doc: [Character] { Array(Self.standIn) + typed + Array(marked) }
    private func idx(_ p: UITextPosition) -> Int { (p as? StandInPosition)?.i ?? docLength }

    var selectedTextRange: UITextRange? {
        get { StandInRange(docLength, docLength) }
        set {}
    }

    var markedTextRange: UITextRange? {
        marked.isEmpty ? nil : StandInRange(Self.standIn.count + typed.count, docLength)
    }

    var beginningOfDocument: UITextPosition { StandInPosition(0) }
    var endOfDocument: UITextPosition { StandInPosition(docLength) }

    func text(in range: UITextRange) -> String? {
        guard let r = range as? StandInRange else { return nil }
        let d = doc
        let a = max(0, min(r.a, d.count)), b = max(a, min(r.b, d.count))
        return String(d[a..<b])
    }

    /// The keyboard replacing text (dictation revising what it typed, or a
    /// word deleted at once while delete repeats): Delete back to where the
    /// range starts, then the new text and what came after the range, typed
    /// again.
    func replace(_ range: UITextRange, withText text: String) {
        guard let r = range as? StandInRange else { return }
        let base = Self.standIn.count
        let end = base + typed.count
        let a = min(max(r.a, 0), end), b = min(max(r.b, a), end)
        let tail = b > base ? Array(typed[(b - base)...]) : typed
        for _ in 0..<(end - a) {
            send?("key", ["type": "down", "vk": 0x33, "mods": [String]()])
            send?("key", ["type": "up", "vk": 0x33, "mods": [String]()])
        }
        typed = a > base ? Array(typed[..<(a - base)]) : []
        let again = text + String(tail)
        if !again.isEmpty { insertText(again) }
    }

    func setMarkedText(_ markedText: String?, selectedRange: NSRange) {
        marked = markedText ?? ""
    }

    func unmarkText() {
        let t = marked
        marked = ""
        if !t.isEmpty { insertText(t) }
    }

    func textRange(from fromPosition: UITextPosition, to toPosition: UITextPosition) -> UITextRange? {
        StandInRange(idx(fromPosition), idx(toPosition))
    }

    func position(from position: UITextPosition, offset: Int) -> UITextPosition? {
        let i = idx(position) + offset
        return (0...docLength).contains(i) ? StandInPosition(i) : nil
    }

    func position(from position: UITextPosition, in direction: UITextLayoutDirection, offset: Int) -> UITextPosition? {
        switch direction {
        case .left, .up: self.position(from: position, offset: -offset)
        default: self.position(from: position, offset: offset)
        }
    }

    func compare(_ position: UITextPosition, to other: UITextPosition) -> ComparisonResult {
        let a = idx(position), b = idx(other)
        return a < b ? .orderedAscending : a > b ? .orderedDescending : .orderedSame
    }

    func offset(from: UITextPosition, to toPosition: UITextPosition) -> Int { idx(toPosition) - idx(from) }

    func position(within range: UITextRange, farthestIn direction: UITextLayoutDirection) -> UITextPosition? {
        guard let r = range as? StandInRange else { return nil }
        return StandInPosition(direction == .left || direction == .up ? r.a : r.b)
    }

    func characterRange(byExtending position: UITextPosition, in direction: UITextLayoutDirection) -> UITextRange? {
        let i = idx(position)
        return direction == .left || direction == .up ? StandInRange(max(0, i - 1), i) : StandInRange(i, min(docLength, i + 1))
    }

    func baseWritingDirection(for position: UITextPosition, in direction: UITextStorageDirection) -> NSWritingDirection { .leftToRight }
    func setBaseWritingDirection(_ writingDirection: NSWritingDirection, for range: UITextRange) {}

    // The screen has no caret of its own to show: the keyboard's suggestions
    // and the like go at the bottom of the view.
    func caretRect(for position: UITextPosition) -> CGRect { CGRect(x: bounds.midX, y: bounds.maxY - 1, width: 1, height: 1) }
    func firstRect(for range: UITextRange) -> CGRect { caretRect(for: endOfDocument) }
    func selectionRects(for range: UITextRange) -> [UITextSelectionRect] { [] }
    func closestPosition(to point: CGPoint) -> UITextPosition? { endOfDocument }
    func closestPosition(to point: CGPoint, within range: UITextRange) -> UITextPosition? { range.end }
    func characterRange(at point: CGPoint) -> UITextRange? { nil }
}
