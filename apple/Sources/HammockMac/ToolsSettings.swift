import HammockKit
import SwiftUI

/// Hammock's tools besides terminals and agents: what a pane can show,
/// scheduled tasks, and agents' session search. Each can be turned off
/// (Settings ▸ Tools, the `<tool>-enabled` keys, hammockconfig.Tools), for
/// someone who already has their own way of doing that (another diff
/// reviewer or scheduler, say): off, it isn't offered anywhere in Hammock's
/// menus, the command palette or shortcuts, and agents lose its tools (hmk
/// mcp leaves them out; hmkd refuses a session that still has them). What's
/// open already stays open; schedules don't run. Session Search is the
/// agents' alone: the session finder stays.
enum HammockTool: String, CaseIterable, Identifiable {
    case browser, simulator, editor, diff, files, log, schedules, sessions

    var id: String { rawValue }

    var pref: Pref { Pref("\(rawValue)-enabled", defaults: "\(rawValue)Enabled", true) }

    /// A kind of pane (not scheduled tasks or session search).
    var isPane: Bool { self != .schedules && self != .sessions }

    /// Turned on (the default).
    @MainActor var on: Bool { Prefs.shared.bool(pref) }

    var title: String {
        switch self {
        case .browser: "Browser"
        case .simulator: "Simulator"
        case .editor: "Editor"
        case .diff: "Diff Viewer"
        case .files: "Files"
        case .log: "Log Viewer"
        case .schedules: "Automations"
        case .sessions: "Session Search"
        }
    }

    var symbol: String {
        switch self {
        case .browser: "globe"
        case .simulator: "iphone"
        case .editor: "doc.text"
        case .diff: "plus.forwardslash.minus"
        case .files: "folder"
        case .log: "list.bullet.rectangle"
        case .schedules: "clock"
        case .sessions: "magnifyingglass"
        }
    }

    var tint: Color {
        switch self {
        case .browser: .blue
        case .simulator: .gray
        case .editor: .indigo
        case .diff: .green
        case .files: .cyan
        case .log: .orange
        case .schedules: .purple
        case .sessions: .teal
        }
    }

    /// What it is, and what agents do with it.
    var blurb: String {
        switch self {
        case .browser: "Web pages beside your terminals, like your dev server. Agents can open, read and click through them, and show you their output there."
        case .simulator: "iOS simulators in a tab or beside your code. Agents can open them, look, tap and type, and install and launch apps."
        case .editor: "Text files you can edit in Hammock. Agents can open one and point you at lines in it."
        case .diff: "Review changes like a pull request's Files Changed, and comment on them. Agents can open one to show you their work, and answer your comments."
        case .files: "A folder's files as a tree, like an editor's file explorer, to open in other panes. Agents can open one and point you at files in it."
        case .log: "Logs from a file, the Mac, a simulator or a command, as entries you can filter. Agents can open and read them."
        case .schedules: "Agents or commands run on a schedule or when something happens, each run a new tab. Agents can create and run them, with your say-so. Off, none run."
        case .sessions: "Agents can search and read past Claude Code and Codex sessions on this Mac, to pick up earlier decisions. You can still find and resume them."
        }
    }

    /// The settings search's words for it.
    var keywords: String {
        switch self {
        case .browser: "web chromium page browser_open mcp turn off disable"
        case .simulator: "ios iphone xcode sim_open mcp turn off disable"
        case .editor: "text file code editor_open mcp turn off disable"
        case .diff: "git review changes pull request diff_open comments mcp turn off disable"
        case .files: "file browser explorer tree folder finder files_open mcp turn off disable"
        case .log: "logs console logcat log_open mcp turn off disable"
        case .schedules: "automations scheduled tasks schedule triggers cron launchd recurring automation_save schedule_save mcp turn off disable"
        case .sessions: "past sessions history transcripts memory session_search mcp turn off disable"
        }
    }
}

struct ToolsSettings: View {
    var body: some View {
        Form {
            SettingsPaneHeader(tab: .tools)
            // Kinds of pane, then the rest.
            ForEach([HammockTool.allCases.filter(\.isPane), HammockTool.allCases.filter { !$0.isPane }], id: \.self) { tools in
                Section {
                    ForEach(tools) { t in
                        ToolRow(tool: t).settingAnchor("tools.\(t.rawValue)")
                    }
                }
            }
            Section("Agents") {
                Toggle("Show what agents do in browsers and simulators", isOn: Prefs.shared.boolBinding(.agentPresence))
                    .settingAnchor("tools.agentpresence")
            }
            Section("Automations") {
                WorkingHoursRow().settingAnchor("tools.workinghours")
            }
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
    }
}

/// Working hours (the config file's working-hours, "[DAYS ]HH:MM-HH:MM"):
/// when automations' Free Time trigger may fire.
private struct WorkingHoursRow: View {
    private var value: Binding<String> { Prefs.shared.stringBinding(.workingHours) }

    /// Its parts: days as written ("mon-fri"), and from and to (minutes).
    private var parts: (days: String, from: Int, to: Int) {
        let v = value.wrappedValue.trimmingCharacters(in: .whitespaces)
        var days = "mon-fri", hours = v
        if let sp = v.lastIndex(of: " ") {
            days = String(v[..<sp]).trimmingCharacters(in: .whitespaces)
            hours = String(v[v.index(after: sp)...])
        }
        let hm = hours.split(separator: "-").map { p -> Int? in
            let c = p.split(separator: ":").compactMap { Int($0) }
            return c.count == 2 ? c[0] * 60 + c[1] : nil
        }
        guard hm.count == 2, let f = hm[0], let t = hm[1] else { return (days, 9 * 60, 17 * 60) }
        return (days, f, t)
    }

    private func set(days: String? = nil, from: Int? = nil, to: Int? = nil) {
        let p = parts
        let f = from ?? p.from, t = to ?? p.to
        value.wrappedValue = "\(days ?? p.days) " + String(format: "%02d:%02d-%02d:%02d", f / 60, f % 60, t / 60, t % 60)
    }

    private func time(_ m: Int, _ set: @escaping (Int) -> Void) -> Binding<Date> {
        Binding(get: { Calendar.current.date(bySettingHour: m / 60, minute: m % 60, second: 0, of: Date()) ?? Date() },
                set: { d in
                    let c = Calendar.current.dateComponents([.hour, .minute], from: d)
                    set((c.hour ?? 0) * 60 + (c.minute ?? 0))
                })
    }

    var body: some View {
        LabeledContent("Working hours") {
            HStack {
                Picker("Days", selection: Binding(get: { parts.days }, set: { set(days: $0) })) {
                    Text("Weekdays").tag("mon-fri")
                    Text("Every day").tag("daily")
                    if !["mon-fri", "daily"].contains(parts.days) { Text(parts.days).tag(parts.days) }
                }
                .labelsHidden()
                .fixedSize()
                DatePicker("From", selection: time(parts.from) { set(from: $0) }, displayedComponents: .hourAndMinute)
                    .labelsHidden()
                Text("to")
                DatePicker("To", selection: time(parts.to) { set(to: $0) }, displayedComponents: .hourAndMinute)
                    .labelsHidden()
            }
        }
    }
}

private struct ToolRow: View {
    let tool: HammockTool

    var body: some View {
        Toggle(isOn: Prefs.shared.boolBinding(tool.pref)) {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: tool.symbol)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(.white)
                    .frame(width: 26, height: 26)
                    .background(RoundedRectangle(cornerRadius: 6, style: .continuous).fill(tool.tint.gradient))
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 2) {
                    Text(tool.title)
                    Text(tool.blurb)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        .padding(.vertical, 2)
    }
}
