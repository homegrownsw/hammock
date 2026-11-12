import AppKit

/// The terminal font: font-family / font-size from the config (falling back to
/// Ghostty's; see AppConfig), else SF Mono 13. An installed Nerd Font is added
/// as a fallback so prompt glyphs render.
@MainActor
enum TerminalFont {
    static func resolve() -> (regular: NSFont, bold: NSFont) {
        let family = AppConfig.shared.fontFamily
        let base = AppConfig.shared.fontSize ?? 13
        let size = base + clampedZoom(UserDefaults.standard.double(forKey: "terminalFontZoom"), base: base)
        var regular = family.flatMap { NSFont(name: $0, size: size) ?? fontFromFamily($0, size: size) }
            ?? NSFont.monospacedSystemFont(ofSize: size, weight: .regular)
        regular = withNerdFallback(regular)
        let bold = withNerdFallback(NSFontManager.shared.convert(regular, toHaveTrait: .boldFontMask))
        return (regular, bold)
    }

    /// Bigger and Smaller keep the font between 6 and 72pt (or the
    /// configured size, if that's outside them).
    static func clampedZoom(_ zoom: Double, base: Double) -> Double {
        min(max(zoom, min(0, 6 - base)), max(0, 72 - base))
    }

    private static func fontFromFamily(_ family: String, size: CGFloat) -> NSFont? {
        NSFontManager.shared.font(withFamily: family, traits: [], weight: 5, size: size)
    }

    private static func withNerdFallback(_ font: NSFont) -> NSFont {
        guard !font.fontName.localizedCaseInsensitiveContains("nerd"),
              let nerd = NSFontManager.shared.availableFonts.first(where: { $0.localizedCaseInsensitiveContains("NerdFontMono") || $0.localizedCaseInsensitiveContains("SymbolsNerdFont") })
        else { return font }
        let cascade = [NSFontDescriptor(name: nerd, size: font.pointSize)]
        let desc = font.fontDescriptor.addingAttributes([.cascadeList: cascade])
        return NSFont(descriptor: desc, size: font.pointSize) ?? font
    }

}

extension TerminalFont {
    static let changed = Notification.Name("HammockTerminalFontChanged")

    /// Adjusts the font size by delta points (0 resets to the configured size).
    static func zoom(by delta: Double) {
        let d = UserDefaults.standard
        if delta == 0 {
            d.removeObject(forKey: "terminalFontZoom")
        } else {
            let base = AppConfig.shared.fontSize ?? 13
            // From the zoom shown, so presses past a limit aren't saved up.
            let shown = clampedZoom(d.double(forKey: "terminalFontZoom"), base: base)
            d.set(clampedZoom(shown + delta, base: base), forKey: "terminalFontZoom")
        }
        NotificationCenter.default.post(name: changed, object: nil)
    }
}
