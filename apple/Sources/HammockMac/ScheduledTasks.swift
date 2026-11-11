import AppKit
import HammockKit
import Observation
import SwiftUI

/// Scheduled tasks on the Mac: View ▸ Scheduled Tasks (the list, with the
/// selected one's runs) and File ▸ New Scheduled Task… (ScheduleEditor).
@MainActor
@Observable
final class ScheduleUI {
    static let shared = ScheduleUI()

    /// HMK_DEBUG_SCHEDULES=list or edit opens that sheet at launch (DebugSnapshot).
    private static let debug = ProcessInfo.processInfo.environment["HMK_DEBUG_SCHEDULES"]

    var showingList = ScheduleUI.debug == "list"
    /// The schedule the list shows.
    var selected: String?
    var editing: EditRequest?

    struct EditRequest: Identifiable {
        let id = UUID()
        var schedule: Schedule
    }

    func showList(select id: String? = nil) {
        if let id { selected = id }
        showingList = true
    }

    func edit(_ s: Schedule) { editing = EditRequest(schedule: s) }

    /// A new schedule on this computer: in groupID (else the Group you're
    /// in, if it's this computer's), in its folder.
    func new(_ model: AppModel, groupID: String? = nil, cwd: String? = nil, kind: String = "claude") {
        let local = model.scheduleGroups(on: "")
        let group = (groupID ?? model.currentGroup?.id).flatMap { id in local.first { $0.id == id } }
        let folder = cwd ?? group.map { model.groupFolder($0) } ?? ""
        editing = EditRequest(schedule: .with {
            $0.kind = kind
            $0.groupID = group?.id ?? ""
            $0.groupName = group?.name ?? ""
            $0.cwd = GroupFolder.tilde(folder.isEmpty ? NSHomeDirectory() : folder)
            $0.weekdays = [1, 2, 3, 4, 5]
            $0.minuteOfDay = 9 * 60
        })
    }

    /// Schedule This… on a tab: its folder, Group and agent. An agent tab's
    /// also start from its session: named after the tab (as you named it,
    /// else the session's title), with the first prompt you sent in it.
    func scheduleThis(_ model: AppModel, _ tab: WorkspaceTab) {
        let agent = model.shownAgentPane(of: tab)
        let pane = agent ?? model.panes[tab.focusedPaneID]
        new(model, groupID: tab.groupID, cwd: pane?.cwd, kind: agent?.agent.harness ?? "command")
        guard let agent, let request = editing else { return }
        let name = !tab.name.isEmpty ? tab.name : tab.autoKey.hasPrefix("agent:") ? tab.autoName : ""
        // Shown at once, the prompt filled in when the session's been read
        // (unless you've started typing one).
        editing?.schedule.name = name
        Task {
            guard let start = await model.sessionStart(harness: agent.agent.harness, id: agent.agent.sessionID),
                  editing?.id == request.id else { return }
            prefill = Prefill(request: request.id, name: name.isEmpty ? start.title ?? "" : name, prompt: start.prompt)
        }
    }

    /// What Schedule This read from a session after its editor opened: the
    /// editor (of that request) takes each field you haven't typed in.
    var prefill: Prefill?
    struct Prefill: Equatable {
        var request: UUID
        var name: String
        var prompt: String
    }

    /// Whether a tab can be scheduled from: one in a Group of this computer's.
    static func canSchedule(_ model: AppModel, _ tab: WorkspaceTab) -> Bool {
        model.group(of: tab)?.isRemote == false
    }

    /// Opens a debug sheet asked for by HMK_DEBUG_SCHEDULES=edit, once the
    /// schedules have arrived.
    func openDebugEditor(_ model: AppModel) {
        guard Self.debug == "edit" else { return }
        if let s = model.sortedSchedules.first { edit(s) } else { new(model) }
    }

    /// Hosts the sheets on the window: the list, and the editor (over the
    /// list when it's open).
    struct Host: ViewModifier {
        let model: AppModel
        @Bindable private var ui = ScheduleUI.shared

        func body(content: Content) -> some View {
            content
                .sheet(isPresented: $ui.showingList) {
                    ScheduledTasksSheet(model: model)
                        .sheet(item: Binding(get: { ui.showingList ? ui.editing : nil }, set: { ui.editing = $0 })) {
                            ScheduleEditor(model: model, original: $0.schedule, request: $0.id)
                        }
                }
                .sheet(item: Binding(get: { ui.showingList ? nil : ui.editing }, set: { ui.editing = $0 })) {
                    ScheduleEditor(model: model, original: $0.schedule, request: $0.id)
                }
        }
    }
}

/// View ▸ Scheduled Tasks: every schedule on the left (name, where and
/// when, next run, last run), the selected one's settings and runs on the right.
struct ScheduledTasksSheet: View {
    let model: AppModel
    @Bindable private var ui = ScheduleUI.shared
    @Environment(\.dismiss) private var dismiss

    private var list: [Schedule] { model.sortedSchedules }

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("Scheduled Tasks").font(.title3.weight(.semibold))
                Spacer()
                Button { ScheduleUI.shared.new(model) } label: { Label("New Scheduled Task…", systemImage: "plus") }
                    .help(Keys.hint("New Scheduled Task", "new_scheduled_task"))
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            Divider()
            if list.isEmpty {
                ContentUnavailableView {
                    Label("No Scheduled Tasks", systemImage: "clock")
                } description: {
                    Text("Run Claude Code, Codex or a command at set times. Each run opens a tab in its Group, so you can pick up where it left off.")
                } actions: {
                    Button("New Scheduled Task…") { ScheduleUI.shared.new(model) }
                        .buttonStyle(.borderedProminent)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                HStack(spacing: 0) {
                    List(selection: $ui.selected) {
                        ForEach(list, id: \.id) { s in
                            ScheduleListRow(model: model, schedule: s).tag(s.id)
                                .contextMenu { ScheduleMenuItems(model: model, schedule: s) }
                        }
                    }
                    .listStyle(.inset)
                    .frame(width: 290)
                    Divider()
                    if let s = ui.selected.flatMap({ model.schedules[$0] }) {
                        ScheduleDetail(model: model, schedule: s)
                            .id(s.id)
                    } else {
                        ContentUnavailableView("Select a Scheduled Task", systemImage: "clock")
                            .frame(maxWidth: .infinity, maxHeight: .infinity)
                    }
                }
            }
            Divider()
            HStack {
                Spacer()
                Button("Done") { dismiss() }
                    .keyboardShortcut(.defaultAction)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
        }
        .frame(width: 900, height: 600)
        .onAppear(perform: keepSelection)
        .onChange(of: list.map(\.id)) { keepSelection() }
    }

    /// Something is always selected while there's anything to select.
    private func keepSelection() {
        if ui.selected.flatMap({ model.schedules[$0] }) == nil { ui.selected = list.first?.id }
    }
}

/// A schedule in the list: name, where and when, next run, last run.
struct ScheduleListRow: View {
    let model: AppModel
    let schedule: Schedule

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 5) {
                Text(schedule.name).fontWeight(.medium).lineLimit(1)
                if let c = model.computer(of: schedule) { ComputerBadge(c, size: 11) }
                Spacer(minLength: 4)
                if let r = schedule.runs.first {
                    Image(systemName: r.status.symbol)
                        .foregroundStyle(r.status.color)
                        .font(.caption)
                        .help("Last run: \(r.status.label)")
                }
            }
            Text(ScheduleText.where(model, schedule))
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Text(ScheduleText.next(schedule))
                .font(.caption)
                .foregroundStyle(schedule.error.isEmpty ? .tertiary : .secondary)
                .lineLimit(1)
        }
        .padding(.vertical, 3)
    }
}

/// How schedules are put in the Mac's lists and menus.
enum ScheduleText {
    /// "🗞️ Notes · Claude Code · Weekdays at 08:30".
    @MainActor static func `where`(_ model: AppModel, _ s: Schedule) -> String {
        [groupName(model, s), s.kindName, s.whenText].filter { !$0.isEmpty }.joined(separator: " · ")
    }

    /// Its Group's name as the sidebar has it (the one remembered, once closed).
    @MainActor static func groupName(_ model: AppModel, _ s: Schedule) -> String {
        if let g = model.groups.first(where: { $0.id == s.groupID }) {
            return (g.icon.isEmpty ? "" : g.icon + " ") + model.title(of: g)
        }
        return (s.groupIcon.isEmpty ? "" : s.groupIcon + " ") + s.groupName
    }

    /// "Next: Today at 08:30", "Paused", "Skipping Thu 6 Nov at 08:30", or what's wrong.
    static func next(_ s: Schedule) -> String {
        if !s.error.isEmpty { return s.error }
        if s.paused { return "Paused" }
        guard s.nextRunUnixMs > 0 else { return "Not due again" }
        if s.skipUntilUnixMs > s.nextRunUnixMs {
            return "Skipping " + Schedule.dateText(s.nextRunUnixMs)
        }
        return "Next: " + Schedule.dateText(s.nextRunUnixMs)
    }

    static func overlap(_ o: Schedule.Overlap) -> String {
        switch o {
        case .stopPrevious: "Stop the last run, start fresh"
        case .runBoth: "Run both"
        default: "Skip this run"
        }
    }

    static func notify(_ n: Schedule.Notify) -> String {
        switch n {
        case .problems: "Only about problems"
        case .never: "Never"
        default: "When it’s ready"
        }
    }
}

/// A schedule's actions, for its right-click menu.
struct ScheduleMenuItems: View {
    let model: AppModel
    let schedule: Schedule

    var body: some View {
        Button("Run Now") { ScheduleActions.runNow(model, schedule) }
        Button(schedule.paused ? "Resume" : "Pause") { ScheduleActions.setPaused(model, schedule, !schedule.paused) }
        Button("Edit…") { ScheduleUI.shared.edit(schedule) }
        Divider()
        Button("Delete…", role: .destructive) { ScheduleActions.confirmDelete(model, schedule) }
    }
}

/// What the list's buttons, menus and the palette do, with errors said in a toast.
@MainActor
enum ScheduleActions {
    static func runNow(_ model: AppModel, _ s: Schedule) {
        ScheduleUI.shared.showingList = false
        Task {
            do { try await model.runScheduleNow(s.id) } catch { model.showToast("Couldn’t run “\(s.name)”: \(error.localizedDescription)") }
        }
    }

    static func setPaused(_ model: AppModel, _ s: Schedule, _ paused: Bool) {
        Task {
            do { try await model.setSchedulePaused(s.id, paused) } catch { model.showToast(error.localizedDescription) }
        }
    }

    static func skip(_ model: AppModel, _ s: Schedule, _ skip: Bool) {
        Task {
            do { try await model.skipNextRun(s.id, skip: skip) } catch { model.showToast(error.localizedDescription) }
        }
    }

    /// Asks first (an alert: the list may be a sheet already).
    static func confirmDelete(_ model: AppModel, _ s: Schedule) {
        let alert = NSAlert()
        alert.messageText = "Delete “\(s.name)”?"
        alert.informativeText = "It won’t run again. Tabs from its runs stay open."
        alert.addButton(withTitle: "Delete").hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        Task {
            do { try await model.deleteSchedule(s.id) } catch { model.showToast("Couldn’t delete it: \(error.localizedDescription)") }
        }
    }

    static func open(_ model: AppModel, _ run: ScheduleRun, of s: Schedule) {
        ScheduleUI.shared.showingList = false
        Task { await model.open(run, of: s) }
    }
}

/// The selected schedule: its actions, settings and runs.
struct ScheduleDetail: View {
    let model: AppModel
    let schedule: Schedule

    /// Its next run is skipped (Skip Next Run).
    private var skipping: Bool { schedule.nextRunUnixMs > 0 && schedule.skipUntilUnixMs > schedule.nextRunUnixMs }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                header
                actions
                if !schedule.error.isEmpty {
                    Label(schedule.error, systemImage: "exclamationmark.triangle.fill")
                        .font(.callout)
                        .foregroundStyle(.orange)
                }
                details
                runs
            }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Text(schedule.name).font(.title2.weight(.semibold)).lineLimit(2)
                if let c = model.computer(of: schedule) { ComputerBadge(c, size: 14) }
                if schedule.paused {
                    Text("Paused")
                        .font(.caption.weight(.medium))
                        .padding(.horizontal, 7).padding(.vertical, 2)
                        .background(.quaternary, in: Capsule())
                }
            }
            Text(ScheduleText.where(model, schedule))
                .foregroundStyle(.secondary)
        }
    }

    private var actions: some View {
        HStack(spacing: 8) {
            Button(schedule.paused ? "Resume" : "Pause") { ScheduleActions.setPaused(model, schedule, !schedule.paused) }
            Button("Edit…") { ScheduleUI.shared.edit(schedule) }
            Button("Run Now") { ScheduleActions.runNow(model, schedule) }
            if !schedule.paused, schedule.nextRunUnixMs > 0 {
                Button(skipping ? "Don’t Skip" : "Skip Next Run") { ScheduleActions.skip(model, schedule, !skipping) }
            }
            Spacer()
            Button("Delete…", role: .destructive) { ScheduleActions.confirmDelete(model, schedule) }
        }
        .controlSize(.regular)
    }

    private var details: some View {
        Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 14, verticalSpacing: 7) {
            row("Next run", ScheduleText.next(schedule).replacingOccurrences(of: "Next: ", with: ""))
            row(schedule.kind == "command" ? "Command" : "Prompt", schedule.prompt, mono: schedule.kind == "command", lines: 3)
            row("Folder", GroupFolder.tilde(schedule.cwd), mono: true)
            row("If still running", ScheduleText.overlap(schedule.overlap))
            if schedule.kind != "command" {
                row("Gives up", "After waiting \(Schedule.durationText(minutes: schedule.givesUpAfter)) for you")
            }
            row("Keeps", "The last \(schedule.keptRuns) run\(schedule.keptRuns == 1 ? "" : "s")’ tabs, and any you’ve typed in")
            row("Notifies", ScheduleText.notify(schedule.effectiveNotify))
            if schedule.kind == "claude" {
                row("Remembered tools", schedule.allowedTools.isEmpty ? "None" : "\(schedule.allowedTools.count): " + schedule.allowedTools.joined(separator: ", "), lines: 2)
            }
            if schedule.kind == "codex" {
                row("Codex", [schedule.codexSandbox.isEmpty ? "Your sandbox setting" : schedule.codexSandbox,
                              schedule.codexApproval.isEmpty ? "your approval setting" : "approval " + schedule.codexApproval].joined(separator: ", "))
            }
            let made = schedule.createdUnixMs > 0 ? " · " + Schedule.dateText(schedule.createdUnixMs) : ""
            row("Made by", (schedule.createdBy.isEmpty ? "You" : schedule.createdBy) + made)
        }
    }

    private func row(_ label: String, _ value: String, mono: Bool = false, lines: Int = 1) -> some View {
        GridRow {
            Text(label)
                .foregroundStyle(.secondary)
                .gridColumnAlignment(.trailing)
            Text(value)
                .font(mono ? .system(.body, design: .monospaced) : .body)
                .lineLimit(lines)
                .truncationMode(.middle)
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    @ViewBuilder
    private var runs: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Runs").font(.headline)
            if schedule.runs.isEmpty {
                Text("None yet.").foregroundStyle(.secondary)
            } else {
                VStack(spacing: 0) {
                    ForEach(Array(schedule.runs.enumerated()), id: \.element.id) { i, r in
                        if i > 0 { Divider() }
                        RunRow(model: model, schedule: schedule, run: r)
                    }
                }
                .padding(.horizontal, 10)
                .background(.background.opacity(0.6), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                .overlay { RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(.quaternary) }
            }
        }
    }
}

/// One run: how it went, when, for how long, its last words, and a way back to it.
struct RunRow: View {
    let model: AppModel
    let schedule: Schedule
    let run: ScheduleRun

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: run.status.symbol)
                .foregroundStyle(run.status.color)
                .frame(width: 16)
                .help(run.status.label)
                .accessibilityLabel(run.status.label)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 6) {
                    Text(run.whenUnixMs > 0 ? Schedule.dateText(run.whenUnixMs) : run.status.label)
                    if run.manual { marker("Run Now") }
                    if run.catchUp { marker("catch-up") }
                    if let d = run.durationText() {
                        Text(d).foregroundStyle(.secondary).monospacedDigit()
                    }
                }
                Text(note)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .help(note)
            }
            Spacer(minLength: 8)
            link
        }
        .padding(.vertical, 7)
    }

    /// Its note, else its status (with a command's exit code).
    private var note: String {
        if !run.note.isEmpty { return run.note }
        if schedule.kind == "command", run.status == .failed { return "Exit code \(run.exitCode)" }
        return run.status.label
    }

    @ViewBuilder
    private var link: some View {
        if run.hasTab(in: model) {
            Button("Open Tab") { ScheduleActions.open(model, run, of: schedule) }
                .buttonStyle(.link)
        } else if schedule.kind != "command", !run.sessionID.isEmpty {
            Button("Resume Session") { ScheduleActions.open(model, run, of: schedule) }
                .buttonStyle(.link)
                .help("Its tab has closed: carry on with its session in a new tab")
        }
    }

    private func marker(_ text: String) -> some View {
        Text(text)
            .font(.caption2.weight(.medium))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 5).padding(.vertical, 1)
            .background(.quaternary, in: Capsule())
    }
}

/// In the sidebar's footer, from half an hour before the next run is due:
/// what it is. A click opens the list.
struct ScheduleFooter: View {
    let model: AppModel
    /// How long before a run the footer says it's next.
    static let lead: TimeInterval = 30 * 60

    var body: some View {
        if !model.schedules.isEmpty {
            // Each minute, so "today" and "tomorrow" stay true, and it shows
            // as the next run comes within the half hour.
            TimelineView(.periodic(from: .now, by: 60)) { ctx in
                if let s = model.nextSchedule, Date(timeIntervalSince1970: TimeInterval(s.nextRunUnixMs) / 1000).timeIntervalSince(ctx.date) <= Self.lead {
                    row(now: ctx.date)
                }
            }
        }
    }

    private func row(now: Date) -> some View {
        HStack(spacing: 6) {
            Image(systemName: "clock")
            Text(text(now: now)).lineLimit(1).truncationMode(.middle)
            Spacer(minLength: 0)
        }
        .font(.caption)
        .foregroundStyle(.secondary)
        .padding(.horizontal, 16)
        .padding(.top, 8)
        .contentShape(Rectangle())
        .onTapGesture { ScheduleUI.shared.showList(select: model.nextSchedule?.id) }
        .help(Keys.hint("Scheduled Tasks", "scheduled_tasks"))
    }

    private func text(now: Date) -> String {
        guard let s = model.nextSchedule else { return "No scheduled runs coming up" }
        let when = Schedule.dateText(s.nextRunUnixMs, now: now)
        // "Today at 08:30" reads as "Next: Morning briefing at 08:30".
        if when.hasPrefix("Today ") { return "Next: \(s.name) \(when.dropFirst("Today ".count))" }
        return "Next: \(s.name), \(when.hasPrefix("Tomorrow") ? "tomorrow" + when.dropFirst("Tomorrow".count) : when)"
    }
}
