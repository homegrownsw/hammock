import SwiftUI

/// Agents' usage limits (Claude's and Codex's 5-hour and weekly windows), as
/// the Mac's agents last reported them: shown in the Mac's sidebar and at the
/// bottom of the iPhone's sessions, each agent only once chosen in Settings.
public enum UsageLimitsPrefs {
    /// UserDefaults' key (the iPhone's); Prefs reads it, and the Mac's config key.
    public static func key(_ harness: String) -> String { "sidebarUsage." + harness }
    /// The agents whose limits can be shown, as Settings names them: those
    /// installed on the Mac (the daemon's catalogue), and any switched on
    /// already, so it can be switched off.
    @MainActor public static var agents: [(harness: String, name: String)] {
        let on = UsageChoice.shared.on
        return AgentCatalog.withUsage
            .filter { $0.installed || on.contains($0.id) }
            .map { ($0.id, AgentCatalog.shortName(of: $0.id)) }
    }

    /// Called with the ids of the agents chosen (sorted) whenever the
    /// choice changes: the Mac keeps usage-live-agents to it, so the daemon
    /// asks only their services for their usage.
    @MainActor public static var onChosenChange: (([String]) -> Void)?

    /// The ids of the agents chosen, sorted, including any the catalogue
    /// doesn't list yet.
    @MainActor public static var chosenIDs: [String] { UsageChoice.shared.on.sorted() }

    /// The agents chosen in Settings, in the agents' order (observed).
    @MainActor public static func chosen() -> [String] {
        let on = UsageChoice.shared.on
        return agents.map(\.harness).filter { on.contains($0) }
    }

    /// The agents chosen, with their usage, in the agents' order.
    @MainActor public static func shown(_ model: AppModel) -> [Hammock_V1_AgentUsage] {
        chosen().compactMap { h in model.usage.first { $0.harness == h } }
    }

    /// "3h30m", "45m", "1d5h", "2d".
    public static func until(_ date: Date, now: Date) -> String {
        let m = max(Int(date.timeIntervalSince(now) / 60), 0)
        let d = m / 1440, h = (m % 1440) / 60, mm = m % 60
        // Whole days from two up, to the nearest (2 days 22 hours is "3d").
        if d >= 2 { return "\(Int((Double(m) / 1440).rounded()))d" }
        if d == 1 { return h > 0 ? "1d\(h)h" : "1d" }
        if h > 0 { return mm > 0 ? "\(h)h\(mm)m" : "\(h)h" }
        return "\(mm)m"
    }
}

/// Settings' switch for showing an agent's limits.
public struct UsageToggle: View {
    let harness: String

    public init(harness: String) { self.harness = harness }

    public var body: some View {
        Toggle(AgentCatalog.shortName(of: harness), isOn: Prefs.shared.boolBinding(.usageLimits(harness)))
    }
}

/// Which agents' limits are switched on (Prefs: the Mac's config file, the
/// iPhone's UserDefaults), kept so views showing them follow Settings'
/// switches, however many agents there are.
@MainActor
@Observable
final class UsageChoice {
    static let shared = UsageChoice()
    private(set) var on: Set<String> = []

    private init() { refresh() }

    /// Reads the choice, and again whenever a setting it read (or the
    /// catalogue) changes: only touches `on` when the choice did. The names
    /// known too, so one chosen last time counts before the catalogue lists
    /// it.
    func refresh() {
        let now = withObservationTracking {
            Set(AgentCatalog.shared.kinds.map(\.id)).union(AgentCatalog.known.keys)
                .filter { Prefs.shared.bool(.usageLimits($0)) }
        } onChange: {
            Task { @MainActor in UsageChoice.shared.refresh() }
        }
        if now != on {
            on = now
            UsageLimitsPrefs.onChosenChange?(now.sorted())
        }
    }
}

/// Each agent's windows: a slim bar per window with how much is used and
/// how long until it resets.
public struct UsageLimitsView: View {
    let usage: [Hammock_V1_AgentUsage]
    /// Now, for the time to each reset: the caller's TimelineView's date.
    /// (Not one of its own: nested in the iPhone's Sessions, a List inside a
    /// TimelineView, one froze the app as it scrolled into view.)
    let now: Date
    let tint: Color
    let font: Font
    let labelWidth: CGFloat

    public init(usage: [Hammock_V1_AgentUsage], now: Date, tint: Color, font: Font = .caption2, labelWidth: CGFloat = 22) {
        self.usage = usage
        self.now = now
        self.tint = tint
        self.font = font
        self.labelWidth = labelWidth
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            ForEach(usage, id: \.harness) { a in
                VStack(alignment: .leading, spacing: 3) {
                    Text(AgentCatalog.shortName(of: a.harness))
                        .font(font.weight(.semibold))
                        .foregroundStyle(.secondary)
                    ForEach(a.windows, id: \.label) { w in row(w, now: now) }
                }
                .help(updated(a))
            }
        }
    }

    private func row(_ w: Hammock_V1_UsageWindow, now: Date) -> some View {
        // Past its reset, the window has started again (until the agent says more).
        let reset = w.resetsUnix > 0 ? Date(timeIntervalSince1970: TimeInterval(w.resetsUnix)) : nil
        // Not `$0 <= now`: Date's `<=` is weak-linked from Foundation on the
        // macOS 27.2 SDK, and a 27.2 beta (26B5086k) that lacks it jumped to
        // null and crashed at launch. Comparing seconds needs no Foundation.
        let over = reset.map { $0.timeIntervalSince(now) <= 0 } ?? false
        let used = over ? 0 : min(max(w.usedPercent, 0), 100)
        let fill: Color = used >= 90 ? .red : used >= 75 ? .orange : tint
        return HStack(spacing: 6) {
            Text(w.label)
                .frame(width: labelWidth, alignment: .leading)
            Capsule()
                .fill(Color.primary.opacity(0.1))
                .overlay(alignment: .leading) {
                    GeometryReader { g in
                        Capsule().fill(fill).frame(width: max(g.size.width * used / 100, used > 0 ? 3 : 0))
                    }
                }
                .frame(height: 4)
            Text("\(Int(used.rounded()))%")
                .frame(width: labelWidth * 1.5, alignment: .trailing)
            Text(over || reset == nil ? "—" : UsageLimitsPrefs.until(reset!, now: now))
                .frame(width: labelWidth * 2, alignment: .leading)
        }
        .font(font.monospacedDigit())
        .foregroundStyle(.secondary)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(w.label) limit: \(Int(used.rounded())) percent used")
    }

    private func updated(_ a: Hammock_V1_AgentUsage) -> String {
        guard a.fetchedUnixMs > 0 else { return "As the agent last reported" }
        let t = Date(timeIntervalSince1970: TimeInterval(a.fetchedUnixMs) / 1000)
        return "As the agent last reported, \(t.formatted(.relative(presentation: .named, unitsStyle: .wide)))"
    }
}
