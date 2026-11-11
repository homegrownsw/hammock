import AppKit
import HammockKit
import SwiftUI

/// Settings ▸ Devices ▸ Screen Sharing (ADR-0014): whether your iPhone and
/// other Macs may see and control this Mac's screen (screen-sharing in the
/// config file, read by the daemon), and the permissions macOS needs to
/// give Hammock Screen Sharing for it.
struct ScreenSharingSection: View {
    let model: AppModel
    @State private var screen = false // Screen Recording allowed
    @State private var input = false  // Accessibility allowed
    @State private var checked = false

    private var on: Bool { AppConfig.shared.screenSharing }

    var body: some View {
        Section {
            Toggle(isOn: Binding(get: { on }, set: { AppConfig.shared.set("screen-sharing", $0 ? "true" : nil) })) {
                Text("Let your iPhone and other Macs see and control this Mac’s screen")
                Text("Never guests or agents. Your iPhone asks for Face ID first.")
            }
            if on {
                // Accessibility first: macOS has Hammock Screen Sharing quit and
                // reopen after Screen Recording is allowed.
                step(done: input, title: "Allow Accessibility",
                     detail: "So clicks and typing from your devices reach this Mac.", enabled: true) {
                    await ask("input")
                }
                step(done: screen, title: "Allow Screen Recording",
                     detail: "So your devices can see the screen. macOS lists it as \(Bundle.main.object(forInfoDictionaryKey: "CFBundleName") as? String ?? "Hammock").", enabled: input) {
                    await ask("screen")
                }
            }
        } header: {
            Text("Screen Sharing")
        } footer: {
            if on {
                Text("After a restart, log in at the Mac first.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        }
        .task(id: on) { await refresh() }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await refresh() }
        }
    }

    private func step(done: Bool, title: String, detail: String, enabled: Bool, action: @escaping () async -> Void) -> some View {
        HStack(spacing: 12) {
            Group {
                if checked {
                    Image(systemName: done ? "checkmark.circle.fill" : "circle")
                        .font(.title3)
                        .foregroundStyle(done ? .green : .secondary)
                        .accessibilityLabel(done ? "Done" : "Not done")
                } else {
                    // The helper starts on the first check, which takes a moment.
                    ProgressView().controlSize(.small).accessibilityLabel("Checking")
                }
            }
            .frame(width: 22)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                Text(detail).font(.callout).foregroundStyle(.secondary)
            }
            Spacer()
            if done {
                Text("Allowed").foregroundStyle(.secondary)
            } else {
                Button("Allow…") { Task { await action() } }
                    .buttonStyle(.borderedProminent)
                    .disabled(!enabled)
            }
        }
    }

    private func refresh() async {
        guard on else { return }
        // The daemon reads the config file on each request: give the
        // change a moment to be written.
        try? await Task.sleep(for: .milliseconds(200))
        apply(await model.listDisplays())
    }

    private func ask(_ what: String) async {
        apply(await model.listDisplays(requestAccess: what))
    }

    private func apply(_ r: Hammock_V1_Displays?) {
        guard let r else { return }
        checked = true
        screen = r.screenRecording
        input = r.accessibility
    }
}
