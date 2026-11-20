import Foundation

/// The composer's message history, as a shell's: ↑ in an empty box (or on
/// its first line) brings back what you sent before, one at a time, and ↓
/// goes the other way, back to what you were typing.
///
/// The steps are this pane's messages: those in the conversation (yours,
/// and "!" commands), with those sent from here that it doesn't have (sent
/// before a /clear, or not in the transcript yet) kept on this device.
struct ComposerHistory<Draft> {
    private(set) var entries: [String] = []
    /// Which entry is shown (nil: what you were typing).
    private(set) var index: Int?
    /// What you were typing when you started stepping back.
    private(set) var draft: Draft?

    var browsing: Bool { index != nil }

    /// What you were typing is kept to come back to: while stepping, and
    /// after a message brought back has been edited (↓ from its last line
    /// still brings it back, as readline does).
    var keepsDraft: Bool { draft != nil }

    /// What to save as the draft now: what you were typing while a message
    /// brought back is shown unedited; else (nil) what's in the box.
    var savedDraft: Draft? { browsing ? draft : nil }

    /// One further back: entries are set as stepping starts.
    mutating func up(from current: Draft, entries all: @autoclosure () -> [String]) -> String? {
        if let i = index {
            guard i > 0 else { return nil }
            index = i - 1
            return entries[i - 1]
        }
        let e = all()
        guard !e.isEmpty else { return nil }
        entries = e
        // Stepping again after an edit: what you were typing first is still
        // the one to come back to.
        if draft == nil { draft = current }
        index = e.count - 1
        return e[e.count - 1]
    }

    /// One forward: the next entry, or past the newest, what you were typing.
    mutating func down() -> (entry: String?, draft: Draft?)? {
        guard let i = index else {
            // An edited message: ↓ brings back what you were typing.
            guard let d = draft else { return nil }
            reset()
            return (nil, d)
        }
        if i + 1 < entries.count {
            index = i + 1
            return (entries[i + 1], nil)
        }
        let d = draft
        reset()
        return (nil, d)
    }

    /// The text now in the box: once it's not the entry brought back (it's
    /// been edited), stepping ends (↑ and ↓ move the cursor through it), but
    /// what you were typing is kept for ↓ to bring back.
    mutating func edited(_ text: String) {
        if let i = index, entries[i] != text { index = nil }
    }

    mutating func reset() {
        index = nil
        draft = nil
        entries = []
    }

    /// Whether ↑ (up) or ↓ steps through the history rather than moving the
    /// cursor: ↑ with nothing but the first line before the cursor, in an
    /// empty box, at its very start, or while stepping; ↓ while stepping,
    /// with nothing but the last line after the cursor. Never with text
    /// selected (cursor nil). cursor is a UTF-16 offset.
    static func steps(up: Bool, text: String, cursor: Int?, browsing: Bool) -> Bool {
        guard let cursor else { return false }
        let u = Array(text.utf16)
        let at = min(max(cursor, 0), u.count)
        let newline = UInt16(UInt8(ascii: "\n"))
        if up {
            guard !u[..<at].contains(newline) else { return false }
            return u.isEmpty || browsing || at == 0
        }
        return browsing && !u[at...].contains(newline)
    }

    /// The steps: the conversation's messages in order, with those sent from
    /// here it doesn't have: older ones (before a /clear) ahead of it, newer
    /// ones (not in the transcript yet) after it. Each once, at its latest;
    /// the newest `cap`.
    static func merged(sent: [String], conversation: [String], cap: Int = ComposerHistoryStore.cap) -> [String] {
        func clean(_ s: [String]) -> [String] {
            s.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty && $0.count <= ComposerHistoryStore.longest }
        }
        let conv = clean(conversation), sent = clean(sent)
        let inConv = Set(conv)
        let last = sent.lastIndex(where: inConv.contains)
        let older = last.map { Array(sent[..<$0]) } ?? []
        let newer = last.map { Array(sent[($0 + 1)...]) } ?? sent
        var seen = Set<String>()
        var out: [String] = []
        for m in (older.filter { !inConv.contains($0) } + conv + newer.filter { !inConv.contains($0) }).reversed() where seen.insert(m).inserted {
            out.append(m)
        }
        return Array(out.prefix(cap).reversed())
    }
}

/// What's been sent from each pane's composer, kept on this device (a few
/// dozen of each, for the panes written to most recently).
enum ComposerHistoryStore {
    static let cap = 50
    static let panes = 40
    /// Longer messages (a pasted log) aren't kept.
    static let longest = 4000
    private static let key = "agentHistory"

    private struct Pane: Codable {
        var id: String
        var sent: [String]
    }

    /// A message as kept: its attachments' marks out, its spacing (an
    /// indented snippet) as typed, as the transcript has it.
    static func recordable(_ text: String) -> String {
        // Each chip goes with the space put in after it.
        text.replacingOccurrences(of: "\u{FFFC} ", with: "").replacingOccurrences(of: "\u{FFFC}", with: "")
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// The last list decoded, by the bytes it came from: decoded again only
    /// when they change, not on each ↑.
    nonisolated(unsafe) private static var cache: (data: Data, list: [Pane])?
    private static let cacheLock = NSLock()

    /// Most recently written last.
    private static func all(_ d: UserDefaults) -> [Pane] {
        guard let data = d.data(forKey: key) else { return [] }
        if let c = cacheLock.withLock({ cache }), c.data == data { return c.list }
        let list = (try? JSONDecoder().decode([Pane].self, from: data)) ?? []
        cacheLock.withLock { cache = (data, list) }
        return list
    }

    static func sent(_ paneID: String, defaults d: UserDefaults = .standard) -> [String] {
        all(d).first { $0.id == paneID }?.sent ?? []
    }

    /// Adds a message sent from paneID (the same as the last one isn't kept twice).
    static func record(_ paneID: String, _ text: String, defaults d: UserDefaults = .standard) {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty, t.count <= longest else { return }
        var list = all(d)
        var p = list.first { $0.id == paneID } ?? Pane(id: paneID, sent: [])
        list.removeAll { $0.id == paneID }
        p.sent.removeAll { $0 == t }
        p.sent.append(t)
        p.sent = Array(p.sent.suffix(cap))
        list.append(p)
        list = Array(list.suffix(panes))
        if let data = try? JSONEncoder().encode(list) { d.set(data, forKey: key) }
    }
}
