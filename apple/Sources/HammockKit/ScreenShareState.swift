import Foundation

/// Screen sharing on a phone (ADR-0014): a Mac's screen open full screen,
/// minimised to a bar that stays connected while the rest of the app is
/// used, or ended. One screen at a time: opening another Mac's replaces it.
public struct ScreenShareState: Equatable, Sendable {
    public enum Phase: Equatable, Sendable {
        case ended
        case open
        case minimised
    }

    public private(set) var phase: Phase = .ended
    public private(set) var computerID = ""
    public private(set) var name = ""
    /// Bumped each time a screen is opened afresh (not when it's opened
    /// again from the bar): a new viewer, and new streams.
    public private(set) var session = 0

    public init() {}

    public var isShowing: Bool { phase != .ended }
    public var isMinimised: Bool { phase == .minimised }

    /// Opens a Mac's screen full screen: the one already shared comes back
    /// as it was (the same streams); another replaces it.
    public mutating func open(computerID: String, name: String) {
        self.name = name
        if phase != .ended, computerID == self.computerID {
            phase = .open
            return
        }
        self.computerID = computerID
        phase = .open
        session += 1
    }

    /// Down to the bar. Only an open screen minimises.
    public mutating func minimise() {
        if phase == .open { phase = .minimised }
    }

    /// Back to full screen from the bar.
    public mutating func expand() {
        if phase == .minimised { phase = .open }
    }

    public mutating func end() {
        phase = .ended
        computerID = ""
        name = ""
    }

    /// The most frames a second asked of the Mac for a screen's two layers:
    /// the detail (about the phone's size) and the base (the whole display,
    /// small). Minimised, the bar's thumbnail needs only a frame or two a
    /// second; open, each layer's full rate (the detail's is the Mac's
    /// most, the base's leaves the encoder to the detail).
    public static func detailFPS(minimised: Bool) -> UInt32 { minimised ? 2 : 60 }
    public static func baseFPS(minimised: Bool) -> UInt32 { minimised ? 1 : 15 }
}
