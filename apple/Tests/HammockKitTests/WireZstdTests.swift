import Foundation
import Testing
@testable import HammockKit

/// Messages from the Go encoder (internal/wirezstd, TestFixture), in order,
/// on one link: decoded by the phone's decoder.
@Test func wireZstdDecodesGoMessages() throws {
    let wire = try #require(WireZstd())
    let encoded = [
            "ARIotS/9BFCQAAAIAWhlbGxvIHRlcm1pbmFsDQo=",
            "ARI8AAAAAVQABA8V",
            "AAoDcmF3",
            "AZjXCmwBACQCYWdlbnQgdmlldzogYSBsb25nIHRyYW5zY3JpcHQgbGluZQFUFgU02v43AVQAAAABVAAQM5Wr8f8=",
            "AQA=",
    ]
    let big = Data(String(repeating: "agent view: a long transcript line\n", count: 5000).utf8)
    let want = [Data("\u{08}\u{01}hello terminal\r\n".utf8), Data("\u{08}\u{01}hello terminal\r\n".utf8), Data("\u{0a}\u{03}raw".utf8), big, Data()]
    for (i, e) in encoded.enumerated() {
        let got = try wire.decode(try #require(Data(base64Encoded: e)))
        #expect(got == want[i], "message \(i)")
    }
    // A plain message passes through; a malformed one is refused.
    #expect(try wire.decode(Data([0x08, 0x01])) == Data([0x08, 0x01]))
    #expect(throws: (any Error).self) { try WireZstd()!.decode(Data([1, 0xff, 0xff, 0xff, 0xff, 0x7f])) }
}
