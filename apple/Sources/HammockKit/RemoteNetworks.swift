import Foundation

// The networks remote access listens on (Settings ▸ Devices ▸ Remote
// Access): Tailscale, a local network or a VPN, by subnet, and the extra
// addresses given out in pairing codes and links. Local clients only.

public typealias RemoteNetwork = Hammock_V1_RemoteNetwork

extension AppModel {
    /// Replaces the networks remote access listens on (RemoteNetwork ids)
    /// and the extra host[:port] addresses given out. The daemon writes them
    /// to the config file (remote-network, remote-address) and listens at once.
    public func setRemoteNetworks(_ ids: [String], extraAddresses: [String]) async throws {
        guard let conn = connection else { throw DaemonError.notConnected }
        try await conn.call {
            $0.setRemoteNetworks = .with {
                $0.networks = ids
                $0.extraAddresses = extraAddresses
            }
        }
    }
}

extension Hammock_V1_Devices {
    /// Remote access can be reached now: a chosen network this Mac is on.
    public var listensSomewhere: Bool { networks.contains { $0.enabled && $0.connected } }

    /// No network is chosen at all: ask where to listen before turning
    /// remote access on, pairing or linking. (One chosen but not here now,
    /// say Tailscale off for a moment, isn't asked about again.)
    public var needsNetworkChoice: Bool { !networks.contains { $0.enabled } }
}

extension Hammock_V1_RemoteNetwork {
    /// "Wi-Fi · 192.168.1.0/24", or just "Tailscale".
    public var title: String { id == label || kind == "tailscale" ? label : "\(label) · \(id)" }

    /// What to suggest the first time: Tailscale, if this Mac is on it. A
    /// local network or a VPN is a choice to make, never a default: the
    /// Wi-Fi this Mac is on may be a café's, and anyone on it could knock.
    public var suggested: Bool { connected && kind == "tailscale" && !`public` }
}
