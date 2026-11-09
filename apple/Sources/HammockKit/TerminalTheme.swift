import Foundation
import GhosttyVt

/// Default colours for a terminal, matched to the app's appearance.
public struct TerminalTheme: Equatable, Sendable {
    public var background: RGB
    public var foreground: RGB
    public var cursor: RGB
    public var selection: RGB
    public var selectionForeground: RGB? = nil // nil = keep the text's own colour
    public var ansi: [RGB] // 16 colours

    static func hex(_ v: UInt32) -> RGB { RGB(r: UInt8(v >> 16 & 0xFF), g: UInt8(v >> 8 & 0xFF), b: UInt8(v & 0xFF)) }

    public static let light = TerminalTheme(
        background: hex(0xFFFFFF), foreground: hex(0x1D1D1F), cursor: hex(0x1D1D1F), selection: hex(0xB3D7FF),
        ansi: [0x1D1D1F, 0xC42B21, 0x1E7B34, 0x946300, 0x0058CC, 0x8944AB, 0x16707F, 0x8E8E93,
               0x6B6B70, 0xFF3B30, 0x34C759, 0xB58A00, 0x007AFF, 0xAF52DE, 0x32ADE6, 0xC7C7CC].map(hex))

    public static let dark = TerminalTheme(
        background: hex(0x1E1E20), foreground: hex(0xE5E5EA), cursor: hex(0xE5E5EA), selection: hex(0x3F638B),
        ansi: [0x48484A, 0xFF6961, 0x4CD964, 0xFFD60A, 0x64A8FF, 0xC589F2, 0x5AC8DA, 0xD1D1D6,
               0x8E8E93, 0xFF453A, 0x30D158, 0xFFE55C, 0x0A84FF, 0xBF5AF2, 0x64D2FF, 0xFFFFFF].map(hex))

    /// Names of the built-in themes, alongside any Ghostty theme files.
    public static let builtInLight = "Hammock Light"
    public static let builtInDark = "Hammock Dark"

    /// The full 256-colour palette: theme ANSI colours, then the standard cube and greys.
    var palette256: [GhosttyColorRgb] {
        var p = ansi.map { GhosttyColorRgb(r: $0.r, g: $0.g, b: $0.b) }
        let steps: [UInt8] = [0, 95, 135, 175, 215, 255]
        for r in steps { for g in steps { for b in steps { p.append(GhosttyColorRgb(r: r, g: g, b: b)) } } }
        for i in 0..<24 { let v = UInt8(8 + i * 10); p.append(GhosttyColorRgb(r: v, g: v, b: v)) }
        return p
    }
}

extension TerminalEngine {
    func applyTheme(_ t: TerminalTheme, to terminal: GhosttyTerminal) {
        var bg = GhosttyColorRgb(r: t.background.r, g: t.background.g, b: t.background.b)
        var fg = GhosttyColorRgb(r: t.foreground.r, g: t.foreground.g, b: t.foreground.b)
        var cur = GhosttyColorRgb(r: t.cursor.r, g: t.cursor.g, b: t.cursor.b)
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND, &bg)
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND, &fg)
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_COLOR_CURSOR, &cur)
        var pal = t.palette256
        pal.withUnsafeMutableBufferPointer { _ = ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_COLOR_PALETTE, $0.baseAddress) }
    }
}

extension TerminalTheme {
    /// Parses a Ghostty theme file (`background = #rrggbb`, `palette = 3=#rrggbb`,
    /// ...). Anything the file doesn't set comes from `base`.
    public init(ghosttyTheme text: String, base: TerminalTheme) {
        self = base
        for line in text.split(whereSeparator: \.isNewline) {
            let t = line.trimmingCharacters(in: .whitespaces)
            guard !t.hasPrefix("#"), let eq = t.firstIndex(of: "=") else { continue }
            let key = t[..<eq].trimmingCharacters(in: .whitespaces)
            let value = t[t.index(after: eq)...].trimmingCharacters(in: .whitespaces)
            switch key {
            case "background": background = Self.parse(value) ?? background
            case "foreground": foreground = Self.parse(value) ?? foreground
            case "cursor-color": cursor = Self.parse(value) ?? cursor
            case "selection-background": selection = Self.parse(value) ?? selection
            case "selection-foreground": selectionForeground = Self.parse(value)
            case "palette":
                let parts = value.split(separator: "=", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
                if parts.count == 2, let i = Int(parts[0]), ansi.indices.contains(i), let c = Self.parse(parts[1]) { ansi[i] = c }
            default: break
            }
        }
    }

    static func parse(_ s: String) -> RGB? {
        let hex = s.hasPrefix("#") ? String(s.dropFirst()) : s
        guard hex.count == 6, let v = UInt32(hex, radix: 16) else { return nil }
        return Self.hex(v)
    }
}
