import HammockKit
import SwiftUI

// Scheduled tasks on the iPhone: seen, run, skipped and paused here, made
// and edited on the Mac (or with `hmk schedule`; the daemon refuses
// SaveSchedule and DeleteSchedule from phones). The cockpit shows runs that
// are ready and what's coming up; the list and each schedule's page are
// pushed onto the cockpit's navigation as ScheduleRoute ids.

/// The ids the cockpit's navigation pushes for schedules (its path holds
/// strings: tab and pane ids, and these).
enum ScheduleRoute {
    static let list = "schedules"
    private static let prefix = "schedule:"

    static func detail(_ id: String) -> String { prefix + id }

    /// The schedule a pushed id is the page of.
    static func scheduleID(_ route: String) -> String? {
        route.hasPrefix(prefix) ? String(route.dropFirst(prefix.count)) : nil
    }
}

/// How schedules are put on the phone (the Mac's ScheduleText, which
/// HammockMac keeps to itself).
@MainActor
enum ScheduleWords {
    /// Its Group's name with its emoji (the one remembered, once closed).
    static func group(_ model: AppModel, _ s: Schedule) -> String {
        if let g = model.groups.first(where: { $0.id == s.groupID }) {
            return (g.icon.isEmpty ? "" : g.icon + " ") + model.title(of: g)
        }
        return (s.groupIcon.isEmpty ? "" : s.groupIcon + " ") + s.groupName
    }

    /// "🗞️ Notes · Claude Code · Weekdays at 08:30".
    static func `where`(_ model: AppModel, _ s: Schedule) -> String {
        [group(model, s), s.kindName, s.whenText].filter { !$0.isEmpty }.joined(separator: " · ")
    }

    /// "Next: Today at 08:30", "Paused", "Skipping Thu 6 Nov at 08:30", or what's wrong.
    static func next(_ s: Schedule, now: Date = Date()) -> String {
        if !s.error.isEmpty { return s.error }
        if s.paused { return "Paused" }
        guard s.nextRunUnixMs > 0 else { return "Not due again" }
        if s.skipping { return "Skipping " + Schedule.dateText(s.nextRunUnixMs, now: now) }
        return "Next: " + Schedule.dateText(s.nextRunUnixMs, now: now)
    }

    /// A folder on the Mac with its home as ~.
    static func folder(_ path: String) -> String {
        path.replacingOccurrences(of: #"^/(Users|home)/[^/]+"#, with: "~", options: .regularExpression)
    }

    /// A run's note, else its status (with a command's exit code).
    static func note(_ run: ScheduleRun, of s: Schedule) -> String {
        if !run.note.isEmpty { return run.note }
        if s.kind == "command", run.status == .failed { return "Exit code \(run.exitCode)" }
        return run.status.label
    }

    /// What the run showed beside it (`hmk show`): "Showed latest.html".
    static func output(_ run: ScheduleRun) -> String? {
        guard !run.output.isEmpty else { return nil }
        let name = URL(string: run.output).flatMap { $0.scheme == "file" || $0.scheme == nil ? nil : ($0.host ?? run.output) }
            ?? (run.output as NSString).lastPathComponent
        return "Showed \(name)"
    }
}

extension Schedule {
    /// Its next run is skipped (Skip Next Run).
    var skipping: Bool { nextRunUnixMs > 0 && skipUntilUnixMs > nextRunUnixMs }
    /// It will run again on its own.
    var upcoming: Bool { nextRunUnixMs > 0 && !paused }
}

/// What the cockpit's and the list's buttons do, with errors said in a toast.
@MainActor
enum ScheduleActionsIOS {
    /// Runs it now and returns its new tab (to show), if it opened.
    static func runNow(_ model: AppModel, _ s: Schedule) async -> String? {
        do {
            let tab = try await model.runScheduleNow(s.id)
            return model.tabs[tab] != nil ? tab : nil
        } catch {
            model.showToast("Couldn’t run “\(s.name)”: \(error.localizedDescription)")
            return nil
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

    /// Goes to a run: what to push (its agent's pane, or its tab), or after
    /// resuming its session in a new tab once its own has closed.
    static func open(_ model: AppModel, _ run: ScheduleRun, of s: Schedule) async -> String? {
        if run.hasTab(in: model) {
            await model.open(run, of: s)
            return model.panes[run.paneID] != nil ? run.paneID : run.tabID
        }
        let before = model.selectedTabID
        await model.open(run, of: s)
        guard let tab = model.selectedTabID, tab != before else { return nil }
        return model.focusedPaneID.flatMap { model.panes[$0]?.tabID == tab ? $0 : nil } ?? tab
    }

    /// A run can be gone back to: its tab, or its agent's session.
    static func canOpen(_ model: AppModel, _ run: ScheduleRun, of s: Schedule) -> Bool {
        run.hasTab(in: model) || (s.kind != "command" && !run.sessionID.isEmpty)
    }
}

// MARK: The cockpit

/// Runs that finished on their own in the last 12 hours, whose tab is still
/// open and that you haven't looked at, newest first.
@MainActor
func readyRuns(_ model: AppModel, now: Date) -> [(schedule: Schedule, run: ScheduleRun)] {
    let since = Int64((now.timeIntervalSince1970 - 12 * 3600) * 1000)
    var out: [(schedule: Schedule, run: ScheduleRun)] = []
    for s in model.schedules.values {
        for r in s.runs where r.status == .ready && r.endedUnixMs >= since && r.hasTab(in: model) {
            if let p = model.panes[r.paneID], p.hasAgent {
                // Looked at, here or on the Mac.
                if p.agent.seen || p.agent.state != .idle { continue }
            } else if let opened = model.tabOpenedAt[r.tabID], opened * 1000 >= Double(r.endedUnixMs) {
                continue // a command's tab, opened on this phone since
            }
            out.append((s, r))
        }
    }
    return out.sorted { $0.run.endedUnixMs > $1.run.endedUnixMs }
}

/// "Ready for You": a card per run that's ready. Tapping one opens it.
struct ReadyRunsSection: View {
    let model: AppModel
    let runs: [(schedule: Schedule, run: ScheduleRun)]
    let open: (String) -> Void

    var body: some View {
        Section {
            ForEach(runs, id: \.run.id) { item in
                Button {
                    Task { if let id = await ScheduleActionsIOS.open(model, item.run, of: item.schedule) { open(id) } }
                } label: {
                    ReadyRunRow(model: model, schedule: item.schedule, run: item.run)
                }
                .buttonStyle(.plain)
            }
        } header: {
            Text("Ready for You")
        }
    }
}

/// A run that's ready: its schedule, Group and time, what it said, and
/// what it showed.
struct ReadyRunRow: View {
    let model: AppModel
    let schedule: Schedule
    let run: ScheduleRun

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            Image(systemName: run.status.symbol)
                .font(.system(size: 17))
                .foregroundStyle(run.status.color)
                .accessibilityLabel(run.status.label)
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(schedule.name).font(.headline).lineLimit(1)
                    if let c = model.computer(of: schedule) { ComputerBadge(c, size: 12) }
                    Spacer(minLength: 6)
                    Text(Date(timeIntervalSince1970: Double(run.endedUnixMs) / 1000).formatted(date: .omitted, time: .shortened))
                        .font(.subheadline).foregroundStyle(.secondary).monospacedDigit()
                }
                Text(ScheduleWords.group(model, schedule))
                    .font(.subheadline).foregroundStyle(.secondary).lineLimit(1)
                if !run.note.isEmpty {
                    Text(Markdown.inlineText(run.note))
                        .font(.subheadline)
                        .lineLimit(3)
                }
                if let shown = ScheduleWords.output(run) {
                    HStack(spacing: 4) {
                        Image(systemName: "doc.richtext")
                        Text(shown)
                    }
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        }
        .padding(.vertical, 2)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
        .accessibilityHint("Opens the run")
    }
}

/// "Coming Up": the next few schedules to run, with Run Now and Skip; See
/// All opens the list.
struct ComingUpSection: View {
    let model: AppModel
    let now: Date
    /// Pushes a tab, pane or ScheduleRoute.
    let push: (String) -> Void

    private var next: [Schedule] { Array(model.sortedSchedules.filter(\.upcoming).prefix(3)) }

    var body: some View {
        Section {
            let next = self.next
            if next.isEmpty {
                let paused = model.schedules.values.filter(\.paused).count
                Text(paused > 0 ? "Nothing coming up: \(paused == 1 ? "1 task is" : "\(paused) tasks are") paused." : "Nothing coming up.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            ForEach(next, id: \.id) { s in
                ComingUpRow(model: model, schedule: s, now: now, push: push)
            }
        } header: {
            HStack {
                Text("Coming Up")
                Spacer()
                Button("See All") { push(ScheduleRoute.list) }
                    .font(.subheadline)
                    .textCase(nil)
            }
        }
    }
}

/// A schedule that runs soon: its name and when, Skip and Run Now; Pause
/// and its page by swipe and long press.
struct ComingUpRow: View {
    let model: AppModel
    let schedule: Schedule
    let now: Date
    let push: (String) -> Void
    @State private var starting = false

    var body: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 5) {
                    Text(schedule.name).font(.body.weight(.medium)).lineLimit(1)
                    if let c = model.computer(of: schedule) { ComputerBadge(c, size: 12) }
                }
                when
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 4)
            Button(schedule.skipping ? "Don’t Skip" : "Skip") {
                ScheduleActionsIOS.skip(model, schedule, !schedule.skipping)
            }
            .buttonStyle(.bordered)
            .accessibilityLabel(schedule.skipping ? "Don’t Skip the Next Run" : "Skip the Next Run")
            Button {
                starting = true
                Task {
                    if let tab = await ScheduleActionsIOS.runNow(model, schedule) { push(tab) }
                    starting = false
                }
            } label: {
                if starting { ProgressView() } else { Text("Run") }
            }
            .buttonStyle(.bordered)
            .disabled(starting)
            .accessibilityLabel("Run Now")
        }
        .controlSize(.small)
        .buttonBorderShape(.capsule)
        .fixedSize(horizontal: false, vertical: true)
        .contentShape(Rectangle())
        .onTapGesture { push(ScheduleRoute.detail(schedule.id)) }
        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
            Button(schedule.paused ? "Resume" : "Pause", systemImage: schedule.paused ? "play" : "pause") {
                ScheduleActionsIOS.setPaused(model, schedule, !schedule.paused)
            }
            .tint(.orange)
        }
        .contextMenu {
            ScheduleMenuIOS(model: model, schedule: schedule, push: push)
        }
    }

    /// "Today at 08:30 · 🗞️ Notes" (the time struck through while it's
    /// skipped), saying when its last run is still going.
    private var when: Text {
        let date = Text(Schedule.dateText(schedule.nextRunUnixMs, now: now)).strikethrough(schedule.skipping)
        let running = schedule.runs.first.map { $0.status == .running || $0.status == .waiting ? "\($0.status.label) · " : "" } ?? ""
        let group = ScheduleWords.group(model, schedule)
        return Text("\(running)\(date)\(group.isEmpty ? "" : " · " + group)")
    }
}

/// A schedule's actions, for its long-press menu.
struct ScheduleMenuIOS: View {
    let model: AppModel
    let schedule: Schedule
    let push: (String) -> Void
    var showsDetails = true

    var body: some View {
        Button("Run Now", systemImage: "play.fill") {
            Task { if let tab = await ScheduleActionsIOS.runNow(model, schedule) { push(tab) } }
        }
        if schedule.upcoming {
            Button(schedule.skipping ? "Don’t Skip Next Run" : "Skip Next Run", systemImage: schedule.skipping ? "arrow.uturn.backward" : "forward.end") {
                ScheduleActionsIOS.skip(model, schedule, !schedule.skipping)
            }
        }
        Button(schedule.paused ? "Resume" : "Pause", systemImage: schedule.paused ? "play.circle" : "pause.circle") {
            ScheduleActionsIOS.setPaused(model, schedule, !schedule.paused)
        }
        if showsDetails {
            Divider()
            Button("Details", systemImage: "info.circle") { push(ScheduleRoute.detail(schedule.id)) }
        }
    }
}

// MARK: The list

/// Every scheduled task, soonest first, then paused and finished ones.
struct ScheduledTasksScreen: View {
    let model: AppModel
    @Binding var path: [String]

    var body: some View {
        TimelineView(.periodic(from: .now, by: 60)) { ctx in
            List {
                Section {
                    ForEach(model.sortedSchedules, id: \.id) { s in
                        NavigationLink(value: ScheduleRoute.detail(s.id)) {
                            ScheduleListRowIOS(model: model, schedule: s, now: ctx.date)
                        }
                        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                            Button(s.paused ? "Resume" : "Pause", systemImage: s.paused ? "play" : "pause") {
                                ScheduleActionsIOS.setPaused(model, s, !s.paused)
                            }
                            .tint(.orange)
                        }
                        .swipeActions(edge: .leading, allowsFullSwipe: true) {
                            Button("Run Now", systemImage: "play.fill") {
                                Task { if let tab = await ScheduleActionsIOS.runNow(model, s) { path.append(tab) } }
                            }
                            .tint(.accentColor)
                        }
                        .contextMenu {
                            ScheduleMenuIOS(model: model, schedule: s, push: { path.append($0) }, showsDetails: false)
                        }
                    }
                } footer: {
                    if !model.schedules.isEmpty { MacOnlyFooter() }
                }
            }
            .overlay {
                if model.schedules.isEmpty {
                    ContentUnavailableView {
                        Label("No Scheduled Tasks", systemImage: "clock")
                    } description: {
                        Text("Run Claude Code, Codex or a command at set times: each run opens a tab in its Group.\nCreate scheduled tasks on your Mac (or with `hmk schedule`).")
                    }
                }
            }
        }
        .navigationTitle("Scheduled Tasks")
        .navigationBarTitleDisplayMode(.inline)
    }
}

/// Where scheduled tasks are made.
struct MacOnlyFooter: View {
    var body: some View {
        Text("Create and edit scheduled tasks on your Mac (or with `hmk schedule`).")
    }
}

/// A schedule in the list: name, where and when, next run, last run.
struct ScheduleListRowIOS: View {
    let model: AppModel
    let schedule: Schedule
    let now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 5) {
                Text(schedule.name).font(.body.weight(.medium)).lineLimit(1)
                if let c = model.computer(of: schedule) { ComputerBadge(c, size: 12) }
                Spacer(minLength: 4)
                if let r = schedule.runs.first {
                    Image(systemName: r.status.symbol)
                        .foregroundStyle(r.status.color)
                        .font(.subheadline)
                        .accessibilityLabel("Last run: \(r.status.label)")
                }
            }
            Text(ScheduleWords.where(model, schedule))
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Text(ScheduleWords.next(schedule, now: now))
                .font(.footnote)
                .foregroundStyle(schedule.error.isEmpty ? AnyShapeStyle(.tertiary) : AnyShapeStyle(.orange))
                .lineLimit(2)
        }
        .padding(.vertical, 2)
    }
}

// MARK: A schedule's page

/// One scheduled task: its actions, when and what it runs, and its runs.
struct ScheduleDetailScreen: View {
    let model: AppModel
    let scheduleID: String
    @Binding var path: [String]
    @State private var starting = false

    var body: some View {
        Group {
            if let s = model.schedules[scheduleID] {
                TimelineView(.periodic(from: .now, by: 60)) { ctx in
                    content(s, now: ctx.date)
                }
                .navigationTitle(s.name)
            } else {
                ContentUnavailableView("Scheduled Task Deleted", systemImage: "clock.badge.xmark",
                                       description: Text("It was deleted on your Mac."))
            }
        }
        .navigationBarTitleDisplayMode(.inline)
    }

    private func content(_ s: Schedule, now: Date) -> some View {
        List {
            Section {
                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 8) {
                        Text(s.name).font(.title3.weight(.semibold)).lineLimit(2)
                        if let c = model.computer(of: s) { ComputerBadge(c, size: 14) }
                        if s.paused {
                            Text("Paused")
                                .font(.caption.weight(.medium))
                                .padding(.horizontal, 7).padding(.vertical, 2)
                                .background(.quaternary, in: Capsule())
                        }
                    }
                    Text(ScheduleWords.where(model, s))
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                    if !s.error.isEmpty {
                        Label(s.error, systemImage: "exclamationmark.triangle.fill")
                            .font(.subheadline)
                            .foregroundStyle(.orange)
                            .padding(.top, 4)
                    }
                }
                .padding(.vertical, 2)
            }
            Section {
                Button {
                    starting = true
                    Task {
                        if let tab = await ScheduleActionsIOS.runNow(model, s) { path.append(tab) }
                        starting = false
                    }
                } label: {
                    HStack {
                        Label("Run Now", systemImage: "play.fill")
                        if starting { Spacer(); ProgressView() }
                    }
                }
                .disabled(starting)
                if s.upcoming {
                    Button(s.skipping ? "Don’t Skip Next Run" : "Skip Next Run", systemImage: s.skipping ? "arrow.uturn.backward" : "forward.end") {
                        ScheduleActionsIOS.skip(model, s, !s.skipping)
                    }
                }
                Button(s.paused ? "Resume" : "Pause", systemImage: s.paused ? "play.circle" : "pause.circle") {
                    ScheduleActionsIOS.setPaused(model, s, !s.paused)
                }
            }
            Section("Details") {
                LabeledContent("When", value: s.whenText)
                LabeledContent("Next Run", value: nextRun(s, now: now))
                LabeledContent("Runs", value: s.kindName)
                VStack(alignment: .leading, spacing: 4) {
                    Text(s.kind == "command" ? "Command" : "Prompt")
                    Text(s.prompt.isEmpty ? "None" : s.prompt)
                        .font(s.kind == "command" ? .system(.subheadline, design: .monospaced) : .subheadline)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                }
                .padding(.vertical, 2)
                LabeledContent("Folder") {
                    Text(ScheduleWords.folder(s.cwd))
                        .font(.system(.subheadline, design: .monospaced))
                        .lineLimit(2)
                        .truncationMode(.middle)
                }
                LabeledContent("Group", value: ScheduleWords.group(model, s))
                if let c = model.computer(of: s) {
                    LabeledContent("Computer") {
                        HStack(spacing: 5) { ComputerBadge(c, size: 13); Text(c.name) }
                    }
                }
                if s.kind == "claude", !s.allowedTools.isEmpty {
                    LabeledContent("Always Allowed", value: s.allowedTools.joined(separator: ", "))
                }
                let made = s.createdUnixMs > 0 ? " · " + Schedule.dateText(s.createdUnixMs, now: now) : ""
                LabeledContent("Made By", value: (s.createdBy.isEmpty ? "You" : s.createdBy) + made)
            }
            Section {
                if s.runs.isEmpty {
                    Text("None yet.").foregroundStyle(.secondary)
                }
                ForEach(s.runs, id: \.id) { r in
                    if ScheduleActionsIOS.canOpen(model, r, of: s) {
                        Button {
                            Task { if let id = await ScheduleActionsIOS.open(model, r, of: s) { path.append(id) } }
                        } label: {
                            RunRowIOS(model: model, schedule: s, run: r, now: now, linked: true)
                        }
                        .buttonStyle(.plain)
                    } else {
                        RunRowIOS(model: model, schedule: s, run: r, now: now, linked: false)
                    }
                }
            } header: {
                Text("Runs")
            } footer: {
                MacOnlyFooter()
            }
            .id("runs")
        }
        #if DEBUG
        .debugScroll(to: "runs", if: "HMK_DEBUG_BOTTOM") // dev aid
        #endif
    }

    private func nextRun(_ s: Schedule, now: Date) -> String {
        let text = ScheduleWords.next(s, now: now)
        return text.hasPrefix("Next: ") ? String(text.dropFirst("Next: ".count)) : text
    }
}

/// One run: how it went, when, for how long, its note, what it showed, and
/// whether it can be gone back to (its tab, or its session resumed).
struct RunRowIOS: View {
    let model: AppModel
    let schedule: Schedule
    let run: ScheduleRun
    let now: Date
    let linked: Bool

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Image(systemName: run.status.symbol)
                .foregroundStyle(run.status.color)
                .frame(width: 18)
                .accessibilityLabel(run.status.label)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(run.whenUnixMs > 0 ? Schedule.dateText(run.whenUnixMs, now: now) : run.status.label)
                        .font(.subheadline.weight(.medium))
                        .lineLimit(1)
                    if run.manual { marker("Run Now") }
                    if run.catchUp { marker("catch-up") }
                    Spacer(minLength: 4)
                    if let d = run.durationText(now: now) {
                        Text(d).font(.subheadline).foregroundStyle(.secondary).monospacedDigit()
                    }
                }
                Text(ScheduleWords.note(run, of: schedule))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                if let shown = ScheduleWords.output(run) {
                    HStack(spacing: 4) {
                        Image(systemName: "doc.richtext")
                        Text(shown)
                    }
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                if linked, !run.hasTab(in: model) {
                    Text("Resume Session")
                        .font(.footnote.weight(.medium))
                        .foregroundStyle(.tint)
                }
            }
            if linked {
                Image(systemName: "chevron.right")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.vertical, 2)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
        .accessibilityHint(linked ? (run.hasTab(in: model) ? "Opens its tab" : "Resumes its session in a new tab") : "")
    }

    private func marker(_ text: String) -> some View {
        Text(text)
            .font(.caption2.weight(.medium))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 5).padding(.vertical, 1)
            .background(.quaternary, in: Capsule())
    }
}
