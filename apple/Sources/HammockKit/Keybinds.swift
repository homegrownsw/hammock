import Foundation

/// A keyboard shortcut as the config file writes it (`ctrl+opt+shift+cmd+b`,
/// Ghostty's trigger syntax) and as menus show it (`⌃⌥⇧⌘B`).
public struct KeyChord: Hashable, Sendable {
    public struct Modifiers: OptionSet, Hashable, Sendable {
        public let rawValue: Int
        public init(rawValue: Int) { self.rawValue = rawValue }
        public static let control = Modifiers(rawValue: 1)
        public static let option = Modifiers(rawValue: 2)
        public static let shift = Modifiers(rawValue: 4)
        public static let command = Modifiers(rawValue: 8)
    }

    /// The key's canonical name: a lowercase character (`b`, `1`, `[`), or a
    /// named key (`up`, `return`, `f5`, `plus`, `equal`, …). `digits` stands
    /// for 1…9 in a numbered family (⌘1…⌘9 picking tabs).
    public var key: String
    public var modifiers: Modifiers

    public init(_ key: String, _ modifiers: Modifiers = .command) {
        self.key = key
        self.modifiers = modifiers
    }

    public static let digits = "digits"
    public var isFamily: Bool { key == Self.digits }
    public var isFunctionKey: Bool { key.count >= 2 && key.hasPrefix("f") && Int(key.dropFirst()) != nil }

    /// The chord this family uses for its nth member (0-based; 1…9).
    public func member(_ i: Int) -> KeyChord? {
        guard isFamily, (0..<9).contains(i) else { return nil }
        return KeyChord("\(i + 1)", modifiers)
    }

    // MARK: Names

    /// Named keys, with the character a menu binds (nil: the name is the key).
    static let named: [String: String] = [
        "return": "\r", "escape": "\u{1b}", "tab": "\t", "space": " ", "delete": "\u{8}",
        "forward_delete": "\u{f728}", "up": "\u{f700}", "down": "\u{f701}", "left": "\u{f702}", "right": "\u{f703}",
        "home": "\u{f729}", "end": "\u{f72b}", "page_up": "\u{f72c}", "page_down": "\u{f72d}",
        "plus": "+", "equal": "=", "minus": "-", "comma": ",", "period": ".", "slash": "/", "backslash": "\\",
        "semicolon": ";", "quote": "'", "backquote": "`", "bracket_left": "[", "bracket_right": "]",
    ]
    /// What people type for a key, mapped to its canonical name.
    static let aliases: [String: String] = [
        "enter": "return", "ret": "return", "↩": "return", "⏎": "return", "esc": "escape", "⎋": "escape", "⇥": "tab",
        "backspace": "delete", "⌫": "delete", "del": "forward_delete", "⌦": "forward_delete",
        "arrow_up": "up", "arrow_down": "down", "arrow_left": "left", "arrow_right": "right",
        "↑": "up", "↓": "down", "←": "left", "→": "right", "pageup": "page_up", "pagedown": "page_down",
        "pgup": "page_up", "pgdn": "page_down", "↖": "home", "↘": "end", "⇞": "page_up", "⇟": "page_down",
        "+": "plus", "=": "equal", "-": "minus", "−": "minus", "1-9": "digits", "1…9": "digits", "digit": "digits",
    ]
    /// Punctuation written by name in the config file, where `+` and `=` are syntax.
    static let writtenByName: Set<String> = ["plus", "equal", "minus"]
    static let modifierNames: [String: Modifiers] = [
        "cmd": .command, "command": .command, "super": .command, "⌘": .command,
        "ctrl": .control, "control": .control, "⌃": .control,
        "opt": .option, "option": .option, "alt": .option, "⌥": .option,
        "shift": .shift, "⇧": .shift,
    ]
    static let glyphs: [(Modifiers, Character)] = [(.control, "⌃"), (.option, "⌥"), (.shift, "⇧"), (.command, "⌘")]

    /// A key's canonical name from what was typed, or nil if it isn't one.
    /// Punctuation is its character (`[`, `,`), except `plus`, `equal` and
    /// `minus`, which the config syntax needs spelt out.
    static func canonicalKey(_ raw: String) -> String? {
        let s = raw.lowercased()
        if let a = aliases[s] { return a }
        if writtenByName.contains(s) || s == digits { return s }
        if let c = named[s] { return c.count == 1 && (c.first!.isPunctuation || c.first!.isSymbol) ? c : s }
        if s.count >= 2, s.hasPrefix("f"), let n = Int(s.dropFirst()), (1...20).contains(n) { return s }
        guard s.count == 1, let c = s.first, !c.isWhitespace, !c.isNewline else { return nil }
        return s
    }

    /// Parses `cmd+shift+b`, `shift+cmd+b`, `cmd shift b`, `⇧⌘B` or `⇧⌘ B`.
    /// Needs a key; modifiers may be none (a bare F-key).
    public init?(parsing text: String) {
        var t = text.trimmingCharacters(in: .whitespaces)
        guard !t.isEmpty else { return nil }
        var mods: Modifiers = []
        // Leading glyphs: ⌃⌥⇧⌘.
        while let c = t.first, let m = Self.glyphs.first(where: { $0.1 == c })?.0 {
            mods.insert(m)
            t.removeFirst()
            t = t.trimmingCharacters(in: .whitespaces)
        }
        // `cmd++` is ⌘ and the + key.
        var parts: [String]
        if t.hasSuffix("++") {
            parts = t.dropLast(2).split(separator: "+").map(String.init) + ["+"]
        } else if t.count == 1 {
            parts = [t]
        } else {
            parts = t.split(whereSeparator: { $0 == "+" || $0 == " " }).map(String.init)
        }
        guard let last = parts.popLast() else { return nil }
        for p in parts {
            guard let m = Self.modifierNames[p.lowercased()] else { return nil }
            mods.insert(m)
        }
        guard let key = Self.canonicalKey(last) else { return nil }
        self.init(key, mods)
    }

    /// As the config file writes it: `ctrl+opt+shift+cmd+key`.
    public var configString: String {
        var parts: [String] = []
        if modifiers.contains(.control) { parts.append("ctrl") }
        if modifiers.contains(.option) { parts.append("opt") }
        if modifiers.contains(.shift) { parts.append("shift") }
        if modifiers.contains(.command) { parts.append("cmd") }
        return (parts + [key]).joined(separator: "+")
    }

    /// As a menu shows it: `⇧⌘B`, `⌘1…9` for a family.
    public var label: String {
        var s = ""
        for (m, g) in Self.glyphs where modifiers.contains(m) { s.append(g) }
        return s + keyLabel
    }

    public var keyLabel: String {
        switch key {
        case "return": return "↩"
        case "escape": return "⎋"
        case "tab": return "⇥"
        case "space": return "Space"
        case "delete": return "⌫"
        case "forward_delete": return "⌦"
        case "up": return "↑"
        case "down": return "↓"
        case "left": return "←"
        case "right": return "→"
        case "home": return "↖"
        case "end": return "↘"
        case "page_up": return "⇞"
        case "page_down": return "⇟"
        case Self.digits: return "1…9"
        default:
            if let c = Self.named[key] { return c }
            return key.uppercased()
        }
    }

    /// The character a menu item binds for this key (nil for a family).
    public var keyCharacter: Character? {
        if isFamily { return nil }
        if let c = Self.named[key] { return c.first }
        if isFunctionKey, let n = Int(key.dropFirst()), let u = UnicodeScalar(0xF704 + n - 1) { return Character(u) }
        return key.first
    }

    /// Whether this chord, or one of its family's, is `other` (or one of its
    /// family's). `plus` is the `equal` key, with ⇧ or without: a menu's ⌘+
    /// fires for ⌘= too.
    public func overlaps(_ other: KeyChord) -> Bool {
        let zoomKeys: Set<String> = ["plus", "equal"]
        if key != other.key, zoomKeys.contains(key), zoomKeys.contains(other.key) {
            return modifiers.subtracting(.shift) == other.modifiers.subtracting(.shift)
        }
        guard modifiers == other.modifiers else { return false }
        if key == other.key { return true }
        let digit: (String) -> Bool = { $0.count == 1 && ("1"..."9").contains($0) }
        return (isFamily && digit(other.key)) || (other.isFamily && digit(key))
    }
}

/// Something a shortcut can do: a menu command, by the name the config file uses.
public struct KeybindAction: Identifiable, Hashable, Sendable {
    public let id: String
    public let title: String
    public let group: String
    /// Other words people might search for it by.
    public let keywords: String
    public let defaultChord: KeyChord?
    /// A numbered family (⌘1…⌘9): only its modifiers are chosen.
    public var isFamily: Bool { defaultChord?.isFamily ?? false }

    public init(_ id: String, _ title: String, _ group: String, _ chord: KeyChord?, _ keywords: String = "") {
        self.id = id
        self.title = title
        self.group = group
        self.keywords = keywords
        self.defaultChord = chord
    }
}

/// Every action a shortcut can be given, in menu order. Settings lists them;
/// `Keys` (the Mac app) binds them.
public enum Keybinds {
    public static let groups = ["Hammock", "File", "Edit", "View", "Pane", "Floating Panes", "Simulator", "Editor", "Help"]

    /// Groups whose actions only apply with that kind of pane focused: two
    /// of them can share a shortcut (⌘S saves a file, or screenshots a simulator).
    public static let paneScoped: Set<String> = ["Simulator", "Editor"]

    /// Whether two actions can't both be reached at once, so may share a shortcut.
    public static func apart(_ a: String, _ b: String) -> Bool {
        guard let x = byID[a]?.group, let y = byID[b]?.group else { return false }
        return x != y && paneScoped.contains(x) && paneScoped.contains(y)
    }

    public static let actions: [KeybindAction] = {
        func c(_ key: String, _ m: KeyChord.Modifiers = .command) -> KeyChord { KeyChord(key, m) }
        let cs: KeyChord.Modifiers = [.command, .shift], co: KeyChord.Modifiers = [.command, .option]
        let cc: KeyChord.Modifiers = [.command, .control], cos: KeyChord.Modifiers = [.command, .option, .shift]
        let cco: KeyChord.Modifiers = [.command, .control, .option]
        return [
            // Hammock (the app menu)
            .init("about", "About Hammock", "Hammock", nil, "version info uninstall quit stop server"),
            .init("check_for_updates", "Check for Updates…", "Hammock", nil, "upgrade new version sparkle"),
            // Help (listed last in Settings ▸ Keyboard, as in the menu bar)
            .init("welcome", "Welcome to Hammock…", "Help", nil, "onboarding help intro tour"),
            .init("documentation", "Hammock Documentation", "Help", nil, "docs help manual guide reference website"),
            .init("acknowledgements", "Acknowledgements", "Help", nil, "licences licenses third party open source credits notices copyright"),
            // File
            .init("new_tab", "New Tab", "File", c("t"), "create terminal shell"),
            .init("new_browser_tab", "New Browser Tab", "File", c("b", cs), "create web chrome page"),
            .init("new_agent_tab", "New Agent Tab…", "File", c("n", co), "start claude codex ai session launch"),
            .init("new_log_tab", "New Log Tab…", "File", c("l", co), "create console logs logcat tail stream"),
            .init("new_editor_tab", "New Editor Tab", "File", c("e", co), "create text file untitled note write code"),
            .init("new_diff_tab", "New Diff Tab…", "File", nil, "create git review changes compare branch commit range uncommitted"),
            .init("quick_open", "Quick Open…", "File", c("p"), "find file fuzzy go to open editor"),
            .init("open_file", "Open File…", "File", c("o"), "editor text code browse finder"),
            .init("new_scheduled_task", "New Scheduled Task…", "File", c("n", cos), "create schedule cron timer recurring automation"),
            .init("new_group", "New Group", "File", c("n"), "create workspace project"),
            .init("new_group_in_directory", "New Group in Directory…", "File", c("n", cs), "create folder project open path cwd workspace"),
            .init("new_group_on_computer", "New Group on Computer 1…9", "File", c(KeyChord.digits, cco), "create linked computer mac remote"),
            .init("link_computer", "Link a Computer…", "File", c("l", cc), "pair mac another machine linux tailscale local network vpn"),
            .init("pair_device", "Pair a Device…", "File", nil, "iphone phone ipad link"),
            .init("join_shared_group", "Join Shared Group…", "File", nil, "invite share link"),
            .init("rename_tab", "Rename Tab…", "File", c("r", cs), "name title label"),
            .init("configure_group", "Configure Group…", "File", c("g", co), "rename name icon emoji picture edit settings"),
            .init("change_group_folder", "Change Group Folder…", "File", nil, "directory cwd path"),
            .init("share_group", "Share Group…", "File", nil, "invite collaborate"),
            .init("command_palette", "Command Palette…", "File", c("k"), "commands search quick open launcher"),
            .init("find_session", "Find Session…", "File", c("o", cs), "resume history claude agent search"),
            .init("find_session_in_group", "Resume a Session from This Group…", "File", c("o", co), "history claude agent search"),
            .init("close", "Close", "File", c("w"), "close pane tab group"),
            .init("close_window", "Close Window", "File", c("w", cs)),
            .init("reopen_closed", "Reopen Closed Tab or Pane", "File", c("t", cs), "undo close restore"),
            // Edit
            .init("find_in_page", "Find in Page…", "Edit", c("f"), "search browser"),
            .init("find_next", "Find Next", "Edit", c("g"), "search browser"),
            .init("find_previous", "Find Previous", "Edit", c("g", cs), "search browser back"),
            // View
            .init("toggle_sidebar", "Show or Hide Sidebar", "View", c("s", cc), "hide show peek"),
            .init("show_all_computers", "Show All Computers", "View", c("0", cc), "filter sidebar every linked"),
            .init("show_computer", "Show Computer 1…9", "View", c(KeyChord.digits, cc), "filter sidebar only linked"),
            // Not ⌥⌘S: AppKit's own (hidden) Toggle Sidebar has it, and took it.
            .init("scheduled_tasks", "Scheduled Tasks", "View", c("s", cs), "schedule cron timer recurring runs automation"),
            // ⌥⌘R: free in macOS and here.
            .init("review_changes", "Review Changes", "View", c("r", co), "diff git review changes pull request branch commit uncommitted"),
            // ⌥⌘Z: free in macOS; a setting, so it works with any pane focused.
            .init("editor_wrap", "Wrap Lines in Editors", "View", c("z", co), "text word soft wrap long lines editor"),
            .init("next_tab", "Next Tab", "View", c("]", cs), "switch cycle go forward"),
            .init("previous_tab", "Previous Tab", "View", c("[", cs), "switch cycle go back"),
            .init("next_group", "Next Group", "View", c("down", co), "switch cycle go"),
            .init("previous_group", "Previous Group", "View", c("up", co), "switch cycle go back"),
            .init("select_tab", "Tab 1…9", "View", c(KeyChord.digits), "go to switch nth number"),
            .init("bigger", "Bigger", "View", c("plus"), "zoom in font size larger increase"),
            .init("smaller", "Smaller", "View", c("minus"), "zoom out font size decrease"),
            .init("actual_size", "Actual Size", "View", c("0"), "zoom reset font size"),
            // Pane
            .init("next_pane", "Next Pane", "Pane", c("]"), "switch cycle focus"),
            .init("previous_pane", "Previous Pane", "Pane", c("["), "switch cycle focus back"),
            .init("split_right", "Split Right", "Pane", c("d"), "divide vertical side by side"),
            .init("split_down", "Split Down", "Pane", c("d", cs), "divide horizontal below"),
            .init("split_left", "Split Left", "Pane", nil, "divide vertical"),
            .init("split_up", "Split Up", "Pane", nil, "divide horizontal above"),
            .init("toggle_agent_view", "Toggle Agent View", "Pane", c("a", cs), "terminal log raw claude"),
            .init("copy_session_link", "Copy Session Link", "Pane", c("c", co), "claude code remote control web url share agent conversation"),
            // ⇧⌘M: free in macOS and here (⇧⌘D is Split Down).
            .init("dictate", "Dictate", "Pane", c("m", cs), "dictation voice speech microphone mic talk speak transcribe agent view"),
            .init("sleep_agent", "Sleep or Wake Agent", "Pane", nil, "wake resume suspend idle memory ram free claude codex agent"),
            .init("split_browser_right", "Split Right with Browser", "Pane", c("b", co), "web divide"),
            .init("split_browser_down", "Split Down with Browser", "Pane", c("b", cos), "web divide"),
            .init("dev_tools", "Show DevTools", "Pane", c("i", co), "inspector developer browser console"),
            .init("split_log_right", "Split Right with Log…", "Pane", nil, "logs console divide"),
            .init("split_diff_right", "Split Right with Diff…", "Pane", nil, "git review changes compare branch commit divide"),
            .init("split_editor_right", "Split Right with Editor", "Pane", c("e", cc), "pane text file code untitled"),
            .init("split_editor_down", "Split Down with Editor", "Pane", c("e", [.command, .control, .shift]), "pane text file code untitled"),
            .init("show_simulator_log", "Show Simulator Log", "Pane", nil, "logs console ios iphone"),
            .init("zoom_pane", "Zoom Pane", "Pane", c("return", cs), "maximise maximize fullscreen focus unzoom"),
            .init("move_pane_to_new_tab", "Move Pane to New Tab", "Pane", nil, "break out detach"),
            .init("close_pane", "Close Pane", "Pane", nil),
            // Floating panes (⌘M is the window's Minimize, ⌥⌘F the simulator's fold)
            .init("new_floating_pane", "New Floating Pane", "Floating Panes", c("f", cs), "float overlay popup window"),
            .init("toggle_floats", "Show or Hide Floating Panes", "Floating Panes", c("j"), "float overlay toggle"),
            .init("float_or_dock", "Float or Dock Pane", "Floating Panes", c("j", cs), "float overlay"),
            .init("minimise_float", "Minimise Floating Pane", "Floating Panes", c("m", cc), "minimize float hide"),
            // Simulator (only with one focused: ⌘← and ⌘→ are the terminal's otherwise)
            .init("sim_home", "Home", "Simulator", c("h", cs), "ios iphone button"),
            .init("sim_lock", "Lock", "Simulator", c("l"), "ios iphone sleep power button"),
            .init("sim_rotate_left", "Rotate Left", "Simulator", c("left"), "ios iphone orientation turn"),
            .init("sim_rotate_right", "Rotate Right", "Simulator", c("right"), "ios iphone orientation turn"),
            .init("sim_keyboard", "Toggle Software Keyboard", "Simulator", c("k", cs), "ios iphone typing"),
            // Simulator.app's own Save Screen and Copy Screen.
            .init("sim_screenshot_file", "Screenshot to File", "Simulator", c("s"), "ios iphone capture save image"),
            .init("sim_screenshot_copy", "Screenshot to Clipboard", "Simulator", c("c", cc), "ios iphone capture copy image"),
            .init("sim_fold", "Open or Close Phone", "Simulator", c("f", co), "ios iphone fold foldable duo hinge"),
            .init("sim_half_open", "Half Open Phone", "Simulator", nil, "ios iphone fold foldable duo hinge"),
            .init("sim_shut_down", "Shut Down Simulator", "Simulator", nil, "ios iphone stop quit"),
            // Editor (only with one focused: ⌘S and ⌘L are the simulator's otherwise)
            .init("editor_save", "Save", "Editor", c("s"), "write file disk"),
            .init("editor_save_as", "Save As…", "Editor", c("s", cos), "write file rename new name"),
            .init("editor_go_to_line", "Go to Line…", "Editor", c("l"), "jump line number"),
            .init("editor_reload", "Reload from Disk", "Editor", nil, "revert discard changes file"),
            .init("editor_show_diff", "Compare with File on Disk", "Editor", nil, "diff changes conflict merge"),
        ]
    }()

    public static let byID: [String: KeybindAction] = Dictionary(uniqueKeysWithValues: actions.map { ($0.id, $0) })

    /// Shortcuts macOS or every Mac app keeps, which a binding would shadow
    /// or never receive: why, for a warning.
    public static let reserved: [KeyChord: String] = {
        let cs: KeyChord.Modifiers = [.command, .shift], co: KeyChord.Modifiers = [.command, .option], cc: KeyChord.Modifiers = [.command, .control]
        var r: [KeyChord: String] = [
            KeyChord("q"): "Quit Hammock", KeyChord("h"): "Hide Hammock", KeyChord("h", co): "Hide Others",
            KeyChord("m"): "Minimize", KeyChord("c"): "Copy", KeyChord("v"): "Paste", KeyChord("x"): "Cut",
            KeyChord("a"): "Select All", KeyChord("z"): "Undo", KeyChord("z", cs): "Redo",
            KeyChord("tab"): "the app switcher", KeyChord("space"): "Spotlight", KeyChord("`"): "cycling windows",
            KeyChord("q", cc): "Lock Screen", KeyChord("f", cc): "full screen", KeyChord("d", cc): "Look Up",
            KeyChord("d", co): "hiding the Dock", KeyChord("t", co): "the toolbar", KeyChord("escape", co): "Force Quit",
            KeyChord("space", cc): "the emoji picker", KeyChord("space", .control): "switching input sources",
            KeyChord("3", cs): "screenshots", KeyChord("4", cs): "screenshots", KeyChord("5", cs): "screenshots",
            KeyChord("/", cs): "Help", KeyChord("q", cs): "Log Out", KeyChord("q", [.command, .option, .shift]): "Log Out",
            KeyChord("f2", .control): "focusing the menu bar", KeyChord("f3", .control): "focusing the Dock",
        ]
        for k in ["up", "down", "left", "right"] { r[KeyChord(k, .control)] = "Mission Control" }
        return r
    }()

    /// Why macOS keeps this chord (or one of its family's), if it does.
    public static func reservedReason(_ chord: KeyChord) -> String? {
        if let r = reserved[chord] { return r }
        guard chord.isFamily else { return nil }
        return (0..<9).lazy.compactMap { chord.member($0).flatMap { reserved[$0] } }.first
    }
}

/// A `keybind` line that couldn't be used, and why. Settings keeps it in the
/// file when it rewrites the others, so it can still be fixed by hand.
public struct KeybindProblem: Equatable, Hashable, Sendable, CustomStringConvertible {
    public let line: String
    public let reason: String
    public var description: String { "“\(line)”: \(reason)" }
}

/// The shortcut each action has: the defaults, changed by the config file's
/// `keybind` lines (applied in order, as Ghostty does):
///
///     keybind = cmd+shift+b=new_browser_tab   a shortcut for an action (taking it from any other)
///     keybind = cmd+j=unbind                  no action on cmd+j (cmd+1 also unbinds a ⌘1…9 family)
///     keybind = none=new_tab                  no shortcut for New Tab
///     keybind = ctrl+cmd+digits=select_tab    a numbered family's modifiers (⌃⌘1…9)
///     keybind = clear                         start from no shortcuts at all
public struct KeybindSet: Equatable, Sendable {
    /// Action id → its shortcut; absent: none.
    public private(set) var chords: [String: KeyChord]

    public static let defaults = KeybindSet(chords: Dictionary(uniqueKeysWithValues: Keybinds.actions.compactMap { a in a.defaultChord.map { (a.id, $0) } }))

    public init(chords: [String: KeyChord]) { self.chords = chords }

    public subscript(id: String) -> KeyChord? { chords[id] }

    /// The `keybind` values, applied over the defaults. Lines that can't be
    /// read are skipped and described in `problems`.
    public static func parse(_ values: [String]) -> (set: KeybindSet, problems: [KeybindProblem]) {
        var set = defaults
        var problems: [KeybindProblem] = []
        for raw in values {
            let v = raw.trimmingCharacters(in: .whitespaces)
            if v == "clear" { set.chords = [:]; continue }
            guard let eq = v.lastIndex(of: "="), eq != v.startIndex else {
                problems.append(KeybindProblem(line: v, reason: "expected trigger=action"))
                continue
            }
            let trigger = v[..<eq].trimmingCharacters(in: .whitespaces)
            let action = v[v.index(after: eq)...].trimmingCharacters(in: .whitespaces).lowercased()
            if trigger.lowercased() == "none" {
                guard Keybinds.byID[action] != nil else { problems.append(KeybindProblem(line: v, reason: "no action called \(action)")); continue }
                set.chords[action] = nil
                continue
            }
            guard let chord = KeyChord(parsing: trigger) else {
                problems.append(KeybindProblem(line: v, reason: "can’t read the shortcut “\(trigger)”"))
                continue
            }
            if action == "unbind" {
                for (id, c) in set.chords where c.overlaps(chord) { set.chords[id] = nil }
                continue
            }
            guard let a = Keybinds.byID[action] else {
                problems.append(KeybindProblem(line: v, reason: "no action called \(action)"))
                continue
            }
            if a.isFamily != chord.isFamily {
                problems.append(KeybindProblem(line: v, reason: a.isFamily ? "\(action) takes modifiers+digits" : "only a numbered action takes digits"))
                continue
            }
            set.assign(chord, to: action)
        }
        return (set, problems)
    }

    /// Gives an action a shortcut (or none), taking it from any action that
    /// had it. Returns the actions it was taken from.
    @discardableResult
    public mutating func assign(_ chord: KeyChord?, to action: String) -> [String] {
        var displaced: [String] = []
        if let chord {
            for (id, c) in chords where id != action && c.overlaps(chord) && !Keybinds.apart(id, action) {
                chords[id] = nil
                displaced.append(id)
            }
        }
        chords[action] = chord
        return displaced.sorted()
    }

    /// The other actions using this chord (or one of its family's).
    public func conflicts(_ chord: KeyChord, excluding action: String? = nil) -> [String] {
        let order = { (id: String) in Keybinds.actions.firstIndex { $0.id == id } ?? 0 }
        return chords.filter { entry in
            entry.key != action && entry.value.overlaps(chord) && !(action.map { Keybinds.apart(entry.key, $0) } ?? false)
        }.map(\.key).sorted { order($0) < order($1) }
    }

    public func isDefault(_ id: String) -> Bool { chords[id] == Keybinds.byID[id]?.defaultChord }

    /// The `keybind` lines that make this set from the defaults: shortcuts
    /// taken away first, then the ones given.
    public var configValues: [String] {
        var out: [String] = []
        let changed = Keybinds.actions.filter { !isDefault($0.id) }
        for a in changed where chords[a.id] == nil {
            // Its default isn't simply someone else's now (given it below,
            // which takes it from this one): say so. One that may share it
            // (Keybinds.apart) doesn't take it.
            guard let d = a.defaultChord, !changed.contains(where: { b in
                b.id != a.id && chords[b.id]?.overlaps(d) == true && !Keybinds.apart(a.id, b.id)
            }) else { continue }
            out.append("none=\(a.id)")
        }
        for a in changed {
            if let c = chords[a.id] { out.append("\(c.configString)=\(a.id)") }
        }
        return out
    }
}

/// Settings' search over the actions: words in any order, matching titles,
/// groups, keywords and synonyms, forgiving typos and abbreviations; or a
/// shortcut typed as text (`cmd d`, `⇧⌘B`), finding what it's bound to.
public enum KeybindSearch {
    static let synonyms: [String: [String]] = [
        "search": ["find"], "find": ["search"], "web": ["browser"], "browser": ["web"],
        "window": ["pane", "tab"], "divide": ["split"], "split": ["divide"],
        "zoom": ["bigger", "smaller", "size"], "font": ["bigger", "smaller", "size"],
        "ai": ["agent", "claude"], "claude": ["agent"], "codex": ["agent"], "agent": ["claude"],
        "ios": ["simulator"], "iphone": ["simulator"], "sim": ["simulator"],
        "prefs": ["settings"], "preferences": ["settings"], "keys": ["keyboard", "shortcuts"],
        "hotkey": ["keyboard", "shortcuts"], "kill": ["close"], "quit": ["close"], "exit": ["close"],
        "workspace": ["group"], "project": ["group"], "float": ["floating"], "popup": ["floating"],
        "cron": ["scheduled"], "timer": ["scheduled"], "go": ["next", "previous", "select"],
    ]

    /// The actions matching the query, best first (all of them, in order, for an empty one).
    public static func rank(_ query: String, in set: KeybindSet, actions: [KeybindAction] = Keybinds.actions) -> [KeybindAction] {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return actions }
        func ranked(asText: Bool) -> [KeybindAction] {
            var scored: [(KeybindAction, Int, Int)] = []
            for (i, a) in actions.enumerated() {
                if let s = score(q, a, set[a.id], asText: asText) { scored.append((a, s, i)) }
            }
            return scored.sorted { $0.1 != $1.1 ? $0.1 > $1.1 : $0.2 < $1.2 }.map(\.0)
        }
        // Read as a shortcut and bound to nothing, it may be words on the
        // way to a title (“command p…” for Command Palette).
        let results = ranked(asText: false)
        return results.isEmpty && (chord(q) != nil || modifiersOnly(q) != nil) ? ranked(asText: true) : results
    }

    /// The query read as a shortcut, if it's written like one (with a modifier).
    public static func chord(_ q: String) -> KeyChord? {
        guard modifiersOnly(q) == nil, hasModifier(q) else { return nil }
        let joined = q.trimmingCharacters(in: .whitespaces).replacingOccurrences(of: " ", with: "+").replacingOccurrences(of: "++", with: "+")
        return KeyChord(parsing: joined) ?? KeyChord(parsing: q)
    }

    /// The query as modifiers alone (`⌃⌘`, `ctrl cmd`): every shortcut using exactly those.
    public static func modifiersOnly(_ q: String) -> KeyChord.Modifiers? {
        var mods: KeyChord.Modifiers = []
        var rest = q.lowercased()
        for (m, g) in KeyChord.glyphs where rest.contains(g) {
            mods.insert(m)
            rest.removeAll { $0 == g }
        }
        for word in rest.split(whereSeparator: { $0 == "+" || $0 == " " }) {
            guard let m = KeyChord.modifierNames[String(word)] else { return nil }
            mods.insert(m)
        }
        return mods.isEmpty ? nil : mods
    }

    static func hasModifier(_ q: String) -> Bool {
        q.contains { "⌘⌃⌥⇧".contains($0) }
            || q.lowercased().split(whereSeparator: { $0 == "+" || $0 == " " }).contains { KeyChord.modifierNames[String($0)] != nil }
    }

    public static func score(_ q: String, _ a: KeybindAction, _ chord: KeyChord?, asText: Bool = false) -> Int? {
        if !asText, let mods = modifiersOnly(q) {
            guard let chord, chord.modifiers == mods else { return nil }
            return 5_000
        }
        if !asText, let typed = Self.chord(q) {
            guard let chord else { return nil }
            if chord == typed { return 10_000 }
            return chord.overlaps(typed) ? 9_000 : nil
        }
        let lower = q.lowercased()
        let title = a.title.lowercased()
        let words = words(a)
        let tokens = lower.split(separator: " ").map(String.init)
        var total = 0
        for t in tokens {
            let alternatives = [t] + (synonyms[t] ?? [])
            // A synonym counts for half: the word itself is the better match.
            let scores = alternatives.enumerated().compactMap { i, alt in tokenScore(alt, words).map { i == 0 ? $0 : $0 / 2 } }
            guard let best = scores.max() else {
                // An abbreviation of the title (“nbt” for New Browser Tab) is the last resort.
                return tokens.count == 1 ? abbreviation(t, title) : nil
            }
            total += best
        }
        if title.hasPrefix(lower) { total += 50 }
        return total
    }

    /// Words to match, weighted: the title's, then the group's and id's, then keywords.
    static func words(_ a: KeybindAction) -> [(String, Int)] {
        func split(_ s: String) -> [String] {
            s.lowercased().split(whereSeparator: { !$0.isLetter && !$0.isNumber }).map(String.init)
        }
        return split(a.title).map { ($0, 3) } + split(a.group).map { ($0, 2) } + split(a.id).map { ($0, 2) } + split(a.keywords).map { ($0, 1) }
    }

    /// How well one typed word matches the best of the action's words:
    /// whole word, its start, inside it, or within a typo or two of it (or of its start).
    static func tokenScore(_ t: String, _ words: [(String, Int)]) -> Int? {
        let limit = t.count >= 7 ? 2 : 1
        var best: Int?
        for (w, weight) in words {
            let s: Int
            if w == t {
                s = 30
            } else if w.hasPrefix(t) {
                s = 20
            } else if t.count >= 3, w.contains(t) {
                s = 10
            } else if t.count >= 4, editDistance(t, w, limit: limit) != nil || editDistance(t, String(w.prefix(t.count)), limit: limit) != nil {
                s = 6
            } else {
                continue
            }
            best = max(best ?? 0, s * weight)
        }
        return best
    }

    /// The query's letters in order through the title, word starts counting most: “nbt”, “splr”.
    static func abbreviation(_ t: String, _ title: String) -> Int? {
        guard t.count >= 2 else { return nil }
        var idx = title.startIndex, score = 0
        for ch in t {
            guard let found = title[idx...].firstIndex(of: ch) else { return nil }
            let atWordStart = found == title.startIndex || title[title.index(before: found)] == " "
            score += atWordStart ? 4 : 1
            idx = title.index(after: found)
        }
        return score
    }

    /// Levenshtein distance (a swapped pair counting once), if within the limit.
    static func editDistance(_ a: String, _ b: String, limit: Int) -> Int? {
        let a = Array(a), b = Array(b)
        guard abs(a.count - b.count) <= limit else { return nil }
        guard !a.isEmpty, !b.isEmpty else { return max(a.count, b.count) <= limit ? max(a.count, b.count) : nil }
        var prev2: [Int] = [], prev = Array(0...b.count)
        for i in 1...a.count {
            var cur = [i] + Array(repeating: 0, count: b.count)
            for j in 1...b.count {
                cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (a[i - 1] == b[j - 1] ? 0 : 1))
                if i > 1, j > 1, a[i - 1] == b[j - 2], a[i - 2] == b[j - 1] { cur[j] = min(cur[j], prev2[j - 2] + 1) }
            }
            prev2 = prev
            prev = cur
        }
        return prev[b.count] <= limit ? prev[b.count] : nil
    }
}
