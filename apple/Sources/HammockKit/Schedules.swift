import Foundation
import SwiftUI

/// Scheduled tasks (the daemon's schedules.go): an agent or a command run in
/// a Group on a schedule. Each run is a tab there. Shared by the Mac and iOS.
public typealias Schedule = Hammock_V1_Schedule
public typealias ScheduleRun = Hammock_V1_ScheduleRun
/// What happening starts a run (the daemon's schedtriggers.go).
public typealias EventTrigger = Hammock_V1_EventTrigger

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

    /// Made-up schedules shown as if the daemon had sent them: for debug
    /// hooks only (HMK_DEBUG_SCHEDULES_SAMPLE), never sent anywhere.
    public func showSampleSchedules(_ list: [Schedule]) {
        for s in list { schedules[s.id] = s }
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
    /// returns its id). early: Run Early, in place of its next run (which
    /// moves on to the one after), not as well as it.
    @discardableResult
    public func runScheduleNow(_ id: String, early: Bool = false, reveal: Bool = true) async throws -> String {
        let tab = try await scheduleCall { $0.runSchedule = .with { $0.scheduleID = id; $0.early = early } }.created.id
        guard reveal, !tab.isEmpty else { return tab }
        // Its tab comes in the next state update (from another computer, a moment later).
        for _ in 0..<50 where tabs[tab] == nil { try? await Task.sleep(for: .milliseconds(40)) }
        if tabs[tab] != nil { selectedTabID = tab }
        return tab
    }

    /// What triggering an automation did (Shortcuts' Run Hammock Automation).
    public enum TriggerOutcome: Sendable, Equatable {
        case started
        /// Waiting to run, and why ("waiting for its cooldown, until 14:05";
        /// "" from an older Hammock, which doesn't say).
        case queued(String)
    }

    /// The most a trigger sends: the rest is cut (and the caller told).
    public static let maxTriggerText = 64 << 10

    /// Starts an automation as an event, from source ("shortcuts"), with
    /// what happened: text (cut to 64 KB on a character), a short label,
    /// and files on the automation's computer. Throws why it won't run
    /// ("Automations are off", "it's paused"). cut: the text was cut.
    public func triggerSchedule(_ id: String, source: String, text: String, label: String = "",
                                files: [String] = []) async throws -> (outcome: TriggerOutcome, cut: Bool) {
        var data = text
        var cut = false
        if data.utf8.count > Self.maxTriggerText {
            var n = 0
            var end = data.startIndex
            for i in data.indices {
                let c = data[i].utf8.count
                if n + c > Self.maxTriggerText { break }
                n += c
                end = data.index(after: i)
            }
            data = String(data[..<end])
            cut = true
        }
        let r = try await scheduleCall {
            $0.runSchedule = .with {
                $0.scheduleID = id
                $0.trigger = true
                $0.source = source
                $0.data = data
                $0.label = label
                $0.files = files
                $0.statusReply = true
            }
        }
        switch r.msg {
        case .triggerReply(let t):
            return (t.status == .queued ? .queued(t.reason) : .started, cut || t.cut)
        case .created:
            return (.started, cut)
        default:
            return (.queued(""), cut) // (an older Hammock's Ok)
        }
    }

    public func setSchedulePaused(_ id: String, _ paused: Bool) async throws {
        _ = try await scheduleCall { $0.setSchedulePaused = .with { $0.scheduleID = id; $0.paused = paused } }
    }

    /// A webhook trigger's URLs (and why nothing can reach them, if so);
    /// regenerate makes a new secret, so the old URL stops working.
    public func scheduleWebhook(_ id: String, trigger: String = "", regenerate: Bool = false) async throws -> Hammock_V1_ScheduleWebhookReply {
        try await scheduleCall {
            $0.scheduleWebhook = .with { $0.scheduleID = id; $0.triggerID = trigger; $0.regenerate = regenerate }
        }.scheduleWebhookReply
    }

    /// The Mac's calendars, for calendar triggers (CalendarFeed).
    public func sendCalendarEvents(_ events: Hammock_V1_CalendarEvents) async throws {
        _ = try await scheduleCall { $0.calendarEvents = events }
    }

    /// What the Mac app sees, for This Mac triggers (MacWatcher).
    public func sendMacAppState(_ state: Hammock_V1_MacAppState) async throws {
        _ = try await scheduleCall { $0.macAppState = state }
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
        case .unspecified, .running, .waiting, .queued: false
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
        case .queued: "Waiting to go in"
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
        case .queued: "clock.arrow.circlepath"
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

    /// Its next run is skipped (Skip Next Run).
    public var skipping: Bool { nextRunUnixMs > 0 && skipUntilUnixMs > nextRunUnixMs }
    /// It will run again on its own.
    public var upcoming: Bool { nextRunUnixMs > 0 && !paused }
    /// Run Early can start its next run now.
    public var canRunEarly: Bool { upcoming && !skipping }

    /// How soon a next run has to be for running now to mean Run Early.
    public static let earlyWindow: TimeInterval = 12 * 60 * 60

    /// Running it now should take its next run's place, not add a run: that
    /// run is coming up soon. What Run Now buttons do first, and say so.
    public func runsEarlyFirst(now: Date = Date()) -> Bool {
        canRunEarly && Double(nextRunUnixMs) / 1000 - now.timeIntervalSince1970 <= Self.earlyWindow
    }

    /// The next run, as Run Early's buttons name it: "09:00" today, else
    /// "Tomorrow at 09:00" or "Thu 6 Nov at 09:00".
    public func nextRunShort(now: Date = Date()) -> String {
        let when = Self.dateText(nextRunUnixMs, now: now)
        return when.hasPrefix("Today at ") ? String(when.dropFirst("Today at ".count)) : when
    }

    /// The next run, for a sentence: "at 09:00", "tomorrow at 09:00", "on
    /// Thu 6 Nov at 09:00".
    public func nextRunPhrase(now: Date = Date()) -> String {
        let when = Self.dateText(nextRunUnixMs, now: now)
        if when.hasPrefix("Today ") { return String(when.dropFirst("Today ".count)) }
        if when.hasPrefix("Tomorrow ") { return "tomorrow " + when.dropFirst("Tomorrow ".count) }
        return "on " + when
    }

    /// "Run Now Instead of 09:00".
    public func runEarlyTitle(now: Date = Date()) -> String { "Run Now Instead of \(nextRunShort(now: now))" }
    /// "Run Now and at 09:00 Too": Run Now, while Run Early is first.
    public func runAsWellTitle(now: Date = Date()) -> String {
        let when = nextRunShort(now: now)
        return when.contains(" at ") ? "Run Now and \(when) Too" : "Run Now and at \(when) Too"
    }

    /// Its runs carry on one session (its own, or one of yours), rather
    /// than each a new tab and session.
    public var continuesSession: Bool { session != .new && kind != "command" }

    /// Where its runs go when they carry on a session: "Continues ‹name›",
    /// "Continues its own session"; nil for a new tab each run.
    public var sessionText: String? {
        guard continuesSession else { return nil }
        let name = sessionName.isEmpty ? String(sessionID.prefix(8)) : sessionName
        switch session {
        case .own: return name.isEmpty ? "Continues its own session" : "Continues \(name)"
        default: return "Continues \(name)"
        }
    }

    /// Whether an agent's sessions can be continued (it can resume them).
    @MainActor public static func canContinue(_ kind: String) -> Bool {
        kind != "command" && AgentCatalog.kind(kind)?.canResume == true
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
    /// 09:15", its cron line, "Once, Thu 23 Dec at 17:00", "When CI fails on
    /// my pull request", or the time and "+ 2 triggers".
    public var whenText: String {
        let times = timesText
        switch (times, events.count) {
        case (nil, 0): return "Never"
        case (nil, 1): return "When " + events[0].text(names: nil).lowercasedFirst
        case (nil, let n): return "On \(n) triggers"
        case (let t?, 0): return t
        case (let t?, let n): return "\(t) + \(n) trigger\(n == 1 ? "" : "s")"
        }
    }

    /// When it runs by the clock (nil: only on its triggers).
    public var timesText: String? {
        switch timing {
        case .once: return "Once, " + Self.dateText(onceUnixMs)
        case .events: return nil
        default:
            if !cron.isEmpty { return "Cron \(cron)" }
            return Self.daysText(weekdays) + " at " + Self.timeText(Int(minuteOfDay))
        }
    }

    /// Seconds between its triggered runs (the daemon's default for 0).
    public var cooldown: Int { hasCooldownSeconds ? max(Int(cooldownSeconds), 0) : 60 }

    /// A cooldown in words: "1 minute", "5 minutes", "1 hour", "1 week", "None".
    public static func cooldownWords(_ s: Int) -> String {
        switch s {
        case ...0: "None"
        case let s where s % 2_592_000 == 0: s == 2_592_000 ? "1 month" : "\(s / 2_592_000) months"
        case let s where s % 604_800 == 0: s == 604_800 ? "1 week" : "\(s / 604_800) weeks"
        case let s where s % 86_400 == 0: s == 86_400 ? "1 day" : "\(s / 86_400) days"
        case let s where s % 3600 == 0: s == 3600 ? "1 hour" : "\(s / 3600) hours"
        case let s where s % 60 == 0: s == 60 ? "1 minute" : "\(s / 60) minutes"
        default: "\(s) seconds"
        }
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

    /// What a list of runs marks it with: what started it ("CI failed on
    /// #42"), "Instead of 09:00" (Run Early), "Run Now", or nothing.
    public func marker(now: Date = Date()) -> String? {
        if !trigger.isEmpty { return trigger }
        if early {
            let when = Schedule.dateText(dueUnixMs, now: now)
            return "Instead of " + (when.hasPrefix("Today at ") ? String(when.dropFirst("Today at ".count)) : when)
        }
        return manual ? "Run Now" : nil
    }

    /// When it started (or was due), for a list of runs.
    public var whenUnixMs: Int64 { startedUnixMs > 0 ? startedUnixMs : dueUnixMs > 0 ? dueUnixMs : endedUnixMs }

    /// The tab it ran in is still open.
    @MainActor public func hasTab(in model: AppModel) -> Bool { !tabClosed && !tabID.isEmpty && model.tabs[tabID] != nil }
}

extension EventTrigger {
    /// What fires it, in words, as the daemon and hmk say it: "CI fails on
    /// my pull request or branch", "An agent finishes in Assistant". names
    /// names a pane or Group by id (nil: by id).
    public func text(names: ((String) -> String?)?) -> String {
        func place(_ pane: String, _ group: String) -> String {
            let id = pane.isEmpty ? group : pane
            if id.isEmpty { return "" }
            guard let names else { return " in \(id)" }
            if let n = names(id), !n.isEmpty { return " in \(n)" }
            return pane.isEmpty ? " in a Group that’s closed" : " in a pane that’s closed"
        }
        switch kind {
        case .github(let g):
            let name = g.repo.isEmpty ? g.watch : g.repo
            let repo = name.isEmpty ? "" : " (\(name))"
            switch g.event {
            case .ciFailed: return "CI fails on my pull request or branch" + repo
            case .reviewRequested: return "My review is requested" + repo
            case .reviewed: return "My pull request gets a review or comment" + repo
            case .labeled: return "An issue or pull request is labelled “\(g.label)”" + repo
            case .assigned: return "An issue or pull request is assigned to me" + repo
            case .merged: return "My pull request is merged" + repo
            case .commit: return "A new commit on " + (g.branch.isEmpty ? "the default branch" : g.branch) + repo
            case .release: return (g.tags.isEmpty ? "A new release or tag" : "A new release or tag matching \(g.tags)") + repo
            case .issueOpened: return "A new issue is opened" + repo
            case .approved: return "My pull request is approved" + repo
            case .workflow: return "The “\(g.workflow)” workflow finishes" + repo
            case .notification: return "I get a GitHub notification" + repo
            default: return "GitHub" + repo
            }
        case .agent(let a):
            let what = a.finished && a.needsYou ? "finishes or needs me" : a.needsYou ? "needs me" : "finishes"
            return "An agent \(what)" + place(a.paneID, a.groupID)
        case .exit(let x):
            return "A command fails" + place(x.paneID, x.groupID)
        case .log(let l):
            return "A log line matches /\(l.pattern)/" + place(l.paneID, "")
        case .files(let f):
            var s = f.glob.isEmpty ? "Files change" : "\(f.glob) changes"
            if !f.folder.isEmpty { s += " in " + (f.folder as NSString).abbreviatingWithTildeInPath }
            return s
        case .calendar(let c):
            if c.when == .free {
                let m = c.freeMinutes > 0 ? Int(c.freeMinutes) : 30
                return "Free time of \(m == 1 ? "1 minute" : "\(m) minutes") or more begins in working hours" + (c.calendar.isEmpty ? "" : " (\(c.calendar))")
            }
            var s = c.when == .end ? "At the end of events"
                : c.minutesBefore == 0 ? "At the start of events"
                : c.minutesBefore == 1 ? "1 minute before events" : "\(c.minutesBefore) minutes before events"
            if !c.title.isEmpty { s += " titled “\(c.title)”" }
            if !c.calendar.isEmpty { s += " in \(c.calendar)" }
            if c.withAttendees { s += " with other people" }
            return s
        case .webhook:
            return "Something calls its webhook URL"
        case .shortcut:
            return "Shortcuts runs it"
        case .mac(let m):
            return m.text
        case .check(let c):
            let what = switch c.on {
            case .change: "’s output changes"
            case .recover: " passes again"
            default: " starts failing"
            }
            return "“\(c.command)”\(what), checked " + Self.everyWords(Int(c.everyMinutes))
        case .url(let u):
            return u.words
        case nil:
            return "An unknown event"
        }
    }

    /// How often, in minutes: "every minute", "every 5 minutes", "every
    /// hour", "every 2 days" (schedtrig.EveryWords). 0: the default, 5.
    public static func everyWords(_ minutes: Int) -> String {
        let m = minutes <= 0 ? 5 : minutes
        func n(_ v: Int, _ unit: String) -> String { v == 1 ? "1 \(unit)" : "\(v) \(unit)s" }
        if m <= 1 { return "every minute" }
        if m % 1440 == 0 { return m == 1440 ? "every day" : "every " + n(m / 1440, "day") }
        if m % 60 == 0 { return m == 60 ? "every hour" : "every " + n(m / 60, "hour") }
        return "every " + n(m, "minute")
    }

    /// The line under it: when it last looked and fired, or what's holding
    /// back what it fired ("Last checked 2 min ago · last fired yesterday
    /// 14:02"); nil when there's nothing to say yet (schedtrig.Status).
    public func statusText(now: Date = Date()) -> String? {
        var parts: [String] = []
        if checkedUnixMs > 0 { parts.append("last checked " + Self.ago(checkedUnixMs, now: now)) }
        if firedUnixMs > 0 {
            parts.append("last fired " + Self.when(firedUnixMs, now: now))
        } else if checkedUnixMs > 0 {
            parts.append("never fired")
        }
        if !held.isEmpty { parts.append(held) }
        guard let first = parts.first else { return nil }
        parts[0] = first.prefix(1).uppercased() + first.dropFirst()
        return parts.joined(separator: " · ")
    }

    /// "just now", "2 min ago", "3 h ago", "2 days ago".
    static func ago(_ ms: Int64, now: Date) -> String {
        let secs = Int(now.timeIntervalSince1970) - Int(ms / 1000)
        if secs < 60 { return "just now" }
        if secs < 3600 { return "\(secs / 60) min ago" }
        if secs < 86_400 { return "\(secs / 3600) h ago" }
        let d = secs / 86_400
        return d == 1 ? "1 day ago" : "\(d) days ago"
    }

    /// "14:02", "yesterday 14:02", "Mon 14:02", "3 Nov".
    static func when(_ ms: Int64, now: Date) -> String {
        let at = Date(timeIntervalSince1970: Double(ms) / 1000)
        let cal = Calendar.current
        let days = cal.dateComponents([.day], from: cal.startOfDay(for: at), to: cal.startOfDay(for: now)).day ?? 0
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_GB_POSIX")
        switch days {
        case ...0: f.dateFormat = "HH:mm"
        case 1: f.dateFormat = "'yesterday' HH:mm"
        case 2..<7: f.dateFormat = "EEE HH:mm"
        default: f.dateFormat = cal.component(.year, from: at) == cal.component(.year, from: now) ? "d MMM" : "d MMM yyyy"
        }
        return f.string(from: at)
    }

    /// An SF Symbol for its kind.
    public var symbol: String {
        switch kind {
        case .github: "arrow.triangle.pull"
        case .agent: "sparkles"
        case .exit: "xmark.octagon"
        case .log: "text.magnifyingglass"
        case .files: "doc.badge.ellipsis"
        case .calendar: "calendar"
        case .webhook: "link"
        case .shortcut: "square.2.layers.3d"
        case .check: "checkmark.seal"
        case .url: "globe"
        case .mac(let m): m.symbol
        case nil: "bolt"
        }
    }
}

/// A This Mac trigger (the daemon's macwatch.go).
public typealias MacTrigger = Hammock_V1_MacTrigger
/// A URL check (the daemon's schedurl.go).
public typealias URLCheck = Hammock_V1_UrlCheckTrigger

extension Hammock_V1_UrlCheckTrigger {
    /// What fires it, in words (schedtrig.DescribeURL).
    public var words: String {
        let what = switch on {
        case .change: " changes"
        case .contains: " starts containing “\(text)”"
        default: " starts failing"
        }
        return url + what + ", checked " + EventTrigger.everyWords(Int(everyMinutes))
    }
}
/// An Only If condition (the daemon's schedonlyif.go).
public typealias ScheduleCondition = Hammock_V1_Condition

extension Hammock_V1_MacTrigger {
    /// What fires it, in words (schedtrig's describeMac).
    public var text: String {
        func n(_ v: Int32, _ unit: String) -> String { v == 1 ? "1 \(unit)" : "\(v) \(unit)s" }
        let disk = volume.isEmpty || volume == "/" ? "The startup disk" : (volume as NSString).abbreviatingWithTildeInPath
        switch event {
        case .diskLow:
            return gigabytes > 0 ? "\(disk) has less than \(gigabytes) GB free" : "\(disk) has less than \(percent > 0 ? percent : 10)% free"
        case .batteryLow: return "The battery falls below \(percent > 0 ? percent : 20)%"
        case .pluggedIn: return "The Mac is plugged in"
        case .unplugged: return "The Mac is unplugged"
        case .wake: return minutes > 0 ? "The Mac wakes after sleeping \(n(minutes, "minute")) or more" : "The Mac wakes"
        case .away: return "I’ve been away from the Mac for \(n(minutes > 0 ? minutes : 15, "minute"))"
        case .back: return "I’m back at the Mac after \(n(minutes > 0 ? minutes : 10, "minute")) or more away"
        case .drive: return name.isEmpty ? "A drive is connected" : "A drive named “\(name)” is connected"
        case .wifiJoined: return name.isEmpty ? "The Mac joins a Wi-Fi network" : "The Mac joins the Wi-Fi “\(name)”"
        case .wifiLeft: return name.isEmpty ? "The Mac leaves a Wi-Fi network" : "The Mac leaves the Wi-Fi “\(name)”"
        case .appOpened: return "“\(name)” opens"
        case .appQuit: return "“\(name)” quits"
        case .display: return name.isEmpty ? "A display is connected" : "A display named “\(name)” is connected"
        case .placeArrive: return "The Mac arrives at “\(name)”"
        case .placeLeave: return "The Mac leaves “\(name)”"
        default: return "Something on this Mac"
        }
    }

    /// The Mac app watches for it (the daemon can't): it fires only while the app is open.
    public var appWatched: Bool {
        switch event {
        case .wifiJoined, .wifiLeft, .appOpened, .appQuit, .display, .placeArrive, .placeLeave: true
        default: false
        }
    }

    /// Arriving at or leaving one of the Mac's places.
    public var isPlace: Bool { event == .placeArrive || event == .placeLeave }

    public var symbol: String {
        switch event {
        case .diskLow: "internaldrive"
        case .batteryLow: "battery.25percent"
        case .pluggedIn, .unplugged: "powerplug"
        case .wake: "sun.horizon"
        case .away, .back: "figure.walk"
        case .drive: "externaldrive"
        case .wifiJoined, .wifiLeft: "wifi"
        case .appOpened, .appQuit: "app.badge"
        case .display: "display"
        case .placeArrive, .placeLeave: "mappin.and.ellipse"
        default: "desktopcomputer"
        }
    }
}

extension Hammock_V1_Condition {
    /// The condition in words, to follow "Only if" (schedtrig.DescribeCondition).
    public var text: String {
        switch kind {
        case .power: return "the Mac is plugged in"
        case .away: return minutes > 0 ? "I’ve been away for \(minutes == 1 ? "1 minute" : "\(minutes) minutes")" : "I’m away from the Mac"
        case .wifi: return "on the Wi-Fi “\(wifi)”"
        case .place: return "the Mac is at “\(place)”"
        case .hours: return "between \(Self.hhmm(fromMinute)) and \(Self.hhmm(toMinute))"
        case .days: return "on " + Self.daysWords(weekdays)
        case .free: return "I’m not in a meeting"
        case .disk: return "the startup disk has more than \(percent)% free"
        default: return "an unknown condition"
        }
    }

    /// "09:30" (24-hour, as the daemon writes it).
    public static func hhmm(_ m: Int32) -> String { String(format: "%02d:%02d", m / 60, m % 60) }

    /// "weekdays", "weekends", "every day", "Mon, Wed" (schedtrig.DaysWords).
    public static func daysWords(_ days: [Int32]) -> String {
        let set = Set(days)
        if set.isEmpty || set.count == 7 { return "every day" }
        if set == [1, 2, 3, 4, 5] { return "weekdays" }
        if set == [0, 6] { return "weekends" }
        let names = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
        return [1, 2, 3, 4, 5, 6, 0].filter { set.contains($0) }.map { names[Int($0)] }.joined(separator: ", ")
    }
}

extension Schedule {
    /// Its Only If conditions in a line: "the Mac is plugged in and on weekdays" (nil: none).
    public var onlyIfText: String? {
        onlyIf.isEmpty ? nil : onlyIf.map(\.text).joined(separator: " and ")
    }
}

private extension String {
    /// The string with its first letter in lower case ("CI …" kept: two capitals).
    var lowercasedFirst: String {
        guard let f = first, f.isUppercase else { return self }
        if count > 1, self[index(after: startIndex)].isUppercase { return self }
        return f.lowercased() + dropFirst()
    }
}

extension AppModel {
    /// Names a pane (by its tab) or Group, for words about triggers.
    public func triggerName(_ id: String) -> String? {
        if let g = groups.first(where: { $0.id == id }) { return title(of: g) }
        if let p = panes[id], let t = tabs[p.tabID] { return title(of: t) }
        return nil
    }
}
