import HammockKit
import SwiftUI

/// A tab of several panes: a card for each, laid out as the tab is on the
/// Mac (side by side or stacked, in their proportions). Tap one to open it.
struct PaneOverview: View {
    let model: AppModel
    let tab: WorkspaceTab
    let open: (String) -> Void
    @State private var closing: Pane?

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            OverviewLayout(model: model, layout: tab.layout, focused: tab.focusedPaneID, open: open) { closing = $0 }
            if !tab.floating.isEmpty {
                FloatingRow(model: model, tab: tab, open: open) { closing = $0 }
            }
        }
            .padding(12)
            .background(Color(uiColor: .systemGroupedBackground))
            .confirmationDialog(closing.map { "Close \($0.displayName)?" } ?? "", isPresented: Binding(get: { closing != nil }, set: { if !$0 { closing = nil } }),
                                titleVisibility: .visible, presenting: closing) { p in
                Button("Close Pane", role: .destructive) { Task { await model.closePane(p.id) } }
            } message: { p in
                Text(closeWarning(p) ?? "")
            }
    }
}

private struct OverviewLayout: View {
    let model: AppModel
    let layout: PaneLayout
    let focused: String
    let open: (String) -> Void
    /// Asks before closing a pane with something running.
    let confirmClose: (Pane) -> Void

    var body: some View {
        switch layout.node {
        case .paneID(let id):
            PaneCard(model: model, paneID: id, focused: id == focused)
                .contentShape(.contextMenuPreview, RoundedRectangle(cornerRadius: 14))
                .contentShape(RoundedRectangle(cornerRadius: 14))
                .onTapGesture { open(id) }
                .contextMenu { actions(id) }
        case .split(let s):
            let side = s.direction != .vertical
            let n = s.children.count
            let w = s.weights.count == n ? s.weights.map(CGFloat.init) : Array(repeating: 1 / CGFloat(n), count: n)
            GeometryReader { geo in
                let gap: CGFloat = 10
                let total = (side ? geo.size.width : geo.size.height) - gap * CGFloat(max(n - 1, 0))
                let stack = side ? AnyLayout(HStackLayout(spacing: gap)) : AnyLayout(VStackLayout(spacing: gap))
                stack {
                    ForEach(Array(s.children.enumerated()), id: \.element.stableKey) { i, child in
                        AnyView(OverviewLayout(model: model, layout: child, focused: focused, open: open, confirmClose: confirmClose))
                            .frame(width: side ? w[i] * total : nil, height: side ? nil : w[i] * total)
                    }
                }
            }
        case nil:
            Color.clear
        }
    }

    /// A pane's long-press menu.
    @ViewBuilder private func actions(_ id: String) -> some View {
        let pane = model.panes[id]
        let drive = model.group(ofPane: id)?.canDrive != false
        Button("Open", systemImage: "arrow.up.left.and.arrow.down.right") { open(id) }
        if let pane, pane.hasAgent, !pane.exited {
            Section {
                if pane.agent.state == .working || pane.agent.state == .needsInput {
                    Button("Interrupt", systemImage: "stop.circle") { model.send(id, Data([0x1B])) }
                }
                if AgentCatalog.canSwitch(pane.agent.harness) {
                    ForEach(AgentCatalog.switchable, id: \.id) { k in
                        Button("Continue in \(AgentCatalog.shortName(of: k.id))", systemImage: "arrow.triangle.swap") { model.switchAgent(id, to: k.id) }
                            .disabled(pane.agent.harness == k.id)
                    }
                }
                if pane.asleep {
                    Button("Wake Agent", systemImage: "sun.max") { model.sleepPane(id, wake: true) }
                } else if pane.canSleep {
                    Button("Put Agent to Sleep", systemImage: "moon.zzz") { model.sleepPane(id) }
                }
            }
            .disabled(!drive)
            if let url = pane.sessionURL {
                Button("Copy Session Link", systemImage: "link") { UIPasteboard.general.url = url }
            }
        }
        Section {
            Button("Move to New Tab", systemImage: "rectangle.badge.plus") { Task { await model.breakOut(id) } }
            Button("Close Pane", systemImage: "xmark", role: .destructive) {
                if let pane, closeWarning(pane) != nil { confirmClose(pane) } else { Task { await model.closePane(id) } }
            }
        }
        .disabled(!drive)
    }
}

/// The tab's floating panes (over its splits on the Mac), front first, each
/// marked if it's minimised there; tap one to open it.
private struct FloatingRow: View {
    let model: AppModel
    let tab: WorkspaceTab
    let open: (String) -> Void
    let confirmClose: (Pane) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Floating").font(.footnote.weight(.semibold)).foregroundStyle(.secondary)
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 10) {
                    ForEach(tab.floating.reversed(), id: \.paneID) { f in
                        PaneCard(model: model, paneID: f.paneID, focused: f.paneID == tab.focusedPaneID)
                            .frame(width: 180, height: 120)
                            .overlay(alignment: .topTrailing) {
                                if f.minimised {
                                    Text("Minimised").font(.caption2.weight(.semibold))
                                        .padding(.horizontal, 6).padding(.vertical, 2)
                                        .background(.thinMaterial, in: Capsule())
                                        .padding(8)
                                }
                            }
                            .contentShape(.contextMenuPreview, RoundedRectangle(cornerRadius: 14))
                            .contentShape(RoundedRectangle(cornerRadius: 14))
                            .onTapGesture { open(f.paneID) }
                            .contextMenu { actions(f) }
                    }
                }
            }
        }
    }

    @ViewBuilder private func actions(_ f: FloatingPane) -> some View {
        let id = f.paneID
        Button("Open", systemImage: "arrow.up.left.and.arrow.down.right") { open(id) }
        Section {
            if f.minimised {
                Button("Restore on the Mac", systemImage: "macwindow") { model.arrange(id, .restore) }
            } else {
                Button("Minimise on the Mac", systemImage: "minus.rectangle") { model.arrange(id, .minimise) }
            }
            Button("Dock in the Splits", systemImage: "rectangle.split.2x1") { model.arrange(id, .dock) }
            Button("Close Pane", systemImage: "xmark", role: .destructive) {
                if let pane = model.panes[id], closeWarning(pane) != nil { confirmClose(pane) } else { Task { await model.closePane(id) } }
            }
        }
        .disabled(model.group(ofPane: id)?.canDrive == false)
    }
}

/// A pane at a glance: what it is, where, and its agent's state and latest
/// word.
private struct PaneCard: View {
    let model: AppModel
    let paneID: String
    let focused: Bool

    private var pane: Pane? { model.panes[paneID] }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                icon
                Text(name).font(.subheadline.weight(.semibold)).lineLimit(1)
            }
            if let detail, !detail.isEmpty {
                Text(detail).font(.caption).foregroundStyle(.secondary).lineLimit(1).truncationMode(.head)
            }
            if let a = pane?.agent, pane?.hasAgent == true {
                Text(a.summary.isEmpty ? pane?.agentStatusLabel ?? a.state.label : a.summary)
                    .font(.footnote)
                    .foregroundStyle(a.state == .needsInput ? Color.orange : .primary)
                    .lineLimit(6)
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Color(uiColor: .secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(focused ? Color.accentColor.opacity(0.6) : .clear, lineWidth: 2))
    }

    private var name: String {
        guard let p = pane else { return "" }
        if p.kind == .browser || p.kind == .simulator { return p.title.isEmpty ? (p.kind == .browser ? "Browser" : "Simulator") : p.title }
        return p.displayName
    }

    private var detail: String? {
        guard let p = pane else { return nil }
        return p.kind == .browser ? URL(string: p.url)?.host() : p.kind == .simulator ? nil : p.kind == .editor ? (p.editor.dirty ? "Edited" : nil) : p.kind == .diff ? diffDetail(p) : p.shortCwd
    }

    /// A diff pane: its open threads, and replies you haven't read.
    private func diffDetail(_ p: Pane) -> String? {
        var parts: [String] = []
        if p.diff.openThreads > 0 { parts.append(p.diff.openThreads == 1 ? "1 thread" : "\(p.diff.openThreads) threads") }
        if p.diff.unread > 0 { parts.append("\(p.diff.unread) new") }
        return parts.isEmpty ? p.diff.title : parts.joined(separator: " · ")
    }

    @ViewBuilder private var icon: some View {
        switch pane?.kind {
        case .browser: Image(systemName: "globe").foregroundStyle(.secondary)
        case .simulator: Image(systemName: "iphone").foregroundStyle(.secondary)
        case .editor: Image(systemName: "doc.text").foregroundStyle(.secondary)
        case .diff: Image(systemName: "plusminus").foregroundStyle(.secondary)
        default:
            if let p = pane, p.hasAgent, p.agent.state != .unspecified {
                StateGlyph(state: p.agent.shownState, asleep: p.asleep, progress: p.agentProgress)
            } else {
                ProgramIcon(program: pane?.exited == false ? pane?.program ?? [] : [], size: 17)
            }
        }
    }
}

/// Why closing a pane needs a yes, if it does: an agent at work (unless
/// turned off), or programs other than an agent at rest still running.
@MainActor
func closeWarning(_ p: Pane) -> String? {
    let warnBusy = UserDefaults.standard.object(forKey: "warnCloseBusyAgent") as? Bool ?? true
    if warnBusy, p.hasAgent, !p.exited, [.working, .needsInput, .starting].contains(p.agent.shownState) || p.agent.subagents > 0 {
        let a = p.agent
        let doing = a.shownState == .needsInput ? "is waiting for you" : a.runningCommand ? "is running a command"
            : a.shownState == .idle ? "has \(a.subagents == 1 ? "a subagent" : "\(a.subagents) subagents") still running" : "is still working"
        return "\(p.displayName.capitalized) \(doing). Closing stops it."
    }
    var names: [String] = []
    for n in p.processes where !(p.hasAgent && n.lowercased() == p.agent.harness.lowercased()) && !names.contains(n) { names.append(n) }
    guard !names.isEmpty else { return nil }
    return ListFormatter.localizedString(byJoining: names) + (names.count == 1 ? " is" : " are") + " still running in it and will be stopped."
}
