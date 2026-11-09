import Foundation
import Observation

public typealias LogEntry = Hammock_V1_LogEntry
public typealias LogLevel = Hammock_V1_LogLevel
public typealias LogFacet = Hammock_V1_LogFacet
public typealias LogSource = Hammock_V1_LogSource

/// One log pane's view: the entries the daemon sends for the filter set
/// here (the daemon filters; this keeps the newest few thousand). Shared by
/// the Mac and iOS apps.
@MainActor
@Observable
public final class LogSession {
    public let paneID: String
    public private(set) var entries: [LogEntry] = []
    public private(set) var facets: [LogFacet] = []
    /// Older matching entries the daemon still has.
    public private(set) var hasMore = false
    public private(set) var total: Int64 = 0
    public private(set) var loadingOlder = false
    public private(set) var loaded = false
    public private(set) var error = ""

    /// The filter. Changing it asks the daemon again (text after a pause).
    public private(set) var text = ""
    public private(set) var levels: Set<LogLevel> = []
    public private(set) var only: [String] = []
    public private(set) var hide: [String] = []

    /// Bumped when entries arrive at the end (views scroll to follow).
    public private(set) var appended = 0

    weak var model: AppModel?
    var refs = 0
    /// Waiting for the page of a new filter: entries for the old one still
    /// on their way are dropped.
    @ObservationIgnored private var awaiting: Hammock_V1_LogFilter?
    @ObservationIgnored private var textTask: Task<Void, Never>?

    /// The most kept here; the oldest go (and can be paged back in).
    public static let cap = 5000

    init(paneID: String, model: AppModel) {
        self.paneID = paneID
        self.model = model
    }

    public var filter: Hammock_V1_LogFilter {
        .with {
            $0.text = text
            $0.levels = levels.sorted { $0.rawValue < $1.rawValue }
            $0.only = only
            $0.hide = hide
        }
    }

    public var isFiltered: Bool { !text.isEmpty || !levels.isEmpty || !only.isEmpty || !hide.isEmpty }

    func watch() {
        let f = filter
        awaiting = f
        model?.connection?.send { $0.watchLog = .with { $0.paneID = paneID; $0.watch = true; $0.filter = f; $0.limit = 500 } }
    }

    func apply(_ v: Hammock_V1_LogView) {
        if !v.error.isEmpty { error = v.error }
        if !v.facets.isEmpty || v.replace { if v.facets != facets { facets = v.facets } }
        if v.total > 0 || v.replace { total = v.total }
        if v.replace {
            // Only the page for the filter asked for last.
            if let a = awaiting, v.filter != a { return }
            awaiting = nil
            entries = v.entries
            hasMore = v.hasMore_p
            loaded = true
            appended += 1
            return
        }
        guard awaiting == nil else { return }
        if v.older {
            loadingOlder = false
            guard v.filter == filter else { return }
            entries = v.entries + entries
            hasMore = v.hasMore_p
            return
        }
        guard !v.entries.isEmpty else { return }
        var es = entries
        es.append(contentsOf: v.entries)
        if es.count > Self.cap + Self.cap / 5 {
            es.removeFirst(es.count - Self.cap)
            hasMore = true
        }
        entries = es
        appended += 1
    }

    // MARK: Filtering

    public func setText(_ t: String) {
        guard t != text else { return }
        text = t
        textTask?.cancel()
        textTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(200))
            guard !Task.isCancelled else { return }
            self?.watch()
        }
    }

    public func toggleLevel(_ l: LogLevel) {
        if levels.contains(l) { levels.remove(l) } else { levels.insert(l) }
        watch()
    }

    public func setLevels(_ ls: Set<LogLevel>) {
        levels = ls
        watch()
    }

    /// Show only entries with key ("process:Safari"), or stop.
    public func toggleOnly(_ key: String) {
        hide.removeAll { $0 == key }
        if let i = only.firstIndex(of: key) { only.remove(at: i) } else { only.append(key) }
        watch()
    }

    /// Hide entries with key, or show them again.
    public func toggleHide(_ key: String) {
        only.removeAll { $0 == key }
        if let i = hide.firstIndex(of: key) { hide.remove(at: i) } else { hide.append(key) }
        watch()
    }

    public func clearFilters() {
        text = ""
        levels = []
        only = []
        hide = []
        watch()
    }

    public func loadOlder() {
        guard hasMore, !loadingOlder, let first = entries.first else { return }
        loadingOlder = true
        let f = filter
        model?.connection?.send { $0.watchLog = .with { $0.paneID = paneID; $0.watch = true; $0.filter = f; $0.beforeSeq = first.seq; $0.limit = 500 } }
    }

    /// Forget the entries held for the pane (the terminal keeps its output).
    public func clearLog() {
        model?.connection?.send { $0.clearLog_p = .with { $0.paneID = paneID } }
    }

    /// The facets of one kind ("process", "tag", "level"), most first.
    public func facets(_ kind: String) -> [(name: String, key: String, count: Int64)] {
        facets.compactMap { f in
            let parts = f.key.split(separator: ":", maxSplits: 1).map(String.init)
            guard parts.count == 2, parts[0] == kind else { return nil }
            return (parts[1], f.key, f.count)
        }
    }

    /// The levels the log has, lowest first.
    public var presentLevels: [LogLevel] {
        facets("level").compactMap { LogLevel(name: $0.name) }.sorted { $0.rawValue < $1.rawValue }
    }
}

extension LogLevel {
    public init?(name: String) {
        switch name {
        case "debug": self = .debug
        case "info": self = .info
        case "notice": self = .notice
        case "warning": self = .warning
        case "error": self = .error
        case "fault": self = .fault
        default: return nil
        }
    }

    public var label: String {
        switch self {
        case .debug: "Debug"
        case .notice: "Notice"
        case .warning: "Warning"
        case .error: "Error"
        case .fault: "Fault"
        default: "Info"
        }
    }

    /// As a column shows it.
    public var short: String {
        switch self {
        case .debug: "DEBUG"
        case .notice: "NOTE"
        case .warning: "WARN"
        case .error: "ERROR"
        case .fault: "FAULT"
        default: "INFO"
        }
    }
}

extension Hammock_V1_Pane {
    /// A log pane: a terminal whose output is shown as a log.
    public var isLog: Bool { hasLog }
}

extension AppModel {
    /// The pane's log view, watching on first use.
    public func acquireLog(_ paneID: String) -> LogSession {
        let s = logSessions[paneID] ?? LogSession(paneID: paneID, model: self)
        logSessions[paneID] = s
        s.refs += 1
        if s.refs == 1 { s.watch() }
        return s
    }

    public func releaseLog(_ paneID: String) {
        guard let s = logSessions[paneID] else { return }
        s.refs -= 1
        if s.refs <= 0 {
            logSessions[paneID] = nil
            connection?.send { $0.watchLog = .with { $0.paneID = paneID; $0.watch = false } }
        }
    }

    func rewatchLogs() {
        for s in logSessions.values { s.watch() }
    }

}
