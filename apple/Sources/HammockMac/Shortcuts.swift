import AppKit
import HammockKit
import SwiftUI

/// Hammock's keyboard shortcuts, in one place: the menu bar binds them, the
/// command palette and tooltips show them. Each is an action in
/// `Keybinds.actions` (HammockKit), with its default; the config file's
/// `keybind` lines (Settings ▸ Keyboard writes them) change them, so
/// everything reading them here follows. Defaults are checked against
/// macOS's own (⌥⌘D hides the Dock, ⌃⌘D looks a word up, ⌃⌘F is full
/// screen, ⌃⌘Q locks the screen, ⌥⌘S is AppKit's hidden Toggle Sidebar).
/// nil: no shortcut.
@MainActor
enum Keys {
    static func shortcut(_ id: String) -> KeyboardShortcut? { AppConfig.shared.keybinds[id]?.shortcut }

    /// "Title (⇧⌘A)", or just the title with no shortcut: for tooltips.
    /// `for` qualifies it: "Split Right (⌘D for a terminal)".
    static func hint(_ title: String, _ id: String, for what: String? = nil) -> String {
        label(id).map { "\(title) (\($0)\(what.map { " for \($0)" } ?? ""))" } ?? title
    }

    /// An action's shortcut as a menu shows it ("⇧⌘A"), if it has one.
    static func label(_ id: String) -> String? { AppConfig.shared.keybinds[id]?.label }

    // Hammock
    /// SwiftUI's own Settings item's, which can't be changed (HammockApp.swift).
    static let settings = KeyboardShortcut(",", modifiers: .command)
    static var about: KeyboardShortcut? { shortcut("about") }
    static var checkForUpdates: KeyboardShortcut? { shortcut("check_for_updates") }
    static var welcome: KeyboardShortcut? { shortcut("welcome") }
    static var acknowledgements: KeyboardShortcut? { shortcut("acknowledgements") }

    // File
    static var newTab: KeyboardShortcut? { shortcut("new_tab") }
    static var newBrowserTab: KeyboardShortcut? { shortcut("new_browser_tab") }
    static var newAgentTab: KeyboardShortcut? { shortcut("new_agent_tab") }
    static var newLogTab: KeyboardShortcut? { shortcut("new_log_tab") }
    static var newEditorTab: KeyboardShortcut? { shortcut("new_editor_tab") }
    static var newDiffTab: KeyboardShortcut? { shortcut("new_diff_tab") }
    static var quickOpen: KeyboardShortcut? { shortcut("quick_open") }
    static var openFile: KeyboardShortcut? { shortcut("open_file") }
    static var newScheduledTask: KeyboardShortcut? { shortcut("new_scheduled_task") }
    static var newGroup: KeyboardShortcut? { shortcut("new_group") }
    static var newGroupInDirectory: KeyboardShortcut? { shortcut("new_group_in_directory") }
    static var linkComputer: KeyboardShortcut? { shortcut("link_computer") }
    static var pairDevice: KeyboardShortcut? { shortcut("pair_device") }
    static var joinSharedGroup: KeyboardShortcut? { shortcut("join_shared_group") }
    static var palette: KeyboardShortcut? { shortcut("command_palette") }
    static var findSession: KeyboardShortcut? { shortcut("find_session") }
    static var findSessionInGroup: KeyboardShortcut? { shortcut("find_session_in_group") }
    static var renameTab: KeyboardShortcut? { shortcut("rename_tab") }
    static var configureGroup: KeyboardShortcut? { shortcut("configure_group") }
    static var changeGroupFolder: KeyboardShortcut? { shortcut("change_group_folder") }
    static var shareGroup: KeyboardShortcut? { shortcut("share_group") }
    static var close: KeyboardShortcut? { shortcut("close") }
    static var closeWindow: KeyboardShortcut? { shortcut("close_window") }
    static var reopenClosed: KeyboardShortcut? { shortcut("reopen_closed") }

    // Edit
    static var findInPage: KeyboardShortcut? { shortcut("find_in_page") }
    static var findNext: KeyboardShortcut? { shortcut("find_next") }
    static var findPrevious: KeyboardShortcut? { shortcut("find_previous") }

    // View
    static var toggleSidebar: KeyboardShortcut? { shortcut("toggle_sidebar") }
    static var allComputers: KeyboardShortcut? { shortcut("show_all_computers") }
    static var scheduledTasks: KeyboardShortcut? { shortcut("scheduled_tasks") }
    static var nextTab: KeyboardShortcut? { shortcut("next_tab") }
    static var previousTab: KeyboardShortcut? { shortcut("previous_tab") }
    static var nextGroup: KeyboardShortcut? { shortcut("next_group") }
    static var previousGroup: KeyboardShortcut? { shortcut("previous_group") }
    static var bigger: KeyboardShortcut? { shortcut("bigger") }
    static var smaller: KeyboardShortcut? { shortcut("smaller") }
    static var actualSize: KeyboardShortcut? { shortcut("actual_size") }

    // Pane
    static var nextPane: KeyboardShortcut? { shortcut("next_pane") }
    static var previousPane: KeyboardShortcut? { shortcut("previous_pane") }
    static var splitRight: KeyboardShortcut? { shortcut("split_right") }
    static var splitDown: KeyboardShortcut? { shortcut("split_down") }
    static var splitLeft: KeyboardShortcut? { shortcut("split_left") }
    static var splitUp: KeyboardShortcut? { shortcut("split_up") }
    static var toggleAgentView: KeyboardShortcut? { shortcut("toggle_agent_view") }
    static var copySessionLink: KeyboardShortcut? { shortcut("copy_session_link") }
    static var dictate: KeyboardShortcut? { shortcut("dictate") }
    static var sleepAgent: KeyboardShortcut? { shortcut("sleep_agent") }
    static var splitBrowserRight: KeyboardShortcut? { shortcut("split_browser_right") }
    static var splitBrowserDown: KeyboardShortcut? { shortcut("split_browser_down") }
    static var devTools: KeyboardShortcut? { shortcut("dev_tools") }
    static var splitLogRight: KeyboardShortcut? { shortcut("split_log_right") }
    static var splitDiffRight: KeyboardShortcut? { shortcut("split_diff_right") }
    static var splitEditorRight: KeyboardShortcut? { shortcut("split_editor_right") }
    static var splitEditorDown: KeyboardShortcut? { shortcut("split_editor_down") }
    static var showSimulatorLog: KeyboardShortcut? { shortcut("show_simulator_log") }
    static var zoomPane: KeyboardShortcut? { shortcut("zoom_pane") }
    static var movePaneToNewTab: KeyboardShortcut? { shortcut("move_pane_to_new_tab") }
    static var closePane: KeyboardShortcut? { shortcut("close_pane") }
    // Floating panes
    static var newFloatingPane: KeyboardShortcut? { shortcut("new_floating_pane") }
    static var toggleFloats: KeyboardShortcut? { shortcut("toggle_floats") }
    static var floatOrDock: KeyboardShortcut? { shortcut("float_or_dock") }
    static var minimiseFloat: KeyboardShortcut? { shortcut("minimise_float") }

    // Simulator (only with one focused: ⌘← and ⌘→ are the terminal's otherwise)
    static var simHome: KeyboardShortcut? { shortcut("sim_home") }
    static var simLock: KeyboardShortcut? { shortcut("sim_lock") }
    static var simRotateLeft: KeyboardShortcut? { shortcut("sim_rotate_left") }
    static var simRotateRight: KeyboardShortcut? { shortcut("sim_rotate_right") }
    static var simKeyboard: KeyboardShortcut? { shortcut("sim_keyboard") }
    static var simFold: KeyboardShortcut? { shortcut("sim_fold") }
    static var simHalfOpen: KeyboardShortcut? { shortcut("sim_half_open") }

    // Editor (only with one focused: ⌘S and ⌘L are the simulator's otherwise)
    static var editorSave: KeyboardShortcut? { shortcut("editor_save") }
    static var editorSaveAs: KeyboardShortcut? { shortcut("editor_save_as") }
    static var editorGoToLine: KeyboardShortcut? { shortcut("editor_go_to_line") }
    static var editorReload: KeyboardShortcut? { shortcut("editor_reload") }
    static var editorShowDiff: KeyboardShortcut? { shortcut("editor_show_diff") }
    static var editorWrap: KeyboardShortcut? { shortcut("editor_wrap") }
    static var reviewChanges: KeyboardShortcut? { shortcut("review_changes") }
    static var simShutDown: KeyboardShortcut? { shortcut("sim_shut_down") }
    static var simScreenshotFile: KeyboardShortcut? { shortcut("sim_screenshot_file") }
    static var simScreenshotCopy: KeyboardShortcut? { shortcut("sim_screenshot_copy") }

    /// ⌘1…⌘9 for the nth tab of the Group you're in.
    static func tab(_ i: Int) -> KeyboardShortcut? { member("select_tab", i) }
    /// ⌃⌘1…: show only the nth computer.
    static func showComputer(_ i: Int) -> KeyboardShortcut? { member("show_computer", i) }
    /// ⌃⌥⌘1…: a new Group on the nth computer.
    static func newGroupOn(_ i: Int) -> KeyboardShortcut? { member("new_group_on_computer", i) }

    private static func member(_ id: String, _ i: Int) -> KeyboardShortcut? {
        AppConfig.shared.keybinds[id]?.member(i)?.shortcut
    }

    /// The action a key press is bound to, if any (for views that see keys
    /// before the menus do, like a browser pane).
    static func action(for event: NSEvent) -> String? {
        guard let chord = KeyChord(event: event) else { return nil }
        return AppConfig.shared.keybinds.conflicts(chord).first
    }
}

extension KeyChord {
    var shortcut: KeyboardShortcut? {
        guard let c = keyCharacter else { return nil }
        var m: EventModifiers = []
        if modifiers.contains(.command) { m.insert(.command) }
        if modifiers.contains(.shift) { m.insert(.shift) }
        if modifiers.contains(.option) { m.insert(.option) }
        if modifiers.contains(.control) { m.insert(.control) }
        return KeyboardShortcut(KeyEquivalent(c), modifiers: m)
    }

    /// Named keys by virtual key code (kVK_*).
    private static let keyCodes: [UInt16: String] = [
        36: "return", 76: "return", 53: "escape", 48: "tab", 49: "space", 51: "delete", 117: "forward_delete",
        126: "up", 125: "down", 123: "left", 124: "right", 115: "home", 119: "end", 116: "page_up", 121: "page_down",
        122: "f1", 120: "f2", 99: "f3", 118: "f4", 96: "f5", 97: "f6", 98: "f7", 100: "f8", 101: "f9", 109: "f10",
        103: "f11", 111: "f12", 105: "f13", 107: "f14", 113: "f15", 106: "f16", 64: "f17", 79: "f18", 80: "f19", 90: "f20",
    ]

    /// The chord a key press makes: its key as on the unmodified keyboard
    /// (⇧⌘[ is `shift+cmd+[`, not `{`).
    init?(event: NSEvent) {
        guard event.type == .keyDown else { return nil }
        let flags = event.modifierFlags.intersection(.deviceIndependentFlagsMask)
        var mods: Modifiers = []
        if flags.contains(.command) { mods.insert(.command) }
        if flags.contains(.shift) { mods.insert(.shift) }
        if flags.contains(.option) { mods.insert(.option) }
        if flags.contains(.control) { mods.insert(.control) }
        if let named = Self.keyCodes[event.keyCode] {
            self.init(named, mods)
            return
        }
        guard let base = event.characters(byApplyingModifiers: [])?.lowercased(), base.count == 1,
              let chord = KeyChord(parsing: base) else { return nil }
        self.init(chord.key, mods)
    }
}

extension KeyboardShortcut {
    /// As a menu shows it: ⌃⌥⇧⌘ then the key, e.g. "⇧⌘T".
    var label: String {
        var s = ""
        if modifiers.contains(.control) { s += "⌃" }
        if modifiers.contains(.option) { s += "⌥" }
        if modifiers.contains(.shift) { s += "⇧" }
        if modifiers.contains(.command) { s += "⌘" }
        let keys: [Character: String] = [
            KeyEquivalent.return.character: "↩", KeyEquivalent.escape.character: "⎋",
            KeyEquivalent.delete.character: "⌫", KeyEquivalent.tab.character: "⇥", KeyEquivalent.space.character: "Space",
            KeyEquivalent.leftArrow.character: "←", KeyEquivalent.rightArrow.character: "→",
            KeyEquivalent.upArrow.character: "↑", KeyEquivalent.downArrow.character: "↓",
            KeyEquivalent.deleteForward.character: "⌦", KeyEquivalent.home.character: "↖", KeyEquivalent.end.character: "↘",
            KeyEquivalent.pageUp.character: "⇞", KeyEquivalent.pageDown.character: "⇟",
        ]
        if let scalar = key.character.unicodeScalars.first, (0xF704...0xF717).contains(scalar.value) {
            return s + "F\(scalar.value - 0xF704 + 1)"
        }
        return s + (keys[key.character] ?? String(key.character).uppercased())
    }
}
