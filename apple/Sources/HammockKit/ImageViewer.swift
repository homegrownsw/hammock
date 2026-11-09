import SwiftUI

/// A picture from the agent view, opened to look at closely: it fits the
/// screen, and you zoom (pinch, double-tap or double-click) and pan around.
struct ImageViewer: View {
    let image: PlatformImage
    @Environment(\.dismiss) private var dismiss
    #if os(macOS)
    /// Worked out once, on opening: the sheet becomes the key window, and
    /// sizing to it again would shrink it each time.
    private let sheetSize: CGSize

    init(image: PlatformImage) {
        self.image = image
        sheetSize = Self.size(for: image)
    }
    #endif

    var body: some View {
        #if os(iOS)
        ZStack(alignment: .topTrailing) {
            Color.black.ignoresSafeArea()
            ZoomingImage(image: image).ignoresSafeArea()
            Button { dismiss() } label: {
                Image(systemName: "xmark").font(.body.weight(.semibold)).frame(width: 22, height: 22)
            }
            .agentGlassButton()
            .buttonBorderShape(.circle)
            .padding()
            .accessibilityLabel("Close")
        }
        .statusBarHidden()
        #else
        VStack(spacing: 0) {
            ZoomingImage(image: image)
            HStack {
                Text("Pinch or double-click to zoom").font(.caption).foregroundStyle(.secondary)
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.cancelAction)
            }
            .padding(10)
        }
        // A sheet doesn't take an ideal size, only a fixed one: without it,
        // the sheet stays at its minimum width, a strip for a wide picture.
        .frame(width: sheetSize.width, height: sheetSize.height)
        #endif
    }

    #if os(macOS)
    /// The picture at its own size, plus the bar under it, shrunk to fit
    /// inside the window it opens over (or the screen, without one).
    private static func size(for image: NSImage) -> CGSize {
        let bar: CGFloat = 44, margin: CGFloat = 60
        // The main window: a sheet never becomes main, so this is the one it opens over.
        let room = (NSApp.mainWindow ?? NSApp.keyWindow)?.contentLayoutRect.size
            ?? NSScreen.main?.visibleFrame.size ?? CGSize(width: 1200, height: 900)
        let maxW = max(480, room.width - margin * 2), maxH = max(360, room.height - margin * 2)
        let pic = image.size
        guard pic.width > 0, pic.height > 0 else { return CGSize(width: 480, height: 360) }
        let k = min(1, maxW / pic.width, (maxH - bar) / pic.height)
        return CGSize(width: max(480, (pic.width * k).rounded()), height: max(360, (pic.height * k).rounded() + bar))
    }
    #endif
}

#if os(iOS)
import UIKit

/// The picture in a zooming scroll view: fitted, pinch up to 8×, a
/// double-tap zooms to where you tapped (or back out), and it stays centred
/// while smaller than the screen.
private struct ZoomingImage: UIViewRepresentable {
    let image: UIImage

    func makeUIView(context: Context) -> Scroll {
        let v = Scroll(image: image)
        return v
    }

    func updateUIView(_ v: Scroll, context: Context) {}

    final class Scroll: UIScrollView, UIScrollViewDelegate {
        private let imageView: UIImageView
        private var fitted = CGSize.zero

        init(image: UIImage) {
            imageView = UIImageView(image: image)
            super.init(frame: .zero)
            delegate = self
            showsVerticalScrollIndicator = false
            showsHorizontalScrollIndicator = false
            decelerationRate = .fast
            // At normal size a downward drag dismisses the viewer (the zoom
            // transition's own gesture): no vertical bounce to take it.
            alwaysBounceVertical = false
            alwaysBounceHorizontal = false
            contentInsetAdjustmentBehavior = .never
            addSubview(imageView)
            let tap = UITapGestureRecognizer(target: self, action: #selector(doubleTapped(_:)))
            tap.numberOfTapsRequired = 2
            addGestureRecognizer(tap)
        }

        required init?(coder: NSCoder) { fatalError() }

        override func layoutSubviews() {
            super.layoutSubviews()
            guard let img = imageView.image, bounds.width > 0, img.size.width > 0 else { return }
            if fitted != bounds.size {
                // The picture fitted to the screen is 1×.
                fitted = bounds.size
                let k = min(bounds.width / img.size.width, bounds.height / img.size.height)
                zoomScale = 1
                imageView.frame = CGRect(origin: .zero, size: CGSize(width: img.size.width * k, height: img.size.height * k))
                contentSize = imageView.frame.size
                minimumZoomScale = 1
                maximumZoomScale = max(8, 1 / k * 2)
            }
            centre()
        }

        func viewForZooming(in scrollView: UIScrollView) -> UIView? { imageView }
        func scrollViewDidZoom(_ scrollView: UIScrollView) { centre() }

        /// While smaller than the screen, the picture sits in its middle.
        private func centre() {
            let x = max(0, (bounds.width - contentSize.width) / 2), y = max(0, (bounds.height - contentSize.height) / 2)
            contentInset = UIEdgeInsets(top: y, left: x, bottom: y, right: x)
        }

        @objc private func doubleTapped(_ g: UITapGestureRecognizer) {
            if zoomScale > minimumZoomScale * 1.05 {
                setZoomScale(minimumZoomScale, animated: true)
                return
            }
            let p = g.location(in: imageView), k = min(maximumZoomScale, 3)
            let size = CGSize(width: bounds.width / k, height: bounds.height / k)
            zoom(to: CGRect(x: p.x - size.width / 2, y: p.y - size.height / 2, width: size.width, height: size.height), animated: true)
        }
    }
}
#else
import AppKit

/// The picture in a magnifying scroll view: fitted, pinch on the trackpad
/// (or double-click) to zoom, scroll to pan.
private struct ZoomingImage: NSViewRepresentable {
    let image: NSImage

    func makeNSView(context: Context) -> Scroll {
        let s = Scroll()
        s.hasVerticalScroller = true
        s.hasHorizontalScroller = true
        s.autohidesScrollers = true
        s.allowsMagnification = true
        s.maxMagnification = 8
        s.drawsBackground = false
        s.contentView = CentringClip()
        let v = NSImageView(image: image)
        v.imageScaling = .scaleProportionallyUpOrDown
        v.frame = CGRect(origin: .zero, size: image.size)
        s.documentView = v
        return s
    }

    func updateNSView(_ s: Scroll, context: Context) {}

    final class Scroll: NSScrollView {
        private var fittedTo = CGSize.zero

        override func layout() {
            super.layout()
            guard let doc = documentView, bounds.width > 0, fittedTo != bounds.size else { return }
            fittedTo = bounds.size
            // Fitted (never larger than the picture itself) to start.
            let k = min(1, min(bounds.width / doc.frame.width, bounds.height / doc.frame.height))
            minMagnification = min(k, 1)
            magnification = k
        }

        override func mouseDown(with event: NSEvent) {
            guard event.clickCount == 2, let doc = documentView else { return super.mouseDown(with: event) }
            let p = doc.convert(event.locationInWindow, from: nil)
            let fit = minMagnification
            setMagnification(magnification > fit * 1.05 ? fit : min(maxMagnification, max(fit * 3, 1)), centeredAt: p)
        }
    }

    /// Keeps a picture smaller than the view in its middle.
    final class CentringClip: NSClipView {
        override func constrainBoundsRect(_ proposed: NSRect) -> NSRect {
            var r = super.constrainBoundsRect(proposed)
            guard let doc = documentView else { return r }
            if r.width > doc.frame.width { r.origin.x = (doc.frame.width - r.width) / 2 }
            if r.height > doc.frame.height { r.origin.y = (doc.frame.height - r.height) / 2 }
            return r
        }
    }
}
#endif
