import SwiftUI

/// Lays its views out left to right, wrapping onto new lines, as words in a
/// sentence: each at its own size, lined up on their centres.
public struct FlowLayout: Layout {
    public var spacing: CGFloat
    public var lineSpacing: CGFloat

    public init(spacing: CGFloat = 6, lineSpacing: CGFloat = 6) {
        self.spacing = spacing
        self.lineSpacing = lineSpacing
    }

    public func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let lines = lines(for: subviews, width: proposal.width ?? .infinity)
        let width = lines.map(\.width).max() ?? 0
        let height = lines.map(\.height).reduce(0, +) + lineSpacing * CGFloat(max(lines.count - 1, 0))
        return CGSize(width: proposal.width.map { min($0, width) } ?? width, height: height)
    }

    public func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var y = bounds.minY
        for line in lines(for: subviews, width: bounds.width) {
            var x = bounds.minX
            for i in line.items {
                let size = subviews[i].sizeThatFits(.unspecified)
                let w = min(size.width, bounds.width)
                subviews[i].place(at: CGPoint(x: x, y: y + line.height / 2), anchor: .leading,
                                  proposal: ProposedViewSize(width: w, height: size.height))
                x += w + spacing
            }
            y += line.height + lineSpacing
        }
    }

    private struct Line {
        var items: [Int] = []
        var width: CGFloat = 0
        var height: CGFloat = 0
    }

    private func lines(for subviews: Subviews, width: CGFloat) -> [Line] {
        var out: [Line] = []
        var cur = Line()
        for i in subviews.indices {
            let size = subviews[i].sizeThatFits(.unspecified)
            let w = min(size.width, width)
            if !cur.items.isEmpty, cur.width + spacing + w > width {
                out.append(cur)
                cur = Line()
            }
            cur.width += (cur.items.isEmpty ? 0 : spacing) + w
            cur.height = max(cur.height, size.height)
            cur.items.append(i)
        }
        if !cur.items.isEmpty { out.append(cur) }
        return out
    }
}
