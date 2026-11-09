import AppKit
import HammockKit
import SwiftUI

/// Settings ▸ Keyboard: every action's shortcut, searchable, each
/// recorded by pressing it. Changes are `keybind` lines in the config file
/// (`AppConfig.setKeybinds`), which the menus, palette and tooltips follow.
struct KeyboardSettings: View {
    private var config: AppConfig { .shared }
    @State private var query: String
    @State private var pending: Pending?
    @FocusState private var searchFocused: Bool

    init(initialQuery: String = "") { _query = State(initialValue: initialQuery) }

    /// A shortcut that needs saying yes to: it's another action's, or macOS's.
    private struct Pending: Identifiable {
        let action: KeybindAction
        let chord: KeyChord
        let takenFrom: [KeybindAction]
        let reserved: String?
        let terminal: Bool
        var id: String { action.id + chord.configString }
    }

    var body: some View {
        VStack(spacing: 0) {
            searchField
                .padding(.horizontal, 20)
                .padding(.top, 12)
            if !config.keybindProblems.isEmpty { problems }
            Form {
                if query.trimmingCharacters(in: .whitespaces).isEmpty {
                    SettingsPaneHeader(tab: .keyboard)
                    ForEach(Keybinds.groups, id: \.self) { group in
                        Section(group) {
                            ForEach(Keybinds.actions.filter { $0.group == group }) { row($0, showGroup: false) }
                        }
                    }
                } else {
                    let results = KeybindSearch.rank(query, in: config.keybinds)
                    Section {
                        if results.isEmpty {
                            Text("No shortcuts match “\(query)”.")
                                .foregroundStyle(.secondary)
                        }
                        ForEach(results) { row($0, showGroup: true) }
                    }
                }
            }
            .formStyle(.grouped)
            // The window's background behind all of it, so the search field and
            // footer don't sit on a band of a different shade.
            .scrollContentBackground(.hidden)
            .settingsScrollTarget()
            footer
        }
        // Settings' own search picked a shortcut: show it here.
        .onChange(of: SettingsNav.shared.keyboardQuery, initial: true) { _, q in
            guard let q else { return }
            query = q
            SettingsNav.shared.keyboardQuery = nil
        }
        .alert(pending.map(alertTitle) ?? "", isPresented: Binding(get: { pending != nil }, set: { if !$0 { pending = nil } }), presenting: pending) { p in
            Button(p.takenFrom.isEmpty ? "Use Anyway" : "Replace") { apply(p.chord, to: p.action, checked: true) }
            Button("Cancel", role: .cancel) {}
        } message: { p in
            Text(alertMessage(p))
        }
        .onDisappear { ShortcutRecording.shared.stop() }
    }

    // MARK: Pieces

    private var searchField: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass").foregroundStyle(.secondary)
            TextField("Search actions or shortcuts (⌘D, ctrl cmd)", text: $query)
                .textFieldStyle(.plain)
                .focused($searchFocused)
            if !query.isEmpty {
                Button { query = "" } label: { Image(systemName: "xmark.circle.fill") }
                    .buttonStyle(.plain)
                    .foregroundStyle(.tertiary)
                    .help("Clear Search")
            }
        }
        .padding(.horizontal, 8)
        .frame(height: 28)
        .background(RoundedRectangle(cornerRadius: 7).fill(Color(nsColor: .textBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Color.primary.opacity(searchFocused ? 0 : 0.12), lineWidth: 0.5))
        .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(Color.themeAccent.opacity(searchFocused ? 0.6 : 0), lineWidth: 2.5).padding(-1.5))
    }

    private var problems: some View {
        VStack(alignment: .leading, spacing: 3) {
            Label("\(config.keybindProblems.count) keybind line\(config.keybindProblems.count == 1 ? "" : "s") in the config file couldn't be used:", systemImage: "exclamationmark.triangle.fill")
                .foregroundStyle(.orange)
            ForEach(config.keybindProblems, id: \.self) { Text($0.description).foregroundStyle(.secondary).padding(.leading, 22) }
            Text("Saving here keeps them in the file, to fix there.").foregroundStyle(.tertiary).padding(.leading, 22)
        }
        .font(.callout)
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 22)
        .padding(.top, 10)
    }

    private func row(_ a: KeybindAction, showGroup: Bool) -> some View {
        let chord = config.keybinds[a.id]
        let changed = !config.keybinds.isDefault(a.id)
        return HStack(spacing: 8) {
            VStack(alignment: .leading, spacing: 1) {
                Text(a.title)
                if showGroup {
                    Text(a.group).font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: 12)
            if changed {
                Button { apply(a.defaultChord, to: a, checked: false) } label: {
                    Image(systemName: "arrow.uturn.backward.circle.fill")
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .help(a.defaultChord.map { "Reset to \($0.label)" } ?? "Reset to no shortcut")
            }
            ShortcutRecorder(id: a.id, chord: chord, family: a.isFamily) { record($0, for: a) }
        }
        .settingAnchor("key.\(a.id)")
        .contextMenu {
            Button("Reset to Default") { apply(a.defaultChord, to: a, checked: false) }.disabled(!changed)
            Button("Remove Shortcut") { apply(nil, to: a) }.disabled(chord == nil)
            Divider()
            Button("Copy Config Line") {
                let line = "keybind = \(chord.map(\.configString) ?? "none")=\(a.id)"
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(line, forType: .string)
            }
        }
    }

    private var footer: some View {
        HStack {
            let changed = Keybinds.actions.filter { !config.keybinds.isDefault($0.id) }.count
            Text(changed == 0 ? "All shortcuts are Hammock's defaults." : "\(changed) shortcut\(changed == 1 ? "" : "s") changed from the defaults.")
                .font(.callout)
                .foregroundStyle(.secondary)
            Spacer()
            Button("Open Config File") {
                config.ensureFile()
                NSWorkspace.shared.open(config.fileURL)
            }
            .help("keybind lines in \(AppConfig.shared.displayPath); edits apply within a couple of seconds")
            Button("Reset All…") { confirmResetAll() }
                .disabled(changed == 0 && config.keybindProblems.isEmpty)
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 10)
    }

    // MARK: Changing

    /// A recorded shortcut (nil: removed), checked against the others and macOS's.
    private func record(_ chord: KeyChord?, for a: KeybindAction) {
        apply(chord, to: a, checked: false)
    }

    /// Unchecked, a shortcut another action has, macOS keeps or the
    /// terminal needs asks first.
    private func apply(_ chord: KeyChord?, to a: KeybindAction, checked: Bool = true) {
        if let chord, !checked {
            let taken = config.keybinds.conflicts(chord, excluding: a.id).compactMap { Keybinds.byID[$0] }
            let reserved = Keybinds.reservedReason(chord)
            // Menus see keys before the terminal does: without ⌘, a shortcut
            // is taken from the programs in your panes (⌃C, ⌥B, F-keys).
            let terminal = !chord.modifiers.contains(.command)
            if !taken.isEmpty || reserved != nil || terminal {
                pending = Pending(action: a, chord: chord, takenFrom: taken, reserved: reserved, terminal: terminal)
                return
            }
        }
        var binds = config.keybinds
        binds.assign(chord, to: a.id)
        config.setKeybinds(binds)
    }

    private func confirmResetAll() {
        let alert = NSAlert()
        alert.messageText = "Reset all shortcuts to Hammock's defaults?"
        alert.informativeText = "This removes every keybind line from the config file, including any that couldn't be read."
        alert.addButton(withTitle: "Reset All")
        alert.addButton(withTitle: "Cancel")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        config.set("keybind", values: [])
    }

    private func alertTitle(_ p: Pending) -> String {
        if let first = p.takenFrom.first {
            return "\(p.chord.label) is already \(p.takenFrom.count == 1 ? "used for \(first.title)" : "used for \(p.takenFrom.count) actions")"
        }
        if let r = p.reserved { return "\(p.chord.label) is macOS’s shortcut for \(r)" }
        return "\(p.chord.label) won’t reach the programs in your panes"
    }

    private func alertMessage(_ p: Pending) -> String {
        var parts: [String] = []
        if !p.takenFrom.isEmpty {
            let names = p.takenFrom.map(\.title).formatted(.list(type: .and))
            parts.append("Use it for \(p.action.title) instead? \(names) will have no shortcut.")
        }
        if let r = p.reserved {
            parts.append(p.takenFrom.isEmpty
                ? "It may never reach Hammock, or using it here stops \(r) working from the keyboard."
                : "It's also macOS’s shortcut for \(r), so it may never reach Hammock.")
        }
        if p.terminal {
            parts.append("Without ⌘, Hammock takes it before the terminal and the programs running there see it: shells and editors use keys like ⌃C, ⌥B and the F-keys.")
        }
        return parts.joined(separator: " ")
    }
}

/// The shortcut field: click it, press the keys. Esc cancels, ⌫ removes the
/// shortcut; a key needs ⌘, ⌃ or ⌥ (or is an F-key). A numbered family
/// takes its modifiers from a digit pressed with them.
struct ShortcutRecorder: View {
    let id: String
    let chord: KeyChord?
    let family: Bool
    let onRecord: (KeyChord?) -> Void
    private var recording: ShortcutRecording { .shared }

    private var isRecording: Bool { recording.activeID == id }

    var body: some View {
        HStack(spacing: 4) {
            Text(text)
                .font(.system(size: 12, weight: chord != nil && !isRecording ? .medium : .regular))
                .foregroundStyle(chord == nil || isRecording ? .secondary : .primary)
                .lineLimit(1)
                .frame(maxWidth: .infinity)
            if chord != nil, !isRecording {
                Button { onRecord(nil) } label: { Image(systemName: "xmark.circle.fill") }
                    .buttonStyle(.plain)
                    .foregroundStyle(.tertiary)
                    .help("Remove Shortcut")
            }
        }
        .padding(.horizontal, 7)
        .frame(width: 136, height: 22)
        .background(RoundedRectangle(cornerRadius: 6).fill(isRecording ? Color.themeAccent.opacity(0.12) : Color(nsColor: .controlBackgroundColor)))
        .overlay(RoundedRectangle(cornerRadius: 6).strokeBorder(isRecording ? Color.themeAccent : Color.primary.opacity(0.16), lineWidth: isRecording ? 1.5 : 0.5))
        .contentShape(Rectangle())
        .onTapGesture { recording.start(id: id, family: family, onRecord) }
        .help(isRecording ? "Press the new shortcut. Esc cancels; ⌫ removes it." : family ? "Click, then press modifiers with a digit (⌃⌘1)" : "Click to record a shortcut")
        .accessibilityElement(children: .combine)
        .accessibilityLabel(chord?.label ?? "No shortcut")
        .accessibilityHint("Records a new shortcut")
        .accessibilityAddTraits(.isButton)
    }

    private var text: String {
        guard isRecording else { return chord?.label ?? "Record Shortcut" }
        let held = KeyChord("", recording.held).label
        if family { return held.isEmpty ? "Modifiers + 1…9" : held + "1…9" }
        return held.isEmpty ? "Type Shortcut…" : held + "…"
    }
}

/// The one shortcut being recorded, app-wide: keys go to it, not the menus,
/// until it has one, is cancelled, or something else is clicked.
@MainActor
@Observable
final class ShortcutRecording {
    static let shared = ShortcutRecording()
    private(set) var activeID: String?
    private(set) var held: KeyChord.Modifiers = []
    @ObservationIgnored private var monitor: Any?
    @ObservationIgnored private var resign: NSObjectProtocol?
    @ObservationIgnored private var family = false
    @ObservationIgnored private var done: ((KeyChord?) -> Void)?

    func start(id: String, family: Bool, _ done: @escaping (KeyChord?) -> Void) {
        guard activeID != id else { return }
        stop()
        activeID = id
        self.family = family
        self.done = done
        monitor = NSEvent.addLocalMonitorForEvents(matching: [.keyDown, .flagsChanged, .leftMouseDown, .rightMouseDown]) { [weak self] e in
            self?.handle(e) ?? e
        }
        resign = NotificationCenter.default.addObserver(forName: NSWindow.didResignKeyNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.stop() }
        }
    }

    func stop() {
        if let monitor { NSEvent.removeMonitor(monitor) }
        if let resign { NotificationCenter.default.removeObserver(resign) }
        monitor = nil
        resign = nil
        activeID = nil
        held = []
        done = nil
    }

    private func handle(_ e: NSEvent) -> NSEvent? {
        switch e.type {
        case .flagsChanged:
            held = Self.modifiers(e.modifierFlags)
            return e
        case .leftMouseDown, .rightMouseDown:
            // A click elsewhere ends it (one on the field starts it again).
            stop()
            return e
        default:
            break
        }
        let mods = Self.modifiers(e.modifierFlags)
        guard let chord = KeyChord(event: e) else { NSSound.beep(); return nil }
        let finish = { (c: KeyChord?) in
            let done = self.done
            self.stop()
            done?(c)
        }
        if mods.isEmpty {
            switch chord.key {
            case "escape", "tab": stop(); return nil
            case "delete", "forward_delete": finish(nil); return nil
            default: break
            }
        }
        let hasModifier = !mods.isDisjoint(with: [.command, .control, .option])
        if family {
            guard hasModifier, chord.key.count == 1, ("1"..."9").contains(chord.key) else { NSSound.beep(); return nil }
            finish(KeyChord(KeyChord.digits, mods))
        } else {
            guard hasModifier || chord.isFunctionKey else { NSSound.beep(); return nil }
            finish(chord)
        }
        return nil
    }

    private static func modifiers(_ flags: NSEvent.ModifierFlags) -> KeyChord.Modifiers {
        var m: KeyChord.Modifiers = []
        if flags.contains(.control) { m.insert(.control) }
        if flags.contains(.option) { m.insert(.option) }
        if flags.contains(.shift) { m.insert(.shift) }
        if flags.contains(.command) { m.insert(.command) }
        return m
    }
}

/// Which Settings pane shows (remembered, and set to open one directly).
enum SettingsTab: String {
    case general, appearance, terminal, editor, keyboard, agents, agentView, notifications, sharing, devices, ssh, about
    static let key = "settingsTab"

    static func select(_ tab: SettingsTab) { UserDefaults.standard.set(tab.rawValue, forKey: key) }
}
