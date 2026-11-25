import AppKit
import HammockKit
import SwiftUI

/// The schedule editor's Triggers: what besides its times starts a run (the
/// daemon's schedtriggers.go), each edited in place, added from a menu.
struct ScheduleTriggersSection: View {
    let model: AppModel
    /// The schedule as saved (its id, for a webhook's URL; none while new).
    let scheduleID: String
    @Binding var events: [EventTrigger]
    /// Seconds between runs its triggers start (the daemon's default shown while unset).
    @Binding var cooldown: Int
    /// On another computer of yours: its panes and Groups (and no calendar).
    let computerID: String
    /// It runs a command, which gets what happened in variables, not in its prompt.
    var isCommand = false

    var body: some View {
        Section {
            ForEach(events.indices, id: \.self) { i in
                TriggerEditorRow(model: model, scheduleID: scheduleID, computerID: computerID, trigger: $events[i]) {
                    events.remove(at: i)
                }
            }
            HStack {
                Menu("Add Trigger") {
                    Menu("GitHub") {
                        ForEach(Self.githubEvents, id: \.0) { e, title in
                            Button(title) { add(.github(.with { $0.event = e })) }
                        }
                    }
                    Button("An Agent Finishes or Needs You") { add(.agent(.with { $0.finished = true })) }
                    Button("A Command Fails") { add(.exit(.init())) }
                    Button("A Log Line Matches") { add(.log(.with { $0.paneID = logPanes.first?.id ?? "" })) }
                    Button("Files Change") { add(.files(.init())) }
                    if computerID.isEmpty || computerID == model.myUserID {
                        Button("Before a Calendar Event") {
                            add(.calendar(.with { $0.minutesBefore = 10 }))
                            CalendarFeed.shared.requestAccess()
                        }
                    }
                    Button("Webhook") { add(.webhook(.init())) }
                }
                .fixedSize()
                Spacer()
                if !events.isEmpty {
                    Picker("At most every", selection: $cooldown) {
                        ForEach(cooldownChoices, id: \.self) { s in Text(s == 0 ? "No limit" : Schedule.cooldownWords(s)).tag(s) }
                    }
                    .fixedSize()
                }
            }
        } header: {
            Text("Triggers")
        } footer: {
            if !events.isEmpty {
                Text(isCommand
                     ? "The command gets the details of what happened in variables, such as $HMK_EVENT_URL and $HMK_EVENT_TITLE for a pull request."
                     : "The details of what happened, such as a pull request’s link, number and title, are added after your prompt. To put them somewhere else, write {{event}} there.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    static let githubEvents: [(EventTrigger.GitHub.Event, String)] = [
        (.ciFailed, "CI Fails on My Pull Request"), (.reviewRequested, "My Review Is Requested"),
        (.reviewed, "My Pull Request Gets a Review or Comment"), (.labeled, "An Issue or Pull Request Is Labelled…"),
        (.assigned, "Something Is Assigned to Me"), (.merged, "My Pull Request Is Merged"),
        (.commit, "A New Commit Is Pushed"), (.release, "A New Release or Tag"),
    ]

    private var cooldownChoices: [Int] {
        let base = [0, 60, 300, 900, 1800, 3600, 86_400, 604_800, 2_592_000]
        return base.contains(cooldown) ? base : (base + [cooldown]).sorted()
    }

    private var logPanes: [Pane] { TriggerEditorRow.logPanes(model, computerID) }

    private func add(_ kind: EventTrigger.OneOf_Kind) {
        events.append(.with { $0.kind = kind })
    }
}

extension EventTrigger {
    typealias GitHub = Hammock_V1_GitHubTrigger
}

/// One trigger, its kind's fields in a row or two.
struct TriggerEditorRow: View {
    let model: AppModel
    let scheduleID: String
    let computerID: String
    @Binding var trigger: EventTrigger
    let remove: () -> Void

    @State private var webhook: Hammock_V1_ScheduleWebhookReply? = TriggerEditorRow.debugWebhook
    @State private var webhookError: String?
    /// A made-up URL shown at once (HMK_DEBUG_SCHEDULES_SAMPLE).
    nonisolated(unsafe) static var debugWebhook: Hammock_V1_ScheduleWebhookReply?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline) {
                Image(systemName: trigger.symbol).foregroundStyle(.secondary).frame(width: 18)
                fields
                Spacer(minLength: 4)
                Button(action: remove) { Image(systemName: "minus.circle.fill").foregroundStyle(.secondary) }
                    .buttonStyle(.borderless)
                    .accessibilityLabel("Remove trigger")
            }
            if !trigger.error.isEmpty {
                Label(trigger.error, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .padding(.leading, 26)
            }
        }
    }

    @ViewBuilder
    private var fields: some View {
        switch trigger.kind {
        case .github:
            VStack(alignment: .leading, spacing: 4) {
                Picker("GitHub", selection: github(\.event)) {
                    ForEach(ScheduleTriggersSection.githubEvents, id: \.0) { e, title in Text(title.replacingOccurrences(of: "…", with: "")).tag(e) }
                }
                switch github(\.event).wrappedValue {
                case .labeled: TextField("Label", text: github(\.label), prompt: Text("needs triage"))
                case .commit: TextField("Branch", text: github(\.branch), prompt: Text("The default branch"))
                case .release: TextField("Tags", text: github(\.tags), prompt: Text("Any, or a pattern like v*"))
                default: EmptyView()
                }
                TextField("Repository", text: github(\.watch), prompt: Text(githubFolderRepo.isEmpty ? "The folder’s, or owner/name" : githubFolderRepo))
                Text("Checked every couple of minutes with gh.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        case .agent:
            VStack(alignment: .leading, spacing: 4) {
                HStack {
                    Text("An agent")
                    Toggle("Finishes", isOn: agent(\.finished)).toggleStyle(.checkbox)
                    Toggle("Needs you", isOn: agent(\.needsYou)).toggleStyle(.checkbox)
                }
                placePicker(pane: agent(\.paneID), group: agent(\.groupID))
            }
        case .exit:
            VStack(alignment: .leading, spacing: 4) {
                Text("A pane’s command exits with an error")
                placePicker(pane: exit(\.paneID), group: exit(\.groupID))
            }
        case .log:
            VStack(alignment: .leading, spacing: 4) {
                Picker("Log", selection: log(\.paneID)) {
                    if Self.logPanes(model, computerID).isEmpty { Text("No log panes open").tag("") }
                    ForEach(Self.logPanes(model, computerID)) { p in Text(paneTitle(p)).tag(p.id) }
                    if !log(\.paneID).wrappedValue.isEmpty, model.panes[log(\.paneID).wrappedValue] == nil {
                        Text("A pane that’s closed").tag(log(\.paneID).wrappedValue)
                    }
                }
                TextField("Matches", text: log(\.pattern), prompt: Text("error|panic"))
                    .font(.system(.body, design: .monospaced))
            }
        case .files:
            VStack(alignment: .leading, spacing: 4) {
                TextField("Files", text: files(\.glob), prompt: Text("Any file, or *.swift, src/**/*.ts"))
                    .font(.system(.body, design: .monospaced))
                HStack {
                    TextField("In", text: files(\.folder), prompt: Text("The automation’s folder"))
                        .font(.system(.body, design: .monospaced))
                    // Only this Mac's folders can be browsed; another computer's are typed.
                    if computerID.isEmpty || computerID == model.myUserID {
                        Button("Choose…") {
                            let start = files(\.folder).wrappedValue
                            if let f = GroupFolder.pick(startingAt: ((start.isEmpty ? "~" : start) as NSString).expandingTildeInPath,
                                                        message: "Choose the folder whose files this trigger watches") {
                                files(\.folder).wrappedValue = GroupFolder.tilde(f)
                            }
                        }
                    }
                }
            }
        case .calendar:
            VStack(alignment: .leading, spacing: 4) {
                Picker("Before", selection: calendar(\.minutesBefore)) {
                    ForEach(minuteChoices, id: \.self) { m in Text(m == 0 ? "At the start" : "\(m) minutes before").tag(m) }
                }
                TextField("Title has", text: calendar(\.title), prompt: Text("Any event"))
                TextField("Calendar", text: calendar(\.calendar), prompt: Text("Any calendar"))
                Toggle("Only events with other people", isOn: calendar(\.withAttendees))
            }
        case .webhook:
            VStack(alignment: .leading, spacing: 4) {
                Text("Webhook")
                if scheduleID.isEmpty || trigger.id.isEmpty {
                    Text("Its URL is made when you save.").font(.caption).foregroundStyle(.secondary)
                } else {
                    if let u = webhook?.urls.first {
                        Text(u).font(.caption.monospaced()).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle).textSelection(.enabled)
                    }
                    HStack {
                        Button("Copy URL") { Task { await copyWebhook(regenerate: false) } }
                        Button("New URL…") { confirmNewURL() }
                    }
                    .controlSize(.small)
                    if let p = webhook?.problem, !p.isEmpty {
                        Text(p).font(.caption).foregroundStyle(.orange)
                    }
                    if let e = webhookError { Text(e).font(.caption).foregroundStyle(.red) }
                }
            }
        case nil:
            Text("An unknown trigger")
        }
    }

    private var minuteChoices: [Int32] {
        let base: [Int32] = [0, 1, 2, 5, 10, 15, 30, 60]
        let cur = calendar(\.minutesBefore).wrappedValue
        return base.contains(cur) ? base : (base + [cur]).sorted()
    }

    /// The folder's repository, as the daemon last found it (while it
    /// watches its own).
    private var githubFolderRepo: String {
        if case .github(let g) = trigger.kind, g.watch.isEmpty { return g.repo }
        return ""
    }

    // MARK: Where

    /// "Anywhere", a Group, or a tab's pane: where an agent or command trigger watches.
    private func placePicker(pane: Binding<String>, group: Binding<String>) -> some View {
        let groups = model.scheduleGroups(on: computerID)
        let sel = Binding<String>(get: {
            !pane.wrappedValue.isEmpty ? "p:" + pane.wrappedValue : !group.wrappedValue.isEmpty ? "g:" + group.wrappedValue : ""
        }, set: { v in
            pane.wrappedValue = v.hasPrefix("p:") ? String(v.dropFirst(2)) : ""
            group.wrappedValue = v.hasPrefix("g:") ? String(v.dropFirst(2)) : ""
        })
        return Picker("In", selection: sel) {
            Text("Any pane").tag("")
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
                Text(cur.hasPrefix("p:") ? "A pane that’s closed" : "A Group that’s closed").tag(cur)
            }
        }
    }

    private static func placeTags(_ model: AppModel, _ groups: [WorkspaceGroup]) -> Set<String> {
        var out = Set<String>()
        for g in groups {
            out.insert("g:" + g.id)
            for t in model.tabs(in: g) where !t.focusedPaneID.isEmpty { out.insert("p:" + t.focusedPaneID) }
        }
        return out
    }

    /// The log panes a log trigger can watch, on the schedule's computer.
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

    private func copyWebhook(regenerate: Bool) async {
        do {
            let r = try await model.scheduleWebhook(scheduleID, trigger: trigger.id, regenerate: regenerate)
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

    private func github<T>(_ kp: WritableKeyPath<Hammock_V1_GitHubTrigger, T>) -> Binding<T> {
        Binding(get: { if case .github(let g) = trigger.kind { return g[keyPath: kp] }; return Hammock_V1_GitHubTrigger()[keyPath: kp] },
                set: { v in if case .github(var g) = trigger.kind { g[keyPath: kp] = v; trigger.kind = .github(g) } })
    }

    private func agent<T>(_ kp: WritableKeyPath<Hammock_V1_AgentTrigger, T>) -> Binding<T> {
        Binding(get: { if case .agent(let a) = trigger.kind { return a[keyPath: kp] }; return Hammock_V1_AgentTrigger()[keyPath: kp] },
                set: { v in if case .agent(var a) = trigger.kind { a[keyPath: kp] = v; trigger.kind = .agent(a) } })
    }

    private func exit<T>(_ kp: WritableKeyPath<Hammock_V1_ExitTrigger, T>) -> Binding<T> {
        Binding(get: { if case .exit(let x) = trigger.kind { return x[keyPath: kp] }; return Hammock_V1_ExitTrigger()[keyPath: kp] },
                set: { v in if case .exit(var x) = trigger.kind { x[keyPath: kp] = v; trigger.kind = .exit(x) } })
    }

    private func log<T>(_ kp: WritableKeyPath<Hammock_V1_LogTrigger, T>) -> Binding<T> {
        Binding(get: { if case .log(let l) = trigger.kind { return l[keyPath: kp] }; return Hammock_V1_LogTrigger()[keyPath: kp] },
                set: { v in if case .log(var l) = trigger.kind { l[keyPath: kp] = v; trigger.kind = .log(l) } })
    }

    private func files<T>(_ kp: WritableKeyPath<Hammock_V1_FilesTrigger, T>) -> Binding<T> {
        Binding(get: { if case .files(let f) = trigger.kind { return f[keyPath: kp] }; return Hammock_V1_FilesTrigger()[keyPath: kp] },
                set: { v in if case .files(var f) = trigger.kind { f[keyPath: kp] = v; trigger.kind = .files(f) } })
    }

    private func calendar<T>(_ kp: WritableKeyPath<Hammock_V1_CalendarTrigger, T>) -> Binding<T> {
        Binding(get: { if case .calendar(let c) = trigger.kind { return c[keyPath: kp] }; return Hammock_V1_CalendarTrigger()[keyPath: kp] },
                set: { v in if case .calendar(var c) = trigger.kind { c[keyPath: kp] = v; trigger.kind = .calendar(c) } })
    }
}

extension EventTrigger {
    /// What still needs filling in before it can be saved ("" when nothing).
    var missing: String {
        switch kind {
        case .github(let g): g.event == .labeled && g.label.trimmingCharacters(in: .whitespaces).isEmpty ? "Which label?" : ""
        case .agent(let a): !a.finished && !a.needsYou ? "An agent trigger fires when it finishes, needs you, or both" : ""
        case .log(let l): l.paneID.isEmpty ? "Which log pane?" : l.pattern.trimmingCharacters(in: .whitespaces).isEmpty ? "What should the log line match?" : ""
        default: ""
        }
    }
}

/// Made-up scheduled tasks with triggers, for HMK_DEBUG_SCHEDULES_SAMPLE:
/// test data only.
enum ScheduleSamples {
    static let webhook: Hammock_V1_ScheduleWebhookReply = .with {
        $0.urls = ["https://100.64.0.2:7433/hook/sample-secret-not-real"]
        $0.path = "/hook/sample-secret-not-real"
    }

    static func all(now: Date = Date()) -> [Schedule] {
        let ms = { (ago: TimeInterval) in Int64((now.timeIntervalSince1970 - ago) * 1000) }
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
            $0.runs = [.with { $0.id = "sr_s5"; $0.status = .ready; $0.trigger = "An event in 10 minutes"; $0.kind = "claude"; $0.startedUnixMs = ms(5 * 3600); $0.endedUnixMs = ms(5 * 3600 - 200); $0.note = "Notes are in 1-1 Sam.md." }]
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
            $0.events = [.with { $0.id = "tr_s5"; $0.kind = .files(.with { $0.glob = "src/**/*.go" }) }]
        }
        return [ci, prep, tests]
    }
}
