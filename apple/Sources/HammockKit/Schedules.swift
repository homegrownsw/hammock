import Foundation
import SwiftUI

/// Scheduled tasks (the daemon's schedules.go): an agent or a command run in
/// a Group on a schedule. Each run is a tab there. Shared by the Mac and iOS.
public typealias Schedule = Hammock_V1_Schedule
public typealias ScheduleRun = Hammock_V1_ScheduleRun

// MARK: State

extension AppModel {
    /// Keeps the schedules from a state update, and tells onScheduleRun
    /// about runs that ended since the last one.
    func applySchedules(_ u: Hammock_V1_StateUpdate) {
        if u.full {
            var all: [String: Schedule] = [:]
            for s in u.schedules { all[s.id] = s }
            if all != schedules { schedules = all }
            return
        }
        for s in u.schedules {
            let old = schedules[s.id]
            if old == s { continue }
            schedules[s.id] = s
            // Only what you could have seen happen: a run ending now, not
            // one that had ended before this app connected.
            // A schedule first seen now (a linked computer's arriving once
            // it connects) brings its history, not news.
            guard let onScheduleRun, let old else { continue }
            for r in s.runs where r.status.ended {
                let before = old.runs.first { $0.id == r.id }
                if before == nil || before?.status.ended == false { onScheduleRun(s, r) }
            }
        }
        for id in u.deletedScheduleIds where schedules[id] != nil { schedules[id] = nil }
    }

    /// Every schedule: the ones still to run (soonest first), then paused
    /// and finished ones by name.
    public var sortedSchedules: [Schedule] {
        schedules.values.sorted { a, b in
            let (x, y) = (a.nextRunUnixMs, b.nextRunUnixMs)
            if (x > 0) != (y > 0) { return x > 0 }
            if x != y, x > 0 { return x < y }
            let c = a.name.localizedStandardCompare(b.name)
            return c == .orderedSame ? a.id < b.id : c == .orderedAscending
        }
    }

    /// The schedule that runs next (none paused, or due again).
    public var nextSchedule: Schedule? {
        schedules.values.filter { $0.nextRunUnixMs > 0 && !$0.paused }.min { $0.nextRunUnixMs < $1.nextRunUnixMs }
    }

    /// The schedule a pane is a run of.
    public func schedule(for pane: Pane) -> Schedule? {
        pane.scheduleID.isEmpty ? nil : schedules[pane.scheduleID]
    }

    /// The schedule a tab holds a run of (its first such pane's).
    public func schedule(of tab: WorkspaceTab) -> Schedule? {
        for p in panes(in: tab) { if let s = schedule(for: p) { return s } }
        return nil
    }

    /// The linked computer a schedule runs on (nil: this one).
    public func computer(of schedule: Schedule) -> Computer? {
        guard !schedule.computerID.isEmpty, schedule.computerID != myUserID else { return nil }
        return computer(schedule.computerID).flatMap { $0.thisComputer ? nil : $0 }
    }

    /// The Groups a schedule's runs can open in: its computer's own (not
    /// ones shared with you). computerID "" or this one's: this computer.
    public func scheduleGroups(on computerID: String) -> [WorkspaceGroup] {
        if computerID.isEmpty || computerID == myUserID { return groups.filter { !$0.isRemote } }
        return groups.filter { $0.isOnLinkedComputer && $0.hostUserID == computerID }
    }
}

// MARK: Actions

extension AppModel {
    private func scheduleCall(_ build: @escaping (inout ClientMessage) -> Void) async throws -> ServerMessage {
        guard let conn = connection else { throw DaemonError.notConnected }
        return try await conn.call(build)
    }

    /// Creates (no id) or replaces a schedule; the daemon's copy, or why it
    /// wouldn't (a folder that isn't there, a bad cron line…).
    @discardableResult
    public func saveSchedule(_ s: Schedule) async throws -> Schedule {
        let saved = try await scheduleCall { $0.saveSchedule = .with { $0.schedule = s } }.scheduleReply.schedule
        if !saved.id.isEmpty { schedules[saved.id] = saved }
        return saved
    }

    public func deleteSchedule(_ id: String) async throws {
        _ = try await scheduleCall { $0.deleteSchedule = .with { $0.scheduleID = id } }
        schedules[id] = nil
    }

    /// Runs it now, in a new tab of its Group, and goes to that tab (it
    /// returns its id).
    @discardableResult
    public func runScheduleNow(_ id: String, reveal: Bool = true) async throws -> String {
        let tab = try await scheduleCall { $0.runSchedule = .with { $0.scheduleID = id } }.created.id
        guard reveal, !tab.isEmpty else { return tab }
        // Its tab comes in the next state update (from another computer, a moment later).
        for _ in 0..<50 where tabs[tab] == nil { try? await Task.sleep(for: .milliseconds(40)) }
        if tabs[tab] != nil { selectedTabID = tab }
        return tab
    }

    public func setSchedulePaused(_ id: String, _ paused: Bool) async throws {
        _ = try await scheduleCall { $0.setSchedulePaused = .with { $0.scheduleID = id; $0.paused = paused } }
    }

    /// Skips its next run (skip false: runs it after all).
    public func skipNextRun(_ id: String, skip: Bool = true) async throws {
        _ = try await scheduleCall { $0.skipScheduleRun = .with { $0.scheduleID = id; $0.skip = skip } }
    }

    /// Goes to a run: its tab, or (closed) its agent's session resumed in a
    /// new tab of the schedule's Group.
    public func open(_ run: ScheduleRun, of s: Schedule) async {
        if !run.tabClosed, tabs[run.tabID] != nil {
            selectedTabID = run.tabID
            if panes[run.paneID] != nil { focusedPaneID = run.paneID }
            return
        }
        guard s.kind != "command", !run.sessionID.isEmpty else { return }
        let session = SessionInfo.with { $0.harness = s.kind; $0.id = run.sessionID }
        let group = groups.contains { $0.id == s.groupID } ? s.groupID : nil
        await resumeSession(session, groupID: group, computerID: computer(of: s)?.userID ?? "")
    }
}

// MARK: Words

extension ScheduleRun.Status {
    /// Over: it won't change again.
    public var ended: Bool {
        switch self {
        case .unspecified, .running, .waiting: false
        default: true
        }
    }

    /// Worth telling you about when you only want to hear of problems.
    public var isProblem: Bool { self == .failed || self == .gaveUp }

    public var label: String {
        switch self {
        case .running: "Running"
        case .waiting: "Waiting for you"
        case .ready: "Ready"
        case .failed: "Failed"
        case .gaveUp: "Gave up"
        case .skipped: "Skipped"
        case .missed: "Missed"
        case .stopped: "Stopped"
        default: "Unknown"
        }
    }

    public var symbol: String {
        switch self {
        case .running: "circle.dotted.circle"
        case .waiting: "exclamationmark.circle.fill"
        case .ready: "checkmark.circle.fill"
        case .failed: "xmark.circle.fill"
        case .gaveUp: "hourglass.circle.fill"
        case .skipped: "forward.circle"
        case .missed: "moon.circle"
        case .stopped: "stop.circle"
        default: "circle"
        }
    }

    public var color: Color {
        switch self {
        case .running: .blue
        case .waiting: .orange
        case .ready: .green
        case .failed, .gaveUp: .red
        default: .secondary
        }
    }
}

extension Schedule {
    /// What it runs, in words: "Claude Code", "Codex" or "Command".
    @MainActor public var kindName: String { Self.kindName(kind) }

    @MainActor public static func kindName(_ kind: String) -> String {
        switch kind {
        case "command": "Command"
        case "": "Claude Code"
        default: AgentCatalog.name(of: kind)
        }
    }

    /// Run tabs kept (the daemon's default for 0).
    public var keptRuns: Int { keepRuns > 0 ? Int(keepRuns) : 7 }
    /// Minutes a run may wait on you before it's stopped (the daemon's default for 0).
    public var givesUpAfter: Int { giveUpMinutes > 0 ? Int(giveUpMinutes) : 240 }

    /// When it tells you, with the default worked out from its kind.
    public var effectiveNotify: Notify {
        notify != .default ? notify : (kind == "command" ? .problems : .ready)
    }

    /// Whether a run ending like this should tell you.
    public func notifies(_ status: ScheduleRun.Status) -> Bool {
        switch effectiveNotify {
        case .never: false
        case .problems: status.isProblem
        default: status == .ready || status.isProblem
        }
    }

    /// When it runs: "Weekdays at 08:30", "Every day at 18:00", "Mon, Thu at
    /// 09:15", or its cron line.
    public var whenText: String {
        if !cron.isEmpty { return "Cron \(cron)" }
        return Self.daysText(weekdays) + " at " + Self.timeText(Int(minuteOfDay))
    }

    /// Short weekday names (Sun first), in the user's language.
    public static var weekdaySymbols: [String] { Calendar.current.shortWeekdaySymbols }

    public static func daysText(_ days: [Int32]) -> String {
        let set = Set(days)
        if set.isEmpty || set.count == 7 { return "Every day" }
        if set == [1, 2, 3, 4, 5] { return "Weekdays" }
        if set == [0, 6] { return "Weekends" }
        // Monday first, as most of the world's weeks.
        return [1, 2, 3, 4, 5, 6, 0].filter { set.contains($0) }.map { weekdaySymbols[Int($0)] }.joined(separator: ", ")
    }

    /// A time of day (minutes past midnight) as the user's clock shows it.
    public static func timeText(_ minute: Int) -> String {
        let d = Calendar.current.date(bySettingHour: minute / 60, minute: minute % 60, second: 0, of: Date()) ?? Date()
        return d.formatted(date: .omitted, time: .shortened)
    }

    /// "Today at 08:30", "Tomorrow at 08:30", "Thu 6 Nov at 08:30".
    public static func dateText(_ unixMs: Int64, now: Date = Date()) -> String {
        let d = Date(timeIntervalSince1970: Double(unixMs) / 1000)
        let time = d.formatted(date: .omitted, time: .shortened)
        let cal = Calendar.current
        if cal.isDate(d, inSameDayAs: now) { return "Today at \(time)" }
        if let t = cal.date(byAdding: .day, value: 1, to: now), cal.isDate(d, inSameDayAs: t) { return "Tomorrow at \(time)" }
        if let y = cal.date(byAdding: .day, value: -1, to: now), cal.isDate(d, inSameDayAs: y) { return "Yesterday at \(time)" }
        let sameYear = cal.component(.year, from: d) == cal.component(.year, from: now)
        let day = d.formatted(sameYear ? .dateTime.weekday(.abbreviated).day().month(.abbreviated) : .dateTime.day().month(.abbreviated).year())
        return "\(day) at \(time)"
    }

    /// "4 hours", "90 minutes", "1 hour".
    public static func durationText(minutes: Int) -> String {
        if minutes % 60 == 0 { return minutes == 60 ? "1 hour" : "\(minutes / 60) hours" }
        return "\(minutes) minutes"
    }

    /// The next time weekdays/minute fall after now (cron isn't worked out
    /// here: the daemon's next_run_unix_ms has it once saved).
    public static func nextRun(weekdays: [Int32], minuteOfDay: Int, after now: Date = Date()) -> Date? {
        let cal = Calendar.current
        let days = Set(weekdays)
        for offset in 0..<8 {
            guard let day = cal.date(byAdding: .day, value: offset, to: now),
                  let at = cal.date(bySettingHour: minuteOfDay / 60, minute: minuteOfDay % 60, second: 0, of: day) else { continue }
            let wd = Int32(cal.component(.weekday, from: at) - 1)
            if at > now, days.isEmpty || days.contains(wd) { return at }
        }
        return nil
    }

    /// What its last run did, for lists: "Ready · Today at 08:31".
    public var lastRunText: String? {
        guard let r = runs.first else { return nil }
        let at = r.startedUnixMs > 0 ? r.startedUnixMs : r.dueUnixMs
        return at > 0 ? "\(r.status.label) · \(Self.dateText(at))" : r.status.label
    }
}

extension ScheduleRun {
    /// How long it ran (or has been running), as "4m", "1h 12m", "38s".
    public func durationText(now: Date = Date()) -> String? {
        guard startedUnixMs > 0 else { return nil }
        let end = endedUnixMs > 0 ? Double(endedUnixMs) / 1000 : now.timeIntervalSince1970
        let secs = max(0, Int(end - Double(startedUnixMs) / 1000))
        if secs < 60 { return "\(secs)s" }
        let m = secs / 60
        if m < 60 { return "\(m)m" }
        return m % 60 == 0 ? "\(m / 60)h" : "\(m / 60)h \(m % 60)m"
    }

    /// When it started (or was due), for a list of runs.
    public var whenUnixMs: Int64 { startedUnixMs > 0 ? startedUnixMs : dueUnixMs > 0 ? dueUnixMs : endedUnixMs }

    /// The tab it ran in is still open.
    @MainActor public func hasTab(in model: AppModel) -> Bool { !tabClosed && !tabID.isEmpty && model.tabs[tabID] != nil }
}
