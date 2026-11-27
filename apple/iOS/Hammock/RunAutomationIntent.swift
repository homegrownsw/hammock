import AppIntents
import Foundation
import HammockKit

// Shortcuts' Run Hammock Automation, on the iPhone: starts one of your
// automations on a paired Mac as an event, with what happened (fenced as
// data in its run), over remote access, as the app reaches its Mac. It runs
// automations, never adds, changes or deletes them, and doesn't ask for
// Face ID (a Shortcuts automation can run it with the phone locked).

/// A paired Mac.
struct MacEntity: AppEntity {
    static let typeDisplayRepresentation: TypeDisplayRepresentation = "Mac"
    static let defaultQuery = MacQuery()

    let id: String
    let name: String

    var displayRepresentation: DisplayRepresentation { DisplayRepresentation(title: "\(name)") }
}

struct MacQuery: EntityQuery {
    func entities(for identifiers: [MacEntity.ID]) async throws -> [MacEntity] {
        await PhoneAutomations.macs().filter { identifiers.contains($0.id) }
    }

    func suggestedEntities() async throws -> [MacEntity] { await PhoneAutomations.macs() }
}

/// An automation on a paired Mac (its id says which).
struct PhoneAutomationEntity: AppEntity {
    static let typeDisplayRepresentation: TypeDisplayRepresentation = "Hammock Automation"
    static let defaultQuery = PhoneAutomationQuery()

    let id: String
    let name: String
    let mac: String

    var displayRepresentation: DisplayRepresentation { DisplayRepresentation(title: "\(name)", subtitle: "\(mac)") }
}

struct PhoneAutomationQuery: EntityStringQuery {
    @IntentParameterDependency<RunAutomationIntent>(\.$mac) var intent

    func entities(for identifiers: [PhoneAutomationEntity.ID]) async throws -> [PhoneAutomationEntity] {
        // Each keeps what it was saved as: asking every Mac to run a shortcut would be slow.
        identifiers.compactMap { id in
            PhoneAutomations.split(id).map { PhoneAutomationEntity(id: id, name: PhoneAutomations.savedName(id) ?? "Automation", mac: $0.host) }
        }
    }

    func suggestedEntities() async throws -> [PhoneAutomationEntity] {
        try await PhoneAutomations.automations(on: intent?.mac.id)
    }

    func entities(matching string: String) async throws -> [PhoneAutomationEntity] {
        try await suggestedEntities().filter { $0.name.localizedCaseInsensitiveContains(string) }
    }
}

struct RunAutomationIntent: AppIntent {
    static let title: LocalizedStringResource = "Run Hammock Automation"
    static let description = IntentDescription("Runs one of your Hammock automations on a paired Mac, with what happened. Its result is Started, Queued (and why), Skipped (and why) or Failed (and why).")
    static let openAppWhenRun = false

    @Parameter(title: "Mac", description: "The Mac to choose its automations from (all of them, if none).")
    var mac: MacEntity?

    @Parameter(title: "Automation")
    var automation: PhoneAutomationEntity

    @Parameter(title: "Text", description: "What happened, given to the run as data (up to 64 KB).", inputConnectionBehavior: .connectToPreviousIntentResult)
    var text: String?

    @Parameter(title: "What Happened", description: "A short label for it, such as “New message”.")
    var label: String?

    static var parameterSummary: some ParameterSummary {
        Summary("Run \(\.$automation) with \(\.$text)") {
            \.$mac
            \.$label
        }
    }

    init() {}

    func perform() async throws -> some IntentResult & ReturnsValue<String> & ProvidesDialog {
        let result = await PhoneAutomations.run(automation.id, text: text ?? "", label: label ?? "")
        return .result(value: result, dialog: "\(result)")
    }
}

/// The app's side of the action: its paired Macs, their automations, and
/// running one, each over a brief connection of its own.
@MainActor
enum PhoneAutomations {
    nonisolated private static let namesKey = "shortcuts.automationNames"

    nonisolated static func split(_ id: String) -> (host: String, schedule: String)? {
        let parts = id.split(separator: "\u{1F}", maxSplits: 1).map(String.init)
        return parts.count == 2 ? (parts[0], parts[1]) : nil
    }

    nonisolated static func savedName(_ id: String) -> String? {
        (UserDefaults.standard.dictionary(forKey: namesKey) as? [String: String])?[id]
    }

    private static func saveNames(_ list: [PhoneAutomationEntity]) {
        var names = (UserDefaults.standard.dictionary(forKey: namesKey) as? [String: String]) ?? [:]
        for a in list { names[a.id] = a.name }
        UserDefaults.standard.set(names, forKey: namesKey)
    }

    static func macs() -> [MacEntity] {
        (Push.shared.session?.hosts ?? []).filter { $0.deviceID != nil }.map { MacEntity(id: $0.id, name: $0.hostName) }
    }

    /// A connection to a paired Mac with its state in: the app's own if it's
    /// that Mac's, else a brief one (stop it after, if made).
    private static func connection(to hostID: String) async throws -> (model: AppModel, brief: Bool) {
        guard let session = Push.shared.session,
              let h = session.hosts.first(where: { $0.id == hostID }) else { throw Failure("That Mac isn't paired with this iPhone") }
        if session.host?.id == hostID, let m = session.model, m.connected, m.hasFullState { return (m, false) }
        guard let m = session.briefConnection(to: h) else { throw Failure("That Mac isn't paired with this iPhone") }
        for _ in 0..<75 where !(m.connected && m.hasFullState) { try? await Task.sleep(for: .milliseconds(200)) }
        guard m.connected, m.hasFullState else {
            m.stop()
            throw Failure("Couldn’t reach \(h.hostName): it may be asleep, or remote access is off")
        }
        return (m, true)
    }

    static func automations(on hostID: String?) async throws -> [PhoneAutomationEntity] {
        let hosts = (Push.shared.session?.hosts ?? []).filter { $0.deviceID != nil && (hostID == nil || $0.id == hostID) }
        var out: [PhoneAutomationEntity] = []
        var lastError: Error?
        for h in hosts {
            do {
                let (m, brief) = try await connection(to: h.id)
                defer { if brief { m.stop() } }
                out += m.schedules.values
                    .sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
                    .map { PhoneAutomationEntity(id: h.id + "\u{1F}" + $0.id, name: $0.name, mac: h.hostName) }
            } catch {
                lastError = error
            }
        }
        if out.isEmpty, let lastError { throw lastError }
        saveNames(out)
        return out
    }

    /// The result line: "Started", "Queued: why", "Skipped: why", "Failed: why".
    static func run(_ id: String, text: String, label: String) async -> String {
        guard let (host, schedule) = split(id) else { return "Failed: choose the automation again" }
        let running = BackgroundTime("Run a Hammock automation")
        defer { running.end() }
        let conn: (model: AppModel, brief: Bool)
        do { conn = try await connection(to: host) } catch { return "Failed: " + error.localizedDescription }
        defer { if conn.brief { conn.model.stop() } }
        do {
            let r = try await conn.model.triggerSchedule(schedule, source: "shortcuts", text: text, label: label)
            return r.outcome.line(cut: r.cut)
        } catch {
            return "Failed: " + error.localizedDescription
        }
    }

    struct Failure: LocalizedError {
        let message: String
        init(_ m: String) { message = m }
        var errorDescription: String? { message }
    }
}
