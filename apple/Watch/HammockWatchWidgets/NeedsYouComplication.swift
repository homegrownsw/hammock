import SwiftUI
import WidgetKit

/// The watch face's Hammock complications: how many agents need you, and
/// what they're asking. They read the list the watch app keeps in the App
/// Group (PhoneLink), and the app reloads them when it changes.
@main
struct HammockWatchWidgets: WidgetBundle {
    var body: some Widget {
        NeedsYouComplication()
    }
}

struct NeedsYouComplication: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: "NeedsYou", provider: Provider()) { entry in
            NeedsYouComplicationView(entry: entry)
                .containerBackground(.clear, for: .widget)
        }
        .configurationDisplayName("Needs You")
        .description("The agents waiting on you.")
        .supportedFamilies([.accessoryCircular, .accessoryRectangular, .accessoryInline, .accessoryCorner])
    }
}

struct Entry: TimelineEntry {
    let date: Date
    let snapshot: WatchSnapshot?

    var waiting: [WatchSession] { snapshot?.needsYou ?? [] }
    var working: Int { snapshot?.sessions.filter { $0.section == .working }.count ?? 0 }
    var total: Int { snapshot?.sessions.count ?? 0 }
}

struct Provider: TimelineProvider {
    static let group = "group.com.homegrownsw.hammock"

    static func stored() -> WatchSnapshot? {
        guard let data = UserDefaults(suiteName: group)?.data(forKey: WatchWire.snapshotKey) else { return nil }
        return try? WatchWire.decode(WatchSnapshot.self, from: data)
    }

    func placeholder(in context: Context) -> Entry { Entry(date: .now, snapshot: Self.sample) }

    func getSnapshot(in context: Context, completion: @escaping (Entry) -> Void) {
        completion(Entry(date: .now, snapshot: context.isPreview ? Self.sample : Self.stored()))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<Entry>) -> Void) {
        // The app reloads these when the list changes; now and then anyway.
        completion(Timeline(entries: [Entry(date: .now, snapshot: Self.stored())], policy: .after(.now.addingTimeInterval(30 * 60))))
    }

    /// The gallery's: two agents waiting.
    static let sample = WatchSnapshot(sessions: [
        WatchSession(host: "", hostName: "", paneID: "a", title: "checkout-flow", harness: "claude", state: .needsInput, summary: "Allow Bash?"),
        WatchSession(host: "", hostName: "", paneID: "b", title: "api-server", harness: "codex", state: .needsInput, summary: "Which database?"),
        WatchSession(host: "", hostName: "", paneID: "c", title: "login-fix", harness: "claude", state: .working),
    ])
}

struct NeedsYouComplicationView: View {
    let entry: Entry
    @Environment(\.widgetFamily) private var family

    /// The status palette's amber and indigo (StatusIcon's), dark.
    private let amber = Color(red: 1, green: 0x9F / 255, blue: 0x0A / 255)
    private let indigo = Color(red: 0x7D / 255, green: 0x7A / 255, blue: 1)

    var body: some View {
        switch family {
        case .accessoryCircular:
            Gauge(value: Double(entry.waiting.count), in: 0...Double(max(entry.total, 1))) {
                Image(systemName: "figure.wave")
            } currentValueLabel: {
                Text("\(entry.waiting.count)").foregroundStyle(entry.waiting.isEmpty ? .secondary : amber)
            }
            .gaugeStyle(.accessoryCircularCapacity)
            .tint(amber)
            .widgetAccentable()
        case .accessoryCorner:
            Text("\(entry.waiting.count)")
                .font(.title2.weight(.semibold))
                .foregroundStyle(entry.waiting.isEmpty ? .secondary : amber)
                .widgetLabel { Text(entry.waiting.isEmpty ? "Hammock" : "Need you") }
        case .accessoryInline:
            Text(inline)
        default:
            VStack(alignment: .leading, spacing: 1) {
                Text(entry.waiting.isEmpty ? "Hammock" : "Needs You")
                    .font(.headline)
                    .foregroundStyle(entry.waiting.isEmpty ? .primary : amber)
                    .widgetAccentable()
                if entry.waiting.isEmpty {
                    Text(entry.working == 0 ? "No agents need you" : "\(entry.working) working")
                        .foregroundStyle(entry.working == 0 ? .secondary : indigo)
                } else {
                    ForEach(entry.waiting.prefix(2)) { s in
                        (Text(s.title).fontWeight(.semibold) + Text(" " + Self.plain(s.summary)).foregroundStyle(.secondary))
                            .lineLimit(1)
                    }
                }
            }
            .font(.footnote)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    private var inline: String {
        let n = entry.waiting.count
        if n > 0 { return n == 1 ? "1 agent needs you" : "\(n) agents need you" }
        if entry.working > 0 { return entry.working == 1 ? "1 agent working" : "\(entry.working) agents working" }
        return "Hammock"
    }

    /// Markdown's marks dropped, for one line.
    static func plain(_ s: String) -> String {
        s.replacingOccurrences(of: "`", with: "").replacingOccurrences(of: "**", with: "")
    }
}
