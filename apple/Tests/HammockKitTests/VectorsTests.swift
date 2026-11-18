import Foundation
import HammockKit
import XCTest

/// The same vectors the daemon's and the Android app's tests check
/// (testdata/client/vectors.json, internal/daemon/vectors_test.go): what the
/// iPhone builds must be byte for byte what the daemon expects.
final class VectorsTests: XCTestCase {
    private var vectors: [String: Any] {
        get throws {
            let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
                .appendingPathComponent("../../../testdata/client/vectors.json").standardizedFileURL
            return try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any] ?? [:]
        }
    }

    func testPairingLinks() throws {
        let list = try XCTUnwrap(vectors["pairing"] as? [[String: Any]])
        XCTAssertFalse(list.isEmpty)
        for v in list {
            let p = try XCTUnwrap(Pairing(code: v["link"] as? String ?? ""))
            XCTAssertEqual(p.hostName, v["host_name"] as? String)
            XCTAssertEqual(p.certSha256, v["cert_sha256"] as? String)
            XCTAssertEqual(p.secret.map { String(format: "%02x", $0) }.joined(), v["secret_hex"] as? String)
            XCTAssertEqual(p.addresses, v["addresses"] as? [String])
            XCTAssertEqual(p.remoteEnabled, v["remote_enabled"] as? Bool)
        }
    }

    func testApprovalMessages() throws {
        let list = try XCTUnwrap(vectors["approval"] as? [[String: Any]])
        XCTAssertFalse(list.isEmpty)
        for v in list {
            let r = HammockKeyRequest.with {
                $0.keyID = v["key_id"] as? String ?? ""; $0.id = v["id"] as? String ?? ""
                $0.dataHash = v["data_hash"] as? String ?? ""; $0.paneID = v["pane_id"] as? String ?? ""
            }
            let expires = (v["expires_unix_ms"] as? NSNumber)?.int64Value ?? 0
            let got = HammockKeyApproval.message(r, window: v["window"] as? Bool ?? false, expires: expires)
            XCTAssertEqual(String(decoding: got, as: UTF8.self), v["message"] as? String)
        }
    }
}
