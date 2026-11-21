import Foundation
import Testing
@testable import HammockKit

/// The phone terminal's ⌥ (meta), Home and End.
struct PhoneTerminalKeysTests {
    @Test func metaIsEscFirst() {
        #expect(PhoneTerminalKeys.meta(Data("b".utf8)) == Data("\u{1b}b".utf8))
        #expect(PhoneTerminalKeys.meta(Data([0x7F])) == Data([0x1B, 0x7F]))
    }

    @Test func optionLetterFromAHardwareKeyboard() {
        // US layout: ⌥B types ∫, ⌥F ƒ: sent as Esc b, Esc f.
        #expect(PhoneTerminalKeys.optionLetter(characters: "∫", ignoringModifiers: "b", shift: false) == Data("\u{1b}b".utf8))
        #expect(PhoneTerminalKeys.optionLetter(characters: "ƒ", ignoringModifiers: "f", shift: false) == Data("\u{1b}f".utf8))
        // A dead key (⌥E) types nothing yet.
        #expect(PhoneTerminalKeys.optionLetter(characters: "", ignoringModifiers: "e", shift: false) == Data("\u{1b}e".utf8))
        #expect(PhoneTerminalKeys.optionLetter(characters: "ı", ignoringModifiers: "B", shift: true) == Data("\u{1b}B".utf8))
    }

    @Test func optionTypesTheLayoutsOwnCharacters() {
        // German: ⌥L is @, ⌥7 is |.
        #expect(PhoneTerminalKeys.optionLetter(characters: "@", ignoringModifiers: "l", shift: false) == nil)
        #expect(PhoneTerminalKeys.optionLetter(characters: "|", ignoringModifiers: "7", shift: false) == nil)
        #expect(PhoneTerminalKeys.optionLetter(characters: ".", ignoringModifiers: ".", shift: false) == nil)
    }

    @Test func homeAndEndFollowCursorKeyMode() {
        let t = TerminalEngine(cols: 20, rows: 4)
        #expect(t.encodeKey(macKeyCode: 0x73, mods: [], text: nil, unshifted: 0) == Data("\u{1b}[H".utf8))
        #expect(t.encodeKey(macKeyCode: 0x77, mods: [], text: nil, unshifted: 0) == Data("\u{1b}[F".utf8))
        t.write(Data("\u{1b}[?1h".utf8)) // application cursor keys
        #expect(t.encodeKey(macKeyCode: 0x73, mods: [], text: nil, unshifted: 0) == Data("\u{1b}OH".utf8))
        #expect(t.encodeKey(macKeyCode: 0x77, mods: [], text: nil, unshifted: 0) == Data("\u{1b}OF".utf8))
    }
}
