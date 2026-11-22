import ActivityKit
import AppIntents
import SwiftUI
import WidgetKit

/// The Live Activity: the agent that most needs you (else the one at work),
/// where it is, what it's doing and for how long, and how many others are;
/// Allow and Deny when it waits on a permission prompt. In the theme's
/// colours, as the Mac sent its accent.
struct HammockLiveActivity: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: HammockActivityAttributes.self) { context in
            LockScreenLive(content: context.state, host: context.attributes.host, stale: context.isStale)
                .widgetURL(link(context.state, host: context.attributes.host))
        } dynamicIsland: { context in
            let c = context.state
            let host = context.attributes.host
            // The island is always dark: the accent's dark form.
            let tint = accent(c, dark: true)
            return DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    AgentTile(content: c, tint: tint, ink: ink(c, dark: true), size: 32)
                        .padding(.leading, 6)
                }
                DynamicIslandExpandedRegion(.trailing) {
                    StatusPill(content: c, tint: tint, ink: ink(c, dark: true))
                        .padding(.trailing, 6)
                        .padding(.top, 4)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    // Who and where under the tile, left-aligned with it: beside it,
                    // the camera leaves them too little room.
                    VStack(alignment: .leading, spacing: 4) {
                        HStack(alignment: .firstTextBaseline) {
                            Heading(content: c)
                            Elapsed(content: c)
                                .font(.caption.monospacedDigit())
                                .foregroundStyle(.secondary)
                                .multilineTextAlignment(.trailing)
                                .frame(maxWidth: 80, alignment: .trailing)
                        }
                        if let doing = c.doing, !doing.isEmpty {
                            Text(doing).font(.callout).lineLimit(2)
                                .layoutPriority(1)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        if c.answerable {
                            AnswerButtons(content: c, host: host, tint: tint, ink: ink(c, dark: true)).controlSize(.small)
                        }
                    }
                    .padding(.horizontal, 6)
                }
            } compactLeading: {
                AgentMark(harness: c.harness ?? c.agent.lowercased(), size: 18, tint: tint)
                    .padding(.leading, 2)
            } compactTrailing: {
                HStack(spacing: 3) {
                    StateMark(content: c, tint: tint).font(.system(size: 15, weight: .semibold))
                    if c.others > 0 {
                        Text("+\(c.others)").font(.caption2.weight(.semibold).monospacedDigit()).foregroundStyle(tint)
                    }
                }
            } minimal: {
                if c.kind == .needsYou {
                    StateMark(content: c, tint: tint)
                } else {
                    AgentMark(harness: c.harness ?? c.agent.lowercased(), size: 16, tint: tint)
                }
            }
            .widgetURL(link(c, host: host))
            .keylineTint(tint)
        }
    }

    private func link(_ c: LiveContent, host: String) -> URL {
        if let pane = c.pane, !pane.isEmpty { return HammockURL.pane(host: host, pane: pane) }
        return HammockURL.needsYou(host: host)
    }
}

/// The Lock Screen's: on the theme's tint, the agent's tile, its tab and
/// where it is, its state, and what it's doing or asks on a card.
struct LockScreenLive: View {
    let content: LiveContent
    let host: String
    let stale: Bool
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let c = content
        let dark = scheme == .dark
        let tint = accent(c, dark: dark)
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .center, spacing: 10) {
                AgentTile(content: c, tint: tint, ink: ink(c, dark: dark), size: 40)
                Heading(content: c)
                Spacer(minLength: 4)
                VStack(alignment: .trailing, spacing: 3) {
                    StatusPill(content: c, tint: tint, ink: ink(c, dark: dark))
                    Elapsed(content: c)
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.trailing)
                        .frame(maxWidth: 90, alignment: .trailing)
                }
            }
            if let doing = c.doing, !doing.isEmpty {
                Text(doing)
                    .font(.callout)
                    .lineLimit(3)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 7)
                    .background(RoundedRectangle(cornerRadius: 14, style: .continuous).fill(tint.opacity(dark ? 0.16 : 0.1)))
            }
            // Allow and Deny instead, when it asks: the Lock Screen gives it little height.
            if c.answerable {
                AnswerButtons(content: c, host: host, tint: tint, ink: ink(c, dark: dark))
            } else {
                Footer(content: c, tint: tint)
            }
            if stale {
                Text("Not updated lately").font(.caption2).foregroundStyle(.secondary)
            }
        }
        .padding(14)
        .activityBackgroundTint(backdrop(c, dark: dark))
        .activitySystemActionForegroundColor(tint)
    }
}

/// Who and where: the tab (else the agent), then the Group and the agent.
struct Heading: View {
    let content: LiveContent

    var body: some View {
        let c = content
        let tab = (c.tab ?? "").isEmpty ? nil : c.tab
        let group = [c.groupIcon, c.group].compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: " ")
        let under = tab == nil ? (group.isEmpty ? nil : group) : [group, c.agent].filter { !$0.isEmpty }.joined(separator: " · ")
        VStack(alignment: .leading, spacing: 1) {
            Text(tab ?? c.agent).font(.headline).lineLimit(1)
            if let under, !under.isEmpty {
                Text(under).font(.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// Its context gauge and the others.
struct Footer: View {
    let content: LiveContent
    let tint: Color

    var body: some View {
        let c = content
        let more = (c.more ?? "").isEmpty ? nil : c.more
        if c.contextFraction != nil || more != nil {
            HStack(spacing: 8) {
                if let f = c.contextFraction {
                    Gauge(value: f) { Text("Context") }
                        .gaugeStyle(.accessoryLinearCapacity)
                        .labelsHidden()
                        .tint(tint)
                        .frame(width: 56)
                        .accessibilityLabel("Context \(c.ctx ?? 0)% full")
                    Text("\(c.ctx ?? 0)%").font(.caption2.monospacedDigit()).foregroundStyle(.secondary)
                }
                if let more {
                    Text(more).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: 0)
            }
        }
    }
}

/// How long it's been at it (or waiting); once it's over, when.
struct Elapsed: View {
    let content: LiveContent

    var body: some View {
        if content.over {
            Text("at ") + Text(content.started, style: .time)
        } else {
            Text("for ") + Text(timerInterval: content.started...Date.distantFuture, countsDown: false)
        }
    }
}

/// Allow and Deny, answered through the app once the phone's unlocked.
struct AnswerButtons: View {
    let content: LiveContent
    let host: String
    let tint: Color
    let ink: Color

    var body: some View {
        HStack(spacing: 8) {
            Button(intent: LiveAnswerIntent(host: host, pane: content.pane ?? "", item: content.item ?? "", prompt: content.prompt ?? "", allow: false)) {
                Text("Deny").frame(maxWidth: .infinity)
            }
            .buttonStyle(.bordered)
            .tint(.secondary)
            Button(intent: LiveAnswerIntent(host: host, pane: content.pane ?? "", item: content.item ?? "", prompt: content.prompt ?? "", allow: true)) {
                Text("Allow").frame(maxWidth: .infinity).foregroundStyle(ink)
            }
            .buttonStyle(.borderedProminent)
            .tint(tint)
        }
        .buttonBorderShape(.capsule)
        .font(.subheadline.weight(.semibold))
    }
}
