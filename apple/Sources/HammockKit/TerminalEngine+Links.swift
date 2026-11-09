import Foundation
import GhosttyVt

/// A link on screen: a program's hyperlink (OSC 8) or a URL in the text,
/// and the cells it covers (a URL can wrap onto the next row).
public struct TerminalLink: Equatable, Sendable {
    public struct Span: Equatable, Sendable {
        public var row: Int
        public var cols: Range<Int>
    }

    public var url: String
    public var spans: [Span]
    /// Set by the program (OSC 8), so its text may say something other
    /// than where it goes; else a URL found in the text, which shows itself.
    public var hyperlink = false

    public func contains(x: Int, y: Int) -> Bool { spans.contains { $0.row == y && $0.cols.contains(x) } }
}

extension TerminalEngine {
    /// The link at a viewport cell, if any: a hyperlink the program set
    /// there, else a URL in the text around it.
    public func link(atX x: Int, y: Int) -> TerminalLink? {
        guard x >= 0, y >= 0, x < cols, y < rows else { return nil }
        let f = frame()
        if let uri = hyperlinkURI(x: x, y: y) {
            // The run of cells on the row with the same link.
            var lo = x, hi = x
            while lo > 0, hyperlinkURI(x: lo - 1, y: y) == uri { lo -= 1 }
            while hi + 1 < cols, hyperlinkURI(x: hi + 1, y: y) == uri { hi += 1 }
            return TerminalLink(url: uri, spans: [.init(row: y, cols: lo..<(hi + 1))], hyperlink: true)
        }
        return Self.urlLink(in: f, x: x, y: y)
    }

    private func hyperlinkURI(x: Int, y: Int) -> String? {
        guard let terminal = handle else { return nil }
        var ref = GhosttyGridRef()
        ref.size = MemoryLayout<GhosttyGridRef>.size
        var pt = GhosttyPoint()
        pt.tag = GHOSTTY_POINT_TAG_VIEWPORT
        pt.value.coordinate = GhosttyPointCoordinate(x: UInt16(clamping: x), y: UInt32(y))
        guard ghostty_terminal_grid_ref(terminal, pt, &ref) == GHOSTTY_SUCCESS else { return nil }
        var len = 0
        var buf = [UInt8](repeating: 0, count: 512)
        var r = ghostty_grid_ref_hyperlink_uri(&ref, &buf, buf.count, &len)
        if r == GHOSTTY_OUT_OF_SPACE {
            buf = [UInt8](repeating: 0, count: len)
            r = ghostty_grid_ref_hyperlink_uri(&ref, &buf, buf.count, &len)
        }
        guard r == GHOSTTY_SUCCESS, len > 0 else { return nil }
        return String(decoding: buf[..<len], as: UTF8.self)
    }

    private static let urlPattern = try! NSRegularExpression(pattern: #"(?:https?|file)://[^\s<>"'`]+"#)

    /// A URL in the text at a cell. The row is read with the rows around it
    /// that it runs into (a full row continuing onto the next, as a wrapped
    /// URL does).
    static func urlLink(in f: TerminalFrame, x: Int, y: Int) -> TerminalLink? {
        // The run of rows: back while the row before is full, on while this one is.
        func full(_ r: Int) -> Bool { r >= 0 && r < f.cells.count && !(f.cells[r].last?.text.isEmpty ?? true) }
        var first = y, last = y
        while first > 0, full(first - 1) { first -= 1 }
        while last + 1 < f.cells.count, full(last) { last += 1 }
        // The text, and each character's cell.
        var text = ""
        var cellOf: [(row: Int, col: Int)] = []
        for r in first...last {
            for (c, cell) in f.cells[r].enumerated() where !cell.spacer {
                let s = cell.text.isEmpty ? " " : cell.text
                for _ in s.utf16 { cellOf.append((r, c)) }
                text += s
            }
        }
        let ns = text as NSString
        for m in urlPattern.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            var range = m.range
            // Not the punctuation after it (a sentence's full stop, a
            // bracket the URL didn't open).
            while range.length > 0 {
                let last = ns.substring(with: NSRange(location: range.location + range.length - 1, length: 1))
                let open = ns.substring(with: range).filter { $0 == "(" }.count, close = ns.substring(with: range).filter { $0 == ")" }.count
                if ".,;:!?'\"]}>".contains(last) || (last == ")" && close > open) { range.length -= 1 } else { break }
            }
            guard range.length > 0 else { continue }
            let cells = cellOf[range.location..<(range.location + range.length)]
            guard cells.contains(where: { $0.row == y && $0.col == x }) else { continue }
            var spans: [TerminalLink.Span] = []
            for (r, group) in Dictionary(grouping: cells, by: { $0.row }).sorted(by: { $0.key < $1.key }) {
                let cs = group.map(\.col)
                spans.append(.init(row: r, cols: cs.min()!..<(cs.max()! + 1)))
            }
            return TerminalLink(url: ns.substring(with: range), spans: spans)
        }
        return nil
    }
}
