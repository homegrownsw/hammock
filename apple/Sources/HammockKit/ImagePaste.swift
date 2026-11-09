#if os(iOS)
import ObjectiveC
import SwiftUI
import UIKit

/// Lets a SwiftUI text field take a pasted picture (the composer's: it
/// becomes an attachment). A text field's text view offers Paste only for
/// text, so with a photo on the pasteboard iOS showed no Paste at all.
///
/// SwiftUI has no say in that, so this finds the text view the field is
/// built on (beside this probe) and, for that one view only, answers the
/// paste: Paste is offered when the pasteboard has an image, and pasting an
/// image (with no text) hands the images to onImages. Other text views of
/// the same class are untouched (a mark on the instance decides).
struct ImagePasteProbe: UIViewRepresentable {
    let onImages: ([UIImage]) -> Void

    func makeUIView(context: Context) -> Probe {
        let v = Probe()
        v.isUserInteractionEnabled = false
        v.onImages = onImages
        return v
    }

    func updateUIView(_ v: Probe, context: Context) { v.onImages = onImages }

    final class Probe: UIView {
        var onImages: (([UIImage]) -> Void)?
        private weak var hooked: UITextView?

        override func didMoveToWindow() {
            super.didMoveToWindow()
            // The field's text view is laid out alongside: look once it is.
            DispatchQueue.main.async { [weak self] in self?.hook() }
        }

        override func layoutSubviews() {
            super.layoutSubviews()
            if hooked == nil { hook() }
        }

        private func hook() {
            guard hooked == nil, window != nil else { return }
            // Up a few levels from the probe, then the nearest text view below.
            var root: UIView? = self
            for _ in 0..<6 {
                guard let up = root?.superview else { break }
                root = up
                if let tv = Self.textView(in: up) {
                    ImagePaste.install(on: tv) { [weak self] images in self?.onImages?(images) }
                    hooked = tv
                    return
                }
            }
        }

        private static func textView(in v: UIView) -> UITextView? {
            if let tv = v as? UITextView, tv.isEditable { return tv }
            for s in v.subviews {
                if let tv = textView(in: s) { return tv }
            }
            return nil
        }
    }
}

enum ImagePaste {
    private static var handlerKey: UInt8 = 0
    private static var patched = Set<ObjectIdentifier>()

    /// Pasting an image into tv calls handler; text pastes as before.
    @MainActor static func install(on tv: UITextView, handler: @escaping ([UIImage]) -> Void) {
        objc_setAssociatedObject(tv, &handlerKey, Box(handler), .OBJC_ASSOCIATION_RETAIN_NONATOMIC)
        let cls: AnyClass = type(of: tv)
        guard patched.insert(ObjectIdentifier(cls)).inserted else { return }

        let paste = #selector(UIResponder.paste(_:))
        let canPerform = #selector(UIResponder.canPerformAction(_:withSender:))

        if let m = class_getInstanceMethod(cls, canPerform) {
            typealias Fn = @convention(c) (AnyObject, Selector, Selector, Any?) -> Bool
            let original = unsafeBitCast(method_getImplementation(m), to: Fn.self)
            let block: @convention(block) (AnyObject, Selector, Any?) -> Bool = { obj, action, sender in
                if action == paste, handlerFor(obj) != nil, UIPasteboard.general.hasImages { return true }
                return original(obj, canPerform, action, sender)
            }
            class_replaceMethod(cls, canPerform, imp_implementationWithBlock(block), method_getTypeEncoding(m))
        }
        if let m = class_getInstanceMethod(cls, paste) {
            typealias Fn = @convention(c) (AnyObject, Selector, Any?) -> Void
            let original = unsafeBitCast(method_getImplementation(m), to: Fn.self)
            let block: @convention(block) (AnyObject, Any?) -> Void = { obj, sender in
                let pb = UIPasteboard.general
                // Text comes first (a copied web snippet carries both).
                if let h = handlerFor(obj), pb.hasImages, !pb.hasStrings, let images = pb.images, !images.isEmpty {
                    h(images)
                    return
                }
                original(obj, paste, sender)
            }
            class_replaceMethod(cls, paste, imp_implementationWithBlock(block), method_getTypeEncoding(m))
        }
    }

    private static func handlerFor(_ obj: AnyObject) -> (([UIImage]) -> Void)? {
        (objc_getAssociatedObject(obj, &handlerKey) as? Box)?.handler
    }

    private final class Box {
        let handler: ([UIImage]) -> Void
        init(_ h: @escaping ([UIImage]) -> Void) { handler = h }
    }
}
#endif
