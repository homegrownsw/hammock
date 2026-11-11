import HammockKit
import SwiftUI

// Where remote access listens: the networks (Tailscale, a local network, a
// VPN) and extra addresses in Settings ▸ Devices ▸ Remote Access, and
// "Where should Hammock listen?", asked the first time something needs remote
// access (turning it on, pairing a device, linking a computer) and none of
// the chosen networks is one this Mac is on.

/// A network's symbol: Tailscale, Wi-Fi, Ethernet or a VPN.
func networkSymbol(_ n: RemoteNetwork) -> String {
    switch n.kind {
    case "tailscale": "point.3.connected.trianglepath.dotted"
    case "vpn": "lock.shield"
    default: n.label.localizedCaseInsensitiveContains("wi") ? "wifi" : "cable.connector.horizontal"
    }
}

/// One network, with a checkbox: chosen or not.
struct NetworkRow: View {
    let network: RemoteNetwork
    @Binding var isOn: Bool

    var body: some View {
        Toggle(isOn: $isOn) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Image(systemName: networkSymbol(network))
                    .foregroundStyle(network.connected ? Color.accentColor : .secondary)
                    .frame(width: 18)
                VStack(alignment: .leading, spacing: 2) {
                    Text(network.title)
                    Text(network.addresses.isEmpty ? "Not connected" : "This Mac: " + network.addresses.joined(separator: ", "))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .textSelection(.enabled)
                    if network.public {
                        Label("A public address range: anyone on the internet who can reach it could try to connect (they'd still need to be paired).",
                              systemImage: "exclamationmark.triangle.fill")
                            .font(.caption)
                            .foregroundStyle(.orange)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
        }
        .toggleStyle(.checkbox)
    }
}

/// "Where should Hammock listen?": the networks, those this Mac is on now
/// ticked, and Confirm, which saves them. done(true) once saved, false if
/// cancelled.
struct NetworkChooser: View {
    let model: AppModel
    let status: Hammock_V1_Devices
    let done: (Bool) -> Void
    @State private var chosen: Set<String>
    @State private var saving = false
    @State private var error: String?

    init(model: AppModel, status: Hammock_V1_Devices, done: @escaping (Bool) -> Void) {
        self.model = model
        self.status = status
        self.done = done
        _chosen = State(initialValue: Set(status.networks.filter { $0.enabled || $0.suggested }.map(\.id)))
    }

    /// At least one network this Mac is on now: otherwise nothing could connect.
    private var canConfirm: Bool { status.networks.contains { chosen.contains($0.id) && $0.connected } }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Where should Hammock listen?").font(.title3.weight(.bold))
                Text("Devices on the networks you choose still need to be paired or linked.")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            GroupBox {
                if status.networks.isEmpty {
                    Text("This Mac isn't on a network Hammock can listen on. Connect to Wi‑Fi, Ethernet, Tailscale or a VPN first.")
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, minHeight: 50)
                } else {
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(Array(status.networks.enumerated()), id: \.element.id) { i, n in
                            if i > 0 { Divider() }
                            NetworkRow(network: n, isOn: Binding(
                                get: { chosen.contains(n.id) },
                                set: { if $0 { chosen.insert(n.id) } else { chosen.remove(n.id) } }))
                        }
                    }
                    .padding(4)
                }
            }
            Text("Change them any time in Settings ▸ Devices ▸ Remote Access.")
                .font(.callout)
                .foregroundStyle(.secondary)
            if let error {
                Label(error, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout)
                    .foregroundStyle(.red)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                Button("Cancel") { done(false) }.keyboardShortcut(.cancelAction)
                Button("Listen on These") { Task { await confirm() } }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!canConfirm || saving)
            }
        }
    }

    private func confirm() async {
        saving = true
        defer { saving = false }
        do {
            try await model.setRemoteNetworks(status.networks.map(\.id).filter(chosen.contains), extraAddresses: status.extraAddresses)
            done(true)
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// Asks "Where should Hammock listen?" (as a sheet, with .networkGate) when
/// remote access has no chosen network this Mac is on now.
@MainActor
@Observable
final class NetworkGate {
    private(set) var asking = false
    /// What the sheet shows (kept while it closes).
    private(set) var status = Hammock_V1_Devices()
    @ObservationIgnored private var waiting: CheckedContinuation<Bool, Never>?

    /// True once remote access has a network to listen on, asking first if
    /// it hasn't; false if the person cancelled. If the daemon can't say (or
    /// this Mac is on no network at all), true: what's asked next says why.
    func ensure(_ model: AppModel) async -> Bool {
        guard let s = try? await model.deviceStatus(), s.needsNetworkChoice, !s.networks.isEmpty else { return true }
        finish(false)
        return await withCheckedContinuation { c in
            waiting = c
            status = s
            asking = true
        }
    }

    func finish(_ ok: Bool) {
        asking = false
        let c = waiting
        waiting = nil
        c?.resume(returning: ok)
    }
}

extension View {
    /// The gate's "Where should Hammock listen?" sheet.
    func networkGate(_ gate: NetworkGate, model: AppModel) -> some View {
        sheet(isPresented: Binding(get: { gate.asking }, set: { if !$0 { gate.finish(false) } })) {
            NetworkChooser(model: model, status: gate.status) { gate.finish($0) }
                .padding(22)
                .frame(width: 480)
        }
    }
}

/// Settings ▸ Devices: open at Remote Access (Remote Access Networks…).
@MainActor
@Observable
final class DevicesSettingsFocus {
    static let shared = DevicesSettingsFocus()
    /// Bumped to scroll Settings ▸ Devices to Remote Access.
    var remoteAccess = 0

    func showRemoteAccess(_ openSettings: OpenSettingsAction) {
        SettingsTab.select(.devices)
        remoteAccess += 1
        openSettings()
    }
}

/// Settings ▸ Devices ▸ Remote Access: the networks remote access listens
/// on, and the extra addresses given out with this Mac's own.
struct RemoteNetworksSettings: View {
    let model: AppModel
    let status: Hammock_V1_Devices
    /// Called once a change is saved, to read the daemon's lists again.
    let reload: () async -> Void
    @State private var newAddress = ""
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Networks Hammock listens on").font(.headline)
            if status.networks.isEmpty {
                Text("This Mac isn't on a network Hammock can listen on.").foregroundStyle(.secondary)
            }
            ForEach(status.networks, id: \.id) { n in
                NetworkRow(network: n, isOn: Binding(get: { n.enabled }, set: { on in
                    let ids = status.networks.filter { $0.id == n.id ? on : $0.enabled }.map(\.id)
                    save(ids, status.extraAddresses)
                }))
            }
            if model.remoteEnabled && !status.listensSomewhere {
                Label("Remote access is on, but this Mac isn't on any of the networks you chose, so nothing can reach it now.",
                      systemImage: "exclamationmark.triangle.fill")
                    .font(.callout)
                    .foregroundStyle(.orange)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        VStack(alignment: .leading, spacing: 6) {
            Text("Extra addresses").font(.headline)
            Text("Added to pairing codes and links: a DNS name, VPN endpoint or forwarded port (host or host:port).")
                .font(.callout)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            ForEach(status.extraAddresses, id: \.self) { a in
                HStack {
                    Text(a).font(.body.monospaced()).textSelection(.enabled)
                    Spacer()
                    Button {
                        save(enabledIDs, status.extraAddresses.filter { $0 != a })
                    } label: {
                        Image(systemName: "minus.circle")
                    }
                    .buttonStyle(.borderless)
                    .help("Remove \(a)")
                    .accessibilityLabel("Remove \(a)")
                }
            }
            HStack {
                TextField("Address", text: $newAddress, prompt: Text("mac.example.com or 203.0.113.5:7433"))
                    .labelsHidden()
                    .textFieldStyle(.roundedBorder)
                    .onSubmit(add)
                Button(action: add) { Image(systemName: "plus") }
                    .help("Add this address")
                    .accessibilityLabel("Add address")
                    .disabled(!valid(newAddress))
            }
        }
        if let error {
            Label(error, systemImage: "exclamationmark.triangle.fill")
                .font(.callout)
                .foregroundStyle(.red)
        }
    }

    private var enabledIDs: [String] { status.networks.filter(\.enabled).map(\.id) }

    private func valid(_ s: String) -> Bool {
        let t = s.trimmingCharacters(in: .whitespaces)
        return !t.isEmpty && !t.contains(where: \.isWhitespace) && !status.extraAddresses.contains(t)
    }

    private func add() {
        guard valid(newAddress) else { return }
        let a = newAddress.trimmingCharacters(in: .whitespaces)
        newAddress = ""
        save(enabledIDs, status.extraAddresses + [a])
    }

    private func save(_ ids: [String], _ extra: [String]) {
        Task {
            do {
                try await model.setRemoteNetworks(ids, extraAddresses: extra)
                error = nil
            } catch {
                self.error = error.localizedDescription
            }
            await reload()
        }
    }
}
