import AppKit
import HammockKit
import Observation
import SwiftUI

/// File ▸ New Agent Tab…: the Start an Agent sheet, for a Group.
@MainActor
@Observable
final class AgentLauncher {
    static let shared = AgentLauncher()

    struct Request: Identifiable {
        let id = UUID()
        var groupID: String?
        var computerID: String?
        var harness = "claude"
    }

    var request: Request?

    /// In the Group you're in (or groupID), with that agent picked.
    func open(_ model: AppModel, groupID: String? = nil, computerID: String? = nil, harness: String = "claude") {
        if let computerID {
            // On a computer: one of its Groups (the one you're in, if it's there).
            let mine = model.currentGroup.flatMap { g in
                (computerID.isEmpty ? !g.isOnLinkedComputer : g.hostUserID == computerID) ? g.id : nil
            }
            request = Request(groupID: mine, computerID: computerID, harness: harness)
            return
        }
        request = Request(groupID: groupID ?? model.currentGroup?.id ?? model.shownGroups.first?.id, harness: harness)
    }
}

struct StartAgentSheet: View {
    let model: AppModel
    let request: AgentLauncher.Request
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        StartAgentView(model: model, groupID: request.groupID, computerID: request.computerID, harness: request.harness, chooseLocal: {
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
