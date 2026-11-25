import Foundation
import Network

/// What a phone says about its link to the computer, on every screen
/// (the iPhone's ConnectionBar): nothing while connected, or while it's
/// been down only a moment (a blip); then connecting (not connected yet
/// since the app opened) or reconnecting, and after a while, that it
/// can't reach it. It keeps trying throughout, and Try Again tries now.
public enum LinkStatus: Equatable, Sendable {
    case connecting
    case reconnecting
    case unreachable

    /// Down for less than this: nothing shown.
    public static let showAfter: TimeInterval = 1
    /// Down for this long: "Can't reach".
    public static let unreachableAfter: TimeInterval = 8

    /// What to show, nil for nothing. `downSince`: when this try at
    /// connecting began (nil while connected).
    public static func shown(connected: Bool, everConnected: Bool, downSince: Date?, now: Date) -> LinkStatus? {
        guard !connected, let downSince else { return nil }
        let down = now.timeIntervalSince(downSince)
        if down < showAfter { return nil }
        if down >= unreachableAfter { return .unreachable }
        return everConnected ? .reconnecting : .connecting
    }

    public func title(host: String) -> String {
        switch self {
        case .connecting: "Connecting to \(host)…"
        case .reconnecting: "Reconnecting to \(host)…"
        case .unreachable: "Can't reach \(host)"
        }
    }

    /// The line under the title, once it can't reach the computer: why,
    /// and how old what's on screen is.
    public func detail(reason: String?, lastLive: Date?, now: Date) -> String? {
        guard self == .unreachable else { return nil }
        var parts = [reason ?? Self.genericReason]
        if let lastLive {
            parts.append("Last updated \(elapsedString(sinceUnixMs: Int64(lastLive.timeIntervalSince1970 * 1000), now: now)) ago.")
        }
        return parts.joined(separator: " ")
    }

    static let genericReason = "It may be asleep, or on another network."

    /// What VoiceOver says as the status changes, if anything: that it
    /// can't reach the computer, and that it's back once something was
    /// shown. Nothing for a blip, or for connecting and reconnecting
    /// (the bar says those, and they pass).
    public static func announcement(from old: LinkStatus?, to new: LinkStatus?, host: String) -> String? {
        switch (old, new) {
        case (let o, .unreachable?) where o != .unreachable: "Can't reach \(host)"
        case (.connecting?, nil): "Connected to \(host)"
        case (.some, nil): "Reconnected to \(host)"
        default: nil
        }
    }

    /// Why a try at connecting failed, in a person's words; nil when there's
    /// nothing better to say than that it may be asleep or away.
    public static func reason(for error: Error?) -> String? {
        guard let error else { return nil }
        if let e = error as? DaemonError {
            switch e {
            case .closed, .notConnected: return nil
            case .server(let m): return m.isEmpty ? nil : sentence(m)
            }
        }
        if let e = error as? NWError {
            switch e {
            case .posix(let code):
                switch code {
                case .ETIMEDOUT: return "It didn't answer."
                case .ECONNREFUSED: return "Hammock isn't running there, or remote access is off."
                case .ENETDOWN: return "It isn't reachable from this network."
                case .EHOSTUNREACH, .ENETUNREACH, .EHOSTDOWN, .ECONNRESET, .ECONNABORTED, .ENOTCONN, .EPIPE: return nil
                default: return nil
                }
            case .tls: return "It isn't the computer this iPhone paired with (its certificate changed)."
            case .dns: return nil
            default: return nil
            }
        }
        return sentence(error.localizedDescription)
    }

    private static func sentence(_ s: String) -> String {
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let last = t.last else { return t }
        return ".!?".contains(last) ? t : t + "."
    }
}
