import AppKit
import HammockKit
import SwiftUI

// Settings, laid out like System Settings: panes in a sidebar with coloured
// icons, a header at the top of each, Back and Forward, and a search that
// finds single settings (SettingsIndex) and jumps to them.

extension SettingsTab: Identifiable {
    var id: String { rawValue }

    /// The sidebar, in its groups.
    static let sidebar: [[SettingsTab]] = [
        [.general, .appearance, .terminal, .tools, .editor, .keyboard],
        [.agents, .agentView, .notifications],
        [.sharing, .devices, .ssh],
    ]

    var title: String {
        switch self {
        case .general: "General"
        case .appearance: "Appearance"
        case .terminal: "Terminal"
        case .tools: "Tools"
        case .editor: "Editor"
        case .keyboard: "Keyboard"
        case .agents: "Agents"
        case .agentView: "Agent View"
        case .notifications: "Notifications"
        case .sharing: "Sharing"
        case .devices: "Devices"
        case .ssh: "SSH"
        case .about: "About"
        }
    }

    var symbol: String {
        switch self {
        case .general: "gearshape.fill"
        case .appearance: "circle.lefthalf.filled"
        case .terminal: "apple.terminal.fill"
        case .tools: "wrench.and.screwdriver.fill"
        case .editor: "doc.text.fill"
        case .keyboard: "keyboard.fill"
        case .agents: "sparkles"
        case .agentView: "text.bubble.fill"
        case .notifications: "bell.badge.fill"
        case .sharing: "person.2.fill"
        case .devices: "iphone"
        case .ssh: "key.fill"
        case .about: "info.circle.fill"
        }
    }

    var tint: Color {
        switch self {
        case .general, .keyboard: Color(white: 0.55)
        case .appearance: Color(white: 0.12)
        case .terminal: Color(red: 0.15, green: 0.19, blue: 0.16)
        case .tools: .cyan
        case .editor: .indigo
        case .agents: .orange
        case .agentView: .purple
        case .notifications: .red
        case .sharing: .green
        case .devices: .blue
        case .ssh: .teal
        case .about: .blue
        }
    }

    /// The sentence under the pane's header.
    var blurb: String {
        switch self {
        case .general: "The server, the menu bar, Groups, the hmk command and links to other apps."
        case .appearance: "Light and dark, your theme, and how the window, tabs, panes and cursor look."
        case .terminal: "The terminal's font, copying and pasting, and images programs show."
        case .tools: "What Hammock can open beside your terminals. If you already do one of these your own way, turn it off and neither Hammock nor its agents will offer it."
        case .editor: "Editor and diff panes: wrapping, line numbers, indents, font, Vim keys and how diffs are laid out."
        case .keyboard: "Change any shortcut. Search by name or keys."
        case .agents: "Closing agents at work, putting idle ones to sleep, usage limits in the sidebar, and Hammock's hooks and tools for agents."
        case .agentView: "How agent panes open, and the agent view's colour and fonts."
        case .notifications: "Hear about agents that need you, in the Hammock window or as macOS notifications."
        case .sharing: "Your name and picture, and the Groups you share with other people."
        case .devices: "Your other computers, the iPhones paired with this Mac, remote access and screen sharing."
        case .ssh: "Your Hammock Key, and lending SSH keys between your computers."
        case .about: "Its version, updates, quitting with the server, uninstalling, and the software Hammock includes."
        }
    }
}

/// A pane's icon: its symbol on a rounded square of its colour.
struct SettingsIcon: View {
    let tab: SettingsTab
    var size: CGFloat = 20

    var body: some View {
        Image(systemName: tab.symbol)
            // Room around the symbol, as System Settings' icons have.
            .font(.system(size: size * 0.44, weight: .semibold))
            .foregroundStyle(.white)
            .frame(width: size, height: size)
            .background(RoundedRectangle(cornerRadius: size * 0.24, style: .continuous).fill(tab.tint.gradient))
            .accessibilityHidden(true)
    }
}

/// The top of a pane: its icon, name and what's in it.
struct SettingsPaneHeader: View {
    let tab: SettingsTab

    var body: some View {
        Section {
            VStack(spacing: 6) {
                SettingsIcon(tab: tab, size: 52)
                Text(tab.title).font(.title3.bold()).padding(.top, 4)
                Text(tab.blurb)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 8)
            .padding(.horizontal, 24)
        }
    }
}

/// The Settings window drawn as System Settings is: one toolbar row (Back,
/// Forward and the pane's title, left beside the sidebar) and the sidebar
/// running up to the top, under the window's buttons. The Settings scene
/// gives its window the preferences style (a centred title over a row of
/// toolbar items) and ignores windowToolbarStyle, so this sets it on the window.
struct SettingsWindowStyle: NSViewRepresentable {
    func makeNSView(context: Context) -> NSView { Probe() }
    func updateNSView(_ nsView: NSView, context: Context) {}

    private final class Probe: NSView {
        override func viewDidMoveToWindow() {
            super.viewDidMoveToWindow()
            guard let window else { return }
            window.styleMask.insert(.fullSizeContentView)
            window.toolbarStyle = .unified
            window.titleVisibility = .visible
            window.titlebarSeparatorStyle = .automatic
        }
    }
}

/// Settings' switches in the theme's colour. The window itself takes the
/// system's accent, so buttons and pop-up menus look as System Settings'
/// do; switches and choices (radio buttons, segments: `.themeTint()` on
/// each) keep Hammock's colour.
struct ThemedSwitchStyle: ToggleStyle {
    func makeBody(configuration: Configuration) -> some View {
        Toggle(configuration)
            .toggleStyle(.switch)
            .themeTint()
    }
}

/// Where Settings is: Back and Forward, and the setting search jumped to.
@MainActor @Observable
final class SettingsNav {
    static let shared = SettingsNav()

    /// The row a search jumped to: it scrolls into view and is ringed for a moment.
    private(set) var highlight: String?
    /// Set when a search picks a shortcut: Keyboard searches for it.
    var keyboardQuery: String?
    private(set) var back: [SettingsTab] = []
    private(set) var forward: [SettingsTab] = []
    /// A move made by Back or Forward, so it isn't recorded as a new step.
    private var stepping = false

    var current: SettingsTab { SettingsTab(rawValue: UserDefaults.standard.string(forKey: SettingsTab.key) ?? "") ?? .general }

    /// The pane changed (from the sidebar, a search or elsewhere): a step to go back from.
    func moved(from old: SettingsTab, to new: SettingsTab) {
        guard old != new else { return }
        if stepping { stepping = false; return }
        back.append(old)
        forward.removeAll()
    }

    func goBack() {
        guard let to = back.popLast() else { return }
        forward.append(current)
        stepping = true
        SettingsTab.select(to)
    }

    func goForward() {
        guard let to = forward.popLast() else { return }
        back.append(current)
        stepping = true
        SettingsTab.select(to)
    }

    /// Opens a search result's pane, scrolled to its row and ringed.
    func reveal(_ entry: SettingsEntry) {
        if let query = entry.keyboardQuery { keyboardQuery = query }
        SettingsTab.select(entry.tab)
        highlight = entry.anchor
        let anchor = entry.anchor
        Task {
            try? await Task.sleep(for: .seconds(2.5))
            if highlight == anchor { highlight = nil }
        }
    }
}

extension View {
    /// A row a search can jump to: scrolled into view and ringed when it does.
    func settingAnchor(_ id: String) -> some View { modifier(SettingAnchor(id: id)) }

    /// A pane's Form: scrolls to the row a search jumped to.
    func settingsScrollTarget() -> some View { modifier(SettingsScroll()) }
}

private struct SettingAnchor: ViewModifier {
    let id: String

    func body(content: Content) -> some View {
        let on = SettingsNav.shared.highlight == id
        content
            .background {
                RoundedRectangle(cornerRadius: 7, style: .continuous)
                    .fill(Color.accentColor.opacity(on ? 0.12 : 0))
                    .overlay(RoundedRectangle(cornerRadius: 7, style: .continuous).strokeBorder(Color.accentColor.opacity(on ? 1 : 0), lineWidth: 2))
                    .padding(.horizontal, -8)
                    .padding(.vertical, -5)
                    .allowsHitTesting(false)
            }
            .animation(.easeOut(duration: 0.35), value: on)
            .id(id)
    }
}

private struct SettingsScroll: ViewModifier {
    func body(content: Content) -> some View {
        ScrollViewReader { proxy in
            content.onChange(of: SettingsNav.shared.highlight, initial: true) { _, id in
                guard let id else { return }
                // After the pane's first layout, when it has just opened.
                DispatchQueue.main.asyncAfter(deadline: .now() + 0.05) {
                    withAnimation { proxy.scrollTo(id, anchor: .center) }
                }
            }
        }
    }
}

/// One setting search can find.
struct SettingsEntry: Identifiable, Hashable {
    let tab: SettingsTab
    let section: String
    let label: String
    /// Its row's settingAnchor; nil opens the pane at the top.
    let anchor: String?
    /// Other words it's found by.
    var keywords = ""
    /// A shortcut: Keyboard searches for this.
    var keyboardQuery: String?

    var id: String { "\(tab.rawValue)/\(anchor ?? section)/\(label)" }
}

/// Every setting search finds, by pane and section. A setting added to a
/// pane gets a line here, and its row a matching settingAnchor.
enum SettingsIndex {
    static let entries: [SettingsEntry] = {
        func e(_ tab: SettingsTab, _ section: String, _ label: String, _ anchor: String?, _ keywords: String = "") -> SettingsEntry {
            SettingsEntry(tab: tab, section: section, label: label, anchor: anchor, keywords: keywords)
        }
        return [
            e(.general, "Server", "Start the server at login", "general.login", "daemon background start launch"),
            e(.general, "Menu Bar", "Show Hammock in the menu bar", "general.menubar", "status item icon"),
            e(.general, "Groups", "Keep a Group when its last tab closes", "general.keepgroups", "empty close"),
            e(.general, "Command-line tool", "Install the hmk command", "general.cli", "cli terminal path shell"),
            e(.general, "Privacy", "Full Disk Access for programs in panes", "general.fulldiskaccess", "privacy permission files 1password op tcc security"),
            e(.general, "Links", "Clicked links open in", "general.linkopen", "url click command ⌘ browser safari chrome pane tab hammock web link-open"),
            e(.general, "Links to other apps", "Links to other apps", "general.links", "browser url scheme open obsidian zoom"),
            e(.general, "Simulators", "Shut down simulators when their last pane closes", "general.simulators", "ios xcode iphone done"),
            e(.about, "Updates", "Check for updates daily", "about.updates", "sparkle version upgrade"),
            e(.about, "Updates", "Check for updates now", "about.checknow", "sparkle version upgrade new release"),
            e(.about, "Quitting", "Quit and stop the server", "about.quit", "daemon shut down exit"),
            e(.about, "Uninstall", "Uninstall Hammock", "about.uninstall", "remove delete trash bin data"),
            e(.about, "Acknowledgements", "Third-party software and its licences", "about.acknowledgements", "acknowledgements licences licenses open source credits notices copyright chromium"),
            e(.general, "Server", "Server status and state folder", "general.daemon", "daemon host connected"),

            e(.appearance, "Theme", "Appearance", "appearance.mode", "dark light mode automatic"),
            e(.appearance, "Theme", "Theme", "appearance.theme", "colours colors palette"),
            e(.appearance, "Window", "Sidebar glassy or opaque", "appearance.sidebar", "translucent transparency opacity"),
            e(.appearance, "Tabs", "Tab layout", "appearance.layout", "vertical horizontal tab bar sidebar"),
            e(.appearance, "Tabs", "Group headings", "appearance.headings", "prominent subtle"),
            e(.appearance, "Tabs", "Group icons", "appearance.groupicons"),
            e(.appearance, "Tabs", "Agent and program status", "appearance.tabstatus", "icon end of row osc 7501 progress"),
            e(.appearance, "Tabs", "Show finished agents", "appearance.finished", "done needs you"),
            e(.appearance, "Tabs", "Show active agents in collapsed Groups", "appearance.collapsed"),
            e(.appearance, "Tabs", "Close button on hover", "appearance.closebutton", "tab close x"),
            e(.appearance, "Tabs", "Program icons", "appearance.programicons", "vim git colorful plain"),
            e(.appearance, "Panes", "Pane spacing", "appearance.spacing", "compact spacious gap"),
            e(.appearance, "Panes", "Dim inactive panes", "appearance.dim", "fade focus"),
            e(.appearance, "Panes", "Border around the active pane", "appearance.border", "focus outline show"),
            e(.appearance, "Panes", "Pane animations", "appearance.paneanimations", "motion animate transitions instant zoom split close reduce"),
            e(.appearance, "Cursor", "Cursor shape", "appearance.cursor", "block bar underline caret"),
            e(.appearance, "Cursor", "Cursor blink", "appearance.blink", "caret"),
            e(.appearance, "Config", "Edit config file", "appearance.config", "themes folder ghostty"),

            e(.terminal, "Font", "Font", "terminal.font", "typeface family"),
            e(.terminal, "Font", "Font size", "terminal.size", "zoom text"),
            e(.terminal, "Font", "Line spacing", "terminal.linespacing", "cell height"),
            e(.terminal, "Font", "Thicken text", "terminal.thicken", "bold"),
            e(.terminal, "Text", "Copy text when selected", "terminal.copy", "clipboard selection"),
            e(.terminal, "Text", "Ask before pasting text that could run commands", "terminal.paste", "clipboard paste protection"),

            e(.tools, "Tools", "Tools", nil, "features turn off disable hide mcp agents"),
        ] + HammockTool.allCases.map { e(.tools, "Tools", $0.title, "tools.\($0.rawValue)", $0.keywords) } + [
            e(.tools, "Agents", "Show what agents do in browsers and simulators", "tools.agentpresence",
              "cursor pointer glow badge take over taps clicks mcp presence"),
            e(.editor, "Editor panes", "Wrap long lines", "editor.wrap", "word wrap"),
            e(.editor, "Editor panes", "Line numbers", "editor.linenumbers", "gutter"),
            e(.editor, "Editor panes", "Indent with spaces or tabs", "editor.indent", "tabs editorconfig"),
            e(.editor, "Editor panes", "Tab width", "editor.tabwidth", "indent size spaces editorconfig"),
            e(.editor, "Editor panes", "Editor font", "editor.font", "typeface family diff ligatures"),
            e(.editor, "Editor panes", "Editor font size", "editor.fontsize", "text"),
            e(.editor, "Editor panes", "Vim keys", "editor.vim", "vi modal"),
            e(.editor, "Diff panes", "Split or unified layout", "diff.style", "diff review changes side by side one column"),
            e(.editor, "Diff panes", "Ignore whitespace", "diff.whitespace", "diff review changes spaces indentation -w"),
            e(.editor, "Diff panes", "Wrap long lines", "diff.wrap", "diff review changes word wrap"),

            e(.agents, "Closing", "Warn before closing an agent that's at work", "agents.closeguard", "quit confirm"),
            e(.agents, "Idle Agents", "Put idle agents to sleep", "agents.sleep", "sleep suspend wake resume memory ram free idle hours agent-sleep"),
            e(.agents, "Usage Limits in the Sidebar", "Usage limits in the sidebar", "agents.usage", "claude codex gemini cursor copilot limit 5 hours week"),
            e(.agents, "Usage Limits in the Sidebar", "Ask each agent's service for its usage", "agents.usagelive", "claude.ai anthropic live limit sign-in"),
            e(.agents, "Hooks and tools", "Install hooks and tools", "agents.hooks", "mcp claude codex settings.json resume"),

            e(.agentView, "Agent view", "Agent panes open in", "agentview.default", "terminal agent view"),
            e(.agentView, "Colour", "Agent view colour", "agentview.colour", "color bubble"),
            e(.agentView, "Text", "Agent view font", "agentview.font", "text size line spacing"),
            e(.agentView, "Code", "Agent view code font", "agentview.codefont", "monospace"),
            e(.agentView, "Code", "Wrap long lines in code blocks", "agentview.codewrap", "wrap line wrapping markdown"),
            e(.agentView, "Attachments", "Picture size", "agentview.imagesize", "image screenshot attachment resize scale tokens"),
            e(.agentView, "Attachments", "Picture size on other computers", "agentview.imagesizeremote", "image screenshot attachment resize remote linked network tokens"),
            e(.agentView, "Dictation", "Dictation button", "agentview.dictation", "dictate microphone mic voice speech talk speak transcribe"),

            e(.notifications, "Notify me when an agent", "Is waiting for my input", "notify.input", "alert"),
            e(.notifications, "Notify me when an agent", "Asks for permission", "notify.grants", "alert approve"),
            e(.notifications, "Notify me when an agent", "Finishes", "notify.finished", "alert done"),
            e(.notifications, "Notify me when an agent", "Hits an error", "notify.errored", "alert fail"),
            e(.notifications, "Notify me when an agent", "Replies to my review comments", "notify.reviewReplies", "diff review comment thread answer"),
            e(.notifications, "Notify me when another program", "Is waiting for my input", "notify.program.input", "alert status osc 7501 brew script build deploy blocked"),
            e(.notifications, "Notify me when another program", "Finishes", "notify.program.finished", "alert done status osc 7501 brew script build deploy"),
            e(.notifications, "Notify me when another program", "Hits an error", "notify.program.errored", "alert fail status osc 7501 brew script build deploy"),
            e(.notifications, "How to notify me", "Show notifications", "notify.style", "in-app window banner macos"),
            e(.notifications, "How to notify me", "Also notify for the tab I'm looking at", "notify.visible", "alert"),
            e(.notifications, "How to notify me", "Show a count on the Dock icon", "notify.badge", "badge"),
            e(.notifications, "How to notify me", "Send a test notification", "notify.test"),
            e(.notifications, "Sounds", "Waiting for my input", "notify.sound.needsInput", "sound alert chime kalimba system none mute needs you"),
            e(.notifications, "Sounds", "Asking for permission", "notify.sound.permission", "sound alert chime kalimba system none mute approve"),
            e(.notifications, "Sounds", "Finished", "notify.sound.finished", "sound alert chime kalimba system none mute done"),
            e(.notifications, "Sounds", "Error", "notify.sound.error", "sound alert chime kalimba system none mute fail"),
            e(.notifications, "Sounds", "Other notifications", "notify.sound.other", "sound alert chime kalimba system none mute review join link"),

            e(.sharing, "You", "Your name and picture", nil, "avatar profile account"),
            e(.sharing, "Sharing", "Allow Group sharing", "sharing.groups", "share groups other people invite toolbar button"),
            e(.sharing, "Sharing", "Shared Groups", nil, "people guests"),

            e(.devices, "Your Computers", "Your computers", nil, "linked mac linux"),
            e(.devices, "Remote access", "Allow paired devices to connect remotely", "devices.remote", "iphone phone tailscale"),
            e(.devices, "Pair a Device", "Pair a device", nil, "iphone phone code qr"),
            e(.devices, "Paired Devices", "Paired devices", nil, "iphone phone revoke"),
            e(.devices, "Screen Sharing", "Screen sharing", nil, "watching screen vnc"),

            e(.ssh, "Hammock Key", "Hammock Key", nil, "secure enclave touch id signing github"),
            e(.ssh, "Your Computers", "Use the SSH keys of the computer you're at", "ssh.forwarding", "agent forwarding 1password"),
        ]
    }()

    /// The settings, then the keyboard shortcuts, matching every word of `query`.
    static func search(_ query: String) -> [SettingsEntry] {
        let words = query.lowercased().split(whereSeparator: \.isWhitespace).map(String.init)
        guard !words.isEmpty else { return [] }
        func matches(_ text: String) -> Bool { words.allSatisfy { text.contains($0) } }
        let settings = entries.filter { matches("\($0.label) \($0.section) \($0.tab.title) \($0.keywords)".lowercased()) }
        let shortcuts = Keybinds.actions.filter { matches("\($0.title) \($0.group) \($0.keywords)".lowercased()) }.map {
            SettingsEntry(tab: .keyboard, section: $0.group, label: $0.title, anchor: "key.\($0.id)", keyboardQuery: $0.title)
        }
        return settings + shortcuts
    }
}
