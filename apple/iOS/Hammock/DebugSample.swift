#if DEBUG
import HammockKit
import Foundation

/// Dev aid: HMK_DEBUG_SAMPLE=1 (in the simulator, SIMCTL_CHILD_HMK_DEBUG_SAMPLE=1
/// with `simctl launch`) shows a made-up Mac instead of a paired one: Groups
/// on it and on a linked computer, agents working, waiting and done, and
/// scheduled tasks with runs, for looking at the cockpit without a daemon.
/// Nothing is sent anywhere; actions fail as when disconnected.
enum DebugSample {
    static var enabled: Bool { ProcessInfo.processInfo.environment["HMK_DEBUG_SAMPLE"] == "1" }

    static let me = "us_sample_macbook"
    static let studio = "us_sample_studio"

    @MainActor
    static func model() -> AppModel {
        let m = AppModel(clientKind: "ios")
        m.showSample(state(now: Date()), hostName: "MacBook", userID: me)
        return m
    }

    static func state(now: Date) -> Hammock_V1_StateUpdate {
        let ms = { (d: Date) in Int64(d.timeIntervalSince1970 * 1000) }
        let ago = { (minutes: Double) in ms(now.addingTimeInterval(-minutes * 60)) }
        let cal = Calendar.current
        /// The day `days` from today at hh:mm.
        let at = { (days: Int, hour: Int, minute: Int) -> Int64 in
            let day = cal.date(byAdding: .day, value: days, to: now) ?? now
            return ms(cal.date(bySettingHour: hour, minute: minute, second: 0, of: day) ?? day)
        }

        func group(_ id: String, _ name: String, _ icon: String, order: Int32, tabs: [String], host: String = "") -> WorkspaceGroup {
            .with {
                $0.id = id; $0.name = name; $0.icon = icon; $0.order = order; $0.tabIds = tabs
                if !host.isEmpty { $0.hostUserID = host; $0.myRole = .owner }
            }
        }
        func tab(_ id: String, _ group: String, _ name: String, pane: String) -> WorkspaceTab {
            .with { $0.id = id; $0.groupID = group; $0.name = name; $0.layout = .with { $0.paneID = pane }; $0.focusedPaneID = pane }
        }
        func pane(_ id: String, _ tab: String, agent: AgentState? = nil, summary: String = "", since: Double = 5, seen: Bool = false,
                  schedule: String = "", run: String = "") -> Pane {
            .with {
                $0.id = id; $0.tabID = tab; $0.kind = .terminal; $0.cwd = "/Users/me/Developer/hammock"
                $0.cols = 80; $0.rows = 40
                $0.scheduleID = schedule; $0.scheduleRunID = run
                if let agent {
                    $0.agent = .with { $0.harness = "claude"; $0.state = agent; $0.summary = summary; $0.stateSinceUnixMs = ago(since); $0.seen = seen }
                }
            }
        }

        var u = Hammock_V1_StateUpdate()
        u.full = true
        u.computers = .with {
            $0.list = [
                .with { $0.userID = me; $0.name = "MacBook"; $0.model = "MacBook Pro"; $0.kind = "laptop"; $0.os = "macos"; $0.thisComputer = true; $0.online = true },
                .with { $0.userID = studio; $0.name = "Studio"; $0.model = "Mac Studio"; $0.kind = "studio"; $0.os = "macos"; $0.tint = "#7C3AED"; $0.online = true },
            ]
            $0.activeID = me
        }
        u.groups = [
            group("g_notes", "Notes", "🗞️", order: 0, tabs: ["t_brief", "t_inbox"]),
            group("g_hammock", "Hammock", "🌴", order: 1, tabs: ["t_triage", "t_polish", "t_shell", "t_backup"]),
            group("g_release", "Releases", "📦", order: 2, tabs: ["t_notes"], host: studio),
            // Its tabs all closed: what to open next.
            .with { $0.id = "g_units"; $0.name = "Units"; $0.icon = "⚖️"; $0.order = 3; $0.folder = "/Users/me/Developer/units" },
        ]
        u.tabs = [
            tab("t_brief", "g_notes", "Morning briefing", pane: "p_brief"),
            tab("t_inbox", "g_notes", "Inbox zero", pane: "p_inbox"),
            tab("t_triage", "g_hammock", "Triage new issues", pane: "p_triage"),
            tab("t_polish", "g_hammock", "Agent view polish", pane: "p_polish"),
            tab("t_shell", "g_hammock", "zsh", pane: "p_shell"),
            tab("t_backup", "g_hammock", "Back up dotfiles", pane: "p_backup"),
            tab("t_notes", "g_release", "Weekly release notes", pane: "p_notes"),
        ]
        u.panes = [
            pane("p_brief", "t_brief", agent: .idle, summary: "Wrote today’s briefing: 3 meetings, 2 threads waiting on you.", since: 95,
                 schedule: "s_brief", run: "r_brief_today"),
            pane("p_inbox", "t_inbox", agent: .idle, summary: "Archived 41 newsletters and drafted two replies.", since: 240, seen: true),
            pane("p_triage", "t_triage", agent: .needsInput, summary: "Bash: gh issue list --label needs-triage --json number,title", since: 3,
                 schedule: "s_triage", run: "r_triage_now"),
            pane("p_polish", "t_polish", agent: .working, summary: "Editing AgentViews.swift", since: 2),
            pane("p_shell", "t_shell"),
            pane("p_backup", "t_backup", schedule: "s_backup", run: "r_backup_y"),
            pane("p_notes", "t_notes", agent: .idle, summary: "Release notes for 1.8 are ready to review.", since: 50,
                 schedule: "s_notes", run: "r_notes_today"),
        ]
        // The Mac's theme (HMK_DEBUG_MAC_THEME=<name>, or custom), so the
        // phone's Same as Mac has one to follow.
        u.theme = .with { $0.name = ProcessInfo.processInfo.environment["HMK_DEBUG_MAC_THEME"] ?? "Tokyo Night" }
        u.attention = [
            .with { $0.id = "a_triage"; $0.paneID = "p_triage"; $0.state = .needsInput; $0.createdUnixMs = ago(3)
                $0.summary = "Bash: gh issue list --label needs-triage --json number,title" },
        ]

        func run(_ id: String, _ status: ScheduleRun.Status, started: Int64, minutes: Double, note: String = "", tab: String = "", pane: String = "",
                 output: String = "", closed: Bool = false, session: String = "", manual: Bool = false) -> ScheduleRun {
            .with {
                $0.id = id; $0.status = status; $0.dueUnixMs = manual ? 0 : started; $0.startedUnixMs = started
                if status != .running, status != .waiting, status != .missed { $0.endedUnixMs = started + Int64(minutes * 60_000) }
                if status == .missed { $0.startedUnixMs = 0 }
                $0.note = note; $0.tabID = tab; $0.paneID = pane; $0.output = output; $0.tabClosed = closed
                $0.sessionID = session; $0.manual = manual
            }
        }
        u.schedules = [
            .with {
                $0.id = "s_brief"; $0.name = "Morning briefing"; $0.groupID = "g_notes"; $0.groupName = "Notes"; $0.groupIcon = "🗞️"
                $0.kind = "claude"; $0.cwd = "/Users/me/Notes"; $0.weekdays = [1, 2, 3, 4, 5]; $0.minuteOfDay = 8 * 60 + 30
                $0.prompt = "Read my calendar, Slack and email since yesterday evening and write today’s briefing to briefing.html, then show it."
                $0.nextRunUnixMs = at(1, 8, 30); $0.computerID = me; $0.createdBy = "You"; $0.createdUnixMs = at(-20, 21, 4)
                $0.allowedTools = ["Bash(gh issue list:*)", "mcp__slack__read"]
                $0.runs = [
                    run("r_brief_today", .ready, started: ago(100), minutes: 4, note: "3 meetings today; the 1:1 with Sam moved to 15:00. 2 Slack threads need you.",
                        tab: "t_brief", pane: "p_brief", output: "/Users/me/Notes/briefing.html", session: "sess1"),
                    run("r_brief_y", .ready, started: at(-1, 8, 30), minutes: 5, note: "Quiet day: no meetings before noon.", tab: "t_old", closed: true, session: "sess0"),
                    run("r_brief_2", .missed, started: at(-2, 8, 30), minutes: 0, note: "The Mac was asleep"),
                    run("r_brief_3", .gaveUp, started: at(-3, 8, 30), minutes: 240, note: "Waited 4 hours for you to allow Bash", tab: "t_old2", closed: true, session: "sess-1"),
                ]
            },
            .with {
                $0.id = "s_triage"; $0.name = "Triage new issues"; $0.groupID = "g_hammock"; $0.groupName = "Hammock"; $0.groupIcon = "🌴"
                $0.kind = "claude"; $0.cwd = "/Users/me/Developer/hammock"; $0.minuteOfDay = 10 * 60
                $0.prompt = "Label and prioritise new GitHub issues."
                $0.nextRunUnixMs = at(1, 10, 0); $0.computerID = me
                $0.runs = [run("r_triage_now", .waiting, started: ago(6), minutes: 0, note: "Asking to run gh", tab: "t_triage", pane: "p_triage")]
            },
            .with {
                $0.id = "s_backup"; $0.name = "Back up dotfiles"; $0.groupID = "g_hammock"; $0.groupName = "Hammock"; $0.groupIcon = "🌴"
                $0.kind = "command"; $0.cwd = "/Users/me"; $0.minuteOfDay = 18 * 60
                $0.prompt = "rsync -a ~/.config/ /Volumes/Backup/config/"
                $0.nextRunUnixMs = ms(now.addingTimeInterval(3 * 3600)); $0.skipUntilUnixMs = ms(now.addingTimeInterval(3 * 3600 + 60))
                $0.computerID = me
                $0.runs = [run("r_backup_y", .ready, started: at(-1, 18, 0), minutes: 0.5, tab: "t_backup", pane: "p_backup")]
            },
            .with {
                $0.id = "s_notes"; $0.name = "Weekly release notes"; $0.groupID = "g_release"; $0.groupName = "Releases"; $0.groupIcon = "📦"
                $0.kind = "codex"; $0.cwd = "/Users/me/Developer/app"; $0.weekdays = [5]; $0.minuteOfDay = 16 * 60
                $0.prompt = "Write this week’s release notes from the merged PRs."
                $0.nextRunUnixMs = at(2, 16, 0); $0.computerID = studio
                $0.runs = [run("r_notes_today", .ready, started: ago(58), minutes: 7, note: "Release notes for 1.8: 12 fixes, 3 features.",
                               tab: "t_notes", pane: "p_notes", output: "https://github.com/homegrownsw/app/releases/new", manual: true)]
            },
            .with {
                $0.id = "s_caches"; $0.name = "Clear build caches"; $0.groupID = "g_hammock"; $0.groupName = "Hammock"; $0.groupIcon = "🌴"
                $0.kind = "command"; $0.cwd = "/Users/me/Developer/hammock"; $0.weekdays = [0]; $0.minuteOfDay = 3 * 60
                $0.prompt = "just clean"; $0.paused = true; $0.computerID = me
                $0.runs = [run("r_caches", .failed, started: at(-4, 3, 0), minutes: 1, note: "Exit code 1: no such recipe", tab: "t_c", closed: true)]
            },
        ]
        // HMK_DEBUG_HMK_KEY=1: the Mac's Hammock Key, this phone allowed to
        // approve it, and a request waiting (its sheet shows).
        if ProcessInfo.processInfo.environment["HMK_DEBUG_HMK_KEY"] == "1" {
            u.hammockKey = .with {
                $0.exists = true; $0.available = true; $0.fingerprint = "SHA256:q2Xh0f3m3w2a9cJk1Vt8nZs4yLr6pE5uB7dG0iH1oKc"
                $0.approvers = [.with { $0.deviceID = "sample"; $0.name = "iPhone" }]
            }
            u.hammockKeyRequests = [.with {
                $0.id = "fk_sample"; $0.what = "Sign for git (a commit or tag)"; $0.command = "git commit -m Fix the release notes"
                $0.paneID = "p_triage"; $0.paneLabel = "Hammock ▸ Triage"; $0.agent = false
                $0.headline = "A program in Hammock ▸ Triage: Sign for git (a commit or tag) (git commit -m Fix the release notes)"
                $0.dataHash = String(repeating: "ab", count: 32); $0.createdUnixMs = ago(0); $0.expiresUnixMs = ms(now.addingTimeInterval(120))
                $0.windowMs = 600_000
            }]
        }
        return u
    }

    /// Something in a sample pane's terminal (nothing comes from a Mac), once.
    @MainActor
    static func fill(_ s: PaneSession) {
        guard filled.insert(s.paneID).inserted else { return }
        let e = "\u{1B}["
        let text = "\(e)1;32mme@MacBook\(e)0m \(e)1;34m~/Developer/hammock\(e)0m $ git status --short\r\n"
            + " \(e)31mM\(e)0m apple/iOS/Hammock/SettingsScreen.swift\r\n"
            + " \(e)31mM\(e)0m internal/daemon/theme.go\r\n"
            + "\(e)32m??\(e)0m apple/iOS/Hammock/PhoneTheme.swift\r\n"
            + "\(e)1;32mme@MacBook\(e)0m \(e)1;34m~/Developer/hammock\(e)0m $ go test ./internal/...\r\n"
            + "ok  \tinternal/daemon\t\(e)33m12.4s\(e)0m\r\n"
            + "ok  \tinternal/hammockconfig\t\(e)33m0.2s\(e)0m\r\n"
            + "\(e)35mPASS\(e)0m \(e)36m(cached)\(e)0m\r\n"
            + "\(e)1;32mme@MacBook\(e)0m \(e)1;34m~/Developer/hammock\(e)0m $ "
        s.engine.write(Data(text.utf8))
    }
    @MainActor private static var filled: Set<String> = []
}
#endif
