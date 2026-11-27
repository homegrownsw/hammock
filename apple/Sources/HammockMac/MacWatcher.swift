import AppKit
import CoreLocation
import CoreWLAN
import HammockKit

/// This Mac triggers the daemon can't watch itself (its macwatch.go): while
/// an automation on this Mac has a Wi-Fi, app or display trigger, or a
/// Wi-Fi Only If condition, the app tells the daemon the Wi-Fi network's
/// name (macOS gives it only to apps with Location access), the apps open
/// and the displays connected, again as they change. The daemon fires on
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
    /// whether that's the Wi-Fi.
    private var wanted: (any: Bool, wifi: Bool) {
        guard let model, Prefs.shared.bool(HammockTool.schedules.pref) else { return (false, false) }
        var any = false, wifi = false
        for s in model.schedules.values where !s.paused && (s.computerID.isEmpty || s.computerID == model.myUserID) {
            for t in s.events {
                guard case .mac(let m) = t.kind, m.appWatched else { continue }
                any = true
                if m.event == .wifiJoined || m.event == .wifiLeft { wifi = true }
            }
            if s.onlyIf.contains(where: { $0.kind == .wifi }) { any = true; wifi = true }
        }
        return (any, wifi)
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
        guard want.any else { lastSent = nil; return }
        observe()
        var msg = Hammock_V1_MacAppState()
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
