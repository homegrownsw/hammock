import AppKit
import HammockKit
import Observation
import SwiftUI

/// Automations on the Mac: a window of their own (View ▸ Automations), the
/// list beside the selected one, read (ScheduleDetail) or edited in place
/// (ScheduleEditor: Edit, File ▸ New Automation…, Automate This…).
@MainActor
@Observable
final class ScheduleUI {
    static let shared = ScheduleUI()

    /// HMK_DEBUG_SCHEDULES=list or edit opens the window (and the editor) at launch (DebugSnapshot).
    private static let debug = ProcessInfo.processInfo.environment["HMK_DEBUG_SCHEDULES"]

    /// The Automations window is the key window: its commands (Run
    /// Automation Now) are in the menu bar, and a browser pane's aren't.
    private(set) var windowIsKey = false
    /// The automation the window shows.
    var selected: String?
    /// The one being edited (or made), in place of the selected one's page.
    var editing: ScheduleDraft?
    /// What the list is filtered by.
    var search = ""

    @ObservationIgnored private(set) var window: NSWindow?
    @ObservationIgnored private var observers: [NSObjectProtocol] = []
    @ObservationIgnored private var closer: WindowCloser?

    /// Opens the window (selecting id), keeping anything being edited.
    func showList(select id: String? = nil) {
        if let id, editing == nil { selected = id }
        openWindow()
    }

    func edit(_ s: Schedule) {
        selected = s.id
        guard let model = AppModelRegistry.shared else { return }
        editing = ScheduleDraft(model: model, original: s)
        openWindow()
    }

    /// Cancel (or Esc): stops editing, asking first if anything's changed.
    func cancelEdit() {
        guard let draft = editing else { return }
        if draft.changed {
            let alert = NSAlert()
            alert.messageText = draft.isNew ? "Discard this new automation?" : "Discard your changes to “\(draft.original.name)”?"
            alert.addButton(withTitle: "Discard").hasDestructiveAction = true
            alert.addButton(withTitle: "Keep Editing")
            guard alert.runModal() == .alertFirstButtonReturn else { return }
        }
        editing = nil
    }

    /// The selected automation, while the window's in front.
    func selectedSchedule(_ model: AppModel) -> Schedule? {
        windowIsKey && editing == nil ? selected.flatMap { model.schedules[$0] } : nil
    }

    /// Run Automation Now (⌘R in the window).
    func runSelected(_ model: AppModel) {
        guard let s = selectedSchedule(model) else { return }
        ScheduleActions.runNow(model, s)
    }

    /// A new automation on this computer: in groupID (else the Group you're
    /// in, if it's this computer's), in its folder.
    func new(_ model: AppModel, groupID: String? = nil, cwd: String? = nil, kind: String = "claude") {
        let local = model.scheduleGroups(on: "")
        let group = (groupID ?? model.currentGroup?.id).flatMap { id in local.first { $0.id == id } }
        let folder = cwd ?? group.map { model.groupFolder($0) } ?? ""
        editing = ScheduleDraft(model: model, original: .with {
            $0.kind = kind
            $0.groupID = group?.id ?? ""
            $0.groupName = group?.name ?? ""
            $0.cwd = GroupFolder.tilde(folder.isEmpty ? NSHomeDirectory() : folder)
            $0.weekdays = [1, 2, 3, 4, 5]
            $0.minuteOfDay = 9 * 60
        })
        openWindow()
    }

    /// Another like s, to change before it's saved.
    func duplicate(_ model: AppModel, _ s: Schedule) {
        var copy = s
        copy.id = ""
        copy.name = s.name + " copy"
        copy.runs = []
        copy.nextRunUnixMs = 0
        copy.skipUntilUnixMs = 0
        copy.createdBy = ""
        copy.createdUnixMs = 0
        copy.error = ""
        copy.paused = false
        for i in copy.events.indices {
            copy.events[i].id = ""
            copy.events[i].checkedUnixMs = 0
            copy.events[i].firedUnixMs = 0
            copy.events[i].held = ""
            copy.events[i].error = ""
        }
        editing = ScheduleDraft(model: model, original: copy)
        openWindow()
    }

    /// Automate This… on a tab: its folder, Group and agent. An agent tab's
    /// also start from its session: named after the tab (as you named it,
    /// else the session's title), with the first prompt you sent in it.
    func scheduleThis(_ model: AppModel, _ tab: WorkspaceTab) {
        let agent = model.shownAgentPane(of: tab)
        let pane = agent ?? model.panes[tab.focusedPaneID]
        new(model, groupID: tab.groupID, cwd: pane?.cwd, kind: agent?.agent.harness ?? "command")
        guard let agent, let draft = editing else { return }
        let name = !tab.name.isEmpty ? tab.name : tab.autoKey.hasPrefix("agent:") ? tab.autoName : ""
        // Shown at once, the prompt filled in when the session's been read
        // (unless you've started typing one).
        draft.s.name = name
        let original = draft.s
        Task {
            guard let start = await model.sessionStart(harness: agent.agent.harness, id: agent.agent.sessionID),
                  editing === draft else { return }
            if draft.s.name == original.name, name.isEmpty, let t = start.title, !t.isEmpty { draft.s.name = t }
            if draft.s.prompt.isEmpty, draft.s.kind != "command" { draft.s.prompt = start.prompt }
        }
    }

    /// Whether a tab can be automated: one in a Group of this computer's.
    static func canSchedule(_ model: AppModel, _ tab: WorkspaceTab) -> Bool {
        HammockTool.schedules.on && model.group(of: tab)?.isRemote == false
    }

    /// Opens the window asked for by HMK_DEBUG_SCHEDULES, once the
    /// automations have arrived.
    func openDebug(_ model: AppModel) {
        switch Self.debug {
        case "edit":
            let first = selected.flatMap { model.schedules[$0] } ?? model.sortedSchedules.first
            if let s = first { edit(s) } else { new(model) }
        case "new": new(model)
        default: showList()
        }
    }

    // MARK: The window

    private func openWindow() {
        if let window {
            window.makeKeyAndOrderFront(nil)
            NSApp.activate()
            return
        }
        guard let model = AppModelRegistry.shared else { return }
        let host = NSHostingController(rootView: AutomationsView(model: model).themeTint())
        // Its toolbar and title are the view's (.toolbar, .navigationTitle).
        host.sceneBridgingOptions = [.toolbars, .title]
        let w = NSWindow(contentViewController: host)
        w.styleMask = [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView]
        w.isReleasedWhenClosed = false
        w.toolbarStyle = .unified
        w.contentMinSize = NSSize(width: 820, height: 520)
        w.setContentSize(NSSize(width: 1040, height: 740))
        w.center()
        w.setFrameAutosaveName("HammockAutomations")
        let closer = WindowCloser { [weak self] in
            guard let self else { return }
            // Whatever was being edited goes with it.
            self.editing = nil
            self.windowIsKey = false
            self.window = nil
            self.closer = nil
            self.observers.forEach(NotificationCenter.default.removeObserver)
            self.observers = []
        }
        w.delegate = closer
        self.closer = closer
        for (name, key) in [(NSWindow.didBecomeKeyNotification, true), (NSWindow.didResignKeyNotification, false)] {
            observers.append(NotificationCenter.default.addObserver(forName: name, object: w, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.windowIsKey = key }
            })
        }
        window = w
        w.makeKeyAndOrderFront(nil)
        NSApp.activate()
    }

    /// Brings the workspace window forward: a run's tab is there.
    func showWorkspace() {
        WorkspaceWindow.window?.makeKeyAndOrderFront(nil)
    }

    private final class WindowCloser: NSObject, NSWindowDelegate {
        let onClose: () -> Void
        init(_ onClose: @escaping () -> Void) { self.onClose = onClose }
        func windowWillClose(_ notification: Notification) { onClose() }
    }
}

/// The Automations window: the list (by when they run, or by Group; paused
/// ones last) beside the selected one's page, or the one being edited.
struct AutomationsView: View {
    let model: AppModel
    @Bindable private var ui = ScheduleUI.shared
    /// How the list is divided: "when" (on a schedule, on triggers) or "group".
    @AppStorage("automationsSections") private var sections = "when"
    @State private var hovered: String?

    private var all: [Schedule] { model.sortedSchedules }
    /// Those matching the search: by name, prompt or command, Group, kind or when.
    private var list: [Schedule] {
        let q = ui.search.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return all }
        return all.filter { s in
            [s.name, s.prompt, ScheduleText.groupName(model, s), s.kindName, s.listWhenText].contains { $0.localizedStandardContains(q) }
        }
    }

    private var editing: Bool { ui.editing != nil }

    var body: some View {
        NavigationSplitView {
            sidebar
                .navigationSplitViewColumnWidth(min: 240, ideal: 290, max: 380)
                .toolbar(removing: .sidebarToggle)
                .background { SidebarTint().ignoresSafeArea() }
        } detail: {
            detail
                .settingsPaneCard()
        }
        .navigationTitle("Automations")
        .searchable(text: $ui.search, placement: .sidebar, prompt: "Search")
        .toolbar { toolbar }
        // Its toolbar, sidebar and search field drawn as Settings' are.
        .background(SettingsWindowStyle())
        .toggleStyle(ThemedSwitchStyle())
        .frame(minWidth: 820, minHeight: 520)
        .onAppear(perform: keepSelection)
        .onChange(of: list.map(\.id)) { keepSelection() }
    }

    // MARK: Toolbar

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        if let draft = ui.editing {
            ToolbarItem(placement: .cancellationAction) {
                Button("Cancel") { ui.cancelEdit() }
                    .keyboardShortcut(.cancelAction)
            }
            ToolbarItem(placement: .confirmationAction) {
                Button(draft.isNew ? "Create" : "Save") { draft.save(model) }
                    .keyboardShortcut("s")
                    .buttonStyle(.borderedProminent)
                    .disabled(draft.saving || !draft.canSave)
                    .help(draft.missing ?? (draft.isNew ? "Create it (⌘S)" : "Save your changes (⌘S)"))
            }
        } else {
            if let s = ui.selected.flatMap({ model.schedules[$0] }) {
                ToolbarItemGroup(placement: .primaryAction) {
                    Button("Edit") { ui.edit(s) }
                        .help("Change what it runs and when")
                    ScheduleRunButton(model: model, schedule: s)
                    Menu {
                        ScheduleMenuItems(model: model, schedule: s, inWindow: true)
                        Divider()
                        Picker("Sections", selection: $sections) {
                            Text("By When They Run").tag("when")
                            Text("By Group").tag("group")
                        }
                    } label: {
                        Label("More", systemImage: "ellipsis")
                    }
                    .help("More")
                }
            }
            ToolbarItem(placement: .primaryAction) {
                Button { ui.new(model) } label: { Label("New Automation", systemImage: "plus") }
                    .help(Keys.hint("New Automation", "new_scheduled_task"))
            }
        }
    }

    // MARK: List

    /// The list's sections, paused ones last.
    private var listSections: [(title: String, items: [Schedule])] {
        let on = list.filter { !$0.paused }
        var out: [(String, [Schedule])] = []
        if sections == "group" {
            var order: [String] = []
            var byGroup: [String: [Schedule]] = [:]
            for s in on {
                let g = ScheduleText.groupName(model, s)
                if byGroup[g] == nil { order.append(g) }
                byGroup[g, default: []].append(s)
            }
            out = order.map { ($0.isEmpty ? "No Group" : $0, byGroup[$0] ?? []) }
        } else {
            out = [("On a Schedule", on.filter { $0.timesText != nil }), ("On Triggers", on.filter { $0.timesText == nil })]
        }
        out.append(("Paused", list.filter(\.paused)))
        return out.filter { !$0.1.isEmpty }
    }

    @ViewBuilder
    private var sidebar: some View {
        if all.isEmpty && !editing {
            Color.clear
        } else if list.isEmpty && !editing {
            Text("No automations match “\(ui.search)”.")
                .font(.callout)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .padding()
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            List {
                if let d = ui.editing, d.isNew {
                    Section {
                        ScheduleListRow(model: model, schedule: d.s, draft: true)
                            .settingsSidebarRow("draft", selected: true, hovered: $hovered) {}
                    }
                }
                ForEach(listSections, id: \.title) { section in
                    Section(section.title) {
                        ForEach(section.items, id: \.id) { s in
                            ScheduleListRow(model: model, schedule: s, draft: ui.editing?.original.id == s.id)
                                .settingsSidebarRow(s.id, selected: ui.editing.map { $0.original.id == s.id } ?? (ui.selected == s.id), hovered: $hovered) {
                                    ui.selected = s.id
                                }
                                .contextMenu { ScheduleMenuItems(model: model, schedule: s) }
                        }
                    }
                }
            }
            // While one's edited, the list waits: Save or Cancel first.
            .disabled(editing)
            .opacity(editing ? 0.55 : 1)
            .focusable(!editing)
            .focusEffectDisabled()
            .onKeyPress(.downArrow) { move(1) }
            .onKeyPress(.upArrow) { move(-1) }
            // ⌫ (Edit ▸ Delete): the selected one, asking first.
            .onDeleteCommand {
                if !editing, let s = ui.selected.flatMap({ model.schedules[$0] }) { ScheduleActions.confirmDelete(model, s) }
            }
        }
    }

    /// The next or previous one in the list, as it's shown.
    private func move(_ by: Int) -> KeyPress.Result {
        let order = listSections.flatMap(\.items).map(\.id)
        guard !editing, !order.isEmpty else { return .ignored }
        let i = ui.selected.flatMap { order.firstIndex(of: $0) } ?? -by
        ui.selected = order[max(0, min(order.count - 1, i + by))]
        return .handled
    }

    // MARK: Detail

    @ViewBuilder
    private var detail: some View {
        if let draft = ui.editing {
            ScheduleEditor(model: model, draft: draft)
                .id(draft.id)
        } else if all.isEmpty {
            ContentUnavailableView {
                Label("No Automations", systemImage: "clock")
            } description: {
                Text("Run Claude Code, Codex or a command at set times, or when something happens. Each run opens a tab in its Group, so you can pick up where it left off.")
            } actions: {
                Button("New Automation") { ui.new(model) }
                    .buttonStyle(.borderedProminent)
            }
        } else if let s = ui.selected.flatMap({ model.schedules[$0] }) {
            ScheduleDetail(model: model, schedule: s)
                .id(s.id)
        } else {
            ContentUnavailableView("Select an Automation", systemImage: "clock")
        }
    }

    /// Something is always selected while there's anything to select (that the search shows).
    private func keepSelection() {
        guard !editing else { return }
        if !list.contains(where: { $0.id == ui.selected }) { ui.selected = listSections.first?.items.first?.id }
    }
}

/// An automation in the list: its name, what starts it, and a dot when its
/// last run failed or waits on you.
struct ScheduleListRow: View {
    let model: AppModel
    let schedule: Schedule
    /// It's the one being edited (or made).
    var draft = false

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: schedule.timesText != nil ? "clock" : "bolt")
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(.secondary)
                .frame(width: 26, height: 26)
                .background(.quaternary.opacity(0.7), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 5) {
                    Text(schedule.name.isEmpty ? "New Automation" : schedule.name)
                        .fontWeight(.semibold)
                        .foregroundStyle(schedule.paused ? .secondary : .primary)
                        .lineLimit(1)
                    if let c = model.computer(of: schedule) { ComputerBadge(c, size: 11) }
                }
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 4)
            if let dot {
                Circle().fill(dot.color).frame(width: 7, height: 7)
                    .help(dot.help)
                    .accessibilityLabel(dot.help)
            }
        }
        .padding(.vertical, 2)
    }

    private var subtitle: String {
        if draft { return "Editing" }
        if schedule.paused { return "Paused" }
        if !schedule.error.isEmpty { return schedule.error }
        var parts = [schedule.listWhenText]
        if schedule.kind != "claude", !schedule.kind.isEmpty { parts.insert(schedule.kindName, at: 0) }
        if let r = schedule.runs.first, r.status.isProblem { parts.append("last run \(r.status.label.lowercased())") }
        return parts.joined(separator: " · ")
    }

    /// Red: its last run failed, or it can't run; orange: a run waits on you.
    private var dot: (color: Color, help: String)? {
        guard !draft else { return nil }
        if !schedule.error.isEmpty { return (.red, schedule.error) }
        guard let r = schedule.runs.first else { return nil }
        if schedule.runs.contains(where: { $0.status == .waiting }) { return (.orange, "Waiting for you") }
        return r.status.isProblem ? (.red, "Last run: \(r.status.label)") : nil
    }
}

/// How automations are put in the Mac's lists and menus.
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
        guard s.nextRunUnixMs > 0 else { return s.events.isEmpty ? "Not due again" : "When triggered" }
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

/// An automation's actions, for its right-click menu and the window's ⋯.
struct ScheduleMenuItems: View {
    let model: AppModel
    let schedule: Schedule
    /// In the Automations window, which has Edit, Run Now and On beside it already.
    var inWindow = false

    var body: some View {
        if !inWindow {
            Button("Run Now") { ScheduleActions.runNow(model, schedule) }
        }
        if schedule.canRunEarly, !inWindow {
            Button(schedule.runEarlyTitle()) { ScheduleActions.runNow(model, schedule, early: true) }
        }
        if !schedule.paused, schedule.nextRunUnixMs > 0 {
            Button(schedule.skipping ? "Don’t Skip Next Run" : "Skip Next Run") { ScheduleActions.skip(model, schedule, !schedule.skipping) }
        }
        Button(schedule.paused ? "Turn On" : "Pause") { ScheduleActions.setPaused(model, schedule, !schedule.paused) }
        Divider()
        Button("Edit") { ScheduleUI.shared.edit(schedule) }
        Button("Duplicate") { ScheduleUI.shared.duplicate(model, schedule) }
        if schedule.hasWebhook {
            Button("Copy Webhook URL") { ScheduleActions.copyWebhook(model, schedule) }
        }
        Divider()
        Button("Delete…", role: .destructive) { ScheduleActions.confirmDelete(model, schedule) }
    }
}

/// Run Now, or (its next run coming up soon) Run Early, saying which, with
/// the other in its menu.
struct ScheduleRunButton: View {
    let model: AppModel
    let schedule: Schedule

    var body: some View {
        if !schedule.canRunEarly {
            Button { ScheduleActions.runNow(model, schedule) } label: { Label("Run Now", systemImage: "play.fill") }
                .labelStyle(.titleAndIcon)
                .help("Run it now")
        } else {
            let early = schedule.runsEarlyFirst()
            Menu {
                if early {
                    Button(schedule.runAsWellTitle()) { ScheduleActions.runNow(model, schedule) }
                } else {
                    Button(schedule.runEarlyTitle()) { ScheduleActions.runNow(model, schedule, early: true) }
                }
            } label: {
                Label(early ? schedule.runEarlyTitle() : "Run Now", systemImage: "play.fill")
                    .labelStyle(.titleAndIcon)
            } primaryAction: {
                ScheduleActions.runNow(model, schedule, early: early)
            }
            .help(early ? "Runs it now, and not \(schedule.nextRunPhrase())" : "Runs it now, and \(schedule.nextRunPhrase()) as usual")
        }
    }
}

extension Schedule {
    /// It has a webhook trigger (its URL: Copy Webhook URL).
    var hasWebhook: Bool { events.contains { if case .webhook = $0.kind { true } else { false } } }
}

/// What the window's buttons, menus and the palette do, with errors said in a toast.
@MainActor
enum ScheduleActions {
    /// early: Run Early, in place of its next run. Its tab opens in the
    /// workspace window, which comes forward.
    static func runNow(_ model: AppModel, _ s: Schedule, early: Bool = false) {
        ScheduleUI.shared.showWorkspace()
        Task {
            do { try await model.runScheduleNow(s.id, early: early) } catch { model.showToast("Couldn’t run “\(s.name)”: \(error.localizedDescription)") }
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

    /// Asks first.
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

    /// Its webhook's URL, on the clipboard.
    static func copyWebhook(_ model: AppModel, _ s: Schedule) {
        Task {
            do {
                let r = try await model.scheduleWebhook(s.id)
                guard let u = r.urls.first else {
                    model.showToast(r.problem.isEmpty ? "The webhook can’t be reached now" : r.problem)
                    return
                }
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(u, forType: .string)
                model.showToast(r.problem.isEmpty ? "Copied the webhook’s URL" : "Copied the webhook’s URL. \(r.problem)")
            } catch {
                model.showToast("Couldn’t get its URL: \(error.localizedDescription)")
            }
        }
    }

    static func open(_ model: AppModel, _ run: ScheduleRun, of s: Schedule) {
        ScheduleUI.shared.showWorkspace()
        Task { await model.open(run, of: s) }
    }
}

/// A run's status: a spinner while it runs, else its symbol.
struct RunStatusIcon: View {
    let status: ScheduleRun.Status

    var body: some View {
        if status == .running {
            ProgressView().controlSize(.mini)
                .accessibilityLabel(status.label)
        } else {
            Image(systemName: status.symbol)
                .foregroundStyle(status.color)
        }
    }
}

/// In the sidebar's footer, from half an hour before the next run is due:
/// what it is. A click opens the Automations window.
struct ScheduleFooter: View {
    let model: AppModel
    /// How long before a run the footer says it's next.
    static let lead: TimeInterval = 30 * 60

    var body: some View {
        if HammockTool.schedules.on, !model.schedules.isEmpty {
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
        .help(Keys.hint("Automations", "scheduled_tasks"))
    }

    private func text(now: Date) -> String {
        guard let s = model.nextSchedule else { return "No automations coming up" }
        let when = Schedule.dateText(s.nextRunUnixMs, now: now)
        // "Today at 08:30" reads as "Next: Morning briefing at 08:30".
        if when.hasPrefix("Today ") { return "Next: \(s.name) \(when.dropFirst("Today ".count))" }
        return "Next: \(s.name), \(when.hasPrefix("Tomorrow") ? "tomorrow" + when.dropFirst("Tomorrow".count) : when)"
    }
}
