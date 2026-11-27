import AppKit
import HammockKit
import SwiftUI

/// The schedule editor's Only If: conditions a run it starts itself must
/// meet, or it's skipped (the daemon's schedonlyif.go), each edited in
/// place, added from a menu of their own.
struct ScheduleOnlyIfSection: View {
    @Binding var conditions: [ScheduleCondition]
    /// On another computer of yours (which may not be a Mac): only what
    /// any computer can tell.
    let computerID: String
    let model: AppModel

    var body: some View {
        Section {
            ForEach(conditions.indices, id: \.self) { i in
                ConditionRow(condition: $conditions[i]) { conditions.remove(at: i) }
            }
            Menu("Add Condition") {
                if onThisMac {
                    Button("Mac Is Plugged In") { add(.with { $0.kind = .power }) }
                    Button("I’m Away") { add(.with { $0.kind = .away }) }
                    Button("On a Wi-Fi Network…") {
                        add(.with { $0.kind = .wifi; $0.wifi = ScheduleTriggersSection.currentWiFi })
                        MacWatcher.shared.requestLocation()
                    }
                    Button("Not in a Meeting") {
                        add(.with { $0.kind = .free })
                        CalendarFeed.shared.requestAccess()
                    }
                    Divider()
                }
                Button("Between Times…") { add(.with { $0.kind = .hours; $0.fromMinute = 9 * 60; $0.toMinute = 17 * 60 }) }
                Button("On Days…") { add(.with { $0.kind = .days; $0.weekdays = [1, 2, 3, 4, 5] }) }
                Button("Disk Has Space…") { add(.with { $0.kind = .disk; $0.percent = 10 }) }
            }
            .fixedSize()
        } header: {
            Text("Only If")
        }
    }

    private var onThisMac: Bool { computerID.isEmpty || computerID == model.myUserID }

    private func add(_ c: ScheduleCondition) { conditions.append(c) }
}

/// One Only If condition.
private struct ConditionRow: View {
    @Binding var condition: ScheduleCondition
    let remove: () -> Void

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Image(systemName: symbol).foregroundStyle(.secondary).frame(width: 18)
            fields
            Spacer(minLength: 4)
            Button(action: remove) { Image(systemName: "minus.circle.fill").foregroundStyle(.secondary) }
                .buttonStyle(.borderless)
                .accessibilityLabel("Remove condition")
        }
    }

    private var symbol: String {
        switch condition.kind {
        case .power: "powerplug"
        case .away: "figure.walk"
        case .wifi: "wifi"
        case .hours: "clock"
        case .days: "calendar.day.timeline.left"
        case .free: "calendar.badge.checkmark"
        case .disk: "internaldrive"
        default: "questionmark"
        }
    }

    @ViewBuilder
    private var fields: some View {
        switch condition.kind {
        case .power:
            Text("The Mac is plugged in")
        case .away:
            Picker("I’m away", selection: $condition.minutes) {
                ForEach(awayChoices, id: \.self) { m in Text(m == 0 ? "Now" : "For \(Schedule.durationText(minutes: Int(m)))").tag(m) }
            }
        case .wifi:
            TextField("On the Wi-Fi", text: $condition.wifi, prompt: Text("Network name"))
        case .hours:
            HStack {
                DatePicker("Between", selection: time(\.fromMinute), displayedComponents: .hourAndMinute)
                DatePicker("and", selection: time(\.toMinute), displayedComponents: .hourAndMinute)
            }
        case .days:
            HStack(spacing: 4) {
                Text("On")
                ForEach([1, 2, 3, 4, 5, 6, 0] as [Int32], id: \.self) { d in
                    Toggle(Schedule.weekdaySymbols[Int(d)], isOn: day(d))
                        .toggleStyle(.button)
                        .controlSize(.small)
                }
            }
        case .free:
            Text("I’m not in a meeting")
        case .disk:
            Picker("The startup disk has more than", selection: $condition.percent) {
                ForEach(percentChoices, id: \.self) { p in Text("\(p)% free").tag(p) }
            }
        default:
            Text(condition.text)
        }
    }

    private var awayChoices: [Int32] {
        let base: [Int32] = [0, 5, 10, 15, 30, 60, 120]
        return base.contains(condition.minutes) ? base : (base + [condition.minutes]).sorted()
    }

    private var percentChoices: [Int32] {
        let base: [Int32] = [5, 10, 15, 20, 25, 50]
        return base.contains(condition.percent) ? base : (base + [condition.percent]).sorted()
    }

    private func day(_ d: Int32) -> Binding<Bool> {
        Binding(get: { condition.weekdays.contains(d) },
                set: { on in
                    var set = Set(condition.weekdays)
                    if on { set.insert(d) } else { set.remove(d) }
                    condition.weekdays = set.sorted()
                })
    }

    private func time(_ kp: WritableKeyPath<ScheduleCondition, Int32>) -> Binding<Date> {
        Binding(get: {
            let m = Int(condition[keyPath: kp])
            return Calendar.current.date(bySettingHour: m / 60, minute: m % 60, second: 0, of: Date()) ?? Date()
        }, set: { d in
            let c = Calendar.current.dateComponents([.hour, .minute], from: d)
            condition[keyPath: kp] = Int32((c.hour ?? 0) * 60 + (c.minute ?? 0))
        })
    }
}

extension ScheduleCondition {
    /// What still needs filling in before it can be saved ("" when nothing).
    var missing: String {
        switch kind {
        case .wifi: wifi.trimmingCharacters(in: .whitespaces).isEmpty ? "Which Wi-Fi network?" : ""
        case .days: weekdays.isEmpty ? "Which days?" : ""
        case .hours: fromMinute == toMinute ? "The times are the same" : ""
        default: ""
        }
    }
}

/// A This Mac trigger's fields.
struct MacTriggerFields: View {
    @Binding var trigger: MacTrigger
    let event: MacTrigger.Event

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            switch event {
            case .diskLow:
                Picker("Disk", selection: $trigger.volume) {
                    Text("The startup disk").tag("")
                    ForEach(Self.volumes, id: \.self) { v in Text((v as NSString).lastPathComponent).tag(v) }
                    if !trigger.volume.isEmpty, !Self.volumes.contains(trigger.volume) { Text(trigger.volume).tag(trigger.volume) }
                }
                Picker("Has less than", selection: diskMark) {
                    ForEach([5, 10, 15, 20, 25] as [Int32], id: \.self) { p in Text("\(p)% free").tag("p\(p)") }
                    ForEach([10, 20, 50, 100] as [Int32], id: \.self) { g in Text("\(g) GB free").tag("g\(g)") }
                    if !diskMarks.contains(diskMark.wrappedValue) { Text(diskMarkText).tag(diskMark.wrappedValue) }
                }
            case .batteryLow:
                Picker("The battery falls below", selection: $trigger.percent) {
                    ForEach(choices([5, 10, 15, 20, 30, 50], trigger.percent), id: \.self) { p in Text("\(p)%").tag(p) }
                }
            case .pluggedIn, .unplugged:
                Picker("The Mac is", selection: $trigger.event) {
                    Text("Plugged in").tag(MacTrigger.Event.pluggedIn)
                    Text("Unplugged").tag(MacTrigger.Event.unplugged)
                }
            case .away:
                Picker("I’ve been away for", selection: $trigger.minutes) {
                    ForEach(choices([5, 10, 15, 30, 60, 120], trigger.minutes), id: \.self) { m in Text(Schedule.durationText(minutes: Int(m))).tag(m) }
                }
            case .back:
                Picker("I’m back after", selection: $trigger.minutes) {
                    ForEach(choices([5, 10, 15, 30, 60, 120], trigger.minutes), id: \.self) { m in Text("\(Schedule.durationText(minutes: Int(m))) or more away").tag(m) }
                }
            case .wake:
                Picker("The Mac wakes", selection: $trigger.minutes) {
                    ForEach(choices([0, 15, 30, 60, 240, 480], trigger.minutes), id: \.self) { m in
                        Text(m == 0 ? "After any sleep" : "After sleeping \(Schedule.durationText(minutes: Int(m))) or more").tag(m)
                    }
                }
            case .appOpened, .appQuit:
                HStack {
                    Picker("When", selection: $trigger.event) {
                        Text("Opens").tag(MacTrigger.Event.appOpened)
                        Text("Quits").tag(MacTrigger.Event.appQuit)
                    }
                    .fixedSize()
                    TextField("App", text: $trigger.name, prompt: Text("Xcode, or com.apple.dt.Xcode"))
                    Button("Choose…") { chooseApp() }
                }
            case .drive:
                TextField("A drive is connected, named", text: $trigger.name, prompt: Text("Any drive"))
            case .display:
                TextField("A display is connected, named", text: $trigger.name, prompt: Text("Any display"))
            case .wifiJoined, .wifiLeft:
                Picker("The Mac", selection: $trigger.event) {
                    Text("Joins").tag(MacTrigger.Event.wifiJoined)
                    Text("Leaves").tag(MacTrigger.Event.wifiLeft)
                }
                TextField("Wi-Fi network", text: $trigger.name, prompt: Text("Any network"))
            default:
                Text(trigger.text)
            }
            if trigger.appWatched {
                Text("Works while Hammock is open.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func choices(_ base: [Int32], _ cur: Int32) -> [Int32] {
        base.contains(cur) ? base : (base + [cur]).sorted()
    }

    private var diskMarks: [String] { [5, 10, 15, 20, 25].map { "p\($0)" } + [10, 20, 50, 100].map { "g\($0)" } }

    /// The disk's mark as one choice: "p10" (percent) or "g50" (GB).
    private var diskMark: Binding<String> {
        Binding(get: { trigger.gigabytes > 0 ? "g\(trigger.gigabytes)" : "p\(trigger.percent > 0 ? trigger.percent : 10)" },
                set: { v in
                    let n = Int32(v.dropFirst()) ?? 10
                    if v.hasPrefix("g") { trigger.gigabytes = n; trigger.percent = 0 } else { trigger.percent = n; trigger.gigabytes = 0 }
                })
    }

    private var diskMarkText: String { trigger.gigabytes > 0 ? "\(trigger.gigabytes) GB free" : "\(trigger.percent)% free" }

    /// Disks other than the startup disk, mounted now.
    private static var volumes: [String] {
        let urls = FileManager.default.mountedVolumeURLs(includingResourceValuesForKeys: [.volumeIsBrowsableKey], options: [.skipHiddenVolumes]) ?? []
        return urls.map(\.path).filter { $0.hasPrefix("/Volumes/") }
    }

    private func chooseApp() {
        let panel = NSOpenPanel()
        panel.allowedContentTypes = [.application]
        panel.directoryURL = URL(fileURLWithPath: "/Applications")
        panel.message = "Choose the app this trigger watches"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        trigger.name = FileManager.default.displayName(atPath: url.path).replacingOccurrences(of: ".app", with: "")
    }
}
