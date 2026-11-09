// Renders Hammock's app icon: a macOS squircle in forest green, lit from the
// top, with the mark (a brush-stroke tilde in six colours, glowing) across
// it. Writes an .iconset, converts it with iconutil, and writes a 1024 PNG
// beside the .icns.
// Usage: swift scripts/make-icon.swift apple/Resources/AppIcon.icns
//        swift scripts/make-icon.swift apple/Resources/AppIcon-Beta.icns beta
//        swift scripts/make-icon.swift apple/iOS/Hammock/AppIcon.icon/Assets/mark.png layer
// The beta (dev build) icon swaps the forest green for a lighter blue, to tell
// the two apart at a glance in the Dock and the app switcher.
// "layer" writes just the mark, on transparency: the foreground of the iOS
// icon (AppIcon.icon, an Icon Composer bundle whose icon.json supplies the
// green fill).
import AppKit
import CoreImage

let out = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "AppIcon.icns"
let mode = CommandLine.arguments.count > 2 ? CommandLine.arguments[2] : ""
let beta = mode == "beta"

func rgb(_ hex: UInt32, _ a: CGFloat = 1) -> CGColor {
    CGColor(srgbRed: CGFloat(hex >> 16 & 0xFF) / 255, green: CGFloat(hex >> 8 & 0xFF) / 255, blue: CGFloat(hex & 0xFF) / 255, alpha: a)
}
let space = CGColorSpace(name: CGColorSpace.sRGB)!
// The tile: light at the top centre falling off to near black.
let tile = beta ? [rgb(0x4FA6F0), rgb(0x2673C7), rgb(0x174C8A)] : [rgb(0x1D6046), rgb(0x0F3627), rgb(0x071A12)]
// The mark's six colours, left to right (HammockMark.spectrum in HammockKit).
let spectrum = [0x61D836, 0xFFD60A, 0xFF9F0A, 0xFF453A, 0xBF5AF2, 0x0A84FF].map { rgb(UInt32($0)) }

// The mark's outline on its 24-point grid, y down: the same path as
// HammockMark.path in apple/Sources/HammockKit/HammockMark.swift.
let markBounds = CGRect(x: 2.2, y: 7.7, width: 19.6, height: 8.7)
let tipSoftening: CGFloat = 0.6
func markPath(scale: CGFloat, origin: CGPoint) -> CGPath {
    func p(_ x: CGFloat, _ y: CGFloat) -> CGPoint { CGPoint(x: origin.x + x * scale, y: origin.y + y * scale) }
    let path = CGMutablePath()
    path.move(to: p(2.5, 13.2))
    path.addCurve(to: p(12, 11), control1: p(5, 7.8), control2: p(9, 8.4))
    path.addCurve(to: p(21.5, 9.6), control1: p(15, 13.6), control2: p(18.4, 14))
    path.addCurve(to: p(11.6, 13.6), control1: p(19, 16), control2: p(14.8, 16.2))
    path.addCurve(to: p(2.5, 13.2), control1: p(8.6, 11.2), control2: p(5.4, 10.8))
    path.closeSubpath()
    return path
}

func context(_ px: Int) -> CGContext {
    CGContext(data: nil, width: px, height: px, bitsPerComponent: 8, bytesPerRow: 0, space: space,
              bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
}

/// Draws the mark, fitted to `width` and centred on `centre`, in its colours.
/// The context is y up; the mark's grid is y down, so it's flipped here.
func drawMark(_ cg: CGContext, centre: CGPoint, width: CGFloat) {
    let scale = width / markBounds.width
    // In y-down terms, centred on the stroke itself; then flipped about the centre.
    let path = markPath(scale: scale, origin: CGPoint(x: -markBounds.midX * scale, y: -markBounds.midY * scale))
    var flip = CGAffineTransform(translationX: centre.x, y: centre.y).scaledBy(x: 1, y: -1)
    let placed = path.copy(using: &flip)!
    let tips = placed.copy(strokingWithWidth: tipSoftening * scale, lineCap: .round, lineJoin: .round, miterLimit: 10)
    let gradient = CGGradient(colorsSpace: space, colors: spectrum as CFArray, locations: nil)!
    let left = CGPoint(x: centre.x - markBounds.width / 2 * scale, y: centre.y)
    let right = CGPoint(x: centre.x + markBounds.width / 2 * scale, y: centre.y)
    for shape in [placed, tips] {
        cg.saveGState()
        cg.addPath(shape)
        cg.clip()
        cg.drawLinearGradient(gradient, start: left, end: right, options: [.drawsBeforeStartLocation, .drawsAfterEndLocation])
        cg.restoreGState()
    }
}

/// The mark with its glow: a blurred copy under the sharp one.
func drawGlowingMark(_ cg: CGContext, px: Int, centre: CGPoint, width: CGFloat) {
    let glow = context(px)
    drawMark(glow, centre: centre, width: width)
    let image = CIImage(cgImage: glow.makeImage()!)
    let blurred = image.clampedToExtent()
        .applyingFilter("CIGaussianBlur", parameters: [kCIInputRadiusKey: width * 0.045])
        .cropped(to: image.extent)
    let ci = CIContext(options: [.workingColorSpace: space])
    if let halo = ci.createCGImage(blurred, from: image.extent) {
        cg.saveGState()
        cg.setAlpha(px <= 64 ? 0.55 : 0.9)
        cg.draw(halo, in: CGRect(x: 0, y: 0, width: px, height: px))
        cg.restoreGState()
    }
    drawMark(cg, centre: centre, width: width)
}

func png(_ cg: CGContext) -> Data {
    NSBitmapImageRep(cgImage: cg.makeImage()!).representation(using: .png, properties: [:])!
}

if mode == "layer" {
    // The iOS icon's foreground: the mark across two thirds of the canvas,
    // a little above centre (it's wider than tall, so centred it looks low).
    let px = 1024
    let cg = context(px)
    let s = CGFloat(px)
    drawGlowingMark(cg, px: px, centre: CGPoint(x: s / 2, y: s * 0.51), width: s * 0.66)
    try! png(cg).write(to: URL(fileURLWithPath: out))
    print("wrote \(out)")
    exit(0)
}

func render(_ px: Int) -> Data {
    let cg = context(px)
    let s = CGFloat(px)
    // Apple's icon grid: 824/1024 body with a continuous-corner squircle.
    let inset = s * 100 / 1024
    let body = CGRect(x: inset, y: inset, width: s - inset * 2, height: s - inset * 2)
    let squircle = CGPath(roundedRect: body, cornerWidth: body.width * 0.225, cornerHeight: body.width * 0.225, transform: nil)
    cg.saveGState()
    cg.setShadow(offset: CGSize(width: 0, height: -s * 0.01), blur: s * 0.03, color: CGColor(gray: 0, alpha: 0.35))
    cg.addPath(squircle)
    cg.setFillColor(tile[2])
    cg.fillPath()
    cg.restoreGState()
    // Lit from the top centre.
    cg.saveGState()
    cg.addPath(squircle)
    cg.clip()
    let light = CGGradient(colorsSpace: space, colors: tile as CFArray, locations: [0, 0.52, 1])!
    let top = CGPoint(x: body.midX, y: body.maxY)
    cg.drawRadialGradient(light, startCenter: top, startRadius: 0, endCenter: top, endRadius: body.height * 1.05,
                          options: [.drawsAfterEndLocation])
    // A faint highlight along the top edge.
    let edge = CGGradient(colorsSpace: space, colors: [CGColor(gray: 1, alpha: 0.10), CGColor(gray: 1, alpha: 0)] as CFArray, locations: [0, 1])!
    cg.drawLinearGradient(edge, start: CGPoint(x: 0, y: body.maxY), end: CGPoint(x: 0, y: body.maxY - body.height * 0.12), options: [])
    cg.restoreGState()
    // The mark: across two thirds of the body, a hair above centre.
    drawGlowingMark(cg, px: px, centre: CGPoint(x: body.midX, y: body.midY + body.height * 0.01), width: body.width * 0.66)
    return png(cg)
}

let iconset = NSTemporaryDirectory() + "AppIcon.iconset"
try? FileManager.default.removeItem(atPath: iconset)
try! FileManager.default.createDirectory(atPath: iconset, withIntermediateDirectories: true)
for base in [16, 32, 128, 256, 512] {
    try! render(base).write(to: URL(fileURLWithPath: "\(iconset)/icon_\(base)x\(base).png"))
    try! render(base * 2).write(to: URL(fileURLWithPath: "\(iconset)/icon_\(base)x\(base)@2x.png"))
}
let p = Process()
p.executableURL = URL(fileURLWithPath: "/usr/bin/iconutil")
p.arguments = ["-c", "icns", iconset, "-o", out]
try! p.run()
p.waitUntilExit()
try! render(1024).write(to: URL(fileURLWithPath: (out as NSString).deletingPathExtension + "-1024.png"))
print("wrote \(out)")
