import AppKit
import HammockKit
import SwiftUI

/// New Scheduled Task… and Edit…: what runs (Claude Code, Codex or a
/// command), where (folder and Group), when, and how runs are kept and told.
/// The daemon checks it on save; what it says is shown here.
struct ScheduleEditor: View {
    let model: AppModel
    let original: Schedule
    /// The ScheduleUI request it's showing, for Schedule This's prefill.
    var request: UUID?

    @State private var draft: Schedule
    @State private var when: When
    @State private var days: Set<Int32>
    @State private var saving = false
    @State private var error: String?
    @Environment(\.dismiss) private var dismiss

    enum When: Hashable { case everyDay, weekdays, weekends, custom, cron }

    init(model: AppModel, original: Schedule, request: UUID? = nil) {
        self.model = model
        self.original = original
        self.request = request
        var s = original
        // A Group since closed: the picker shows it by name ("" here), and
        // it's saved with its own id, so the next run brings that Group back.
        if !s.groupID.isEmpty, !model.groups.contains(where: { $0.id == s.groupID }) {
            closedGroupID = s.groupID
            s.groupID = ""
        }
        if s.computerID.isEmpty || s.computerID == model.myUserID {
            s.cwd = GroupFolder.tilde(s.cwd)
        }
        _draft = State(initialValue: s)
        let set = Set(s.weekdays)
        _days = State(initialValue: set.isEmpty ? [1, 2, 3, 4, 5] : set)
        _when = State(initialValue: !s.cron.isEmpty ? .cron
            : set.isEmpty || set.count == 7 ? .everyDay
            : set == [1, 2, 3, 4, 5] ? .weekdays
            : set == [0, 6] ? .weekends : .custom)
    }

    /// The schedule's own Group, closed since (kept: runs bring it back).
    private var closedGroupID: String?

    private var isNew: Bool { original.id.isEmpty }
    /// What it can run: the agents the daemon schedules, then a command (and
    /// the kind it has, if the daemon no longer lists it).
    private var kinds: [String] {
        var k = AgentCatalog.schedulable.map(\.id)
        if draft.kind != "command", !k.contains(draft.kind) { k.append(draft.kind) }
        return k + ["command"]
    }
    /// On another computer of yours: its folders and Groups.
    private var computer: Computer? { model.computer(of: original) }
    private var groups: [WorkspaceGroup] { model.scheduleGroups(on: computer?.userID ?? "") }

    var body: some View {
        VStack(spacing: 0) {
            Text(isNew ? "New Scheduled Task" : "Edit Scheduled Task")
                .font(.headline)
                .padding(.top, 14)
            Form {
                Section {
                    TextField("Name", text: $draft.name, prompt: Text(namePrompt))
                    Picker("Run", selection: $draft.kind) {
                        ForEach(kinds, id: \.self) { Text(Schedule.kindName($0)).tag($0) }
                    }
                    .segmentedOrMenu(kinds.count, fits: 3)
                    VStack(alignment: .leading, spacing: 4) {
                        Text(draft.kind == "command" ? "Command" : "Prompt")
                        TextEditor(text: $draft.prompt)
                            .font(draft.kind == "command" ? .system(.body, design: .monospaced) : .body)
                            .frame(minHeight: 70, maxHeight: 140)
                            .scrollContentBackground(.hidden)
                            .padding(4)
                            .background(.background, in: RoundedRectangle(cornerRadius: 6))
                            .overlay { RoundedRectangle(cornerRadius: 6).strokeBorder(.quaternary) }
                        Text(draft.kind == "command" ? "Run by your login shell, in the folder." : "The agent’s first message. It can call `hmk show` to put a page beside the run.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }

                Section {
                    LabeledContent("Folder") {
                        HStack {
                            TextField("Folder", text: $draft.cwd, prompt: Text("~/Developer/app"))
                                .labelsHidden()
                                .font(.system(.body, design: .monospaced))
                            if computer == nil {
                                Button("Choose…") {
                                    if let f = GroupFolder.pick(startingAt: (draft.cwd as NSString).expandingTildeInPath) { draft.cwd = GroupFolder.tilde(f) }
                                }
                            }
                        }
                    }
                    Picker("Group", selection: groupBinding) {
                        ForEach(groups) { g in
                            Label { Text(model.title(of: g)) } icon: { model.groupMenuIcon(g) }.tag(g.id)
                        }
                        Divider()
                        Text(draft.groupName.isEmpty ? "The folder’s Group, or a new one" : "“\(draft.groupName)” (closed)").tag("")
                    }
                } footer: {
                    Text("Each run opens a tab here. If the Group has been closed, the run brings it back.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Section("When") {
                    HStack(spacing: 6) {
                        chip("Every day", .everyDay)
                        chip("Weekdays", .weekdays)
                        chip("Weekends", .weekends)
                        chip("Custom days", .custom)
                        chip("Cron", .cron)
                    }
                    if when == .custom {
                        HStack(spacing: 6) {
                            ForEach([1, 2, 3, 4, 5, 6, 0] as [Int32], id: \.self) { d in
                                dayChip(d)
                            }
                        }
                    }
                    if when == .cron {
                        TextField("Cron", text: $draft.cron, prompt: Text("30 8 * * 1-5"))
                            .font(.system(.body, design: .monospaced))
                        Text("Five fields (minute, hour, day of month, month, weekday), in this computer’s time.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    } else {
                        DatePicker("Time", selection: timeBinding, displayedComponents: .hourAndMinute)
                    }
                }

                Section {
                    Picker("If still running", selection: $draft.overlap) {
                        Text(ScheduleText.overlap(.skip)).tag(Schedule.Overlap.skip)
                        Text(ScheduleText.overlap(.stopPrevious)).tag(Schedule.Overlap.stopPrevious)
                        Text(ScheduleText.overlap(.runBoth)).tag(Schedule.Overlap.runBoth)
                    }
                    if draft.kind != "command" {
                        Picker("Give up after", selection: giveUpBinding) {
                            ForEach(giveUpChoices, id: \.self) { m in
                                Text(Schedule.durationText(minutes: m)).tag(m)
                            }
                        }
                        .help("A run waiting on you this long is stopped")
                    }
                    Stepper("Keep the last \(draft.keptRuns) run\(draft.keptRuns == 1 ? "" : "s")", value: keepBinding, in: 1...30)
                    Picker("Notify me", selection: notifyBinding) {
                        Text(ScheduleText.notify(.ready)).tag(Schedule.Notify.ready)
                        Text(ScheduleText.notify(.problems)).tag(Schedule.Notify.problems)
                        Text(ScheduleText.notify(.never)).tag(Schedule.Notify.never)
                    }
                    if draft.kind == "command" {
                        Toggle("Close the tab when it succeeds", isOn: $draft.closeOnSuccess)
                    }
                } footer: {
                    Text(draft.kind == "command"
                         ? "Older runs’ tabs close as new ones open, except ones you’ve typed in."
                         : "Gives up only while the agent waits on you. Older runs’ tabs close as new ones open, except ones you’ve typed in. Questions always go to Needs You.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                if draft.kind == "claude" {
                    Section {
                        if draft.allowedTools.isEmpty {
                            Text("None yet. When a run asks, “Always Allow for This Schedule” in Needs You adds the tool here.")
                                .font(.callout)
                                .foregroundStyle(.secondary)
                        }
                        ForEach(draft.allowedTools, id: \.self) { tool in
                            HStack {
                                Text(tool).font(.system(.body, design: .monospaced)).lineLimit(1).truncationMode(.middle)
                                Spacer()
                                Button {
                                    draft.allowedTools.removeAll { $0 == tool }
                                } label: { Image(systemName: "minus.circle.fill").foregroundStyle(.secondary) }
                                    .buttonStyle(.borderless)
                                    .help("Ask again in later runs")
                                    .accessibilityLabel("Remove \(tool)")
                            }
                        }
                    } header: {
                        Text("Remembered tools")
                    }
                }

                if draft.kind == "codex" {
                    Section("Codex") {
                        Picker("Sandbox", selection: $draft.codexSandbox) {
                            Text("Your Codex settings").tag("")
                            Divider()
                            Text("Read only").tag("read-only")
                            Text("Workspace write").tag("workspace-write")
                            Text("Full access (no sandbox)").tag("danger-full-access")
                        }
                        Picker("Approval", selection: $draft.codexApproval) {
                            Text("Your Codex settings").tag("")
                            Divider()
                            Text("Untrusted commands").tag("untrusted")
                            Text("On failure").tag("on-failure")
                            Text("On request").tag("on-request")
                            Text("Never").tag("never")
                        }
                    }
                }

                if let error {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                        .font(.callout)
                }
            }
            .formStyle(.grouped)

            Divider()
            HStack(spacing: 8) {
                Text(nextRunText)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                if let c = computer {
                    ComputerBadge(c, size: 12)
                    Text("On \(c.name)").font(.callout).foregroundStyle(.secondary)
                }
                Spacer()
                Button("Run Now") { save(thenRun: true) }
                    .disabled(saving || !canSave)
                    .help(isNew ? "Save it and run it now" : "Save any changes and run it now")
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button(isNew ? "Schedule" : "Save") { save(thenRun: false) }
                    .keyboardShortcut(.defaultAction)
                    .disabled(saving || !canSave)
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 12)
        }
        .frame(width: 580, height: 720)
        .onChange(of: ScheduleUI.shared.prefill, initial: true) { _, p in
            guard let p, p.request == request else { return }
            ScheduleUI.shared.prefill = nil
            if draft.name == original.name, !p.name.isEmpty { draft.name = p.name }
            if draft.prompt.isEmpty, draft.kind != "command" { draft.prompt = p.prompt }
        }
    }

    // MARK: Fields

    private var namePrompt: String {
        let p = draft.prompt.trimmingCharacters(in: .whitespacesAndNewlines)
        return p.isEmpty ? "Morning briefing" : String(p.split(separator: "\n").first?.prefix(40) ?? "")
    }

    private var groupBinding: Binding<String> {
        Binding(get: { draft.groupID }, set: { id in
            draft.groupID = id
            if let g = groups.first(where: { $0.id == id }) {
                draft.groupName = g.name
                draft.groupIcon = g.icon
            }
        })
    }

    private var timeBinding: Binding<Date> {
        Binding(get: {
            let m = Int(draft.minuteOfDay)
            return Calendar.current.date(bySettingHour: m / 60, minute: m % 60, second: 0, of: Date()) ?? Date()
        }, set: { d in
            let c = Calendar.current.dateComponents([.hour, .minute], from: d)
            draft.minuteOfDay = Int32((c.hour ?? 0) * 60 + (c.minute ?? 0))
        })
    }

    private var giveUpChoices: [Int] {
        let base = [60, 120, 240, 480, 1440]
        return base.contains(draft.givesUpAfter) ? base : (base + [draft.givesUpAfter]).sorted()
    }

    private var giveUpBinding: Binding<Int> {
        Binding(get: { draft.givesUpAfter }, set: { draft.giveUpMinutes = Int32($0) })
    }

    private var keepBinding: Binding<Int> {
        Binding(get: { draft.keptRuns }, set: { draft.keepRuns = Int32($0) })
    }

    /// Shows the default for its kind until you pick one.
    private var notifyBinding: Binding<Schedule.Notify> {
        Binding(get: { draft.effectiveNotify }, set: { draft.notify = $0 })
    }

    private func chip(_ title: String, _ value: When) -> some View {
        Button(title) { when = value }
            .buttonStyle(ChipStyle(on: when == value))
    }

    private func dayChip(_ d: Int32) -> some View {
        Button(Schedule.weekdaySymbols[Int(d)]) {
            if days.contains(d) { if days.count > 1 { days.remove(d) } } else { days.insert(d) }
        }
        .buttonStyle(ChipStyle(on: days.contains(d)))
    }

    // MARK: Saving

    private var weekdays: [Int32] {
        switch when {
        case .weekdays: [1, 2, 3, 4, 5]
        case .weekends: [0, 6]
        case .custom: days.sorted()
        default: []
        }
    }

    private var canSave: Bool {
        !draft.prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !draft.cwd.trimmingCharacters(in: .whitespaces).isEmpty
            && (when != .cron || !draft.cron.trimmingCharacters(in: .whitespaces).isEmpty)
    }

    /// "Next run: Tomorrow at 09:00", as the daemon will have it.
    private var nextRunText: String {
        if draft.paused { return "Paused" }
        if when == .cron {
            if draft.cron.trimmingCharacters(in: .whitespaces) == original.cron, original.nextRunUnixMs > 0 {
                return "Next run: " + Schedule.dateText(original.nextRunUnixMs)
            }
            return "Next run: worked out when saved"
        }
        guard let d = Schedule.nextRun(weekdays: weekdays, minuteOfDay: Int(draft.minuteOfDay)) else { return "" }
        return "Next run: " + Schedule.dateText(Int64(d.timeIntervalSince1970 * 1000))
    }

    private func save(thenRun: Bool) {
        var s = draft
        s.weekdays = when == .cron ? [] : weekdays
        if when != .cron { s.cron = "" }
        s.cron = s.cron.trimmingCharacters(in: .whitespaces)
        s.cwd = s.cwd.trimmingCharacters(in: .whitespaces) // ~ is the daemon's to expand, on its computer
        if s.groupID.isEmpty, let closed = closedGroupID { s.groupID = closed }
        // Changed elsewhere while this was open: a pause, a tool remembered
        // from Needs You. Kept, less the tools removed here.
        if let current = model.schedules[original.id] {
            s.paused = current.paused
            s.allowedTools = current.allowedTools.filter { draft.allowedTools.contains($0) || !original.allowedTools.contains($0) }
        }
        // Only what's yours to set: the daemon keeps its own fields.
        s.runs = []
        s.nextRunUnixMs = 0
        s.error = ""
        saving = true
        error = nil
        Task {
            do {
                // Unchanged and only to run: no need to save.
                let saved = !isNew && s == stripped(original) ? original : try await model.saveSchedule(s)
                if thenRun {
                    ScheduleUI.shared.showingList = false
                    dismiss()
                    do { try await model.runScheduleNow(saved.id) } catch { model.showToast("Couldn’t run “\(saved.name)”: \(error.localizedDescription)") }
                    return
                }
                ScheduleUI.shared.selected = saved.id
                dismiss()
            } catch {
                self.error = error.localizedDescription
                saving = false
            }
        }
    }

    /// The original as save would send it, to tell whether anything changed.
    private func stripped(_ o: Schedule) -> Schedule {
        var s = o
        if s.computerID.isEmpty || s.computerID == model.myUserID { s.cwd = GroupFolder.tilde(s.cwd) }
        s.runs = []
        s.nextRunUnixMs = 0
        s.error = ""
        return s
    }
}

/// The When row's choices: a capsule, filled with the accent while chosen.
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
