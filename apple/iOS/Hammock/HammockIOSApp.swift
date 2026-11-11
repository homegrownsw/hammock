import CryptoKit
import HammockKit
import Network
import SwiftUI

@main
struct HammockIOSApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @State private var session = Session()
    @AppStorage("appearance") private var appearance = "system"
    @AppStorage(PhoneTheme.prefKey) private var themePref = ""

    init() { PhoneTheme.migrate() }

    var body: some Scene {
        WindowGroup {
            RootView(session: session)
                .appLock()  // Settings ▸ Privacy
                .hammockAppearance(appearance)
                // A link could come from anywhere: ask before pairing.
                .onOpenURL { url in session.offer(url.absoluteString) }
                .sheet(item: $session.offered) { PairConfirmSheet(session: session, offer: $0) }
                // Settings ▸ Theme (by default, the Mac's) colours the whole app.
                .phoneTheme(PhoneTheme.resolve(pref: themePref, host: session.model?.hostTheme))
                .onChange(of: session.model?.hostTheme) { PhoneTheme.remember($1) }
        }
    }
}

/// Owns the paired Macs (this device's key for each) and the live model of
/// the one being shown.
@MainActor
@Observable
final class Session {
    private(set) var model: AppModel?
    private(set) var host: StoredHost?
    private(set) var hosts: [StoredHost] = []
    /// A Mac being paired while another is shown: it takes over once paired
    /// if pendingSwitches, else it's only added to the list.
    private(set) var pending: AppModel?
    private var pendingSwitches = true
    var pairingError: String?
    /// A pairing link opened from outside the app, waiting for a yes.
    var offered: PairOffer?

    init() {
        #if DEBUG
        // Dev aid: straight into the demo (DemoMac), nothing paired or stored.
        if DemoMac.launchedInto {
            startDemo()
            return
        }
        #endif
        let stored = IdentityStore.load()
        hosts = stored.hosts
        if let h = hosts.first(where: { $0.id == stored.active }) ?? hosts.first {
            host = h
            model = connect(h, secret: nil)
        } else if let code = ProcessInfo.processInfo.environment["HMK_PAIR_CODE"] {
            // Dev aid: simctl launch with SIMCTL_CHILD_HMK_PAIR_CODE=… pairs
            // without the system "Open in Hammock?" prompt.
            pair(with: code)
        }
    }

    /// A `hammock://pair/…` link opened from elsewhere: asks first (see
    /// PairConfirmSheet).
    func offer(_ code: String) {
        guard let p = Pairing(code: code) else {
            pairingError = "That doesn't look like a Hammock pairing code."
            return
        }
        offered = PairOffer(code: code, pairing: p)
    }

    /// Pairs using a `hammock://pair/…` link or code from the Mac. With a Mac
    /// already shown, it stays until the new one has paired, and then only
    /// gives way to it if switching.
    func pair(with code: String, switching: Bool = true) {
        guard let p = Pairing(code: code) else {
            pairingError = "That doesn't look like a Hammock pairing code."
            return
        }
        guard !p.addresses.isEmpty else {
            pairingError = "The computer didn't include an address. Is remote access on there, with a network chosen (Hammock ▸ Settings ▸ Devices on a Mac, hmk remote networks on a server)?"
            return
        }
        pairingError = p.remoteEnabled ? nil
            : "Turn on “Allow paired devices to connect” in Hammock ▸ Settings ▸ Devices on your Mac (on a server: hmk remote on), then try again."
        // The code lists every address the Mac listens on; pair over the
        // first this phone can reach (a local network's only while on it).
        let candidates = RemoteCandidates.ordered(p.addresses, lanSubnets: p.lanSubnets, lastWorked: nil,
                                                  localIPs: RemoteCandidates.interfaceAddresses())
        let token = UUID()
        probe = token
        Task {
            let address = await Self.firstReachable(candidates)
            guard probe == token else { return } // cancelled, or another code since
            probe = nil
            guard let address else {
                pairingError = "Can't reach the computer. Is remote access on there, and is this iPhone on one of its networks (the same Wi‑Fi, Tailscale or your VPN)?"
                return
            }
            pair(p, address: address, switching: switching)
        }
    }

    /// The pairing still finding a reachable address, if any.
    private var probe: UUID?

    private func pair(_ p: Pairing, address: String, switching: Bool) {
        let h = StoredHost(hostName: p.hostName, address: address, certSHA256: p.certSha256,
                           deviceID: nil, privateKey: Curve25519.Signing.PrivateKey().rawRepresentation,
                           addresses: p.addresses, lanSubnets: p.lanSubnets)
        pending?.stop()
        if model == nil {
            host = h
            model = connect(h, secret: p.secret)
        } else {
            pendingSwitches = switching
            pending = connect(h, secret: p.secret)
        }
    }

    /// The first of `addresses` ("host:port") to take a connection, tried
    /// at once (each a moment after the last, in order), each given a few
    /// seconds.
    private static func firstReachable(_ addresses: [String]) async -> String? {
        await withTaskGroup(of: String?.self) { group in
            for (i, a) in addresses.enumerated() {
                group.addTask {
                    if i > 0 { try? await Task.sleep(for: .milliseconds(250 * i)) }
                    if Task.isCancelled { return nil }
                    return await reachable(a) ? a : nil
                }
            }
            for await a in group {
                if let a {
                    group.cancelAll()
                    return a
                }
            }
            return nil
        }
    }

    private static func reachable(_ address: String) async -> Bool {
        guard let u = URL(string: "tcp://\(address)"), let host = u.host, let port = u.port.flatMap({ NWEndpoint.Port(rawValue: UInt16($0)) })
        else { return false }
        let conn = NWConnection(host: NWEndpoint.Host(host), port: port, using: .tcp)
        let q = DispatchQueue(label: "hammock.probe")
        final class Once: @unchecked Sendable { var done = false } // touched only on q
        let once = Once()
        return await withCheckedContinuation { cont in
            let finish: @Sendable (Bool) -> Void = { ok in
                guard !once.done else { return }
                once.done = true
                conn.cancel()
                cont.resume(returning: ok)
            }
            conn.stateUpdateHandler = { state in
                switch state {
                case .ready: finish(true)
                case .failed, .waiting, .cancelled: finish(false)
                default: break
                }
            }
            conn.start(queue: q)
            q.asyncAfter(deadline: .now() + 4) { finish(false) }
        }
    }

    /// Stops a pairing that hasn't finished.
    func cancelPairing() {
        probe = nil
        if let pending {
            pending.stop()
            self.pending = nil
        } else if let model, host?.deviceID == nil {
            model.stop()
            self.model = nil
            host = nil
        }
    }

    /// Shows another paired Mac.
    /// What this iPhone calls itself to its Macs (Devices ▸ Name): iOS only
    /// tells apps "iPhone", so it's yours to set.
    static var deviceName: String {
        get { UserDefaults.standard.string(forKey: "deviceName").flatMap { $0.isEmpty ? nil : $0 } ?? UIDevice.current.name }
        set { UserDefaults.standard.set(newValue.trimmingCharacters(in: .whitespacesAndNewlines), forKey: "deviceName") }
    }

    /// The Mac shown says what kind it is: kept for its icon in the list.
    func noteKind(_ kind: String) {
        guard !kind.isEmpty, let h = host, h.kind != kind, let i = hosts.firstIndex(where: { $0.id == h.id }) else { return }
        hosts[i].kind = kind
        host?.kind = kind
        persist()
    }

    /// The demo is shown (DemoMac): a made-up Mac, nothing paired or stored.
    var isDemo: Bool { model?.standIn != nil }

    /// Shows the demo, in place of any Mac shown.
    func startDemo() {
        model?.stop()
        host = StoredHost(hostName: "MacBook", address: "", certSHA256: "demo", deviceID: "sample", privateKey: Data())
        let m = AppModel(standIn: DemoMac(), clientKind: "ios")
        m.start()
        model = m
    }

    /// Leaves the demo: for a paired Mac if there is one, else pairing.
    func endDemo() {
        guard isDemo else { return }
        model?.stop()
        model = nil
        host = nil
        if let next = hosts.first { show(next) }
    }

    /// Renamed: the Mac shown hears it now (it's sent as the phone signs in).
    func renamed() {
        guard let h = host, h.deviceID != nil, pending == nil, !isDemo else { return }
        model?.stop()
        model = connect(h, secret: nil)
    }

    func show(_ h: StoredHost) {
        guard h.id != host?.id else { return }
        model?.stop()
        host = h
        model = connect(h, secret: nil)
        persist()
    }

    /// Forgets a Mac (it can be paired again with a new code).
    func forget(_ h: StoredHost) {
        PushKeys.forget(h.id) // it can't send this phone anything readable now
        hosts.removeAll { $0.id == h.id }
        if h.id == host?.id {
            model?.stop()
            model = nil
            host = nil
            if let next = hosts.first { show(next) }
        }
        persist()
    }

    private func persist() { IdentityStore.save(StoredHosts(hosts: hosts, active: host?.id)) }

    private func connect(_ h: StoredHost, secret: Data?) -> AppModel {
        let id = RemoteIdentity(address: h.address, addresses: h.addresses ?? [h.address], lanSubnets: h.lanSubnets ?? [],
                                certSHA256: h.certSHA256, deviceID: h.deviceID, privateKey: h.privateKey,
                                pairingSecret: secret, deviceName: Self.deviceName, platform: "ios")
        let m = AppModel(remote: id, clientKind: "ios")
        m.onPaired = { [weak self, weak m] deviceID in
            guard let self, let m else { return }
            var paired = h
            paired.deviceID = deviceID
            self.hosts.removeAll { $0.id == paired.id }
            self.hosts.append(paired)
            self.pairingError = nil
            if self.pending === m {
                self.pending = nil
                guard self.pendingSwitches else {
                    // Added to the list; the Mac shown stays shown.
                    m.stop()
                    self.persist()
                    return
                }
                self.model?.stop()
                self.model = m
            }
            self.host = paired
            self.persist()
        }
        // Dealt with (here, on the Mac or anywhere else): its notification
        // goes from Notification Center, as on the Mac. Silent pushes do
        // this while the app isn't running (DeliveredNotes.clear).
        m.onResolved = { ids in Push.shared.withdraw(ids, host: h.id) }
        m.onFullState = { [weak m] in
            guard let m else { return }
            Push.shared.reconcile(m, host: h.id)
        }
        // Renamed on the Mac (Settings ▸ Sharing): the list and title follow.
        m.onHostName = { [weak self] name in
            guard let self, let i = self.hosts.firstIndex(where: { $0.id == h.id }), self.hosts[i].hostName != name else { return }
            self.hosts[i].hostName = name
            if self.host?.id == h.id { self.host?.hostName = name }
            self.persist()
        }
        // Where the Mac is now: kept, so the next connection finds it
        // even if its local network's address has changed since.
        m.onRemoteAddresses = { [weak self] id in
            guard let self, let i = self.hosts.firstIndex(where: { $0.id == h.id }) else { return }
            var updated = self.hosts[i]
            updated.address = id.address
            updated.addresses = id.addresses
            updated.lanSubnets = id.lanSubnets
            guard updated != self.hosts[i] else { return }
            self.hosts[i] = updated
            if self.host?.id == h.id {
                self.host?.address = id.address
                self.host?.addresses = id.addresses
                self.host?.lanSubnets = id.lanSubnets
            }
            self.persist()
        }
        m.start()
        return m
    }
}

/// A Hammock Key request, for .sheet(item:).
struct PendingKeyRequest: Identifiable {
    let request: HammockKeyRequest
    var id: String { request.id }
}

/// A pairing link waiting for a yes.
struct PairOffer: Identifiable {
    let code: String
    let pairing: Pairing
    var id: String { code }
    var name: String { pairing.hostName.replacingOccurrences(of: ".local", with: "") }
}

/// Asks before pairing with a Mac from a link opened outside the app (one
/// you didn't scan or paste here), showing which Mac it is.
struct PairConfirmSheet: View {
    let session: Session
    let offer: PairOffer
    @Environment(\.dismiss) private var dismiss
    @State private var started = false

    /// This device's id on that Mac: a new one once pairing has finished.
    private var deviceID: String? { session.hosts.first { $0.id == offer.pairing.certSha256 }?.deviceID }
    @State private var deviceIDBefore: String?
    private var paired: Bool { started && deviceID != nil && deviceID != deviceIDBefore }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    VStack(spacing: 8) {
                        Image(systemName: "desktopcomputer").font(.system(size: 40)).foregroundStyle(.tint)
                        Text("Pair with \(offer.name.isEmpty ? "this computer" : offer.name)?").font(.title3.bold()).multilineTextAlignment(.center)
                        Text("This iPhone will connect to it to show its terminals and agents.")
                            .font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
                    }
                    .frame(maxWidth: .infinity)
                    .listRowBackground(Color.clear)
                }
                Section {
                    LabeledContent("Address", value: offer.pairing.addresses.first ?? "None")
                    LabeledContent("Certificate") {
                        Text(offer.pairing.shortFingerprint).font(.system(.body, design: .monospaced))
                    }
                } footer: {
                    Text("Only pair if you just asked this computer for a pairing code.")
                }
                if started {
                    Section {
                        if let error = session.pairingError ?? (session.pending ?? session.model)?.lastError {
                            Text(error).foregroundStyle(.red)
                        } else {
                            Label("Pairing…", systemImage: "antenna.radiowaves.left.and.right").foregroundStyle(.secondary)
                        }
                    }
                }
            }
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        if started, !paired { session.cancelPairing() }
                        dismiss()
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Pair") {
                        deviceIDBefore = deviceID
                        started = true
                        // Added to your Macs; shown only if none was.
                        session.pair(with: offer.code, switching: false)
                    }
                    .disabled(started)
                }
            }
        }
        .presentationDetents([.medium, .large])
        .interactiveDismissDisabled(started && !paired)
        .onChange(of: deviceID) { if paired { dismiss() } }
    }
}

struct RootView: View {
    let session: Session

    /// The Hammock Key request to show: one waiting, if this phone may approve
    /// it and you haven't closed its sheet.
    private func keyRequest(_ model: AppModel) -> PendingKeyRequest? {
        guard let me = session.host?.deviceID, model.hammockKey.approvers.contains(where: { $0.deviceID == me }) else { return nil }
        return model.hammockKeyRequests.first { !Push.shared.keyDismissed.contains($0.id) }.map(PendingKeyRequest.init)
    }

    var body: some View {
        if let model = session.model, session.host?.deviceID != nil || model.connected {
            CockpitView(model: model, session: session)
                // In the demo, what would change the Mac says it needs one.
                .alert("This Needs Your Mac", isPresented: Binding(
                    get: { session.isDemo && model.lastError == DemoMac.needsMac },
                    set: { if !$0 { model.lastError = nil } }
                )) {
                    Button("Pair Your Mac") { session.endDemo() }
                    Button("OK", role: .cancel) {}
                } message: {
                    Text("This is a demo with a made-up Mac. Pair your own to start agents, open tabs and run tasks.")
                }
                .onAppear { Push.shared.session = session }
                // Each Mac learns where to push when it's shown, and this
                // phone's approval key for its Hammock Key.
                .onChange(of: model.connected, initial: true) { _, on in
                    guard on else { return }
                    Task { await Push.shared.registerWithMac() }
                    if let key = ApprovalKey.publicKey() { model.sendApprovalKey(key) }
                }
                .sheet(item: Binding(get: { keyRequest(model) }, set: { r in
                    // Closed without an answer: not shown again (the one on screen).
                    if r == nil, let id = Push.shared.keyShown { Push.shared.keyDismissed.insert(id) }
                })) { r in
                    HammockKeyApprovalSheet(model: model, request: r.request, hostName: session.host?.hostName ?? "your Mac",
                                         approveNow: Push.shared.approveKey == r.id)
                        .onAppear {
                            Push.shared.keyShown = r.id
                            if Push.shared.approveKey == r.id { Push.shared.approveKey = nil }
                        }
                }
        } else {
            PairingView(session: session)
        }
    }
}
