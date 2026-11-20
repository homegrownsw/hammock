import HammockKit
import SwiftUI

/// This iPhone's settings: how agent sessions read, and the app's look.
struct SettingsScreen: View {
    let model: AppModel
    @AppStorage(AgentViewPrefs.defaultView) private var defaultView = "agent"
    @AppStorage(AgentViewPrefs.fontName) private var fontName = ""
    @AppStorage(AgentViewPrefs.fontSize) private var fontSize = AgentViewPrefs.defaultSize
    @AppStorage(AgentViewPrefs.codeFontSize) private var codeFontSize = AgentViewPrefs.defaultSize - 1
    @AppStorage(AgentViewPrefs.codeWrap) private var codeWrap = "prose"
    /// Everything the iPhone sends goes over the network: the remote size.
    @AppStorage(AgentViewPrefs.imageSizeRemote) private var imageSize = AttachmentSize.medium.rawValue
    @AppStorage(AgentViewPrefs.lineSpacing) private var lineSpacing = AgentViewPrefs.defaultSpacing
    @AppStorage(AgentViewPrefs.dictation) private var dictation = true
    @AppStorage(PhoneTheme.prefKey) private var themePref = ""
    @Environment(\.phoneTheme) private var theme
    #if DEBUG
    @State private var debugThemePicker = ProcessInfo.processInfo.environment["HMK_DEBUG_THEME_PICKER"] == "1"
    @State private var debugAcknowledgements = ProcessInfo.processInfo.environment["HMK_DEBUG_ACKNOWLEDGEMENTS"] != nil
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
                    Text("Each session remembers your choice once you switch it. For \(AgentCatalog.agentViewNames) sessions.")
                }
                Section("Text") {
                    FontPickerLink(title: "Font", selection: $fontName, defaultName: "System", families: families)
                    Stepper("Size: \(Int(fontSize))", value: $fontSize, in: 12...28)
                    Stepper("Line spacing: \(Int(lineSpacing))", value: $lineSpacing, in: 0...12)
                    Stepper("Code size: \(Int(codeFontSize))", value: $codeFontSize, in: 10...26)
                    Picker("Wrap code", selection: $codeWrap) {
                        Text("Markdown, Text and Tools").tag("prose")
                        Text("Every Block").tag("always")
                        Text("Never").tag("never")
                    }
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
                Section("Attachments") {
                    Picker("Picture size", selection: $imageSize) {
                        ForEach(AttachmentSize.allCases) { Text($0.label).tag($0.rawValue) }
                    }
                }
                Section("Dictation") {
                    Toggle("Dictation Button", isOn: $dictation)
                }
            } trailing: {
                Section {
                    FontPickerLink(title: "Font", selection: $terminalFont, defaultName: "SF Mono", families: monoFamilies)
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
                Section("Editors and Diffs") {
                    FontPickerLink(title: "Font", selection: Prefs.shared.stringBinding(.editorFontFamily), defaultName: "Same as Terminal", families: monoFamilies)
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
                    Link("Hammock Documentation", destination: HammockLinks.docs)
                    NavigationLink("Acknowledgements") { AcknowledgementsScreen() }
                }
            }
            .themedScreenBackground()
            .navigationTitle("Settings")
            .connectionBar()
            #if DEBUG
            // Dev aid: HMK_DEBUG_THEME_PICKER=1 opens Theme.
            .navigationDestination(isPresented: $debugThemePicker) {
                ThemePicker(selection: $themePref, mac: PhoneTheme.resolve(pref: "", host: model.hostTheme))
            }
            // HMK_DEBUG_ACKNOWLEDGEMENTS=1 opens Acknowledgements.
            .navigationDestination(isPresented: $debugAcknowledgements) { AcknowledgementsScreen() }
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

/// A font setting's row, opening the fonts on a screen of their own (a
/// navigation-link Picker's, but on the theme's background, which the
/// system's screen can't take).
struct FontPickerLink: View {
    let title: String
    @Binding var selection: String
    /// What "" means: the default's name.
    let defaultName: String
    let families: [String]

    var body: some View {
        NavigationLink {
            FontList(title: title, selection: $selection, defaultName: defaultName, families: families)
        } label: {
            LabeledContent(title, value: selection.isEmpty ? defaultName : selection)
        }
    }
}

/// FontPickerLink's screen: picking one goes back, as a Picker's does.
private struct FontList: View {
    let title: String
    @Binding var selection: String
    let defaultName: String
    let families: [String]
    @Environment(\.dismiss) private var dismiss
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        List {
            row(defaultName, tag: "", font: nil)
            ForEach(families, id: \.self) { row($0, tag: $0, font: .custom($0, size: 17)) }
        }
        .themedScreenBackground()
        .navigationTitle(title)
        .navigationBarTitleDisplayMode(.inline)
    }

    private func row(_ name: String, tag: String, font: Font?) -> some View {
        Button {
            selection = tag
            dismiss()
        } label: {
            HStack {
                Text(name).font(font)
                Spacer()
                if selection == tag { Image(systemName: "checkmark").foregroundStyle(theme.accent).fontWeight(.semibold) }
            }
        }
        .tint(.primary) // the names in black or white, as Settings' rows
    }
}
