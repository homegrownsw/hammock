import SwiftUI

/// Start an agent: which agent, the folder on the Mac it works in (a
/// suggestion, typed, or browsed), the Group its tab goes in, and an
/// optional first message. Shared by the Mac (a sheet) and iOS.
public struct StartAgentView: View {
    let model: AppModel
    /// The tab's ID once started (nil: cancelled or failed).
    let onDone: (String?) -> Void
    /// The Mac's own folder picker, for a Group on this Mac.
    let chooseLocal: (() -> String?)?
    /// The tab it opened, once the daemon has started it.
    let onStarted: ((String) -> Void)?

    @State private var groupID: String
    /// The computer it starts on ("": this one): its Groups and folders.
    @State private var computerID: String
    @State private var harness: String
    @State private var cwd = ""
    @State private var cwdTouched = false
    @State private var prompt = ""
    @State private var list: Hammock_V1_FolderList?
    @State private var starting = false
    @State private var browsing = false

    public init(model: AppModel, groupID: String?, computerID: String? = nil, harness: String = "claude", chooseLocal: (() -> String?)? = nil, onDone: @escaping (String?) -> Void, onStarted: ((String) -> Void)? = nil) {
        self.model = model
        self.onStarted = onStarted
        self.onDone = onDone
        self.chooseLocal = chooseLocal
        let given = groupID.flatMap { id in model.groups.first { $0.id == id && $0.canAddTabs } }
        let computer = computerID ?? given.flatMap { model.computer(of: $0)?.userID } ?? ""
        let usable = Self.groups(model, on: computer)
        _computerID = State(initialValue: computer)
        _groupID = State(initialValue: given.flatMap { g in usable.first { $0.id == g.id }?.id } ?? usable.first?.id ?? "")
        _harness = State(initialValue: harness)
    }

    /// The Groups a tab can go in on a computer ("": this one, and Groups
    /// others share with you).
    static func groups(_ model: AppModel, on computer: String) -> [WorkspaceGroup] {
        model.groups.filter { $0.canAddTabs && (computer.isEmpty ? !$0.isOnLinkedComputer : $0.isOnLinkedComputer && $0.hostUserID == computer) }
    }

    private var usable: [WorkspaceGroup] { Self.groups(model, on: computerID) }

    private var group: WorkspaceGroup? { model.groups.first { $0.id == groupID } }
    private var installed: [String] { list?.agents ?? AgentCatalog.installed.map(\.id) }
    private var agents: [Hammock_V1_AgentKind] { AgentCatalog.startable(installedIDs: list?.agents, chosen: harness) }
    private var home: String { list?.home ?? "" }

    public var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker("Agent", selection: $harness) {
                        ForEach(agents, id: \.id) { Text(AgentCatalog.name(of: $0.id)).tag($0.id) }
                    }
                    .segmentedOrMenu(agents.count, fits: 2)
                    if list != nil, !installed.contains(harness) {
                        Label("\(AgentCatalog.name(of: harness)) isn't installed on \(hostName): the tab will say so.", systemImage: "exclamationmark.triangle")
                            .font(.caption)
                            .foregroundStyle(.orange)
                    }
                }

                if model.hasLinkedComputers || usable.count > 1 {
                    Section {
                        if model.hasLinkedComputers {
                            Picker("Computer", selection: $computerID) {
                                ForEach(model.computers, id: \.userID) { c in
                                    Label(c.thisComputer ? "This Mac" : c.name, systemImage: c.symbol)
                                        .tag(c.thisComputer ? "" : c.userID)
                                        .selectionDisabled(!c.online)
                                }
                            }
                            .onChange(of: computerID) {
                                groupID = usable.first?.id ?? ""
                                cwdTouched = false
                            }
                        }
                        Picker("Group", selection: $groupID) {
                            ForEach(usable) { g in
                                Text(g.isRemote && !g.isOnLinkedComputer ? "\(model.title(of: g)) · \(model.hostLabel(of: g))" : model.title(of: g)).tag(g.id)
                            }
                            Text(usable.isEmpty ? "A new Group" : "The folder's Group, or a new one").tag("")
                        }
                        .onChange(of: groupID) { cwdTouched = false } // its folder, as at first
                    }
                }

                Section {
                    TextField("Folder", text: Binding(get: { cwd }, set: { new in
                        // Only an edit counts (the field also sets what it has as it appears).
                        guard new != cwd else { return }
                        cwd = new
                        cwdTouched = true
                    }), prompt: Text("~/Developer/app"))
                        .font(.system(.body, design: .monospaced))
                        #if os(iOS)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        #endif
                    HStack {
                        Button("Browse…") { browsing = true }
                        if let chooseLocal, group?.isRemote != true, computerID.isEmpty {
                            Button("Choose in Finder…") {
                                if let p = chooseLocal() { cwd = tilde(p); cwdTouched = true }
                            }
                        }
                    }
                    ForEach(Array((list?.suggestions ?? []).prefix(8)), id: \.path) { s in
                        Button {
                            cwd = tilde(s.path)
                            cwdTouched = true
                        } label: {
                            HStack {
                                VStack(alignment: .leading, spacing: 1) {
                                    Text(tilde(s.path)).font(.system(.callout, design: .monospaced)).lineLimit(1).truncationMode(.head)
                                    Text(s.why).font(.caption).foregroundStyle(.secondary)
                                }
                                Spacer()
                                if expand(cwd) == s.path { Image(systemName: "checkmark").foregroundStyle(Color.accentColor) }
                            }
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                    }
                } header: {
                    Text("Folder on \(hostName)")
                }

                Section {
                    TextField("First message", text: $prompt, prompt: Text("Optional: what to work on"), axis: .vertical)
                        .lineLimit(3...8)
                } footer: {
                    Text("It starts in a new tab, in the folder's login shell, as if you'd typed \(harness)\(prompt.isEmpty ? "" : " and your message").")
                }
            }
            .formStyle(.grouped)
            .navigationTitle("Start an Agent")
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { onDone(nil) }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Start") { start() }
                        .disabled(starting || cwd.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
            .navigationDestination(isPresented: $browsing) {
                FolderBrowser(model: model, groupID: groupID, computerID: computerID, start: expand(cwd).isEmpty ? home : expand(cwd)) { picked in
                    cwd = tilde(picked)
                    cwdTouched = true
                    browsing = false
                }
            }
        }
        .task(id: groupID + computerID) {
            guard let l = await model.listFolders(groupID: groupID, computerID: computerID) else { return }
            list = l
            // The Group's own folder (else home), unless one was picked.
            if !cwdTouched || cwd.trimmingCharacters(in: .whitespaces).isEmpty {
                cwdTouched = false
                cwd = l.suggestions.first(where: { $0.why == "This Group" }).map { tilde($0.path) } ?? "~"
            }
            if !l.agents.isEmpty, !l.agents.contains(harness), let other = l.agents.first { harness = other }
        }
    }

    /// Whose Mac the folders are on.
    private var hostName: String {
        if let g = group, g.isRemote { return model.hostLabel(of: g) }
        if let c = model.computer(computerID), !c.thisComputer { return c.name }
        return "your Mac"
    }

    private func tilde(_ p: String) -> String {
        guard !home.isEmpty else { return p }
        if p == home { return "~" }
        return p.hasPrefix(home + "/") ? "~" + p.dropFirst(home.count) : p
    }

    private func expand(_ p: String) -> String {
        let t = p.trimmingCharacters(in: .whitespaces)
        guard !home.isEmpty else { return t }
        if t == "~" { return home }
        return t.hasPrefix("~/") ? home + t.dropFirst(1) : t
    }

    private func start() {
        starting = true
        let (h, dir, gid, text, cid) = (harness, expand(cwd), groupID, prompt.trimmingCharacters(in: .whitespacesAndNewlines), computerID)
        // The tab shows at once; this closes as it does.
        onDone(nil)
        Task {
            if let id = await model.startAgent(h, cwd: dir, groupID: gid, prompt: text, computerID: cid) { onStarted?(id) }
        }
    }
}

/// The folders on the Mac (a Group's host for a shared one), to pick one.
public struct FolderBrowser: View {
    let model: AppModel
    let groupID: String
    let computerID: String
    let pick: (String) -> Void
    @State private var path: String
    @State private var list: Hammock_V1_FolderList?
    @State private var filter = ""

    public init(model: AppModel, groupID: String, computerID: String = "", start: String, pick: @escaping (String) -> Void) {
        self.model = model
        self.groupID = groupID
        self.computerID = computerID
        self.pick = pick
        _path = State(initialValue: start)
    }

    public var body: some View {
        List {
            if let l = list {
                Section {
                    Button {
                        pick(l.path)
                    } label: {
                        Label("Use “\((l.path as NSString).lastPathComponent.isEmpty ? l.path : (l.path as NSString).lastPathComponent)”", systemImage: "checkmark.circle.fill")
                    }
                    if !l.parent.isEmpty {
                        Button { path = l.parent } label: { Label("Up to \((l.parent as NSString).lastPathComponent.isEmpty ? "/" : (l.parent as NSString).lastPathComponent)", systemImage: "arrow.up") }
                    }
                } footer: {
                    Text(l.path).font(.system(.caption, design: .monospaced))
                }
                if !l.error.isEmpty {
                    Text(l.error).foregroundStyle(.secondary)
                }
                Section {
                    ForEach(l.folders.filter { filter.isEmpty || $0.localizedCaseInsensitiveContains(filter) }, id: \.self) { name in
                        Button {
                            path = (l.path as NSString).appendingPathComponent(name)
                        } label: {
                            HStack {
                                Label(name, systemImage: "folder")
                                Spacer()
                                Image(systemName: "chevron.right").font(.caption).foregroundStyle(.tertiary)
                            }
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                    }
                    if l.folders.isEmpty, l.error.isEmpty {
                        Text("No folders inside").foregroundStyle(.secondary)
                    }
                }
            } else {
                ProgressView()
            }
        }
        .searchable(text: $filter, prompt: "Filter folders")
        .navigationTitle((path as NSString).lastPathComponent.isEmpty ? "/" : (path as NSString).lastPathComponent)
        .task(id: path) {
            filter = ""
            list = await model.listFolders(path, groupID: groupID, computerID: computerID)
        }
    }
}

extension View {
    /// A picker's segments while they fit (Claude Code and Codex, as it's
    /// always been), a menu once more agents are there.
    @ViewBuilder public func segmentedOrMenu(_ count: Int, fits: Int) -> some View {
        if count > fits { pickerStyle(.menu) } else { pickerStyle(.segmented) }
    }
}
