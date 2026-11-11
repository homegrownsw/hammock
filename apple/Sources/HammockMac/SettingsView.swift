import AppKit
import HammockKit
import HammockUpdates
import SwiftUI
import UserNotifications

struct SettingsView: View {
    let model: AppModel
    @AppStorage(SettingsTab.key) private var tab = SettingsTab.general.rawValue

    @State private var query: String
    @State private var picked: SettingsEntry.ID?
    private var nav: SettingsNav { .shared }
    private var selected: SettingsTab { SettingsTab(rawValue: tab) ?? .general }

    init(model: AppModel, initialQuery: String = "") {
        self.model = model
        _query = State(initialValue: initialQuery)
    }

    var body: some View {
        NavigationSplitView {
            // A fixed width: the Settings window otherwise squeezes the
            // sidebar to its minimum.
            sidebar
                .frame(minWidth: 250)
                .navigationSplitViewColumnWidth(250)
                .toolbar(removing: .sidebarToggle)
        } detail: {
            pane(selected)
                .id(selected)
                .navigationTitle(selected.title)
                .toolbar {
                    ToolbarItemGroup(placement: .navigation) {
                        Button { nav.goBack() } label: { Label("Back", systemImage: "chevron.left") }
                            .disabled(nav.back.isEmpty)
                            .help("Back")
                        Button { nav.goForward() } label: { Label("Forward", systemImage: "chevron.right") }
                            .disabled(nav.forward.isEmpty)
                            .help("Forward")
                    }
                }
        }
        .searchable(text: $query, placement: .sidebar, prompt: "Search")
        .onSubmit(of: .search) { picked = SettingsIndex.search(query).first?.id }
        .onChange(of: query) { picked = nil }
        .onChange(of: tab) { old, new in
            nav.moved(from: SettingsTab(rawValue: old) ?? .general, to: SettingsTab(rawValue: new) ?? .general)
        }
        .background(SettingsWindowStyle())
        .toggleStyle(ThemedSwitchStyle())
        // Room beside the sidebar for Appearance's swatches and the longer sections.
        .frame(minWidth: 850, idealWidth: 950, minHeight: 560, idealHeight: 760)
    }

    // MARK: Sidebar

    @ViewBuilder private var sidebar: some View {
        if query.trimmingCharacters(in: .whitespaces).isEmpty {
            List(selection: Binding(get: { selected }, set: { if let t = $0 { tab = t.rawValue } })) {
                Section { hammockCard }
                ForEach(SettingsTab.sidebar, id: \.self) { group in
                    Section {
                        ForEach(group) { t in
                            Label { Text(t.title) } icon: { SettingsIcon(tab: t) }
                                .tag(t)
                        }
                    }
                }
            }
        } else {
            results
        }
    }

    /// Hammock and its server, where System Settings has your account: a row
    /// of the sidebar that opens About.
    private var hammockCard: some View {
        HStack(spacing: 10) {
            Image(nsImage: NSApp.applicationIconImage)
                .resizable()
                .frame(width: 38, height: 38)
            VStack(alignment: .leading, spacing: 1) {
                Text("Hammock").fontWeight(.semibold)
                Text(model.connected ? "Server running" : "Server not running")
                    .font(.caption)
                    .foregroundStyle(model.connected || selected == .about ? Color.secondary : Color.orange)
            }
        }
        .padding(.vertical, 2)
        .tag(SettingsTab.about)
        .accessibilityLabel("Hammock, \(model.connected ? "server running" : "server not running"): About")
    }

    /// What search found, by pane: picking one opens it at that setting.
    private var results: some View {
        let found = SettingsIndex.search(query)
        let shortcuts = found.filter { $0.keyboardQuery != nil }.prefix(12)
        let shown = found.filter { $0.keyboardQuery == nil } + shortcuts
        let tabs = ([SettingsTab.about] + SettingsTab.sidebar.joined()).filter { t in shown.contains { $0.tab == t } }
        return List(selection: $picked) {
            if shown.isEmpty {
                Text("No settings match “\(query)”.")
                    .foregroundStyle(.secondary)
            }
            ForEach(tabs) { t in
                Section {
                    ForEach(shown.filter { $0.tab == t }) { r in
                        VStack(alignment: .leading, spacing: 1) {
                            Text(r.label).lineLimit(3)
                            Text(r.section).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                        }
                        .padding(.vertical, 2)
                        .padding(.leading, 26)
                        .tag(r.id)
                    }
                } header: {
                    Label { Text(t.title) } icon: { SettingsIcon(tab: t, size: 18) }
                }
            }
        }
        .onChange(of: picked) { _, id in
            if let r = shown.first(where: { $0.id == id }) { nav.reveal(r) }
        }
    }

    // MARK: Panes

    @ViewBuilder private func pane(_ t: SettingsTab) -> some View {
        switch t {
        case .general: GeneralSettings(model: model)
        case .appearance: AppearanceSettings()
        case .terminal: TerminalSettings()
        case .editor: EditorSettings()
        case .keyboard: KeyboardSettings()
        case .agents: AgentSettings()
        case .agentView: AgentViewSettings()
        case .notifications: NotificationSettings(model: model)
        case .sharing: SharingSettings(model: model)
        case .devices: DevicesSettings(model: model)
        case .ssh: SSHSettings(model: model)
        case .about: AboutSettings(model: model)
        }
    }
}

private struct GeneralSettings: View {
    let model: AppModel
    @State private var daemonAtLogin = DaemonService.isEnabled
    @State private var cliPath = CommandLineTool.installedPath
    @State private var error: String?

    var body: some View {
        Form {
            SettingsPaneHeader(tab: .general)
            Section {
                Toggle("Start the server at login", isOn: $daemonAtLogin)
                    .onChange(of: daemonAtLogin) { _, on in
                        do { try DaemonService.setEnabled(on) } catch {
                            self.error = error.localizedDescription
                            daemonAtLogin = DaemonService.isEnabled
                        }
                    }
                    .settingAnchor("general.login")
                Text("Keeps your terminals and agents running when Hammock isn't open.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Section("Menu Bar") {
                Picker("Show Hammock in the menu bar", selection: Binding(
                    get: { AppConfig.shared.menuBar },
                    set: { AppConfig.shared.set("menu-bar", $0 == "active" ? "active" : nil) })) {
                    Text("Always").tag("always")
                    Text("When a screen is shared or the server is down").tag("active")
                }
                .settingAnchor("general.menubar")
            }
            Section("Groups") {
                Toggle("Keep a Group when its last tab closes", isOn: Binding(
                    get: { AppConfig.shared.keepEmptyGroups },
                    set: { AppConfig.shared.set("keep-empty-groups", $0 ? nil : "false") }))
                    .settingAnchor("general.keepgroups")
            }
            Section("Command-line tool") {
                LabeledContent(CommandLineTool.linkName) {
                    if let path = cliPath {
                        HStack {
                            Text((path as NSString).abbreviatingWithTildeInPath).foregroundStyle(.secondary)
                            Button("Remove") { CommandLineTool.uninstall(); cliPath = CommandLineTool.installedPath }
                        }
                    } else {
                        Button("Install") {
                            do { cliPath = try CommandLineTool.install() } catch { self.error = error.localizedDescription }
                        }
                    }
                }
                .settingAnchor("general.cli")
                Text("Control Hammock from any terminal.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Section("Browser panes") {
                Picker("Links that open a new tab", selection: Binding(
                    get: { AppConfig.shared.browserNewTabExternal },
                    set: { AppConfig.shared.set("browser-new-tab", $0 ? nil : "hammock") })) {
                    Text("Open in your default browser").tag(true)
                    Text("Open in a Hammock browser tab").tag(false)
                }
                Text("⌘-click, middle-click and sign-in windows always open in Hammock.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Section("Links to other apps") {
                let schemes = AppConfig.shared.browserOpenSchemes
                if schemes.isEmpty {
                    Text("None allowed yet").foregroundStyle(.secondary)
                }
                ForEach(schemes, id: \.self) { scheme in
                    LabeledContent("\(scheme):") {
                        Button("Forget") { AppConfig.shared.setBrowserOpenSchemes(schemes.filter { $0 != scheme }) }
                    }
                }
                Text("These open in their app without asking.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .settingAnchor("general.links")
            }
            Section("Simulators") {
                Toggle("Shut down simulators when their last pane closes", isOn: Binding(
                    get: { AppConfig.shared.simulatorShutdown },
                    set: { AppConfig.shared.set("simulator-shutdown", $0 ? nil : "false") }))
                    .settingAnchor("general.simulators")
                Text("Only ones Hammock started.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Section("Server") {
                LabeledContent("Status", value: model.connected ? "Connected" : "Not connected")
                    .settingAnchor("general.daemon")
                LabeledContent("Host", value: model.hostName.isEmpty ? "—" : model.hostName)
                LabeledContent("State folder") {
                    Button((stateDir as NSString).abbreviatingWithTildeInPath) {
                        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: stateDir)
                    }
                    .buttonStyle(.link)
                }
            }
            if let error {
                Text(error).foregroundStyle(.red).font(.callout)
            }
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
    }

    private var stateDir: String {
        ((DaemonConnection.defaultSocketPath as NSString).deletingLastPathComponent as NSString).deletingLastPathComponent
    }
}

struct NotificationSettings: View {
    let model: AppModel
    private var prefs: Prefs { .shared }
    private var style: NotifyStyle { .current }
    @State private var status: UNAuthorizationStatus?

    var body: some View {
        Form {
            SettingsPaneHeader(tab: .notifications)
            Section("Notify me when an agent") {
                Toggle("Is waiting for my input", isOn: prefs.boolBinding(NotifyPrefs.needsInput))
                    .settingAnchor("notify.input")
                Toggle("Asks for permission", isOn: prefs.boolBinding(NotifyPrefs.grants))
                    .settingAnchor("notify.grants")
                Toggle("Finishes", isOn: prefs.boolBinding(NotifyPrefs.finished))
                    .settingAnchor("notify.finished")
                Toggle("Hits an error", isOn: prefs.boolBinding(NotifyPrefs.errored))
                    .settingAnchor("notify.errored")
            }
            Section("How to notify me") {
                Picker("Show notifications", selection: Binding(get: { style }, set: { prefs.set(NotifyPrefs.style, $0.rawValue) })) {
                    Text("In Hammock when it's in front, otherwise macOS").tag(NotifyStyle.auto)
                    Text("As macOS notifications").tag(NotifyStyle.system)
                    Text("In the Hammock window").tag(NotifyStyle.inApp)
                }
                .pickerStyle(.radioGroup)
                .themeTint()
                .settingAnchor("notify.style")
                Toggle("Play a sound with notifications", isOn: prefs.boolBinding(NotifyPrefs.sound))
                    .settingAnchor("notify.sound")
                Toggle("Also notify for the tab I'm looking at", isOn: prefs.boolBinding(NotifyPrefs.whenVisible))
                    .settingAnchor("notify.visible")
                Toggle("Show a count on the Dock icon", isOn: prefs.boolBinding(NotifyPrefs.badge))
                    .onChange(of: prefs.bool(NotifyPrefs.badge)) { Notifier.shared.updateBadge(model) }
                    .settingAnchor("notify.badge")
                Button("Send a Test Notification") { Notifier.shared.sendTest() }
                    .disabled(style == .system && status == .denied)
                    .settingAnchor("notify.test")
            }
            if style.usesSystem {
                Section("macOS") {
                    LabeledContent("Permission") {
                        HStack {
                            Text(statusText).foregroundStyle(status == .authorized ? Color.secondary : Color.orange)
                            Button("Open System Settings") {
                                NSWorkspace.shared.open(URL(string: "x-apple.systempreferences:com.apple.Notifications-Settings.extension")!)
                            }
                        }
                    }
                }
            }
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
        .task { status = await Notifier.shared.authorization() }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { status = await Notifier.shared.authorization() }
        }
    }

    private var statusText: String {
        switch status {
        case .authorized, .provisional: return "Allowed"
        case .denied: return "Turned off in System Settings"
        case .notDetermined: return "Not asked yet"
        default: return "…"
        }
    }
}

struct AppearanceSettings: View {
    private var config: AppConfig { .shared }
    private var layout: SidebarLayout { .current }
    private var showFinished: Bool { Prefs.shared.bool(FinishedPrefs.pref) }
    private var showAgentsCollapsed: Bool { Prefs.shared.bool(CollapsedAgentsPrefs.pref) }
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        Form {
            SettingsPaneHeader(tab: .appearance)
            Picker("Appearance", selection: Binding(get: { config.appearance }, set: { config.setAppearance($0) })) {
                Text("Automatic").tag(AppConfig.Appearance.auto)
                Text("Light").tag(AppConfig.Appearance.light)
                Text("Dark").tag(AppConfig.Appearance.dark)
            }
            .pickerStyle(.segmented)
            .themeTint()
            .settingAnchor("appearance.mode")
            Section {
                ThemeGrid()
                    .settingAnchor("appearance.theme")
            } header: {
                Text("Theme")
            }
            if config.isCustomTheme { customTheme }
            Section("Window") {
                HStack(spacing: 16) {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("Sidebar")
                        HStack {
                            Text("Glassy").font(.callout).foregroundStyle(.secondary)
                            Slider(value: Binding(get: { config.sidebarOpacity }, set: { config.previewSidebarOpacity($0) }), in: 0...1) { editing in
                                if !editing { config.setSidebarOpacity(config.sidebarOpacity) }
                            }
                            Text("Opaque").font(.callout).foregroundStyle(.secondary)
                        }
                    }
                    CurrentMiniWindow(liveOpacity: true) { $0.wallpaper = true }.frame(width: 120, height: 74)
                }
                .settingAnchor("appearance.sidebar")
            }
            Section("Tabs") {
                PicturePicker(label: "Layout", options: SidebarLayout.allCases.map { ($0, $0.shortLabel) }, selection: Binding(get: { layout }, set: { SidebarLayout.set($0) })) { l in
                    CurrentMiniWindow { $0.layout = l }
                }
                .settingAnchor("appearance.layout")
                PicturePicker(label: "Group headings", options: [(AppConfig.GroupHeadings.prominent, "Prominent"), (.subtle, "Subtle")],
                              selection: Binding(get: { config.groupHeadings }, set: { config.setGroupHeadings($0) })) { h in
                    GroupHeadingsPicture(subtle: h == .subtle, icons: config.groupIcons)
                }
                .settingAnchor("appearance.headings")
                Text("Horizontal tabs always use Prominent.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                PictureToggle(label: "Group icons",
                              isOn: Binding(get: { config.groupIcons }, set: { config.set("group-icons", $0 ? nil : "false") })) {
                    GroupHeadingsPicture(subtle: config.groupHeadings == .subtle, icons: config.groupIcons)
                }
                .settingAnchor("appearance.groupicons")
                PicturePicker(label: "Agent status", options: [(AppConfig.TabStatus.icon, "In the tab's icon"), (.trailing, "At the end of the row")],
                              selection: Binding(get: { config.tabStatus }, set: { config.setTabStatus($0) })) { s in
                    CurrentMiniWindow { $0.tabStatus = s; if $0.layout == .horizontal { $0.layout = .vertical } }
                }
                .settingAnchor("appearance.tabstatus")
                PictureToggle(label: "Show finished agents", caption: "In their own list at the bottom of the sidebar.", isOn: Prefs.shared.boolBinding(FinishedPrefs.pref)) {
                    FinishedPicture(on: showFinished)
                }
                .settingAnchor("appearance.finished")
                PictureToggle(label: "Show active agents in collapsed Groups",
                              isOn: Prefs.shared.boolBinding(CollapsedAgentsPrefs.pref)) {
                    CollapsedAgentsPicture(on: showAgentsCollapsed)
                }
                .disabled(layout == .horizontal)
                .settingAnchor("appearance.collapsed")
                PictureToggle(label: "Close button on hover",
                              isOn: Binding(get: { config.tabCloseButton }, set: { config.set("tab-close-button", $0 ? nil : "false") })) {
                    TabClosePicture(on: config.tabCloseButton)
                }
                .settingAnchor("appearance.closebutton")
                PicturePicker(label: "Program icons", options: [(ProgramIconStyle.off, "None"), (.plain, "Plain"), (.colorful, "Colorful")],
                              selection: Binding(get: { config.programIconStyle }, set: { config.set("program-icons", $0 == .plain ? nil : $0.rawValue) })) { s in
                    ProgramIconsPicture(style: s)
                }
                .settingAnchor("appearance.programicons")
            }
            Section("Panes") {
                PicturePicker(label: "Spacing", options: [(AppConfig.PaneSpacing.compact, "Compact"), (.spacious, "Spacious")],
                              selection: Binding(get: { config.paneSpacing }, set: { config.setPaneSpacing($0) })) { s in
                    CurrentMiniWindow { $0.spacious = s == .spacious }
                }
                .settingAnchor("appearance.spacing")
                PictureToggle(label: "Dim inactive panes", isOn: Binding(get: { config.dimInactivePanes }, set: { config.set("dim-inactive-panes", $0 ? "true" : nil) })) {
                    FocusPicture(dim: config.dimInactivePanes, border: false)
                }
                .settingAnchor("appearance.dim")
                PictureToggle(label: "Border around the active pane",
                              isOn: Binding(get: { config.activePaneBorder }, set: { config.set("active-pane-border", $0 ? "true" : nil) })) {
                    FocusPicture(dim: false, border: config.activePaneBorder)
                }
                .settingAnchor("appearance.border")
            }
            Section("Cursor") {
                PicturePicker(label: "Shape", options: AppConfig.cursorStyleNames.map { ($0.1, $0.2) },
                              selection: Binding(get: { config.cursorStyleName }, set: { config.setCursorStyle($0) }), width: 70, height: 40) { name in
                    CursorPicture(style: name, blink: false)
                }
                .settingAnchor("appearance.cursor")
                PictureToggle(label: "Blink", caption: "Programs can override this.",
                              isOn: Binding(get: { config.cursorBlink }, set: { config.set("cursor-style-blink", $0 ? nil : "false") })) {
                    CursorPicture(style: config.cursorStyleName, blink: config.cursorBlink)
                }
                .settingAnchor("appearance.blink")
            }
            Section {
                HStack {
                    Button("Edit Config File") {
                        config.ensureFile()
                        NSWorkspace.shared.open(config.fileURL)
                    }
                    Button("Open Themes Folder") {
                        try? FileManager.default.createDirectory(at: config.themesDir, withIntermediateDirectories: true)
                        NSWorkspace.shared.open(config.themesDir)
                    }
                }
                .settingAnchor("appearance.config")
            }
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
    }

    /// Custom…: each of a theme's colours on its own.
    @ViewBuilder private var customTheme: some View {
        let themes = config.availableThemes()
        Section("Window colours") {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 76), spacing: 10)], spacing: 12) {
                ForEach(config.windowThemes) { t in
                    WindowThemeSwatch(theme: t, selected: t.name == config.windowThemeName) { config.setWindowTheme(t.name) }
                }
            }
            .padding(.vertical, 4)
            Text("sidebar-color and toolbar-color in the config file override these.")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
        Section("Terminal colours") {
            Picker("Light theme", selection: Binding(get: { config.lightThemeName }, set: { config.setThemes(light: $0, dark: config.darkThemeName) })) {
                ForEach(themes, id: \.self) { Text($0).tag($0) }
            }
            ThemeSwatch(theme: config.lightTheme)
            Picker("Dark theme", selection: Binding(get: { config.darkThemeName }, set: { config.setThemes(light: config.lightThemeName, dark: $0) })) {
                ForEach(themes, id: \.self) { Text($0).tag($0) }
            }
            ThemeSwatch(theme: config.darkTheme)
            Text("Ghostty themes, including any in ~/.config/hammock/themes or ~/.config/ghostty/themes.")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
        Section("Agent view colour") {
            AgentColorChoices()
            AgentColorPreview(color: config.agentColors.bubble)
        }
        Section("Active pane border colour") {
            LabeledContent("Border colour") {
                HStack {
                    if config.activePaneBorderColorValue != nil {
                        Button("Use Accent Colour") { config.setActivePaneBorderColor(nil) }
                    }
                    ColorPicker("Border colour", selection: Binding(
                        get: { PaneLook.borderColor(dark: colorScheme == .dark) },
                        set: { config.setActivePaneBorderColor(NSColor($0)) }), supportsOpacity: false)
                        .labelsHidden()
                }
            }
        }
    }
}

/// Custom's agent colour: the window theme's (Automatic), one of the agent
/// colours, a theme's, or any colour.
private struct AgentColorChoices: View {
    private var config: AppConfig { .shared }

    var body: some View {
        let value = config.agentColorValue
        let named = AgentColor.all.map { ($0.name, $0.bubble) }
            + AppTheme.all.filter { !$0.ownTint }.compactMap { t in t.bubble.map { (t.name, $0.color) } }
        let isOther = value != nil && !named.contains { $0.0 == value }
        VStack(alignment: .leading, spacing: 8) {
            LazyVGrid(columns: [GridItem(.adaptive(minimum: 64), spacing: 8)], spacing: 10) {
                swatch("Automatic", AgentColor.bubble(config.windowThemeName) ?? Color.themeAccent, on: value == nil, system: "sparkles") { config.setAgentColor(nil) }
                ForEach(named, id: \.0) { name, color in
                    swatch(name, color, on: value == name) { config.setAgentColor(name) }
                }
            }
            LabeledContent("Another colour") {
                ColorPicker("Another colour", selection: Binding(
                    get: { isOther ? config.agentColors.color : Color.themeAccent },
                    set: { c in
                        guard let n = NSColor(c).usingColorSpace(.sRGB) else { return }
                        config.setAgentColor(String(format: "#%02X%02X%02X", Int((n.redComponent * 255).rounded()), Int((n.greenComponent * 255).rounded()), Int((n.blueComponent * 255).rounded())))
                    }), supportsOpacity: false)
                    .labelsHidden()
            }
        }
        .padding(.vertical, 4)
    }

    private func swatch(_ name: String, _ color: Color, on: Bool, system: String? = nil, pick: @escaping () -> Void) -> some View {
        Button(action: pick) {
            VStack(spacing: 5) {
                ZStack {
                    Circle().fill(color.gradient).frame(width: 28, height: 28)
                    if let system { Image(systemName: system).font(.caption.weight(.semibold)).foregroundStyle(.white) }
                }
                .padding(3)
                .overlay(Circle().strokeBorder(on ? color : .clear, lineWidth: 2))
                Text(name).font(.caption).foregroundStyle(on ? .primary : .secondary).lineLimit(1)
            }
            .frame(maxWidth: .infinity)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(system != nil ? "The window theme's colour; the system accent with Native" : name)
        .accessibilityLabel(name)
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

/// The theme's background with its 16 colours, as a quick preview.
private struct ThemeSwatch: View {
    let theme: TerminalTheme

    var body: some View {
        HStack(spacing: 3) {
            ForEach(Array(theme.ansi.enumerated()), id: \.offset) { _, c in
                RoundedRectangle(cornerRadius: 2).fill(color(c)).frame(width: 16, height: 16)
            }
        }
        .padding(6)
        .background(RoundedRectangle(cornerRadius: 5).fill(color(theme.background)))
        .overlay(alignment: .trailing) {
            Text("Aa").font(.system(.body, design: .monospaced)).foregroundStyle(color(theme.foreground)).padding(.trailing, -28)
        }
    }

    private func color(_ c: RGB) -> Color { Color(red: Double(c.r) / 255, green: Double(c.g) / 255, blue: Double(c.b) / 255) }
}

private struct TerminalSettings: View {
    private var config: AppConfig { .shared }

    private var families: [String] {
        NSFontManager.shared.availableFontFamilies.filter { name in
            NSFont(name: name, size: 12).map { $0.isFixedPitch || name.localizedCaseInsensitiveContains("mono") || name.localizedCaseInsensitiveContains("nerd") } ?? false
        }
    }

    var body: some View {
        Form {
            SettingsPaneHeader(tab: .terminal)
            Section {
                Picker("Font", selection: Binding(get: { config.fontFamily ?? "" }, set: { set("font-family", $0.isEmpty ? nil : $0) })) {
                    Text("SF Mono").tag("")
                    Divider()
                    ForEach(families, id: \.self) { Text($0).tag($0) }
                }
                .settingAnchor("terminal.font")
                LabeledContent("Size") {
                    Stepper("\(Int(config.fontSize ?? 13)) pt", value: Binding(get: { config.fontSize ?? 13 }, set: { set("font-size", String(Int($0))) }), in: 6...36, step: 1)
                }
                .settingAnchor("terminal.size")
                LabeledContent("Line spacing") {
                    Stepper(lineSpacingLabel, value: Binding(get: { config.cellHeightPercent ?? 0 }, set: { config.set("adjust-cell-height", $0 == 0 ? nil : "\(Int($0))%") }), in: -20...100, step: 5)
                }
                .settingAnchor("terminal.linespacing")
                Toggle("Thicken text", isOn: Binding(get: { config.fontThicken }, set: { set("font-thicken", $0 ? "true" : nil) }))
                    .settingAnchor("terminal.thicken")
                Toggle("Copy text when selected", isOn: Binding(get: { config.copyOnSelect }, set: { config.set("copy-on-select", $0 ? "true" : nil) }))
                    .settingAnchor("terminal.copy")
                Toggle("Show images from file paths", isOn: Binding(get: { config.imageFileMedium }, set: { config.set("image-file-medium", $0 ? nil : "false") }))
                    .settingAnchor("terminal.images")
                Text("Used by Claude Code, among others. Applies to new panes.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                Toggle("Ask before pasting text that could run commands", isOn: Binding(get: { config.pasteProtection }, set: { config.set("clipboard-paste-protection", $0 ? nil : "false") }))
                    .settingAnchor("terminal.paste")
                Text("Unset keys follow your Ghostty config.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
    }

    private var lineSpacingLabel: String {
        guard let v = config.cellHeightAdjust else { return "Normal" }
        guard let pct = config.cellHeightPercent else { return "\(v) pt" }
        return pct == 0 ? "Normal" : pct > 0 ? "+\(Int(pct))%" : "\(Int(pct))%"
    }

    private func set(_ key: String, _ value: String?) {
        UserDefaults.standard.removeObject(forKey: "terminalFontZoom")
        config.set(key, value)
    }
}

/// Editor panes: the editor-* keys.
struct EditorSettings: View {
    var body: some View {
        Form {
            SettingsPaneHeader(tab: .editor)
            EditorSettingsSection()
            DiffSettingsSection()
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
    }
}

private struct AgentSettings: View {
    @State private var output = ""
    @State private var running = false

    /// What turning on usage-live does.
    private var liveUsageNote: String {
        var note = "Checks every 5 minutes with each agent's own sign-in, which never leaves this Mac. macOS may ask for Keychain access."
        if AppConfig.shared.claudeUsageLive && !AppConfig.shared.usageLive {
            note += " For now, only Claude's, as you set up before."
        }
        return note
    }

    var body: some View {
        Form {
            SettingsPaneHeader(tab: .agents)
            Section {
                Toggle("Warn before closing an agent that's at work", isOn: Prefs.shared.boolBinding(CloseGuard.pref))
                    .settingAnchor("agents.closeguard")
            }
            Section("Usage Limits in the Sidebar") {
                ForEach(UsageLimitsPrefs.agents, id: \.harness) { a in
                    UsageToggle(harness: a.harness)
                        .settingAnchor(a.harness == UsageLimitsPrefs.agents.first?.harness ? "agents.usage" : "agents.usage.\(a.harness)")
                }
                Toggle("Ask each agent's service for its usage", isOn: Binding(
                    get: { AppConfig.shared.usageLive },
                    set: {
                        AppConfig.shared.set("usage-live", $0 ? "true" : nil)
                        AppConfig.shared.set("claude-usage-live", nil)
                    }))
                    .settingAnchor("agents.usagelive")
                Text(liveUsageNote)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            Section {
                Text("Hooks give exact agent states and let sessions resume after a restart. They also give agents Hammock's tools: their own panes and browser control.")
                    .font(.callout)
                HStack {
                    Button("Preview Changes") { run(["hooks", "install", "--dry-run"]) }
                    Button("Install Hooks & Tools") { run(["hooks", "install"]) }
                        .buttonStyle(.borderedProminent)
                    Button("Remove") { run(["hooks", "install", "--uninstall"]) }
                    if running { ProgressView().controlSize(.small) }
                }
                .settingAnchor("agents.hooks")
                Text("Edits each installed agent's settings, with a backup. Your existing hooks are kept.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if !output.isEmpty {
                Section("Result") {
                    ScrollView {
                        Text(output)
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .frame(height: 160)
                }
            }
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
    }

    private func run(_ args: [String]) {
        guard let hammock = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/hmk") as URL?,
              FileManager.default.isExecutableFile(atPath: hammock.path) else {
            output = "hmk was not found in the app bundle."
            return
        }
        running = true
        Task.detached {
            let p = Process()
            p.executableURL = hammock
            p.arguments = args
            let pipe = Pipe()
            p.standardOutput = pipe
            p.standardError = pipe
            var text = ""
            do {
                try p.run()
                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                p.waitUntilExit()
                text = String(decoding: data, as: UTF8.self)
            } catch {
                text = error.localizedDescription
            }
            let result = text
            await MainActor.run {
                output = result
                running = false
            }
        }
    }
}

/// A window theme to pick: its sidebar and toolbar colours, light above dark.
private struct WindowThemeSwatch: View {
    let theme: WindowTheme
    let selected: Bool
    let pick: () -> Void

    var body: some View {
        Button(action: pick) {
            VStack(spacing: 5) {
                VStack(spacing: 0) {
                    row(theme.sidebar?.light, theme.toolbar?.light, fallback: NSColor(white: 0.93, alpha: 1))
                    row(theme.sidebar?.dark, theme.toolbar?.dark, fallback: NSColor(white: 0.17, alpha: 1))
                }
                .frame(width: 64, height: 40)
                .overlay {
                    if let mark = theme.mark {
                        Circle().fill(Color(nsColor: mark)).frame(width: 14, height: 14)
                            .overlay(Circle().strokeBorder(.white.opacity(0.6), lineWidth: 1))
                    } else {
                        // Native: the system's glass.
                        Image(systemName: "circle.lefthalf.filled").foregroundStyle(.secondary)
                    }
                }
                .clipShape(RoundedRectangle(cornerRadius: 7))
                .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Color.primary.opacity(0.12), lineWidth: 0.5))
                .padding(3)
                .overlay(RoundedRectangle(cornerRadius: 9).strokeBorder(selected ? Color.themeAccent : .clear, lineWidth: 2))
                Text(theme.name).font(.caption).lineLimit(1)
            }
        }
        .buttonStyle(.plain)
        .help(theme.name)
    }

    /// A sidebar beside its toolbar, in one appearance.
    private func row(_ sidebar: NSColor?, _ toolbar: NSColor?, fallback: NSColor) -> some View {
        HStack(spacing: 0) {
            Color(nsColor: sidebar ?? fallback).frame(width: 22)
            Color(nsColor: toolbar ?? fallback.blended(withFraction: 0.04, of: .white) ?? fallback)
        }
    }
}
