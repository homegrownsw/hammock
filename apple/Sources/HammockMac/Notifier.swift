import AppKit
import HammockKit
import UserNotifications

/// Which events notify, set in Settings ▸ Notifications (the config file).
enum NotifyPrefs {
    static let needsInput = Pref("notify-needs-input", defaults: "notify.needsInput", true)
    static let grants = Pref("notify-permission", defaults: "notify.grants", true)
    static let finished = Pref("notify-finished", defaults: "notify.finished", true)
    static let errored = Pref("notify-error", defaults: "notify.errored", true)
    static let sound = Pref("notify-sound", defaults: "notify.sound", true)
    /// Also for the tab you're looking at.
    static let whenVisible = Pref("notify-current-tab", defaults: "notify.whenVisible", false)
    /// The count on the Dock icon.
    static let badge = Pref("dock-badge", defaults: "notify.badge", true)
    /// Where they show: a NotifyStyle.
    static let style = Pref("notify-style", defaults: "notify.style", .string, NotifyStyle.auto.rawValue, renamed: ["inApp": NotifyStyle.inApp.rawValue])

    /// The sound for agents (needing you, finishing, erroring) and requests: a soft kalimba note
    /// (NeedsYou.caf, in the app's Resources, made by scripts/needs-you-sound.py).
    @MainActor static var needsYouSound: UNNotificationSound? {
        on(sound) ? UNNotificationSound(named: UNNotificationSoundName(installedSound ?? "NeedsYou.caf")) : nil
    }

    /// Notification Center didn't find the sound in the app's Resources (it
    /// played the default instead), so it's also put in ~/Library/Sounds,
    /// where it always looks, under a name of its own. Its file name there,
    /// or nil if that couldn't be written.
    static let installedSound: String? = {
        let name = "Hammock Needs You.caf"
        guard let src = Bundle.main.url(forResource: "NeedsYou", withExtension: "caf"),
              let lib = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask).first else { return nil }
        let dir = lib.appendingPathComponent("Sounds")
        let dst = dir.appendingPathComponent(name)
        let fm = FileManager.default
        if fm.contentsEqual(atPath: src.path, andPath: dst.path) { return name }
        do {
            try fm.createDirectory(at: dir, withIntermediateDirectories: true)
            if fm.fileExists(atPath: dst.path) { try fm.removeItem(at: dst) }
            try fm.copyItem(at: src, to: dst)
            return name
        } catch {
            NSLog("Hammock: couldn't put the notification sound in %@: %@", dir.path, error.localizedDescription)
            return nil
        }
    }()

    @MainActor static func on(_ pref: Pref) -> Bool { Prefs.shared.bool(pref) }
}

/// Posts notifications for attention items, skipping panes the user is
/// already looking at: to Notification Center (which needs an app bundle) or
/// as cards in the window (InAppNotices), as NotifyStyle says. Withdraws them
/// once what they were about is dealt with.
@MainActor
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    static let shared = Notifier()
    private var center: UNUserNotificationCenter? {
        Bundle.main.bundleIdentifier == nil ? nil : UNUserNotificationCenter.current()
    }

    func setUp() {
        guard let center else { return }
        center.delegate = self
        _ = NotifyPrefs.installedSound // in place before the first notification
    }

    /// Posts a notification, asking macOS for permission the first time:
    /// when an agent first needs you, so the prompt makes sense, rather than
    /// at launch.
    private func post(_ request: UNNotificationRequest) {
        guard let center else { return }
        Task {
            if await center.notificationSettings().authorizationStatus == .notDetermined {
                guard (try? await center.requestAuthorization(options: [.alert, .sound, .badge])) == true else { return }
            }
            // Dealt with while macOS was asked (the permission prompt waits
            // for you): withdrawn already, so not posted now.
            guard Self.stillLive(request) else { return }
            try? await center.add(request)
        }
    }

    /// Shows a notification where Settings says: in the window (playing its
    /// sound here, as Notification Center would) or as a macOS notification.
    /// `open` is what clicking the card does.
    private func deliver(_ request: UNNotificationRequest, icon: InAppNotice.Icon, open: (@MainActor () -> Void)? = nil) {
        guard NotifyStyle.current.showsInAppNow else { return post(request) }
        let c = request.content
        InAppNotices.shared.post(InAppNotice(id: request.identifier, icon: icon, title: c.title, subtitle: c.subtitle, body: c.body,
                                             live: c.userInfo[Self.live] != nil, open: open))
        if c.sound != nil { Self.playSound() }
    }

    private static let sound: NSSound? = Bundle.main.url(forResource: "NeedsYou", withExtension: "caf")
        .flatMap { NSSound(contentsOf: $0, byReference: true) }

    private static func playSound() {
        guard let s = sound ?? NSSound(named: "Glass") else { return }
        s.stop()
        s.play()
    }

    /// Clicking a card about a pane: as clicking its macOS notification does.
    private static func reveal(_ paneID: String) -> @MainActor () -> Void {
        { NSApp.activate(); AppModelRegistry.shared?.reveal(paneID: paneID) }
    }

    func notify(_ item: AttentionItem, pane: Pane?, model: AppModel) {
        updateBadge(model)
        // With computers linked, the one you used last tells you (its
        // sidebar has every computer's agents).
        guard model.notifiesHere else { return }
        switch item.state {
        case .needsInput: guard NotifyPrefs.on(NotifyPrefs.needsInput) else { return }
        case .idle: guard NotifyPrefs.on(NotifyPrefs.finished) else { return }
        case .errored: guard NotifyPrefs.on(NotifyPrefs.errored) else { return }
        default: break
        }
        // Presence-aware: if the app is active and the pane is on screen, don't interrupt.
        if !NotifyPrefs.on(NotifyPrefs.whenVisible), NSApp.isActive, let tab = model.selectedTab,
           AppModel.shownPaneIDs(of: tab).contains(item.paneID) { return }
        let content = UNMutableNotificationContent()
        // The tab as the sidebar names it ("Tab 2" when unnamed), in its Group.
        let tab = pane.flatMap { model.tab(of: $0) }
        content.title = tab.map { model.title(of: $0) } ?? (item.paneID.isEmpty ? "Hammock" : "Agent") // (none: Hammock's own)
        if let s = pane.flatMap({ model.schedule(for: $0) }) {
            content.subtitle = "Scheduled · \(s.name)"
        } else if let group = tab.flatMap({ model.group(of: $0) }) {
            content.subtitle = (group.icon.isEmpty ? "" : group.icon + " ") + model.title(of: group)
        }
        let who = pane?.displayName.capitalized ?? "Agent"
        switch item.state {
        case .needsInput where item.question: content.body = "\(who) asks: " + item.summary
        case .needsInput: content.body = item.summary.isEmpty ? "\(who) needs your input" : item.summary
        case .idle: content.body = "\(who) finished"
        case .errored: content.body = "\(who) \(item.summary)"
        default: content.body = item.summary
        }
        content.sound = NotifyPrefs.needsYouSound
        content.userInfo = ["paneID": item.paneID, Self.live: true]
        content.threadIdentifier = item.paneID
        deliver(UNNotificationRequest(identifier: item.id, content: content, trigger: nil), icon: .agent(item.state), open: Self.reveal(item.paneID))
    }

    /// A scheduled run ended (the daemon raises no "finished" item for
    /// one): as its schedule's Notify me says. Questions on the way go
    /// through Needs You, as any agent's.
    func notifyRun(_ run: ScheduleRun, of s: Schedule, model: AppModel) {
        guard model.notifiesHere, s.notifies(run.status) else { return }
        // Its tab in front of you: you saw it end.
        if !NotifyPrefs.on(NotifyPrefs.whenVisible), NSApp.isActive, let tab = model.selectedTab, tab.id == run.tabID { return }
        let content = UNMutableNotificationContent()
        content.title = s.name
        content.subtitle = ScheduleText.groupName(model, s)
        switch run.status {
        case .ready: content.body = run.note.isEmpty ? "Ready" : run.note
        case .gaveUp: content.body = "Gave up after waiting \(Schedule.durationText(minutes: s.givesUpAfter)) for you"
        default:
            let why = !run.note.isEmpty ? run.note : s.kind == "command" ? "exit code \(run.exitCode)" : "it hit an error"
            content.body = "Failed: " + why
        }
        content.sound = NotifyPrefs.needsYouSound
        content.userInfo = ["scheduleID": s.id, "scheduleRunID": run.id]
        content.threadIdentifier = "schedule-" + s.id
        let state: AgentState = run.status == .ready ? .idle : .errored
        deliver(UNNotificationRequest(identifier: "run-" + run.id, content: content, trigger: nil), icon: .agent(state)) {
            Self.openRun(scheduleID: s.id, runID: run.id)
        }
    }

    /// Clicking a run's notification: its tab, its session resumed if
    /// that's closed, or else the schedule in the list.
    private static func openRun(scheduleID: String, runID: String) {
        NSApp.activate()
        guard let model = AppModelRegistry.shared, let s = model.schedules[scheduleID] else { return }
        if let run = s.runs.first(where: { $0.id == runID }), run.hasTab(in: model) || (s.kind != "command" && !run.sessionID.isEmpty) {
            Task { await model.open(run, of: s) }
        } else {
            ScheduleUI.shared.showList(select: scheduleID)
        }
    }

    func notifyGrant(_ grant: GrantRequest, model: AppModel) {
        updateBadge(model)
        // Your other computers' agents ask here too: the one you used last tells you.
        guard model.notifiesHere, NotifyPrefs.on(NotifyPrefs.grants) else { return }
        let content = UNMutableNotificationContent()
        content.title = grant.person ? "Allow this?" : "Permission requested"
        if let origin = grant.originText {
            content.subtitle = origin
        } else if let tab = model.panes[grant.requesterPaneID].flatMap({ model.tab(of: $0) }) {
            content.subtitle = model.title(of: tab)
        }
        content.body = grant.headlineText
        content.sound = NotifyPrefs.needsYouSound
        content.userInfo = ["paneID": grant.requesterPaneID, Self.live: true]
        deliver(UNNotificationRequest(identifier: grant.id, content: content, trigger: nil), icon: .agent(.needsInput), open: Self.reveal(grant.requesterPaneID))
    }

    /// Someone asks to join a Group you share: let them view or decline from
    /// the banner; driving is decided in Hammock, where the card shows their key.
    func notifyJoin(_ req: JoinRequest, model: AppModel) {
        let drive = UNNotificationAction(identifier: "join.drive", title: "Let Drive…", options: .foreground)
        let view = UNNotificationAction(identifier: "join.view", title: "Let View")
        let decline = UNNotificationAction(identifier: "join.decline", title: "Decline", options: .destructive)
        center?.setNotificationCategories([UNNotificationCategory(identifier: "join", actions: [drive, view, decline], intentIdentifiers: [])])
        let content = UNMutableNotificationContent()
        let who = model.people[req.userID]
        let group = model.groups.first { $0.id == req.groupID }
        content.title = "\(who?.name ?? "Someone") wants to join"
        content.subtitle = group.map { model.title(of: $0) } ?? ""
        content.body = [who.map { "From \($0.deviceName)." }, req.keyWarning(name: who?.name).map { $0 + "." }]
            .compactMap { $0 }.joined(separator: " ")
        content.categoryIdentifier = "join"
        content.sound = NotifyPrefs.needsYouSound
        content.userInfo = ["joinID": req.id, Self.live: true]
        // As a card, a click opens Hammock, where the join card in Needs You decides.
        deliver(UNNotificationRequest(identifier: req.id, content: content, trigger: nil), icon: .symbol("person.badge.plus"), open: { NSApp.activate() })
    }

    /// Another computer of yours asks to link: its code, to check on both.
    func notifyLink(_ req: LinkRequest) {
        guard !req.code.isEmpty else { return } // shown once it has one
        let content = UNMutableNotificationContent()
        content.title = "\(req.name) wants to link"
        content.body = "Check the code on both computers: \(req.code)"
        content.sound = NotifyPrefs.on(NotifyPrefs.sound) ? .default : nil
        content.userInfo = [Self.live: true]
        deliver(UNNotificationRequest(identifier: req.id, content: content, trigger: nil), icon: .symbol("link"))
    }

    /// A device paired with this Mac: worth knowing even when you did it,
    /// and more so when you didn't.
    func notifyPaired(_ device: String) {
        let content = UNMutableNotificationContent()
        content.title = "\(device.isEmpty ? "A device" : device) paired"
        content.body = "It can now reach this Mac’s Groups. Revoke it in Settings ▸ Devices."
        content.sound = NotifyPrefs.on(NotifyPrefs.sound) ? .default : nil
        deliver(UNNotificationRequest(identifier: "paired-\(Date().timeIntervalSince1970)", content: content, trigger: nil), icon: .symbol("iphone"))
    }

    /// Marks a notification about something that can still need you (an
    /// agent, a grant, a join or link request), which goes once that's dealt
    /// with. Ones posted before it existed are told by their paneID or
    /// joinID, or a link request's id.
    private static let live = "live"

    private static func isLive(_ request: UNNotificationRequest) -> Bool {
        let info = request.content.userInfo
        return info[live] != nil || info["paneID"] != nil || info["joinID"] != nil || request.identifier.hasPrefix("ln_")
    }

    /// Whether what a notification is about still needs you (always, for one
    /// that isn't about anything that clears).
    private static func stillLive(_ request: UNNotificationRequest) -> Bool {
        guard request.content.userInfo[live] != nil, let model = AppModelRegistry.shared else { return true }
        return model.liveNoticeIDs.contains(request.identifier)
    }

    /// What these notifications were about is dealt with (answered, seen,
    /// cleared, withdrawn): take them out of Notification Center and the
    /// window. An agent's newer state replaces its pane's notification by
    /// itself, as the item keeps its id.
    func withdraw(_ ids: [String]) {
        ids.forEach(InAppNotices.shared.dismiss)
        center?.removeDeliveredNotifications(withIdentifiers: ids)
        center?.removePendingNotificationRequests(withIdentifiers: ids)
    }

    /// After connecting (and at launch): withdraws notifications about things
    /// no longer live, including ones left from before Hammock last quit.
    func reconcile(_ model: AppModel) {
        let live = model.liveNoticeIDs
        InAppNotices.shared.dismissAll { $0.live && !live.contains($0.id) }
        guard let center else { return }
        Task {
            let delivered = await center.deliveredNotifications()
            // Read again: something raised (and posted) while macOS answered is live.
            let live = model.liveNoticeIDs
            let stale = delivered.filter { Self.isLive($0.request) && !live.contains($0.request.identifier) }
                .map(\.request.identifier)
            if !stale.isEmpty { center.removeDeliveredNotifications(withIdentifiers: stale) }
        }
    }

    func updateBadge(_ model: AppModel) {
        let n = model.attention.filter { $0.state == .needsInput }.count + model.grants.count
        NSApp.dockTile.badgeLabel = n > 0 && NotifyPrefs.on(NotifyPrefs.badge) ? "\(n)" : nil
    }

    /// Whether macOS lets Hammock notify, for Settings.
    func authorization() async -> UNAuthorizationStatus {
        guard let center else { return .denied }
        return await center.notificationSettings().authorizationStatus
    }

    /// A test in the chosen style: from Settings, Hammock is in front, so the
    /// automatic style shows the card.
    func sendTest() {
        let content = UNMutableNotificationContent()
        content.title = "Hammock"
        content.body = NotifyStyle.current.showsInAppNow
            ? "Notifications are working. Agents that need you show up here."
            : "Notifications are working. You'll hear about agents that need you here."
        content.sound = NotifyPrefs.needsYouSound
        deliver(UNNotificationRequest(identifier: "test-\(Date().timeIntervalSince1970)", content: content, trigger: nil), icon: .symbol("bell"))
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        if let joinID = response.notification.request.content.userInfo["joinID"] as? String {
            let action = response.actionIdentifier
            await MainActor.run {
                guard let model = AppModelRegistry.shared, let req = model.joinRequests.first(where: { $0.id == joinID }) else { return }
                switch action {
                // Driving is decided on the join card in Needs You, with the key in view.
                case "join.view": model.respond(to: req, role: .viewer)
                case "join.decline": model.respond(to: req, role: nil)
                default: NSApp.activate() // decide in Needs You
                }
            }
            return
        }
        if let scheduleID = response.notification.request.content.userInfo["scheduleID"] as? String {
            let runID = response.notification.request.content.userInfo["scheduleRunID"] as? String ?? ""
            await MainActor.run { Self.openRun(scheduleID: scheduleID, runID: runID) }
            return
        }
        guard let paneID = response.notification.request.content.userInfo["paneID"] as? String else { return }
        await MainActor.run {
            NSApp.activate()
            AppModelRegistry.shared?.reveal(paneID: paneID)
        }
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification) async -> UNNotificationPresentationOptions {
        [.banner, .sound]
    }
}
