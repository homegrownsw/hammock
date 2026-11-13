import Foundation
import Observation
#if canImport(UIKit)
import UIKit
#else
import AppKit
#endif

public typealias AgentBlock = Hammock_V1_AgentBlock
public typealias AgentSubagent = Hammock_V1_AgentSubagent
public typealias AgentShell = Hammock_V1_AgentShell
public typealias AgentPrompt = Hammock_V1_AgentPrompt
public typealias AgentTask = Hammock_V1_AgentTask
public typealias SessionLink = Hammock_V1_AgentLink

/// One pane's agent view: the conversation the daemon parsed from the
/// agent's session file, the prompt on its screen, and the actions that
/// answer it. Shared by the Mac and iOS apps.
@MainActor
@Observable
public final class AgentSession {
    public let paneID: String
    /// Set: a subagent's own transcript (read only), not the pane's agent.
    public let subagentID: String
    /// Set: a background shell's output (read only), not the pane's agent.
    public let shellID: String
    /// The session's subagents, oldest first (the main view's).
    public private(set) var subagents: [AgentSubagent] = []
    /// The session's background shells, oldest first (the main view's).
    public private(set) var shells: [AgentShell] = []
    /// A shell's view: its output, the last of it.
    public private(set) var shellOutput = ""
    /// The agent's task list as it stands (empty: none, or none left).
    public private(set) var tasks: [AgentTask] = []
    /// The session's pull requests and artifacts, oldest first.
    public private(set) var links: [SessionLink] = []
    public private(set) var blocks: [AgentBlock] = []
    public private(set) var prompt = AgentPrompt()
    public private(set) var working = false
    public private(set) var status = ""
    /// What's typed in the agent's input box in the terminal.
    public private(set) var draft = ""
    public private(set) var hasMore = false
    public private(set) var commands: [String] = []
    /// Those of `commands` that only run at the start of a message (the
    /// agent's built-ins): not offered later in one.
    public private(set) var startCommands: Set<String> = []
    /// The model of the latest reply (e.g. claude-opus-5-5), the context it
    /// took, and the window that counts against.
    public private(set) var agentModel = ""
    public private(set) var contextTokens: Int64 = 0
    public private(set) var contextWindow: Int64 = 0
    public private(set) var error = ""
    public private(set) var loadingOlder = false
    /// The daemon has sent the conversation (or said there isn't one yet):
    /// no blocks after this is an empty chat, not one still loading.
    public private(set) var heard = false
    /// Messages sent from here that the transcript hasn't shown yet.
    public private(set) var pendingSends: [PendingSend] = []
    /// Your messages already in the transcript when the pending ones were sent.
    private var seenUserIDs: Set<String> = []
    /// Set while an answer is being typed, and the error if it didn't match.
    public private(set) var answering = false
    public var answerError: String?

    weak var model: AppModel?
    /// Set: a past session read through the session finder (no pane).
    var pastSession: (harness: String, id: String)?

    /// Who's in the pane, as people say it: "Codex" or "Claude".
    public var agentName: String {
        let h = model?.panes[paneID]?.agent.harness ?? ""
        return AgentCatalog.shortName(of: h.isEmpty ? "claude" : h)
    }
    var refs = 0
    /// When this view of it was asked for, and whether its rows have shown
    /// since (DevLog's timings).
    @ObservationIgnored var askedAt: Date?
    @ObservationIgnored var shownSinceAsked = false
    @ObservationIgnored private var images: [String: Data] = [:]
    /// Pictures decoded, so a row made again (scrolled back to) shows its
    /// picture at once, at its size, not a placeholder that then grows.
    @ObservationIgnored private var decoded: [String: PlatformImage] = [:]
    /// The rows you've opened (tool calls, groups, thinking, notes), by
    /// row: kept here, not in the rows, so a row made again (a lazy row
    /// scrolled away and back) stays open.
    public private(set) var opened: Set<String> = []

    func isOpen(_ id: String) -> Bool { opened.contains(id) }

    func setOpen(_ id: String, _ on: Bool) {
        if on { opened.insert(id) } else { opened.remove(id) }
    }

    /// A picture already decoded, if it has been.
    func decodedImage(_ ref: String) -> PlatformImage? { decoded[ref] }

    /// A picture, fetched and decoded once.
    func image(_ ref: String) async -> PlatformImage? {
        if let i = decoded[ref] { return i }
        guard let d = await file(ref), let i = PlatformImage(data: d) else { return nil }
        decoded[ref] = i
        return i
    }
    @ObservationIgnored private var imageWaiters: [String: [CheckedContinuation<Data?, Never>]] = [:]

    init(paneID: String, subagentID: String = "", shellID: String = "", model: AppModel) {
        self.paneID = paneID
        self.subagentID = subagentID
        self.shellID = shellID
        self.model = model
    }

    /// Its place in the model's sessions: the pane, and the subagent or
    /// shell if any.
    static func key(_ paneID: String, _ subagentID: String, _ shellID: String = "") -> String {
        if !shellID.isEmpty { return paneID + "$" + shellID }
        return subagentID.isEmpty ? paneID : paneID + "#" + subagentID
    }

    /// Most of a shell's output kept here.
    nonisolated static let shellKept = 256 << 10

    /// A shell's output so far, with what's new: a line redrawn (a carriage
    /// return, as progress bars do) keeps only its last.
    nonisolated static func appendOutput(_ old: String, _ new: String) -> String {
        var s = old + new
        if new.contains("\r") || old.hasSuffix("\r") {
            s = s.replacingOccurrences(of: "\r\n", with: "\n")
            // A line ending in one (still being redrawn) keeps it, so what
            // comes next replaces the line rather than joining it.
            let redrawing = s.hasSuffix("\r")
            s = s.split(separator: "\n", omittingEmptySubsequences: false)
                .map { line in line.split(separator: "\r", omittingEmptySubsequences: false).last { !$0.isEmpty }.map(String.init) ?? "" }
                .joined(separator: "\n")
            if redrawing { s += "\r" }
        }
        if s.utf8.count > shellKept {
            s = String(decoding: Array(s.utf8.suffix(shellKept)), as: UTF8.self)
            if let nl = s.firstIndex(of: "\n") { s = String(s[s.index(after: nl)...]) }
        }
        return s
    }

    /// The blocks as the conversation's rows (tool calls grouped), kept here
    /// so a view doesn't regroup them each time it renders.
    public private(set) var items: [AgentItem] = []

    func apply(_ v: Hammock_V1_AgentView) {
        if v.replace, let asked = askedAt, !shownSinceAsked {
            DevLog.agent("pane \(paneID): rows arrived after \(DevLog.since(asked)): \(v.blocks.count) blocks, \((try? v.serializedBytes() as [UInt8])?.count ?? 0) bytes, had \(blocks.count)")
        }
        // While Claude works the daemon sends its status line several times a
        // second. Every write to an observed property re-renders its readers
        // even when the value is the same, so only write what changed: the
        // transcript re-renders when blocks change, not on each tick.
        var bs = blocks
        // Kept: rows already shown above a new snapshot's (coming back, or
        // reconnecting, sends the latest turns again).
        var keptAbove = false
        if v.replace {
            bs = v.blocks
            if pastSession == nil, let first = v.blocks.first, let k = blocks.firstIndex(where: { $0.id == first.id }), k > 0 {
                bs = Array(blocks[..<k]) + v.blocks
                keptAbove = true
            }
            set(\.commands, v.commands)
            set(\.startCommands, Set(v.startCommands))
        } else if v.older {
            bs = v.blocks + bs
            loadingOlder = false
        } else if !v.blocks.isEmpty {
            var index = Dictionary(uniqueKeysWithValues: bs.enumerated().map { ($1.id, $0) })
            for b in v.blocks {
                if let i = index[b.id] {
                    bs[i] = b
                } else {
                    index[b.id] = bs.count
                    bs.append(b)
                }
            }
        }
        if bs != blocks {
            // Messages are rendered ahead, off the main thread.
            MarkdownView.prepare(v.blocks.compactMap { [.text, .plan, .thinking].contains($0.kind) ? $0.text : nil }, .current)
            blocks = bs
            let grouped = AgentItem.group(bs)
            if grouped != items { items = grouped }
        }
        if (v.replace && !keptAbove) || v.older { set(\.hasMore, v.hasMore_p) }
        if v.replace, !heard { heard = true }
        if v.replace || !v.subagents.isEmpty { set(\.subagents, v.subagents) }
        if v.replace || !v.shells.isEmpty { set(\.shells, v.shells) }
        if v.replace || v.hasTasks { set(\.tasks, v.tasks.tasks) }
        if v.replace || v.hasLinks { set(\.links, v.links.links) }
        if !shellID.isEmpty {
            set(\.shellOutput, v.replace ? Self.appendOutput("", v.shellOutput) : Self.appendOutput(shellOutput, v.shellOutput))
        }
        if !v.older {
            set(\.agentModel, v.model)
            set(\.contextTokens, v.contextTokens)
            set(\.contextWindow, v.contextWindow)
            set(\.prompt, v.prompt)
            set(\.working, v.working)
            set(\.status, v.status)
            // What's typed in its input box in the terminal (the message box
            // offers it; sending replaces it).
            if subagentID.isEmpty, pastSession == nil { set(\.draft, v.draft) }
            set(\.error, v.error)
        }
        // A sent message shows as pending until a new message of yours
        // appears in the transcript. Not by exact text: one queued while
        // Claude works arrives reworded (spacing, image references).
        if !pendingSends.isEmpty, v.older {
            seenUserIDs.formUnion(blocks.filter { $0.kind == .user }.map(\.id)) // history paged in, not new
        } else if !pendingSends.isEmpty {
            for b in blocks where b.kind == .user && !seenUserIDs.contains(b.id) {
                seenUserIDs.insert(b.id)
                let n = Self.squashed(b.text)
                if let i = pendingSends.firstIndex(where: { let p = Self.squashed($0.text); return n.contains(p) || p.contains(n) }) {
                    pendingSends.remove(at: i)
                } else if !pendingSends.isEmpty {
                    pendingSends.removeFirst()
                }
                if pendingSends.isEmpty { break }
            }
        }
    }

    private func set<T: Equatable>(_ key: ReferenceWritableKeyPath<AgentSession, T>, _ value: T) {
        if self[keyPath: key] != value { self[keyPath: key] = value }
    }

    /// Switches the agent's model (Claude's /model, with an alias such as
    /// "opus" or "sonnet[1m]").
    public func switchModel(to alias: String) {
        Task { await send("/model " + alias, pending: false) }
    }

    /// Pages in older turns.
    public func loadOlder() {
        guard hasMore, !loadingOlder, let first = blocks.first else { return }
        loadingOlder = true
        if let past = pastSession {
            // A past session (the finder's preview): read, not watched.
            Task {
                let r = try? await model?.connection?.call { $0.readSession = .with { $0.harness = past.harness; $0.id = past.id; $0.beforeBlockID = first.id } }
                if let v = r?.agentView { apply(v) } else { loadingOlder = false }
            }
            return
        }
        model?.connection?.send { $0.watchAgent = .with { $0.paneID = self.paneID; $0.subagentID = self.subagentID; $0.shellID = self.shellID; $0.watch = true; $0.beforeBlockID = first.id } }
    }

    /// Picks option n (1-based) of the prompt on screen; with text, types it
    /// into that option (the "other" option); with notes, sends them with
    /// the pick (a question with previews: AgentPrompt.notes).
    public func answer(option: Int, text: String = "", notes: String = "") async {
        await answer { $0.option = Int32(option); $0.text = text; $0.notes = notes }
    }

    /// Multi-select: tick exactly these options (1-based), then move on.
    public func answer(ticking options: [Int]) async {
        await answer { $0.toggles = options.map(Int32.init) }
    }

    private func answer(_ build: @escaping (inout Hammock_V1_AgentAnswer) -> Void) async {
        guard let conn = model?.connection else { return }
        let seen = prompt.options
        answering = true
        answerError = nil
        defer { answering = false }
        do {
            _ = try await conn.call {
                $0.agentAnswer = .with {
                    $0.paneID = self.paneID
                    $0.optionsSeen = seen
                    build(&$0)
                }
            }
        } catch {
            answerError = error.localizedDescription
        }
    }

    /// Sends a message (with images, attached by path), queued by the agent
    /// if it's working. Inline: text has a mark (U+FFFC) where each
    /// attachment goes. local: the attachments as the composer has them, to
    /// show the message with until the transcript does. False if it didn't
    /// go (not connected): the caller gives the text back.
    @discardableResult
    func send(_ text: String, attachments: [(name: String, data: Data, path: String?)], inline: Bool = false, local: [BubbleAttachment] = [], pending: Bool = true) async -> Bool {
        guard let conn = model?.connection else {
            answerError = "Not connected. Your message wasn't sent."
            return false
        }
        let p = PendingSend(text: ComposerDoc(text: text).plain.trimmingCharacters(in: .whitespacesAndNewlines),
                            rich: inline && !local.isEmpty ? text.trimmingCharacters(in: .whitespacesAndNewlines) : "",
                            attachments: local)
        if pending {
            if pendingSends.isEmpty { seenUserIDs = Set(blocks.filter { $0.kind == .user }.map(\.id)) }
            if !p.text.isEmpty || !p.attachments.isEmpty { pendingSends.append(p) }
        }
        do {
            _ = try await conn.call {
                $0.agentSend = .with {
                    $0.paneID = self.paneID
                    $0.text = text
                    $0.inline = inline
                    $0.attachments = attachments.map { a in
                        if let path = a.path { return .with { $0.name = a.name; $0.path = path } }
                        let (name, data) = claudeReadable(a.name, a.data)
                        return .with { $0.name = name; $0.data = data }
                    }
                }
            }
        } catch {
            pendingSends.removeAll { $0.id == p.id }
            answerError = error.localizedDescription
            return false
        }
        return true
    }

    /// Sends text as typed, with nothing attached.
    @discardableResult
    public func send(_ text: String, pending: Bool = true) async -> Bool {
        await send(text, attachments: [], pending: pending)
    }

    private static func squashed(_ s: String) -> String {
        s.split(whereSeparator: \.isWhitespace).joined(separator: " ")
    }

    /// Stops the agent (Esc).
    public func interrupt() { model?.send(paneID, Data([0x1B])) }

    /// An image or a referenced file, fetched once and cached.
    public func file(_ ref: String) async -> Data? {
        if let d = images[ref] { return d }
        if imageWaiters[ref] != nil {
            return await withCheckedContinuation { imageWaiters[ref, default: []].append($0) }
        }
        imageWaiters[ref] = []
        var data: Data?
        if let conn = model?.connection, let r = try? await conn.call({ $0.readAgentFile = .with { $0.paneID = self.paneID; $0.ref = ref } }) {
            data = r.agentFile.data
            images[ref] = data
        }
        for w in imageWaiters.removeValue(forKey: ref) ?? [] { w.resume(returning: data) }
        return data
    }
}

extension AppModel {
    /// Returns the pane's agent session (or one of its subagents' or
    /// background shells'), watching on first use.
    public func acquireAgent(_ paneID: String, subagent: String = "", shell: String = "") -> AgentSession {
        let key = AgentSession.key(paneID, subagent, shell)
        let s = agentSessions[key] ?? AgentSession(paneID: paneID, subagentID: subagent, shellID: shell, model: self)
        agentSessions[key] = s
        s.refs += 1
        if s.refs == 1 {
            s.askedAt = .now
            s.shownSinceAsked = false
            DevLog.agent("pane \(paneID): asked (\(s.blocks.isEmpty ? "new" : "kept, \(s.blocks.count) blocks"), \(connection == nil ? "not connected" : "connected"))")
            connection?.send { $0.watchAgent = .with { $0.paneID = paneID; $0.subagentID = subagent; $0.shellID = shell; $0.watch = true } }
        }
        return s
    }

    public func releaseAgent(_ paneID: String, subagent: String = "", shell: String = "") {
        let key = AgentSession.key(paneID, subagent, shell)
        guard let s = agentSessions[key] else { return }
        s.refs -= 1
        guard s.refs <= 0 else { return }
        // Kept a while: switching back to the agent view (from the terminal,
        // another tab) shows it at once, without fetching it all again.
        Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(120))
            guard let self, let s = self.agentSessions[key], s.refs <= 0 else { return }
            self.agentSessions[key] = nil
            self.connection?.send { $0.watchAgent = .with { $0.paneID = paneID; $0.subagentID = subagent; $0.shellID = shell; $0.watch = false } }
        }
    }

    /// Whether a pane runs an agent the agent view understands.
    public func supportsAgentView(_ pane: Pane) -> Bool {
        pane.hasAgent && AgentCatalog.hasAgentView(pane.agent.harness) && pane.agent.state != .exited
    }

    func rewatchAgents() {
        for s in agentSessions.values {
            DevLog.agent("pane \(s.paneID): asked again on reconnecting")
            s.askedAt = .now
            s.shownSinceAsked = false
            connection?.send { $0.watchAgent = .with { $0.paneID = s.paneID; $0.subagentID = s.subagentID; $0.shellID = s.shellID; $0.watch = true } }
        }
    }
}

/// Claude reads PNG, JPEG, GIF and WebP: anything else that's an image (a
/// phone's HEIC photo, say) goes as JPEG.
func claudeReadable(_ name: String, _ data: Data) -> (String, Data) {
    let magic = [UInt8](data.prefix(12))
    let png = magic.starts(with: [0x89, 0x50, 0x4E, 0x47]), jpeg = magic.starts(with: [0xFF, 0xD8, 0xFF])
    let gif = magic.starts(with: [0x47, 0x49, 0x46]), webp = magic.count == 12 && magic[8...11] == [0x57, 0x45, 0x42, 0x50]
    if png || jpeg || gif || webp { return (name, data) }
    guard let image = PlatformImage(data: data) else { return (name, data) }
    #if canImport(UIKit)
    guard let jpg = image.jpegData(compressionQuality: 0.85) else { return (name, data) }
    #else
    guard let tiff = image.tiffRepresentation, let rep = NSBitmapImageRep(data: tiff),
          let jpg = rep.representation(using: .jpeg, properties: [.compressionFactor: 0.85]) else { return (name, data) }
    #endif
    return ((name as NSString).deletingPathExtension + ".jpg", jpg)
}

/// A message sent from here, until the transcript shows it.
public struct PendingSend: Identifiable {
    public let id = UUID()
    /// As the transcript will show it (no chips), to know it there by.
    public let text: String
    /// With its chips' marks (none: "").
    let rich: String
    let attachments: [BubbleAttachment]
}

/// A view's hold on an agent session (AppModel.acquireAgent): released when
/// it's let go, so a view keeps its session for as long as its state lives,
/// whatever order SwiftUI says it appeared and disappeared in.
@MainActor
final class AgentLease {
    let session: AgentSession
    private let model: AppModel
    private let paneID, subagent, shell: String

    init(_ model: AppModel, _ paneID: String, subagent: String = "", shell: String = "") {
        self.model = model
        self.paneID = paneID
        self.subagent = subagent
        self.shell = shell
        session = model.acquireAgent(paneID, subagent: subagent, shell: shell)
    }

    deinit {
        let (model, paneID, subagent, shell) = (model, paneID, subagent, shell)
        Task { @MainActor in model.releaseAgent(paneID, subagent: subagent, shell: shell) }
    }
}
