import Foundation
#if os(iOS) && canImport(ActivityKit)
import ActivityKit
#endif
#if os(iOS) && canImport(WidgetKit)
import WidgetKit
#endif

// The iPhone's Live Activity and Needs You widget: what they show, and
// where they open. Self-contained (Foundation only), as the widget
// extension and the notification extension compile this file too
// (the Xcode project), without the rest of HammockKit.

/// What a Live Activity shows, as the Mac's daemon makes it
/// (internal/daemon/live.go's liveContent, the same JSON): the
/// content-state its pushes carry, and what the app is told while it's
/// open (StateUpdate.live_activity). The words are the Mac's: this only draws them.
public struct LiveContent: Codable, Hashable, Sendable {
    public var agent: String
    public var harness: String?
    public var tab: String?
    public var group: String?
    public var groupIcon: String?
    /// Where, in words: "🗺 Atlas · Map Judder".
    public var place: String?
    public var state: String
    /// The state in words: "Working", "Needs you".
    public var status: String
    /// What it's doing now, or what it asks.
    public var doing: String?
    /// When the state began (Unix seconds): its timer counts from it.
    public var since: Int
    /// How full its context is, 0–100, when the Mac knows.
    public var ctx: Int?
    public var pane: String?
    /// The permission prompt Allow and Deny answer (AgentAnswer.prompt_id).
    public var prompt: String?
    /// The Needs You item answering clears.
    public var item: String?
    /// The others: "+2 more working".
    public var more: String?
    public var others: Int
    /// The count on Hammock's icon.
    public var waiting: Int
    /// The theme's accent, "#RRGGBB", in the light and the dark.
    public var accent: String?
    public var accentDark: String?

    public init(agent: String, harness: String? = nil, tab: String? = nil, group: String? = nil, groupIcon: String? = nil, place: String? = nil,
                state: String, status: String, doing: String? = nil, since: Int, ctx: Int? = nil, pane: String? = nil, prompt: String? = nil,
                item: String? = nil, more: String? = nil, others: Int = 0, waiting: Int = 0, accent: String? = nil, accentDark: String? = nil) {
        self.agent = agent
        self.harness = harness
        self.tab = tab
        self.group = group
        self.groupIcon = groupIcon
        self.place = place
        self.state = state
        self.status = status
        self.doing = doing
        self.since = since
        self.ctx = ctx
        self.pane = pane
        self.prompt = prompt
        self.item = item
        self.more = more
        self.others = others
        self.waiting = waiting
        self.accent = accent
        self.accentDark = accentDark
    }

    public enum Kind: Sendable { case working, needsYou, finished, errored }

    public var kind: Kind {
        switch state {
        case "needs_you": .needsYou
        case "finished": .finished
        case "errored": .errored
        default: .working
        }
    }

    /// Allow and Deny answer it: it waits on a permission prompt the Mac read.
    public var answerable: Bool { kind == .needsYou && !(prompt ?? "").isEmpty && !(pane ?? "").isEmpty }

    /// Over: it finished or failed (shown a little while, then gone).
    public var over: Bool { kind == .finished || kind == .errored }

    /// When its state began.
    public var started: Date { Date(timeIntervalSince1970: TimeInterval(since)) }

    /// Its context gauge, 0–1, when known.
    public var contextFraction: Double? { ctx.map { min(1, max(0, Double($0) / 100)) } }

    /// The content in the Mac's JSON (nil for none, or what doesn't read).
    public static func decode(_ json: String) -> LiveContent? {
        guard !json.isEmpty, let d = json.data(using: .utf8) else { return nil }
        return try? JSONDecoder().decode(LiveContent.self, from: d)
    }

    /// The accent as red, green and blue (0–1), dark or light; nil for the phone's own.
    public func accentRGB(dark: Bool) -> (Double, Double, Double)? {
        LiveContent.rgb(dark ? (accentDark ?? accent) : accent)
    }

    static func rgb(_ hex: String?) -> (Double, Double, Double)? {
        guard let hex, hex.count == 7, hex.hasPrefix("#"), let v = UInt32(hex.dropFirst(), radix: 16) else { return nil }
        return (Double(v >> 16 & 0xFF) / 255, Double(v >> 8 & 0xFF) / 255, Double(v & 0xFF) / 255)
    }
}

#if os(iOS) && canImport(ActivityKit)
/// The Live Activity: one per Mac (its attributes), its content the Mac's.
/// The relay names this type in a push that starts one (attributes-type).
public struct HammockActivityAttributes: ActivityAttributes {
    public typealias ContentState = LiveContent
    /// The Mac, by its certificate fingerprint (as notifications name it).
    public var host: String
    /// Its name, as shown.
    public var mac: String

    public init(host: String, mac: String) {
        self.host = host
        self.mac = mac
    }
}
#endif

/// Something waiting on you, as the Needs You widget lists it.
public struct NeedsYouItem: Codable, Hashable, Sendable, Identifiable {
    /// What it's about (a notification's id): the same thing again replaces it.
    public var id: String
    /// The Mac, by its certificate fingerprint.
    public var host: String
    public var pane: String?
    /// Who: the tab, or the agent.
    public var title: String
    /// Where: the Group (and the Mac, for a linked one).
    public var place: String
    /// What it's asking.
    public var ask: String
    /// When it began waiting (Unix seconds).
    public var at: Double

    public init(id: String, host: String, pane: String?, title: String, place: String, ask: String, at: Double) {
        self.id = id
        self.host = host
        self.pane = pane
        self.title = title
        self.place = place
        self.ask = ask
        self.at = at
    }

    /// Where tapping it goes: its pane, else Needs You.
    public var url: URL {
        if let pane, !pane.isEmpty { return HammockURL.pane(host: host, pane: pane) }
        return HammockURL.needsYou(host: host)
    }
}

/// What the Needs You widget shows: kept in the App Group by the app (from
/// the Mac it shows) and the notification extension (from what it
/// decrypts), so nothing more leaves the phone, and read by the widget.
public struct NeedsYouSnapshot: Codable, Hashable, Sendable {
    public var items: [NeedsYouItem]
    /// The count on Hammock's icon, as the Macs last said.
    public var badge: Int

    public init(items: [NeedsYouItem] = [], badge: Int = 0) {
        self.items = items
        self.badge = badge
    }

    /// The first `limit` to show: the one waiting longest first.
    public func top(_ limit: Int) -> [NeedsYouItem] {
        Array(items.sorted { $0.at == $1.at ? $0.id < $1.id : $0.at < $1.at }.prefix(limit))
    }

    /// With item added (or replacing what it's about).
    public func adding(_ item: NeedsYouItem) -> NeedsYouSnapshot {
        var s = self
        s.items.removeAll { $0.id == item.id && $0.host == item.host }
        s.items.append(item)
        return s
    }

    /// Without what these were about (dealt with), on that Mac.
    public func removing(_ ids: Set<String>, host: String) -> NeedsYouSnapshot {
        var s = self
        s.items.removeAll { $0.host == host && ids.contains($0.id) }
        return s
    }

    /// With a Mac's items as it says they are now (the app, connected to it).
    public func replacing(host: String, with items: [NeedsYouItem]) -> NeedsYouSnapshot {
        var s = self
        s.items.removeAll { $0.host == host }
        s.items += items
        return s
    }
}

/// The widget's data in the App Group.
public enum NeedsYouStore {
    public static let appGroup = "group.com.homegrownsw.hammock"
    static let key = "needsYou"
    /// The widget's kind (WidgetKit), to reload it.
    public static let widgetKind = "NeedsYou"

    public static func load() -> NeedsYouSnapshot {
        guard let d = UserDefaults(suiteName: appGroup)?.data(forKey: key),
              let s = try? JSONDecoder().decode(NeedsYouSnapshot.self, from: d) else { return NeedsYouSnapshot() }
        return s
    }

    /// Changes it, and has the widget read it again if it changed.
    public static func update(_ change: (NeedsYouSnapshot) -> NeedsYouSnapshot) {
        let old = load()
        let new = change(old)
        guard new != old, let d = try? JSONEncoder().encode(new) else { return }
        UserDefaults(suiteName: appGroup)?.set(d, forKey: key)
        #if os(iOS) && canImport(WidgetKit)
        WidgetCenter.shared.reloadTimelines(ofKind: widgetKind)
        #endif
    }
}

/// hammock:// links into the app: a pane on a Mac, or its Needs You.
public enum HammockURL {
    public enum Target: Equatable, Sendable {
        case pane(host: String, pane: String)
        case needsYou(host: String)
    }

    public static func pane(host: String, pane: String) -> URL {
        URL(string: "hammock://pane/\(escape(host))/\(escape(pane))")!
    }

    public static func needsYou(host: String) -> URL {
        URL(string: "hammock://needs-you/\(escape(host))")!
    }

    /// What a link opens (nil for any other, a pairing link included).
    public static func target(_ url: URL) -> Target? {
        guard url.scheme == "hammock" else { return nil }
        let parts = url.path.split(separator: "/").map { String($0).removingPercentEncoding ?? String($0) }
        switch url.host {
        case "pane" where parts.count == 2 && !parts[0].isEmpty && !parts[1].isEmpty:
            return .pane(host: parts[0], pane: parts[1])
        case "needs-you" where parts.count == 1 && !parts[0].isEmpty:
            return .needsYou(host: parts[0])
        default:
            return nil
        }
    }

    private static func escape(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(CharacterSet(charactersIn: "-_."))) ?? s
    }
}
