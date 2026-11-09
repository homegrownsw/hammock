import AppKit
import HammockKit
import SwiftUI

/// A Group's folder: where its new tabs open. Changed here only for
/// Groups on this Mac, since the folder is picked in this Mac's Finder.
@MainActor
enum GroupFolder {
    static func canChange(_ g: WorkspaceGroup) -> Bool { !g.isRemote }

    static func tilde(_ path: String) -> String { (path as NSString).abbreviatingWithTildeInPath }

    /// Asks for a folder, starting in `startingAt`.
    static func pick(startingAt: String, message: String = "Choose where this Group's new tabs open") -> String? {
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "Choose"
        panel.message = message
        if !startingAt.isEmpty { panel.directoryURL = URL(fileURLWithPath: startingAt) }
        guard panel.runModal() == .OK, let url = panel.url else { return nil }
        return url.path
    }

    /// Picks a folder for the Group and sets it.
    static func choose(_ model: AppModel, _ g: WorkspaceGroup) {
        guard let f = pick(startingAt: model.groupFolder(g)) else { return }
        Task { await model.setGroupFolder(g.id, f) }
    }

    /// New Group in Directory…: picks a folder, then makes an empty Group
    /// there, named after it.
    static func newGroup(_ model: AppModel) {
        guard let f = pick(startingAt: NSHomeDirectory(), message: "Choose the folder for a new Group") else { return }
        Task { await model.newGroup(inFolder: f) }
    }
}

/// A Group with no tabs, selected: what to open in it next, as the
/// launch pad at the end of onboarding does, in the Group's folder.
struct EmptyGroupView: View {
    let model: AppModel
    let group: WorkspaceGroup
    @State private var shown = false

    private var folder: String { model.groupFolder(group) }

    var body: some View {
        VStack(spacing: 0) {
            Spacer(minLength: 24)
            GroupIconView(model: model, group: group, size: 64) {
                Image(systemName: "square.stack.3d.up")
                    .font(.system(size: 40, weight: .light))
                    .foregroundStyle(Color.themeAccent)
            }
            .font(.system(size: 52))
            .frame(height: 72)
            .scaleEffect(shown ? 1 : 0.9)
            Text(model.title(of: group))
                .font(.system(size: 30, weight: .bold))
                .kerning(-0.4)
                .lineLimit(1)
                .padding(.top, 12)
            Text(group.canAddTabs ? "Nothing open here yet. What would you like to start?" : subtitleWhenClosed)
                .font(.system(size: 15))
                .foregroundStyle(.secondary)
                .padding(.top, 6)
            folderLine.padding(.top, 14)

            if group.canAddTabs {
                HStack(alignment: .top, spacing: 16) {
                    terminalCard
                    agentCard
                }
                // A set height: sized to fit (fixedSize) with cards that
                // stretch, the page asked the split view for more height than
                // the window has, and pushed the sidebar's rows out of sight.
                // Tall enough for a description on two lines (the agent
                // card's usually is), with the cards' full padding below.
                .frame(maxWidth: 640)
                .frame(height: 196)
                .padding(.top, 28)
                .padding(.horizontal, 40)
            }

            // Yours to close (here or on your other computer while it's on).
            if group.isOnLinkedComputer ? !group.offline : !group.isRemote, let close = Keys.label("close") {
                Text("\(close) closes this Group.")
                    .font(.system(size: 12))
                    .foregroundStyle(.tertiary)
                    .padding(.top, 22)
            }
            Spacer(minLength: 24)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .onAppear { withAnimation(.spring(duration: 0.5, bounce: 0.35)) { shown = true } }
    }

    private var subtitleWhenClosed: String {
        group.offline ? "This Group's computer can't be reached right now." : "Only people who can drive can open tabs here."
    }

    @ViewBuilder private var folderLine: some View {
        HStack(spacing: 6) {
            Image(systemName: "folder").foregroundStyle(.secondary)
            Text("Opens in \(folder.isEmpty ? "your home folder" : GroupFolder.tilde(folder))")
                .lineLimit(1).truncationMode(.middle)
                .foregroundStyle(.secondary)
            if GroupFolder.canChange(group) {
                Button("Change…") { GroupFolder.choose(model, group) }
                    .buttonStyle(.link)
            }
        }
        .font(.system(size: 13))
        .padding(.horizontal, 12).padding(.vertical, 6)
        .background(.background.opacity(0.6), in: Capsule())
        .overlay(Capsule().strokeBorder(Color.primary.opacity(0.06)))
    }

    private var terminalCard: some View {
        LaunchCard(prominent: true, title: "New Terminal", text: "A shell in this Group's folder, ready to go.", keys: Keys.newTab?.label) {
            Task { await model.createTab(in: group.id) }
        } icon: {
            Text(">_").font(.system(size: 15, weight: .bold, design: .monospaced))
        }
    }

    private var agentCard: some View {
        LaunchCard(prominent: false, title: "Start an Agent…", text: "Claude Code, Codex or another agent in this folder, with a first message if you like.", keys: Keys.newAgentTab?.label) {
            AgentLauncher.shared.open(model, groupID: group.id)
        } icon: {
            Image(systemName: "sparkles").font(.system(size: 20, weight: .medium))
        }
    }
}

/// One of the big buttons on an empty page (an empty Group's, no Groups at
/// all): an icon, a title, a line or two and its shortcut. In the theme's
/// colours: the prominent one its deep colour (your messages' in the agent
/// view, made for white text), the other its toolbar's, with the accent.
struct LaunchCard<Icon: View>: View {
    @Environment(\.colorScheme) private var scheme
    let prominent: Bool
    let title: String
    let text: String
    let keys: String?
    let action: () -> Void
    @ViewBuilder let icon: () -> Icon

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 10) {
                icon()
                    .foregroundStyle(prominent ? Color.white : accent)
                    .frame(width: 40, height: 40)
                    .background(prominent ? Color.white.opacity(0.18) : accent.opacity(0.14), in: RoundedRectangle(cornerRadius: 11, style: .continuous))
                Text(title)
                    .font(.system(size: 17, weight: prominent ? .bold : .semibold))
                Text(text)
                    .font(.system(size: 13))
                    .foregroundStyle(prominent ? AnyShapeStyle(Color.white.opacity(0.85)) : AnyShapeStyle(.secondary))
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 0)
                if let keys { shortcut(keys) }
            }
            .foregroundStyle(prominent ? AnyShapeStyle(.white) : AnyShapeStyle(.primary))
            .padding(20)
            .frame(maxWidth: .infinity, minHeight: 168, maxHeight: .infinity, alignment: .topLeading)
            .background { background }
            .contentShape(Rectangle())
        }
        .buttonStyle(LaunchButtonStyle())
        .accessibilityLabel(title)
    }

    private var accent: Color { .themeAccent }
    private var deep: Color { AppConfig.shared.agentColors.bubble }
    /// The theme's toolbar colour (its lightest surface in light mode), or the window's background.
    private var surface: Color { AppConfig.shared.toolbarTint(dark: scheme == .dark).map { Color(nsColor: $0) } ?? Color(nsColor: .windowBackgroundColor) }

    @ViewBuilder private var background: some View {
        let shape = RoundedRectangle(cornerRadius: 16, style: .continuous)
        if prominent {
            shape.fill(LinearGradient(colors: [deep.mix(with: .white, by: 0.12), deep.mix(with: .black, by: 0.4)], startPoint: .topLeading, endPoint: .bottomTrailing))
                .shadow(color: deep.mix(with: .black, by: 0.5).opacity(0.32), radius: 18, y: 14)
        } else {
            shape.fill(surface.opacity(0.9))
                .overlay(shape.strokeBorder(accent.opacity(0.15)))
                .shadow(color: .black.opacity(0.06), radius: 12, y: 8)
        }
    }

    private func shortcut(_ keys: String) -> some View {
        Text(keys)
            .font(.system(size: 11, weight: .semibold))
            .foregroundStyle(prominent ? AnyShapeStyle(deep.mix(with: .black, by: 0.3)) : AnyShapeStyle(.secondary))
            .padding(.horizontal, 7).padding(.vertical, 3)
            .background(prominent ? AnyShapeStyle(Color.white.opacity(0.9)) : AnyShapeStyle(surface), in: RoundedRectangle(cornerRadius: 5))
            .overlay(RoundedRectangle(cornerRadius: 5).strokeBorder(Color.primary.opacity(prominent ? 0 : 0.15)))
    }
}

/// No Groups at all: the empty Group's page, for making the first one.
struct NoGroupsView: View {
    let model: AppModel
    @State private var shown = false

    var body: some View {
        VStack(spacing: 0) {
            Spacer(minLength: 24)
            Image(systemName: "square.stack.3d.up")
                .font(.system(size: 40, weight: .light))
                .foregroundStyle(Color.themeAccent)
                .frame(height: 72)
                .scaleEffect(shown ? 1 : 0.9)
            Text("No Groups Yet")
                .font(.system(size: 30, weight: .bold))
                .kerning(-0.4)
                .padding(.top, 12)
            Text("A Group holds the tabs and agents for one piece of work.")
                .font(.system(size: 15))
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .padding(.top, 6)
            HStack(alignment: .top, spacing: 16) {
                LaunchCard(prominent: true, title: "New Group", text: "A Group with a terminal in your home folder, ready to go.", keys: Keys.newGroup?.label) {
                    Task { await model.newGroup() }
                } icon: {
                    Image(systemName: "plus").font(.system(size: 19, weight: .semibold))
                }
                LaunchCard(prominent: false, title: "New Group in Directory…", text: "Pick a project's folder: its tabs and agents open there.", keys: Keys.newGroupInDirectory?.label) {
                    GroupFolder.newGroup(model)
                } icon: {
                    Image(systemName: "folder.badge.plus").font(.system(size: 18, weight: .medium))
                }
            }
            // A set height, as on an empty Group's page (see there).
            .frame(maxWidth: 640)
            .frame(height: 196)
            .padding(.top, 28)
            .padding(.horizontal, 40)
            Spacer(minLength: 24)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .onAppear { withAnimation(.spring(duration: 0.5, bounce: 0.35)) { shown = true } }
    }
}
