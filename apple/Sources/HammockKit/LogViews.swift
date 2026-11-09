import SwiftUI
#if canImport(UIKit)
import UIKit
#else
import AppKit
#endif

/// A log pane's entries, filtered: a filter bar (text, levels, processes and
/// tags) over the newest entries, following the end as they come. Shared by
/// the Mac and iOS apps.
public struct LogConsoleView: View {
    let model: AppModel
    let paneID: String
    @State private var session: LogSession?

    public init(model: AppModel, paneID: String) {
        self.model = model
        self.paneID = paneID
    }

    public var body: some View {
        Group {
            if let session {
                LogConsoleContent(session: session)
            } else {
                Color.clear
            }
        }
        .onAppear { if session == nil { session = model.acquireLog(paneID) } }
        .onDisappear {
            if session != nil {
                model.releaseLog(paneID)
                session = nil
            }
        }
    }
}

private struct LogConsoleContent: View {
    let session: LogSession
    @State private var text = ""
    @State private var follow = true
    @State private var expanded: Set<UInt64> = []
    @State private var lastOffset: CGFloat = 0

    var body: some View {
        VStack(spacing: 0) {
            LogFilterBar(session: session, text: $text, follow: $follow)
            Divider()
            if session.entries.isEmpty {
                empty
            } else {
                list
            }
        }
        .onAppear { text = session.text }
        .onChange(of: text) { _, t in session.setText(t) }
    }

    @ViewBuilder private var empty: some View {
        VStack(spacing: 8) {
            Spacer()
            if !session.error.isEmpty {
                Text(session.error).foregroundStyle(.secondary)
            } else if !session.loaded {
                ProgressView().controlSize(.small)
            } else if session.isFiltered {
                Text("Nothing matches").font(.headline).foregroundStyle(.secondary)
                Button("Clear Filters") { text = ""; session.clearFilters() }
            } else {
                Text("Waiting for the log…").foregroundStyle(.secondary)
            }
            Spacer()
        }
        .frame(maxWidth: .infinity)
    }

    private var list: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0) {
                    if session.hasMore {
                        Button(session.loadingOlder ? "Loading…" : "Show Older") { session.loadOlder() }
                            .buttonStyle(.borderless)
                            .font(.caption)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 6)
                            .onAppear { if !follow { session.loadOlder() } }
                    }
                    ForEach(session.entries, id: \.seq) { e in
                        LogRow(entry: e, highlight: session.text, expanded: expanded.contains(e.seq), session: session)
                            .id(e.seq)
                            .contentShape(Rectangle())
                            .onTapGesture {
                                if expanded.contains(e.seq) { expanded.remove(e.seq) } else { expanded.insert(e.seq) }
                            }
                    }
                }
                .padding(.vertical, 2)
            }
            .onScrollGeometryChange(for: CGFloat.self) { g in
                g.contentOffset.y + g.containerSize.height - g.contentSize.height
            } action: { _, fromEnd in
                // Scrolled up by hand: stop following; back at the end: follow.
                if fromEnd > -24 {
                    if !follow { follow = true }
                } else if fromEnd < lastOffset - 4, follow {
                    follow = false
                }
                lastOffset = fromEnd
            }
            .onChange(of: session.appended) {
                guard follow, let last = session.entries.last?.seq else { return }
                proxy.scrollTo(last, anchor: .bottom)
            }
            .onChange(of: follow) { _, on in
                if on, let last = session.entries.last?.seq { withAnimation(.snappy(duration: 0.2)) { proxy.scrollTo(last, anchor: .bottom) } }
            }
            .onAppear {
                if let last = session.entries.last?.seq { proxy.scrollTo(last, anchor: .bottom) }
            }
        }
    }
}

/// Text, level chips, process and tag menus, the filters in use, follow
/// and clear.
private struct LogFilterBar: View {
    let session: LogSession
    @Binding var text: String
    @Binding var follow: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            #if os(iOS)
            HStack(spacing: 10) {
                field
                followButton
                clearButton
            }
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                    levelChips
                    facetMenu("process", title: "Process", icon: "app")
                    facetMenu("tag", title: "Tag", icon: "tag")
                    Text(count).font(.caption).foregroundStyle(.secondary).fixedSize()
                }
            }
            #else
            HStack(spacing: 8) {
                field
                levelChips
                facetMenu("process", title: "Process", icon: "app")
                facetMenu("tag", title: "Tag", icon: "tag")
                Spacer(minLength: 0)
                Text(count).font(.caption).foregroundStyle(.secondary).lineLimit(1).fixedSize()
                followButton
                clearButton
            }
            #endif
            if !session.only.isEmpty || !session.hide.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 6) {
                        ForEach(session.only, id: \.self) { k in token(k, hidden: false) }
                        ForEach(session.hide, id: \.self) { k in token(k, hidden: true) }
                        Button("Clear") { text = ""; session.clearFilters() }
                            .buttonStyle(.borderless)
                            .font(.caption)
                    }
                }
            }
        }
        .font(.callout)
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
    }

    private var field: some View {
        HStack(spacing: 5) {
            Image(systemName: "line.3.horizontal.decrease").foregroundStyle(.secondary)
            TextField("Filter", text: $text, prompt: Text("Filter (text or /regex/)"))
                .textFieldStyle(.plain)
                #if os(iOS)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                #endif
            if !text.isEmpty {
                Button { text = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(.tertiary) }
                    .buttonStyle(.plain)
            }
        }
        .padding(.horizontal, 7)
        .padding(.vertical, 4)
        .background(.quaternary.opacity(0.6), in: RoundedRectangle(cornerRadius: 6))
        .frame(minWidth: 120)
    }

    private var followButton: some View {
        Button { follow.toggle() } label: {
            Image(systemName: follow ? "arrow.down.to.line.compact" : "pause")
        }
        .buttonStyle(.borderless)
        .help(follow ? "Following new entries (scroll up to stop)" : "Paused: follow new entries")
    }

    private var clearButton: some View {
        Button { session.clearLog() } label: { Image(systemName: "trash") }
            .buttonStyle(.borderless)
            .help("Clear the log (the terminal keeps its output)")
    }

    private var count: String {
        let n = session.entries.count
        if session.isFiltered { return "\(n)\(session.hasMore ? "+" : "") shown" }
        return "\(session.total.formatted()) entries"
    }

    private var levelChips: some View {
        HStack(spacing: 3) {
            ForEach(session.presentLevels, id: \.self) { l in
                let on = session.levels.contains(l)
                Button { session.toggleLevel(l) } label: {
                    Text(l.label)
                        .font(.caption.weight(.medium))
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .foregroundStyle(on ? Color.white : l.color)
                        .background(on ? l.color : l.color.opacity(0.12), in: Capsule())
                }
                .buttonStyle(.plain)
                .help(on ? "Showing \(l.label.lowercased()) entries: click to stop" : "Show only \(l.label.lowercased()) entries (with any others picked)")
            }
        }
    }

    private func facetMenu(_ kind: String, title: String, icon: String) -> some View {
        let fs = session.facets(kind)
        return Menu {
            ForEach(fs.prefix(40), id: \.key) { f in
                Toggle(isOn: Binding(get: { session.only.contains(f.key) }, set: { _ in session.toggleOnly(f.key) })) {
                    Text("\(f.name)  \(f.count.formatted())")
                }
            }
            if fs.isEmpty { Text("None yet") }
        } label: {
            Label(title, systemImage: icon)
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
        .disabled(fs.isEmpty)
        .opacity(fs.isEmpty ? 0.5 : 1)
    }

    private func token(_ key: String, hidden: Bool) -> some View {
        let parts = key.split(separator: ":", maxSplits: 1).map(String.init)
        let name = parts.count == 2 ? parts[1] : key
        return HStack(spacing: 3) {
            Image(systemName: hidden ? "eye.slash" : (parts.first == "tag" ? "tag" : "app"))
            Text(hidden ? "Not \(name)" : name).lineLimit(1)
            Button {
                if hidden { session.toggleHide(key) } else { session.toggleOnly(key) }
            } label: { Image(systemName: "xmark").font(.system(size: 8, weight: .bold)) }
                .buttonStyle(.plain)
        }
        .font(.caption)
        .padding(.horizontal, 7)
        .padding(.vertical, 2)
        .background(Color.accentColor.opacity(0.14), in: Capsule())
    }
}

private struct LogRow: View {
    let entry: LogEntry
    let highlight: String
    let expanded: Bool
    let session: LogSession

    #if os(iOS)
    private let size: CGFloat = 12
    #else
    private let size: CGFloat = 11.5
    #endif

    private static let timeFormat: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "HH:mm:ss.SSS"
        return f
    }()

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(Self.timeFormat.string(from: Date(timeIntervalSince1970: Double(entry.timeUnixMs) / 1000)))
                    .foregroundStyle(.secondary)
                Text(entry.level.short)
                    .fontWeight(.semibold)
                    .foregroundStyle(entry.level.color)
                    .frame(width: 44, alignment: .leading)
                if !entry.process.isEmpty {
                    Text(entry.process).foregroundStyle(.secondary).lineLimit(1)
                }
                if !entry.tag.isEmpty {
                    Text(entry.tag).foregroundStyle(.tertiary).lineLimit(1)
                }
                Text(messageText)
                    .foregroundStyle(entry.level == .debug ? .secondary : .primary)
                    .lineLimit(expanded ? nil : 1)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if !expanded, extraLines > 0 {
                    Text("+\(extraLines)").font(.system(size: size - 2)).foregroundStyle(.tertiary)
                }
            }
            if expanded { details }
        }
        .font(.system(size: size, design: .monospaced))
        .padding(.horizontal, 10)
        .padding(.vertical, 2)
        .background(entry.level.rowTint)
        .contextMenu { menu }
    }

    private var extraLines: Int { entry.message.reduce(0) { $1 == "\n" ? $0 + 1 : $0 } }

    private var messageText: AttributedString {
        var s = AttributedString(expanded ? entry.message : String(entry.message.prefix(while: { $0 != "\n" })))
        let q = highlight.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty, !(q.hasPrefix("/") && q.hasSuffix("/") && q.count > 2) else { return s }
        var from = s.startIndex
        while from < s.endIndex, let r = s[from...].range(of: q, options: .caseInsensitive) {
            s[r].backgroundColor = Color.yellow.opacity(0.45)
            from = r.upperBound
        }
        return s
    }

    @ViewBuilder private var details: some View {
        let rows: [(String, String)] = [
            ("pid", entry.pid == 0 ? "" : String(entry.pid)),
            ("category", entry.category),
        ].filter { !$0.1.isEmpty } + entry.fields.sorted { $0.key < $1.key }.map { ($0.key, $0.value) }
        if !rows.isEmpty {
            Grid(alignment: .leading, horizontalSpacing: 10, verticalSpacing: 2) {
                ForEach(rows, id: \.0) { k, v in
                    GridRow {
                        Text(k).foregroundStyle(.secondary)
                        Text(v).textSelection(.enabled)
                    }
                }
            }
            .padding(.leading, 70)
        }
    }

    @ViewBuilder private var menu: some View {
        Button("Copy Message") { copy(entry.message) }
        if !entry.process.isEmpty {
            Divider()
            Button("Show Only \(entry.process)") { session.toggleOnly("process:" + entry.process) }
            Button("Hide \(entry.process)") { session.toggleHide("process:" + entry.process) }
        }
        if !entry.tag.isEmpty {
            Divider()
            Button("Show Only \(entry.tag)") { session.toggleOnly("tag:" + entry.tag) }
            Button("Hide \(entry.tag)") { session.toggleHide("tag:" + entry.tag) }
        }
        Divider()
        Button("Show Only \(entry.level.label)") { session.setLevels([entry.level]) }
    }

    private func copy(_ s: String) {
        #if canImport(UIKit)
        UIPasteboard.general.string = s
        #else
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(s, forType: .string)
        #endif
    }
}

extension LogLevel {
    public var color: Color {
        switch self {
        case .debug: .gray
        case .notice: .teal
        case .warning: .orange
        case .error: .red
        case .fault: .purple
        default: .blue
        }
    }

    var rowTint: Color {
        switch self {
        case .warning: Color.orange.opacity(0.07)
        case .error: Color.red.opacity(0.08)
        case .fault: Color.purple.opacity(0.1)
        default: .clear
        }
    }
}
