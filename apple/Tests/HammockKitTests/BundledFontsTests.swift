#if os(macOS)
import AppKit
#else
import UIKit
#endif
import CoreText
import Foundation
import Testing
@testable import HammockKit

/// apple/Resources, laid out as the Mac app's Resources are (Fonts/ beside the pages).
private let resources = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
    .appendingPathComponent("../../Resources").standardizedFileURL

@Test func everyBundledFontIsInResourcesWithItsFamily() throws {
    let bundle = try #require(Bundle(url: resources))
    for face in BundledFonts.faces {
        let url = try #require(BundledFonts.url(face.file, in: bundle), "\(face.file) missing")
        let descs = CTFontManagerCreateFontDescriptorsFromURL(url as CFURL) as? [CTFontDescriptor] ?? []
        let family = try #require(descs.first.map { CTFontCopyFamilyName(CTFontCreateWithFontDescriptor($0, 12, nil)) as String })
        #expect(family == face.family)
    }
    #expect(Set(BundledFonts.faces.map(\.family)) == Set(BundledFonts.families + [BundledFonts.symbolsFamily]))
}

@MainActor @Test func symbolsFallBackAfterAnyFont() throws {
    BundledFonts.register(bundle: try #require(Bundle(url: resources)))
    let font = BundledFonts.withSymbols(CTFontCreateWithName("Menlo-Regular" as CFString, 13, nil))
    // A Powerline arrow and a Nerd Font folder icon: in neither Menlo nor the system's fallbacks.
    let line = CTLineCreateWithAttributedString(NSAttributedString(string: "\u{E0B0}\u{F07B}", attributes: [.init(kCTFontAttributeName as String): font]))
    let runs = CTLineGetGlyphRuns(line) as! [CTRun]
    let names = runs.map { CTFontCopyPostScriptName((CTRunGetAttributes($0) as NSDictionary)[kCTFontAttributeName] as! CTFont) as String }
    #expect(names == [BundledFonts.symbolsName])
}

@Test func pagesGetFontFacesRelativeToThemselves() throws {
    let bundle = try #require(Bundle(url: resources))
    let css = BundledFonts.fontFaceCSS(forPage: resources.appendingPathComponent("diff.html"), bundle: bundle)
    #expect(css.components(separatedBy: "@font-face").count - 1 == BundledFonts.faces.count)
    #expect(css.contains(#"font-family: "Obel Mono"; src: url("Fonts/ObelMono-VF.ttf"); font-weight: 100 900; font-style: normal;"#))
}

/// The terminal makes its italic fonts from the regular and bold ones
/// (NSFontManager on the Mac, descriptor traits on the iPhone): the icons
/// must survive that.
@MainActor @Test func italicsKeepTheSymbolsFallback() throws {
    BundledFonts.register(bundle: try #require(Bundle(url: resources)))
    let regular = BundledFonts.withSymbols(CTFontCreateWithName("Menlo-Regular" as CFString, 13, nil))
    let viaTraits = try #require(CTFontCreateCopyWithSymbolicTraits(regular, 13, nil, .traitItalic, .traitItalic))
    #if os(macOS)
    let viaManager = NSFontManager.shared.convert(regular as NSFont, toHaveTrait: .italicFontMask) as CTFont
    let italics = [viaTraits, viaManager]
    #else
    let italics = [viaTraits]
    #endif
    for font in italics {
        #expect((CTFontCopyPostScriptName(font) as String).contains("Italic"))
        let line = CTLineCreateWithAttributedString(NSAttributedString(string: "\u{E0B0}", attributes: [.init(kCTFontAttributeName as String): font]))
        let runs = CTLineGetGlyphRuns(line) as! [CTRun]
        let names = runs.map { CTFontCopyPostScriptName((CTRunGetAttributes($0) as NSDictionary)[kCTFontAttributeName] as! CTFont) as String }
        #expect(names == [BundledFonts.symbolsName])
    }
}

/// The terminal's default font, the system's monospaced one, ignores a
/// cascade list: its rows give icons it hasn't got to the symbols font.
@MainActor @Test func terminalRowsDrawIconsInTheSymbolsFont() throws {
    BundledFonts.register(bundle: try #require(Bundle(url: resources)))
    #if os(macOS)
    let system = NSFont.monospacedSystemFont(ofSize: 13, weight: .regular) as CTFont
    #else
    let system = UIFont.monospacedSystemFont(ofSize: 13, weight: .regular) as CTFont
    #endif
    let fonts = TerminalFonts(regular: system, bold: system, italic: system, boldItalic: system,
                              cellWidth: 8, cellHeight: 16, baseline: 12, scale: 2)
    let layout = TerminalLineLayout(fonts: fonts, palette: TerminalPalette(foreground: RGB(r: 0, g: 0, b: 0), background: RGB(r: 255, g: 255, b: 255),
                                                                           selection: RGB(r: 0, g: 0, b: 255), selectionForeground: nil))
    let line = layout.line(["a", "\u{E0B0}", "\u{F07B}", "b"].map { TerminalCell(text: $0) })
    var fontsUsed: [String] = []
    for t in line.text {
        switch t {
        case .glyphs(let g): fontsUsed += g.glyphs.map { _ in CTFontCopyPostScriptName(g.font) as String }
        case .fitted(let g): fontsUsed.append(CTFontCopyPostScriptName(g.font) as String)
        }
    }
    #expect(fontsUsed.filter { $0 == BundledFonts.symbolsName }.count == 2)
    #expect(!fontsUsed.contains("LastResort"))
}
