import SwiftUI
#if os(macOS)
import AppKit
#else
import UIKit
#endif

/// One of Hammock's themes: everything that picks a colour, together. The
/// terminal's light and dark colours, the window's sidebar and toolbar tints,
/// the agent view's colour (and your messages'), and the focused pane's border.
/// Chosen in Settings ▸ Appearance (`app-theme` in the Mac's config), and on
/// the iPhone, which follows the Mac's unless you pick one there.
///
/// Every colour is here, so a theme looks the same without Ghostty installed
/// (the terminal themes named after Ghostty's are copies of its files).
public struct AppTheme: Identifiable, Sendable, Equatable {
    /// A colour for light mode and one for dark, as 0xRRGGBB.
    public struct Pair: Sendable, Equatable {
        public let light: UInt32
        public let dark: UInt32
        public init(_ light: UInt32, _ dark: UInt32) { self.light = light; self.dark = dark }
        public init(_ both: UInt32) { light = both; dark = both }
        public func value(dark isDark: Bool) -> UInt32 { isDark ? dark : light }
        /// One that changes with the appearance it's drawn in.
        public var color: Color { AppTheme.dynamic(light, dark) }
    }

    public let name: String
    /// The terminal themes' names (in Settings' Custom pickers too) and colours.
    public let lightTerminalName: String
    public let darkTerminalName: String
    public let light: TerminalTheme
    public let dark: TerminalTheme
    /// The sidebar's and toolbar's tints; nil: the system's glass (Native).
    public let sidebar: Pair?
    public let toolbar: Pair?
    /// Buttons, links and the like, a lighter shade in the dark; nil: the
    /// system accent (Native).
    public let accent: Pair?
    /// Your messages in the agent view: deep enough for white text in both.
    public let bubble: Pair?
    /// Whether it's one of Hammock's own tints (Settings shows them first),
    /// rather than a well-known terminal theme's colours.
    public let ownTint: Bool

    public var id: String { name }

    /// The focused pane's border: the theme's accent.
    public var border: Pair? { accent }

    public static let native = "Native"
    /// What a new install starts with.
    public static let defaultName = "Hammock"
    /// `app-theme = custom`: each setting chosen on its own.
    public static let custom = "custom"

    /// By name, ignoring case and accents ("rose pine" is Rosé Pine), as
    /// hmkd and hmk match them (hammockconfig.ThemeName).
    public static func named(_ name: String) -> AppTheme? {
        let key = fold(name)
        return all.first { fold($0.name) == key }
    }

    private static func fold(_ s: String) -> String {
        s.trimmingCharacters(in: .whitespaces).folding(options: [.caseInsensitive, .diacriticInsensitive], locale: nil)
    }

    /// Terminal themes the curated ones bring, by name (beside Ghostty's files).
    public static func terminal(named name: String) -> TerminalTheme? {
        for t in all {
            if t.lightTerminalName == name { return t.light }
            if t.darkTerminalName == name { return t.dark }
        }
        return nil
    }

    /// Every terminal theme name the curated ones bring, without the
    /// built-in Hammock Light and Dark.
    public static var terminalNames: [String] {
        var seen = Set([TerminalTheme.builtInLight, TerminalTheme.builtInDark])
        var out: [String] = []
        for t in all {
            for n in [t.lightTerminalName, t.darkTerminalName] where seen.insert(n).inserted { out.append(n) }
        }
        return out
    }

    // MARK: The themes

    public static let all: [AppTheme] = [
        AppTheme(name: "Hammock", lightTerminalName: TerminalTheme.builtInLight, darkTerminalName: TerminalTheme.builtInDark,
                 light: .light, dark: .dark,
                 sidebar: Pair(0xD5EBE0, 0x0D2A20), toolbar: Pair(0xEBF5F0, 0x133528),
                 accent: Pair(0x1F7A57, 0x3FB88A), bubble: Pair(0x1D6046, 0x1F6A4C), ownTint: true),
        AppTheme(name: native, lightTerminalName: TerminalTheme.builtInLight, darkTerminalName: TerminalTheme.builtInDark,
                 light: .light, dark: .dark, sidebar: nil, toolbar: nil, accent: nil, bubble: nil, ownTint: true),
        tint("Graphite", sidebar: Pair(0xE6E6EA, 0x1C1C1F), toolbar: Pair(0xF3F3F5, 0x26262A),
             accent: Pair(0x3E3E46, 0xE2E2E8), bubble: Pair(0x5E5E68)),
        tint("Ocean", sidebar: Pair(0xD5E6FA, 0x0F2238), toolbar: Pair(0xEBF3FD, 0x162D47),
             accent: Pair(0x2B6CC4, 0x72A8F0), bubble: Pair(0x2B6CC4)),
        tint("Forest", sidebar: Pair(0xD6EEDB, 0x11281A), toolbar: Pair(0xEBF7EE, 0x183423),
             accent: Pair(0x2E8B4F, 0x5EC786), bubble: Pair(0x2E8B4F)),
        tint("Rose", sidebar: Pair(0xF9D9E3, 0x331821), toolbar: Pair(0xFCEDF2, 0x40202B),
             accent: Pair(0xCC4A72, 0xEA7398), bubble: Pair(0xCC4A72)),
        tint("Lavender", sidebar: Pair(0xE4DBFA, 0x221A3A), toolbar: Pair(0xF3EFFD, 0x2C2349),
             accent: Pair(0x7352D1, 0xA68EF2), bubble: Pair(0x7352D1)),
        tint("Sand", sidebar: Pair(0xF5E8C3, 0x2F2A1C), toolbar: Pair(0xFBF5E2, 0x3B3524),
             accent: Pair(0xA87A1F, 0xD9A743), bubble: Pair(0xA87A1F)),
        known("Catppuccin",
              lightName: "Catppuccin Latte",
              light: term(bg: 0xEFF1F5, fg: 0x4C4F69, cursor: 0xDC8A78, sel: 0xDC8A78, selFg: 0xEFF1F5,
                          [0xBCC0CC, 0xD20F39, 0x40A02B, 0xDF8E1D, 0x1E66F5, 0xEA76CB, 0x179299, 0x5C5F77,
                           0xACB0BE, 0xE7103F, 0x46B02F, 0xE49931, 0x3878F6, 0xEF95D7, 0x19A1A8, 0x6C6F85]),
              darkName: "Catppuccin Mocha",
              dark: term(bg: 0x1E1E2E, fg: 0xCDD6F4, cursor: 0xF5E0DC, sel: 0xF5E0DC, selFg: 0x1E1E2E,
                         [0x45475A, 0xF38BA8, 0xA6E3A1, 0xF9E2AF, 0x89B4FA, 0xF5C2E7, 0x94E2D5, 0xBAC2DE,
                          0x585B70, 0xF7AEC2, 0xC2ECBF, 0xFCD682, 0xAECCFC, 0xF398DA, 0xB1EAE1, 0xA6ADC8]),
              accent: Pair(0x8839EF, 0xCBA6F7), bubble: Pair(0x8839EF, 0x7A4FC0)),
        known("Tokyo Night",
              lightName: "TokyoNight Day",
              light: term(bg: 0xE1E2E7, fg: 0x3760BF, cursor: 0x3760BF, sel: 0x99A7DF, selFg: 0x3760BF,
                          [0xE9E9ED, 0xF52A65, 0x587539, 0x8C6C3E, 0x2E7DE9, 0x9854F1, 0x007197, 0x6172B0,
                           0xA1A6C5, 0xF52A65, 0x587539, 0x8C6C3E, 0x2E7DE9, 0x9854F1, 0x007197, 0x3760BF]),
              darkName: "TokyoNight",
              dark: term(bg: 0x1A1B26, fg: 0xC0CAF5, cursor: 0xC0CAF5, sel: 0x33467C, selFg: 0xC0CAF5,
                         [0x15161E, 0xF7768E, 0x9ECE6A, 0xE0AF68, 0x7AA2F7, 0xBB9AF7, 0x7DCFFF, 0xA9B1D6,
                          0x414868, 0xF7768E, 0x9ECE6A, 0xE0AF68, 0x7AA2F7, 0xBB9AF7, 0x7DCFFF, 0xC0CAF5]),
              accent: Pair(0x2E7DE9, 0x7AA2F7), bubble: Pair(0x2E66C8, 0x3D59A1)),
        known("Gruvbox",
              lightName: "Gruvbox Light",
              light: term(bg: 0xFBF1C7, fg: 0x3C3836, cursor: 0x3C3836, sel: 0x3C3836, selFg: 0xFBF1C7,
                          [0xFBF1C7, 0xCC241D, 0x98971A, 0xD79921, 0x458588, 0xB16286, 0x689D6A, 0x7C6F64,
                           0x928374, 0x9D0006, 0x79740E, 0xB57614, 0x076678, 0x8F3F71, 0x427B58, 0x3C3836]),
              darkName: "Gruvbox Dark",
              dark: term(bg: 0x282828, fg: 0xEBDBB2, cursor: 0xEBDBB2, sel: 0x665C54, selFg: 0xEBDBB2,
                         [0x282828, 0xCC241D, 0x98971A, 0xD79921, 0x458588, 0xB16286, 0x689D6A, 0xA89984,
                          0x928374, 0xFB4934, 0xB8BB26, 0xFABD2F, 0x83A598, 0xD3869B, 0x8EC07C, 0xEBDBB2]),
              accent: Pair(0xAF3A03, 0xFE8019), bubble: Pair(0xAF3A03, 0xB4531A)),
        known("Nord",
              lightName: "Nord Light",
              light: term(bg: 0xE5E9F0, fg: 0x414858, cursor: 0x7BB3C3, sel: 0xD8DEE9, selFg: 0x4C556A,
                          [0x3B4252, 0xBF616A, 0x96B17F, 0xC5A565, 0x81A1C1, 0xB48EAD, 0x7BB3C3, 0xA5ABB6,
                           0x4C566A, 0xBF616A, 0x96B17F, 0xC5A565, 0x81A1C1, 0xB48EAD, 0x82AFAE, 0xECEFF4]),
              darkName: "Nord",
              dark: term(bg: 0x2E3440, fg: 0xD8DEE9, cursor: 0xECEFF4, sel: 0xECEFF4, selFg: 0x4C566A,
                         [0x3B4252, 0xBF616A, 0xA3BE8C, 0xEBCB8B, 0x81A1C1, 0xB48EAD, 0x88C0D0, 0xE5E9F0,
                          0x596377, 0xBF616A, 0xA3BE8C, 0xEBCB8B, 0x81A1C1, 0xB48EAD, 0x8FBCBB, 0xECEFF4]),
              accent: Pair(0x5E81AC, 0x88C0D0), bubble: Pair(0x5E81AC)),
        known("Rosé Pine",
              lightName: "Rose Pine Dawn",
              light: term(bg: 0xFAF4ED, fg: 0x575279, cursor: 0x575279, sel: 0xDFDAD9, selFg: 0x575279,
                          [0xF2E9E1, 0xB4637A, 0x286983, 0xEA9D34, 0x56949F, 0x907AA9, 0xD7827E, 0x575279,
                           0x9893A5, 0xB4637A, 0x286983, 0xEA9D34, 0x56949F, 0x907AA9, 0xD7827E, 0x575279]),
              darkName: "Rose Pine",
              dark: term(bg: 0x191724, fg: 0xE0DEF4, cursor: 0xE0DEF4, sel: 0x403D52, selFg: 0xE0DEF4,
                         [0x26233A, 0xEB6F92, 0x31748F, 0xF6C177, 0x9CCFD8, 0xC4A7E7, 0xEBBCBA, 0xE0DEF4,
                          0x6E6A86, 0xEB6F92, 0x31748F, 0xF6C177, 0x9CCFD8, 0xC4A7E7, 0xEBBCBA, 0xE0DEF4]),
              accent: Pair(0x907AA9, 0xC4A7E7), bubble: Pair(0x7E6A99, 0x6E5A8F)),
        known("Solarized",
              lightName: "iTerm2 Solarized Light",
              light: term(bg: 0xFDF6E3, fg: 0x657B83, cursor: 0x657B83, sel: 0xEEE8D5, selFg: 0x586E75,
                          [0x073642, 0xDC322F, 0x859900, 0xB58900, 0x268BD2, 0xD33682, 0x2AA198, 0xBBB5A2,
                           0x002B36, 0xCB4B16, 0x586E75, 0x657B83, 0x839496, 0x6C71C4, 0x93A1A1, 0xFDF6E3]),
              darkName: "iTerm2 Solarized Dark",
              dark: term(bg: 0x002B36, fg: 0x839496, cursor: 0x839496, sel: 0x073642, selFg: 0x93A1A1,
                         [0x073642, 0xDC322F, 0x859900, 0xB58900, 0x268BD2, 0xD33682, 0x2AA198, 0xEEE8D5,
                          0x335E69, 0xCB4B16, 0x586E75, 0x657B83, 0x839496, 0x6C71C4, 0x93A1A1, 0xFDF6E3]),
              accent: Pair(0x268BD2), bubble: Pair(0x1F72AD, 0x1D6A9E)),
    ]

    /// One of Hammock's tints: its terminal is Hammock Light and Dark on a
    /// background taken from the toolbar's tint, so the window reads as one.
    private static func tint(_ name: String, sidebar: Pair, toolbar: Pair, accent: Pair, bubble: Pair) -> AppTheme {
        var l = TerminalTheme.light, d = TerminalTheme.dark
        l.background = TerminalTheme.hex(mix(0xFFFFFF, toolbar.light, 0.55))
        d.background = TerminalTheme.hex(mix(0x1E1E20, toolbar.dark, 0.5))
        return AppTheme(name: name, lightTerminalName: "\(name) Light", darkTerminalName: "\(name) Dark",
                        light: l, dark: d, sidebar: sidebar, toolbar: toolbar, accent: accent, bubble: bubble, ownTint: true)
    }

    /// A well-known terminal theme: the window's tints come from the
    /// terminal's backgrounds, as Match Terminal makes them.
    private static func known(_ name: String, lightName: String, light: TerminalTheme, darkName: String, dark: TerminalTheme,
                              accent: Pair, bubble: Pair) -> AppTheme {
        let tints = matchingTints(light: light, dark: dark)
        return AppTheme(name: name, lightTerminalName: lightName, darkTerminalName: darkName, light: light, dark: dark,
                        sidebar: tints.sidebar, toolbar: tints.toolbar, accent: accent, bubble: bubble, ownTint: false)
    }

    /// Window tints from terminal backgrounds: the toolbar in them, the
    /// sidebar a shade further from the text (darker in light mode, lighter
    /// in dark). The Mac's Match Terminal window theme uses the same.
    public static func matchingTints(light: TerminalTheme, dark: TerminalTheme) -> (sidebar: Pair, toolbar: Pair) {
        let l = hex(light.background), d = hex(dark.background)
        return (Pair(mix(l, 0x000000, 0.06), mix(d, 0xFFFFFF, 0.06)), Pair(l, d))
    }

    private static func term(bg: UInt32, fg: UInt32, cursor: UInt32, sel: UInt32, selFg: UInt32, _ ansi: [UInt32]) -> TerminalTheme {
        TerminalTheme(background: TerminalTheme.hex(bg), foreground: TerminalTheme.hex(fg), cursor: TerminalTheme.hex(cursor),
                      selection: TerminalTheme.hex(sel), selectionForeground: TerminalTheme.hex(selFg), ansi: ansi.map(TerminalTheme.hex))
    }

    // MARK: Colour helpers

    public static func hex(_ c: RGB) -> UInt32 { UInt32(c.r) << 16 | UInt32(c.g) << 8 | UInt32(c.b) }

    /// `a` moved `f` of the way to `b`.
    public static func mix(_ a: UInt32, _ b: UInt32, _ f: Double) -> UInt32 {
        func ch(_ v: UInt32, _ s: UInt32) -> Double { Double((v >> s) & 0xFF) }
        func m(_ s: UInt32) -> UInt32 { UInt32((ch(a, s) + (ch(b, s) - ch(a, s)) * f).rounded()) << s }
        return m(16) | m(8) | m(0)
    }

    public static func color(_ v: UInt32) -> Color {
        Color(.sRGB, red: Double((v >> 16) & 0xFF) / 255, green: Double((v >> 8) & 0xFF) / 255, blue: Double(v & 0xFF) / 255)
    }

    /// One colour in light mode, another in dark.
    public static func dynamic(_ light: UInt32, _ dark: UInt32) -> Color {
        func c(_ v: UInt32) -> (CGFloat, CGFloat, CGFloat) { (CGFloat((v >> 16) & 0xFF) / 255, CGFloat((v >> 8) & 0xFF) / 255, CGFloat(v & 0xFF) / 255) }
        let (l, d) = (c(light), c(dark))
        #if os(macOS)
        return Color(nsColor: NSColor(name: nil) { a in
            let (r, g, b) = a.bestMatch(from: [.darkAqua, .aqua]) == .darkAqua ? d : l
            return NSColor(srgbRed: r, green: g, blue: b, alpha: 1)
        })
        #else
        return Color(uiColor: UIColor { t in
            let (r, g, b) = t.userInterfaceStyle == .dark ? d : l
            return UIColor(red: r, green: g, blue: b, alpha: 1)
        })
        #endif
    }

    /// `#RRGGBB`, or `light:#…,dark:#…`, as the config file writes colours.
    public static func parsePair(_ value: String?) -> Pair? {
        guard let value, !value.isEmpty else { return nil }
        func h(_ s: Substring) -> UInt32? {
            var t = s.trimmingCharacters(in: .whitespaces)
            if t.hasPrefix("#") { t.removeFirst() }
            return t.count == 6 ? UInt32(t, radix: 16) : nil
        }
        var light: UInt32?, dark: UInt32?
        for part in value.split(separator: ",") {
            let p = part.trimmingCharacters(in: .whitespaces)
            if p.hasPrefix("light:") { light = h(p.dropFirst(6)) } else if p.hasPrefix("dark:") { dark = h(p.dropFirst(5)) } else { light = h(Substring(p)); dark = light }
        }
        guard let l = light ?? dark, let d = dark ?? light else { return nil }
        return Pair(l, d)
    }

    public static func format(_ p: Pair) -> String {
        p.light == p.dark ? String(format: "#%06X", p.light) : String(format: "light:#%06X,dark:#%06X", p.light, p.dark)
    }
}
