import AppKit
import HammockKit
import SwiftUI
import UniformTypeIdentifiers
import WebKit

// Files panes on the Mac: a folder's tree (FilesBridge's page, in
// HammockKit) under the pane's header, its menus this Mac's own.

@MainActor
enum FilesUI {
    /// The editor each files pane opens files in, while it's still there:
    /// opening another file shows it there rather than adding a pane.
    static var linkedEditor: [String: String] = [:]

    /// A new tab of the Group's folder.
    static func newTab(_ model: AppModel, groupID: String? = nil) {
        guard let g = groupID ?? model.currentGroup?.id ?? model.groups.first?.id else { return }
        Task { await model.openFiles(groupID: g) }
    }

    /// Beside paneID: on its left, as an editor's file explorer is.
    static func split(_ model: AppModel, _ paneID: String, direction: SplitDirection = .horizontal, before: Bool = true) {
        Task { await model.openFiles(groupID: nil, splitPane: paneID, direction: direction, before: before) }
    }

    /// Shows path in the files pane in paneID's tab, or one opened on its left.
    static func reveal(_ model: AppModel, path: String, besides paneID: String) {
        if let f = model.filesPane(besides: paneID) {
            model.revealInFiles(pane: f, path: path)
            model.focus(f)
            return
        }
        Task { await model.openFiles(reveal: path, groupID: nil, splitPane: paneID, direction: .horizontal, before: true) }
    }

    /// What Reveal in Files shows for a pane: an editor's file, else its folder.
    static func revealPath(_ model: AppModel, _ paneID: String?) -> String? {
        guard let id = paneID, let p = model.panes[id], !p.isFiles else { return nil }
        if p.isEditor { return p.editor.path.isEmpty ? nil : p.editor.path }
        if p.isDiff { return p.diff.repo.isEmpty ? nil : p.diff.repo }
        return p.cwd.isEmpty ? nil : p.cwd
    }

    /// Whether the pane's files are on this Mac (Finder and other apps can
    /// open them), not a guest's or a linked computer's.
    static func isLocal(_ model: AppModel, _ paneID: String) -> Bool {
        guard let g = model.group(ofPane: paneID) else { return true }
        return !(g.isRemote || model.isOnLinkedComputer(g))
    }

    // MARK: What a file opens in

    enum Target { case editor, newEditor, browser, terminal, diff, finder, defaultApp }

    /// Shown rendered by a browser pane.
    static let browserKinds: Set<String> = ["html", "htm", "xhtml", "svg", "png", "jpg", "jpeg", "gif", "webp", "avif", "bmp", "ico", "pdf"]
    /// Not text: opened in their own app rather than an editor.
    static let binaryKinds: Set<String> = [
        "heic", "tif", "tiff", "icns", "psd", "sketch", "fig", "mov", "mp4", "m4v", "avi", "mkv", "webm", "mp3", "m4a", "wav", "aiff", "flac",
        "zip", "gz", "tgz", "xz", "bz2", "7z", "rar", "dmg", "pkg", "ipa", "apk", "aab", "app", "o", "a", "dylib", "so", "exe", "dll", "class",
        "jar", "wasm", "woff", "woff2", "ttf", "otf", "sqlite", "db", "key", "pages", "numbers", "docx", "xlsx", "pptx", "doc", "xls", "ppt",
    ]

    /// What double-click (Return) opens a file in.
    static func defaultTarget(_ path: String, local: Bool) -> Target {
        let ext = (path as NSString).pathExtension.lowercased()
        if browserKinds.contains(ext) { return .browser }
        if binaryKinds.contains(ext) { return local ? .defaultApp : .finder }
        return .editor
    }

    static func open(_ model: AppModel, filesPane: String, paths: [String], dirs: [Bool] = [], target: Target? = nil) {
        for (i, path) in paths.enumerated() {
            open(model, filesPane: filesPane, path: path, dir: i < dirs.count ? dirs[i] : false, target: target)
        }
    }

    static func open(_ model: AppModel, filesPane: String, path: String, dir: Bool, target: Target? = nil) {
        let local = isLocal(model, filesPane)
        let t = target ?? (dir ? .terminal : defaultTarget(path, local: local))
        let folder = dir ? path : (path as NSString).deletingLastPathComponent
        switch t {
        case .editor, .newEditor:
            Task { await openInEditor(model, filesPane: filesPane, path: path, reuse: t == .editor) }
        case .browser:
            Task { await model.splitBrowser(filesPane, direction: .horizontal, url: URL(fileURLWithPath: path).absoluteString) }
        case .terminal:
            Task { await model.splitTerminal(filesPane, cwd: folder) }
        case .diff:
            Task {
                do { try await model.openDiff(repo: folder, groupID: nil, splitPane: filesPane) } catch {
                    model.filesSession(filesPane)?.error = PaneNotices.why(error)
                }
            }
        case .finder:
            if local { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)]) }
        case .defaultApp:
            if local { NSWorkspace.shared.open(URL(fileURLWithPath: path)) }
        }
    }

    /// Opens path in this files pane's editor (its file replaced), or a new
    /// one on its right; a file already open in the tab is just shown.
    static func openInEditor(_ model: AppModel, filesPane: String, path: String, reuse: Bool) async {
        if let p = model.panes[filesPane], let tab = model.tab(of: p),
           let open = model.panes(in: tab).first(where: { $0.isEditor && $0.editor.path == path }) {
            model.focus(open.id)
            return
        }
        do {
            if reuse, let ed = linkedEditor[filesPane], let p = model.panes[ed], p.isEditor, !p.editor.dirty,
               model.panes[filesPane].map({ model.tab(of: $0)?.id }) == model.tab(of: p)?.id {
                if let id = try await model.openEditor(path, groupID: nil, replacePane: ed) { linkedEditor[filesPane] = id }
                return
            }
            if let id = try await model.openEditor(path, groupID: nil, splitPane: filesPane, direction: .horizontal) {
                linkedEditor[filesPane] = id
                model.focus(id)
            }
        } catch {
            model.filesSession(filesPane)?.error = PaneNotices.why(error)
        }
    }

    // MARK: The right-click menu

    static func menu(_ model: AppModel, _ session: FilesSession, paths: [String], dirs: [Bool]) -> NSMenu {
        let pane = session.paneID
        let menu = NSMenu()
        let local = isLocal(model, pane)
        let info = model.panes[pane]?.files ?? FilesInfo()
        let one = paths.count == 1 ? paths[0] : nil
        let oneDir = one != nil && dirs.first == true
        let add = { (title: String, run: @escaping () -> Void) in menu.addItem(ClosureItem(title, key: nil, run: run)) }
        if !paths.isEmpty {
            let files = zip(paths, dirs + Array(repeating: false, count: max(0, paths.count - dirs.count))).filter { !$0.1 }.map(\.0)
            if !files.isEmpty {
                add(files.count == 1 ? "Open" : "Open \(files.count) Files") { open(model, filesPane: pane, paths: files) }
                let openIn = NSMenu()
                let item = { (title: String, t: Target) in
                    openIn.addItem(ClosureItem(title, key: nil) { open(model, filesPane: pane, paths: files, target: t) })
                }
                if HammockTool.editor.on {
                    item("Editor", .editor)
                    item("New Editor Pane", .newEditor)
                }
                if HammockTool.browser.on { item("Browser", .browser) }
                if local { item("Default App", .defaultApp) }
                let sub = NSMenuItem(title: "Open In", action: nil, keyEquivalent: "")
                sub.submenu = openIn
                menu.addItem(sub)
            }
            if oneDir, let one {
                add("Show Only This Folder") { session.setRoot(one) }
            }
            if let one {
                let folder = oneDir ? one : (one as NSString).deletingLastPathComponent
                add("Open Terminal Here") { open(model, filesPane: pane, path: folder, dir: true, target: .terminal) }
                if HammockTool.diff.on, !info.repo.isEmpty {
                    add("Show Changes") { open(model, filesPane: pane, path: folder, dir: true, target: .diff) }
                }
            }
            if local {
                add("Reveal in Finder") { NSWorkspace.shared.activateFileViewerSelecting(paths.map { URL(fileURLWithPath: $0) }) }
            }
            menu.addItem(.separator())
            add(paths.count == 1 ? "Copy Path" : "Copy Paths") { copy(paths.joined(separator: "\n")) }
            add(paths.count == 1 ? "Copy Relative Path" : "Copy Relative Paths") {
                copy(paths.map { relative($0, to: info.repo.isEmpty ? info.root : info.repo) }.joined(separator: "\n"))
            }
        } else {
            add("Open Terminal Here") { open(model, filesPane: pane, path: info.root, dir: true, target: .terminal) }
            if local {
                add("Reveal in Finder") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: info.root)]) }
            }
            menu.addItem(.separator())
            add(info.showHidden ? "Hide Hidden Files" : "Show Hidden Files") { session.setHidden(!info.showHidden) }
            add("Collapse Folders") { session.command("collapseAll") }
            if !info.followGroup {
                add("Back to the Group's Folder") { session.setRoot("") }
            }
            add("Copy Path") { copy(info.root) }
        }
        return menu
    }

    static func copy(_ s: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
    }

    static func relative(_ path: String, to base: String) -> String {
        guard !base.isEmpty, path.hasPrefix(base + "/") else { return path }
        return String(path.dropFirst(base.count + 1))
    }
}

struct FilesPaneView: View {
    let model: AppModel
    let paneID: String
    let isFocused: Bool
    @State private var lease: PaneLease<FilesSession>?
    private var session: FilesSession? { lease?.session }
    @Environment(\.colorScheme) private var colorScheme

    private var pane: Pane? { model.panes[paneID] }
    private var info: FilesInfo { pane?.files ?? FilesInfo() }
    private var canDrive: Bool { model.group(ofPane: paneID)?.canDrive != false && model.group(ofPane: paneID)?.offline != true }

    var body: some View {
        VStack(spacing: 0) {
            header
                .contextMenu { PaneMenuItems(model: model, paneID: paneID) }
                .paneSwapHandle(paneID, model: model)
            PaneErrorBanner(paneID: paneID)
            if let session {
                FilesWebViewRep(model: model, session: session, paneID: paneID, isFocused: isFocused, config: config, info: info)
                    .overlay(alignment: .bottom) {
                        PanePopup(text: Bindable(session).error)
                    }
            } else {
                Color.clear
            }
        }
        .animation(.easeOut(duration: 0.2), value: PaneNotices.shared.banners[paneID]?.id)
        .background(PaneWindowReader(paneID: paneID))
        .onAppear { if lease == nil { lease = model.leaseFiles(paneID) } }
    }

    private var config: FilesConfig {
        let dark = colorScheme == .dark
        let c = AppConfig.shared
        var conf = FilesConfig(theme: c.theme(isDark: dark), dark: dark, fontSize: 13, canDrive: canDrive, accent: .themeAccent,
                               home: FilesUI.isLocal(model, paneID) ? NSHomeDirectory() : "")
        conf.ops = false // renaming, creating and moving come with the daemon's file operations
        return conf
    }

    private var header: some View {
        HStack(spacing: 8) {
            Image(systemName: "folder")
                .foregroundStyle(.secondary)
                .frame(width: 14)
            PaneKindMenu(model: model, paneID: paneID) {
                Text("Files").fontWeight(.semibold)
            }
            .layoutPriority(1)
            Spacer(minLength: 6)
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
}

struct FilesWebViewRep: NSViewRepresentable {
    let model: AppModel
    let session: FilesSession
    let paneID: String
    let isFocused: Bool
    let config: FilesConfig
    let info: FilesInfo

    final class Coordinator {
        var bridge: FilesBridge?
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> FilesWK {
        var made: FilesWK?
        let bridge = FilesBridge(session: session) { conf in
            let v = FilesWK(frame: .zero, configuration: conf)
            made = v
            return v
        }
        context.coordinator.bridge = bridge
        let v = made!
        v.bridge = bridge
        v.paneID = paneID
        bridge.onFocus = { [model, paneID] in model.viewFocused(paneID) }
        bridge.onOpen = { [model, paneID] path, dir, _ in
            FilesUI.open(model, filesPane: paneID, path: path, dir: dir)
        }
        bridge.onMenu = { [weak v, model, session] paths, dirs, point in
            guard let v else { return }
            FilesUI.menu(model, session, paths: paths, dirs: dirs).popUp(positioning: nil, at: point, in: v)
        }
        bridge.configure(config)
        bridge.setInfo(info)
        v.isFocusTarget = isFocused
        return v
    }

    func updateNSView(_ v: FilesWK, context: Context) {
        context.coordinator.bridge?.configure(config)
        context.coordinator.bridge?.setInfo(info)
        v.isFocusTarget = isFocused
    }
}

/// The page's view: focus as the pane's.
final class FilesWK: WKWebView {
    weak var bridge: FilesBridge?
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
                self.bridge?.focus()
            }
        }
    }
}
