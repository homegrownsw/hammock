import CryptoKit
import Foundation

/// The keys this phone's Macs encrypt notifications with (one per Mac, by
/// its certificate fingerprint), shared with the notification extension
/// through the app group.
enum PushKeys {
    static let group = "group.com.homegrownsw.hammock"

    private static var file: URL? {
        FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group)?.appendingPathComponent("push-keys.json")
    }

    static func all() -> [String: Data] {
        guard let file, let data = try? Data(contentsOf: file),
              let m = try? JSONDecoder().decode([String: Data].self, from: data) else { return [:] }
        return m
    }

    /// The key for a Mac, made on first use.
    static func key(for host: String) -> Data {
        var m = all()
        if let k = m[host] { return k }
        let k = SymmetricKey(size: .bits256).withUnsafeBytes { Data($0) }
        m[host] = k
        save(m)
        return k
    }

    static func forget(_ host: String) {
        var m = all()
        m[host] = nil
        save(m)
    }

    private static func save(_ m: [String: Data]) {
        guard let file, let data = try? JSONEncoder().encode(m) else { return }
        // Readable while locked (after the first unlock), as notifications arrive then.
        try? data.write(to: file, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
    }

    /// Decrypts a notification from any of the Macs: only if it names the
    /// Mac whose key opened it (one Mac can't speak for another).
    static func open(_ sealed: Data) -> [String: Any]? {
        for (host, key) in all() {
            if let box = try? AES.GCM.SealedBox(combined: sealed),
               let plain = try? AES.GCM.open(box, using: SymmetricKey(data: key)),
               let obj = try? JSONSerialization.jsonObject(with: plain) as? [String: Any],
               (obj["host"] as? String)?.lowercased() == host.lowercased() {
                return obj
            }
        }
        return nil
    }

    /// The decrypted note behind a delivered notification's userInfo, if any.
    static func note(_ userInfo: [AnyHashable: Any]) -> [String: Any]? {
        guard let e = userInfo["e"] as? String, let sealed = Data(base64Encoded: e) else { return nil }
        return open(sealed)
    }
}
