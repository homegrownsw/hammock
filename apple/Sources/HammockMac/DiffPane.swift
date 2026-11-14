import AppKit
import HammockKit
import SwiftUI
import WebKit

// Diff panes on the Mac: a git diff to review, like a pull request's Files
// Changed: the page (DiffBridge, in HammockKit) under the pane's header.

@MainActor
enum DiffUI {
    /// Opens a diff of the repository the focused pane is in, in a new tab
    /// just after the current one. source nil: what a pull request from its
    /// branch would show (uncommitted changes too), else what's uncommitted.
    static func review(_ model: AppModel, source: Hammock_V1_DiffSource? = nil) {
        guard let group = model.currentGroup ?? model.groups.first else { return }
        let tab = model.selectedTab
        Task {
            do {
                try await model.openDiff(repo: repoFolder(model) ?? "", groupID: group.id, afterTab: tab?.id, source: source)
            } catch {
                let a = NSAlert()
                a.messageText = "Couldn't show the changes"
                a.informativeText = error.localizedDescription
                a.runModal()
            }
        }
    }

    /// Where to look for a repository: the focused pane's folder (an
    /// editor's file's, a diff's repository), else the Group's.
    static func repoFolder(_ model: AppModel) -> String? {
        if let id = model.selectedTab?.focusedPaneID, let p = model.panes[id] {
            if p.isEditor, !p.editor.path.isEmpty { return (p.editor.path as NSString).deletingLastPathComponent }
            if p.isDiff, !p.diff.repo.isEmpty { return p.diff.repo }
            if !p.cwd.isEmpty { return p.cwd }
        }
        if let g = model.currentGroup, !g.folder.isEmpty { return g.folder }
        return nil
    }

    static var uncommitted: Hammock_V1_DiffSource { .with { $0.kind = .uncommitted } }
    static var branch: Hammock_V1_DiffSource { .with { $0.kind = .branch; $0.withUncommitted = true } }
    static var lastCommit: Hammock_V1_DiffSource { .with { $0.kind = .commit; $0.rev = "HEAD" } }
}

struct DiffPaneView: View {
    let model: AppModel
    let paneID: String
    let isFocused: Bool
    @State private var lease: PaneLease<DiffSession>?
    private var session: DiffSession? { lease?.session }
    @Environment(\.colorScheme) private var colorScheme

    private var pane: Pane? { model.panes[paneID] }
    private var info: DiffInfo { pane?.diff ?? DiffInfo() }
    private var canDrive: Bool { model.group(ofPane: paneID)?.canDrive != false && model.group(ofPane: paneID)?.offline != true }

    var body: some View {
        VStack(spacing: 0) {
            header
                .contextMenu { PaneMenuItems(model: model, paneID: paneID) }
                .paneSwapHandle(paneID, model: model)
            if let session {
                DiffWebViewRep(model: model, session: session, paneID: paneID, isFocused: isFocused, config: config, info: info)
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
        .onAppear { if lease == nil { lease = model.leaseDiff(paneID) } }
    }

    private var config: DiffConfig {
        let dark = colorScheme == .dark
        let c = AppConfig.shared
        let agents = model.diffAgents(for: paneID)
        // Sending to an agent is the Group's owner's (a guest comments only).
        let canSend = model.group(ofPane: paneID).map { !$0.isRemote || $0.myRole == .owner } ?? true
        return .from(theme: c.theme(isDark: dark), dark: dark, fontFamily: c.fontFamily ?? "SF Mono", fontSize: c.fontSize ?? 13,
                     view: info.view, canDrive: canDrive, target: agents.target, agents: agents.agents, canSend: canSend,
                     accent: .themeAccent)
    }

    private var header: some View {
        HStack(spacing: 8) {
            Image(systemName: "plusminus")
                .foregroundStyle(.secondary)
                .frame(width: 14)
            PaneKindMenu(model: model, paneID: paneID) {
                Text("Changes").fontWeight(.semibold)
            }
            .layoutPriority(1) // the folder gives way first
            Text((info.repo as NSString).abbreviatingWithTildeInPath)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.head)
            Spacer(minLength: 6)
            if info.unread > 0 {
                Button {
                    session?.showNextUnread()
                } label: {
                    Text(info.unread == 1 ? "1 new reply" : "\(info.unread) new replies")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 7).padding(.vertical, 2)
                        .background(Color.themeAccent, in: Capsule())
                }
                .buttonStyle(.plain)
                .help("Go to the next reply you haven't seen (N in the diff)")
            }
            if info.queued > 0 {
                Text("\(info.queued) waiting to send")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 7).padding(.vertical, 2)
                    .background(.quaternary, in: Capsule())
                    .help("Comments held until the agent has your answer: it's waiting on you")
            }
            if session?.loading == true {
                ProgressView().controlSize(.mini)
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
}

struct DiffWebViewRep: NSViewRepresentable {
    let model: AppModel
    let session: DiffSession
    let paneID: String
    let isFocused: Bool
    let config: DiffConfig
    let info: DiffInfo

    final class Coordinator {
        var bridge: DiffBridge?
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeNSView(context: Context) -> DiffWK {
        var made: DiffWK?
        let bridge = DiffBridge(session: session) { conf in
            let v = DiffWK(frame: .zero, configuration: conf)
            made = v
            return v
        }
        context.coordinator.bridge = bridge
        let v = made!
        v.bridge = bridge
        v.model = model
        v.paneID = paneID
        bridge.onFocus = { [model, paneID] in model.viewFocused(paneID) }
        bridge.onOpenFile = { [model, paneID] path in
            guard let p = model.panes[paneID], p.isDiff else { return }
            let full = (p.diff.repo as NSString).appendingPathComponent(path)
            guard HammockTool.editor.on else {
                // Turned off in Settings ▸ Tools: the file's own app, as a
                // terminal's file links open (a file that would run something
                // is shown in the Finder). Not a shared Group's: it's on its host.
                if model.group(ofPane: paneID)?.isRemote != true { FileLink.open(full) }
                return
            }
            EditorUI.shared.open(model, full, groupID: nil, splitPane: paneID, direction: .horizontal)
        }
        bridge.configure(config)
        bridge.setInfo(info)
        v.isFocusTarget = isFocused
        return v
    }

    func updateNSView(_ v: DiffWK, context: Context) {
        context.coordinator.bridge?.configure(config)
        context.coordinator.bridge?.setInfo(info)
        v.isFocusTarget = isFocused
    }
}

/// The page's view: focus as the pane's, and a context menu without the
/// browser's items.
final class DiffWK: WKWebView {
    weak var bridge: DiffBridge?
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
        }
    }

    override func willOpenMenu(_ menu: NSMenu, with event: NSEvent) {
        let drop: Set<String> = ["WKMenuItemIdentifierReload", "WKMenuItemIdentifierGoBack", "WKMenuItemIdentifierGoForward",
                                 "WKMenuItemIdentifierOpenLinkInNewWindow", "WKMenuItemIdentifierDownloadLinkedFile",
                                 "WKMenuItemIdentifierOpenImageInNewWindow", "WKMenuItemIdentifierDownloadImage",
                                 "WKMenuItemIdentifierOpenFrameInNewWindow", "WKMenuItemIdentifierShareMenu"]
        for item in menu.items where drop.contains(item.identifier?.rawValue ?? "") { menu.removeItem(item) }
        while menu.items.first?.isSeparatorItem == true { menu.removeItem(at: 0) }
        guard let model, let s = bridge?.session else { return }
        if !menu.items.isEmpty { menu.addItem(.separator()) }
        menu.addItem(ClosureItem("Refresh", key: nil) { s.load(refresh: true) })
        if let p = model.panes[paneID], p.isDiff, !p.diff.repo.isEmpty {
            menu.addItem(ClosureItem("Copy Repository Path", key: nil) {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(p.diff.repo, forType: .string)
            })
        }
    }
}

// MARK: - Settings

/// Settings ▸ Editor: diff panes (the diff-* keys).
struct DiffSettingsSection: View {
    private var prefs: Prefs { .shared }

    var body: some View {
        Section("Diff panes") {
            Picker("Layout", selection: prefs.stringBinding(.diffStyle)) {
                Text("Unified").tag("unified")
                Text("Split").tag("split")
            }
            .settingAnchor("diff.style")
            Toggle("Ignore whitespace", isOn: Binding(
                get: { prefs.string(.diffWhitespace) == "ignore" },
                set: { prefs.set(.diffWhitespace, $0 ? "ignore" : "show") }))
                .settingAnchor("diff.whitespace")
            Toggle("Wrap long lines", isOn: prefs.boolBinding(.diffWrap))
                .settingAnchor("diff.wrap")
            Text("A diff pane's own choices, from its settings button, take precedence.")
                .font(.callout)
                .foregroundStyle(.secondary)
        }
    }
}
