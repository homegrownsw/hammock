import SwiftUI

// What the HammockKit files the watch compiles (StatusIcon, WorkingDot,
// AgentMark, WatchWire) expect from the rest of HammockKit, which doesn't
// build for watchOS.

/// StatusIcon's state: the watch's own, from the phone.
public typealias AgentState = WatchAgentState

/// A program's icon where there's no agent: the watch only shows agents.
public struct ProgramIcon: View {
    let program: [String]
    let size: CGFloat
    public var body: some View {
        Image(systemName: "terminal").resizable().scaledToFit().frame(width: size, height: size).foregroundStyle(.secondary)
    }
}

extension Color {
    /// 0xRRGGBB.
    init(hex: UInt32) {
        self.init(red: Double((hex >> 16) & 0xFF) / 255, green: Double((hex >> 8) & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }
}

/// The phone's theme, as the watch draws it.
struct WatchTheme {
    var accent: Color
    var bubble: Color

    init(_ s: WatchSnapshot?) {
        accent = Color(hex: s?.accent ?? WatchSnapshot.hammockGreen)
        bubble = Color(hex: s?.bubble ?? WatchSnapshot.hammockBubble)
    }
}

private struct WatchThemeKey: EnvironmentKey {
    static let defaultValue = WatchTheme(nil)
}

extension EnvironmentValues {
    var watchTheme: WatchTheme {
        get { self[WatchThemeKey.self] }
        set { self[WatchThemeKey.self] = newValue }
    }
}

extension WatchSession {
    /// The status icon for it.
    var icon: StatusIcon {
        StatusIcon(state: resting ? nil : state, resting: resting, size: 15, asleep: asleep, progress: progress, waiting: waitingOnLimit)
    }

    var agentName: String { AgentMark.name(of: harness) }
}
