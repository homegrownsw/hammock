import Foundation

// What the iPhone and the Apple Watch say to each other over
// WatchConnectivity: small Codable values the phone makes from the Macs'
// state (WatchSnapshots.swift), so the watch never sees the daemon's
// protocol. Foundation only: the watch app compiles this file alone.

/// An agent's state, as the watch draws it (the daemon's AgentState).
public enum WatchAgentState: String, Codable, Sendable, Hashable {
    case unspecified, starting, working, needsInput, idle, errored, exited
}

/// One agent session: a tab with an agent, by its most pressing pane, as
/// the iPhone's Sessions list has them.
public struct WatchSession: Codable, Sendable, Hashable, Identifiable {
    /// The Mac's id (its certificate's fingerprint) and its name.
    public var host: String
    public var hostName: String
    public var paneID: String
    /// The tab's name.
    public var title: String
    /// "claude", "codex", …
    public var harness: String
    public var state: WatchAgentState
    /// Finished and seen: at rest.
    public var resting: Bool
    /// What it's doing, saying or asking (markdown), else its state.
    public var summary: String
    public var sinceMs: Int64
    public var progress: Int?
    public var asleep: Bool
    /// Stopped at its usage limit, carrying on by itself when it resets.
    public var waitingOnLimit: Bool
    /// Its Group, for a second line when there's room.
    public var group: String

    public var id: String { host + "/" + paneID }

    public init(host: String, hostName: String, paneID: String, title: String, harness: String, state: WatchAgentState,
                resting: Bool = false, summary: String = "", sinceMs: Int64 = 0, progress: Int? = nil, asleep: Bool = false,
                waitingOnLimit: Bool = false, group: String = "") {
        self.host = host
        self.hostName = hostName
        self.paneID = paneID
        self.title = title
        self.harness = harness
        self.state = state
        self.resting = resting
        self.summary = summary
        self.sinceMs = sinceMs
        self.progress = progress
        self.asleep = asleep
        self.waitingOnLimit = waitingOnLimit
        self.group = group
    }

    /// Where it goes in the list: needs you, working, or finished.
    public enum Section: Int, Comparable, Sendable {
        case needsYou, working, recent
        public static func < (a: Section, b: Section) -> Bool { a.rawValue < b.rawValue }
    }

    public var section: Section {
        switch state {
        case .needsInput, .errored: .needsYou
        case .working, .starting: .working
        default: .recent
        }
    }
}

/// An agent's usage limits (one agent's windows: "5h", "1w").
public struct WatchUsage: Codable, Sendable, Hashable {
    public struct Window: Codable, Sendable, Hashable {
        public var label: String
        public var usedPercent: Double
        public var resetsAt: Date?
        public init(label: String, usedPercent: Double, resetsAt: Date?) {
            self.label = label
            self.usedPercent = usedPercent
            self.resetsAt = resetsAt
        }
    }
    public var harness: String
    public var name: String
    public var windows: [Window]
    public init(harness: String, name: String, windows: [Window]) {
        self.harness = harness
        self.name = name
        self.windows = windows
    }
}

/// A scheduled task coming up.
public struct WatchSchedule: Codable, Sendable, Hashable, Identifiable {
    public var host: String
    public var hostName: String
    public var scheduleID: String
    public var name: String
    public var nextRun: Date
    public var id: String { host + "/" + scheduleID }
    public init(host: String, hostName: String, scheduleID: String, name: String, nextRun: Date) {
        self.host = host
        self.hostName = hostName
        self.scheduleID = scheduleID
        self.name = name
        self.nextRun = nextRun
    }
}

/// Everything the watch's lists show, from every paired Mac.
public struct WatchSnapshot: Codable, Sendable, Hashable {
    /// Most pressing first: needs you, working, finished; newest change first in each.
    public var sessions: [WatchSession]
    public var usage: [WatchUsage]
    public var schedules: [WatchSchedule]
    /// Paired Macs, by name; and those that couldn't be reached just now.
    public var macs: [String]
    public var unreachable: [String]
    /// The phone's theme (0xRRGGBB): its accent, and your messages' bubble.
    public var accent: UInt32
    public var bubble: UInt32
    public var at: Date

    public init(sessions: [WatchSession] = [], usage: [WatchUsage] = [], schedules: [WatchSchedule] = [], macs: [String] = [],
                unreachable: [String] = [], accent: UInt32 = WatchSnapshot.hammockGreen, bubble: UInt32 = WatchSnapshot.hammockBubble, at: Date = .now) {
        self.sessions = sessions
        self.usage = usage
        self.schedules = schedules
        self.macs = macs
        self.unreachable = unreachable
        self.accent = accent
        self.bubble = bubble
        self.at = at
    }

    /// Hammock's own theme, dark (AppTheme's): until the phone says otherwise.
    public static let hammockGreen: UInt32 = 0x3FB88A
    public static let hammockBubble: UInt32 = 0x1F6A4C

    /// Sessions in list order: needs you, working, finished; newest change first in each.
    public static func ordered(_ sessions: [WatchSession]) -> [WatchSession] {
        sessions.sorted { a, b in a.section != b.section ? a.section < b.section : a.sinceMs > b.sinceMs }
    }

    /// Several Macs: each row says which.
    public var showsMacNames: Bool { macs.count > 1 }

    public var needsYou: [WatchSession] { sessions.filter { $0.section == .needsYou } }
}

/// A prompt on an agent's screen waiting for an answer: a permission, a
/// question, or a plan to approve.
public struct WatchPrompt: Codable, Sendable, Hashable {
    public enum Kind: String, Codable, Sendable { case question, permission, plan }
    public var kind: Kind
    /// Sent back with the answer: the Mac only answers the prompt it's for.
    public var promptID: String
    /// The question, or e.g. "Do you want to proceed?".
    public var title: String
    /// The question tool's short header ("Database"), when it has one.
    public var header: String
    /// A permission's tool and command, as the screen shows it.
    public var body: String
    /// As shown, numbered from 1.
    public var options: [String]
    public var descriptions: [String]
    public var multiSelect: Bool
    /// Multi-select: options 1 to this are checkboxes.
    public var checkboxes: Int
    /// The option that takes typed text (1-based); 0 none.
    public var other: Int

    public init(kind: Kind, promptID: String, title: String, header: String = "", body: String = "", options: [String],
                descriptions: [String] = [], multiSelect: Bool = false, checkboxes: Int = 0, other: Int = 0) {
        self.kind = kind
        self.promptID = promptID
        self.title = title
        self.header = header
        self.body = body
        self.options = options
        self.descriptions = descriptions
        self.multiSelect = multiSelect
        self.checkboxes = checkboxes
        self.other = other
    }
}

/// One line of the watch's conversation: only what reads on a wrist.
public struct WatchBlock: Codable, Sendable, Hashable, Identifiable {
    public enum Kind: String, Codable, Sendable {
        /// A message you sent.
        case user
        /// The agent's markdown.
        case text
        /// A picture (a screenshot, an image you attached): `ref` fetches it.
        case image
        /// A run of tool calls, folded to one line ("Ran 4 commands, edited 2 files").
        case tools
        /// A question it asked, and your answer.
        case question
        /// A turn finished, after `durationMs`.
        case done
    }
    public var id: String
    public var kind: Kind
    public var text: String
    /// .question: your answer.
    public var answer: String
    public var ref: String
    public var durationMs: Int64

    public init(id: String, kind: Kind, text: String = "", answer: String = "", ref: String = "", durationMs: Int64 = 0) {
        self.id = id
        self.kind = kind
        self.text = text
        self.answer = answer
        self.ref = ref
        self.durationMs = durationMs
    }
}

/// One agent's conversation, as the watch shows it.
public struct WatchConversation: Codable, Sendable, Hashable {
    public var session: WatchSession?
    public var blocks: [WatchBlock]
    public var prompt: WatchPrompt?
    public var working: Bool
    /// Earlier blocks there are to ask for (a larger limit).
    public var hasMore: Bool
    /// Messages sent while it works can go at once (Claude Code's Ctrl+Enter).
    public var canSendNow: Bool
    /// Replies to offer: from what it last said, then your quick replies.
    public var suggestions: [String]

    public init(session: WatchSession?, blocks: [WatchBlock] = [], prompt: WatchPrompt? = nil, working: Bool = false,
                hasMore: Bool = false, canSendNow: Bool = false, suggestions: [String] = []) {
        self.session = session
        self.blocks = blocks
        self.prompt = prompt
        self.working = working
        self.hasMore = hasMore
        self.canSendNow = canSendNow
        self.suggestions = suggestions
    }
}

/// What the watch asks the phone.
public enum WatchRequest: Codable, Sendable, Hashable {
    /// The lists: every Mac's sessions, usage and schedules.
    case snapshot
    /// An agent's conversation, its last `limit` blocks.
    case conversation(host: String, pane: String, limit: Int)
    /// A picture in it, made small enough for the watch.
    case image(host: String, pane: String, ref: String)
    /// Sends a message, queued while it works unless `now`.
    case send(host: String, pane: String, text: String, now: Bool)
    /// Answers the prompt `promptID`: picks option (1-based), with text for
    /// the "other" option; or, multi-select, ticks `ticking` and moves on.
    case answer(host: String, pane: String, promptID: String, option: Int, ticking: [Int], text: String)
    /// Stops the agent (Esc).
    case interrupt(host: String, pane: String)
    /// Runs a scheduled task now.
    case runSchedule(host: String, schedule: String)
}

/// The phone's answer.
public enum WatchReply: Codable, Sendable, Hashable {
    case snapshot(WatchSnapshot)
    case conversation(WatchConversation)
    case image(Data)
    case done
    /// Something went wrong, as people should read it ("Couldn't reach Studio.").
    case failed(String)
}

public enum WatchWire {
    public static func encode<T: Encodable>(_ v: T) throws -> Data { try PropertyListEncoder.binary.encode(v) }
    public static func decode<T: Decodable>(_ t: T.Type, from data: Data) throws -> T { try PropertyListDecoder().decode(t, from: data) }

    /// The application context's key for the latest snapshot (complications
    /// and the list read it before the phone has answered).
    public static let snapshotKey = "snapshot"

    /// The longest side of a picture sent to the watch, in pixels, and the
    /// most a message may carry (WatchConnectivity's limit is about 64 KB).
    public static let imageSide: Double = 400
    public static let imageBytes = 48 * 1024
}

private extension PropertyListEncoder {
    static var binary: PropertyListEncoder {
        let e = PropertyListEncoder()
        e.outputFormat = .binary
        return e
    }
}

/// Replies the watch offers: a yes or no when the agent asked, its own
/// offer back when it offered something ("Want me to open a PR?": "Yes,
/// open a PR"), then your quick replies.
public enum WatchSuggestions {
    public static let max = 6

    public static func make(lastText: String, quickReplies: [String]) -> [String] {
        var out: [String] = []
        let question = lastQuestion(in: lastText)
        if let question {
            if let offer = offer(in: question) {
                out.append("Yes, " + offer)
            } else {
                out.append("Yes")
            }
            out.append("No")
        }
        out += quickReplies
        var seen = Set<String>()
        return Array(out.filter { seen.insert($0.lowercased()).inserted }.prefix(max))
    }

    /// The last sentence when it's a question.
    static func lastQuestion(in text: String) -> String? {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard t.hasSuffix("?") else { return nil }
        // Back to the end of the sentence (or line) before it.
        let body = t.dropLast()
        let start = body.lastIndex(where: { ".!?\n".contains($0) }).map { body.index(after: $0) } ?? body.startIndex
        let s = String(t[start...]).trimmingCharacters(in: .whitespacesAndNewlines)
        return s.isEmpty ? nil : s
    }

    /// "Want me to open a PR?" → "open a PR"; nil when it isn't an offer.
    static func offer(in question: String) -> String? {
        let lead = ["want me to ", "would you like me to ", "should i ", "shall i ", "do you want me to ", "can i "]
        let lower = question.lowercased()
        for l in lead {
            guard let r = lower.range(of: l) else { continue }
            // Only near the start: "Should I …?", "OK — want me to …?".
            guard lower.distance(from: lower.startIndex, to: r.lowerBound) <= 12 else { continue }
            let i = question.index(question.startIndex, offsetBy: lower.distance(from: lower.startIndex, to: r.upperBound))
            var rest = String(question[i...]).trimmingCharacters(in: .whitespaces)
            if rest.hasSuffix("?") { rest.removeLast() }
            // Short ones only: a long one doesn't fit on a button.
            guard !rest.isEmpty, rest.count <= 40, !rest.contains(" or ") else { return nil }
            return rest
        }
        return nil
    }
}

extension WatchSession {
    /// "2m", "1h", "3d": how long it's been in its state.
    public func elapsed(now: Date = .now) -> String {
        let s = max(0, Int(now.timeIntervalSince1970 - Double(sinceMs) / 1000))
        if s < 60 { return "now" }
        if s < 3600 { return "\(s / 60)m" }
        if s < 86400 { return "\(s / 3600)h" }
        return "\(s / 86400)d"
    }
}

extension WatchBlock {
    /// "Finished in 4m 12s".
    public static func duration(_ ms: Int64) -> String {
        let s = Int(ms / 1000)
        if s < 60 { return "\(s)s" }
        if s < 3600 { return "\(s / 60)m \(s % 60)s" }
        return "\(s / 3600)h \((s % 3600) / 60)m"
    }
}
