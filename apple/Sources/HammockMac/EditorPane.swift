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
        if let w = WorkspaceWindow.current { panel.beginSheetModal(for: w, completionHandler: done) } else { done(panel.runModal()) }
    }

    func open(_ model: AppModel, _ path: String, groupID: String?, splitPane: String? = nil, direction: SplitDirection = .horizontal,
              replacePane: String? = nil) {
        guard let id = replacePane, let p = model.panes[id] else {
            openNow(model, path, groupID: groupID, splitPane: splitPane, direction: direction, replacePane: nil, large: false)
            return
        }
        // Already showing it; else ask first if its own file has unsaved edits.
        if p.isEditor, !p.editor.path.isEmpty, p.editor.path == path { return }
        let name = path.isEmpty ? "a new file" : (path as NSString).lastPathComponent
        CloseGuard.confirmReplace(model, id, title: "Open \(name) here instead?") {
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
                if await PaneNotices.shared.ask(replacePane ?? splitPane, msg, info: "Large files can make the editor slow.", button: "Open") {
                    openNow(model, path, groupID: groupID, splitPane: splitPane, direction: direction, replacePane: replacePane, large: true)
                }
            } catch {
                let retry = PaneNotices.Action(title: "Retry") {
                    self.openNow(model, path, groupID: groupID, splitPane: splitPane, direction: direction, replacePane: replacePane, large: large)
                }
                Self.failed(model, in: replacePane ?? splitPane, "Couldn't open \((path as NSString).lastPathComponent)", error, actions: [retry])
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
        Task { await save(model, paneID, s, as: "", name: pane.editor.name) }
    }

    func save(_ model: AppModel, _ paneID: String, _ s: EditorSession, as path: String, name: String, force: Bool = false) async {
        do {
            try await s.save(as: path, force: force)
            PaneNotices.shared.dismiss(paneID) // an earlier failure's, done with
        } catch {
            let m = error.localizedDescription
            if !force, m.contains("already exists") || m.contains("changed on disk") {
                let changed = m.contains("changed on disk")
                if await PaneNotices.shared.ask(paneID, changed ? "\(name) changed on disk since you opened it" : m,
                                                info: changed ? "Saving replaces what's there now with your version." : "Replacing it overwrites what's there.",
                                                button: changed ? "Save Anyway" : "Replace") {
                    await save(model, paneID, s, as: path, name: name, force: true)
                }
                return
            }
            PaneNotices.shared.error(paneID, "Couldn't save \(name)", error, actions: [
                .init(title: "Retry") { Task { await self.save(model, paneID, s, as: path, name: name, force: force) } },
                .init(title: "Save As…") { self.chooseSaveAs(model, paneID) },
            ])
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
            Task { await self.save(model, paneID, s, as: url.path, name: url.lastPathComponent, force: true) }
        }
        if let w = PaneNotices.shared.window(for: paneID) { panel.beginSheetModal(for: w, completionHandler: done) } else { done(panel.runModal()) }
    }

    func reload(_ model: AppModel, _ paneID: String, asked: Bool = false) {
        guard let pane = model.panes[paneID], let s = model.editorSession(paneID) else { return }
        let name = pane.editor.name
        Task {
            if pane.editor.dirty, !asked,
               await !PaneNotices.shared.ask(paneID, "Discard your changes to \(name)?", info: "The file's saved version replaces them.", button: "Reload") { return }
            do {
                try await s.reload()
                PaneNotices.shared.dismiss(paneID)
            } catch {
                PaneNotices.shared.error(paneID, "Couldn't reload \(name)", error, actions: [.init(title: "Retry") { self.reload(model, paneID, asked: true) }])
            }
        }
    }

    /// Rename…: a new name for the file, in its folder (or a path from
    /// there); unsaved edits go with it. Untitled, it's Save As.
    func rename(_ model: AppModel, _ paneID: String) {
        guard let pane = model.panes[paneID], let s = model.editorSession(paneID) else { return }
        if pane.editor.path.isEmpty { chooseSaveAs(model, paneID); return }
        let a = NSAlert()
        a.messageText = "Rename \(pane.editor.name)"
        a.informativeText = "In \(GroupFolder.tilde((pane.editor.path as NSString).deletingLastPathComponent))"
        let field = NSTextField(string: pane.editor.name)
        field.frame = NSRect(x: 0, y: 0, width: 280, height: 22)
        field.lineBreakMode = .byTruncatingMiddle
        a.accessoryView = field
        a.addButton(withTitle: "Rename")
        a.addButton(withTitle: "Cancel")
        a.window.initialFirstResponder = field
        // The name without its extension selected, as the Finder does.
        let selectStem = {
            let stem = (pane.editor.name as NSString).deletingPathExtension
            a.window.makeFirstResponder(field)
            field.currentEditor()?.selectedRange = NSRange(location: 0, length: (stem.isEmpty ? pane.editor.name : stem).utf16.count)
        }
        Task {
            guard await PaneNotices.shared.present(a, in: PaneNotices.shared.window(for: paneID), shown: selectStem) == .alertFirstButtonReturn else { return }
            let name = field.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !name.isEmpty, name != pane.editor.name else { return }
            do { try await s.rename(to: (name as NSString).expandingTildeInPath) } catch {
                PaneNotices.shared.error(paneID, "Couldn't rename \(pane.editor.name)", error, actions: [.init(title: "Rename…") { self.rename(model, paneID) }])
            }
        }
    }

    /// Whether the pane's file is this Mac's, to show in the Finder.
    func canReveal(_ model: AppModel, _ paneID: String) -> Bool {
        guard let pane = model.panes[paneID], pane.isEditor, !pane.editor.path.isEmpty, let g = model.group(ofPane: paneID) else { return false }
        return !(g.isRemote || model.isOnLinkedComputer(g))
    }

    func revealInFinder(_ model: AppModel, _ paneID: String) {
        guard canReveal(model, paneID), let path = model.panes[paneID]?.editor.path else { return }
        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)])
    }

    // MARK: Markdown's preview

    /// Whether the pane's file can be shown rendered.
    func canPreview(_ model: AppModel, _ paneID: String?) -> Bool {
        session(model, paneID)?.canPreview == true
    }

    func view(_ model: AppModel, _ paneID: String) -> EditorInfo.View {
        let info = model.panes[paneID]?.editor ?? EditorInfo()
        return session(model, paneID)?.shownView(info) ?? info.view
    }

    /// Whether the preview shows alone (no source to go to a line in).
    func previewOnly(_ model: AppModel, _ paneID: String?) -> Bool {
        session(model, paneID)?.previewOnly == true
    }

    /// Whether a view chosen here is only this Mac's: a View member can't
    /// change the pane, which is the host's.
    func localView(_ model: AppModel, _ paneID: String) -> Bool {
        model.group(ofPane: paneID)?.canDrive == false
    }

    /// Preview (or, split, the source and preview side by side), or back to the source.
    func togglePreview(_ model: AppModel, _ paneID: String?, split: Bool = false) {
        guard canPreview(model, paneID), let id = paneID, let s = session(model, id) else { return }
        let want: EditorInfo.View = split ? .split : .preview
        s.setView(view(model, id) == want ? .source : want, local: localView(model, id))
    }

    func previewLabel(_ model: AppModel, _ paneID: String, split: Bool = false) -> String {
        let v = view(model, paneID)
        return split ? (v == .split ? "Hide Preview" : "Show Preview Beside Source") : (v == .preview ? "Show Source" : "Show Preview")
    }

    /// A link in the preview to another file: it opens beside, shown as this one is.
    func openLink(_ model: AppModel, from paneID: String, _ path: String) {
        guard model.group(ofPane: paneID)?.canDrive != false else { return }
        let ext = (path as NSString).pathExtension.lowercased()
        let view = ["md", "markdown", "mdown", "mkd"].contains(ext) ? self.view(model, paneID) : .source
        Task {
            do {
                try await model.openEditor(path, groupID: nil, splitPane: paneID, view: view)
            } catch {
                PaneNotices.shared.error(paneID, "Couldn't open \((path as NSString).lastPathComponent)", error, actions: [
                    .init(title: "Retry") { self.openLink(model, from: paneID, path) },
                ])
            }
        }
    }

    func showDiff(_ model: AppModel, _ paneID: String) {
        guard let s = model.editorSession(paneID) else { return }
        if s.showingDiff { s.hideDiff(); return }
        Task {
            do { try await s.showDiff() } catch {
                PaneNotices.shared.error(paneID, "Couldn't compare it with the file", error, actions: [.init(title: "Retry") { self.showDiff(model, paneID) }])
            }
        }
    }

    // MARK: Errors

    /// An error opening a file: a banner on the pane it was opened from (or
    /// in) when that's an editor or a diff, else a note in the window.
    static func failed(_ model: AppModel, in paneID: String?, _ message: String, _ error: Error, actions: [PaneNotices.Action]) {
        if let id = paneID, let p = model.panes[id], p.isEditor || p.isDiff {
            PaneNotices.shared.error(id, message, error, actions: actions)
        } else {
            model.showToast("\(message): \(PaneNotices.why(error))")
        }
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
    @State private var lease: PaneLease<EditorSession>?
    private var session: EditorSession? { lease?.session }
    @Environment(\.colorScheme) private var colorScheme

    private var pane: Pane? { model.panes[paneID] }
    private var info: EditorInfo { pane?.editor ?? EditorInfo() }
    private var canEdit: Bool { model.group(ofPane: paneID)?.canDrive != false && model.group(ofPane: paneID)?.offline != true }

    var body: some View {
        VStack(spacing: 0) {
            header
                .contextMenu { PaneMenuItems(model: model, paneID: paneID) }
                .paneSwapHandle(paneID, model: model)
            PaneErrorBanner(paneID: paneID)
            if let session {
                banner(session)
                EditorWebViewRep(model: model, session: session, paneID: paneID, isFocused: isFocused,
                                 config: config, info: info, readOnly: !canEdit)
                    .overlay(alignment: .bottom) {
                        PanePopup(text: Bindable(session).error)
                    }
            } else {
                Color.clear
            }
        }
        .animation(.easeOut(duration: 0.2), value: PaneNotices.shared.banners[paneID]?.id)
        .background(PaneWindowReader(paneID: paneID))
        .onAppear { if lease == nil { lease = model.leaseEditor(paneID) } }
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
            HStack(spacing: 5) {
                PaneKindMenu(model: model, paneID: paneID) {
                    Text(info.name.isEmpty ? "Untitled" : info.name).fontWeight(.semibold).lineLimit(1)
                }
                // Beside the menu, not in its label: a Menu's label draws only
                // its text and images. `.tooltip`, as `.help` never shows here.
                if info.dirty {
                    Circle().fill(.secondary).frame(width: 7, height: 7)
                        .frame(width: 13, height: 20)
                        .contentShape(Rectangle())
                        .tooltip("Unsaved Changes")
                        .accessibilityElement()
                        .accessibilityLabel("Unsaved Changes")
                }
            }
            .layoutPriority(1) // the folder gives way first
            Text(shortPath)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.head)
            Spacer(minLength: 6)
            if let session {
                if !session.previewOnly {
                    Text(session.selected > 0 ? "\(session.selected) selected" : "\(session.line):\(session.column)")
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                }
                LanguageMenu(session: session, info: info, enabled: canEdit)
                if session.canPreview {
                    EditorViewPicker(view: session.shownView(info)) { session.setView($0, local: EditorUI.shared.localView(model, paneID)) }
                }
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

/// Source, Split or Preview, for a file that can be shown rendered (Markdown).
struct EditorViewPicker: View {
    let view: EditorInfo.View
    let set: (EditorInfo.View) -> Void

    var body: some View {
        Picker("View", selection: Binding(get: { view }, set: set)) {
            Image(systemName: "chevron.left.forwardslash.chevron.right").tooltip("Source", label: "Source").tag(EditorInfo.View.source)
            Image(systemName: "rectangle.split.2x1").tooltip("Source and Preview", label: "Source and Preview").tag(EditorInfo.View.split)
            Image(systemName: "doc.richtext").tooltip("Preview", label: "Preview").tag(EditorInfo.View.preview)
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .controlSize(.mini)
        .fixedSize()
        .accessibilityLabel("View")
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
                    .listRowBackground(i == selection ? Color.themeAccent.opacity(0.2) : Color.clear)
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
        bridge.onOpenFile = { [model, paneID] path in EditorUI.shared.openLink(model, from: paneID, path) }
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

    // A file dragged from a files pane opens here, in this pane's place
    // (text dragged in still drops into the text, as the page has it).
    private func filesDrag(_ info: NSDraggingInfo) -> String? {
        guard info.draggingSource is FilesWK, let model, model.group(ofPane: paneID)?.canDrive != false,
              let urls = info.draggingPasteboard.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL],
              urls.count == 1, !urls[0].hasDirectoryPath else { return nil }
        return urls[0].path
    }

    override func draggingEntered(_ info: NSDraggingInfo) -> NSDragOperation {
        filesDrag(info) != nil ? .generic : super.draggingEntered(info)
    }

    override func draggingUpdated(_ info: NSDraggingInfo) -> NSDragOperation {
        filesDrag(info) != nil ? .generic : super.draggingUpdated(info)
    }

    override func performDragOperation(_ info: NSDraggingInfo) -> Bool {
        guard let path = filesDrag(info), let model else { return super.performDragOperation(info) }
        let id = paneID
        Task { _ = try? await model.openEditor(path, groupID: nil, replacePane: id) }
        return true
    }

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
        let ui = EditorUI.shared
        if !menu.items.isEmpty { menu.addItem(.separator()) }
        menu.addItem(ClosureItem("Find…", key: "find_in_page") { ui.run(model, self.paneID, "find") })
        if !ui.previewOnly(model, paneID) {
            menu.addItem(ClosureItem("Go to Line…", key: "editor_go_to_line") { ui.run(model, self.paneID, "gotoLine") })
        }
        if ui.canPreview(model, paneID) {
            menu.addItem(.separator())
            menu.addItem(ClosureItem(ui.previewLabel(model, paneID), key: "editor_toggle_preview") { ui.togglePreview(model, self.paneID) })
            menu.addItem(ClosureItem(ui.previewLabel(model, paneID, split: true), key: "editor_toggle_split_preview") { ui.togglePreview(model, self.paneID, split: true) })
        }
        menu.addItem(.separator())
        if canEdit {
            menu.addItem(ClosureItem("Save", key: "editor_save") { ui.save(model, self.paneID) })
            menu.addItem(ClosureItem("Save As…", key: "editor_save_as") { ui.save(model, self.paneID, saveAs: true) })
            if !ed.path.isEmpty {
                menu.addItem(ClosureItem(bridge?.session.showingDiff == true ? "Hide Comparison" : "Compare with File on Disk", key: "editor_show_diff") { ui.showDiff(model, self.paneID) })
                menu.addItem(ClosureItem("Reload from Disk", key: "editor_reload") { ui.reload(model, self.paneID) })
                menu.addItem(ClosureItem("Rename…", key: "editor_rename") { ui.rename(model, self.paneID) })
            }
            menu.addItem(.separator())
        }
        if !ed.path.isEmpty {
            menu.addItem(ClosureItem("Copy Path", key: nil) {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(ed.path, forType: .string)
            })
            if ui.canReveal(model, paneID) {
                menu.addItem(ClosureItem("Reveal in Finder", key: "editor_reveal_in_finder") { ui.revealInFinder(model, self.paneID) })
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
        if let key { showShortcut(key) }
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
                        .listRowBackground(i == selection ? Color.themeAccent.opacity(0.2) : Color.clear)
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
        Task { await EditorUI.shared.save(model, paneID, s, as: p, name: name) }
    }
}

// MARK: - Settings

/// Settings ▸ Editor (the editor-* keys).
struct EditorSettingsSection: View {
    private var prefs: Prefs { .shared }

    var body: some View {
        Section("Editor panes") {
            Toggle("Wrap long lines", isOn: prefs.boolBinding(.editorWrap))
                .settingAnchor("editor.wrap")
            Toggle("Line numbers", isOn: prefs.boolBinding(.editorLineNumbers))
                .settingAnchor("editor.linenumbers")
            Picker("Indent with", selection: prefs.stringBinding(.editorIndent)) {
                Text("Spaces").tag("spaces")
                Text("Tabs").tag("tabs")
            }
            .settingAnchor("editor.indent")
            Stepper("Tab width: \(Int(prefs.double(.editorTabWidth)))", value: prefs.doubleBinding(.editorTabWidth), in: 1...16, step: 1)
                .settingAnchor("editor.tabwidth")
            FontPicker(label: "Font", selection: prefs.stringBinding(.editorFontFamily), none: "Same as Terminal",
                       families: TerminalFont.families)
            .settingAnchor("editor.font")
            HStack {
                Toggle("Own font size", isOn: Binding(
                    get: { !prefs.string(.editorFontSize).isEmpty },
                    set: { prefs.set(.editorFontSize, $0 ? Pref.format(AppConfig.shared.fontSize ?? 13) : "") }))
                if !prefs.string(.editorFontSize).isEmpty {
                    Stepper("\(Int(prefs.double(.editorFontSize))) pt", value: prefs.doubleBinding(.editorFontSize), in: 6...36, step: 1)
                }
            }
            .settingAnchor("editor.fontsize")
            Toggle("Vim keys", isOn: prefs.boolBinding(.editorVim))
                .settingAnchor("editor.vim")
            Text(".editorconfig and a file's existing indentation take precedence.")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
    }
}
