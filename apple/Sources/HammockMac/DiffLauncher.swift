import AppKit
import HammockKit
import Observation
import SwiftUI

/// Split ▸ Diff… and File ▸ New Diff Tab…: the Open a Diff sheet, to choose
/// what a diff pane compares before it opens. It starts on this branch
/// against the branch it would go to (as Review Changes does).
@MainActor
@Observable
final class DiffLauncher {
    static let shared = DiffLauncher()

    struct Request: Identifiable {
        let id = UUID()
        /// The Group it's in: its computer's repositories, and the new tab's.
        var groupID: String
        /// A split beside this pane (else a new tab after afterTab).
        var splitPane: String?
        var direction: SplitDirection = .horizontal
        var afterTab: String?
        /// Where to look for a repository at first ("": the Group's folder).
        var folder = ""
    }

    var request: Request?

    /// Beside paneID, on the repository it's in.
    func split(_ model: AppModel, _ paneID: String, _ direction: SplitDirection) {
        guard let p = model.panes[paneID], let g = model.group(ofPane: paneID) else { return }
        request = Request(groupID: g.id, splitPane: paneID, direction: direction, folder: Self.folder(of: p))
    }

    /// A new tab in groupID (the Group you're in), after the current tab.
    func newTab(_ model: AppModel, groupID: String? = nil) {
        guard let g = groupID ?? model.currentGroup?.id ?? model.shownGroups.first?.id else { return }
        let tab = model.selectedTab.flatMap { $0.groupID == g ? $0 : nil }
        let folder = g == model.currentGroup?.id ? DiffUI.repoFolder(model) ?? "" : ""
        request = Request(groupID: g, afterTab: tab?.id, folder: folder)
    }

    /// A pane's folder: an editor's file's, a diff's repository, a terminal's.
    static func folder(of p: Pane) -> String {
        if p.isEditor, !p.editor.path.isEmpty { return (p.editor.path as NSString).deletingLastPathComponent }
        if p.isDiff, !p.diff.repo.isEmpty { return p.diff.repo }
        return p.cwd
    }
}

struct OpenDiffSheet: View {
    let model: AppModel
    let request: DiffLauncher.Request
    @Environment(\.dismiss) private var dismiss

    enum Kind: Hashable { case branch, uncommitted, commit, range }

    @State private var folder: String
    @State private var info: Hammock_V1_RepoInfo?
    @State private var reading = false
    @State private var kind = Kind.branch
    @State private var kindTouched = false
    @State private var base = ""
    @State private var withUncommitted = true
    @State private var commit = ""
    @State private var from = ""
    @State private var to = ""
    @State private var mergeBase = false
    @State private var opening = false
    @State private var error = ""

    init(model: AppModel, request: DiffLauncher.Request) {
        self.model = model
        self.request = request
        _folder = State(initialValue: Self.tilde(request.folder, local: model.groups.first { $0.id == request.groupID }?.isRemote != true))
    }

    private var group: WorkspaceGroup? { model.groups.first { $0.id == request.groupID } }
    private var isLocal: Bool { group?.isRemote != true && group?.isOnLinkedComputer != true }
    private var repo: String { info?.repo ?? "" }
    private var branch: String { info?.branch ?? "" }
    private var defaultBranch: String { info?.defaultBranch ?? "" }
    /// Branches to compare this one with: every other, the default first.
    private var bases: [String] { (info?.branches ?? []).filter { $0 != branch } }
    private var commits: [Hammock_V1_DiffCommit] { info?.commits ?? [] }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker("Compare", selection: Binding(get: { kind }, set: { kind = $0; kindTouched = true })) {
                        Text("This Branch").tag(Kind.branch)
                        Text("Uncommitted").tag(Kind.uncommitted)
                        Text("A Commit").tag(Kind.commit)
                        Text("A Range").tag(Kind.range)
                    }
                    .pickerStyle(.segmented)
                    .labelsHidden()
                } footer: {
                    Text(explanation)
                }

                switch kind {
                case .branch: branchFields
                case .uncommitted: EmptyView()
                case .commit: commitList
                case .range: rangeFields
                }

                Section {
                    TextField("Folder", text: $folder, prompt: Text("The Group's folder"))
                        .font(.system(.body, design: .monospaced))
                        .labelsHidden()
                    if isLocal {
                        Button("Choose in Finder…") { chooseFolder() }
                    }
                    if let info, !info.error.isEmpty {
                        Label(info.error, systemImage: "exclamationmark.triangle")
                            .font(.caption)
                            .foregroundStyle(.orange)
                    }
                } header: {
                    Text("Repository")
                } footer: {
                    if !repo.isEmpty {
                        Text(branch.isEmpty ? "\(Self.tilde(repo, local: isLocal)), HEAD detached" : "\(Self.tilde(repo, local: isLocal)), on \(branch)")
                            .font(.system(.caption, design: .monospaced))
                    }
                }

                if !error.isEmpty {
                    Label(error, systemImage: "exclamationmark.triangle")
                        .foregroundStyle(.orange)
                }
            }
            .formStyle(.grouped)
            .navigationTitle(request.splitPane == nil ? "New Diff Tab" : "Open a Diff")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Open") { open() }
                        .disabled(!canOpen)
                }
                if reading {
                    ToolbarItem(placement: .status) { ProgressView().controlSize(.small) }
                }
            }
        }
        .frame(width: 520, height: 560)
        .task(id: folder) { await read() }
    }

    // MARK: Fields

    @ViewBuilder private var branchFields: some View {
        Section {
            Picker("Against", selection: $base) {
                if bases.isEmpty { Text(base.isEmpty ? "No other branches" : base).tag(base) }
                ForEach(bases, id: \.self) { b in
                    Text(b == defaultBranch ? "\(b) (default)" : b).tag(b)
                }
            }
            .disabled(bases.isEmpty)
            Toggle("Include uncommitted changes", isOn: $withUncommitted)
        }
    }

    @ViewBuilder private var commitList: some View {
        Section {
            if commits.isEmpty {
                Text(reading ? "Reading the history…" : "No commits yet").foregroundStyle(.secondary)
            }
            ForEach(commits, id: \.sha) { c in
                Button {
                    commit = c.sha
                } label: {
                    HStack(spacing: 8) {
                        Text(String(c.sha.prefix(7)))
                            .font(.system(.callout, design: .monospaced))
                            .foregroundStyle(.secondary)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(c.subject).lineLimit(1)
                            Text("\(c.author), \(Self.ago(c.unix))").font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        if commit == c.sha { Image(systemName: "checkmark").foregroundStyle(Color.accentColor) }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
        } header: {
            Text("Commit")
        }
    }

    @ViewBuilder private var rangeFields: some View {
        Section {
            TextField("From", text: $from, prompt: Text(defaultBranch.isEmpty ? "main" : defaultBranch))
                .font(.system(.body, design: .monospaced))
            TextField("To", text: $to, prompt: Text("HEAD"))
                .font(.system(.body, design: .monospaced))
            Toggle("From where they diverged (merge base)", isOn: $mergeBase)
        } footer: {
            Text("Branches, tags or commits.")
        }
    }

    private var explanation: String {
        switch kind {
        case .branch:
            let name = branch.isEmpty ? "HEAD" : branch
            let against = base.isEmpty ? "the branch it would merge into" : base
            return "What a pull request from \(name) to \(against) would show: everything since it left \(against)\(withUncommitted ? ", and what isn't committed yet" : "")."
        case .uncommitted: return "Everything not yet committed, staged or not, new files too, against the last commit."
        case .commit: return "One commit's changes, against its parent."
        case .range: return mergeBase ? "What changed on To since it left From (From...To)." : "Everything between From and To (From..To)."
        }
    }

    private var canOpen: Bool {
        guard !opening, !repo.isEmpty else { return false }
        switch kind {
        case .branch: return !base.isEmpty
        case .commit: return !commit.isEmpty
        case .uncommitted, .range: return true
        }
    }

    private var source: Hammock_V1_DiffSource {
        switch kind {
        case .branch: .with { $0.kind = .branch; $0.base = base; $0.withUncommitted = withUncommitted }
        case .uncommitted: DiffUI.uncommitted
        case .commit: .with { $0.kind = .commit; $0.rev = commit }
        case .range: .with {
            $0.kind = .range
            $0.base = from.trimmingCharacters(in: .whitespaces).isEmpty ? (defaultBranch.isEmpty ? "main" : defaultBranch) : from.trimmingCharacters(in: .whitespaces)
            $0.rev = to.trimmingCharacters(in: .whitespaces).isEmpty ? "HEAD" : to.trimmingCharacters(in: .whitespaces)
            $0.mergeBase = mergeBase
        }
        }
    }

    // MARK: Actions

    /// The repository the folder is in (after a pause while it's typed).
    private func read() async {
        if info != nil { try? await Task.sleep(for: .milliseconds(350)) }
        guard !Task.isCancelled else { return }
        reading = true
        defer { reading = false }
        let r = await model.readRepo(folder.trimmingCharacters(in: .whitespaces), groupID: request.groupID)
        guard !Task.isCancelled else { return }
        info = r ?? .with { $0.error = "Couldn't reach \(group.map { model.hostLabel(of: $0) } ?? "the Mac")" }
        guard let r, r.error.isEmpty else { return }
        // This branch against its default, as Review Changes starts; on the
        // default branch itself (or detached), what's uncommitted.
        let onOwnBranch = !r.branch.isEmpty && !r.defaultBranch.isEmpty && r.branch != r.defaultBranch
        if !kindTouched { kind = onOwnBranch ? .branch : .uncommitted }
        let others = r.branches.filter { $0 != r.branch }
        if !others.contains(base) { base = others.first ?? "" }
        if !r.commits.contains(where: { $0.sha == commit }) { commit = r.commits.first?.sha ?? "" }
    }

    private func open() {
        opening = true
        error = ""
        let (src, repo, r) = (source, repo, request)
        Task {
            do {
                try await model.openDiff(repo: repo, groupID: r.groupID, afterTab: r.afterTab, splitPane: r.splitPane,
                                         direction: r.direction, source: src)
                dismiss()
            } catch {
                self.error = error.localizedDescription
                opening = false
            }
        }
    }

    private func chooseFolder() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose"
        if !repo.isEmpty { panel.directoryURL = URL(fileURLWithPath: repo) }
        if panel.runModal() == .OK, let p = panel.url?.path { folder = Self.tilde(p, local: true) }
    }

    // MARK: Helpers

    /// ~ for the home folder, on this Mac (the daemon expands it again).
    static func tilde(_ p: String, local: Bool) -> String {
        local ? (p as NSString).abbreviatingWithTildeInPath : p
    }

    static func ago(_ unix: Int64) -> String {
        let f = RelativeDateTimeFormatter()
        f.unitsStyle = .full
        return f.localizedString(for: Date(timeIntervalSince1970: TimeInterval(unix)), relativeTo: .now)
    }
}
