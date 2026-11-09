import HammockKit
import SwiftUI
import UIKit

/// A page watched as video, on the phone: pinch to zoom (one finger then
/// pans the zoomed view); fitted, one finger scrolls the page itself, with
/// momentum. A tap clicks and a long press right-clicks, wherever the
/// zoom is. Positions go out as fractions of the page. The view fills the
/// space it's given: the page sits fitted in its middle, and zooms into all of it.
struct RemotePageView: UIViewRepresentable {
    let stream: MediaStream
    /// The page's size in pixels (its aspect is what matters).
    let pageSize: CGSize
    /// op, args: as AppModel.browserInput.
    let input: (String, [String: Any]) -> Void

    func makeUIView(context: Context) -> PageScrollView {
        let v = PageScrollView()
        v.page.hosted = stream.layer
        v.input = input
        v.pageSize = pageSize
        return v
    }

    func updateUIView(_ v: PageScrollView, context: Context) {
        v.page.hosted = stream.layer
        v.input = input
        v.pageSize = pageSize
    }

    final class PageScrollView: UIScrollView, UIScrollViewDelegate, UIGestureRecognizerDelegate {
        let page = MediaView.LayerView()
        var input: ((String, [String: Any]) -> Void)?
        private let scroller = UIPanGestureRecognizer()
        private var momentum: CADisplayLink?
        private var velocity = CGPoint.zero
        var pageSize = CGSize.zero {
            didSet { if pageSize != oldValue { setNeedsLayout() } }
        }
        /// The bounds and page size the page was last fitted to.
        private var fittedFor: (CGSize, CGSize)?

        override init(frame: CGRect) {
            super.init(frame: frame)
            delegate = self
            minimumZoomScale = 1
            maximumZoomScale = 4
            bouncesZoom = true
            showsHorizontalScrollIndicator = false
            showsVerticalScrollIndicator = false
            contentInsetAdjustmentBehavior = .never
            isScrollEnabled = false // until zoomed in
            addSubview(page)
            scroller.maximumNumberOfTouches = 1
            scroller.delegate = self
            scroller.addTarget(self, action: #selector(scroll(_:)))
            page.addGestureRecognizer(scroller)
            let tap = UITapGestureRecognizer(target: self, action: #selector(tap(_:)))
            page.addGestureRecognizer(tap)
            let hold = UILongPressGestureRecognizer(target: self, action: #selector(hold(_:)))
            hold.minimumPressDuration = 0.5
            page.addGestureRecognizer(hold)
            tap.require(toFail: hold)
        }

        required init?(coder: NSCoder) { fatalError() }

        override func layoutSubviews() {
            super.layoutSubviews()
            // A new size (rotation, a resized page) starts again from fitted.
            if fittedFor.map({ $0.0 != bounds.size || $0.1 != pageSize }) ?? true {
                fittedFor = (bounds.size, pageSize)
                setZoomScale(1, animated: false)
                page.frame = CGRect(origin: .zero, size: fitted())
                contentSize = page.frame.size
                scrollViewDidZoom(self)
            }
            center()
        }

        /// The page's size fitted inside the view.
        private func fitted() -> CGSize {
            guard bounds.width > 0, bounds.height > 0, pageSize.width > 0, pageSize.height > 0 else { return bounds.size }
            let s = min(bounds.width / pageSize.width, bounds.height / pageSize.height)
            return CGSize(width: pageSize.width * s, height: pageSize.height * s)
        }

        /// Keeps the page centred while it's smaller than the view in either direction.
        private func center() {
            let x = max(0, (bounds.width - contentSize.width) / 2)
            let y = max(0, (bounds.height - contentSize.height) / 2)
            let inset = UIEdgeInsets(top: y, left: x, bottom: y, right: x)
            if contentInset != inset { contentInset = inset }
        }

        func viewForZooming(in scrollView: UIScrollView) -> UIView? { page }

        func scrollViewDidZoom(_ scrollView: UIScrollView) {
            center()
            // Zoomed in, one finger pans the view; fitted, it scrolls the page.
            isScrollEnabled = zoomScale > 1.01
            scroller.isEnabled = !isScrollEnabled
        }

        /// A point in the page view as fractions of the page.
        private func fraction(_ p: CGPoint) -> [String: Any] {
            let b = page.bounds
            guard b.width > 0, b.height > 0 else { return ["x": 0, "y": 0] }
            return ["x": min(max(p.x / b.width, 0), 1), "y": min(max(p.y / b.height, 0), 1)]
        }

        private func click(_ p: CGPoint, button: String) {
            stopMomentum()
            let at = fraction(p)
            input?("mouse", at.merging(["type": "down", "clicks": 1, "button": button]) { a, _ in a })
            input?("mouse", at.merging(["type": "up", "clicks": 1, "button": button]) { a, _ in a })
        }

        @objc private func tap(_ g: UITapGestureRecognizer) {
            click(g.location(in: page), button: "left")
        }

        @objc private func hold(_ g: UILongPressGestureRecognizer) {
            if g.state == .began { click(g.location(in: page), button: "right") }
        }

        private func wheel(_ dx: CGFloat, _ dy: CGFloat) {
            let b = page.bounds
            guard b.width > 0, b.height > 0, dx != 0 || dy != 0 else { return }
            input?("wheel", anchor.merging(["dx": dx / b.width, "dy": dy / b.height]) { a, _ in a })
        }

        /// Where a scroll started, as page fractions: the wheel events' position.
        private var scrollStart = CGPoint.zero
        private var anchor: [String: Any] { ["x": scrollStart.x, "y": scrollStart.y] }

        @objc private func scroll(_ g: UIPanGestureRecognizer) {
            switch g.state {
            case .began:
                stopMomentum()
                let f = fraction(g.location(in: page))
                scrollStart = CGPoint(x: f["x"] as? CGFloat ?? 0, y: f["y"] as? CGFloat ?? 0)
            case .changed:
                let t = g.translation(in: page)
                g.setTranslation(.zero, in: page)
                wheel(-t.x, -t.y)
            case .ended:
                let v = g.velocity(in: page)
                velocity = CGPoint(x: -v.x, y: -v.y)
                startMomentum()
            default:
                break
            }
        }

        // Momentum: the page keeps scrolling after a flick, slowing as
        // UIScrollView does (about 0.998 per millisecond).
        private func startMomentum() {
            guard hypot(velocity.x, velocity.y) > 60 else { return }
            let link = CADisplayLink(target: self, selector: #selector(glide(_:)))
            link.add(to: .main, forMode: .common)
            momentum = link
        }

        @objc private func glide(_ link: CADisplayLink) {
            let dt = link.targetTimestamp - link.timestamp
            wheel(velocity.x * dt, velocity.y * dt)
            let k = pow(0.998, dt * 1000)
            velocity = CGPoint(x: velocity.x * k, y: velocity.y * k)
            if hypot(velocity.x, velocity.y) < 20 { stopMomentum() }
        }

        private func stopMomentum() {
            momentum?.invalidate()
            momentum = nil
        }

        override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent?) {
            stopMomentum() // a touch stops a glide, as in any scroll view
            super.touchesBegan(touches, with: event)
        }

        override func willMove(toWindow w: UIWindow?) {
            if w == nil { stopMomentum() }
        }
    }
}
