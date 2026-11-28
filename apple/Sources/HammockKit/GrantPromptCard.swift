import SwiftUI

/// Hammock asking you to allow something an agent in this pane asked for
/// (a GrantRequest: adding an automation, searching past sessions, typing
/// into another pane), or that hmk run in its shell asked for: the whole of
/// it, in place of the message box, as a permission prompt of the agent's
/// own shows there; over the bottom of its terminal when that's showing
/// (GrantPromptOverlay). The same answers as Needs You's card.
struct GrantPromptCard: View {
    @Environment(\.agentAccent) private var accent
    @Environment(\.agentFonts) private var fonts
    let model: AppModel
    let grant: GrantRequest
    /// How tall it may grow before scrolling (a schedule's prompt can be long).
    var maxHeight: CGFloat = 460
    @State private var natural: CGFloat = 0
    /// The answer just given, for its row's spinner (and so it's given once).
    @State private var answered: GrantDecision?

    /// The other requests from this pane waiting behind this one.
    private var queued: Int { model.grants.filter { $0.requesterPaneID == grant.requesterPaneID }.count - 1 }

    var body: some View {
        ScrollView {
            card.onGeometryChange(for: CGFloat.self) { $0.size.height } action: { natural = $0 }
        }
        .scrollBounceBehavior(.basedOnSize)
        .frame(height: natural > 0 ? min(natural, maxHeight) : nil)
        .clipShape(RoundedRectangle(cornerRadius: 24, style: .continuous))
        .agentGlass(in: RoundedRectangle(cornerRadius: 24, style: .continuous))
        .disabled(answered != nil || !model.connected)
    }

    private var card: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 6) {
                Image(systemName: "hand.raised.fill").foregroundStyle(.orange)
                Text("Hammock needs your permission").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                if queued > 0 { Text("· \(queued) more").font(.caption).foregroundStyle(.tertiary) }
                Spacer()
                ElapsedText(sinceUnixMs: grant.createdUnixMs)
                    .font(.caption)
                    .foregroundStyle(.tertiary)
            }
            Text(grant.titleText)
                .font(fonts.body(weight: .semibold))
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            if grant.hasDetails {
                GrantDetailsView(grant.details, font: fonts.body(0.92), codeFont: fonts.code(0.85))
            }
            if let origin = grant.originText {
                Text(origin)
                    .font(fonts.body(0.92))
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            if let note = grant.agentNote {
                // The agent's own words, marked as such (untrusted: Hammock's are above).
                VStack(alignment: .leading, spacing: 4) {
                    Text("The agent says").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    Text(note)
                        .font(fonts.body(0.92))
                        .textSelection(.enabled)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(10)
                .background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            }
            VStack(spacing: 6) {
                ForEach(options, id: \.label) { o in
                    option(o.icon, o.label, o.decision)
                }
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// Its answers, with icons rather than numbers: unlike the agent's own
    /// card, it takes no number keys.
    private var options: [(icon: String, label: String, decision: GrantDecision)] {
        var o: [(String, String, GrantDecision)] = [("checkmark", "Allow once", .allowOnce)]
        if grant.canAllowAlways { o.append(("checkmark.seal", "Always allow in this Group", .allowGroup)) }
        o.append(("xmark", "Deny", .deny))
        return o
    }

    private func option(_ icon: String, _ label: String, _ decision: GrantDecision) -> some View {
        Button { answer(decision) } label: {
            HStack(alignment: .center, spacing: 10) {
                Image(systemName: icon).font(.caption.weight(.semibold))
                    .frame(width: 20, height: 20)
                    .background(Circle().fill(.quaternary))
                Text(label).font(fonts.body())
                Spacer(minLength: 0)
                if answered == decision { ProgressView().controlSize(.small) }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
            .background(RoundedRectangle(cornerRadius: 10, style: .continuous).fill(.quaternary.opacity(0.6)))
        }
        .buttonStyle(.plain)
        .accessibilityLabel(label)
    }

    private func answer(_ decision: GrantDecision) {
        guard answered == nil else { return }
        answered = decision
        model.respond(to: grant, decision)
    }
}

/// The pane's oldest request from Hammock, floating over the bottom of its
/// terminal (on top, not beside it: the terminal keeps its size, so its
/// screen doesn't reflow while you answer). Nothing when there's none, or
/// in a Group shared with you to watch.
public struct GrantPromptOverlay: View {
    let model: AppModel
    let paneID: String

    public init(model: AppModel, paneID: String) {
        self.model = model
        self.paneID = paneID
    }

    public var body: some View {
        GeometryReader { geo in
            VStack {
                Spacer(minLength: 0)
                if model.group(ofPane: paneID)?.canDrive != false,
                   let grant = model.grants.last(where: { $0.requesterPaneID == paneID }) {
                    GrantPromptCard(model: model, grant: grant, maxHeight: max(200, geo.size.height * 0.7))
                        .id(grant.id)
                        .frame(maxWidth: 600)
                        .shadow(color: .black.opacity(0.18), radius: 12, y: 4)
                        .padding(12)
                        .transition(.move(edge: .bottom).combined(with: .opacity))
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .animation(.snappy(duration: 0.25), value: model.grants.last { $0.requesterPaneID == paneID }?.id)
        }
    }
}
