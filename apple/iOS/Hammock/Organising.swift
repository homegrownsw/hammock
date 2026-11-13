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

    var body: some View {
        Button("Rename…", systemImage: "pencil", action: rename)
        if let newTab {
            Button("New Tab in Group", systemImage: "plus", action: newTab)
            Button("Start an Agent…", systemImage: "sparkles") { AgentStarter.shared.start(groupID: tab.groupID) }
        }
        // Tabs move between Groups on the same Mac.
        let host = model.group(of: tab)?.hostUserID
        let others = model.groups.filter { $0.id != tab.groupID && $0.hostUserID == host && $0.canAddTabs }
        if !others.isEmpty {
            Menu("Move to Group", systemImage: "folder") {
                ForEach(others) { g in
                    Button { model.moveTab(tab.id, to: g.id, index: g.tabIds.count) } label: {
                        // Its emoji, picture or the default, as in the sidebar.
                        Label { Text(model.title(of: g)) } icon: { model.groupMenuIcon(g) }
                    }
                }
            }
        }
        // The agent the row stands for, at rest or asleep.
        if let p = model.shownAgentPane(of: tab), p.asleep || p.canSleep, model.group(of: tab)?.canDrive != false {
            if p.asleep {
                Button("Wake Agent", systemImage: "sun.max") { model.sleepPane(p.id, wake: true) }
            } else {
                Button("Put Agent to Sleep", systemImage: "moon.zzz") { model.sleepPane(p.id) }
            }
        }
        Divider()
        Button("Close Tab", systemImage: "xmark", role: .destructive) { Task { await model.closeTab(tab.id) } }
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
                    Button("Leave Group", systemImage: "rectangle.portrait.and.arrow.right", role: .destructive) {
                        Task { await model.leave(group.id) }
                    }
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

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(group.canAddTabs ? "Nothing open here yet." : group.offline ? "Its computer can't be reached right now." : "Only people who can drive can open tabs here.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
            let folder = model.groupFolder(group)
            if !folder.isEmpty {
                Label(folder.replacingOccurrences(of: #"^/Users/[^/]+"#, with: "~", options: .regularExpression), systemImage: "folder")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .lineLimit(1).truncationMode(.middle)
            }
            if group.canAddTabs {
                HStack(spacing: 10) {
                    Button(action: newTab) {
                        // The List tints a row's icons its accent, which
                        // vanishes on the filled button: the label's own colour.
                        Label { Text("Terminal") } icon: { Image(systemName: "terminal").foregroundStyle(.white) }
                            .frame(maxWidth: .infinity)
                    }
                    .prominentButton()
                    Button { AgentStarter.shared.start(groupID: group.id) } label: {
                        Label("Agent…", systemImage: "sparkles").frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.bordered)
                }
                .controlSize(.large)
            }
        }
        .padding(.vertical, 6)
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
                        Text("Kept in Hammock on your Mac. An emoji shows instead, if set; a project's own .hammock/icon.png shows when no picture is set here.")
                    }
                    .onChange(of: photo) {
                        Task {
                            if let d = try? await photo?.loadTransferable(type: Data.self) {
                                // A phone's photo may be HEIC: the Mac reads PNG and JPEG.
                                picture = UIImage(data: d)?.jpegData(compressionQuality: 0.9) ?? d
                            }
                        }
                    }
                }
            }
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
