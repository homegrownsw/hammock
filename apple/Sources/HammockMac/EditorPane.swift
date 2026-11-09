import AppKit
import HammockKit
import SwiftUI
import UniformTypeIdentifiers
import WebKit

// Editor panes on the Mac: CodeMirror in a WKWebView (EditorBridge, in
// HammockKit), under a header with the file's name and state. The buffer
// is the daemon's; this is one view of it.

/// Quick Open: where what's picked opens.
struct QuickOpenRequest: Identifiable {
    let id = UUID()
    var groupID: String
    var splitPane: String?
    var direction: SplitDirection = .horizontal
    /// Open in this pane instead (its file closes).
    var replacePane: String?
}

@MainActor
@Observable
final class EditorUI {
    static let shared = EditorUI()
    var quickOpen: QuickOpenRequest?
    /// Save As for a Group on another computer: a path typed, not picked.
    var saveAsPane: String?

    private func groupID(_ model: AppModel) -> String? {
        model.currentGroup?.id ?? model.groups.first?.id
    }

    // MARK: Opening

    func newTab(_ model: AppModel) {
        guard let g = groupID(model) else { return }
        open(model, "", groupID: g)
    }

    func split(_ model: AppModel, _ paneID: String, _ direction: SplitDirection) {
        open(model, "", groupID: nil, splitPane: paneID, direction: direction)
    }

    func showQuickOpen(_ model: AppModel, splitPane: String? = nil, direction: SplitDirection = .horizontal, replacePane: String? = nil) {
        guard let g = (splitPane ?? replacePane).flatMap({ model.group(ofPane: $0)?.id }) ?? groupID(model) else { return }
        quickOpen = QuickOpenRequest(groupID: g, splitPane: splitPane, direction: direction, replacePane: replacePane)
    }

    /// The open panel: this Mac's files (a Group elsewhere opens through
    /// Quick Open). With replacePane, the file opens in that pane instead.
    func openFile(_ model: AppModel, replacePane: String? = nil) {
        guard let g = replacePane.flatMap({ model.group(ofPane: $0) }) ?? model.currentGroup ?? model.groups.first else { return }
        if g.isRemote || model.isOnLinkedComputer(g) {
            showQuickOpen(model, replacePane: replacePane)
            return
        }
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = replacePane == nil
        panel.message = replacePane == nil ? "Open in an editor pane" : "Open in this pane"
        if let p = replacePane.flatMap({ model.panes[$0] }), p.isEditor, !p.editor.path.isEmpty {
            panel.directoryURL = URL(fileURLWithPath: (p.editor.path as NSString).deletingLastPathComponent)
        } else if !g.folder.isEmpty {
            panel.directoryURL = URL(fileURLWithPath: g.folder)
        }
        let done: (NSApplication.ModalResponse) -> Void = { r in
            guard r == .OK else { return }
            for url in panel.urls { self.open(model, url.path, groupID: g.id, replacePane: replacePane) }
        }
        if let w = WorkspaceWindow.window { panel.beginSheetModal(for: w, completionHandler: done) } else { done(panel.runModal()) }
    }

    func open(_ model: AppModel, _ path: String, groupID: String?, splitPane: String? = nil, direction: SplitDirection = .horizontal,
              replacePane: String? = nil) {
        guard let id = replacePane, let p = model.panes[id] else {
            openNow(model, path, groupID: groupID, splitPane: splitPane, direction: direction, replacePane: nil, large: false)
            return
        }
        // Already showing it; else ask first if its own file has unsaved edits.
        if p.isEditor, !p.editor.path.isEmpty, p.editor.path == path { return }
        CloseGuard.confirm(model, panes: [id], title: "Open \((path as NSString).lastPathComponent) here instead?", button: "Open") {
            self.openNow(model, path, groupID: groupID, splitPane: splitPane, direction: direction, replacePane: id, large: false)
        }
    }

    private func openNow(_ model: AppModel, _ path: String, groupID: String?, splitPane: String?, direction: SplitDirection,
                         replacePane: String?, large: Bool) {
        Task {
            do {
                try await model.openEditor(path, groupID: groupID, splitPane: splitPane, direction: direction,
                                           replacePane: replacePane, large: large)
            } catch where isLargeFileError(error) {
                let msg = String(error.localizedDescription.dropFirst("large file: ".count))
                if Self.ask(msg, info: "Large files can make the editor slow.", button: "Open") {
                    openNow(model, path, groupID: groupID, splitPane: splitPane, direction: direction, replacePane: replacePane, large: true)
                }
            } catch {
                Self.show("Couldn't open \((path as NSString).lastPathComponent)", error)
            }
        }
    }

    // MARK: The focused editor

    func session(_ model: AppModel, _ paneID: String?) -> EditorSession? {
        guard let id = paneID, model.panes[id]?.isEditor == true else { return nil }
        return model.editorSession(id)
    }

    func run(_ model: AppModel, _ paneID: String?, _ command: String) {
        session(model, paneID)?.run(command)
    }

    func save(_ model: AppModel, _ paneID: String, saveAs: Bool = false) {
        guard let pane = model.panes[paneID], pane.isEditor, let s = model.editorSession(paneID) else { return }
        // A read-only file can't be saved over: ⌘S offers a copy.
        if saveAs || pane.editor.path.isEmpty || pane.editor.readOnly {
            chooseSaveAs(model, paneID)
            return
        }
        Task { await save(s, as: "", name: pane.editor.name) }
    }

    func save(_ s: EditorSession, as path: String, name: String, force: Bool = false) async {
        do {
            try await s.save(as: path, force: force)
        } catch {
            let m = error.localizedDescription
            if !force, m.contains("already exists") || m.contains("changed on disk") {
                let changed = m.contains("changed on disk")
                if Self.ask(changed ? "\(name) changed on disk since you opened it" : m,
                            info: changed ? "Saving replaces what's there now with your version." : "Replacing it overwrites what's there.",
                            button: changed ? "Save Anyway" : "Replace") {
                    await save(s, as: path, name: name, force: true)
                }
                return
            }
            Self.show("Couldn't save \(name)", error)
        }
    }

    /// Save As: the save panel for this Mac's files, else a path to type.
    func chooseSaveAs(_ model: AppModel, _ paneID: String) {
        guard let pane = model.panes[paneID], let s = model.editorSession(paneID) else { return }
        let group = model.group(ofPane: paneID)
        if group?.isRemote == true || group.map(model.isOnLinkedComputer) == true {
            saveAsPane = paneID
            return
        }
        let panel = NSSavePanel()
        let folder = pane.editor.path.isEmpty ? pane.editor.folder : (pane.editor.path as NSString).deletingLastPathComponent
        if !folder.isEmpty { panel.directoryURL = URL(fileURLWithPath: folder) }
        panel.nameFieldStringValue = pane.editor.path.isEmpty ? "Untitled.txt" : pane.editor.name
        panel.canCreateDirectories = true
        let done: (NSApplication.ModalResponse) -> Void = { r in
            guard r == .OK, let url = panel.url else { return }
            // The panel asked about replacing already.
            Task { await self.save(s, as: url.path, name: url.lastPathComponent, force: true) }
        }
        if let w = WorkspaceWindow.window { panel.beginSheetModal(for: w, completionHandler: done) } else { done(panel.runModal()) }
    }

    func reload(_ model: AppModel, _ paneID: String) {
        guard let pane = model.panes[paneID], let s = model.editorSession(paneID) else { return }
        if pane.editor.dirty, !Self.ask("Discard your changes to \(pane.editor.name)?", info: "The file's saved version replaces them.", button: "Reload") { return }
        Task {
            do { try await s.reload() } catch { Self.show("Couldn't reload \(pane.editor.name)", error) }
        }
    }

    func showDiff(_ model: AppModel, _ paneID: String) {
        guard let s = model.editorSession(paneID) else { return }
        if s.showingDiff { s.hideDiff(); return }
        Task {
            do { try await s.showDiff() } catch { Self.show("Couldn't compare it with the file", error) }
        }
    }

    // MARK: Alerts

    static func ask(_ message: String, info: String, button: String) -> Bool {
        let a = NSAlert()
        a.messageText = message
        a.informativeText = info
        a.addButton(withTitle: button)
        a.addButton(withTitle: "Cancel")
        return a.runModal() == .alertFirstButtonReturn
    }

    static func show(_ message: String, _ error: Error) {
        let a = NSAlert()
        a.messageText = message
        a.informativeText = error.localizedDescription
        a.runModal()
    }
}

extension AppModel {
    /// A Group whose files aren't this Mac's: shared with you, or on another of
    /// your computers (both have a host).
    func isOnLinkedComputer(_ g: WorkspaceGroup) -> Bool { g.isRemote }
}

// MARK: - The pane

struct EditorPaneView: View {
    let model: AppModel
    let paneID: String
    let isFocused: Bool
    @State private var session: EditorSession?
    @Environment(\.colorScheme) private var colorScheme

    private var pane: Pane? { model.panes[paneID] }
    private var info: EditorInfo { pane?.editor ?? EditorInfo() }
    private var canEdit: Bool { model.group(ofPane: paneID)?.canDrive != false && model.group(ofPane: paneID)?.offline != true }

    var body: some View {
        VStack(spacing: 0) {
            header
                .contextMenu { PaneMenuItems(model: model, paneID: paneID) }
                .paneSwapHandle(paneID, model: model)
            if let session {
                banner(session)
                EditorWebViewRep(model: model, session: session, paneID: paneID, isFocused: isFocused,
                                 config: config, info: info, readOnly: !canEdit)
                    .overlay(alignment: .bottom) {
                        if !session.error.isEmpty {
                            Text(session.error)
                                .font(.callout)
                                .padding(.horizontal, 10).padding(.vertical, 6)
                                .background(.regularMaterial, in: Capsule())
                                .padding(10)
                                .onTapGesture { session.error = "" }
                        }
                    }
            } else {
                Color.clear
            }
        }
        .onAppear { if session == nil { session = model.acquireEditor(paneID) } }
        .onDisappear {
            if session != nil {
                model.releaseEditor(paneID)
                session = nil
            }
        }
    }

    private var config: EditorConfig {
        let dark = colorScheme == .dark
        let c = AppConfig.shared
        return .from(theme: c.theme(isDark: dark), dark: dark, fontFamily: c.fontFamily ?? "SF Mono", fontSize: c.fontSize ?? 13, readOnly: !canEdit)
    }

    private var header: some View {
        HStack(spacing: 8) {
            Image(systemName: info.path.isEmpty ? "doc" : "doc.text")
                .foregroundStyle(.secondary)
                .frame(width: 14)
            PaneKindMenu(model: model, paneID: paneID) {
                HStack(spacing: 4) {
                    Text(info.name.isEmpty ? "Untitled" : info.name).fontWeight(.semibold)
                    if info.dirty {
                        Circle().fill(.secondary).frame(width: 7, height: 7)
                            .help("Edited: not saved yet (⌘S saves)")
                    }
                }
            }
            Text(shortPath)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.head)
            Spacer(minLength: 6)
            if let session {
                Text(session.selected > 0 ? "\(session.selected) selected" : "\(session.line):\(session.column)")
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
                LanguageMenu(session: session, info: info, enabled: canEdit)
            }
            AvatarStack(model.presence(inPane: paneID), size: 18)
            if model.group(ofPane: paneID)?.canDrive == false {
                Text("View only")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .background(.quaternary, in: Capsule())
            }
            ZoomButton(model: model, paneID: paneID)
            PaneCloseButton(model: model, paneID: paneID)
        }
        .font(.callout)
        .padding(.leading, 15)
        .padding(.trailing, 12)
        .frame(height: 28)
        .background(isFocused ? Color.themeAccent.opacity(0.06) : Color.clear)
    }

    private var shortPath: String {
        guard !info.path.isEmpty else { return info.folder.isEmpty ? "" : (info.folder as NSString).abbreviatingWithTildeInPath }
        let dir = (info.path as NSString).deletingLastPathComponent
        if !info.folder.isEmpty, dir.hasPrefix(info.folder) {
            let rel = String(dir.dropFirst(info.folder.count)).trimmingCharacters(in: CharacterSet(charactersIn: "/"))
            return rel
        }
        return (dir as NSString).abbreviatingWithTildeInPath
    }

    @ViewBuilder private func banner(_ s: EditorSession) -> some View {
        if s.showingDiff {
            EditorBanner(text: "Your version, against the file on disk: take or leave each difference.", tint: .blue) {
                Button("Done") { s.hideDiff() }
            }
        } else if info.conflict {
            EditorBanner(text: "\(info.name) changed on disk while you were editing it.", tint: .orange) {
                Button("Compare") { EditorUI.shared.showDiff(model, paneID) }
                Button("Reload") { EditorUI.shared.reload(model, paneID) }
                Button("Keep Mine") { Task { try? await s.keepMine() } }
            }
        } else if info.readOnly, canEdit {
            EditorBanner(text: "\(info.name) is read-only: you don't have permission to change it.", tint: .secondary, symbol: "lock.fill") {
                Button("Save As…") { EditorUI.shared.save(model, paneID, saveAs: true) }
            }
        } else if info.missing {
            EditorBanner(text: "\(info.name) isn't on disk. Saving creates it.", tint: .secondary) { EmptyView() }
        }
    }
}

struct EditorBanner<Buttons: View>: View {
    let text: String
    let tint: Color
    var symbol = "exclamationmark.triangle.fill"
    @ViewBuilder let buttons: () -> Buttons

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: symbol).foregroundStyle(tint)
            Text(text).lineLimit(2)
            Spacer(minLength: 6)
            buttons().controlSize(.small)
        }
        .font(.callout)
        .padding(.horizontal, 12).padding(.vertical, 6)
        .background(tint.opacity(0.1))
    }
}

/// The language highlighting the file: the page's guess, or one chosen.
struct LanguageMenu: View {
    let session: EditorSession
    let info: EditorInfo
    let enabled: Bool

    @State private var picking = false

    var body: some View {
        Button {
            picking = true
        } label: {
            Text(session.languageName.isEmpty ? "Plain Text" : session.languageName)
                .foregroundStyle(.secondary)
        }
        .buttonStyle(.plain)
        .fixedSize()
        .disabled(!enabled)
        .help("The language it's highlighted as: click to choose another")
        .popover(isPresented: $picking, arrowEdge: .bottom) {
            LanguagePicker(names: session.languageNames, chosen: info.language) { name in
                session.setLanguage(name)
                picking = false
            }
        }
    }
}

/// Type to find a language; Return picks the first match. "" is Detect from Name.
struct LanguagePicker: View {
    let names: [String]
    let chosen: String
    let pick: (String) -> Void
    @State private var query = ""
    @State private var selection = 0
    @FocusState private var fieldFocused: Bool

    private static let detect = "Detect from Name"

    private var rows: [String] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return [Self.detect] + names }
        let starts = names.filter { $0.lowercased().hasPrefix(q) }
        let contains = names.filter { !$0.lowercased().hasPrefix(q) && $0.lowercased().contains(q) }
        return starts + contains
    }

    var body: some View {
        VStack(spacing: 0) {
            TextField("Language", text: $query)
                .textFieldStyle(.roundedBorder)
                .focused($fieldFocused)
                .onSubmit { choose() }
                .onKeyPress(.downArrow) { move(1); return .handled }
                .onKeyPress(.upArrow) { move(-1); return .handled }
                .padding(8)
            Divider()
            ScrollViewReader { proxy in
                List(Array(rows.enumerated()), id: \.element) { i, name in
                    HStack {
                        Text(name).foregroundStyle(name == Self.detect ? .secondary : .primary)
                        Spacer()
                        if name == chosen || (name == Self.detect && chosen.isEmpty) {
                            Image(systemName: "checkmark").foregroundStyle(.secondary)
                        }
                    }
                    .contentShape(Rectangle())
                    .onTapGesture { selection = i; choose() }
                    .id(i)
                    .listRowBackground(i == selection ? Color.accentColor.opacity(0.2) : Color.clear)
                }
                .listStyle(.plain)
                .onChange(of: selection) { _, s in proxy.scrollTo(s) }
                .onAppear {
                    if let i = rows.firstIndex(of: chosen.isEmpty ? Self.detect : chosen) {
                        selection = i
                        proxy.scrollTo(i, anchor: .center)
                    }
                }
            }
            .frame(height: 280)
        }
        .frame(width: 240)
        .onAppear { fieldFocused = true }
        .onChange(of: query) { _, _ in selection = 0 }
    }

    private func move(_ d: Int) {
        guard !rows.isEmpty else { return }
        selection = min(max(selection + d, 0), rows.count - 1)
    }

    private func choose() {
        guard rows.indices.contains(selection) else { return }
        let n = rows[selection]
        pick(n == Self.detect ? "" : n)
    }
}

// MARK: - The web view

struct EditorWebViewRep: NSViewRepresentable {
    let model: AppModel
    let session: EditorSession
    let paneID: String
    let isFocused: Bool
    let config: EditorConfig
    let info: EditorInfo
    let readOnly: Bool

    final class Coordinator {
        var bridge: EditorBridge?
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> EditorWK {
        var made: EditorWK?
        let bridge = EditorBridge(session: session) { conf in
            let v = EditorWK(frame: .zero, configuration: conf)
            made = v
            return v
        }
        context.coordinator.bridge = bridge
        let v = made!
        v.bridge = bridge
        v.model = model
        v.paneID = paneID
        bridge.onFocus = { [model, paneID] in model.viewFocused(paneID) }
        bridge.onSave = { [model, paneID] saveAs in EditorUI.shared.save(model, paneID, saveAs: saveAs) }
        bridge.configure(config)
        bridge.setInfo(info, readOnly: readOnly)
        v.isFocusTarget = isFocused
        return v
    }

    func updateNSView(_ v: EditorWK, context: Context) {
        context.coordinator.bridge?.configure(config)
        context.coordinator.bridge?.setInfo(info, readOnly: readOnly)
        v.isFocusTarget = isFocused
    }
}

/// The page's view: focus as the pane's, a Mac context menu, and the Edit
/// menu's Undo and Redo going to CodeMirror's history.
final class EditorWK: WKWebView {
    weak var bridge: EditorBridge?
    weak var model: AppModel?
    var paneID = ""
    private var observers: [NSObjectProtocol] = []

    var isFocusTarget = false {
        didSet { if isFocusTarget, !oldValue { focusSoon() } }
    }

    override init(frame: CGRect, configuration: WKWebViewConfiguration) {
        super.init(frame: frame, configuration: configuration)
        observers.append(NotificationCenter.default.addObserver(forName: TerminalNSView.refocus, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { if self?.isFocusTarget == true { self?.focusSoon() } }
        })
    }

    required init?(coder: NSCoder) { fatalError() }

    deinit {
        for o in observers { NotificationCenter.default.removeObserver(o) }
    }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    func focusSoon() {
        DispatchQueue.main.async { [weak self] in
            guard let self, self.isFocusTarget, !PaletteState.shared.isOpen, let w = self.window else { return }
            if !(w.firstResponder is NSView && (w.firstResponder as! NSView).isDescendant(of: self)) {
                w.makeFirstResponder(self)
            }
            self.bridge?.session.run("focus")
        }
    }

    @objc func undo(_ sender: Any?) { bridge?.session.run("undo") }
    @objc func redo(_ sender: Any?) { bridge?.session.run("redo") }

    /// WebKit's own items that fit (Look Up, Translate, Cut, Copy, Paste,
    /// Spelling, Services…), then the editor's.
    override func willOpenMenu(_ menu: NSMenu, with event: NSEvent) {
        let drop: Set<String> = ["WKMenuItemIdentifierReload", "WKMenuItemIdentifierGoBack", "WKMenuItemIdentifierGoForward",
                                 "WKMenuItemIdentifierOpenLinkInNewWindow", "WKMenuItemIdentifierDownloadLinkedFile",
                                 "WKMenuItemIdentifierOpenImageInNewWindow", "WKMenuItemIdentifierDownloadImage",
                                 "WKMenuItemIdentifierOpenFrameInNewWindow", "WKMenuItemIdentifierShareMenu"]
        for item in menu.items where drop.contains(item.identifier?.rawValue ?? "") { menu.removeItem(item) }
        while menu.items.first?.isSeparatorItem == true { menu.removeItem(at: 0) }
        guard let model, let pane = model.panes[paneID] else { return }
        let ed = pane.editor
        let canEdit = model.group(ofPane: paneID)?.canDrive != false
        let local = !(model.group(ofPane: paneID).map { $0.isRemote || model.isOnLinkedComputer($0) } ?? false)
        let ui = EditorUI.shared
        if !menu.items.isEmpty { menu.addItem(.separator()) }
        menu.addItem(ClosureItem("Find…", key: "find_in_page") { ui.run(model, self.paneID, "find") })
        menu.addItem(ClosureItem("Go to Line…", key: "editor_go_to_line") { ui.run(model, self.paneID, "gotoLine") })
        menu.addItem(.separator())
        if canEdit {
            menu.addItem(ClosureItem("Save", key: "editor_save") { ui.save(model, self.paneID) })
            menu.addItem(ClosureItem("Save As…", key: "editor_save_as") { ui.save(model, self.paneID, saveAs: true) })
            if !ed.path.isEmpty {
                menu.addItem(ClosureItem(bridge?.session.showingDiff == true ? "Hide Comparison" : "Compare with File on Disk", key: "editor_show_diff") { ui.showDiff(model, self.paneID) })
                menu.addItem(ClosureItem("Reload from Disk", key: "editor_reload") { ui.reload(model, self.paneID) })
            }
            menu.addItem(.separator())
        }
        if !ed.path.isEmpty {
            menu.addItem(ClosureItem("Copy Path", key: nil) {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(ed.path, forType: .string)
            })
            if local {
                menu.addItem(ClosureItem("Reveal in Finder", key: nil) { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: ed.path)]) })
                menu.addItem(ClosureItem("Open in Default App", key: nil) { NSWorkspace.shared.open(URL(fileURLWithPath: ed.path)) })
            }
            menu.addItem(.separator())
        }
        menu.addItem(ClosureItem("Split Right with Editor", key: "split_editor_right") { ui.split(model, self.paneID, .horizontal) })
        menu.addItem(ClosureItem("Split Down with Editor", key: "split_editor_down") { ui.split(model, self.paneID, .vertical) })
        menu.addItem(ClosureItem("Quick Open…", key: "quick_open") { ui.showQuickOpen(model, splitPane: self.paneID) })
    }
}

/// A menu item that runs a closure, showing an action's shortcut.
@MainActor
final class ClosureItem: NSMenuItem {
    private let run: () -> Void

    init(_ title: String, key: String?, run: @escaping () -> Void) {
        self.run = run
        super.init(title: title, action: #selector(fire), keyEquivalent: "")
        target = self
        if let key, let chord = AppConfig.shared.keybinds[key], let c = chord.keyCharacter {
            keyEquivalent = String(c).lowercased()
            var m: NSEvent.ModifierFlags = []
            if chord.modifiers.contains(.command) { m.insert(.command) }
            if chord.modifiers.contains(.shift) { m.insert(.shift) }
            if chord.modifiers.contains(.option) { m.insert(.option) }
            if chord.modifiers.contains(.control) { m.insert(.control) }
            keyEquivalentModifierMask = m
        }
    }

    required init(coder: NSCoder) { fatalError() }

    @objc private func fire() { run() }
}

// MARK: - Quick Open

struct QuickOpenSheet: View {
    let model: AppModel
    let request: QuickOpenRequest
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    @State private var results: [String] = []
    @State private var folder = ""
    @State private var selection = 0
    @State private var error = ""
    @State private var task: Task<Void, Never>?
    @FocusState private var fieldFocused: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
                TextField("Open a file in \(model.groups.first { $0.id == request.groupID }.map { $0.name.isEmpty ? "this Group" : $0.name } ?? "this Group")…", text: $query)
                    .textFieldStyle(.plain)
                    .font(.title3)
                    .focused($fieldFocused)
                    .onSubmit { open() }
                    .onKeyPress(.downArrow) { move(1); return .handled }
                    .onKeyPress(.upArrow) { move(-1); return .handled }
                    .onKeyPress(.escape) { dismiss(); return .handled }
            }
            .padding(12)
            Divider()
            ScrollViewReader { proxy in
                List(Array(results.enumerated()), id: \.element) { i, path in
                    row(path, selected: i == selection)
                        .id(i)
                        .contentShape(Rectangle())
                        .onTapGesture { selection = i; open() }
                        .listRowBackground(i == selection ? Color.accentColor.opacity(0.2) : Color.clear)
                }
                .listStyle(.plain)
                .onChange(of: selection) { _, s in proxy.scrollTo(s) }
            }
            .frame(height: 320)
            if !error.isEmpty || results.isEmpty {
                Text(error.isEmpty ? (query.isEmpty ? "No files here" : "No matches: Return opens “\(query)” as a new file") : error)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .padding(8)
            }
        }
        .frame(width: 560)
        .onAppear { fieldFocused = true; search() }
        .onChange(of: query) { _, _ in search() }
    }

    private func row(_ path: String, selected: Bool) -> some View {
        let name = (path as NSString).lastPathComponent
        let dir = (path as NSString).deletingLastPathComponent
        return HStack(spacing: 8) {
            Image(systemName: "doc.text").foregroundStyle(.secondary)
            Text(name).fontWeight(selected ? .semibold : .regular)
            Text(dir).foregroundStyle(.secondary).lineLimit(1).truncationMode(.head)
            Spacer()
        }
        .padding(.vertical, 2)
    }

    private func move(_ d: Int) {
        guard !results.isEmpty else { return }
        selection = min(max(selection + d, 0), results.count - 1)
    }

    private func search() {
        task?.cancel()
        let q = query
        task = Task {
            if !q.isEmpty { try? await Task.sleep(for: .milliseconds(60)) }
            guard !Task.isCancelled else { return }
            do {
                let list = try await model.listFiles(groupID: request.groupID, query: q, limit: 100)
                guard !Task.isCancelled else { return }
                results = list.paths
                folder = list.folder
                selection = 0
                error = ""
            } catch {
                self.error = error.localizedDescription
                results = []
            }
        }
    }

    private func open() {
        let path: String
        if results.indices.contains(selection) {
            path = results[selection]
        } else {
            path = query.trimmingCharacters(in: .whitespaces)
        }
        guard !path.isEmpty else { return }
        // Relative to the Group's folder (the daemon resolves it there).
        EditorUI.shared.open(model, path, groupID: request.splitPane == nil ? request.groupID : nil,
                             splitPane: request.splitPane, direction: request.direction, replacePane: request.replacePane)
        dismiss()
    }
}

/// Save As for a Group on another computer: a path in its folder.
struct EditorSaveAsSheet: View {
    let model: AppModel
    let paneID: String
    @Environment(\.dismiss) private var dismiss
    @State private var path = ""

    private var info: EditorInfo { model.panes[paneID]?.editor ?? EditorInfo() }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Save As").font(.headline)
            Text("A path in \(info.folder.isEmpty ? "the Group's folder" : (info.folder as NSString).abbreviatingWithTildeInPath), or a full path.")
                .foregroundStyle(.secondary)
            TextField("notes.md", text: $path)
                .textFieldStyle(.roundedBorder)
                .onSubmit { save() }
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Save") { save() }.keyboardShortcut(.defaultAction).disabled(path.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 420)
        .onAppear { path = info.path.isEmpty ? "" : (info.path.hasPrefix(info.folder + "/") ? String(info.path.dropFirst(info.folder.count + 1)) : info.path) }
    }

    private func save() {
        guard let s = model.editorSession(paneID) else { return }
        let p = path, name = (path as NSString).lastPathComponent
        dismiss()
        Task { await EditorUI.shared.save(s, as: p, name: name) }
    }
}

// MARK: - Settings

/// Settings ▸ Editor (the editor-* keys).
struct EditorSettingsSection: View {
    private var prefs: Prefs { .shared }

    var body: some View {
        Section("Editor panes") {
            Toggle("Wrap long lines", isOn: prefs.boolBinding(.editorWrap))
            Toggle("Line numbers", isOn: prefs.boolBinding(.editorLineNumbers))
            Picker("Indent with", selection: prefs.stringBinding(.editorIndent)) {
                Text("Spaces").tag("spaces")
                Text("Tabs").tag("tabs")
            }
            Stepper("Tab width: \(Int(prefs.double(.editorTabWidth)))", value: prefs.doubleBinding(.editorTabWidth), in: 1...16, step: 1)
            HStack {
                Toggle("Own font size", isOn: Binding(
                    get: { !prefs.string(.editorFontSize).isEmpty },
                    set: { prefs.set(.editorFontSize, $0 ? Pref.format(AppConfig.shared.fontSize ?? 13) : "") }))
                if !prefs.string(.editorFontSize).isEmpty {
                    Stepper("\(Int(prefs.double(.editorFontSize))) pt", value: prefs.doubleBinding(.editorFontSize), in: 6...36, step: 1)
                }
            }
            Toggle("Vim keys", isOn: prefs.boolBinding(.editorVim))
            Text("A file's .editorconfig, or how it's already indented, beats the indent settings. Without its own size, the editor follows the terminal's font.")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
    }
}
