import SwiftUI

// Design notes: numbered pins dropped on a browser pane's page or a
// simulator pane's screen, each with a note, sent to an agent as one
// message with a screenshot showing them (the daemon's pins.go builds it).
// The board is the pane's (Pane.pins), the same on every device; which
// panes are taking pins (pin mode) is this app's own. Each app draws the
// overlay its own way (the Mac's click and hover, the iPhone's touch and
// hold); the marks, the note editor and the Send bar are shared here.

public typealias Pin = Hammock_V1_Pin

/// Which panes are taking pins in this app: clicks (taps) there drop one,
/// rather than reaching the page or device.
@MainActor
@Observable
public final class PinMode {
    public static let shared = PinMode()
    public private(set) var panes: Set<String> = []
    /// The pin whose note is open to edit (one at a time).
    public var editing: String?

    /// HMK_DEBUG_PIN_MODE=1: on in every pane that takes pins, for snapshots.
    static let debugAll = ProcessInfo.processInfo.environment["HMK_DEBUG_PIN_MODE"] != nil
    static let samplePins: [Pin] = [
        Pin.with {
            $0.id = "pin_sample1"; $0.number = 1; $0.x = 0.80; $0.y = 0.475; $0.note = "Make this the theme's green"
            $0.what = "button.btn.btn-primary “Save changes”"
            $0.rect = .with { $0.x = 0.717; $0.y = 0.451; $0.w = 0.169; $0.h = 0.05 }
        },
        Pin.with {
            $0.id = "pin_sample2"; $0.number = 2; $0.x = 0.25; $0.y = 0.17
            $0.what = "h1 “Profile settings”"
            $0.rect = .with { $0.x = 0.116; $0.y = 0.149; $0.w = 0.20; $0.h = 0.05 }
        },
    ]

    public func isOn(_ paneID: String) -> Bool { Self.debugAll || panes.contains(paneID) }

    public func set(_ paneID: String, _ on: Bool) {
        if on { panes.insert(paneID) } else { panes.remove(paneID) }
        if !on { editing = nil }
    }

    public func toggle(_ paneID: String) { set(paneID, !isOn(paneID)) }

    /// Notes being typed, not yet saved (by pin: its pane and text): Send
    /// saves them first.
    @ObservationIgnored private var drafts: [String: (pane: String, text: String)] = [:]

    func draft(_ pinID: String, pane: String, text: String?) { drafts[pinID] = text.map { (pane, $0) } }

    func takeDrafts(_ paneID: String) -> [(String, String)] {
        let mine = drafts.filter { $0.value.pane == paneID }
        for id in mine.keys { drafts[id] = nil }
        return mine.map { ($0.key, $0.value.text) }
    }
}

extension AppModel {
    /// Whether a pane takes design notes: a browser's page or a simulator's screen.
    public func canPin(_ paneID: String) -> Bool {
        guard let p = panes[paneID] else { return false }
        return p.kind == .browser || p.kind == .simulator
    }

    /// A pane's pins, in order. HMK_DEBUG_PIN_MODE: made-up ones where it has none, for snapshots.
    public func pins(_ paneID: String) -> [Pin] {
        let real = panes[paneID]?.pins.pins ?? []
        if PinMode.debugAll, real.isEmpty, canPin(paneID) { return PinMode.samplePins }
        return real
    }

    /// The agents a pane's notes can go to (those running in its Group), and
    /// the one they go to now (Hammock's choice, or yours).
    public func pinAgents(for paneID: String) -> (target: DiffAgent?, agents: [DiffAgent]) {
        guard let pane = panes[paneID] else { return (nil, []) }
        return (panes[pane.pins.targetPaneID].map(groupAgent), groupAgents(of: paneID))
    }

    /// An agent as a picker names it: its kind, and its tab.
    func groupAgent(_ p: Pane) -> DiffAgent {
        let tab = self.tab(of: p).map { title(of: $0) } ?? ""
        return DiffAgent(id: p.id, name: AgentCatalog.shortName(of: p.agent.harness), detail: tab)
    }

    /// The agents running in a pane's Group, by tab.
    func groupAgents(of paneID: String) -> [DiffAgent] {
        let group = self.group(ofPane: paneID)?.id
        var list: [DiffAgent] = []
        for p in panes.values where p.hasAgent && p.agent.state != .exited && p.id != paneID && self.group(ofPane: p.id)?.id == group {
            list.append(groupAgent(p))
        }
        list.sort { ($0.detail, $0.id) < ($1.detail, $1.id) }
        return list
    }

    /// Runs a pin command on a pane, showing what went wrong as a toast.
    @discardableResult
    public func pinCommand(_ paneID: String, _ build: @escaping (inout Hammock_V1_PinCommand) -> Void) async -> Hammock_V1_PinReply? {
        guard let conn = connection else {
            showToast("Not connected")
            return nil
        }
        var m = Hammock_V1_PinCommand()
        m.paneID = paneID
        build(&m)
        let msg = m
        do {
            return try await conn.call { $0.pinCommand = msg }.pinReply
        } catch {
            showToast(error.localizedDescription)
            return nil
        }
    }

    /// What's under a point (fractions of the page), for pointing in pin
    /// mode: quietly nil when it can't be told.
    public func inspectPin(_ paneID: String, at p: CGPoint) async -> Hammock_V1_PinReply? {
        guard let conn = connection else { return nil }
        let msg = Hammock_V1_PinCommand.with {
            $0.paneID = paneID
            $0.action = .inspect
            $0.x = Double(p.x)
            $0.y = Double(p.y)
        }
        return try? await conn.call { $0.pinCommand = msg }.pinReply
    }

    /// Drops a pin at a point (fractions of the page or screen), its note
    /// opened to write.
    public func addPin(_ paneID: String, at p: CGPoint) {
        Task {
            if let r = await pinCommand(paneID, { $0.action = .add; $0.x = Double(p.x); $0.y = Double(p.y) }) {
                PinMode.shared.editing = r.pinID
            }
        }
    }

    public func notePin(_ paneID: String, _ pinID: String, _ note: String) {
        Task { await pinCommand(paneID) { $0.action = .note; $0.pinID = pinID; $0.note = note } }
    }

    public func deletePin(_ paneID: String, _ pinID: String) {
        if PinMode.shared.editing == pinID { PinMode.shared.editing = nil }
        Task { await pinCommand(paneID) { $0.action = .delete; $0.pinID = pinID } }
    }

    public func clearPins(_ paneID: String) {
        PinMode.shared.editing = nil
        Task { await pinCommand(paneID) { $0.action = .clear } }
    }

    public func setPinTarget(_ paneID: String, _ target: String) {
        Task { await pinCommand(paneID) { $0.action = .setTarget; $0.targetPaneID = target } }
    }

    /// Sends a pane's pins to its agent, leaving pin mode once they've gone.
    public func sendPins(_ paneID: String) {
        PinMode.shared.editing = nil
        let name = pinAgents(for: paneID).target?.name ?? "the agent"
        // Notes still being typed go first, so what's sent has them.
        let drafts = PinMode.shared.takeDrafts(paneID)
        Task {
            for (pinID, text) in drafts {
                await pinCommand(paneID) { $0.action = .note; $0.pinID = pinID; $0.note = text }
            }
            guard let r = await pinCommand(paneID, { $0.action = .send }) else { return }
            if r.queued {
                showToast("Held until \(name) has your answer: it’s waiting on you")
            } else {
                showToast("Notes sent to \(name)")
                PinMode.shared.set(paneID, false)
            }
        }
    }
}

// MARK: - Views

/// A pane's pins, drawn over its page or screen (area, in this view's
/// coordinates): each element outlined, a dot where the pin is and its
/// number in a badge. Tapping a badge opens its note (onSelect).
public struct PinMarks: View {
    let pins: [Pin]
    let area: CGRect
    var editing: String?
    var onSelect: (Pin) -> Void

    public init(pins: [Pin], area: CGRect, editing: String? = nil, onSelect: @escaping (Pin) -> Void) {
        self.pins = pins
        self.area = area
        self.editing = editing
        self.onSelect = onSelect
    }

    public var body: some View {
        ZStack(alignment: .topLeading) {
            ForEach(pins, id: \.id) { pin in
                if pin.hasRect {
                    let r = rect(pin.rect)
                    RoundedRectangle(cornerRadius: 2)
                        .fill(.tint.opacity(pin.id == editing ? 0.14 : 0.06))
                        .strokeBorder(.tint.opacity(pin.id == editing ? 0.95 : 0.55), lineWidth: 1.5)
                        .frame(width: r.width, height: r.height)
                        .position(x: r.midX, y: r.midY)
                        .allowsHitTesting(false)
                }
            }
            ForEach(pins, id: \.id) { pin in
                PinBadge(number: Int(pin.number), selected: pin.id == editing, noted: !pin.note.isEmpty)
                    .position(badgeCenter(pin))
                    .onTapGesture { onSelect(pin) }
                    .accessibilityLabel("Pin \(pin.number)\(pin.note.isEmpty ? "" : ": \(pin.note)")")
                    .accessibilityAddTraits(.isButton)
                Circle().fill(.tint).overlay(Circle().strokeBorder(.white, lineWidth: 1.5))
                    .frame(width: 8, height: 8)
                    .position(point(pin))
                    .allowsHitTesting(false)
            }
        }
        .frame(width: area.maxX, height: area.maxY, alignment: .topLeading)
    }

    public func point(_ pin: Pin) -> CGPoint {
        CGPoint(x: area.minX + CGFloat(pin.x) * area.width, y: area.minY + CGFloat(pin.y) * area.height)
    }

    /// Up and to the right of the point, so what's under it shows, and kept
    /// on the page.
    public func badgeCenter(_ pin: Pin) -> CGPoint {
        let p = point(pin), r = PinBadge.size / 2
        return CGPoint(x: min(max(p.x + r * 0.9, area.minX + r), area.maxX - r),
                       y: min(max(p.y - r * 0.9, area.minY + r), area.maxY - r))
    }

    func rect(_ r: Hammock_V1_PinRect) -> CGRect {
        CGRect(x: area.minX + CGFloat(r.x) * area.width, y: area.minY + CGFloat(r.y) * area.height,
               width: CGFloat(r.w) * area.width, height: CGFloat(r.h) * area.height)
    }
}

/// A pin's number, in a badge of the theme's colour.
public struct PinBadge: View {
    let number: Int
    var selected = false
    var noted = true
    public static let size: CGFloat = 24

    public init(number: Int, selected: Bool = false, noted: Bool = true) {
        self.number = number
        self.selected = selected
        self.noted = noted
    }

    public var body: some View {
        Text("\(number)")
            .font(.system(size: 12, weight: .bold, design: .rounded))
            .monospacedDigit()
            .foregroundStyle(.white)
            .frame(minWidth: Self.size, minHeight: Self.size)
            .background(Circle().fill(.tint))
            .overlay(Circle().strokeBorder(.white, lineWidth: selected ? 2.5 : 1.5))
            .overlay(alignment: .topTrailing) {
                // Not yet noted: a pin with nothing said about it.
                if !noted {
                    Circle().fill(.white).frame(width: 7, height: 7).offset(x: 1, y: -1)
                }
            }
            .shadow(color: .black.opacity(0.3), radius: 3, y: 1)
            .scaleEffect(selected ? 1.12 : 1)
            .animation(.snappy(duration: 0.15), value: selected)
            .contentShape(Circle())
    }
}

/// A pin's note, to write or change (committed as it closes), and Delete.
public struct PinNoteEditor: View {
    let model: AppModel
    let paneID: String
    let pin: Pin
    var onClose: () -> Void
    @State private var text = ""
    @State private var saved = "" // what the daemon has
    @FocusState private var focused: Bool

    public init(model: AppModel, paneID: String, pin: Pin, onClose: @escaping () -> Void) {
        self.model = model
        self.paneID = paneID
        self.pin = pin
        self.onClose = onClose
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                PinBadge(number: Int(pin.number))
                Text((pin.author.isEmpty ? "" : "\(pin.author) · ") + (pin.what.isEmpty ? "Looking…" : pin.what))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer(minLength: 0)
            }
            TextField("What should change here?", text: $text, axis: .vertical)
                .lineLimit(3...8)
                .textFieldStyle(.plain)
                .focused($focused)
                .padding(8)
                .background(RoundedRectangle(cornerRadius: 8).fill(Color.primary.opacity(0.06)))
                .onSubmit { close() }
            HStack {
                Button(role: .destructive) {
                    model.deletePin(paneID, pin.id)
                    onClose()
                } label: {
                    Label("Delete Pin", systemImage: "trash")
                }
                Spacer()
                Button("Done") { close() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
            }
        }
        .padding(12)
        #if os(iOS)
        .frame(maxWidth: .infinity) // the phone's sheet's width
        #else
        .frame(width: 300)
        #endif
        .onAppear {
            text = pin.note
            saved = pin.note
            focused = true
        }
        // Saved as it's typed (a moment after each change), so every device
        // sees it and Send has it.
        .task(id: text) {
            guard text != saved else { return }
            PinMode.shared.draft(pin.id, pane: paneID, text: text)
            try? await Task.sleep(for: .milliseconds(600))
            if !Task.isCancelled { commit() }
        }
        .onDisappear { commit() }
    }

    private func commit() {
        PinMode.shared.draft(pin.id, pane: paneID, text: nil)
        if text != saved, model.pins(paneID).contains(where: { $0.id == pin.id }) {
            saved = text
            model.notePin(paneID, pin.id, text)
        }
    }

    private func close() {
        commit()
        onClose()
    }
}

/// What pin mode shows along the pane: how many pins, Clear, Send (naming
/// the agent, with a menu to pick another), and Done.
public struct PinSendBar: View {
    let model: AppModel
    let paneID: String
    /// Leaves pin mode (the Mac's Esc, the phone's Done).
    var onDone: () -> Void

    public init(model: AppModel, paneID: String, onDone: @escaping () -> Void) {
        self.model = model
        self.paneID = paneID
        self.onDone = onDone
    }

    private var board: Hammock_V1_PinBoard? { model.panes[paneID]?.pins }

    public var body: some View {
        let pins = model.pins(paneID)
        let (target, agents) = model.pinAgents(for: paneID)
        HStack(spacing: 10) {
            Image(systemName: "mappin.and.ellipse")
                .foregroundStyle(.tint)
            Text(status(pins.count))
                .font(.callout.weight(.medium))
                .lineLimit(1)
            Spacer(minLength: 4)
            if !pins.isEmpty {
                Button("Clear") { model.clearPins(paneID) }
                    .buttonStyle(.borderless)
                Menu {
                    if agents.isEmpty {
                        Text("No agents in this Group")
                    }
                    ForEach(agents, id: \.id) { a in
                        Button {
                            model.setPinTarget(paneID, a.id)
                        } label: {
                            if a.id == target?.id { Label(agentTitle(a), systemImage: "checkmark") } else { Text(agentTitle(a)) }
                        }
                    }
                    if board?.targetChosen == true {
                        Divider()
                        Button("Let Hammock Choose") { model.setPinTarget(paneID, "") }
                    }
                } label: {
                    Label(target.map { "Send to \($0.name)" } ?? "Send", systemImage: "paperplane.fill")
                } primaryAction: {
                    model.sendPins(paneID)
                }
                .disabled(target == nil || board?.queued == true)
                .fixedSize()
                #if os(macOS)
                .menuStyle(.borderedButton)
                #endif
            }
            Button("Done", action: onDone)
                .buttonStyle(.borderless)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .background(.regularMaterial, in: Capsule())
        .overlay(Capsule().strokeBorder(.separator.opacity(0.6)))
        .shadow(color: .black.opacity(0.15), radius: 8, y: 2)
    }

    private func status(_ n: Int) -> String {
        if board?.queued == true { return "Held: the agent’s waiting on you" }
        switch n {
        case 0:
            #if os(macOS)
            return "Click to pin a note"
            #else
            return "Touch and hold to pin a note"
            #endif
        case 1: return "1 note"
        default: return "\(n) notes"
        }
    }

    private func agentTitle(_ a: DiffAgent) -> String { a.detail.isEmpty ? a.name : "\(a.name) · \(a.detail)" }
}
