import AppKit
import HammockKit
import SwiftUI

/// The window's own colours: the sidebar's and the toolbar's, each for light
/// and dark. Native has none (the system's glass); the rest tint it. Picked in
/// Settings ▸ Appearance (`window-theme`); `sidebar-color` and `toolbar-color`
/// in the config file override either.
struct WindowTheme: Identifiable, Equatable {
    let name: String
    let sidebar: (light: NSColor, dark: NSColor)?
    let toolbar: (light: NSColor, dark: NSColor)?
    /// The colour that names it, for its swatch in Settings.
    var mark: NSColor? = nil
    var id: String { name }

    static func == (a: Self, b: Self) -> Bool { a.name == b.name }

    static let native = "Native"
    static let matchTerminal = "Match Terminal"

    static let builtIn: [WindowTheme] = [
        WindowTheme(name: native, sidebar: nil, toolbar: nil),
        theme("Graphite", "E6E6EA", "F3F3F5", "1C1C1F", "26262A", mark: "6E6E78"),
        theme("Hammock", "D7F4DF", "E9F9ED", "0C311A", "123A21", mark: "1F7A57"),
        theme("Ocean", "D5E6FA", "EBF3FD", "0F2238", "162D47", mark: "2F7AD9"),
        theme("Forest", "D6EEDB", "EBF7EE", "11281A", "183423", mark: "2F9A55"),
        theme("Rose", "F9D9E3", "FCEDF2", "331821", "40202B", mark: "D9577E"),
        theme("Lavender", "E4DBFA", "F3EFFD", "221A3A", "2C2349", mark: "7E5BD9"),
        theme("Sand", "F5E8C3", "FBF5E2", "2F2A1C", "3B3524", mark: "C79A2E"),
    ]

    private static func theme(_ name: String, _ sl: String, _ tl: String, _ sd: String, _ td: String, mark: String) -> WindowTheme {
        WindowTheme(name: name, sidebar: (hex(sl)!, hex(sd)!), toolbar: (hex(tl)!, hex(td)!), mark: hex(mark))
    }

    /// The terminal's backgrounds: the toolbar in them, the sidebar a shade
    /// further from the text (darker in light mode, lighter in dark).
    static func matching(light: TerminalTheme, dark: TerminalTheme) -> WindowTheme {
        func color(_ c: RGB) -> NSColor { NSColor(srgbRed: CGFloat(c.r) / 255, green: CGFloat(c.g) / 255, blue: CGFloat(c.b) / 255, alpha: 1) }
        let l = color(light.background), d = color(dark.background)
        return WindowTheme(name: matchTerminal,
                           sidebar: (l.blended(withFraction: 0.06, of: .black) ?? l, d.blended(withFraction: 0.06, of: .white) ?? d),
                           toolbar: (l, d), mark: color(dark.foreground))
    }

    /// `#RRGGBB` (or `RRGGBB`, or with alpha `#RRGGBBAA`).
    static func hex(_ s: String) -> NSColor? {
        var t = s.trimmingCharacters(in: .whitespaces)
        if t.hasPrefix("#") { t.removeFirst() }
        guard t.count == 6 || t.count == 8, let v = UInt64(t, radix: 16) else { return nil }
        let a = t.count == 8 ? CGFloat(v & 0xFF) / 255 : 1
        let rgb = t.count == 8 ? v >> 8 : v
        return NSColor(srgbRed: CGFloat((rgb >> 16) & 0xFF) / 255, green: CGFloat((rgb >> 8) & 0xFF) / 255, blue: CGFloat(rgb & 0xFF) / 255, alpha: a)
    }

    /// A colour for both appearances (`#RRGGBB`) or each (`light:#…,dark:#…`).
    static func colorPair(_ value: String?) -> (light: NSColor, dark: NSColor)? {
        guard let value, !value.isEmpty else { return nil }
        var light: NSColor?, dark: NSColor?
        for part in value.split(separator: ",").map({ $0.trimmingCharacters(in: .whitespaces) }) {
            if part.hasPrefix("light:") { light = hex(String(part.dropFirst(6))) } else if part.hasPrefix("dark:") { dark = hex(String(part.dropFirst(5))) } else { light = hex(part); dark = light }
        }
        guard let l = light ?? dark, let d = dark ?? light else { return nil }
        return (l, d)
    }
}

/// The sidebar's tint over its glass, from the window theme, as opaque as
/// `sidebar-opacity` says.
struct SidebarTint: View {
    @Environment(\.colorScheme) private var scheme
    private var config: AppConfig { .shared }

    var body: some View {
        if let tint = config.sidebarTint(dark: scheme == .dark) {
            Color(nsColor: tint.color).opacity(tint.alpha)
        }
    }
}
