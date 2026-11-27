import HammockKit
import SwiftUI

/// The "each run" pop-up for an agent's automation: a new tab and session
/// each run, this automation's own session carried on, or a session of
/// yours (Continue a Session… picks one: an open tab, or a recent session).
/// Only agents that can resume their sessions offer the last two.
struct ScheduleSessionPicker: View {
    let model: AppModel
    @Bindable var draft: ScheduleDraft
    /// The computer it runs on ("" this one): its tabs and sessions.
    let computerID: String
    @State private var picking = false

    private var canContinue: Bool { Schedule.canContinue(draft.s.kind) }

    var body: some View {
        Picker("Each run", selection: choice) {
            Text("New tab each run").tag("new")
            if canContinue {
                Text("Continue this automation’s session").tag("own")
                if draft.s.session == .picked {
                    Text(draft.s.sessionText ?? "Continues a session").tag("picked")
                }
                Divider()
                Text("Continue a Session…").tag("pick")
            }
        }
        .labelsHidden()
        .fixedSize()
        .help("Where each run goes: a new tab, or on in one session, waiting while its agent is busy or something’s typed in it")
        .popover(isPresented: $picking, arrowEdge: .bottom) {
            SessionPickList(model: model, kind: draft.s.kind, computerID: computerID) { id, name, warning in
                setContinuing(.picked)
                draft.s.sessionID = id
                draft.s.sessionName = name
                draft.s.sessionWarning = warning
                picking = false
            }
        }
        .onChange(of: draft.s.kind) { _, kind in
            if !Schedule.canContinue(kind), draft.s.session != .new { choice.wrappedValue = "new" }
        }
    }

    private var choice: Binding<String> {
        Binding(get: {
            switch draft.s.session {
            case .own: "own"
            case .picked: "picked"
            default: "new"
            }
        }, set: { tag in
            switch tag {
            case "own":
                setContinuing(.own)
                draft.s.sessionID = draft.original.session == .own ? draft.original.sessionID : ""
                draft.s.sessionName = draft.original.session == .own ? draft.original.sessionName : ""
                draft.s.sessionWarning = ""
            case "pick":
                picking = true
            case "picked":
                break
            default:
                draft.s.session = .new
                draft.s.sessionID = ""
                draft.s.sessionName = ""
                draft.s.sessionWarning = ""
                if draft.s.overlap == .wait { draft.s.overlap = .skip }
            }
        })
    }

    /// Carrying on a session waits for the last run by default; it never
    /// stops it or runs beside it.
    private func setContinuing(_ s: Schedule.Session) {
        if draft.s.session == .new || draft.s.overlap != .skip { draft.s.overlap = .wait }
        draft.s.session = s
    }
}

/// A plain warning: the session's tab lets its agent do more than the
/// automation would.
struct SessionWarning: View {
    let text: String

    var body: some View {
        Label(text, systemImage: "exclamationmark.triangle.fill")
            .font(.callout)
            .foregroundStyle(.orange)
            .fixedSize(horizontal: false, vertical: true)
    }
}

/// Continue a Session…'s list: open tabs whose agent is this kind (on the
/// automation's computer, in your own Groups, not ones shared with you),
/// then recent sessions from the session finder.
struct SessionPickList: View {
    let model: AppModel
    let kind: String
    let computerID: String
    let pick: (_ id: String, _ name: String, _ warning: String) -> Void
    @State private var query = ""
    @State private var recent: [SessionInfo] = []
    @State private var loading = true

    private struct OpenTab: Identifiable {
        let pane: Pane
        let tab: WorkspaceTab
        let group: WorkspaceGroup
        var id: String { pane.id }
    }

    private var openTabs: [OpenTab] {
        var out: [OpenTab] = []
        for g in model.scheduleGroups(on: computerID) {
            for t in model.tabs(in: g) {
                for p in model.panes(in: t) where p.hasAgent && p.agent.harness == kind && !p.agent.sessionID.isEmpty {
                    out.append(OpenTab(pane: p, tab: t, group: g))
                }
            }
        }
        let q = query.lowercased()
        return q.isEmpty ? out : out.filter { (model.title(of: $0.tab) + " " + model.title(of: $0.group)).lowercased().contains(q) }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            TextField("Search", text: $query, prompt: Text("Search sessions"))
                .textFieldStyle(.roundedBorder)
            List {
                let tabs = openTabs
                if !tabs.isEmpty {
                    Section("Open Tabs") {
                        ForEach(tabs) { o in
                            row(title: model.title(of: o.tab), detail: model.title(of: o.group)) {
                                let name = model.title(of: o.tab)
                                pick(o.pane.agent.sessionID, name, SessionPickList.warning(o.pane, kind: kind))
                            }
                        }
                    }
                }
                let open = Set(tabs.map(\.pane.agent.sessionID))
                let rest = recent.filter { !open.contains($0.id) }
                Section("Recent Sessions") {
                    if rest.isEmpty {
                        Text(loading ? "Looking…" : "None").foregroundStyle(.secondary)
                    }
                    ForEach(rest, id: \.id) { s in
                        row(title: s.title.isEmpty ? String(s.id.prefix(8)) : s.title,
                            detail: GroupFolder.tilde(s.cwd) + " · " + Schedule.dateText(s.updatedUnixMs)) {
                            pick(s.id, s.title, "")
                        }
                    }
                }
            }
            .listStyle(.inset)
        }
        .padding(12)
        .frame(width: 400, height: 380)
        .task(id: query) {
            loading = true
            let got = await model.listSessions(query, limit: 40, computerID: computerID)
            recent = (got?.sessions ?? []).filter { $0.harness == kind }
            loading = false
        }
    }

    private func row(title: String, detail: String, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title).lineLimit(1)
                Text(detail).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    /// What the pane's own command line shows of an agent allowed more than
    /// an automation run ("" for nothing seen). The daemon says for sure on
    /// save, from the agent's own process.
    static func warning(_ pane: Pane, kind: String) -> String {
        let wide = ["--dangerously-skip-permissions", "--allow-dangerously-skip-permissions",
                    "--dangerously-bypass-approvals-and-sandbox", "--yolo", "--full-auto", "bypassPermissions", "danger-full-access"]
        guard let flag = pane.command.first(where: { wide.contains($0) || wide.contains(String($0.split(separator: "=").last ?? "")) }) else { return "" }
        return "Its tab runs \(Schedule.kindName(kind)) with \(flag), so runs sent there can do more than this automation allows"
    }
}
