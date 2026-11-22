import CoreGraphics

/// The size of a row's touch-and-hold preview of a pane (the iPhone's
/// Sessions and Groups rows): as wide as there's room for, and as tall as
/// the pane's shape asks, between a third and 60% of the screen's height.
public enum HoldPreview {
    /// A terminal cell's width over its height, near enough for its shape.
    static let cellAspect: CGFloat = 0.6
    /// The tile's padding, around its name and its content: clear of the
    /// preview's rounded corners.
    public static let edge: CGFloat = 12
    /// A terminal's text inside the content, clear of its rounded corners.
    public static let inset: CGFloat = 10
    /// The tile's name line, and the space under it.
    static let nameLine: CGFloat = 24

    /// `room`: the screen, less its margins. `cols` and `rows`: the pane's
    /// grid, 0 when it has none (a browser, a simulator) or isn't known.
    public static func size(room: CGSize, cols: Int, rows: Int) -> CGSize {
        let width = max(room.width, 0)
        let tallest = (room.height * 0.6).rounded()
        let around = 2 * (edge + inset)
        guard cols > 0, rows > 0, width > around else { return CGSize(width: width, height: tallest) }
        let shaped = (width - around) * CGFloat(rows) / (CGFloat(cols) * cellAspect) + around + nameLine
        let shortest = (room.height / 3).rounded()
        return CGSize(width: width, height: min(max(shaped.rounded(), shortest), tallest))
    }
}
