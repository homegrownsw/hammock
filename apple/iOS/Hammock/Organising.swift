import HammockKit
import PhotosUI
import SwiftUI

// Making, naming, arranging and closing Groups and tabs from the phone, as
// the Mac's sidebar does.

extension View {
    /// Renames the tab while one is set (an empty name goes back to the
    /// automatic one).
    func renameTabAlert(model: AppModel, tab: Binding<WorkspaceTab?>) -> some View {
        modifier(RenameTabAlert(model: model, tab: tab))
    }
}

struct RenameTabAlert: ViewModifier {
    let model: AppModel
    @Binding var tab: WorkspaceTab?
    @State private var name = ""

    func body(content: Content) -> some View {
        content
            .alert("Rename Tab", isPresented: Binding(get: { tab != nil }, set: { if !$0 { tab = nil } }), presenting: tab) { t in
                TextField(automaticName(t), text: $name)
                Button("Cancel", role: .cancel) {}
                Button("Rename") {
                    let n = name.trimmingCharacters(in: .whitespacesAndNewlines)
                    Task { await model.renameTab(t.id, n) }
                }
            } message: { _ in
                Text("Leave it empty to name it after what it's doing.")
            }
            .onChange(of: tab?.id) { name = tab?.name ?? "" }
    }

    private func automaticName(_ t: WorkspaceTab) -> String {
        var unnamed = t
        unnamed.name = ""
        return model.title(of: unnamed)
    }
}

/// The tab actions a row's menu and the tab screen share.
struct TabActions: View {
    let model: AppModel
    let tab: WorkspaceTab
    let rename: () -> Void
    var newTab: (() -> Void)?
    /// A tab of the Group's files (FilesScreen).
    var newFilesTab: (() -> Void)?
    /// Set to the tab when closing it needs a yes (closeTabConfirmation).
    @Binding var closing: WorkspaceTab?
    /// Set to the tab to ask about removing its worktree (removeWorktreeConfirmation).
    @Binding var removing: WorkspaceTab?

    var body: some View {
        let (closing, removing) = ($closing, $removing)
        MenuEntries(entries: Self.entries(model: model, tab: tab, rename: rename, newTab: newTab, newFilesTab: newFilesTab,
                                          askRemove: { removing.wrappedValue = $0 }) { closing.wrappedValue = $0 })
    }

    /// Its items, for SwiftUI's menus (body) and UIKit's (a row's HoldMenu).
    /// `askClose`: closing it needs a yes (closeTabConfirmation); `askRemove`:
    /// Remove Worktree… asks first (removeWorktreeConfirmation).
    static func entries(model: AppModel, tab: WorkspaceTab, rename: @escaping () -> Void, newTab: (() -> Void)?, newFilesTab: (() -> Void)? = nil,
                        askRemove: @escaping (WorkspaceTab) -> Void, askClose: @escaping (WorkspaceTab) -> Void) -> [MenuEntry] {
        var out: [MenuEntry] = [.button("Rename…", systemImage: "pencil", rename)]
        if let newTab {
            out.append(.button("New Tab in Group", systemImage: "plus", newTab))
            if let h = model.defaultAgent(in: model.group(of: tab)) {
                out.append(.button(AppModel.defaultAgentTitle(h), systemImage: "sparkles") { AgentStarter.shared.startDefault(model, groupID: tab.groupID) })
            }
            out.append(.button("Start an Agent…", systemImage: "sparkles") { AgentStarter.shared.start(groupID: tab.groupID) })
        }
        if let newFilesTab {
            out.append(.button("New Files Tab", systemImage: "folder", newFilesTab))
        }
        // Tabs move between Groups on the same Mac.
        let host = model.group(of: tab)?.hostUserID
        let others = model.groups.filter { $0.id != tab.groupID && $0.hostUserID == host && $0.canAddTabs }
        if !others.isEmpty {
            out.append(.submenu("Move to Group", systemImage: "folder", others.map { g in
                // Its emoji, picture or the default, as in the sidebar.
                MenuEntry(title: model.title(of: g), icon: model.groupMenuUIImage(g).map { .picture($0) } ?? .symbol(AppModel.groupMenuSymbol),
                          kind: .action { model.moveTab(tab.id, to: g.id, index: g.tabIds.count) })
            }))
        }
        // The agent the row stands for, at rest or asleep.
        if let p = model.shownAgentPane(of: tab), p.asleep || p.canSleep, model.group(of: tab)?.canDrive != false {
            if p.asleep {
                out.append(.button("Wake Agent", systemImage: "sun.max") { model.sleepPane(p.id, wake: true) })
            } else {
                out.append(.button("Put Agent to Sleep", systemImage: "moon.zzz") { model.sleepPane(p.id) })
            }
        }
        out.append(.divider)
        if model.canRemoveWorktree(tab) {
            out.append(.button("Remove Worktree…", systemImage: "trash", destructive: true) { askRemove(tab) })
        }
        out.append(.button("Close Tab", systemImage: "xmark", destructive: true) { requestCloseTab(model, tab, ask: askClose) })
        return out
    }
}

/// Closes a tab, or hands it to `ask` when that would stop something in it
/// (an agent at work, a program running), for closeTabConfirmation to ask.
@MainActor
func requestCloseTab(_ model: AppModel, _ tab: WorkspaceTab, ask: (WorkspaceTab) -> Void) {
    if closeWarning(model.panes(in: tab)) != nil { ask(tab) } else { Task { await model.closeTab(tab.id) } }
}

extension View {
    /// Asks whether to remove a tab's worktree while one is set (Remove
    /// Worktree…), then does, closing the tabs in it.
    func removeWorktreeConfirmation(model: AppModel, tab: Binding<WorkspaceTab?>) -> some View {
        confirmationDialog(tab.wrappedValue.map { model.removeWorktreeQuestion($0).title } ?? "",
                           isPresented: Binding(get: { tab.wrappedValue != nil }, set: { if !$0 { tab.wrappedValue = nil } }),
                           titleVisibility: .visible, presenting: tab.wrappedValue) { t in
            if model.removeWorktreeQuestion(t).canRemove {
                Button("Remove Worktree", role: .destructive) { Task { await model.removeWorktree(t.id, force: t.git.dirty) } }
            } else {
                Button("OK", role: .cancel) {}
            }
        } message: { t in
            Text(model.removeWorktreeQuestion(t, warnBusy: UserDefaults.standard.object(forKey: "warnCloseBusyAgent") as? Bool ?? true).message)
        }
    }

    /// Asks whether to close the tab while one is set (requestCloseTab),
    /// saying what it stops, as closing a pane does.
    func closeTabConfirmation(model: AppModel, tab: Binding<WorkspaceTab?>) -> some View {
        confirmationDialog(tab.wrappedValue.map { "Close “\(model.title(of: $0))”?" } ?? "",
                           isPresented: Binding(get: { tab.wrappedValue != nil }, set: { if !$0 { tab.wrappedValue = nil } }),
                           titleVisibility: .visible, presenting: tab.wrappedValue) { t in
            Button("Close Tab", role: .destructive) { Task { await model.closeTab(t.id) } }
        } message: { t in
            Text(closeWarning(model.panes(in: t)) ?? "")
        }
    }
}

/// A Group's section header, with its actions. A Group shared with you can
/// only be left, as on the Mac.
struct GroupHeader: View {
    let model: AppModel
    let group: WorkspaceGroup
    /// Collapsed on this phone: its tabs hidden, the header saying what's in them.
    var collapsed = false
    var toggle: () -> Void = {}
    let newTab: () -> Void
    let edit: () -> Void
    let delete: () -> Void
    let leave: () -> Void

    /// What a collapsed Group's agents are doing, the most pressing first.
    private var agentState: AgentState? {
        let states = model.tabs(in: group).compactMap { model.agentState(of: $0) }
        return [AgentState.needsInput, .errored, .working, .idle, .starting].first(where: states.contains)
    }

    var body: some View {
        HStack(spacing: 6) {
            // The Group's name collapses and expands it.
            Button(action: toggle) {
                HStack(spacing: 6) {
                    GroupIconView(model: model, group: group, size: 26) { EmptyView() }
                    Text(model.title(of: group))
                    if let c = model.computer(of: group) {
                        // On another of your computers: its badge.
                        ComputerBadge(c, size: 13)
                        if group.offline { Text("asleep").fontWeight(.regular).foregroundStyle(.secondary) }
                    } else if group.isRemote {
                        // Shared with you: whose Mac it's on.
                        Text("· " + model.hostLabel(of: group) + (group.offline ? " · offline" : ""))
                            .fontWeight(.regular)
                            .lineLimit(1)
                    }
                    Image(systemName: "chevron.right")
                        .font(.caption.weight(.semibold))
                        .imageScale(.medium)
                        .foregroundStyle(.tertiary)
                        .rotationEffect(.degrees(collapsed ? 0 : 90))
                    if collapsed {
                        if let s = agentState { StateGlyph(state: s) }
                        if !group.tabIds.isEmpty {
                            Text("\(group.tabIds.count)").fontWeight(.regular).foregroundStyle(.secondary).monospacedDigit()
                        }
                    }
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(model.title(of: group))
            .accessibilityValue(collapsed ? "Collapsed" : "Expanded")
            .accessibilityHint(collapsed ? "Shows its tabs" : "Hides its tabs")
            // The toggle ends at its chevron (and a collapsed Group's
            // summary): the space before + is left alone, so aiming for +
            // doesn't collapse the Group.
            Spacer(minLength: 0)
            if group.isRemote, !group.isOnLinkedComputer, let host = model.people[group.hostUserID] { AvatarView(person: host, size: 18) }
            Menu {
                Button("Terminal", systemImage: "apple.terminal", action: newTab)
                if let h = model.defaultAgent(in: group) {
                    Button(AgentCatalog.name(of: h), systemImage: "sparkles") { AgentStarter.shared.startDefault(model, groupID: group.id) }
                }
                Button("Agent…", systemImage: "sparkles") { AgentStarter.shared.start(groupID: group.id) }
            } label: {
                // A finger's worth to aim at, not just the thin glyph: an
                // invisible 44pt area reaching left into the empty header
                // (away from the … beside it), laid out as the glyph alone.
                Image(systemName: "plus").background(alignment: .trailing) { HeaderHitArea() }
            }
            .accessibilityLabel("New Tab")
            .disabled(!group.canAddTabs)
            // New tabs and agents are the + beside it.
            Menu {
                if group.isRemote, !group.isOnLinkedComputer {
                    Button("Leave Group", systemImage: "rectangle.portrait.and.arrow.right", role: .destructive, action: leave)
                } else {
                    Button("Edit Group…", systemImage: "pencil", action: edit)
                    Divider()
                    Button("Delete Group", systemImage: "trash", role: .destructive, action: delete)
                }
            } label: {
                Image(systemName: "ellipsis.circle").background(alignment: .leading) { HeaderHitArea() }
            }
            .accessibilityLabel("Group Actions")
        }
        .font(.subheadline.weight(.semibold))
        .textCase(nil)
        .imageScale(.large)
    }
}

/// A header button's tap area: 44pt, bigger than its glyph, without
/// changing the header's layout (a background overflows; it isn't sized).
private struct HeaderHitArea: View {
    var body: some View { Color.clear.frame(width: 44, height: 44).contentShape(Rectangle()) }
}

/// A Group whose tabs have all closed: what to open in it next, in
/// the folder it remembers.
struct EmptyGroupRowIOS: View {
    let model: AppModel
    let group: WorkspaceGroup
    let newTab: () -> Void
    @Environment(\.phoneTheme) private var theme

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 4) {
                Text(group.canAddTabs ? "Nothing open yet" : group.offline ? "Its computer can't be reached right now." : "Only people who can drive can open tabs here.")
                    .font(.subheadline.weight(.medium))
                    .foregroundStyle(.secondary)
                let folder = model.groupFolder(group)
                if !folder.isEmpty {
                    HStack(spacing: 5) {
                        Image(systemName: "folder.fill").imageScale(.small)
                        Text(folder.replacingOccurrences(of: #"^/Users/[^/]+"#, with: "~", options: .regularExpression))
                            .lineLimit(1).truncationMode(.middle)
                    }
                    .font(.footnote)
                    .foregroundStyle(.tertiary)
                }
            }
            if group.canAddTabs {
                HStack(spacing: 8) {
                    Button(action: newTab) {
                        StartTile(title: "Terminal") { Image(systemName: "terminal.fill") }
                    }
                    .buttonStyle(StartTileStyle(fill: theme.bubble, prominent: true))
                    if let h = model.defaultAgent(in: group) {
                        Button { AgentStarter.shared.startDefault(model, groupID: group.id) } label: {
                            StartTile(title: AgentCatalog.shortName(of: h)) { AgentMark(harness: h, size: 20) }
                        }
                        .buttonStyle(StartTileStyle(fill: theme.accent, prominent: false))
                    }
                    Button { AgentStarter.shared.start(groupID: group.id) } label: {
                        StartTile(title: "Agent…") { Image(systemName: "sparkles") }
                    }
                    .buttonStyle(StartTileStyle(fill: theme.accent, prominent: false))
                }
            }
        }
        .padding(.vertical, 8)
    }
}

/// One of an empty Group's starters: its icon over a one-line name.
private struct StartTile<Icon: View>: View {
    let title: String
    @ViewBuilder let icon: Icon

    var body: some View {
        VStack(spacing: 6) {
            icon.font(.title3).frame(height: 24)
            Text(title)
                .font(.footnote.weight(.semibold))
                .lineLimit(1).minimumScaleFactor(0.8)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 12)
        .padding(.horizontal, 6)
    }
}

/// The first tile filled with the theme's deeper shade (white on it), the
/// rest a wash of its accent; each dims a little while pressed. Its own
/// colours, not the List's tint, which would turn the icons the fill's.
private struct StartTileStyle: ButtonStyle {
    let fill: Color
    let prominent: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .foregroundStyle(prominent ? Color.white : fill)
            .background(prominent ? fill : fill.opacity(0.16),
                        in: RoundedRectangle(cornerRadius: 16, style: .continuous))
            .opacity(configuration.isPressed ? 0.7 : 1)
            .scaleEffect(configuration.isPressed ? 0.97 : 1)
            .animation(.easeOut(duration: 0.15), value: configuration.isPressed)
            .contentShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
    }
}

/// A new Group, or an existing one's name and emoji.
struct GroupEditor: View {
    let model: AppModel
    /// nil: making a new Group.
    let group: WorkspaceGroup?
    /// A new Group's first tab, to open.
    var onCreated: (String) -> Void = { _ in }
    @State private var name = ""
    @State private var icon = ""
    @State private var saving = false
    /// A picture picked here (empty: remove the Group's).
    @State private var picture: Data?
    @State private var photo: PhotosPickerItem?
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("Name", text: $name, prompt: Text(automaticName))
                    TextField("Emoji", text: $icon, prompt: Text("Emoji (optional)"))
                        .onChange(of: icon) {
                            // One emoji: the newest one typed.
                            if icon.count > 1 { icon = String(icon.suffix(1)) }
                        }
                } footer: {
                    Text("Unnamed, a Group is named after the folder it's working in.")
                }
                if let g = group, !g.isRemote {
                    Section {
                        HStack(spacing: 12) {
                            if let shown = picture ?? model.groupPicture(g), !shown.isEmpty, let img = UIImage(data: shown) {
                                Image(uiImage: img).resizable().scaledToFit().frame(width: 40, height: 40)
                                    .clipShape(RoundedRectangle(cornerRadius: 9, style: .continuous))
                            }
                            PhotosPicker("Choose Picture", selection: $photo, matching: .images)
                            Spacer()
                            if (picture ?? model.groupPicture(g))?.isEmpty == false {
                                Button("Remove", role: .destructive) { picture = Data() }
                            }
                        }
                    } header: {
                        Text("Picture")
                    } footer: {
                        Text("Kept in Hammock on your Mac. Choosing one replaces the emoji. A project's own .hammock/icon.png shows when no picture is set here.")
                    }
                    .onChange(of: photo) {
                        Task {
                            if let d = try? await photo?.loadTransferable(type: Data.self) {
                                // A phone's photo may be HEIC: the Mac reads PNG and JPEG.
                                picture = UIImage(data: d)?.jpegData(compressionQuality: 0.9) ?? d
                                icon = "" // the picture replaces the emoji, which would show instead
                            }
                        }
                    }
                }
            }
            .themedScreenBackground()
            .navigationTitle(group == nil ? "New Group" : "Edit Group")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(group == nil ? "Create" : "Save", action: save).disabled(saving)
                }
            }
        }
        .onAppear {
            name = group?.name ?? ""
            icon = group?.icon ?? ""
        }
        .presentationDetents([.medium, .large])
    }

    private var automaticName: String {
        guard var g = group else { return "Name (optional)" }
        g.name = ""
        return model.title(of: g)
    }

    private func save() {
        let n = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let i = icon.trimmingCharacters(in: .whitespacesAndNewlines)
        saving = true
        Task {
            if let g = group {
                if n != g.name { await model.renameGroup(g.id, n) }
                if i != g.icon { await model.setGroupIcon(g.id, i) }
                if let picture { await model.setGroupPicture(g.id, picture) }
            } else if let id = await model.createGroup(name: n, on: model.computerFilter) {
                if !i.isEmpty { await model.setGroupIcon(id, i) }
                if let tab = await model.createTab(in: id) { onCreated(tab) }
            }
            dismiss()
        }
    }
}

extension AppModel {
    /// A Group's name with its emoji.
    func label(of group: WorkspaceGroup) -> String {
        group.icon.isEmpty ? title(of: group) : "\(group.icon) \(title(of: group))"
    }
}

/// Start an Agent, from any + menu or tab menu: one sheet over the
/// app; the tab it opens is shown in the tab (Sessions or Groups) it was
/// asked from.
@MainActor
@Observable
final class AgentStarter {
    static let shared = AgentStarter()

    struct Request: Identifiable {
        let id = UUID()
        var groupID: String?
        var origin = ""
    }

    var request: Request?
    /// The tab the last start opened, and where it was asked from.
    private(set) var opened: (tabID: String, origin: String, nonce: UUID)?

    func start(groupID: String?) { request = Request(groupID: groupID) }

    /// The tab (Sessions or Groups) on screen, for the tab a start without
    /// the sheet opens (CockpitView keeps it).
    var shownTab = ""

    /// The Group's computer's default agent (its Settings ▸ Agents), started
    /// without the sheet, in the folder the sheet would suggest.
    func startDefault(_ model: AppModel, groupID: String) {
        let origin = shownTab
        Task { if let id = await model.startDefaultAgent(groupID: groupID) { didOpen(id, origin: origin) } }
    }

    func didOpen(_ tabID: String, origin: String) { opened = (tabID, origin, UUID()) }
}

extension View {
    /// Shows the tab a start opened, in this root's navigation.
    func showsStartedAgents(origin: String, path: Binding<[String]>) -> some View {
        onChange(of: AgentStarter.shared.opened?.nonce) {
            if let o = AgentStarter.shared.opened, o.origin == origin { path.wrappedValue.append(o.tabID) }
        }
    }
}

/// The Groups list's filter by computer, once computers are linked: All,
/// then each (its badge, dimmed while it's asleep).
struct ComputerChips: View {
    @Bindable var model: AppModel

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                chip(nil)
                ForEach(model.computers, id: \.userID) { chip($0) }
            }
            .padding(.horizontal, 16)
        }
    }

    private func chip(_ c: Computer?) -> some View {
        let id = c.map { $0.thisComputer ? model.myUserID : $0.userID } ?? ""
        let on = model.computerFilter == id
        return Button {
            withAnimation(.snappy(duration: 0.25)) { model.computerFilter = id }
        } label: {
            HStack(spacing: 6) {
                if let c {
                    ComputerIcon(c, size: 15, weight: .regular, color: on ? Color(.systemBackground) : nil)
                    Text(c.name)
                } else {
                    Text("All")
                }
            }
            .font(.subheadline.weight(on ? .semibold : .regular))
            // Reversed out of the label colour (white text was white on white in dark mode).
            .foregroundStyle(on ? Color(.systemBackground) : Color.primary)
            .padding(.horizontal, 14)
            .frame(minHeight: 34)
            .background(on ? Color(.label) : Color(.secondarySystemGroupedBackground), in: Capsule())
            .opacity(c?.online == false ? 0.55 : 1)
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}
