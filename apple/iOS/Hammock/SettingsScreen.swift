import HammockKit
import SwiftUI

/// This iPhone's settings: how agent sessions read, and the app's look.
struct SettingsScreen: View {
    let model: AppModel
    @AppStorage(AgentViewPrefs.defaultView) private var defaultView = "agent"
    @AppStorage(AgentViewPrefs.fontName) private var fontName = ""
    @AppStorage(AgentViewPrefs.fontSize) private var fontSize = AgentViewPrefs.defaultSize
    @AppStorage(AgentViewPrefs.codeFontSize) private var codeFontSize = AgentViewPrefs.defaultSize - 1
    @AppStorage(AgentViewPrefs.lineSpacing) private var lineSpacing = AgentViewPrefs.defaultSpacing
    @AppStorage(PhoneTheme.prefKey) private var themePref = ""
    @Environment(\.phoneTheme) private var theme
    #if DEBUG
    @State private var debugThemePicker = ProcessInfo.processInfo.environment["HMK_DEBUG_THEME_PICKER"] == "1"
    #endif
    @AppStorage(TerminalPrefs.fontName) private var terminalFont = ""
    @AppStorage(TerminalPrefs.fontSize) private var terminalSize = TerminalPrefs.defaultSize
    @AppStorage(TerminalPrefs.lineSpacing) private var terminalSpacing = 0.0
    @AppStorage("appearance") private var appearance = "system"

    private let families = UIFont.familyNames.sorted()
    private let monoFamilies = TerminalPrefs.monospacedFamilies

    var body: some View {
        NavigationStack {
            // The look, how sessions read, then the rest.
            FormColumns {
                Section {
                    NavigationLink {
                        ThemePicker(selection: $themePref, mac: PhoneTheme.resolve(pref: "", host: model.hostTheme))
                    } label: {
                        LabeledContent {
                            HStack(spacing: 8) {
                                Text(AppTheme.named(themePref) == nil ? "Same as Mac" : theme.name)
                                ThemeSwatch(theme: theme)
                            }
                        } label: {
                            Text("Theme")
                        }
                    }
                    Picker("Appearance", selection: $appearance) {
                        Text("System").tag("system")
                        Text("Light").tag("light")
                        Text("Dark").tag("dark")
                    }
                    .pickerStyle(.segmented)
                } header: {
                    Text("Appearance")
                } footer: {
                    Text("The app's colour, your messages' and the terminal's. By default, your Mac's theme.")
                }
                Section {
                    Picker("Agent tabs open in", selection: $defaultView) {
                        Text("Agent View").tag("agent")
                        Text("Terminal").tag("terminal")
                    }
                    // A menu picker keeps the tint it was made with: made
                    // again when the colour changes, with the new one (the
                    // app's tint gets here after this redraws).
                    .tint(theme.accent)
                    .id(theme.name)
                } header: {
                    Text("Agent View")
                } footer: {
                    Text("Each session remembers your choice once you switch it. Claude Code sessions only, for now.")
                }
                Section("Text") {
                    Picker("Font", selection: $fontName) {
                        Text("System").tag("")
                        ForEach(families, id: \.self) { Text($0).font(.custom($0, size: 17)).tag($0) }
                    }
                    .pickerStyle(.navigationLink)
                    Stepper("Size: \(Int(fontSize))", value: $fontSize, in: 12...28)
                    Stepper("Line spacing: \(Int(lineSpacing))", value: $lineSpacing, in: 0...12)
                    Stepper("Code size: \(Int(codeFontSize))", value: $codeFontSize, in: 10...26)
                }
                Section("Preview") {
                    VStack(alignment: .leading, spacing: 8) {
                        AgentColorPreview(color: theme.bubble)
                        Text("Here's the plan: **two** small changes, then a test run.")
                            .font(fontName.isEmpty ? .system(size: fontSize) : .custom(fontName, size: fontSize))
                            .lineSpacing(lineSpacing)
                        Text("go test ./internal/daemon/")
                            .font(.system(size: codeFontSize, design: .monospaced))
                            .foregroundStyle(.secondary)
                    }
                    .padding(.vertical, 4)
                }
            } trailing: {
                Section {
                    Picker("Font", selection: $terminalFont) {
                        Text("SF Mono").tag("")
                        ForEach(monoFamilies, id: \.self) { Text($0).font(.custom($0, size: 17)).tag($0) }
                    }
                    .pickerStyle(.navigationLink)
                    Stepper("Size: \(Int(terminalSize))", value: $terminalSize, in: 7...18)
                    Stepper("Line spacing: \(terminalSpacing == 0 ? "Normal" : "\(terminalSpacing > 0 ? "+" : "")\(Int(terminalSpacing))%")",
                            value: $terminalSpacing, in: -20...100, step: 5)
                    Text("~/Developer/hammock $ just ios-device")
                        .font(Font(TerminalPrefs.font(size: terminalSize, bold: false)))
                        .foregroundStyle(theme.terminalForeground)
                        .lineLimit(1)
                        .listRowBackground(theme.terminalBackground)
                } header: {
                    Text("Terminal")
                } footer: {
                    Text("Smaller text fits more columns. Panes wider than your screen shrink to fit.")
                }
                Section {
                    AppLockSettings()
                } header: {
                    Text("Privacy")
                } footer: {
                    Text("Ask for \(AppLock.method) when you come back to Hammock after the time above, and hide it in the app switcher.")
                }
                Section {
                    ForEach(UsageLimitsPrefs.agents, id: \.harness) { UsageToggle(harness: $0.harness) }
                } header: {
                    Text("Usage Limits")
                } footer: {
                    Text("At the bottom of Sessions: how much of each limit window (5 hours, a week) is used, and when it resets, as your Mac's agents last reported it. Only agents installed on your Mac are listed. For up-to-date numbers, turn on Ask each agent's service for its usage in Settings ▸ Agents on the Mac, and show the agent's limits in the Mac's sidebar too.")
                }
                Section {
                    ForEach(Push.events, id: \.0) { event, label in
                        Toggle(label, isOn: Binding(
                            get: { UserDefaults.standard.object(forKey: Push.prefKey(event)) as? Bool ?? true },
                            set: { UserDefaults.standard.set($0, forKey: Push.prefKey(event)); Task { await Push.shared.registerWithMac() } }))
                    }
                } header: {
                    Text("Notify Me When")
                } footer: {
                    Text("Sent while you're away from your Mac. Push: \(Push.shared.status). Encrypted on your Mac; only this iPhone can read them.")
                }
                Section("About") {
                    LabeledContent("Version", value: Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "")
                }
            }
            .navigationTitle("Settings")
            #if DEBUG
            // Dev aid: HMK_DEBUG_THEME_PICKER=1 opens Theme.
            .navigationDestination(isPresented: $debugThemePicker) {
                ThemePicker(selection: $themePref, mac: PhoneTheme.resolve(pref: "", host: model.hostTheme))
            }
            #endif
        }
    }
}

extension View {
    /// Applies the Appearance setting.
    func hammockAppearance(_ a: String) -> some View {
        preferredColorScheme(a == "light" ? .light : a == "dark" ? .dark : nil)
    }
}
