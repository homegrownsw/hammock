import CoreImage.CIFilterBuiltins
import HammockKit
import SwiftUI

/// Settings ▸ Devices: remote access (on the networks chosen: Tailscale, a
/// local network, a VPN), pairing and revocation.
struct DevicesSettings: View {
    let model: AppModel
    @State private var remote = false
    @State private var pairing: Pairing?
    @State private var status = Hammock_V1_Devices()
    @State private var error: String?
    @State private var gate = NetworkGate()
    private var focus = DevicesSettingsFocus.shared

    /// sample: what to show before the daemon's asked (debug snapshots).
    init(model: AppModel, sample: Hammock_V1_Devices? = nil) {
        self.model = model
        if let sample { _status = State(initialValue: sample) }
    }

    var body: some View {
        ScrollViewReader { proxy in
            form
                .onChange(of: focus.remoteAccess, initial: true) { _, n in
                    guard n > 0 else { return }
                    DispatchQueue.main.async {
                        withAnimation { proxy.scrollTo("remote-access", anchor: .top) }
                        focus.remoteAccess = 0 // once: not every time Devices shows
                    }
                }
        }
        .networkGate(gate, model: model)
    }

    private var form: some View {
        Form {
            SettingsPaneHeader(tab: .devices)
            YourComputersSection(model: model)
            Section {
                Toggle("Allow paired devices to connect remotely", isOn: $remote)
                    .settingAnchor("devices.remote")
                    // Linked computers reach this one through it too.
                    .disabled(remote && model.hasLinkedComputers)
                    .onChange(of: remote) { _, on in
                        guard on != model.remoteEnabled else { return }
                        Task {
                            // The first time: where to listen.
                            if on, !(await gate.ensure(model)) {
                                remote = false
                                await reload()
                                return
                            }
                            do {
                                try await model.setRemoteAccess(on)
                                error = nil
                            } catch {
                                self.error = error.localizedDescription
                                remote = model.remoteEnabled
                            }
                            await reload()
                        }
                    }
                // Why it didn't turn on (or a pairing code couldn't be made), by the switch.
                if let error {
                    Label(error, systemImage: "exclamationmark.triangle.fill")
                        .font(.callout)
                        .foregroundStyle(.red)
                }
                Text("Your iPhone connects directly to this Mac on the networks below, encrypted and pinned to this Mac's certificate. Anything a paired device does is logged.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                if model.hasLinkedComputers {
                    Text("Your linked computers reach this one this way too, so it stays on while any are linked. Unlink them above to turn it off.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                RemoteNetworksSettings(model: model, status: status) { await reload() }
                if let warning = networkWarning {
                    Label(warning, systemImage: "exclamationmark.triangle.fill")
                        .font(.callout)
                        .foregroundStyle(.orange)
                }
            } header: {
                Text("Remote Access")
            }
            .id("remote-access")
            Section("Pair a Device") {
                if let pairing, pairing.url != nil {
                    PairingCodeView(pairing: pairing)
                } else {
                    Button("Show Pairing Code") {
                        Task { if await gate.ensure(model) { await newPairing() } }
                    }
                    .disabled(!remote)
                }
            }
            Section("Paired Devices") {
                if status.devices.isEmpty {
                    Text("None yet").foregroundStyle(.secondary)
                }
                ForEach(status.devices) { d in
                    HStack {
                        Image(systemName: d.platform == "ios" ? "iphone" : "laptopcomputer")
                        VStack(alignment: .leading) {
                            Text(d.name)
                            Text("Last seen \(Date(timeIntervalSince1970: Double(d.lastSeenUnixMs) / 1000).formatted(.relative(presentation: .named)))")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Revoke", role: .destructive) {
                            Task {
                                try? await model.revoke(d.id)
                                await reload()
                            }
                        }
                    }
                }
            }
            ScreenSharingSection(model: model)
            RecentActivity(model: model)
        }
        .formStyle(.grouped)
        .settingsScrollTarget()
        .task {
            remote = model.remoteEnabled
            // Networks come and go (Wi-Fi, a VPN): keep the list current.
            while !Task.isCancelled {
                await reload()
                try? await Task.sleep(for: .seconds(5))
            }
        }
    }

    private func newPairing() async {
        do { pairing = try await model.createPairing() } catch { self.error = error.localizedDescription }
    }

    private func reload() async {
        if let s = try? await model.deviceStatus() { status = s }
    }

    /// Another network on this Mac (a VPN such as Cloudflare WARP) with
    /// addresses in Tailscale's range.
    private var networkWarning: String? {
        let otherRange = status.otherTailscaleRange
        if !otherRange.isEmpty {
            return "Another network on this Mac (\(otherRange.joined(separator: ", "))) uses Tailscale’s address range, as VPNs like Cloudflare WARP do. Hammock listens only on Tailscale’s own addresses."
        }
        if status.tailscaleUnknown {
            return "More than one network on this Mac uses Tailscale’s address range, as VPNs like Cloudflare WARP do, and Hammock couldn’t ask Tailscale which is its own, so it listens on all of them."
        }
        return nil
    }
}

extension Hammock_V1_Device: Identifiable {}

/// A pairing code: the QR, and its link to copy.
struct PairingCodeView: View {
    let pairing: Pairing

    var body: some View {
        if let url = pairing.url {
            HStack(alignment: .top, spacing: 16) {
                QRCodeView(text: url.absoluteString)
                    .frame(width: 160, height: 160)
                VStack(alignment: .leading, spacing: 8) {
                    Text("Scan with Hammock on your iPhone, or open this link on it:")
                        .font(.callout)
                    Text(url.absoluteString)
                        .font(.system(.caption2, design: .monospaced))
                        .textSelection(.enabled)
                        .lineLimit(4)
                    Text("Single use · expires in 10 minutes")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Button("Copy Link") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(url.absoluteString, forType: .string)
                    }
                }
            }
        }
    }
}

/// Pairing a device from anywhere (File ▸ Pair a Device…, the palette, or
/// `hmk pair`): the same code as Settings ▸ Devices, over the window.
struct PairingSheet: View {
    let model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var pairing: Pairing?
    @State private var error: String?
    @State private var remote = true
    /// Set when remote access has no network to listen on yet: where to listen, first.
    @State private var choosing: Hammock_V1_Devices?

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            if let choosing {
                NetworkChooser(model: model, status: choosing) { ok in
                    if ok {
                        self.choosing = nil
                        Task { await start() }
                    } else {
                        dismiss()
                    }
                }
            } else {
                pairingView
            }
        }
        .padding(20)
        .frame(width: 520)
        .task {
            if let s = try? await model.deviceStatus(), s.needsNetworkChoice, !s.networks.isEmpty {
                choosing = s
            } else {
                await start()
            }
        }
    }

    @ViewBuilder private var pairingView: some View {
        Text("Pair a Device").font(.title3.weight(.semibold))
        if !remote {
            Text("Remote access is off, so your iPhone can't reach this Mac yet.")
                .foregroundStyle(.secondary)
            Button("Turn On Remote Access") {
                Task {
                    do {
                        try await model.setRemoteAccess(true)
                        remote = true
                        await load()
                    } catch { self.error = error.localizedDescription }
                }
            }
        } else if let pairing {
            PairingCodeView(pairing: pairing)
        } else if error == nil {
            ProgressView().frame(maxWidth: .infinity)
        }
        if let error {
            Label(error, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
        }
        HStack {
            Spacer()
            Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
        }
    }

    private func start() async {
        remote = model.remoteEnabled
        if remote { await load() }
    }

    private func load() async {
        do { pairing = try await model.createPairing() } catch { self.error = error.localizedDescription }
    }
}

struct QRCodeView: View {
    let text: String

    var body: some View {
        if let image = Self.render(text) {
            Image(nsImage: image)
                .interpolation(.none)
                .resizable()
                .accessibilityLabel("Pairing QR code")
        }
    }

    static func render(_ text: String) -> NSImage? {
        let f = CIFilter.qrCodeGenerator()
        f.message = Data(text.utf8)
        f.correctionLevel = "M"
        guard let out = f.outputImage?.transformed(by: CGAffineTransform(scaleX: 8, y: 8)) else { return nil }
        let rep = NSCIImageRep(ciImage: out)
        let img = NSImage(size: rep.size)
        img.addRepresentation(rep)
        return img
    }
}

/// Recent Activity (the audit log, as `hmk audit`): what agents, your
/// phone and guests did through Hammock, the permissions asked and given, and
/// failed sign-ins, in words, with runs of the same thing as one row.
private struct RecentActivity: View {
    let model: AppModel
    @State private var events: [Hammock_V1_AuditEvent] = []
    @State private var filter = Filter.all
    @State private var shown = 25

    enum Filter: String, CaseIterable, Identifiable {
        case all = "Everything", agent = "Agents", device = "Devices", guest = "Guests", security = "Security"
        var id: String { rawValue }
    }

    private var filtered: [Hammock_V1_AuditEvent] {
        filter == .all ? events : events.filter { $0.kind == String(describing: filter) }
    }

    var body: some View {
        Section {
            let rows = filtered
            if rows.isEmpty {
                Text(events.isEmpty ? "Nothing yet" : "Nothing of this kind").foregroundStyle(.secondary)
            }
            ForEach(Array(rows.prefix(shown).enumerated()), id: \.offset) { _, e in
                ActivityRow(event: e)
            }
            if rows.count > shown {
                Button("Show More") { shown += 50 }
            }
        } header: {
            HStack {
                Text("Recent Activity")
                Spacer()
                Picker("Show", selection: $filter) {
                    ForEach(Filter.allCases) { Text($0.rawValue).tag($0) }
                }
                .labelsHidden()
                .fixedSize()
            }
        } footer: {
            Text("What agents, your devices and guests have done through Hammock, and the permissions asked for. Also `hmk audit`.")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
        .task {
            while !Task.isCancelled {
                events = await model.listAudit(limit: 1000)
                try? await Task.sleep(for: .seconds(3))
            }
        }
    }
}

private struct ActivityRow: View {
    let event: Hammock_V1_AuditEvent

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Image(systemName: symbol)
                .foregroundStyle(tint)
                .frame(width: 18)
            VStack(alignment: .leading, spacing: 2) {
                (Text(event.who).fontWeight(.medium) + Text(" " + event.summary))
                    .lineLimit(3)
                    .textSelection(.enabled)
                Text(when)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .help(raw)
    }

    private var symbol: String {
        switch event.kind {
        case "agent": "sparkles"
        case "device": "iphone"
        case "guest": "person.2"
        case "security": "exclamationmark.shield"
        default: "person.crop.circle"
        }
    }

    private var tint: Color {
        switch event.kind {
        case "agent": .orange
        case "device": .blue
        case "guest": .green
        case "security": .red
        default: .secondary
        }
    }

    private var when: String {
        let last = Date(timeIntervalSince1970: Double(event.timeUnixMs) / 1000)
        var s = last.formatted(.relative(presentation: .named))
        if event.count > 1 {
            let first = Date(timeIntervalSince1970: Double(event.firstUnixMs) / 1000)
            s = "\(event.count) times, \(first.formatted(date: .omitted, time: .shortened))–\(last.formatted(date: .omitted, time: .shortened)) · " + s
        }
        return s
    }

    /// The raw event, for a hover.
    private var raw: String {
        [event.actor, event.action, event.target, event.detail].filter { !$0.isEmpty }.joined(separator: " · ")
    }
}
