import Foundation
#if DEBUG || HMK_DEV
import os
#endif

/// Timings for diagnosing, in development builds only: Debug, and the
/// iPhone's `just ios-device` builds (HMK_DEV). Builds shipped to others
/// leave them out. Read them in Console (subsystem com.homegrownsw.hammock), or with
/// `log stream --predicate 'subsystem == "com.homegrownsw.hammock"'`.
enum DevLog {
    #if DEBUG || HMK_DEV
    private static let agent = Logger(subsystem: "com.homegrownsw.hammock", category: "agentview")
    private static let editorLog = Logger(subsystem: "com.homegrownsw.hammock", category: "editor")
    #endif

    /// The agent view: asking for a conversation, its rows arriving, and
    /// their first showing.
    static func agent(_ message: @autoclosure () -> String) {
        #if DEBUG || HMK_DEV
        let m = message()
        agent.info("\(m, privacy: .public)")
        #endif
    }

    /// Editor panes: what their page couldn't do.
    static func editor(_ message: @autoclosure () -> String) {
        #if DEBUG || HMK_DEV
        let m = message()
        editorLog.info("\(m, privacy: .public)")
        #endif
    }

    /// Milliseconds since t, for a log line.
    static func since(_ t: Date?) -> String {
        guard let t else { return "?" }
        return "\(Int(Date().timeIntervalSince(t) * 1000)) ms"
    }
}
