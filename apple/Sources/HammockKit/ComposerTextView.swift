import SwiftUI
import UniformTypeIdentifiers
#if os(macOS)
import AppKit
import Quartz
typealias PlatformFont = NSFont
typealias PlatformColor = NSColor
#else
import UIKit
typealias PlatformFont = UIFont
typealias PlatformColor = UIColor
#endif

// The composer's text: a native text view (NSTextView, UITextView), as
// SwiftUI's TextField can't hold a picture in its text or colour part of it.
// An attachment's chip is a text attachment, one character (the object
// replacement character), that carries the attachment's id in its file
// wrapper, so it survives being cut, pasted, dragged and undone.

/// A chip in the text, and the id of the attachment it stands for.
final class ChipAttachment: NSTextAttachment {
    var chipID = UUID()

    static let ext = "hammockchip"

    static func make(_ id: UUID, image: PlatformImage, font: PlatformFont) -> ChipAttachment {
        let fw = FileWrapper(regularFileWithContents: Data(id.uuidString.utf8))
        fw.preferredFilename = id.uuidString + "." + ext
        let a = ChipAttachment(data: nil, ofType: nil)
        a.fileWrapper = fw
        a.chipID = id
        // Centred on the text's capitals.
        let y = (font.capHeight - image.size.height) / 2
        #if os(macOS)
        let cell = ChipCell(imageCell: image)
        cell.baseline = y
        a.attachmentCell = cell
        #else
        a.image = image
        a.bounds = CGRect(x: 0, y: y, width: image.size.width, height: image.size.height)
        #endif
        return a
    }

    /// The id a text attachment carries: one of ours, or one that came back
    /// from a pasteboard (just the file wrapper).
    static func id(of a: NSTextAttachment) -> UUID? {
        if let c = a as? ChipAttachment { return c.chipID }
        if let name = a.fileWrapper?.preferredFilename ?? a.fileWrapper?.filename, name.hasSuffix("." + ext) {
            return UUID(uuidString: String(name.dropLast(ext.count + 1)))
        }
        if let d = a.fileWrapper?.regularFileContents ?? a.contents, let s = String(data: d, encoding: .utf8) { return UUID(uuidString: s) }
        return nil
    }
}

#if os(macOS)
private final class ChipCell: NSTextAttachmentCell {
    var baseline: CGFloat = 0
    override func cellBaselineOffset() -> NSPoint { NSPoint(x: 0, y: baseline) }
}
#endif

/// The composer's text view's settings, and its callbacks: the view's
/// coordinator, which the composer also keeps to edit the text (insert a
/// chip, complete a command) as typing would, undoably.
@MainActor
final class ComposerTextController: NSObject {
    #if os(macOS)
    fileprivate weak var view: ComposerNSTextView?
    #else
    fileprivate weak var view: ComposerUITextView?
    #endif

    var font: PlatformFont = .systemFont(ofSize: 14)
    var lineSpacing: CGFloat = 0
    var highlight: PlatformColor = .systemBlue
    /// Ranges (UTF-16) to colour: the commands it knows.
    var highlighted: (String) -> [NSRange] = { _ in [] }
    /// A chip's picture (nil: no such attachment, so the chip goes).
    var chipImage: (UUID) -> PlatformImage? = { _ in nil }
    var onChange: (ComposerDoc, Int?) -> Void = { _, _ in }
    var onSubmit: () -> Void = {}
    var onTab: () -> Bool = { false }
    var onFiles: ([URL]) -> Void = { _ in }
    var onImages: ([Data]) -> Void = { _ in }
    var onFocus: (Bool) -> Void = { _ in }
    var maxLines = 10
    /// What the view last reported, so a change from outside is told apart.
    fileprivate(set) var doc = ComposerDoc()
    private var chipsKey = ""

    // MARK: Editing, from the composer

    /// The text, set from code (a draft, after sending): not undoable.
    func set(_ d: ComposerDoc, cursorAtEnd: Bool = true) {
        doc = d
        guard let v = view else { return }
        let s = attributed(d)
        storage(v).setAttributedString(s)
        restyle()
        v.undoManager?.removeAllActions()
        if cursorAtEnd { select(NSRange(location: s.length, length: 0)) }
        report()
    }

    /// Puts a chip where the cursor is (the end, before the text's been
    /// clicked), with a space after it, as typing would.
    func insertChip(_ id: UUID) {
        guard let v = view, let a = chipString(id) else { return }
        let s = NSMutableAttributedString(attributedString: a)
        let r = selected(v)
        let text = storage(v).string as NSString
        let next = r.location + r.length < text.length ? text.character(at: r.location + r.length) : 0
        if next != 0x20, next != 0x0A { s.append(NSAttributedString(string: " ", attributes: typing())) }
        replace(r, with: s)
    }

    /// Replaces a range as typing would, and puts the cursor after it.
    func replace(_ r: NSRange, with s: NSAttributedString) {
        guard let v = view else { return }
        #if os(macOS)
        guard v.shouldChangeText(in: r, replacementString: s.string) else { return }
        v.textStorage?.replaceCharacters(in: r, with: s)
        v.didChangeText()
        v.setSelectedRange(NSRange(location: r.location + s.length, length: 0))
        #else
        let start = v.position(from: v.beginningOfDocument, offset: r.location)
        let end = v.position(from: v.beginningOfDocument, offset: r.location + r.length)
        if let start, let end, let tr = v.textRange(from: start, to: end) {
            // Through the text input system, for undo.
            v.selectedTextRange = tr
            if s.containsAttachments(in: NSRange(location: 0, length: s.length)) {
                let undoText = NSAttributedString(attributedString: v.attributedText.attributedSubstring(from: r))
                let um = v.undoManager
                v.textStorage.replaceCharacters(in: r, with: s)
                um?.registerUndo(withTarget: self) { c in c.replace(NSRange(location: r.location, length: s.length), with: undoText) }
                v.selectedRange = NSRange(location: r.location + s.length, length: 0)
                textChanged()
            } else {
                v.replace(tr, withText: s.string)
            }
        }
        #endif
    }

    func replace(_ r: NSRange, with string: String) {
        replace(r, with: NSAttributedString(string: string, attributes: typing()))
    }

    func focus() {
        guard let v = view else { return }
        #if os(macOS)
        v.window?.makeFirstResponder(v)
        #else
        if !v.isFirstResponder { v.becomeFirstResponder() }
        #endif
    }

    /// Settings changed (font, colours, chips' pictures): restyles the text.
    func refresh(chipsKey key: String) {
        guard let v = view else { return }
        if key != chipsKey {
            chipsKey = key
            // Chips drawn again (a thumbnail arrived, the appearance changed).
            let st = storage(v)
            var swaps: [(NSRange, NSAttributedString)] = []
            st.enumerateAttribute(.attachment, in: NSRange(location: 0, length: st.length)) { val, r, _ in
                guard let a = val as? NSTextAttachment, let id = ChipAttachment.id(of: a), let s = chipString(id) else { return }
                swaps.append((r, s))
            }
            if !swaps.isEmpty {
                let sel = selected(v)
                st.beginEditing()
                for (r, s) in swaps.reversed() { st.replaceCharacters(in: r, with: s) }
                st.endEditing()
                select(sel)
            }
        }
        restyle()
        #if os(macOS)
        v.typingAttributes = typing()
        #else
        v.typingAttributes = typing()
        #endif
    }

    // MARK: The text

    fileprivate func typing() -> [NSAttributedString.Key: Any] {
        let p = NSMutableParagraphStyle()
        p.lineSpacing = lineSpacing
        #if os(macOS)
        return [.font: font, .foregroundColor: NSColor.labelColor, .paragraphStyle: p]
        #else
        return [.font: font, .foregroundColor: UIColor.label, .paragraphStyle: p]
        #endif
    }

    private func chipString(_ id: UUID) -> NSAttributedString? {
        guard let img = chipImage(id) else { return nil }
        let s = NSMutableAttributedString(attachment: ChipAttachment.make(id, image: img, font: font))
        s.addAttributes(typing(), range: NSRange(location: 0, length: s.length))
        return s
    }

    private func attributed(_ d: ComposerDoc) -> NSAttributedString {
        let out = NSMutableAttributedString()
        var k = 0
        var run = ""
        func flush() {
            if !run.isEmpty { out.append(NSAttributedString(string: run, attributes: typing())) }
            run = ""
        }
        for ch in d.text {
            if ch == attachMark {
                flush()
                if k < d.chips.count, let s = chipString(d.chips[k]) { out.append(s) }
                k += 1
            } else {
                run.append(ch)
            }
        }
        flush()
        return out
    }

    #if os(macOS)
    private func storage(_ v: ComposerNSTextView) -> NSTextStorage { v.textStorage! }
    private func selected(_ v: ComposerNSTextView) -> NSRange { v.selectedRange() }
    private func select(_ r: NSRange) {
        guard let v = view else { return }
        v.setSelectedRange(NSRange(location: min(r.location, v.textStorage!.length), length: 0))
    }
    #else
    private func storage(_ v: ComposerUITextView) -> NSTextStorage { v.textStorage }
    private func selected(_ v: ComposerUITextView) -> NSRange { v.selectedRange }
    private func select(_ r: NSRange) {
        guard let v = view else { return }
        v.selectedRange = NSRange(location: min(r.location, v.textStorage.length), length: 0)
    }
    #endif

    /// After any change: chips that aren't (any more) attachments it has
    /// go, chips back from a pasteboard are drawn again, the text gets its
    /// font and colours (pasted text keeps none of its own), and the
    /// composer hears what it now says.
    fileprivate func textChanged() {
        guard let v = view else { return }
        let st = storage(v)
        var sel = selected(v)
        var edits: [(NSRange, NSAttributedString?)] = []
        st.enumerateAttribute(.attachment, in: NSRange(location: 0, length: st.length)) { val, r, _ in
            guard let a = val as? NSTextAttachment else { return }
            guard let id = ChipAttachment.id(of: a), let s = chipString(id) else {
                edits.append((r, nil))
                return
            }
            if !(a is ChipAttachment) { edits.append((r, s)) }
        }
        if !edits.isEmpty {
            st.beginEditing()
            for (r, s) in edits.reversed() {
                if let s { st.replaceCharacters(in: r, with: s) } else {
                    st.deleteCharacters(in: r)
                    if sel.location > r.location { sel.location -= r.length }
                }
            }
            st.endEditing()
            select(sel)
        }
        restyle()
        report()
    }

    private func restyle() {
        guard let v = view else { return }
        // Not while an input method is composing: its marked text has its own look.
        #if os(macOS)
        if v.hasMarkedText() { return }
        #else
        if v.markedTextRange != nil { return }
        #endif
        let st = storage(v)
        let all = NSRange(location: 0, length: st.length)
        st.beginEditing()
        for (k, val) in typing() { st.addAttribute(k, value: val, range: all) }
        for r in highlighted(st.string) where NSMaxRange(r) <= st.length {
            st.addAttribute(.foregroundColor, value: highlight, range: r)
        }
        st.endEditing()
    }

    fileprivate func report() {
        guard let v = view else { return }
        let st = storage(v)
        var d = ComposerDoc(text: st.string)
        st.enumerateAttribute(.attachment, in: NSRange(location: 0, length: st.length)) { val, _, _ in
            if let a = val as? NSTextAttachment, let id = ChipAttachment.id(of: a) { d.chips.append(id) }
        }
        doc = d
        let r = selected(v)
        onChange(d, r.length == 0 ? r.location : nil)
    }

    // MARK: Pasting and dropping files

    /// Pictures and movies (files, or a picture on its own) become
    /// attachments; text and chips paste as usual.
    #if os(macOS)
    fileprivate func takeFiles(_ pb: NSPasteboard) -> Bool {
        let types = pb.types ?? []
        if types.contains(.rtfd) || types.contains(.string) && !types.contains(.fileURL) { return false }
        if let urls = pb.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL], !urls.isEmpty {
            let media = urls.filter { url in
                guard let t = UTType(filenameExtension: url.pathExtension) else { return false }
                return t.conforms(to: .image) || t.conforms(to: .movie)
            }
            guard !media.isEmpty else { return false }
            onFiles(media)
            return true
        }
        if let png = pb.data(forType: .png) {
            onImages([png])
            return true
        }
        if let tiff = pb.data(forType: .tiff), let rep = NSBitmapImageRep(data: tiff), let png = rep.representation(using: .png, properties: [:]) {
            onImages([png])
            return true
        }
        return false
    }
    #endif
}

#if os(macOS)

// MARK: - Mac

final class ComposerNSTextView: NSTextView {
    weak var controller: ComposerTextController?

    /// NSTextView would take QuickLook's panel over for its own selected
    /// attachments (none: "No items selected"), from the attachments tray's.
    override func acceptsPreviewPanelControl(_ panel: QLPreviewPanel!) -> Bool { false }

    override func paste(_ sender: Any?) {
        if controller?.takeFiles(.general) == true { return }
        super.paste(sender)
    }

    override func performDragOperation(_ sender: any NSDraggingInfo) -> Bool {
        if sender.draggingSource as? NSTextView !== self, controller?.takeFiles(sender.draggingPasteboard) == true { return true }
        return super.performDragOperation(sender)
    }

    override func draggingEntered(_ sender: any NSDraggingInfo) -> NSDragOperation {
        let op = super.draggingEntered(sender)
        if sender.draggingSource as? NSTextView !== self, sender.draggingPasteboard.types?.contains(.fileURL) == true { return .copy }
        return op
    }
}

struct ComposerTextView: NSViewRepresentable {
    let controller: ComposerTextController
    /// Changes when the chips' pictures do (numbers, thumbnails, appearance).
    let chipsKey: String

    func makeCoordinator() -> ComposerTextController { controller }

    func makeNSView(context: Context) -> NSScrollView {
        let tv = ComposerNSTextView(usingTextLayoutManager: false)
        tv.controller = controller
        tv.delegate = context.coordinator
        tv.isRichText = true
        tv.importsGraphics = true
        tv.allowsImageEditing = false
        tv.allowsUndo = true
        tv.drawsBackground = false
        tv.isAutomaticQuoteSubstitutionEnabled = false
        tv.isAutomaticDashSubstitutionEnabled = false
        tv.isAutomaticTextReplacementEnabled = false
        // No Writing Tools button beside the text: it's a message to an agent.
        tv.writingToolsBehavior = .none
        tv.textContainerInset = .zero
        tv.textContainer?.lineFragmentPadding = 0
        tv.textContainer?.widthTracksTextView = true
        tv.isVerticallyResizable = true
        tv.isHorizontallyResizable = false
        tv.autoresizingMask = [.width]
        tv.registerForDraggedTypes([.fileURL, .png, .tiff])
        let sv = NSScrollView()
        sv.drawsBackground = false
        sv.hasVerticalScroller = true
        sv.autohidesScrollers = true
        sv.borderType = .noBorder
        sv.documentView = tv
        controller.view = tv
        tv.typingAttributes = controller.typing()
        controller.set(controller.doc)
        return sv
    }

    func updateNSView(_ sv: NSScrollView, context: Context) {
        controller.refresh(chipsKey: chipsKey)
    }

    func sizeThatFits(_ proposal: ProposedViewSize, nsView sv: NSScrollView, context: Context) -> CGSize? {
        guard let tv = sv.documentView as? NSTextView, let lm = tv.layoutManager, let tc = tv.textContainer else { return nil }
        let width = proposal.width ?? 300
        tc.containerSize = NSSize(width: width, height: .greatestFiniteMagnitude)
        lm.ensureLayout(for: tc)
        let line = lm.defaultLineHeight(for: controller.font) + controller.lineSpacing
        var h = lm.usedRect(for: tc).height
        if tv.string.isEmpty || tv.string.hasSuffix("\n") { h = max(h, line) }
        return CGSize(width: width, height: min(max(h, line), line * CGFloat(controller.maxLines)).rounded(.up))
    }
}

extension ComposerTextController: NSTextViewDelegate {
    func textDidChange(_ notification: Notification) { textChanged() }

    func textViewDidChangeSelection(_ notification: Notification) {
        guard let v = view else { return }
        let r = v.selectedRange()
        onChange(doc, r.length == 0 ? r.location : nil)
    }

    func textDidBeginEditing(_ notification: Notification) { onFocus(true) }
    func textDidEndEditing(_ notification: Notification) { onFocus(false) }

    func textView(_ textView: NSTextView, doCommandBy sel: Selector) -> Bool {
        switch sel {
        case #selector(NSResponder.insertNewline(_:)):
            // Return sends; ⇧↩ (and ⌥↩) start a new line.
            let mods = NSApp.currentEvent?.modifierFlags ?? []
            if mods.contains(.shift) || mods.contains(.option) {
                textView.insertNewlineIgnoringFieldEditor(nil)
            } else {
                onSubmit()
            }
            return true
        case #selector(NSResponder.insertTab(_:)):
            if !onTab() { textView.window?.selectNextKeyView(nil) }
            return true
        case #selector(NSResponder.insertBacktab(_:)):
            textView.window?.selectPreviousKeyView(nil)
            return true
        default:
            return false
        }
    }
}

#else

// MARK: - iPhone

final class ComposerUITextView: UITextView {
    weak var controller: ComposerTextController?

    override func canPerformAction(_ action: Selector, withSender sender: Any?) -> Bool {
        if action == #selector(paste(_:)), UIPasteboard.general.hasImages { return true }
        return super.canPerformAction(action, withSender: sender)
    }

    /// A picture on its own (no text with it: a web snippet carries both)
    /// is attached; anything else pastes as usual.
    override func paste(_ sender: Any?) {
        let pb = UIPasteboard.general
        if pb.hasImages, !pb.hasStrings, let images = pb.images, !images.isEmpty {
            controller?.onImages(images.compactMap { img in
                let alpha = img.cgImage.map { ![.none, .noneSkipFirst, .noneSkipLast].contains($0.alphaInfo) } ?? false
                return alpha ? img.pngData() : img.jpegData(compressionQuality: 0.9)
            })
            return
        }
        super.paste(sender)
    }

    override var keyCommands: [UIKeyCommand]? {
        let tab = UIKeyCommand(input: "\t", modifierFlags: [], action: #selector(tabPressed))
        tab.wantsPriorityOverSystemBehavior = true
        let send = UIKeyCommand(input: "\r", modifierFlags: .command, action: #selector(sendPressed))
        return [tab, send]
    }

    @objc private func tabPressed() { _ = controller?.onTab() }
    @objc private func sendPressed() { controller?.onSubmit() }
}

struct ComposerTextView: UIViewRepresentable {
    let controller: ComposerTextController
    let chipsKey: String

    func makeCoordinator() -> ComposerTextController { controller }

    func makeUIView(context: Context) -> ComposerUITextView {
        let tv = ComposerUITextView(usingTextLayoutManager: false)
        tv.controller = controller
        tv.delegate = context.coordinator
        tv.backgroundColor = .clear
        tv.textContainerInset = .zero
        tv.textContainer.lineFragmentPadding = 0
        tv.isScrollEnabled = true
        tv.allowsEditingTextAttributes = false
        tv.writingToolsBehavior = .none
        tv.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        controller.view = tv
        tv.typingAttributes = controller.typing()
        controller.set(controller.doc)
        return tv
    }

    func updateUIView(_ tv: ComposerUITextView, context: Context) {
        controller.refresh(chipsKey: chipsKey)
    }

    func sizeThatFits(_ proposal: ProposedViewSize, uiView tv: ComposerUITextView, context: Context) -> CGSize? {
        let width = proposal.width ?? 300
        let line = controller.font.lineHeight + controller.lineSpacing
        let h = tv.sizeThatFits(CGSize(width: width, height: .greatestFiniteMagnitude)).height
        return CGSize(width: width, height: min(max(h, line), line * CGFloat(controller.maxLines)).rounded(.up))
    }
}

extension ComposerTextController: UITextViewDelegate {
    func textViewDidChange(_ textView: UITextView) { textChanged() }

    func textViewDidChangeSelection(_ textView: UITextView) {
        let r = textView.selectedRange
        onChange(doc, r.length == 0 ? r.location : nil)
    }

    func textViewDidBeginEditing(_ textView: UITextView) { onFocus(true) }
    func textViewDidEndEditing(_ textView: UITextView) { onFocus(false) }
}

#endif
