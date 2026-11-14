import AppKit
import HammockKit
import Observation
import SwiftUI

/// File ▸ New Agent Tab…: the Start an Agent sheet, for a Group; and, with
/// a default agent (Settings ▸ Agents), New Claude Code Tab beside it,
/// which starts that one without asking.
@MainActor
@Observable
final class AgentLauncher {
    static let shared = AgentLauncher()

    struct Request: Identifiable {
        let id = UUID()
        var groupID: String?
        var computerID: String?
        var harness: String?
        /// Beside a pane (a split menu), rather than in a new tab.
        var split: AgentSplit?
    }

    var request: Request?

    /// In the Group you're in (or groupID), with that agent picked (nil: the
    /// computer's default agent, else Claude Code).
    func open(_ model: AppModel, groupID: String? = nil, computerID: String? = nil, harness: String? = nil) {
        if let computerID {
            request = Request(groupID: mine(model, on: computerID), computerID: computerID, harness: harness)
            return
        }
        request = Request(groupID: groupID ?? current(model), harness: harness)
    }

    /// The default agent New Agent Tab would start in the Group you're in (or
    /// groupID), or on a computer ("": this one): nil, none there.
    func defaultAgent(_ model: AppModel, groupID: String? = nil, computerID: String? = nil) -> String? {
        if let computerID { return model.defaultAgent(onComputer: computerID) }
        let id = groupID ?? current(model)
        return model.defaultAgent(in: id.flatMap { id in model.groups.first { $0.id == id } })
    }

    /// The default agent, started without the sheet, where open would start
    /// it; the sheet if there's none there after all.
    func newDefault(_ model: AppModel, groupID: String? = nil, computerID: String? = nil) {
        guard defaultAgent(model, groupID: groupID, computerID: computerID) != nil else {
            open(model, groupID: groupID, computerID: computerID)
            return
        }
        let group = computerID.map { mine(model, on: $0) } ?? (groupID ?? current(model))
        Task { await model.startDefaultAgent(groupID: group, computerID: computerID) }
    }

    /// The sheet, starting it beside a pane (a split menu's Agent…).
    func open(_ model: AppModel, beside split: AgentSplit) {
        request = Request(split: split)
    }

    private func current(_ model: AppModel) -> String? { model.currentGroup?.id ?? model.shownGroups.first?.id }

    /// On a computer: one of its Groups (the one you're in, if it's there).
    private func mine(_ model: AppModel, on computerID: String) -> String? {
        model.currentGroup.flatMap { g in
            (computerID.isEmpty ? !g.isOnLinkedComputer : g.hostUserID == computerID) ? g.id : nil
        }
    }
}

/// New Claude Code Tab (when the Group's computer has a default agent) and
/// New Agent Tab…, for a menu: the File menu, a Group's, a computer's.
struct NewAgentTabItems: View {
    let model: AppModel
    var groupID: String?
    /// A computer's menu ("": this one): "… on Studio".
    var computerID: String?
    var computerName: String?
    /// Short titles, under a "New Tab" submenu: "Claude Code Tab", "Agent Tab…".
    var short = false
    /// The shortcuts (the File menu's, and New Tab's submenus).
    var shortcuts = true

    var body: some View {
        let launcher = AgentLauncher.shared
        let harness = launcher.defaultAgent(model, groupID: groupID, computerID: computerID)
        if let harness {
            Button(title(AppModel.defaultAgentTitle(harness, on: computerName))) {
                launcher.newDefault(model, groupID: groupID, computerID: computerID)
            }
            .keyboardShortcut(shortcuts ? Keys.newDefaultAgentTab : nil)
        }
        Button(title("New Agent Tab" + (computerName.map { " on \($0)" } ?? "") + "…")) {
            launcher.open(model, groupID: groupID, computerID: computerID)
        }
        .keyboardShortcut(shortcuts ? Keys.newAgentTab(harness != nil) : nil)
    }

    private func title(_ s: String) -> String { short ? String(s.dropFirst("New ".count)) : s }
}

struct StartAgentSheet: View {
    let model: AppModel
    let request: AgentLauncher.Request
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        StartAgentView(model: model, groupID: request.groupID, computerID: request.computerID, harness: request.harness, split: request.split, chooseLocal: {
            let panel = NSOpenPanel()
            panel.canChooseFiles = false
            panel.canChooseDirectories = true
            panel.allowsMultipleSelection = false
            panel.prompt = "Choose"
            return panel.runModal() == .OK ? panel.url?.path : nil
        }) { _ in dismiss() }
            .frame(width: 520, height: 600)
    }
}
