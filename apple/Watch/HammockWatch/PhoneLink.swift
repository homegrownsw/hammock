import Foundation
import Observation
import WatchConnectivity
import WidgetKit

/// The watch's way to your Macs: through Hammock on your iPhone, which it
/// asks (waking it in the background) over WatchConnectivity. The latest
/// list also comes as the phone's application context, kept here and in
/// the App Group for the complications.
@MainActor
@Observable
final class PhoneLink: NSObject {
    static let shared = PhoneLink()

    /// The lists, as the phone last said.
    private(set) var snapshot: WatchSnapshot?
    /// Asking for the lists now.
    private(set) var loading = false
    /// Why the last ask failed, as people should read it.
    private(set) var problem: String?
    /// Pictures fetched, by pane and ref.
    @ObservationIgnored private var images: [String: Data] = [:]

    /// The App Group the complications read the list from.
    nonisolated static let group = "group.com.homegrownsw.hammock"

    func start() {
        snapshot = Self.stored()
        #if DEBUG
        if WatchSample.on {
            snapshot = WatchSample.snapshot
            return
        }
        #endif
        guard WCSession.isSupported() else { return }
        WCSession.default.delegate = self
        WCSession.default.activate()
    }

    /// Asks for the lists again.
    func refresh() async {
        loading = true
        defer { loading = false }
        do {
            if case let .snapshot(s) = try await ask(.snapshot) {
                keep(s)
                problem = nil
            }
        } catch {
            problem = error.localizedDescription
        }
    }

    /// Asks the phone something; its reply, or why there's none.
    func ask(_ request: WatchRequest) async throws -> WatchReply {
        #if DEBUG
        if WatchSample.on { return WatchSample.reply(request) }
        #endif
        let s = WCSession.default
        for _ in 0..<20 where s.activationState != .activated { try? await Task.sleep(for: .milliseconds(100)) }
        guard s.activationState == .activated else { throw LinkError.noPhone }
        guard s.isReachable else { throw LinkError.unreachable }
        let data = try WatchWire.encode(request)
        let reply: Data = try await withCheckedThrowingContinuation { cont in
            s.sendMessageData(data, replyHandler: { cont.resume(returning: $0) }, errorHandler: { cont.resume(throwing: $0) })
        }
        let r = try WatchWire.decode(WatchReply.self, from: reply)
        if case let .failed(why) = r { throw LinkError.failed(why) }
        return r
    }

    /// A picture in a conversation (cached).
    func image(host: String, pane: String, ref: String) async -> Data? {
        let key = host + "/" + pane + "/" + ref
        if let d = images[key] { return d }
        guard case let .image(d)? = try? await ask(.image(host: host, pane: pane, ref: ref)) else { return nil }
        images[key] = d
        return d
    }

    private func keep(_ s: WatchSnapshot) {
        snapshot = s
        if let data = try? WatchWire.encode(s) { UserDefaults(suiteName: Self.group)?.set(data, forKey: WatchWire.snapshotKey) }
        WidgetCenter.shared.reloadAllTimelines()
    }

    /// The list as last kept (the complications read it too).
    nonisolated static func stored() -> WatchSnapshot? {
        guard let data = UserDefaults(suiteName: group)?.data(forKey: WatchWire.snapshotKey) else { return nil }
        return try? WatchWire.decode(WatchSnapshot.self, from: data)
    }

    enum LinkError: LocalizedError {
        case noPhone, unreachable, failed(String)
        var errorDescription: String? {
            switch self {
            case .noPhone: "Open Hammock on your iPhone to set up the watch."
            case .unreachable: "Your iPhone isn't nearby."
            case .failed(let why): why
            }
        }
    }
}

extension PhoneLink: WCSessionDelegate {
    nonisolated func session(_ session: WCSession, activationDidCompleteWith state: WCSessionActivationState, error: Error?) {
        let context = session.receivedApplicationContext
        Task { @MainActor in self.received(context) }
    }

    nonisolated func session(_ session: WCSession, didReceiveApplicationContext context: [String: Any]) {
        nonisolated(unsafe) let c = context
        Task { @MainActor in self.received(c) }
    }

    private func received(_ context: [String: Any]) {
        guard let data = context[WatchWire.snapshotKey] as? Data, let s = try? WatchWire.decode(WatchSnapshot.self, from: data),
              s.at > (snapshot?.at ?? .distantPast) else { return }
        keep(s)
    }
}
