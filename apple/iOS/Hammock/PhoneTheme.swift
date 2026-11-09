import HammockKit
import SwiftUI

/// The iPhone's theme: the Mac's (Settings ▸ Appearance there, sent by its
/// daemon) unless Settings ▸ Theme here picks one of the curated ones. It
/// colours the app's tint, the agent view, and the terminal.
///
/// Two are equal when they come from the same place (a curated theme's name,
/// or what the Mac sent for its Custom), not by their colours: each is built
/// once and kept, so SwiftUI's onChange and the terminals only hear of a
/// real change.
struct PhoneTheme: Equatable, Sendable {
    /// Where a theme comes from: what makes it the same one.
    enum Source: Hashable, Sendable {
        case curated(String)
        case mac(Hammock_V1_HostTheme)
    }

    let source: Source
    /// What Settings shows: a curated theme's name, or "Custom" (the Mac's own).
    let name: String
    /// Buttons and the like, lighter in the dark; your messages' colour.
    let accent: Color
    let bubble: Color
    let light: TerminalTheme
    let dark: TerminalTheme
    /// Hammock's own green: the lock screen keeps its mesh.
    let isHammock: Bool

    static func == (a: PhoneTheme, b: PhoneTheme) -> Bool { a.source == b.source }

    func terminal(dark isDark: Bool) -> TerminalTheme { isDark ? dark : light }

    /// The terminal's background, in light and dark.
    var terminalBackground: Color { AppTheme.dynamic(AppTheme.hex(light.background), AppTheme.hex(dark.background)) }
    var terminalForeground: Color { AppTheme.dynamic(AppTheme.hex(light.foreground), AppTheme.hex(dark.foreground)) }

    /// UserDefaults: "" (the Mac's) or a curated theme's name.
    static let prefKey = "app.theme"
    /// The last theme a Mac sent, so the app opens in it before connecting.
    private static let lastMacKey = "app.theme.lastMac"

    /// Each curated theme, built once.
    private static let curatedThemes: [String: PhoneTheme] =
        Dictionary(uniqueKeysWithValues: AppTheme.all.map { ($0.name, PhoneTheme(curated: $0)) })

    static func curated(_ t: AppTheme) -> PhoneTheme { curatedThemes[t.name] ?? PhoneTheme(curated: t) }

    static let hammock = curated(AppTheme.named(AppTheme.defaultName)!)

    /// The theme in effect: the pick here, else the Mac's (or the last one
    /// a Mac sent), else Hammock's.
    @MainActor static func resolve(pref: String, host: Hammock_V1_HostTheme?) -> PhoneTheme {
        if !pref.isEmpty, let t = AppTheme.named(pref) { return curated(t) }
        guard let host = host ?? lastMac else { return hammock }
        return mac(host)
    }

    /// A Mac's theme, as it draws it.
    @MainActor static func mac(_ host: Hammock_V1_HostTheme) -> PhoneTheme {
        if host.name.caseInsensitiveCompare(AppTheme.custom) == .orderedSame { return custom(host) }
        return AppTheme.named(host.name).map(curated) ?? hammock
    }

    /// Remembers what the Mac sent, for the next launch.
    @MainActor static func remember(_ host: Hammock_V1_HostTheme?) {
        guard let host, host != lastMac, let data = try? host.serializedBytes() as Data else { return }
        UserDefaults.standard.set(data, forKey: lastMacKey)
        storedMac = host
    }

    @MainActor static var lastMac: Hammock_V1_HostTheme? {
        if let storedMac { return storedMac }
        storedMac = UserDefaults.standard.data(forKey: lastMacKey).flatMap { try? Hammock_V1_HostTheme(serializedBytes: $0) }
        return storedMac
    }

    /// lastMac, read once (the body asks often).
    @MainActor private static var storedMac: Hammock_V1_HostTheme?

    private init(source: Source, name: String, accent: Color, bubble: Color, light: TerminalTheme, dark: TerminalTheme, isHammock: Bool) {
        self.source = source; self.name = name; self.accent = accent; self.bubble = bubble
        self.light = light; self.dark = dark; self.isHammock = isHammock
    }

    /// A curated theme. Native and Hammock take the iPhone's own green (its
    /// AccentColor), as the app has always looked.
    private init(curated t: AppTheme) {
        let own = t.name == AppTheme.native || t.name == AppTheme.defaultName
        self.init(source: .curated(t.name), name: t.name,
                  accent: own ? Self.hammockAccent : (t.accent?.color ?? Self.hammockAccent),
                  bubble: own ? Self.hammockBubble : (t.bubble?.color ?? Self.hammockBubble),
                  light: t.light, dark: t.dark, isHammock: own)
    }

    /// The Mac's own choices: its terminal themes (the file it sent, which
    /// beats a curated copy of the same name, else the one the app has by
    /// that name) and its agent colour, built once for what it sent.
    @MainActor private static func custom(_ host: Hammock_V1_HostTheme) -> PhoneTheme {
        if let t = customThemes[host] { return t }
        // The agent colour as the Mac resolves it (AppConfig.agentColors):
        // agent-color, else the window theme's colour, else (Native, Match
        // Terminal) the system's accent, which here is Hammock's green.
        let agent = agentColors(host.agentColor) ?? agentColors(windowTint(host.windowTheme)) ?? (hammockAccent, hammockBubble, true)
        let t = PhoneTheme(source: .mac(host), name: "Custom", accent: agent.color, bubble: agent.bubble,
                           light: terminal(host.lightTerminal, file: host.lightTerminalFile, base: .light),
                           dark: terminal(host.darkTerminal, file: host.darkTerminalFile, base: .dark), isHammock: agent.own)
        if customThemes.count > 8 { customThemes.removeAll() }
        customThemes[host] = t
        return t
    }

    /// Custom themes already built.
    @MainActor private static var customThemes: [Hammock_V1_HostTheme: PhoneTheme] = [:]

    /// An `agent-color` value's colours, as the Mac reads it: an AgentColor's,
    /// a curated theme's, or a colour given (its light one is your messages',
    /// deep enough for white text). Hammock's are the iPhone's green.
    private static func agentColors(_ value: String) -> (color: Color, bubble: Color, own: Bool)? {
        guard !value.isEmpty else { return nil }
        if let a = AgentColor.named(value), let b = AgentColor.bubble(value) { return (a, b, value == AppTheme.defaultName) }
        if let t = AppTheme.named(value), t.accent != nil {
            let c = curated(t)
            return (c.accent, c.bubble, c.isHammock)
        }
        if let p = AppTheme.parsePair(value) { return (p.color, AppTheme.color(p.light), false) }
        return nil
    }

    /// The Mac's window themes with a colour of their own, each an
    /// AgentColor of the same name (WindowTheme.builtIn, without Native).
    private static let windowTints = ["Graphite", "Hammock", "Ocean", "Forest", "Rose", "Lavender", "Sand"]

    /// A `window-theme` value's AgentColor name (any case, as the Mac reads
    /// it), "" for Native, Match Terminal or one it doesn't know.
    private static func windowTint(_ value: String) -> String {
        let v = value.trimmingCharacters(in: .whitespaces)
        return windowTints.first { $0.caseInsensitiveCompare(v) == .orderedSame } ?? ""
    }

    @MainActor private static func terminal(_ name: String, file: String, base: TerminalTheme) -> TerminalTheme {
        if !file.isEmpty {
            if let t = parsed[file] { return t }
            let t = TerminalTheme(ghosttyTheme: file, base: base)
            if parsed.count > 16 { parsed.removeAll() }
            parsed[file] = t
            return t
        }
        switch name {
        case TerminalTheme.builtInLight: return .light
        case TerminalTheme.builtInDark: return .dark
        default: return AppTheme.terminal(named: name) ?? base
        }
    }

    /// Theme files already read.
    @MainActor private static var parsed: [String: TerminalTheme] = [:]

    // The iPhone's Hammock green (AgentColor's, as its AccentColor).
    private static let hammockAccent = AgentColor.named("Hammock") ?? .accentColor
    private static let hammockBubble = AgentColor.bubble("Hammock") ?? .accentColor

    // MARK: The terminal (UIKit)

    /// The theme in effect, for the terminal view (set by the app's root).
    @MainActor static var current = hammock {
        didSet { if current != oldValue { NotificationCenter.default.post(name: changed, object: nil) } }
    }
    static let changed = Notification.Name("PhoneTheme.changed")

    // MARK: Migration

    /// The agent colour picked here before themes (Settings ▸ Agent View ▸
    /// Colour) becomes the theme of that name (Blue, which has none, Ocean),
    /// once. Hammock's green (or none) was the default: it stays the Mac's.
    static func migrate() {
        let d = UserDefaults.standard
        guard let old = d.string(forKey: AgentViewPrefs.color) else { return }
        d.removeObject(forKey: AgentViewPrefs.color)
        guard !old.isEmpty, old != AppTheme.defaultName, d.string(forKey: prefKey) == nil else { return }
        let name = old == "Blue" ? "Ocean" : old
        if let t = AppTheme.named(name) { d.set(t.name, forKey: prefKey) }
    }
}

private struct PhoneThemeKey: EnvironmentKey { static let defaultValue = PhoneTheme.hammock }

extension EnvironmentValues {
    /// The app's theme (PhoneTheme), set at the root.
    var phoneTheme: PhoneTheme {
        get { self[PhoneThemeKey.self] }
        set { self[PhoneThemeKey.self] = newValue }
    }
}

extension View {
    /// Themes everything below: the tint, the agent view's colours, and (for
    /// UIKit) the terminal's.
    func phoneTheme(_ t: PhoneTheme) -> some View {
        tint(t.accent)
            .environment(\.phoneTheme, t)
            .environment(\.agentBubble, t.bubble)
            .environment(\.agentAutomaticColor, t.accent)
            .environment(\.agentAutomaticBubble, t.bubble)
            .onChange(of: t, initial: true) { PhoneTheme.current = t }
    }
}

// MARK: Settings

/// A theme's look in a small tile: its terminal's background, with its
/// accent in a dot.
struct ThemeSwatch: View {
    let theme: PhoneTheme

    var body: some View {
        RoundedRectangle(cornerRadius: 6, style: .continuous)
            .fill(theme.terminalBackground)
            .overlay(alignment: .bottomTrailing) {
                Circle().fill(theme.accent).frame(width: 11, height: 11).padding(4)
            }
            .overlay(RoundedRectangle(cornerRadius: 6, style: .continuous).strokeBorder(.separator, lineWidth: 0.5))
            .frame(width: 30, height: 22)
            .accessibilityHidden(true)
    }
}

/// Settings ▸ Theme: the Mac's, or one of the curated ones.
struct ThemePicker: View {
    @Binding var selection: String
    /// The Mac's theme (Hammock's until it says).
    let mac: PhoneTheme
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        List {
            Section {
                row("Same as Mac", detail: mac.name, theme: mac, tag: "")
            } footer: {
                Text("Your Mac's theme, from Settings ▸ Appearance there.")
            }
            Section {
                ForEach(AppTheme.all) { t in row(t.name, detail: nil, theme: PhoneTheme.curated(t), tag: t.name) }
            }
        }
        .navigationTitle("Theme")
        .navigationBarTitleDisplayMode(.inline)
    }

    private func row(_ title: String, detail: String?, theme: PhoneTheme, tag: String) -> some View {
        Button {
            selection = tag
        } label: {
            HStack(spacing: 12) {
                ThemeSwatch(theme: theme)
                VStack(alignment: .leading, spacing: 1) {
                    Text(title).foregroundStyle(.primary)
                    if let detail { Text(detail).font(.footnote).foregroundStyle(.secondary) }
                }
                Spacer()
                if selection == tag || (tag.isEmpty && AppTheme.named(selection) == nil) {
                    Image(systemName: "checkmark").foregroundStyle(theme.accent).fontWeight(.semibold)
                }
            }
        }
        .tint(.primary) // the names in black or white, as Settings' rows
    }
}
