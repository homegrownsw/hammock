import HammockKit
import SwiftUI

// MARK: - herdr

/// herdr's saved workspace, `hmk import herdr --dry-run --json`: what
/// importing it makes.
struct HerdrPlan: Decodable {
    struct Group: Decodable, Identifiable {
        let id: String
        let name: String
        let folder: String
        let exists: Bool
        let tabs: [Tab]
    }
    struct Tab: Decodable {
        let name: String
        let layout: Node
        let panes: [Pane]
    }
    /// A pane (an index in panes), or two halves side by side ("right") or
    /// stacked ("down"), ratio the first's share.
    final class Node: Decodable {
        let pane: Int?
        let direction: String?
        let ratio: Double?
        let first: Node?
        let second: Node?
    }
    struct Pane: Decodable {
        let cwd: String
        let agent: String?
        let agentName: String?
        let resume: String?
    }
    let running: Bool
    let groups: [Group]
}

enum HerdrImport {
    /// herdr's folder, as herdr finds it (`herdr.Dir` in Go).
    static var dir: String {
        let env = ProcessInfo.processInfo.environment
        if let d = env["HERDR_HOME"], d.hasPrefix("/") { return d }
        if let x = env["XDG_CONFIG_HOME"], x.hasPrefix("/") { return x + "/herdr" }
        return NSHomeDirectory() + "/.config/herdr"
    }

    /// herdr has saved a workspace on this Mac.
    static var found: Bool { FileManager.default.fileExists(atPath: dir + "/session.json") }

    /// The bundled hmk, talking to this app's daemon.
    static func run(_ args: [String]) async -> HammockCLI.Result {
        await HammockCLI.run(["import", "herdr"] + args, env: ["HMK_SOCKET": DaemonConnection.defaultSocketPath])
    }
}

struct HerdrStep: View {
    @State private var plan: HerdrPlan?
    @State private var loadError: String?
    @State private var selected: Set<String> = []
    @State private var importing = false
    @State private var importError: String?

    private var chosen: [HerdrPlan.Group] { plan?.groups.filter { selected.contains($0.id) } ?? [] }

    var body: some View {
        VStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 0) {
                StepHeading(title: "Bring your workspace from herdr",
                            subtitle: "Each herdr workspace becomes a Group, with its tabs, splits and folders, and its agents pick their conversations up where they left off.")
                if let plan {
                    if plan.running { runningBanner.padding(.top, 16) }
                    if plan.groups.isEmpty {
                        Text("herdr has no saved workspaces to bring over.")
                            .foregroundStyle(.secondary)
                            .padding(.top, 30)
                    } else {
                        ScrollView { cards.padding(.vertical, 2) }
                            .padding(.top, 18)
                    }
                    if let importError {
                        Text(importError).font(.system(size: 12)).foregroundStyle(.red).textSelection(.enabled).padding(.top, 8)
                    }
                } else {
                    HStack(spacing: 10) {
                        if let loadError {
                            Text(loadError).foregroundStyle(.red).textSelection(.enabled)
                            Button("Try Again") { Task { await load() } }
                        } else {
                            ProgressView().controlSize(.small)
                            Text("Reading herdr's workspace…").foregroundStyle(.secondary)
                        }
                    }
                    .padding(.top, 40)
                }
            }
            .padding(.horizontal, 88)
            .padding(.top, 4)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)

            StepFooter {
                if importing { ProgressView().controlSize(.small) }
                Button(primaryTitle) { Task { await runImport() } }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.large)
                    .keyboardShortcut(.defaultAction)
                    .disabled(importing || (plan == nil && loadError == nil))
            }
        }
        .task { if plan == nil { await load() } }
    }

    private var primaryTitle: String {
        switch chosen.count {
        case 0: plan?.groups.isEmpty == false ? "Skip" : "Continue"
        case 1: "Import 1 Group"
        case let n: "Import \(n) Groups"
        }
    }

    private var runningBanner: some View {
        HStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(Brand.needsYou)
            Text("herdr is still running. Quit it first, so the same agents aren't working in both.")
            Spacer(minLength: 8)
            Button("Check Again") { Task { await load() } }
        }
        .font(.system(size: 12))
        .padding(.horizontal, 14).padding(.vertical, 10)
        .background(Brand.needsYou.opacity(0.1), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    }

    private var cards: some View {
        let groups = plan?.groups ?? []
        let rows = stride(from: 0, to: groups.count, by: 2).map { Array(groups[$0..<min($0 + 2, groups.count)]) }
        return Grid(horizontalSpacing: 16, verticalSpacing: 16) {
            ForEach(rows, id: \.first?.id) { row in
                GridRow(alignment: .top) {
                    ForEach(row) { card($0) }
                    if row.count < 2 { Color.clear.gridCellUnsizedAxes([.horizontal, .vertical]) }
                }
            }
        }
        .fixedSize(horizontal: false, vertical: true)
    }

    private func card(_ g: HerdrPlan.Group) -> some View {
        let on = selected.contains(g.id)
        return OnboardingCard(padding: 16, highlighted: on) {
            VStack(alignment: .leading, spacing: 10) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(g.name).font(.system(size: 15, weight: .semibold)).lineLimit(1)
                        Text((g.folder as NSString).abbreviatingWithTildeInPath)
                            .font(.system(size: 12)).foregroundStyle(.secondary)
                            .lineLimit(1).truncationMode(.middle)
                    }
                    Spacer(minLength: 8)
                    if g.exists {
                        Text("Already in Hammock").font(.system(size: 12)).foregroundStyle(.secondary).fixedSize()
                    }
                    Toggle("Import \(g.name)", isOn: Binding(get: { on }, set: { v in
                        if v { selected.insert(g.id) } else { selected.remove(g.id) }
                    }))
                    .toggleStyle(.checkbox)
                    .labelsHidden()
                }
                Divider()
                VStack(alignment: .leading, spacing: 7) {
                    ForEach(Array(g.tabs.enumerated()), id: \.offset) { i, t in tabRow(t, number: i + 1) }
                }
            }
        }
        .opacity(on ? 1 : 0.7)
    }

    private func tabRow(_ t: HerdrPlan.Tab, number: Int) -> some View {
        let agents = t.panes.compactMap { p in p.resume != nil ? p.agentName : nil }
        return HStack(spacing: 9) {
            HerdrLayoutThumb(node: t.layout)
                .frame(width: 26, height: 17)
                .accessibilityHidden(true)
            Text(t.name.isEmpty ? "Tab \(number)" : t.name)
                .foregroundStyle(t.name.isEmpty ? .secondary : .primary)
                .lineLimit(1).truncationMode(.tail)
            Spacer(minLength: 6)
            if let a = agents.first {
                Label(agents.count > 1 ? "\(agents.count) agents resume" : "\(a) resumes", systemImage: "arrow.clockwise")
                    .font(.system(size: 11))
                    .foregroundStyle(Brand.done)
                    .lineLimit(1)
                    .fixedSize()
            }
        }
        .font(.system(size: 12.5))
    }

    private func load() async {
        loadError = nil
        let r = await HerdrImport.run(["--dry-run", "--json"])
        guard r.ok, let p = try? Self.decoder.decode(HerdrPlan.self, from: r.stdout) else {
            loadError = r.output.isEmpty ? "Couldn't read herdr's workspace." : r.output
            return
        }
        withAnimation(.smooth) {
            // Those Hammock has already start unticked.
            if plan == nil { selected = Set(p.groups.filter { !$0.exists }.map(\.id)) }
            plan = p
        }
    }

    private func runImport() async {
        importError = nil
        let ids = chosen.map(\.id)
        if !ids.isEmpty {
            importing = true
            let r = await HerdrImport.run(["--only", ids.joined(separator: ",")])
            importing = false
            guard r.ok else {
                importError = r.output
                return
            }
            Onboarding.shared.imported = ids.count
        }
        Onboarding.shared.next()
    }

    private static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.keyDecodingStrategy = .convertFromSnakeCase
        return d
    }()
}

/// A tab's splits, drawn small.
struct HerdrLayoutThumb: View {
    let node: HerdrPlan.Node

    var body: some View {
        GeometryReader { geo in
            cell(node, geo.size)
        }
    }

    private func cell(_ n: HerdrPlan.Node, _ size: CGSize) -> AnyView {
        guard let first = n.first, let second = n.second else {
            return AnyView(RoundedRectangle(cornerRadius: 2, style: .continuous)
                .fill(Color.primary.opacity(0.07))
                .strokeBorder(Color.primary.opacity(0.35), lineWidth: 1)
                .frame(width: size.width, height: size.height))
        }
        let gap: CGFloat = 1.5
        let r = n.ratio ?? 0.5
        if n.direction == "down" {
            let h = (size.height - gap) * r
            return AnyView(VStack(spacing: gap) {
                cell(first, CGSize(width: size.width, height: h))
                cell(second, CGSize(width: size.width, height: size.height - gap - h))
            })
        }
        let w = (size.width - gap) * r
        return AnyView(HStack(spacing: gap) {
            cell(first, CGSize(width: w, height: size.height))
            cell(second, CGSize(width: size.width - gap - w, height: size.height))
        })
    }
}
