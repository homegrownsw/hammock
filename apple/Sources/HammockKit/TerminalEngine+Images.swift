import CoreGraphics
import Foundation
import GhosttyVt
import ImageIO

/// An image a program put on screen with the Kitty graphics protocol, ready
/// to draw: where (in cells and pixels) and the pixels themselves.
public struct TerminalImage {
    public enum Layer { case belowBackground, belowText, aboveText }
    public var layer: Layer
    public var z: Int32
    public var col: Int32           // viewport cell of the top-left corner (may be negative)
    public var row: Int32
    public var offsetX: CGFloat      // pixels into that cell
    public var offsetY: CGFloat
    public var width: CGFloat        // pixels to draw at
    public var height: CGFloat
    public var image: CGImage        // already cropped to the source rectangle
}

/// Kitty images need storage, and PNGs a decoder (ImageIO here). Set up once
/// per process; each terminal then gets a storage limit.
enum KittyGraphicsSetup {
    static let storageLimit: UInt64 = 128 << 20

    /// Whether new terminals take Kitty images sent as a file path (t=f):
    /// `image-file-medium` in Hammock's config (set by the Mac app's AppConfig).
    nonisolated(unsafe) static var fileMedium = true

    static let installDecoder: Void = {
        let decode: GhosttySysDecodePngFn = { _, allocator, data, len, out in
            guard let data, let out, len > 0 else { return false }
            let cf = CFDataCreateWithBytesNoCopy(nil, data, len, kCFAllocatorNull)!
            guard let src = CGImageSourceCreateWithData(cf, nil), let img = CGImageSourceCreateImageAtIndex(src, 0, nil) else { return false }
            let w = img.width, h = img.height
            let n = w * h * 4
            guard w > 0, h > 0, let buf = ghostty_alloc(allocator, n) else { return false }
            guard let ctx = CGContext(data: buf, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                      space: CGColorSpace(name: CGColorSpace.sRGB)!,
                                      bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else {
                ghostty_free(allocator, buf, n)
                return false
            }
            ctx.clear(CGRect(x: 0, y: 0, width: w, height: h))
            ctx.draw(img, in: CGRect(x: 0, y: 0, width: w, height: h))
            // Kitty wants straight (unpremultiplied) RGBA.
            for i in stride(from: 0, to: n, by: 4) {
                let a = Int(buf[i + 3])
                if a > 0, a < 255 {
                    for c in 0..<3 { buf[i + c] = UInt8(min(255, Int(buf[i + c]) * 255 / a)) }
                }
            }
            out.pointee = GhosttySysImage(width: UInt32(w), height: UInt32(h), data: buf, data_len: n)
            return true
        }
        // The option's value is the function pointer itself.
        ghostty_sys_set(GHOSTTY_SYS_OPT_DECODE_PNG, unsafeBitCast(decode, to: UnsafeRawPointer.self))
    }()

    static func enable(_ terminal: GhosttyTerminal) {
        _ = installDecoder
        var limit = storageLimit
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_STORAGE_LIMIT, &limit)
        // Images sent as a file path (t=f), as the daemon accepts them (it
        // answers the program's queries); the file is on this Mac. Both
        // follow image-file-medium in Hammock's config.
        var file = fileMedium
        ghostty_terminal_set(terminal, GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_MEDIUM_FILE, &file)
    }
}

extension TerminalEngine {
    /// Whether terminals made from now on read Kitty images sent as a file
    /// path (t=f); `image-file-medium` in Hammock's config.
    public static var imageFileMedium: Bool {
        get { KittyGraphicsSetup.fileMedium }
        set { KittyGraphicsSetup.fileMedium = newValue }
    }

    /// The images on screen as of the last frame(), back to front within
    /// each layer. Pixel images are cached by image id and generation.
    public func images() -> [TerminalImage] {
        guard let terminal, let renderState else { return [] }
        var gen: UInt64 = 0
        ghostty_render_state_get(renderState, GHOSTTY_RENDER_STATE_DATA_IMAGE_PLACEMENTS_GENERATION, &gen)
        if gen == 0 { return [] } // Kitty graphics off, or nothing ever placed
        if imageIter == nil {
            var it: GhosttyRenderStateImageIterator?
            ghostty_render_state_image_iterator_new(nil, &it)
            imageIter = it
        }
        guard let it = imageIter else { return [] }
        var iter: GhosttyRenderStateImageIterator? = it
        guard ghostty_render_state_get(renderState, GHOSTTY_RENDER_STATE_DATA_IMAGE_ITERATOR, &iter) == GHOSTTY_SUCCESS else { return [] }
        var graphics: GhosttyKittyGraphics?
        ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_KITTY_GRAPHICS, &graphics)
        guard let graphics else { return [] }

        var out: [TerminalImage] = []
        var live = Set<String>()
        while ghostty_render_state_image_iterator_next(it) {
            func get<T>(_ d: GhosttyRenderStateImageData, _ v: T) -> T {
                var x = v
                ghostty_render_state_image_get(it, d, &x)
                return x
            }
            var proto = GHOSTTY_IMAGE_PROTOCOL_KITTY
            ghostty_render_state_image_get(it, GHOSTTY_RENDER_STATE_IMAGE_DATA_PROTOCOL, &proto)
            guard proto == GHOSTTY_IMAGE_PROTOCOL_KITTY else { continue }
            let id: UInt32 = get(GHOSTTY_RENDER_STATE_IMAGE_DATA_IMAGE_ID, 0)
            let generation: UInt64 = get(GHOSTTY_RENDER_STATE_IMAGE_DATA_IMAGE_GENERATION, 0)
            let key = "\(id):\(generation)"
            live.insert(key)
            guard let full = imageCache[key] ?? kittyImage(graphics, id) else { continue }
            imageCache[key] = full
            let sx: UInt32 = get(GHOSTTY_RENDER_STATE_IMAGE_DATA_SOURCE_X, 0)
            let sy: UInt32 = get(GHOSTTY_RENDER_STATE_IMAGE_DATA_SOURCE_Y, 0)
            let sw: UInt32 = get(GHOSTTY_RENDER_STATE_IMAGE_DATA_SOURCE_WIDTH, 0)
            let sh: UInt32 = get(GHOSTTY_RENDER_STATE_IMAGE_DATA_SOURCE_HEIGHT, 0)
            var img = full
            if sw > 0, sh > 0, Int(sw) != full.width || Int(sh) != full.height,
               let c = full.cropping(to: CGRect(x: Int(sx), y: Int(sy), width: Int(sw), height: Int(sh))) {
                img = c
            }
            var layer = GHOSTTY_RENDER_STATE_IMAGE_LAYER_ABOVE_TEXT
            ghostty_render_state_image_get(it, GHOSTTY_RENDER_STATE_IMAGE_DATA_LAYER, &layer)
            let l: TerminalImage.Layer = layer == GHOSTTY_RENDER_STATE_IMAGE_LAYER_BELOW_BACKGROUND ? .belowBackground
                : layer == GHOSTTY_RENDER_STATE_IMAGE_LAYER_BELOW_TEXT ? .belowText : .aboveText
            out.append(TerminalImage(
                layer: l, z: get(GHOSTTY_RENDER_STATE_IMAGE_DATA_Z, Int32(0)),
                col: get(GHOSTTY_RENDER_STATE_IMAGE_DATA_VIEWPORT_COL, Int32(0)),
                row: get(GHOSTTY_RENDER_STATE_IMAGE_DATA_VIEWPORT_ROW, Int32(0)),
                offsetX: CGFloat(get(GHOSTTY_RENDER_STATE_IMAGE_DATA_CELL_OFFSET_X, UInt32(0))),
                offsetY: CGFloat(get(GHOSTTY_RENDER_STATE_IMAGE_DATA_CELL_OFFSET_Y, UInt32(0))),
                width: CGFloat(get(GHOSTTY_RENDER_STATE_IMAGE_DATA_PIXEL_WIDTH, UInt32(0))),
                height: CGFloat(get(GHOSTTY_RENDER_STATE_IMAGE_DATA_PIXEL_HEIGHT, UInt32(0))),
                image: img))
        }
        imageCache = imageCache.filter { live.contains($0.key) }
        return out.sorted { $0.z < $1.z }
    }

    /// An image's pixels as a CGImage.
    private func kittyImage(_ graphics: GhosttyKittyGraphics, _ id: UInt32) -> CGImage? {
        guard let image = ghostty_kitty_graphics_image(graphics, id) else { return nil }
        var w: UInt32 = 0, h: UInt32 = 0
        var format = GHOSTTY_KITTY_IMAGE_FORMAT_RGBA
        var ptr: UnsafePointer<UInt8>?
        var len = 0
        ghostty_kitty_graphics_image_get(image, GHOSTTY_KITTY_IMAGE_DATA_WIDTH, &w)
        ghostty_kitty_graphics_image_get(image, GHOSTTY_KITTY_IMAGE_DATA_HEIGHT, &h)
        ghostty_kitty_graphics_image_get(image, GHOSTTY_KITTY_IMAGE_DATA_FORMAT, &format)
        ghostty_kitty_graphics_image_get(image, GHOSTTY_KITTY_IMAGE_DATA_DATA_PTR, &ptr)
        ghostty_kitty_graphics_image_get(image, GHOSTTY_KITTY_IMAGE_DATA_DATA_LEN, &len)
        guard let ptr, w > 0, h > 0, len > 0 else { return nil }
        let width = Int(w), height = Int(h)
        // Expand to RGBA.
        var rgba = [UInt8](repeating: 255, count: width * height * 4)
        let src = UnsafeBufferPointer(start: ptr, count: len)
        let px = width * height
        switch format {
        case GHOSTTY_KITTY_IMAGE_FORMAT_RGBA:
            guard len >= px * 4 else { return nil }
            rgba.withUnsafeMutableBufferPointer { _ = memcpy($0.baseAddress!, ptr, px * 4) }
        case GHOSTTY_KITTY_IMAGE_FORMAT_RGB:
            guard len >= px * 3 else { return nil }
            for i in 0..<px { rgba[i * 4] = src[i * 3]; rgba[i * 4 + 1] = src[i * 3 + 1]; rgba[i * 4 + 2] = src[i * 3 + 2] }
        case GHOSTTY_KITTY_IMAGE_FORMAT_GRAY_ALPHA:
            guard len >= px * 2 else { return nil }
            for i in 0..<px { let g = src[i * 2]; rgba[i * 4] = g; rgba[i * 4 + 1] = g; rgba[i * 4 + 2] = g; rgba[i * 4 + 3] = src[i * 2 + 1] }
        case GHOSTTY_KITTY_IMAGE_FORMAT_GRAY:
            guard len >= px else { return nil }
            for i in 0..<px { let g = src[i]; rgba[i * 4] = g; rgba[i * 4 + 1] = g; rgba[i * 4 + 2] = g }
        default:
            return nil
        }
        guard let provider = CGDataProvider(data: Data(rgba) as CFData) else { return nil }
        return CGImage(width: width, height: height, bitsPerComponent: 8, bitsPerPixel: 32, bytesPerRow: width * 4,
                       space: CGColorSpace(name: CGColorSpace.sRGB)!,
                       bitmapInfo: CGBitmapInfo(rawValue: CGImageAlphaInfo.last.rawValue),
                       provider: provider, decode: nil, shouldInterpolate: true, intent: .defaultIntent)
    }
}
