import Foundation
import Testing
@testable import HammockKit

@Suite struct PromptKeyTests {
    private func action(_ c: String, _ key: PromptKey.Key = .other, mods: Bool = false, options: Int = 3, several: Bool = false) -> PromptKey? {
        PromptKey.action(characters: c, key: key, hasModifiers: mods, options: options, multipleQuestions: several)
    }

    @Test func numbersPickOptions() {
        #expect(action("1") == .pick(1))
        #expect(action("3") == .pick(3))
        #expect(action("4") == nil)
        #expect(action("0") == nil)
        #expect(action("a") == nil)
        #expect(action("12") == nil)
        #expect(action("1", mods: true) == nil)
    }

    @Test func arrowsMoveOnlyBetweenSeveralQuestions() {
        #expect(action("", .left) == nil)
        #expect(action("", .left, several: true) == .previous)
        #expect(action("", .right, several: true) == .next)
        #expect(action("", .escape) == .escape)
    }
}

@Suite struct ComposerHistoryTests {
    typealias H = ComposerHistory<String>

    @Test func stepsBackAndForthKeepingTheDraft() {
        var h = H()
        let all = ["one", "two", "three"]
        #expect(t(h.up(from: "draft", text: "draft", entries: all)) == "three")
        #expect(t(h.up(from: "", text: "three", entries: all)) == "two")
        #expect(t(h.up(from: "", text: "two", entries: all)) == "one")
        #expect(t(h.up(from: "", text: "one", entries: all)) == nil)  // the oldest: stays
        #expect(t(h.down(from: "", text: "one")?.entry) == "two")
        #expect(t(h.down(from: "", text: "two")?.entry) == "three")
        let back = h.down(from: "", text: "three")
        #expect(back?.entry == nil && back?.draft == "draft")
        #expect(!h.browsing)
        #expect(h.down(from: "draft", text: "draft") == nil)
    }

    @Test func nothingToStepThrough() {
        var h = H()
        #expect(t(h.up(from: "x", text: "x", entries: [])) == nil)
        #expect(!h.browsing)
    }

    @Test func arrowsStepOnlyAtTheEdges() {
        #expect(H.steps(up: true, text: "", cursor: 0, browsing: false))
        #expect(H.steps(up: true, text: "hello", cursor: 0, browsing: false))
        #expect(!H.steps(up: true, text: "hello", cursor: 5, browsing: false))  // moves to the start first
        #expect(H.steps(up: true, text: "hello", cursor: 5, browsing: true))
        #expect(!H.steps(up: true, text: "a\nb", cursor: 3, browsing: true))  // up a line first
        #expect(H.steps(up: true, text: "a\nb", cursor: 1, browsing: true))
        #expect(!H.steps(up: true, text: "hello", cursor: nil, browsing: true))  // text selected
        #expect(!H.steps(up: false, text: "", cursor: 0, browsing: false))
        #expect(H.steps(up: false, text: "a\nb", cursor: 3, browsing: true))
        #expect(!H.steps(up: false, text: "a\nb", cursor: 0, browsing: true))  // down a line first
    }

    @Test func mergesTheConversationWithWhatWasSentHere() {
        // Sent before a /clear (not in this conversation), then in it, then on its way.
        let sent = ["old one", "fix it", "and the tests", "not there yet"]
        let conv = ["typed in the terminal", "fix it", "and the tests"]
        #expect(H.merged(sent: sent, conversation: conv) == ["old one", "typed in the terminal", "fix it", "and the tests", "not there yet"])
        // Each once, at its latest.
        #expect(H.merged(sent: [], conversation: ["a", "b", "a"]) == ["b", "a"])
        // Nothing sent here in it: all of it after.
        #expect(H.merged(sent: ["x"], conversation: ["a"]) == ["a", "x"])
        #expect(H.merged(sent: [], conversation: ["a", "b", "c"], cap: 2) == ["b", "c"])
        #expect(H.merged(sent: ["  "], conversation: [""]) == [])
    }

    @Test func keepsWhatsSentPerPane() {
        let d = UserDefaults(suiteName: "ComposerHistoryTests")!
        d.removePersistentDomain(forName: "ComposerHistoryTests")
        ComposerHistoryStore.record("p1", "hello", defaults: d)
        ComposerHistoryStore.record("p1", "again ", defaults: d)
        ComposerHistoryStore.record("p1", "hello", defaults: d)
        ComposerHistoryStore.record("p2", "other", defaults: d)
        #expect(ComposerHistoryStore.sent("p1", defaults: d) == ["again", "hello"])
        #expect(ComposerHistoryStore.sent("p2", defaults: d) == ["other"])
        for i in 0..<(ComposerHistoryStore.cap + 5) { ComposerHistoryStore.record("p3", "m\(i)", defaults: d) }
        #expect(ComposerHistoryStore.sent("p3", defaults: d).count == ComposerHistoryStore.cap)
        d.removePersistentDomain(forName: "ComposerHistoryTests")
    }
}

@Suite struct SlashRankingTests {
    private func offered(_ t: String, _ commands: [String], startOnly: Set<String> = []) -> [String] {
        SlashToken.suggestions(for: SlashToken.at(t.endIndex, in: t)!, in: t, from: commands, startOnly: startOnly)
    }

    @Test func ranksPrefixThenPartThenAnywhereThenInOrder() {
        let commands = ["/compact", "/review", "/review-pr", "/plugin:pr-tidy", "/approve", "/prompts:draft"]
        // Begins with it, then a part does, then it's anywhere, then its letters in order.
        #expect(offered("/pr", commands) == ["/prompts:draft", "/review-pr", "/plugin:pr-tidy", "/approve"])
        #expect(offered("/rvpr", commands) == ["/review-pr"])
        #expect(offered("/zz", commands) == [])
        #expect(offered("/", commands) == commands)
    }

    @Test func offersAtMostEight() {
        let many = (1...20).map { "/c\($0)" }
        #expect(offered("/c", many).count == 8)
    }

    @Test func rankValues() {
        #expect(SlashToken.rank("/rev", "/review") == 0)
        #expect(SlashToken.rank("/pr", "/review-pr") == 1)
        #expect(SlashToken.rank("/view", "/review") == 2)
        #expect(SlashToken.rank("/rw", "/review") == 3)
        #expect(SlashToken.rank("/x", "/review") == nil)
    }

    @Test func findsAnAtMention() {
        let t = "look at @src/ma"
        let r = SlashToken.at(t.endIndex, in: t, marker: "@")
        #expect(r.map { String(t[$0]) } == "@src/ma")
        let mail = "you@example"
        #expect(SlashToken.at(mail.endIndex, in: mail, marker: "@") == nil)
    }

    @Test func namesMentionedFilesAsTheAgentWould() {
        #expect(ComposerSuggestion.path("a/b.go", folder: "/Users/me/app", cwd: "/Users/me/app") == "a/b.go")
        #expect(ComposerSuggestion.path("a/b.go", folder: "/Users/me/app", cwd: "/Users/me/app/a") == "b.go")
        #expect(ComposerSuggestion.path("x.go", folder: "/Users/me/app", cwd: "/Users/me/other") == "/Users/me/app/x.go")
        #expect(ComposerSuggestion.path("x.go", folder: "/Users/me/app", cwd: "") == "/Users/me/app/x.go")
        #expect(ComposerSuggestion.mention("a b.txt") == "@\"a b.txt\"")
        #expect(ComposerSuggestion.mention("ab.txt") == "@ab.txt")
    }
}

@Suite struct PromptKeyTakingTests {
    @Test func numberKeysWaitForQuiet() {
        let t0: TimeInterval = 1000
        func ok(_ at: Double, shown: Double = 0, taken: TimeInterval = 0, last: TimeInterval = 0) -> Bool {
            PromptKey.acceptsPick(now: t0 + at, shownAt: t0 + shown, keysTakenAt: taken, lastKeyAt: last)
        }
        #expect(!ok(0.5))
        #expect(ok(1.1))
        // Taken late (the retry, ~1 s after it appeared, the person typing on):
        // a "2" just after doesn't pick.
        #expect(!ok(1.15, taken: t0 + 1.05))
        // Taken at once after a pause, a digit 0.5–1 s later doesn't either.
        #expect(!ok(0.8, taken: t0))
    }

    @Test func typingASentenceNeverPicks() {
        // Three seconds of typing at 8 keys a second, digits among them.
        let t0: TimeInterval = 1000
        var last: TimeInterval = 0
        var picked = false
        for k in 0..<24 {
            let now = t0 + Double(k) / 8
            if k % 5 == 2, PromptKey.acceptsPick(now: now, shownAt: t0, keysTakenAt: t0, lastKeyAt: last) { picked = true }
            last = now
        }
        #expect(!picked)
        // A second after the last key: a deliberate digit picks.
        #expect(PromptKey.acceptsPick(now: last + 1.01, shownAt: t0, keysTakenAt: t0, lastKeyAt: last))
    }

    @Test func returnSubmitsOnlyAfterQuiet() {
        #expect(PromptKey.action(characters: "\r", key: .submit, hasModifiers: false, options: 3, multipleQuestions: false) == .submit)
        #expect(PromptKey.action(characters: "\r", key: .submit, hasModifiers: true, options: 3, multipleQuestions: false) == nil)
        // "…tests⏎" typed as the card came: the quiet rule says no.
        let t0: TimeInterval = 1000
        #expect(!PromptKey.acceptsPick(now: t0 + 0.4, shownAt: t0, keysTakenAt: t0, lastKeyAt: t0 + 0.3))
        // After a second's quiet, Return answers.
        #expect(PromptKey.acceptsPick(now: t0 + 2.5, shownAt: t0, keysTakenAt: t0, lastKeyAt: t0 + 1.4))
    }

    @Test func aRepeatCountsAsAKey() {
        // Delete held for 1.5 s (repeats every 0.1 s), then "2" at once: no.
        let t0: TimeInterval = 1000
        var last: TimeInterval = 0
        for k in 0...15 { last = t0 + Double(k) / 10 }
        #expect(!PromptKey.acceptsPick(now: last + 0.05, shownAt: t0 - 5, keysTakenAt: t0 - 5, lastKeyAt: last))
    }

    @Test func waitsOutTypingInTheMessageBox() {
        let now: TimeInterval = 1000
        // Nothing typed, or not for a second: at once.
        #expect(PromptKey.keyDelay(lastEdit: nil, now: now) == nil)
        #expect(PromptKey.keyDelay(lastEdit: now - 1.5, now: now) == nil)
        // Typed just now: again once the second's up.
        let wait = PromptKey.keyDelay(lastEdit: now - 0.3, now: now)
        #expect(wait != nil && abs(wait! - 0.7) < 0.001)
        // The retry then finds nothing typed meanwhile: it takes them.
        #expect(PromptKey.keyDelay(lastEdit: now - 0.3, now: now + wait!) == nil)
    }
}

@Suite(.serialized) struct PromptClockTests {
    @Test func timesComeFromTheInjectedMonotonicClock() {
        let real = PromptKey.clock
        defer { PromptKey.clock = real }
        // Monotonic by default: seconds since boot, not the wall clock.
        #expect(abs(PromptKey.now - ProcessInfo.processInfo.systemUptime) < 1)
        var fake: TimeInterval = 50
        PromptKey.clock = { fake }
        #expect(!PromptKey.acceptsPick(shownAt: 49.5, keysTakenAt: 0, lastKeyAt: 0))
        fake = 51
        #expect(PromptKey.acceptsPick(shownAt: 49.5, keysTakenAt: 0, lastKeyAt: 0))
        #expect(PromptKey.keyDelay(lastEdit: 50.6) != nil)
    }
}

@Suite struct HistoryKeepsChipsTests {
    @Test func anEditedLineKeepsItsAttachments() {
        // The box's own value (ComposerDoc on the Mac: chips and all) is what's kept.
        var h = ComposerHistory<[String]>()
        _ = h.up(from: ["draft"], text: "draft", entries: ["one", "two"])
        _ = h.up(from: ["two", "chip"], text: "two \u{FFFC}", entries: ["one", "two"])
        guard case .kept(let kept)? = h.down(from: ["one"], text: "one")?.entry else { Issue.record("not kept"); return }
        #expect(kept == ["two", "chip"])
        #expect(h.unchanged("two \u{FFFC}"))
    }
}

@Suite struct ComposerReviewFixTests {
    @Test func anEditedLineMovesTheCursorNotTheHistory() {
        var h = ComposerHistory<String>()
        _ = h.up(from: "draft", text: "draft", entries: ["one", "two"])
        #expect(h.unchanged("two"))
        #expect(!h.unchanged("two, edited"))  // ↑ then only from its very start
        #expect(!ComposerHistory<String>.steps(up: true, text: "two, edited", cursor: 11, browsing: h.unchanged("two, edited")))
        #expect(ComposerHistory<String>.steps(up: true, text: "two, edited", cursor: 0, browsing: h.unchanged("two, edited")))
    }

    @Test func keepsAMessagesSpacing() {
        #expect(ComposerHistoryStore.recordable("fix this:\n    let x = 1\n") == "fix this:\n    let x = 1")
        #expect(ComposerHistoryStore.recordable("compare \u{FFFC} with \u{FFFC} now") == "compare with now")
    }

    @Test func returnTakesASuggestionOnlyOnceMoved() {
        #expect(!ComposerSuggestion.returnTakes(suggesting: true, moved: false))
        #expect(ComposerSuggestion.returnTakes(suggesting: true, moved: true))
        #expect(!ComposerSuggestion.returnTakes(suggesting: false, moved: true))
    }
}

@Suite struct HistoryKeepsEditsTests {
    @Test func downThenUpComesBackToTheEdit() {
        var h = ComposerHistory<String>()
        let all = ["run tests", "deploy"]
        #expect(t(h.up(from: "my draft", text: "my draft", entries: all)) == "deploy")
        #expect(t(h.up(from: "", text: "deploy", entries: all)) == "run tests")
        // Edited, then ↓ out of habit: the edit's kept…
        #expect(t(h.down(from: "run the unit tests", text: "run the unit tests")?.entry) == "deploy")
        // …and ↑ comes back to it, not the original.
        #expect(t(h.up(from: "", text: "deploy", entries: all)) == "run the unit tests")
        #expect(h.savedDraft == "my draft")
    }

    @Test func theDraftStillComesBack() {
        var h = ComposerHistory<String>()
        let all = ["one"]
        _ = h.up(from: "my draft", text: "my draft", entries: all)
        let back = h.down(from: "one, edited", text: "one, edited")
        #expect(back?.draft == "my draft")
        #expect(h.savedDraft == nil)  // what's in the box is the draft now
        // And the edit's still there going back up.
        #expect(t(h.up(from: "my draft", text: "my draft", entries: all)) == "one, edited")
    }

    @Test func editingTheDraftAfterSteppingKeepsIt() {
        var h = ComposerHistory<String>()
        _ = h.up(from: "a", text: "a", entries: ["x"])
        _ = h.down(from: "", text: "x")
        #expect(t(h.up(from: "a, more", text: "a, more", entries: ["x"])) == "x")
        #expect(h.down(from: "", text: "x")?.draft == "a, more")
    }
}

@Suite struct MentionFilesTests {
    @Test func showsOnlyForTheWordTheyWereFoundFor() {
        let f = MentionFiles(pane: "p1", query: "src", paths: ["src/a.go"])
        #expect(f.shows(pane: "p1", query: "src"))
        #expect(f.shows(pane: "p1", query: "src/a"))   // still typing it: until the new ones come
        #expect(!f.shows(pane: "p1", query: "doc"))    // another word
        #expect(!f.shows(pane: "p1", query: "sr"))
        #expect(!f.shows(pane: "p2", query: "src"))    // another pane
        #expect(!MentionFiles().shows(pane: "", query: ""))
    }
}

/// A line from history as text: as sent, or as kept.
func t(_ l: ComposerHistory<String>.Line??) -> String? {
    switch l ?? nil {
    case .sent(let s): s
    case .kept(let s): s
    case nil: nil
    }
}
