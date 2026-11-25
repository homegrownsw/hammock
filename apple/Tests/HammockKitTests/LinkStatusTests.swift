import Foundation
import Network
import Testing
@testable import HammockKit

/// What the phones say about their link to the computer: nothing for a
/// blip, then connecting or reconnecting, then that it can't reach it.
struct LinkStatusTests {
    private let t0 = Date(timeIntervalSince1970: 1_000_000)

    private func shown(connected: Bool = false, ever: Bool = true, downFor: TimeInterval?) -> LinkStatus? {
        LinkStatus.shown(connected: connected, everConnected: ever,
                         downSince: downFor.map { t0.addingTimeInterval(-$0) }, now: t0)
    }

    @Test func quietWhileConnected() {
        #expect(shown(connected: true, downFor: nil) == nil)
        #expect(shown(connected: true, downFor: 60) == nil)
    }

    @Test func blipsAreNotShown() {
        #expect(shown(downFor: 0) == nil)
        #expect(shown(downFor: 0.9) == nil)
        #expect(shown(downFor: -3) == nil) // a clock behind the drop
    }

    @Test func aRealDropShowsPromptly() {
        #expect(shown(downFor: LinkStatus.showAfter) == .reconnecting)
        #expect(shown(ever: false, downFor: 2) == .connecting)
    }

    @Test func aWhileDownCantReachIt() {
        #expect(shown(downFor: LinkStatus.unreachableAfter - 0.1) == .reconnecting)
        #expect(shown(downFor: LinkStatus.unreachableAfter) == .unreachable)
        #expect(shown(ever: false, downFor: 30) == .unreachable)
    }

    @Test func wording() {
        #expect(LinkStatus.connecting.title(host: "Studio") == "Connecting to Studio…")
        #expect(LinkStatus.reconnecting.title(host: "Studio") == "Reconnecting to Studio…")
        #expect(LinkStatus.unreachable.title(host: "Studio") == "Can't reach Studio")
    }

    @Test func detailSaysWhyAndHowOld() {
        #expect(LinkStatus.reconnecting.detail(reason: "It didn't answer.", lastLive: t0, now: t0) == nil)
        #expect(LinkStatus.unreachable.detail(reason: nil, lastLive: nil, now: t0) == "It may be asleep, or on another network.")
        #expect(LinkStatus.unreachable.detail(reason: "It didn't answer.", lastLive: t0.addingTimeInterval(-180), now: t0)
                == "It didn't answer. Last updated 3m ago.")
    }

    @Test func announcements() {
        #expect(LinkStatus.announcement(from: nil, to: .reconnecting, host: "Studio") == nil)
        #expect(LinkStatus.announcement(from: .reconnecting, to: .unreachable, host: "Studio") == "Can't reach Studio")
        #expect(LinkStatus.announcement(from: .unreachable, to: .unreachable, host: "Studio") == nil)
        #expect(LinkStatus.announcement(from: .unreachable, to: nil, host: "Studio") == "Reconnected to Studio")
        #expect(LinkStatus.announcement(from: .reconnecting, to: nil, host: "Studio") == "Reconnected to Studio")
        #expect(LinkStatus.announcement(from: .connecting, to: nil, host: "Studio") == "Connected to Studio")
        #expect(LinkStatus.announcement(from: nil, to: nil, host: "Studio") == nil)
    }

    @Test func reasons() {
        #expect(LinkStatus.reason(for: nil) == nil)
        #expect(LinkStatus.reason(for: DaemonError.closed) == nil)
        #expect(LinkStatus.reason(for: NWError.posix(.ETIMEDOUT)) == "It didn't answer.")
        #expect(LinkStatus.reason(for: NWError.posix(.ECONNREFUSED)) == "Hammock isn't running there, or remote access is off.")
        #expect(LinkStatus.reason(for: NWError.posix(.EHOSTUNREACH)) == nil)
        #expect(LinkStatus.reason(for: NWError.posix(.ENETDOWN)) == "It isn't reachable from this network.")
        #expect(LinkStatus.reason(for: DaemonError.server("No address for this Mac can be reached from this network."))
                == "No address for this Mac can be reached from this network.")
        #expect(LinkStatus.reason(for: DaemonError.server("Bad challenge")) == "Bad challenge.")
    }
}

/// Try Again and a dropped link, against a stand-in daemon.
@MainActor
@Suite(.serialized) struct ReconnectNowTests {
    final class Daemon: DaemonStandIn, @unchecked Sendable {
        private let lock = NSLock()
        private var away = false
        var reachable: Bool { lock.withLock { !away } }
        func setAway(_ on: Bool) { lock.withLock { away = on } }

        func receive(_ msg: ClientMessage, reply: @escaping @Sendable (ServerMessage) -> Void) {
            switch msg.msg {
            case .hello: reply(.with { $0.replyTo = msg.id; $0.helloReply = .with { $0.hostName = "Studio" } })
            case .subscribe: reply(.with { $0.replyTo = msg.id; $0.state = .with { $0.full = true } })
            default: break
            }
        }
    }

    private func wait(_ what: @autoclosure () -> Bool, seconds: Double = 5) async {
        let end = Date().addingTimeInterval(seconds)
        while !what(), Date() < end { try? await Task.sleep(for: .milliseconds(20)) }
    }

    @Test func dropKeepsHistoryAndTryAgainReconnects() async {
        let d = Daemon()
        let m = AppModel(standIn: d, clientKind: "test")
        #expect(m.downSince != nil && !m.everConnected)
        m.start()
        await wait(m.connected)
        #expect(m.connected && m.everConnected && m.downSince == nil)

        d.setAway(true)
        m.dropLink()
        await wait(!m.connected)
        #expect(m.downSince != nil && m.lastLive != nil)
        // It keeps failing: Try Again shows it trying at once.
        try? await Task.sleep(for: .milliseconds(700))
        m.reconnectNow()
        #expect(LinkStatus.shown(connected: m.connected, everConnected: m.everConnected, downSince: m.downSince, now: Date()) == .reconnecting)

        d.setAway(false)
        m.reconnectNow()
        await wait(m.connected)
        #expect(m.connected && m.downSince == nil && m.linkError == nil)
        m.stop()
    }

    @Test func refreshReturnsOnceConnected() async {
        let d = Daemon()
        d.setAway(true)
        let m = AppModel(standIn: d, clientKind: "test")
        m.start()
        try? await Task.sleep(for: .milliseconds(100))
        d.setAway(false)
        await m.refresh(timeout: 5)
        #expect(m.connected)
        await m.refresh(timeout: 5) // connected: asked, and it answers
        #expect(m.connected)
        m.stop()
    }
}
