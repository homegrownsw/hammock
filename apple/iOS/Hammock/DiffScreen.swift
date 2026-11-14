import HammockKit
import SwiftUI
import WebKit

// Diff panes on the iPhone: the same page as the Mac's (diff.html, in
// HammockKit's DiffBridge), narrow: one column, the files' tree a sheet
// over them (its button in the page's bar). Comments, replies and sending
// them to the agent work as they do on the Mac.

struct DiffScreen: View {
    let model: AppModel
    let paneID: String
    var canDrive = true

    @State private var lease: PaneLease<DiffSession>?
    private var session: DiffSession? { lease?.session }
    @Environment(\.colorScheme) private var colorScheme
    @Environment(\.phoneTheme) private var theme

    private var pane: Pane? { model.panes[paneID] }
    private var info: DiffInfo { pane?.diff ?? DiffInfo() }

    var body: some View {
        VStack(spacing: 0) {
            if let session {
                DiffWebViewIOS(session: session, config: config, info: info)
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
        // (No menu of its own: the pane's is there, and Refresh is in the page.)
        .background(Color(uiColor: UIColor(rgb: theme.terminal(dark: colorScheme == .dark).background)))
        .onAppear { if lease == nil { lease = model.leaseDiff(paneID) } }
    }

    private var config: DiffConfig {
        let dark = colorScheme == .dark
        let agents = model.diffAgents(for: paneID)
        let canSend = model.group(ofPane: paneID).map { !$0.isRemote || $0.myRole == .owner } ?? true
        // One column here, whatever the pane's choice: split needs width.
        var view = info.view
        view.style = "unified"
        return .from(theme: theme.terminal(dark: dark), dark: dark, fontFamily: TerminalPrefs.family, fontSize: 12, view: view,
                     canDrive: canDrive, target: agents.target, agents: agents.agents, canSend: canSend, accent: theme.accent)
    }
}

struct DiffWebViewIOS: UIViewRepresentable {
    let session: DiffSession
    let config: DiffConfig
    let info: DiffInfo

    final class Coordinator {
        var bridge: DiffBridge?
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    func makeUIView(context: Context) -> WKWebView {
        let bridge = DiffBridge(session: session) { WKWebView(frame: .zero, configuration: $0) }
        context.coordinator.bridge = bridge
        bridge.configure(config)
        bridge.setInfo(info)
        return bridge.webView
    }

    func updateUIView(_ v: WKWebView, context: Context) {
        context.coordinator.bridge?.configure(config)
        context.coordinator.bridge?.setInfo(info)
    }
}
