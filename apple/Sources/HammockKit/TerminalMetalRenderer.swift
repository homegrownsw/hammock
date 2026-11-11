import CoreGraphics
import CoreText
import Foundation
import Metal
import simd

/// Draws a terminal's rows with Metal (#18, docs/design/metal-renderer.md).
///
/// It draws the same `TerminalLine`s the CoreGraphics renderer does
/// (TerminalLineLayout), so what goes where is decided once. Glyphs are
/// rasterised by CoreText into an atlas, with the same settings the pane
/// draws with (no font smoothing unless font-thicken), at the quarter pixel
/// they land on; everything is then drawn as one batch of instanced quads.
/// Blending is in sRGB-encoded values, as CoreGraphics blends text, so text
/// keeps the weight it has with CoreText.
///
/// What each row becomes is kept by the line's id (a kept line keeps its
/// id), so a frame only builds the rows that are new.
public final class TerminalMetalRenderer {
    public static let pixelFormat = MTLPixelFormat.bgra8Unorm

    /// What every pane shares: the device, its queue, the pipeline and the
    /// glyph atlas (one per app, not per pane: it's 16 MB).
    public final class Context {
        public let device: MTLDevice
        let queue: MTLCommandQueue
        let pipeline: MTLRenderPipelineState
        let atlas: GlyphAtlas

        public static let shared: Context? = Context()

        init?() {
            guard let device = MTLCreateSystemDefaultDevice(), let queue = device.makeCommandQueue(),
                  let atlas = GlyphAtlas(device: device) else { return nil }
            do {
                let library = try device.makeLibrary(source: TerminalMetalRenderer.shaders, options: nil)
                let desc = MTLRenderPipelineDescriptor()
                desc.vertexFunction = library.makeFunction(name: "terminal_vertex")
                desc.fragmentFunction = library.makeFunction(name: "terminal_fragment")
                let attachment = desc.colorAttachments[0]!
                attachment.pixelFormat = TerminalMetalRenderer.pixelFormat
                // Premultiplied source over, in encoded sRGB (bgra8Unorm, not
                // _srgb): how CoreGraphics composites text into the pane.
                attachment.isBlendingEnabled = true
                attachment.rgbBlendOperation = .add
                attachment.alphaBlendOperation = .add
                attachment.sourceRGBBlendFactor = .one
                attachment.sourceAlphaBlendFactor = .one
                attachment.destinationRGBBlendFactor = .oneMinusSourceAlpha
                attachment.destinationAlphaBlendFactor = .oneMinusSourceAlpha
                pipeline = try device.makeRenderPipelineState(descriptor: desc)
            } catch {
                return nil
            }
            self.device = device
            self.queue = queue
            self.atlas = atlas
        }
    }

    public let context: Context
    public var device: MTLDevice { context.device }
    private var atlas: GlyphAtlas { context.atlas }

    /// One quad: a rectangle, or a glyph from the atlas.
    struct Instance {
        var rect: SIMD4<Float>   // x, y, width, height in device pixels
        var uv: SIMD4<Float>     // atlas pixels: x0, y0, x1, y1
        var color: SIMD4<Float>  // straight (not premultiplied) sRGB
        var clip: SIMD4<Float>   // x0, y0, x1, y1 in device pixels; x1 < x0: none
        var kind: UInt32         // 0 rectangle, 1 glyph tinted with color, 2 colour glyph
        var pad: SIMD3<UInt32> = .zero
    }

    /// What a row draws: its backgrounds (all rows' go first), then the rest,
    /// relative to the row's top left (inside the padding), in pixels.
    private struct RowInstances { var back: [Instance]; var front: [Instance] }
    private var rows: [UInt64: RowInstances] = [:]
    private var olderRows: [UInt64: RowInstances] = [:]
    public var capacity = 512

    /// What the kept rows were built for: the atlas they point into, the
    /// scale and the rows' height (row backgrounds are drawn that tall).
    private var builtFor: (atlas: Int, scale: CGFloat, rowHeight: CGFloat) = (-1, 0, 0)
    private var rowHeight: CGFloat { builtFor.rowHeight }
    private var colors: [TerminalColor: SIMD4<Float>] = [:]

    public init?(context: Context? = .shared) {
        guard let context else { return nil }
        self.context = context
    }

    /// Everything a frame shows, in points (scaled by `scale`).
    public struct Scene {
        public var size: CGSize
        public var scale: CGFloat
        public var thicken: Bool
        public var background: RGB
        public var padding: CGFloat
        public var cellHeight: CGFloat
        /// Rows to draw, each at its top: backgrounds of all of them first.
        public var rows: [(top: CGFloat, line: TerminalLine)] = []
        /// Drawn after the rows (the cursor, a link's underline), in the
        /// pane's own space.
        public var overlay: [TerminalLine.Rect] = []
        /// Text drawn after the overlay (the character under a block cursor).
        public var overlayText: [(top: CGFloat, line: TerminalLine)] = []

        public init(size: CGSize, scale: CGFloat, thicken: Bool, background: RGB, padding: CGFloat, cellHeight: CGFloat) {
            self.cellHeight = cellHeight
            self.size = size
            self.scale = scale
            self.thicken = thicken
            self.background = background
            self.padding = padding
        }
    }

    /// Encodes a frame into `texture` (its size in pixels is the scene's).
    /// Returns the command buffer, committed; present or wait on it.
    @discardableResult
    public func render(_ scene: Scene, to texture: MTLTexture, before commit: ((MTLCommandBuffer) -> Void)? = nil) -> MTLCommandBuffer? {
        if builtFor != (atlas.generation, scene.scale, scene.cellHeight) {
            builtFor = (atlas.generation, scene.scale, scene.cellHeight)
            rows = [:]
            olderRows = [:]
        }
        var instances = build(scene)
        if atlas.overflowed {
            // The atlas filled up while building: start it afresh (every
            // pane builds its rows again) and build this frame again.
            atlas.reset()
            builtFor.atlas = atlas.generation
            rows = [:]
            olderRows = [:]
            instances = build(scene)
        }
        atlas.upload()

        guard let cb = context.queue.makeCommandBuffer() else { return nil }
        let pass = MTLRenderPassDescriptor()
        pass.colorAttachments[0].texture = texture
        pass.colorAttachments[0].loadAction = .clear
        pass.colorAttachments[0].storeAction = .store
        let bg = scene.background
        pass.colorAttachments[0].clearColor = MTLClearColor(red: Double(bg.r) / 255, green: Double(bg.g) / 255, blue: Double(bg.b) / 255, alpha: 1)
        guard let enc = cb.makeRenderCommandEncoder(descriptor: pass) else { return nil }
        if !instances.isEmpty {
            let length = instances.count * MemoryLayout<Instance>.stride
            let buffer = instances.withUnsafeBytes { device.makeBuffer(bytes: $0.baseAddress!, length: length, options: .storageModeShared) }
            var viewport = SIMD2<Float>(Float(texture.width), Float(texture.height))
            enc.setRenderPipelineState(context.pipeline)
            enc.setVertexBuffer(buffer, offset: 0, index: 0)
            enc.setVertexBytes(&viewport, length: MemoryLayout<SIMD2<Float>>.size, index: 1)
            enc.setFragmentTexture(atlas.texture, index: 0)
            enc.drawPrimitives(type: .triangleStrip, vertexStart: 0, vertexCount: 4, instanceCount: instances.count)
        }
        enc.endEncoding()
        commit?(cb)
        cb.commit()
        return cb
    }

    // MARK: Building

    private func build(_ scene: Scene) -> [Instance] {
        let s = Float(scene.scale)
        let pad = Float(scene.padding) * s
        var back: [Instance] = [], front: [Instance] = []
        back.reserveCapacity(scene.rows.count * 8)
        front.reserveCapacity(scene.rows.count * 200)
        for (top, line) in scene.rows {
            let r = instances(for: line, scale: scene.scale, thicken: scene.thicken)
            let dy = Float(top) * s
            for var i in r.back { i.rect.x += pad; i.rect.y += dy; back.append(i) }
            for var i in r.front { shift(&i, dx: pad, dy: dy); front.append(i) }
        }
        var all = back
        all += front
        for r in scene.overlay { all.append(rect(r.rect, color: r.color, scale: scene.scale)) }
        for (top, line) in scene.overlayText {
            let r = build(line, scale: scene.scale, thicken: scene.thicken)
            let dy = Float(top) * s
            for var i in r.front { shift(&i, dx: pad, dy: dy); all.append(i) }
        }
        return all
    }

    private func shift(_ i: inout Instance, dx: Float, dy: Float) {
        i.rect.x += dx
        i.rect.y += dy
        if i.clip.z >= i.clip.x {
            i.clip.x += dx; i.clip.z += dx
            i.clip.y += dy; i.clip.w += dy
        }
    }

    private func instances(for line: TerminalLine, scale: CGFloat, thicken: Bool) -> RowInstances {
        if let r = rows[line.id] { return r }
        if let r = olderRows.removeValue(forKey: line.id) {
            keep(line.id, r)
            return r
        }
        let r = build(line, scale: scale, thicken: thicken)
        keep(line.id, r)
        return r
    }

    private func keep(_ id: UInt64, _ r: RowInstances) {
        if rows.count >= capacity {
            olderRows = rows
            rows = Dictionary(minimumCapacity: capacity)
        }
        rows[id] = r
    }

    private func color(_ c: TerminalColor) -> SIMD4<Float> {
        if let v = colors[c] { return v }
        let v = SIMD4<Float>(Float(c.rgb.r) / 255, Float(c.rgb.g) / 255, Float(c.rgb.b) / 255, Float(c.alpha))
        if colors.count > 4096 { colors = [:] }
        colors[c] = v
        return v
    }

    private static let noClip = SIMD4<Float>(1, 0, 0, 0)

    private func rect(_ r: CGRect, color c: TerminalColor, scale: CGFloat) -> Instance {
        let s = Float(scale)
        return Instance(rect: SIMD4(Float(r.minX) * s, Float(r.minY) * s, Float(r.width) * s, Float(r.height) * s),
                        uv: .zero, color: color(c), clip: Self.noClip, kind: 0)
    }

    private func build(_ line: TerminalLine, scale: CGFloat, thicken: Bool) -> RowInstances {
        let s = Float(scale)
        var r = RowInstances(back: [], front: [])
        for b in line.backgrounds {
            r.back.append(Instance(rect: SIMD4(Float(b.x) * s, 0, Float(b.width) * s, Float(rowHeight) * s),
                                   uv: .zero, color: color(TerminalColor(b.color)), clip: Self.noClip, kind: 0))
        }
        for x in line.rects { r.front.append(rect(x.rect, color: x.color, scale: scale)) }
        for item in line.text {
            switch item {
            case .glyphs(let g):
                let c = color(g.color)
                for (glyph, p) in zip(g.glyphs, g.positions) {
                    // Glyph space to the row's pixels: at (x, baseline), y up.
                    let m = CGAffineTransform(a: scale, b: 0, c: 0, d: -scale, tx: p.x * scale, ty: -p.y * scale)
                    if let i = glyphInstance(g.font, glyph, m, color: c, clip: nil, thicken: thicken) { r.front.append(i) }
                }
            case .fitted(let f):
                let m = CGAffineTransform(scaleX: 1, y: -1).concatenating(f.transform).concatenating(CGAffineTransform(scaleX: scale, y: scale))
                let clip = f.clip.map { CGRect(x: $0.minX * scale, y: $0.minY * scale, width: $0.width * scale, height: $0.height * scale) }
                if let i = glyphInstance(f.font, f.glyph, m, color: color(f.color), clip: clip, thicken: thicken) { r.front.append(i) }
            }
        }
        return r
    }

    /// A glyph drawn with `m` (glyph space to the row's pixels, y down).
    /// Placed as CoreGraphics places glyphs, so it lands on the same
    /// pixels: its origin on a whole pixel vertically and a quarter pixel
    /// across (subpixel positioning is horizontal only). Whole pixels move
    /// the quad; the quarter is rasterised in.
    private func glyphInstance(_ font: CTFont, _ glyph: CGGlyph, _ m: CGAffineTransform, color: SIMD4<Float>, clip: CGRect?, thicken: Bool) -> Instance? {
        let qx = (m.tx * 4).rounded() / 4
        let ix = floor(qx), iy = ceil(m.ty)
        let fx = qx - ix, fy: CGFloat = 0
        var local = m
        local.tx = fx
        local.ty = fy
        guard let e = atlas.glyph(font, glyph, local, thicken: thicken) else { return nil }
        let x = Float(ix) + Float(e.origin.x), y = Float(iy) + Float(e.origin.y)
        let clipV = clip.map { SIMD4<Float>(Float($0.minX), Float($0.minY), Float($0.maxX), Float($0.maxY)) } ?? Self.noClip
        return Instance(rect: SIMD4(x, y, Float(e.size.width), Float(e.size.height)),
                        uv: SIMD4(Float(e.uv.minX), Float(e.uv.minY), Float(e.uv.maxX), Float(e.uv.maxY)),
                        color: color, clip: clipV, kind: e.colored ? 2 : 1)
    }

    /// Glyphs and rows built so far, and how full the atlas is (the bench).
    public var stats: (glyphs: Int, rows: Int, atlasFull: Double) { (atlas.count, rows.count + olderRows.count, atlas.fullness) }

    static let shaders = """
    #include <metal_stdlib>
    using namespace metal;

    struct Instance { float4 rect; float4 uv; float4 color; float4 clip; uint kind; uint3 pad; };
    struct VOut {
        float4 position [[position]];
        float2 uv;
        float2 px;
        float4 color;
        float4 clip [[flat]];
        uint kind [[flat]];
    };

    vertex VOut terminal_vertex(uint vid [[vertex_id]], uint iid [[instance_id]],
                                const device Instance *instances [[buffer(0)]],
                                constant float2 &viewport [[buffer(1)]]) {
        Instance i = instances[iid];
        float2 corner = float2(vid & 1, vid >> 1);
        float2 p = i.rect.xy + corner * i.rect.zw;
        VOut o;
        o.position = float4(p.x / viewport.x * 2.0 - 1.0, 1.0 - p.y / viewport.y * 2.0, 0.0, 1.0);
        o.uv = mix(i.uv.xy, i.uv.zw, corner);
        o.px = p;
        o.color = i.color;
        o.clip = i.clip;
        o.kind = i.kind;
        return o;
    }

    fragment float4 terminal_fragment(VOut in [[stage_in]], texture2d<float> atlas [[texture(0)]]) {
        if (in.clip.z >= in.clip.x &&
            (in.px.x < in.clip.x || in.px.x > in.clip.z || in.px.y < in.clip.y || in.px.y > in.clip.w)) {
            discard_fragment();
        }
        float4 c = in.color;
        if (in.kind == 0) { return float4(c.rgb * c.a, c.a); }
        constexpr sampler s(coord::pixel, filter::nearest);
        float4 t = atlas.sample(s, in.uv);
        if (in.kind == 1) { float a = t.a * c.a; return float4(c.rgb * a, a); }
        return t * c.a;
    }
    """
}

/// Glyphs rasterised by CoreText into one RGBA texture, packed in shelves.
/// Monochrome glyphs are white with their coverage as alpha (tinted when
/// drawn); colour glyphs (emoji) keep their colours, premultiplied.
final class GlyphAtlas {
    struct Entry {
        var uv: CGRect          // in the atlas, pixels
        var origin: CGPoint     // the bitmap's top left from the glyph's whole-pixel origin
        var size: CGSize
        var colored: Bool
    }

    private struct Key: Hashable {
        let font: FontKey
        let glyph: CGGlyph
        let a: Float, b: Float, c: Float, d: Float, tx: Float, ty: Float
        let thicken: Bool
    }

    /// A CTFont by value: fallback fonts come back as new objects per line.
    private struct FontKey: Hashable {
        let font: CTFont
        func hash(into h: inout Hasher) { h.combine(CFHash(font)) }
        static func == (l: FontKey, r: FontKey) -> Bool { l.font === r.font || CFEqual(l.font, r.font) }
    }

    let texture: MTLTexture
    let width = 2048, height = 2048
    private var pixels: [UInt8]
    private var dirty: (minY: Int, maxY: Int)?
    private var entries: [Key: Entry?] = [:]
    private var shelfX = 1, shelfY = 1, shelfH = 0
    private(set) var overflowed = false
    /// Bumped by each reset: rows built against an older one are stale.
    private(set) var generation = 0
    var count: Int { entries.count }
    var fullness: Double { Double(shelfY + shelfH) / Double(height) }
    private let space = CGColorSpace(name: CGColorSpace.sRGB)!

    init?(device: MTLDevice) {
        let desc = MTLTextureDescriptor.texture2DDescriptor(pixelFormat: .rgba8Unorm, width: 2048, height: 2048, mipmapped: false)
        desc.usage = .shaderRead
        guard let t = device.makeTexture(descriptor: desc) else { return nil }
        texture = t
        pixels = [UInt8](repeating: 0, count: 2048 * 2048 * 4)
    }

    func reset() {
        generation += 1
        entries = [:]
        shelfX = 1; shelfY = 1; shelfH = 0
        overflowed = false
        pixels.withUnsafeMutableBytes { _ = memset($0.baseAddress!, 0, $0.count) }
        dirty = (0, height - 1)
    }

    /// The glyph drawn with `m` (its linear part, and a translation under a
    /// pixel), rasterised if it's new. nil: nothing to draw (a space).
    func glyph(_ font: CTFont, _ glyph: CGGlyph, _ m: CGAffineTransform, thicken: Bool) -> Entry? {
        let key = Key(font: FontKey(font: font), glyph: glyph, a: Float(m.a), b: Float(m.b), c: Float(m.c), d: Float(m.d), tx: Float(m.tx), ty: Float(m.ty), thicken: thicken)
        if let e = entries[key] { return e }
        let e = rasterise(font, glyph, m, thicken: thicken)
        entries[key] = e
        return e
    }

    private func rasterise(_ font: CTFont, _ glyph: CGGlyph, _ m: CGAffineTransform, thicken: Bool) -> Entry? {
        var g = glyph
        var bounds = CGRect.zero
        CTFontGetBoundingRectsForGlyphs(font, .horizontal, &g, &bounds, 1)
        guard bounds.width > 0, bounds.height > 0 else { return nil }
        // Its bounds in pixels, with room for anti-aliasing.
        let px = bounds.applying(m).insetBy(dx: -2, dy: -2)
        let x0 = Int(floor(px.minX)), y0 = Int(floor(px.minY))
        let w = Int(ceil(px.maxX)) - x0, h = Int(ceil(px.maxY)) - y0
        guard w > 0, h > 0, w < width / 4, h < height / 4 else { return nil }
        guard let slot = place(w, h) else { overflowed = true; return nil }

        guard let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4, space: space,
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return nil }
        ctx.setShouldAntialias(true)
        ctx.setAllowsFontSmoothing(true)
        ctx.setShouldSmoothFonts(thicken)
        ctx.setShouldSubpixelPositionFonts(true)
        ctx.setShouldSubpixelQuantizeFonts(false)
        // The row's pixels (y down) to the bitmap's (y up).
        ctx.translateBy(x: CGFloat(-x0), y: CGFloat(h + y0))
        ctx.scaleBy(x: 1, y: -1)
        ctx.concatenate(m)
        ctx.setFillColor(CGColor(srgbRed: 1, green: 1, blue: 1, alpha: 1))
        var origin = CGPoint.zero
        CTFontDrawGlyphs(font, &g, &origin, 1, ctx)
        guard let data = ctx.data else { return nil }
        let src = data.bindMemory(to: UInt8.self, capacity: w * h * 4)
        pixels.withUnsafeMutableBytes { buf in
            for row in 0..<h {
                memcpy(buf.baseAddress! + ((slot.y + row) * width + slot.x) * 4, src + row * w * 4, w * 4)
            }
        }
        dirty = (min(dirty?.minY ?? slot.y, slot.y), max(dirty?.maxY ?? 0, slot.y + h - 1))
        let colored = CTFontGetSymbolicTraits(font).contains(.traitColorGlyphs)
        return Entry(uv: CGRect(x: slot.x, y: slot.y, width: w, height: h), origin: CGPoint(x: x0, y: y0),
                     size: CGSize(width: w, height: h), colored: colored)
    }

    private func place(_ w: Int, _ h: Int) -> (x: Int, y: Int)? {
        if shelfX + w + 1 > width {
            shelfY += shelfH + 1
            shelfX = 1
            shelfH = 0
        }
        guard shelfY + h + 1 <= height else { return nil }
        let at = (shelfX, shelfY)
        shelfX += w + 1
        shelfH = max(shelfH, h)
        return at
    }

    /// Copies what's changed to the texture.
    func upload() {
        guard let d = dirty else { return }
        dirty = nil
        pixels.withUnsafeBytes { buf in
            texture.replace(region: MTLRegionMake2D(0, d.minY, width, d.maxY - d.minY + 1), mipmapLevel: 0,
                            withBytes: buf.baseAddress! + d.minY * width * 4, bytesPerRow: width * 4)
        }
    }
}
