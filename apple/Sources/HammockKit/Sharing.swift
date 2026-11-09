import Foundation

// Multiplayer (ADR-0008): sharing Groups from this Mac, and joining Groups
// others share.

extension WorkspaceGroup {
    /// Shared with you from another Mac.
    public var isRemote: Bool { !hostUserID.isEmpty }
    /// Shared by you with someone.
    public var isShared: Bool { !isRemote && members.contains { $0.role == .viewer || $0.role == .driver } }
    /// You may type here (your own Groups, your other computers', or as a driver).
    public var canDrive: Bool { !isRemote || myRole == .driver || myRole == .owner }
    /// Whether a tab can be made here: your own Groups, or a shared one you
    /// drive while its Mac is online.
    public var canAddTabs: Bool { canDrive && !offline }
}

extension Person {
    /// "Sam Patel" → "SP".
    public var initials: String {
        let parts = name.split(separator: " ").prefix(2)
        return parts.map { String($0.prefix(1)) }.joined().uppercased()
    }
    /// A steady hue per person (0…1), from their id.
    public var hue: Double {
        Double(userID.unicodeScalars.reduce(UInt32(7)) { ($0 &* 31) &+ $1.value } % 360) / 360
    }
}

extension Role {
    /// What letting someone drive really allows, for tooltips beside the choice.
    public static let driverMeans = "They can type into this Group's panes and open tabs in it: running any command as you on this computer."

    public var label: String {
        switch self {
        case .viewer: return "Can view"
        case .driver: return "Can drive"
        case .host: return "Host"
        case .owner: return "Your computer"
        default: return ""
        }
    }
}

extension AppModel {
    public var me: Person? { people[myUserID] }

    /// The people in a Group (you first when it's yours, the host first when
    /// it's shared with you) with where they are.
    public func members(of group: WorkspaceGroup) -> [(Member, Person)] {
        group.members.compactMap { m in people[m.userID].map { (m, $0) } }
    }

    /// Who (other than you) is in a tab right now.
    public func presence(in tab: WorkspaceTab) -> [(Member, Person)] {
        guard let g = groups.first(where: { $0.id == tab.groupID }) else { return [] }
        return members(of: g).filter { $0.0.online && $0.0.tabID == tab.id && $0.0.userID != myUserID }
    }

    /// Who (other than you) is in a pane, and whether they're typing.
    public func presence(inPane id: String) -> [(Member, Person)] {
        guard let p = panes[id], let t = tabs[p.tabID] else { return [] }
        return presence(in: t).filter { $0.0.paneID == id }
    }

    public func group(ofPane id: String) -> WorkspaceGroup? {
        panes[id].flatMap { tabs[$0.tabID] }.flatMap { group(of: $0) }
    }

    /// The Mac a shared Group runs on, as "Sam's Mac" (its owner and device).
    public func hostLabel(of group: WorkspaceGroup) -> String {
        if let c = computer(of: group) { return c.name }
        guard let p = people[group.hostUserID] else { return "Another Mac" }
        return p.deviceName.isEmpty ? p.name : "\(p.name) · \(p.deviceName)"
    }

    /// Groups you're sharing, and how many guests are here now.
    public var sharing: (groups: Int, online: Int) {
        let shared = groups.filter(\.isShared)
        let online = Set(shared.flatMap { $0.members.filter { $0.online && $0.role != .host }.map(\.userID) })
        return (shared.count, online.count)
    }

    /// Moves a Group (yours or shared with you) to a position in your sidebar.
    public func moveGroup(_ id: String, to index: Int) {
        // At once here, so the drop lands without waiting for the daemon.
        if let from = groups.firstIndex(where: { $0.id == id }) {
            var g = groups
            let moved = g.remove(at: from)
            g.insert(moved, at: min(max(0, index), g.count))
            groups = g
        }
        conn?.send { $0.moveGroup = .with { $0.groupID = id; $0.index = Int32(index) } }
    }

    // MARK: Hosting

    /// The Group's invite link (made on first use; regenerate replaces it).
    public func inviteLink(_ groupID: String, regenerate: Bool = false) async throws -> String {
        guard let conn = connection else { throw DaemonError.closed }
        return try await conn.call { $0.createInvite = .with { $0.groupID = groupID; $0.regenerate = regenerate } }.invite.link
    }

    public func revokeInvite(_ groupID: String) async {
        _ = await run { $0.revokeInvite = .with { $0.groupID = groupID } }
    }

    /// Lets someone in (as a viewer or driver), or declines (nil).
    public func respond(to request: JoinRequest, role: Role?) {
        joinRequests.removeAll { $0.id == request.id }
        Task { _ = await run { $0.respondJoin = .with { $0.id = request.id; $0.role = role ?? .unspecified } } }
    }

    /// Changes someone's role, or removes them (nil).
    public func setRole(_ role: Role?, of userID: String, in groupID: String) async {
        _ = await run { $0.setMember = .with { $0.groupID = groupID; $0.userID = userID; $0.role = role ?? .unspecified } }
    }

    /// Stops sharing a Group, or with nil everything (everyone is disconnected).
    public func stopSharing(_ groupID: String?) async {
        _ = await run { $0.stopSharing = .with { $0.groupID = groupID ?? "" } }
    }

    /// Your name and picture; with deviceName, this Mac's name as others see it
    /// too ("" for the Mac's own).
    public func setProfile(name: String, avatar: Data?, deviceName: String? = nil) async {
        _ = await run {
            $0.setProfile = .with {
                $0.name = name
                $0.avatar = avatar ?? Data()
                if let deviceName { $0.deviceName = deviceName }
            }
        }
    }

    // MARK: Joining

    /// Joins from an invite link; returns once the host has answered (the
    /// Group's id), or throws (declined, unreachable, a bad link).
    public func join(link: String) async throws -> String {
        guard let conn = connection else { throw DaemonError.closed }
        return try await conn.call { $0.joinShared = .with { $0.link = link } }.created.id
    }

    public func leave(_ groupID: String) async {
        _ = await run { $0.leaveShared = .with { $0.groupID = groupID } }
    }

    /// Sends a dropped file to the pane's Mac, which saves it and types its path.
    public func upload(_ data: Data, name: String, to paneID: String) async {
        _ = await run { $0.uploadFile = .with { $0.paneID = paneID; $0.name = name; $0.data = data } }
    }
}

/// An invite link's contents, for showing who's inviting you before joining.
public struct InvitePreview: Sendable {
    public let hostName: String
    public let groupName: String

    public init?(link: String) {
        guard link.hasPrefix("hammock://join/") else { return nil }
        var s = String(link.dropFirst("hammock://join/".count)).replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        while s.count % 4 != 0 { s += "=" }
        guard let data = Data(base64Encoded: s), let t = try? Hammock_V1_InviteToken(serializedBytes: data) else { return nil }
        hostName = t.hostName.replacingOccurrences(of: ".local", with: "")
        groupName = t.groupName
    }
}

extension AppModel {
    /// A paired phone asks this Mac to push to it while you're away (an
    /// empty relay id stops it).
    public func registerPush(relayURL: String, relayID: String, key: Data, events: [String]) async throws {
        guard let conn = connection else { throw DaemonError.closed }
        try await conn.call { $0.registerPush = .with { $0.relayURL = relayURL; $0.relayID = relayID; $0.key = key; $0.events = events } }
    }
}
