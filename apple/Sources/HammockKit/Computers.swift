import Foundation
import SwiftUI
#if os(macOS)
import AppKit
#else
import UIKit
#endif

// Linked computers (ADR-0013): your own computers, each showing every
// other's Groups. A linked computer's Groups carry its badge (its icon, as
// picked, or its kind's symbol, in its colour); this one's don't.

extension Computer {
    /// Its SF Symbol: the one picked as its icon, if this OS has it, else
    /// its kind's (also when its icon is an emoji: see `emoji`).
    public var symbol: String {
        if !icon.isEmpty, icon.allSatisfy(\.isASCII), Self.symbolExists(icon) { return icon }
        return Self.symbol(kind: kind, os: os)
    }

    /// Its icon, when an emoji was picked (an SF Symbol's name is ASCII).
    public var emoji: String? { icon.isEmpty || icon.allSatisfy(\.isASCII) ? nil : icon }

    /// Whether this OS has the SF Symbol (one picked on a newer one may not).
    public static func symbolExists(_ name: String) -> Bool {
        if let known = symbolCache.object(forKey: name as NSString) { return known.boolValue }
        #if os(macOS)
        let ok = NSImage(systemSymbolName: name, accessibilityDescription: nil) != nil
        #else
        let ok = UIImage(systemName: name) != nil
        #endif
        symbolCache.setObject(NSNumber(value: ok), forKey: name as NSString)
        return ok
    }
    private static let symbolCache = NSCache<NSString, NSNumber>()

    /// The SF Symbol for a kind of computer (Computer.kind, .os).
    public static func symbol(kind: String, os: String = "") -> String {
        switch kind {
        case "laptop": return "laptopcomputer"
        case "studio": return "macstudio"
        case "mini": return "macmini"
        case "tower": return "macpro.gen3"
        case "server": return "server.rack"
        default: return os == "linux" ? "server.rack" : "desktopcomputer"
        }
    }

    /// Not linked until it's linked again: it doesn't link with this one
    /// any more, or waits for its code to be checked.
    public var needsLinking: Bool { refused || suspect }

    /// Its link needs you: Link Again (and, unless only its certificate
    /// changed, Remove).
    public var needsAttention: Bool { refused || suspect || certChanged }

    /// Its badge colour (#RRGGBB), else the text's: picked to have none
    /// ("none", the Default swatch), or not given one yet.
    public var color: Color { Color(hex: tint) ?? .primary }

    /// It has no colour of its own (drawn in the text's).
    public var isNeutral: Bool { Color(hex: tint) == nil }

    /// "Mac Studio · online", "Asleep · last seen 14:02".
    public var status: String {
        if thisComputer { return model.isEmpty ? "This computer" : "This \(model)" }
        let kind = model.isEmpty ? (os == "linux" ? "Linux" : "Mac") : model
        // It unlinked this one, or was set up again: Link Again, or Remove.
        if refused { return "\(kind) · doesn't link with this computer any more" }
        // Introduced by a computer after that one was unlinked.
        if suspect { return "\(kind) · introduced by a computer since unlinked: check its code" }
        if certChanged { return "\(kind) · its certificate changed: check its code" }
        if online { return "\(kind) · online" }
        guard lastSeenUnixMs > 0 else { return "\(kind) · offline" }
        let seen = Date(timeIntervalSince1970: Double(lastSeenUnixMs) / 1000)
        let f = Calendar.current.isDateInToday(seen) ? seen.formatted(date: .omitted, time: .shortened) : seen.formatted(date: .abbreviated, time: .shortened)
        return "Asleep · last seen \(f)"
    }
}

extension Color {
    /// A colour from "#RRGGBB".
    public init?(hex: String) {
        guard hex.count == 7, hex.hasPrefix("#"), let v = UInt32(hex.dropFirst(), radix: 16) else { return nil }
        self.init(red: Double((v >> 16) & 0xFF) / 255, green: Double((v >> 8) & 0xFF) / 255, blue: Double(v & 0xFF) / 255)
    }
}

/// The colours offered for a computer's badge: none (neutral, Default) first.
/// A computer's colour when it's picked to have none (several may).
public let neutralTint = "none"

public let computerTints = [neutralTint, "#2563EB", "#7C3AED", "#15803D", "#C2410C", "#DB2777", "#0891B2", "#A16207", "#4F46E5"]

extension WorkspaceGroup {
    /// On one of your other computers (linked), rather than shared with you.
    public var isOnLinkedComputer: Bool { isRemote && myRole == .owner }
    /// Its sessions can be resumed from it ("Resume a Session…"): it's yours,
    /// here or on a linked computer that's reachable; not one shared with you.
    public var canResumeSessions: Bool { !isRemote || (isOnLinkedComputer && !offline) }
}

extension AppModel {
    /// This computer.
    public var thisComputer: Computer? { computers.first { $0.thisComputer } }
    /// Your other computers, linked with this one.
    public var linkedComputers: [Computer] { computers.filter { !$0.thisComputer } }
    /// Computers whose screens you can open (screen sharing): your linked
    /// Macs that are online, and the one a phone connects to. A Mac shows
    /// its own only in the dev build (Hammock β) or with HMK_OWN_SCREEN=1, to
    /// try it out on one Mac.
    public var screenComputers: [Computer] {
        computers.filter { c in
            if c.thisComputer { return Self.showsOwnScreen }
            return c.online && !c.needsLinking && c.os != "linux"
        }
    }
    #if os(iOS)
    static let showsOwnScreen = true
    #else
    static let showsOwnScreen = Bundle.main.bundleIdentifier?.hasSuffix(".beta") == true
        || ProcessInfo.processInfo.environment["HMK_OWN_SCREEN"] == "1"
    #endif

    /// Whether any computers are linked (the switcher and badges show).
    public var hasLinkedComputers: Bool { computers.contains { !$0.thisComputer && !$0.needsLinking } }

    public func computer(_ userID: String) -> Computer? { computers.first { $0.userID == userID } }

    /// The linked computer a Group is on (nil: this one, or a Group someone shared).
    public func computer(of group: WorkspaceGroup) -> Computer? {
        group.isOnLinkedComputer ? computer(group.hostUserID) : nil
    }

    /// The Groups the sidebar shows: all of them, or the chosen computer's.
    public var shownGroups: [WorkspaceGroup] {
        guard !computerFilter.isEmpty, hasLinkedComputers else { return groups }
        if computerFilter == myUserID { return groups.filter { !$0.isOnLinkedComputer } }
        return groups.filter { $0.isOnLinkedComputer && $0.hostUserID == computerFilter }
    }

    /// Whether this computer should post notifications: it's the one you
    /// used last, or nothing's linked.
    public var notifiesHere: Bool { !hasLinkedComputers || activeComputerID.isEmpty || activeComputerID == myUserID }

    /// The computers on your tailnet, as Tailscale lists them for your user.
    public func discoverComputers() async throws -> Hammock_V1_DiscoveredComputers {
        guard let conn = connection else { throw DaemonError.closed }
        return try await conn.call { $0.discoverComputers = .with { _ in } }.discoveredComputers
    }

    /// Links with a computer (an address from discovery, or a hammock://link/
    /// link). Returns once linked; throws if not (declined, codes differ).
    public func linkComputer(_ address: String) async throws {
        guard let conn = connection else { throw DaemonError.closed }
        try await conn.call { $0.linkComputer = .with { $0.address = address } }
    }

    /// Answers a link request: the codes match, or not.
    public func respondLink(_ id: String, accept: Bool) {
        if !accept { linkRequests.removeAll { $0.id == id } }
        Task { _ = await run { $0.respondLink = .with { $0.id = id; $0.accept = accept } } }
    }

    /// Unlinks a computer from all of yours (this one's id: from the
    /// others); forget: only here (Remove), nobody asked, its key not refused.
    public func unlinkComputer(_ userID: String, forget: Bool = false) async {
        if computerFilter == userID { computerFilter = "" }
        _ = await run { $0.unlinkComputer = .with { $0.userID = userID; $0.forget = forget } }
    }

    public func setTint(_ tint: String, of userID: String) async {
        if let e = await setComputerLook(userID, tint: tint) { showToast("Couldn’t change the colour: \(e)") }
    }

    /// Sets a computer's colour ("": kept) and, if given, its icon (an SF
    /// Symbol's name or an emoji; "": its kind's symbol), on all your
    /// computers. Returns why not, if it wasn't (e.g. another has that
    /// colour, or it's offline).
    @discardableResult
    public func setComputerLook(_ userID: String, tint: String = "", icon: String? = nil) async -> String? {
        guard let conn = connection else { return "Not connected" }
        do {
            _ = try await conn.call { $0.setComputerTint = .with { m in
                m.userID = userID
                m.tint = tint
                if let icon { m.icon = icon }
            } }
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    /// The other computer (linked) that has this colour: each has its own.
    public func tintOwner(_ hex: String, except userID: String) -> Computer? {
        guard hex != neutralTint else { return nil } // several may have none
        return computers.first { $0.userID != userID && !$0.needsLinking && $0.tint.caseInsensitiveCompare(hex) == .orderedSame }
    }

    /// A hammock://link/ link to this computer, for one that can't find it.
    public func linkInvite() async throws -> String {
        guard let conn = connection else { throw DaemonError.closed }
        return try await conn.call { $0.createLinkInvite = .with { _ in } }.invite.link
    }
}

/// A computer's icon: the emoji picked for it, else its symbol in its
/// colour (or color, given).
public struct ComputerIcon: View {
    let computer: Computer
    var size: CGFloat
    var weight: Font.Weight
    var color: Color?

    public init(_ computer: Computer, size: CGFloat = 14, weight: Font.Weight = .medium, color: Color? = nil) {
        self.computer = computer
        self.size = size
        self.weight = weight
        self.color = color
    }

    public var body: some View {
        if let e = computer.emoji {
            Text(e).font(.system(size: size * 0.92))
        } else {
            Image(systemName: computer.symbol)
                .font(.system(size: size, weight: weight))
                .foregroundStyle(color ?? computer.color)
        }
    }
}

/// A linked computer's badge: its icon in its colour, with its name and
/// state as help.
public struct ComputerBadge: View {
    let computer: Computer
    var size: CGFloat

    public init(_ computer: Computer, size: CGFloat = 14) {
        self.computer = computer
        self.size = size
    }

    public var body: some View {
        ComputerIcon(computer, size: size)
            .help("On \(computer.name) · \(computer.status)")
            .accessibilityLabel("On \(computer.name)")
    }
}
