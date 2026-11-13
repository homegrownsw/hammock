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
    func testStartsListensAndFinishes() async throws {
        let engine = FakeEngine()
        let d = ComposerDictation()
        d.makeEngine = { engine }
        d.start(into: ComposerTextController(), commands: [])
        XCTAssertTrue(d.active)
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertEqual(d.state, .listening)
        engine.events?(.level(0.5))
        XCTAssertEqual(d.level, 0.5)
        engine.finishedWith = "Hello there"
        await d.finish()
        XCTAssertFalse(d.active)
        XCTAssertEqual(d.level, 0)
    }

    func testStopNowCancels() async throws {
        let engine = FakeEngine()
        let d = ComposerDictation()
        d.makeEngine = { engine }
        d.start(into: ComposerTextController(), commands: [])
        try await Task.sleep(for: .milliseconds(50))
        d.stopNow()
        XCTAssertFalse(d.active)
        XCTAssertTrue(engine.cancelled)
    }

    func testMicrophoneRefusedSaysSo() async throws {
        let engine = FakeEngine()
        engine.failure = DictationError.microphoneDenied
        let d = ComposerDictation()
        d.makeEngine = { engine }
        d.start(into: ComposerTextController(), commands: [])
        try await Task.sleep(for: .milliseconds(50))
        XCTAssertFalse(d.active)
        XCTAssertTrue(d.problemIsPermission)
        XCTAssertNotNil(d.problem)
    }
}
