import XCTest
@testable import HammockKit
import ImageIO

final class AgentItemTests: XCTestCase {
    func testEditsStandOutsideToolGroups() {
        func tool(_ id: String, diff: String = "") -> AgentBlock {
            .with { $0.id = id; $0.kind = .tool; $0.diff = diff }
        }
        let items = AgentItem.group([tool("a"), tool("b"), tool("e", diff: "-x\n+y"), tool("c"), tool("d")])
        XCTAssertEqual(items.map(\.id), ["g:a", "g:e", "g:c"])
        XCTAssertEqual(items[1].block?.diff, "-x\n+y")
    }

    func testAnEditKeepsItsRowWhenItsDiffArrives() {
        func tool(_ id: String, diff: String = "") -> AgentBlock {
            .with { $0.id = id; $0.kind = .tool; $0.diff = diff }
        }
        let running = AgentItem.group([tool("a"), tool("e")])
        let made = AgentItem.group([tool("a"), tool("e", diff: "-x\n+y")])
        XCTAssertEqual(running.map(\.id), ["g:a"])
        XCTAssertEqual(made.map(\.id), ["g:a", "g:e"])
        let alone = AgentItem.group([tool("e")])
        XCTAssertEqual(AgentItem.group([tool("e", diff: "-x\n+y")]).map(\.id), alone.map(\.id))
    }

    func testNarratedThinkingStandsBetweenToolCalls() {
        let tool: (String) -> AgentBlock = { id in .with { $0.id = id; $0.kind = .tool } }
        let think: (String, String) -> AgentBlock = { id, t in .with { $0.id = id; $0.kind = .thinking; $0.text = t } }
        let items = AgentItem.group([tool("a"), think("s", ""), tool("b"), think("n", "Committing now."), tool("c")])
        XCTAssertEqual(items.map(\.id), ["g:a", "n", "g:c"])
    }
}

final class AttachmentTests: XCTestCase {
    func testHEICGoesAsJPEG() throws {
        // A real HEIC: render a tiny image and encode it with ImageIO.
        let data = NSMutableData()
        let dest = try XCTUnwrap(CGImageDestinationCreateWithData(data, "public.heic" as CFString, 1, nil))
        let ctx = try XCTUnwrap(CGContext(data: nil, width: 8, height: 8, bitsPerComponent: 8, bytesPerRow: 0,
                                          space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        CGImageDestinationAddImage(dest, try XCTUnwrap(ctx.makeImage()), nil)
        XCTAssertTrue(CGImageDestinationFinalize(dest))
        let (name, out) = claudeReadable("photo-1.png", data as Data)
        XCTAssertEqual(name, "photo-1.jpg")
        XCTAssertEqual([UInt8](out.prefix(3)), [0xFF, 0xD8, 0xFF])
        let png = Data([0x89, 0x50, 0x4E, 0x47, 1, 2, 3])
        XCTAssertEqual(claudeReadable("a.png", png).1, png)
    }
}
