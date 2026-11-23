import AppKit
import XCTest
@testable import HammockKit

@MainActor
final class ComposerChipTests: XCTestCase {
    private let chip = "\u{FFFC}"
    private var keep: [NSWindow] = []

    /// A composer's text controller with a real text view holding `text`,
    /// the cursor at `cursor` (the end if nil).
    private func controller(_ text: String, cursor: Int? = nil) -> (ComposerTextController, NSTextView) {
        let c = ComposerTextController()
        c.chipImage = { _ in NSImage(size: NSSize(width: 10, height: 10)) }
        let (w, v) = c.attachTestView()
        keep.append(w)
        c.set(ComposerDoc(plain: text))
        if let cursor { v.setSelectedRange(NSRange(location: cursor, length: 0)) }
        return (c, v)
    }

    func testChipAfterASentenceGetsASpaceEitherSide() {
        let (c, v) = controller("Done.")
        c.insertChip(UUID())
        XCTAssertEqual(v.string, "Done. \(chip) ")
    }

    func testChipAfterASpaceGetsNoSecond() {
        let (c, v) = controller("Done. ")
        c.insertChip(UUID())
        XCTAssertEqual(v.string, "Done. \(chip) ")
    }

    func testChipInAnEmptyBox() {
        let (c, v) = controller("")
        c.insertChip(UUID())
        XCTAssertEqual(v.string, "\(chip) ")
    }

    func testChipMidWordIsSetOff() {
        let (c, v) = controller("abcd", cursor: 2)
        c.insertChip(UUID())
        XCTAssertEqual(v.string, "ab \(chip) cd")
    }

    func testChipAtTheStartOfALine() {
        let (c, v) = controller("One\nTwo", cursor: 4)
        c.insertChip(UUID())
        XCTAssertEqual(v.string, "One\n\(chip) Two")
    }

    func testMoveToEndPutsTheChipLast() {
        let (c, v) = controller("Look at this.", cursor: 0)
        c.moveToEnd()
        c.insertChip(UUID())
        XCTAssertEqual(v.string, "Look at this. \(chip) ")
    }

    private func pasteboard(_ urls: [URL]) -> NSPasteboard {
        let pb = NSPasteboard(name: NSPasteboard.Name("hammock-test-\(UUID().uuidString)"))
        pb.clearContents()
        pb.writeObjects(urls as [NSURL])
        return pb
    }

    func testDroppedFolderGoesInAsItsPath() {
        let (c, v) = controller("Look in")
        var attached: [URL] = []
        c.onFiles = { attached = $0 }
        let pb = pasteboard([URL(fileURLWithPath: "/Users/me/Developer/app", isDirectory: true)])
        defer { pb.releaseGlobally() }
        XCTAssertTrue(c.dropAtEnd(pb))
        XCTAssertEqual(v.string, "Look in /Users/me/Developer/app ")
        XCTAssertEqual(attached, [])
    }

    func testPicturesAttachAndOtherFilesGoInAsPaths() {
        let (c, v) = controller("")
        var attached: [URL] = []
        c.onFiles = { attached = $0 }
        let shot = URL(fileURLWithPath: "/tmp/shot.png")
        let pb = pasteboard([shot, URL(fileURLWithPath: "/tmp/notes.txt"), URL(fileURLWithPath: "/tmp/a b.md")])
        defer { pb.releaseGlobally() }
        XCTAssertTrue(c.dropAtEnd(pb))
        XCTAssertEqual(v.string, "/tmp/notes.txt /tmp/a b.md ")
        XCTAssertEqual(attached, [shot])
    }

    func testTakesFilesButNotText() {
        let c = ComposerTextController()
        let pb = NSPasteboard(name: NSPasteboard.Name("hammock-test-\(UUID().uuidString)"))
        defer { pb.releaseGlobally() }
        pb.clearContents()
        pb.writeObjects([URL(fileURLWithPath: "/tmp/shot.png") as NSURL])
        XCTAssertTrue(c.takesFiles(pb))
        pb.clearContents()
        pb.writeObjects([URL(fileURLWithPath: "/tmp/clip.mov") as NSURL])
        XCTAssertTrue(c.takesFiles(pb))
        pb.clearContents()
        pb.writeObjects([URL(fileURLWithPath: "/tmp/notes.txt") as NSURL])
        XCTAssertTrue(c.takesFiles(pb))
        pb.clearContents()
        pb.setString("hello", forType: .string)
        XCTAssertFalse(c.takesFiles(pb))
    }
}
