import HammockKit
import SwiftUI
import WebKit

// Files panes on the iPhone: the Mac's page (files.html, in HammockKit's
// FilesBridge) at touch sizes. A tap opens a file in a pane beside it (an
// editor, or a browser for pages, images and PDFs); touch and hold has the
// rest, and Move to… picks a folder.

struct FilesScreen: View {
    let model: AppModel
    let paneID: String
    var canDrive = true

    @State private var lease: PaneLease<FilesSession>?
    private var session: FilesSession? { lease?.session }
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.phoneTheme) private var theme
    /// What touch and hold was on: the selection, and which are folders.
    @State private var menu: FilesMenuTarget?
    @State private var trashing: [String]?
    @State private var moving: [String]?

    private var pane: Pane? { model.panes[paneID] }
    private var info: FilesInfo { pane?.files ?? FilesInfo() }

    var body: some View {
        VStack(spacing: 0) {
            if let session {
                FilesWebViewIOS(session: session, config: config, info: info,
                                onOpen: { path, dir in open(path, dir: dir) },
                                onMenu: { paths, dirs in menu = FilesMenuTarget(paths: paths, dirs: dirs) },
                                onTrash: { trashing = $0 })
                    .overlay(alignment: .bottom) {
                        if !session.error.isEmpty {
                            Text(session.error)
                                .font(.callout)
                                .padding(.horizontal, 12).padding(.vertical, 8)
                                .background(.regularMaterial, in: Capsule())
                                .padding(12)
                                .onTapGesture { session.error = "" }
                        }
                    }
            } else {
                Color.clear
            }
        }
        .background(Color(uiColor: UIColor(rgb: theme.terminal(dark: colorScheme == .dark).background)))
        .onAppear { if lease == nil { lease = model.leaseFiles(paneID) } }
        .confirmationDialog(menu?.title ?? "", isPresented: Binding(get: { menu != nil }, set: { if !$0 { menu = nil } }),
                            titleVisibility: .visible, presenting: menu) { m in
            actions(m)
        }
        .confirmationDialog(trashTitle, isPresented: Binding(get: { trashing != nil }, set: { if !$0 { trashing = nil } }),
                            titleVisibility: .visible, presenting: trashing) { paths in
            Button("Move to Trash", role: .destructive) { session?.trash(paths) }
        }
        .sheet(item: Binding(get: { moving.map { MovePaths(paths: $0) } }, set: { moving = $0?.paths })) { m in
            if let session {
                FolderPicker(session: session, root: info.root, paths: m.paths) { dest in
                    session.move(m.paths, into: dest)
                    moving = nil
                }
            }
        }
    }

    private var config: FilesConfig {
        let dark = colorScheme == .dark
        return FilesConfig(theme: theme.terminal(dark: dark), dark: dark, fontSize: 15, canDrive: canDrive, accent: theme.accent)
    }

    private var trashTitle: String {
        let n = trashing?.count ?? 0
        return n == 1 ? "Move \((trashing![0] as NSString).lastPathComponent) to the Trash?" : "Move \(n) items to the Trash?"
    }

    @ViewBuilder private func actions(_ m: FilesMenuTarget) -> some View {
        let files = zip(m.paths, m.dirs).filter { !$0.1 }.map(\.0)
        if m.paths.isEmpty {
            if canDrive {
                Button("New File") { session?.command("newFile") }
                Button("New Folder") { session?.command("newFolder") }
            }
            Button(info.showHidden ? "Hide Hidden Files" : "Show Hidden Files") { session?.setHidden(!info.showHidden) }
            Button("Collapse Folders") { session?.command("collapseAll") }
            if !info.followGroup {
                Button("Back to the Group's Folder") { session?.setRoot("") }
            }
        } else {
            if files.count == 1 {
                Button("Open") { open(files[0], dir: false) }
                if FilesOpening.browserKinds.contains((files[0] as NSString).pathExtension.lowercased()) == false {
                    Button("Open in Browser") { openInBrowser(files[0]) }
                }
            }
            if m.paths.count == 1, m.dirs.first == true {
                Button("Show Only This Folder") { session?.setRoot(m.paths[0]) }
            }
            if canDrive {
                if m.paths.count == 1 { Button("Rename") { session?.command("rename") } }
                Button("New File") { session?.command("newFile") }
                Button("New Folder") { session?.command("newFolder") }
                Button("Duplicate") { session?.command("duplicate") }
                Button("Move to…") { moving = m.paths }
            }
            Button(m.paths.count == 1 ? "Copy Path" : "Copy Paths") { UIPasteboard.general.string = m.paths.joined(separator: "\n") }
            if canDrive {
                Button("Move to Trash", role: .destructive) { trashing = m.paths }
            }
        }
    }

    // MARK: Opening

    private func open(_ path: String, dir: Bool) {
        guard !dir else { return }
        let ext = (path as NSString).pathExtension.lowercased()
        if FilesOpening.browserKinds.contains(ext) { return openInBrowser(path) }
        if FilesOpening.binaryKinds.contains(ext) {
            session?.error = "\((path as NSString).lastPathComponent) opens on the computer it's on"
            return
        }
        Task { await openInEditor(path) }
    }

    private func openInBrowser(_ path: String) {
        Task {
            if let id = await model.splitBrowser(paneID, direction: .horizontal, url: URL(fileURLWithPath: path).absoluteString) { show(id) }
        }
    }

    /// Focuses a pane opened from here and slides the tab to it.
    private func show(_ id: String) {
        model.focus(id)
        PaneShowRequest.shared.paneID = id
    }

    /// In the editor this pane opened last (its file replaced), or a new one
    /// beside it; a file the tab has open already is just shown.
    private func openInEditor(_ path: String) async {
        if let p = model.panes[paneID], let tab = model.tab(of: p),
           let open = model.panes(in: tab).first(where: { $0.isEditor && $0.editor.path == path }) {
            show(open.id)
            return
        }
        do {
            if let ed = FilesOpening.linkedEditor[paneID], let p = model.panes[ed], p.isEditor, !p.editor.dirty {
                if let id = try await model.openEditor(path, groupID: nil, replacePane: ed) {
                    FilesOpening.linkedEditor[paneID] = id
                    show(id)
                }
                return
            }
            if let id = try await model.openEditor(path, groupID: nil, splitPane: paneID) {
                FilesOpening.linkedEditor[paneID] = id
                show(id)
            }
        } catch {
            session?.error = error.localizedDescription
        }
    }
}

/// What a file opens in, on a phone.
@MainActor
enum FilesOpening {
    static var linkedEditor: [String: String] = [:]
    static let browserKinds: Set<String> = ["html", "htm", "xhtml", "svg", "png", "jpg", "jpeg", "gif", "webp", "avif", "bmp", "ico", "pdf"]
    static let binaryKinds: Set<String> = [
        "heic", "tif", "tiff", "icns", "psd", "sketch", "mov", "mp4", "m4v", "avi", "mkv", "webm", "mp3", "m4a", "wav", "aiff", "flac",
        "zip", "gz", "tgz", "xz", "bz2", "7z", "rar", "dmg", "pkg", "ipa", "apk", "aab", "o", "a", "dylib", "so", "exe", "dll", "class",
        "jar", "wasm", "woff", "woff2", "ttf", "otf", "sqlite", "db", "key", "pages", "numbers", "docx", "xlsx", "pptx",
    ]
}

struct FilesMenuTarget {
    let paths: [String]
    let dirs: [Bool]

    var title: String {
        switch paths.count {
        case 0: ""
        case 1: (paths[0] as NSString).lastPathComponent
        default: "\(paths.count) items"
        }
    }
}

private struct MovePaths: Identifiable {
    let paths: [String]
    var id: String { paths.joined(separator: "\n") }
}

/// Move to…: a folder in the pane's folder, gone into a level at a time.
private struct FolderPicker: View {
    let session: FilesSession
    let root: String
    let paths: [String]
    let done: (String) -> Void
    @State private var folder = ""
    @State private var subfolders: [String] = []
    @State private var loading = true
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                if folder != root {
                    Button {
                        go((folder as NSString).deletingLastPathComponent)
                    } label: {
                        Label("Enclosing Folder", systemImage: "arrow.up")
                    }
                }
                ForEach(subfolders, id: \.self) { name in
                    let path = (folder as NSString).appendingPathComponent(name)
                    Button {
                        go(path)
                    } label: {
                        Label(name, systemImage: "folder")
                    }
                    .disabled(paths.contains(path))
                }
                if !loading && subfolders.isEmpty {
                    Text("No folders in here").foregroundStyle(.secondary)
                }
            }
            .navigationTitle((folder as NSString).lastPathComponent)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) { Button("Move Here") { done(folder) } }
            }
        }
        .task { go(root) }
    }

    private func go(_ path: String) {
        guard path == root || path.hasPrefix(root + "/") else { return }
        folder = path
        loading = true
        Task {
            let l = await session.listDir(path)
            if folder == path {
                subfolders = (l?.entries ?? []).filter { $0.dir && !$0.name.hasPrefix(".") }.map(\.name)
                loading = false
            }
        }
    }
}

struct FilesWebViewIOS: UIViewRepresentable {
    let session: FilesSession
    let config: FilesConfig
    let info: FilesInfo
    let onOpen: (String, Bool) -> Void
    let onMenu: ([String], [Bool]) -> Void
    let onTrash: ([String]) -> Void

    final class Coordinator {
        var bridge: FilesBridge?
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> WKWebView {
        let bridge = FilesBridge(session: session) { WKWebView(frame: .zero, configuration: $0) }
        context.coordinator.bridge = bridge
        bridge.onOpen = { path, dir, _ in onOpen(path, dir) }
        bridge.onMenu = { paths, dirs, _ in onMenu(paths, dirs) }
        // Trashing from the page's own key (a keyboard's ⌘⌫) asks first too.
        bridge.onOp = { [session] m in
            if m["type"] as? String == "trash" { onTrash(m["paths"] as? [String] ?? []) } else { session.op(m) }
        }
        bridge.configure(config)
        bridge.setInfo(info)
        return bridge.webView
    }

    func updateUIView(_ v: WKWebView, context: Context) {
        context.coordinator.bridge?.configure(config)
        context.coordinator.bridge?.setInfo(info)
    }
}
