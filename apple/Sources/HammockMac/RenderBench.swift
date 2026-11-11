import AppKit
import HammockKit

/// Measures the terminal renderer (issue #18: is CoreText what limits us?).
///
/// HMK_DEBUG_RENDER_BENCH=<file.json> draws fixed screens offscreen, at 2x,
/// through the pane's real drawing code, then writes the timings to the file
/// (and a table to stderr), the last frame of each screen beside it
/// (`<file>-<screen>.png`, to compare renderers by eye), and quits. Each
/// draw is split in two: `frame` (copying the screen out of libghostty into
/// Swift cells) and `draw` (drawing those cells). Between draws the screen
/// changes as it would under output: a new line scrolls in, a TUI redraws, or
/// a key is typed. `just render-bench` runs it on this checkout's β.
///
/// It leaves out what happens after drawing (Core Animation uploading the
/// layer, the display's own latency): keystroke-to-photon (#19) is for that.
/// HMK_DEBUG_RENDER_STATS=<file> times every real draw instead, appending a
/// JSON line every two seconds while panes draw, for a pane under real load
/// (`cat` of a big file, say).
@MainActor
enum RenderBench {
    struct Screen {
        let name: String
        let cols: Int
        let rows: Int
        /// What the screen shows before the first draw.
        let setup: String
        /// The bytes written before draw n.
        let step: (Int) -> String
    }

    struct Stats: Encodable {
        let p50: Double, p99: Double, mean: Double, max: Double

        init(_ ms: [Double]) {
            let s = ms.sorted()
            func at(_ q: Double) -> Double { s.isEmpty ? 0 : s[min(s.count - 1, Int((Double(s.count) * q).rounded(.down)))] }
            p50 = at(0.5)
            p99 = at(0.99)
            mean = s.isEmpty ? 0 : s.reduce(0, +) / Double(s.count)
            max = s.last ?? 0
        }
    }

    /// One way of drawing a screen: the whole pane each time, or only the
    /// rows output changed (with the cursor's), as the pane does.
    struct Run: Encodable {
        let frameMs: Stats, drawMs: Stats, totalMs: Stats
        /// Rows drawn per draw, on average.
        let rows: Double
        /// Rows whose layout was kept from an earlier draw (TerminalLineLayout).
        let cacheHits: Double
    }

    struct Result: Encodable {
        let screen: String
        let cols: Int, rows: Int
        let pixels: String
        let draws: Int
        let full: Run
        let changedRows: Run
        /// Whether the last frame drawn row by row is the same, pixel for
        /// pixel, as the one drawn whole: a missed row would show here.
        let same: Bool
    }

    struct Report: Encodable {
        let renderer: String
        let machine: String
        let os: String
        let font: String
        let scale: Int
        let date: String
        let results: [Result]
    }

    static func runIfRequested() {
        guard let out = ProcessInfo.processInfo.environment["HMK_DEBUG_RENDER_BENCH"], !out.isEmpty else { return }
        let draws = Int(ProcessInfo.processInfo.environment["HMK_DEBUG_RENDER_BENCH_DRAWS"] ?? "") ?? 120
        let only = ProcessInfo.processInfo.environment["HMK_DEBUG_RENDER_BENCH_ONLY"].map { Set($0.split(separator: ",").map(String.init)) }
        let base = (out as NSString).deletingPathExtension
        var results: [Result] = []
        for screen in makeScreens(draws: draws) where only?.contains(screen.name) ?? true {
            guard let (full, fullImage) = measure(screen, draws: draws, changedRows: false),
                  let (rows, rowsImage) = measure(screen, draws: draws, changedRows: true) else { continue }
            if let png = NSBitmapImageRep(cgImage: fullImage).representation(using: .png, properties: [:]) {
                try? png.write(to: URL(fileURLWithPath: "\(base)-\(screen.name).png"))
            }
            let same = fullImage.dataProvider?.data == rowsImage.dataProvider?.data
            if !same, let png = NSBitmapImageRep(cgImage: rowsImage).representation(using: .png, properties: [:]) {
                try? png.write(to: URL(fileURLWithPath: "\(base)-\(screen.name)-rows.png"))
            }
            results.append(Result(screen: screen.name, cols: screen.cols, rows: screen.rows,
                                  pixels: "\(fullImage.width)x\(fullImage.height)", draws: draws, full: full, changedRows: rows, same: same))
        }

        let (font, _) = TerminalFont.resolve()
        let report = Report(renderer: "coretext", machine: sysctl("hw.model"), os: ProcessInfo.processInfo.operatingSystemVersionString,
                            font: "\(font.fontName) \(font.pointSize)pt", scale: Int(scale), date: ISO8601DateFormatter().string(from: Date()), results: results)
        let enc = JSONEncoder()
        enc.outputFormatting = [.prettyPrinted, .sortedKeys]
        if let data = try? enc.encode(report) { try? data.write(to: URL(fileURLWithPath: out)) }

        var table = "renderer \(report.renderer), \(report.machine), \(report.font), @\(report.scale)x, ms per draw (p50 / p99)\n"
        table += "screen".padding(toLength: 22, withPad: " ", startingAt: 0)
            + "grid      frame          whole pane       changed rows     rows/draw  cached  same\n"
        for r in results {
            func pair(_ s: Stats) -> String { String(format: "%6.2f / %-6.2f", s.p50, s.p99) }
            table += r.screen.padding(toLength: 22, withPad: " ", startingAt: 0)
                + "\(r.cols)x\(r.rows)".padding(toLength: 10, withPad: " ", startingAt: 0)
                + pair(r.full.frameMs) + " " + pair(r.full.totalMs) + "  " + pair(r.changedRows.totalMs) + "  "
                + String(format: "%6.1f", r.changedRows.rows) + String(format: "  %5.0f%%", r.full.cacheHits * 100) + "    " + (r.same ? "yes" : "NO") + "\n"
        }
        FileHandle.standardError.write(Data(table.utf8))
        exit(0)
    }

    private static let scale: CGFloat = 2

    /// Draws a screen draws times (after ten to warm up), each after its
    /// next step of output, and returns the timings and the last frame.
    private static func measure(_ screen: Screen, draws: Int, changedRows: Bool) -> (Run, CGImage)? {
        let warmup = 10
        let session = PaneSession.standalone(paneID: "bench-\(screen.name)", cols: screen.cols, rows: screen.rows)
        let view = TerminalNSView(frame: .zero)
        view.session = session
        defer { view.session = nil }
        let size = NSSize(width: CGFloat(screen.cols) * view.cellWidth + view.padding * 2,
                          height: CGFloat(screen.rows) * view.cellHeight + view.padding * 2)
        view.setFrameSize(size)
        session.engine.write(Data(screen.setup.utf8))

        let w = Int(size.width * scale), h = Int(size.height * scale)
        guard let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: 0,
                                  space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                  bitmapInfo: CGImageAlphaInfo.premultipliedFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue) else { return nil }
        // Flipped, in points, as AppKit hands a flipped view its context.
        ctx.translateBy(x: 0, y: CGFloat(h))
        ctx.scaleBy(x: scale, y: -scale)
        let gc = NSGraphicsContext(cgContext: ctx, flipped: true)

        var frameMs: [Double] = [], drawMs: [Double] = [], totalMs: [Double] = [], rows: [Double] = []
        TerminalNSView.timing = { f, d in
            frameMs.append(f * 1000)
            drawMs.append(d * 1000)
            totalMs.append((f + d) * 1000)
        }
        defer { TerminalNSView.timing = nil }
        var counted = (hits: 0, misses: 0)
        for i in 0..<(warmup + draws) {
            if i == warmup { frameMs = []; drawMs = []; totalMs = []; rows = []; counted = view.layoutCounts }
            if i > 0 { session.engine.write(Data(screen.step(i - 1).utf8)) }
            // The first draw is whole either way: there's nothing there yet.
            let changes = session.engine.takeChanges()
            let rects = (changedRows && i > 0 ? view.rectsToRedraw(changes) : nil) ?? [view.bounds]
            let rowCount = rects.reduce(0.0) { $0 + ($1.height >= view.bounds.height ? Double(screen.rows) : 1) }
            rows.append(Swift.min(rowCount, Double(screen.rows)))
            guard !rects.isEmpty else { continue }
            view.benchRects = rects
            NSGraphicsContext.saveGraphicsState()
            NSGraphicsContext.current = gc
            // AppKit clips a view's drawing to the rects it asked for.
            ctx.saveGState()
            ctx.clip(to: rects)
            view.draw(rects.reduce(rects[0]) { $0.union($1) })
            ctx.restoreGState()
            NSGraphicsContext.restoreGraphicsState()
        }
        view.benchRects = nil
        let end = view.layoutCounts
        let looked = (end.hits - counted.hits) + (end.misses - counted.misses)
        guard let image = ctx.makeImage() else { return nil }
        return (Run(frameMs: Stats(frameMs), drawMs: Stats(drawMs), totalMs: Stats(totalMs),
                    rows: rows.isEmpty ? 0 : rows.reduce(0, +) / Double(rows.count),
                    cacheHits: looked == 0 ? 0 : Double(end.hits - counted.hits) / Double(looked)), image)
    }

    private static func sysctl(_ name: String) -> String {
        var size = 0
        sysctlbyname(name, nil, &size, nil, 0)
        var buf = [CChar](repeating: 0, count: max(size, 1))
        sysctlbyname(name, &buf, &size, nil, 0)
        return String(cString: buf)
    }

    // MARK: Screens

    /// The same every run, so runs (and renderers) compare.
    private struct Rand {
        var s: UInt64
        mutating func next(_ n: Int) -> Int {
            s ^= s << 13; s ^= s >> 7; s ^= s << 17
            return Int(s % UInt64(n))
        }
    }

    private static let words = ["func", "return", "let", "var", "if", "else", "for", "in", "self", "guard", "nil", "true", "false",
                                "session", "engine", "frame()", "cells", "row", "col", "draw", "{", "}", "(", ")", "=", "==", "+=",
                                "// comment", "\"string\"", "0x2580", "42", "[Int]", "CGFloat", "NSView", "->", ":", ",", "."]

    /// A line of code-like words exactly `cols` wide, coloured or not.
    private static func line(_ r: inout Rand, cols: Int, colour: Bool) -> String {
        var s = "", n = 0
        while n < cols {
            let w = words[r.next(words.count)]
            let piece = n + w.count + 1 <= cols ? w + " " : String(w.prefix(cols - n))
            if colour {
                switch r.next(10) {
                case 0: s += "\u{1b}[1;38;5;\(r.next(256))m"
                case 1: s += "\u{1b}[38;5;\(r.next(256));48;5;\(r.next(256))m"
                case 2: s += "\u{1b}[3;38;2;\(r.next(256));\(r.next(256));\(r.next(256))m"
                default: s += "\u{1b}[38;5;\(r.next(256))m"
                }
            }
            s += piece
            if colour { s += "\u{1b}[0m" }
            n += piece.count
        }
        return s
    }

    /// Wide (CJK), emoji, Nerd Font icons, powerline separators, box
    /// drawing, blocks and combining marks: the fallback-font paths.
    private static func unicodeLine(_ r: inout Rand, cols: Int) -> String {
        let pieces: [(String, Int)] = [("漢字", 4), ("日本語", 6), ("🚀", 2), ("✅", 2), ("👍🏽", 2), ("\u{E0B0}", 1), ("\u{E0B2}", 1),
                                       ("\u{F121}", 1), ("\u{F07B}", 1), ("\u{E725}", 1), ("─┼─", 3), ("│", 1), ("▁▃▅▇█", 5),
                                       ("é", 1), ("ñ", 1), ("a\u{0301}", 1), ("abc", 3), ("→", 1), (" ", 1)]
        var s = "", n = 0
        while true {
            let (p, w) = pieces[r.next(pieces.count)]
            guard n + w <= cols else { break }
            s += r.next(3) == 0 ? "\u{1b}[38;5;\(r.next(256))m\(p)\u{1b}[0m" : p
            n += w
        }
        return s
    }

    /// A full-screen program (htop, an editor): a status bar, boxes, rows
    /// with backgrounds, bars of blocks. Redrawn whole every step.
    private static func tui(_ r: inout Rand, cols: Int, rows: Int, tick: Int) -> String {
        var s = "\u{1b}[H\u{1b}[7m" + " Hammock bench  tick \(tick)".padding(toLength: cols, withPad: " ", startingAt: 0) + "\u{1b}[0m"
        for y in 2...rows {
            s += "\u{1b}[\(y);1H"
            if y == 2 || y == rows {
                s += (y == 2 ? "┌" : "└") + String(repeating: "─", count: cols - 2) + (y == 2 ? "┐" : "┘")
                continue
            }
            let bg = y % 2 == 0 ? "\u{1b}[48;5;236m" : ""
            let pct = (y * 7 + tick * 3) % 100
            let bar = String(repeating: "█", count: pct / 5) + String(repeating: "░", count: 20 - pct / 5)
            let label = String(format: "%5d  %-10@ %3d%% ", 1000 + y, words[r.next(words.count)] as NSString, pct)
            var body = bg + "│\u{1b}[1m" + label + "\u{1b}[22;38;5;\(70 + y % 50)m" + bar + "\u{1b}[39m "
            let used = 1 + label.count + 20 + 1
            body += line(&r, cols: cols - used - 1, colour: false) + "\u{1b}[0m│"
            s += body
        }
        return s
    }

    static func makeScreens(draws: Int) -> [Screen] {
        func scrolling(_ name: String, _ cols: Int, _ rows: Int, colour: Bool) -> Screen {
            var r = Rand(s: 0x9E3779B97F4A7C15)
            var lines: [String] = []
            for _ in 0..<(rows + 200) { lines.append(line(&r, cols: cols, colour: colour)) }
            return Screen(name: name, cols: cols, rows: rows, setup: lines[0..<rows].joined(separator: "\r\n"),
                          step: { i in "\r\n" + lines[rows + i % 200] })
        }
        // A flood: a whole screen of new lines between draws, none seen before.
        func flood(_ name: String, _ cols: Int, _ rows: Int) -> Screen {
            var r = Rand(s: 0x5851F42D4C957F2D)
            let screens = (0...(draws + 10)).map { _ in (0..<rows).map { _ in line(&r, cols: cols, colour: true) }.joined(separator: "\r\n") }
            return Screen(name: name, cols: cols, rows: rows, setup: screens[0], step: { i in "\r\n" + screens[min(i + 1, screens.count - 1)] })
        }
        var u = Rand(s: 0xD1B54A32D192ED03)
        var uni: [String] = []
        for _ in 0..<260 { uni.append(unicodeLine(&u, cols: 200)) }
        var t = Rand(s: 0x94D049BB133111EB)
        var tuis: [String] = []
        for i in 0..<16 { tuis.append(tui(&t, cols: 200, rows: 60, tick: i)) }
        var p = Rand(s: 0xBF58476D1CE4E5B9)
        let history = (0..<12).map { _ in "\u{1b}[32m~/Developer/app\u{1b}[0m $ " + line(&p, cols: 60, colour: false) }.joined(separator: "\r\n")
        let typed = Array("git commit -m \"Draw the terminal with Metal and a glyph cache\"")
        var c = Rand(s: 0x2545F4914F6CDD1D)
        let colourful = (0..<59).map { _ in line(&c, cols: 200, colour: true) }.joined(separator: "\r\n")
        func typing(_ i: Int) -> String { i % typed.count == typed.count - 1 ? "\u{1b}[\(typed.count - 1)D\u{1b}[K" : String(typed[i % typed.count]) }
        return [
            scrolling("plain-80x24", 80, 24, colour: false),
            scrolling("plain-200x60", 200, 60, colour: false),
            scrolling("plain-400x100", 400, 100, colour: false),
            scrolling("colour-200x60", 200, 60, colour: true),
            scrolling("colour-400x100", 400, 100, colour: true),
            flood("flood-200x60", 200, 60),
            flood("flood-400x100", 400, 100),
            Screen(name: "tui-200x60", cols: 200, rows: 60, setup: "\u{1b}[?1049h" + tuis[0], step: { i in tuis[(i + 1) % tuis.count] }),
            Screen(name: "unicode-200x60", cols: 200, rows: 60, setup: uni[0..<60].joined(separator: "\r\n"),
                   step: { i in "\r\n" + uni[60 + i % 200] }),
            // A shell with a little history, and someone typing: one cell
            // changes per draw, but the whole pane is drawn.
            Screen(name: "typing-200x60", cols: 200, rows: 60, setup: history + "\r\n\u{1b}[32m~/Developer/app\u{1b}[0m $ ",
                   step: typing),
            // The same keys at the bottom of a full screen of colour (a
            // coding agent's TUI, an editor).
            Screen(name: "typing-colour-200x60", cols: 200, rows: 60, setup: colourful + "\r\n\u{1b}[32m~/Developer/app\u{1b}[0m $ ",
                   step: typing),
        ]
    }
}

/// HMK_DEBUG_RENDER_STATS=<file>: every real terminal draw, and every write
/// of output into a pane's engine, timed; a JSON line appended every two
/// seconds in which anything happened (see RenderBench).
@MainActor
enum RenderStats {
    private static var frameMs: [Double] = []
    private static var drawMs: [Double] = []
    private static var writeMs = 0.0, writeBytes = 0, writes = 0, snapshots = 0
    private static var out: FileHandle?

    static func startIfRequested() {
        guard let path = ProcessInfo.processInfo.environment["HMK_DEBUG_RENDER_STATS"], !path.isEmpty else { return }
        if !FileManager.default.fileExists(atPath: path) { FileManager.default.createFile(atPath: path, contents: nil) }
        out = FileHandle(forWritingAtPath: path)
        out?.seekToEndOfFile()
        TerminalNSView.timing = { f, d in
            frameMs.append(f * 1000)
            drawMs.append(d * 1000)
        }
        PaneSession.writeTiming = { bytes, seconds, snapshot in
            writeMs += seconds * 1000
            writeBytes += bytes
            writes += 1
            if snapshot { snapshots += 1 }
        }
        let window = 2.0
        Timer.scheduledTimer(withTimeInterval: window, repeats: true) { _ in
            MainActor.assumeIsolated {
                guard !drawMs.isEmpty || writes > 0 else { return }
                let total = zip(frameMs, drawMs).map(+)
                let line: [String: Any] = [
                    "t": ISO8601DateFormatter().string(from: Date()),
                    "draws": total.count,
                    "drawsPerSec": Double(total.count) / window,
                    // Share of the main thread spent drawing terminals.
                    "busy": total.reduce(0, +) / (window * 1000),
                    "frameMs": stats(frameMs), "drawMs": stats(drawMs), "totalMs": stats(total),
                    // Output parsed into the engines, on the main thread too;
                    // snapshots are the daemon catching a lagging client up.
                    "writes": writes, "writeMBPerSec": Double(writeBytes) / 1_000_000 / window,
                    "writeBusy": writeMs / (window * 1000), "snapshots": snapshots,
                ]
                frameMs = []
                drawMs = []
                writeMs = 0; writeBytes = 0; writes = 0; snapshots = 0
                if let data = try? JSONSerialization.data(withJSONObject: line, options: [.sortedKeys]) {
                    out?.write(data + Data("\n".utf8))
                }
            }
        }
    }

    private static func stats(_ ms: [Double]) -> [String: Double] {
        let s = RenderBench.Stats(ms)
        return ["p50": s.p50, "p99": s.p99, "mean": s.mean, "max": s.max]
    }
}
