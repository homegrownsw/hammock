#if DEBUG
import Foundation

/// Dev aid: HMK_DEBUG_SAMPLE=1 (SIMCTL_CHILD_HMK_DEBUG_SAMPLE=1 with simctl)
/// shows made-up sessions, with no phone; HMK_DEBUG_CONVERSATION=<pane>
/// opens one's conversation (s1 needs you, s3 works). For screenshots.
enum WatchSample {
    static let on = ProcessInfo.processInfo.environment["HMK_DEBUG_SAMPLE"] == "1"
    static let now = Int64(Date().timeIntervalSince1970 * 1000)

    static var snapshot: WatchSnapshot {
        let host = "sample"
        func s(_ pane: String, _ title: String, _ harness: String, _ state: WatchAgentState, _ summary: String, ago: Int64, progress: Int? = nil) -> WatchSession {
            WatchSession(host: host, hostName: "Studio", paneID: pane, title: title, harness: harness, state: state,
                         summary: summary, sinceMs: now - ago * 60_000, progress: progress, group: "app")
        }
        return WatchSnapshot(
            sessions: WatchSnapshot.ordered([
                s("s1", "checkout-flow", "claude", .needsInput, "Allow Bash: `npm test`?", ago: 0),
                s("s2", "api-server", "codex", .needsInput, "Which database should I use?", ago: 4),
                s("s3", "login-fix", "claude", .working, "Adding a test for the Safari click", ago: 6),
                s("s4", "widgets", "gemini", .idle, "Fixed the flaky test", ago: 18),
            ]),
            usage: [WatchUsage(harness: "claude", name: "Claude", windows: [
                .init(label: "5h", usedPercent: 62, resetsAt: Date().addingTimeInterval(80 * 60)),
                .init(label: "1w", usedPercent: 38, resetsAt: Date().addingTimeInterval(4 * 86400)),
            ])],
            schedules: [WatchSchedule(host: host, hostName: "Studio", scheduleID: "n", name: "Nightly triage", nextRun: Date().addingTimeInterval(5 * 3600))],
            macs: ["Studio"], quickReplies: ["Continue", "Looks good", "Run the tests"])
    }

    static var openConversation: WatchSession? {
        guard on, let pane = ProcessInfo.processInfo.environment["HMK_DEBUG_CONVERSATION"] else { return nil }
        return snapshot.sessions.first { $0.paneID == pane }
    }

    static func reply(_ r: WatchRequest) -> WatchReply {
        switch r {
        case .snapshot: return .snapshot(snapshot)
        case let .conversation(_, pane, _):
            let session = snapshot.sessions.first { $0.paneID == pane }
            var blocks = [
                WatchBlock(id: "u", kind: .user, text: "The login button does nothing in Safari. Can you look?"),
                WatchBlock(id: "t1", kind: .text, text: "Found it: the click handler was attached before the button rendered. I moved it into `onMount`."),
                WatchBlock(id: "tools", kind: .tools, text: "Ran 4 commands, edited 2 files"),
            ]
            var prompt: WatchPrompt?
            if pane == "s1" {
                prompt = WatchPrompt(kind: .permission, promptID: "p", title: "Do you want to proceed?", body: "Bash: npm test -- --run src/checkout",
                                     options: ["Yes", "Yes, and don't ask again for npm test", "No, and tell Claude what to do differently"], other: 3)
            } else if pane == "s2" {
                prompt = WatchPrompt(kind: .question, promptID: "q", title: "Which database should the API use?", header: "Database",
                                     options: ["PostgreSQL", "SQLite", "Type something"], descriptions: ["Matches production", "Simplest for local work", ""], other: 3)
            } else if session?.state == .idle {
                blocks += [
                    WatchBlock(id: "t2", kind: .text, text: "Sign-in works in Safari now and the new test passes. Want me to open a PR?"),
                    WatchBlock(id: "d", kind: .done, durationMs: 252_000),
                ]
            }
            let last = blocks.last { $0.kind == .text }?.text ?? ""
            return .conversation(WatchConversation(session: session, blocks: blocks, prompt: prompt, working: session?.state == .working,
                                                   canSendNow: true, suggestions: prompt == nil ? WatchSuggestions.make(lastText: last, quickReplies: snapshot.quickReplies) : []))
        case .image: return .failed("No pictures in the sample.")
        default: return .done
        }
    }
}
#endif
