import Foundation

/// The Hammock Key: this Mac's own SSH key, in its Secure Enclave, which
/// signs only once you approve, with Touch ID on the Mac or on a phone you
/// let approve (hammockkey.go in the daemon, helpers/key on the Mac).
public typealias HammockKeyStatus = Hammock_V1_HammockKeyStatus
public typealias HammockKeyRequest = Hammock_V1_HammockKeyRequest
public typealias SSHSetup = Hammock_V1_SshSetupReply

public enum HammockKeyApproval {
    /// What a phone signs to approve a request, naming the key it's for
    /// (the helper checks the same bytes: approvalMessage in
    /// helpers/key/main.swift; hammockkey_test.go has the same test vector).
    public static func message(_ r: HammockKeyRequest, window: Bool, expires: Int64) -> Data {
        Data("hmk-key-approval-v2\n\(r.keyID)\n\(r.id)\n\(r.dataHash)\n\(window ? "window" : "once")\n\(r.paneID)\n\(expires)".utf8)
    }

    /// How long a phone's approval lasts: it signs the request as it is now.
    public static func expiry(now: Date = Date()) -> Int64 {
        Int64(now.addingTimeInterval(120).timeIntervalSince1970 * 1000)
    }
}

extension AppModel {
    /// Creates or deletes the Hammock Key, or changes which phones may approve
    /// it: the Mac asks you with Touch ID (this Mac's own clients only).
    @discardableResult
    public func hammockKeyCommand(_ op: Hammock_V1_HammockKeyCommand.Op, deviceID: String = "") async throws -> HammockKeyStatus {
        guard let conn = connection else { throw DaemonError.notConnected }
        let st = try await conn.call { $0.hammockKeyCommand = .with { $0.op = op; $0.deviceID = deviceID } }.hammockKeyStatus
        hammockKey = st
        return st
    }

    /// How ssh, git and jj signing in Hammock's panes reach Hammock's agent;
    /// apply adds Hammock's block to ~/.ssh/config, applyJJ its file to jj's
    /// config.
    public func sshSetup(apply: Bool = false, applyJJ: Bool = false) async throws -> SSHSetup {
        guard let conn = connection else { throw DaemonError.notConnected }
        return try await conn.call { $0.sshSetup = .with { $0.apply = apply; $0.applyJj = applyJJ } }.sshSetupReply
    }

    /// Says no to a request to sign with the Hammock Key (from anywhere).
    public func denyHammockKey(_ r: HammockKeyRequest) {
        hammockKeyRequests.removeAll { $0.id == r.id }
        Task { _ = await run { $0.respondHammockKey = .with { $0.id = r.id; $0.allow = false } } }
    }

    /// A phone's approval: its approval key's signature over
    /// HammockKeyApproval.message.
    public func approveHammockKey(_ r: HammockKeyRequest, window: Bool, signature: Data, expires: Int64) async throws {
        guard let conn = connection else { throw DaemonError.notConnected }
        try await conn.call {
            $0.respondHammockKey = .with {
                $0.id = r.id; $0.allow = true; $0.window = window
                $0.signature = signature; $0.expiresUnixMs = expires
            }
        }
    }

    /// Sends this phone's approval key (its Secure Enclave's), so the Mac
    /// can let it approve.
    public func sendApprovalKey(_ publicKey: Data) {
        Task { _ = await run { $0.setApprovalKey = .with { $0.publicKey = publicKey } } }
    }
}
