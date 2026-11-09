import AppKit
import HammockKit
import Observation
import SwiftUI

/// Opening a log: what the New Log sheet is for (a tab in a Group, or a
/// split beside a pane), and what it starts with.
struct LogRequest: Identifiable {
    let id = UUID()
    var groupID: String?
    var splitPane: String?
    var direction: SplitDirection = .horizontal
    var source = LogSource()
}

@MainActor
@Observable
final class LogUI {
    static let shared = LogUI()
    var request: LogRequest?
    /// Panes showing their raw terminal instead of the log.
    private(set) var raw: Set<String> = []

    /// New Log Tab… in the Group you're in.
    func newTab(_ model: AppModel) {
        request = LogRequest(groupID: model.currentGroup?.id ?? model.groups.first?.id)
    }

    /// The log sheet, for a split beside pane.
    func split(_ model: AppModel, _ paneID: String, _ direction: SplitDirection) {
        var r = LogRequest(splitPane: paneID, direction: direction)
        if let p = model.panes[paneID], p.kind == .simulator {
            r.source.kind = "simulator"
            r.source.device = p.deviceUdid
        }
        request = r
    }

    /// A simulator pane's log, beside it, straight away.
    func showSimulatorLog(_ model: AppModel, _ paneID: String) {
        guard let p = model.panes[paneID], p.kind == .simulator else { return }
        let src = LogSource.with { $0.kind = "simulator"; $0.device = p.deviceUdid; $0.label = "\(p.title) log" }
        Task { await model.openLog(src, in: nil, splitting: paneID, direction: .vertical) }
    }

    func showsRaw(_ paneID: String) -> Bool { raw.contains(paneID) }
    func setRaw(_ paneID: String, _ on: Bool) {
        if on { raw.insert(paneID) } else { raw.remove(paneID) }
    }
}

/// The header control that flips a log pane between its log and terminal.
struct LogViewToggle: View {
    let paneID: String
    var ui = LogUI.shared

    var body: some View {
        Picker("View", selection: Binding(get: { ui.showsRaw(paneID) }, set: { ui.setRaw(paneID, $0) })) {
            Image(systemName: "apple.terminal").tag(true)
            Image(systemName: "list.bullet.rectangle").tag(false)
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .controlSize(.small)
        .fixedSize()
        .tooltip(Keys.hint("Log or raw terminal", "toggle_agent_view"))
    }
}

/// New Log Tab…: a file, this Mac's log, a simulator's, Android's, or a
/// command's output.
struct NewLogSheet: View {
    let model: AppModel
    let request: LogRequest
    @Environment(\.dismiss) private var dismiss

    @State private var kind = "file"
    @State private var path = ""
    @State private var process = ""
    @State private var predicate = ""
    @State private var level = "default"
    @State private var device = ""
    @State private var serial = ""
    @State private var command = ""
    @State private var opening = false
    private var sims: SimulatorList { .shared }

    private static let kinds: [(String, String)] = [
        ("file", "File"), ("macos", "This Mac"), ("simulator", "Simulator"), ("logcat", "Android"), ("command", "Command"),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(request.splitPane == nil ? "New Log Tab" : "Open a Log Beside")
                .font(.title3.weight(.semibold))
            Picker("Source", selection: $kind) {
                ForEach(Self.kinds, id: \.0) { Text($0.1).tag($0.0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()

            Form { fields }
                .formStyle(.grouped)
                .scrollDisabled(true)
                .frame(minHeight: 150)

            Text(explanation).font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }.keyboardShortcut(.cancelAction)
                Button("Open") { open() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(!valid || opening)
            }
        }
        .padding(20)
        .frame(width: 520)
        .onAppear {
            let s = request.source
            if !s.kind.isEmpty { kind = s.kind }
            device = s.device
            path = s.path
            process = s.process
            sims.refresh(model)
        }
    }

    @ViewBuilder private var fields: some View {
        switch kind {
        case "file":
            HStack {
                TextField("File", text: $path, prompt: Text("/path/to/app.log"))
                Button("Choose…") { choose() }
            }
        case "macos", "simulator":
            if kind == "simulator" {
                Picker("Simulator", selection: $device) {
                    Text("The one running").tag("")
                    ForEach(sims.sorted) { d in Text(SimulatorList.label(d)).tag(d.udid) }
                }
            }
            TextField("Process", text: $process, prompt: Text(kind == "macos" ? "Every process (a firehose)" : "Every process"))
            TextField("Predicate", text: $predicate, prompt: Text("subsystem == \"com.example.app\""))
            Picker("Level", selection: $level) {
                Text("Default").tag("default")
                Text("Info").tag("info")
                Text("Debug").tag("debug")
            }
        case "logcat":
            TextField("Device serial", text: $serial, prompt: Text("The only device connected"))
        default:
            TextField("Command", text: $command, prompt: Text("pnpm dev"))
        }
    }

    private var explanation: String {
        switch kind {
        case "file": "Follows the file as it grows (and through rotation), starting with its last 5,000 lines."
        case "macos": "Streams the unified log (log stream). Name a process or a predicate, or it's everything this Mac logs."
        case "simulator": "Streams the simulator's unified log. It must be running."
        case "logcat": "Streams adb logcat from a connected Android device or emulator."
        default: "Runs the command in the Group's folder; what it prints is the log."
        }
    }

    private var valid: Bool {
        switch kind {
        case "file": !path.trimmingCharacters(in: .whitespaces).isEmpty
        case "command": !command.trimmingCharacters(in: .whitespaces).isEmpty
        default: true
        }
    }

    private func choose() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        if panel.runModal() == .OK, let url = panel.url { path = url.path }
    }

    private func open() {
        let src = LogSource.with {
            $0.kind = kind
            switch kind {
            case "file": $0.path = (path.trimmingCharacters(in: .whitespaces) as NSString).expandingTildeInPath
            case "macos", "simulator":
                $0.process = process.trimmingCharacters(in: .whitespaces)
                $0.predicate = predicate.trimmingCharacters(in: .whitespaces)
                $0.level = level
                $0.device = kind == "simulator" ? device : ""
            case "logcat": $0.device = serial.trimmingCharacters(in: .whitespaces)
            default: $0.command = [command]
            }
        }
        opening = true
        let r = request
        Task {
            let id = await model.openLog(src, in: r.groupID, splitting: r.splitPane, direction: r.direction)
            opening = false
            if id != nil { dismiss() }
        }
    }
}
