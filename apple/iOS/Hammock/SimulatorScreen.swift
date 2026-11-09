import HammockKit
import SwiftUI

/// A simulator pane on the phone: the Mac's simulator as live video
/// (MediaStream, H.264 over QUIC), with screenshots a few times a second
/// until the video plays or while it's down, driven by taps, swipes, typing
/// and its Home and Lock buttons.
struct SimulatorScreen: View {
    let model: AppModel
    let paneID: String
    let active: Bool
    /// View only (a shared Group without Drive): shown, not driven.
    var canDrive = true

    @State private var image: UIImage?
    @State private var points = CGSize(width: 402, height: 874) // the device's screen
    @State private var failed = false
    @State private var typing = false
    @State private var text = ""
    @State private var dragStart: Date?
    @State private var stream: MediaStream?
    @State private var playing = false
    @State private var pinch: (start: Date, anchor: UnitPoint, scale: CGFloat)?
    @State private var pinched = false // the drag that came with a pinch isn't a swipe
    @State private var shot: SimulatorShot? // a screenshot to share
    @State private var shooting = false

    private var pane: Pane? { model.panes[paneID] }

    var body: some View {
        VStack(spacing: 12) {
            GeometryReader { geo in
                let fit = fitted(in: geo.size)
                ZStack {
                    if let stream, playing {
                        MediaView(stream: stream)
                            .frame(width: fit.width, height: fit.height)
                            .clipShape(RoundedRectangle(cornerRadius: corner(fit), style: .continuous))
                            .contentShape(Rectangle())
                            .gesture(magnify(fit).simultaneously(with: drag(fit)), isEnabled: canDrive)
                    } else if let image {
                        Image(uiImage: image)
                            .resizable()
                            .frame(width: fit.width, height: fit.height)
                            .clipShape(RoundedRectangle(cornerRadius: corner(fit), style: .continuous))
                            .gesture(magnify(fit).simultaneously(with: drag(fit)), isEnabled: canDrive)
                    } else if failed {
                        ContentUnavailableView("Can't See This Simulator", systemImage: "iphone.slash",
                                               description: Text("Its Mac isn't answering, or it's still booting."))
                    } else {
                        ProgressView()
                    }
                }
                .frame(width: geo.size.width, height: geo.size.height)
            }
        }
        .padding(.horizontal)
        .navigationTitle(pane?.title ?? "Simulator")
        // The system's bar, not one of our own: at the bottom of an iPhone,
        // down the side of iPhone Duo (where it leaves the device its
        // height, and keeps out of the fold).
        .toolbar {
            ToolbarItemGroup(placement: .bottomBar) { Group {
                Button("Home", systemImage: "house") { button("home") }
                Button("Lock", systemImage: "lock") { button("lock") }
                Button("Rotate Left", systemImage: "rotate.left") { model.rotateSimulator(paneID, "left") }
                Button("Rotate Right", systemImage: "rotate.right") { model.rotateSimulator(paneID, "right") }
                // Tap to type; hold for the software keyboard.
                Menu {
                    Button("Toggle Software Keyboard", systemImage: "keyboard.chevron.compact.down") { model.pressSimulatorButton(paneID, "keyboard") }
                } label: {
                    Label("Type", systemImage: "keyboard")
                } primaryAction: {
                    typing = true
                }
                if pane?.deviceFoldable == true {
                    // Tap to open or close; hold for half open.
                    let open = (pane?.deviceHinge ?? 0) > 120
                    Menu {
                        Button("Open Phone", systemImage: "book") { model.foldSimulator(paneID, "open") }
                        Button("Half Open Phone", systemImage: "laptopcomputer") { model.foldSimulator(paneID, "half") }
                        Button("Close Phone", systemImage: "book.closed") { model.foldSimulator(paneID, "closed") }
                    } label: {
                        Label(open ? "Close Phone" : "Open Phone", systemImage: open ? "book.closed" : "book")
                    } primaryAction: {
                        model.toggleFold(paneID)
                    }
                }
            }
            .disabled(!canDrive) }
        }
        // A screenshot in the device's pixels, to save to Photos, copy or send.
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button("Share Screenshot", systemImage: "camera") {
                    shooting = true
                    Task {
                        if let png = await model.simulatorScreenshot(paneID), let img = UIImage(data: png) { shot = SimulatorShot(image: img) }
                        shooting = false
                    }
                }
                .disabled(shooting)
            }
        }
        .sheet(item: $shot) { ActivitySheet(items: [$0.image]).ignoresSafeArea() }
        .task(id: active) {
            guard active else { return }
            await withTaskGroup(of: Void.self) { g in
                g.addTask { await watchVideo() }
                g.addTask { await watch() }
            }
        }
        .alert("Type on the Simulator", isPresented: $typing) {
            TextField("Text", text: $text)
            Button("Send") {
                let t = text
                text = ""
                Task { _ = await model.simulatorCall(paneID, "text", ["text": t]) }
            }
            Button("Cancel", role: .cancel) { text = "" }
        }
    }

    /// Video while this pane is showing; a stream that fails is replaced.
    private func watchVideo() async {
        while !Task.isCancelled {
            guard let s = model.videoStream(for: paneID) else {
                try? await Task.sleep(for: .seconds(1))
                continue
            }
            let states = AsyncStream<MediaStream.State> { cont in
                s.onState = { cont.yield($0) }
            }
            s.onSize = { px in
                // Phones and tablets are 3x or 2x (as the simulator host decides).
                let scale: CGFloat = px.width >= 1000 ? 3 : 2
                points = CGSize(width: px.width / scale, height: px.height / scale)
            }
            stream = s
            s.start()
            for await st in states {
                if st == .playing { playing = true }
                if case .failed = st { break }
                if Task.isCancelled { break }
            }
            s.stop()
            playing = false
            stream = nil
            if Task.isCancelled { break }
            try? await Task.sleep(for: .seconds(2))
        }
        stream?.stop()
        stream = nil
        playing = false
    }

    /// Screenshots, one after another, while this pane is showing and the
    /// video isn't playing.
    private func watch() async {
        while !Task.isCancelled {
            if playing {
                try? await Task.sleep(for: .milliseconds(500))
                continue
            }
            if let r = await model.simulatorCall(paneID, "screenshot", ["format": "jpeg"]), let img = UIImage(data: r.data) {
                image = img
                failed = false
                if let w = (r.json["width"] as? NSNumber)?.doubleValue, let h = (r.json["height"] as? NSNumber)?.doubleValue, w > 0, h > 0 {
                    points = CGSize(width: w, height: h)
                }
            } else if image == nil {
                failed = true
            }
            try? await Task.sleep(for: .milliseconds(250))
        }
    }

    /// The screen's corners: about 55pt on the device (an iPhone's), whatever
    /// its shape (a Duo opened is wide), scaled to the view.
    private func corner(_ fit: CGSize) -> CGFloat {
        let device = min(55, min(points.width, points.height) * 0.137)
        return device * fit.width / max(points.width, 1)
    }

    private func fitted(in size: CGSize) -> CGSize {
        guard size.width > 0, size.height > 0 else { return .zero }
        let s = min(size.width / points.width, size.height / points.height)
        return CGSize(width: points.width * s, height: points.height * s)
    }

    /// A short touch taps; a longer move swipes, at the speed you moved.
    private func drag(_ fit: CGSize) -> some Gesture {
        DragGesture(minimumDistance: 0)
            .onChanged { _ in if dragStart == nil { dragStart = .now } }
            .onEnded { g in
                if pinched {
                    dragStart = nil
                    return
                }
                let k = points.width / max(fit.width, 1)
                let a = CGPoint(x: g.startLocation.x * k, y: g.startLocation.y * k)
                let b = CGPoint(x: g.location.x * k, y: g.location.y * k)
                let ms = Int((Date.now.timeIntervalSince(dragStart ?? .now)) * 1000)
                dragStart = nil
                Task {
                    if hypot(b.x - a.x, b.y - a.y) < 8 {
                        _ = await model.simulatorCall(paneID, "tap", ["x": a.x, "y": a.y])
                    } else {
                        _ = await model.simulatorCall(paneID, "swipe", ["x": a.x, "y": a.y, "x2": b.x, "y2": b.y, "ms": min(max(ms, 120), 1500)])
                    }
                }
            }
    }

    /// Two fingers: a pinch on the device, played once you let go (the
    /// device's XCTest runner synthesizes whole gestures).
    private func magnify(_ fit: CGSize) -> some Gesture {
        MagnifyGesture()
            .onChanged { g in
                pinched = true
                if pinch == nil { pinch = (.now, g.startAnchor, 1) }
                pinch?.scale = g.magnification
            }
            .onEnded { g in
                defer {
                    pinch = nil
                    // Past the drag's own end, whichever comes first.
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) { pinched = false }
                }
                guard let p = pinch, abs(g.magnification - 1) > 0.05 else { return }
                let ms = Int(Date.now.timeIntervalSince(p.start) * 1000)
                Task {
                    _ = await model.simulatorCall(paneID, "pinch", ["x": p.anchor.x * points.width, "y": p.anchor.y * points.height,
                                                                     "scale": g.magnification, "ms": min(max(ms, 250), 800)])
                }
            }
    }

    private func button(_ name: String) {
        Task { _ = await model.simulatorCall(paneID, "button", ["name": name]) }
    }

}

/// A simulator screenshot waiting to be shared.
struct SimulatorShot: Identifiable {
    let id = UUID()
    let image: UIImage
}

/// The system share sheet (Save Image, Copy, AirDrop…).
struct ActivitySheet: UIViewControllerRepresentable {
    let items: [Any]

    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: items, applicationActivities: nil)
    }

    func updateUIViewController(_ vc: UIActivityViewController, context: Context) {}
}
