import AppKit
import HammockKit
import SwiftUI

/// ⌘⇧O: find an agent's session on this Mac, by its title,
/// folder or anything said in it, and resume it in a new tab in its
/// folder's Group (Phase 5's thread history browser). It's the palette in
/// its other mode (PaletteState.mode), and looks like it. From a Group's
/// "Resume a Session…", only the sessions in that Group's folders, until
/// its chip is cleared.
struct SessionFinder: View {
    let model: AppModel
    @State private var query = ""
    @State private var results: [SessionInfo] = []
    @State private var indexing = false
    @State private var loaded = false
    @State private var selection = 0
    @State private var appeared = false
    @State private var confirming: String?
    @FocusState private var fieldFocused: Bool
    @Environment(\.colorScheme) private var colorScheme

    private var state: PaletteState { .shared }
    private var selected: SessionInfo? { selection < results.count ? results[selection] : nil }

    var body: some View {
        ZStack(alignment: .top) {
            Color.black.opacity(appeared ? 0.18 : 0)
                .ignoresSafeArea()
                .onTapGesture { close() }

            VStack(spacing: 0) {
                HStack(spacing: 10) {
                    Image(systemName: "clock.arrow.circlepath")
                        .font(.title2)
                        .foregroundStyle(.linearGradient(colors: [.orange, .pink], startPoint: .topLeading, endPoint: .bottomTrailing))
                        .symbolEffect(.rotate.counterClockwise, options: .nonRepeating, value: appeared)
                    if let g = inGroup {
                        groupChip(g)
                    }
                    TextField("Search", text: $query, prompt: Text(prompt).foregroundStyle(.secondary))
                        .textFieldStyle(.plain)
                        .font(.system(size: 20))
                        .focused($fieldFocused)
                        .onSubmit { resume(in: nil) }
                        .onKeyPress(keys: [.return]) { press in
                            guard press.modifiers.contains(.option) else { return .ignored }
                            resume(in: other)
                            return .handled
                        }
                        .onKeyPress(.downArrow) { move(1); return .handled }
                        .onKeyPress(.upArrow) { move(-1); return .handled }
                        .onKeyPress(.escape) { close(); return .handled }
                        // ⌫ in an empty field takes the Group's filter off
                        // (a fresh press: not ⌫ held down to clear the query).
                        .onKeyPress(.delete, phases: .down) { _ in
                            guard query.isEmpty, inGroup != nil else { return .ignored }
                            clearGroup()
                            return .handled
                        }
                        .onChange(of: query) { selection = 0 }
                    if indexing {
                        ProgressView().controlSize(.small).help("Indexing your sessions (the first time only)")
                    }
                }
                .padding(.horizontal, 18)
                .frame(height: 56)

                Divider()

                if results.isEmpty {
                    Text(emptyText)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 22)
                } else {
                    KeyboardList(items: results, id: \.key, selection: $selection, query: query, maxHeight: 420,
                                 choose: { selection = $0; resume(in: nil) }) { s, selected in
                        SessionRow(session: s, selected: selected, flashing: confirming == s.key)
                    }
                }

                Divider()
                HStack(spacing: 14) {
                    hint("return", "Resume")
                    if let other { hint("option", "↩ in \(AgentCatalog.shortName(of: other))") }
                    Spacer()
                    if let s = selected, !s.openPaneID.isEmpty {
                        Text("Open in a tab: Resume goes there").font(.caption).foregroundStyle(.secondary)
                    }
                }
                .padding(.horizontal, 16)
                .frame(height: 32)
            }
            .frame(width: 680)
            .background {
                RoundedRectangle(cornerRadius: 18).fill(.regularMaterial)
                    .overlay(RoundedRectangle(cornerRadius: 18).fill(Color.white.opacity(colorScheme == .light ? 0.75 : 0)))
            }
            .overlay(RoundedRectangle(cornerRadius: 18).strokeBorder(Color.primary.opacity(0.1), lineWidth: 0.5))
            .shadow(color: .black.opacity(0.25), radius: 30, y: 14)
            .scaleEffect(appeared ? 1 : 0.97)
            .opacity(appeared ? 1 : 0)
            .blur(radius: appeared ? 0 : 2)
            .padding(.top, 90)
        }
        .onAppear {
            // As the palette: take the keyboard from the terminal, then focus.
            NSApp.keyWindow?.makeFirstResponder(nil)
            fieldFocused = true
            DispatchQueue.main.async { fieldFocused = true }
            withAnimation(.snappy(duration: 0.18)) { appeared = true }
        }
        // Again when the query changes, or it's switched to another computer's sessions.
        .task(id: query + "\u{0}" + computerID + "\u{0}" + (inGroup?.id ?? "")) {
            // A moment's pause while typing; none for the first list.
            if loaded { try? await Task.sleep(for: .milliseconds(120)) }
            while !Task.isCancelled {
                guard let r = await model.listSessions(query, computerID: computerID, groupID: inGroup?.id ?? "") else { return }
                if Task.isCancelled { return }
                withAnimation(.snappy(duration: 0.15)) {
                    results = r.sessions
                    indexing = r.indexing
                    loaded = true
                }
                if selection >= results.count { selection = 0 }
                // The first index is still being built: more will come.
                guard r.indexing else { return }
                try? await Task.sleep(for: .milliseconds(500))
            }
        }
    }

    /// The Group whose folders' sessions it shows, if it was opened from
    /// that Group's "Resume a Session…" and that hasn't been cleared.
    private var inGroup: WorkspaceGroup? {
        guard state.sessionsInGroup, let id = state.sessionGroupID else { return nil }
        return model.groups.first { $0.id == id }
    }

    private var prompt: String {
        if let g = inGroup { return "Find a session in \(g.name)’s folders…" }
        return model.computer(computerID).map { "Find a Claude or Codex session on \($0.name)…" } ?? "Find a Claude or Codex session…"
    }

    private var emptyText: String {
        if !loaded || indexing { return "Indexing your sessions…" }
        if let g = inGroup {
            return query.isEmpty ? "No sessions in \(g.name)’s folders. ⌫ shows every session." : "No sessions in \(g.name)’s folders match “\(query)”."
        }
        return query.isEmpty ? "No sessions yet." : "No sessions match “\(query)”."
    }

    /// "In <Group>", with an ✕ to see every session.
    private func groupChip(_ g: WorkspaceGroup) -> some View {
        HStack(spacing: 4) {
            Text("In \(g.name)").lineLimit(1)
            Button { clearGroup() } label: {
                Image(systemName: "xmark.circle.fill").foregroundStyle(.secondary)
            }
            .buttonStyle(.plain)
            .help("Show every session (⌫)")
        }
        .font(.system(size: 13, weight: .medium))
        .padding(.leading, 9)
        .padding(.trailing, 5)
        .padding(.vertical, 4)
        .background(Capsule().fill(Color.themeAccent.opacity(0.15)))
        .fixedSize()
        .help("Only the sessions in the folders open in \(g.name)")
    }

    private func clearGroup() {
        withAnimation(.snappy(duration: 0.15)) { state.sessionsInGroup = false }
        selection = 0
        fieldFocused = true
    }

    /// The other agent the selected session can continue in.
    private var other: String? {
        guard let s = selected else { return nil }
        return AgentCatalog.other(of: s.harness)
    }

    private func hint(_ symbol: String, _ text: String) -> some View {
        HStack(spacing: 4) {
            Image(systemName: symbol).font(.caption2)
            Text(text).font(.caption)
        }
        .foregroundStyle(.secondary)
    }

    private func move(_ delta: Int) {
        guard !results.isEmpty else { return }
        selection = (selection + delta + results.count) % results.count
    }

    /// The linked computer whose sessions these are: the one the Group it
    /// was opened for is on ("" for this one).
    private var computerID: String {
        if !state.sessionComputerID.isEmpty { return state.sessionComputerID }
        guard let id = state.sessionGroupID, let g = model.groups.first(where: { $0.id == id }) else { return "" }
        return model.computer(of: g)?.userID ?? ""
    }

    private func resume(in target: String?) {
        guard let s = selected else { return }
        confirming = s.key
        var group = state.sessionGroupID ?? model.currentGroup?.id
        // A session goes into a Group on its own computer, else its folder's there.
        if let g = group.flatMap({ id in model.groups.first { $0.id == id } }), (model.computer(of: g)?.userID ?? "") != computerID { group = nil }
        Task { await model.resumeSession(s, in: target, groupID: group, computerID: computerID) }
        close()
    }

    private func close() {
        withAnimation(.easeOut(duration: 0.12)) { appeared = false }
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.12) {
            state.isOpen = false
            state.mode = .commands
            NotificationCenter.default.post(name: TerminalNSView.refocus, object: nil)
        }
    }
}

private struct SessionRow: View {
    let session: SessionInfo
    let selected: Bool
    let flashing: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            SessionGlyph(harness: session.harness, size: 16, font: 14)
                .frame(width: 24, height: 20)
                .help(AgentCatalog.name(of: session.harness))
            VStack(alignment: .leading, spacing: 2) {
                Text(session.title).font(.system(size: 14, weight: .medium)).lineLimit(1)
                Text(details).font(.system(size: 12)).foregroundStyle(.secondary).lineLimit(1)
                // The selected one says where it matched, or what was last asked.
                if selected, let line = excerpt {
                    Text(line).font(.system(size: 12)).foregroundStyle(.secondary).lineLimit(2)
                        .padding(.top, 2)
                }
            }
            Spacer(minLength: 8)
            if !session.openPaneID.isEmpty {
                Text("Open")
                    .font(.caption2.weight(.semibold))
                    .padding(.horizontal, 6).padding(.vertical, 2)
                    .background(Capsule().fill(Color.green.opacity(0.18)))
                    .foregroundStyle(.green)
            }
            if selected {
                Image(systemName: "return").font(.caption).foregroundStyle(.tertiary).padding(.top, 3)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background {
            RoundedRectangle(cornerRadius: 10)
                .fill(flashing ? Color.themeAccent.opacity(0.3) : selected ? Color.themeAccent.opacity(0.16) : .clear)
        }
        .contentShape(Rectangle())
    }

    private var details: String {
        var parts = [shortPath(session.cwd), relative(Date(timeIntervalSince1970: Double(session.updatedUnixMs) / 1000))]
        parts.append(session.messages == 1 ? "1 message" : "\(session.messages) messages")
        return parts.filter { !$0.isEmpty }.joined(separator: " · ")
    }

    /// The search's match (in bold), else the last thing asked.
    private var excerpt: AttributedString? {
        if !session.snippet.isEmpty {
            var out = AttributedString()
            for (i, part) in session.snippet.split(separator: "«", omittingEmptySubsequences: false).enumerated() {
                if i == 0 {
                    out += AttributedString(String(part))
                    continue
                }
                let pieces = part.split(separator: "»", maxSplits: 1, omittingEmptySubsequences: false)
                var hit = AttributedString(String(pieces[0]))
                hit.font = .system(size: 12, weight: .bold)
                hit.foregroundColor = .primary
                out += hit
                if pieces.count > 1 { out += AttributedString(String(pieces[1])) }
            }
            return out
        }
        let last = session.lastPrompt.isEmpty ? session.firstPrompt : session.lastPrompt
        return last.isEmpty ? nil : AttributedString("❯ " + last)
    }

    private func shortPath(_ p: String) -> String {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        return p.hasPrefix(home) ? "~" + p.dropFirst(home.count) : p
    }

    private func relative(_ d: Date) -> String {
        let s = Date().timeIntervalSince(d)
        if s < 60 { return "just now" }
        if s < 3600 { return "\(Int(s / 60)) min ago" }
        let cal = Calendar.current
        if cal.isDateInToday(d) { return d.formatted(date: .omitted, time: .shortened) }
        if cal.isDateInYesterday(d) { return "yesterday" }
        if cal.isDate(d, equalTo: Date(), toGranularity: .year) { return d.formatted(.dateTime.day().month(.abbreviated)) }
        return d.formatted(.dateTime.day().month(.abbreviated).year())
    }
}
