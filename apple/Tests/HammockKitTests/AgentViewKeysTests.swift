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
        #expect(h.up(from: "draft", entries: all) == "three")
        #expect(h.up(from: "ignored", entries: all) == "two")
        #expect(h.up(from: "", entries: all) == "one")
        #expect(h.up(from: "", entries: all) == nil)  // the oldest: stays
        #expect(h.down()?.entry == "two")
        #expect(h.down()?.entry == "three")
        let back = h.down()
        #expect(back?.entry == nil && back?.draft == "draft")
        #expect(!h.browsing)
        #expect(h.down() == nil)
    }

    @Test func nothingToStepThrough() {
        var h = H()
        #expect(h.up(from: "x", entries: []) == nil)
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
