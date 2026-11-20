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
