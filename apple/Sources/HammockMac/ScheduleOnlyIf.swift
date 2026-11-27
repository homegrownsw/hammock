import AppKit
import HammockKit
import SwiftUI

/// The editor's Only If: conditions a run it starts itself must meet, or
/// it's skipped (the daemon's schedonlyif.go), each a sentence edited in
/// place, added from a menu of their own.
struct ScheduleOnlyIfBox: View {
    @Binding var conditions: [ScheduleCondition]
    /// On another computer of yours (which may not be a Mac): only what
    /// any computer can tell.
    let computerID: String
    let model: AppModel

    var body: some View {
        RowBox {
            ForEach(conditions.indices, id: \.self) { i in
                ConditionRow(condition: $conditions[i]) { conditions.remove(at: i) }
            }
            Menu {
                if onThisMac {
                    Button("Mac Is Plugged In") { add(.with { $0.kind = .power }) }
                    Button("I’m Away") { add(.with { $0.kind = .away }) }
                    Button("On a Wi-Fi Network…") {
                        add(.with { $0.kind = .wifi; $0.wifi = ScheduleWhenBox.currentWiFi })
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
            } label: {
                Label("Add Condition", systemImage: "plus")
            }
            .menuStyle(.button)
            .buttonStyle(.borderless)
            .fixedSize()
            .help("A run it starts on its own is skipped unless all of these hold")
            .padding(.horizontal, 12)
            .padding(.vertical, 7)
        }
    }

    private var onThisMac: Bool { computerID.isEmpty || computerID == model.myUserID }

    private func add(_ c: ScheduleCondition) { conditions.append(c) }
}

/// One Only If condition, as a sentence.
private struct ConditionRow: View {
    @Binding var condition: ScheduleCondition
    let remove: () -> Void

    var body: some View {
        WhenRow(symbol: symbol, removeLabel: "Remove condition", remove: remove,
                footer: condition.missing.isEmpty ? nil : AnyView(Text(condition.missing).font(.caption).foregroundStyle(.orange))) {
            fields
        }
    }

    private var symbol: String {
        switch condition.kind {
        case .power: "powerplug"
        case .away: "figure.walk"
        case .wifi: "wifi"
        case .hours: "clock"
        case .days: "calendar"
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
            Text("I’m away")
            Picker("Away", selection: $condition.minutes) {
                ForEach(awayChoices, id: \.self) { m in Text(m == 0 ? "now" : "for \(Schedule.durationText(minutes: Int(m)))").tag(m) }
            }
            .labelsHidden()
            .fixedSize()
        case .wifi:
            Text("On the Wi-Fi")
            TextField("Network", text: $condition.wifi, prompt: Text("Network name"))
                .labelsHidden()
                .frame(width: 150)
        case .hours:
            Text("Between")
            DatePicker("From", selection: time(\.fromMinute), displayedComponents: .hourAndMinute)
                .labelsHidden()
                .datePickerStyle(.field)
                .fixedSize()
            Text("and")
            DatePicker("To", selection: time(\.toMinute), displayedComponents: .hourAndMinute)
                .labelsHidden()
                .datePickerStyle(.field)
                .fixedSize()
        case .days:
            Text("On")
            ForEach([1, 2, 3, 4, 5, 6, 0] as [Int32], id: \.self) { d in
                Button(Schedule.weekdaySymbols[Int(d)]) { toggle(d) }
                    .buttonStyle(ChipStyle(on: condition.weekdays.contains(d)))
                    .accessibilityAddTraits(condition.weekdays.contains(d) ? .isSelected : [])
            }
        case .free:
            Text("I’m not in a meeting")
        case .disk:
            Text("The startup disk has more than")
            Picker("Free", selection: $condition.percent) {
                ForEach(percentChoices, id: \.self) { p in Text("\(p)% free").tag(p) }
            }
            .labelsHidden()
            .fixedSize()
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

    private func toggle(_ d: Int32) {
        var set = Set(condition.weekdays)
        if set.contains(d) { set.remove(d) } else { set.insert(d) }
        condition.weekdays = set.sorted()
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

/// A This Mac trigger's sentence, its fields in it; target: the row's
/// token for what it watches (apps, drives, displays, networks).
struct MacTriggerFields<Target: View>: View {
    @Binding var trigger: MacTrigger
    let event: MacTrigger.Event
    @ViewBuilder let target: () -> Target

    var body: some View {
        switch event {
        case .diskLow:
            Text("Free space on")
            Picker("Disk", selection: $trigger.volume) {
                Text("the startup disk").tag("")
                ForEach(Self.volumes, id: \.self) { v in Text((v as NSString).lastPathComponent).tag(v) }
                if !trigger.volume.isEmpty, !Self.volumes.contains(trigger.volume) { Text(trigger.volume).tag(trigger.volume) }
            }
            .labelsHidden()
            .fixedSize()
            Text("falls below")
            Picker("Free", selection: diskMark) {
                ForEach([5, 10, 15, 20, 25] as [Int32], id: \.self) { p in Text("\(p)%").tag("p\(p)") }
                ForEach([10, 20, 50, 100] as [Int32], id: \.self) { g in Text("\(g) GB").tag("g\(g)") }
                if !diskMarks.contains(diskMark.wrappedValue) { Text(diskMarkText).tag(diskMark.wrappedValue) }
            }
            .labelsHidden()
            .fixedSize()
        case .batteryLow:
            Text("The battery falls below")
            Picker("Battery", selection: $trigger.percent) {
                ForEach(choices([5, 10, 15, 20, 30, 50], trigger.percent), id: \.self) { p in Text("\(p)%").tag(p) }
            }
            .labelsHidden()
            .fixedSize()
        case .pluggedIn, .unplugged:
            Text("The Mac is")
            Picker("Power", selection: $trigger.event) {
                Text("plugged in").tag(MacTrigger.Event.pluggedIn)
                Text("unplugged").tag(MacTrigger.Event.unplugged)
            }
            .labelsHidden()
            .fixedSize()
        case .away:
            Text("I’ve been away for")
            Picker("Away for", selection: $trigger.minutes) {
                ForEach(choices([5, 10, 15, 30, 60, 120], trigger.minutes), id: \.self) { m in Text(Schedule.durationText(minutes: Int(m))).tag(m) }
            }
            .labelsHidden()
            .fixedSize()
        case .back:
            Text("I’m back after")
            Picker("Away at least", selection: $trigger.minutes) {
                ForEach(choices([5, 10, 15, 30, 60, 120], trigger.minutes), id: \.self) { m in Text(Schedule.durationText(minutes: Int(m))).tag(m) }
            }
            .labelsHidden()
            .fixedSize()
            Text("or more away")
        case .wake:
            Text("The Mac wakes")
            Picker("After sleeping", selection: $trigger.minutes) {
                ForEach(choices([0, 15, 30, 60, 240, 480], trigger.minutes), id: \.self) { m in
                    Text(m == 0 ? "after any sleep" : "after sleeping \(Schedule.durationText(minutes: Int(m))) or more").tag(m)
                }
            }
            .labelsHidden()
            .fixedSize()
        case .appOpened, .appQuit:
            target()
            Picker("When", selection: $trigger.event) {
                Text("opens").tag(MacTrigger.Event.appOpened)
                Text("quits").tag(MacTrigger.Event.appQuit)
            }
            .labelsHidden()
            .fixedSize()
        case .drive:
            Text("A drive is connected:")
            target()
        case .display:
            Text("A display is connected:")
            target()
        case .wifiJoined, .wifiLeft:
            Text("The Mac")
            Picker("Joins or leaves", selection: $trigger.event) {
                Text("joins").tag(MacTrigger.Event.wifiJoined)
                Text("leaves").tag(MacTrigger.Event.wifiLeft)
            }
            .labelsHidden()
            .fixedSize()
            target()
        default:
            Text(trigger.text)
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

    private var diskMarkText: String { trigger.gigabytes > 0 ? "\(trigger.gigabytes) GB" : "\(trigger.percent)%" }

    /// Disks other than the startup disk, mounted now.
    private static var volumes: [String] {
        let urls = FileManager.default.mountedVolumeURLs(includingResourceValuesForKeys: [.volumeIsBrowsableKey], options: [.skipHiddenVolumes]) ?? []
        return urls.map(\.path).filter { $0.hasPrefix("/Volumes/") }
    }
}
