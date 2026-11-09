import AppKit
import Observation
import Sparkle

/// In-app updates for the Mac app, through Sparkle. Its own module so the
/// SwiftPM build (HammockMac) and the Xcode project's HammockMacApp target both
/// link Sparkle the same way.
///
/// Only release builds carry a feed (SUFeedURL and SUPublicEDKey, added by
/// `UPDATES=1 scripts/build-mac-app.sh`, which `just release` sets); local
/// and β builds have none and everything here is inert. Checks run daily
/// and ask before installing; after an update DaemonUpdater swaps in the new
/// daemon and panes survive.
@MainActor
@Observable
public final class Updater {
    public static let shared = Updater()

    /// False while a check is already running (Sparkle's own rule).
    public private(set) var canCheck = false
    @ObservationIgnored private let controller: SPUStandardUpdaterController?
    @ObservationIgnored private var observation: NSKeyValueObservation?

    public var isAvailable: Bool { controller != nil }

    public var checksAutomatically = false {
        didSet { controller?.updater.automaticallyChecksForUpdates = checksAutomatically }
    }

    /// "0.1.212 (212)"
    public var version: String {
        let info = Bundle.main.infoDictionary ?? [:]
        let short = info["CFBundleShortVersionString"] as? String ?? "?"
        let build = info["CFBundleVersion"] as? String ?? "?"
        return short == build ? short : "\(short) (\(build))"
    }

    private init() {
        guard Bundle.main.object(forInfoDictionaryKey: "SUFeedURL") != nil else {
            controller = nil
            return
        }
        let c = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: nil, userDriverDelegate: nil)
        controller = c
        checksAutomatically = c.updater.automaticallyChecksForUpdates
        observation = c.updater.observe(\.canCheckForUpdates, options: [.initial, .new]) { [weak self] updater, _ in
            let can = updater.canCheckForUpdates
            Task { @MainActor in self?.canCheck = can }
        }
    }

    public func checkForUpdates() {
        controller?.checkForUpdates(nil)
    }
}
