import ActivityKit
import AppIntents
import SwiftUI
import WidgetKit

/// The Live Activity: the agent that most needs you (else the one at work),
/// where it is, what it's doing and for how long, and how many others are;
/// Allow and Deny when it waits on a permission prompt.
struct HammockLiveActivity: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: HammockActivityAttributes.self) { context in
            LockScreenLive(content: context.state, host: context.attributes.host, stale: context.isStale)
                .widgetURL(link(context.state, host: context.attributes.host))
        } dynamicIsland: { context in
            let c = context.state
            let host = context.attributes.host
            return DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    HStack(spacing: 6) {
                        AgentGlyph(harness: c.harness, agent: c.agent, size: 18)
                        Text(c.agent).font(.headline).lineLimit(1)
                    }
                    .padding(.leading, 4)
                }
                DynamicIslandExpandedRegion(.trailing) {
                    HStack(spacing: 4) {
                        StateMark(content: c, tint: IslandTint.color(c))
                        Elapsed(content: c).font(.subheadline.monospacedDigit())
                    }
                    .padding(.trailing, 4)
                }
                DynamicIslandExpandedRegion(.center) {
                    if let place = c.place, !place.isEmpty {
                        Text(place).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                DynamicIslandExpandedRegion(.bottom) {
                    VStack(alignment: .leading, spacing: 6) {
                        LiveLines(content: c, compact: true)
                        if c.answerable { AnswerButtons(content: c, host: host) }
                    }
                    .padding(.horizontal, 4)
                }
            } compactLeading: {
                AgentGlyph(harness: c.harness, agent: c.agent, size: 16).foregroundStyle(IslandTint.color(c))
            } compactTrailing: {
                HStack(spacing: 3) {
                    if c.kind == .needsYou {
                        Circle().fill(.orange).frame(width: 8, height: 8)
                    } else {
                        StateMark(content: c, tint: IslandTint.color(c))
                    }
                    if c.others > 0 { Text("+\(c.others)").font(.caption2.monospacedDigit()) }
                }
            } minimal: {
                if c.kind == .needsYou {
                    Image(systemName: "exclamationmark.circle.fill").foregroundStyle(.orange)
                } else {
                    AgentGlyph(harness: c.harness, agent: c.agent, size: 14).foregroundStyle(IslandTint.color(c))
                }
            }
            .widgetURL(link(c, host: host))
            .keylineTint(IslandTint.color(c))
        }
    }

    private func link(_ c: LiveContent, host: String) -> URL {
        if let pane = c.pane, !pane.isEmpty { return HammockURL.pane(host: host, pane: pane) }
        return HammockURL.needsYou(host: host)
    }
}

/// The Dynamic Island is always dark: the accent's dark form.
enum IslandTint {
    static func color(_ c: LiveContent) -> Color { accent(c, dark: true) }
}

/// The Lock Screen's.
struct LockScreenLive: View {
    let content: LiveContent
    let host: String
    let stale: Bool
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let c = content
        let tint = accent(c, dark: scheme == .dark)
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                AgentGlyph(harness: c.harness, agent: c.agent, size: 22).foregroundStyle(tint)
                VStack(alignment: .leading, spacing: 1) {
                    Text(c.agent).font(.headline).lineLimit(1)
                    if let place = c.place, !place.isEmpty {
                        Text(place).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                Spacer(minLength: 4)
                VStack(alignment: .trailing, spacing: 1) {
                    HStack(spacing: 4) {
                        StateMark(content: c, tint: tint)
                        Text(c.status).font(.subheadline.weight(.semibold))
                    }
                    Elapsed(content: c).font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                }
            }
            LiveLines(content: c, compact: false)
            if c.answerable { AnswerButtons(content: c, host: host) }
            if stale {
                Text("Not updated lately").font(.caption2).foregroundStyle(.secondary)
            }
        }
        .padding(14)
        .activityBackgroundTint(nil)
        .activitySystemActionForegroundColor(tint)
    }
}

/// What it's doing (or asks), its context, and the others.
struct LiveLines: View {
    let content: LiveContent
    let compact: Bool

    var body: some View {
        let c = content
        VStack(alignment: .leading, spacing: 4) {
            if let doing = c.doing, !doing.isEmpty {
                Text(doing).font(compact ? .callout : .body).lineLimit(compact ? 2 : 3)
            }
            HStack(spacing: 8) {
                if let f = c.contextFraction {
                    Gauge(value: f) { Text("Context") } currentValueLabel: { Text("\(c.ctx ?? 0)%") }
                        .gaugeStyle(.accessoryLinearCapacity)
                        .frame(maxWidth: 90)
                        .accessibilityLabel("Context \(c.ctx ?? 0)% full")
                }
                if let more = c.more, !more.isEmpty {
                    Text(more).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
            }
        }
    }
}

/// How long it's been at it (or waiting); once it's over, when.
struct Elapsed: View {
    let content: LiveContent

    var body: some View {
        if content.over {
            Text(content.started, style: .time)
        } else {
            Text(timerInterval: content.started...Date.distantFuture, countsDown: false)
        }
    }
}

/// Allow and Deny, answered through the app once the phone's unlocked.
struct AnswerButtons: View {
    let content: LiveContent
    let host: String

    var body: some View {
        HStack(spacing: 8) {
            Button(intent: LiveAnswerIntent(host: host, pane: content.pane ?? "", item: content.item ?? "", prompt: content.prompt ?? "", allow: false)) {
                Text("Deny").frame(maxWidth: .infinity)
            }
            .tint(.red)
            Button(intent: LiveAnswerIntent(host: host, pane: content.pane ?? "", item: content.item ?? "", prompt: content.prompt ?? "", allow: true)) {
                Text("Allow").frame(maxWidth: .infinity)
            }
            .tint(accent(content, dark: true))
        }
        .buttonStyle(.borderedProminent)
        .font(.subheadline.weight(.semibold))
    }
}
