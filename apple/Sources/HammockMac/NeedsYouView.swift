import HammockKit
import SwiftUI

/// Shown in the sidebar as "Finished" (agents done, wanting nothing); off
/// in Settings ▸ Appearance.
enum FinishedPrefs {
    static let pref = Pref("sidebar-finished", defaults: "showFinished", true)
}

/// Collapsed Groups also show their tabs with an active agent: working,
/// waiting on you, failed, or finished and not yet looked at. Off by
/// default; in Settings ▸ Appearance and the command palette.
enum CollapsedAgentsPrefs {
    static let pref = Pref("collapsed-group-agents", defaults: "collapsedShowsAgents", false)
}

/// The attention queue, pinned to the bottom of the sidebar so the Groups
/// list above never shifts: Needs You (questions, permission prompts, join
/// requests, errors) and, below it, Finished (agents done, wanting nothing).
struct NeedsYouView: View {
    let model: AppModel
    private var showFinished: Bool { Prefs.shared.bool(FinishedPrefs.pref) }
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var needs: [AttentionItem] { model.attention.filter { $0.state != .idle } }
    private var finished: [AttentionItem] { showFinished ? model.attention.filter { $0.state == .idle } : [] }

    /// Whether there's anything to show.
    static func shows(_ model: AppModel, finished: Bool) -> Bool {
        !model.grants.isEmpty || !model.joinRequests.isEmpty || !Self.incomingLinks(model).isEmpty
            || model.attention.contains { $0.state != .idle || finished }
    }

    /// What the queue shows, sorted (a card only moving to the top doesn't
    /// count), for the bar's animation: a card arriving or leaving animates
    /// whichever update brought it.
    static func animationKey(_ model: AppModel, finished: Bool) -> [String] {
        let shown = model.attention.filter { $0.state != .idle || finished }
        return (Self.incomingLinks(model).map(\.id) + model.joinRequests.map(\.id) + model.grants.map(\.id)
            + shown.map { $0.id + ($0.state == .idle ? "+done" : "") }).sorted()
    }

    private var needsCount: Int {
        needs.count + model.grants.count + model.joinRequests.count + Self.incomingLinks(model).count
    }

    // Always here, with both lists of cards in place even when empty: a card
    // only plays its own exit while its ForEach stays (a view taken away
    // takes its cards with it, without theirs). The headings come and go.
    var body: some View {
        let shows = Self.shows(model, finished: showFinished)
        VStack(alignment: .leading, spacing: 0) {
            if shows {
                Divider().padding(.bottom, 10)
            }
            if needsCount > 0 {
                heading("Needs You", count: needsCount, tint: .orange)
                    .padding(.bottom, 6)
                    .transition(.opacity)
            }
            cards(maxHeight: 220) {
                ForEach(Self.incomingLinks(model), id: \.id) { req in
                    LinkRequestCard(model: model, request: req)
                }
                ForEach(model.joinRequests, id: \.id) { req in
                    JoinCard(model: model, request: req)
                }
                ForEach(model.grants) { grant in
                    GrantCard(model: model, grant: grant)
                }
                ForEach(needs.prefix(6)) { item in
                    AttentionCard(model: model, item: item)
                }
            }
            if !finished.isEmpty {
                HStack {
                    heading("Finished", count: finished.count, tint: .secondary)
                    Spacer()
                    Button("Clear") { clearFinished() }
                        .buttonStyle(.borderless)
                        .font(.caption)
                        .padding(.trailing, 8)
                        .help("Dismiss the finished agents")
                }
                .padding(.top, needsCount > 0 ? 12 : 0)
                .padding(.bottom, 6)
                .transition(.opacity)
            }
            cards(maxHeight: 160) {
                ForEach(finished.prefix(6)) { item in
                    AttentionCard(model: model, item: item)
                }
            }
        }
        .padding(.horizontal, 8)
        .padding(.top, shows ? 4 : 0)
    }

    /// A list of cards, as tall as they are up to maxHeight, then scrolling;
    /// no height at all when empty.
    private func cards(maxHeight: CGFloat, @ViewBuilder _ content: () -> some View) -> some View {
        ScrollView {
            VStack(spacing: 6) { content() }
        }
        .frame(maxHeight: maxHeight)
        .fixedSize(horizontal: false, vertical: true)
    }

    /// Clear: the finished cards leave one after another, top first, rather
    /// than all at once (any not shown go with the last). Each only if it's
    /// still finished by then: a pane's item keeps its id, and may be asking
    /// something new.
    private func clearFinished() {
        let step = reduceMotion ? 0 : 0.05
        for (i, item) in finished.enumerated() {
            let id = item.id
            DispatchQueue.main.asyncAfter(deadline: .now() + Double(min(i, 5)) * step) {
                if model.attention.contains(where: { $0.id == id && $0.state == .idle }) { model.clearAttention(id) }
            }
        }
    }

    /// Other computers asking to link with this one (their code showing).
    static func incomingLinks(_ model: AppModel) -> [LinkRequest] {
        model.linkRequests.filter { $0.incoming && !$0.code.isEmpty }
    }

    private func heading(_ title: String, count: Int, tint: Color) -> some View {
        HStack(spacing: 6) {
            Text(title)
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(.secondary)
            Text("\(count)")
                .contentTransition(.numericText(value: Double(count)))
                .font(.caption2.weight(.bold))
                .foregroundStyle(.white)
                .padding(.horizontal, 5)
                .frame(minWidth: 16, minHeight: 16)
                .background(tint, in: Capsule())
        }
        .padding(.horizontal, 8)
    }
}

struct AttentionCard: View {
    let model: AppModel
    let item: AttentionItem
    @State private var exit = CardExit.fade

    private var pane: Pane? { model.panes[item.paneID] }
    /// The schedule this is a run of.
    private var schedule: Schedule? { pane.flatMap { model.schedule(for: $0) } }
    private var tabName: String { pane.flatMap { model.tab(of: $0) }.map { model.title(of: $0) } ?? (item.paneID.isEmpty ? "Hammock" : "Pane") }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                StatusIcon(state: item.state, size: 16)
                VStack(alignment: .leading, spacing: 1) {
                    HStack(spacing: 5) {
                        Text(tabName).fontWeight(.semibold).lineLimit(1)
                        // On another of your computers: its badge.
                        if let c = model.group(ofPane: item.paneID).flatMap({ model.computer(of: $0) }) {
                            ComputerBadge(c, size: 12)
                        }
                    }
                    if let s = schedule {
                        Label("Scheduled · \(s.name)", systemImage: "clock")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    Text(subtitle)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                Spacer(minLength: 4)
                ElapsedText(sinceUnixMs: item.createdUnixMs)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            if item.state == .needsInput, item.question {
                HStack(spacing: 6) {
                    Spacer()
                    Button("Answer…") { model.reveal(paneID: item.paneID) }
                        .buttonStyle(.borderedProminent)
                }
                .controlSize(.small)
            } else if item.state == .needsInput {
                HStack(spacing: 6) {
                    Spacer()
                    Button("Deny") { respond(allow: false) }
                    Button("Reply…") { model.reveal(paneID: item.paneID) }
                    if let s = schedule, s.kind == "claude" {
                        // A scheduled Claude run: allow it in every run from now on.
                        Menu("Allow") {
                            Button("Allow Once") { respond(allow: true) }
                            Button("Always Allow for This Schedule") { respond(.allowSchedule) }
                        } primaryAction: {
                            respond(allow: true)
                        }
                        .menuStyle(.button)
                        .buttonStyle(.borderedProminent)
                        .fixedSize()
                        .help("Allow, or always allow this tool in “\(s.name)”’s runs")
                    } else {
                        Button("Allow") { respond(allow: true) }
                            .buttonStyle(.borderedProminent)
                    }
                }
                .controlSize(.small)
            }
        }
        .padding(10)
        .background(.background, in: RoundedRectangle(cornerRadius: 11))
        .shadow(color: .black.opacity(0.06), radius: 1.5, y: 1)
        .contentShape(Rectangle())
        .onTapGesture {
            model.reveal(paneID: item.paneID)
            if item.state != .needsInput { model.clearAttention(item.id) }
        }
        .cardMotion(exit, cornerRadius: 11)
    }

    private var subtitle: String {
        let who = pane.map { $0.displayName.capitalized } ?? "Agent"
        switch item.state {
        case .needsInput where item.question: return "\(who) asks: " + item.summary
        case .needsInput: return item.summary.isEmpty ? "\(who) is waiting for you" : item.summary
        case .idle: return "\(who) finished" + (item.summary.isEmpty ? "" : " · \(item.summary)")
        case .errored: return "\(who) \(item.summary.isEmpty ? "errored" : item.summary)"
        default: return item.summary
        }
    }

    /// Answers a permission prompt, if the pane still shows it.
    private func respond(allow: Bool) { respond(allow ? .allow : .deny) }

    private func respond(_ decision: Hammock_V1_AgentAnswer.Decision) {
        CardExit.leave(decision == .deny ? .denied : .allowed, $exit) { model.decide(item, decision) }
    }
}

/// An agent asking to act outside its default scope (via MCP).
struct GrantCard: View {
    let model: AppModel
    let grant: GrantRequest
    @State private var exit = CardExit.fade

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "hand.raised.circle.fill")
                    .symbolRenderingMode(.palette)
                    .foregroundStyle(.white, .orange)
                    .font(.system(size: 16))
                    .accessibilityLabel("Permission request")
                VStack(alignment: .leading, spacing: 3) {
                    // Hammock's words, then the agent's own, marked as such.
                    Text(grant.headlineText).fontWeight(.semibold)
                        .lineLimit(grant.action == "schedule" ? nil : 4) // a schedule: all of what you'd allow
                    if let note = grant.agentNote {
                        Text("The agent says: “\(note)”")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .lineLimit(3)
                    }
                }
            }
            HStack(spacing: 6) {
                Spacer()
                Button("Deny") { CardExit.leave(.denied, $exit) { model.respond(to: grant, .deny) } }
                Button("Always in Group") { CardExit.leave(.allowed, $exit) { model.respond(to: grant, .allowGroup) } }
                Button("Allow Once") { CardExit.leave(.allowed, $exit) { model.respond(to: grant, .allowOnce) } }
                    .buttonStyle(.borderedProminent)
            }
            .controlSize(.small)
        }
        .padding(10)
        .background(.background, in: RoundedRectangle(cornerRadius: 11))
        .shadow(color: .black.opacity(0.06), radius: 1.5, y: 1)
        .cardMotion(exit, cornerRadius: 11)
    }
}

/// Someone asking to join a Group you share.
struct JoinCard: View {
    let model: AppModel
    let request: JoinRequest
    @State private var exit = CardExit.fade

    var body: some View {
        let who = model.people[request.userID]
        let group = model.groups.first { $0.id == request.groupID }
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                if let who { AvatarView(person: who, size: 24) }
                VStack(alignment: .leading, spacing: 1) {
                    Text("\(who?.name ?? "Someone") wants to join").font(.callout.weight(.semibold))
                    Text([group.map { model.title(of: $0) }, who?.deviceName].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
                        .font(.caption).foregroundStyle(.secondary)
                    if !request.keyFingerprint.isEmpty {
                        Text("Key \(request.keyFingerprint)")
                            .font(.caption.monospaced()).foregroundStyle(.secondary)
                            .textSelection(.enabled)
                            .help("They can read theirs in Hammock’s Settings ▸ Sharing")
                    }
                }
            }
            if let warning = request.keyWarning(name: who?.name) {
                Label(warning, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption).foregroundStyle(.orange)
            }
            HStack {
                Button("Decline") { CardExit.leave(.denied, $exit) { model.respond(to: request, role: nil) } }
                Spacer()
                Button("Let View") { CardExit.leave(.allowed, $exit) { model.respond(to: request, role: .viewer) } }
                Button("Let Drive") { CardExit.leave(.allowed, $exit) { model.respond(to: request, role: .driver) } }
                    .buttonStyle(.borderedProminent)
            }
            .controlSize(.small)
        }
        .padding(10)
        .background(.background.opacity(0.6), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        .cardMotion(exit, cornerRadius: 10, style: .continuous)
    }
}

/// How a card in the queue leaves it. Answered yes, it slides off to the
/// right, tinted green as it goes; answered no, off to the left. Anything
/// else (cleared, replied to, answered in the pane or on another device)
/// fades, shrinking a little. Under Reduce Motion they all just fade.
enum CardExit {
    case fade, allowed, denied

    /// Marks the card as leaving this way, then answers a moment later, so
    /// the card has drawn its tint before the answer takes it away: grants,
    /// joins and links leave the model in the same update as their answer,
    /// and the daemon's clearing an attention item can come back within the
    /// frame. Once only: a second click, while it's going, answers nothing.
    @MainActor static func leave(_ exit: CardExit, _ state: Binding<CardExit>, then answer: @escaping @MainActor () -> Void) {
        guard state.wrappedValue == .fade else { return }
        withAnimation(.easeOut(duration: 0.1)) { state.wrappedValue = exit }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.12) { answer() }
    }

    func transition(reduceMotion: Bool) -> AnyTransition {
        // Arriving: down into place from just above, growing a touch.
        let insertion: AnyTransition = reduceMotion ? .opacity
            : .offset(y: -8).combined(with: .scale(scale: 0.96, anchor: .top)).combined(with: .opacity)
        let removal: AnyTransition
        switch (self, reduceMotion) {
        case (_, true): removal = .opacity
        case (.allowed, _): removal = .offset(x: 48).combined(with: .opacity)
        case (.denied, _): removal = .offset(x: -48).combined(with: .opacity)
        case (.fade, _): removal = .scale(scale: 0.95).combined(with: .opacity)
        }
        return .asymmetric(insertion: insertion, removal: removal)
    }
}

private struct CardMotion: ViewModifier {
    let exit: CardExit
    let cornerRadius: CGFloat
    let style: RoundedCornerStyle
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func body(content: Content) -> some View {
        content
            .overlay {
                if exit == .allowed {
                    RoundedRectangle(cornerRadius: cornerRadius, style: style)
                        .fill(StatusPalette.green.opacity(0.12))
                        .strokeBorder(StatusPalette.green.opacity(0.55))
                        .allowsHitTesting(false)
                }
            }
            .transition(exit.transition(reduceMotion: reduceMotion))
    }
}

extension View {
    /// A Needs You card's arrival and exit (CardExit); the corners its
    /// background has, for the tint.
    func cardMotion(_ exit: CardExit, cornerRadius: CGFloat, style: RoundedCornerStyle = .circular) -> some View {
        modifier(CardMotion(exit: exit, cornerRadius: cornerRadius, style: style))
    }
}
