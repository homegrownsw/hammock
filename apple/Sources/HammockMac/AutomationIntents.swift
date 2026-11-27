import Foundation
import HammockKit

/// The app's side of Shortcuts' Run Hammock Automation
/// (RunAutomationIntent.swift): the automations it can pick, and running
/// one through the daemon, as hmk trigger does.
@MainActor
enum AutomationIntents {
    static func attach(_ model: AppModel) {
        AutomationHooks.list = { [weak model] in
            guard let model, model.connected else { return nil }
            return model.schedules.values
                .sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
                .map { s in
                    let c = model.computer(of: s)
                    let elsewhere = c.map { !$0.thisComputer } ?? false
                    return AutomationEntity(id: s.id, name: s.name, computer: elsewhere ? (c?.name ?? "") : "")
                }
        }
        AutomationHooks.trigger = { [weak model] id, text, label, files in
            guard let model else { return "Failed: Hammock isn't running" }
            for _ in 0..<50 where !model.connected { try? await Task.sleep(for: .milliseconds(200)) }
            guard model.connected else { return "Failed: Hammock isn't connected to its daemon" }
            guard let s = model.schedules[id] else { return "Failed: there's no such automation (deleted?)" }
            if !files.isEmpty, let c = model.computer(of: s), !c.thisComputer {
                return "Failed: files can only go to an automation on this Mac"
            }
            return await resultLine { try await model.triggerSchedule(id, source: "shortcuts", text: text, label: label, files: files) }
        }
    }

    /// The action's result: "Started", "Queued: why", "Skipped: why",
    /// "Failed: why", and whether the text was cut.
    static func resultLine(_ call: () async throws -> (outcome: AppModel.TriggerOutcome, cut: Bool)) async -> String {
        do {
            let r = try await call()
            return r.outcome.line(cut: r.cut)
        } catch {
            return "Failed: " + error.localizedDescription
        }
    }
}
