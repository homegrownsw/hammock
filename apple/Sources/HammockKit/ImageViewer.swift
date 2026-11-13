#if os(iOS)
import AVKit
import SwiftUI
import UIKit

/// A picture from the agent view, opened to look at closely: it fits the
/// screen, and you zoom (pinch or double-tap) and pan around. A video plays.
/// (The Mac opens them in QuickLook.)
struct ImageViewer: View {
    let image: PlatformImage
    var video: URL?
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ZStack(alignment: .topTrailing) {
            Color.black.ignoresSafeArea()
            if let video {
                VideoPlayer(player: AVPlayer(url: video)).ignoresSafeArea()
            } else {
                ZoomingImage(image: image).ignoresSafeArea()
            }
            Button { dismiss() } label: {
                Image(systemName: "xmark").font(.body.weight(.semibold)).frame(width: 22, height: 22)
            }
            .agentGlassButton()
            .buttonBorderShape(.circle)
            .padding()
            .accessibilityLabel("Close")
        }
        .statusBarHidden()
    }
}

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
#endif
