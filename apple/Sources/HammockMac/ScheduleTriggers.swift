import AppKit
import CoreWLAN
import HammockKit
import SwiftUI

/// The editor's When: its times (a schedule, or once) and what else starts
/// a run (the daemon's schedtriggers.go), each a sentence edited in place,
/// added from Add Trigger; triggers that differ only by their target share a
/// row. Then how far apart runs its triggers start are.
struct ScheduleWhenBox: View {
    let model: AppModel
    @Bindable var draft: ScheduleDraft
    /// On another computer of yours: its panes and Groups (and no calendar).
    let computerID: String
    /// The row (by its first trigger's index) whose targets show.
    @State private var targetsShown: Int?

    private var rows: [[Int]] { EventTrigger.rows(draft.s.events) }

    var body: some View {
        RowBox {
            switch draft.s.timing {
            case .repeat: TimesRow(draft: draft)
            case .once: OnceRow(draft: draft)
            default: EmptyView()
            }
            ForEach(rows, id: \.[0]) { row in
                TriggerEditorRow(model: model, scheduleID: draft.original.id, computerID: computerID,
                                 events: $draft.s.events, row: row,
                                 targetsShown: Binding(get: { targetsShown == row[0] }, set: { targetsShown = $0 ? row[0] : nil }))
            }
            HStack(spacing: 8) {
                addMenu
                Spacer(minLength: 8)
                if !draft.s.events.isEmpty {
                    Text("at most once every").foregroundStyle(.secondary)
                    Picker("At most once every", selection: cooldown) {
                        ForEach(cooldownChoices, id: \.self) { s in Text(s == 0 ? "No limit" : Schedule.cooldownWords(s)).tag(s) }
                    }
                    .labelsHidden()
                    .fixedSize()
                    .help("Triggers that fire sooner wait, and start one run together")
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 7)
        }
    }

    // MARK: Add Trigger

    private var addMenu: some View {
        Menu {
            Section("Hammock") {
                Menu("Agents") {
                    Button("An Agent Finishes") { add(.agent(.with { $0.finished = true })) }
                    Button("An Agent Needs You") { add(.agent(.with { $0.needsYou = true })) }
                }
                Menu("Commands") {
                    Button("A Command Fails") { add(.exit(.init())) }
                }
                Menu("Log Lines") {
                    Button("A Log Line Matches…") { add(.log(.with { $0.paneID = logPanes.first?.id ?? "" })) }
                }
                Menu("Files") {
                    Button("Files Change…") { add(.files(.init())) }
                }
            }
            Section("Code") {
                Menu("GitHub") {
                    ForEach(Self.githubEvents, id: \.0) { e, title, _ in
                        Button(title) { add(.github(.with { $0.event = e })) }
                    }
                }
                Button("Command Check…") { add(.check(.with { $0.everyMinutes = 5 })) }
            }
            if onThisMac {
                Section("This Mac") {
                    Menu("Disk and Battery") {
                        Button("Disk Space Is Low…") { add(.mac(.with { $0.event = .diskLow; $0.percent = 10 })) }
                        Button("Battery Is Low…") { add(.mac(.with { $0.event = .batteryLow; $0.percent = 20 })) }
                        Divider()
                        Button("Mac Is Plugged In") { add(.mac(.with { $0.event = .pluggedIn })) }
                        Button("Mac Is Unplugged") { add(.mac(.with { $0.event = .unplugged })) }
                    }
                    Menu("Away and Back") {
                        Button("I’ve Been Away…") { add(.mac(.with { $0.event = .away; $0.minutes = 15 })) }
                        Button("I’m Back…") { add(.mac(.with { $0.event = .back; $0.minutes = 10 })) }
                        Button("Mac Wakes…") { add(.mac(.with { $0.event = .wake })) }
                    }
                    Menu("Apps Open or Quit") {
                        Button("An App Opens…") { add(.mac(.with { $0.event = .appOpened })) }
                        Button("An App Quits…") { add(.mac(.with { $0.event = .appQuit })) }
                    }
                    Menu("Drives and Displays") {
                        Button("A Drive Is Connected…") { add(.mac(.with { $0.event = .drive })) }
                        Button("A Display Is Connected…") { add(.mac(.with { $0.event = .display })) }
                    }
                    Menu("Wi-Fi") {
                        Button("Mac Joins a Network…") {
                            add(.mac(.with { $0.event = .wifiJoined; $0.name = Self.currentWiFi }))
                            MacWatcher.shared.requestLocation()
                        }
                        Button("Mac Leaves a Network…") {
                            add(.mac(.with { $0.event = .wifiLeft; $0.name = Self.currentWiFi }))
                            MacWatcher.shared.requestLocation()
                        }
                    }
                }
            }
            Section("Time and Place") {
                Button("Schedule…") { addTimes(.repeat) }
                    .disabled(draft.s.timing == .repeat)
                Button("Once…") { addTimes(.once) }
                    .disabled(draft.s.timing == .once)
                if onThisMac {
                    Menu("Calendar") {
                        Button("Before an Event…") { addCalendar(.with { $0.minutesBefore = 10 }) }
                        Button("When an Event Starts…") { addCalendar(.init()) }
                        Button("When an Event Ends…") { addCalendar(.with { $0.when = .end }) }
                        Button("When Free Time Begins…") { addCalendar(.with { $0.when = .free; $0.freeMinutes = 30 }) }
                    }
                }
            }
            Section("Other") {
                if onThisMac {
                    Button("Shortcuts") { add(.shortcut(.init())) }
                }
                Button("Webhook") { add(.webhook(.init())) }
            }
        } label: {
            Label("Add Trigger", systemImage: "plus")
        }
        .menuStyle(.button)
        .buttonStyle(.borderless)
        .themeLinkTint()
        .fixedSize()
    }

    /// GitHub's events: the menu's title, and the row's words.
    static let githubEvents: [(EventTrigger.GitHub.Event, String, String)] = [
        (.ciFailed, "CI Fails on My Pull Request", "CI fails on my pull request or branch"),
        (.reviewRequested, "My Review Is Requested", "My review is requested"),
        (.reviewed, "My Pull Request Gets a Review or Comment", "My pull request gets a review or comment"),
        (.approved, "My Pull Request Is Approved", "My pull request is approved"),
        (.merged, "My Pull Request Is Merged", "My pull request is merged"),
        (.labeled, "An Issue or Pull Request Is Labelled…", "An issue or pull request is labelled"),
        (.assigned, "Something Is Assigned to Me", "Something is assigned to me"),
        (.issueOpened, "A New Issue Is Opened", "A new issue is opened"),
        (.commit, "A New Commit Is Pushed", "A new commit is pushed"),
        (.release, "A New Release or Tag", "A new release or tag"),
        (.workflow, "A Workflow Finishes…", "The workflow"),
        (.notification, "I Get a GitHub Notification", "I get a GitHub notification"),
    ]

    private var cooldown: Binding<Int> {
        Binding(get: { draft.s.cooldown }, set: { draft.s.cooldownSeconds = Int32($0) })
    }

    private var cooldownChoices: [Int] {
        let base = [0, 60, 300, 600, 900, 1800, 3600, 86_400, 604_800, 2_592_000]
        let cur = draft.s.cooldown
        return base.contains(cur) ? base : (base + [cur]).sorted()
    }

    private var logPanes: [Pane] { TriggerEditorRow.logPanes(model, computerID) }

    /// It runs on this Mac (not a linked computer, which may not be one).
    private var onThisMac: Bool { computerID.isEmpty || computerID == model.myUserID }

    /// Adds a trigger, or (one like it watching something else is there)
    /// opens that row's list to add to.
    private func add(_ kind: EventTrigger.OneOf_Kind) {
        let t = EventTrigger.with { $0.kind = kind }
        if t.target != nil, let row = rows.first(where: { draft.s.events[$0[0]].shape == t.shape }) {
            targetsShown = row[0]
            return
        }
        draft.s.events.append(t)
        // Something to name: its list opens.
        if let target = t.target, target != .repository, t.targetValue.isEmpty { targetsShown = draft.s.events.count - 1 }
    }

    private func addCalendar(_ c: Hammock_V1_CalendarTrigger) {
        add(.calendar(c))
        CalendarFeed.shared.requestAccess()
    }

    /// Schedule… or Once…: the automation's times, in place of the other.
    private func addTimes(_ timing: Schedule.Timing) {
        draft.s.timing = timing
        if timing == .repeat, draft.s.minuteOfDay == 0, draft.s.weekdays.isEmpty, !draft.cronMode {
            draft.s.weekdays = [1, 2, 3, 4, 5]
            draft.s.minuteOfDay = 9 * 60
        }
    }

    /// The Wi-Fi network this Mac is on, when the app may know it ("" if not).
    static var currentWiFi: String { CWWiFiClient.shared().interface()?.ssid() ?? "" }
}

extension EventTrigger {
    typealias GitHub = Hammock_V1_GitHubTrigger
}

/// The days and time it runs: "[Weekdays] at [17:30]", or a cron line.
private struct TimesRow: View {
    @Bindable var draft: ScheduleDraft

    var body: some View {
        WhenRow(symbol: "clock", removeLabel: "Remove the schedule", remove: { draft.s.timing = .events }) {
            Picker("Days", selection: days) {
                Text("Every day").tag("every")
                Text("Weekdays").tag("weekdays")
                Text("Weekends").tag("weekends")
                Divider()
                Text("Custom Days").tag("custom")
                Text("Cron").tag("cron")
            }
            .labelsHidden()
            .fixedSize()
            if draft.cronMode {
                TextField("Cron", text: $draft.s.cron, prompt: Text("30 8 * * 1-5"))
                    .labelsHidden()
                    .font(.system(.body, design: .monospaced))
                    .frame(width: 150)
                    .help("Five fields (minute, hour, day of month, month, weekday), in this computer’s time")
            } else {
                if draft.customDays {
                    ForEach([1, 2, 3, 4, 5, 6, 0] as [Int32], id: \.self) { d in
                        Button(Schedule.weekdaySymbols[Int(d)]) { toggle(d) }
                            .buttonStyle(ChipStyle(on: draft.s.weekdays.contains(d)))
                            .accessibilityAddTraits(draft.s.weekdays.contains(d) ? .isSelected : [])
                    }
                }
                Text("at").foregroundStyle(.secondary)
                DatePicker("Time", selection: time, displayedComponents: .hourAndMinute)
                    .labelsHidden()
                    .datePickerStyle(.field)
                    .fixedSize()
            }
        }
    }

    private var days: Binding<String> {
        Binding(get: {
            if draft.cronMode { return "cron" }
            if draft.customDays { return "custom" }
            let set = Set(draft.s.weekdays)
            return set.isEmpty || set.count == 7 ? "every" : set == [1, 2, 3, 4, 5] ? "weekdays" : set == [0, 6] ? "weekends" : "custom"
        }, set: { v in
            draft.cronMode = v == "cron"
            draft.customDays = v == "custom"
            switch v {
            case "every": draft.s.weekdays = []
            case "weekdays": draft.s.weekdays = [1, 2, 3, 4, 5]
            case "weekends": draft.s.weekdays = [0, 6]
            case "custom": if draft.s.weekdays.isEmpty { draft.s.weekdays = [1, 2, 3, 4, 5] }
            default: break
            }
        })
    }

    private func toggle(_ d: Int32) {
        var set = Set(draft.s.weekdays)
        if set.isEmpty { set = Set(0...6) }
        if set.contains(d) { if set.count > 1 { set.remove(d) } } else { set.insert(d) }
        draft.s.weekdays = set.count == 7 ? [] : set.sorted()
    }

    private var time: Binding<Date> {
        Binding(get: {
            let m = Int(draft.s.minuteOfDay)
            return Calendar.current.date(bySettingHour: m / 60, minute: m % 60, second: 0, of: Date()) ?? Date()
        }, set: { d in
            let c = Calendar.current.dateComponents([.hour, .minute], from: d)
            draft.s.minuteOfDay = Int32((c.hour ?? 0) * 60 + (c.minute ?? 0))
        })
    }
}

/// A one-off: "Once, on [date and time]".
private struct OnceRow: View {
    @Bindable var draft: ScheduleDraft

    var body: some View {
        WhenRow(symbol: "calendar", removeLabel: "Remove the date", remove: { draft.s.timing = .events }) {
            Text("Once, on")
            DatePicker("Date", selection: $draft.once, displayedComponents: [.date, .hourAndMinute])
                .labelsHidden()
                .datePickerStyle(.field)
                .fixedSize()
        }
    }
}

/// A row of When: its symbol, its sentence (wrapping), and −.
struct WhenRow<Content: View>: View {
    let symbol: String
    let removeLabel: String
    let remove: () -> Void
    @ViewBuilder var content: Content
    var footer: AnyView?

    init(symbol: String, removeLabel: String, remove: @escaping () -> Void, footer: AnyView? = nil, @ViewBuilder content: () -> Content) {
        self.symbol = symbol
        self.removeLabel = removeLabel
        self.remove = remove
        self.footer = footer
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(alignment: .center, spacing: 8) {
                Image(systemName: symbol)
                    .foregroundStyle(.secondary)
                    .frame(width: 18)
                FlowLayout(spacing: 6) { content }
                    .frame(maxWidth: .infinity, alignment: .leading)
                RemoveRowButton(label: removeLabel, action: remove)
            }
            if let footer { footer.padding(.leading, 26) }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }
}

/// One row of triggers: a sentence with its fields in it (its first
/// trigger's; a change goes to all of the row's), the targets it watches as
/// a token, and how they've been doing.
struct TriggerEditorRow: View {
    let model: AppModel
    let scheduleID: String
    let computerID: String
    @Binding var events: [EventTrigger]
    /// The row's triggers' indices in events (several: alike but for their target).
    let row: [Int]
    @Binding var targetsShown: Bool

    @State private var webhook: Hammock_V1_ScheduleWebhookReply? = TriggerEditorRow.debugWebhook
    @State private var webhookError: String?
    /// A made-up URL shown at once (HMK_DEBUG_SCHEDULES_SAMPLE).
    nonisolated(unsafe) static var debugWebhook: Hammock_V1_ScheduleWebhookReply?

    private var first: EventTrigger { row.first.flatMap { $0 < events.count ? events[$0] : nil } ?? EventTrigger() }

    var body: some View {
        WhenRow(symbol: first.symbol, removeLabel: "Remove trigger", remove: removeRow,
                footer: AnyView(footer)) {
            fields
        }
    }

    private var footer: some View {
        VStack(alignment: .leading, spacing: 2) {
            let missing = first.missing
            if !missing.isEmpty {
                Text(missing).font(.caption).foregroundStyle(.orange)
            }
            if case .webhook = first.kind { webhookLine }
            TriggerStatusLine(trigger: EventTrigger.merged(row.compactMap { $0 < events.count ? events[$0] : nil }))
        }
    }

    /// The row's trigger, for its fields: what's set goes to each of the
    /// row's triggers, keeping its own id, target and what the daemon keeps.
    private var trigger: Binding<EventTrigger> {
        Binding(get: { first }, set: { new in
            for i in row where i < events.count {
                var t = new
                let old = events[i]
                t.id = old.id
                t.checkedUnixMs = old.checkedUnixMs
                t.firedUnixMs = old.firedUnixMs
                t.held = old.held
                t.error = old.error
                if t.target != nil, old.target != nil { t.targetValue = old.targetValue }
                events[i] = t
            }
        })
    }

    private func removeRow() {
        for i in row.sorted(by: >) where i < events.count { events.remove(at: i) }
    }

    // MARK: The sentence

    @ViewBuilder
    private var fields: some View {
        switch first.kind {
        case .github:
            pop(github(\.event), ScheduleWhenBox.githubEvents.map { ($0.0, $0.2) })
            switch github(\.event).wrappedValue {
            case .labeled:
                field("Label", github(\.label), "needs triage", width: 110)
            case .commit:
                word("on")
                field("Branch", github(\.branch), "the default branch", width: 140)
            case .release:
                word("matching")
                field("Tags", github(\.tags), "any tag, or v*", width: 110)
            case .workflow:
                field("Workflow", github(\.workflow), "CI, or ci.yml", width: 110)
                word("finishes")
            default: EmptyView()
            }
            word("in")
            targetToken
        case .agent:
            word("An agent")
            pop(agentWhat, [("f", "finishes"), ("n", "needs me"), ("fn", "finishes or needs me")])
            word("in")
            placePicker(pane: agent(\.paneID), group: agent(\.groupID))
        case .exit:
            word("A command fails in")
            placePicker(pane: exit(\.paneID), group: exit(\.groupID))
        case .log:
            word("A line in")
            Picker("Log", selection: log(\.paneID)) {
                if Self.logPanes(model, computerID).isEmpty { Text("No log panes open").tag("") }
                ForEach(Self.logPanes(model, computerID)) { p in Text(paneTitle(p)).tag(p.id) }
                if !log(\.paneID).wrappedValue.isEmpty, model.panes[log(\.paneID).wrappedValue] == nil {
                    Text("A pane that’s closed").tag(log(\.paneID).wrappedValue)
                }
            }
            .labelsHidden()
            .fixedSize()
            word("matches")
            field("Matches", log(\.pattern), "error|panic", width: 140, mono: true)
        case .files:
            field("Files", files(\.glob), "Any file, or *.swift", width: 150, mono: true)
            word("changes in")
            field("Folder", files(\.folder), "its folder", width: 160, mono: true)
            // Only this Mac's folders can be browsed; another computer's are typed.
            if computerID.isEmpty || computerID == model.myUserID {
                Button("Choose…") {
                    let start = files(\.folder).wrappedValue
                    if let f = GroupFolder.pick(startingAt: ((start.isEmpty ? "~" : start) as NSString).expandingTildeInPath,
                                                message: "Choose the folder whose files this trigger watches") {
                        files(\.folder).wrappedValue = GroupFolder.tilde(f)
                    }
                }
                .controlSize(.small)
            }
        case .calendar:
            Picker("When", selection: calendarWhen) {
                Text("Before an event").tag("before")
                Text("An event starts").tag("start")
                Text("An event ends").tag("end")
                Text("Free time begins").tag("free")
            }
            .labelsHidden()
            .fixedSize()
            switch calendarWhen.wrappedValue {
            case "free":
                word("for at least")
                pop(calendar(\.freeMinutes), choices([15, 30, 45, 60, 90, 120], calendar(\.freeMinutes).wrappedValue).map { ($0, Schedule.durationText(minutes: Int($0))) })
                    .help("Within your working hours (Settings ▸ Tools)")
                word("in")
                field("Calendar", calendar(\.calendar), "any calendar", width: 120)
            default:
                if calendarWhen.wrappedValue == "before" {
                    word("by")
                    pop(calendar(\.minutesBefore), choices([1, 2, 5, 10, 15, 30, 60], calendar(\.minutesBefore).wrappedValue).map { ($0, $0 == 1 ? "1 minute" : "\($0) minutes") })
                }
                word("titled")
                field("Title has", calendar(\.title), "anything", width: 110)
                word("in")
                targetToken
                Toggle("with other people", isOn: calendar(\.withAttendees))
                    .toggleStyle(.checkbox)
            }
        case .mac(let m):
            MacTriggerFields(trigger: mac, event: m.event, target: { targetToken })
        case .webhook:
            word("Something calls its webhook URL")
        case .check:
            field("Command", check(\.command), "make test", width: 190, mono: true)
            pop(check(\.on), [(.fail, "starts failing"), (.change, "changes its output"), (.recover, "passes again")])
            word("checked")
            pop(check(\.everyMinutes), choices([1, 5, 10, 15, 30, 60, 180, 360, 720, 1440], max(check(\.everyMinutes).wrappedValue, 1))
                .map { ($0, EventTrigger.everyWords(Int($0))) })
            word("with a time limit of")
            pop(check(\.timeoutSeconds), choices([0, 10, 30, 300, 900, 1800, 3600], check(\.timeoutSeconds).wrappedValue).map { s in
                (s, s == 0 || s == 60 ? "1 minute" : s % 60 == 0 ? "\(s / 60) minutes" : "\(s) seconds")
            })
        case .shortcut:
            word("Shortcuts runs it")
            Button("Open Shortcuts") {
                NSWorkspace.shared.open(URL(fileURLWithPath: "/System/Applications/Shortcuts.app"))
            }
            .controlSize(.small)
            .help("With the Run Hammock Automation action, on this Mac or your iPhone")
        case nil:
            word("An unknown trigger")
        }
    }

    private func word(_ s: String) -> some View {
        Text(s)
    }

    private func field(_ label: String, _ text: Binding<String>, _ prompt: String, width: CGFloat, mono: Bool = false) -> some View {
        TextField(label, text: text, prompt: Text(prompt))
            .labelsHidden()
            .font(mono ? .system(.body, design: .monospaced) : .body)
            .frame(width: width)
    }

    private func pop<T: Hashable>(_ selection: Binding<T>, _ items: [(T, String)]) -> some View {
        Picker("", selection: selection) {
            ForEach(items, id: \.0) { v, title in Text(title).tag(v) }
        }
        .labelsHidden()
        .fixedSize()
    }

    private func choices(_ base: [Int32], _ cur: Int32) -> [Int32] {
        base.contains(cur) ? base : (base + [cur]).sorted()
    }

    // MARK: Targets

    /// The row's targets as a token: "16 repositories", "acme/api", "any calendar".
    @ViewBuilder
    private var targetToken: some View {
        if let target = first.target {
            let members = row.filter { $0 < events.count }.map { events[$0] }
            Button(members.count > 1 ? target.count(members.count) : first.targetText) { targetsShown = true }
                .buttonStyle(TokenStyle())
                .popover(isPresented: $targetsShown, arrowEdge: .bottom) {
                    TargetsPopover(target: target, values: members.map(\.targetValue), resolved: members.map(\.targetText),
                                   add: addTarget, remove: removeTarget, choose: target == .app ? chooseApp : nil,
                                   status: EventTrigger.merged(members))
                }
        }
    }

    /// Another target: the row's one "any" becomes it, else a trigger like
    /// the others is added for it.
    private func addTarget(_ value: String) {
        let v = value.trimmingCharacters(in: .whitespaces)
        guard !v.isEmpty, !row.contains(where: { events[$0].targetValue == v }) else { return }
        if row.count == 1, events[row[0]].targetValue.isEmpty {
            events[row[0]].targetValue = v
            return
        }
        var t = first.shape
        t.targetValue = v
        events.append(t)
    }

    /// A target taken away (the row's nth); its last becomes "any". The
    /// row's first trigger stays first, so the row (and its list) stay put.
    private func removeTarget(_ n: Int) {
        guard n < row.count else { return }
        if row.count == 1 {
            events[row[0]].targetValue = ""
            return
        }
        let i = row[n]
        if n == 0, let last = row.last {
            events[i] = events[last]
            events.remove(at: last)
        } else {
            events.remove(at: i)
        }
    }

    private func chooseApp() -> String? {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.application]
        panel.directoryURL = URL(fileURLWithPath: "/Applications")
        panel.message = "Choose the app this trigger watches"
        guard panel.runModal() == .OK, let url = panel.url else { return nil }
        return FileManager.default.displayName(atPath: url.path).replacingOccurrences(of: ".app", with: "")
    }

    // MARK: Where

    /// "Any pane", a Group, or a tab's pane: where an agent or command trigger watches.
    private func placePicker(pane: Binding<String>, group: Binding<String>) -> some View {
        let groups = model.scheduleGroups(on: computerID)
        let sel = Binding<String>(get: {
            !pane.wrappedValue.isEmpty ? "p:" + pane.wrappedValue : !group.wrappedValue.isEmpty ? "g:" + group.wrappedValue : ""
        }, set: { v in
            pane.wrappedValue = v.hasPrefix("p:") ? String(v.dropFirst(2)) : ""
            group.wrappedValue = v.hasPrefix("g:") ? String(v.dropFirst(2)) : ""
        })
        return Picker("In", selection: sel) {
            Text("any pane").tag("")
            Divider()
            ForEach(groups) { g in
                Text(model.title(of: g)).tag("g:" + g.id)
            }
            Divider()
            ForEach(groups) { g in
                ForEach(model.tabs(in: g)) { t in
                    if !t.focusedPaneID.isEmpty {
                        Text("\(model.title(of: g)) ▸ \(model.title(of: t))").tag("p:" + t.focusedPaneID)
                    }
                }
            }
            let cur = sel.wrappedValue
            if !cur.isEmpty, !Self.placeTags(model, groups).contains(cur) {
                Text(cur.hasPrefix("p:") ? "a pane that’s closed" : "a Group that’s closed").tag(cur)
            }
        }
        .labelsHidden()
        .fixedSize()
    }

    private static func placeTags(_ model: AppModel, _ groups: [WorkspaceGroup]) -> Set<String> {
        var out = Set<String>()
        for g in groups {
            out.insert("g:" + g.id)
            for t in model.tabs(in: g) where !t.focusedPaneID.isEmpty { out.insert("p:" + t.focusedPaneID) }
        }
        return out
    }

    /// The log panes a log trigger can watch, on the automation's computer.
    static func logPanes(_ model: AppModel, _ computerID: String) -> [Pane] {
        let groups = Set(model.scheduleGroups(on: computerID).map(\.id))
        return model.panes.values.filter { p in
            p.hasLog && model.tabs[p.tabID].map { groups.contains($0.groupID) } == true
        }.sorted { $0.id < $1.id }
    }

    private func paneTitle(_ p: Pane) -> String {
        guard let t = model.tabs[p.tabID] else { return p.id }
        return model.title(of: t)
    }

    // MARK: Webhook

    @ViewBuilder
    private var webhookLine: some View {
        if scheduleID.isEmpty || first.id.isEmpty {
            Text("Its URL is made when you save").font(.caption).foregroundStyle(.secondary)
        } else {
            HStack(spacing: 8) {
                if let u = webhook?.urls.first {
                    Text(u).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                }
                Button("Copy URL") { Task { await copyWebhook(regenerate: false) } }
                    .buttonStyle(ThemeLinkStyle())
                Button("New URL…") { confirmNewURL() }
                    .buttonStyle(ThemeLinkStyle())
            }
            .font(.caption)
            if let p = webhook?.problem, !p.isEmpty {
                Text(p).font(.caption).foregroundStyle(.orange)
            }
            if let e = webhookError { Text(e).font(.caption).foregroundStyle(.red) }
        }
    }

    private func copyWebhook(regenerate: Bool) async {
        do {
            let r = try await model.scheduleWebhook(scheduleID, trigger: first.id, regenerate: regenerate)
            webhook = r
            webhookError = nil
            if let u = r.urls.first {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(u, forType: .string)
                model.showToast("Copied the webhook’s URL")
            }
        } catch {
            webhookError = error.localizedDescription
        }
    }

    private func confirmNewURL() {
        let alert = NSAlert()
        alert.messageText = "Make a new URL for this webhook?"
        alert.informativeText = "The old URL stops working. Anything that calls it needs the new one."
        alert.addButton(withTitle: "New URL")
        alert.addButton(withTitle: "Cancel")
        if alert.runModal() == .alertFirstButtonReturn {
            Task { await copyWebhook(regenerate: true) }
        }
    }

    // MARK: Bindings into the oneof

    /// An agent trigger's "finishes", "needs me" or both, as one choice.
    private var agentWhat: Binding<String> {
        Binding(get: {
            let a = agent(\.finished).wrappedValue, n = agent(\.needsYou).wrappedValue
            return a && n ? "fn" : n ? "n" : "f"
        }, set: { v in
            guard case .agent(var a) = first.kind else { return }
            a.finished = v.contains("f")
            a.needsYou = v.contains("n")
            trigger.wrappedValue.kind = .agent(a)
        })
    }

    /// A calendar trigger's when, with "before" for a start with minutes before it.
    private var calendarWhen: Binding<String> {
        Binding(get: {
            guard case .calendar(let c) = first.kind else { return "start" }
            switch c.when {
            case .end: return "end"
            case .free: return "free"
            default: return c.minutesBefore > 0 ? "before" : "start"
            }
        }, set: { v in
            guard case .calendar(var c) = first.kind else { return }
            switch v {
            case "end": c.when = .end; c.minutesBefore = 0
            case "free": c.when = .free; c.minutesBefore = 0; if c.freeMinutes <= 0 { c.freeMinutes = 30 }
            case "before": c.when = .start; if c.minutesBefore <= 0 { c.minutesBefore = 10 }
            default: c.when = .start; c.minutesBefore = 0
            }
            trigger.wrappedValue.kind = .calendar(c)
        })
    }

    /// The This Mac trigger, bound into the oneof.
    private var mac: Binding<Hammock_V1_MacTrigger> {
        Binding(get: { if case .mac(let m) = first.kind { return m }; return .init() },
                set: { trigger.wrappedValue.kind = .mac($0) })
    }

    private func github<T>(_ kp: WritableKeyPath<Hammock_V1_GitHubTrigger, T>) -> Binding<T> {
        Binding(get: { if case .github(let g) = first.kind { return g[keyPath: kp] }; return Hammock_V1_GitHubTrigger()[keyPath: kp] },
                set: { v in if case .github(var g) = first.kind { g[keyPath: kp] = v; trigger.wrappedValue.kind = .github(g) } })
    }

    private func agent<T>(_ kp: WritableKeyPath<Hammock_V1_AgentTrigger, T>) -> Binding<T> {
        Binding(get: { if case .agent(let a) = first.kind { return a[keyPath: kp] }; return Hammock_V1_AgentTrigger()[keyPath: kp] },
                set: { v in if case .agent(var a) = first.kind { a[keyPath: kp] = v; trigger.wrappedValue.kind = .agent(a) } })
    }

    private func exit<T>(_ kp: WritableKeyPath<Hammock_V1_ExitTrigger, T>) -> Binding<T> {
        Binding(get: { if case .exit(let x) = first.kind { return x[keyPath: kp] }; return Hammock_V1_ExitTrigger()[keyPath: kp] },
                set: { v in if case .exit(var x) = first.kind { x[keyPath: kp] = v; trigger.wrappedValue.kind = .exit(x) } })
    }

    private func log<T>(_ kp: WritableKeyPath<Hammock_V1_LogTrigger, T>) -> Binding<T> {
        Binding(get: { if case .log(let l) = first.kind { return l[keyPath: kp] }; return Hammock_V1_LogTrigger()[keyPath: kp] },
                set: { v in if case .log(var l) = first.kind { l[keyPath: kp] = v; trigger.wrappedValue.kind = .log(l) } })
    }

    private func files<T>(_ kp: WritableKeyPath<Hammock_V1_FilesTrigger, T>) -> Binding<T> {
        Binding(get: { if case .files(let f) = first.kind { return f[keyPath: kp] }; return Hammock_V1_FilesTrigger()[keyPath: kp] },
                set: { v in if case .files(var f) = first.kind { f[keyPath: kp] = v; trigger.wrappedValue.kind = .files(f) } })
    }

    private func check<T>(_ kp: WritableKeyPath<Hammock_V1_CommandCheckTrigger, T>) -> Binding<T> {
        Binding(get: { if case .check(let c) = first.kind { return c[keyPath: kp] }; return Hammock_V1_CommandCheckTrigger()[keyPath: kp] },
                set: { v in if case .check(var c) = first.kind { c[keyPath: kp] = v; trigger.wrappedValue.kind = .check(c) } })
    }

    private func calendar<T>(_ kp: WritableKeyPath<Hammock_V1_CalendarTrigger, T>) -> Binding<T> {
        Binding(get: { if case .calendar(let c) = first.kind { return c[keyPath: kp] }; return Hammock_V1_CalendarTrigger()[keyPath: kp] },
                set: { v in if case .calendar(var c) = first.kind { c[keyPath: kp] = v; trigger.wrappedValue.kind = .calendar(c) } })
    }
}

/// A row's targets, to add to and take from: tokens with ×, and a field
/// that adds what's typed.
struct TargetsPopover: View {
    let target: EventTrigger.Target
    /// As set ("": any, or the folder's repository).
    let values: [String]
    /// As they read (the folder's repository as found).
    let resolved: [String]
    let add: (String) -> Void
    let remove: (Int) -> Void
    /// Apps: pick one in the Finder.
    var choose: (() -> String?)?
    /// The row's triggers as one, for its status line.
    let status: EventTrigger
    @State private var text = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            FlowLayout(spacing: 5, lineSpacing: 5) {
                ForEach(Array(resolved.enumerated()), id: \.offset) { i, v in
                    HStack(spacing: 3) {
                        Text(v).lineLimit(1)
                        // The one "any" left isn't taken away: it's what none means.
                        if !(values.count == 1 && values[i].isEmpty) {
                            Button { remove(i) } label: { Image(systemName: "xmark").font(.caption2.weight(.bold)) }
                                .buttonStyle(.borderless)
                                .foregroundStyle(.secondary)
                                .accessibilityLabel("Remove \(v)")
                        }
                    }
                    .font(.callout)
                    .padding(.leading, 7)
                    .padding(.trailing, 5)
                    .padding(.vertical, 2)
                    .background(.quaternary.opacity(0.6), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                }
                TextField(target.prompt, text: $text, prompt: Text(target.prompt))
                    .textFieldStyle(.plain)
                    .frame(minWidth: 130)
                    .onSubmit {
                        add(text)
                        text = ""
                    }
            }
            .padding(8)
            .background(.background, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .overlay { RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(.quaternary) }
            if let choose {
                Button("Choose…") { if let v = choose() { add(v) } }
                    .controlSize(.small)
            }
            TriggerStatusLine(trigger: status)
        }
        .padding(14)
        .frame(width: 400)
    }
}

extension EventTrigger {
    /// What still needs filling in before it can be saved ("" when nothing).
    var missing: String {
        switch kind {
        case .github(let g):
            g.event == .labeled && g.label.trimmingCharacters(in: .whitespaces).isEmpty ? "Which label?"
                : g.event == .workflow && g.workflow.trimmingCharacters(in: .whitespaces).isEmpty ? "Which workflow?" : ""
        case .mac(let m): (m.event == .appOpened || m.event == .appQuit) && m.name.trimmingCharacters(in: .whitespaces).isEmpty ? "Which app?" : ""
        case .agent(let a): !a.finished && !a.needsYou ? "An agent trigger fires when it finishes, needs you, or both" : ""
        case .log(let l): l.paneID.isEmpty ? "Which log pane?" : l.pattern.trimmingCharacters(in: .whitespaces).isEmpty ? "What should the log line match?" : ""
        case .check(let c): c.command.trimmingCharacters(in: .whitespaces).isEmpty ? "Which command should the check run?" : ""
        default: ""
        }
    }
}

/// Made-up automations with triggers, for HMK_DEBUG_SCHEDULES_SAMPLE:
/// test data only.
enum ScheduleSamples {
    static let webhook: Hammock_V1_ScheduleWebhookReply = .with {
        $0.urls = ["https://100.64.0.2:7433/hook/sample-secret-not-real"]
        $0.path = "/hook/sample-secret-not-real"
    }

    static func all(now: Date = Date()) -> [Schedule] {
        let ms = { (ago: TimeInterval) in Int64((now.timeIntervalSince1970 - ago) * 1000) }
        let repos = ["acme/widgets", "acme/api", "acme/web-app", "acme/ios", "acme/android", "acme/docs", "acme/cli", "acme/infra", "acme/gizmo"]
        let review = Schedule.with {
            $0.id = "sc_sample_review"
            $0.name = "PR review follow-up"
            $0.kind = "claude"
            $0.prompt = "Read the new review or comment on this pull request. If it asks for changes, make them on its branch and push, then reply in the thread with what you changed.\n\nIf it’s only an approval, do nothing. Keep replies short, and don’t resolve threads yourself."
            $0.cwd = "/Users/me/Developer/app"
            $0.groupName = "Work"
            $0.timing = .events
            $0.cooldownSeconds = 600
            $0.createdBy = "You"
            $0.createdUnixMs = ms(2 * 86_400)
            $0.keepRuns = 7
            $0.allowedTools = ["Bash(git push:*)", "Bash(gh pr view:*)", "Bash(gh pr comment:*)", "Edit", "Write"]
            $0.events = repos.enumerated().map { i, r in
                .with {
                    $0.id = "tr_r\(i)"
                    $0.kind = .github(.with { $0.event = .reviewed; $0.watch = r; $0.repo = r })
                    $0.checkedUnixMs = ms(120)
                    if i == 1 { $0.firedUnixMs = ms(26 * 3600) }
                }
            }
            $0.onlyIf = [.with { $0.kind = .days; $0.weekdays = [1, 2, 3, 4, 5] }, .with { $0.kind = .hours; $0.fromMinute = 9 * 60; $0.toMinute = 18 * 60 }]
            $0.runs = [
                .with { $0.id = "sr_r1"; $0.status = .waiting; $0.trigger = "Comment on #479 · Cache the avatar list"; $0.kind = "claude"; $0.startedUnixMs = ms(720); $0.note = "Waiting for you" },
                .with { $0.id = "sr_r2"; $0.status = .ready; $0.trigger = "Review on #482 · Fix the login redirect"; $0.kind = "claude"; $0.startedUnixMs = ms(26 * 3600); $0.endedUnixMs = ms(26 * 3600 - 360); $0.note = "Pushed 2 commits."; $0.sessionID = "s1" },
                .with { $0.id = "sr_r3"; $0.status = .ready; $0.trigger = "Comment on #477 · Add retry to uploads"; $0.kind = "claude"; $0.startedUnixMs = ms(3 * 86_400); $0.endedUnixMs = ms(3 * 86_400 - 180); $0.note = "Replied, no changes."; $0.sessionID = "s2" },
                .with { $0.id = "sr_r4"; $0.status = .gaveUp; $0.trigger = "Review on #471 · Bump the SDK"; $0.kind = "claude"; $0.startedUnixMs = ms(4 * 86_400); $0.endedUnixMs = ms(4 * 86_400 - 4 * 3600); $0.note = "Gave up after waiting 4 hours."; $0.sessionID = "s3" },
                .with { $0.id = "sr_r5"; $0.status = .ready; $0.manual = true; $0.kind = "claude"; $0.startedUnixMs = ms(5 * 86_400); $0.endedUnixMs = ms(5 * 86_400 - 100); $0.note = "Nothing new." },
            ]
        }
        let ci = Schedule.with {
            $0.id = "sc_sample_ci"
            $0.name = "Fix failing CI"
            $0.kind = "claude"
            $0.prompt = "CI failed on a pull request of mine. Find out why from the failing checks, fix it on that branch and push."
            $0.cwd = "/Users/me/Developer/app"
            $0.groupName = "app"
            $0.timing = .events
            $0.cooldownSeconds = 300
            $0.createdBy = "You"
            $0.events = [
                .with { $0.id = "tr_s1"; $0.kind = .github(.with { $0.event = .ciFailed; $0.repo = "acme/widgets" }) },
                .with { $0.id = "tr_s2"; $0.kind = .github(.with { $0.event = .labeled; $0.label = "needs fix"; $0.repo = "acme/widgets" }) },
                .with { $0.id = "tr_s7"; $0.kind = .github(.with { $0.event = .commit; $0.watch = "acme/gizmo"; $0.repo = "acme/gizmo" }) },
                .with { $0.id = "tr_s8"; $0.kind = .github(.with { $0.event = .release; $0.tags = "v*"; $0.watch = "acme/gizmo"; $0.repo = "acme/gizmo" }) },
                .with { $0.id = "tr_s3"; $0.kind = .webhook(.init()) },
                .with { $0.id = "tr_s6"; $0.kind = .calendar(.with { $0.minutesBefore = 15; $0.title = "Release" }) },
            ]
            $0.runs = [
                .with { $0.id = "sr_s1"; $0.status = .running; $0.trigger = "CI failed on #42"; $0.kind = "claude"; $0.startedUnixMs = ms(120) },
                .with { $0.id = "sr_s2"; $0.status = .ready; $0.trigger = "Webhook"; $0.kind = "claude"; $0.startedUnixMs = ms(3 * 3600); $0.endedUnixMs = ms(3 * 3600 - 540); $0.note = "Fixed the flaky login test and pushed it to fix-login." },
                .with { $0.id = "sr_s3"; $0.status = .ready; $0.trigger = "CI failed on #40 (and 2 more)"; $0.kind = "claude"; $0.startedUnixMs = ms(26 * 3600); $0.endedUnixMs = ms(26 * 3600 - 700); $0.note = "Fixed the three failing checks on #40." },
                .with { $0.id = "sr_s4"; $0.status = .ready; $0.manual = true; $0.kind = "claude"; $0.startedUnixMs = ms(50 * 3600); $0.endedUnixMs = ms(50 * 3600 - 300); $0.note = "Nothing failing right now." },
            ]
        }
        let prep = Schedule.with {
            $0.id = "sc_sample_prep"
            $0.name = "1:1 prep"
            $0.kind = "claude"
            $0.prompt = "Write prep notes for this meeting: {{event}}"
            $0.cwd = "/Users/me/Notes"
            $0.groupName = "Notes"
            $0.timing = .events
            $0.events = [.with { $0.id = "tr_s4"; $0.kind = .calendar(.with { $0.minutesBefore = 10; $0.title = "1:1"; $0.withAttendees = true }) }]
            // Continues a session of yours, in a tab allowed more than it.
            $0.session = .picked
            $0.overlap = .wait
            $0.sessionID = "0b7c-sample-session"
            $0.sessionName = "Meeting notes"
            $0.sessionWarning = "Its tab runs Claude Code with --dangerously-skip-permissions, so runs sent there can do more than this automation allows"
            $0.runs = [
                .with { $0.id = "sr_s6"; $0.status = .queued; $0.continued = true; $0.trigger = "An event in 10 minutes"; $0.kind = "claude"; $0.waitingSinceUnixMs = ms(60); $0.note = "Something’s typed in its message box" },
                .with { $0.id = "sr_s5"; $0.status = .ready; $0.continued = true; $0.borrowed = true; $0.trigger = "An event in 10 minutes"; $0.kind = "claude"; $0.startedUnixMs = ms(5 * 3600); $0.endedUnixMs = ms(5 * 3600 - 200); $0.note = "Notes are in 1-1 Sam.md." },
            ]
        }
        let morning = Schedule.with {
            $0.id = "sc_sample_morning"
            $0.name = "Morning summary"
            $0.kind = "claude"
            $0.prompt = "Summarise what changed overnight in my repositories and what needs me today."
            $0.cwd = "/Users/me/Notes"
            $0.groupName = "Notes"
            $0.weekdays = [1, 2, 3, 4, 5]
            $0.minuteOfDay = 8 * 60 + 30
            $0.nextRunUnixMs = ms(-14 * 3600)
            $0.runs = [.with { $0.id = "sr_m1"; $0.status = .ready; $0.kind = "claude"; $0.dueUnixMs = ms(10 * 3600); $0.startedUnixMs = ms(10 * 3600); $0.endedUnixMs = ms(10 * 3600 - 240); $0.note = "Three pull requests need a review." }]
        }
        let tests = Schedule.with {
            $0.id = "sc_sample_tests"
            $0.name = "Nightly tests"
            $0.kind = "command"
            $0.prompt = "make test"
            $0.cwd = "/Users/me/Developer/app"
            $0.groupName = "app"
            $0.minuteOfDay = 60
            $0.nextRunUnixMs = ms(-8 * 3600)
            $0.events = [
                .with { $0.id = "tr_s5"; $0.kind = .files(.with { $0.glob = "src/**/*.go" }); $0.firedUnixMs = ms(26 * 3600) },
                .with {
                    $0.id = "tr_s9"
                    $0.kind = .check(.with { $0.command = "curl -fsS localhost:8080/health"; $0.everyMinutes = 5; $0.lastResult = "Passed" })
                    $0.checkedUnixMs = ms(120)
                    $0.firedUnixMs = ms(3 * 3600)
                    $0.held = "waiting for its cooldown, until 14:05"
                },
                .with { $0.id = "tr_s10"; $0.kind = .mac(.with { $0.event = .pluggedIn }); $0.firedUnixMs = ms(5 * 3600) },
                .with { $0.id = "tr_s11"; $0.kind = .mac(.with { $0.event = .wifiJoined; $0.name = "Office" }) },
                .with { $0.id = "tr_s12"; $0.kind = .mac(.with { $0.event = .wifiJoined; $0.name = "Studio" }) },
            ]
            $0.onlyIf = [.with { $0.kind = .power }, .with { $0.kind = .hours; $0.fromMinute = 9 * 60; $0.toMinute = 18 * 60 }]
            $0.runs = [.with { $0.id = "sr_t1"; $0.status = .failed; $0.kind = "command"; $0.dueUnixMs = ms(23 * 3600); $0.startedUnixMs = ms(23 * 3600); $0.endedUnixMs = ms(23 * 3600 - 95); $0.exitCode = 2 }]
        }
        let weekly = Schedule.with {
            $0.id = "sc_sample_weekly"
            $0.name = "Weekly report"
            $0.kind = "codex"
            $0.prompt = "Write this week’s report from the merged pull requests."
            $0.cwd = "/Users/me/Notes"
            $0.groupName = "Notes"
            $0.weekdays = [5]
            $0.minuteOfDay = 16 * 60
            $0.paused = true
        }
        return [review, ci, prep, morning, tests, weekly]
    }
}
