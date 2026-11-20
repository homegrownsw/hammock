import CoreGraphics

/// Where a terminal's grid was drawn in a view, in points: turns a touch
/// into a cell, and a selection into the places its handles and its edit
/// menu go. The iPhone's terminal (TerminalScreen.swift) uses it; Android
/// has its own copy (TouchGrid.kt).
public struct TerminalTouchGrid: Equatable, Sendable {
    public var origin: CGPoint
    public var cellWidth: CGFloat
    public var cellHeight: CGFloat
    public var cols: Int
    public var rows: Int

    public init(origin: CGPoint, cellWidth: CGFloat, cellHeight: CGFloat, cols: Int, rows: Int) {
        self.origin = origin
        self.cellWidth = max(cellWidth, 1)
        self.cellHeight = max(cellHeight, 1)
        self.cols = max(cols, 1)
        self.rows = max(rows, 1)
    }

    /// The row a point is on, unclamped: negative above the grid, `rows` or more below it.
    public func row(at y: CGFloat) -> Int {
        Int(((y - origin.y) / cellHeight).rounded(.down))
    }

    /// The cell under a point, held to the grid.
    public func cell(at p: CGPoint) -> TerminalCellPoint {
        let x = Int(((p.x - origin.x) / cellWidth).rounded(.down))
        return TerminalCellPoint(x: min(max(x, 0), cols - 1), y: min(max(row(at: p.y), 0), rows - 1))
    }

    /// The cell a selection handle's caret at a point takes in: the start
    /// handle's caret sits on a cell's left edge, the end's on its right,
    /// so each goes to the nearest gap between cells. Past a row's end, the
    /// start goes on to the next row's first cell; before its first, the
    /// end goes back to the last row's last. Held to the grid.
    public func handleCell(at p: CGPoint, edge: SelectionEdge) -> TerminalCellPoint {
        let gap = Int(((p.x - origin.x) / cellWidth).rounded())
        var y = min(max(row(at: p.y), 0), rows - 1)
        var x = edge == .start ? gap : gap - 1
        if x >= cols {
            if y < rows - 1 { x = 0; y += 1 } else { x = cols - 1 }
        } else if x < 0 {
            if y > 0 { x = cols - 1; y -= 1 } else { x = 0 }
        }
        return TerminalCellPoint(x: x, y: y)
    }

    /// Where a handle's caret goes: a line the height of a row, on the left
    /// of the selection's first cell or the right of its last.
    public func caret(_ cell: TerminalCellPoint, edge: SelectionEdge) -> CGRect {
        let x = origin.x + CGFloat(edge == .start ? cell.x : cell.x + 1) * cellWidth
        return CGRect(x: x, y: origin.y + CGFloat(cell.y) * cellHeight, width: 0, height: cellHeight)
    }

    /// Whether a cell's row is on the screen (an end scrolled away isn't).
    public func shows(_ cell: TerminalCellPoint) -> Bool { cell.y >= 0 && cell.y < rows }

    /// What a selection covers on the screen, for its edit menu to avoid:
    /// whole rows wide when it spans several. Nil when none of it shows.
    public func rect(from start: TerminalCellPoint, to end: TerminalCellPoint) -> CGRect? {
        let top = max(start.y, 0), bottom = min(end.y, rows - 1)
        guard top <= bottom else { return nil }
        let oneRow = start.y == end.y
        let left = oneRow ? start.x : 0, right = oneRow ? end.x + 1 : cols
        return CGRect(x: origin.x + CGFloat(left) * cellWidth, y: origin.y + CGFloat(top) * cellHeight,
                      width: CGFloat(right - left) * cellWidth, height: CGFloat(bottom - top + 1) * cellHeight)
    }

    /// How far past the grid's top or bottom a point is, in rows (negative
    /// above, positive below, 0 on it): dragging a handle there scrolls.
    public func overscroll(at y: CGFloat) -> Int {
        let r = row(at: y)
        return r < 0 ? r : (r >= rows ? r - rows + 1 : 0)
    }
}
