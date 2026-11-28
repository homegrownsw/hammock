import SwiftUI
import WatchKit

/// The agents' usage limits, and scheduled tasks coming up (run one now).
struct UsageView: View {
    @State private var link = PhoneLink.shared
    @State private var running: String?
    @State private var problem: String?
    @Environment(\.watchTheme) private var theme

    var body: some View {
        TimelineView(.periodic(from: .now, by: 60)) { ctx in
            List {
                if let s = link.snapshot {
                    ForEach(s.usage, id: \.harness) { u in
                        Section {
                            ForEach(u.windows, id: \.label) { w in window(w, now: ctx.date) }
                        } header: {
                            Label { Text(u.name) } icon: { AgentMark(harness: u.harness, size: 12) }
                        }
                    }
                    if !s.schedules.isEmpty {
                        Section("Coming Up") {
                            ForEach(s.schedules) { schedule($0, macs: s.showsMacNames) }
                        }
                    }
                    if s.usage.isEmpty && s.schedules.isEmpty {
                        Text("Choose the agents whose limits to show in Hammock's Settings on your iPhone.")
                            .font(.footnote).foregroundStyle(.secondary).listRowBackground(Color.clear)
                    }
                }
                if let problem {
                    Text(problem).font(.footnote).foregroundStyle(StatusPalette.red).listRowBackground(Color.clear)
                }
            }
        }
        .navigationTitle("Usage")
    }

    private func window(_ w: WatchUsage.Window, now: Date) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text("\(w.label) · \(Int(w.usedPercent.rounded()))%")
                Spacer()
                if let r = w.resetsAt { Text(Self.until(r, now: now)).foregroundStyle(.secondary) }
            }
            .font(.footnote)
            Gauge(value: min(max(w.usedPercent, 0), 100), in: 0...100) { EmptyView() }
                .gaugeStyle(.linearCapacity)
                .tint(w.usedPercent >= 80 ? StatusPalette.red : w.usedPercent >= 50 ? StatusPalette.amber : theme.accent)
        }
        .accessibilityElement(children: .combine)
    }

    private func schedule(_ s: WatchSchedule, macs: Bool) -> some View {
        HStack(spacing: 8) {
            VStack(alignment: .leading, spacing: 1) {
                Text(s.name).font(.headline).lineLimit(2)
                Text(s.nextRun, format: .relative(presentation: .named)).font(.caption2).foregroundStyle(.secondary)
                if macs { Text(s.hostName).font(.caption2).foregroundStyle(.tertiary) }
            }
            Spacer(minLength: 4)
            Button {
                Task { await run(s) }
            } label: {
                if running == s.id { ProgressView() } else { Image(systemName: "play.fill") }
            }
            .frame(width: 36, height: 36)
            .buttonStyle(.borderedProminent)
            .buttonBorderShape(.circle)
            .tint(theme.accent)
            .accessibilityLabel("Run \(s.name) Now")
            .disabled(running != nil)
        }
    }

    private func run(_ s: WatchSchedule) async {
        running = s.id
        defer { running = nil }
        do {
            _ = try await PhoneLink.shared.ask(.runSchedule(host: s.host, schedule: s.scheduleID))
            WKInterfaceDevice.current().play(.start)
            problem = nil
            await link.refresh()
        } catch {
            problem = error.localizedDescription
        }
    }

    /// "resets in 1h 20m", "resets Tue".
    static func until(_ date: Date, now: Date) -> String {
        let m = max(0, Int(date.timeIntervalSince(now) / 60))
        if m >= 24 * 60 { return "resets " + date.formatted(.dateTime.weekday(.abbreviated)) }
        return m >= 60 ? "resets \(m / 60)h \(m % 60)m" : "resets \(m)m"
    }
}
