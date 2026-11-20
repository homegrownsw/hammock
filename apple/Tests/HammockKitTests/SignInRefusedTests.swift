import Foundation
import Testing
@testable import HammockKit

/// Only the daemon refusing a paired device's sign-in stops a remote
/// client trying (the Mac removed it); anything else may pass.
struct SignInRefusedTests {
    @Test func refusedSignIn() {
        #expect(AppModel.signInRefused(DaemonError.server("authentication failed")))
        #expect(AppModel.signInRefused(DaemonError.server("Authentication Failed")))
    }

    @Test func otherFailuresRetry() {
        #expect(!AppModel.signInRefused(DaemonError.server("API authentication failed")))
        #expect(!AppModel.signInRefused(DaemonError.server("Bad challenge")))
        #expect(!AppModel.signInRefused(DaemonError.closed))
        #expect(!AppModel.signInRefused(DaemonError.notConnected))
        #expect(!AppModel.signInRefused(URLError(.timedOut)))
    }
}
