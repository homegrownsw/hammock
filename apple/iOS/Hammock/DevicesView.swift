import HammockKit
import SwiftUI
import VisionKit

/// Your paired computers (Macs, and servers running hmk): which one is
/// shown, switching, forgetting, adding.
struct DevicesView: View {
    let session: Session
    @State private var path: [String] = []
    @State private var screen: ScreenRequest?
    @State private var viewed = 0  // bumped as the viewer closes: the cards look again at once
    @State private var deviceName = UserDefaults.standard.string(forKey: "deviceName") ?? ""
    @FocusState private var naming: Bool
    /// Macs whose screens can be shared: asked of each (one too old to say,
    /// or with Screen Sharing off, isn't listed).
    @State private var shareable: Set<String> = []

    var body: some View {
        let screens = session.model.map { m in m.connected ? m.screenComputers.filter { shareable.contains($0.userID) } : [] } ?? []
        NavigationStack(path: $path) {
            // Your computers, beside their Macs' screens (and this iPhone) when
            // there's room.
            FormColumns(enabled: !screens.isEmpty) {
                Section {
                    ForEach(session.hosts) { h in
                        Button { session.show(h) } label: { HostRow(session: session, host: h) }
                            .foregroundStyle(.primary)
                            .swipeActions {
                                Button("Forget", role: .destructive) { session.forget(h) }
                            }
                            .contextMenu {
                                Button("Forget This Computer", systemImage: "trash", role: .destructive) { session.forget(h) }
                            }
                    }
                    NavigationLink(value: "pair") {
                        Label("Add Computer", systemImage: "plus")
                    }
                } header: {
                    Text("Computers")
                } footer: {
                    Text("The iPhone shows one computer at a time. Tap one to switch.")
                }
            } trailing: {
                if let m = session.model, !screens.isEmpty {
                    Section {
                        ForEach(screens, id: \.userID) { c in
                            Button {
                                screen = ScreenRequest(computerID: c.userID, name: c.name)
                            } label: {
                                ScreenCard(model: m, computer: c, viewed: viewed)
                            }
                            .buttonStyle(.plain)
                            // Apart, not touching.
                            .listRowInsets(EdgeInsets(top: 0, leading: 0, bottom: 10, trailing: 0))
                            .listRowBackground(Color.clear)
                            .accessibilityHint("Shows this Mac's screen")
                        }
                    } header: {
                        Text("Show Screen")
                    } footer: {
                        Text("See and control a Mac’s whole screen, to approve a prompt or do something only its windows do. Turn it on in Hammock ▸ Settings ▸ Devices on that Mac.")
                    }
                }
                Section {
                    LabeledContent("Name") {
                        TextField(UIDevice.current.name, text: $deviceName)
                            .multilineTextAlignment(.trailing)
                            .submitLabel(.done)
                            .onSubmit(rename)
                            .focused($naming)
                    }
                    .onChange(of: naming) { _, on in if !on { rename() } }
                } header: {
                    Text("This iPhone")
                } footer: {
                    Text("What your computers call this iPhone.")
                }
            }
            .navigationTitle("Devices")
            .onChange(of: session.model?.thisComputer?.kind, initial: true) { _, k in if let k { session.noteKind(k) } }
            .task(id: session.model?.connected == true ? session.model?.screenComputers.map(\.userID) ?? [] : []) { await checkScreens() }
            .fullScreenCover(item: $screen, onDismiss: { viewed += 1 }) { r in
                if let m = session.model { PhoneScreenViewer(model: m, request: r) }
            }
            .navigationDestination(for: String.self) { _ in
                PairingScreen(session: session) { path.removeAll() }
            }
        }
    }
}

extension DevicesView {
    /// Asks each Mac whether its screen can be shared now.
    /// The name typed (empty: the iPhone's own): kept, and told to the Mac shown.
    private func rename() {
        let new = deviceName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard new != (UserDefaults.standard.string(forKey: "deviceName") ?? "") else { return }
        Session.deviceName = new
        session.renamed()
    }

    func checkScreens() async {
        guard let m = session.model, m.connected else {
            shareable = []
            return
        }
        var ok: Set<String> = []
        for c in m.screenComputers {
            if let r = await m.listDisplays(computerID: c.userID), r.enabled, !r.displays.isEmpty { ok.insert(c.userID) }
        }
        shareable = ok
    }
}

private struct HostRow: View {
    let session: Session
    let host: StoredHost

    var body: some View {
        let current = host.id == session.host?.id
        HStack(spacing: 12) {
            Image(systemName: current ? session.model?.thisComputer?.symbol ?? Computer.symbol(kind: host.kind ?? "") : Computer.symbol(kind: host.kind ?? ""))
                .font(.title2)
                .foregroundStyle(current ? AnyShapeStyle(.tint) : AnyShapeStyle(.secondary))
                .frame(width: 34)
            VStack(alignment: .leading, spacing: 2) {
                Text(host.displayName).font(.headline)
                Text(status(current)).font(.subheadline).foregroundStyle(.secondary)
            }
            Spacer()
            if current { Image(systemName: "checkmark").foregroundStyle(.tint).fontWeight(.semibold) }
        }
    }

    private func status(_ current: Bool) -> String {
        guard current, let m = session.model else { return host.address }
        return m.connected ? "Connected" : (m.lastError ?? "Connecting…")
    }
}

/// Pairs a new computer: scan the code it shows, or paste its link.
struct PairingScreen: View {
    let session: Session
    var done: () -> Void = {}
    /// First run: a way into the demo, for anyone without a Mac to hand.
    var offersDemo = false
    @State private var code = ""
    @State private var started = false

    var body: some View {
        Form {
            Section {
                Text("On your Mac, open Hammock ▸ Settings ▸ Devices, turn on remote access and choose **Show Pairing Code**. On a server, run **hmk pair**.")
                    .foregroundStyle(.secondary)
            }
            if DataScannerViewController.isSupported {
                Section("Scan the code") {
                    QRScanner { pair($0) }
                        .frame(height: 280)
                        .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                        .listRowInsets(EdgeInsets())
                }
            }
            Section("Or paste the link") {
                TextField("hammock://pair/…", text: $code, axis: .vertical)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .font(.system(.footnote, design: .monospaced))
                Button("Pair") { pair(code) }.disabled(code.isEmpty)
            }
            if started, let m = session.pending ?? (session.host?.deviceID == nil ? session.model : nil) {
                Section {
                    Label(m.lastError ?? "Pairing…", systemImage: "antenna.radiowaves.left.and.right").foregroundStyle(.secondary)
                }
            }
            if let error = session.pairingError {
                Section { Text(error).foregroundStyle(.red) }
            }
            if offersDemo {
                // A side door, not the way in: a footnote, under everything.
                Section {} footer: {
                    VStack(spacing: 6) {
                        HStack(spacing: 4) {
                            Text("No Mac to hand?")
                            Button("See a demo") { session.startDemo() }
                        }
                        Text("Hammock for Mac is free at [homegrownsw.com/hammock](https://homegrownsw.com/hammock/).")
                    }
                    .font(.footnote)
                    .frame(maxWidth: .infinity)
                    .multilineTextAlignment(.center)
                }
            }
        }
        .navigationTitle("Add Computer")
        .navigationBarTitleDisplayMode(.inline)
        .onChange(of: session.hosts.count) { old, new in if started, new > old { done() } }
    }

    private func pair(_ c: String) {
        guard !started || session.pending == nil else { return }
        started = true
        session.pair(with: c.trimmingCharacters(in: .whitespacesAndNewlines))
    }
}

/// The camera, reading the first hammock:// QR code it sees.
struct QRScanner: UIViewControllerRepresentable {
    let found: (String) -> Void

    func makeUIViewController(context: Context) -> DataScannerViewController {
        let vc = DataScannerViewController(recognizedDataTypes: [.barcode(symbologies: [.qr])], isHighlightingEnabled: true)
        vc.delegate = context.coordinator
        try? vc.startScanning()
        return vc
    }

    func updateUIViewController(_ vc: DataScannerViewController, context: Context) {}

    func makeCoordinator() -> Coordinator { Coordinator(found: found) }

    final class Coordinator: NSObject, DataScannerViewControllerDelegate {
        let found: (String) -> Void
        private var done = false
        init(found: @escaping (String) -> Void) { self.found = found }

        func dataScanner(_ s: DataScannerViewController, didAdd items: [RecognizedItem], allItems: [RecognizedItem]) {
            for case .barcode(let b) in items {
                if !done, let v = b.payloadStringValue, v.hasPrefix("hammock://pair/") {
                    done = true
                    found(v)
                }
            }
        }
    }
}

/// A Mac whose screen you can open: a card showing what's on it (now, or
/// what it last showed if it's asleep), refreshed while it's on screen.
private struct ScreenCard: View {
    let model: AppModel
    let computer: Computer
    let viewed: Int  // the viewer's closed this many times: refresh on each
    @State private var picture: UIImage?
    @State private var aspect: CGFloat = 16 / 9
    @State private var asleep = false
    @State private var taken: Date?
    /// The card's width in pixels: what the snapshot needs (iPhone Duo's
    /// two displays differ, so not the screen's).
    @State private var width: CGFloat = 0
    @Environment(\.displayScale) private var scale

    var body: some View {
        ZStack(alignment: .bottomLeading) {
            Group {
                if let picture {
                    Image(uiImage: picture).resizable().aspectRatio(contentMode: .fill)
                } else {
                    Rectangle().fill(.quaternary)
                        .overlay(ComputerIcon(computer, size: 44, weight: .regular, color: Color.secondary.opacity(0.6)))
                }
            }
            .aspectRatio(aspect, contentMode: .fit)
            .frame(maxWidth: .infinity)
            .clipped()
            .saturation(asleep ? 0.3 : 1)
            .brightness(asleep ? -0.15 : 0)
            // The name over a fade, so it reads on any picture.
            LinearGradient(colors: [.clear, .black.opacity(0.75)], startPoint: .center, endPoint: .bottom)
            HStack(alignment: .bottom) {
                VStack(alignment: .leading, spacing: 2) {
                    Label { Text(computer.name) } icon: { ComputerIcon(computer, size: 15, color: .white) }.font(.headline)
                    Text(caption).font(.subheadline).foregroundStyle(.white.opacity(0.8))
                }
                Spacer()
                Image(systemName: "arrow.up.left.and.arrow.down.right")
                    .font(.headline)
                    .padding(10)
                    .background(.ultraThinMaterial, in: Circle())
            }
            .foregroundStyle(.white)
            .padding(14)
        }
        .clipShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
        .contentShape(RoundedRectangle(cornerRadius: 20, style: .continuous))
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
        .task(id: "\(computer.userID) \(viewed)") {
            // Every few seconds while it's shown.
            while !Task.isCancelled {
                await refresh()
                try? await Task.sleep(for: .seconds(asleep ? 10 : 5))
            }
        }
    }

    private var caption: String {
        if asleep, let taken {
            return "Display Asleep · \(taken.formatted(.relative(presentation: .named)))"
        }
        if asleep { return "Display Asleep" }
        return computer.thisComputer ? "The Mac this iPhone is connected to" : computer.status
    }

    private func refresh() async {
        let px = UInt32(max(width, 400) * scale)
        guard let r = await model.screenSnapshot(computerID: computer.userID, maxWidth: px) else { return }
        asleep = r.asleep
        taken = r.takenUnixMs > 0 ? Date(timeIntervalSince1970: TimeInterval(r.takenUnixMs) / 1000) : nil
        if let img = UIImage(data: r.jpeg) {
            picture = img
            if r.height > 0 { aspect = CGFloat(r.width) / CGFloat(r.height) }
        }
    }
}
