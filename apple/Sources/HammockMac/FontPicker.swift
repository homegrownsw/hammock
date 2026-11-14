import AppKit
import SwiftUI

/// A font menu for Settings: each family's name drawn in that family, as in
/// TextEdit's font menus. The first item, `none`, is the unset choice (tag
/// ""), drawn in `noneFont`. The button shows the chosen one in its font too.
/// An AppKit pop-up because SwiftUI's Picker drops a menu item's font on the
/// Mac.
struct FontPicker: View {
    let label: String
    @Binding var selection: String
    let none: String
    var noneFont: NSFont? = nil
    let families: [String]

    var body: some View {
        LabeledContent(label) {
            FontPopUp(selection: $selection, none: none, noneFont: noneFont, families: families)
                .fixedSize()
        }
    }
}

private struct FontPopUp: NSViewRepresentable {
    @Binding var selection: String
    let none: String
    let noneFont: NSFont?
    let families: [String]

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> FontPopUpButton {
        let b = FontPopUpButton(frame: .zero, pullsDown: false)
        b.target = context.coordinator
        b.action = #selector(Coordinator.picked(_:))
        (b.cell as? NSPopUpButtonCell)?.usesItemFromMenu = false
        return b
    }

    func updateNSView(_ b: FontPopUpButton, context: Context) {
        context.coordinator.selection = $selection
        let items = [none] + families
        if context.coordinator.built != items {
            context.coordinator.built = items
            build(b.menu ?? NSMenu())
        }
        // A family no longer installed still shows as chosen, as Picker did.
        let tag = selection
        if b.menu?.items.first(where: { ($0.representedObject as? String) == tag }) == nil, !tag.isEmpty {
            b.menu?.addItem(item(tag, font: nil))
            context.coordinator.built = nil
        }
        if let i = b.menu?.items.firstIndex(where: { ($0.representedObject as? String) == tag }) {
            b.selectItem(at: i)
        }
        let size = b.font?.pointSize ?? NSFont.systemFontSize
        b.showTitle(tag.isEmpty ? none : tag,
                    font: tag.isEmpty ? noneFont.map { NSFont(descriptor: $0.fontDescriptor, size: size) ?? $0 } : Self.font(tag, size: size))
    }

    private func build(_ menu: NSMenu) {
        menu.removeAllItems()
        let first = NSMenuItem(title: none, action: nil, keyEquivalent: "")
        first.representedObject = ""
        if let noneFont { first.attributedTitle = NSAttributedString(string: none, attributes: [.font: noneFont]) }
        menu.addItem(first)
        menu.addItem(.separator())
        let size = NSFont.menuFont(ofSize: 0).pointSize
        for family in families { menu.addItem(item(family, font: Self.font(family, size: size))) }
    }

    private func item(_ family: String, font: NSFont?) -> NSMenuItem {
        let m = NSMenuItem(title: family, action: nil, keyEquivalent: "")
        m.representedObject = family
        if let font { m.attributedTitle = NSAttributedString(string: family, attributes: [.font: font]) }
        return m
    }

    /// The family at that size, unless it can't draw its own name (a symbol
    /// font), which stays in the system font.
    private static func font(_ family: String, size: CGFloat) -> NSFont? {
        guard let f = NSFont(name: family, size: size)
            ?? NSFontManager.shared.font(withFamily: family, traits: [], weight: 5, size: size) else { return nil }
        let chars = Array(family.utf16)
        var glyphs = [CGGlyph](repeating: 0, count: chars.count)
        return CTFontGetGlyphsForCharacters(f, chars, &glyphs, chars.count) ? f : nil
    }

    @MainActor final class Coordinator: NSObject {
        var selection: Binding<String>?
        var built: [String]?

        @objc func picked(_ sender: NSPopUpButton) {
            selection?.wrappedValue = sender.selectedItem?.representedObject as? String ?? ""
        }
    }
}

/// Shows the chosen family in its own font, and sizes itself to it rather
/// than to the menu's widest name.
final class FontPopUpButton: NSPopUpButton {
    private var shown: NSMenuItem?

    func showTitle(_ title: String, font: NSFont?) {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        if let font = font.map(fitted) { item.attributedTitle = NSAttributedString(string: title, attributes: [.font: font]) }
        guard item.title != shown?.title || item.attributedTitle != shown?.attributedTitle,
              let cell = cell as? NSPopUpButtonCell else { return }
        shown = item
        cell.menuItem = item
        invalidateIntrinsicContentSize()
        needsDisplay = true
    }

    /// A font taller than the button's own (Zapfino, say) shrunk to fit it.
    private func fitted(_ f: NSFont) -> NSFont {
        let system = NSFont.systemFont(ofSize: f.pointSize)
        let limit = (system.ascender - system.descender) * 1.15
        let height = f.ascender - f.descender
        guard height > limit else { return f }
        return NSFont(descriptor: f.fontDescriptor, size: f.pointSize * limit / height) ?? f
    }

    override var intrinsicContentSize: NSSize {
        let probe = NSPopUpButton(frame: .zero, pullsDown: false)
        probe.controlSize = controlSize
        probe.font = font
        if let shown { probe.menu?.addItem(shown.copy() as! NSMenuItem) }
        return probe.intrinsicContentSize
    }
}
