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
        #expect(h.up(from: "draft", text: "draft", entries: all) == "three")
        #expect(h.up(from: "", text: "three", entries: all) == "two")
        #expect(h.up(from: "", text: "two", entries: all) == "one")
        #expect(h.up(from: "", text: "one", entries: all) == nil)  // the oldest: stays
        #expect(h.down(from: "", text: "one")?.entry == "two")
        #expect(h.down(from: "", text: "two")?.entry == "three")
        let back = h.down(from: "", text: "three")
        #expect(back?.entry == nil && back?.draft == "draft")
        #expect(!h.browsing)
        #expect(h.down(from: "draft", text: "draft") == nil)
    }

    @Test func nothingToStepThrough() {
        var h = H()
        #expect(h.up(from: "x", text: "x", entries: []) == nil)
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
        let t0 = Date(timeIntervalSince1970: 1000)
        let never = Date.distantPast
        func ok(_ at: Double, shown: Double = 0, taken: Date = .distantPast, last: Date = .distantPast) -> Bool {
            PromptKey.acceptsPick(now: t0.addingTimeInterval(at), shownAt: t0.addingTimeInterval(shown), keysTakenAt: taken, lastKeyAt: last)
        }
        #expect(!ok(0.5))
        #expect(ok(1.1))
        // Taken late (the retry, ~1 s after it appeared, the person typing on):
        // a "2" just after doesn't pick.
        #expect(!ok(1.15, taken: t0.addingTimeInterval(1.05), last: never))
        // Taken at once after a pause, a digit 0.5–1 s later doesn't either.
        #expect(!ok(0.8, taken: t0, last: never))
    }

    @Test func typingASentenceNeverPicks() {
        // Three seconds of typing at 8 keys a second, digits among them.
        let t0 = Date(timeIntervalSince1970: 1000)
        var last = Date.distantPast
        var picked = false
        for k in 0..<24 {
            let now = t0.addingTimeInterval(Double(k) / 8)
            let isDigit = k % 5 == 2
            if isDigit, PromptKey.acceptsPick(now: now, shownAt: t0, keysTakenAt: t0, lastKeyAt: last) { picked = true }
            last = now
        }
        #expect(!picked)
        // A second after the last key: a deliberate digit picks.
        #expect(PromptKey.acceptsPick(now: last.addingTimeInterval(1.01), shownAt: t0, keysTakenAt: t0, lastKeyAt: last))
    }

    @Test func waitsOutTypingInTheMessageBox() {
        let now = Date(timeIntervalSince1970: 1000)
        // Nothing typed, or not for a second: at once.
        #expect(PromptKey.keyDelay(lastEdit: nil, now: now) == nil)
        #expect(PromptKey.keyDelay(lastEdit: now.addingTimeInterval(-1.5), now: now) == nil)
        // Typed just now: again once the second's up.
        let wait = PromptKey.keyDelay(lastEdit: now.addingTimeInterval(-0.3), now: now)
        #expect(wait != nil && abs(wait! - 0.7) < 0.001)
        // The retry then finds nothing typed meanwhile: it takes them.
        #expect(PromptKey.keyDelay(lastEdit: now.addingTimeInterval(-0.3), now: now.addingTimeInterval(wait!)) == nil)
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
        #expect(h.up(from: "my draft", text: "my draft", entries: all) == "deploy")
        #expect(h.up(from: "", text: "deploy", entries: all) == "run tests")
        // Edited, then ↓ out of habit: the edit's kept…
        #expect(h.down(from: "", text: "run the unit tests")?.entry == "deploy")
        // …and ↑ comes back to it, not the original.
        #expect(h.up(from: "", text: "deploy", entries: all) == "run the unit tests")
        #expect(h.savedDraft == "my draft")
    }

    @Test func theDraftStillComesBack() {
        var h = ComposerHistory<String>()
        let all = ["one"]
        _ = h.up(from: "my draft", text: "my draft", entries: all)
        let back = h.down(from: "", text: "one, edited")
        #expect(back?.draft == "my draft")
        #expect(h.savedDraft == nil)  // what's in the box is the draft now
        // And the edit's still there going back up.
        #expect(h.up(from: "my draft", text: "my draft", entries: all) == "one, edited")
    }

    @Test func editingTheDraftAfterSteppingKeepsIt() {
        var h = ComposerHistory<String>()
        _ = h.up(from: "a", text: "a", entries: ["x"])
        _ = h.down(from: "", text: "x")
        #expect(h.up(from: "a, more", text: "a, more", entries: ["x"]) == "x")
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
