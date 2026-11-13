import AppKit
import HammockKit
import SwiftUI

/// Hammock's config file, ~/.config/hammock/config, in Ghostty's syntax:
///
///     appearance = auto | light | dark
///     app-theme = Hammock              (a curated theme: AppTheme.all; or custom: theme, window-theme, agent-color,
///                                       active-pane-border-color, sidebar-color and toolbar-color apply.
///                                       Unset: Hammock, or custom if any of those is set, from before themes)
///     theme = dark:NAME,light:NAME     (or `theme = NAME` for both)
///     font-family = Hack Nerd Font
///     font-size = 15
///     font-thicken = false
///     terminal-renderer = metal        (or coretext: draw terminal panes with CoreGraphics, as before Metal; for panes opened after it's set)
///     adjust-cell-height = 20%         (line spacing: taller (or, negative, shorter) terminal rows; or points: 2)
///     window-theme = NAME              (Native, Graphite, Hammock, Ocean, …, Match Terminal)
///     agent-color = Ocean              (the agent view's colour: an AgentColor or theme name, or #RRGGBB, light:#…,dark:#…; unset: the window theme's)
///     sidebar-color = #RRGGBB          (or light:#…,dark:#…; overrides the window theme, with app-theme = custom)
///     toolbar-color = #RRGGBB
///     sidebar-opacity = 0.4            (0 glassy … 1 opaque)
///     clipboard-paste-protection = true      (ask before a paste that could run commands)
///     clipboard-paste-bracketed-safe = true  (unless the program takes bracketed pastes)
///     simulator-shutdown = true        (simulators Hammock booted shut down after their last pane; read by hmkd)
///     agent-presence = true            (show agents acting in browser and simulator panes: a cursor, a glow and a badge; read by hmkd)
///     ssh-agent-forwarding = true      (panes on your linked computers use the SSH keys of the one you're at; read by hmkd)
///     ssh-upstream-agent = PATH        (the SSH agent Hammock's passes requests on to; default: ~/.ssh/config's IdentityAgent, else SSH_AUTH_SOCK; read by hmkd)
///     screen-sharing = false           (your iPhone and other Macs may see and control this Mac's screen; read by hmkd)
///     remote-network = tailscale       (repeatable: the networks remote access listens on: tailscale, or a subnet like 192.168.1.0/24;
///                                       unset: Tailscale only; read by hmkd; Settings ▸ Devices writes it)
///     remote-address = HOST[:PORT]     (repeatable: extra addresses given out in pairing codes and links, a DNS name or a forwarded port; read by hmkd)
///     usage-live = false               (agents' usage limits asked of each agent's own service every 5 minutes, with the sign-in its CLI keeps here, not just what it saves on disk; read by hmkd)
///     usage-live-agents = claude, codex  (the agents usage-live asks: Settings keeps it to those whose limits the sidebar shows; unset: all; read by hmkd)
///     menu-bar = always                (or active: the menu bar item only while a screen's shared or the server's down; read by Hammock Menu)
///     keep-empty-groups = true         (⌘W on a Group's last tab keeps the Group; false: closes it too)
///     notify-needs-input = true        (and notify-permission, notify-finished, notify-error, notify-review-replies: which agent events notify)
///     notify-style = auto              (or system, in-app: where notifications show)
///     notify-current-tab = false       (also for the tab you're looking at; dock-badge = true)
///     notify-sound-needs-input = Hammock Chime  (and notify-sound-permission, -finished, -error, -other: each kind's sound,
///                                       Hammock Kalimba, Chime, Low, Marimba or Bell, a macOS sound like Glass, or none)
///     sidebar-layout = vertical        (or compact, horizontal: the Group's tabs in a tab bar)
///     sidebar-finished = true          (finished agents listed at the bottom of the sidebar)
///     collapsed-group-agents = false   (collapsed Groups still show tabs with an active agent)
///     confirm-close-busy-agent = true  (ask before closing an agent at work)
///     simulator-frame = true           (the device drawn around a simulator pane)
///     group-sharing = false            (true: Share Group in the toolbar, menus and palette)
///     agent-default-view = terminal    (or agent: the view agent panes open in)
///     agent-font-family = NAME         (agent-font-size = 14, agent-line-spacing = 3; agent-code-font-family, unset: the
///                                       terminal's; agent-code-font-size = 13; agent-code-wrap = prose, always or never.
///                                       HammockKit's Prefs, UserDefaults on the iPhone)
///     agent-image-size = large         (or original, medium, small: pictures attached in the agent view, scaled to
///                                       1568, 1024 or 768 px; agent-image-size-remote = medium, for a linked computer's)
///     usage-limits-claude = false      (usage-limits-codex: an agent's usage limits in the sidebar)
///     group-headings = prominent       (or subtle: Group headings in the sidebar smaller and dimmer than its tabs)
///     group-icons = true               (false: no icons on Group headings in the sidebar)
///     tab-status = icon                (or trailing: sidebar rows show which agent on the left, status at the end)
///     pane-spacing = compact           (or spacious: panes spaced apart, with rounded corners)
///     dim-inactive-panes = false       (true: panes other than the focused one are dimmed a little)
///     active-pane-border = false       (true: a border around the focused pane)
///     active-pane-border-color = accent  (or #RRGGBB, light:#…,dark:#…)
///     pane-animations = true           (false: panes open, close, zoom and float at once, without animating)
///     browser-open-schemes = obsidian,zoommtg  (links a browser pane opens in their app without asking)
///     browser-new-tab = external       (hammock: links a page opens in a new tab open as a browser tab, not in your browser; read by hmkd)
///     keybind = cmd+shift+b=new_browser_tab    (repeatable: a shortcut for an action, taking it from any other;
///                                               cmd+j=unbind, none=new_tab, ctrl+cmd+digits=select_tab, clear.
///                                               Actions: Keybinds.actions in HammockKit; Settings ▸ Keyboard writes them)
///     tab-close-button = true          (false: no close button on a tab you hover over, in the sidebar or the tab bar)
///     program-icons = plain            (colorful: on a squircle in the program's colours, agents' logos too; off: the terminal glyph for all but agents)
///     pane-icon = btop=monitor         (a program's icon: a built-in icon (vim, git, python, monitor…: scripts/program-icons/icons.json) or an SF Symbol; several with commas, or repeat the key)
///     editor-wrap = false              (editor panes: wrap long lines; editor-line-numbers = true; editor-vim = false: Vim keys)
///     editor-tab-width = 4             (editor-indent = spaces or tabs; a file's .editorconfig, or its own indentation, wins)
///     editor-font-size = 13            (unset: the terminal's size; editor-tui-external = false: hmk tui opens $EDITOR instead)
///     diff-style = unified             (diff panes: unified or split; diff-whitespace = show or ignore; diff-wrap = false)
///     diff-enabled = true              (and browser-enabled, simulator-enabled, editor-enabled, log-enabled: false and Hammock
///                                       doesn't offer that tool, in its menus or to agents: Settings ▸ Tools; read by hmkd and hmk mcp)
///
/// Font, cursor and paste keys fall back to Ghostty's config. Themes are Ghostty theme files,
/// found in ~/.config/hammock/themes, ~/.config/ghostty/themes and
/// Ghostty.app's bundled themes. Settings edits the same file, and edits made
/// elsewhere apply within a couple of seconds.
///
/// A β keeps its own file, in its state dir (a copy of yours to begin with),
/// so testing never changes your settings. Any key given as a launch argument
/// beats the file's, for testing: `open -n "Hammock β.app" --args -notify-style in-app`.
@MainActor
@Observable
final class AppConfig {
    static let shared = AppConfig()
    static let changed = Notification.Name("HammockConfigChanged")

    enum Appearance: String, CaseIterable, Identifiable {
        case auto, light, dark
        var id: String { rawValue }
    }

    /// Where a sidebar row shows its agent's state: in the tab's icon, or
    /// at the end of the row, with the icon saying which agent it is.
    enum TabStatus: String {
        case icon, trailing
    }

    /// How a Group's heading looks in the sidebar (with tabs under it):
    /// prominent, bigger than its tabs, or subtle, smaller and dimmer
    /// than them (its icon too, if shown: group-icons).
    enum GroupHeadings: String {
        case prominent, subtle
    }

    private(set) var appearance: Appearance = .auto
    /// `app-theme`: a curated theme's name (AppTheme), or AppTheme.custom,
    /// when the terminal, window, agent and border colours are each set.
    private(set) var appThemeName = AppTheme.defaultName
    /// The curated theme in use; nil with Custom.
    var appTheme: AppTheme? { AppTheme.named(appThemeName) }
    var isCustomTheme: Bool { appTheme == nil }
    /// Custom's agent colour (`agent-color`): nil follows the window theme.
    private(set) var agentColorValue: String?
    private(set) var lightThemeName = TerminalTheme.builtInLight
    private(set) var darkThemeName = TerminalTheme.builtInDark
    private(set) var lightTheme = TerminalTheme.light
    private(set) var darkTheme = TerminalTheme.dark
    private(set) var fontFamily: String?
    private(set) var fontSize: Double?
    /// The font comes from Ghostty's config (not set in Hammock's).
    private(set) var fontFromGhostty = false
    /// The keys Ghostty's config sets: removing one of them from Hammock's
    /// brings Ghostty's value back (see setOverGhostty).
    @ObservationIgnored private var ghosttyKeys: Set<String> = []
    private(set) var fontThicken = false
    /// terminal-renderer: "metal" (the default) draws terminal panes with
    /// Metal (#18); "coretext" with CoreGraphics, as before.
    private(set) var terminalRenderer = "metal"
    /// Terminal line spacing, Ghostty's `adjust-cell-height`: "20%" (of the
    /// font's line height) or "2" (points); negative makes rows shorter.
    private(set) var cellHeightAdjust: String?
    /// The percentage, when it's given as one (Settings' stepper).
    var cellHeightPercent: Double? {
        guard let v = cellHeightAdjust, v.hasSuffix("%") else { return nil }
        return Double(v.dropLast().trimmingCharacters(in: .whitespaces))
    }
    /// The extra height (points) for a font whose natural line height is `base`.
    func cellHeightExtra(base: Double) -> Double {
        guard let v = cellHeightAdjust else { return 0 }
        if let pct = cellHeightPercent { return base * pct / 100 }
        return Double(v) ?? 0
    }
    private(set) var cursorStyleName = "block"
    var cursorStyle: CursorStyle { Self.cursorStyle(cursorStyleName) }
    /// Thin Bar is a bar drawn 1pt wide (Ghostty's bar is that thin).
    var cursorThin: Bool { cursorStyleName == "bar_thin" }
    private(set) var cursorBlink = true
    private(set) var copyOnSelect = false
    /// Ask before pasting text that could run commands as it lands (a line
    /// break, or the end-of-paste sequence), as Ghostty does.
    private(set) var pasteProtection = true
    /// …except into a program that asked for bracketed paste.
    private(set) var pasteBracketedSafe = true
    private(set) var windowThemeName = WindowTheme.native
    private(set) var sidebarColor: (light: NSColor, dark: NSColor)?
    private(set) var toolbarColor: (light: NSColor, dark: NSColor)?
    /// How opaque the sidebar's tint is: 0 is the glass alone, 1 solid.
    private(set) var sidebarOpacity = 0.0
    /// …as the file has it, not following the slider while it moves.
    private(set) var savedSidebarOpacity = 0.0
    /// Simulators Hammock booted shut down a minute after their last pane
    /// closes (the daemon reads the key; this is for Settings).
    private(set) var simulatorShutdown = true
    /// Links a page opens in a new tab go to the person's default browser
    /// (`browser-new-tab = external`, the default), not a Hammock browser tab.
    /// hmkd reads the key; Settings shows it.
    private(set) var browserNewTabExternal = true
    /// SSH agent forwarding between your linked computers: new panes use
    /// Hammock's agent, which adds the keys of the computer you're at (the
    /// daemon reads the key; this is for Settings).
    private(set) var sshAgentForwarding = true
    /// Screen sharing (ADR-0014): your paired phones and linked computers
    /// may see and control this Mac's screen (the daemon reads the key).
    private(set) var screenSharing = false
    /// Agents' usage limits asked of each one's own service, with the
    /// sign-in its CLI keeps here, rather than what it saves on disk now
    /// and then (the daemon reads the key; this is for Settings). Only the
    /// agents in usage-live-agents are asked: the ones shown in the sidebar
    /// (followUsageChoice).
    private(set) var usageLive = false
    /// claude-usage-live, the setting's name when it asked Anthropic alone:
    /// still asks for Claude's, and only Claude's (the daemon reads it).
    private(set) var claudeUsageLive = false
    /// usage-live-agents: the agents usage-live asks (followUsageChoice
    /// keeps it; nil while unset).
    private(set) var usageLiveAgents: String?
    /// The menu bar item (Hammock Menu): "always", or "active" (only while a
    /// screen's shared or the server's down).
    private(set) var menuBar = "always"
    /// ⌘W on a Group's last tab leaves the Group (empty, showing what to
    /// open next) rather than closing it too.
    private(set) var keepEmptyGroups = true
    /// Where sidebar rows show agent status.
    private(set) var tabStatus = TabStatus.icon
    /// How Group headings look in the sidebar.
    private(set) var groupHeadings = GroupHeadings.prominent
    /// Group headings in the sidebar show the Group's icon.
    private(set) var groupIcons = true
    /// How a tab's split panes sit: hairlines between them (compact), or
    /// spaced apart as rounded cards on an empty Group's background (spacious).
    enum PaneSpacing: String { case compact, spacious }
    private(set) var paneSpacing = PaneSpacing.compact
    /// Panes other than the focused one dimmed a little.
    private(set) var dimInactivePanes = false
    /// Panes animate opening, closing, zooming and floating; off, they
    /// change at once.
    private(set) var paneAnimations = true
    /// A close button on the tab under the pointer (sidebar and tab bar).
    private(set) var tabCloseButton = true
    /// How terminals show their program: plain, colorful, or off.
    private(set) var programIconStyle = ProgramIconStyle.plain
    /// A border around the focused pane, in the accent colour unless
    /// `active-pane-border-color` names one.
    private(set) var activePaneBorder = false
    private(set) var activePaneBorderColor: (light: NSColor, dark: NSColor)?
    /// The raw value, for Settings' colour well ("accent" or nil: the accent).
    private(set) var activePaneBorderColorValue: String?
    /// URL schemes (lowercase, no colon) a browser pane's links open in
    /// their app without asking: chosen with "Always allow" when asked.
    private(set) var browserOpenSchemes: [String] = []
    /// Every action's shortcut: the defaults, changed by `keybind` lines (`Keys` reads them).
    private(set) var keybinds = KeybindSet.defaults
    /// `keybind` lines that couldn't be read, described (Settings ▸ Keyboard shows them).
    private(set) var keybindProblems: [KeybindProblem] = []

    let dir: URL
    var fileURL: URL { dir.appendingPathComponent("config") }
    var themesDir: URL { dir.appendingPathComponent("themes") }

    private let home = FileManager.default.homeDirectoryForCurrentUser
    private var stamp: [Date?] = []
    private var timer: Timer?

    /// Where Hammock's config lives unless told otherwise.
    static var defaultDir: URL { FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".config/hammock") }

    private init() {
        if let d = ProcessInfo.processInfo.environment["HMK_CONFIG_DIR"] {
            dir = URL(fileURLWithPath: d)
        } else if let state = Bundle.main.object(forInfoDictionaryKey: "HammockStateDir") as? String, state != "Hammock" {
            // A β keeps its own, in its state dir, so testing never changes
            // your real settings (hmkd's hammockconfig.Dir does the same).
            dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appendingPathComponent(state)
            Self.seed(dir)
        } else {
            dir = Self.defaultDir
        }
        load()
        timer = Timer.scheduledTimer(withTimeInterval: 1.5, repeats: true) { _ in
            MainActor.assumeIsolated { AppConfig.shared.reloadIfChanged() }
        }
    }

    func theme(isDark: Bool) -> TerminalTheme { isDark ? darkTheme : lightTheme }

    /// The file's path as Settings shows it: ~/.config/hammock/config, or a β's.
    var displayPath: String {
        let p = fileURL.path, h = home.path
        return p.hasPrefix(h + "/") ? "~" + p.dropFirst(h.count) : p
    }

    /// A β's config starts as a copy of your real one, the first time, so it
    /// looks like yours until you change it (hammockconfig.seed does the same).
    private static func seed(_ dir: URL) {
        let dst = dir.appendingPathComponent("config")
        guard !FileManager.default.fileExists(atPath: dst.path),
              let data = try? Data(contentsOf: defaultDir.appendingPathComponent("config")) else { return }
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try? data.write(to: dst, options: .atomic)
    }

    // MARK: Themes

    private var themeDirs: [URL] {
        // A β's config is elsewhere; your own themes still count.
        let own = Self.defaultDir.appendingPathComponent("themes")
        return [themesDir] + (own == themesDir ? [] : [own]) + [home.appendingPathComponent(".config/ghostty/themes"),
         URL(fileURLWithPath: "/Applications/Ghostty.app/Contents/Resources/ghostty/themes")]
    }

    /// Built-ins first, then every theme file found, by name.
    func availableThemes() -> [String] {
        var names = Set<String>()
        for d in themeDirs {
            for f in (try? FileManager.default.contentsOfDirectory(atPath: d.path)) ?? []
            where !f.hasPrefix(".") && !f.contains(".backup") {
                names.insert(f)
            }
        }
        let ours = AppTheme.terminalNames
        names.subtract(ours)
        return [TerminalTheme.builtInLight, TerminalTheme.builtInDark] + ours + names.sorted { $0.localizedStandardCompare($1) == .orderedAscending }
    }

    /// Whether a theme can be used: built in, or its file found.
    func hasTheme(_ name: String) -> Bool {
        name == TerminalTheme.builtInLight || name == TerminalTheme.builtInDark || AppTheme.terminal(named: name) != nil || themeFile(name) != nil
    }

    /// A theme's colours without selecting it (for previews).
    func previewTheme(_ name: String, dark: Bool) -> TerminalTheme {
        resolve(name, base: dark ? .dark : .light)
    }

    private func themeFile(_ name: String) -> URL? {
        themeDirs.map { $0.appendingPathComponent(name) }.first { FileManager.default.fileExists(atPath: $0.path) }
    }

    private func resolve(_ name: String, base: TerminalTheme) -> TerminalTheme {
        switch name {
        case TerminalTheme.builtInLight: return .light
        case TerminalTheme.builtInDark: return .dark
        default:
            // Your own file of that name beats the curated copy (the phone does the same).
            if let url = themeFile(name), let text = try? String(contentsOf: url, encoding: .utf8) {
                return TerminalTheme(ghosttyTheme: text, base: base)
            }
            return AppTheme.terminal(named: name) ?? base
        }
    }

    // MARK: Loading

    private static func read(_ url: URL) -> [String: String] {
        guard let text = try? String(contentsOf: url, encoding: .utf8) else { return [:] }
        var out: [String: String] = [:]
        for line in text.split(whereSeparator: \.isNewline) {
            let t = line.trimmingCharacters(in: .whitespaces)
            guard !t.hasPrefix("#"), let eq = t.firstIndex(of: "=") else { continue }
            let key = t[..<eq].trimmingCharacters(in: .whitespaces)
            let value = t[t.index(after: eq)...].trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\""))
            if out[key] == nil { out[key] = value }
        }
        return out
    }

    /// Every value of a repeatable key, in order.
    private static func readAll(_ url: URL, key: String) -> [String] {
        guard let text = try? String(contentsOf: url, encoding: .utf8) else { return [] }
        return text.split(whereSeparator: \.isNewline).compactMap { line in
            let t = line.trimmingCharacters(in: .whitespaces)
            guard !t.hasPrefix("#"), let eq = t.firstIndex(of: "="), t[..<eq].trimmingCharacters(in: .whitespaces) == key else { return nil }
            return t[t.index(after: eq)...].trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\""))
        }
    }

    /// Keys given as launch arguments (`open … --args -notify-style in-app`),
    /// which beat the file's, for testing: UserDefaults' argument domain.
    private static let launchOverrides: [String: String] = {
        (UserDefaults.standard.volatileDomain(forName: UserDefaults.argumentDomain)).mapValues { "\($0)" }
    }()

    private var ghosttyConfigs: [URL] {
        [home.appendingPathComponent(".config/ghostty/config"),
         home.appendingPathComponent("Library/Application Support/com.mitchellh.ghostty/config")]
    }

    func load() {
        let saved = Self.read(fileURL)
        let ours = saved.merging(Self.launchOverrides) { _, arg in arg }
        var ghostty: [String: String] = [:]
        for url in ghosttyConfigs { ghostty.merge(Self.read(url)) { first, _ in first } }
        ghosttyKeys = Set(ghostty.keys)

        appearance = ours["appearance"].flatMap(Appearance.init(rawValue:)) ?? .auto
        // Empty values count as unset, as hmkd (hammockconfig) reads them.
        let appThemeValue = ours["app-theme"].flatMap { $0.isEmpty ? nil : $0 }
        hasCustomValues = Self.customKeys.contains { ours[$0]?.isEmpty == false }
        let fromBefore = appThemeValue == nil && (hasCustomValues || !Self.legacyAgentColor.isEmpty)
        // An unknown name is Hammock, as hmkd reads it.
        if let v = appThemeValue, v.caseInsensitiveCompare(AppTheme.custom) == .orderedSame {
            appThemeName = AppTheme.custom
        } else {
            appThemeName = fromBefore ? AppTheme.custom : (AppTheme.named(appThemeValue ?? "") ?? AppTheme.named(AppTheme.defaultName)!).name
        }
        agentColorValue = ours["agent-color"].flatMap { $0.isEmpty ? nil : $0 }
        (lightThemeName, darkThemeName) = Self.themePair(ours["theme"])
        if appTheme == nil {
            lightTheme = resolve(lightThemeName, base: .light)
            darkTheme = resolve(darkThemeName, base: .dark)
        }
        fontFamily = (ours["font-family"] ?? ghostty["font-family"]).flatMap { $0.isEmpty ? nil : $0 }
        fontFromGhostty = ours["font-family"] == nil && fontFamily != nil
        fontSize = (ours["font-size"] ?? ghostty["font-size"]).flatMap(Double.init)
        fontThicken = (ours["font-thicken"] ?? ghostty["font-thicken"]) == "true"
        terminalRenderer = ours["terminal-renderer"] == "coretext" ? "coretext" : "metal"
        cellHeightAdjust = (ours["adjust-cell-height"] ?? ghostty["adjust-cell-height"]).flatMap { $0.isEmpty ? nil : $0 }
        // Ghostty's bar is thin, so falling back to it looks the same here.
        let style = ours["cursor-style"] ?? ghostty["cursor-style"].map { $0 == "bar" ? "bar_thin" : $0 }
        cursorStyleName = Self.cursorStyleNames.contains { $0.1 == style } ? style! : "block"
        cursorBlink = (ours["cursor-style-blink"] ?? ghostty["cursor-style-blink"]) != "false"
        // Off unless set here: Ghostty's own default (on) isn't followed.
        copyOnSelect = ["true", "clipboard"].contains(ours["copy-on-select"] ?? "")
        pasteProtection = (ours["clipboard-paste-protection"] ?? ghostty["clipboard-paste-protection"]) != "false"
        pasteBracketedSafe = (ours["clipboard-paste-bracketed-safe"] ?? ghostty["clipboard-paste-bracketed-safe"]) != "false"
        windowThemeName = ours["window-theme"].flatMap { name in Self.windowThemeNames.first { $0.caseInsensitiveCompare(name) == .orderedSame } } ?? WindowTheme.native
        sidebarColor = WindowTheme.colorPair(ours["sidebar-color"])
        toolbarColor = WindowTheme.colorPair(ours["toolbar-color"])
        sidebarOpacity = min(max(ours["sidebar-opacity"].flatMap(Double.init) ?? 0, 0), 1)
        savedSidebarOpacity = sidebarOpacity
        simulatorShutdown = ours["simulator-shutdown"] != "false"
        browserNewTabExternal = ours["browser-new-tab"] != "hammock"
        sshAgentForwarding = ours["ssh-agent-forwarding"] != "false"
        screenSharing = ours["screen-sharing"] == "true"
        usageLive = ours["usage-live"] == "true"
        claudeUsageLive = ours["claude-usage-live"] == "true"
        usageLiveAgents = ours["usage-live-agents"]
        menuBar = ours["menu-bar"] == "active" ? "active" : "always"
        keepEmptyGroups = ours["keep-empty-groups"] != "false"
        tabStatus = ours["tab-status"].flatMap(TabStatus.init(rawValue:)) ?? .icon
        groupHeadings = ours["group-headings"].flatMap(GroupHeadings.init(rawValue:)) ?? .prominent
        groupIcons = ours["group-icons"] != "false"
        // "comfortable": Spacious's name before 0.3.1.
        paneSpacing = ours["pane-spacing"].flatMap { PaneSpacing(rawValue: $0 == "comfortable" ? "spacious" : $0) } ?? .compact
        dimInactivePanes = ours["dim-inactive-panes"] == "true"
        paneAnimations = ours["pane-animations"] != "false"
        tabCloseButton = ours["tab-close-button"] != "false"
        activePaneBorder = ours["active-pane-border"] == "true"
        activePaneBorderColorValue = ours["active-pane-border-color"].flatMap { $0.isEmpty || $0 == "accent" ? nil : $0 }
        activePaneBorderColor = WindowTheme.colorPair(activePaneBorderColorValue)
        if let t = appTheme {
            // The theme's colours; the individual keys wait for Custom.
            lightTheme = t.light
            darkTheme = t.dark
            activePaneBorderColor = t.border.map(Self.nsPair)
            sidebarColor = nil
            toolbarColor = nil
        }
        browserOpenSchemes = (ours["browser-open-schemes"] ?? "").split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespaces).lowercased() }.filter { !$0.isEmpty }
        let binds = KeybindSet.parse(Self.readAll(fileURL, key: "keybind"))
        // Only on a change: menus rebuild for every read of it.
        if binds.set != keybinds { keybinds = binds.set }
        if binds.problems != keybindProblems { keybindProblems = binds.problems }
        ProgramIcon.setOverrides(Self.readAll(fileURL, key: "pane-icon"))
        programIconStyle = ProgramIconStyle(config: ours["program-icons"])
        ProgramIcon.setStyle(programIconStyle)
        agentColors = resolveAgentColors()
        Prefs.shared.useConfig(ours, fallbacks: prefFallbacks) { [weak self] in self?.set($0) }
        stamp = watchedStamps()
        applyAppearance()
        NotificationCenter.default.post(name: Self.changed, object: nil)
        // Once a launch: if the file can't be written, the next load would
        // find it from before themes again, and again.
        if !migrationTried, fromBefore || !Self.legacyAgentColor.isEmpty {
            migrationTried = true
            migrateToThemes(agentColorSet: ours["agent-color"]?.isEmpty == false)
        }
        if !prefsMigrationTried {
            prefsMigrationTried = true
            migratePrefs(saved)
        }
        migrateSoundSwitch()
    }

    /// `notify-sound = false` (Settings' "Play a sound with notifications",
    /// before each kind had its own sound): every kind's sound none, unless
    /// the file picks one, and the switch gone, from UserDefaults too (from
    /// before 0.3.1: migratePrefs would leave it there, as the file doesn't
    /// keep it, and it would come back every launch).
    private func migrateSoundSwitch() {
        let file = Self.read(fileURL)
        // Once a launch: if the file can't be written, the next load would find it again, and again.
        guard !soundSwitchMigrationTried, let v = file[NotifyPrefs.sound.key] else { return }
        soundSwitchMigrationTried = true
        var changes: [(String, String?)] = [(NotifyPrefs.sound.key, nil)]
        if v == "false" {
            changes += NotifyPrefs.sounds.filter { file[$0.key] == nil }.map { ($0.key, AlertSound.none) }
        }
        UserDefaults.standard.removeObject(forKey: NotifyPrefs.sound.defaultsKey)
        set(changes)
    }

    @ObservationIgnored private var prefsMigrationTried = false
    @ObservationIgnored private var soundSwitchMigrationTried = false

    // MARK: Settings that were in UserDefaults (Prefs)

    /// Fallbacks that follow another setting: the agent view's code font
    /// is the terminal's unless chosen.
    private var prefFallbacks: [String: String] { [Pref.agentCodeFont.key: fontFamily ?? ""] }

    /// Settings kept in UserDefaults before 0.3.1 move into the file, once:
    /// those you'd changed, unless the file already sets them. Only what
    /// this app saved counts (not registered defaults), and each is removed
    /// from UserDefaults once the file has it.
    private func migratePrefs(_ file: [String: String]) {
        guard let id = Bundle.main.bundleIdentifier,
              let saved = UserDefaults.standard.persistentDomain(forName: id) else { return }
        let fallbacks = prefFallbacks
        var changes: [(String, String?)] = []
        var found: [Pref] = []
        for p in Pref.shared + Pref.mac {
            guard let v = saved[p.defaultsKey] else { continue }
            found.append(p)
            if file[p.key] == nil, let s = p.configValue(fromDefaults: v), s != (fallbacks[p.key] ?? p.fallback) {
                changes.append((p.key, s))
            }
        }
        guard !found.isEmpty else { return }
        if !changes.isEmpty { set(changes) }
        let now = Self.read(fileURL)
        for p in found where now[p.key] != nil || !changes.contains(where: { $0.0 == p.key }) {
            UserDefaults.standard.removeObject(forKey: p.defaultsKey)
        }
    }

    @ObservationIgnored private var migrationTried = false
    /// Any of the Custom keys has a value in the file (kept, unused, while a
    /// curated theme's in use, to come back when you choose Custom again).
    @ObservationIgnored private var hasCustomValues = false

    // MARK: Themes (app-theme)

    /// The keys a Custom theme is made of. Any of them set before themes
    /// existed makes it Custom, so nobody's colours change.
    static let customKeys = ["theme", "window-theme", "agent-color", "active-pane-border-color", "sidebar-color", "toolbar-color"]

    /// The agent colour Settings ▸ Agent View kept in UserDefaults before
    /// themes; it's moved to `agent-color`.
    private static var legacyAgentColor: String { UserDefaults.standard.string(forKey: AgentViewPrefs.color) ?? "" }

    /// From before themes: Custom, written down, with the agent colour moved
    /// into the config file (where Custom keeps it, and the agent view's
    /// own pref would otherwise override every theme's).
    private func migrateToThemes(agentColorSet: Bool) {
        let legacy = Self.legacyAgentColor
        UserDefaults.standard.removeObject(forKey: AgentViewPrefs.color)
        var changes: [(String, String?)] = []
        if isCustomTheme { changes.append(("app-theme", AppTheme.custom)) }
        if !legacy.isEmpty, !agentColorSet, isCustomTheme { changes.append(("agent-color", legacy)) }
        if !changes.isEmpty { set(changes) }
    }

    /// Picks a theme by name, or Custom: your Custom colours from before if
    /// the file still has them, else each starting from the theme you had,
    /// so nothing changes until you change it. (`hmk theme custom` does the same.)
    func setAppTheme(_ name: String) {
        guard AppTheme.named(name) == nil else { return set([("app-theme", AppTheme.named(name)!.name)]) }
        guard let t = appTheme, !hasCustomValues else { return set([("app-theme", AppTheme.custom)]) }
        let native = t.name == AppTheme.native
        let window = native ? nil : t.ownTint ? t.name : WindowTheme.matchTerminal
        let builtIn = t.lightTerminalName == TerminalTheme.builtInLight && t.darkTerminalName == TerminalTheme.builtInDark
        set([("app-theme", AppTheme.custom),
             ("window-theme", window),
             ("theme", builtIn ? nil : "dark:\(t.darkTerminalName),light:\(t.lightTerminalName)"),
             ("agent-color", native ? nil : t.name),
             ("active-pane-border-color", t.border.map(AppTheme.format))])
    }

    /// Custom's agent colour: nil follows the window theme.
    func setAgentColor(_ value: String?) { set("agent-color", value.flatMap { $0.isEmpty ? nil : $0 }) }

    /// The agent view's colour (buttons, links) and your messages' (deep
    /// enough for white text), from the theme, or with Custom `agent-color`
    /// and else the window theme's.
    private(set) var agentColors: (color: Color, bubble: Color) = (.accentColor, .accentColor)

    /// Made once a load, so views comparing them see the same colours.
    private func resolveAgentColors() -> (color: Color, bubble: Color) {
        if let t = appTheme { return (t.accent?.color ?? .accentColor, t.bubble?.color ?? .accentColor) }
        return Self.agentColors(agentColorValue) ?? Self.agentColors(windowThemeName) ?? (.accentColor, .accentColor)
    }

    /// An `agent-color` value's colours: an AgentColor's, a theme's, or a
    /// colour given (its light one is your messages', as AgentColor's are).
    static func agentColors(_ value: String?) -> (color: Color, bubble: Color)? {
        guard let value, !value.isEmpty else { return nil }
        if let c = AgentColor.named(value), let b = AgentColor.bubble(value) { return (c, b) }
        if let t = AppTheme.named(value), let a = t.accent, let b = t.bubble { return (a.color, b.color) }
        if let p = AppTheme.parsePair(value) { return (p.color, AppTheme.color(p.light)) }
        return nil
    }

    static func nsPair(_ p: AppTheme.Pair) -> (light: NSColor, dark: NSColor) { (nsColor(p.light), nsColor(p.dark)) }
    static func nsColor(_ v: UInt32) -> NSColor {
        NSColor(srgbRed: CGFloat((v >> 16) & 0xFF) / 255, green: CGFloat((v >> 8) & 0xFF) / 255, blue: CGFloat(v & 0xFF) / 255, alpha: 1)
    }

    /// Ghostty's cursor-style names.
    static let cursorStyleNames: [(CursorStyle, String, String)] = [
        (.block, "block", "Block"), (.bar, "bar", "Bar"), (.bar, "bar_thin", "Thin Bar"), (.underline, "underline", "Underline"),
        (.hollow, "block_hollow", "Hollow Block"),
    ]

    static func cursorStyle(_ value: String?) -> CursorStyle {
        cursorStyleNames.first { $0.1 == value }?.0 ?? .block
    }

    func setCursorStyle(_ name: String) { set("cursor-style", name) }

    /// Sets a key that falls back to Ghostty's config, nil being Hammock's
    /// default. That's written out as `hammockDefault` while Ghostty's config
    /// sets the key, since removing it would bring Ghostty's value back.
    func setOverGhostty(_ key: String, _ value: String?, hammockDefault: String) {
        set(key, value ?? (ghosttyKeys.contains(key) ? hammockDefault : nil))
    }

    /// `dark:A,light:B` (either order) or a single name for both.
    static func themePair(_ value: String?) -> (light: String, dark: String) {
        guard let value, !value.isEmpty else { return (TerminalTheme.builtInLight, TerminalTheme.builtInDark) }
        var light: String?, dark: String?
        for part in value.split(separator: ",").map({ $0.trimmingCharacters(in: .whitespaces) }) {
            if part.hasPrefix("light:") { light = String(part.dropFirst(6)) } else if part.hasPrefix("dark:") { dark = String(part.dropFirst(5)) } else { light = part; dark = part }
        }
        return (light ?? TerminalTheme.builtInLight, dark ?? TerminalTheme.builtInDark)
    }

    func applyAppearance() {
        switch appearance {
        case .auto: NSApp?.appearance = nil
        case .light: NSApp?.appearance = NSAppearance(named: .aqua)
        case .dark: NSApp?.appearance = NSAppearance(named: .darkAqua)
        }
    }

    private func watchedStamps() -> [Date?] {
        var urls = [fileURL] + ghosttyConfigs
        if appTheme == nil, let u = themeFile(lightThemeName) { urls.append(u) }
        if appTheme == nil, let u = themeFile(darkThemeName) { urls.append(u) }
        return urls.map { (try? FileManager.default.attributesOfItem(atPath: $0.path)[.modificationDate]) as? Date }
    }

    private func reloadIfChanged() {
        if watchedStamps() != stamp { load() }
    }

    // MARK: Window colours

    static let windowThemeNames = WindowTheme.builtIn.map(\.name) + [WindowTheme.matchTerminal]

    /// Every window theme, as Settings shows them.
    var windowThemes: [WindowTheme] { WindowTheme.builtIn + [WindowTheme.matching(light: lightTheme, dark: darkTheme)] }

    var windowTheme: WindowTheme {
        if let t = appTheme {
            return WindowTheme(name: t.name, sidebar: t.sidebar.map(Self.nsPair), toolbar: t.toolbar.map(Self.nsPair), mark: t.accent.map { Self.nsColor($0.light) })
        }
        return windowThemes.first { $0.name == windowThemeName } ?? WindowTheme.builtIn[0]
    }

    /// The sidebar's tint and how opaque to lay it over the glass, if any.
    /// A theme's colour shows even at its glassiest (as tinted glass);
    /// Native's only comes in as you make the sidebar more opaque.
    func sidebarTint(dark: Bool) -> (color: NSColor, alpha: Double)? { sidebarTint(dark: dark, opacity: sidebarOpacity) }

    func sidebarTint(dark: Bool, opacity: Double) -> (color: NSColor, alpha: Double)? {
        if let c = sidebarColor ?? windowTheme.sidebar {
            return (dark ? c.dark : c.light, 0.55 + 0.45 * opacity)
        }
        guard opacity > 0 else { return nil }
        return (.windowBackgroundColor, opacity)
    }

    /// The theme's toolbar colour, if any: toasts' and notification cards' surface.
    func toolbarTint(dark: Bool) -> NSColor? {
        (toolbarColor ?? windowTheme.toolbar).map { dark ? $0.dark : $0.light }
    }

    func setWindowTheme(_ name: String) { set("window-theme", name == WindowTheme.native ? nil : name) }

    /// While the slider moves: shown, not yet written.
    func previewSidebarOpacity(_ v: Double) { sidebarOpacity = v }

    func setSidebarOpacity(_ v: Double) { set("sidebar-opacity", v <= 0 ? nil : String(format: "%.2f", v)) }

    // MARK: Writing

    /// Sets (or with nil removes) a key, keeping the rest of the file as it is.
    func set(_ key: String, _ value: String?) { set(key, values: value.map { [$0] } ?? []) }

    /// Sets a repeatable key's lines (all of them; none removes it), where
    /// its first one was, keeping the rest of the file as it is.
    func set(_ key: String, values: [String]) { set([(key, values)]) }

    /// Sets (or with nil removes) several keys at once, with one reload.
    func set(_ changes: [(String, String?)]) { set(changes.map { ($0.0, $0.1.map { [$0] } ?? []) }) }

    private func set(_ changes: [(String, [String])]) {
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        var lines = (try? String(contentsOf: fileURL, encoding: .utf8))?.components(separatedBy: "\n") ?? [Self.header]
        for (key, values) in changes { Self.replace(key, values, in: &lines) }
        // Through a symlink (dotfiles), so it stays one.
        try? lines.joined(separator: "\n").write(to: fileURL.resolvingSymlinksInPath(), atomically: true, encoding: .utf8)
        // The daemon warns about security settings loosened in the file by
        // anything else (configwatch.go): these were you, in Settings.
        let security = changes.filter { Self.securityKeys.contains($0.0) }
        if !security.isEmpty { AppModelRegistry.shared?.noteConfigWritten(security.map { (key: $0.0, values: $0.1) }) }
        load()
    }

    /// The keys the daemon watches for being loosened behind your back.
    private static let securityKeys: Set<String> = [
        "screen-sharing", "remote-network", "ssh-upstream-agent", "ssh-agent-forwarding",
        "browser-open-schemes", "clipboard-paste-protection",
    ]

    private static func replace(_ key: String, _ values: [String], in lines: inout [String]) {
        let isKey: (String) -> Bool = { line in
            let t = line.trimmingCharacters(in: .whitespaces)
            guard !t.hasPrefix("#"), let eq = t.firstIndex(of: "=") else { return false }
            return t[..<eq].trimmingCharacters(in: .whitespaces) == key
        }
        let newLines = values.map { "\(key) = \($0)" }
        let matches = lines.indices.filter { isKey(lines[$0]) }
        if let first = matches.first {
            for i in matches.reversed() { lines.remove(at: i) }
            lines.insert(contentsOf: newLines, at: first)
        } else if lines.last == "" {
            lines.insert(contentsOf: newLines, at: lines.count - 1)
        } else {
            lines.append(contentsOf: newLines)
        }
    }

    /// Keeps usage-live-agents to the agents whose limits the sidebar shows
    /// (Settings ▸ Agents), now and whenever they change, so turning on
    /// usage-live never sends an agent's sign-in to its service unless its
    /// limits are shown.
    func followUsageChoice() {
        let write = { (ids: [String]) in
            let value = ids.joined(separator: ", ")
            if AppConfig.shared.usageLiveAgents != value { AppConfig.shared.set("usage-live-agents", value) }
        }
        UsageLimitsPrefs.onChosenChange = write
        write(UsageLimitsPrefs.chosenIDs)
    }

    func setAppearance(_ a: Appearance) { set("appearance", a == .auto ? nil : a.rawValue) }
    func setTabStatus(_ s: TabStatus) { set("tab-status", s == .icon ? nil : s.rawValue) }
    func setGroupHeadings(_ h: GroupHeadings) { set("group-headings", h == .prominent ? nil : h.rawValue) }
    func setPaneSpacing(_ s: PaneSpacing) { set("pane-spacing", s == .compact ? nil : s.rawValue) }
    /// The focused pane's border colour: nil is the accent colour.
    func setActivePaneBorderColor(_ c: NSColor?) {
        guard let c = c?.usingColorSpace(.sRGB) else { return set("active-pane-border-color", nil) }
        let hex = String(format: "#%02X%02X%02X", Int((c.redComponent * 255).rounded()), Int((c.greenComponent * 255).rounded()), Int((c.blueComponent * 255).rounded()))
        set("active-pane-border-color", hex)
    }
    func setBrowserOpenSchemes(_ schemes: [String]) {
        set("browser-open-schemes", schemes.isEmpty ? nil : schemes.joined(separator: ","))
    }

    /// Writes the `keybind` lines that make these shortcuts (none: the defaults).
    /// Lines that couldn't be read are kept, after the rest, to be fixed by hand.
    func setKeybinds(_ binds: KeybindSet) { set("keybind", values: binds.configValues + keybindProblems.map(\.line)) }

    func setThemes(light: String, dark: String) {
        let builtIn = light == TerminalTheme.builtInLight && dark == TerminalTheme.builtInDark
        set("theme", builtIn ? nil : "dark:\(dark),light:\(light)")
    }

    /// Creates the config file (with a commented header) so it can be opened.
    func ensureFile() {
        guard !FileManager.default.fileExists(atPath: fileURL.path) else { return }
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        try? (Self.header + "\n").write(to: fileURL, atomically: true, encoding: .utf8)
    }

    private static let header = """
        # Hammock config (Ghostty syntax). Changes apply automatically.
        #   appearance = auto | light | dark
        #   app-theme = Hammock            (Hammock, Native, Graphite, Ocean, Forest, Rose, Lavender, Sand, Catppuccin,
        #                                   Tokyo Night, Gruvbox, Nord, Rosé Pine, Solarized; or custom: the theme, window-theme,
        #                                   agent-color, active-pane-border-color, sidebar-color and toolbar-color keys apply)
        #   theme = dark:NAME,light:NAME   (Ghostty theme files; see Settings ▸ Appearance)
        #   font-family = NAME, font-size = 15, font-thicken = false
        #   terminal-renderer = metal      (or coretext: CoreGraphics, as before Metal; for terminal panes opened after it's set)
        #   adjust-cell-height = 20%       (terminal line spacing: a percentage of the line height, or points; negative: tighter)
        #   cursor-style = block | bar | bar_thin | underline | block_hollow, cursor-style-blink = true
        #   copy-on-select = false
        #   clipboard-paste-protection = true   (ask before a paste that could run commands)
        #   clipboard-paste-bracketed-safe = true   (no need to ask when the program takes bracketed pastes)
        #   window-theme = Native | Graphite | Hammock | Ocean | Forest | Rose | Lavender | Sand | Match Terminal
        #   agent-color = Ocean            (the agent view's colour: a colour or theme name, or #RRGGBB, light:#…,dark:#…)
        #   sidebar-color = #RRGGBB, toolbar-color = #RRGGBB   (or light:#…,dark:#…; override the theme)
        #   sidebar-opacity = 0 … 1        (0 glassy, 1 opaque)
        #   group-headings = prominent | subtle   (subtle: Group headings in the sidebar smaller and dimmer than its tabs)
        #   group-icons = true             (false: no icons on Group headings in the sidebar)
        #   tab-status = icon | trailing   (agent status in a sidebar row's icon, or at its end with the agent's logo as the icon)
        #   pane-spacing = compact | spacious   (spacious: split panes spaced apart, with rounded corners)
        #   dim-inactive-panes = false     (true: dim the panes you're not typing in, a little)
        #   active-pane-border = false     (true: a border around the focused pane)
        #   active-pane-border-color = accent   (or #RRGGBB, or light:#…,dark:#…)
        #   pane-animations = true         (false: panes open, close, zoom and float at once)
        #   simulator-shutdown = true      (simulators Hammock booted shut down a minute after their last pane closes)
        #   agent-presence = true          (show agents acting in browser and simulator panes: a cursor, a glow and a badge)
        #   ssh-agent-forwarding = true    (panes on your linked computers use the SSH keys of the one you're at)
        #   ssh-upstream-agent = PATH      (the SSH agent Hammock's passes requests on to; default: ~/.ssh/config's IdentityAgent for every host, else SSH_AUTH_SOCK)
        #   screen-sharing = false         (your iPhone and other Macs may see and control this Mac's screen)
        #   remote-network = tailscale     (one line per network remote access listens on: tailscale, or a subnet like 192.168.1.0/24; none set: Tailscale only)
        #   remote-address = HOST[:PORT]   (one line per extra address given out in pairing codes and links: a DNS name, a VPN endpoint, a forwarded port)
        #   usage-live = false             (agents' usage limits asked of each agent's own service every 5 minutes, with the sign-in its CLI keeps here)
        #   usage-live-agents = claude, codex   (the agents usage-live asks; Settings keeps it to those the sidebar shows)
        #   menu-bar = always | active     (the menu bar item; active: only while a screen's shared or the server's down)
        #   keep-empty-groups = true       (⌘W on a Group's last tab keeps the Group; false: closes it too)
        #   notify-needs-input = true      (notify me when an agent waits for my input; and notify-permission, notify-finished, notify-error; notify-review-replies = false: an agent answering my diff comments)
        #   notify-style = auto | system | in-app   (auto: in the window while it's in front, else as macOS notifications)
        #   notify-current-tab = false, dock-badge = true   (current tab: also for the tab I'm looking at)
        #   notify-sound-needs-input = Hammock Chime   (each kind's sound; and notify-sound-permission = Hammock Chime, notify-sound-finished = Hammock Kalimba, notify-sound-error = Hammock Low, notify-sound-other = Hammock Kalimba. Also Hammock Marimba, Hammock Bell, a macOS sound like Glass, or none)
        #   sidebar-layout = vertical | compact | horizontal   (horizontal: the Group's tabs in a tab bar along the top)
        #   sidebar-finished = true        (finished agents get their own list at the bottom of the sidebar)
        #   collapsed-group-agents = false (true: a collapsed Group still shows its tabs with an active agent)
        #   confirm-close-busy-agent = true   (ask before closing a pane or tab whose agent is at work)
        #   simulator-frame = true         (simulator panes draw the device around the screen)
        #   group-sharing = false          (true: Share Group in the toolbar, menus and palette, to share Groups with other people)
        #   agent-default-view = terminal | agent   (which view agent panes open in)
        #   agent-font-family = NAME, agent-font-size = 14, agent-line-spacing = 3   (the agent view's text; unset: the system font)
        #   agent-code-font-family = NAME, agent-code-font-size = 13   (its code; unset: the terminal's font)
        #   agent-code-wrap = prose | always | never   (which code blocks wrap long lines; prose: Markdown and text)
        #   agent-image-size = large, agent-image-size-remote = medium   (pictures attached in the agent view: original | large | medium | small; remote: on a linked computer)
        #   usage-limits-claude = false, usage-limits-codex = false   (an agent's usage limits in the sidebar)
        #   browser-open-schemes = obsidian,zoommtg   (links a browser pane opens in their app without asking)
        #   browser-new-tab = external | hammock   (links a page opens in a new tab: your default browser, or a Hammock browser tab)
        #   keybind = cmd+shift+b=new_browser_tab   (one line per shortcut; Settings ▸ Keyboard lists every action's name)
        #   keybind = cmd+j=unbind, keybind = none=new_tab, keybind = ctrl+cmd+digits=select_tab (⌃⌘1…9), keybind = clear
        #   tui-animations = true          (hmk tui: false snaps instead of animating)
        #   tui-nerd-font = false          (hmk tui: true shows program icons as Nerd Font glyphs)
        #   tab-close-button = true        (false: no close button on a tab you hover over, in the sidebar or the tab bar)
        #   program-icons = plain | colorful | off   (a terminal's program: its icon, on a squircle in its colours, or none; agents keep their logo, on a squircle when colorful)
        #   pane-icon = btop=monitor       (a program's icon: a built-in one, like vim, git, python, monitor, or an SF Symbol; several with commas, or repeat the key)
        #   editor-wrap = false            (editor panes: wrap long lines instead of scrolling sideways)
        #   editor-line-numbers = true     (editor panes: line numbers in the gutter)
        #   editor-tab-width = 4, editor-indent = spaces | tabs   (what Tab inserts; a file's .editorconfig beats them)
        #   editor-font-size = 13          (editor panes on the Mac and iPhone; unset: the terminal's font size)
        #   editor-vim = false             (editor panes on the Mac and iPhone: Vim keys)
        #   editor-tui-external = false    (hmk tui: open editor panes in $VISUAL or $EDITOR; edits from your other devices show there only when it reloads the file)
        #   diff-style = unified | split   (diff panes: old and new in one column, or side by side; a pane's own choice beats it)
        #   diff-whitespace = show | ignore   (diff panes: ignore lines that differ only in their spaces, as git diff -w)
        #   diff-wrap = false              (diff panes: wrap long lines instead of scrolling sideways)
        #   browser-enabled = true, simulator-enabled = true, editor-enabled = true, diff-enabled = true, log-enabled = true
        #                                  (false: Hammock doesn't offer that tool, in its menus or to agents)
        """
}

extension Color {
    /// The theme's colour (the agent view's): for highlights we draw
    /// ourselves; the system's accent with Native.
    @MainActor static var themeAccent: Color { AppConfig.shared.agentColors.color }
}

extension View {
    /// Controls in the theme's colour: buttons, switches, segments, sliders.
    @MainActor func themeTint() -> some View { tint(AppConfig.shared.agentColors.color) }
}
