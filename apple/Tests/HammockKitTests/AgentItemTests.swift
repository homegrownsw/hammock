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

    func testOnlyLongTurnsSayHowLongTheyWorked() {
        let done: (String, Int64) -> AgentBlock = { id, ms in .with { $0.id = id; $0.kind = .done; $0.durationMs = ms } }
        let text: (String) -> AgentBlock = { id in .with { $0.id = id; $0.kind = .text; $0.text = "Done." } }
        let items = AgentItem.group([text("a"), done("quick", 4_000), text("b"), done("long", 1_831_000)])
        XCTAssertEqual(items.map(\.id), ["a", "b", "long"])
        let tool: (String) -> AgentBlock = { id in .with { $0.id = id; $0.kind = .tool } }
        XCTAssertEqual(AgentItem.group([tool("a"), tool("b"), done("long", 60_000)]).map(\.id), ["g:a", "long"])
        XCTAssertEqual(AgentItem.group([tool("a"), done("quick", 4_000), tool("b")]).map(\.id), ["g:a"])
    }

    func testKindsFromANewerDaemonGetNoRow() {
        let text: (String) -> AgentBlock = { id in .with { $0.id = id; $0.kind = .text; $0.text = "Hi." } }
        let unknown: AgentBlock = .with { $0.id = "x"; $0.kind = .UNRECOGNIZED(99) }
        XCTAssertEqual(AgentItem.group([text("a"), unknown, text("b")]).map(\.id), ["a", "b"])
    }

    func testDoneRowWords() {
        XCTAssertEqual(DoneRow.duration(ms: 45_900), "45s")
        XCTAssertEqual(DoneRow.duration(ms: 1_831_000), "30m 31s")
        XCTAssertEqual(DoneRow.duration(ms: 7_500_000), "2h 5m")
        let now = Calendar.current.date(from: DateComponents(year: 2026, month: 11, day: 13, hour: 12, minute: 30))!
        let ms = Int64(now.timeIntervalSince1970 * 1000)
        XCTAssertEqual(DoneRow.words(durationMs: 1_831_000, doneUnixMs: ms, now: now),
                       "Worked for 30m 31s · done " + now.formatted(date: .omitted, time: .shortened))
        let earlier = now.addingTimeInterval(-3 * 86_400)
        XCTAssertEqual(DoneRow.words(durationMs: 60_000, doneUnixMs: Int64(earlier.timeIntervalSince1970 * 1000), now: now),
                       "Worked for 1m 0s · done " + earlier.formatted(date: .abbreviated, time: .shortened))
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
