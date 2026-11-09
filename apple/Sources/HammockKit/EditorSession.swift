import Foundation
import Observation

public typealias EditorInfo = Hammock_V1_EditorInfo

/// One editor pane as this app shows it: the go-between for the daemon,
/// which keeps the buffer, and the pane's CodeMirror page (EditorBridge),
/// which does the editing and the collab rebasing. Shared by the Mac and
/// iOS apps.
@MainActor
@Observable
public final class EditorSession {
    public let paneID: String
    /// Where the cursor is, for the pane's status (1-based).
    public private(set) var line = 1
    public private(set) var column = 1
    public private(set) var selected = 0
    /// The language highlighting it ("" plain text), as the page found it.
    public private(set) var languageName = ""
    public private(set) var loaded = false
    /// The diff against the file on disk is showing.
    public private(set) var showingDiff = false
    public var error = ""

    weak var model: AppModel?
    var refs = 0
    /// The page commands go to: the one focused last (else the newest).
    @ObservationIgnored weak var bridge: EditorBridge?
    /// Every page showing the pane (two windows can show one tab): each
    /// gets every update, or its edits are pushed at a stale version.
    @ObservationIgnored private let pages = NSHashTable<EditorBridge>.weakObjects()
    /// The last snapshot and what's come since, for a page that loads (or
    /// reloads) after they arrived.
    @ObservationIgnored private var snapshot: Hammock_V1_EditorUpdate?
    @ObservationIgnored private var since: [Hammock_V1_EditorUpdate] = []
    @ObservationIgnored private var lastGit: [Hammock_V1_GitMark] = []
    /// The next snapshot replaces what the pages have unconfirmed (the
    /// daemon refused it), rather than their carrying it over.
    @ObservationIgnored private var discardLocal = false

    init(paneID: String, model: AppModel) {
        self.paneID = paneID
        self.model = model
    }

    func attach() {
        model?.connection?.send { $0.attachPane = .with { $0.paneID = paneID } }
    }

    func apply(_ u: Hammock_V1_EditorUpdate) {
        if u.gitKnown || !u.git.isEmpty || (u.updates.isEmpty && !u.snapshot && !u.hasSelection) { lastGit = u.git }
        if u.snapshot {
            snapshot = u
            since = []
            loaded = true
            for p in pages.allObjects { p.load(u, git: lastGit, discardLocal: discardLocal) }
            discardLocal = false
            return
        }
        if snapshot != nil, !u.updates.isEmpty { since.append(u) }
        // Not too many kept: past that, a page loading late asks again.
        if since.count > 2000 {
            since = []
            snapshot = nil
        }
        for p in pages.allObjects { p.receive(u) }
    }

    /// A page ready for the buffer: what's arrived so far.
    func pageReady(_ b: EditorBridge) {
        pages.add(b)
        bridge = b
        guard let s = snapshot else {
            attach() // its snapshot loads it
            return
        }
        b.load(s, git: lastGit)
        for u in since { b.receive(u) }
    }

    // MARK: From the page

    /// The page took focus: commands (find, undo…) go to it.
    func pageFocused(_ b: EditorBridge) { bridge = b }

    func push(from page: EditorBridge, version: UInt64, updates: [[String: Any]]) {
        guard let conn = model?.connection else {
            page.pushResult(false)
            return
        }
        let changes = updates.map { u in
            Hammock_V1_EditorChange.with {
                $0.changes = u["changes"] as? String ?? "[]"
                $0.clientID = u["clientID"] as? String ?? ""
            }
        }
        conn.call({ $0.editorPush = .with { $0.paneID = self.paneID; $0.version = version; $0.updates = changes } }) { [weak self, weak page] r in
            Task { @MainActor in
                guard let self else { return }
                switch r {
                case .success(let m): page?.pushResult(m.editorPushReply.accepted)
                case .failure(let e):
                    // Changes the daemon couldn't apply: start again from its buffer.
                    self.error = e.localizedDescription
                    page?.pushResult(false)
                    self.resync(discardLocal: true)
                }
            }
        }
    }

    func selected(_ m: [String: Any]) {
        line = m["line"] as? Int ?? line
        column = m["column"] as? Int ?? column
        selected = m["selected"] as? Int ?? 0
        guard m["synced"] as? Bool == true, let v = (m["version"] as? NSNumber)?.uint64Value else { return }
        let a = UInt32((m["anchor"] as? Int) ?? 0), h = UInt32((m["head"] as? Int) ?? 0)
        model?.connection?.send { $0.editorSelect = .with { $0.paneID = paneID; $0.version = v; $0.anchor = a; $0.head = h } }
    }

    func languageFound(_ name: String) {
        if name != languageName { languageName = name }
    }

    /// Out of step with the daemon: its whole buffer again.
    func resync(discardLocal: Bool = false) {
        if discardLocal { self.discardLocal = true }
        snapshot = nil
        since = []
        attach()
    }

    // MARK: Commands

    /// Saves (as path, if given). Errors come back for the caller to show;
    /// "already exists" and "changed on disk" ones can be retried with force.
    public func save(as path: String = "", force: Bool = false) async throws {
        try await command { $0.action = .save; $0.path = path; $0.force = force }
    }

    public func reload() async throws { try await command { $0.action = .reload } }
    public func keepMine() async throws { try await command { $0.action = .keepMine } }

    public func setLanguage(_ name: String) {
        Task { try? await command { $0.action = .setLanguage; $0.language = name } }
    }

    private func command(_ build: @escaping (inout Hammock_V1_EditorCommand) -> Void) async throws {
        guard let conn = model?.connection else { throw EditorError.offline }
        _ = try await conn.call { m in
            m.editorCommand = .with {
                $0.paneID = self.paneID
                build(&$0)
            }
        }
    }

    /// Shows the buffer against the file on disk (each difference can be taken or left).
    public func showDiff() async throws {
        guard let conn = model?.connection else { throw EditorError.offline }
        let r = try await conn.call { $0.readEditor = .with { $0.paneID = self.paneID; $0.disk = true } }
        bridge?.showDiff(r.editorUpdate.diskMissing ? "" : r.editorUpdate.diskText)
        showingDiff = true
    }

    public func hideDiff() {
        bridge?.hideDiff()
        showingDiff = false
    }

    /// Runs one of the page's commands: undo, redo, find, findNext,
    /// findPrevious, gotoLine, selectAll, left/right/up/down, wordLeft,
    /// wordRight, lineStart, lineEnd, tab, indent, outdent, focus, blur.
    public func run(_ name: String) { bridge?.command(name) }

    public func insert(_ text: String) { bridge?.insert(text) }

    /// The selected text (for Look Up, Services, a context menu).
    public func selectedText() async -> String { await bridge?.selectionText() ?? "" }

    public var languageNames: [String] { bridge?.languageNames ?? [] }
}

public enum EditorError: LocalizedError {
    case offline
    public var errorDescription: String? { "Not connected to Hammock" }
}

extension AppModel {
    /// The pane's editor session, attached on first use.
    public func acquireEditor(_ paneID: String) -> EditorSession {
        let s = editorSessions[paneID] ?? EditorSession(paneID: paneID, model: self)
        editorSessions[paneID] = s
        s.refs += 1
        if s.refs == 1 { s.attach() }
        return s
    }

    public func releaseEditor(_ paneID: String) {
        guard let s = editorSessions[paneID] else { return }
        s.refs -= 1
        if s.refs <= 0 {
            editorSessions[paneID] = nil
            connection?.send { $0.detachPane = .with { $0.paneID = paneID } }
        }
    }

    /// The pane's session if a view has it.
    public func editorSession(_ paneID: String) -> EditorSession? { editorSessions[paneID] }

    func reattachEditors() {
        for s in editorSessions.values { s.resync() }
    }

    /// Asks for a file list for quick open (the daemon ranks it).
    public func listFiles(groupID: String, query: String, limit: Int = 200) async throws -> Hammock_V1_FileList {
        guard let conn = connection else { throw EditorError.offline }
        return try await conn.call { $0.listFiles = .with { $0.groupID = groupID; $0.query = query; $0.limit = UInt32(limit) } }.fileList
    }

    /// Opens a file (path empty: an untitled buffer) in an editor pane: a new
    /// tab in the Group, or a split beside a pane. Errors come back to show
    /// (a large file's start with "large file:", to ask and retry with large).
    public func openEditor(_ path: String, groupID: String?, splitPane: String? = nil, direction: SplitDirection = .horizontal,
                           replacePane: String? = nil, line: Int = 0, large: Bool = false) async throws {
        guard let conn = connection else { throw EditorError.offline }
        let open = Hammock_V1_EditorOpen.with { $0.path = path; $0.line = UInt32(max(line, 0)); $0.large = large }
        if let replacePane {
            let r = try await conn.call { $0.replacePane = .with { $0.paneID = replacePane; $0.spec = .with { $0.editor = open } } }
            focus(r.created.id)
        } else if let splitPane {
            _ = try await conn.call {
                $0.createPane = .with {
                    $0.splitPaneID = splitPane
                    $0.direction = direction
                    $0.spec = .with { $0.editor = open }
                }
            }
        } else if let groupID {
            let r = try await conn.call { $0.createTab = .with { $0.groupID = groupID; $0.pane = .with { $0.editor = open } } }
            selectedTabID = r.created.id
        }
    }
}

extension Hammock_V1_Pane {
    public var isEditor: Bool { kind == .editor }
}

/// Large files are refused unless asked again.
public func isLargeFileError(_ e: Error) -> Bool { e.localizedDescription.hasPrefix("large file: ") }
