import Foundation
import Security

/// A paired Mac and this device's key for it, kept in the Keychain.
struct StoredHost: Codable, Sendable, Identifiable, Equatable {
    var hostName: String
    /// The address that worked last ("host:port"), tried first.
    var address: String
    var certSHA256: String
    var deviceID: String?
    var privateKey: Data
    /// Its kind (Computer.kind: laptop, studio…), as it last said, for its icon.
    var kind: String?
    /// Every address the Mac gave, from pairing and then each time it's
    /// connected to (its local network's address can change), and its local
    /// networks' subnets: an address in one is only tried while this phone
    /// is on that network (RemoteCandidates). Nil before they were kept:
    /// `address` alone.
    var addresses: [String]?
    var lanSubnets: [String]?

    /// The Mac's certificate is its identity (an address can change).
    var id: String { certSHA256 }
    var displayName: String { hostName.replacingOccurrences(of: ".local", with: "") }
}

/// Every paired Mac, and which one the app shows.
struct StoredHosts: Codable, Sendable {
    var hosts: [StoredHost] = []
    var active: String?
}

enum IdentityStore {
    private static func query(_ account: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: "com.homegrownsw.hammock.host",
         kSecAttrAccount as String: account]
    }

    static func load() -> StoredHosts {
        if let data = read("hosts"), let s = try? JSONDecoder().decode(StoredHosts.self, from: data) {
            return StoredHosts(hosts: s.hosts.filter { $0.deviceID != nil }, active: s.active)
        }
        // Before several Macs: one host under "default".
        if let data = read("default"), let h = try? JSONDecoder().decode(StoredHost.self, from: data), h.deviceID != nil {
            let s = StoredHosts(hosts: [h], active: h.id)
            save(s)
            return s
        }
        return StoredHosts()
    }

    static func save(_ s: StoredHosts) {
        guard let data = try? JSONEncoder().encode(s) else { return }
        #if targetEnvironment(simulator)
        try? FileManager.default.createDirectory(at: simulatorDir, withIntermediateDirectories: true)
        // Unsigned simulator builds have no Keychain entitlement.
        try? data.write(to: simulatorDir.appendingPathComponent("hosts.json"), options: .completeFileProtection)
        #endif
        SecItemDelete(query("hosts") as CFDictionary)
        SecItemDelete(query("default") as CFDictionary)
        var q = query("hosts")
        q[kSecValueData as String] = data
        q[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        SecItemAdd(q as CFDictionary, nil)
    }

    private static func read(_ account: String) -> Data? {
        #if targetEnvironment(simulator)
        let file = simulatorDir.appendingPathComponent(account == "hosts" ? "hosts.json" : "host.json")
        if let d = try? Data(contentsOf: file) { return d }
        #endif
        var q = query(account)
        q[kSecReturnData as String] = true
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess else { return nil }
        return out as? Data
    }

    #if targetEnvironment(simulator)
    private static var simulatorDir: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
    }
    #endif
}
