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
    /// A pushed note about an agent's permission prompt names the pane and
    /// prompt its Allow and Deny answer; no other note offers them.
    func testAgentPromptNotes() throws {
        let list = try XCTUnwrap(vectors["push"] as? [[String: Any]])
        let notes = list.compactMap { $0["note"] as? [String: Any] }
        let prompt = try XCTUnwrap(notes.first { $0["c"] as? String == AgentPromptNotice.category })
        let a = try XCTUnwrap(AgentPromptNotice.answers(prompt))
        XCTAssertEqual(a.pane, prompt["pane"] as? String)
        XCTAssertEqual(a.item, prompt["id"] as? String)
        XCTAssertEqual(a.prompt, prompt["prompt"] as? String)
        for n in notes where n["c"] as? String != AgentPromptNotice.category { XCTAssertNil(AgentPromptNotice.answers(n)) }
        var unread = prompt
        unread["prompt"] = nil
        XCTAssertNil(AgentPromptNotice.answers(unread))
        XCTAssertEqual(AgentPromptNotice.allows(AgentPromptNotice.allowAction), true)
        XCTAssertEqual(AgentPromptNotice.allows(AgentPromptNotice.denyAction), false)
        XCTAssertNil(AgentPromptNotice.allows("grant.allow"))
    }

    /// The Mac offers Allow and Deny for the same items the daemon's pushes do.
    func testAgentPromptOffers() {
        let item = AttentionItem.with { $0.state = .needsInput; $0.promptID = "k1" }
        XCTAssertTrue(AgentPromptNotice.offers(item))
        for change in [{ (i: inout AttentionItem) in i.question = true }, { $0.program = true }, { $0.promptID = "" }, { $0.state = .idle }, { $0.state = .errored }] {
            var i = item
            change(&i)
            XCTAssertFalse(AgentPromptNotice.offers(i))
        }
    }

    private static let states: [String: AgentState] = ["needs_input": .needsInput, "idle": .idle, "errored": .errored, "working": .working]

    /// What the Mac's notifications say about an agent, as the daemon's pushes say it.
    func testNoticeBodies() throws {
        let list = try XCTUnwrap(vectors["notice"] as? [[String: Any]])
        XCTAssertFalse(list.isEmpty)
        for v in list {
            let item = AttentionItem.with {
                $0.state = Self.states[v["state"] as? String ?? ""] ?? .unspecified
                $0.question = v["question"] as? Bool ?? false
                $0.program = v["program"] as? Bool ?? false
                $0.summary = v["summary"] as? String ?? ""
            }
            XCTAssertEqual(item.noticeBody(who: v["who"] as? String ?? ""), v["body"] as? String)
        }
    }

    /// A pane's name as a notification names it: the daemon's capitalizeWords is Foundation's capitalized.
    func testNoticeNames() throws {
        let list = try XCTUnwrap(vectors["names"] as? [[String: Any]])
        XCTAssertFalse(list.isEmpty)
        for v in list {
            XCTAssertEqual((v["name"] as? String ?? "").capitalized, v["who"] as? String)
        }
    }

    /// The number on Hammock's icon, as the daemon counts it for pushes.
    func testBadgeCount() throws {
        let list = try XCTUnwrap(vectors["badge"] as? [[String: Any]])
        XCTAssertFalse(list.isEmpty)
        for v in list {
            let attention = (v["attention"] as? [String] ?? []).map { s in AttentionItem.with { $0.state = Self.states[s] ?? .unspecified } }
            let n = AppModel.badgeCount(attention: attention, grants: v["grants"] as? Int ?? 0, joinRequests: v["joins"] as? Int ?? 0,
                                        keyRequests: v["keys"] as? Int ?? 0)
            XCTAssertEqual(n, v["count"] as? Int)
        }
    }
}
