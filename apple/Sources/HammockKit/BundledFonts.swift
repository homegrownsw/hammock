import CoreText
import Foundation
import WebKit

/// The fonts Hammock ships (apple/Resources/Fonts; their licences are in
/// third_party/licenses and the Acknowledgements), so the iPhone, where people
/// can't add their own, has a few popular terminal fonts, and Nerd Font icons
/// show in any font. Registered for this process only: nothing is installed.
public enum BundledFonts {
    /// The families offered in Settings, alphabetically.
    public static let families = ["Cascadia Code", "Fira Code", "Hack", "JetBrains Mono", "Obel Mono"]
    /// Symbols Nerd Font Mono, the icons alone: the fallback for every
    /// terminal font, so prompt glyphs render whatever font is chosen.
    public static let symbolsFamily = "Symbols Nerd Font Mono"
    public static let symbolsName = "SymbolsNFM"

    /// One file: its family, weight (a range for a variable font) and style,
    /// as CSS wants them for the editor and diff pages.
    struct Face {
        let file: String
        let family: String
        let weight: String
        let italic: Bool
    }

    static let faces: [Face] = [
        Face(file: "CascadiaCode.ttf", family: "Cascadia Code", weight: "200 700", italic: false),
        Face(file: "CascadiaCodeItalic.ttf", family: "Cascadia Code", weight: "200 700", italic: true),
        Face(file: "FiraCode-VF.ttf", family: "Fira Code", weight: "300 700", italic: false),
        Face(file: "Hack-Regular.ttf", family: "Hack", weight: "400", italic: false),
        Face(file: "Hack-Bold.ttf", family: "Hack", weight: "700", italic: false),
        Face(file: "Hack-Italic.ttf", family: "Hack", weight: "400", italic: true),
        Face(file: "Hack-BoldItalic.ttf", family: "Hack", weight: "700", italic: true),
        Face(file: "JetBrainsMono-VF.ttf", family: "JetBrains Mono", weight: "100 800", italic: false),
        Face(file: "JetBrainsMono-Italic-VF.ttf", family: "JetBrains Mono", weight: "100 800", italic: true),
        Face(file: "ObelMono-VF.ttf", family: "Obel Mono", weight: "100 900", italic: false),
        Face(file: "ObelMono-Italic-VF.ttf", family: "Obel Mono", weight: "100 900", italic: true),
        Face(file: "SymbolsNerdFontMono-Regular.ttf", family: symbolsFamily, weight: "400", italic: false),
    ]

    /// Where a face's file is: Fonts/ in the Mac app's Resources (its build
    /// script copies the folder), or the bundle's top level (Xcode flattens
    /// the synchronized folder).
    static func url(_ file: String, in bundle: Bundle) -> URL? {
        let name = (file as NSString).deletingPathExtension, ext = (file as NSString).pathExtension
        return bundle.url(forResource: name, withExtension: ext, subdirectory: "Fonts")
            ?? bundle.url(forResource: name, withExtension: ext)
    }

    nonisolated(unsafe) private static var registered = false

    /// Makes the bundled fonts available to this process (Core Text, AppKit,
    /// UIKit and SwiftUI; web views get them from `fontFaceCSS`). Once, at
    /// launch, before anything picks a font.
    @MainActor public static func register(bundle: Bundle = .main) {
        guard !registered else { return }
        registered = true
        let urls = faces.compactMap { url($0.file, in: bundle) }
        guard !urls.isEmpty else { return }
        CTFontManagerRegisterFontURLs(urls as CFArray, .process, false, nil)
    }

    /// `font` with Symbols Nerd Font Mono after it, for the characters it
    /// doesn't have (a Nerd Font has them already).
    public static func withSymbols(_ font: CTFont) -> CTFont {
        let name = CTFontCopyPostScriptName(font) as String
        let family = CTFontCopyFamilyName(font) as String
        guard !name.localizedCaseInsensitiveContains("nerd"), !family.localizedCaseInsensitiveContains("nerd"),
              name != symbolsName else { return font }
        let size = CTFontGetSize(font)
        let cascade = [CTFontDescriptorCreateWithNameAndSize(symbolsName as CFString, size)]
        let desc = CTFontDescriptorCreateCopyWithAttributes(CTFontCopyFontDescriptor(font),
                                                            [kCTFontCascadeListAttribute: cascade] as CFDictionary)
        return CTFontCreateWithFontDescriptor(desc, size, nil)
    }

    /// @font-face rules for the bundled fonts, for a page loaded from `page`
    /// (a file in the bundle): web views draw in another process, which
    /// doesn't see fonts registered in this one. The pages' font stacks end
    /// with Symbols Nerd Font Mono (apple/Editor/src).
    public static func fontFaceCSS(forPage page: URL, bundle: Bundle = .main) -> String {
        let dir = page.deletingLastPathComponent().standardizedFileURL.path + "/"
        return faces.compactMap { f -> String? in
            guard let u = url(f.file, in: bundle)?.standardizedFileURL.path, u.hasPrefix(dir) else { return nil }
            let rel = String(u.dropFirst(dir.count)).addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? ""
            return "@font-face { font-family: \"\(f.family)\"; src: url(\"\(rel)\"); font-weight: \(f.weight); font-style: \(f.italic ? "italic" : "normal"); }"
        }.joined(separator: "\n")
    }
}

extension BundledFonts {
    /// Adds `fontFaceCSS` to the page bundled as `name`.html, as it loads.
    @MainActor static func addFontFaces(to ucc: WKUserContentController, page name: String, bundle: Bundle = .main) {
        guard let page = bundle.url(forResource: name, withExtension: "html"),
              let json = try? JSONSerialization.data(withJSONObject: [fontFaceCSS(forPage: page, bundle: bundle)]),
              let css = String(data: json, encoding: .utf8) else { return }
        let js = "(() => { const s = document.createElement('style'); s.textContent = \(css)[0]; document.documentElement.appendChild(s) })()"
        ucc.addUserScript(WKUserScript(source: js, injectionTime: .atDocumentStart, forMainFrameOnly: true))
    }
}
