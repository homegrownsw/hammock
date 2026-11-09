import HammockKit
import XCTest

final class HammockKeyTests: XCTestCase {
    /// The bytes a phone signs: the Hammock Key helper (helpers/key) and
    /// hammockkey_test.go's TestApprovalMessageVector build the same.
    func testApprovalMessageVector() {
        let r = HammockKeyRequest.with {
            $0.keyID = "k1"; $0.id = "fk_1"; $0.dataHash = "ab"; $0.paneID = "pn_1"
        }
        XCTAssertEqual(String(decoding: HammockKeyApproval.message(r, window: true, expires: 123), as: UTF8.self),
                       "hmk-key-approval-v2\nk1\nfk_1\nab\nwindow\npn_1\n123")
        XCTAssertEqual(String(decoding: HammockKeyApproval.message(r, window: false, expires: 123), as: UTF8.self),
                       "hmk-key-approval-v2\nk1\nfk_1\nab\nonce\npn_1\n123")
    }
}
