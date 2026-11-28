import SwiftUI

/// The parts of what a permission request would allow (an automation's:
/// what runs, when, where, its allowed tools and its prompt), beneath its
/// title: short ones as label and value, the tools as chips, the prompt in
/// a box with its lines. Compact (a card in a list) shows a few lines of
/// the long ones, with Show All for the rest.
public struct GrantDetailsView: View {
    let details: [Hammock_V1_GrantDetail]
    var compact: Bool
    var font: Font
    var codeFont: Font
    @State private var expanded = false

    public init(_ details: [Hammock_V1_GrantDetail], compact: Bool = false,
                font: Font = .body, codeFont: Font = .system(.callout, design: .monospaced)) {
        self.details = details
        self.compact = compact
        self.font = font
        self.codeFont = codeFont
    }

    /// Lines of a prompt, and tools, shown before Show All.
    private static let promptLines = 4
    private static let toolCount = 6

    private var short: [Hammock_V1_GrantDetail] { details.filter { !isBlock($0) } }
    private var blocks: [Hammock_V1_GrantDetail] { details.filter(isBlock) }

    private func isBlock(_ d: Hammock_V1_GrantDetail) -> Bool { d.code || d.text.contains("\n") || d.label == "Prompt" }

    private var clipped: Bool { compact && !expanded }

    /// Whether anything is left out until Show All.
    private var hasMore: Bool {
        compact && blocks.contains { d in
            d.code ? lines(d).count > Self.toolCount : lines(d).count > Self.promptLines || d.text.count > 240
        }
    }

    private func lines(_ d: Hammock_V1_GrantDetail) -> [String] {
        d.text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if !short.isEmpty {
                Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 8, verticalSpacing: 4) {
                    ForEach(Array(short.enumerated()), id: \.offset) { _, d in
                        GridRow {
                            label(d.label).gridColumnAlignment(.trailing)
                            Text(d.text)
                                .font(font)
                                .textSelection(.enabled)
                                .fixedSize(horizontal: false, vertical: true)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                }
            }
            ForEach(Array(blocks.enumerated()), id: \.offset) { _, d in
                VStack(alignment: .leading, spacing: 4) {
                    label(d.label)
                    if d.code { chips(d) } else { box(d) }
                }
            }
            if hasMore {
                Button(expanded ? "Show Less" : "Show All") {
                    withAnimation(.snappy) { expanded.toggle() }
                }
                .buttonStyle(.borderless)
                .font(.caption.weight(.semibold))
            }
        }
    }

    private func label(_ s: String) -> some View {
        Text(s).font(.caption.weight(.semibold)).foregroundStyle(.secondary)
    }

    private func chips(_ d: Hammock_V1_GrantDetail) -> some View {
        let all = lines(d)
        let shown = clipped ? Array(all.prefix(Self.toolCount)) : all
        return FlowLayout(spacing: 4, lineSpacing: 4) {
            ForEach(Array(shown.enumerated()), id: \.offset) { _, t in
                Text(t)
                    .font(codeFont)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .padding(.horizontal, 6)
                    .padding(.vertical, 2)
                    .background(.quaternary.opacity(0.6), in: RoundedRectangle(cornerRadius: 5, style: .continuous))
                    .textSelection(.enabled)
            }
            if shown.count < all.count {
                Text("\(all.count - shown.count) more")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 4)
                    .padding(.vertical, 2)
            }
        }
    }

    private func box(_ d: Hammock_V1_GrantDetail) -> some View {
        Text(d.text)
            .font(font)
            .lineLimit(clipped ? Self.promptLines : nil)
            .textSelection(.enabled)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(10)
            .background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

extension Hammock_V1_GrantRequest {
    /// A made-up agent asking to add an automation, for snapshots
    /// (HMK_DEBUG_PROMPT=grant, HMK_DEBUG_NEEDS_YOU).
    public static func debugAutomation(pane: String) -> Self {
        .with {
            $0.id = "gr_debug"; $0.requesterPaneID = pane; $0.action = "schedule"
            $0.headline = "Claude in Web ▸ api wants to add the automation “Nightly dependency check”: every day at 02:00, run Claude in ~/Developer/app with “Check for outdated dependencies, open a PR updating any with security fixes, and summarise the rest.”"
            $0.title = "Claude in Web ▸ api wants to add the automation “Nightly dependency check”"
            $0.details = [
                .with { $0.label = "Runs"; $0.text = "Claude Code" },
                .with { $0.label = "When"; $0.text = "Every day at 02:00" },
                .with { $0.label = "In"; $0.text = "~/Developer/app" },
                .with { $0.label = "Allowed tools"; $0.text = "Read\nEdit\nGrep\nBash(npm outdated:*)\nBash(npm install:*)\nBash(git commit:*)\nBash(gh pr create --draft:*)"; $0.code = true },
                .with { $0.label = "Prompt"; $0.text = "Check for outdated dependencies.\n1. Open a PR updating any with security fixes.\n2. Summarise the rest." },
            ]
            $0.description_p = "So the dependency updates happen without you having to ask each morning."
            $0.createdUnixMs = Int64(Date().timeIntervalSince1970 * 1000) - 40_000
        }
    }
}
