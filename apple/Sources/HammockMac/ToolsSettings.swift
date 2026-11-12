import HammockKit
import SwiftUI

/// Hammock's tools besides terminals and agents: what a pane can show. Each
/// can be turned off (Settings ▸ Tools, the `<kind>-enabled` keys), for
/// someone who already has their own way of doing that (another diff
/// reviewer, say): off, it isn't offered anywhere in Hammock's menus, the
/// command palette or shortcuts, and agents lose its tools (hmk mcp leaves
/// them out; hmkd refuses a session that still has them). What's open
/// already stays open.
enum HammockTool: String, CaseIterable, Identifiable {
    case browser, simulator, editor, diff, log

    var id: String { rawValue }

    var pref: Pref { Pref("\(rawValue)-enabled", defaults: "\(rawValue)Enabled", true) }

    /// Turned on (the default).
    @MainActor var on: Bool { Prefs.shared.bool(pref) }

    var title: String {
        switch self {
        case .browser: "Browser"
        case .simulator: "Simulator"
        case .editor: "Editor"
        case .diff: "Diff Viewer"
        case .log: "Log Viewer"
        }
    }

    var symbol: String {
        switch self {
        case .browser: "globe"
        case .simulator: "iphone"
        case .editor: "doc.text"
        case .diff: "plus.forwardslash.minus"
        case .log: "list.bullet.rectangle"
        }
    }

    var tint: Color {
        switch self {
        case .browser: .blue
        case .simulator: .gray
        case .editor: .indigo
        case .diff: .green
        case .log: .orange
        }
    }

    /// What it is, and what agents do with it.
    var blurb: String {
        switch self {
        case .browser: "Web pages beside your terminals, like your dev server. Agents can open, read and click through them, and show you their output there."
        case .simulator: "iOS simulators in a tab or beside your code. Agents can open them, look, tap and type, and install and launch apps."
        case .editor: "Text files you can edit in Hammock. Agents can open one and point you at lines in it."
        case .diff: "Review changes like a pull request's Files Changed, and comment on them. Agents can open one to show you their work, and answer your comments."
        case .log: "Logs from a file, the Mac, a simulator or a command, as entries you can filter. Agents can open and read them."
        }
    }

    /// The settings search's words for it.
    var keywords: String {
        switch self {
        case .browser: "web chromium page browser_open mcp turn off disable"
        case .simulator: "ios iphone xcode sim_open mcp turn off disable"
        case .editor: "text file code editor_open mcp turn off disable"
        case .diff: "git review changes pull request diff_open comments mcp turn off disable"
        case .log: "logs console logcat log_open mcp turn off disable"
        }
    }
}

struct ToolsSettings: View {
    var body: some View {
        Form {
            SettingsPaneHeader(tab: .tools)
            Section {
                ForEach(HammockTool.allCases) { t in
                    ToolRow(tool: t).settingAnchor("tools.\(t.rawValue)")
                }
            }
            Section("Agents") {
                Toggle("Show what agents do in browsers and simulators", isOn: Prefs.shared.boolBinding(.agentPresence))
                    .settingAnchor("tools.agentpresence")
            }
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
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
