import SwiftUI
#if canImport(UIKit)
import UIKit
#else
import AppKit
#endif

/// Measures how smooth the agent view is, in development builds, with
/// HMK_DEBUG_JANK set (to a file it appends a JSON line to every two
/// seconds; "1" logs to DevLog instead). Counted per window:
///
/// - frames and hitches: a frame later than one and a half of the display's
///   intervals, and the time they lost;
/// - snaps: rows in view that moved more than 30 pt in one frame while
///   nothing was scrolling (no finger on it, no animated scroll), the
///   content jumping rather than moving;
/// - bounces: rows moving one way then the other within a third of a
///   second, while nobody scrolls;
/// - shuffles: rows in view moving more than 12 pt apart in one frame (laid
///   out again, unanimated);
/// - rows rendered (AgentItemView bodies) and row geometry changes.
///
/// Off (the default) every hook here returns at once.
@MainActor
final class JankProbe {
    static let shared = JankProbe()
    /// HMK_DEBUG_JANK_ROWS: also follow each row's place (snaps, bounces,
    /// shuffles). It costs time itself, so leave it off when timing frames.
    nonisolated static let rows: Bool = enabled && ProcessInfo.processInfo.environment["HMK_DEBUG_JANK_ROWS"] != nil
    nonisolated static let enabled: Bool = {
        #if DEBUG || HMK_DEV
        return ProcessInfo.processInfo.environment["HMK_DEBUG_JANK"] != nil
        #else
        return false
        #endif
    }()

    private var out: FileHandle?
    private var logOnly = false
    private var link: AnyObject?
    private var started = false

    // Row positions (on screen, in the scroll view), by row id.
    private var rows: [String: CGRect] = [:]
    private var last: [String: CGRect] = [:]
    /// Each row's last movement while nothing scrolled: its sign and when.
    private var lastSign = 0
    private var lastSignAt: CFTimeInterval = 0
    /// The scroll view's height, and its content's.
    var viewport: CGFloat = 10_000
    var contentHeight: CGFloat = 0
    private var events = ProcessInfo.processInfo.environment["HMK_DEBUG_JANK_EVENTS"] != nil
    var userScrolling = false
    var animatingScroll = false

    // This window's counts.
    private var windowStart: CFTimeInterval = 0
    private var frames = 0, hitches = 0
    private var lostMs = 0.0, worstMs = 0.0
    private var snaps = 0, snapPx = 0.0, bounces = 0, shuffles = 0
    private var bodies = 0, geometry = 0
    private var lastFrame: CFTimeInterval = 0
    private var interval: CFTimeInterval = 1.0 / 60
    var note = ""

    func start() {
        guard Self.enabled, !started else { return }
        started = true
        let v = ProcessInfo.processInfo.environment["HMK_DEBUG_JANK"] ?? ""
        if v == "1" {
            logOnly = true
        } else {
            FileManager.default.createFile(atPath: v, contents: nil)
            out = FileHandle(forWritingAtPath: v)
            out?.seekToEndOfFile()
        }
        // Away (in the background, or another app in front) isn't a hitch:
        // the first frame back starts afresh.
        #if canImport(UIKit)
        let active = UIApplication.didBecomeActiveNotification
        #else
        let active = NSApplication.didBecomeActiveNotification
        #endif
        NotificationCenter.default.addObserver(forName: active, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.lastFrame = 0
                self?.last = [:]
                self?.note = "returned"
            }
        }
        #if canImport(UIKit)
        let l = CADisplayLink(target: Tick(self), selector: #selector(Tick.tick(_:)))
        l.add(to: .main, forMode: .common)
        link = l
        #else
        if let screen = NSScreen.main {
            let l = screen.displayLink(target: Tick(self), selector: #selector(Tick.tick(_:)))
            l.add(to: .main, forMode: .common)
            link = l
        }
        #endif
    }

    func body() { if Self.enabled { bodies += 1 } }

    func row(_ id: String, _ frame: CGRect) {
        guard Self.enabled else { return }
        rows[id] = frame
        geometry += 1
    }

    func gone(_ id: String) {
        guard Self.enabled else { return }
        rows[id] = nil
    }

    fileprivate func frame(_ now: CFTimeInterval, _ duration: CFTimeInterval) {
        if duration > 0 { interval = duration }
        if windowStart == 0 { windowStart = now }
        if lastFrame > 0 {
            let dt = now - lastFrame
            frames += 1
            if dt > 1 {
                // Away (the app in the background), not a hitch.
                lastFrame = now
                last = rows
                return
            }
            if dt > interval * 1.5 {
                hitches += 1
                lostMs += (dt - interval) * 1000
            }
            worstMs = max(worstMs, dt * 1000)
        }
        lastFrame = now
        if !userScrolling, !animatingScroll {
            // The rows in view that kept their height: how far they moved.
            var moves: [(id: String, dy: CGFloat)] = []
            for (id, r) in rows where r.maxY > 0 && r.minY < viewport {
                guard let p = last[id], abs(r.height - p.height) < 0.5 else { continue }
                moves.append((id, r.minY - p.minY))
            }
            let dys = moves.map(\.dy).sorted()
            if !dys.isEmpty {
                let m = dys[dys.count / 2]
                if abs(m) > 30 {
                    snaps += 1
                    snapPx += abs(m)
                    event("snap", m, moves.count)
                }
                if abs(m) > 0.5 {
                    let sign = m > 0 ? 1 : -1
                    if lastSign != 0, sign != lastSign, now - lastSignAt < 0.33 {
                        bounces += 1
                        event("bounce", m, moves.count)
                    }
                    lastSign = sign
                    lastSignAt = now
                }
                // Rows in view moving by different amounts: laid out again.
                if let lo = dys.first, let hi = dys.last, hi - lo > 12 {
                    shuffles += 1
                    event("shuffle", hi - lo, moves.count)
                }
            }
        }
        last = rows
        if now - windowStart >= 2 { flush(now) }
    }

    private func flush(_ now: CFTimeInterval) {
        defer {
            windowStart = now
            frames = 0; hitches = 0; lostMs = 0; worstMs = 0
            snaps = 0; snapPx = 0; bounces = 0; shuffles = 0; bodies = 0; geometry = 0
            note = ""
        }
        // Idle windows (nothing on screen moving or drawing) say nothing.
        guard hitches > 0 || snaps > 0 || bounces > 0 || bodies > 0 || geometry > 0 else { return }
        let line = String(format: #"{"t":%.1f,"frames":%d,"hitches":%d,"lostMs":%.0f,"worstMs":%.0f,"snaps":%d,"snapPx":%.0f,"bounces":%d,"shuffles":%d,"bodies":%d,"geometry":%d,"rows":%d,"note":"%@"}"#,
                          Date().timeIntervalSince1970, frames, hitches, lostMs, worstMs, snaps, snapPx, bounces, shuffles, bodies, geometry, rows.count, note)
        if logOnly {
            DevLog.agent("jank \(line)")
        } else {
            out?.write(Data((line + "\n").utf8))
        }
    }

    private func event(_ kind: String, _ value: CGFloat, _ rows: Int) {
        guard events else { return }
        let line = String(format: #"{"ev":"%@","t":%.3f,"v":%.1f,"rows":%d,"content":%.0f}"#, kind, Date().timeIntervalSince1970, value, rows, contentHeight)
        out?.write(Data((line + "\n").utf8))
    }

    private final class Tick: NSObject {
        weak var probe: JankProbe?
        init(_ p: JankProbe) { probe = p }
        #if canImport(UIKit)
        @objc func tick(_ l: CADisplayLink) {
            let p = probe
            MainActor.assumeIsolated { p?.frame(l.timestamp, l.targetTimestamp - l.timestamp) }
        }
        #else
        @objc func tick(_ l: CADisplayLink) {
            let p = probe
            MainActor.assumeIsolated { p?.frame(l.timestamp, l.targetTimestamp - l.timestamp) }
        }
        #endif
    }
}

extension View {
    /// Reports this row's place on screen to the jank probe (when on).
    @ViewBuilder
    func jankRow(_ id: String) -> some View {
        if JankProbe.rows {
            onGeometryChange(for: CGRect.self) { $0.frame(in: .scrollView) } action: { JankProbe.shared.row(id, $0) }
                .onDisappear { JankProbe.shared.gone(id) }
        } else {
            self
        }
    }

    /// Tells the jank probe when the scroll view is being scrolled (by a
    /// finger, or an animated scroll), so moving rows then don't count.
    @ViewBuilder
    func jankScroll() -> some View {
        if JankProbe.enabled {
            onScrollPhaseChange { _, phase in
                JankProbe.shared.userScrolling = phase == .tracking || phase == .interacting || phase == .decelerating
                JankProbe.shared.animatingScroll = phase == .animating
            }
            .onAppear { JankProbe.shared.start() }
        } else {
            self
        }
    }
}
