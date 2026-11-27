import AppIntents
import Foundation

// Shortcuts' Run Hammock Automation, on the Mac: starts one of your
// automations (on this Mac, or a computer linked to it) as an event, with
// what happened (fenced as data in its run). It runs automations, never
// adds, changes or deletes them. This file uses only AppIntents and
// Foundation: scripts/build-mac-app.sh compiles it on its own to make the
// action's metadata (Metadata.appintents), as SwiftPM doesn't. The app
// does the work (AutomationIntents.swift), through the hooks below.

/// An automation, for Shortcuts to pick: its computer under its name when
/// it isn't this Mac.
struct AutomationEntity: AppEntity {
    static let typeDisplayRepresentation: TypeDisplayRepresentation = "Hammock Automation"
    static let defaultQuery = AutomationQuery()

    let id: String
    let name: String
    let computer: String

    var displayRepresentation: DisplayRepresentation {
        computer.isEmpty ? DisplayRepresentation(title: "\(name)") : DisplayRepresentation(title: "\(name)", subtitle: "\(computer)")
    }
}

struct AutomationQuery: EntityStringQuery {
    func entities(for identifiers: [AutomationEntity.ID]) async throws -> [AutomationEntity] {
        let all = try await AutomationHooks.all()
        return identifiers.compactMap { id in all.first { $0.id == id } }
    }

    func suggestedEntities() async throws -> [AutomationEntity] {
        try await AutomationHooks.all()
    }

    func entities(matching string: String) async throws -> [AutomationEntity] {
        try await AutomationHooks.all().filter { $0.name.localizedCaseInsensitiveContains(string) }
    }
}

struct RunAutomationIntent: AppIntent {
    static let title: LocalizedStringResource = "Run Hammock Automation"
    static let description = IntentDescription("Runs one of your Hammock automations, with what happened. Its result is Started, Queued (and why), Skipped (and why) or Failed (and why).")
    static let openAppWhenRun = false

    @Parameter(title: "Automation")
    var automation: AutomationEntity

    @Parameter(title: "Text", description: "What happened, given to the run as data (up to 64 KB).", inputConnectionBehavior: .connectToPreviousIntentResult)
    var text: String?

    @Parameter(title: "What Happened", description: "A short label for it, such as “New message”.")
    var label: String?

    @Parameter(title: "Files", description: "Files for the run, on this Mac.", supportedContentTypes: [.item])
    var files: [IntentFile]?

    static var parameterSummary: some ParameterSummary {
        Summary("Run \(\.$automation) with \(\.$text)") {
            \.$label
            \.$files
        }
    }

    init() {}

    func perform() async throws -> some IntentResult & ReturnsValue<String> & ProvidesDialog {
        var paths: [String] = []
        for f in files ?? [] {
            paths.append(try AutomationHooks.keep(f))
        }
        let result = await AutomationHooks.run(automation.id, text ?? "", label ?? "", paths)
        return .result(value: result, dialog: "\(result)")
    }
}

/// What the app does for the action (set as it launches); the action waits
/// for them a little while the app starts.
enum AutomationHooks {
    struct NotReady: LocalizedError {
        var errorDescription: String? { "Hammock isn't connected to its daemon yet" }
    }

    @MainActor static var list: (() async -> [AutomationEntity]?)?
    @MainActor static var trigger: ((String, String, String, [String]) async -> String)?

    static func all() async throws -> [AutomationEntity] {
        for _ in 0..<50 {
            if let l = await list, let got = await l() { return got }
            try? await Task.sleep(for: .milliseconds(200))
        }
        throw NotReady()
    }

    /// The result line: "Started", "Queued: why", "Skipped: why", "Failed: why".
    static func run(_ id: String, _ text: String, _ label: String, _ files: [String]) async -> String {
        for _ in 0..<50 {
            if let t = await trigger { return await t(id, text, label, files) }
            try? await Task.sleep(for: .milliseconds(200))
        }
        return "Failed: Hammock didn't start"
    }

    /// Keeps a file Shortcuts gave, where the daemon can read it, for a few
    /// days: its path.
    static func keep(_ f: IntentFile) throws -> String {
        let fm = FileManager.default
        let root = fm.urls(for: .cachesDirectory, in: .userDomainMask)[0]
            .appendingPathComponent(Bundle.main.bundleIdentifier ?? "Hammock").appendingPathComponent("Shortcuts Files")
        if let old = try? fm.contentsOfDirectory(at: root, includingPropertiesForKeys: [.creationDateKey]) {
            for d in old where ((try? d.resourceValues(forKeys: [.creationDateKey]))?.creationDate ?? .distantFuture) < Date().addingTimeInterval(-7 * 86_400) {
                try? fm.removeItem(at: d)
            }
        }
        let dir = root.appendingPathComponent(UUID().uuidString)
        try fm.createDirectory(at: dir, withIntermediateDirectories: true)
        let name = (f.filename as NSString).lastPathComponent
        let url = dir.appendingPathComponent(name.isEmpty || name == "." || name == ".." ? "file" : name)
        try f.data.write(to: url)
        return url.path
    }
}
