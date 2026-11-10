import AppKit
import HammockKit
import SwiftUI

// MARK: - Running the bundled hammock

enum HammockCLI {
    struct Result: Sendable {
        let ok: Bool
        let stdout: Data
        let output: String // stdout + stderr, for showing
    }

    /// Runs the app's own `hmk` with args, off the main thread.
    static func run(_ args: [String]) async -> Result {
        guard let hammock = CommandLineTool.bundled, FileManager.default.isExecutableFile(atPath: hammock.path) else {
            return Result(ok: false, stdout: Data(), output: "hmk was not found in the app bundle.")
        }
        return await Task.detached {
            let p = Process()
            p.executableURL = hammock
            p.arguments = args
            let out = Pipe(), err = Pipe()
            p.standardOutput = out
            p.standardError = err
            do {
                try p.run()
                // Read both before waiting so a full pipe can't stall the child.
                async let o = out.fileHandleForReading.readDataToEndOfFile()
                async let e = err.fileHandleForReading.readDataToEndOfFile()
                let (od, ed) = await (o, e)
                p.waitUntilExit()
                let text = String(decoding: od, as: UTF8.self) + String(decoding: ed, as: UTF8.self)
                return Result(ok: p.terminationStatus == 0, stdout: od, output: text.trimmingCharacters(in: .whitespacesAndNewlines))
            } catch {
                return Result(ok: false, stdout: Data(), output: error.localizedDescription)
            }
        }.value
    }
}

private let home = NSHomeDirectory()
private func tilde(_ path: String) -> String { (path as NSString).abbreviatingWithTildeInPath }

// MARK: - 3 · Keep it running

struct DaemonStep: View {
    @State private var wantDaemon = true
    @State private var wantCLI = true
    @State private var daemonOn = DaemonService.isEnabled
    @State private var needsApproval = DaemonService.needsApproval
    @State private var cliPath = CommandLineTool.installedPath
    @State private var pathHint: PathHint?
    @State private var error: String?
    /// Continue was pressed once; a second press moves on even if something's
    /// still pending (approval, the PATH line).
    @State private var applied = false

    var body: some View {
        VStack(spacing: 0) {
            VStack(spacing: 0) {
                ZStack {
                    RoundedRectangle(cornerRadius: 18, style: .continuous)
                        .fill(LinearGradient(colors: [Brand.fern, Brand.pine], startPoint: .top, endPoint: .bottom))
                    Image(systemName: "power").font(.system(size: 28, weight: .semibold)).foregroundStyle(Brand.mist)
                }
                .frame(width: 64, height: 64)
                .shadow(color: Brand.forest.opacity(0.25), radius: 12, y: 10)
                .accessibilityHidden(true)
                StepHeading(title: "Keep Hammock running",
                            subtitle: "Two small things that make Hammock part of your Mac. Both are on by default, and you can turn them off in Settings ▸ General.",
                            centered: true)
                    .frame(maxWidth: 540)
                    .padding(.top, 18)

                VStack(spacing: 14) {
                    row(icon: AnyView(Image(systemName: "macwindow.on.rectangle").font(.system(size: 18)).foregroundStyle(Brand.forest)),
                        title: Text("Run in the background at login"),
                        text: Text("Terminals and agents keep going when the window is closed, and come back after a restart. Runs as you, no admin password."),
                        detail: nil, isOn: $wantDaemon) {
                        if wantDaemon && needsApproval {
                            banner(tint: Brand.needsYou) {
                                Image(systemName: "exclamationmark.circle.fill").foregroundStyle(.white, Brand.needsYou)
                                Text("macOS wants you to approve this. Turn on **\(Bundle.main.object(forInfoDictionaryKey: "CFBundleName") as? String ?? "Hammock")** in Login Items & Extensions, then come back. This updates by itself.")
                                    .fixedSize(horizontal: false, vertical: true)
                                Spacer(minLength: 8)
                                Button("Open Login Items") { DaemonService.openLoginItems() }
                            }
                        } else if wantDaemon && daemonOn {
                            banner(tint: Brand.done) {
                                DoneCheck()
                                Text("Running, and starts when you log in").foregroundStyle(Brand.done)
                                Spacer()
                            }
                        }
                    }
                    row(icon: AnyView(Text(">_").font(.system(size: 13, weight: .bold, design: .monospaced)).foregroundStyle(Brand.mist)),
                        iconBackground: Color(red: 0.12, green: 0.114, blue: 0.106),
                        title: Text("Install the \(Text("hmk").font(.system(size: 14, weight: .semibold, design: .monospaced))) command"),
                        text: Text("Drive Hammock from any shell or script: \(Self.mono("hmk ls")), \(Self.mono("hmk tab new")), \(Self.mono("hmk send"))…"),
                        detail: cliDetail, isOn: $wantCLI) {
                        if wantCLI, let hint = pathHint {
                            VStack(alignment: .leading, spacing: 8) {
                                Text("Installed to **\(tilde(hint.dir))**, which isn't on your PATH yet. Add this line to **\(hint.file)**:")
                                CodeLine(text: hint.line)
                            }
                            .font(.system(size: 13))
                            .padding(.horizontal, 20).padding(.vertical, 12)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(Color.primary.opacity(0.04))
                        }
                    }
                    if let error {
                        Text(error).font(.callout).foregroundStyle(.red)
                    }
                }
                .frame(width: 640)
                .padding(.top, 30)
            }
            .padding(.top, 20)
            .frame(maxHeight: .infinity, alignment: .top)

            StepFooter {
                Button(applied && (needsApproval || pathHint != nil) ? "Continue Anyway" : "Continue") { apply() }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .keyboardShortcut(.defaultAction)
            }
        }
        .task {
            // Follow approval in System Settings while this step is open.
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                let on = DaemonService.isEnabled, waiting = DaemonService.needsApproval
                if on != daemonOn || waiting != needsApproval {
                    withAnimation(.smooth) { daemonOn = on; needsApproval = waiting }
                }
            }
        }
    }

    private static func mono(_ s: String) -> Text {
        Text(s).font(.system(size: 12, design: .monospaced)).foregroundColor(.primary)
    }

    private var cliDetail: String {
        if !wantCLI { return cliPath == nil ? "Not installed" : "Will be removed" }
        if let cliPath { return "✓ Linked at \(tilde(cliPath))" }
        let dir = CommandLineTool.candidates.first { FileManager.default.isWritableFile(atPath: $0) } ?? CommandLineTool.candidates.last!
        return "Will link \(tilde((dir as NSString).appendingPathComponent(CommandLineTool.linkName))) to this app"
    }

    private func apply() {
        error = nil
        if applied && (needsApproval || pathHint != nil) {
            Onboarding.shared.next()
            return
        }
        do {
            if wantDaemon != DaemonService.isEnabled && !(wantDaemon && DaemonService.needsApproval) {
                try DaemonService.setEnabled(wantDaemon)
            }
        } catch {
            self.error = error.localizedDescription
        }
        daemonOn = DaemonService.isEnabled
        needsApproval = DaemonService.needsApproval
        do {
            if wantCLI, cliPath == nil {
                let link = try CommandLineTool.install()
                cliPath = link
                pathHint = PathHint.check(link)
            } else if !wantCLI, cliPath != nil {
                CommandLineTool.uninstall()
                cliPath = nil
            }
        } catch {
            self.error = "Couldn't install hammock: \(error.localizedDescription)"
        }
        Onboarding.shared.daemonOn = daemonOn
        Onboarding.shared.cliPath = cliPath
        applied = true
        if error == nil && !(wantDaemon && needsApproval) && pathHint == nil {
            Onboarding.shared.next()
        }
    }

    private func row<Extra: View>(icon: AnyView, iconBackground: Color = Brand.mist.opacity(0.35), title: Text, text: Text, detail: String?,
                                  isOn: Binding<Bool>, @ViewBuilder extra: () -> Extra) -> some View {
        VStack(spacing: 0) {
            HStack(alignment: .top, spacing: 14) {
                icon
                    .frame(width: 36, height: 36)
                    .background(iconBackground, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 3) {
                    title.font(.system(size: 15, weight: .semibold))
                    text.font(.system(size: 13)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                    if let detail {
                        Text(detail).font(.system(size: 12)).foregroundStyle(.secondary).padding(.top, 5)
                    }
                }
                Spacer(minLength: 12)
                Toggle(isOn: isOn) { title }
                    .toggleStyle(.switch)
                    .labelsHidden()
            }
            .padding(.horizontal, 20).padding(.vertical, 18)
            extra()
        }
        .background(.background.opacity(0.85))
        .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(Color.primary.opacity(0.08)))
        .shadow(color: Brand.pine.opacity(0.05), radius: 10, y: 6)
        .animation(.smooth, value: needsApproval)
        .animation(.smooth, value: daemonOn)
    }

    private func banner<C: View>(tint: Color, @ViewBuilder _ content: () -> C) -> some View {
        HStack(spacing: 12) { content() }
            .font(.system(size: 13))
            .padding(.horizontal, 20).padding(.vertical, 11)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(tint.opacity(0.1))
            .overlay(alignment: .top) { Rectangle().fill(tint.opacity(0.25)).frame(height: 1) }
    }
}

/// When the CLI landed in a folder the user's shell doesn't put on PATH.
struct PathHint: Equatable {
    let dir: String
    let file: String
    let line: String

    static func check(_ link: String) -> PathHint? {
        let dir = (link as NSString).deletingLastPathComponent
        if ["/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"].contains(dir) { return nil }
        if (ProcessInfo.processInfo.environment["PATH"] ?? "").split(separator: ":").contains(where: { String($0) == dir }) { return nil }
        let short = dir.replacingOccurrences(of: home, with: "")  // "/.local/bin"
        for rc in [".zshrc", ".zprofile", ".zshenv", ".bash_profile", ".bashrc", ".profile", ".config/fish/config.fish"] {
            if let text = try? String(contentsOfFile: (home as NSString).appendingPathComponent(rc), encoding: .utf8), text.contains(short) { return nil }
        }
        let shell = (ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh") as NSString
        switch shell.lastPathComponent {
        case "fish": return PathHint(dir: dir, file: "~/.config/fish/config.fish", line: "fish_add_path $HOME\(short)")
        case "bash": return PathHint(dir: dir, file: "~/.bash_profile", line: "export PATH=\"$HOME\(short):$PATH\"")
        default: return PathHint(dir: dir, file: "~/.zshrc", line: "export PATH=\"$HOME\(short):$PATH\"")
        }
    }
}

/// A dark one-line code box with a Copy button.
struct CodeLine: View {
    let text: String
    @State private var copied = false

    var body: some View {
        HStack(spacing: 10) {
            Text(text)
                .font(.system(size: 12, design: .monospaced))
                .foregroundStyle(Color(red: 0.91, green: 0.886, blue: 0.84))
                .textSelection(.enabled)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer(minLength: 8)
            Button(copied ? "Copied" : "Copy") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(text, forType: .string)
                copied = true
            }
            .buttonStyle(.plain)
            .font(.system(size: 12))
            .foregroundStyle(.white)
            .padding(.horizontal, 12).frame(height: 26)
            .background(Color.white.opacity(0.14), in: RoundedRectangle(cornerRadius: 6))
        }
        .padding(.leading, 12).padding(.trailing, 8).padding(.vertical, 8)
        .background(Color(red: 0.12, green: 0.114, blue: 0.106), in: RoundedRectangle(cornerRadius: 8))
    }
}

// MARK: - 4 · Agents

/// `hmk hooks install --dry-run --json`.
struct HooksPlan: Decodable {
    struct Harness: Decodable, Identifiable {
        let id: String
        let name: String
        let binary: String
        let version: String
        let changes: [String]
        let notes: [String]
        let files: [File]
        let upToDate: Bool
        var found: Bool { !binary.isEmpty }
    }
    struct File: Decodable, Identifiable {
        let path: String
        let added: Int
        let removed: Int
        let lines: [Line]
        var id: String { path }
    }
    struct Line: Decodable {
        let op: String
        let text: String
    }
    let harnesses: [Harness]
}

struct AgentsStep: View {
    @State private var plan: HooksPlan?
    @State private var loadError: String?
    @State private var selected: Set<String> = []
    @State private var showDiff = false
    @State private var fileIndex = 0
    @State private var installing = false
    @State private var installError: String?

    private var found: [HooksPlan.Harness] { plan?.harnesses.filter(\.found) ?? [] }
    private var toInstall: [HooksPlan.Harness] { found.filter { selected.contains($0.id) && !$0.upToDate } }
    private var files: [HooksPlan.File] { toInstall.flatMap(\.files) }

    var body: some View {
        VStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 0) {
                if let plan, found.isEmpty {
                    NoAgents(plan: plan) { await load() }
                } else {
                    StepHeading(title: "Connect your agents",
                                subtitle: "Hammock already spots Claude Code, Codex and other agents by watching their screens. Connecting them makes it exact, and gives them Hammock's tools.")
                    benefits.padding(.top, 14)
                    if plan == nil {
                        HStack(spacing: 10) {
                            if let loadError {
                                Text(loadError).foregroundStyle(.red).textSelection(.enabled)
                                Button("Try Again") { Task { await load() } }
                            } else {
                                ProgressView().controlSize(.small)
                                Text("Looking for your agents…").foregroundStyle(.secondary)
                            }
                        }
                        .padding(.top, 40)
                    } else {
                        cards.padding(.top, 18)
                        diffSection.padding(.top, 12)
                    }
                }
            }
            .padding(.horizontal, 88)
            .padding(.top, 4)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)

            StepFooter {
                if installing { ProgressView().controlSize(.small) }
                Button(primaryTitle) { Task { await install() } }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .keyboardShortcut(.defaultAction)
                    .disabled(installing || (plan == nil && loadError == nil))
            }
        }
        .task { if plan == nil { await load() } }
    }

    private var primaryTitle: String {
        switch toInstall.count {
        // Skip when there was something to connect and it's all unchecked.
        case 0: found.contains { !$0.upToDate } ? "Skip" : "Continue"
        case 1: "Connect"
        default: "Connect Both"
        }
    }

    private var benefits: some View {
        HStack(spacing: 10) {
            chip { HStack(spacing: 3) { StatusIcon(state: .working, size: 12); StatusIcon(state: .needsInput, size: 12); StatusIcon(state: .idle, size: 12) } } text: { "Exact working, waiting and done states" }
            chip { Image(systemName: "arrow.clockwise").foregroundStyle(Brand.fern) } text: { "Sessions resume after a restart" }
            chip { Image(systemName: "rectangle.split.2x1").foregroundStyle(Brand.fern) } text: { "Agents can open panes and drive browsers, inside their own Group" }
        }
    }

    private func chip<I: View>(@ViewBuilder icon: () -> I, text: () -> String) -> some View {
        HStack(spacing: 8) {
            icon().accessibilityHidden(true)
            Text(text())
        }
        .font(.system(size: 12))
        .padding(.leading, 8).padding(.trailing, 12).padding(.vertical, 6)
        .background(.background.opacity(0.85), in: Capsule())
        .overlay(Capsule().strokeBorder(Color.primary.opacity(0.08)))
    }

    private var cards: some View {
        HStack(alignment: .top, spacing: 16) {
            ForEach(found) { h in
                let on = selected.contains(h.id)
                OnboardingCard(padding: 18, highlighted: on && !h.upToDate) {
                    VStack(alignment: .leading, spacing: 12) {
                        HStack(spacing: 12) {
                            AgentLogo(id: h.id)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(h.name).font(.system(size: 15, weight: .semibold))
                                HStack(spacing: 5) {
                                    DoneCheck(size: 11)
                                    Text("Found\(h.version.isEmpty ? "" : " " + h.version) · \(tilde(h.binary))")
                                        .lineLimit(1).truncationMode(.middle)
                                }
                                .font(.system(size: 12))
                                .foregroundStyle(Brand.done)
                            }
                            Spacer(minLength: 8)
                            if h.upToDate {
                                // A note means part of it needs doing by hand.
                                Text(h.notes.isEmpty ? "Connected" : "Needs a step")
                                    .font(.system(size: 12, weight: .medium))
                                    .foregroundStyle(h.notes.isEmpty ? Brand.done : Brand.needsYou)
                            } else {
                                Toggle("Connect", isOn: Binding(get: { on }, set: { v in
                                    if v { selected.insert(h.id) } else { selected.remove(h.id) }
                                    fileIndex = 0
                                }))
                                .toggleStyle(.checkbox)
                            }
                        }
                        Divider()
                        VStack(alignment: .leading, spacing: 6) {
                            if h.upToDate {
                                bullet(h.notes.isEmpty ? "Already set up. Nothing to change." : "Hammock's tools are set up; one setting needs adding by hand:")
                            }
                            ForEach(h.changes, id: \.self) { bullet($0) }
                            ForEach(h.notes, id: \.self) { n in
                                Label { Text(n).fixedSize(horizontal: false, vertical: true) } icon: {
                                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(Brand.needsYou)
                                }
                                .font(.system(size: 12))
                                .textSelection(.enabled)
                            }
                        }
                    }
                }
            }
        }
        .fixedSize(horizontal: false, vertical: true)
    }

    private func bullet(_ s: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text("•").foregroundStyle(.tertiary)
            Text(s).fixedSize(horizontal: false, vertical: true)
        }
        .font(.system(size: 12))
    }

    @ViewBuilder private var diffSection: some View {
        if let installError {
            ScrollView {
                Text(installError).font(.system(size: 11, design: .monospaced)).foregroundStyle(.red).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: 120)
        }
        if !files.isEmpty {
            HStack(spacing: 14) {
                Button {
                    withAnimation(.smooth) { showDiff.toggle() }
                } label: {
                    Label("Show changes", systemImage: "chevron.right")
                        .labelStyle(DisclosureLabelStyle(open: showDiff))
                }
                .buttonStyle(.plain)
                .foregroundStyle(Color.themeAccent)
                Text("A backup of each file is saved next to it. Your own hooks and servers are left alone.")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }
            if showDiff {
                let file = files[min(fileIndex, files.count - 1)]
                VStack(spacing: 0) {
                    HStack(spacing: 2) {
                        ForEach(Array(files.enumerated()), id: \.offset) { i, f in
                            Button {
                                fileIndex = i
                            } label: {
                                HStack(spacing: 6) {
                                    Text(f.path)
                                    Text("+\(f.added)").foregroundStyle(Brand.done)
                                    if f.removed > 0 { Text("−\(f.removed)").foregroundStyle(.red) }
                                }
                                .font(.system(size: 11.5, design: .monospaced))
                                .padding(.horizontal, 12).padding(.vertical, 6)
                                .background(i == fileIndex ? AnyShapeStyle(.background) : AnyShapeStyle(.clear),
                                            in: UnevenRoundedRectangle(topLeadingRadius: 7, topTrailingRadius: 7))
                            }
                            .buttonStyle(.plain)
                        }
                        Spacer()
                    }
                    .padding([.horizontal, .top], 6)
                    .background(Color.primary.opacity(0.05))
                    ScrollView([.vertical, .horizontal]) {
                        VStack(alignment: .leading, spacing: 0) {
                            ForEach(Array(file.lines.enumerated()), id: \.offset) { _, l in
                                DiffRow(line: l)
                            }
                        }
                        .padding(.vertical, 8)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .background(.background)
                }
                .frame(maxHeight: .infinity)
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Color.primary.opacity(0.1)))
                .padding(.top, 2)
                .padding(.bottom, 4)
                .transition(.opacity)
            }
        }
    }

    private func load() async {
        loadError = nil
        let r = await HammockCLI.run(["hooks", "install", "--dry-run", "--json"])
        guard r.ok, let p = try? JSONDecoder().decode(HooksPlan.self, from: r.stdout) else {
            loadError = r.output.isEmpty ? "Couldn't check your agents." : r.output
            return
        }
        withAnimation(.smooth) {
            plan = p
            selected = Set(p.harnesses.filter { $0.found && !$0.upToDate }.map(\.id))
        }
        Onboarding.shared.claudeFound = p.harnesses.contains { $0.id == "claude" && $0.found }
    }

    private func install() async {
        installError = nil
        let ids = toInstall.map(\.id)
        if !ids.isEmpty {
            installing = true
            let r = await HammockCLI.run(["hooks", "install", "--harness", ids.joined(separator: ",")])
            installing = false
            guard r.ok else {
                installError = r.output
                return
            }
        }
        Onboarding.shared.connected = found.filter { $0.upToDate || ids.contains($0.id) }.map(\.name)
        Onboarding.shared.next()
    }
}

private struct DisclosureLabelStyle: LabelStyle {
    let open: Bool
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 6) {
            configuration.icon
                .font(.system(size: 9, weight: .bold))
                .rotationEffect(.degrees(open ? 90 : 0))
            configuration.title.font(.system(size: 13, weight: .medium))
        }
    }
}

private struct DiffRow: View {
    let line: HooksPlan.Line

    var body: some View {
        if line.op == "…" {
            Text("⋯")
                .font(.system(size: 11.5, design: .monospaced))
                .foregroundStyle(.tertiary)
                .padding(.horizontal, 14)
                .frame(maxWidth: .infinity, alignment: .leading)
        } else {
            HStack(spacing: 0) {
                Text(line.op == " " ? "" : line.op == "-" ? "−" : "+")
                    .frame(width: 18, alignment: .leading)
                    .foregroundStyle(line.op == "+" ? Brand.done : .red)
                Text(line.text)
                    .fixedSize()
            }
            .font(.system(size: 11.5, design: .monospaced))
            .lineSpacing(4)
            .padding(.horizontal, 14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(line.op == "+" ? Brand.done.opacity(0.12) : line.op == "-" ? Color.red.opacity(0.1) : .clear)
        }
    }
}

private struct AgentLogo: View {
    let id: String
    var dimmed = false

    var body: some View {
        if id == "claude" || id == "codex" || dimmed { tile } else {
            // Agents beyond the first two: their own mark on a quiet tile.
            AgentMark(harness: id, size: 22)
                .frame(width: 38, height: 38)
                .background(Color.primary.opacity(0.07), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                .accessibilityHidden(true)
        }
    }

    private var tile: some View {
        let claude = id == "claude"
        return Text(claude ? "C" : "◎")
            .font(.system(size: 17, weight: .bold))
            .foregroundStyle(dimmed ? Color.secondary : claude ? Color(red: 0.65, green: 0.3, blue: 0.14) : Color(nsColor: .textBackgroundColor))
            .frame(width: 38, height: 38)
            .background(dimmed ? Color.primary.opacity(0.07) : claude ? Color(red: 0.965, green: 0.89, blue: 0.84) : Color.primary,
                        in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            .accessibilityHidden(true)
    }
}

private struct NoAgents: View {
    let plan: HooksPlan
    let recheck: () async -> Void
    @State private var checking = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            StepHeading(title: "Connect your agents",
                        subtitle: "Hammock works best with a coding agent, but we couldn't find one on this Mac (Claude Code, Codex, Gemini CLI, Cursor, Copilot and others). Install one, then check again. Or carry on: shells, browsers and simulators work without them.")
                .frame(maxWidth: 720, alignment: .leading)
            HStack(alignment: .top, spacing: 16) {
                card(id: "claude", name: "Claude Code", how: "Install it from any terminal:", command: "curl -fsSL https://claude.ai/install.sh | bash")
                card(id: "codex", name: "Codex", how: "Install it with Homebrew:", command: "brew install codex")
            }
            .fixedSize(horizontal: false, vertical: true)
            .padding(.top, 26)
            HStack(spacing: 14) {
                Button {
                    checking = true
                    Task { await recheck(); checking = false }
                } label: {
                    Label("Check Again", systemImage: "arrow.clockwise")
                }
                .disabled(checking)
                if checking { ProgressView().controlSize(.small) }
                Text("Looks on your PATH and the usual install folders. You can also connect agents later in Settings ▸ Agents.")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }
            .padding(.top, 18)
            Spacer(minLength: 16)
            HStack(spacing: 12) {
                Image(systemName: "info.circle.fill")
                    .font(.system(size: 16))
                    .foregroundStyle(Brand.mist, Brand.forest)
                    .accessibilityHidden(true)
                Text("Using a different agent? Hammock still spots most agents by their screens. Hooks for others are coming.")
                    .font(.system(size: 13))
            }
            .padding(.horizontal, 16).padding(.vertical, 14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Brand.mist.opacity(0.3), in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            .padding(.bottom, 8)
        }
        .frame(maxHeight: .infinity, alignment: .top)
    }

    private func card(id: String, name: String, how: String, command: String) -> some View {
        OnboardingCard(padding: 18) {
            VStack(alignment: .leading, spacing: 14) {
                HStack(spacing: 12) {
                    AgentLogo(id: id, dimmed: true)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(name).font(.system(size: 15, weight: .semibold))
                        Text("Not found").font(.system(size: 12)).foregroundStyle(.secondary)
                    }
                }
                VStack(alignment: .leading, spacing: 6) {
                    Text(how).font(.system(size: 12)).foregroundStyle(.secondary)
                    CodeLine(text: command)
                }
            }
        }
    }
}

// MARK: - 5 · Finish

struct FinishStep: View {
    let model: AppModel
    @State private var flow = Onboarding.shared
    @State private var shown = false
    @State private var claude = false

    var body: some View {
        ZStack {
            VStack(spacing: 0) {
                VStack(spacing: 0) {
                    Image(nsImage: NSApp.applicationIconImage)
                        .resizable()
                        .frame(width: 112, height: 112)
                        .shadow(color: Brand.forest.opacity(0.3), radius: 18, y: 14)
                        .scaleEffect(shown ? 1 : 0.85)
                    Text("Kick back, it's Hammock time.")
                        .font(.system(size: 40, weight: .bold))
                        .kerning(-0.6)
                    Text(done.isEmpty ? "What would you like to do first?" : "You're all set. What would you like to do first?")
                        .font(.system(size: 16))
                        .foregroundStyle(.secondary)
                        .padding(.top, 8)
                    if !done.isEmpty {
                        HStack(spacing: 8) {
                            ForEach(done, id: \.self) { d in
                                HStack(spacing: 6) {
                                    DoneCheck()
                                    Text(d)
                                }
                                .font(.system(size: 12))
                                .padding(.leading, 6).padding(.trailing, 10).padding(.vertical, 4)
                                .background(.background.opacity(0.85), in: Capsule())
                                .overlay(Capsule().strokeBorder(Color.primary.opacity(0.06)))
                            }
                        }
                        .padding(.top, 14)
                    }
                }

                HStack(alignment: .top, spacing: 16) {
                    Button(action: startProject) {
                        VStack(alignment: .leading, spacing: 10) {
                            Image(systemName: claude ? "sparkles" : "folder")
                                .font(.system(size: 20, weight: .medium))
                                .foregroundStyle(Brand.mist)
                                .frame(width: 40, height: 40)
                                .background(Brand.mist.opacity(0.18), in: RoundedRectangle(cornerRadius: 11, style: .continuous))
                            Text(claude ? "Start Claude Code in a project" : "Open a project")
                                .font(.system(size: 17, weight: .bold))
                            Text(claude ? "Pick a folder. Hammock makes a Group named after it and starts claude there."
                                        : "Pick a folder. Hammock makes a Group named after it with a shell there.")
                                .font(.system(size: 13))
                                .foregroundStyle(Color(red: 1, green: 0.94, blue: 0.86).opacity(0.85))
                                .fixedSize(horizontal: false, vertical: true)
                            Spacer(minLength: 0)
                            HStack(spacing: 8) {
                                Text("Choose Folder…")
                                    .font(.system(size: 13, weight: .semibold))
                                    .foregroundStyle(Brand.pine)
                                    .padding(.horizontal, 14).frame(height: 30)
                                    .background(Brand.mist, in: RoundedRectangle(cornerRadius: 8))
                                if let start = Self.startDir { Text("starts in \(tilde(start))").font(.system(size: 12)).foregroundStyle(.white.opacity(0.7)) }
                            }
                        }
                        .foregroundStyle(.white)
                        .padding(20)
                        .frame(maxWidth: .infinity, minHeight: 168, maxHeight: .infinity, alignment: .topLeading)
                        .background(LinearGradient(colors: [Brand.fern, Brand.forest], startPoint: .topLeading, endPoint: .bottomTrailing),
                                    in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                        .shadow(color: Brand.pine.opacity(0.32), radius: 18, y: 14)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(LaunchButtonStyle())
                    // 1.35 : 1 : 1, as in the design.
                    .frame(width: (960 - 32) * 1.35 / 3.35)

                    launchCard(icon: AnyView(Text(">_").font(.system(size: 14, weight: .bold, design: .monospaced)).foregroundStyle(Brand.mist)),
                               iconBackground: Color(red: 0.12, green: 0.114, blue: 0.106),
                               title: "Open a shell", text: "A terminal in your Home Group, ready to go.", action: openShell)
                    launchCard(icon: AnyView(Image(systemName: "safari").font(.system(size: 20)).foregroundStyle(Brand.forest)),
                               iconBackground: Brand.mist.opacity(0.35),
                               title: "Look around", text: "Go straight into Hammock and explore at your own pace.") { flow.finish() }
                }
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: 960)
                .padding(.top, 30)
                .padding(.horizontal, 72)

                HStack(spacing: 18) {
                    Text("Handy keys").font(.system(size: 12, weight: .semibold)).foregroundStyle(.secondary)
                    ForEach([("new_group", "New Group"), ("new_tab", "New tab"), ("split_right", "Split"), ("command_palette", "Command palette"), ("new_browser_tab", "Browser tab")].compactMap { id, label in Keys.label(id).map { ($0, label) } }, id: \.0) { k, label in
                        HStack(spacing: 6) {
                            Text(k)
                                .font(.system(size: 11, weight: .semibold))
                                .padding(.horizontal, 6).padding(.vertical, 2)
                                .background(.background, in: RoundedRectangle(cornerRadius: 5))
                                .overlay(RoundedRectangle(cornerRadius: 5).strokeBorder(Color.primary.opacity(0.15)))
                            Text(label)
                        }
                        .font(.system(size: 12))
                    }
                }
                .padding(.horizontal, 18).padding(.vertical, 10)
                .background(.background.opacity(0.6), in: RoundedRectangle(cornerRadius: 12))
                .padding(.top, 20)

                Spacer(minLength: 0)
                HStack {
                    Button("Back") { flow.back() }.buttonStyle(.plain).foregroundStyle(.secondary)
                    Spacer()
                }
                .padding(.horizontal, 28)
                .frame(height: 64)
            }
            .padding(.top, 0)

            Confetti().allowsHitTesting(false)
        }
        .onAppear {
            claude = flow.claudeFound || Self.hasClaude()
            withAnimation(.spring(duration: 0.6, bounce: 0.4)) { shown = true }
        }
    }

    private var done: [String] {
        var out: [String] = []
        if flow.daemonOn { out.append("Running in the background") }
        if flow.cliPath != nil { out.append("\(CommandLineTool.linkName) command installed") }
        if !flow.connected.isEmpty { out.append(flow.connected.joined(separator: " and ") + " connected") }
        return out
    }

    private func launchCard(icon: AnyView, iconBackground: Color, title: String, text: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 10) {
                icon
                    .frame(width: 40, height: 40)
                    .background(iconBackground, in: RoundedRectangle(cornerRadius: 11, style: .continuous))
                Text(title).font(.system(size: 16, weight: .semibold))
                Text(text).font(.system(size: 13)).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            .padding(20)
            .frame(maxWidth: .infinity, minHeight: 168, maxHeight: .infinity, alignment: .topLeading)
            .background(.background.opacity(0.9), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 16, style: .continuous).strokeBorder(Color.primary.opacity(0.08)))
            .shadow(color: Brand.pine.opacity(0.06), radius: 12, y: 8)
            .contentShape(Rectangle())
        }
        .buttonStyle(LaunchButtonStyle())
        .frame(maxWidth: .infinity)
    }

    static var startDir: String? {
        let dev = (home as NSString).appendingPathComponent("Developer")
        var isDir: ObjCBool = false
        return FileManager.default.fileExists(atPath: dev, isDirectory: &isDir) && isDir.boolValue ? dev : nil
    }

    /// The same places `hmk hooks install` looks for claude.
    static func hasClaude() -> Bool {
        let fm = FileManager.default
        let path = (ProcessInfo.processInfo.environment["PATH"] ?? "").split(separator: ":").map { "\($0)/claude" }
        let extra = [".local/bin/claude", ".claude/local/claude", ".local/share/mise/shims/claude", ".npm-global/bin/claude"].map { (home as NSString).appendingPathComponent($0) }
        return (path + extra + ["/opt/homebrew/bin/claude", "/usr/local/bin/claude"]).contains { fm.isExecutableFile(atPath: $0) }
    }

    private func startProject() {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.prompt = claude ? "Start Claude Code" : "Open"
        panel.message = "Choose a project folder"
        if let start = Self.startDir { panel.directoryURL = URL(fileURLWithPath: start) }
        guard panel.runModal() == .OK, let url = panel.url else { return }
        let runClaude = claude
        Task {
            guard let group = await model.createGroup(name: url.lastPathComponent) else { return }
            // In the user's login shell, so claude is found the way their
            // terminal finds it, and the shell stays when claude exits.
            let shell = ProcessInfo.processInfo.environment["SHELL"] ?? "/bin/zsh"
            let command = runClaude ? [shell, "-lic", "claude; exec \"$0\" -l"] : []
            _ = await model.createTab(in: group, command: command, cwd: url.path)
            flow.finish()
        }
    }

    private func openShell() {
        Task {
            var group = model.groups.first { model.title(of: $0) == "Home" }?.id ?? model.groups.first?.id
            if group == nil { group = await model.createGroup(name: "Home") }
            if let group { _ = await model.createTab(in: group) }
            flow.finish()
        }
    }
}

struct LaunchButtonStyle: ButtonStyle {
    @State private var hover = false
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .scaleEffect(configuration.isPressed ? 0.98 : hover ? 1.015 : 1)
            .animation(.spring(duration: 0.25), value: configuration.isPressed)
            .animation(.spring(duration: 0.3), value: hover)
            .onHover { hover = $0 }
    }
}

/// A one-off burst of confetti in the icon's colours, falling for a few
/// seconds and then gone.
private struct Confetti: View {
    private struct Piece {
        let x, vx, vy, delay, spin, flutter, size: Double
        let round: Bool
        let color: Color
    }

    @State private var start = Date()
    @State private var running = true
    private static let duration = 5.5
    private let pieces: [Piece] = {
        var seed: UInt64 = 0x5EED
        func rnd() -> Double {
            seed = seed &* 6364136223846793005 &+ 1442695040888963407
            return Double(seed >> 11) / Double(1 << 53)
        }
        let colors: [Color] = HammockMark.colors + [Brand.mist, Brand.fern]
        return (0..<150).map { _ in
            Piece(x: rnd(), vx: (rnd() - 0.5) * 120, vy: 40 + rnd() * 140, delay: rnd() * 320, spin: (rnd() - 0.5) * 14, flutter: 4 + rnd() * 8,
                  size: 6 + rnd() * 6, round: rnd() < 0.25, color: colors[Int(rnd() * Double(colors.count)) % colors.count])
        }
    }()

    var body: some View {
        if running {
            TimelineView(.animation) { tl in
                Canvas { ctx, size in
                    let t = tl.date.timeIntervalSince(start)
                    let gravity = 140.0
                    let fade = max(0, min(1, (Self.duration - t) / 1.2))
                    for p in pieces {
                        // Dropped from above the top edge, spread across it and
                        // staggered so they arrive over a second or so.
                        let x = p.x * size.width + p.vx * t
                        let y = -20 - p.delay + p.vy * t + 0.5 * gravity * t * t
                        guard y > -20, y < size.height + 20 else { continue }
                        var c = ctx
                        c.opacity = fade
                        c.translateBy(x: x, y: y)
                        c.rotate(by: .radians(p.spin * t))
                        let squash = abs(cos(p.flutter * t))
                        let rect = CGRect(x: -p.size / 2, y: -p.size * 0.3 * squash, width: p.size, height: p.round ? p.size * squash : p.size * 0.6 * squash)
                        c.fill(p.round ? Path(ellipseIn: rect) : Path(roundedRect: rect, cornerRadius: 1.5), with: .color(p.color))
                    }
                }
            }
            .task {
                start = Date()
                try? await Task.sleep(for: .seconds(Self.duration))
                running = false
            }
            .accessibilityHidden(true)
        }
    }
}
