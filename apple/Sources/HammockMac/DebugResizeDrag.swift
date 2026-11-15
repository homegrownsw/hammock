import AppKit
import HammockKit

/// Dev aid: the debug director's `drag SECONDS` (HMK_DEBUG_DIRECTOR) resizes
/// the main window on every display frame, as a drag of its corner would,
/// shrinking it by up to 300 by 150 points and back once a second, then
/// writes to stderr how the main thread kept up: the time between frames
/// (a hitch is one over 1.5 refreshes), the time each resize took to lay
/// out and draw, the terminals' whole updateLayer, and the resizes sent to
/// the daemon. It isn't a real live resize (inLiveResize stays false): the
/// terminals are told one starts and ends, as AppKit tells every view, and
/// other views aren't.
@MainActor
final class ResizeDrag: NSObject {
    /// Told each resize sent to the daemon.
    static var resizeSent: (() -> Void)?
    /// Told the seconds of each terminal updateLayer, the whole of it.
    static var layerTiming: ((CFTimeInterval) -> Void)?

    private let window: NSWindow
    private let start: NSRect
    private let seconds: Double
    private var link: CADisplayLink?
    private var t0: CFTimeInterval = 0
    private var last: CFTimeInterval = 0
    private var gaps: [Double] = [], steps: [Double] = [], layers: [Double] = []
    private var resizes = 0
    private var done: CheckedContinuation<Void, Never>?

    private init(window: NSWindow, seconds: Double) {
        self.window = window
        start = window.frame
        self.seconds = seconds
    }

    static func run(seconds: Double) async {
        guard let w = NSApp.windows.first(where: { $0.isVisible && $0.isMainWindow })
            ?? NSApp.windows.first(where: { $0.isVisible && !($0 is NSPanel) && $0.frame.width > 400 }) else {
            FileHandle.standardError.write(Data("ResizeDrag: no window\n".utf8))
            return
        }
        let d = ResizeDrag(window: w, seconds: seconds)
        await d.go()
    }

    private func go() async {
        Self.resizeSent = { [weak self] in self?.resizes += 1 }
        Self.layerTiming = { [weak self] s in self?.layers.append(s * 1000) }
        let terminals = Self.terminals(in: window.contentView)
        for t in terminals { t.viewWillStartLiveResize() }
        await withCheckedContinuation { c in
            done = c
            let l = window.contentView!.displayLink(target: self, selector: #selector(tick(_:)))
            l.add(to: .main, forMode: .common)
            link = l
        }
        Self.resizeSent = nil
        Self.layerTiming = nil
        for t in terminals { t.viewDidEndLiveResize() }
        window.setFrame(start, display: true)
        let refresh = 1000 / Double(window.screen?.maximumFramesPerSecond ?? 60)
        let hitches = gaps.filter { $0 > refresh * 1.5 }
        func s(_ ms: [Double]) -> String {
            let st = RenderBench.Stats(ms)
            return String(format: "p50 %.2f p99 %.2f max %.2f mean %.2f", st.p50, st.p99, st.max, st.mean)
        }
        let report = """
        ResizeDrag: \(gaps.count) frames in \(String(format: "%.1f", seconds)) s (refresh \(String(format: "%.1f", refresh)) ms), \
        \(hitches.count) hitches, \(String(format: "%.0f", hitches.reduce(0) { $0 + $1 - refresh })) ms lost
          frame gap ms: \(s(gaps))
          resize step ms: \(s(steps))
          terminal updateLayer ms (\(layers.count)): \(s(layers))
          resizes sent: \(resizes)

        """
        FileHandle.standardError.write(Data(report.utf8))
    }

    private static func terminals(in v: NSView?) -> [TerminalNSView] {
        guard let v else { return [] }
        return (v as? TerminalNSView).map { [$0] } ?? v.subviews.flatMap { terminals(in: $0) }
    }

    @objc private func tick(_ l: CADisplayLink) {
        let now = CACurrentMediaTime()
        if t0 == 0 { t0 = now; last = now }
        let t = now - t0
        if t > 0.001 { gaps.append((now - last) * 1000) }
        last = now
        guard t < seconds else {
            l.invalidate()
            done?.resume()
            done = nil
            return
        }
        let k = (1 - cos(t * 2 * .pi)) / 2 // 0 → 1 → 0 each second
        var f = start
        f.size.width -= (300 * k).rounded()
        f.size.height -= (150 * k).rounded()
        f.origin.y += start.height - f.height // the top left stays put
        let a = CACurrentMediaTime()
        window.setFrame(f, display: true)
        steps.append((CACurrentMediaTime() - a) * 1000)
    }
}
