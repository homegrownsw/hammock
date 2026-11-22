import SwiftUI
import WidgetKit

/// Needs You: how many things wait on you, and the first of them (who,
/// where, what it asks), each opening its pane. From the App Group, kept by
/// the app and the notification extension (NeedsYouStore), which reload it.
struct NeedsYouWidget: Widget {
    var body: some WidgetConfiguration {
        StaticConfiguration(kind: NeedsYouStore.widgetKind, provider: NeedsYouProvider()) { entry in
            NeedsYouView(entry: entry)
                .containerBackground(.fill.tertiary, for: .widget)
        }
        .configurationDisplayName("Needs You")
        .description("What your agents are waiting on you for.")
        .supportedFamilies([.systemSmall, .systemMedium, .accessoryCircular, .accessoryRectangular, .accessoryInline])
    }
}

struct NeedsYouEntry: TimelineEntry {
    let date: Date
    let snapshot: NeedsYouSnapshot
}

struct NeedsYouProvider: TimelineProvider {
    func placeholder(in context: Context) -> NeedsYouEntry { NeedsYouEntry(date: .now, snapshot: Self.sample) }

    func getSnapshot(in context: Context, completion: @escaping (NeedsYouEntry) -> Void) {
        let s = NeedsYouStore.load()
        completion(NeedsYouEntry(date: .now, snapshot: context.isPreview && s.items.isEmpty ? Self.sample : s))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<NeedsYouEntry>) -> Void) {
        // Reloaded whenever what it shows changes (NeedsYouStore.update).
        completion(Timeline(entries: [NeedsYouEntry(date: .now, snapshot: NeedsYouStore.load())], policy: .never))
    }

    /// Made up, for the widget gallery.
    static let sample = NeedsYouSnapshot(items: [
        NeedsYouItem(id: "1", host: "", pane: nil, title: "Map Judder", place: "🗺 Atlas", ask: "Allow Bash: npm test?", at: 0),
        NeedsYouItem(id: "2", host: "", pane: nil, title: "api", place: "Web", ask: "Claude asks: Which database should I use?", at: 1),
    ], badge: 2)
}

struct NeedsYouView: View {
    let entry: NeedsYouEntry
    @Environment(\.widgetFamily) private var family

    var body: some View {
        let s = entry.snapshot
        switch family {
        case .accessoryInline:
            Text(s.badge == 0 ? "Nothing needs you" : "\(s.badge) need\(s.badge == 1 ? "s" : "") you")
        case .accessoryCircular:
            ZStack {
                AccessoryWidgetBackground()
                VStack(spacing: 0) {
                    Text("\(s.badge)").font(.title2.weight(.semibold).monospacedDigit())
                    Image(systemName: "hand.raised.fill").font(.caption2)
                }
            }
            .widgetURL(s.top(1).first?.url)
        case .accessoryRectangular:
            if let first = s.top(1).first {
                VStack(alignment: .leading, spacing: 1) {
                    Text("\(first.title)\(s.badge > 1 ? " +\(s.badge - 1)" : "")").font(.headline).lineLimit(1)
                    Text(first.ask).font(.caption).lineLimit(2)
                }
                .widgetURL(first.url)
            } else {
                Text("Nothing needs you").font(.caption)
            }
        default:
            let items = s.top(family == .systemSmall ? 1 : 2)
            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Image(systemName: "hand.raised.fill").foregroundStyle(.orange)
                    Text("Needs You").font(.headline)
                    Spacer()
                    Text("\(s.badge)").font(.title3.weight(.semibold).monospacedDigit())
                }
                if items.isEmpty {
                    Spacer()
                    Text("Nothing needs you").font(.callout).foregroundStyle(.secondary)
                    Spacer()
                } else {
                    ForEach(items) { item in
                        Link(destination: item.url) {
                            VStack(alignment: .leading, spacing: 1) {
                                Text(item.title).font(.subheadline.weight(.semibold)).lineLimit(1)
                                if !item.place.isEmpty { Text(item.place).font(.caption2).foregroundStyle(.secondary).lineLimit(1) }
                                Text(item.ask).font(.caption).lineLimit(family == .systemSmall ? 3 : 2)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                    Spacer(minLength: 0)
                }
            }
        }
    }
}
