import Foundation

/// The phone terminal's key bar and keyboard, the part worth testing:
/// ⌥ as meta.
public enum PhoneTerminalKeys {
    /// Meta: Esc, then what the key sends (⌥ from the key bar, or held on
    /// a hardware keyboard), as terminals send ⌥ set to Esc+.
    public static func meta(_ data: Data) -> Data {
        var d = Data([0x1B])
        d.append(data)
        return d
    }

    /// ⌥ held with a letter on a hardware keyboard, as meta: Esc and the
    /// letter (a capital with ⇧). `characters` is what the key types with
    /// ⌥, `ignoringModifiers` without. Nil for anything else, and where the
    /// layout types a plain ASCII character with ⌥ (@ is ⌥L on a German
    /// keyboard): that's typed as it is.
    public static func optionLetter(characters: String, ignoringModifiers: String, shift: Bool) -> Data? {
        guard ignoringModifiers.count == 1, let c = ignoringModifiers.unicodeScalars.first,
              c.isASCII, CharacterSet.letters.contains(c) else { return nil }
        if let typed = characters.unicodeScalars.first, characters.unicodeScalars.count == 1,
           typed.isASCII, typed.value >= 0x20, typed.value < 0x7F,
           String(typed).lowercased() != ignoringModifiers.lowercased() {
            return nil
        }
        let letter = shift ? ignoringModifiers.uppercased() : ignoringModifiers.lowercased()
        return meta(Data(letter.utf8))
    }
}
