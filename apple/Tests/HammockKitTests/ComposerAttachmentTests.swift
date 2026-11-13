import CoreGraphics
import ImageIO
import UniformTypeIdentifiers
import XCTest
@testable import HammockKit

final class ComposerAttachmentTests: XCTestCase {
    /// A PNG of w×h pixels, opaque or not.
    private func png(_ w: Int, _ h: Int, alpha: Bool = false) -> Data {
        let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(),
                            bitmapInfo: alpha ? CGImageAlphaInfo.premultipliedLast.rawValue : CGImageAlphaInfo.noneSkipLast.rawValue)!
        ctx.setFillColor(red: 1, green: 0.5, blue: 0, alpha: alpha ? 0.5 : 1)
        ctx.fill(CGRect(x: 0, y: 0, width: w, height: h))
        let out = NSMutableData()
        let dst = CGImageDestinationCreateWithData(out, UTType.png.identifier as CFString, 1, nil)!
        CGImageDestinationAddImage(dst, ctx.makeImage()!, nil)
        CGImageDestinationFinalize(dst)
        return out as Data
    }

    func testResizedCapsTheLongEdge() {
        let (name, data) = ImageResize.resized(name: "shot.png", data: png(3000, 1500), maxEdge: 1024)
        XCTAssertEqual(name, "shot.jpg")
        XCTAssertEqual(ImageResize.pixelSize(data), CGSize(width: 1024, height: 512))
    }

    func testResizedKeepsTransparencyAsPNG() {
        let (name, data) = ImageResize.resized(name: "icon.png", data: png(2000, 2000, alpha: true), maxEdge: 768)
        XCTAssertEqual(name, "icon.png")
        XCTAssertEqual(ImageResize.pixelSize(data), CGSize(width: 768, height: 768))
    }

    func testResizedNeverGrowsOrTouchesOriginal() {
        let small = png(400, 300)
        XCTAssertEqual(ImageResize.resized(name: "a.png", data: small, maxEdge: 1024).1, small)
        let big = png(3000, 2000)
        XCTAssertEqual(ImageResize.resized(name: "a.png", data: big, maxEdge: nil).1, big)
    }

    func testSizesScale() {
        XCTAssertEqual(AttachmentSize.large.scaled(CGSize(width: 3136, height: 1000)), CGSize(width: 1568, height: 500))
        XCTAssertEqual(AttachmentSize.small.scaled(CGSize(width: 500, height: 400)), CGSize(width: 500, height: 400))
        XCTAssertEqual(AttachmentSize.original.scaled(CGSize(width: 5000, height: 4000)), CGSize(width: 5000, height: 4000))
    }

    func testThumbnail() async {
        let t = await ImageResize.thumbnail(png(1000, 500), name: "a.png", maxPixels: 100)
        XCTAssertNotNil(t)
    }

    func testDocPlain() {
        let d = ComposerDoc(text: "Compare \u{FFFC} with \u{FFFC}  please", chips: [UUID(), UUID()])
        XCTAssertEqual(d.plain, "Compare with please")
        XCTAssertFalse(d.isBlank)
        XCTAssertTrue(ComposerDoc(text: "  ").isBlank)
        XCTAssertEqual(ComposerDoc(plain: "a\u{FFFC}b").text, "ab")
    }

    @MainActor func testDraftKeepsAttachmentsAndDropsMissingChips() throws {
        let pane = "pn_test_\(UUID().uuidString.prefix(8))"
        defer { ComposerDrafts.remove(pane) }
        let a = ComposerAttachment(number: 1, name: "a.png", data: png(10, 10), size: .small)
        let b = ComposerAttachment(number: 2, name: "b.png", data: png(20, 20))
        let doc = ComposerDoc(text: "see \u{FFFC} and \u{FFFC}", chips: [a.id, b.id])
        ComposerDrafts.save(pane, doc: doc, attachments: [a, b])
        var (d, atts) = try XCTUnwrap(ComposerDrafts.load(pane))
        XCTAssertEqual(d, doc)
        XCTAssertEqual(atts.map(\.id), [a.id, b.id])
        XCTAssertEqual(atts.first?.size, .small)
        XCTAssertEqual(atts.last?.data, b.data)
        // b taken off: its file goes, and so does its chip.
        ComposerDrafts.save(pane, doc: doc, attachments: [a])
        (d, atts) = try XCTUnwrap(ComposerDrafts.load(pane))
        XCTAssertEqual(d.text, "see \u{FFFC} and ")
        XCTAssertEqual(d.chips, [a.id])
        ComposerDrafts.save(pane, doc: doc, attachments: [])
        XCTAssertNil(ComposerDrafts.load(pane))
        XCTAssertFalse(FileManager.default.fileExists(atPath: ComposerDrafts.dir(pane).path))
    }

    func testHighlights() {
        let cmds: Set = ["/model", "/clear", "/review"]
        let t = "/model then /review and /nope and a/model"
        let r = SlashToken.highlights(in: t, commands: cmds, startOnly: ["/clear"]).map { (t as NSString).substring(with: $0) }
        XCTAssertEqual(r, ["/model", "/review"])
        XCTAssertEqual(SlashToken.highlights(in: "hi /clear", commands: cmds, startOnly: ["/clear"]), [])
        XCTAssertEqual(SlashToken.highlights(in: "  /clear", commands: cmds, startOnly: ["/clear"]).count, 1)
    }
}
