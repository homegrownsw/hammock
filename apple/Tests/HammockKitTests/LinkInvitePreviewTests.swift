import Foundation
import Testing
@testable import HammockKit

@Suite struct LinkInvitePreviewTests {
    private func link(_ t: Hammock_V1_LinkToken) throws -> String {
        let b = try t.serializedData().base64EncodedString()
            .replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
        return "hammock://link/" + b
    }

    @Test func readsAddressesAndName() throws {
        let l = try link(.with { $0.name = "Studio"; $0.addresses = ["100.64.0.2:7433", "studio.local:7433"]; $0.certSha256 = "ab"; $0.userID = "u1" })
        let p = try #require(LinkInvitePreview(link: l))
        #expect(p.name == "Studio")
        #expect(p.addresses == ["100.64.0.2:7433", "studio.local:7433"])
    }

    @Test func refusesDamagedOrIncompleteLinks() throws {
        #expect(LinkInvitePreview(link: "hammock://join/abc") == nil)
        #expect(LinkInvitePreview(link: "hammock://link/!!!") == nil)
        // The daemon refuses a link without a key or user id: so does the preview.
        #expect(LinkInvitePreview(link: try link(.with { $0.addresses = ["x:1"]; $0.userID = "u" })) == nil)
        #expect(LinkInvitePreview(link: try link(.with { $0.certSha256 = "ab"; $0.userID = "u" })) == nil)
    }
}

@Suite struct LinkStartTests {
    @Test func outsideLinksAskInAppOnesStart() {
        #expect(LinkStart(link: nil, fromOutside: false) == .discover)
        #expect(LinkStart(link: nil, fromOutside: true) == .discover)
        // Link Again… passes a plain address: it starts, it isn't a "damaged link".
        #expect(LinkStart(link: "100.64.0.2:7433", fromOutside: false) == .start("100.64.0.2:7433"))
        #expect(LinkStart(link: "hammock://link/abc", fromOutside: false) == .start("hammock://link/abc"))
        #expect(LinkStart(link: "hammock://link/abc", fromOutside: true) == .ask("hammock://link/abc"))
    }
}
