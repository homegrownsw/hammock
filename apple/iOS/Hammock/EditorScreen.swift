import HammockKit
import SwiftUI
import UIKit
import WebKit

/// An editor pane on the phone: the same CodeMirror page as the Mac's
/// (EditorBridge), editing the daemon's buffer. A bar above the keyboard
/// has the keys a phone's keyboard lacks.
struct EditorScreen: View {
    let model: AppModel
    let paneID: String
    var canDrive = true

    @State private var session: EditorSession?
    @State private var saveAsPath = ""
    @State private var askingSaveAs = false
    @State private var problem: String?
    @State private var confirm: Confirm?
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.phoneTheme) private var theme

    private enum Confirm: Identifiable {
        case overwrite(path: String, message: String), reload
        var id: String {
            switch self {
            case .overwrite(let p, _): "o" + p
            case .reload: "r"
            }
        }
    }

    private var pane: Pane? { model.panes[paneID] }
    private var info: EditorInfo { pane?.editor ?? EditorInfo() }
    private var editable: Bool { canDrive && !info.readOnly }

    var body: some View {
        VStack(spacing: 0) {
            if let session {
                banner(session)
                EditorWebViewIOS(session: session, config: config, info: info, readOnly: !canDrive) { saveAs in
                    if saveAs || info.path.isEmpty { startSaveAs() } else { save() }
                }
            } else {
                Color.clear
            }
        }
        .background(Color(uiColor: UIColor(rgb: theme.terminal(dark: colorScheme == .dark).background)))
        .safeAreaInset(edge: .bottom) { if editable, let session { keyBar(session) } }
        .toolbar {
            ToolbarItem(placement: .primaryAction) { menu }
        }
        .onAppear { if session == nil { session = model.acquireEditor(paneID) } }
        .onDisappear {
            if session != nil {
                model.releaseEditor(paneID)
                session = nil
            }
        }
        .alert("Save As", isPresented: $askingSaveAs) {
            TextField("notes.md", text: $saveAsPath)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
            Button("Save") { save(as: saveAsPath) }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("A path in \(info.folder.isEmpty ? "the Group's folder" : (info.folder as NSString).lastPathComponent), or a full path.")
        }
        .alert(problem ?? "", isPresented: Binding(get: { problem != nil }, set: { if !$0 { problem = nil } })) {
            Button("OK", role: .cancel) {}
        }
        .confirmationDialog(confirmTitle, isPresented: Binding(get: { confirm != nil }, set: { if !$0 { confirm = nil } }), titleVisibility: .visible) {
            switch confirm {
            case .overwrite(let path, _):
                Button("Save Anyway", role: .destructive) { save(as: path, force: true) }
            case .reload:
                Button("Discard My Changes", role: .destructive) { Task { try? await session?.reload() } }
            case nil:
                EmptyView()
            }
        }
    }

    private var confirmTitle: String {
        switch confirm {
        case .overwrite(_, let m): m
        case .reload: "Discard your changes to \(info.name)?"
        case nil: ""
        }
    }

    private var config: EditorConfig {
        let dark = colorScheme == .dark
        return .from(theme: theme.terminal(dark: dark), dark: dark, fontFamily: "SF Mono", fontSize: 13, readOnly: !canDrive)
    }

    private var menu: some View {
        Menu {
            if canDrive {
                if editable {
                    Button("Save", systemImage: "square.and.arrow.down") { info.path.isEmpty ? startSaveAs() : save() }
                }
                Button("Save As…", systemImage: "square.and.arrow.down.on.square") { startSaveAs() }
            }
            Button("Find", systemImage: "magnifyingglass") { session?.run("find") }
            Button("Go to Line", systemImage: "arrow.right.to.line") { session?.run("gotoLine") }
            if let session {
                Menu("Language: \(session.languageName.isEmpty ? "Plain Text" : session.languageName)", systemImage: "chevron.left.forwardslash.chevron.right") {
                    Button("Detect from Name") { session.setLanguage("") }
                    ForEach(session.languageNames, id: \.self) { n in Button(n) { session.setLanguage(n) } }
                }
                .disabled(!canDrive)
            }
            if !info.path.isEmpty {
                Button(session?.showingDiff == true ? "Hide Comparison" : "Compare with File on Disk", systemImage: "arrow.left.arrow.right") { toggleDiff() }
                if editable {
                    Button("Reload from Disk", systemImage: "arrow.clockwise") {
                        if info.dirty { confirm = .reload } else { Task { try? await session?.reload() } }
                    }
                }
            }
        } label: {
            Label("More", systemImage: info.dirty ? "ellipsis.circle.fill" : "ellipsis.circle")
        }
    }

    @ViewBuilder private func banner(_ s: EditorSession) -> some View {
        if s.showingDiff {
            bannerRow("Your version, against the file on disk.", tint: .blue) { Button("Done") { s.hideDiff() } }
        } else if info.conflict {
            bannerRow("\(info.name) changed on disk.", tint: .orange) {
                Button("Compare") { toggleDiff() }
                Button("Reload") { confirm = .reload }
                Button("Keep Mine") { Task { try? await s.keepMine() } }
            }
        } else if info.readOnly, canDrive {
            bannerRow("\(info.name) is read-only: you don't have permission to change it.", tint: .secondary) {
                Button("Save As…") { startSaveAs() }
            }
        } else if info.dirty {
            HStack(spacing: 6) {
                Circle().fill(.secondary).frame(width: 6, height: 6)
                Text("Edited").font(.caption).foregroundStyle(.secondary)
                Spacer()
            }
            .padding(.horizontal, 12).padding(.vertical, 3)
        }
    }

    private func bannerRow<B: View>(_ text: String, tint: Color, @ViewBuilder buttons: () -> B) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(text).font(.callout)
            HStack(spacing: 12) { buttons() }.font(.callout.weight(.semibold)).buttonStyle(.borderless)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 12).padding(.vertical, 8)
        .background(tint.opacity(0.12))
    }

    /// The keys a phone's keyboard hasn't got.
    private func keyBar(_ s: EditorSession) -> some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 4) {
                key("arrow.right.to.line", "Tab") { s.run("tab") }
                key("decrease.indent", "Outdent") { s.run("outdent") }
                key("arrow.left", "Left") { s.run("left") }
                key("arrow.right", "Right") { s.run("right") }
                key("arrow.up", "Up") { s.run("up") }
                key("arrow.down", "Down") { s.run("down") }
                key("arrow.uturn.backward", "Undo") { s.run("undo") }
                key("arrow.uturn.forward", "Redo") { s.run("redo") }
                key("magnifyingglass", "Find") { s.run("find") }
                key("square.and.arrow.down", "Save") { info.path.isEmpty ? startSaveAs() : save() }
                key("keyboard.chevron.compact.down", "Hide Keyboard") { s.run("blur") }
            }
            .padding(.horizontal, 8)
        }
        .padding(.vertical, 6)
        .background(.bar)
    }

    private func key(_ symbol: String, _ label: String, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: symbol)
                .frame(width: 40, height: 34)
                .background(.quaternary, in: RoundedRectangle(cornerRadius: 7))
        }
        .buttonStyle(.plain)
        .accessibilityLabel(label)
    }

    // MARK: Actions

    private func startSaveAs() {
        saveAsPath = info.path.isEmpty ? "" : (info.path.hasPrefix(info.folder + "/") ? String(info.path.dropFirst(info.folder.count + 1)) : info.path)
        askingSaveAs = true
    }

    private func save(as path: String = "", force: Bool = false) {
        guard let s = session else { return }
        Task {
            do {
                try await s.save(as: path, force: force)
            } catch {
                let m = error.localizedDescription
                if !force, m.contains("already exists") || m.contains("changed on disk") {
                    confirm = .overwrite(path: path, message: m.contains("changed on disk") ? "\(info.name) changed on disk since. Save yours over it?" : m)
                } else {
                    problem = m
                }
            }
        }
    }

    private func toggleDiff() {
        guard let s = session else { return }
        if s.showingDiff { s.hideDiff(); return }
        Task {
            do { try await s.showDiff() } catch { problem = error.localizedDescription }
        }
    }
}

private extension UIColor {
    convenience init(rgb c: RGB) {
        self.init(red: CGFloat(c.r) / 255, green: CGFloat(c.g) / 255, blue: CGFloat(c.b) / 255, alpha: 1)
    }
}

struct EditorWebViewIOS: UIViewRepresentable {
    let session: EditorSession
    let config: EditorConfig
    let info: EditorInfo
    let readOnly: Bool
    var onSave: (Bool) -> Void

    final class Coordinator {
        var bridge: EditorBridge?
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> WKWebView {
        let bridge = EditorBridge(session: session) { WKWebView(frame: .zero, configuration: $0) }
        context.coordinator.bridge = bridge
        bridge.onSave = onSave
        bridge.configure(config)
        bridge.setInfo(info, readOnly: readOnly)
        return bridge.webView
    }

    func updateUIView(_ v: WKWebView, context: Context) {
        context.coordinator.bridge?.onSave = onSave
        context.coordinator.bridge?.configure(config)
        context.coordinator.bridge?.setInfo(info, readOnly: readOnly)
    }
}
