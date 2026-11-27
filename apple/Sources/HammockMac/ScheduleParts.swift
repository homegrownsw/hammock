import AppKit
import HammockKit
import SwiftUI

// The pieces the Automations window's read view and editor are built from:
// sentences that wrap (FlowLayout), the tokens and boxes in them, and
// triggers that differ only by their target (a repository, a calendar, an
// app, a drive, a display, a Wi-Fi network) gathered into one row.

/// Lays its views out left to right, wrapping onto new lines, as words in a
/// sentence: each at its own size, lined up on their centres.
struct FlowLayout: Layout {
    var spacing: CGFloat = 6
    var lineSpacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let lines = lines(for: subviews, width: proposal.width ?? .infinity)
        let width = lines.map(\.width).max() ?? 0
        let height = lines.map(\.height).reduce(0, +) + lineSpacing * CGFloat(max(lines.count - 1, 0))
        return CGSize(width: proposal.width.map { min($0, width) } ?? width, height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var y = bounds.minY
        for line in lines(for: subviews, width: bounds.width) {
            var x = bounds.minX
            for i in line.items {
                let size = subviews[i].sizeThatFits(.unspecified)
                let w = min(size.width, bounds.width)
                subviews[i].place(at: CGPoint(x: x, y: y + line.height / 2), anchor: .leading,
                                  proposal: ProposedViewSize(width: w, height: size.height))
                x += w + spacing
            }
            y += line.height + lineSpacing
        }
    }

    private struct Line {
        var items: [Int] = []
        var width: CGFloat = 0
        var height: CGFloat = 0
    }

    private func lines(for subviews: Subviews, width: CGFloat) -> [Line] {
        var out: [Line] = []
        var cur = Line()
        for i in subviews.indices {
            let size = subviews[i].sizeThatFits(.unspecified)
            let w = min(size.width, width)
            if !cur.items.isEmpty, cur.width + spacing + w > width {
                out.append(cur)
                cur = Line()
            }
            cur.width += (cur.items.isEmpty ? 0 : spacing) + w
            cur.height = max(cur.height, size.height)
            cur.items.append(i)
        }
        if !cur.items.isEmpty { out.append(cur) }
        return out
    }
}

/// A value in a sentence you can click to change (a popover's): the theme's
/// colour on a light wash of it.
struct TokenStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.medium))
            .lineLimit(1)
            .foregroundStyle(Color.themeAccent)
            .padding(.horizontal, 7)
            .padding(.vertical, 3)
            .background(Color.themeAccent.opacity(configuration.isPressed ? 0.24 : 0.13), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .contentShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
    }
}

/// A plain value shown in a row (a repository, a condition): a light grey chip.
struct Chip: View {
    let text: String
    var mono = false

    init(_ text: String, mono: Bool = false) {
        self.text = text
        self.mono = mono
    }

    var body: some View {
        Text(text)
            .font(mono ? .caption.monospaced() : .callout)
            .lineLimit(1)
            .truncationMode(.middle)
            .padding(.horizontal, 7)
            .padding(.vertical, 2)
            .background(.quaternary.opacity(0.6), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
    }
}

/// Small capitals over a section: WHEN, ONLY IF, RUN.
struct SectionLabel: View {
    let text: String

    init(_ text: String) { self.text = text }

    var body: some View {
        Text(text.uppercased())
            .font(.caption.weight(.semibold))
            .tracking(0.4)
            .foregroundStyle(.secondary)
            .accessibilityAddTraits(.isHeader)
    }
}

/// An outlined box of rows with hairlines between them.
struct RowBox<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Group(subviews: content) { rows in
                ForEach(Array(rows.enumerated()), id: \.element.id) { i, row in
                    if i > 0 { Divider().opacity(0.6) }
                    row
                }
            }
        }
        .background(.background.opacity(0.5), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay { RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(.quaternary) }
    }
}

/// The − at the end of a row that takes it away.
struct RemoveRowButton: View {
    let label: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Image(systemName: "minus.circle.fill")
                .font(.system(size: 15))
                .foregroundStyle(.tertiary)
        }
        .buttonStyle(.borderless)
        .help(label)
        .accessibilityLabel(label)
    }
}

// MARK: Triggers gathered by target

extension EventTrigger {
    /// What a trigger watches that others like it may watch something else
    /// of: those differing only in this share one row.
    enum Target {
        case repository, calendar, app, drive, display, network

        /// "16 repositories".
        func count(_ n: Int) -> String {
            let noun = switch self {
            case .repository: n == 1 ? "repository" : "repositories"
            case .calendar: n == 1 ? "calendar" : "calendars"
            case .app: n == 1 ? "app" : "apps"
            case .drive: n == 1 ? "drive" : "drives"
            case .display: n == 1 ? "display" : "displays"
            case .network: n == 1 ? "network" : "networks"
            }
            return "\(n) \(noun)"
        }

        /// What an empty target means.
        var any: String {
            switch self {
            case .repository: "the folder’s repository"
            case .calendar: "any calendar"
            case .app: "an app"
            case .drive: "any drive"
            case .display: "any display"
            case .network: "any network"
            }
        }

        /// The field's placeholder, adding one.
        var prompt: String {
            switch self {
            case .repository: "Add owner/name"
            case .calendar: "Add a calendar"
            case .app: "Add an app"
            case .drive: "Add a drive’s name"
            case .display: "Add a display’s name"
            case .network: "Add a network"
            }
        }
    }

    var target: Target? {
        switch kind {
        case .github: .repository
        case .calendar(let c): c.when == .free ? nil : .calendar
        case .mac(let m):
            switch m.event {
            case .appOpened, .appQuit: .app
            case .drive: .drive
            case .display: .display
            case .wifiJoined, .wifiLeft: .network
            default: nil
            }
        default: nil
        }
    }

    /// Its target's value ("": the folder's repository, or any).
    var targetValue: String {
        get {
            switch kind {
            case .github(let g): g.watch
            case .calendar(let c): c.calendar
            case .mac(let m): m.name
            default: ""
            }
        }
        set {
            switch kind {
            case .github(var g): g.watch = newValue; g.repo = ""; kind = .github(g)
            case .calendar(var c): c.calendar = newValue; kind = .calendar(c)
            case .mac(var m): m.name = newValue; kind = .mac(m)
            default: break
            }
        }
    }

    /// Its target as it reads: the repository the daemon found for the
    /// folder's, else the value.
    var targetText: String {
        if case .github(let g) = kind, g.watch.isEmpty { return g.repo.isEmpty ? target?.any ?? "" : g.repo }
        return targetValue.isEmpty ? target?.any ?? "" : targetValue
    }

    /// Itself less its target and what the daemon keeps of it: what the
    /// triggers of one row have in common.
    var shape: EventTrigger {
        var t = self
        t.id = ""
        t.error = ""
        t.checkedUnixMs = 0
        t.firedUnixMs = 0
        t.held = ""
        if t.target != nil { t.targetValue = "" }
        if case .check(var c) = t.kind { c.lastResult = ""; t.kind = .check(c) }
        return t
    }

    /// The triggers' indices in rows: those of one shape with a target
    /// together, in the order each row's first appears.
    static func rows(_ events: [EventTrigger]) -> [[Int]] {
        var out: [[Int]] = []
        var shapes: [EventTrigger] = []
        for (i, t) in events.enumerated() {
            if t.target != nil, let r = shapes.firstIndex(of: t.shape), events[out[r][0]].target != nil {
                out[r].append(i)
            } else {
                out.append([i])
                shapes.append(t.shape)
            }
        }
        return out
    }

    /// One row's triggers as one: the first's settings, the latest any of
    /// them checked and fired, and what holds any back.
    static func merged(_ ts: [EventTrigger]) -> EventTrigger {
        guard var m = ts.first else { return EventTrigger() }
        m.checkedUnixMs = ts.map(\.checkedUnixMs).max() ?? 0
        m.firedUnixMs = ts.map(\.firedUnixMs).max() ?? 0
        m.held = ts.first { !$0.held.isEmpty }?.held ?? ""
        m.error = ts.map(\.error).filter { !$0.isEmpty }.uniqued().joined(separator: " · ")
        return m
    }

    /// What fires it, without its target: "My pull request gets a review or
    /// comment", "An app opens".
    func textWithoutTarget(names: ((String) -> String?)?) -> String {
        guard target != nil else { return text(names: names) }
        var t = self
        t.targetValue = ""
        if case .github(var g) = t.kind { g.repo = ""; t.kind = .github(g) }
        if case .mac(let m) = t.kind {
            switch m.event {
            case .appOpened: return "An app opens"
            case .appQuit: return "An app quits"
            default: break
            }
        }
        return t.text(names: names)
    }
}

extension Array where Element: Hashable {
    /// The elements, each once, in order.
    func uniqued() -> [Element] {
        var seen = Set<Element>()
        return filter { seen.insert($0).inserted }
    }
}

extension Schedule {
    /// Its triggers in rows (EventTrigger.rows).
    var triggerRows: [[EventTrigger]] { EventTrigger.rows(events).map { $0.map { events[$0] } } }

    /// What starts it, for the list: its times ("Weekdays at 08:30"), its
    /// one row of triggers ("Review or comment · 16 repositories"), or how
    /// many.
    var listWhenText: String {
        let rows = triggerRows
        let triggers: String? = switch rows.count {
        case 0: nil
        case 1:
            if let target = rows[0][0].target, rows[0].count > 1 {
                rows[0][0].textWithoutTarget(names: nil) + " · " + target.count(rows[0].count)
            } else {
                "When " + rows[0][0].text(names: nil).lowercasedFirstWord
            }
        default: "On \(rows.count) triggers"
        }
        switch (timesText, triggers) {
        case (let t?, nil): return t
        case (let t?, _): return "\(t) + \(rows.count) trigger\(rows.count == 1 ? "" : "s")"
        case (nil, let x?): return x
        default: return "Only when run"
        }
    }
}

extension String {
    /// The string with its first letter in lower case ("CI …" kept: two capitals).
    var lowercasedFirstWord: String {
        guard let f = first, f.isUppercase else { return self }
        if count > 1, self[index(after: startIndex)].isUppercase { return self }
        return f.lowercased() + dropFirst()
    }
}

/// A link-like button in the theme's colour (AppKit's link style is the
/// system's blue).
extension View {
    /// Pop-ups read as controls: a neutral fill with the label's own colour,
    /// not the theme's tint (which made them read as links). What's meant
    /// as a link (Add Trigger, tokens, Insert Event) gets the theme's
    /// accent back with .themeLinkTint().
    func neutralPopUps() -> some View { tint(Color(nsColor: .labelColor)) }
    /// The theme's accent again, inside neutralPopUps.
    func themeLinkTint() -> some View { tint(Color.themeAccent) }
}

struct ThemeLinkStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(Color.themeAccent)
            .opacity(configuration.isPressed ? 0.6 : 1)
            .contentShape(Rectangle())
    }
}
