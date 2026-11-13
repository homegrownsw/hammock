import AppKit
import XCTest
@testable import HammockKit

final class DictationTextTests: XCTestCase {
    func testJoin() {
        XCTAssertEqual(DictationText.join("", "  Hello"), "Hello")
        XCTAssertEqual(DictationText.join("Hello", "world"), "Hello world")
        XCTAssertEqual(DictationText.join("Hello", " world"), "Hello world")
        XCTAssertEqual(DictationText.join("Hello ", "world"), "Hello world")
        XCTAssertEqual(DictationText.join("Hello", ", world"), "Hello, world")
        XCTAssertEqual(DictationText.join("Hello", ""), "Hello")
    }

    func testNeedsSpace() {
        XCTAssertFalse(DictationText.needsSpace(after: nil, for: "Hi"))
        XCTAssertFalse(DictationText.needsSpace(after: " ", for: "Hi"))
        XCTAssertFalse(DictationText.needsSpace(after: "\n", for: "Hi"))
        XCTAssertTrue(DictationText.needsSpace(after: "a", for: "Hi"))
        XCTAssertFalse(DictationText.needsSpace(after: "a", for: ", then"))
        XCTAssertFalse(DictationText.needsSpace(after: "a", for: ""))
    }

    func testVocabulary() {
        XCTAssertEqual(DictationText.vocabulary(commands: ["/review-pr", "/plugin:fix_it", "/Review-PR", "/clear"]),
                       ["review pr", "fix it", "clear"])
    }

    private let commands = ["/review-pr", "/clear", "/plex-tools:commit", "/init"]

    func testSpokenSlashCommands() {
        // As the recogniser writes it: "slash" a slash, joined to the word before.
        XCTAssertEqual(DictationText.mapSlashCommands("Please run/review PR on the repo", commands: commands),
                       "Please run /review-pr on the repo")
        XCTAssertEqual(DictationText.mapSlashCommands("/review PR", commands: commands), "/review-pr")
        XCTAssertEqual(DictationText.mapSlashCommands("slash clear", commands: commands), "/clear")
        XCTAssertEqual(DictationText.mapSlashCommands("Then Slash review pr.", commands: commands), "Then /review-pr.")
        // A plugin's skill by its own name.
        XCTAssertEqual(DictationText.mapSlashCommands("slash commit this", commands: commands), "/plex-tools:commit this")
        // Several.
        XCTAssertEqual(DictationText.mapSlashCommands("/clear and then /review PR", commands: commands), "/clear and then /review-pr")
        // Spoken as words, one after another.
        XCTAssertEqual(DictationText.mapSlashCommands("slash clear and slash init", commands: commands), "/clear and /init")
        XCTAssertEqual(DictationText.mapSlashCommands("slash nope then slash clear", commands: commands), "slash nope then /clear")
    }

    func testUnknownSlashWordsStayAsHeard() {
        XCTAssertEqual(DictationText.mapSlashCommands("slash foo bar", commands: commands), "slash foo bar")
        XCTAssertEqual(DictationText.mapSlashCommands("and/or", commands: commands), "and/or")
        XCTAssertEqual(DictationText.mapSlashCommands("the slash key", commands: commands), "the slash key")
        XCTAssertEqual(DictationText.mapSlashCommands("no commands here", commands: commands), "no commands here")
        XCTAssertEqual(DictationText.mapSlashCommands("slash clear", commands: []), "slash clear")
    }
}

/// An engine that says what it's told, for the composer's dictation.
@MainActor
private final class FakeEngine: DictationEngine {
    var events: (@MainActor (DictationEvent) -> Void)?
    var finishedWith = ""
    var cancelled = false
    var failure: Error?

    func start(vocabulary: [String], events: @escaping @MainActor (DictationEvent) -> Void) async throws {
        if let failure { throw failure }
        self.events = events
        events(.listening)
    }

    func finish() async -> String { finishedWith }
    func cancel() { cancelled = true }
}

@MainActor
final class ComposerDictationTests: XCTestCase {
    /// A composer's text controller with a real text view, holding `text`.
    private func controller(_ text: String = "") -> (ComposerTextController, NSWindow, NSTextView) {
        let c = ComposerTextController()
        let (w, v) = c.attachTestView()
        c.set(ComposerDoc(plain: text))
        return (c, w, v)
    }

    /// Until the engine's been asked to start (it's started in a Task).
    private func started(_ engine: FakeEngine) async {
        for _ in 0..<100 where engine.events == nil { await Task.yield() }
    }

    func testStartsListensAndFinishes() async throws {
        let engine = FakeEngine()
        let (c, w, v) = controller()
        let d = ComposerDictation()
        d.makeEngine = { engine }
        d.start(into: c, commands: ["/review-pr"])
        XCTAssertTrue(d.active)
        await started(engine)
        XCTAssertEqual(d.state, .listening)
        engine.events?(.level(0.5))
        XCTAssertEqual(d.level, 0.5)
        engine.events?(.text(final: "Please run slash review PR", volatile: "and then"))
        XCTAssertEqual(v.string, "Please run /review-pr and then")
        engine.finishedWith = "Please run slash review PR and then stop."
        await d.finish()
        XCTAssertFalse(d.active)
        XCTAssertEqual(d.level, 0)
        XCTAssertEqual(v.string, "Please run /review-pr and then stop.")
        // One undo takes the whole dictation back.
        w.undoManager?.undo()
        XCTAssertEqual(v.string, "")
        w.undoManager?.redo()
        XCTAssertEqual(v.string, "Please run /review-pr and then stop.")
    }

    func testStopNowCancelsAndKeepsWhatsShown() async throws {
        let engine = FakeEngine()
        let (c, w, v) = controller()
        let d = ComposerDictation()
        d.makeEngine = { engine }
        d.start(into: c, commands: [])
        await started(engine)
        engine.events?(.text(final: "Hello", volatile: "there"))
        d.stopNow()
        XCTAssertFalse(d.active)
        XCTAssertTrue(engine.cancelled)
        XCTAssertEqual(v.string, "Hello there")
        _ = w
    }

    func testMicrophoneRefusedSaysSoAndLeavesTheTextAlone() async throws {
        let engine = FakeEngine()
        engine.failure = DictationError.microphoneDenied
        let (c, w, v) = controller("keep this text")
        v.setSelectedRange(NSRange(location: 5, length: 4))
        let d = ComposerDictation()
        d.makeEngine = { engine }
        d.start(into: c, commands: [])
        for _ in 0..<100 where d.active { await Task.yield() }
        XCTAssertFalse(d.active)
        XCTAssertTrue(d.problemIsPermission)
        XCTAssertNotNil(d.problem)
        // The selection it would have replaced is back, and nothing to undo.
        XCTAssertEqual(v.string, "keep this text")
        XCTAssertEqual(v.selectedRange(), NSRange(location: 5, length: 4))
        XCTAssertFalse(w.undoManager?.canUndo ?? false)
    }

    func testSpacesAroundWordsDictatedMidText() {
        let (c, w, v) = controller("foobar")
        v.setSelectedRange(NSRange(location: 3, length: 0))
        c.beginDictation()
        c.dictate(final: "hello", volatile: "world")
        XCTAssertEqual(v.string, "foo hello world bar")
        // The cursor after the words, before the space put after them.
        XCTAssertEqual(v.selectedRange(), NSRange(location: 15, length: 0))
        XCTAssertTrue(c.dictating)
        c.endDictation()
        XCTAssertEqual(v.string, "foo hello world bar")
        w.undoManager?.undo()
        XCTAssertEqual(v.string, "foobar")
    }

    func testTypingStopsTheDictation() {
        let (c, w, v) = controller()
        var interrupted = false
        c.onDictationInterrupted = { interrupted = true }
        c.beginDictation()
        c.dictate(final: "Hello", volatile: "")
        v.insertText("!", replacementRange: v.selectedRange())
        XCTAssertTrue(interrupted)
        XCTAssertFalse(c.dictating)
        XCTAssertEqual(v.string, "Hello!")
        _ = w
    }

    func testClickingElsewhereStopsTheDictation() {
        let (c, w, v) = controller("Start. ")
        var interrupted = false
        c.onDictationInterrupted = { interrupted = true }
        v.setSelectedRange(NSRange(location: 7, length: 0))
        c.beginDictation()
        c.dictate(final: "Go on", volatile: "")
        XCTAssertFalse(interrupted)
        v.setSelectedRange(NSRange(location: 0, length: 0))
        XCTAssertTrue(interrupted)
        XCTAssertFalse(c.dictating)
        _ = w
    }
}
