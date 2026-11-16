import CoreGraphics
import Foundation

// What a small, live picture of a tab draws, shared by the iPhone's Exposé
// and the Mac's tab preview.

extension PaneLayout {
    /// Each pane's rectangle in `rect`, splits divided by their weights.
    public func rects(in rect: CGRect, gap: CGFloat) -> [(String, CGRect)] {
        switch node {
        case .paneID(let id):
            return [(id, rect)]
        case .split(let s):
            let n = s.children.count
            guard n > 0 else { return [] }
            let side = s.direction != .vertical
            let w = s.weights.count == n ? s.weights.map { CGFloat($0) } : Array(repeating: 1 / CGFloat(n), count: n)
            let total = w.reduce(0, +)
            let span = (side ? rect.width : rect.height) - gap * CGFloat(n - 1)
            var at = side ? rect.minX : rect.minY
            var out: [(String, CGRect)] = []
            for (i, child) in s.children.enumerated() {
                let len = span * (total > 0 ? w[i] / total : 1 / CGFloat(n))
                let r = side ? CGRect(x: at, y: rect.minY, width: len, height: rect.height)
                    : CGRect(x: rect.minX, y: at, width: rect.width, height: len)
                out += child.rects(in: r, gap: gap)
                at += len + gap
            }
            return out
        case nil:
            return []
        }
    }
}

/// The latest of an agent's conversation, for a tile too small for it all.
public enum AgentTail {
    /// What a tile shows of a conversation: your messages, the agent's
    /// words and its tools.
    public static func shown(_ b: AgentBlock) -> Bool {
        switch b.kind {
        case .user, .text: !b.text.isEmpty
        case .tool: !b.title.isEmpty
        default: false
        }
    }

    /// Your last message, the agent's last two messages since and its last
    /// two tools, in order; before it the agent's last words before that,
    /// when it's said nothing since.
    public static func tail(_ blocks: [AgentBlock]) -> [AgentBlock] {
        let all = blocks.filter(shown)
        let asked = all.lastIndex { $0.kind == .user }
        let since = asked.map { Array(all[($0 + 1)...]) } ?? all
        let words = Set(since.filter { $0.kind == .text }.suffix(2).map(\.id))
        let tools = Set(since.filter { $0.kind == .tool }.suffix(2).map(\.id))
        var out = since.filter { words.contains($0.id) || tools.contains($0.id) }
        if let asked {
            out.insert(all[asked], at: 0)
            if words.isEmpty, let before = all[..<asked].last(where: { $0.kind == .text }) { out.insert(before, at: 0) }
        }
        return out
    }

    /// Markdown's marks dropped, for a few lines of plain text.
    public static func plain(_ md: String) -> String {
        (try? AttributedString(markdown: md, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            .map { String($0.characters) } ?? md
    }
}

/// A diff's changes as made-up lines in its proportions (removed, added or
/// context, each with a length), the same every time for the same counts:
/// for a picture of a diff too small to read.
public enum DiffSketch {
    public static func lines(added: UInt32, removed: UInt32, files: UInt32, count: Int = 26) -> [(kind: Int, width: CGFloat)] {
        let total = max(Int(added + removed), 1)
        var seed = UInt64(truncatingIfNeeded: total &* 2654435761 &+ Int(files))
        func next() -> CGFloat {
            seed = seed &* 6364136223846793005 &+ 1442695040888963407
            return CGFloat((seed >> 33) % 1000) / 1000
        }
        let removedShare = Double(removed) / Double(total)
        return (0..<count).map { i in
            let r = next()
            let kind: Int = i % 6 < 2 ? 0 : (Double(r) < removedShare ? -1 : 1)
            return (kind, 0.3 + next() * 0.65)
        }
    }
}
