import Foundation

/// Each computer's default agent (default-agent in its config file, set in
/// its Settings ▸ Agents): New Claude Code Tab starts it without the Start
/// an Agent sheet, beside New Agent Tab…, which still asks. Its daemon says
/// which in its own record (Computer.defaultAgent), only while it's
/// installed there, so the Mac, the iPhone and the TUI all follow the
/// computer a Group is on.
extension AppModel {
    /// The default agent of the computer a Group is on (nil: the current
    /// one's): this computer's for a Group here, a linked one's for a Group
    /// there; none for a Group someone shared with you.
    public func defaultAgent(in group: WorkspaceGroup?) -> String? {
        if let group {
            if group.isOnLinkedComputer { return defaultAgent(onComputer: group.hostUserID) }
            if group.isRemote { return nil }
        }
        return defaultAgent(onComputer: "")
    }

    /// A computer's default agent ("": this one), if it has one.
    public func defaultAgent(onComputer id: String) -> String? {
        let c = id.isEmpty || id == myUserID ? thisComputer : computer(id)
        guard let a = c?.defaultAgent, !a.isEmpty else { return nil }
        return a
    }

    /// The default agent's own menu item: "New Claude Code Tab".
    public static func defaultAgentTitle(_ harness: String, on computer: String? = nil) -> String {
        "New \(AgentCatalog.name(of: harness)) Tab" + (computer.map { " on \($0)" } ?? "")
    }

    /// Starts the default agent without asking: in a new tab in groupID (or,
    /// with computerID, in the Group there the Start an Agent sheet would
    /// pick), in the folder the sheet would suggest: the Group's own, else
    /// home. The new tab's id (nil: no default agent there, or it failed).
    @discardableResult
    public func startDefaultAgent(groupID: String?, computerID: String? = nil) async -> String? {
        let (gid, cid) = StartAgentView.placement(self, groupID: groupID, computerID: computerID)
        let harness = gid.isEmpty ? defaultAgent(onComputer: cid) : defaultAgent(in: groups.first { $0.id == gid })
        guard let harness else { return nil }
        let list = await listFolders(groupID: gid, computerID: cid)
        let cwd = list?.suggestions.first { $0.why == "This Group" }?.path ?? "~"
        return await startAgent(harness, cwd: cwd, groupID: gid, computerID: cid)
    }
}
