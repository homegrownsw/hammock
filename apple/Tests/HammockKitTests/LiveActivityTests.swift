import Foundation
@testable import HammockKit
import XCTest

/// The Live Activity's content as the daemon sends it (testdata/client/
/// vectors.json's "live", as internal/daemon/live.go makes it), and the
/// Needs You widget's list and links.
final class LiveActivityTests: XCTestCase {
    private func liveVectors() throws -> [[String: Any]] {
        let url = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .appendingPathComponent("../../../testdata/client/vectors.json").standardizedFileURL
        let all = try JSONSerialization.jsonObject(with: Data(contentsOf: url)) as? [String: Any] ?? [:]
        return try XCTUnwrap(all["live"] as? [[String: Any]])
    }

    func testReadsTheDaemonsContent() throws {
        let list = try liveVectors()
        XCTAssertFalse(list.isEmpty)
        for v in list {
            let json = String(decoding: try JSONSerialization.data(withJSONObject: v), as: UTF8.self)
            let c = try XCTUnwrap(LiveContent.decode(json), json)
            XCTAssertEqual(c.agent, v["agent"] as? String)
            XCTAssertEqual(c.state, v["state"] as? String)
            XCTAssertEqual(c.status, v["status"] as? String)
            XCTAssertEqual(c.doing, v["doing"] as? String)
            XCTAssertEqual(c.place, v["place"] as? String)
            XCTAssertEqual(c.more, v["more"] as? String)
            XCTAssertEqual(c.since, v["since"] as? Int)
            XCTAssertEqual(c.ctx, v["ctx"] as? Int)
            XCTAssertEqual(c.prompt, v["prompt"] as? String)
            XCTAssertEqual(c.item, v["item"] as? String)
            XCTAssertEqual(c.others, v["others"] as? Int)
            XCTAssertEqual(c.waiting, v["waiting"] as? Int)
            // Allow and Deny only on a prompt it can answer.
            XCTAssertEqual(c.answerable, v["state"] as? String == "needs_you" && v["prompt"] != nil)
            XCTAssertEqual(c.over, v["state"] as? String == "finished")
        }
    }

    func testContentReadsLoosely() {
        XCTAssertNil(LiveContent.decode(""))
        XCTAssertNil(LiveContent.decode("{}"))
        let c = LiveContent.decode(##"{"agent":"Claude","state":"working","status":"Working","since":5,"others":0,"waiting":0,"ctx":130,"accent":"#1F7A57"}"##)
        XCTAssertEqual(c?.kind, .working)
        XCTAssertEqual(c?.contextFraction, 1)
        XCTAssertEqual(c?.started, Date(timeIntervalSince1970: 5))
        let rgb = c?.accentRGB(dark: true)
        XCTAssertEqual(rgb?.0 ?? 0, Double(0x1F) / 255, accuracy: 0.001)
        XCTAssertNil(LiveContent.rgb("green"))
    }

    func testNeedsYouList() {
        let a = NeedsYouItem(id: "at_1", host: "h1", pane: "pn_1", title: "api", place: "Web", ask: "Allow Bash: npm test?", at: 20)
        let b = NeedsYouItem(id: "at_2", host: "h1", pane: "pn_2", title: "ui", place: "Web", ask: "Claude asks: Which?", at: 10)
        let c = NeedsYouItem(id: "at_1", host: "h2", pane: nil, title: "x", place: "Atlas · Studio", ask: "Join?", at: 30)
        var s = NeedsYouSnapshot().adding(a).adding(b).adding(c)
        // The one waiting longest first.
        XCTAssertEqual(s.top(2).map(\.id), ["at_2", "at_1"])
        XCTAssertEqual(s.top(5).count, 3)
        // The same thing again replaces it (per Mac).
        s = s.adding(NeedsYouItem(id: "at_1", host: "h1", pane: "pn_1", title: "api", place: "Web", ask: "Allow Edit?", at: 40))
        XCTAssertEqual(s.items.count, 3)
        XCTAssertEqual(s.items.first { $0.host == "h1" && $0.id == "at_1" }?.ask, "Allow Edit?")
        // Dealt with on one Mac: that Mac's goes, not the other's.
        s = s.removing(["at_1"], host: "h1")
        XCTAssertEqual(s.items.map(\.host).sorted(), ["h1", "h2"])
        s = s.replacing(host: "h1", with: [])
        XCTAssertEqual(s.items.map(\.id), ["at_1"])
        XCTAssertEqual(s.items[0].url, HammockURL.needsYou(host: "h2"))
    }

    func testLinks() {
        let u = HammockURL.pane(host: "9f2c", pane: "pn_1")
        XCTAssertEqual(u.absoluteString, "hammock://pane/9f2c/pn_1")
        XCTAssertEqual(HammockURL.target(u), .pane(host: "9f2c", pane: "pn_1"))
        XCTAssertEqual(HammockURL.target(HammockURL.needsYou(host: "9f2c")), .needsYou(host: "9f2c"))
        XCTAssertNil(HammockURL.target(URL(string: "hammock://pair/abc")!))
        XCTAssertNil(HammockURL.target(URL(string: "hammock://pane/onlyone")!))
        XCTAssertNil(HammockURL.target(URL(string: "https://pane/a/b")!))
    }
}
