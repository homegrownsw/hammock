import SwiftUI

/// What floats above the message box besides the conversation: the
/// session's pull requests and artifacts, as chips that open them, and the
/// agent's task list while any of it is left to do.
struct SessionExtras: View {
    let session: AgentSession
    /// Debug (PromptCardSample): start with the task list open.
    var initiallyExpanded = false

    var body: some View {
        let open = session.tasks.contains { $0.status != .completed }
        if !session.links.isEmpty || open {
            VStack(alignment: .leading, spacing: 8) {
                if !session.links.isEmpty { LinkChips(links: session.links) }
                if open { TaskListCard(tasks: session.tasks, initiallyExpanded: initiallyExpanded) }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .transition(.opacity)
        }
    }
}

// MARK: - Pull requests and artifacts

/// The session's pull requests and artifacts, newest first, as chips.
struct LinkChips: View {
    let links: [SessionLink]
    @Environment(\.openURL) private var openURL

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                ForEach(links.reversed(), id: \.url) { link in
                    Button {
                        if let u = URL(string: link.url) { openURL(u) }
                    } label: {
                        chip(link)
                    }
                    .buttonStyle(.plain)
                    .help(Self.help(link))
                    .contextMenu {
                        if let u = URL(string: link.url) {
                            Button("Open", systemImage: "arrow.up.right.square") { openURL(u) }
                            Button("Copy Link", systemImage: "link") { copy(link.url) }
                        }
                    }
                    .accessibilityLabel(Self.help(link))
                }
            }
            .padding(.horizontal, 2)
        }
        .scrollClipDisabled()
    }

    @ViewBuilder
    private func chip(_ link: SessionLink) -> some View {
        HStack(spacing: 6) {
            if link.kind == .pullRequest {
                Image(systemName: "arrow.triangle.pull").foregroundStyle(Self.stateColor(link.state))
                Text("#\(link.number)").font(.callout.weight(.semibold).monospacedDigit())
                if !link.title.isEmpty {
                    Text(link.title).lineLimit(1).foregroundStyle(.secondary)
                }
            } else {
                Image(systemName: Self.symbol(artifactIcon: link.icon)).foregroundStyle(.tint)
                Text(link.title.isEmpty ? "Artifact" : link.title).lineLimit(1)
            }
        }
        .font(.callout)
        .frame(maxWidth: 260)
        .fixedSize(horizontal: true, vertical: false)
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .agentGlass(in: Capsule(), interactive: true)
    }

    static func help(_ link: SessionLink) -> String {
        if link.kind == .pullRequest {
            let state = stateName(link.state)
            let what = "Pull request #\(link.number)" + (link.repo.isEmpty ? "" : " in \(link.repo)")
            return [what, link.title, state].filter { !$0.isEmpty }.joined(separator: " · ")
        }
        return "Artifact: \(link.title)"
    }

    static func stateName(_ s: String) -> String {
        switch s.uppercased() {
        case "OPEN": return "Open"
        case "DRAFT": return "Draft"
        case "MERGED": return "Merged"
        case "CLOSED": return "Closed"
        default: return ""
        }
    }

    /// As GitHub colours them.
    static func stateColor(_ s: String) -> Color {
        switch s.uppercased() {
        case "OPEN": return .green
        case "MERGED": return .purple
        case "CLOSED": return .red
        default: return .secondary
        }
    }

    /// An artifact's icon word as a symbol, else a page.
    static func symbol(artifactIcon w: String) -> String {
        switch w.lowercased() {
        case "chart", "graph", "dashboard": return "chart.bar.xaxis"
        case "calendar": return "calendar"
        case "map": return "map"
        case "code": return "chevron.left.forwardslash.chevron.right"
        case "recipe", "food": return "fork.knife"
        case "slides", "deck": return "rectangle.on.rectangle"
        case "doc", "document", "notes": return "doc.text"
        case "list", "checklist", "tracker": return "checklist"
        case "game": return "gamecontroller"
        default: return "doc.richtext"
        }
    }
}

// MARK: - Task list

/// The agent's task list: how far through it is and what it's doing now,
/// opened to the whole list.
struct TaskListCard: View {
    let tasks: [AgentTask]
    @State private var expanded: Bool
    @Environment(\.agentAccent) private var accent

    init(tasks: [AgentTask], initiallyExpanded: Bool = false) {
        self.tasks = tasks
        _expanded = State(initialValue: initiallyExpanded)
    }

    private var done: Int { tasks.filter { $0.status == .completed }.count }
    private var current: AgentTask? { tasks.first { $0.status == .inProgress } ?? tasks.first { $0.status == .pending } }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button {
                withAnimation(.snappy(duration: 0.25)) { expanded.toggle() }
            } label: {
                HStack(spacing: 10) {
                    ProgressRing(fraction: tasks.isEmpty ? 0 : Double(done) / Double(tasks.count), tint: accent)
                        .frame(width: 18, height: 18)
                    Text("\(done) of \(tasks.count)").font(.callout.weight(.semibold).monospacedDigit())
                    if let t = current, !expanded {
                        Text(Self.saying(t)).font(.callout).foregroundStyle(.secondary).lineLimit(1)
                    }
                    Spacer(minLength: 0)
                    Image(systemName: "chevron.up")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .rotationEffect(.degrees(expanded ? 180 : 0))
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Tasks, \(done) of \(tasks.count) done")
            .accessibilityHint(expanded ? "Hides the list" : "Shows the list")
            if expanded {
                VStack(alignment: .leading, spacing: 7) {
                    ForEach(tasks, id: \.id) { row($0) }
                }
                .padding(.horizontal, 14)
                .padding(.bottom, 12)
                .transition(.opacity)
            }
        }
        .agentGlass(in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }

    private func row(_ t: AgentTask) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 9) {
            switch t.status {
            case .completed:
                Image(systemName: "checkmark.circle.fill").foregroundStyle(.secondary)
            case .inProgress:
                Image(systemName: "circle.dotted.circle.fill").foregroundStyle(accent)
                    .symbolEffect(.pulse, options: .repeating)
            default:
                Image(systemName: "circle").foregroundStyle(.tertiary)
            }
            Text(t.status == .inProgress ? Self.saying(t) : t.subject)
                .font(.callout.weight(t.status == .inProgress ? .semibold : .regular))
                .strikethrough(t.status == .completed, color: .secondary)
                .foregroundStyle(t.status == .completed ? AnyShapeStyle(.secondary) : AnyShapeStyle(.primary))
                .fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .combine)
        .accessibilityValue(t.status == .completed ? "Done" : t.status == .inProgress ? "Under way" : "To do")
    }

    /// What it says while a task's under way ("Running the tests"), else its subject.
    static func saying(_ t: AgentTask) -> String {
        t.status == .inProgress && !t.activeForm.isEmpty ? t.activeForm : t.subject
    }
}

private struct ProgressRing: View {
    let fraction: Double
    let tint: Color

    var body: some View {
        ZStack {
            Circle().stroke(.quaternary, lineWidth: 3)
            Circle().trim(from: 0, to: fraction)
                .stroke(tint, style: StrokeStyle(lineWidth: 3, lineCap: .round))
                .rotationEffect(.degrees(-90))
        }
        .animation(.snappy, value: fraction)
    }
}
