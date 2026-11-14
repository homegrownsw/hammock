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
