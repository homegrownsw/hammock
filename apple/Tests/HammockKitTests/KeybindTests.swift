import XCTest
@testable import HammockKit

/// shortcuts from the config file's `keybind` lines, and Settings' search.
final class KeybindTests: XCTestCase {
    private let cs: KeyChord.Modifiers = [.command, .shift]

    // MARK: Chords

    func testParsesTheWaysPeopleWriteShortcuts() {
        let b = KeyChord("b", cs)
        for text in ["cmd+shift+b", "shift+cmd+B", "super+shift+b", "command+shift+b", "⇧⌘B", "⇧⌘ b", "cmd shift b"] {
            XCTAssertEqual(KeyChord(parsing: text), b, text)
        }
        XCTAssertEqual(KeyChord(parsing: "ctrl+opt+cmd+up"), KeyChord("up", [.control, .option, .command]))
        XCTAssertEqual(KeyChord(parsing: "alt+cmd+arrow_left"), KeyChord("left", [.option, .command]))
        XCTAssertEqual(KeyChord(parsing: "cmd+shift+enter"), KeyChord("return", cs))
        XCTAssertEqual(KeyChord(parsing: "cmd++"), KeyChord("plus"))
        XCTAssertEqual(KeyChord(parsing: "cmd+-"), KeyChord("minus"))
        XCTAssertEqual(KeyChord(parsing: "cmd+bracket_left"), KeyChord("["))
        XCTAssertEqual(KeyChord(parsing: "cmd+comma"), KeyChord(","))
        XCTAssertEqual(KeyChord(parsing: "f5"), KeyChord("f5", []))
        XCTAssertEqual(KeyChord(parsing: "ctrl+cmd+digits"), KeyChord(KeyChord.digits, [.control, .command]))
        XCTAssertNil(KeyChord(parsing: "cmd+"))
        XCTAssertNil(KeyChord(parsing: "hyper+b"))
        XCTAssertNil(KeyChord(parsing: "cmd+banana"))
    }

    func testWritesAndShowsShortcuts() {
        let c = KeyChord("b", [.command, .shift, .option, .control])
        XCTAssertEqual(c.configString, "ctrl+opt+shift+cmd+b")
        XCTAssertEqual(c.label, "⌃⌥⇧⌘B")
        XCTAssertEqual(KeyChord("return", cs).label, "⇧⌘↩")
        XCTAssertEqual(KeyChord("plus").label, "⌘+")
        XCTAssertEqual(KeyChord("plus").configString, "cmd+plus")
        XCTAssertEqual(KeyChord(KeyChord.digits).label, "⌘1…9")
        XCTAssertEqual(KeyChord("f5", []).label, "F5")
        XCTAssertEqual(KeyChord("up").keyCharacter, "\u{f700}")
        XCTAssertEqual(KeyChord("f1", []).keyCharacter, "\u{f704}")
        // Every default survives being written and read back.
        for a in Keybinds.actions {
            guard let d = a.defaultChord else { continue }
            XCTAssertEqual(KeyChord(parsing: d.configString), d, a.id)
            XCTAssertNotNil(d.keyCharacter ?? (d.isFamily ? " " : nil), a.id)
        }
    }

    func testFamiliesOverlapTheirMembers() {
        let family = KeyChord(KeyChord.digits)
        XCTAssertTrue(family.overlaps(KeyChord("3")))
        XCTAssertTrue(KeyChord("9").overlaps(family))
        XCTAssertFalse(family.overlaps(KeyChord("0")))
        XCTAssertFalse(family.overlaps(KeyChord("3", cs)))
        XCTAssertEqual(family.member(0), KeyChord("1"))
        XCTAssertNil(family.member(9))
    }

    func testPlusIsTheEqualKey() {
        // A key press reads as ⌘= or ⇧⌘=, never ⌘+, which Bigger defaults to.
        XCTAssertTrue(KeyChord("plus").overlaps(KeyChord("equal")))
        XCTAssertTrue(KeyChord("equal", cs).overlaps(KeyChord("plus")))
        XCTAssertFalse(KeyChord("plus").overlaps(KeyChord("equal", [.command, .option])))
        XCTAssertEqual(KeybindSet.defaults.conflicts(KeyChord("equal")), ["bigger"])
    }

    func testDeleteIsTheMenusBackspace() {
        // SwiftUI's KeyEquivalent.delete is U+0008.
        XCTAssertEqual(KeyChord("delete").keyCharacter, "\u{8}")
    }

    // MARK: The registry

    func testDefaultsDontCollide() {
        XCTAssertEqual(Set(Keybinds.actions.map(\.id)).count, Keybinds.actions.count)
        let bound = Keybinds.actions.compactMap { a in a.defaultChord.map { (a.id, $0) } }
        for (i, (a, c)) in bound.enumerated() {
            for (b, d) in bound[(i + 1)...] where !Keybinds.apart(a, b) {
                XCTAssertFalse(c.overlaps(d), "\(a) and \(b) share \(c.label)")
            }
            XCTAssertTrue(Keybinds.groups.contains(Keybinds.byID[a]!.group), a)
        }
        XCTAssertEqual(KeybindSet.parse([]).set, KeybindSet.defaults)
    }

    // MARK: Config lines

    func testABindingReplacesTheDefaultAndTakesTheChordFromOthers() {
        let (set, problems) = KeybindSet.parse(["cmd+t=new_browser_tab"])
        XCTAssertEqual(problems, [])
        XCTAssertEqual(set["new_browser_tab"], KeyChord("t"))
        XCTAssertNil(set["new_tab"])
    }

    func testUnbindNoneAndClear() {
        var (set, _) = KeybindSet.parse(["cmd+j=unbind", "none=new_tab"])
        XCTAssertNil(set["toggle_floats"])
        XCTAssertNil(set["new_tab"])
        XCTAssertEqual(set["new_group"], KeyChord("n"))
        (set, _) = KeybindSet.parse(["clear", "cmd+k=command_palette"])
        XCTAssertEqual(set.chords, ["command_palette": KeyChord("k")])
    }

    func testFamiliesTakeModifiersAndDigits() {
        let (set, problems) = KeybindSet.parse(["ctrl+digits=select_tab", "cmd+digits=new_tab", "cmd+1=select_tab"])
        XCTAssertEqual(set["select_tab"], KeyChord(KeyChord.digits, .control))
        XCTAssertEqual(problems.count, 2)
        // ⌘1 for one action takes the whole ⌘1…9 family's chord away.
        let (taken, _) = KeybindSet.parse(["cmd+1=new_tab"])
        XCTAssertNil(taken["select_tab"])
    }

    func testProblemsAreDescribedAndSkipped() {
        let (set, problems) = KeybindSet.parse(["cmd+t", "cmd+t=launch_rockets", "cmd+nope=new_tab", "  cmd+shift+t = new_tab  "])
        XCTAssertEqual(problems.map(\.line), ["cmd+t", "cmd+t=launch_rockets", "cmd+nope=new_tab"])
        XCTAssertEqual(problems[1].description, "“cmd+t=launch_rockets”: no action called launch_rockets")
        XCTAssertEqual(set["new_tab"], KeyChord("t", cs))
    }

    func testConfigValuesRoundTrip() {
        var set = KeybindSet.defaults
        XCTAssertEqual(set.configValues, [])
        set.assign(KeyChord("t", [.command, .option]), to: "new_tab")
        set.assign(nil, to: "toggle_floats")
        set.assign(KeyChord("d"), to: "zoom_pane") // takes ⌘D from Split Right
        set.assign(KeyChord("e"), to: "split_left") // no default
        set.assign(KeyChord(KeyChord.digits, .control), to: "select_tab")
        let values = set.configValues
        XCTAssertEqual(values, ["none=toggle_floats", "opt+cmd+t=new_tab", "ctrl+digits=select_tab", "cmd+e=split_left", "cmd+d=zoom_pane"])
        XCTAssertEqual(KeybindSet.parse(values).set, set)
        XCTAssertNil(set["split_right"])
    }

    func testAssignReportsWhatItTookFrom() {
        var set = KeybindSet.defaults
        XCTAssertEqual(set.conflicts(KeyChord("d"), excluding: "zoom_pane"), ["split_right"])
        XCTAssertEqual(set.assign(KeyChord("d"), to: "zoom_pane"), ["split_right"])
        XCTAssertEqual(set.conflicts(KeyChord("3")), ["select_tab"])
    }

    func testReservedShortcutsAreKnown() {
        XCTAssertEqual(Keybinds.reservedReason(KeyChord("q")), "Quit Hammock")
        XCTAssertEqual(Keybinds.reservedReason(KeyChord("q", cs)), "Log Out")
        XCTAssertEqual(Keybinds.reservedReason(KeyChord(KeyChord.digits, cs)), "screenshots")
        XCTAssertNil(Keybinds.reservedReason(KeyChord("t")))
    }

    // MARK: Search

    private func top(_ q: String, _ set: KeybindSet = .defaults) -> String? { KeybindSearch.rank(q, in: set).first?.id }
    private func ids(_ q: String) -> [String] { KeybindSearch.rank(q, in: .defaults).map(\.id) }

    func testSearchByWords() {
        XCTAssertEqual(top("new browser"), "new_browser_tab")
        XCTAssertEqual(top("browser new"), "new_browser_tab")
        XCTAssertEqual(top("split right"), "split_right")
        XCTAssertEqual(top("palette"), "command_palette")
        XCTAssertEqual(ids("").count, Keybinds.actions.count)
        XCTAssertEqual(ids("zzzz"), [])
    }

    func testSearchForgivesTyposAndAbbreviations() {
        XCTAssertEqual(top("pallete"), "command_palette")
        XCTAssertEqual(top("splti right"), "split_right")
        XCTAssertEqual(top("screnshot clipboard"), "sim_screenshot_copy")
        XCTAssertEqual(top("nbt"), "new_browser_tab")
    }

    func testSearchUsesKeywordsAndSynonyms() {
        XCTAssertEqual(top("maximize"), "zoom_pane")
        XCTAssertTrue(ids("iphone").allSatisfy { Keybinds.byID[$0]!.group == "Simulator" || $0.contains("simulator") || $0 == "pair_device" })
        XCTAssertTrue(ids("search").contains("find_in_page"))
        XCTAssertTrue(ids("claude").contains("new_agent_tab"))
    }

    func testSearchByShortcut() {
        XCTAssertEqual(top("⌘D"), "split_right")
        XCTAssertEqual(top("cmd d"), "split_right")
        XCTAssertEqual(top("cmd+shift+b"), "new_browser_tab")
        XCTAssertEqual(top("⌘3"), "select_tab")
        XCTAssertEqual(ids("⌘E"), [])
        // Words that read as a shortcut: its action (⌘P is Quick Open)…
        XCTAssertEqual(top("command p"), "quick_open")
        // …and words that don't are words.
        XCTAssertEqual(top("command palette"), "command_palette")
        // Modifiers alone: everything using exactly those.
        let ctrlCmd = ids("⌃⌘")
        XCTAssertTrue(ctrlCmd.contains("toggle_sidebar"))
        XCTAssertFalse(ctrlCmd.contains("new_tab"))
        XCTAssertEqual(Set(ids("ctrl cmd")), Set(ctrlCmd))
        // Your own shortcut, not the default.
        let (mine, _) = KeybindSet.parse(["cmd+e=split_left"])
        XCTAssertEqual(top("⌘E", mine), "split_left")
    }
}

final class PaneScopedKeybindTests: XCTestCase {
    // ⌘S saves a file with an editor focused and screenshots a simulator
    // with one focused: giving one doesn't take it from the other.
    func testEditorAndSimulatorShareShortcuts() {
        XCTAssertTrue(Keybinds.apart("editor_save", "sim_screenshot_file"))
        XCTAssertFalse(Keybinds.apart("editor_save", "new_tab"))
        var set = KeybindSet.defaults
        XCTAssertEqual(set.conflicts(KeyChord("s"), excluding: "editor_save"), [])
        XCTAssertEqual(set.assign(KeyChord("l"), to: "editor_go_to_line"), [])
        XCTAssertEqual(set.chords["sim_lock"], KeyChord("l"))
    }
}
