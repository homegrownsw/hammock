import CoreGraphics
import CoreText
import Foundation
import Metal
import Testing
@testable import HammockKit

// The Metal renderer (#18), drawing into a texture: what lands where. Skipped
// where there's no Metal device. One at a time: renderers share the glyph
// atlas, which (as in the apps, on the main thread) isn't for concurrent use.
@Suite(.serialized) struct TerminalMetalRendererTests {

private let white = RGB(r: 255, g: 255, b: 255), black = RGB(r: 0, g: 0, b: 0)
private let red = RGB(r: 255, g: 0, b: 0), blue = RGB(r: 0, g: 0, b: 255)

private func fonts() -> TerminalFonts {
    let font = CTFontCreateWithName("Menlo" as CFString, 14, nil)
    return TerminalFonts(regular: font, bold: font, italic: font, boldItalic: font, cellWidth: 8.5, cellHeight: 18, baseline: 14, scale: 2)
}

private func layout() -> TerminalLineLayout {
    TerminalLineLayout(fonts: fonts(), palette: TerminalPalette(foreground: black, background: white, selection: blue, selectionForeground: nil))
}

/// Renders `scene` (10 × 4 cells at 2x, no padding) and returns its pixels, BGRA.
private func render(_ build: (inout TerminalMetalRenderer.Scene) -> Void) -> (pixels: [UInt8], width: Int)? {
    guard let r = TerminalMetalRenderer() else { return nil }
    let size = CGSize(width: 85, height: 72)
    var scene = TerminalMetalRenderer.Scene(size: size, scale: 2, thicken: false, background: white, padding: 0, cellHeight: 18)
    build(&scene)
    let w = 170, h = 144
    let desc = MTLTextureDescriptor.texture2DDescriptor(pixelFormat: TerminalMetalRenderer.pixelFormat, width: w, height: h, mipmapped: false)
    desc.usage = [.renderTarget, .shaderRead]
    desc.storageMode = .shared
    guard let t = r.device.makeTexture(descriptor: desc), let cb = r.render(scene, to: t) else { return nil }
    cb.waitUntilCompleted()
    var p = [UInt8](repeating: 0, count: w * h * 4)
    t.getBytes(&p, bytesPerRow: w * 4, from: MTLRegionMake2D(0, 0, w, h), mipmapLevel: 0)
    return (p, w)
}

/// The pixel at (x, y) in points, as RGB.
private func pixel(_ r: (pixels: [UInt8], width: Int), _ x: CGFloat, _ y: CGFloat) -> RGB {
    let i = (Int(y * 2) * r.width + Int(x * 2)) * 4
    return RGB(r: r.pixels[i + 2], g: r.pixels[i + 1], b: r.pixels[i])
}

@Test func drawsBackgroundsAndGlyphs() throws {
    let l = layout()
    var row = "X  ".map { TerminalCell(text: $0 == " " ? "" : String($0)) }
    row[2].bg = red
    guard let out = render({ $0.rows = [(0, l.line(row))] }) else { return } // no Metal here
    #expect(pixel(out, 21, 9) == red)            // the third cell's background
    #expect(pixel(out, 60, 30) == white)         // the cleared background
    // The X has ink somewhere in its cell.
    var ink = false
    for y in stride(from: 2.0, to: 17, by: 0.5) { for x in stride(from: 0.5, to: 8, by: 0.5) where pixel(out, x, y) != white { ink = true } }
    #expect(ink)
}

@Test func drawsImagesInTheirLayers() throws {
    let l = layout()
    var row = [TerminalCell](repeating: TerminalCell(text: ""), count: 10)
    for i in row.indices { row[i].bg = red }
    let ctx = CGContext(data: nil, width: 4, height: 4, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                        bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    ctx.setFillColor(CGColor(srgbRed: 0, green: 0, blue: 1, alpha: 1))
    ctx.fill(CGRect(x: 0, y: 0, width: 4, height: 4))
    let image = ctx.makeImage()!
    func scene(_ layer: TerminalImage.Layer) -> (pixels: [UInt8], width: Int)? {
        render { s in
            s.rows = [(0, l.line(row))]
            s.images = [.init(key: "1:1", source: image, sourceRect: CGRect(x: 0, y: 0, width: 4, height: 4),
                              rect: CGRect(x: 0, y: 0, width: 40, height: 18), layer: layer)]
        }
    }
    guard let below = scene(.belowBackground), let above = scene(.aboveText) else { return }
    #expect(pixel(below, 20, 9) == red)   // under the cells' backgrounds
    #expect(pixel(above, 20, 9) == blue)  // over everything
    #expect(pixel(above, 60, 9) == red)   // only where it's placed
}

@Test func keepsRowsItHasBuilt() throws {
    let l = layout()
    let row = "hello".map { TerminalCell(text: String($0)) }
    guard let r = TerminalMetalRenderer() else { return }
    let desc = MTLTextureDescriptor.texture2DDescriptor(pixelFormat: TerminalMetalRenderer.pixelFormat, width: 100, height: 40, mipmapped: false)
    desc.usage = [.renderTarget]
    let t = try #require(r.device.makeTexture(descriptor: desc))
    var scene = TerminalMetalRenderer.Scene(size: CGSize(width: 50, height: 20), scale: 2, thicken: false, background: white, padding: 0, cellHeight: 18)
    scene.rows = [(0, l.line(row))]
    r.render(scene, to: t)?.waitUntilCompleted()
    let built = r.stats.rows
    r.render(scene, to: t)?.waitUntilCompleted()
    #expect(r.stats.rows == built && built >= 1)
}
}
