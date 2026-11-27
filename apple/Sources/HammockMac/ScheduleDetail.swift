import AppKit
import HammockKit
import SwiftUI

/// The selected automation's page: its name, On, where it runs and when it
/// last and next does; one card reading When, Only If and Run; its runs; and
/// its options, folded, saying only what isn't as it comes.
struct ScheduleDetail: View {
    let model: AppModel
    let schedule: Schedule
    @State private var fullPrompt = false
    @State private var allRuns = false
    @State private var options = false

    /// Runs shown before Show All.
    private static let fewRuns = 4

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                header
                if !schedule.error.isEmpty {
                    Label(schedule.error, systemImage: "exclamationmark.triangle.fill")
                        .font(.callout)
                        .foregroundStyle(.orange)
                }
                card
                runs
                footer
            }
            .padding(.horizontal, 28)
            .padding(.vertical, 22)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    // MARK: Header

    private var header: some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 8) {
                    Text(schedule.name).font(.title2.weight(.bold)).lineLimit(2).textSelection(.enabled)
                    if let c = model.computer(of: schedule) { ComputerBadge(c, size: 15) }
                }
                FlowLayout(spacing: 6) {
                    let group = ScheduleText.groupName(model, schedule)
                    if !group.isEmpty { Chip(group) }
                    Chip(schedule.kindName)
                    Chip(ScheduleText.eachRun(schedule))
                    if let c = model.computer(of: schedule) { Chip("On \(c.name)") }
                }
                if schedule.continuesSession, !schedule.sessionWarning.isEmpty {
                    SessionWarning(text: schedule.sessionWarning)
                }
                TimelineView(.periodic(from: .now, by: 60)) { tl in
                    Text(lastAndNext(now: tl.date))
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: 8)
            // Its own actions, on its title row: On, Run Now, Edit, ⋯.
            HStack(spacing: 10) {
                Toggle("On", isOn: Binding(get: { !schedule.paused }, set: { ScheduleActions.setPaused(model, schedule, !$0) }))
                    .toggleStyle(.switch)
                    .controlSize(.small)
                    .foregroundStyle(.secondary)
                    .fixedSize()
                    .help(schedule.paused ? "Paused: it won’t run on its own" : "Pause it")
                // Plain bordered controls of one height, their labels in the
                // label colour (the app's tint made them read as links).
                Group {
                ScheduleRunButton(model: model, schedule: schedule)
                    .fixedSize()
                Button("Edit") { ScheduleUI.shared.edit(schedule) }
                    .fixedSize()
                    .help("Change what it runs and when")
                Menu {
                    ScheduleMenuItems(model: model, schedule: schedule, inWindow: true)
                    Divider()
                    Picker("Sections", selection: $sections) {
                        Text("By When They Run").tag("when")
                        Text("By Group").tag("group")
                    }
                } label: {
                    // A character, so it's a line tall (as Edit is): a menu's
                    // button is as tall as its label, and the symbol is short.
                    Text("⋯")
                        .accessibilityLabel("More")
                }
                .menuStyle(.button)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("More")
                }
                .buttonStyle(.bordered)
                .neutralPopUps()
            }
            .controlSize(.regular)
            .padding(.top, 2)
        }
    }

    /// How the window's list is divided (its ⋯ menu here sets it).
    @AppStorage("automationsSections") private var sections = "when"

    /// "Last run yesterday at 14:02 · 6m · next when triggered".
    private func lastAndNext(now: Date) -> String {
        var parts: [String] = []
        if let r = schedule.runs.first, r.whenUnixMs > 0 {
            parts.append("Last run " + Schedule.dateText(r.whenUnixMs, now: now).lowercasedFirstWord)
            if let d = r.durationText(now: now) { parts.append(d) }
        } else {
            parts.append("Not run yet")
        }
        if schedule.paused {
            parts.append("paused")
        } else if schedule.error.isEmpty {
            let next = ScheduleText.next(schedule)
            parts.append(next.hasPrefix("Next: ") ? "next " + String(next.dropFirst("Next: ".count)).lowercasedFirstWord : next.lowercasedFirstWord)
        }
        return parts.joined(separator: " · ")
    }

    // MARK: When, Only If, Run

    private var card: some View {
        VStack(alignment: .leading, spacing: 0) {
            cardRow("When") { when }
            if !schedule.onlyIf.isEmpty {
                Divider().opacity(0.6)
                cardRow("Only If") {
                    FlowLayout(spacing: 6) {
                        ForEach(Array(schedule.onlyIf.enumerated()), id: \.offset) { _, c in
                            Chip(c.text.prefix(1).uppercased() + c.text.dropFirst())
                        }
                    }
                }
            }
            Divider().opacity(0.6)
            cardRow(schedule.kind == "command" ? "Command" : "Prompt") { run }
        }
        .padding(.horizontal, 18)
        .background(.background.opacity(0.5), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay { RoundedRectangle(cornerRadius: 12, style: .continuous).strokeBorder(.quaternary) }
    }

    private func cardRow(_ label: String, @ViewBuilder _ content: () -> some View) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 14) {
            SectionLabel(label)
                .frame(width: 66, alignment: .leading)
            content()
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.vertical, 14)
    }

    @ViewBuilder
    private var when: some View {
        VStack(alignment: .leading, spacing: 12) {
            if let times = schedule.timesText {
                Label {
                    Text(schedule.skipping ? "\(times) · skipping \(Schedule.dateText(schedule.nextRunUnixMs).lowercasedFirstWord)" : times)
                        .fontWeight(.medium)
                } icon: {
                    Image(systemName: schedule.timing == .once ? "calendar" : "clock").foregroundStyle(.secondary)
                }
            }
            ForEach(Array(schedule.triggerRows.enumerated()), id: \.offset) { _, row in
                TriggerReadRow(model: model, triggers: row, computerID: schedule.computerID)
            }
            if schedule.timesText == nil, schedule.events.isEmpty {
                Text("Only when run: from here, Shortcuts or hmk")
                    .foregroundStyle(.secondary)
            }
            if !schedule.events.isEmpty {
                Text(schedule.cooldown == 0 ? "No limit on how often its triggers run it"
                     : "At most once every \(Schedule.cooldownWords(schedule.cooldown).replacing(#/^1 /#, with: ""))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var run: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(schedule.prompt)
                .font(schedule.kind == "command" ? .system(.body, design: .monospaced) : .body)
                .lineSpacing(2)
                .lineLimit(fullPrompt ? nil : 4)
                .truncationMode(.tail)
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
            HStack(spacing: 14) {
                Label(ScheduleText.folder(schedule.cwd), systemImage: "folder")
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .textSelection(.enabled)
                if isLong {
                    Button(fullPrompt ? "Show Less" : schedule.kind == "command" ? "Show Full Command" : "Show Full Prompt") { fullPrompt.toggle() }
                        .buttonStyle(ThemeLinkStyle())
                        .font(.callout)
                }
            }
        }
    }

    /// More than four lines' worth, about.
    private var isLong: Bool {
        schedule.prompt.count > 360 || schedule.prompt.split(separator: "\n", omittingEmptySubsequences: false).count > 4
    }

    // MARK: Runs

    private var runs: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline) {
                Text("Runs").font(.headline)
                Spacer()
                if !schedule.continuesSession {
                    Text("Keeps the last \(schedule.keptRuns)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .help("Older runs’ tabs close as new ones open, except ones you’ve typed in")
                }
            }
            .padding(.bottom, 6)
            Divider()
            if schedule.runs.isEmpty {
                Text("None yet.")
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 10)
            } else {
                let shown = allRuns ? schedule.runs : Array(schedule.runs.prefix(Self.fewRuns))
                ForEach(Array(shown.enumerated()), id: \.element.id) { i, r in
                    if i > 0 { Divider().opacity(0.6) }
                    RunRow(model: model, schedule: schedule, run: r)
                }
                if schedule.runs.count > Self.fewRuns {
                    Button(allRuns ? "Show Fewer" : "Show All \(schedule.runs.count)") { allRuns.toggle() }
                        .buttonStyle(ThemeLinkStyle())
                        .font(.callout)
                        .padding(.top, 6)
                }
            }
        }
    }

    // MARK: Options

    private var footer: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
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
                if !options, !changedOptions.isEmpty {
                    Text(changedOptions.joined(separator: " · "))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 8)
                Text(madeBy)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .font(.callout)
            if options {
                Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 14, verticalSpacing: 7) {
                    optionRow("If still running", ScheduleText.overlap(schedule.overlap))
                    if schedule.kind != "command" {
                        optionRow("Give up after", Schedule.durationText(minutes: schedule.givesUpAfter) + " waiting for you")
                    }
                    if !schedule.continuesSession {
                        optionRow("Keep", "The last \(schedule.keptRuns) run\(schedule.keptRuns == 1 ? "" : "s")")
                    }
                    optionRow("Notify me", ScheduleText.notify(schedule.effectiveNotify))
                    if schedule.kind == "codex" {
                        optionRow("Codex sandbox", schedule.codexSandbox.isEmpty ? "Your Codex settings" : schedule.codexSandbox)
                        optionRow("Codex approval", schedule.codexApproval.isEmpty ? "Your Codex settings" : schedule.codexApproval)
                    }
                    if schedule.kind == "claude" {
                        GridRow {
                            Text("Remembered tools").foregroundStyle(.secondary).gridColumnAlignment(.trailing)
                            if schedule.allowedTools.isEmpty {
                                Text("None").help("When a run asks, “Always Allow for This Automation” in Needs You remembers the tool here")
                            } else {
                                FlowLayout(spacing: 5, lineSpacing: 5) {
                                    ForEach(schedule.allowedTools, id: \.self) { Chip($0, mono: true) }
                                }
                            }
                        }
                    }
                }
                .font(.callout)
                .padding(.leading, 18)
                .textSelection(.enabled)
            }
        }
    }

    private func optionRow(_ label: String, _ value: String) -> some View {
        GridRow {
            Text(label).foregroundStyle(.secondary).gridColumnAlignment(.trailing)
            Text(value)
        }
    }

    /// Its options that aren't as a new one's.
    private var changedOptions: [String] {
        var out: [String] = []
        let n = schedule.allowedTools.count
        if schedule.kind == "claude", n > 0 { out.append("\(n) remembered tool\(n == 1 ? "" : "s")") }
        switch schedule.overlap {
        case .stopPrevious: out.append("Stops the last run if still running")
        case .runBoth: out.append("Runs alongside the last")
        case .skip where schedule.continuesSession: out.append("Skips a run while the last is going")
        default: break
        }
        if schedule.kind != "command", schedule.givesUpAfter != 240 { out.append("Gives up after \(Schedule.durationText(minutes: schedule.givesUpAfter))") }
        if schedule.keptRuns != 7, !schedule.continuesSession { out.append("Keeps \(schedule.keptRuns)") }
        if schedule.notify != .default { out.append("Notifies " + ScheduleText.notify(schedule.notify).lowercased()) }
        if schedule.kind == "codex", !schedule.codexSandbox.isEmpty || !schedule.codexApproval.isEmpty { out.append("Codex permissions set") }
        return out
    }

    private var madeBy: String {
        let who = schedule.createdBy.isEmpty || schedule.createdBy == "You" ? "you" : schedule.createdBy
        guard schedule.createdUnixMs > 0 else { return "Made by \(who)" }
        let d = Date(timeIntervalSince1970: Double(schedule.createdUnixMs) / 1000)
        return "Made by \(who) on \(d.formatted(.dateTime.day().month(.abbreviated)))"
    }
}

/// One row of When as read: what fires it, the repositories (or calendars,
/// apps…) it watches, and how it's been doing.
struct TriggerReadRow: View {
    let model: AppModel
    let triggers: [EventTrigger]
    /// The automation's computer ("": this one).
    var computerID = ""
    @State private var allTargets = false

    private static let fewTargets = 6

    private var first: EventTrigger { triggers[0] }
    private var merged: EventTrigger { EventTrigger.merged(triggers) }
    /// The targets worth listing: several, or one that isn't "any".
    private var targets: [String] {
        guard let target = first.target else { return [] }
        // One "any" isn't worth a chip, unless it's the folder's repository, as found.
        if triggers.count == 1, first.targetValue.isEmpty, target != .repository { return [] }
        return triggers.map(\.targetText)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label {
                Text(first.textWithoutTarget(names: model.triggerName)).fontWeight(.medium)
            } icon: {
                Image(systemName: first.symbol).foregroundStyle(.secondary)
            }
            .textSelection(.enabled)
            if !targets.isEmpty {
                FlowLayout(spacing: 5, lineSpacing: 5) {
                    let shown = allTargets ? targets : Array(targets.prefix(Self.fewTargets))
                    ForEach(Array(shown.enumerated()), id: \.offset) { _, t in Chip(t) }
                    if targets.count > Self.fewTargets {
                        Button(allTargets ? "Show fewer" : "+\(targets.count - Self.fewTargets) more") { allTargets.toggle() }
                            .buttonStyle(ThemeLinkStyle())
                            .font(.callout)
                    }
                }
                .padding(.leading, 28)
            }
            TriggerStatusLine(trigger: merged)
                .padding(.leading, 28)
            if case .slack = first.kind {
                SlackLinkLine(model: model, computerID: computerID)
                    .padding(.leading, 28)
            }
        }
    }
}

/// Under a trigger: what's wrong with it, else when it last looked and
/// fired (and a check's last result), or what's holding it back.
struct TriggerStatusLine: View {
    let trigger: EventTrigger

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            if !trigger.error.isEmpty {
                Label(trigger.error, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
            TimelineView(.periodic(from: .now, by: 30)) { tl in
                if let line = line(now: tl.date) {
                    Text(line)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func line(now: Date) -> String? {
        var parts: [String] = []
        if case .check(let c) = trigger.kind, !c.lastResult.isEmpty { parts.append(c.lastResult) }
        if case .url(let u) = trigger.kind, !u.lastResult.isEmpty { parts.append(u.lastResult) }
        if case .mac(let m) = trigger.kind, m.appWatched { parts.append("Works while Hammock is open") }
        if let s = trigger.statusText(now: now) { parts.append(parts.isEmpty ? s : s.prefix(1).lowercased() + s.dropFirst()) }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}

/// One run: what started it, when, for how long, its last words, and a way back to it.
struct RunRow: View {
    let model: AppModel
    let schedule: Schedule
    let run: ScheduleRun

    var body: some View {
        HStack(spacing: 12) {
            RunStatusIcon(status: run.status)
                .frame(width: 18)
                .help(run.status.label)
                .accessibilityLabel(run.status.label)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .fontWeight(.medium)
                    .lineLimit(1)
                Text(subtitle)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.tail)
                    .help(run.note)
            }
            Spacer(minLength: 8)
            link
        }
        .padding(.vertical, 9)
    }

    /// What started it, else how.
    private var title: String {
        if let m = run.marker() { return m }
        if run.catchUp { return "Caught up after waking" }
        return run.dueUnixMs > 0 ? "On its schedule" : run.status.label
    }

    /// "Today at 09:41 · 6m · Fixed the flaky test".
    private var subtitle: String {
        var parts: [String] = []
        if run.whenUnixMs > 0 { parts.append(Schedule.dateText(run.whenUnixMs)) }
        if let d = run.durationText() { parts.append(d) }
        if run.catchUp, run.marker() != nil { parts.append("caught up") }
        parts.append(note)
        return parts.joined(separator: " · ")
    }

    /// Its note, else its status (with a command's exit code).
    private var note: String {
        if run.status == .queued { return run.note.isEmpty ? run.status.label : "\(run.status.label): \(run.note)" }
        if !run.note.isEmpty { return run.note }
        if schedule.kind == "command", run.status == .failed { return "Exit code \(run.exitCode)" }
        return run.status.label
    }

    @ViewBuilder
    private var link: some View {
        if run.hasTab(in: model) {
            Button(run.status == .waiting ? "Answer" : "Open Tab") { ScheduleActions.open(model, run, of: schedule) }
                .buttonStyle(ThemeLinkStyle())
                .fontWeight(run.status == .waiting ? .semibold : .regular)
        } else if schedule.kind != "command", !run.sessionID.isEmpty {
            Button("Resume Session") { ScheduleActions.open(model, run, of: schedule) }
                .buttonStyle(ThemeLinkStyle())
                .help("Its tab has closed: carry on with its session in a new tab")
        }
    }
}

extension ScheduleText {
    /// What each run does with tabs.
    /// A folder as shown: from ~ (this Mac's home, or any computer's
    /// /Users/name or /home/name), and the home folder itself in words.
    @MainActor static func folder(_ path: String) -> String {
        var p = GroupFolder.tilde(path)
        if let r = p.firstMatch(of: #/^/(Users|home)/[^/]+/#) { p = "~" + p[r.range.upperBound...] }
        return p == "~" || p == "~/" ? "Home folder" : p
    }

    static func eachRun(_ s: Schedule) -> String {
        if let text = s.sessionText { return text }
        return s.kind == "command" && s.closeOnSuccess ? "New tab each run, closed if it succeeds" : "New tab each run"
    }
}
