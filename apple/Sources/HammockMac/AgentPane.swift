import AppKit
import Observation
import HammockKit
import SwiftUI
import UniformTypeIdentifiers

/// Which agent panes show the agent view instead of the terminal (toggled
/// per pane, remembered; new panes follow Settings ▸ Agent View).
@MainActor
@Observable
final class AgentModes {
    static let shared = AgentModes()
    private(set) var version = 0

    func showsAgent(_ paneID: String) -> Bool {
        _ = version
        return AgentViewPrefs.showsAgent(paneID)
    }

    func toggle(_ paneID: String) {
        AgentViewPrefs.setShowsAgent(paneID, !AgentViewPrefs.showsAgent(paneID))
        version += 1
    }

    func set(_ paneID: String, _ on: Bool) {
        AgentViewPrefs.setShowsAgent(paneID, on)
        version += 1
    }
}

/// The header control that flips an agent pane between terminal and agent view.
struct AgentViewToggle: View {
    let paneID: String
    var modes = AgentModes.shared

    var body: some View {
        let on = modes.showsAgent(paneID)
        Picker("View", selection: Binding(get: { on }, set: { modes.set(paneID, $0) })) {
            Image(systemName: "apple.terminal").tag(false)
            Image(systemName: "text.bubble").tag(true)
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .controlSize(.small)
        .fixedSize()
        // The theme's colour, as the agent view itself (not the app's green).
        .tint(AppConfig.shared.agentColors.color)
        .tooltip(Keys.hint("Terminal or agent view", "toggle_agent_view"))
    }
}

/// The agent view in a Mac pane.
struct AgentPaneView: View {
    let model: AppModel
    let paneID: String
    @State private var preview: String?

    var body: some View {
        AgentConversationView(model: model, paneID: paneID, onShowTerminal: {
            AgentModes.shared.set(paneID, false)
        }, onOpenFile: { path in
            // A Group shared with you runs on its host: its files are there,
            // fetched through Hammock, not opened from this Mac's disk.
            if model.group(ofPane: paneID)?.isRemote == true {
                preview = path
            } else {
                FileLink.open(path)
            }
        })
        .environment(\.agentAutomaticColor, AgentAutomatic.color)
        .environment(\.agentAutomaticBubble, AgentAutomatic.bubble)
        .background(Color(nsColor: .textBackgroundColor))
        .sheet(item: Binding(get: { preview.map(PreviewPath.init) }, set: { preview = $0?.id })) {
            RemoteFilePreview(model: model, paneID: paneID, path: $0.id)
        }
    }
}

private struct PreviewPath: Identifiable { let id: String }

/// A file an agent in a shared Group referenced, fetched from its host.
/// Someone who can only view gets images only.
struct RemoteFilePreview: View {
    let model: AppModel
    let paneID: String
    let path: String
    @State private var data: Data?
    @State private var failed = false
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text((path as NSString).lastPathComponent).font(.headline).lineLimit(1)
                Spacer()
                Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
            }
            .padding(12)
            Divider()
            Group {
                if let data, let img = NSImage(data: data) {
                    ScrollView { Image(nsImage: img).resizable().scaledToFit().padding() }
                } else if let data {
                    ScrollView([.vertical, .horizontal]) {
                        Text(String(decoding: data.prefix(400_000), as: UTF8.self))
                            .font(.system(.footnote, design: .monospaced))
                            .textSelection(.enabled)
                            .padding()
                    }
                } else if failed {
                    ContentUnavailableView(
                        model.group(ofPane: paneID)?.canDrive == false ? "Only people who can drive can open files." : "Can’t open this file",
                        systemImage: "doc.questionmark", description: Text(path))
                } else {
                    ProgressView()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .frame(minWidth: 480, idealWidth: 640, minHeight: 360, idealHeight: 520)
        .task {
            let s = model.acquireAgent(paneID)
            data = await s.file("file:" + path)
            failed = data == nil
            model.releaseAgent(paneID)
        }
    }
}

/// Opens a file an agent (or a terminal) linked to in its default app, only
/// when it's a plain document whose app won't run it (FileOpening); apps,
/// scripts, installers, aliases and the like are shown in the Finder
/// instead, for you to open yourself.
enum FileLink {
    static func open(_ path: String) {
        // A NUL or other control character: not a path to act on at all.
        guard FileOpening.acceptable(path) else { NSSound.beep(); return }
        let url = resolved(URL(fileURLWithPath: path))
        if opensDirectly(url) {
            NSWorkspace.shared.open(url)
        } else {
            NSWorkspace.shared.activateFileViewerSelecting([url])
        }
    }

    /// Symlinks and Finder aliases followed to what they point at (opening
    /// an alias to an app launches the app).
    static func resolved(_ url: URL) -> URL {
        var url = url.resolvingSymlinksInPath()
        for _ in 0..<8 {
            guard (try? url.resourceValues(forKeys: [.isAliasFileKey]))?.isAliasFile == true,
                  let next = try? URL(resolvingAliasFileAt: url, options: [.withoutUI, .withoutMounting]),
                  next != url else { break }
            url = next.resolvingSymlinksInPath()
        }
        return url
    }

    static func opensDirectly(_ url: URL) -> Bool {
        let v = try? url.resourceValues(forKeys: [.isDirectoryKey, .isPackageKey, .isApplicationKey, .isExecutableKey, .isAliasFileKey, .contentTypeKey])
        guard let v, v.isApplication != true else { return false }
        let app = NSWorkspace.shared.urlForApplication(toOpen: url).flatMap { Bundle(url: $0)?.bundleIdentifier }
        let facts = FileOpening.Facts(type: v.contentType, isDirectory: v.isDirectory == true, isPackage: v.isPackage == true,
                                      isExecutable: v.isDirectory != true && v.isExecutable == true,
                                      isAlias: v.isAliasFile == true, defaultApp: app)
        return FileOpening.opensDirectly(facts, pathExtension: url.pathExtension)
    }
}

/// The agent view's colours on the Mac: the theme's (Settings ▸ Appearance;
/// with Custom, `agent-color`, else the window theme's; Native: the system accent).
enum AgentAutomatic {
    @MainActor static var color: Color { AppConfig.shared.agentColors.color }
    @MainActor static var bubble: Color { AppConfig.shared.agentColors.bubble }
}

/// Settings ▸ Agent View.
struct AgentViewSettings: View {
    private var prefs: Prefs { .shared }
    private var fontName: String { prefs.string(.agentFont) }
    private var fontSize: Double { prefs.double(.agentFontSize) }
    private var codeFontName: String { prefs.string(.agentCodeFont) }
    private var codeFontSize: Double { prefs.double(.agentCodeFontSize) }
    private var lineSpacing: Double { prefs.double(.agentLineSpacing) }
    private let families = NSFontManager.shared.availableFontFamilies

    var body: some View {
        Form {
            SettingsPaneHeader(tab: .agentView)
            Section {
                PicturePicker(label: "Agent panes open in", options: [("terminal", "Terminal"), ("agent", "Agent view")], selection: prefs.stringBinding(.agentDefaultView)) { v in
                    CurrentMiniWindow { $0.agentView = v == "agent"; $0.border = true }
                }
                .settingAnchor("agentview.default")
                Text("\(Keys.label("toggle_agent_view").map { "Switch a pane with \($0). " } ?? "")Claude Code only, for now.")
                    .font(.caption).foregroundStyle(.secondary)
            }
            Section("Colour") {
                AgentColorPreview(color: AgentAutomatic.bubble)
                    .settingAnchor("agentview.colour")
                HStack {
                    Text("The agent view takes its colour from your theme.")
                    Spacer()
                    Button("Appearance Settings…") { SettingsTab.select(.appearance) }
                }
                .font(.callout)
                .foregroundStyle(.secondary)
            }
            Section("Text") {
                Picker("Font", selection: prefs.stringBinding(.agentFont)) {
                    Text("System").tag("")
                    Divider()
                    ForEach(families, id: \.self) { Text($0).tag($0) }
                }
                .settingAnchor("agentview.font")
                Stepper("Size: \(Int(fontSize))", value: prefs.doubleBinding(.agentFontSize), in: 9...28)
                Stepper("Line spacing: \(Int(lineSpacing))", value: prefs.doubleBinding(.agentLineSpacing), in: 0...12)
            }
            Section("Code") {
                Picker("Font", selection: prefs.stringBinding(.agentCodeFont)) {
                    Text("System Monospaced").tag("")
                    Divider()
                    ForEach(families, id: \.self) { Text($0).tag($0) }
                }
                .settingAnchor("agentview.codefont")
                Stepper("Size: \(Int(codeFontSize))", value: prefs.doubleBinding(.agentCodeFontSize), in: 8...26)
                Picker("Wrap long lines", selection: prefs.stringBinding(.agentCodeWrap)) {
                    Text("In Markdown, text and tools").tag("prose")
                    Text("In every block").tag("always")
                    Text("Never").tag("never")
                }
                .settingAnchor("agentview.codewrap")
            }
            Section("Attachments") {
                Picker("Picture size", selection: prefs.stringBinding(.agentImageSize)) {
                    ForEach(AttachmentSize.allCases) { Text($0.label).tag($0.rawValue) }
                }
                .settingAnchor("agentview.imagesize")
                Picker("Picture size on other computers", selection: prefs.stringBinding(.agentImageSizeRemote)) {
                    ForEach(AttachmentSize.allCases) { Text($0.label).tag($0.rawValue) }
                }
                .settingAnchor("agentview.imagesizeremote")
            }
            Section("Dictation") {
                Toggle("Dictation button", isOn: prefs.boolBinding(.agentDictation))
                    .settingAnchor("agentview.dictation")
            }
            Section("Preview") {
                VStack(alignment: .leading, spacing: 8) {
                    Text("Here's the plan: **two** small changes, then a test run.")
                        .font(fontName.isEmpty ? .system(size: fontSize) : .custom(fontName, size: fontSize))
                        .lineSpacing(lineSpacing)
                    Text("go test ./internal/agentview/")
                        .font(codeFontName.isEmpty ? .system(size: codeFontSize, design: .monospaced) : .custom(codeFontName, size: codeFontSize))
                }
            }
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
    }
}
