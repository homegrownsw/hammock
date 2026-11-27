import AppKit
import CoreLocation
import CoreWLAN
import HammockKit

/// This Mac triggers the daemon can't watch itself (its macwatch.go): while
/// an automation on this Mac has a Wi-Fi, app or display trigger, or a
/// Wi-Fi Only If condition, the app tells the daemon the Wi-Fi network's
/// name (macOS gives it only to apps with Location access), the apps open,
/// the displays connected and which of your Places (Places.swift) the Mac
/// is at (their names only), again as they change. The daemon fires on
/// the changes, so these fire only while the app is open: its triggers say
/// "Needs Hammock open" when it isn't.
@MainActor
final class MacWatcher: NSObject, CLLocationManagerDelegate {
    static let shared = MacWatcher()

    /// How often the Wi-Fi network is looked at (macOS doesn't say when it
    /// changes without a delegate on its own thread); how long displays are
    /// left to settle after a wake, or a change, before they're told.
    private static let wifiEvery: TimeInterval = 15
    private static let displaysSettle: TimeInterval = 3
    private static let wakeSettle: TimeInterval = 20

    private weak var model: AppModel?
    private var timer: Timer?
    private var observing = false
    private let location = CLLocationManager()
    private var lastSent: Hammock_V1_MacAppState?
    /// Displays as last told (kept while they settle after a wake).
    private var displays: [Hammock_V1_MacDisplay] = []
    private var displaysPending: DispatchWorkItem?
    private var wokeAt = Date.distantPast

    /// Starts following the model's schedules (once the daemon's state is
    /// in: again after it restarts, which forgets what it was told).
    func attach(_ model: AppModel) {
        self.model = model
        location.delegate = self
        if timer == nil {
            timer = Timer.scheduledTimer(withTimeInterval: Self.wifiEvery, repeats: true) { _ in
                Task { @MainActor in MacWatcher.shared.refresh() }
            }
        }
        displays = Self.currentDisplays()
        refresh(force: true)
    }

    /// Whether any of this Mac's automations needs what the app watches, and
    /// whether that's the Wi-Fi, or places.
    private var wanted: (any: Bool, wifi: Bool, places: Bool) {
        guard let model, Prefs.shared.bool(HammockTool.schedules.pref) else { return (false, false, false) }
        var any = false, wifi = false, places = false
        for s in model.schedules.values where !s.paused && (s.computerID.isEmpty || s.computerID == model.myUserID) {
            for t in s.events {
                guard case .mac(let m) = t.kind, m.appWatched else { continue }
                any = true
                if m.event == .wifiJoined || m.event == .wifiLeft { wifi = true }
                if m.isPlace { places = true }
            }
            if s.onlyIf.contains(where: { $0.kind == .wifi }) { any = true; wifi = true }
            if s.onlyIf.contains(where: { $0.kind == .place }) { any = true; places = true }
        }
        return (any, wifi, places)
    }

    // MARK: Places

    /// Places are watched by CoreLocation (CLMonitor, which keeps watching
    /// across launches) while an automation wants one; what it says is
    /// told to the daemon once it has held for placeSettle (longer after a
    /// wake, when a Mac's coarse location comes and goes), so a Mac waking
    /// where it slept, or a fix that wobbles across a circle's edge, says
    /// nothing new.
    private static let placeSettle: TimeInterval = 60
    private static let placeWakeSettle: TimeInterval = 120
    private var monitor: CLMonitor?
    private var monitorTask: Task<Void, Never>?
    /// What's watched: each place as it was added.
    private var watched: [String: Place] = [:]
    /// What CoreLocation last said of each (true: in; absent: not known).
    private var placeNow: [String: Bool] = [:]
    /// What's been told: placeNow, once settled.
    private var placeTold: [String: Bool] = [:]
    private var placePending: DispatchWorkItem?

    /// Watches this Mac's places while wanted (and Location is allowed), or stops.
    private func syncPlaces(_ want: Bool) {
        // (A Mac's only grant: When In Use is unavailable in macOS.)
        let allowed = location.authorizationStatus == .authorizedAlways
        let places = want && allowed ? AppConfig.shared.places : []
        let target = Dictionary(places.map { ($0.name, $0) }, uniquingKeysWith: { a, _ in a })
        guard target != watched else { return }
        let old = watched
        watched = target
        for name in old.keys where target[name] != old[name] { placeNow[name] = nil; placeTold[name] = nil }
        Task { @MainActor in
            if self.monitor == nil, !target.isEmpty {
                let m = await CLMonitor("hammock-places")
                self.monitor = m
                self.monitorTask = Task { @MainActor in
                    do {
                        for try await e in await m.events {
                            self.placeEvent(e.identifier, e.state)
                        }
                    } catch {}
                }
            }
            guard let m = self.monitor else { return }
            for id in await m.identifiers where target[id] == nil || target[id] != old[id] {
                await m.remove(id)
            }
            for (name, p) in target where old[name] != p {
                await m.add(CLMonitor.CircularGeographicCondition(center: p.coordinate, radius: p.radius), identifier: name)
                if let r = await m.record(for: name) { self.placeEvent(name, r.lastEvent.state) }
            }
        }
    }

    private func placeEvent(_ name: String, _ state: CLMonitor.Event.State) {
        guard watched[name] != nil else { return }
        let now: Bool? = switch state {
        case .satisfied: true
        case .unsatisfied: false
        default: nil
        }
        guard placeNow[name] != now else { return }
        placeNow[name] = now
        placePending?.cancel()
        let wait = Date().timeIntervalSince(wokeAt) < Self.placeWakeSettle ? Self.placeWakeSettle : Self.placeSettle
        let work = DispatchWorkItem { [weak self] in
            Task { @MainActor in
                guard let self else { return }
                // Unknown keeps what was told (the daemon keeps the last too).
                for (k, v) in self.placeNow { self.placeTold[k] = v }
                self.refresh()
            }
        }
        placePending = work
        DispatchQueue.main.asyncAfter(deadline: .now() + wait, execute: work)
    }

    /// Asks for Location access (a Wi-Fi trigger or condition was just added).
    func requestLocation() {
        if location.authorizationStatus == .notDetermined {
            location.requestWhenInUseAuthorization()
        } else {
            refresh(force: true)
        }
    }

    nonisolated func locationManagerDidChangeAuthorization(_ manager: CLLocationManager) {
        Task { @MainActor in MacWatcher.shared.refresh(force: true) }
    }

    private func observe() {
        guard !observing else { return }
        observing = true
        let ws = NSWorkspace.shared.notificationCenter
        for name in [NSWorkspace.didLaunchApplicationNotification, NSWorkspace.didTerminateApplicationNotification] {
            ws.addObserver(forName: name, object: nil, queue: .main) { _ in
                Task { @MainActor in MacWatcher.shared.refresh() }
            }
        }
        ws.addObserver(forName: NSWorkspace.didWakeNotification, object: nil, queue: .main) { _ in
            Task { @MainActor in
                MacWatcher.shared.wokeAt = Date()
                MacWatcher.shared.displaysChanged()
            }
        }
        NotificationCenter.default.addObserver(forName: NSApplication.didChangeScreenParametersNotification, object: nil, queue: .main) { _ in
            Task { @MainActor in MacWatcher.shared.displaysChanged() }
        }
        // Places edited (in the config file, or the Places sheet).
        NotificationCenter.default.addObserver(forName: AppConfig.changed, object: nil, queue: .main) { _ in
            Task { @MainActor in MacWatcher.shared.refresh() }
        }
    }

    /// Displays come and go as a Mac wakes (and as one is plugged in): what's
    /// connected is told once it has settled, so one there before the sleep
    /// and after it isn't news.
    private func displaysChanged() {
        displaysPending?.cancel()
        let wait = Date().timeIntervalSince(wokeAt) < Self.wakeSettle ? Self.wakeSettle : Self.displaysSettle
        let work = DispatchWorkItem { [weak self] in
            Task { @MainActor in
                guard let self else { return }
                self.displays = Self.currentDisplays()
                self.refresh()
            }
        }
        displaysPending = work
        DispatchQueue.main.asyncAfter(deadline: .now() + wait, execute: work)
    }

    /// Tells the daemon what's so, if any automation wants it and it's changed.
    func refresh(force: Bool = false) {
        guard let model, model.connected else { return }
        let want = wanted
        syncPlaces(want.places)
        guard want.any else {
            // Says it's stopped, so the daemon forgets what it was told: what
            // changes meanwhile isn't seen, and mustn't look like news later.
            if lastSent?.idle != true {
                let idle = Hammock_V1_MacAppState.with { $0.idle = true }
                lastSent = idle
                Task { try? await model.sendMacAppState(idle) }
            }
            return
        }
        observe()
        var msg = Hammock_V1_MacAppState()
        if want.places {
            switch location.authorizationStatus {
            case .authorizedAlways, .authorizedWhenInUse:
                msg.places = AppConfig.shared.places.map(\.name)
                msg.placesIn = placeTold.filter { $0.value && watched[$0.key] != nil }.map(\.key).sorted()
                msg.placesOut = placeTold.filter { !$0.value && watched[$0.key] != nil }.map(\.key).sorted()
            case .notDetermined:
                msg.places = AppConfig.shared.places.map(\.name)
                requestLocation()
            default:
                msg.places = AppConfig.shared.places.map(\.name)
                msg.locationDenied = true
            }
        }
        if want.wifi {
            switch location.authorizationStatus {
            case .authorizedAlways, .authorizedWhenInUse:
                msg.wifiKnown = true
                msg.wifi = CWWiFiClient.shared().interface()?.ssid() ?? ""
            case .notDetermined:
                requestLocation()
            default:
                msg.locationDenied = true
            }
        }
        msg.apps = NSWorkspace.shared.runningApplications.filter { $0.activationPolicy == .regular }.map { a in
            .with { $0.name = a.localizedName ?? ""; $0.bundleID = a.bundleIdentifier ?? "" }
        }
        msg.displays = displays
        if !force, msg == lastSent { return }
        lastSent = msg
        Task { try? await model.sendMacAppState(msg) }
    }

    /// The displays connected, each with an id that stays the same for it.
    private static func currentDisplays() -> [Hammock_V1_MacDisplay] {
        NSScreen.screens.compactMap { s in
            guard let n = s.deviceDescription[NSDeviceDescriptionKey("NSScreenNumber")] as? NSNumber else { return nil }
            let id = CGDirectDisplayID(n.uint32Value)
            let serial = CGDisplaySerialNumber(id)
            let key = "\(CGDisplayVendorNumber(id))-\(CGDisplayModelNumber(id))-\(serial == 0 ? id : serial)"
            return .with { $0.id = key; $0.name = s.localizedName }
        }
    }
}
