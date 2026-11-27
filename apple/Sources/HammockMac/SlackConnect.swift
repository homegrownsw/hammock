import AppKit
import HammockKit
import Observation
import SwiftUI

/// Slack for Slack triggers (the daemon's slack.go): connecting your own
/// Slack app (made from Hammock's manifest) to a computer of yours, which
/// then holds the connection. Reached only from Add Trigger ▸ Messages ▸
/// Slack and a Slack trigger's row, not Settings.
@MainActor
@Observable
final class SlackUI {
    static let shared = SlackUI()

    /// The Connect sheet: on which computer ("": this one), and whether it's
    /// changing the tokens of a connection already there.
    struct Request: Identifiable {
        var computerID: String
        var change = false
        var id: String { computerID + (change ? "/change" : "") }
    }

    var request: Request?

    /// Connect Slack… on computerID.
    func connect(on computerID: String, change: Bool = false) { request = Request(computerID: computerID, change: change) }

    /// Disconnect…: asks, then forgets the tokens on the computer that has them.
    func disconnect(_ model: AppModel) {
        guard let holder = model.slackHolder else { return }
        let alert = NSAlert()
        alert.messageText = "Disconnect Slack from \(holder.link.team)?"
        alert.informativeText = "Hammock forgets your Slack app’s tokens, and Slack triggers stop firing until you connect again."
        alert.addButton(withTitle: "Disconnect").hasDestructiveAction = true
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        Task {
            do { _ = try await model.slackCommand(.disconnect, on: holder.computer.userID) } catch { Self.fail(error) }
        }
    }

    /// Slack is held by another computer: asks to move it to computerID.
    /// true: it's moving (or nothing needed it).
    @discardableResult
    func offerMove(_ model: AppModel, to computerID: String) -> Bool {
        let target = computerID.isEmpty ? model.myUserID : computerID
        guard let holder = model.slackHolder, holder.computer.userID != target else { return true }
        let here = model.computer(target).map { $0.thisComputer ? "this Mac" : $0.name } ?? "this Mac"
        let alert = NSAlert()
        alert.messageText = "Slack is connected on \(holder.computer.name)"
        alert.informativeText = "One computer listens to Slack. Move it to \(here)? Slack triggers on \(holder.computer.name) stop firing."
        alert.addButton(withTitle: "Move to \(here.prefix(1).uppercased() + here.dropFirst())")
        alert.addButton(withTitle: "Not Now")
        guard alert.runModal() == .alertFirstButtonReturn else { return false }
        Task {
            do { _ = try await model.slackCommand(.moveHere, on: computerID, from: holder.computer.userID) } catch { Self.fail(error) }
        }
        return true
    }

    static func fail(_ error: Error) {
        let alert = NSAlert()
        alert.messageText = "Slack"
        alert.informativeText = error.localizedDescription
        alert.runModal()
    }
}

/// Connect Slack…: make the app from Hammock's manifest, install it, and
/// paste its two tokens; they're checked with Slack before they're kept.
struct SlackConnectSheet: View {
    let model: AppModel
    let request: SlackUI.Request
    @Environment(\.dismiss) private var dismiss
    @State private var userToken = ""
    @State private var appToken = ""
    @State private var manifestURL: URL?
    @State private var checking = false
    @State private var error: String?
    @State private var connected: Hammock_V1_SlackLink? = SlackConnectSheet.debugConnected
    /// Made-up state for HMK_DEBUG_SLACK (test data only).
    nonisolated(unsafe) static var debugConnected: Hammock_V1_SlackLink?

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 10) {
                Image(systemName: "number.square.fill").font(.system(size: 28)).foregroundStyle(Color.themeAccent)
                Text(request.change ? "Change Slack Tokens" : "Connect Slack").font(.title2.weight(.semibold))
            }
            if let connected {
                Label("Connected to \(connected.team) as \(connected.user)", systemImage: "checkmark.circle.fill")
                    .foregroundStyle(.green)
                Text(connected.url).foregroundStyle(.secondary).textSelection(.enabled)
            } else {
                steps
            }
            if let error {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                if connected != nil {
                    Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
                } else {
                    if checking { ProgressView().controlSize(.small) }
                    Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                    Button("Connect") { Task { await connect() } }
                        .keyboardShortcut(.defaultAction)
                        .disabled(checking || userToken.trimmingCharacters(in: .whitespaces).isEmpty || appToken.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
        }
        .padding(22)
        .frame(width: 500)
        .task {
            if connected != nil { return }
            if let r = try? await model.slackCommand(.status, on: request.computerID) { manifestURL = URL(string: r.manifestURL) }
        }
    }

    private var steps: some View {
        VStack(alignment: .leading, spacing: 14) {
            if !request.change {
                step(1, "Make your Slack app from Hammock’s manifest, then install it to your workspace.") {
                    Button("Create Slack App…") { if let manifestURL { NSWorkspace.shared.open(manifestURL) } }
                        .disabled(manifestURL == nil)
                        .help("Opens api.slack.com with the app filled in: it only reads, as you")
                }
            }
            step(request.change ? 1 : 2, "Copy its User OAuth Token, under OAuth & Permissions.") {
                SecureField("User OAuth Token", text: $userToken, prompt: Text("xoxp-…"))
                    .font(.system(.body, design: .monospaced))
            }
            step(request.change ? 2 : 3, "Under Basic Information ▸ App-Level Tokens, generate a token with the connections:write scope, and copy it.") {
                SecureField("App-level token", text: $appToken, prompt: Text("xapp-…"))
                    .font(.system(.body, design: .monospaced))
            }
        }
        .labelsHidden()
    }

    private func step<Content: View>(_ n: Int, _ text: String, @ViewBuilder content: () -> Content) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Text("\(n)")
                .font(.callout.weight(.semibold).monospacedDigit())
                .foregroundStyle(Color.themeAccent)
                .frame(width: 22, height: 22)
                .background(Color.themeAccent.opacity(0.13), in: Circle())
            VStack(alignment: .leading, spacing: 7) {
                Text(text).fixedSize(horizontal: false, vertical: true)
                content()
            }
        }
    }

    private func connect() async {
        checking = true
        error = nil
        defer { checking = false }
        do {
            let r = try await model.slackCommand(.connect, on: request.computerID,
                                                 userToken: userToken.trimmingCharacters(in: .whitespacesAndNewlines),
                                                 appToken: appToken.trimmingCharacters(in: .whitespacesAndNewlines))
            userToken = ""
            appToken = ""
            connected = r.link
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// Under a Slack trigger: the workspace it listens to (and on which
/// computer), with Change Tokens… and Disconnect…; or Connect Slack….
struct SlackLinkLine: View {
    let model: AppModel
    /// The automation's computer ("": this one).
    let computerID: String

    var body: some View {
        HStack(spacing: 6) {
            if let holder = model.slackHolder {
                let elsewhere = holder.computer.userID != (computerID.isEmpty ? model.myUserID : computerID)
                Text(elsewhere ? "Slack: \(holder.link.team), on \(holder.computer.name)" : "Slack: \(holder.link.team)")
                    .foregroundStyle(.secondary)
                Menu {
                    if elsewhere {
                        Button("Move Slack Here…") { SlackUI.shared.offerMove(model, to: computerID) }
                        Divider()
                    }
                    Button("Change Tokens…") { SlackUI.shared.connect(on: holder.computer.userID, change: true) }
                    Button("Disconnect…") { SlackUI.shared.disconnect(model) }
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuStyle(.borderlessButton)
                .menuIndicator(.hidden)
                .fixedSize()
                .help("Slack’s connection")
            } else {
                Button("Connect Slack…") { SlackUI.shared.connect(on: computerID) }
                    .buttonStyle(ThemeLinkStyle())
            }
        }
        .font(.caption)
    }
}
