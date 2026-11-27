import AppKit
import HammockKit
import Observation
import SwiftUI

/// An automation being made or changed in the Automations window: what it
/// runs and where, its When (times and triggers), Only If and options. The
/// daemon checks it on save; what it says is shown with it.
@MainActor
@Observable
final class ScheduleDraft: Identifiable {
    let id = UUID()
    /// As it was (no id while new).
    let original: Schedule
    var s: Schedule
    /// s as editing began, to tell whether anything's changed.
    @ObservationIgnored private let start: Schedule
    var changed: Bool { s != start }
    /// The days pop-up's Custom Days and Cron, chosen: shown though the
    /// days picked match another choice.
    var customDays: Bool
    var cronMode: Bool
    var saving = false
    var error: String?
    /// The automation's own Group, closed since (kept: runs bring it back).
    @ObservationIgnored let closedGroupID: String?

    var isNew: Bool { original.id.isEmpty }

    init(model: AppModel, original: Schedule) {
        self.original = original
        var s = original
        // A Group since closed: the pop-up shows it by name ("" here), and
        // it's saved with its own id, so the next run brings that Group back.
        if !s.groupID.isEmpty, !model.groups.contains(where: { $0.id == s.groupID }) {
            closedGroupID = s.groupID
            s.groupID = ""
        } else {
            closedGroupID = nil
        }
        if s.computerID.isEmpty || s.computerID == model.myUserID {
            s.cwd = GroupFolder.tilde(s.cwd)
        }
        if s.minuteOfDay == 0, s.weekdays.isEmpty, s.cron.isEmpty, s.timing != .repeat { s.minuteOfDay = 9 * 60 }
        self.s = s
        start = s
        cronMode = !s.cron.isEmpty
        let set = Set(s.weekdays)
        customDays = !set.isEmpty && set.count < 7 && set != [1, 2, 3, 4, 5] && set != [0, 6]
    }

    /// What's still needed before it can be saved (nil: nothing).
    var missing: String? {
        if s.prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return s.kind == "command" ? "Write the command it runs" : "Write the prompt it runs"
        }
        if s.cwd.trimmingCharacters(in: .whitespaces).isEmpty { return "Choose its folder" }
        if s.continuesSession, s.session == .picked, s.sessionID.isEmpty { return "Choose the session it continues" }
        if s.timing == .repeat, cronMode, s.cron.trimmingCharacters(in: .whitespaces).isEmpty { return "Write its cron line" }
        if s.timing == .events, s.events.isEmpty { return "Add a trigger" }
        if let m = s.events.lazy.map(\.missing).first(where: { !$0.isEmpty }) { return m }
        if let m = s.onlyIf.lazy.map(\.missing).first(where: { !$0.isEmpty }) { return m }
        return nil
    }

    var canSave: Bool { missing == nil }

    /// A one-off's date and time: the next hour, until one's picked.
    var once: Date {
        get {
            s.onceUnixMs > 0 ? Date(timeIntervalSince1970: Double(s.onceUnixMs) / 1000)
                : Calendar.current.nextDate(after: Date(), matching: DateComponents(minute: 0), matchingPolicy: .nextTime) ?? Date()
        }
        set { s.onceUnixMs = Int64(newValue.timeIntervalSince1970 * 1000) }
    }

    func save(_ model: AppModel) {
        guard canSave, !saving else { return }
        var s = self.s
        if s.timing == .once, s.onceUnixMs == 0 { s.onceUnixMs = Int64(once.timeIntervalSince1970 * 1000) }
        for i in s.events.indices { s.events[i].error = "" }
        if cronMode { s.weekdays = [] } else { s.cron = "" }
        s.cron = s.cron.trimmingCharacters(in: .whitespaces)
        s.cwd = s.cwd.trimmingCharacters(in: .whitespaces) // ~ is the daemon's to expand, on its computer
        if s.groupID.isEmpty, let closed = closedGroupID { s.groupID = closed }
        // Changed elsewhere while this was open: a pause, a tool remembered
        // from Needs You. Kept, less the tools removed here.
        if let current = model.schedules[original.id] {
            s.paused = current.paused
            s.allowedTools = current.allowedTools.filter { self.s.allowedTools.contains($0) || !original.allowedTools.contains($0) }
        }
        // Only what's yours to set: the daemon keeps its own fields.
        s.runs = []
        s.nextRunUnixMs = 0
        s.error = ""
        saving = true
        error = nil
        Task {
            do {
                // Unchanged: no need to save.
                let saved = !isNew && s == stripped(original, model) ? original : try await model.saveSchedule(s)
                let ui = ScheduleUI.shared
                ui.selected = saved.id
                if ui.editing === self { ui.editing = nil }
                if saved.events.contains(where: { if case .calendar = $0.kind { true } else { false } }) {
                    CalendarFeed.shared.refresh(force: true)
                }
            } catch {
                self.error = error.localizedDescription
                saving = false
            }
        }
    }

    /// The original as save would send it, to tell whether anything changed.
    private func stripped(_ o: Schedule, _ model: AppModel) -> Schedule {
        var s = o
        if s.computerID.isEmpty || s.computerID == model.myUserID { s.cwd = GroupFolder.tilde(s.cwd) }
        s.runs = []
        s.nextRunUnixMs = 0
        s.error = ""
        for i in s.events.indices { s.events[i].error = "" }
        return s
    }
}

/// The automation being edited, in place of its page: its name as a title,
/// what runs where as pop-ups, then When, Only If, the prompt and Options.
struct ScheduleEditor: View {
    let model: AppModel
    @Bindable var draft: ScheduleDraft
    @State private var options = false
    @State private var folderShown = false
    @FocusState private var nameFocused: Bool

    /// On another computer of yours: its folders and Groups.
    private var computer: Computer? { model.computer(of: draft.original) }
    private var computerID: String { computer?.userID ?? "" }
    private var groups: [WorkspaceGroup] { model.scheduleGroups(on: computerID) }
    private var isCommand: Bool { draft.s.kind == "command" }

    /// The agents installed on another computer it runs on (its folder
    /// list says; nil: not heard yet, or this Mac's, from the catalogue).
    @State private var remoteAgents: [String]?

    /// The agents it can run: those the daemon schedules that are installed
    /// on its computer, and the one it has (marked when it isn't installed,
    /// so editing doesn't change it).
    private var agentKinds: [String] {
        let installed = remoteAgents.map(Set.init) ?? Set(AgentCatalog.installed.map(\.id))
        var k = AgentCatalog.schedulable.map(\.id).filter { installed.contains($0) || (computer != nil && remoteAgents == nil) }
        if draft.s.kind != "command", !k.contains(draft.s.kind) { k.append(draft.s.kind) }
        return k
    }

    private func agentInstalled(_ kind: String) -> Bool {
        if let remoteAgents { return remoteAgents.contains(kind) }
        return computer != nil || AgentCatalog.installed.contains { $0.id == kind }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                title
                section("When") {
                    ScheduleWhenBox(model: model, draft: draft, computerID: computerID)
                }
                section("Only If") {
                    ScheduleOnlyIfBox(conditions: $draft.s.onlyIf, computerID: computerID, model: model)
                }
                section(isCommand ? "Command" : "Prompt") { prompt }
                optionsSection
                if let error = draft.error {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                        .font(.callout)
                        .textSelection(.enabled)
                }
            }
            .padding(.horizontal, 28)
            .padding(.vertical, 22)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .neutralPopUps()
        .onAppear { if draft.isNew { nameFocused = true } }
    }

    private func section(_ label: String, @ViewBuilder _ content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 7) {
            SectionLabel(label)
            content()
        }
    }

    // MARK: Name, and what runs where

    private var title: some View {
        VStack(alignment: .leading, spacing: 10) {
            TextField("Name", text: $draft.s.name, prompt: Text(namePrompt))
                .textFieldStyle(.plain)
                .font(.title2.weight(.bold))
                .focused($nameFocused)
                .padding(.bottom, 4)
                .overlay(alignment: .bottom) { Divider() }
            FlowLayout(spacing: 8) {
                Picker("Group", selection: groupBinding) {
                    ForEach(groups) { g in
                        Label { Text(model.title(of: g)) } icon: { model.groupMenuIcon(g) }.tag(g.id)
                    }
                    Divider()
                    if draft.s.groupName.isEmpty {
                        Text("The folder’s Group, or a new one").tag("")
                    } else {
                        // Its own Group, closed since: by name (the menu says it's closed).
                        Section("Closed") { Text(draft.s.groupName).tag("") }
                    }
                }
                .labelsHidden()
                .fixedSize()
                .help(draft.s.groupID.isEmpty && !draft.s.groupName.isEmpty
                      ? "This Group is closed: the next run brings it back"
                      : "Each run opens a tab in this Group. If it’s been closed, the run brings it back.")
                Picker("Runs", selection: $draft.s.kind) {
                    ForEach(agentKinds, id: \.self) { k in
                        Text(agentInstalled(k) ? Schedule.kindName(k) : "\(Schedule.kindName(k)) (not installed)").tag(k)
                    }
                    Divider()
                    Text(Schedule.kindName("command")).tag("command")
                }
                .labelsHidden()
                .fixedSize()
                .task(id: computerID) {
                    guard !computerID.isEmpty else { remoteAgents = nil; return }
                    remoteAgents = await model.listFolders(groupID: "", computerID: computerID)?.agents
                }
                if isCommand {
                    Picker("Each run", selection: $draft.s.closeOnSuccess) {
                        Text("New tab each run").tag(false)
                        Text("New tab each run, closed if it succeeds").tag(true)
                    }
                    .labelsHidden()
                    .fixedSize()
                } else {
                    ScheduleSessionPicker(model: model, draft: draft, computerID: computerID)
                }
                Text("in").foregroundStyle(.secondary)
                Button(draft.s.cwd.isEmpty ? "Choose a folder" : ScheduleText.folder(draft.s.cwd)) { folderShown = true }
                    .buttonStyle(TokenStyle())
                    .fontDesign(ScheduleText.folder(draft.s.cwd).hasPrefix("~") || draft.s.cwd.hasPrefix("/") ? .monospaced : .default)
                    .help(draft.s.cwd.isEmpty ? "The folder it runs in" : "Runs in \(draft.s.cwd)")
                    .popover(isPresented: $folderShown, arrowEdge: .bottom) { folderPopover }
                if let c = computer {
                    HStack(spacing: 4) {
                        ComputerBadge(c, size: 12)
                        Text("On \(c.name)")
                    }
                    .foregroundStyle(.secondary)
                }
            }
            if draft.s.continuesSession, !draft.s.sessionWarning.isEmpty {
                SessionWarning(text: draft.s.sessionWarning)
            }
        }
    }

    private var folderPopover: some View {
        HStack {
            TextField("Folder", text: $draft.s.cwd, prompt: Text("~/Developer/app"))
                .labelsHidden()
                .font(.system(.body, design: .monospaced))
                .frame(width: 300)
                .onSubmit { folderShown = false }
            // Only this Mac's folders can be browsed; another computer's are typed.
            if computer == nil {
                Button("Choose…") {
                    folderShown = false
                    if let f = GroupFolder.pick(startingAt: (draft.s.cwd as NSString).expandingTildeInPath,
                                                message: "Choose the folder this automation runs in") {
                        draft.s.cwd = GroupFolder.tilde(f)
                    }
                }
            }
        }
        .padding(12)
    }

    private var namePrompt: String {
        let p = draft.s.prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        return p.isEmpty ? "Name" : String(p.split(separator: "\n").first?.prefix(40) ?? "")
    }

    private var groupBinding: Binding<String> {
        Binding(get: { draft.s.groupID }, set: { id in
            draft.s.groupID = id
            if let g = groups.first(where: { $0.id == id }) {
                draft.s.groupName = g.name
                draft.s.groupIcon = g.icon
            }
        })
    }

    // MARK: The prompt

    private var prompt: some View {
        VStack(alignment: .leading, spacing: 0) {
            TextEditor(text: $draft.s.prompt)
                .font(isCommand ? .system(.body, design: .monospaced) : .body)
                .scrollContentBackground(.hidden)
                .frame(minHeight: isCommand ? 70 : 170)
                .padding(8)
                .overlay(alignment: .topLeading) {
                    if draft.s.prompt.isEmpty {
                        Text(isCommand ? "make test" : "What the agent should do each run, such as “Summarise what changed overnight and what needs me today.”")
                            .font(isCommand ? .system(.body, design: .monospaced) : .body)
                            .foregroundStyle(.tertiary)
                            .padding(.horizontal, 13)
                            .padding(.vertical, 8)
                            .allowsHitTesting(false)
                    }
                }
                .accessibilityLabel(isCommand ? "Command" : "Prompt")
            if !draft.s.events.isEmpty {
                Divider().opacity(0.6)
                HStack(spacing: 10) {
                    Button("Insert Event") { insertEvent() }
                        .buttonStyle(.borderless)
                        .themeLinkTint()
                        .help(isCommand
                              ? "What happened, in a line ($HMK_EVENT_SUMMARY). Each detail is in a variable too, such as $HMK_EVENT_URL and $HMK_EVENT_TITLE for a pull request."
                              : "Where what happened goes, such as a pull request’s link, number and title. Without it, it’s added after your prompt.")
                    Spacer()
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
            }
        }
        .help(isCommand ? "Run by your login shell, in its folder" : "The agent’s first message. It can call `hmk show` to put a page beside the run.")
        .background(.background.opacity(0.5), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay { RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(.quaternary) }
    }

    private func insertEvent() {
        let token = isCommand ? "\"$HMK_EVENT_SUMMARY\"" : "{{event}}"
        guard !draft.s.prompt.contains(isCommand ? "$HMK_EVENT_SUMMARY" : "{{event}}") else { return }
        let p = draft.s.prompt
        draft.s.prompt = p.isEmpty ? token : p + (isCommand ? " " : p.hasSuffix("\n") ? "\n" : "\n\n") + token
    }

    // MARK: Options

    private var optionsSection: some View {
        VStack(alignment: .leading, spacing: 12) {
            Button {
                withAnimation(.snappy(duration: 0.2)) { options.toggle() }
            } label: {
                HStack(spacing: 5) {
                    Image(systemName: "chevron.right")
                        .font(.caption.weight(.bold))
                        .rotationEffect(.degrees(options ? 90 : 0))
                    Text("Options")
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityValue(options ? "Shown" : "Hidden")
            if options {
                Grid(alignment: .leading, horizontalSpacing: 12, verticalSpacing: 10) {
                    GridRow {
                        optionLabel("If still running")
                        Picker("If still running", selection: $draft.s.overlap) {
                            if draft.s.continuesSession {
                                Text(ScheduleText.overlap(.wait)).tag(Schedule.Overlap.wait)
                                Text(ScheduleText.overlap(.skip)).tag(Schedule.Overlap.skip)
                            } else {
                                Text(ScheduleText.overlap(.skip)).tag(Schedule.Overlap.skip)
                                Text(ScheduleText.overlap(.stopPrevious)).tag(Schedule.Overlap.stopPrevious)
                                Text(ScheduleText.overlap(.runBoth)).tag(Schedule.Overlap.runBoth)
                            }
                        }
                        .labelsHidden()
                        .fixedSize()
                    }
                    if !isCommand {
                        GridRow {
                            optionLabel("Give up after")
                            Picker("Give up after", selection: giveUpBinding) {
                                ForEach(giveUpChoices, id: \.self) { m in Text(Schedule.durationText(minutes: m)).tag(m) }
                            }
                            .labelsHidden()
                            .fixedSize()
                            .help("A run waiting on you this long is stopped. Questions always go to Needs You.")
                        }
                    }
                    if !draft.s.continuesSession { GridRow {
                        optionLabel("Keep the last")
                        HStack(spacing: 6) {
                            TextField("Keep the last", value: keepBinding, format: .number)
                                .labelsHidden()
                                .multilineTextAlignment(.trailing)
                                .frame(width: 44)
                            Stepper("Runs kept", value: keepBinding, in: 1...30).labelsHidden()
                            Text(draft.s.keptRuns == 1 ? "run" : "runs").foregroundStyle(.secondary)
                        }
                        .help("Older runs’ tabs close as new ones open, except ones you’ve typed in")
                    } }
                    GridRow {
                        optionLabel("Notify me")
                        Picker("Notify me", selection: notifyBinding) {
                            Text(ScheduleText.notify(.ready)).tag(Schedule.Notify.ready)
                            Text(ScheduleText.notify(.problems)).tag(Schedule.Notify.problems)
                            Text(ScheduleText.notify(.never)).tag(Schedule.Notify.never)
                        }
                        .labelsHidden()
                        .fixedSize()
                    }
                    if draft.s.kind == "claude" {
                        GridRow {
                            optionLabel("Remembered tools")
                            RememberedTools(tools: $draft.s.allowedTools)
                        }
                    }
                    if draft.s.kind == "codex" {
                        GridRow {
                            optionLabel("Codex sandbox")
                            Picker("Codex sandbox", selection: $draft.s.codexSandbox) {
                                Text("Your Codex settings").tag("")
                                Divider()
                                Text("Read only").tag("read-only")
                                Text("Workspace write").tag("workspace-write")
                                Text("Full access (no sandbox)").tag("danger-full-access")
                            }
                            .labelsHidden()
                            .fixedSize()
                        }
                        GridRow {
                            optionLabel("Codex approval")
                            Picker("Codex approval", selection: $draft.s.codexApproval) {
                                Text("Your Codex settings").tag("")
                                Divider()
                                Text("Untrusted commands").tag("untrusted")
                                Text("On failure").tag("on-failure")
                                Text("On request").tag("on-request")
                                Text("Never").tag("never")
                            }
                            .labelsHidden()
                            .fixedSize()
                        }
                    }
                }
                .padding(.leading, 18)
            }
        }
        .font(.callout)
    }

    private func optionLabel(_ text: String) -> some View {
        Text(text).foregroundStyle(.secondary).gridColumnAlignment(.trailing)
    }

    private var giveUpChoices: [Int] {
        let base = [60, 120, 240, 480, 1440]
        return base.contains(draft.s.givesUpAfter) ? base : (base + [draft.s.givesUpAfter]).sorted()
    }

    private var giveUpBinding: Binding<Int> {
        Binding(get: { draft.s.givesUpAfter }, set: { draft.s.giveUpMinutes = Int32($0) })
    }

    private var keepBinding: Binding<Int> {
        Binding(get: { draft.s.keptRuns }, set: { draft.s.keepRuns = Int32(min(max($0, 1), 30)) })
    }

    /// Shows the default for its kind until you pick one.
    private var notifyBinding: Binding<Schedule.Notify> {
        Binding(get: { draft.s.effectiveNotify }, set: { draft.s.notify = $0 })
    }
}

/// "17 tools": the tools runs may use without asking, remembered from
/// Needs You's Always Allow for This Automation, in a popover to search and
/// take some away.
struct RememberedTools: View {
    @Binding var tools: [String]
    @State private var shown = false
    @State private var query = ""

    var body: some View {
        if tools.isEmpty {
            Text("None")
                .help("When a run asks, “Always Allow for This Automation” in Needs You remembers the tool here")
        } else {
            Button(tools.count == 1 ? "1 tool" : "\(tools.count) tools") { shown = true }
                .buttonStyle(TokenStyle())
                .popover(isPresented: $shown, arrowEdge: .bottom) { list }
        }
    }

    private var list: some View {
        VStack(alignment: .leading, spacing: 8) {
            TextField("Search", text: $query, prompt: Text("Search"))
                .textFieldStyle(.roundedBorder)
            ScrollView {
                VStack(alignment: .leading, spacing: 2) {
                    ForEach(tools.filter { query.isEmpty || $0.localizedStandardContains(query) }, id: \.self) { tool in
                        HStack {
                            Text(tool).font(.system(.callout, design: .monospaced)).lineLimit(1).truncationMode(.middle)
                            Spacer()
                            RemoveRowButton(label: "Ask again for \(tool)") { tools.removeAll { $0 == tool } }
                        }
                        .padding(.vertical, 3)
                    }
                }
            }
            .frame(maxHeight: 280)
        }
        .padding(12)
        .frame(width: 360)
    }
}

/// The When row's choices elsewhere: a capsule, filled with the accent while chosen.
struct ChipStyle: ButtonStyle {
    let on: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout)
            .padding(.horizontal, 10)
            .padding(.vertical, 4)
            .foregroundStyle(on ? Color.white : Color.primary)
            .background(on ? AnyShapeStyle(Color.themeAccent) : AnyShapeStyle(.quaternary), in: Capsule())
            .opacity(configuration.isPressed ? 0.7 : 1)
            .contentShape(Capsule())
    }
}
